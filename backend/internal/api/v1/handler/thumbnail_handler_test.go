package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
)

// stubThumbnailService implements service.ThumbnailService for handler tests.
type stubThumbnailService struct {
	startBackgroundFunc func() (*model.ThumbnailTask, error)
	stopBackgroundFunc  func() error
	getTaskStatusFunc   func() *model.ThumbnailTask
	getStatsFunc        func() (*model.ThumbnailStatsResponse, error)
	getBackgroundLogs   func() []string
	enqueuePhotoFunc    func(photoID uint, source string, priority int) error
	enqueueByPathFunc   func(path string, source string, priority int) (int, error)
}

func (s *stubThumbnailService) StartBackground() (*model.ThumbnailTask, error) {
	if s.startBackgroundFunc != nil {
		return s.startBackgroundFunc()
	}
	return &model.ThumbnailTask{Status: "running"}, nil
}
func (s *stubThumbnailService) StopBackground() error {
	if s.stopBackgroundFunc != nil {
		return s.stopBackgroundFunc()
	}
	return nil
}
func (s *stubThumbnailService) GetTaskStatus() *model.ThumbnailTask {
	if s.getTaskStatusFunc != nil {
		return s.getTaskStatusFunc()
	}
	return nil
}
func (s *stubThumbnailService) GetStats() (*model.ThumbnailStatsResponse, error) {
	if s.getStatsFunc != nil {
		return s.getStatsFunc()
	}
	return &model.ThumbnailStatsResponse{}, nil
}
func (s *stubThumbnailService) GetBackgroundLogs() []string {
	if s.getBackgroundLogs != nil {
		return s.getBackgroundLogs()
	}
	return nil
}
func (s *stubThumbnailService) EnqueuePhoto(photoID uint, source string, priority int, force bool) error {
	if s.enqueuePhotoFunc != nil {
		return s.enqueuePhotoFunc(photoID, source, priority)
	}
	return nil
}
func (s *stubThumbnailService) EnqueueByPath(path string, source string, priority int) (int, error) {
	if s.enqueueByPathFunc != nil {
		return s.enqueueByPathFunc(path, source, priority)
	}
	return 0, nil
}
func (s *stubThumbnailService) HandleShutdown() error { return nil }
func (s *stubThumbnailService) GeneratePhoto(photoID uint, force bool) error { return nil }

func TestThumbnailHandler_StartBackground_Success(t *testing.T) {
	h := NewThumbnailHandler(&stubThumbnailService{})
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/thumbnails/start", nil, nil, h.StartBackground)
	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)
}

func TestThumbnailHandler_StartBackground_Error(t *testing.T) {
	svc := &stubThumbnailService{
		startBackgroundFunc: func() (*model.ThumbnailTask, error) {
			return nil, errors.New("already running")
		},
	}
	h := NewThumbnailHandler(svc)
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/thumbnails/start", nil, nil, h.StartBackground)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestThumbnailHandler_StopBackground_Success(t *testing.T) {
	h := NewThumbnailHandler(&stubThumbnailService{})
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/thumbnails/stop", nil, nil, h.StopBackground)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestThumbnailHandler_StopBackground_Error(t *testing.T) {
	svc := &stubThumbnailService{
		stopBackgroundFunc: func() error { return errors.New("not running") },
	}
	h := NewThumbnailHandler(svc)
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/thumbnails/stop", nil, nil, h.StopBackground)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestThumbnailHandler_GetTask(t *testing.T) {
	h := NewThumbnailHandler(&stubThumbnailService{})
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/thumbnails/task", nil, nil, h.GetTask)
	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)
}

func TestThumbnailHandler_GetStats_Success(t *testing.T) {
	svc := &stubThumbnailService{
		getStatsFunc: func() (*model.ThumbnailStatsResponse, error) {
			return &model.ThumbnailStatsResponse{Total: 10, Completed: 8, Pending: 2}, nil
		},
	}
	h := NewThumbnailHandler(svc)
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/thumbnails/stats", nil, nil, h.GetStats)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestThumbnailHandler_GetStats_Error(t *testing.T) {
	svc := &stubThumbnailService{
		getStatsFunc: func() (*model.ThumbnailStatsResponse, error) {
			return nil, errors.New("db error")
		},
	}
	h := NewThumbnailHandler(svc)
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/thumbnails/stats", nil, nil, h.GetStats)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestThumbnailHandler_GetBackgroundLogs(t *testing.T) {
	svc := &stubThumbnailService{
		getBackgroundLogs: func() []string { return []string{"line1", "line2"} },
	}
	h := NewThumbnailHandler(svc)
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/thumbnails/logs", nil, nil, h.GetBackgroundLogs)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestThumbnailHandler_Enqueue_Success(t *testing.T) {
	h := NewThumbnailHandler(&stubThumbnailService{})
	body := []byte(`{"photo_id":1}`)
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/thumbnails/enqueue", body, nil, h.Enqueue)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestThumbnailHandler_Enqueue_BadJSON(t *testing.T) {
	h := NewThumbnailHandler(&stubThumbnailService{})
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/thumbnails/enqueue", []byte(`{bad`), nil, h.Enqueue)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestThumbnailHandler_EnqueueByPath_Success(t *testing.T) {
	svc := &stubThumbnailService{
		enqueueByPathFunc: func(path, source string, priority int) (int, error) {
			return 5, nil
		},
	}
	h := NewThumbnailHandler(svc)
	body := []byte(`{"path":"/photos"}`)
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/thumbnails/enqueue-path", body, nil, h.EnqueueByPath)
	assert.Equal(t, http.StatusOK, rec.Code)
}
