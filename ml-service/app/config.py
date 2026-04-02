from functools import lru_cache
from pathlib import Path

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="RELIVE_ML_", extra="ignore")

    api_prefix: str = "/api/v1"
    onnx_device: str = "cpu"
    embedding_size: int = 512
    default_confidence: float = 0.98
    detector_score_threshold: float = 0.9
    detector_nms_threshold: float = 0.3
    detector_top_k: int = 5000
    model_cache_dir: str = str(Path("~/.cache/relive-ml/models").expanduser())
    yunet_model_name: str = "face_detection_yunet_2023mar.onnx"
    yunet_model_url: str = "https://github.com/opencv/opencv_zoo/raw/main/models/face_detection_yunet/face_detection_yunet_2023mar.onnx"


@lru_cache
def get_settings() -> Settings:
    return Settings()
