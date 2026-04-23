"""Orientation detection model using ONNX Runtime.

This module provides a lightweight ONNX-based implementation that doesn't require PyTorch.
Memory usage: ~200MB (model) + ~50MB (runtime) vs ~2GB for PyTorch version.
"""

import base64
import math
import time
from pathlib import Path

import cv2
import numpy as np
from PIL import Image

from app.config import get_settings
from app.schemas import DetectOrientationResponse


class OrientationDetectorONNX:
    """ONNX-based orientation detection for photos.

    Uses a pretrained CGD model exported to ONNX format.
    Much lighter than PyTorch version - suitable for NAS devices.
    """

    def __init__(self, model_path: str | None = None) -> None:
        self.settings = get_settings()
        self._session = None
        self._model_path = model_path

    def _get_model_path(self) -> Path:
        """Get the path to the ONNX model file."""
        if self._model_path:
            return Path(self._model_path)

        # Default path: models/orientation_model.onnx
        model_dir = Path(self.settings.model_cache_dir) if hasattr(self.settings, 'model_cache_dir') else Path("/app/models")
        return model_dir / "orientation_model.onnx"

    def _get_session(self):
        """Lazy load ONNX Runtime session."""
        if self._session is None:
            import onnxruntime as ort

            model_path = self._get_model_path()

            # Check if model exists
            if not model_path.exists():
                raise FileNotFoundError(
                    f"ONNX model not found at {model_path}. "
                    f"Please run: python export_to_onnx.py -o {model_path}"
                )

            # Create inference session with optimizations
            sess_options = ort.SessionOptions()
            sess_options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL

            # Use available providers
            providers = ['CPUExecutionProvider']
            if 'CoreMLExecutionProvider' in ort.get_available_providers():
                providers.insert(0, 'CoreMLExecutionProvider')

            self._session = ort.InferenceSession(
                str(model_path),
                sess_options,
                providers=providers
            )

        return self._session

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

        try:
            # Load and preprocess image
            image_tensor = self._load_and_preprocess(
                image_path=image_path,
                image_base64=image_base64
            )
            if image_tensor is None:
                return DetectOrientationResponse(rotation=0, confidence=0.0, processing_time_ms=0)

            # Run inference
            session = self._get_session()
            input_name = session.get_inputs()[0].name
            output_name = session.get_outputs()[0].name

            distribution = session.run([output_name], {input_name: image_tensor})[0]

            # Convert distribution to angle
            angle = self._distribution_to_angle(distribution[0])

            # Convert to discrete rotation
            rotation, confidence = self._angle_to_rotation(angle)

            elapsed_ms = int((time.perf_counter() - started_at) * 1000)
            return DetectOrientationResponse(
                rotation=rotation,
                confidence=confidence,
                processing_time_ms=max(elapsed_ms, 0),
            )

        except Exception as e:
            elapsed_ms = int((time.perf_counter() - started_at) * 1000)
            return DetectOrientationResponse(
                rotation=0,
                confidence=0.3,
                processing_time_ms=max(elapsed_ms, 0),
            )

    def _load_and_preprocess(
        self,
        *,
        image_path: str | None,
        image_base64: str | None,
    ) -> np.ndarray | None:
        """Load image and preprocess for model input."""
        # Load image
        if image_base64:
            try:
                payload = image_base64.split(",", 1)[-1]
                raw = base64.b64decode(payload)
                buffer = np.frombuffer(raw, dtype=np.uint8)
                image = cv2.imdecode(buffer, cv2.IMREAD_COLOR)
                if image is not None:
                    image = cv2.cvtColor(image, cv2.COLOR_BGR2RGB)
            except Exception:
                return None
        elif image_path:
            try:
                image = cv2.imread(image_path)
                if image is None:
                    raise FileNotFoundError(f"image not found: {image_path}")
                image = cv2.cvtColor(image, cv2.COLOR_BGR2RGB)
            except Exception:
                raise FileNotFoundError(f"image not found or unreadable: {image_path}")
        else:
            return None

        # Resize to 224x224
        image = cv2.resize(image, (224, 224))

        # Normalize (ImageNet mean/std)
        image = image.astype(np.float32) / 255.0
        mean = np.array([0.485, 0.456, 0.406])
        std = np.array([0.229, 0.224, 0.225])
        image = (image - mean) / std

        # HWC -> CHW -> NCHW
        image = np.transpose(image, (2, 0, 1))
        image = np.expand_dims(image, 0)

        return image.astype(np.float32)

    def _distribution_to_angle(self, distribution: np.ndarray) -> float:
        """Convert 360-bin probability distribution to angle.

        Args:
            distribution: Probability distribution over 360 bins [360]

        Returns:
            Predicted angle in degrees [0, 360)
        """
        # Use argmax to find the most likely angle
        peak_idx = np.argmax(distribution)
        bin_size = 360.0 / len(distribution)
        angle = peak_idx * bin_size

        # Optional: refine with parabolic interpolation for sub-bin accuracy
        if 0 < peak_idx < len(distribution) - 1:
            y1 = distribution[peak_idx - 1]
            y2 = distribution[peak_idx]
            y3 = distribution[peak_idx + 1]

            # Parabolic fit
            a = 0.5 * (y1 - 2 * y2 + y3)
            b = 0.5 * (y3 - y1)

            if abs(a) > 1e-8:
                offset = -b / (2 * a)
                offset = np.clip(offset, -0.5, 0.5)
                angle += offset * bin_size

        return angle % 360.0

    def _angle_to_rotation(self, angle: float) -> tuple[int, float]:
        """Convert continuous angle to discrete rotation and confidence.

        Args:
            angle: Predicted angle in degrees [0, 360)

        Returns:
            Tuple of (rotation, confidence)
        """
        # Normalize angle to [0, 360)
        angle = angle % 360.0

        # Find the nearest 90-degree multiple
        nearest_90 = round(angle / 90.0) * 90

        # Calculate confidence based on distance from 90° multiple
        distance = abs(angle - nearest_90)
        if distance > 45:
            distance = 90 - distance

        confidence = 1.0 - (distance / 90.0)
        confidence = max(0.5, min(1.0, confidence))

        # Convert: if image was rotated X°, we need (360-X)° to correct
        rotation_needed = (360 - int(nearest_90 % 360)) % 360

        return (rotation_needed, round(confidence, 4))


# Alias for backward compatibility
OrientationDetector = OrientationDetectorONNX
