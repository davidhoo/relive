package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newAutoScanTestService(t *testing.T, rootPath string) (*photoService, ConfigService) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, sqlErr := db.DB()
	if sqlErr != nil {
		t.Fatalf("db handle: %v", sqlErr)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.ResultQueueItem{}); err != nil {
		// ignore queue migration unrelated; keep compile parity
	}
	if err := db.AutoMigrate(&model.ResultQueueItem{}, &model.AppConfig{}, &model.Photo{}, &model.ScanJob{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	configRepo := repository.NewConfigRepository(db)
	configService := NewConfigService(configRepo)
	photoRepo := repository.NewPhotoRepository(db)
	scanJobRepo := repository.NewScanJobRepository(db)
	cfg := &config.Config{}
	cfg.Photos.RootPath = rootPath
	cfg.Photos.SupportedFormats = []string{".jpg", ".jpeg", ".png", ".heic"}
	cfg.Photos.ThumbnailPath = filepath.Join(rootPath, ".thumbnails")
	cfg.Performance.MaxScanWorkers = 1
	service := NewPhotoService(photoRepo, nil, db, scanJobRepo, cfg, configService, nil, nil, nil).(*photoService)
	return service, configService
}

func writeConfigJSON(t *testing.T, configService ConfigService, key string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal config %s: %v", key, err)
	}
	if err := configService.Set(key, string(payload)); err != nil {
		t.Fatalf("save config %s: %v", key, err)
	}
}

