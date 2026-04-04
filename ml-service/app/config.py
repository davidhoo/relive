from functools import lru_cache
from pathlib import Path

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="RELIVE_ML_", extra="ignore")

    api_prefix: str = "/api/v1"
    onnx_device: str = "cpu"
    embedding_size: int = 512
    default_confidence: float = 0.98
    model_pack: str = "buffalo_sc"
    model_cache_dir: str = str(Path("~/.insightface").expanduser())
    det_size: int = 640


@lru_cache
def get_settings() -> Settings:
    return Settings()
