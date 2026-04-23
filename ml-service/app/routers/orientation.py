"""Orientation detection router."""

from fastapi import APIRouter

from app.models.orientation import OrientationDetector
from app.schemas import DetectOrientationRequest, DetectOrientationResponse

router = APIRouter()
detector = OrientationDetector()


@router.post("/detect-orientation", response_model=DetectOrientationResponse)
def detect_orientation(request: DetectOrientationRequest) -> DetectOrientationResponse:
    """Detect the correct orientation of a photo.

    Returns the suggested clockwise rotation angle (0, 90, 180, or 270 degrees)
    to make the photo upright, along with a confidence score.
    """
    return detector.detect(
        image_path=request.image_path,
        image_base64=request.image_base64,
    )
