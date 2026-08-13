"""v2 独立人脸验证器：YuNet ONNX。

与 v1 的 InsightFace buffalo_sc 主检测链路不同，YuNet 是独立验证器。
模型文件作为镜像内受版本和 SHA-256 保护的资产交付，不允许容器运行时下载：
模型缺失或 SHA256SUMS 校验失败时，验证器进入 unavailable 状态，verify_crops 对所有目标
返回 error，使后端历史 worker 写 technical_error——绝不静默退回 v1 同源评分。

verify_crops 的判定语义：
- face: YuNet 在裁剪副本中检测到置信度足够且位于目标中心区域的脸；
- no_face: 成功推理但无可靠脸；
- uncertain: 输入短边不足模型最小可判尺寸或结果边界不可靠；
- error: 加载/推理/解码异常。
"""

from __future__ import annotations

import base64
import hashlib
import logging
from pathlib import Path

import cv2
import numpy as np

from app.schemas import (
    EVIDENCE_SCHEMA_VERSION_V2,
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
_YUNET_CENTER_RATIO = 0.4  # 检测框中心须落在目标中心 region（裁剪中心 40% 区域）内
_YUNET_MIN_INPUT_SHORT_EDGE = 24  # 裁剪副本短边低于此值判 uncertain（模型最小可判尺寸）

# 质量特征归一化短边（计算域）。
_QUALITY_NORM_SHORT_EDGE = 96
_QUALITY_DOMAIN = f"original_face_box_norm_to_{_QUALITY_NORM_SHORT_EDGE}"
_QUALITY_VERSION = "v1"

# YuNet 默认输入尺寸（FaceDetectorYN 的 size 参数）。
_YUNET_INPUT_SIZE = (320, 320)


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
            evidence_schema_version=EVIDENCE_SCHEMA_VERSION_V2,
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

        # 推理异常 → error。
        try:
            detected = self._run_yunet(frame)
        except Exception as exc:  # pragma: no cover - ONNX 推理异常
            logger.warning("YuNet inference failed for face %s: %s", face_id, exc)
            base.reason_codes = ["verifier_inference_failed"]
            return base

        # 质量特征在原图人脸框（精确按 offset+尺寸裁取）上计算并归一化。
        base.quality = _compute_quality(
            frame, w, h,
            int(target.face_box_offset_x), int(target.face_box_offset_y),
            int(target.face_box_width_px), int(target.face_box_height_px),
        )

        # 判定：检测到足够置信且位于目标中心区域的脸 → face；否则 no_face。
        is_face, score = _has_centered_face(detected, w, h)
        if is_face:
            base.verification_status = "face"
            base.verifier_score = round(float(score), 6)
        else:
            base.verification_status = "no_face"
            base.verifier_score = round(float(score), 6)
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


def _decode_base64_image(b64: str) -> np.ndarray | None:
    try:
        payload = b64.split(",", 1)[-1]
        raw = base64.b64decode(payload)
        buffer = np.frombuffer(raw, dtype=np.uint8)
        frame = cv2.imdecode(buffer, cv2.IMREAD_COLOR)
        return frame
    except Exception:
        return None


def _has_centered_face(
    detected: list[tuple[float, tuple[int, int, int, int]]],
    w: int,
    h: int,
) -> tuple[bool, float]:
    """检测框中心须落在裁剪中心 _YUNET_CENTER_RATIO 区域内。

    上下文裁剪以人脸框为中心扩展，目标脸应位于裁剪中心；边缘检测框更可能是上下文里的人。
    返回 (是否为 face, 最高置信度)。无检测时 score=0。
    """
    if not detected:
        return False, 0.0
    cx_low = w * (0.5 - _YUNET_CENTER_RATIO / 2)
    cx_high = w * (0.5 + _YUNET_CENTER_RATIO / 2)
    cy_low = h * (0.5 - _YUNET_CENTER_RATIO / 2)
    cy_high = h * (0.5 + _YUNET_CENTER_RATIO / 2)

    best_score = 0.0
    centered = False
    for conf, (x, y, fw, fh) in detected:
        if conf > best_score:
            best_score = conf
        fx = x + fw / 2
        fy = y + fh / 2
        if cx_low <= fx <= cx_high and cy_low <= fy <= cy_high:
            centered = True
    return centered, best_score


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
