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
import pytest

from app.models import face_verifier
from app.models.face_verifier import (
    FaceVerifier,
    _YUNET_MIN_INPUT_SHORT_EDGE,
    _plan_detection_input,
)
from app.schemas import (
    EVIDENCE_SCHEMA_VERSION_V2,
    EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH,
    EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH_SCALE_NORMALIZED,
    VerifyKnownFaceCropTarget,
)


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


def test_verifier_face_matched_to_target_box(tmp_path: Path):
    """检测框与目标脸框重叠足够（IoU>=0.3）→ face，目标匹配分为检测框分数。"""
    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                # 检测框 (10,10,40,40)，目标框 offset(0,0) size(50,50) → IoU≈0.64。
                faces = np.array([[10, 10, 40, 40, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.9]])
                return None, faces
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((120, 120, 3), 200, dtype=np.uint8)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(
        face_id=3, context_crop_base64=_encode_image(frame),
        face_box_offset_x=0, face_box_offset_y=0,
        face_box_width_px=50, face_box_height_px=50, primary_detector_score=0.4)])
    assert resp.results[0].verification_status == "face"
    assert resp.results[0].verifier_score == 0.9
    assert resp.results[0].target_match_iou is not None
    assert resp.results[0].target_match_iou >= 0.3
    assert resp.results[0].max_context_score == 0.9


def test_verifier_edge_target_box_at_corner_is_face(tmp_path: Path):
    """边缘裁剪回归（Face #538580 等价）：目标脸在裁剪左上角而非中心，仍须判 face。

    100×100 上下文裁剪，目标框 (0,0,25,25)（被原图边界截断后 offset=0）；检测框 (2,2,23,23)
    与目标框高度重叠。旧「裁剪中心 40%」规则会因目标脸不在中心而假阴性；目标框匹配应判 face。
    """
    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                faces = np.array([[2, 2, 23, 23, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.92]])
                return None, faces
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((100, 100, 3), 200, dtype=np.uint8)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(
        face_id=538580, context_crop_base64=_encode_image(frame),
        face_box_offset_x=0, face_box_offset_y=0,
        face_box_width_px=25, face_box_height_px=25, primary_detector_score=0.746871)])
    r = resp.results[0]
    assert r.verification_status == "face"
    assert r.verifier_score == 0.92
    assert r.target_match_iou is not None and r.target_match_iou >= 0.3
    assert r.max_context_score == 0.92
    assert r.evidence_schema_version == EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH_SCALE_NORMALIZED


def test_verifier_group_shot_non_target_is_no_face(tmp_path: Path):
    """群像反例：裁剪内有其他人脸但与目标框不重叠 → no_face，不因分数高而误匹配。"""
    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                # 检测框 (45,45,25,25) 在右下，目标框 (0,0,25,25) 在左上，IoU=0。
                faces = np.array([[45, 45, 25, 25, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.95]])
                return None, faces
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((100, 100, 3), 200, dtype=np.uint8)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(
        face_id=4, context_crop_base64=_encode_image(frame),
        face_box_offset_x=0, face_box_offset_y=0,
        face_box_width_px=25, face_box_height_px=25, primary_detector_score=0.4)])
    r = resp.results[0]
    assert r.verification_status == "no_face"
    # 未匹配目标脸 → 分数为 0，不得把上下文里其他脸的 0.95 当作确认分。
    assert r.verifier_score == 0.0
    assert r.target_match_iou is None
    # 诊断字段保留裁剪内最高检测分，供文案区分「未匹配目标」与「裁剪中无任何检测」。
    assert r.max_context_score == 0.95
    assert "target_face_not_matched" in r.reason_codes
    assert "context_face_not_target" in r.reason_codes


