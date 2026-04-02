from fastapi import APIRouter

from app.models.face import FaceDetector
from app.schemas import DetectFacesRequest, DetectFacesResponse

router = APIRouter()
detector = FaceDetector()


@router.post("/detect-faces", response_model=DetectFacesResponse)
def detect_faces(request: DetectFacesRequest) -> DetectFacesResponse:
    return detector.detect(
        image_path=request.image_path,
        image_base64=request.image_base64,
        min_confidence=request.min_confidence,
        max_faces=request.max_faces,
    )
