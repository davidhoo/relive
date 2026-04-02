from fastapi import APIRouter

from app.schemas import HealthResponse

router = APIRouter()


@router.get("/health", response_model=HealthResponse)
def health() -> HealthResponse:
    return HealthResponse(status="ok")
