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

func TestClientScoreKnownFacesBuildsRequest(t *testing.T) {
	var gotMethod, gotPath string
	var gotRequest ScoreKnownFacesRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		iou := 0.81
		_ = json.NewEncoder(w).Encode(ScoreKnownFacesResponse{
			Results: []ScoreKnownFaceResult{
				{
					FaceID:     42,
					Status:     "matched",
					MatchedIoU: &iou,
					Evidence: &FaceQualityEvidence{
						FaceValidityScore: 0.93,
						RuleVersion:       "v1",
					},
				},
			},
			RuleVersion:  "v1",
			ModelVersion: "insightface-buffalo-sc-v1",
		})
	}))
	defer server.Close()

	client := New(server.URL, 2*time.Second)
	resp, err := client.ScoreKnownFaces(context.Background(), ScoreKnownFacesRequest{
		ImageBase64: "base64data",
		Targets: []ScoreKnownFaceTarget{
			{FaceID: 42, BBox: BoundingBox{X: 0.12, Y: 0.20, Width: 0.18, Height: 0.25}},
		},
	})
	if err != nil {
		t.Fatalf("score known faces: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/api/v1/score-known-faces" {
		t.Fatalf("expected /api/v1/score-known-faces, got %s", gotPath)
	}
	if gotRequest.ImageBase64 != "base64data" {
		t.Fatalf("expected image_base64 forwarded, got %q", gotRequest.ImageBase64)
	}
	if len(gotRequest.Targets) != 1 || gotRequest.Targets[0].FaceID != 42 {
		t.Fatalf("expected one target face_id=42, got %+v", gotRequest.Targets)
	}

	if len(resp.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(resp.Results))
	}
	if resp.Results[0].Status != "matched" {
		t.Fatalf("expected matched, got %s", resp.Results[0].Status)
	}
	if resp.Results[0].MatchedIoU == nil || *resp.Results[0].MatchedIoU != 0.81 {
		t.Fatalf("expected matched_iou 0.81")
	}
	if resp.Results[0].Evidence == nil || resp.Results[0].Evidence.FaceValidityScore != 0.93 {
		t.Fatalf("expected evidence face_validity_score 0.93")
	}
	if resp.RuleVersion != "v1" {
		t.Fatalf("expected rule_version v1, got %s", resp.RuleVersion)
	}
}

func TestClientScoreKnownFacesTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL, 10*time.Millisecond)
	_, err := client.ScoreKnownFaces(context.Background(), ScoreKnownFacesRequest{
		ImageBase64: "x",
		Targets:     []ScoreKnownFaceTarget{{FaceID: 1, BBox: BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2}}},
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClientScoreKnownFacesNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	_, err := client.ScoreKnownFaces(context.Background(), ScoreKnownFacesRequest{
		ImageBase64: "x",
		Targets:     []ScoreKnownFaceTarget{{FaceID: 1, BBox: BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2}}},
	})
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestClientVerifyKnownFaceCropsBuildsRequest(t *testing.T) {
	var gotMethod, gotPath string
	var gotRequest VerifyKnownFaceCropsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(VerifyKnownFaceCropsResponse{
			Results: []VerifyKnownFaceCropResult{
				{
					FaceID:                7,
					VerificationStatus:    "no_face",
					VerifierScore:         0.12,
					VerifierName:          "yunet",
					VerifierVersion:       "opencv-yunet-2023mar",
					PrimaryDetectorScore:  0.4,
					FaceBoxWidthPx:        50,
					FaceBoxHeightPx:       50,
					EvidenceSchemaVersion: "independent_v2",
					ReasonCodes:           []string{"input_too_small"},
				},
			},
			RuleVersion:  "face_quality_v2",
			ModelVersion: "opencv-yunet-2023mar",
		})
	}))
	defer server.Close()

	client := New(server.URL, 2*time.Second)
	resp, err := client.VerifyKnownFaceCrops(context.Background(), VerifyKnownFaceCropsRequest{
		Targets: []VerifyKnownFaceCropTarget{
			{FaceID: 7, ContextCropBase64: "cropb64", FaceBoxWidthPx: 50, FaceBoxHeightPx: 50, PrimaryDetectorScore: 0.4},
		},
	})
	if err != nil {
		t.Fatalf("verify known face crops: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/api/v1/verify-known-face-crops" {
		t.Fatalf("expected /api/v1/verify-known-face-crops, got %s", gotPath)
	}
	if len(gotRequest.Targets) != 1 || gotRequest.Targets[0].FaceID != 7 {
		t.Fatalf("expected one target face_id=7, got %+v", gotRequest.Targets)
	}
	if gotRequest.Targets[0].ContextCropBase64 != "cropb64" {
		t.Fatalf("expected context_crop_base64 forwarded, got %q", gotRequest.Targets[0].ContextCropBase64)
	}

	if len(resp.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(resp.Results))
	}
	if resp.Results[0].VerificationStatus != "no_face" {
		t.Fatalf("expected no_face, got %s", resp.Results[0].VerificationStatus)
	}
	if resp.Results[0].EvidenceSchemaVersion != "independent_v2" {
		t.Fatalf("expected evidence_schema_version=independent_v2, got %s", resp.Results[0].EvidenceSchemaVersion)
	}
}

func TestClientHealthReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(MLHealthResponse{
			Status:            "ok",
			VerifierAvailable: true,
			VerifierName:      MLHealthVerifierNameExpected,
			VerifierVersion:   MLHealthVerifierVersionExpected,
		})
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	res, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if !res.Ready {
		t.Fatalf("expected ready=true, got %+v", res)
	}
	if res.VerifierName != MLHealthVerifierNameExpected {
		t.Fatalf("expected verifier_name %s, got %s", MLHealthVerifierNameExpected, res.VerifierName)
	}
}

func TestClientHealthUnreadyOn503Degraded(t *testing.T) {
	// 复现 NAS #3 根因：模型缺失时 ML health 返回 503 + degraded + verifier_available=false。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(MLHealthResponse{
			Status:            "degraded",
			VerifierAvailable: false,
			VerifierName:      MLHealthVerifierNameExpected,
			VerifierVersion:   MLHealthVerifierVersionExpected,
		})
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	res, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("503 should not return transport error, got %v", err)
	}
	if res.Ready {
		t.Fatalf("expected ready=false on 503 degraded, got %+v", res)
	}
}

func TestClientHealthUnreadyOnIdentityMismatch(t *testing.T) {
	// 防御：health 200 + available=true 但验证器 identity 被替换（如 2026may）→ 未就绪。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(MLHealthResponse{
			Status:            "ok",
			VerifierAvailable: true,
			VerifierName:      "yunet",
			VerifierVersion:   "opencv-yunet-2026may", // 非预期版本
		})
	}))
	defer server.Close()

	client := New(server.URL, time.Second)
	res, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Ready {
		t.Fatalf("expected ready=false on identity mismatch, got %+v", res)
	}
}

func TestClientHealthTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL, 10*time.Millisecond)
	_, err := client.Health(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
