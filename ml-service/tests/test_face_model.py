from pathlib import Path

import base64
import cv2
import numpy as np
import pytest

from app.models.face import FaceDetector


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


def test_face_detector_returns_no_faces_when_opencv_raises(monkeypatch, tmp_path: Path):
    detector = FaceDetector()
    image_path = tmp_path / "opencv-error.jpg"
    cv2.imwrite(str(image_path), np.full((320, 320, 3), 255, dtype=np.uint8))

    class BrokenClassifier:
        def detect(self, *args, **kwargs):
            raise cv2.error("detect", "detectMultiScale", "boom")

    monkeypatch.setattr(detector, "detector", BrokenClassifier())
    monkeypatch.setattr(detector, "detector_input_size", (320, 320))

    result = detector.detect(image_path=str(image_path), min_confidence=0.5, max_faces=3)

    assert result.faces == []
