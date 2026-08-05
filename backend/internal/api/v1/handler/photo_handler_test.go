package handler

import (
	"errors"
	"image/color"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPhotoService implements the minimal PhotoService methods needed for handler tests.
type stubPhotoService struct {
	getPhotosFunc              func(req *model.GetPhotosRequest) ([]*model.Photo, int64, error)
	getPhotosSummaryFunc       func(req *model.GetPhotosRequest) ([]*model.PhotoSummary, int64, error)
	getPhotosSummaryCursorFunc func(req *model.GetPhotosRequest, cursor *repository.PhotoCursor, limit int) ([]*model.PhotoSummary, bool, *repository.PhotoCursor, error)
	getPhotoByIDFunc           func(id uint) (*model.Photo, error)
	countAllFunc               func() (int64, error)
	countAnalyzedFunc          func() (int64, error)
	countUnanalyzedFunc        func() (int64, error)
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
func (s *stubPhotoService) CleanupNonExistentPhotos() (*model.CleanupPhotosResponse, error) {
	return nil, nil
}
func (s *stubPhotoService) StartScan(_ string) (*model.ScanTask, error)    { return nil, nil }
func (s *stubPhotoService) StartRebuild(_ string) (*model.ScanTask, error) { return nil, nil }
func (s *stubPhotoService) StopScanTask(_ string) (*model.ScanTask, error) { return nil, nil }
func (s *stubPhotoService) GetScanTask() *model.ScanTask                   { return nil }
func (s *stubPhotoService) HandleShutdown() error                          { return nil }
func (s *stubPhotoService) RunAutoScanCheck() error                        { return nil }
func (s *stubPhotoService) GetCategories() ([]string, error)               { return nil, nil }
func (s *stubPhotoService) GetTags(_ string, _ int) ([]model.TagWithCount, int64, error) {
	return nil, 0, nil
}
func (s *stubPhotoService) RebuildTagStats() error                                 { return nil }
func (s *stubPhotoService) GeocodePhotoIfNeeded(_ *model.Photo) error              { return nil }
func (s *stubPhotoService) RegeocodeAllPhotos() (int, error)                       { return 0, nil }
func (s *stubPhotoService) DeletePhotosByPathPrefix(_ string) (int64, error)       { return 0, nil }
func (s *stubPhotoService) GetPhotoIDsByPathPrefix(_ string) ([]uint, error)       { return nil, nil }
func (s *stubPhotoService) GetPhotosByPathPrefix(_ string) ([]*model.Photo, error) { return nil, nil }
func (s *stubPhotoService) CountPhotosByPathPrefix(_ string) (int64, error)        { return 0, nil }
func (s *stubPhotoService) GetPathDerivedStatus(_ string) (*model.PathDerivedStatus, error) {
	return nil, nil
}
func (s *stubPhotoService) GetPathDerivedStatusBatch(_ []string) (map[string]*model.PathDerivedStatus, error) {
	return nil, nil
}
func (s *stubPhotoService) CountByStatus() (*model.PhotoCountsResponse, error) {
	return &model.PhotoCountsResponse{}, nil
}
func (s *stubPhotoService) BatchUpdateStatus(_ *model.BatchUpdateStatusRequest) (int64, error) {
	return 0, nil
}
func (s *stubPhotoService) UpdateCategory(_ uint, _ string) error                  { return nil }
func (s *stubPhotoService) UpdateManualRotation(_ uint, _ int) error               { return nil }
func (s *stubPhotoService) BatchRotate(_ *model.BatchRotateRequest) (int64, error) { return 0, nil }
func (s *stubPhotoService) GetAdjacentPhotos(_ uint, _ *model.GetPhotosRequest) (*model.AdjacentPhotosResponse, error) {
	return &model.AdjacentPhotosResponse{}, nil
}
func (s *stubPhotoService) SetEventClusteringService(_ service.EventClusteringService) {}
func (s *stubPhotoService) SetPeopleService(_ service.PeopleService)                   {}
func (s *stubPhotoService) InvalidateScanPathsCache()                                  {}
func (s *stubPhotoService) GetPhotosSummary(req *model.GetPhotosRequest) ([]*model.PhotoSummary, int64, error) {
	if s.getPhotosSummaryFunc != nil {
		return s.getPhotosSummaryFunc(req)
	}
	return nil, 0, nil
}
func (s *stubPhotoService) GetPhotosSummaryCursor(req *model.GetPhotosRequest, cursor *repository.PhotoCursor, limit int) ([]*model.PhotoSummary, bool, *repository.PhotoCursor, error) {
	if s.getPhotosSummaryCursorFunc != nil {
		return s.getPhotosSummaryCursorFunc(req, cursor, limit)
	}
	return nil, false, nil, nil
}

func TestPhotoHandler_GetPhotoStats_Success(t *testing.T) {
	svc := &stubPhotoService{
		countAllFunc:        func() (int64, error) { return 100, nil },
		countAnalyzedFunc:   func() (int64, error) { return 80, nil },
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
		getPhotosSummaryFunc: func(req *model.GetPhotosRequest) ([]*model.PhotoSummary, int64, error) {
			return nil, 0, errors.New("query error")
		},
	}
	h := &PhotoHandler{photoService: svc}

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos", nil, nil, h.GetPhotos)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestPhotoHandler_GetPhotos_Cursor_RejectsNonDefaultSort 游标模式收到非默认排序返回 400。
func TestPhotoHandler_GetPhotos_Cursor_RejectsNonDefaultSort(t *testing.T) {
	h := &PhotoHandler{photoService: &stubPhotoService{}}
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos?pagination=cursor&sort_by=overall_score", nil, nil, h.GetPhotos)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.NotNil(t, resp.Error)
	assert.Equal(t, "INVALID_CURSOR", resp.Error.Code)
}

// TestPhotoHandler_GetPhotos_Cursor_RejectsAsc 游标模式 sort_desc=false 返回 400。
func TestPhotoHandler_GetPhotos_Cursor_RejectsAsc(t *testing.T) {
	h := &PhotoHandler{photoService: &stubPhotoService{}}
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos?pagination=cursor&sort_desc=false", nil, nil, h.GetPhotos)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestPhotoHandler_GetPhotos_Cursor_RejectsMalformedCursor 非法游标返回 400 INVALID_CURSOR。
func TestPhotoHandler_GetPhotos_Cursor_RejectsMalformedCursor(t *testing.T) {
	h := &PhotoHandler{photoService: &stubPhotoService{}}
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos?pagination=cursor&cursor=!!!not-base64!!!", nil, nil, h.GetPhotos)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.NotNil(t, resp.Error)
	assert.Equal(t, "INVALID_CURSOR", resp.Error.Code)
}

// TestPhotoHandler_GetPhotos_Cursor_RejectsKindMismatch 错误 kind 游标返回 400。
func TestPhotoHandler_GetPhotos_Cursor_RejectsKindMismatch(t *testing.T) {
	// 用 photos kind（人物详情）编码一个 cursor，照片列表应拒绝。
	other := encodeCursor(cursorPayload{Version: cursorVersion, Kind: cursorKindPhotos, ID: 5})
	h := &PhotoHandler{photoService: &stubPhotoService{}}
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos?pagination=cursor&cursor="+other, nil, nil, h.GetPhotos)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestPhotoHandler_GetPhotos_Cursor_RejectsUnknownVersion 错误版本游标返回 400。
func TestPhotoHandler_GetPhotos_Cursor_RejectsUnknownVersion(t *testing.T) {
	bad := encodeCursor(cursorPayload{Version: 999, Kind: cursorKindPhotoList, ID: 5})
	h := &PhotoHandler{photoService: &stubPhotoService{}}
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos?pagination=cursor&cursor="+bad, nil, nil, h.GetPhotos)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestPhotoHandler_GetPhotos_Cursor_StalledEmptyNextCursor hasMore=true 但 nextCursor 空 → 500 PAGINATION_STALLED。
func TestPhotoHandler_GetPhotos_Cursor_StalledEmptyNextCursor(t *testing.T) {
	svc := &stubPhotoService{}
	svc.getPhotosSummaryCursorFunc = func(_ *model.GetPhotosRequest, _ *repository.PhotoCursor, _ int) ([]*model.PhotoSummary, bool, *repository.PhotoCursor, error) {
		// hasMore=true 但 nextCursor=nil → 停滞。
		return []*model.PhotoSummary{{ID: 1}}, true, nil, nil
	}
	h := &PhotoHandler{photoService: svc}
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos?pagination=cursor&page_size=10", nil, nil, h.GetPhotos)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.NotNil(t, resp.Error)
	assert.Equal(t, "PAGINATION_STALLED", resp.Error.Code)
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

// newPhotoThumbnailHandlerForTest 构造一个可用的 PhotoHandler，用于缩略图缓存头测试。
// 照片文件使用真实 JPEG，缩略图文件同样真实生成，使 ShouldRefreshThumbnailCacheWithRotation
// 走稳定分支（不再强制刷新），从而验证长缓存响应头。
func newPhotoThumbnailHandlerForTest(t *testing.T) (*PhotoHandler, *model.Photo, *config.Config) {
	t.Helper()

	thumbRoot := t.TempDir()
	photoDir := t.TempDir()
	photoPath := filepath.Join(photoDir, "photo.jpg")
	require.NoError(t, imaging.Save(imaging.New(400, 300, color.NRGBA{R: 10, G: 20, B: 30, A: 255}), photoPath))

	photo := &model.Photo{
		FilePath:        photoPath,
		FileName:        "photo.jpg",
		FileSize:        1,
		FileHash:        "thumb-cache-test",
		Width:           400,
		Height:          300,
		Status:          model.PhotoStatusActive,
		ThumbnailPath:   "photos/test-thumb.jpg",
		ThumbnailStatus: model.ThumbnailStatusReady,
	}

	svc := &stubPhotoService{
		getPhotoByIDFunc: func(id uint) (*model.Photo, error) { return photo, nil },
	}

	cfg := &config.Config{Photos: config.PhotosConfig{ThumbnailPath: thumbRoot}}
	h := &PhotoHandler{photoService: svc, cfg: cfg}

	// 预生成一个 landscape（与原图 400x300 同方向）缩略图，使稳定缓存分支生效。
	thumbPath := filepath.Join(thumbRoot, photo.ThumbnailPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(thumbPath), 0o755))
	require.NoError(t, imaging.Save(imaging.New(120, 90, color.NRGBA{R: 200, G: 200, B: 200, A: 255}), thumbPath))

	return h, photo, cfg
}

// TestPhotoHandler_GetPhotoThumbnail_StableLongCache 照片缩略图稳定时不再 no-cache，
// 返回长期私有浏览器缓存，配合前端版本化 URL 避免对 NAS 重复校验。
func TestPhotoHandler_GetPhotoThumbnail_StableLongCache(t *testing.T) {
	h, _, _ := newPhotoThumbnailHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos/1/thumbnail", nil,
		gin.Params{{Key: "id", Value: "1"}}, h.GetPhotoThumbnail)

	require.Equal(t, http.StatusOK, rec.Code)
	cacheControl := rec.Header().Get("Cache-Control")
	assert.Contains(t, cacheControl, "max-age=31536000")
	assert.Contains(t, cacheControl, "private")
	assert.Contains(t, cacheControl, "immutable")
	assert.NotContains(t, cacheControl, "no-cache")
}

// TestPhotoHandler_GetPhotoThumbnail_CacheIsPrivate 稳定缩略图缓存必须为私有浏览器缓存，
// 不能进入共享缓存，避免受保护图片被中间代理缓存泄露。
func TestPhotoHandler_GetPhotoThumbnail_CacheIsPrivate(t *testing.T) {
	h, _, _ := newPhotoThumbnailHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos/1/thumbnail", nil,
		gin.Params{{Key: "id", Value: "1"}}, h.GetPhotoThumbnail)

	require.Equal(t, http.StatusOK, rec.Code)
	cacheControl := rec.Header().Get("Cache-Control")
	assert.Contains(t, cacheControl, "private")
	assert.NotContains(t, cacheControl, "public")
}

// TestPhotoHandler_GetPhotoThumbnail_UnstableNoCache 没有预生成缩略图（需被动生成）时，
// 返回原图并使用 no-cache，避免长期缓存可能变化的原图。
func TestPhotoHandler_GetPhotoThumbnail_UnstableNoCache(t *testing.T) {
	photoDir := t.TempDir()
	photoPath := filepath.Join(photoDir, "photo.jpg")
	require.NoError(t, imaging.Save(imaging.New(400, 300, color.NRGBA{R: 1, G: 2, B: 3, A: 255}), photoPath))

	photo := &model.Photo{
		FilePath:        photoPath,
		FileName:        "photo.jpg",
		FileSize:        1,
		FileHash:        "thumb-nocache-test",
		Width:           400,
		Height:          300,
		Status:          model.PhotoStatusActive,
		ThumbnailPath:   "", // 无预生成缩略图
		ThumbnailStatus: model.ThumbnailStatusNone,
	}
	svc := &stubPhotoService{
		getPhotoByIDFunc: func(id uint) (*model.Photo, error) { return photo, nil },
	}
	cfg := &config.Config{Photos: config.PhotosConfig{ThumbnailPath: t.TempDir()}}
	h := &PhotoHandler{photoService: svc, cfg: cfg}

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos/1/thumbnail", nil,
		gin.Params{{Key: "id", Value: "1"}}, h.GetPhotoThumbnail)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
}
