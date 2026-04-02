from app.models.face import FaceDetector


def test_face_detector_returns_normalized_faces():
    detector = FaceDetector()

    result = detector.detect(image_path="/photos/family.jpg", min_confidence=0.5, max_faces=2)

    assert result.processing_time_ms >= 0
    assert len(result.faces) == 1

    face = result.faces[0]
    assert 0 <= face.bbox.x <= 1
    assert 0 <= face.bbox.y <= 1
    assert 0 < face.bbox.width <= 1
    assert 0 < face.bbox.height <= 1
    assert face.confidence >= 0.5
    assert face.quality_score > 0
    assert len(face.embedding) == 512


def test_face_detector_respects_confidence_threshold():
    detector = FaceDetector()

    result = detector.detect(image_path="/photos/family.jpg", min_confidence=0.99, max_faces=5)

    assert result.faces == []
