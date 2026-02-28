"""
应用配置管理
"""
from pydantic_settings import BaseSettings
from pathlib import Path


class Settings(BaseSettings):
    """应用配置"""

    # 应用基础配置
    app_name: str = "Relive"
    app_version: str = "0.1.0"
    debug: bool = True

    # API 配置
    api_host: str = "0.0.0.0"
    api_port: int = 8000

    # 数据库配置
    database_url: str = "sqlite:///./relive.db"

    # NAS 配置
    nas_photo_path: str = "/path/to/nas/photos"
    supported_formats: list[str] = [".jpg", ".jpeg", ".png", ".heic", ".raw"]

    # Qwen API 配置
    qwen_api_key: str = ""
    qwen_api_url: str = "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
    qwen_model: str = "qwen-vl-max"

    # 分析配置
    description_min_length: int = 80
    description_max_length: int = 200
    caption_min_length: int = 8
    caption_max_length: int = 30

    # 评分阈值
    min_art_score: int = 60
    min_memory_score: int = 70

    # 任务配置
    scan_interval: int = 3600  # 扫描间隔（秒）
    batch_size: int = 10  # 批量处理照片数

    class Config:
        env_file = ".env"
        env_file_encoding = "utf-8"


# 全局配置实例
settings = Settings()
