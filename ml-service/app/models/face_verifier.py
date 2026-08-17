"""v2 独立人脸验证器：YuNet ONNX。

与 v1 的 InsightFace buffalo_sc 主检测链路不同，YuNet 是独立验证器。
模型文件作为镜像内受版本和 SHA-256 保护的资产交付，不允许容器运行时下载：
模型缺失或 SHA256SUMS 校验失败时，验证器进入 unavailable 状态，verify_crops 对所有目标
返回 error，使后端历史 worker 写 technical_error——绝不静默退回 v1 同源评分。

verify_crops 的判定语义（目标框匹配，而非裁剪图几何中心）：
- face: YuNet 某检测框与请求 face_box_offset/width/height 重叠足够（IoU>=阈值）；
- no_face: 成功推理但无检测框与目标脸框重叠足够（裁剪内可能仍有其他人脸）；
- uncertain: 输入短边不足模型最小可判尺寸或结果边界不可靠；
- error: 加载/推理/解码异常。

边缘裁剪被原图边界截断后，目标脸不再位于裁剪图几何中心，旧「中心 40%」规则会假阴性。
目标框匹配只接受与目标框重叠足够的检测框，群像中附近其他人脸不会替代目标脸。
"""

from __future__ import annotations

import base64
import hashlib
import logging
from dataclasses import dataclass
from pathlib import Path

import cv2
import numpy as np

from app.schemas import (
    CandidateBox,
    EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH_SCALE_NORMALIZED,
    V2QualityFeatures,
    VerifyKnownFaceCropResult,
    VerifyKnownFaceCropTarget,
    VerifyKnownFaceCropsResponse,
    YUNET_VERIFIER_NAME,
    YUNET_VERIFIER_VERSION,
)

logger = logging.getLogger(__name__)

# YuNet 资产根目录（Docker 构建时复制）。
YUNET_ASSET_DIR = Path(__file__).resolve().parent.parent.parent / "assets" / "yunet"
YUNET_MODEL_PATH = YUNET_ASSET_DIR / "face_detection_yunet_2023mar.onnx"
YUNET_SHA256SUMS_PATH = YUNET_ASSET_DIR / "SHA256SUMS"

# YuNet 检测阈值与几何约束（与 rule_version face_quality_v2 绑定；调整须重跑 shadow 校准）。
_YUNET_CONFIDENCE_THRESHOLD = 0.5  # 裁剪副本内 YuNet 检测置信度阈值
# 目标框匹配 IoU 阈值：检测框须与请求目标脸框 IoU>=此值才判 face。
# 与已知框重评分 exclusionIoUThreshold=0.3 保持一致，群像中非目标脸不会因邻近而误匹配。
_YUNET_TARGET_IOU_THRESHOLD = 0.3
_YUNET_MIN_INPUT_SHORT_EDGE = 24  # 裁剪副本短边低于此值判 uncertain（模型最小可判尺寸）

# 质量特征归一化短边（计算域）。
_QUALITY_NORM_SHORT_EDGE = 96
_QUALITY_DOMAIN = f"original_face_box_norm_to_{_QUALITY_NORM_SHORT_EDGE}"
_QUALITY_VERSION = "v1"

# YuNet 默认输入尺寸（FaceDetectorYN 的 size 参数）。
_YUNET_INPUT_SIZE = (320, 320)

# 尺度归一化：目标脸最长边超过此值时，把上下文裁剪等比缩小后再送 YuNet。
# YuNet 主要工作尺度约 10×10 至 300×300 px；超过此值的大脸直接以原像素推理易漏检。
# 小脸（<=此值）不放大——放大不会凭空创造细节，反而可能伪造可靠证据。
_YUNET_MAX_TARGET_LONG_EDGE = 256


@dataclass(frozen=True)
class _DetectionInputPlan:
    """单一检测输入计划：缩放比例与送入 YuNet 的实际输入尺寸。

    scale=1 表示不缩放（目标脸最长边<=256）；<1 表示等比缩小。input_width/height 为
    缩放后的上下文副本尺寸，仅用于送入 YuNet 推理；候选框/目标框/IoU/质量特征始终基于
    未缩放上下文坐标。
    """

    scale: float
    input_width: int
    input_height: int


