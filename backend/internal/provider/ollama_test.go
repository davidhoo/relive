package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

func init() {
	_ = logger.Init(config.LoggingConfig{Level: "error", Console: true})
}

func TestOllamaProvider_Name(t *testing.T) {
	p, err := NewOllamaProvider(&OllamaConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "ollama" {
		t.Errorf("expected name 'ollama', got %q", p.Name())
	}
}

func TestOllamaProvider_IsAvailable_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"models": []interface{}{}})
	}))
	defer srv.Close()

	p, err := NewOllamaProvider(&OllamaConfig{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.IsAvailable() {
		t.Error("expected IsAvailable to return true")
	}
}

func TestOllamaProvider_IsAvailable_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p, err := NewOllamaProvider(&OllamaConfig{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.IsAvailable() {
		t.Error("expected IsAvailable to return false on 500")
	}
}

func TestOllamaProvider_IsAvailable_Unreachable(t *testing.T) {
	p, err := NewOllamaProvider(&OllamaConfig{Endpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.IsAvailable() {
		t.Error("expected IsAvailable to return false for unreachable endpoint")
	}
}

func TestOllamaProvider_Analyze_Success(t *testing.T) {
	responseJSON := `{
		"description": "A beautiful landscape",
		"memory_score": 80,
		"beauty_score": 70,
		"score_reason": "nice scenery",
		"main_category": "风景",
		"tags": "nature,sky"
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"response": responseJSON,
		})
	}))
	defer srv.Close()

	p, err := NewOllamaProvider(&OllamaConfig{
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
	if result.Description != "A beautiful landscape" {
		t.Errorf("unexpected description: %s", result.Description)
	}
	if result.MemoryScore != 80 {
		t.Errorf("expected memory_score 80, got %v", result.MemoryScore)
	}
}

func TestOllamaProvider_Analyze_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p, err := NewOllamaProvider(&OllamaConfig{
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
