from pydantic import BaseModel, Field, model_validator

# 质检规则版本：策略引擎判定映射的版本号。
# 当阈值/原因码集合变化时递增，便于按版本回滚自动结论（POST /people/face-quality/restore-auto）。
FACE_QUALITY_RULE_VERSION = "v1"

# 质检模型版本：人脸验证/质量估计所用模型与启发式的标识。
FACE_QUALITY_MODEL_VERSION = "insightface-buffalo-sc-v1"

# 质量原因码（quality_reasons）枚举。策略引擎据此决定自动排除/灰区/接受。
QUALITY_REASON_TOO_SMALL = "too_small"
QUALITY_REASON_BLURRED = "blurred"
QUALITY_REASON_OVEREXPOSED = "overexposed"
QUALITY_REASON_UNDEREXPOSED = "underexposed"
QUALITY_REASON_INVALID_LANDMARKS = "invalid_landmarks"
QUALITY_REASON_BAD_GEOMETRY = "bad_geometry"
QUALITY_REASON_EXTREME_POSE = "extreme_pose"
QUALITY_REASON_OCCLUDED = "occluded"
QUALITY_REASON_LOW_VALIDITY = "low_validity"
QUALITY_REASON_POSE_UNCERTAIN = "pose_uncertain"


class BoundingBox(BaseModel):
    x: float = Field(ge=0, le=1)
    y: float = Field(ge=0, le=1)
    width: float = Field(gt=0, le=1)
    height: float = Field(gt=0, le=1)


class FaceQualityEvidence(BaseModel):
    """单张人脸的结构化质检证据，供后端策略引擎判定自动排除/灰区/接受。"""

    # 二次人脸验证置信度：独立于第一阶段检测置信度，只对候选框运行。
    face_validity_score: float = Field(ge=0, le=1)
    # 人脸实际像素宽高（基于原图坐标系，非归一化）。
    pixel_width: int = Field(ge=0)
    pixel_height: int = Field(ge=0)
    # 图像质量指标。
    sharpness: float = Field(ge=0)  # Laplacian 方差
    brightness: float = Field(ge=0, le=255)  # 像素均值
    contrast: float = Field(ge=0)  # 像素标准差
    # 关键点完整性与几何合理性（0-1，1 为最佳）。
    landmark_completeness: float = Field(ge=0, le=1)
    landmark_geometry_score: float = Field(ge=0, le=1)
    # 姿态（度）与遮挡/不可估计标记。
    yaw: float | None = None
    pitch: float | None = None
    roll: float | None = None
    pose_estimable: bool = True
    occluded: bool = False
    # 触发的质量原因码列表（QUALITY_REASON_*）。
    quality_reasons: list[str] = Field(default_factory=list)
    # 版本信息。
    rule_version: str = FACE_QUALITY_RULE_VERSION
    model_version: str = FACE_QUALITY_MODEL_VERSION


class DetectedFace(BaseModel):
    bbox: BoundingBox
    confidence: float = Field(ge=0, le=1)
    quality_score: float = Field(ge=0, le=1)
    embedding: list[float]
    evidence: FaceQualityEvidence | None = None


class DetectFacesRequest(BaseModel):
    image_path: str | None = None
    image_base64: str | None = None
    min_confidence: float = Field(default=0.5, ge=0, le=1)
    max_faces: int = Field(default=20, ge=1, le=100)

    @model_validator(mode="after")
    def validate_source(self) -> "DetectFacesRequest":
        if not self.image_path and not self.image_base64:
            raise ValueError("image_path or image_base64 is required")
        return self


class DetectFacesResponse(BaseModel):
    faces: list[DetectedFace]
    processing_time_ms: int = Field(ge=0)
    rule_version: str = FACE_QUALITY_RULE_VERSION
    model_version: str = FACE_QUALITY_MODEL_VERSION


class HealthResponse(BaseModel):
    """ML 服务健康状态。

    对 v2 历史复核链路，健康只在 YuNet 验证器可用时为 ok（HTTP 200）；
    验证器不可用（模型缺失/SHA 不匹配/加载失败）时为 degraded（HTTP 503），
    令后端拒绝创建/恢复/重试 independent_v2 run，Docker healthcheck 标记 unhealthy。
    legacy v1 同源评分不依赖 verifier，其行为不受本字段影响。
    """

    status: str
    verifier_available: bool
    verifier_name: str
    verifier_version: str


# ---- 已知框重评分（score-known-faces）----
# 该接口只对历史重评分 worker 内部使用，不暴露给浏览器。
# 请求给出已旋转校正展示图 + 一组目标归一化 BBox，ML 端在同一张图上运行
# InsightFace 检测，用既有 _build_evidence 生成证据，再与目标框做一对一最高 IoU 匹配。
# 阈值与后端 exclusionIoUThreshold=0.3 一致。无匹配/读图失败/推理异常返回具体非判定状态，
# 不得伪造空证据或自动判为非人脸。


class ScoreKnownFaceTarget(BaseModel):
    """单个重评分目标：一个已知人脸框。"""

    face_id: int
    bbox: BoundingBox


class ScoreKnownFacesRequest(BaseModel):
    image_base64: str
    targets: list[ScoreKnownFaceTarget] = Field(default_factory=list)

    @model_validator(mode="after")
    def validate_targets(self) -> "ScoreKnownFacesRequest":
        if not self.image_base64:
            raise ValueError("image_base64 is required")
        return self


class ScoreKnownFaceResult(BaseModel):
    """单个目标的重评分结果，按请求 target 顺序返回。"""

    face_id: int
    # matched: 找到 IoU>=阈值的一对一匹配并产出证据。
    # unmatched: 未找到匹配框（不得当作 non_face）。
    # error: 图像读取/推理/证据构造异常（可重试）。
    status: str
    matched_iou: float | None = None
    evidence: FaceQualityEvidence | None = None
    quality_score: float | None = None


