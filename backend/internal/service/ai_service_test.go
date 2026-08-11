package service

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/provider"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockingAIProvider struct {
	analyzeStarted sync.Once
	analyzeStartCh chan struct{}
	analyzeGateCh  chan struct{}
	result         *provider.AnalyzeResult
	caption        string
}

type recordingAnalysisCompletedHandler struct {
	photoIDs []uint
	err      error
}

func (h *recordingAnalysisCompletedHandler) HandleAnalysisCompleted(photoID uint) error {
	h.photoIDs = append(h.photoIDs, photoID)
	return h.err
}

func (p *blockingAIProvider) Analyze(request *provider.AnalyzeRequest) (*provider.AnalyzeResult, error) {
	p.analyzeStarted.Do(func() {
		close(p.analyzeStartCh)
	})
	<-p.analyzeGateCh
	return p.result, nil
}

func (p *blockingAIProvider) AnalyzeBatch(requests []*provider.AnalyzeRequest) ([]*provider.AnalyzeResult, error) {
	results := make([]*provider.AnalyzeResult, 0, len(requests))
	for range requests {
		results = append(results, p.result)
	}
	return results, nil
}

func (p *blockingAIProvider) GenerateCaption(request *provider.AnalyzeRequest) (string, error) {
	return p.caption, nil
}

func (p *blockingAIProvider) Name() string {
	return "blocking"
}

func (p *blockingAIProvider) Cost() float64 {
	return 0
}

func (p *blockingAIProvider) BatchCost() float64 {
	return 0
}

func (p *blockingAIProvider) IsAvailable() bool {
	return true
}

func (p *blockingAIProvider) MaxConcurrency() int {
	return 1
}

func (p *blockingAIProvider) SupportsBatch() bool {
	return false
}

func (p *blockingAIProvider) MaxBatchSize() int {
	return 1
}

