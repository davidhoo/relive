package service

import (
	"encoding/json"
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

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&repository.ResultQueueItem{}); err != nil {
		// ignore queue migration unrelated; keep compile parity
	}
	if err := db.AutoMigrate(&repository.ResultQueueItem{}, &model.AppConfig{}, &model.Photo{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	configRepo := repository.NewConfigRepository(db)
	configService := NewConfigService(configRepo)
	photoRepo := repository.NewPhotoRepository(db)
	cfg := &config.Config{}
	cfg.Photos.RootPath = rootPath
	cfg.Photos.SupportedFormats = []string{".jpg", ".jpeg", ".png", ".heic"}
	cfg.Photos.ThumbnailPath = filepath.Join(rootPath, ".thumbnails")
	service := NewPhotoService(photoRepo, cfg, configService, nil).(*photoService)
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

func boolPtr(value bool) *bool {
	return &value
}