def test_verifier_target_match_diagnostics_below_threshold(tmp_path: Path):
    """低于阈值的候选仍须记录诊断，不丢失几何证据。

    目标框 (744,500,745,1083) 取自 Face #538580 真实线上几何：上下文裁剪以人脸框四周各扩展
    100% 构成，offset≈face_w；但该脸位于原图上边界，顶部 padding 被截断，故 offset_y=500 <
    face_h=1083（边缘裁剪特征，与既有「中心 40%」假阴性的根因一致）。

    候选框为结构化占位：完全落在目标框内、IoU≈0.167<0.3，代表「YuNet 框尺度/锚点与存储框
    不同」。尺度归一化后检测在缩放副本上运行，故 factory 返回缩放坐标候选；映射回未缩放
    上下文后应接近 (1000,800,300,450)，IoU≈0.167<0.3。候选框的真实坐标须待任务 5 NAS 定点
    重跑产出 best_target_candidate_box 后替换；本测试仅驱动诊断字段落地，不预先放宽匹配。
    """
    scale = 256 / 1083  # 目标最长边 1083 > 256

    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                # 缩放坐标候选，映射回未缩放后接近 (1000,800,300,450)，IoU≈0.167<0.3。
                faces = np.array([[1000 * scale, 800 * scale, 300 * scale, 450 * scale,
                                   0, 0, 0, 0, 0, 0, 0, 0, 0, 0.77609]])
                return None, faces
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((1600, 1600, 3), 200, dtype=np.uint8)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(
        face_id=538580, context_crop_base64=_encode_image(frame),
        face_box_offset_x=744, face_box_offset_y=500,
        face_box_width_px=745, face_box_height_px=1083, primary_detector_score=0.746871)])
    r = resp.results[0]
    assert r.verification_status == "no_face"
    assert r.max_context_score == 0.77609
    assert r.best_target_iou is not None
    assert r.best_target_candidate_score == 0.77609
    assert r.target_match_iou is None
    assert r.best_target_candidate_box is not None
    # 映射回未缩放坐标后接近 (1000,800,300,450)（允许 ±5 取整漂移：缩放坐标生成 + round(./scale) 反向映射的双向取整偏差）。
    assert abs(r.best_target_candidate_box.x - 1000) <= 5
    assert abs(r.best_target_candidate_box.y - 800) <= 5
    assert abs(r.best_target_candidate_box.width - 300) <= 5
    assert abs(r.best_target_candidate_box.height - 450) <= 5
    assert r.evidence_schema_version == EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH_SCALE_NORMALIZED


def test_verifier_no_detection_keeps_zero_context_score(tmp_path: Path):
    """裁剪内无任何检测 → no_face，max_context_score=0，仅 target_face_not_matched。"""
    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                return None, None
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((120, 120, 3), 200, dtype=np.uint8)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(
        face_id=7, context_crop_base64=_encode_image(frame),
        face_box_offset_x=20, face_box_offset_y=20,
        face_box_width_px=50, face_box_height_px=50, primary_detector_score=0.4)])
    r = resp.results[0]
    assert r.verification_status == "no_face"
    assert r.verifier_score == 0.0
    assert r.max_context_score == 0.0
    assert r.reason_codes == ["target_face_not_matched"]


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
    assert EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH == "independent_v2_target_match_v2"
    assert (
        EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH_SCALE_NORMALIZED
        == "independent_v2_target_match_v3"
    )


# ---- 任务 1：以失败测试固定尺度归一化契约 ----

def test_detection_plan_downscales_large_target_without_upscaling_small_target():
    """缩放依据必须是目标框最长边，不能用整张上下文最长边。

    #538580 等价：上下文裁剪 2235×2666，目标脸框 745×1083（最长边 1083>256）→ 等比缩小，
    使送入 YuNet 的目标脸最长边 ≈256。小脸控制样本（120×153 上下文，目标 40×51）不得放大。
    """
    large = _plan_detection_input(2235, 2666, 745, 1083)
    assert large.scale == pytest.approx(256 / 1083)
    assert (large.input_width, large.input_height) == (
        round(2235 * 256 / 1083),
        round(2666 * 256 / 1083),
    )

    small = _plan_detection_input(120, 153, 40, 51)
    assert small.scale == 1.0
    assert (small.input_width, small.input_height) == (120, 153)


def test_detection_plan_uses_target_long_edge_not_context_long_edge():
    """缩放依据必须是目标框最长边而非上下文最长边，否则边缘裁剪与常规裁剪目标人脸尺度不一致。

    边缘裁剪：上下文短（如 1400），目标脸最长边 1083>256，仍应缩小。
    若误用上下文最长边缩放，会得到不同的 scale，导致边缘裁剪目标脸尺度与常规裁剪不一致。
    """
    plan = _plan_detection_input(1400, 1100, 745, 1083)
    # 目标最长边 1083 > 256 → scale = 256/1083，与 #538580 相同（同目标脸）。
    assert plan.scale == pytest.approx(256 / 1083)
    assert plan.input_width == round(1400 * 256 / 1083)
    assert plan.input_height == round(1100 * 256 / 1083)


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


# ---- 任务 1 Step 2/3：大脸等价回归 + 群像反例（尺度归一化） ----

