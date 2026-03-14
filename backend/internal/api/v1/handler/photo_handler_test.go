package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPhotoService implements the minimal PhotoService methods needed for handler tests.
type stubPhotoService struct {
	getPhotosFunc      func(req *model.GetPhotosRequest) ([]*model.Photo, int64, error)
	getPhotoByIDFunc   func(id uint) (*model.Photo, error)
	countAllFunc       func() (int64, error)
	countAnalyzedFunc  func() (int64, error)
	countUnanalyzedFunc func() (int64, error)
}

func (s *stubPhotoService) GetPhotos(req *model.GetPhotosRequest) ([]*model.Photo, int64, error) {
	if s.getPhotosFunc != nil {
		return s.getPhotosFunc(req)
	}
	return nil, 0, nil
}
func (s *stubPhotoService) GetPhotoByID(id uint) (*model.Photo, error) {
	if s.getPhotoByIDFunc != nil {
		return s.getPhotoByIDFunc(id)
	}
	return nil, errors.New("not found")
}
func (s *stubPhotoService) CountAll() (int64, error) {
	if s.countAllFunc != nil {
		return s.countAllFunc()
	}
	return 0, nil
}
func (s *stubPhotoService) CountAnalyzed() (int64, error) {
	if s.countAnalyzedFunc != nil {
		return s.countAnalyzedFunc()
	}
	return 0, nil
}
func (s *stubPhotoService) CountUnanalyzed() (int64, error) {
	if s.countUnanalyzedFunc != nil {
		return s.countUnanalyzedFunc()
	}
	return 0, nil
}

// No-op implementations for the rest of the PhotoService interface
func (s *stubPhotoService) ScanDirectory(_ string) ([]*model.Photo, error) { return nil, nil }
func (s *stubPhotoService) CleanupNonExistentPhotos() (*model.CleanupPhotosResponse, error) { return nil, nil }
func (s *stubPhotoService) StartScan(_ string) (*model.ScanTask, error) { return nil, nil }
func (s *stubPhotoService) StartRebuild(_ string) (*model.ScanTask, error) { return nil, nil }
func (s *stubPhotoService) StopScanTask(_ string) (*model.ScanTask, error) { return nil, nil }
func (s *stubPhotoService) GetScanTask() *model.ScanTask { return nil }
func (s *stubPhotoService) HandleShutdown() error { return nil }
func (s *stubPhotoService) RunAutoScanCheck() error { return nil }
func (s *stubPhotoService) GetCategories() ([]string, error) { return nil, nil }
func (s *stubPhotoService) GetTags() ([]string, error) { return nil, nil }
func (s *stubPhotoService) GeocodePhotoIfNeeded(_ *model.Photo) error { return nil }
func (s *stubPhotoService) RegeocodeAllPhotos() (int, error) { return 0, nil }
func (s *stubPhotoService) DeletePhotosByPathPrefix(_ string) (int64, error) { return 0, nil }
func (s *stubPhotoService) GetPhotoIDsByPathPrefix(_ string) ([]uint, error) { return nil, nil }
func (s *stubPhotoService) GetPhotosByPathPrefix(_ string) ([]*model.Photo, error) { return nil, nil }
func (s *stubPhotoService) CountPhotosByPathPrefix(_ string) (int64, error) { return 0, nil }
func (s *stubPhotoService) GetPathDerivedStatus(_ string) (*model.PathDerivedStatus, error) { return nil, nil }
func (s *stubPhotoService) GetPathDerivedStatusBatch(_ []string) (map[string]*model.PathDerivedStatus, error) { return nil, nil }
func (s *stubPhotoService) CountByStatus() (*model.PhotoCountsResponse, error) { return &model.PhotoCountsResponse{}, nil }
func (s *stubPhotoService) BatchUpdateStatus(_ *model.BatchUpdateStatusRequest) (int64, error) { return 0, nil }
func (s *stubPhotoService) UpdateCategory(_ uint, _ string) error { return nil }

func TestPhotoHandler_GetPhotoStats_Success(t *testing.T) {
	svc := &stubPhotoService{
		countAllFunc:       func() (int64, error) { return 100, nil },
		countAnalyzedFunc:  func() (int64, error) { return 80, nil },
		countUnanalyzedFunc: func() (int64, error) { return 20, nil },
	}
	h := &PhotoHandler{photoService: svc}

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos/stats", nil, nil, h.GetPhotoStats)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	stats := decodeResponseData[model.PhotoStatsResponse](t, resp)
	assert.Equal(t, int64(100), stats.Total)
	assert.Equal(t, int64(80), stats.Analyzed)
	assert.Equal(t, int64(20), stats.Unanalyzed)
}

func TestPhotoHandler_GetPhotoStats_Error(t *testing.T) {
	svc := &stubPhotoService{
		countAllFunc: func() (int64, error) { return 0, errors.New("db error") },
	}
	h := &PhotoHandler{photoService: svc}

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos/stats", nil, nil, h.GetPhotoStats)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPhotoHandler_GetPhotos_Success(t *testing.T) {
	svc := &stubPhotoService{
		getPhotosFunc: func(req *model.GetPhotosRequest) ([]*model.Photo, int64, error) {
			return []*model.Photo{{FilePath: "/test.jpg"}}, 1, nil
		},
	}
	h := &PhotoHandler{photoService: svc}

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos?page=1&page_size=20", nil, nil, h.GetPhotos)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)
}

func TestPhotoHandler_GetPhotos_Error(t *testing.T) {
	svc := &stubPhotoService{
		getPhotosFunc: func(req *model.GetPhotosRequest) ([]*model.Photo, int64, error) {
			return nil, 0, errors.New("query error")
		},
	}
	h := &PhotoHandler{photoService: svc}

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos", nil, nil, h.GetPhotos)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPhotoHandler_GetPhotoByID_NotFound(t *testing.T) {
	svc := &stubPhotoService{
		getPhotoByIDFunc: func(id uint) (*model.Photo, error) {
			return nil, errors.New("not found")
		},
	}
	h := &PhotoHandler{photoService: svc}

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos/1", nil,
		gin.Params{{Key: "id", Value: "1"}}, h.GetPhotoByID)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPhotoHandler_GetPhotoByID_InvalidID(t *testing.T) {
	h := &PhotoHandler{photoService: &stubPhotoService{}}

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos/abc", nil,
		gin.Params{{Key: "id", Value: "abc"}}, h.GetPhotoByID)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
