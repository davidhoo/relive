"""Orientation detection model for photos.

Automatically selects the best available backend:
- ONNX Runtime (lighter, recommended for NAS)
- PyTorch CGD model (fallback, requires more memory)
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


class OrientationDetector:
    """Detects the correct orientation of photos.

    Uses ONNX Runtime by default (lighter weight).
    Falls back to PyTorch CGD model if ONNX model not available.
    """

    def __init__(self) -> None:
        self.settings = get_settings()
        self._backend = None  # 'onnx' or 'pytorch'
        self._model = None
        self._session = None

    def _init_backend(self):
        """Initialize the appropriate backend."""
        if self._backend is not None:
            return

        # Try ONNX first (lighter weight)
        onnx_path = self._get_onnx_model_path()
        if onnx_path.exists():
            self._backend = 'onnx'
            self._init_onnx(onnx_path)
            return

        # Fall back to PyTorch
        try:
            from app.models.orientation_cgd import CGDAngleEstimation
            self._backend = 'pytorch'
            self._model = CGDAngleEstimation.from_pretrained(
                "maxwoe/image-rotation-angle-estimation"
            )
            self._model.eval()
        except ImportError:
            raise RuntimeError(
                "Neither ONNX model nor PyTorch available. "
                "Please either: "
                "1. Run 'python export_to_onnx.py' to create ONNX model, or "
                "2. Install PyTorch with 'pip install torch torchvision timm pytorch-lightning'"
            )

    def _get_onnx_model_path(self) -> Path:
        """Get the path to the ONNX model file."""
        # Check multiple locations
        possible_paths = [
            Path("/app/onnx_models/orientation_model.onnx"),  # Docker build location
            Path("/app/models/orientation_model.onnx"),       # Volume mount location
            Path(__file__).parent.parent.parent / "models" / "orientation_model.onnx",  # Local dev
        ]
        for p in possible_paths:
            if p.exists():
                return p

        # Default to volume mount path (will trigger download/error if not exists)
        return Path("/app/models/orientation_model.onnx")

    def _init_onnx(self, model_path: Path):
        """Initialize ONNX Runtime session."""
        import onnxruntime as ort

        sess_options = ort.SessionOptions()
        sess_options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL

        providers = ['CPUExecutionProvider']
        if 'CoreMLExecutionProvider' in ort.get_available_providers():
            providers.insert(0, 'CoreMLExecutionProvider')

        self._session = ort.InferenceSession(
            str(model_path),
            sess_options,
            providers=providers
        )

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
            self._init_backend()

            if self._backend == 'onnx':
                return self._detect_onnx(image_path, image_base64, started_at)
            else:
                return self._detect_pytorch(image_path, image_base64, started_at)

        except Exception as e:
            elapsed_ms = int((time.perf_counter() - started_at) * 1000)
            return DetectOrientationResponse(
                rotation=0,
                confidence=0.3,
                processing_time_ms=max(elapsed_ms, 0),
            )

    def _detect_onnx(self, image_path, image_base64, started_at) -> DetectOrientationResponse:
        """Detect using ONNX Runtime."""
        # Load and preprocess
        image_tensor = self._load_and_preprocess(image_path, image_base64)
        if image_tensor is None:
            return DetectOrientationResponse(rotation=0, confidence=0.0, processing_time_ms=0)

        # Run inference
        input_name = self._session.get_inputs()[0].name
        output_name = self._session.get_outputs()[0].name
        distribution = self._session.run([output_name], {input_name: image_tensor})[0]

        # Convert to angle and rotation
        angle = self._distribution_to_angle(distribution[0])
        rotation, confidence = self._angle_to_rotation(angle)

        elapsed_ms = int((time.perf_counter() - started_at) * 1000)
        return DetectOrientationResponse(
            rotation=rotation,
            confidence=confidence,
            processing_time_ms=max(elapsed_ms, 0),
        )

    def _detect_pytorch(self, image_path, image_base64, started_at) -> DetectOrientationResponse:
        """Detect using PyTorch CGD model."""
        # Load image as PIL Image
        pil_image = self._load_image_pil(image_path, image_base64)
        if pil_image is None:
            return DetectOrientationResponse(rotation=0, confidence=0.0, processing_time_ms=0)

        # Predict angle
        import torch
        with torch.no_grad():
            predicted_angle = self._model.predict_angle(pil_image)

        rotation, confidence = self._angle_to_rotation(predicted_angle)

        elapsed_ms = int((time.perf_counter() - started_at) * 1000)
        return DetectOrientationResponse(
            rotation=rotation,
            confidence=confidence,
            processing_time_ms=max(elapsed_ms, 0),
        )

    def _load_and_preprocess(self, image_path, image_base64) -> np.ndarray | None:
        """Load and preprocess image for ONNX model."""
        image = None

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

    def _load_image_pil(self, image_path, image_base64) -> Image.Image | None:
        """Load image as PIL Image."""
        if image_base64:
            try:
                payload = image_base64.split(",", 1)[-1]
                raw = base64.b64decode(payload)
                buffer = np.frombuffer(raw, dtype=np.uint8)
                frame = cv2.imdecode(buffer, cv2.IMREAD_COLOR)
                if frame is not None:
                    frame_rgb = cv2.cvtColor(frame, cv2.COLOR_BGR2RGB)
                    return Image.fromarray(frame_rgb)
            except Exception:
                pass

        if image_path:
            try:
                return Image.open(image_path).convert("RGB")
            except Exception:
                raise FileNotFoundError(f"image not found or unreadable: {image_path}")

        return None

    def _distribution_to_angle(self, distribution: np.ndarray) -> float:
        """Convert 360-bin probability distribution to angle."""
        peak_idx = np.argmax(distribution)
        bin_size = 360.0 / len(distribution)
        angle = peak_idx * bin_size

        # Parabolic interpolation for sub-bin accuracy
        if 0 < peak_idx < len(distribution) - 1:
            y1 = distribution[peak_idx - 1]
            y2 = distribution[peak_idx]
            y3 = distribution[peak_idx + 1]

            a = 0.5 * (y1 - 2 * y2 + y3)
            b = 0.5 * (y3 - y1)

            if abs(a) > 1e-8:
                offset = -b / (2 * a)
                offset = np.clip(offset, -0.5, 0.5)
                angle += offset * bin_size

        return angle % 360.0

    def _angle_to_rotation(self, angle: float) -> tuple[int, float]:
        """Convert continuous angle to discrete rotation and confidence.

        The CGD model predicts how much the image has been rotated (clockwise).
        To correct the image, we need to rotate in the opposite direction.
        Our API returns the clockwise rotation needed to correct.
        """
        angle = angle % 360.0
        nearest_90 = round(angle / 90.0) * 90

        # Calculate confidence
        distance = abs(angle - nearest_90)
        if distance > 45:
            distance = 90 - distance
        confidence = 1.0 - (distance / 90.0)
        confidence = max(0.5, min(1.0, confidence))

        # If image was rotated X°, we need (360-X)° to correct
        rotation_needed = (360 - int(nearest_90 % 360)) % 360

        return (rotation_needed, round(confidence, 4))