def _plan_detection_input(
    frame_width: int,
    frame_height: int,
    target_width: int,
    target_height: int,
) -> _DetectionInputPlan:
    """按目标脸最长边决定是否等比缩小上下文副本，返回缩放比例与输入尺寸。

    缩放依据必须是目标脸最长边，不能用整张上下文最长边：边缘裁剪（被原图边界截断）与
    常规裁剪的上下文尺寸差异大，但同一目标脸应得到一致的检测尺度。用上下文最长边会
    导致不同裁剪策略下同一目标脸尺度不一致。
    """
    target_long_edge = max(target_width, target_height)
    scale = min(1.0, _YUNET_MAX_TARGET_LONG_EDGE / max(1, target_long_edge))
    return _DetectionInputPlan(
        scale=scale,
        input_width=max(1, round(frame_width * scale)),
        input_height=max(1, round(frame_height * scale)),
    )


class FaceVerifier:
    """YuNet 独立验证器。单例，构造时加载并校验模型。"""

    def __init__(self, model_path: Path | None = None, sha_path: Path | None = None) -> None:
        self.model_path = model_path or YUNET_MODEL_PATH
        self.sha_path = sha_path or YUNET_SHA256SUMS_PATH
        self.verifier_name = YUNET_VERIFIER_NAME
        self.verifier_version = YUNET_VERIFIER_VERSION
        self.available = False
        self._detector_factory = None  # 测试可注入；生产用 cv2.FaceDetectorYN_create
        self._load_model()

    # ---- 模型加载与 SHA 校验 ----

    def _load_model(self) -> None:
        if not self.model_path.exists():
            logger.warning("YuNet model missing at %s; verifier unavailable", self.model_path)
            self.available = False
            return
        if not self._verify_sha256():
            logger.error("YuNet model SHA256 mismatch; verifier unavailable (will not fall back to v1)")
            self.available = False
            return
        try:
            # 验证 cv2 能创建检测器（模型文件可被 ONNX 解析）。
            _ = self._create_detector()
            self.available = True
            logger.info("YuNet verifier loaded: %s", self.model_path)
        except Exception as exc:  # pragma: no cover - 真实模型加载异常
            logger.error("YuNet model load failed: %s; verifier unavailable", exc)
            self.available = False

    def _verify_sha256(self) -> bool:
        if not self.sha_path.exists():
            logger.error("YuNet SHA256SUMS missing at %s", self.sha_path)
            return False
        try:
            actual = hashlib.sha256(self.model_path.read_bytes()).hexdigest()
        except Exception as exc:  # pragma: no cover
            logger.error("YuNet SHA256 compute failed: %s", exc)
            return False
        expected = self._read_expected_sha256()
        if expected is None:
            return False
        if actual.lower() != expected.lower():
            logger.error("YuNet SHA256 mismatch: expected %s got %s", expected, actual)
            return False
        return True

    def _read_expected_sha256(self) -> str | None:
        model_name = self.model_path.name
        try:
            for line in self.sha_path.read_text().splitlines():
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                parts = line.split()
                if len(parts) >= 2:
                    digest, name = parts[0], parts[1]
                    if name == model_name:
                        return digest
        except Exception as exc:  # pragma: no cover
            logger.error("YuNet SHA256SUMS read failed: %s", exc)
            return None
        logger.error("YuNet model %s not found in SHA256SUMS", model_name)
        return None

    def _create_detector(self):
        """创建一个 YuNet 检测器实例（非线程安全，每目标一个）。"""
        if self._detector_factory is not None:
            return self._detector_factory()
        detector = cv2.FaceDetectorYN_create(
            str(self.model_path),
            "",
            _YUNET_INPUT_SIZE,
            score_threshold=_YUNET_CONFIDENCE_THRESHOLD,
            nms_threshold=0.3,
            top_k=5,
        )
        return detector

    # ---- 主入口 ----

    def verify_crops(self, targets: list[VerifyKnownFaceCropTarget]) -> VerifyKnownFaceCropsResponse:
        results: list[VerifyKnownFaceCropResult] = []
        for target in targets:
            results.append(self._verify_one(target))
        return VerifyKnownFaceCropsResponse(results=results)

    def _verify_one(self, target: VerifyKnownFaceCropTarget) -> VerifyKnownFaceCropResult:
        face_id = int(target.face_id)
        base = VerifyKnownFaceCropResult(
            face_id=face_id,
            verification_status="error",
            face_box_width_px=int(target.face_box_width_px),
            face_box_height_px=int(target.face_box_height_px),
            primary_detector_score=float(target.primary_detector_score),
            evidence_schema_version=EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH_SCALE_NORMALIZED,
        )

        # 模型不可用：整批 error，绝不退回 v1 同源评分。
        if not self.available:
            base.reason_codes = ["verifier_unavailable"]
            return base

        # 解码上下文裁剪。
        frame = _decode_base64_image(target.context_crop_base64)
        if frame is None:
            base.reason_codes = ["context_decode_failed"]
            return base

        h, w = frame.shape[:2]
        base.context_crop_width_px = int(w)
        base.context_crop_height_px = int(h)
        # 原图尺寸由后端 evidence 记录，ML 端只回填裁剪尺寸；original_* 留 0 由后端覆盖。
        base.original_width = 0
        base.original_height = 0

        # 输入短边不足 → uncertain（验证器无法可靠判断）。
        # 短边判断始终基于未缩放上下文：缩小不会让输入变得可判，放大则可能伪造可靠证据。
        short_edge = min(w, h)
        if short_edge < _YUNET_MIN_INPUT_SHORT_EDGE:
            base.verification_status = "uncertain"
            base.reason_codes = ["input_too_small"]
            base.quality = _compute_quality(
                frame, w, h,
                int(target.face_box_offset_x), int(target.face_box_offset_y),
                int(target.face_box_width_px), int(target.face_box_height_px),
            )
            return base

        # 尺度归一化：目标脸最长边 >256 时等比缩小上下文副本送 YuNet；小脸不放大。
        plan = _plan_detection_input(
            w, h,
            int(target.face_box_width_px), int(target.face_box_height_px),
        )
        base.verifier_input_scale = round(float(plan.scale), 6)
        base.verifier_input_width_px = int(plan.input_width)
        base.verifier_input_height_px = int(plan.input_height)

        # 仅在 scale<1 时生成缩小副本（INTER_AREA 适合下采样）；scale==1 直接用原 frame。
        if plan.scale < 1.0:
            detection_frame = cv2.resize(
                frame,
                (plan.input_width, plan.input_height),
                interpolation=cv2.INTER_AREA,
            )
        else:
            detection_frame = frame

        # 推理异常 → error。
        try:
            raw_detected = self._run_yunet(detection_frame)
        except Exception as exc:  # pragma: no cover - ONNX 推理异常
            logger.warning("YuNet inference failed for face %s: %s", face_id, exc)
            base.reason_codes = ["verifier_inference_failed"]
            return base

        # 质量特征在未缩放原图人脸框（精确按 offset+尺寸裁取）上计算并归一化。
        base.quality = _compute_quality(
            frame, w, h,
            int(target.face_box_offset_x), int(target.face_box_offset_y),
            int(target.face_box_width_px), int(target.face_box_height_px),
        )

        # 把 YuNet 检测框按 1/scale 映射回未缩放上下文坐标；scale==1 时不额外取整。
        detected = _map_boxes_to_original(raw_detected, plan.scale, w, h)

        # 判定：是否匹配到目标脸框（IoU>=阈值），而非裁剪图几何中心。
        m = _match_target_face(
            detected,
            int(target.face_box_offset_x), int(target.face_box_offset_y),
            int(target.face_box_width_px), int(target.face_box_height_px),
        )
        base.max_context_score = round(float(m.max_context_score), 6)
        # 诊断字段：始终记录与目标框最大 IoU 的候选（即使低于阈值），供审计/排障。
        # 不作为自动隔离、质量分或 UI 的确认分；不得据此放宽 target_match_iou 阈值。
        if m.best_target_iou is not None:
            base.best_target_iou = round(float(m.best_target_iou), 6)
            base.best_target_candidate_score = round(float(m.best_target_candidate_score), 6)
            if m.best_target_candidate_box is not None:
                bx, by, bw, bh = m.best_target_candidate_box
                base.best_target_candidate_box = CandidateBox(x=bx, y=by, width=bw, height=bh)

        if m.target_match_iou is not None:
            base.target_match_iou = round(float(m.target_match_iou), 6)
            base.verification_status = "face"
            base.verifier_score = round(float(m.target_match_score), 6)
        else:
            base.verification_status = "no_face"
            # 未匹配目标脸时分数为 0，不再写入裁剪内其他脸分数，避免「未检测到脸 77.6%」矛盾文案。
            base.verifier_score = 0.0
            codes = ["target_face_not_matched"]
            if m.max_context_score > 0:
                codes.append("context_face_not_target")
            base.reason_codes = codes
        return base

    def _run_yunet(self, frame: np.ndarray) -> list[tuple[float, tuple[int, int, int, int]]]:
        """运行 YuNet，返回 (score, (x,y,w,h)) 列表（裁剪副本坐标系）。"""
        detector = self._create_detector()
        detector.setInputSize((frame.shape[1], frame.shape[0]))
        _, faces = detector.detect(frame)
        out: list[tuple[float, tuple[int, int, int, int]]] = []
        if faces is None:
            return out
        for f in faces:
            x, y, fw, fh = int(f[0]), int(f[1]), int(f[2]), int(f[3])
            conf = float(f[-1]) if len(f) >= 14 else 0.0
            out.append((conf, (x, y, fw, fh)))
        return out


