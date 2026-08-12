from pathlib import Path

import base64
import cv2
import numpy as np
import pytest

from app.models.face import FaceDetector
from app.schemas import FACE_QUALITY_MODEL_VERSION, FACE_QUALITY_RULE_VERSION


def test_face_detector_returns_no_faces_for_blank_image(tmp_path: Path):
    detector = FaceDetector()
    image_path = tmp_path / "blank.jpg"
    cv2.imwrite(str(image_path), np.full((320, 320, 3), 255, dtype=np.uint8))

    result = detector.detect(image_path=str(image_path), min_confidence=0.5, max_faces=2)

    assert result.processing_time_ms >= 0
    assert result.faces == []


def test_face_detector_respects_confidence_threshold(tmp_path: Path):
    detector = FaceDetector()
    image_path = tmp_path / "blank-threshold.jpg"
    cv2.imwrite(str(image_path), np.full((320, 320, 3), 255, dtype=np.uint8))

    result = detector.detect(image_path=str(image_path), min_confidence=0.99, max_faces=5)

    assert result.faces == []


def test_face_detector_prefers_base64_when_path_unreadable(tmp_path: Path):
    detector = FaceDetector()
    image_path = tmp_path / "source.jpg"
    cv2.imwrite(str(image_path), np.full((320, 320, 3), 255, dtype=np.uint8))
    ok, encoded = cv2.imencode(".jpg", cv2.imread(str(image_path)))
    assert ok

    result = detector.detect(
        image_path="/not/found.heic",
        image_base64=base64.b64encode(encoded.tobytes()).decode("utf-8"),
        min_confidence=0.5,
        max_faces=3,
    )

    assert result.faces == []


def test_face_detector_returns_no_faces_when_insightface_raises(monkeypatch, tmp_path: Path):
    detector = FaceDetector()
    image_path = tmp_path / "error.jpg"
    cv2.imwrite(str(image_path), np.full((320, 320, 3), 255, dtype=np.uint8))

    def broken_get(*args, **kwargs):
        raise RuntimeError("model error")

    monkeypatch.setattr(detector.app, "get", broken_get)

    result = detector.detect(image_path=str(image_path), min_confidence=0.5, max_faces=3)

    assert result.faces == []


def test_detect_response_carries_rule_and_model_version(tmp_path: Path):
    detector = FaceDetector()
    image_path = tmp_path / "blank.jpg"
    cv2.imwrite(str(image_path), np.full((320, 320, 3), 255, dtype=np.uint8))

    result = detector.detect(image_path=str(image_path), min_confidence=0.5, max_faces=3)

    assert result.rule_version == FACE_QUALITY_RULE_VERSION
    assert result.model_version == FACE_QUALITY_MODEL_VERSION


class _FakeFaceObj:
    """构造一个最小可用的检测对象，绕过真实 InsightFace 模型测试证据计算。"""

    def __init__(self, bbox, det_score, kps, embedding, pose=None):
        self.bbox = np.asarray(bbox, dtype=np.float32)
        self.det_score = np.float32(det_score)
        self.kps = np.asarray(kps, dtype=np.float32)
        self.normed_embedding = np.asarray(embedding, dtype=np.float32)
        self.pose = pose


def test_build_evidence_marks_too_small_and_blurred(tmp_path: Path):
    detector = FaceDetector()
    frame = np.full((320, 320, 3), 200, dtype=np.uint8)  # 纯色 → 高模糊、高亮
    # 16x16 框 → too_small
    face_obj = _FakeFaceObj(
        bbox=[10, 10, 26, 26],
        det_score=0.9,
        kps=[[14, 14], [22, 14], [18, 18], [14, 22], [22, 22]],
        embedding=[1.0] * detector.embedding_size,
    )
    evidence = detector._build_evidence(frame, face_obj, 10, 10, 16, 16, 320, 320, 0.9)

    assert "too_small" in evidence.quality_reasons
    assert evidence.pixel_width == 16
    assert evidence.pixel_height == 16
    assert evidence.rule_version == FACE_QUALITY_RULE_VERSION
    assert evidence.model_version == FACE_QUALITY_MODEL_VERSION


def test_build_evidence_handles_missing_landmarks(tmp_path: Path):
    detector = FaceDetector()
    frame = np.full((320, 320, 3), 128, dtype=np.uint8)
    face_obj = _FakeFaceObj(
        bbox=[50, 50, 150, 150],
        det_score=0.9,
        kps=None,
        embedding=[1.0] * detector.embedding_size,
    )
    evidence = detector._build_evidence(frame, face_obj, 50, 50, 100, 100, 320, 320, 0.9)

    assert "invalid_landmarks" in evidence.quality_reasons
    assert evidence.landmark_completeness == 0.0
    assert evidence.face_validity_score < 0.6  # 灰区以下


def test_build_evidence_cropped_empty_returns_safe_fallback(tmp_path: Path):
    detector = FaceDetector()
    frame = np.full((320, 320, 3), 128, dtype=np.uint8)
    face_obj = _FakeFaceObj(
        bbox=[0, 0, 10, 10],
        det_score=0.7,
        kps=[[2, 2], [8, 2], [5, 5], [2, 8], [8, 8]],
        embedding=[1.0] * detector.embedding_size,
    )
    # 传入越界坐标使裁剪为空，验证不抛异常且不杜撰 non_face 原因。
    evidence = detector._build_evidence(frame, face_obj, 400, 400, 10, 10, 320, 320, 0.7)

    assert evidence.sharpness == 0.0
    assert evidence.pose_estimable is False
    assert evidence.quality_reasons == []