class ScoreKnownFacesResponse(BaseModel):
    results: list[ScoreKnownFaceResult]
    rule_version: str = FACE_QUALITY_RULE_VERSION
    model_version: str = FACE_QUALITY_MODEL_VERSION


# ---- v2 独立复核（verify-known-face-crops）----
# 该接口只对历史复核 worker 内部使用，不暴露给浏览器。
# 请求按 Face 传输「以人脸框为中心、四周各扩展 100%」的上下文裁剪 Base64、face_id、
# 原图人脸框宽高与主检测分。ML 端用独立验证器（YuNet）判定 face/no_face/uncertain/error，
# 并在原图人脸框上计算质量特征。任何单条错误只影响对应 item。
# 不得把 error/uncertain 当作 non_face——这些只能进入待重试/待人工审核状态。

# v2 证据 schema 版本。
EVIDENCE_SCHEMA_VERSION_V2 = "independent_v2"

# v2 目标框匹配规则后的证据 schema 版本。
# 证据管线仍是 independent_v2（evidence_pipeline）；此版本仅标识「face/no_face 改为
# 是否匹配目标脸框」后的证据形态，新增 max_context_score / target_match_iou 诊断字段。
# 旧证据保留 independent_v2，由前端按字段缺失判定为「旧证据，未记录目标匹配诊断」。
EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH = "independent_v2_target_match_v2"

# v2 规则/模型版本（独立于 v1）。
FACE_QUALITY_V2_RULE_VERSION = "face_quality_v2"
YUNET_VERIFIER_NAME = "yunet"
YUNET_VERIFIER_VERSION = "opencv-yunet-2023mar"


class V2QualityFeatures(BaseModel):
    """原图人脸框质量特征（统一到固定短边归一化后计算，标明计算域与版本）。"""

    sharpness_norm: float = Field(ge=0)
    brightness_norm: float = Field(ge=0, le=255)
    contrast_norm: float = Field(ge=0)
    occluded: bool = False
    quality_domain: str
    quality_version: str


class CandidateBox(BaseModel):
    """诊断用候选框（上下文裁剪副本坐标系，像素）。仅供审计/排障，非确认分依据。"""

    x: int = Field(ge=0)
    y: int = Field(ge=0)
    width: int = Field(ge=0)
    height: int = Field(ge=0)


class VerifyKnownFaceCropTarget(BaseModel):
    """单个 v2 复核目标。"""

    face_id: int
    context_crop_base64: str
    face_box_width_px: int = Field(ge=0)
    face_box_height_px: int = Field(ge=0)
    # 人脸框左上角在上下文裁剪中的像素偏移；ML 端用此精确定位人脸框区域计算原图质量指标。
    face_box_offset_x: int = Field(default=0, ge=0)
    face_box_offset_y: int = Field(default=0, ge=0)
    primary_detector_score: float = Field(ge=0, le=1)


class VerifyKnownFaceCropsRequest(BaseModel):
    targets: list[VerifyKnownFaceCropTarget] = Field(default_factory=list)


class VerifyKnownFaceCropResult(BaseModel):
    """单个目标的 v2 复核结果，按请求 target 顺序返回。

    face/no_face 语义为「是否匹配到目标脸框」：仅当某检测框与请求 face_box_offset/width/height
    重叠足够（IoU>=阈值）时判 face，避免群像中位于裁剪中心或分数更高的非目标脸替代目标脸。
    """

    face_id: int
    # face: 独立验证器检测到与目标脸框重叠足够的脸。
    # no_face: 成功推理但未匹配到目标脸（裁剪内可能仍有其他脸）。
    # uncertain: 输入短边不足模型最小可判尺寸或结果边界。
    # error: 加载/推理/解码异常（可重试）。
    verification_status: str
    # 目标匹配分：匹配到目标脸时为该检测框置信度；未匹配时为 0（不再写入裁剪内其他脸分数）。
    verifier_score: float = 0.0
    # 裁剪内所有检测的最高置信度（含非目标脸）。仅供诊断/文案，不得当作「确认脸」置信度。
    max_context_score: float = 0.0
    # 匹配到目标脸时的 IoU；未匹配为 None。
    target_match_iou: float | None = None
    # 诊断：与目标框几何最接近（最大 IoU）的候选，即便低于阈值也记录。无任何候选时为 None。
    # 仅供审计/排障，不作为自动隔离、质量分或 UI 的确认分。
    best_target_iou: float | None = None
    # 诊断：best_target_iou 对应候选的置信度。无候选时为 0。
    best_target_candidate_score: float = 0.0
    # 诊断：best_target_iou 对应候选框（上下文裁剪副本坐标系）。无候选时为 None。
    best_target_candidate_box: CandidateBox | None = None
    verifier_name: str = YUNET_VERIFIER_NAME
    verifier_version: str = YUNET_VERIFIER_VERSION
    original_width: int = 0
    original_height: int = 0
    face_box_width_px: int = 0
    face_box_height_px: int = 0
    context_crop_width_px: int = 0
    context_crop_height_px: int = 0
    context_expand_ratio: float = 1.0
    primary_detector_score: float = 0.0
    quality: V2QualityFeatures | None = None
    reason_codes: list[str] = Field(default_factory=list)
    evidence_schema_version: str = EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH


class VerifyKnownFaceCropsResponse(BaseModel):
    results: list[VerifyKnownFaceCropResult]
    rule_version: str = FACE_QUALITY_V2_RULE_VERSION
    model_version: str = YUNET_VERIFIER_VERSION