func TestAIService_GetProvider_Nil(t *testing.T) {
	svc := &aiService{provider: nil}

	_, err := svc.GetProvider()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestAIService_GetTaskStatus_Nil(t *testing.T) {
	svc := &aiService{}

	status := svc.GetTaskStatus()
	assert.Nil(t, status)
}

func TestAIService_GetTaskStatus_WithTask(t *testing.T) {
	svc := &aiService{
		currentTask: &AnalyzeTask{ID: "task-1", Status: AnalyzeTaskStatusRunning, TotalCount: 10},
	}

	status := svc.GetTaskStatus()
	require.NotNil(t, status)
	assert.Equal(t, "task-1", status.ID)
	assert.Equal(t, AnalyzeTaskStatusRunning, status.Status)
}

func TestAIService_GetBackgroundLogs_Empty(t *testing.T) {
	svc := &aiService{}

	logs := svc.GetBackgroundLogs()
	assert.Empty(t, logs)
}

func TestAIService_GetBackgroundLogs_WithLogs(t *testing.T) {
	svc := &aiService{
		backgroundLogs: []string{"log1", "log2"},
	}

	logs := svc.GetBackgroundLogs()
	assert.Len(t, logs, 2)
	assert.Equal(t, "log1", logs[0])
}

func TestAIService_AnalyzeBatch_NilProvider(t *testing.T) {
	svc := &aiService{provider: nil}

	_, err := svc.AnalyzeBatch(10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestAnalyzeTask_IsRunning(t *testing.T) {
	tests := []struct {
		status   string
		expected bool
	}{
		{AnalyzeTaskStatusRunning, true},
		{AnalyzeTaskStatusSleeping, true},
		{AnalyzeTaskStatusStopping, true},
		{AnalyzeTaskStatusCompleted, false},
		{AnalyzeTaskStatusFailed, false},
		{AnalyzeTaskStatusPending, false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			task := &AnalyzeTask{Status: tt.status}
			assert.Equal(t, tt.expected, task.IsRunning())
		})
	}
}

func TestAIService_AnalyzePhoto_DoesNotOverwritePeopleFields(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	photoRepo := repository.NewPhotoRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "race.jpg")
	info, err := os.Stat(photoPath)
	require.NoError(t, err)

	photo := &model.Photo{
		FilePath:          photoPath,
		FileName:          filepath.Base(photoPath),
		FileSize:          info.Size(),
		FileHash:          "race-hash",
		Width:             320,
		Height:            320,
		ThumbnailStatus:   model.ThumbnailStatusReady,
		FaceProcessStatus: model.FaceProcessStatusNoFace,
		FaceCount:         0,
	}
	require.NoError(t, photoRepo.Create(photo))

	providerStub := &blockingAIProvider{
		analyzeStartCh: make(chan struct{}),
		analyzeGateCh:  make(chan struct{}),
		result: &provider.AnalyzeResult{
			Description:  "并发回归测试描述",
			MainCategory: "人物",
			Tags:         "测试,人物",
			MemoryScore:  80,
			BeautyScore:  70,
			Reason:       "回归测试",
		},
		caption: "并发回归测试文案",
	}

	svc := &aiService{
		photoRepo: photoRepo,
		config: &config.Config{
			Photos: config.PhotosConfig{
				ThumbnailPath: filepath.Join(rootDir, ".thumbnails"),
			},
			AI: config.AIConfig{
				Temperature: 0.7,
				Timeout:     1,
			},
		},
		provider: providerStub,
	}
	completed := &recordingAnalysisCompletedHandler{}
	svc.SetAnalysisCompletedHandler(completed)

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.AnalyzePhoto(photo.ID)
	}()

	select {
	case <-providerStub.analyzeStartCh:
	case <-time.After(2 * time.Second):
		t.Fatal("provider Analyze was not called")
	}

	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      photo.ID,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.98,
		QualityScore: 0.93,
	}))
	require.NoError(t, photoRepo.UpdateFields(photo.ID, map[string]interface{}{
		"face_process_status": model.FaceProcessStatusReady,
		"face_count":          1,
		"top_person_category": model.PersonCategoryFamily,
	}))

	close(providerStub.analyzeGateCh)
	require.NoError(t, <-errCh)

	updated, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	assert.Equal(t, model.FaceProcessStatusReady, updated.FaceProcessStatus)
	assert.Equal(t, 1, updated.FaceCount)
	assert.Equal(t, model.PersonCategoryFamily, updated.TopPersonCategory)
	assert.True(t, updated.AIAnalyzed)
	assert.Equal(t, "并发回归测试描述", updated.Description)
	assert.Equal(t, "并发回归测试文案", updated.Caption)
	assert.Equal(t, "人物", updated.MainCategory)
	assert.False(t, updated.PeopleExcluded)
	assert.Empty(t, updated.PeopleExclusionReason)
	assert.Equal(t, []uint{photo.ID}, completed.photoIDs)

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	assert.Len(t, faces, 1)
}

func TestAIService_AnalysisCompletedPersistsScreenshotExclusion(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	photoRepo := repository.NewPhotoRepository(db)

	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "screen.jpg")
	info, err := os.Stat(photoPath)
	require.NoError(t, err)
	photo := &model.Photo{
		FilePath:        photoPath,
		FileName:        filepath.Base(photoPath),
		FileSize:        info.Size(),
		FileHash:        "screen-hash",
		Width:           320,
		Height:          320,
		ThumbnailStatus: model.ThumbnailStatusReady,
	}
	require.NoError(t, photoRepo.Create(photo))

	providerStub := &blockingAIProvider{
		analyzeStartCh: make(chan struct{}),
		analyzeGateCh:  make(chan struct{}),
		result: &provider.AnalyzeResult{
			Description:  "手机截屏",
			MainCategory: model.PhotoMainCategoryScreenshot,
			Tags:         "截屏",
			MemoryScore:  10,
			BeautyScore:  10,
			Reason:       "界面内容",
		},
		caption: "手机截屏",
	}
	close(providerStub.analyzeGateCh)
	completed := &recordingAnalysisCompletedHandler{}
	svc := &aiService{
		photoRepo: photoRepo,
		config: &config.Config{
			Photos: config.PhotosConfig{ThumbnailPath: filepath.Join(rootDir, ".thumbnails")},
			AI:     config.AIConfig{Temperature: 0.7, Timeout: 1},
		},
		provider: providerStub,
	}
	svc.SetAnalysisCompletedHandler(completed)

	require.NoError(t, svc.AnalyzePhoto(photo.ID))

	updated, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	assert.True(t, updated.PeopleExcluded)
	assert.Equal(t, model.PeopleExclusionReasonScreenshot, updated.PeopleExclusionReason)
	assert.Equal(t, []uint{photo.ID}, completed.photoIDs)
}

