from functools import lru_cache

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="RELIVE_ML_", extra="ignore")

    api_prefix: str = "/api/v1"
    onnx_device: str = "cpu"
    embedding_size: int = 512
    default_confidence: float = 0.98


@lru_cache
def get_settings() -> Settings:
    return Settings()
