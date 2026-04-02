from fastapi.testclient import TestClient

from app.main import app


def test_health_endpoint():
    client = TestClient(app)

    response = client.get("/api/v1/health")

    assert response.status_code == 200
    assert response.json() == {"status": "ok"}


def test_detect_faces_endpoint_shape():
    client = TestClient(app)

    response = client.post(
        "/api/v1/detect-faces",
        json={
            "image_path": "/photos/family.jpg",
            "min_confidence": 0.5,
            "max_faces": 3,
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert "faces" in payload
    assert "processing_time_ms" in payload
    assert len(payload["faces"]) == 1

    face = payload["faces"][0]
    assert set(face.keys()) == {"bbox", "confidence", "quality_score", "embedding"}
    assert set(face["bbox"].keys()) == {"x", "y", "width", "height"}
    assert len(face["embedding"]) == 512