// TestAIService_GetAnalyzeProgress_Lite 验证 lite 模式复用共享缓存，
// 与非 lite 模式返回一致的计数，且会填充共享照片统计缓存。
func TestAIService_GetAnalyzeProgress_Lite(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	photoRepo := repository.NewPhotoRepository(db)

	now := time.Now()
	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1", FileSize: 1000, AIAnalyzed: true, AnalyzedAt: &now}))
	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2", FileSize: 2000, AIAnalyzed: true, AnalyzedAt: &now}))
	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/c.jpg", FileHash: "h3", FileSize: 3000, AIAnalyzed: false}))

	svc := &aiService{
		photoRepo: photoRepo,
		config:    &config.Config{AI: config.AIConfig{Provider: "ollama"}},
	}

	// 清空共享缓存，确保 lite 模式确实会触发加载
	invalidatePhotoStatsCache()

	// 非 lite：3 次独立 COUNT
	full, err := svc.GetAnalyzeProgress(false)
	require.NoError(t, err)
	assert.Equal(t, int64(3), full.Total)
	assert.Equal(t, int64(2), full.Analyzed)
	assert.Equal(t, int64(1), full.Unanalyzed)

	// lite：复用共享缓存（GetPhotoStats 单次聚合），结果一致
	lite, err := svc.GetAnalyzeProgress(true)
	require.NoError(t, err)
	assert.Equal(t, full.Total, lite.Total)
	assert.Equal(t, full.Analyzed, lite.Analyzed)
	assert.Equal(t, full.Unanalyzed, lite.Unanalyzed)

	// lite 调用后共享缓存应已被填充
	sharedPhotoStatsCache.mu.RLock()
	filled := sharedPhotoStatsCache.snapshot != nil
	sharedPhotoStatsCache.mu.RUnlock()
	assert.True(t, filled, "lite mode should populate shared photo stats cache")

	invalidatePhotoStatsCache()
}

// TestAIService_GetAnalyzeProgress_Lite_ReusesCache 验证 lite 模式命中缓存时不再查询 DB。
// 通过删除所有照片后立即调用 lite：若命中缓存，计数仍为旧值。
func TestAIService_GetAnalyzeProgress_Lite_ReusesCache(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	photoRepo := repository.NewPhotoRepository(db)

	now := time.Now()
	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1", FileSize: 1000, AIAnalyzed: true, AnalyzedAt: &now}))

	svc := &aiService{
		photoRepo: photoRepo,
		config:    &config.Config{AI: config.AIConfig{Provider: "ollama"}},
	}
	invalidatePhotoStatsCache()

	// 首次 lite 调用填充缓存：total=1
	first, err := svc.GetAnalyzeProgress(true)
	require.NoError(t, err)
	assert.Equal(t, int64(1), first.Total)

	// 删除照片后再次 lite：缓存未过期，应返回缓存值 1（而非查 DB 得到 0）
	require.NoError(t, photoRepo.Delete(1))
	cached, err := svc.GetAnalyzeProgress(true)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cached.Total, "lite should serve from cache within TTL")

	// 失效后应重新查询得到 0
	invalidatePhotoStatsCache()
	refreshed, err := svc.GetAnalyzeProgress(true)
	require.NoError(t, err)
	assert.Equal(t, int64(0), refreshed.Total)
}
