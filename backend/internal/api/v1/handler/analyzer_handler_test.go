package handler

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestBaseURLUsesForwardedHTTPS(t *testing.T) {
	t.Helper()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "http://internal:8080/api/v1/analyzer/tasks", nil)
	ctx.Request.Host = "internal:8080"
	ctx.Request.Header.Set("X-Forwarded-Proto", "https")
	ctx.Request.Header.Set("X-Forwarded-Host", "photos.example.com")

	got := requestBaseURL(ctx)
	want := "https://photos.example.com"
	if got != want {
		t.Fatalf("expected base url %q, got %q", want, got)
	}
}

func TestRequestBaseURLUsesTLSWhenNoForwardedProto(t *testing.T) {
	t.Helper()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "https://photos.example.com/api/v1/analyzer/tasks", nil)
	ctx.Request.TLS = &tls.ConnectionState{}

	got := requestBaseURL(ctx)
	want := "https://photos.example.com"
	if got != want {
		t.Fatalf("expected base url %q, got %q", want, got)
	}
}

func TestRewriteTaskDownloadURLReplacesInternalHTTPHost(t *testing.T) {
	got := rewriteTaskDownloadURL(
		"http://0.0.0.0:8080/api/v1/photos/42/image?token=temp-token",
		"https://photos.example.com",
		42,
	)
	want := "https://photos.example.com/api/v1/photos/42/image?token=temp-token"
	if got != want {
		t.Fatalf("expected rewritten download url %q, got %q", want, got)
	}
}
