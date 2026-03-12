package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVLLMProvider_Name(t *testing.T) {
	p, err := NewVLLMProvider(&VLLMConfig{Endpoint: "http://localhost:8000"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "vllm" {
		t.Errorf("expected name 'vllm', got %q", p.Name())
	}
}

func TestVLLMProvider_NewRequiresEndpoint(t *testing.T) {
	_, err := NewVLLMProvider(&VLLMConfig{Endpoint: ""})
	if err == nil {
		t.Error("expected error for empty endpoint")
	}
}

func TestVLLMProvider_IsAvailable_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
	}))
	defer srv.Close()

	p, err := NewVLLMProvider(&VLLMConfig{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.IsAvailable() {
		t.Error("expected IsAvailable to return true")
	}
}

func TestVLLMProvider_IsAvailable_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p, err := NewVLLMProvider(&VLLMConfig{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.IsAvailable() {
		t.Error("expected IsAvailable to return false on 500")
	}
}

func TestVLLMProvider_Analyze_Success(t *testing.T) {
	responseJSON := `{
		"description": "A city at night",
		"memory_score": 75,
		"beauty_score": 85,
		"score_reason": "great lighting",
		"main_category": "风景",
		"tags": "city,night"
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": responseJSON,
					},
				},
			},
		})
	}))
	defer srv.Close()

	p, err := NewVLLMProvider(&VLLMConfig{
		Endpoint:    srv.URL,
		Model:       "test-model",
		Temperature: 0.7,
		Timeout:     10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := p.Analyze(&AnalyzeRequest{
		ImageData: []byte("fake-image-data"),
		ImagePath: "/test.jpg",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Description != "A city at night" {
		t.Errorf("unexpected description: %s", result.Description)
	}
	if result.BeautyScore != 85 {
		t.Errorf("expected beauty_score 85, got %v", result.BeautyScore)
	}
}

func TestVLLMProvider_Analyze_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p, err := NewVLLMProvider(&VLLMConfig{
		Endpoint: srv.URL,
		Model:    "test-model",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = p.Analyze(&AnalyzeRequest{
		ImageData: []byte("fake"),
		ImagePath: "/test.jpg",
	})
	if err == nil {
		t.Error("expected error on server error")
	}
}