func TestPhotoService_RunAutoScanCheck_UsesSingleChangedSubtree(t *testing.T) {
	rootDir := t.TempDir()
	service, configService := newAutoScanTestService(t, rootDir)

	tripDir := filepath.Join(rootDir, "2026", "03", "trip")
	familyDir := filepath.Join(rootDir, "2026", "03", "family")
	if err := os.MkdirAll(tripDir, 0o755); err != nil {
		t.Fatalf("mkdir trip dir: %v", err)
	}
	if err := os.MkdirAll(familyDir, 0o755); err != nil {
		t.Fatalf("mkdir family dir: %v", err)
	}

	snapshot, err := service.buildScanTreeSnapshot(rootDir)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	writeConfigJSON(t, configService, "photos.auto_scan", autoScanConfig{Enabled: true, IntervalMinutes: 60})
	now := time.Now()
	writeConfigJSON(t, configService, "photos.scan_paths", scanPathsConfig{Paths: []scanPathConfig{{
		ID:              "path-1",
		Name:            "Root",
		Path:            rootDir,
		Enabled:         true,
		AutoScanEnabled: boolPtr(true),
		LastScannedAt:   &now,
	}}})
	if err := service.saveScanTreeSnapshot("path-1", snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	addedFile := filepath.Join(tripDir, "new.jpg")
	if err := os.WriteFile(addedFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := service.RunAutoScanCheck(); err != nil {
		t.Fatalf("run auto scan check: %v", err)
	}

	task := service.GetScanTask()
	if task == nil {
		t.Fatal("expected scan task to be created")
	}
	if task.Path != tripDir {
		t.Fatalf("expected subtree scan path %s, got %s", tripDir, task.Path)
	}
}

func TestPhotoService_RunAutoScanCheck_FallsBackToRootForMultipleChangedSubtrees(t *testing.T) {
	rootDir := t.TempDir()
	service, configService := newAutoScanTestService(t, rootDir)

	tripDir := filepath.Join(rootDir, "2026", "03", "trip")
	familyDir := filepath.Join(rootDir, "2026", "04", "family")
	if err := os.MkdirAll(tripDir, 0o755); err != nil {
		t.Fatalf("mkdir trip dir: %v", err)
	}
	if err := os.MkdirAll(familyDir, 0o755); err != nil {
		t.Fatalf("mkdir family dir: %v", err)
	}

	snapshot, err := service.buildScanTreeSnapshot(rootDir)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	writeConfigJSON(t, configService, "photos.auto_scan", autoScanConfig{Enabled: true, IntervalMinutes: 60})
	now := time.Now()
	writeConfigJSON(t, configService, "photos.scan_paths", scanPathsConfig{Paths: []scanPathConfig{{
		ID:              "path-1",
		Name:            "Root",
		Path:            rootDir,
		Enabled:         true,
		AutoScanEnabled: boolPtr(true),
		LastScannedAt:   &now,
	}}})
	if err := service.saveScanTreeSnapshot("path-1", snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tripDir, "new.jpg"), []byte("test"), 0o644); err != nil {
		t.Fatalf("write trip file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(familyDir, "new.jpg"), []byte("test"), 0o644); err != nil {
		t.Fatalf("write family file: %v", err)
	}

	if err := service.RunAutoScanCheck(); err != nil {
		t.Fatalf("run auto scan check: %v", err)
	}

	task := service.GetScanTask()
	if task == nil {
		t.Fatal("expected scan task to be created")
	}
	if task.Path != rootDir {
		t.Fatalf("expected full root scan path %s, got %s", rootDir, task.Path)
	}
}

func TestPhotoService_StopScanTask_PersistsStoppedStatus(t *testing.T) {
	rootDir := t.TempDir()
	service, _ := newAutoScanTestService(t, rootDir)

	service.processPhotoFunc = func(filePath string, info os.FileInfo) (*model.Photo, error) {
		time.Sleep(80 * time.Millisecond)
		now := time.Now()
		return &model.Photo{
			FilePath:    filePath,
			FileName:    filepath.Base(filePath),
			FileSize:    info.Size(),
			FileHash:    filePath,
			Width:       1,
			Height:      1,
			CreatedAt:   now,
			UpdatedAt:   now,
			FileModTime: &now,
		}, nil
	}

	for i := 0; i < 5; i++ {
		filePath := filepath.Join(rootDir, fmt.Sprintf("%d.jpg", i))
		if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
			t.Fatalf("write file %d: %v", i, err)
		}
	}

	task, err := service.StartScan(rootDir)
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	if _, err := service.StopScanTask(task.ID); err != nil {
		t.Fatalf("stop scan: %v", err)
	}

	stopped := waitForTaskStatus(t, service, map[string]bool{model.ScanJobStatusStopped: true}, 3*time.Second)
	if stopped.Status != model.ScanJobStatusStopped {
		t.Fatalf("expected stopped status, got %s", stopped.Status)
	}
	if stopped.StopRequestedAt == nil {
		t.Fatal("expected stop_requested_at to be set")
	}
}

func TestPhotoService_HandleShutdown_MarksInterrupted(t *testing.T) {
	rootDir := t.TempDir()
	service, _ := newAutoScanTestService(t, rootDir)

	service.processPhotoFunc = func(filePath string, info os.FileInfo) (*model.Photo, error) {
		time.Sleep(80 * time.Millisecond)
		now := time.Now()
		return &model.Photo{
			FilePath:    filePath,
			FileName:    filepath.Base(filePath),
			FileSize:    info.Size(),
			FileHash:    filePath,
			Width:       1,
			Height:      1,
			CreatedAt:   now,
			UpdatedAt:   now,
			FileModTime: &now,
		}, nil
	}

	for i := 0; i < 3; i++ {
		filePath := filepath.Join(rootDir, fmt.Sprintf("interrupt-%d.jpg", i))
		if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
			t.Fatalf("write file %d: %v", i, err)
		}
	}

	if _, err := service.StartRebuild(rootDir); err != nil {
		t.Fatalf("start rebuild: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	if err := service.HandleShutdown(); err != nil {
		t.Fatalf("handle shutdown: %v", err)
	}

	interrupted := waitForTaskStatus(t, service, map[string]bool{model.ScanJobStatusInterrupted: true}, 3*time.Second)
	if interrupted.Status != model.ScanJobStatusInterrupted {
		t.Fatalf("expected interrupted status, got %s", interrupted.Status)
	}
	if interrupted.ErrorMessage == "" {
		t.Fatal("expected interrupted task to record error message")
	}
}

func TestPhotoService_StartScan_SkipsUnchangedExistingPhotoProcessing(t *testing.T) {
	rootDir := t.TempDir()
	service, _ := newAutoScanTestService(t, rootDir)

	filePath := filepath.Join(rootDir, "existing.jpg")
	content := []byte("test")
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}

	now := time.Now()
	photo := &model.Photo{
		FilePath:    filePath,
		FileName:    filepath.Base(filePath),
		FileSize:    info.Size(),
		FileHash:    "existing-hash",
		Width:       100,
		Height:      100,
		CreatedAt:   now,
		UpdatedAt:   now,
		FileModTime: ptrTime(info.ModTime()),
	}
	if err := service.repo.Create(photo); err != nil {
		t.Fatalf("create existing photo: %v", err)
	}

	called := 0
	service.processPhotoFunc = func(filePath string, info os.FileInfo) (*model.Photo, error) {
		called++
		return nil, fmt.Errorf("processPhoto should not be called for unchanged files")
	}

	if _, err := service.StartScan(rootDir); err != nil {
		t.Fatalf("start scan: %v", err)
	}

	completed := waitForTaskStatus(t, service, map[string]bool{model.ScanJobStatusCompleted: true}, 3*time.Second)
	if completed.ProcessedFiles != 1 {
		t.Fatalf("expected 1 processed file, got %d", completed.ProcessedFiles)
	}
	if called != 0 {
		t.Fatalf("expected processPhoto to be skipped, called %d times", called)
	}

	photos, err := service.repo.ListByPathPrefix(rootDir)
	if err != nil {
		t.Fatalf("list photos by path prefix: %v", err)
	}
	if len(photos) != 1 {
		t.Fatalf("expected 1 photo record, got %d", len(photos))
	}
}

