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
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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
	if err := db.AutoMigrate(&repository.ResultQueueItem{}); err != nil {
		// ignore queue migration unrelated; keep compile parity
	}
	if err := db.AutoMigrate(&repository.ResultQueueItem{}, &model.AppConfig{}, &model.Photo{}, &model.ScanJob{}); err != nil {
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
	service := NewPhotoService(photoRepo, scanJobRepo, cfg, configService, nil, nil, nil).(*photoService)
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

	stopped := waitForTaskStatus(t, service, map[string]bool{"stopped": true}, 3*time.Second)
	if stopped.Status != "stopped" {
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

	interrupted := waitForTaskStatus(t, service, map[string]bool{"interrupted": true}, 3*time.Second)
	if interrupted.Status != "interrupted" {
		t.Fatalf("expected interrupted status, got %s", interrupted.Status)
	}
	if interrupted.ErrorMessage == "" {
		t.Fatal("expected interrupted task to record error message")
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
