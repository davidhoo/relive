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

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	assert.Len(t, faces, 1)
}
