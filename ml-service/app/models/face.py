import base64
import os
import time

import cv2
import numpy as np

from app.config import get_settings
from app.schemas import BoundingBox, DetectFacesResponse, DetectedFace


class FaceDetector:
    def __init__(self) -> None:
        settings = get_settings()
        self.settings = settings
        self.embedding_size = settings.embedding_size
        self.default_confidence = settings.default_confidence
        self.app = self._init_insightface()

    def detect(
        self,
        *,
        image_path: str | None = None,
        image_base64: str | None = None,
        min_confidence: float = 0.5,
        max_faces: int = 20,
    ) -> DetectFacesResponse:
        started_at = time.perf_counter()
        frame = self._load_image(image_path=image_path, image_base64=image_base64)

        faces = []
        if frame is not None and max_faces > 0:
            faces = self._detect_faces(frame, min_confidence, max_faces)

        elapsed_ms = int((time.perf_counter() - started_at) * 1000)
        return DetectFacesResponse(faces=faces, processing_time_ms=max(elapsed_ms, 0))

    def _load_image(self, *, image_path: str | None, image_base64: str | None) -> np.ndarray | None:
        if image_base64:
            try:
                payload = image_base64.split(",", 1)[-1]
                raw = base64.b64decode(payload)
                buffer = np.frombuffer(raw, dtype=np.uint8)
                frame = cv2.imdecode(buffer, cv2.IMREAD_COLOR)
                if frame is not None:
                    return frame
            except Exception:
                pass

        if image_path:
            frame = cv2.imread(image_path)
            if frame is None:
                raise FileNotFoundError(f"image not found or unreadable: {image_path}")
            return frame

        return None

    def _detect_faces(self, frame: np.ndarray, min_confidence: float, max_faces: int) -> list[DetectedFace]:
        frame_height, frame_width = frame.shape[:2]
        if frame_width == 0 or frame_height == 0:
            return []

        try:
            detected = self.app.get(frame)
        except Exception:
            return []

        if not detected:
            return []

        detected = sorted(detected, key=lambda f: float(f.det_score), reverse=True)

        faces = []
        for face_obj in detected:
            score = float(face_obj.det_score)
            if score < min_confidence:
                continue

            x1, y1, x2, y2 = face_obj.bbox.astype(int)
            x1 = max(0, x1)
            y1 = max(0, y1)
            x2 = min(frame_width, x2)
            y2 = min(frame_height, y2)
            width = x2 - x1
            height = y2 - y1
            if width <= 0 or height <= 0:
                continue

            bbox = BoundingBox(
                x=round(x1 / frame_width, 6),
                y=round(y1 / frame_height, 6),
                width=round(width / frame_width, 6),
                height=round(height / frame_height, 6),
            )

            embedding = self._extract_embedding(face_obj)
            quality = self._estimate_quality(frame, x1, y1, width, height, frame_width, frame_height, score)

            faces.append(
                DetectedFace(
                    bbox=bbox,
                    confidence=round(score, 6),
                    quality_score=quality,
                    embedding=embedding,
                )
            )
            if len(faces) >= max_faces:
                break

        return faces

    def _extract_embedding(self, face_obj) -> list[float]:
        emb = face_obj.normed_embedding
        if emb is None:
            return [0.0] * self.embedding_size
        result = emb.tolist()
        if len(result) < self.embedding_size:
            result.extend([0.0] * (self.embedding_size - len(result)))
        elif len(result) > self.embedding_size:
            result = result[: self.embedding_size]
        return [round(float(v), 6) for v in result]

    def _estimate_quality(
        self,
        frame: np.ndarray,
        x: int,
        y: int,
        width: int,
        height: int,
        frame_width: int,
        frame_height: int,
        score: float,
    ) -> float:
        crop = cv2.cvtColor(frame[y : y + height, x : x + width], cv2.COLOR_BGR2GRAY)
        if crop.size == 0:
            return round(score * 0.45, 6)
        area_ratio = (width * height) / float(frame_width * frame_height)
        sharpness = cv2.Laplacian(crop, cv2.CV_64F).var()
        normalized_area = min(max(area_ratio / 0.12, 0.0), 1.0)
        normalized_sharpness = min(max(sharpness / 600.0, 0.0), 1.0)
        normalized_score = min(max(score, 0.0), 1.0)
        return round((normalized_score * 0.45) + (normalized_area * 0.2) + (normalized_sharpness * 0.35), 6)

    def _init_insightface(self):
        from insightface.app import FaceAnalysis

        providers = self._get_providers()
        root = os.environ.get("INSIGHTFACE_HOME", self.settings.model_cache_dir)
        os.makedirs(root, exist_ok=True)

        # 打印使用的 provider（方便调试）
        import logging
        logger = logging.getLogger(__name__)
        logger.info(f"InsightFace using providers: {providers}")

        app = FaceAnalysis(
            name=self.settings.model_pack,
            root=root,
            providers=providers,
        )
        det_size = self.settings.det_size
        app.prepare(ctx_id=0, det_size=(det_size, det_size))
        return app

    def _get_providers(self) -> list[str]:
        import platform

        device = self.settings.onnx_device.lower()

        # macOS Apple Silicon - 优先使用 CoreML
        if platform.system() == "Darwin" and platform.machine() == "arm64":
            # 检查 CoreML 是否可用
            try:
                import onnxruntime as ort
                available_providers = ort.get_available_providers()
                if "CoreMLExecutionProvider" in available_providers:
                    return ["CoreMLExecutionProvider", "CPUExecutionProvider"]
            except Exception:
                pass
            return ["CPUExecutionProvider"]

        if device == "cuda":
            return ["CUDAExecutionProvider", "CPUExecutionProvider"]
        return ["CPUExecutionProvider"]