def _map_boxes_to_original(
    detected: list[tuple[float, tuple[int, int, int, int]]],
    scale: float,
    frame_width: int,
    frame_height: int,
) -> list[tuple[float, tuple[int, int, int, int]]]:
    """把缩放坐标系下的 YuNet 检测框映射回未缩放上下文坐标。

    scale==1 时直接返回原框，不额外取整，避免无缩放场景引入坐标漂移。
    scale<1 时按 round(value/scale) 映射，再裁切到原图边界（负值归零、超出框宽高截断）。
    """
    if scale >= 1.0:
        return detected
    out: list[tuple[float, tuple[int, int, int, int]]] = []
    for conf, (x, y, w, h) in detected:
        mx = max(0, round(x / scale))
        my = max(0, round(y / scale))
        mw = max(0, round(w / scale))
        mh = max(0, round(h / scale))
        # 裁切到原图边界。
        if mx >= frame_width or my >= frame_height:
            continue
        mw = min(mw, frame_width - mx)
        mh = min(mh, frame_height - my)
        if mw <= 0 or mh <= 0:
            continue
        out.append((conf, (mx, my, mw, mh)))
    return out


def _decode_base64_image(b64: str) -> np.ndarray | None:
    try:
        payload = b64.split(",", 1)[-1]
        raw = base64.b64decode(payload)
        buffer = np.frombuffer(raw, dtype=np.uint8)
        frame = cv2.imdecode(buffer, cv2.IMREAD_COLOR)
        return frame
    except Exception:
        return None


