from pathlib import Path

import base64
import cv2
import numpy as np
from fastapi.testclient import TestClient

from app.main import app


def test_health_endpoint(monkeypatch):
    """验证器可用时 health 返回 200 + ok + verifier identity。"""
    client = TestClient(app)
    from app.routers import faces

    class _OKVerifier:
        available = True
        verifier_name = "yunet"
        verifier_version = "opencv-yunet-2023mar"

    monkeypatch.setattr(faces, "verifier", _OKVerifier())

    response = client.get("/api/v1/health")

    assert response.status_code == 200
    assert response.json() == {
        "status": "ok",
        "verifier_available": True,
        "verifier_name": "yunet",
        "verifier_version": "opencv-yunet-2023mar",
    }


def test_health_endpoint_degraded_when_verifier_unavailable(monkeypatch):
    """验证器不可用时 health 返回 503 + degraded，绝不伪报 healthy。

    复现 NAS 运行 #3 的根因：模型缺失时 FaceVerifier.available=False，health 必须降级，
    使后端阻断 v2 run 创建/恢复、Docker healthcheck 标记 unhealthy。
    """
    client = TestClient(app)
    from app.routers import faces

    class _UnavailableVerifier:
        available = False
        verifier_name = "yunet"
        verifier_version = "opencv-yunet-2023mar"

    monkeypatch.setattr(faces, "verifier", _UnavailableVerifier())

    response = client.get("/api/v1/health")

    assert response.status_code == 503
    assert response.json() == {
        "status": "degraded",
        "verifier_available": False,
        "verifier_name": "yunet",
        "verifier_version": "opencv-yunet-2023mar",
    }


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
    assert "rule_version" in payload
    assert "model_version" in payload


def _blank_image_base64() -> str:
    frame = np.full((320, 320, 3), 255, dtype=np.uint8)
    ok, encoded = cv2.imencode(".jpg", frame)
    assert ok
    return base64.b64encode(encoded.tobytes()).decode("utf-8")


