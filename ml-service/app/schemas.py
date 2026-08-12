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
    status: str


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