def _iou(a: tuple[int, int, int, int], b: tuple[int, int, int, int]) -> float:
    """两个 (x, y, w, h) 框的 IoU。宽高为 0 或不相交时返回 0。"""
    ax, ay, aw, ah = a
    bx, by, bw, bh = b
    ax2, ay2 = ax + aw, ay + ah
    bx2, by2 = bx + bw, by + bh
    ix0 = max(ax, bx)
    iy0 = max(ay, by)
    ix1 = min(ax2, bx2)
    iy1 = min(ay2, by2)
    iw = ix1 - ix0
    ih = iy1 - iy0
    if iw <= 0 or ih <= 0:
        return 0.0
    inter = float(iw * ih)
    area_a = float(max(0, aw) * max(0, ah))
    area_b = float(max(0, bw) * max(0, bh))
    union = area_a + area_b - inter
    if union <= 0:
        return 0.0
    return inter / union


@dataclass
class _TargetMatch:
    """_match_target_face 的判定与诊断结果。

    target_match_*：仅当存在 IoU>=阈值的候选时填入（决定 face/no_face）。
    best_target_*：与目标框几何最接近（最大 IoU）的候选诊断，即便低于阈值也记录，
        仅供审计/排障，不作为自动隔离或确认分依据。无任何候选时 best_target_iou 为 None。
    """

    target_match_score: float
    target_match_iou: float | None
    max_context_score: float
    best_target_iou: float | None
    best_target_candidate_score: float
    best_target_candidate_box: tuple[int, int, int, int] | None


