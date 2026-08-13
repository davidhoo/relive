from fastapi import APIRouter

from app.models.face import FaceDetector
from app.models.face_verifier import FaceVerifier
from app.schemas import (
    DetectFacesRequest,
    DetectFacesResponse,
    ScoreKnownFacesRequest,
    ScoreKnownFacesResponse,
    VerifyKnownFaceCropsRequest,
    VerifyKnownFaceCropsResponse,
)

router = APIRouter()
detector = FaceDetector()
verifier = FaceVerifier()


@router.post("/detect-faces", response_model=DetectFacesResponse)
def detect_faces(request: DetectFacesRequest) -> DetectFacesResponse:
    return detector.detect(
        image_path=request.image_path,
        image_base64=request.image_base64,
        min_confidence=request.min_confidence,
        max_faces=request.max_faces,
    )


@router.post("/score-known-faces", response_model=ScoreKnownFacesResponse)
def score_known_faces(request: ScoreKnownFacesRequest) -> ScoreKnownFacesResponse:
    """对历史重评分 worker 内部开放的“已知框评分”接口。

    只接收 base64 展示图（已 EXIF/手动旋转校正）+ 一组目标归一化 BBox，在同一张图上
    运行 InsightFace 检测，复用 _build_evidence 生成证据，再与目标框做一对一最高 IoU 匹配。
    详见 FaceDetector.score_known_faces。
    """
    return detector.score_known_faces(image_base64=request.image_base64, targets=request.targets)


@router.post("/verify-known-face-crops", response_model=VerifyKnownFaceCropsResponse)
def verify_known_face_crops(request: VerifyKnownFaceCropsRequest) -> VerifyKnownFaceCropsResponse:
    """v2 独立复核接口（仅历史复核 worker 内部调用，不暴露给浏览器）。

    按 Face 传输上下文裁剪 Base64、原图人脸框宽高与主检测分，用 YuNet 独立验证器判定
    face/no_face/uncertain/error，并在原图人脸框上计算质量特征。任何单条错误只影响对应 item。
    """
    return verifier.verify_crops(targets=request.targets)
