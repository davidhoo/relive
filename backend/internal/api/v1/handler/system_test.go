package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func init() {
	_ = logger.Init(config.LoggingConfig{Level: "error", Console: false})
}

func TestSystemHandlerGetDatabaseSize(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "relive.db")

	if err := os.WriteFile(dbPath, make([]byte, 128), 0o644); err != nil {
		t.Fatalf("write db file: %v", err)
	}
	if err := os.WriteFile(dbPath+"-wal", make([]byte, 64), 0o644); err != nil {
		t.Fatalf("write wal file: %v", err)
	}

	h := &SystemHandler{
		cfg: &config.Config{
			Database: config.DatabaseConfig{
				Type: "sqlite",
				Path: dbPath,
			},
		},
	}

	if got := h.getDatabaseSize(); got != 192 {
		t.Fatalf("expected database size 192, got %d", got)
	}
}

func TestSystemHandlerGetDatabaseSizeNonSQLite(t *testing.T) {
	h := &SystemHandler{
		cfg: &config.Config{
			Database: config.DatabaseConfig{
				Type: "postgres",
				Path: "/tmp/test.db",
			},
		},
	}

	if got := h.getDatabaseSize(); got != 0 {
		t.Fatalf("expected database size 0 for non-sqlite database, got %d", got)
	}
}

func TestSystemHandlerResetDatabaseState(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	if err := db.AutoMigrate(
		&model.Photo{},
		&model.AnalysisRuntimeLease{},
		&model.DisplayRecord{},
		&model.Device{},
		&model.DailyDisplayBatch{},
		&model.DailyDisplayItem{},
		&model.DailyDisplayAsset{},
		&model.DevicePlaybackState{},
		&model.AppConfig{},
		&model.City{},
		&model.User{},
		&model.ResultQueueItem{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	device := &model.Device{DeviceID: "D1", Name: "Device", APIKey: "key", IsEnabled: true}
	if err := db.Create(device).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	photo := &model.Photo{FilePath: "/tmp/a.jpg", FileName: "a.jpg", FileSize: 1, Width: 1, Height: 1}
	if err := db.Create(photo).Error; err != nil {
		t.Fatalf("create photo: %v", err)
	}
	batch := &model.DailyDisplayBatch{BatchDate: "2026-03-07", Status: model.DailyDisplayBatchStatusReady, ItemCount: 1, CanvasTemplate: "canvas"}
	if err := db.Create(batch).Error; err != nil {
		t.Fatalf("create batch: %v", err)
	}
	item := &model.DailyDisplayItem{BatchID: batch.ID, Sequence: 1, PhotoID: photo.ID, PreviewJPGPath: "preview.jpg", CanvasTemplate: "canvas"}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create item: %v", err)
	}
	asset := &model.DailyDisplayAsset{ItemID: item.ID, RenderProfile: "profile", BinPath: "a.bin", HeaderPath: "a.h", Checksum: "sum"}
	if err := db.Create(asset).Error; err != nil {
		t.Fatalf("create asset: %v", err)
	}
	state := &model.DevicePlaybackState{DeviceID: device.ID, BatchID: batch.ID, BatchDate: batch.BatchDate, CurrentSequence: 1}
	if err := db.Create(state).Error; err != nil {
		t.Fatalf("create playback state: %v", err)
	}
	if err := db.Create(&model.DisplayRecord{PhotoID: photo.ID, DeviceID: device.ID, DisplayedAt: batch.CreatedAt, TriggerType: model.TriggerTypeScheduled}).Error; err != nil {
		t.Fatalf("create display record: %v", err)
	}
	if err := db.Create(&model.AnalysisRuntimeLease{ResourceKey: model.GlobalAnalysisResourceKey, OwnerType: model.AnalysisOwnerTypeAnalyzer, Status: model.AnalysisRuntimeStatusRunning}).Error; err != nil {
		t.Fatalf("create runtime lease: %v", err)
	}
	if err := db.Create(&model.AppConfig{Key: "k", Value: "v"}).Error; err != nil {
		t.Fatalf("create app config: %v", err)
	}
	if err := db.Create(&model.City{GeonameID: 1, Name: "Shanghai", Country: "CN", Latitude: 1, Longitude: 1}).Error; err != nil {
		t.Fatalf("create city: %v", err)
	}
	if err := db.Create(&model.User{Username: "old", PasswordHash: "hash", IsFirstLogin: false}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&model.ResultQueueItem{Data: "{}"}).Error; err != nil {
		t.Fatalf("create result queue item: %v", err)
	}

	h := &SystemHandler{
		db:  db,
		cfg: &config.Config{Database: config.DatabaseConfig{Type: "sqlite", Path: "/tmp/test.db"}},
	}

	if err := h.resetDatabaseState(); err != nil {
		t.Fatalf("resetDatabaseState: %v", err)
	}

	assertCount := func(table string, expected int64) {
		t.Helper()
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != expected {
			t.Fatalf("expected %s count %d, got %d", table, expected, count)
		}
	}

	for _, table := range []string{"result_queue", "display_records", "daily_display_assets", "daily_display_items", "device_playback_states", "daily_display_batches", "analysis_runtime_leases", "devices", "photos", "app_config"} {
		assertCount(table, 0)
	}
	assertCount("cities", 1)
	assertCount("users", 1)

	var user model.User
	if err := db.First(&user).Error; err != nil {
		t.Fatalf("load reset user: %v", err)
	}
	if user.Username != "admin" {
		t.Fatalf("expected reset username admin, got %s", user.Username)
	}
	if !user.IsFirstLogin {
		t.Fatal("expected reset user to require first login")
	}
}

func TestSystemHandler_Health_Success(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	h := NewSystemHandler(db, &config.Config{}, nil)
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/system/health", nil, nil, h.Health)
	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)
}