def _match_target_face(
    detected: list[tuple[float, tuple[int, int, int, int]]],
    target_x: int,
    target_y: int,
    target_w: int,
    target_h: int,
) -> _TargetMatch:
    """在裁剪副本坐标系内，把 YuNet 检测框与目标脸框做 IoU 匹配。

    目标脸框为请求中的 (face_box_offset_x/y, face_box_width/height)。
    仅 IoU >= _YUNET_TARGET_IOU_THRESHOLD 的候选可匹配目标；多个候选命中时选 IoU 最大者，
    IoU 相同时选分数更高者。绝不因裁剪内另一张脸分数更高而判目标脸存在。

    诊断维度始终计算「与目标框最大 IoU 的候选」（best_target_*），即便该候选低于阈值，
    用于审计「YuNet 实际检测到了什么框、与目标框相差多少」。诊断不改变 face/no_face 判定。
    """
    max_context_score = 0.0
    best_match_iou: float | None = None  # 命中阈值的最优 IoU
    best_match_score = 0.0
    # 诊断：与目标框几何最接近的候选；同 IoU 取更高分。无候选时保持 None。
    best_diag_iou: float | None = None
    best_diag_score = 0.0
    best_diag_box: tuple[int, int, int, int] | None = None
    target_box = (target_x, target_y, target_w, target_h)
    for conf, box in detected:
        if conf > max_context_score:
            max_context_score = conf
        iou = _iou(box, target_box)
        # 诊断跟踪先于阈值过滤：即便 iou<阈值也记录为「最接近目标的候选」。
        if (
            best_diag_iou is None
            or iou > best_diag_iou
            or (iou == best_diag_iou and conf > best_diag_score)
        ):
            best_diag_iou = iou
            best_diag_score = conf
            best_diag_box = box
        if iou < _YUNET_TARGET_IOU_THRESHOLD:
            continue
        if best_match_iou is None or iou > best_match_iou or (iou == best_match_iou and conf > best_match_score):
            best_match_iou = iou
            best_match_score = conf
    return _TargetMatch(
        target_match_score=best_match_score,
        target_match_iou=best_match_iou,
        max_context_score=max_context_score,
        best_target_iou=best_diag_iou,
        best_target_candidate_score=best_diag_score,
        best_target_candidate_box=best_diag_box,
    )


def _compute_quality(
    frame: np.ndarray,
    w: int,
    h: int,
    face_offset_x: int,
    face_offset_y: int,
    face_width: int,
    face_height: int,
) -> V2QualityFeatures:
    """在原图人脸框（按 offset+尺寸从上下文裁剪中精确裁取）上计算质量特征，统一归一化到固定短边。

    严格遵循任务书「原图质量指标只在未缩放的人脸框内计算」：用 (offset_x, offset_y,
    face_width, face_height) 精确裁取人脸框区域，而非裁剪中心近似。归一化到固定短边后再算
    Laplacian 清晰度、亮度、对比度，消除尺寸差异对清晰度的影响。遮挡/几何可用性暂以对比度
    过低近似（v1 shadow 校准后细化）。
    """
    # 裁取人脸框区域，裁切到上下文裁剪边界。
    x0 = max(0, face_offset_x)
    y0 = max(0, face_offset_y)
    x1 = min(w, face_offset_x + face_width)
    y1 = min(h, face_offset_y + face_height)
    if x1 <= x0 or y1 <= y0:
        return V2QualityFeatures(
            sharpness_norm=0.0,
            brightness_norm=0.0,
            contrast_norm=0.0,
            occluded=True,
            quality_domain=_QUALITY_DOMAIN,
            quality_version=_QUALITY_VERSION,
        )

    face_crop = frame[y0:y1, x0:x1]
    if face_crop.size == 0:
        return V2QualityFeatures(
            sharpness_norm=0.0,
            brightness_norm=0.0,
            contrast_norm=0.0,
            occluded=True,
            quality_domain=_QUALITY_DOMAIN,
            quality_version=_QUALITY_VERSION,
        )

    short_edge = min(face_crop.shape[1], face_crop.shape[0])
    if short_edge <= 0:
        return V2QualityFeatures(
            sharpness_norm=0.0,
            brightness_norm=0.0,
            contrast_norm=0.0,
            occluded=True,
            quality_domain=_QUALITY_DOMAIN,
            quality_version=_QUALITY_VERSION,
        )
    scale = _QUALITY_NORM_SHORT_EDGE / short_edge
    resized = cv2.resize(face_crop, (max(1, int(face_crop.shape[1] * scale)), _QUALITY_NORM_SHORT_EDGE))
    gray = cv2.cvtColor(resized, cv2.COLOR_BGR2GRAY)
    sharpness = float(cv2.Laplacian(gray, cv2.CV_64F).var())
    brightness = float(gray.mean())
    contrast = float(gray.std())
    # 低对比度近似遮挡/不可用。
    occluded = contrast < 5.0

    return V2QualityFeatures(
        sharpness_norm=round(sharpness, 6),
        brightness_norm=round(brightness, 6),
        contrast_norm=round(contrast, 6),
        occluded=occluded,
        quality_domain=_QUALITY_DOMAIN,
        quality_version=_QUALITY_VERSION,
    )
