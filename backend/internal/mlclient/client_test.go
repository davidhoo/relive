package mlclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientDetectFacesBuildsRequest(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotRequest DetectFacesRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(DetectFacesResponse{
			Faces: []DetectedFace{
				{
					BBox:         BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
					Confidence:   0.95,
					QualityScore: 0.91,
					Embedding:    []float32{0.1, 0.2},
				},
			},
			ProcessingTimeMS: 12,
		})
	}))
	defer server.Close()

	client := New(server.URL, 2*time.Second)
	_, err := client.DetectFaces(context.Background(), DetectFacesRequest{
		ImagePath:     "/photos/family.jpg",
		MinConfidence: 0.6,
		MaxFaces:      4,
	})
	if err != nil {
		t.Fatalf("detect faces: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST request, got %s", gotMethod)
	}
	if gotPath != "/api/v1/detect-faces" {
		t.Fatalf("expected /api/v1/detect-faces path, got %s", gotPath)
	}
	if gotRequest.ImagePath != "/photos/family.jpg" {
		t.Fatalf("expected image_path to be forwarded")
	}
	if gotRequest.MinConfidence != 0.6 {
		t.Fatalf("expected min_confidence to be forwarded")
	}
	if gotRequest.MaxFaces != 4 {
		t.Fatalf("expected max_faces to be forwarded")
	}
}

func TestClientDetectFacesTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL, 10*time.Millisecond)
	_, err := client.DetectFaces(context.Background(), DetectFacesRequest{ImagePath: "/photos/family.jpg"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClientDetectFacesDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(DetectFacesResponse{
			Faces: []DetectedFace{
				{
					BBox:         BoundingBox{X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4},
					Confidence:   0.99,
					QualityScore: 0.88,
					Embedding:    []float32{0.11, 0.22, 0.33},
				},
			},
			ProcessingTimeMS: 34,
		})
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	resp, err := client.DetectFaces(context.Background(), DetectFacesRequest{ImagePath: "/photos/family.jpg"})
	if err != nil {
		t.Fatalf("detect faces: %v", err)
	}

	if resp.ProcessingTimeMS != 34 {
		t.Fatalf("expected processing_time_ms to decode, got %d", resp.ProcessingTimeMS)
	}
	if len(resp.Faces) != 1 {
		t.Fatalf("expected one face, got %d", len(resp.Faces))
	}
	if resp.Faces[0].QualityScore != 0.88 {
		t.Fatalf("expected quality score 0.88, got %f", resp.Faces[0].QualityScore)
	}
	if len(resp.Faces[0].Embedding) != 3 {
		t.Fatalf("expected embedding length 3, got %d", len(resp.Faces[0].Embedding))
	}
}
