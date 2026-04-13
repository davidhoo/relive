package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/lifecycle"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
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

func TestSystemHandlerReset_SchedulesFactoryReset(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "relive.db")

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	h := NewSystemHandler(service.NewSystemService(db), &config.Config{
		Database: config.DatabaseConfig{
			Type: "sqlite",
			Path: dbPath,
		},
	}, lifecycle.NewState())

	exitScheduled := make(chan time.Duration, 1)
	h.scheduleExit = func(delay time.Duration) {
		exitScheduled <- delay
	}

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/system/reset", []byte(`{"confirm_text":"RESET"}`), nil, h.Reset)
	assert.Equal(t, http.StatusOK, rec.Code)

	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Message, "restart")

	data := decodeResponseData[model.SystemResetResponse](t, resp)
	assert.True(t, data.RestartScheduled)
	assert.FileExists(t, filepath.Join(dir, ".factory-reset-pending"))

	select {
	case delay := <-exitScheduled:
		assert.Positive(t, delay)
	default:
		t.Fatal("expected process exit to be scheduled")
	}
}

func TestSystemHandlerReset_RejectsNonSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	h := NewSystemHandler(service.NewSystemService(db), &config.Config{
		Database: config.DatabaseConfig{
			Type: "postgres",
			Path: "/tmp/relive.db",
		},
	}, lifecycle.NewState())
	h.scheduleExit = func(delay time.Duration) {
		t.Fatalf("did not expect exit scheduling for non-sqlite reset, got %v", delay)
	}

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/system/reset", []byte(`{"confirm_text":"RESET"}`), nil, h.Reset)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestSystemHandler_Health_Success(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	svc := service.NewSystemService(db)
	h := NewSystemHandler(svc, &config.Config{}, lifecycle.NewState())
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/system/health", nil, nil, h.Health)
	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)
}

func TestSystemHandler_Readiness_Ready(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	state := lifecycle.NewState()
	h := NewSystemHandler(service.NewSystemService(db), &config.Config{}, state)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/system/readiness", nil, nil, h.Readiness)
	assert.Equal(t, http.StatusOK, rec.Code)

	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)
}

func TestSystemHandler_Readiness_Draining(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	state := lifecycle.NewState()
	state.BeginDraining()
	h := NewSystemHandler(service.NewSystemService(db), &config.Config{}, state)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/system/readiness", nil, nil, h.Readiness)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestSystemHandler_Health_StaysHealthyWhileDraining(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	state := lifecycle.NewState()
	state.BeginDraining()
	h := NewSystemHandler(service.NewSystemService(db), &config.Config{}, state)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/system/health", nil, nil, h.Health)
	assert.Equal(t, http.StatusOK, rec.Code)
}
