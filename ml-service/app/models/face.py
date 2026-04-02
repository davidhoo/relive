import base64
import time
from pathlib import Path
from urllib.request import urlopen

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
        self.model_path = self._ensure_yunet_model()
        self.detector = None
        self.detector_input_size: tuple[int, int] | None = None

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
        gray = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
        frame_height, frame_width = gray.shape[:2]
        if frame_width == 0 or frame_height == 0:
            return []

        detector = self._get_detector(frame_width, frame_height)
        try:
            _, detected = detector.detect(frame)
        except cv2.error:
            return []

        if detected is None:
            return []

        rows = sorted(detected.tolist(), key=lambda row: float(row[-1]), reverse=True)
        faces = []
        for row in rows:
            score = float(row[-1])
            if score < min_confidence:
                continue

            x, y, width, height = row[:4]
            x = max(0, int(round(x)))
            y = max(0, int(round(y)))
            width = max(1, int(round(width)))
            height = max(1, int(round(height)))

            crop = gray[y:y + height, x:x + width]
            if crop.size == 0:
                continue

            bbox = BoundingBox(
                x=round(x / frame_width, 6),
                y=round(y / frame_height, 6),
                width=round(width / frame_width, 6),
                height=round(height / frame_height, 6),
            )
            faces.append(
                DetectedFace(
                    bbox=bbox,
                    confidence=round(score, 6),
                    quality_score=self._estimate_quality(crop, width, height, frame_width, frame_height, score),
                    embedding=self._build_embedding(crop),
                )
            )
            if len(faces) >= max_faces:
                break

        return faces

    def _estimate_quality(
        self,
        crop: np.ndarray,
        width: int,
        height: int,
        frame_width: int,
        frame_height: int,
        score: float,
    ) -> float:
        area_ratio = (width * height) / float(frame_width * frame_height)
        sharpness = cv2.Laplacian(crop, cv2.CV_64F).var()
        normalized_area = min(max(area_ratio / 0.12, 0.0), 1.0)
        normalized_sharpness = min(max(sharpness / 600.0, 0.0), 1.0)
        normalized_score = min(max(score, 0.0), 1.0)
        return round((normalized_score * 0.45) + (normalized_area * 0.2) + (normalized_sharpness * 0.35), 6)

    def _build_embedding(self, crop: np.ndarray) -> list[float]:
        resized = cv2.resize(crop, (32, 16), interpolation=cv2.INTER_AREA)
        normalized = (resized.astype(np.float32) / 127.5) - 1.0
        flattened = normalized.flatten()
        if flattened.size < self.embedding_size:
            flattened = np.pad(flattened, (0, self.embedding_size - flattened.size))
        elif flattened.size > self.embedding_size:
            flattened = flattened[:self.embedding_size]
        return [round(float(value), 6) for value in flattened]

    def _ensure_yunet_model(self) -> str:
        cache_dir = Path(self.settings.model_cache_dir).expanduser()
        cache_dir.mkdir(parents=True, exist_ok=True)
        model_path = cache_dir / self.settings.yunet_model_name
        if model_path.exists():
            return str(model_path)

        tmp_path = model_path.with_suffix(model_path.suffix + ".tmp")
        with urlopen(self.settings.yunet_model_url) as response:
            tmp_path.write_bytes(response.read())
        tmp_path.replace(model_path)
        return str(model_path)

    def _get_detector(self, frame_width: int, frame_height: int):
        input_size = (frame_width, frame_height)
        if self.detector is None:
            self.detector = cv2.FaceDetectorYN.create(
                self.model_path,
                "",
                input_size,
                self.settings.detector_score_threshold,
                self.settings.detector_nms_threshold,
                self.settings.detector_top_k,
            )
            self.detector_input_size = input_size
            return self.detector

        if self.detector_input_size != input_size:
            self.detector.setInputSize(input_size)
            self.detector_input_size = input_size
        return self.detector