func TestPhotoService_StartRebuild_PreservesPeopleFieldsForExistingPhoto(t *testing.T) {
	rootDir := t.TempDir()
	service, _ := newAutoScanTestService(t, rootDir)

	filePath := filepath.Join(rootDir, "existing.jpg")
	if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}

	now := time.Now()
	existing := &model.Photo{
		FilePath:          filePath,
		FileName:          filepath.Base(filePath),
		FileSize:          info.Size(),
		FileHash:          "existing-hash",
		Width:             100,
		Height:            100,
		CreatedAt:         now,
		UpdatedAt:         now,
		FileModTime:       &now,
		FaceProcessStatus: model.FaceProcessStatusReady,
		FaceCount:         2,
		TopPersonCategory: model.PersonCategoryFamily,
	}
	if err := service.repo.Create(existing); err != nil {
		t.Fatalf("create existing photo: %v", err)
	}

	service.processPhotoFunc = func(filePath string, info os.FileInfo) (*model.Photo, error) {
		now := time.Now()
		return &model.Photo{
			FilePath:    filePath,
			FileName:    filepath.Base(filePath),
			FileSize:    info.Size(),
			FileHash:    filePath,
			Width:       1,
			Height:      1,
			CreatedAt:   now,
			UpdatedAt:   now,
			FileModTime: &now,
		}, nil
	}

	if _, err := service.StartRebuild(rootDir); err != nil {
		t.Fatalf("start rebuild: %v", err)
	}

	task := waitForTaskStatus(t, service, map[string]bool{model.ScanJobStatusCompleted: true}, 3*time.Second)
	if task.SkippedFiles != 0 {
		t.Fatalf("expected rebuild to update existing photo without skip, got skipped=%d", task.SkippedFiles)
	}

	updated, err := service.repo.GetByID(existing.ID)
	if err != nil {
		t.Fatalf("load updated photo: %v", err)
	}
	if updated.FaceProcessStatus != model.FaceProcessStatusReady {
		t.Fatalf("expected face_process_status to stay ready, got %s", updated.FaceProcessStatus)
	}
	if updated.FaceCount != 2 {
		t.Fatalf("expected face_count to stay 2, got %d", updated.FaceCount)
	}
	if updated.TopPersonCategory != model.PersonCategoryFamily {
		t.Fatalf("expected top_person_category to stay family, got %s", updated.TopPersonCategory)
	}
}

func TestPhotoService_StartRebuild_DoesNotOverwriteDerivedFieldsUpdatedDuringScan(t *testing.T) {
	rootDir := t.TempDir()
	service, _ := newAutoScanTestService(t, rootDir)

	filePath := filepath.Join(rootDir, "existing.jpg")
	require.NoError(t, os.WriteFile(filePath, []byte("test"), 0o644))

	info, err := os.Stat(filePath)
	require.NoError(t, err)

	now := time.Now()
	existing := &model.Photo{
		FilePath:          filePath,
		FileName:          filepath.Base(filePath),
		FileSize:          info.Size(),
		FileHash:          "existing-hash",
		Width:             100,
		Height:            100,
		CreatedAt:         now,
		UpdatedAt:         now,
		FileModTime:       &now,
		FaceProcessStatus: model.FaceProcessStatusNoFace,
		FaceCount:         0,
		AIAnalyzed:        false,
	}
	require.NoError(t, service.repo.Create(existing))

	processStarted := make(chan struct{})
	processRelease := make(chan struct{})
	service.processPhotoFunc = func(filePath string, info os.FileInfo) (*model.Photo, error) {
		close(processStarted)
		<-processRelease

		now := time.Now()
		return &model.Photo{
			FilePath:        filePath,
			FileName:        filepath.Base(filePath),
			FileSize:        info.Size(),
			FileHash:        "updated-hash",
			Width:           640,
			Height:          480,
			CreatedAt:       now,
			UpdatedAt:       now,
			FileModTime:     &now,
			ThumbnailPath:   "derived/new-thumb.jpg",
			ThumbnailStatus: model.ThumbnailStatusPending,
			GeocodeStatus:   model.GeocodeStatusNone,
		}, nil
	}

	if _, err := service.StartRebuild(rootDir); err != nil {
		t.Fatalf("start rebuild: %v", err)
	}

	select {
	case <-processStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("processPhoto was not called")
	}

	require.NoError(t, service.repo.UpdateFields(existing.ID, map[string]interface{}{
		"face_process_status": model.FaceProcessStatusReady,
		"face_count":          2,
		"top_person_category": model.PersonCategoryFamily,
		"ai_analyzed":         true,
		"description":         "fresh ai description",
		"caption":             "fresh ai caption",
		"main_category":       "人物",
		"memory_score":        88,
		"beauty_score":        77,
		"overall_score":       84,
		"score_reason":        "fresh ai reason",
	}))

	close(processRelease)

	task := waitForTaskStatus(t, service, map[string]bool{model.ScanJobStatusCompleted: true}, 3*time.Second)
	if task.SkippedFiles != 0 {
		t.Fatalf("expected rebuild to update existing photo without skip, got skipped=%d", task.SkippedFiles)
	}

	updated, err := service.repo.GetByID(existing.ID)
	require.NoError(t, err)
	assert.Equal(t, model.FaceProcessStatusReady, updated.FaceProcessStatus)
	assert.Equal(t, 2, updated.FaceCount)
	assert.Equal(t, model.PersonCategoryFamily, updated.TopPersonCategory)
	assert.True(t, updated.AIAnalyzed)
	assert.Equal(t, "fresh ai description", updated.Description)
	assert.Equal(t, "fresh ai caption", updated.Caption)
	assert.Equal(t, "人物", updated.MainCategory)
	assert.Equal(t, 88, updated.MemoryScore)
	assert.Equal(t, 77, updated.BeautyScore)
	assert.Equal(t, 84, updated.OverallScore)
	assert.Equal(t, "fresh ai reason", updated.ScoreReason)
	assert.Equal(t, "updated-hash", updated.FileHash)
	assert.Equal(t, 640, updated.Width)
	assert.Equal(t, 480, updated.Height)
}

