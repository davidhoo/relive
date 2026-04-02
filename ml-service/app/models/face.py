import hashlib
import time

from app.config import get_settings
from app.schemas import BoundingBox, DetectFacesResponse, DetectedFace


class FaceDetector:
    def __init__(self) -> None:
        settings = get_settings()
        self.embedding_size = settings.embedding_size
        self.default_confidence = settings.default_confidence

    def detect(
        self,
        *,
        image_path: str | None = None,
        image_base64: str | None = None,
        min_confidence: float = 0.5,
        max_faces: int = 20,
    ) -> DetectFacesResponse:
        started_at = time.perf_counter()
        source = image_path or image_base64 or ""
        confidence = self.default_confidence

        faces = []
        if source and confidence >= min_confidence and max_faces > 0:
            faces.append(self._build_face(source, confidence))

        elapsed_ms = int((time.perf_counter() - started_at) * 1000)
        return DetectFacesResponse(
            faces=faces[:max_faces],
            processing_time_ms=max(elapsed_ms, 0),
        )

    def _build_face(self, source: str, confidence: float) -> DetectedFace:
        digest = hashlib.sha256(source.encode("utf-8")).digest()

        bbox = BoundingBox(
            x=0.08 + (digest[0] / 255.0) * 0.08,
            y=0.06 + (digest[1] / 255.0) * 0.08,
            width=0.18 + (digest[2] / 255.0) * 0.08,
            height=0.20 + (digest[3] / 255.0) * 0.08,
        )
        quality_score = 0.7 + (digest[4] / 255.0) * 0.25
        embedding = [
            round(((digest[index % len(digest)] / 255.0) * 2.0) - 1.0, 6)
            for index in range(self.embedding_size)
        ]

        return DetectedFace(
            bbox=bbox,
            confidence=confidence,
            quality_score=quality_score,
            embedding=embedding,
        )
