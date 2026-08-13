from fastapi import APIRouter, Response

from app.routers import faces
from app.schemas import HealthResponse

router = APIRouter()


@router.get("/health", response_model=HealthResponse)
def health(response: Response) -> HealthResponse:
    """YuNet 验证器不可用时返回 503 + degraded，使后端阻断 v2 run、Docker 标记 unhealthy。"""
    verifier = faces.verifier
    if not verifier.available:
        response.status_code = 503
        return HealthResponse(
            status="degraded",
            verifier_available=False,
            verifier_name=verifier.verifier_name,
            verifier_version=verifier.verifier_version,
        )
    return HealthResponse(
        status="ok",
        verifier_available=True,
        verifier_name=verifier.verifier_name,
        verifier_version=verifier.verifier_version,
    )
