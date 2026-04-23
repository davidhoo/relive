from fastapi import FastAPI

from app.config import get_settings
from app.routers.faces import router as faces_router
from app.routers.health import router as health_router

settings = get_settings()

app = FastAPI(title="Relive ML Service", version="0.1.0")
app.include_router(health_router, prefix=settings.api_prefix)
app.include_router(faces_router, prefix=settings.api_prefix)
