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
	service := NewPhotoService(photoRepo, nil, scanJobRepo, cfg, configService, nil, nil, nil).(*photoService)
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

func waitForTaskStatus(t *testing.T, service *photoService, statuses map[string]bool, timeout time.Duration) *model.ScanTask {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task := service.GetScanTask()
		if task != nil && statuses[task.Status] {
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

func newPhotoServicePure(t *testing.T) (PhotoService, repository.PhotoRepository, repository.PhotoTagRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.PhotoTag{}, &model.ScanJob{}, &model.AppConfig{}))
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
	svc := NewPhotoService(photoRepo, photoTagRepo, scanJobRepo, cfg, nil, nil, nil, nil)
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
