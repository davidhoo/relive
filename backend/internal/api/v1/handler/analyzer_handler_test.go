package handler

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubAnalysisService 实现 handler.AnalysisService 接口，用于 release/heartbeat handler 测试。
type stubAnalysisService struct {
	releaseFunc  func(taskID, analyzerID string, req model.ReleaseTaskRequest) (*model.ReleaseTaskResult, error)
	heartbeatFunc func(taskID, analyzerID string, lockVersion int64) (time.Time, int64, error)
}

func (s *stubAnalysisService) GetPendingTasks(int, string) ([]model.AnalysisTask, int64, error) {
	return nil, 0, nil
}
func (s *stubAnalysisService) ExtendTaskLock(taskID, analyzerID string, lockVersion int64) (time.Time, int64, error) {
	if s.heartbeatFunc != nil {
		return s.heartbeatFunc(taskID, analyzerID, lockVersion)
	}
	return time.Time{}, 0, nil
}
func (s *stubAnalysisService) ReleaseTask(taskID, analyzerID string, req model.ReleaseTaskRequest) (*model.ReleaseTaskResult, error) {
	if s.releaseFunc != nil {
		return s.releaseFunc(taskID, analyzerID, req)
	}
	return &model.ReleaseTaskResult{TaskID: taskID, NewStatus: "pending"}, nil
}
func (s *stubAnalysisService) SubmitResults([]model.AnalysisResult, uint) (*model.SubmitResultsResponse, error) {
	return nil, nil
}
func (s *stubAnalysisService) SubmitResultsDirectly([]model.AnalysisResult, uint) (*model.SubmitResultsResponse, error) {
	return nil, nil
}
func (s *stubAnalysisService) GetStats(uint) (*model.AnalyzerStatsResponse, error) { return nil, nil }
func (s *stubAnalysisService) CleanExpiredLocks() (int64, error)                   { return 0, nil }
func (s *stubAnalysisService) SetResultQueue(*service.ResultQueue)                 {}

func newReleaseHandler(svc *stubAnalysisService) *AnalyzerHandler {
	return &AnalyzerHandler{analysisService: svc}
}

func doRelease(t *testing.T, h *AnalyzerHandler, taskID, analyzerID string, req model.ReleaseTaskRequest) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/release", func(c *gin.Context) {
		c.Set("device_id", uint(0))
		h.ReleaseTask(c)
	})

	body, _ := json.Marshal(req)
	req2 := httptest.NewRequest("POST", "/release", bytes.NewReader(body))
	req2.Header.Set("X-Analyzer-ID", analyzerID)
	req2.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req2)
	return w
}

// TestReleaseTask_StaleLockReturns409 stale lock 返回 HTTP 409 TASK_LOCK_STALE。
func TestReleaseTask_StaleLockReturns409(t *testing.T) {
	svc := &stubAnalysisService{
		releaseFunc: func(_, _ string, _ model.ReleaseTaskRequest) (*model.ReleaseTaskResult, error) {
			return &model.ReleaseTaskResult{TaskID: "task_1", LockStale: true}, service.ErrTaskLockStale
		},
	}
	h := newReleaseHandler(svc)
	w := doRelease(t, h, "task_1", "analyzer-A", model.ReleaseTaskRequest{Reason: "analysis_failed", FailureClass: "provider_transient"})
	assert.Equal(t, http.StatusConflict, w.Code)

	var resp model.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "TASK_LOCK_STALE", resp.Error.Code)
}

// TestReleaseTask_IdempotentReturns200 幂等重复返回 HTTP 200。
func TestReleaseTask_IdempotentReturns200(t *testing.T) {
	svc := &stubAnalysisService{
		releaseFunc: func(_, _ string, _ model.ReleaseTaskRequest) (*model.ReleaseTaskResult, error) {
			return &model.ReleaseTaskResult{TaskID: "task_1", NewStatus: "retry_wait", Idempotent: true}, nil
		},
	}
	h := newReleaseHandler(svc)
	w := doRelease(t, h, "task_1", "analyzer-A", model.ReleaseTaskRequest{Reason: "analysis_failed", FailureClass: "provider_transient"})
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestReleaseTask_InternalErrorDoesNotLeakProviderBody 内部错误 500，不返回原始 Provider body。
func TestReleaseTask_InternalErrorDoesNotLeakProviderBody(t *testing.T) {
	svc := &stubAnalysisService{
		releaseFunc: func(_, _ string, _ model.ReleaseTaskRequest) (*model.ReleaseTaskResult, error) {
			return nil, errors.New("internal boom with ZINFOID_secret raw provider body")
		},
	}
	h := newReleaseHandler(svc)
	w := doRelease(t, h, "task_1", "analyzer-A", model.ReleaseTaskRequest{Reason: "analysis_failed"})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "ZINFOID_secret", "不应泄漏原始 Provider body")
	assert.Contains(t, w.Body.String(), "Failed to release task")
}

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
		"http://0.0.0.0:8080/api/v1/photos/42/image",
		"https://photos.example.com",
		42,
	)
	want := "https://photos.example.com/api/v1/photos/42/image"
	if got != want {
		t.Fatalf("expected rewritten download url %q, got %q", want, got)
	}
}
