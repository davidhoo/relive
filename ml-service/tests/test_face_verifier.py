"""YuNet FaceVerifier 单元测试。

覆盖 face / no_face / uncertain / error 四种返回，以及模型缺失/SHA 校验失败时
绝不退回 v1 同源评分（available=False → 全部 error）。

测试不依赖真实 YuNet ONNX：通过注入 _detector_factory 与伪造模型文件控制行为。
"""

from __future__ import annotations

import base64
from pathlib import Path

import cv2
import numpy as np

from app.models import face_verifier
from app.models.face_verifier import FaceVerifier, _YUNET_MIN_INPUT_SHORT_EDGE
from app.schemas import EVIDENCE_SCHEMA_VERSION_V2, VerifyKnownFaceCropTarget


def _write_fake_assets(tmp_path: Path, *, model_bytes: bytes = b"fake-onnx", digest: str | None = None) -> tuple[Path, Path]:
    """写入伪造模型文件 + SHA256SUMS。digest=None 时写入正确 digest，否则写入给定错误值。"""
    import hashlib

    model_path = tmp_path / "face_detection_yunet_2023mar.onnx"
    model_path.write_bytes(model_bytes)
    sha_path = tmp_path / "SHA256SUMS"
    real_digest = hashlib.sha256(model_bytes).hexdigest()
    sha_path.write_text(f"{digest or real_digest}  {model_path.name}\n")
    return model_path, sha_path


def _encode_image(frame: np.ndarray) -> str:
    ok, encoded = cv2.imencode(".jpg", frame)
    assert ok
    return base64.b64encode(encoded.tobytes()).decode("utf-8")


def _make_verifier_with_factory(tmp_path: Path, factory) -> FaceVerifier:
    """构造一个 available=True、检测器由 factory 产生的验证器。"""
    model_path, sha_path = _write_fake_assets(tmp_path)
    v = FaceVerifier(model_path=model_path, sha_path=sha_path)
    # 真实 cv2.FaceDetectorYN_create 在假模型上会失败，强制注入测试工厂并标记可用。
    v._detector_factory = factory
    v.available = True
    return v


def test_verifier_unavailable_when_model_missing(tmp_path: Path):
    """模型文件缺失 → available=False，所有目标 error，不退回 v1。"""
    v = FaceVerifier(model_path=tmp_path / "nope.onnx", sha_path=tmp_path / "SHA256SUMS")
    assert v.available is False

    resp = v.verify_crops([VerifyKnownFaceCropTarget(face_id=1, context_crop_base64="x", face_box_width_px=50, face_box_height_px=50, primary_detector_score=0.3)])
    assert len(resp.results) == 1
    assert resp.results[0].verification_status == "error"
    assert "verifier_unavailable" in resp.results[0].reason_codes


def test_verifier_sha_mismatch_marks_unavailable(tmp_path: Path):
    """SHA256 不匹配 → available=False，绝不静默退回 v1。"""
    model_path, sha_path = _write_fake_assets(tmp_path, digest="0" * 64)
    v = FaceVerifier(model_path=model_path, sha_path=sha_path)
    assert v.available is False

    resp = v.verify_crops([VerifyKnownFaceCropTarget(face_id=1, context_crop_base64="x", face_box_width_px=50, face_box_height_px=50, primary_detector_score=0.3)])
    assert resp.results[0].verification_status == "error"


def test_verifier_no_face(tmp_path: Path):
    """成功推理但无检测 → no_face。"""
    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                return None, None
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((120, 120, 3), 200, dtype=np.uint8)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(face_id=2, context_crop_base64=_encode_image(frame), face_box_width_px=50, face_box_height_px=50, primary_detector_score=0.4)])
    assert resp.results[0].verification_status == "no_face"
    assert resp.results[0].quality is not None


