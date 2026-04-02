from pydantic import BaseModel, Field, model_validator


class BoundingBox(BaseModel):
    x: float = Field(ge=0, le=1)
    y: float = Field(ge=0, le=1)
    width: float = Field(gt=0, le=1)
    height: float = Field(gt=0, le=1)


class DetectedFace(BaseModel):
    bbox: BoundingBox
    confidence: float = Field(ge=0, le=1)
    quality_score: float = Field(ge=0, le=1)
    embedding: list[float]


class DetectFacesRequest(BaseModel):
    image_path: str | None = None
    image_base64: str | None = None
    min_confidence: float = Field(default=0.5, ge=0, le=1)
    max_faces: int = Field(default=20, ge=1, le=100)

    @model_validator(mode="after")
    def validate_source(self) -> "DetectFacesRequest":
        if not self.image_path and not self.image_base64:
            raise ValueError("image_path or image_base64 is required")
        return self


class DetectFacesResponse(BaseModel):
    faces: list[DetectedFace]
    processing_time_ms: int = Field(ge=0)


class HealthResponse(BaseModel):
    status: str
