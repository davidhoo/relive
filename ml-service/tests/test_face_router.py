from pathlib import Path

import cv2
import numpy as np
from fastapi.testclient import TestClient

from app.main import app


def test_health_endpoint():
    client = TestClient(app)

    response = client.get("/api/v1/health")

    assert response.status_code == 200
    assert response.json() == {"status": "ok"}


def test_detect_faces_endpoint_shape(tmp_path: Path):
    client = TestClient(app)
    image_path = tmp_path / "blank.jpg"
    cv2.imwrite(str(image_path), np.full((320, 320, 3), 255, dtype=np.uint8))

    response = client.post(
        "/api/v1/detect-faces",
        json={
            "image_path": str(image_path),
            "min_confidence": 0.5,
            "max_faces": 3,
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert "faces" in payload
    assert "processing_time_ms" in payload
    assert payload["faces"] == []