def test_verifier_face_centered(tmp_path: Path):
    """检测框中心落在裁剪中心区域 → face。"""
    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                # YuNet 返回 [x,y,w,h, ..., score]；中心脸放在裁剪中心。
                faces = np.array([[40, 40, 40, 40, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.9]])
                return None, faces
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((120, 120, 3), 200, dtype=np.uint8)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(face_id=3, context_crop_base64=_encode_image(frame), face_box_width_px=50, face_box_height_px=50, primary_detector_score=0.4)])
    assert resp.results[0].verification_status == "face"
    assert resp.results[0].verifier_score == 0.9


def test_verifier_face_off_center_is_no_face(tmp_path: Path):
    """检测框在角落（非中心区域）→ no_face，避免上下文里的人被误判为目标脸。"""
    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                # 角落脸，中心 (5,5) 远离裁剪中心。
                faces = np.array([[0, 0, 10, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.95]])
                return None, faces
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((120, 120, 3), 200, dtype=np.uint8)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(face_id=4, context_crop_base64=_encode_image(frame), face_box_width_px=50, face_box_height_px=50, primary_detector_score=0.4)])
    assert resp.results[0].verification_status == "no_face"


def test_verifier_uncertain_when_input_too_small(tmp_path: Path):
    """裁剪副本短边低于模型最小可判尺寸 → uncertain。"""
    def factory():
        raise AssertionError("不应调用推理")

    v = _make_verifier_with_factory(tmp_path, factory)
    tiny = np.full((10, 10, 3), 200, dtype=np.uint8)  # 短边 10 < 24
    resp = v.verify_crops([VerifyKnownFaceCropTarget(face_id=5, context_crop_base64=_encode_image(tiny), face_box_width_px=5, face_box_height_px=5, primary_detector_score=0.4)])
    assert resp.results[0].verification_status == "uncertain"
    assert "input_too_small" in resp.results[0].reason_codes


def test_verifier_error_on_decode_failure(tmp_path: Path):
    """上下文裁剪解码失败 → error。"""
    def factory():
        raise AssertionError("不应调用推理")

    v = _make_verifier_with_factory(tmp_path, factory)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(face_id=6, context_crop_base64="!!not-base64!!", face_box_width_px=50, face_box_height_px=50, primary_detector_score=0.4)])
    assert resp.results[0].verification_status == "error"
    assert "context_decode_failed" in resp.results[0].reason_codes


def test_min_input_short_edge_constant_positive():
    assert _YUNET_MIN_INPUT_SHORT_EDGE > 0


def test_evidence_schema_version_constant():
    assert EVIDENCE_SCHEMA_VERSION_V2 == "independent_v2"


def test_compute_quality_uses_exact_face_box_offset_not_center():
    """回归测试：质量指标必须在精确人脸框区域计算，不得用裁剪中心近似。

    构造一张上下文裁剪：人脸框区域（offset 40,40, 40x40）填亮像素，其余区域填暗像素。
    若 _compute_quality 用裁剪中心近似，亮度会被暗背景拉低；用精确 offset 则亮度接近亮值。
    """
    from app.models.face_verifier import _compute_quality

    # 120x120 全暗，仅人脸框区域 (40,40)-(80,80) 填亮。
    frame = np.full((120, 120, 3), 10, dtype=np.uint8)
    frame[40:80, 40:80] = 220

    q = _compute_quality(frame, 120, 120, face_offset_x=40, face_offset_y=40, face_width=40, face_height=40)
    # 人脸框内亮度应接近 220（精确区域），而非被暗背景拉低到接近 10/中心混合值。
    assert q.brightness_norm > 180, f"expected face-box brightness ~220, got {q.brightness_norm}"

    # 对照：错误地用裁剪中心 50%（约 30..90 范围）会混入大量暗像素，亮度显著更低。
    # 这里不直接调旧实现，仅断言精确区域亮度足够高即可证明用的是 offset 区域。


def test_compute_quality_occluded_when_low_contrast():
    """低对比度人脸框 → occluded=True。"""
    from app.models.face_verifier import _compute_quality

    frame = np.full((80, 80, 3), 128, dtype=np.uint8)  # 纯灰，对比度 0
    q = _compute_quality(frame, 80, 80, face_offset_x=10, face_offset_y=10, face_width=40, face_height=40)
    assert q.occluded is True
    assert q.contrast_norm < 5.0