def test_verifier_scale_normalized_large_target_face_matched(tmp_path: Path):
    """#538580 等价大脸回归：目标脸最长边 >256 → 等比缩小送 YuNet，候选框映射回原坐标后 IoU>=0.3 → face。

    构造未缩放上下文 2235×2666、目标框 (744,500,745,1083)。假 YuNet 检测器断言收到缩放后尺寸，
    并在缩放坐标系返回目标框等比候选；映射回原上下文后应接近 (744,500,745,1083)。
    """
    captured_input_size: dict[str, tuple[int, int]] = {}

    def factory():
        class D:
            def setInputSize(self, size):
                captured_input_size["size"] = (int(size[0]), int(size[1]))
            def detect(self, _frame):
                # 目标脸最长边 1083 > 256 → scale=256/1083 ≈ 0.23638。
                # 缩放后目标框尺寸约 (745*scale, 1083*scale) ≈ (175, 256)，位置 (744*scale, 500*scale)。
                scale = 256 / 1083
                x = 744 * scale
                y = 500 * scale
                w = 745 * scale
                h = 1083 * scale
                faces = np.array([[x, y, w, h, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.9]])
                return None, faces
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((2666, 2235, 3), 200, dtype=np.uint8)  # (h, w)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(
        face_id=538580, context_crop_base64=_encode_image(frame),
        face_box_offset_x=744, face_box_offset_y=500,
        face_box_width_px=745, face_box_height_px=1083, primary_detector_score=0.746871)])
    r = resp.results[0]

    # YuNet 收到的必须是缩放后尺寸，而非原始 2235×2666。
    expected_w = round(2235 * 256 / 1083)
    expected_h = round(2666 * 256 / 1083)
    assert captured_input_size["size"] == (expected_w, expected_h), (
        f"YuNet 应收到缩放后 {expected_w}x{expected_h}，实际 {captured_input_size['size']}"
    )

    assert r.verification_status == "face"
    assert r.target_match_iou is not None
    assert r.target_match_iou >= 0.3
    assert r.verifier_input_scale == pytest.approx(256 / 1083, rel=1e-3)
    assert r.verifier_input_width_px == expected_w
    assert r.verifier_input_height_px == expected_h
    assert r.evidence_schema_version == EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH_SCALE_NORMALIZED
    # 候选框审计坐标必须映射回原上下文坐标，接近 (744,500,745,1083)。
    # 允许 ±5 取整漂移：factory 在缩放坐标生成框、实现再 round(./scale) 映射回原坐标，
    # 双向取整会引入几个像素的累积偏差，属于尺度归一化的固有误差，不影响 IoU 判定。
    assert r.best_target_candidate_box is not None
    assert abs(r.best_target_candidate_box.x - 744) <= 5
    assert abs(r.best_target_candidate_box.y - 500) <= 5
    assert abs(r.best_target_candidate_box.width - 745) <= 5
    assert abs(r.best_target_candidate_box.height - 1083) <= 5


def test_verifier_scale_normalized_group_shot_non_target_is_no_face(tmp_path: Path):
    """群像反例：同一缩放比例下只返回右下方邻脸候选，映射回原坐标后与目标框不重叠 → no_face。

    目标框 (744,500,745,1083)；假检测器返回右下角邻脸（缩放坐标），映射回原坐标后与目标框 IoU=0。
    """
    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                # 邻脸在原坐标右下角 (1700, 1900, 600, 700)，缩放后送入。
                scale = 256 / 1083
                x = 1700 * scale
                y = 1900 * scale
                w = 600 * scale
                h = 700 * scale
                faces = np.array([[x, y, w, h, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.76363]])
                return None, faces
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((2666, 2235, 3), 200, dtype=np.uint8)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(
        face_id=538582, context_crop_base64=_encode_image(frame),
        face_box_offset_x=744, face_box_offset_y=500,
        face_box_width_px=749, face_box_height_px=1119, primary_detector_score=0.76363)])
    r = resp.results[0]

    assert r.verification_status == "no_face"
    assert r.verifier_score == 0.0
    assert r.max_context_score > 0
    assert r.target_match_iou is None
    assert r.verifier_input_scale < 1.0
    assert "target_face_not_matched" in r.reason_codes
    assert "context_face_not_target" in r.reason_codes


def test_verifier_scale_not_applied_to_small_target(tmp_path: Path):
    """#538665 控制样本：目标脸最长边 <=256 不放大，scale=1，候选框坐标不做缩放映射。"""
    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                # 目标框 (10,10,40,51)，检测框 (12,12,38,49) 与其高度重叠。
                faces = np.array([[12, 12, 38, 49, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.85]])
                return None, faces
        return D()

    v = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((153, 120, 3), 200, dtype=np.uint8)  # (h, w) = (153, 120)
    resp = v.verify_crops([VerifyKnownFaceCropTarget(
        face_id=538665, context_crop_base64=_encode_image(frame),
        face_box_offset_x=10, face_box_offset_y=10,
        face_box_width_px=40, face_box_height_px=51, primary_detector_score=0.924)])
    r = resp.results[0]

    assert r.verification_status == "face"
    assert r.verifier_input_scale == 1.0
    assert r.verifier_input_width_px == 120
    assert r.verifier_input_height_px == 153
    # scale=1 时候选框坐标不额外取整偏移。
    assert r.best_target_candidate_box is not None
    assert r.best_target_candidate_box.x == 12
    assert r.best_target_candidate_box.y == 12
    assert r.best_target_candidate_box.width == 38
    assert r.best_target_candidate_box.height == 49