def test_score_known_faces_blank_image_returns_unmatched():
    """空白图无检测 → 所有目标 unmatched（不得当作 non_face）。"""
    client = TestClient(app)

    response = client.post(
        "/api/v1/score-known-faces",
        json={
            "image_base64": _blank_image_base64(),
            "targets": [
                {"face_id": 42, "bbox": {"x": 0.1, "y": 0.1, "width": 0.2, "height": 0.2}},
                {"face_id": 43, "bbox": {"x": 0.5, "y": 0.5, "width": 0.2, "height": 0.2}},
            ],
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert "results" in payload
    assert len(payload["results"]) == 2
    # 保序 + 全部 unmatched
    assert payload["results"][0]["face_id"] == 42
    assert payload["results"][0]["status"] == "unmatched"
    assert payload["results"][1]["face_id"] == 43
    assert payload["results"][1]["status"] == "unmatched"
    assert "rule_version" in payload
    assert "model_version" in payload


def test_score_known_faces_error_when_insightface_raises(monkeypatch):
    """推理异常 → 所有目标 error（可重试），不伪装判定。"""
    client = TestClient(app)
    from app.routers.faces import detector

    def broken_get(*args, **kwargs):
        raise RuntimeError("model error")

    monkeypatch.setattr(detector.app, "get", broken_get)

    response = client.post(
        "/api/v1/score-known-faces",
        json={
            "image_base64": _blank_image_base64(),
            "targets": [{"face_id": 7, "bbox": {"x": 0.1, "y": 0.1, "width": 0.2, "height": 0.2}}],
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert len(payload["results"]) == 1
    assert payload["results"][0]["status"] == "error"


class _FakeFaceObj:
    def __init__(self, bbox, det_score, kps, embedding, pose=None):
        self.bbox = np.asarray(bbox, dtype=np.float32)
        self.det_score = np.float32(det_score)
        self.kps = np.asarray(kps, dtype=np.float32)
        self.normed_embedding = np.asarray(embedding, dtype=np.float32)
        self.pose = pose


def test_score_known_faces_matches_by_iou_and_is_one_to_one(monkeypatch):
    """可控 mock 检测：目标按最大 IoU 一对一匹配，命中返回 evidence + matched_iou。"""
    client = TestClient(app)
    from app.routers.faces import detector

    # 两个检测框：A 在左上 (40,40,120,120)，B 在右下 (180,180,260,260)。
    fake_faces = [
        _FakeFaceObj(
            bbox=[40, 40, 120, 120],
            det_score=0.95,
            kps=[[60, 60], [100, 60], [80, 80], [60, 100], [100, 100]],
            embedding=[1.0] * detector.embedding_size,
        ),
        _FakeFaceObj(
            bbox=[180, 180, 260, 260],
            det_score=0.9,
            kps=[[200, 200], [240, 200], [220, 220], [200, 240], [240, 240]],
            embedding=[1.0] * detector.embedding_size,
        ),
    ]

    def fake_get(_frame, *args, **kwargs):
        return fake_faces

    monkeypatch.setattr(detector.app, "get", fake_get)

    # 目标 1：与 A 高度重叠（归一化 0.125,0.125,0.25,0.25 ≈ 像素 40,40,80,80）。
    # 目标 2：与 B 高度重叠。
    # 目标 3：与 A、B 都不重叠 → unmatched。
    response = client.post(
        "/api/v1/score-known-faces",
        json={
            "image_base64": _blank_image_base64(),  # base64 仅用于 _load_image；推理用 fake_get
            "targets": [
                {"face_id": 1, "bbox": {"x": 0.125, "y": 0.125, "width": 0.25, "height": 0.25}},
                {"face_id": 2, "bbox": {"x": 0.5625, "y": 0.5625, "width": 0.25, "height": 0.25}},
                {"face_id": 3, "bbox": {"x": 0.01, "y": 0.9, "width": 0.05, "height": 0.05}},
            ],
        },
    )

    assert response.status_code == 200
    results = response.json()["results"]
    assert len(results) == 3

    assert results[0]["face_id"] == 1
    assert results[0]["status"] == "matched"
    assert results[0]["matched_iou"] is not None
    assert results[0]["matched_iou"] > 0.3
    assert results[0]["evidence"] is not None
    assert results[0]["quality_score"] is not None

    assert results[1]["face_id"] == 2
    assert results[1]["status"] == "matched"
    assert results[1]["evidence"] is not None

    assert results[2]["face_id"] == 3
    assert results[2]["status"] == "unmatched"


def test_score_known_faces_one_detection_serves_one_target(monkeypatch):
    """一个检测框不能同时匹配两个目标：第二个竞争者应 unmatched。"""
    client = TestClient(app)
    from app.routers.faces import detector

    fake_faces = [
        _FakeFaceObj(
            bbox=[40, 40, 120, 120],
            det_score=0.95,
            kps=[[60, 60], [100, 60], [80, 80], [60, 100], [100, 100]],
            embedding=[1.0] * detector.embedding_size,
        ),
    ]

    def fake_get(_frame, *args, **kwargs):
        return fake_faces

    monkeypatch.setattr(detector.app, "get", fake_get)

    # 两个目标都指向同一检测框；只有第一个匹配，第二个 unmatched。
    response = client.post(
        "/api/v1/score-known-faces",
        json={
            "image_base64": _blank_image_base64(),
            "targets": [
                {"face_id": 10, "bbox": {"x": 0.125, "y": 0.125, "width": 0.25, "height": 0.25}},
                {"face_id": 11, "bbox": {"x": 0.13, "y": 0.13, "width": 0.25, "height": 0.25}},
            ],
        },
    )

    assert response.status_code == 200
    results = response.json()["results"]
    statuses = sorted(r["status"] for r in results)
    assert statuses == ["matched", "unmatched"]


def test_score_known_faces_rejects_empty_base64():
    """无 image_base64 应被 schema 拒绝（422）。"""
    client = TestClient(app)

    response = client.post(
        "/api/v1/score-known-faces",
        json={"image_base64": "", "targets": []},
    )

    assert response.status_code == 422


def test_verify_known_face_crops_endpoint_shape(monkeypatch):
    """v2 接口保序返回，单条错误只影响对应 item。

    验证器不可用（available=False）→ 所有目标 error（不退回 v1）。本测试强制注入
    available=False，不依赖磁盘模型是否存在，保持与 health 降级测试同模式的确定性。
    """
    client = TestClient(app)
    from app.routers import faces

    # 无论构建前是否 fetch 过模型，强制不可用以验证 error 路径（verifier_unavailable）。
    monkeypatch.setattr(faces.verifier, "available", False)

    response = client.post(
        "/api/v1/verify-known-face-crops",
        json={
            "targets": [
                {"face_id": 1, "context_crop_base64": _blank_image_base64(), "face_box_width_px": 50, "face_box_height_px": 50, "primary_detector_score": 0.4},
                {"face_id": 2, "context_crop_base64": _blank_image_base64(), "face_box_width_px": 50, "face_box_height_px": 50, "primary_detector_score": 0.4},
            ],
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert "results" in payload
    assert len(payload["results"]) == 2
    assert payload["results"][0]["face_id"] == 1
    assert payload["results"][1]["face_id"] == 2
    # evidence_schema_version 在目标框匹配规则后升级为 independent_v2_target_match_v2。
    assert payload["results"][0]["evidence_schema_version"] == "independent_v2_target_match_v2"
    # 验证器不可用 → error，不伪装判定。
    assert payload["results"][0]["verification_status"] == "error"
    assert "verifier_unavailable" in payload["results"][0]["reason_codes"]
    assert "rule_version" in payload


def test_verify_known_face_crops_emits_scale_normalized_schema(monkeypatch):
    """尺度归一化后，成功匹配的大脸证据 schema 升级为 independent_v2_target_match_v3，
    并带 verifier_input_scale/width/height 审计字段。"""
    client = TestClient(app)
    from app.routers import faces

    # 注入可用验证器 + 假检测器工厂：目标框 (0,0,50,50)，scale=1（最长边 50<=256）。
    real_verifier = faces.verifier
    captured = {}

    def fake_factory():
        class D:
            def setInputSize(self, size):
                captured["size"] = (int(size[0]), int(size[1]))
            def detect(self, _frame):
                faces = np.array([[10, 10, 40, 40, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.9]])
                return None, faces
        return D()

    monkeypatch.setattr(real_verifier, "_detector_factory", fake_factory)
    monkeypatch.setattr(real_verifier, "available", True)

    response = client.post(
        "/api/v1/verify-known-face-crops",
        json={
            "targets": [
                {"face_id": 1, "context_crop_base64": _blank_image_base64(),
                 "face_box_offset_x": 0, "face_box_offset_y": 0,
                 "face_box_width_px": 50, "face_box_height_px": 50, "primary_detector_score": 0.4},
            ],
        },
    )
    assert response.status_code == 200
    result = response.json()["results"][0]
    assert result["verification_status"] == "face"
    assert result["evidence_schema_version"] == "independent_v2_target_match_v3"
    assert "verifier_input_scale" in result
    assert result["verifier_input_scale"] == 1.0
    assert result["verifier_input_width_px"] > 0
    assert result["verifier_input_height_px"] > 0
