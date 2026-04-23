"""Orientation detection model for photos."""

import base64
import time

import cv2
import numpy as np

from app.config import get_settings
from app.schemas import DetectOrientationResponse


class OrientationDetector:
    """Detects the correct orientation of photos.

    Returns the suggested rotation angle (0, 90, 180, 270) to make the photo upright.
    """

    def __init__(self) -> None:
        self.settings = get_settings()

    def detect(
        self,
        *,
        image_path: str | None = None,
        image_base64: str | None = None,
    ) -> DetectOrientationResponse:
        """Detect the correct orientation of an image.

        Args:
            image_path: Path to the image file.
            image_base64: Base64 encoded image data.

        Returns:
            DetectOrientationResponse with suggested rotation and confidence.
        """
        started_at = time.perf_counter()

        frame = self._load_image(image_path=image_path, image_base64=image_base64)
        if frame is None:
            return DetectOrientationResponse(rotation=0, confidence=0.0, processing_time_ms=0)

        rotation, confidence = self._detect_orientation(frame)

        elapsed_ms = int((time.perf_counter() - started_at) * 1000)
        return DetectOrientationResponse(
            rotation=rotation,
            confidence=confidence,
            processing_time_ms=max(elapsed_ms, 0),
        )

    def _load_image(self, *, image_path: str | None, image_base64: str | None) -> np.ndarray | None:
        """Load image from path or base64 data."""
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

    def _detect_orientation(self, frame: np.ndarray) -> tuple[int, float]:
        """Detect the correct orientation of the image.

        Uses a heuristic approach based on face detection:
        1. Try detecting faces in all 4 orientations
        2. The orientation with the best face detection results is likely correct

        Returns:
            Tuple of (rotation_angle, confidence)
            rotation_angle: 0, 90, 180, or 270 (clockwise rotation needed)
        """
        # Import here to avoid circular dependency and allow lazy loading
        from insightface.app import FaceAnalysis

        # Initialize face detector (reuse if possible)
        providers = self._get_providers()
        root = self.settings.model_cache_dir

        try:
            app = FaceAnalysis(
                name=self.settings.model_pack,
                root=root,
                providers=providers,
            )
            app.prepare(ctx_id=0, det_size=(640, 640))
        except Exception:
            # If face detection fails, return 0 rotation with low confidence
            return (0, 0.5)

        # Test all 4 orientations
        orientations = [
            (0, frame),  # Original
            (90, self._rotate_90_clockwise(frame)),  # Rotate 90 CW
            (180, self._rotate_180(frame)),  # Rotate 180
            (270, self._rotate_270_clockwise(frame)),  # Rotate 270 CW
        ]

        best_rotation = 0
        best_score = 0.0
        best_count = 0

        for rotation, rotated_frame in orientations:
            try:
                faces = app.get(rotated_frame)
                if not faces:
                    continue

                # Calculate score based on number of faces and their confidence
                count = len(faces)
                avg_confidence = sum(float(f.det_score) for f in faces) / count if count > 0 else 0

                # Score = count * avg_confidence, with bonus for more faces
                score = count * avg_confidence * (1 + 0.1 * (count - 1))

                if score > best_score:
                    best_score = score
                    best_rotation = rotation
                    best_count = count

            except Exception:
                continue

        # Calculate confidence based on detection quality
        if best_count == 0:
            # No faces detected in any orientation
            confidence = 0.5
        else:
            # Higher confidence if more faces detected with higher scores
            confidence = min(best_score / 2.0, 1.0)

        return (best_rotation, round(confidence, 4))

    def _rotate_90_clockwise(self, frame: np.ndarray) -> np.ndarray:
        """Rotate image 90 degrees clockwise."""
        return cv2.rotate(frame, cv2.ROTATE_90_CLOCKWISE)

    def _rotate_180(self, frame: np.ndarray) -> np.ndarray:
        """Rotate image 180 degrees."""
        return cv2.rotate(frame, cv2.ROTATE_180)

    def _rotate_270_clockwise(self, frame: np.ndarray) -> np.ndarray:
        """Rotate image 270 degrees clockwise (90 counter-clockwise)."""
        return cv2.rotate(frame, cv2.ROTATE_90_COUNTERCLOCKWISE)

    def _get_providers(self) -> list[str]:
        """Get ONNX providers based on platform and settings."""
        import platform

        device = self.settings.onnx_device.lower()

        # macOS Apple Silicon - prefer CoreML
        if platform.system() == "Darwin" and platform.machine() == "arm64":
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
