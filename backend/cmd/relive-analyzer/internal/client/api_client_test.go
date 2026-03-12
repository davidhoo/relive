package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
)

func TestAPIClient_CheckHealth_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/system/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "test-key" {
			t.Error("missing API key header")
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(model.Response{Success: true, Message: "ok"})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key")
	err := client.CheckHealth(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIClient_CheckHealth_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"success":false,"message":"internal error"}`))
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key", WithRetry(1, 10*time.Millisecond))
	err := client.CheckHealth(context.Background())
	if err == nil {
		t.Error("expected error on 500")
	}
}

func TestAPIClient_SubmitResults_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/analyzer/results" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(model.Response{Success: true})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key", WithTimeout(5*time.Second))
	results := []model.AnalysisResult{
		{PhotoID: 1, Description: "test", MemoryScore: 80, BeautyScore: 70},
	}
	_, err := client.SubmitResults(context.Background(), results)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIClient_SubmitResults_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"success":false,"message":"bad request"}`))
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key", WithRetry(1, 10*time.Millisecond))
	_, err := client.SubmitResults(context.Background(), []model.AnalysisResult{{PhotoID: 1}})
	if err == nil {
		t.Error("expected error on 400")
	}
}

func TestAPIClient_GetStats_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/analyzer/stats" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(model.Response{
			Success: true,
			Data:    map[string]interface{}{"total": 100, "analyzed": 50},
		})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key")
	stats, err := client.GetStats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}
}

func TestAPIClient_WithRetry(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"success":false}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(model.Response{Success: true})
	}))
	defer srv.Close()

	client := NewAPIClient(srv.URL, "test-key",
		WithTimeout(5*time.Second),
		WithRetry(3, 10*time.Millisecond),
	)
	err := client.CheckHealth(context.Background())
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}
