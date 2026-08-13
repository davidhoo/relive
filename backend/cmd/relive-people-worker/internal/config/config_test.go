package config

import (
	"testing"
)

func TestDefaultFaceDetectionMinConfidence(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.GetFaceDetectionMinConfidence(); got != 0.65 {
		t.Errorf("default face_detection_min_confidence = %v, want 0.65", got)
	}
}

func TestFaceDetectionMinConfidenceOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ML.FaceDetectionMinConfidence = 0.8
	if got := cfg.GetFaceDetectionMinConfidence(); got != 0.8 {
		t.Errorf("overridden face_detection_min_confidence = %v, want 0.8", got)
	}
}

func TestFaceDetectionMinConfidenceOutOfRangeRejected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.APIKey = "k"
	cfg.Server.Endpoint = "http://x"
	cfg.ML.Endpoint = "http://ml"
	cfg.ML.FaceDetectionMinConfidence = 1.5
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for face_detection_min_confidence > 1")
	}
	cfg.ML.FaceDetectionMinConfidence = -0.1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for face_detection_min_confidence < 0")
	}
	// 合法值通过。
	cfg.ML.FaceDetectionMinConfidence = 0.65
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error for valid confidence: %v", err)
	}
}