func waitForTaskStatus(t *testing.T, service *photoService, statuses map[string]bool, timeout time.Duration) *model.ScanTask {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task := service.GetScanTask()
		service.taskMutex.RLock()
		activeJob := service.activeJob
		service.taskMutex.RUnlock()
		if task != nil && statuses[task.Status] && activeJob == nil {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	task := service.GetScanTask()
	if task == nil {
		t.Fatal("expected scan task to exist")
	}
	t.Fatalf("expected task status in %v, got %s", statuses, task.Status)
	return nil
}

func boolPtr(value bool) *bool {
	return &value
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

// --- Pure-logic tests (no filesystem) ---

// newPhotoServiceForList 构造带 configService 的 photoService，用于列表/计数缓存测试。
func newPhotoServiceForList(t *testing.T) (*photoService, repository.PhotoRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.PhotoTag{}, &model.PhotoTagStats{}, &model.ScanJob{}, &model.AppConfig{}))
	// deletePhotoAndTags 现在会清理 faces 和同步 person_identity_profiles，需要这些表存在。
	require.NoError(t, db.AutoMigrate(&model.Person{}, &model.Face{}, &model.PersonIdentityProfile{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	photoRepo := repository.NewPhotoRepository(db)
	configService := NewConfigService(repository.NewConfigRepository(db))
	scanJobRepo := repository.NewScanJobRepository(db)
	cfg := &config.Config{Photos: config.PhotosConfig{ThumbnailPath: t.TempDir()}}
	svc := NewPhotoService(photoRepo, nil, db, scanJobRepo, cfg, configService, nil, nil, nil).(*photoService)
	return svc, photoRepo
}

func newPhotoServicePure(t *testing.T) (PhotoService, repository.PhotoRepository, repository.PhotoTagRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.PhotoTag{}, &model.PhotoTagStats{}, &model.ScanJob{}, &model.AppConfig{}))
	require.NoError(t, db.AutoMigrate(&model.Person{}, &model.Face{}, &model.PersonIdentityProfile{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	photoRepo := repository.NewPhotoRepository(db)
	photoTagRepo := repository.NewPhotoTagRepository(db)
	scanJobRepo := repository.NewScanJobRepository(db)
	cfg := &config.Config{
		Photos: config.PhotosConfig{ThumbnailPath: t.TempDir()},
	}
	svc := NewPhotoService(photoRepo, photoTagRepo, db, scanJobRepo, cfg, nil, nil, nil, nil)
	return svc, photoRepo, photoTagRepo
}

func TestPhotoService_CountAll_Empty(t *testing.T) {
	svc, _, _ := newPhotoServicePure(t)
	count, err := svc.CountAll()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestPhotoService_CountAll_WithPhotos(t *testing.T) {
	svc, repo, _ := newPhotoServicePure(t)
	repo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1"})
	repo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2"})

	count, err := svc.CountAll()
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestPhotoService_CountAnalyzed(t *testing.T) {
	svc, repo, _ := newPhotoServicePure(t)
	now := time.Now()
	repo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1", AIAnalyzed: true, AnalyzedAt: &now})
	repo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2", AIAnalyzed: false})

	count, err := svc.CountAnalyzed()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestPhotoService_CountUnanalyzed(t *testing.T) {
	svc, repo, _ := newPhotoServicePure(t)
	now := time.Now()
	repo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1", AIAnalyzed: true, AnalyzedAt: &now})
	repo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2", AIAnalyzed: false})

	count, err := svc.CountUnanalyzed()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestPhotoService_GetPhotoByID_Found(t *testing.T) {
	svc, repo, _ := newPhotoServicePure(t)
	repo.Create(&model.Photo{FilePath: "/test.jpg", FileHash: "h1"})

	photo, err := svc.GetPhotoByID(1)
	require.NoError(t, err)
	assert.Equal(t, "/test.jpg", photo.FilePath)
}

func TestPhotoService_GetPhotoByID_NotFound(t *testing.T) {
	svc, _, _ := newPhotoServicePure(t)
	_, err := svc.GetPhotoByID(999)
	require.Error(t, err)
}

func TestPhotoService_GetCategories(t *testing.T) {
	svc, repo, _ := newPhotoServicePure(t)
	repo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1", MainCategory: "风景"})
	repo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2", MainCategory: "人物"})
	repo.Create(&model.Photo{FilePath: "/c.jpg", FileHash: "h3", MainCategory: "风景"})

	categories, err := svc.GetCategories()
	require.NoError(t, err)
	assert.Contains(t, categories, "风景")
	assert.Contains(t, categories, "人物")
}

func TestPhotoService_GetTags(t *testing.T) {
	svc, repo, tagRepo := newPhotoServicePure(t)
	repo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1", Tags: "nature,sky"})
	repo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2", Tags: "city,night"})

	// Populate photo_tags table (in production, SyncTags is called during AI analysis)
	tagRepo.SyncTags(1, "nature,sky")
	tagRepo.SyncTags(2, "city,night")

	tags, total, err := svc.GetTags("", 50)
	require.NoError(t, err)
	assert.Len(t, tags, 4)
	assert.Equal(t, int64(4), total)
	tagNames := make([]string, len(tags))
	for i, tc := range tags {
		tagNames[i] = tc.Tag
	}
	assert.Contains(t, tagNames, "nature")
	assert.Contains(t, tagNames, "sky")
	assert.Contains(t, tagNames, "city")
	assert.Contains(t, tagNames, "night")

	// Test search
	filtered, _, err := svc.GetTags("nat", 50)
	require.NoError(t, err)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "nature", filtered[0].Tag)

	// Test limit
	limited, _, err := svc.GetTags("", 2)
	require.NoError(t, err)
	assert.Len(t, limited, 2)
}

func TestPhotoService_GetPathDerivedStatus(t *testing.T) {
	svc, repo, _ := newPhotoServicePure(t)
	lat, lon := 39.9, 116.4
	repo.Create(&model.Photo{
		FilePath: "/photos/a.jpg", FileHash: "h1",
		AIAnalyzed: true, ThumbnailStatus: model.ThumbnailStatusReady,
		GPSLatitude: &lat, GPSLongitude: &lon, GeocodeStatus: model.GeocodeStatusReady,
	})
	repo.Create(&model.Photo{
		FilePath: "/photos/b.jpg", FileHash: "h2",
		AIAnalyzed: false, ThumbnailStatus: model.ThumbnailStatusPending,
	})

	status, err := svc.GetPathDerivedStatus("/photos/")
	require.NoError(t, err)
	assert.Equal(t, int64(2), status.PhotoTotal)
	assert.Equal(t, int64(1), status.AnalyzedTotal)
	assert.Equal(t, int64(1), status.ThumbnailReady)
	assert.Equal(t, int64(1), status.ThumbnailPending)
	assert.Equal(t, int64(1), status.GeocodeTotal)
	assert.Equal(t, int64(1), status.GeocodeReady)
}

func TestPhotoService_DeletePhotosByPathPrefix(t *testing.T) {
	svc, repo, _ := newPhotoServicePure(t)
	repo.Create(&model.Photo{FilePath: "/photos/a.jpg", FileHash: "h1"})
	repo.Create(&model.Photo{FilePath: "/photos/b.jpg", FileHash: "h2"})
	repo.Create(&model.Photo{FilePath: "/other/c.jpg", FileHash: "h3"})

	count, err := svc.DeletePhotosByPathPrefix("/photos/")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	total, _ := svc.CountAll()
	assert.Equal(t, int64(1), total)
}

// TestPhotoService_DeleteCleansTagStats 验证删除照片时同步清理 photo_tags 与统计表。
func TestPhotoService_DeleteCleansTagStats(t *testing.T) {
	svc, repo, tagRepo := newPhotoServicePure(t)
	repo.Create(&model.Photo{FilePath: "/photos/a.jpg", FileHash: "h1"})
	repo.Create(&model.Photo{FilePath: "/photos/b.jpg", FileHash: "h2"})
	tagRepo.SyncTags(1, "nature,sky")
	tagRepo.SyncTags(2, "nature,city")
	// nature=2, sky=1, city=1

	_, err := svc.DeletePhotosByPathPrefix("/photos/")
	require.NoError(t, err)

	tags, total, err := svc.GetTags("", 50)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, tags)

	// photo_tags 明细也应清空（无孤儿）
	var photoTagCount int64
	require.NoError(t, svc.(*photoService).db.Table("photo_tags").Count(&photoTagCount).Error)
	assert.Equal(t, int64(0), photoTagCount)
}

// TestPhotoService_RebuildTagStats 验证重建方法修复漂移。
func TestPhotoService_RebuildTagStats(t *testing.T) {
	svc, repo, tagRepo := newPhotoServicePure(t)
	repo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1"})
	tagRepo.SyncTags(1, "nature,sky")

	// 人为破坏统计
	ps := svc.(*photoService)
	require.NoError(t, ps.db.Exec(`UPDATE photo_tag_stats SET photo_count = 999`).Error)

	require.NoError(t, svc.RebuildTagStats())

	tags, _, err := svc.GetTags("", 50)
	require.NoError(t, err)
	for _, tc := range tags {
		assert.Equal(t, 1, tc.Count)
	}
}

func TestPhotoService_GetPhotosByPathPrefix(t *testing.T) {
	svc, repo, _ := newPhotoServicePure(t)
	repo.Create(&model.Photo{FilePath: "/photos/a.jpg", FileHash: "h1"})
	repo.Create(&model.Photo{FilePath: "/photos/b.jpg", FileHash: "h2"})
	repo.Create(&model.Photo{FilePath: "/other/c.jpg", FileHash: "h3"})

	photos, err := svc.GetPhotosByPathPrefix("/photos/")
	require.NoError(t, err)
	assert.Len(t, photos, 2)
}

func TestPhotoService_CountPhotosByPathPrefix(t *testing.T) {
	svc, repo, _ := newPhotoServicePure(t)
	repo.Create(&model.Photo{FilePath: "/photos/a.jpg", FileHash: "h1"})
	repo.Create(&model.Photo{FilePath: "/other/c.jpg", FileHash: "h2"})

	count, err := svc.CountPhotosByPathPrefix("/photos/")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestPhotoService_GetScanTask_NilWhenNoActive(t *testing.T) {
	svc, _, _ := newPhotoServicePure(t)
	assert.Nil(t, svc.GetScanTask())
}

// TestPhotoService_GetPhotosSummary_NoTotal 验证 NoTotal=true 时跳过 COUNT、total 恒为 0。
func TestPhotoService_GetPhotosSummary_NoTotal(t *testing.T) {
	svc, repo := newPhotoServiceForList(t)
	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Create(&model.Photo{
			FilePath: fmt.Sprintf("/p%02d.jpg", i),
			FileHash: fmt.Sprintf("h%02d", i),
		}))
	}

	summaries, total, err := svc.GetPhotosSummary(&model.GetPhotosRequest{Page: 1, PageSize: 10, NoTotal: true})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total, "NoTotal should skip COUNT")
	assert.Len(t, summaries, 5)
}

// TestPhotoService_GetPhotosSummary_FilteredCountCache 验证筛选计数缓存的命中与失效。
func TestPhotoService_GetPhotosSummary_FilteredCountCache(t *testing.T) {
	svc, repo := newPhotoServiceForList(t)
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(&model.Photo{
			FilePath: fmt.Sprintf("/p%02d.jpg", i),
			FileHash: fmt.Sprintf("h%02d", i),
		}))
	}

	// 首次查询：total=3
	_, total, err := svc.GetPhotosSummary(&model.GetPhotosRequest{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)

	// 直接写入一张照片（绕过服务层失效逻辑）
	require.NoError(t, repo.Create(&model.Photo{FilePath: "/p99.jpg", FileHash: "h99"}))

	// 缓存未失效：仍返回旧的 3
	_, total, err = svc.GetPhotosSummary(&model.GetPhotosRequest{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total, "cached total should be stale until invalidated")

	// 失效后：重新查询得到 4
	svc.invalidateFilteredCountCache()
	_, total, err = svc.GetPhotosSummary(&model.GetPhotosRequest{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
}

// TestPhotoService_GetPhotosSummary_FilteredCountCache_DifferentFilters 验证不同筛选条件使用不同缓存键。
func TestPhotoService_GetPhotosSummary_FilteredCountCache_DifferentFilters(t *testing.T) {
	svc, repo := newPhotoServiceForList(t)
	now := time.Now()
	require.NoError(t, repo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1", AIAnalyzed: true, AnalyzedAt: &now}))
	require.NoError(t, repo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2", AIAnalyzed: false}))

	analyzed := true
	_, totalAnalyzed, err := svc.GetPhotosSummary(&model.GetPhotosRequest{Page: 1, PageSize: 10, Analyzed: &analyzed})
	require.NoError(t, err)
	assert.Equal(t, int64(1), totalAnalyzed)

	// 不同筛选条件不应命中彼此的缓存
	_, totalAll, err := svc.GetPhotosSummary(&model.GetPhotosRequest{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(2), totalAll)
}

// TestPhotoService_CountByStatus_CacheHitAndInvalidate 验证 /photos/counts 缓存命中与失效。
func TestPhotoService_CountByStatus_CacheHitAndInvalidate(t *testing.T) {
	svc, repo := newPhotoServiceForList(t)
	// 2 active + 1 excluded
	require.NoError(t, repo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1"}))
	require.NoError(t, repo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2"}))
	require.NoError(t, repo.Create(&model.Photo{FilePath: "/c.jpg", FileHash: "h3", Status: "excluded"}))

	// 首次查询：cache miss，从 DB 取
	counts, err := svc.CountByStatus()
	require.NoError(t, err)
	assert.Equal(t, int64(2), counts.ActiveCount)
	assert.Equal(t, int64(1), counts.ExcludedCount)

	// 直接写入一张照片（绕过服务层失效逻辑）
	require.NoError(t, repo.Create(&model.Photo{FilePath: "/d.jpg", FileHash: "h4"}))

	// 缓存未失效：仍返回旧的 active=2
	counts, err = svc.CountByStatus()
	require.NoError(t, err)
	assert.Equal(t, int64(2), counts.ActiveCount, "cached counts should be stale until invalidated")

	// 失效后：重新查询得到 active=3
	svc.invalidatePhotoStatusCountsCache()
	counts, err = svc.CountByStatus()
	require.NoError(t, err)
	assert.Equal(t, int64(3), counts.ActiveCount)
	assert.Equal(t, int64(1), counts.ExcludedCount)
}

// TestPhotoService_BatchUpdateStatus_InvalidatesCountsCache 验证批量排除/恢复后 counts 缓存失效。
func TestPhotoService_BatchUpdateStatus_InvalidatesCountsCache(t *testing.T) {
	svc, repo := newPhotoServiceForList(t)
	var ids []uint
	for i := 0; i < 3; i++ {
		p := &model.Photo{FilePath: fmt.Sprintf("/p%02d.jpg", i), FileHash: fmt.Sprintf("h%02d", i)}
		require.NoError(t, repo.Create(p))
		ids = append(ids, p.ID)
	}

	// 预热缓存：active=3
	counts, err := svc.CountByStatus()
	require.NoError(t, err)
	assert.Equal(t, int64(3), counts.ActiveCount)

	// 批量排除 2 张 → 触发缓存失效
	affected, err := svc.BatchUpdateStatus(&model.BatchUpdateStatusRequest{PhotoIDs: ids[:2], Status: "excluded"})
	require.NoError(t, err)
	assert.Equal(t, int64(2), affected)

	// 缓存已失效，重新查询应反映最新状态
	counts, err = svc.CountByStatus()
	require.NoError(t, err)
	assert.Equal(t, int64(1), counts.ActiveCount, "counts cache should be invalidated after batch status update")
	assert.Equal(t, int64(2), counts.ExcludedCount)
}

type recordingPeopleEligibilityHandler struct {
	PeopleService
	photoIDs []uint
}

func (h *recordingPeopleEligibilityHandler) HandleAnalysisCompleted(photoID uint) error {
	h.photoIDs = append(h.photoIDs, photoID)
	return nil
}

func TestPhotoService_UpdateCategoryReconcilesPeopleEligibility(t *testing.T) {
	svc, repo, _ := newPhotoServiceForDeleteTest(t)
	photo := &model.Photo{
		FilePath: "/category-screen.png", FileName: "category-screen.png", FileSize: 1, FileHash: "category-screen",
		Status: model.PhotoStatusActive, AIAnalyzed: true, MainCategory: model.PhotoMainCategoryScreenshot,
		PeopleExcluded: true, PeopleExclusionReason: model.PeopleExclusionReasonScreenshot,
	}
	require.NoError(t, repo.Create(photo))

	recorder := &recordingPeopleEligibilityHandler{}
	svc.SetPeopleService(recorder)

	require.NoError(t, svc.UpdateCategory(photo.ID, "人物"))
	updated, err := repo.GetByID(photo.ID)
	require.NoError(t, err)
	assert.Equal(t, "人物", updated.MainCategory)
	assert.False(t, updated.PeopleExcluded)
	assert.Empty(t, updated.PeopleExclusionReason)
	assert.Equal(t, []uint{photo.ID}, recorder.photoIDs)

	require.NoError(t, svc.UpdateCategory(photo.ID, model.PhotoMainCategoryScreenshot))
	updated, err = repo.GetByID(photo.ID)
	require.NoError(t, err)
	assert.True(t, updated.PeopleExcluded)
	assert.Equal(t, model.PeopleExclusionReasonScreenshot, updated.PeopleExclusionReason)
	assert.Equal(t, []uint{photo.ID, photo.ID}, recorder.photoIDs)
}

// newPhotoServiceForDeleteTest 创建包含 Face/Person/person_photos/trigger 的测试服务。
func newPhotoServiceForDeleteTest(t *testing.T) (*photoService, repository.PhotoRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Photo{}, &model.PhotoTag{}, &model.PhotoTagStats{}, &model.ScanJob{},
		&model.AppConfig{}, &model.Person{}, &model.Face{}, &model.PersonPhoto{},
		&model.PersonIdentityProfile{},
	))
	require.NoError(t, database.AutoMigrate(db))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	photoRepo := repository.NewPhotoRepository(db)
	configService := NewConfigService(repository.NewConfigRepository(db))
	scanJobRepo := repository.NewScanJobRepository(db)
	cfg := &config.Config{Photos: config.PhotosConfig{ThumbnailPath: t.TempDir()}}
	svc := NewPhotoService(photoRepo, nil, db, scanJobRepo, cfg, configService, nil, nil, nil).(*photoService)
	return svc, photoRepo, db
}

// TestPhotoService_DeletePhoto_CleansFaces 验证照片硬删除后不残留 Face。
func TestPhotoService_DeletePhoto_CleansFaces(t *testing.T) {
	svc, repo, db := newPhotoServiceForDeleteTest(t)

	person := &model.Person{Name: "Test", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	photo := &model.Photo{FilePath: "/del.jpg", FileName: "del.jpg", FileSize: 1, FileHash: "hdel", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, repo.Create(photo))
	require.NoError(t, db.Create(&model.Face{PhotoID: photo.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)

	require.NoError(t, svc.deletePhotoAndTags(photo.ID))

	var faceCount int64
	require.NoError(t, db.Model(&model.Face{}).Where("photo_id = ?", photo.ID).Count(&faceCount).Error)
	assert.Equal(t, int64(0), faceCount, "no faces should remain after photo deletion")
}

// TestPhotoService_DeletePhoto_CleansPersonPhotos 验证删除照片后 person_photos 无残留。
func TestPhotoService_DeletePhoto_CleansPersonPhotos(t *testing.T) {
	svc, repo, db := newPhotoServiceForDeleteTest(t)

	person := &model.Person{Name: "PP", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	photo := &model.Photo{FilePath: "/pp.jpg", FileName: "pp.jpg", FileSize: 1, FileHash: "hpp", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, repo.Create(photo))
	require.NoError(t, db.Create(&model.Face{PhotoID: photo.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)

	// trigger 应已创建 person_photos
	var cnt int64
	require.NoError(t, db.Table("person_photos").Where("photo_id = ?", photo.ID).Count(&cnt).Error)
	assert.Equal(t, int64(1), cnt)

	require.NoError(t, svc.deletePhotoAndTags(photo.ID))

	require.NoError(t, db.Table("person_photos").Where("photo_id = ?", photo.ID).Count(&cnt).Error)
	assert.Equal(t, int64(0), cnt, "person_photos should be cleaned after photo deletion")
}

// TestPhotoService_DeletePhoto_MultipleFacesAllCleaned 验证多张 Face 指向同一照片时全部清理。
func TestPhotoService_DeletePhoto_MultipleFacesAllCleaned(t *testing.T) {
	svc, repo, db := newPhotoServiceForDeleteTest(t)

	person := &model.Person{Name: "Multi", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	photo := &model.Photo{FilePath: "/m.jpg", FileName: "m.jpg", FileSize: 1, FileHash: "hm", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, repo.Create(photo))
	for i := 0; i < 3; i++ {
		require.NoError(t, db.Create(&model.Face{PhotoID: photo.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	}

	require.NoError(t, svc.deletePhotoAndTags(photo.ID))

	var faceCount int64
	require.NoError(t, db.Model(&model.Face{}).Where("photo_id = ?", photo.ID).Count(&faceCount).Error)
	assert.Equal(t, int64(0), faceCount, "all 3 faces should be deleted")
}

// TestPhotoService_DeletePhoto_PersonStatsUpdated 验证同一人物还有其他照片时，人物计数和代表人脸正确更新。
func TestPhotoService_DeletePhoto_PersonStatsUpdated(t *testing.T) {
	svc, repo, db := newPhotoServiceForDeleteTest(t)

	person := &model.Person{Name: "Stats", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	p1 := &model.Photo{FilePath: "/s1.jpg", FileName: "s1.jpg", FileSize: 1, FileHash: "hs1", TakenAt: &t1, Status: model.PhotoStatusActive}
	p2 := &model.Photo{FilePath: "/s2.jpg", FileName: "s2.jpg", FileSize: 1, FileHash: "hs2", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, repo.Create(p1))
	require.NoError(t, repo.Create(p2))
	f1 := &model.Face{PhotoID: p1.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}
	f2 := &model.Face{PhotoID: p2.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.8}
	require.NoError(t, db.Create(f1).Error)
	require.NoError(t, db.Create(f2).Error)
	require.NoError(t, db.Model(&model.Person{}).Where("id = ?", person.ID).Update("representative_face_id", f1.ID).Error)

	require.NoError(t, svc.deletePhotoAndTags(p1.ID))

	var person2 model.Person
	require.NoError(t, db.First(&person2, person.ID).Error)
	assert.Equal(t, 1, person2.FaceCount, "face_count should be 1")
	assert.Equal(t, 1, person2.PhotoCount, "photo_count should be 1")
	require.NotNil(t, person2.RepresentativeFaceID)
	assert.Equal(t, f2.ID, *person2.RepresentativeFaceID, "representative should be updated to surviving face")
}

// TestPhotoService_DeletePhoto_LastPhotoPersonStats 验证人物最后一张照片被删除时，计数归零。
func TestPhotoService_DeletePhoto_LastPhotoPersonStats(t *testing.T) {
	svc, repo, db := newPhotoServiceForDeleteTest(t)

	person := &model.Person{Name: "Last", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	photo := &model.Photo{FilePath: "/last.jpg", FileName: "last.jpg", FileSize: 1, FileHash: "hlast", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, repo.Create(photo))
	face := &model.Face{PhotoID: photo.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Model(&model.Person{}).Where("id = ?", person.ID).Update("representative_face_id", face.ID).Error)

	require.NoError(t, svc.deletePhotoAndTags(photo.ID))

	var person2 model.Person
	require.NoError(t, db.First(&person2, person.ID).Error)
	assert.Equal(t, 0, person2.FaceCount, "face_count should be 0")
	assert.Equal(t, 0, person2.PhotoCount, "photo_count should be 0")
	assert.Nil(t, person2.RepresentativeFaceID, "representative_face_id should be NULL")
}

// TestPhotoService_DeletePhoto_IdentityProfileDirty 验证删除照片后 identity profile 被标记 dirty。
func TestPhotoService_DeletePhoto_IdentityProfileDirty(t *testing.T) {
	svc, repo, db := newPhotoServiceForDeleteTest(t)

	person := &model.Person{Name: "Dirty", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	photo := &model.Photo{FilePath: "/d.jpg", FileName: "d.jpg", FileSize: 1, FileHash: "hd", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, repo.Create(photo))
	require.NoError(t, db.Create(&model.Face{PhotoID: photo.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)

	require.NoError(t, svc.deletePhotoAndTags(photo.ID))

	var profile model.PersonIdentityProfile
	err := db.Where("person_id = ?", person.ID).First(&profile).Error
	require.NoError(t, err)
	assert.Equal(t, model.PersonIdentityProfileStatusDirty, profile.Status)
	assert.Equal(t, "photo_deleted", profile.DirtyReason)
}
