package service

import (
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

func setupThumbnailServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.ThumbnailJob{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	return db
}

func newThumbnailServiceForTest(t *testing.T) (ThumbnailService, *gorm.DB) {
	t.Helper()
	db := setupThumbnailServiceTestDB(t)
	photoRepo := repository.NewPhotoRepository(db)
	jobRepo := repository.NewThumbnailJobRepository(db)
	cfg := &config.Config{
		Photos: config.PhotosConfig{ThumbnailPath: t.TempDir()},
	}
	svc := NewThumbnailService(db, photoRepo, jobRepo, cfg)
	return svc, db
}

func TestThumbnailService_GetTaskStatus_Nil(t *testing.T) {
	svc, _ := newThumbnailServiceForTest(t)
	assert.Nil(t, svc.GetTaskStatus())
}

func TestThumbnailService_GetStats_Empty(t *testing.T) {
	svc, _ := newThumbnailServiceForTest(t)

	stats, err := svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats.Total)
}

func TestThumbnailService_GetStats_WithJobs(t *testing.T) {
	svc, db := newThumbnailServiceForTest(t)

	now := time.Now()
	db.Create(&model.ThumbnailJob{PhotoID: 1, FilePath: "/p/1.jpg", Status: "completed", Source: "scan", QueuedAt: now})
	db.Create(&model.ThumbnailJob{PhotoID: 2, FilePath: "/p/2.jpg", Status: "pending", Source: "scan", QueuedAt: now})

	stats, err := svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.Total)
	assert.Equal(t, int64(1), stats.Completed)
	assert.Equal(t, int64(1), stats.Pending)
}

func TestThumbnailService_StopBackground_NotRunning(t *testing.T) {
	svc, _ := newThumbnailServiceForTest(t)

	err := svc.StopBackground()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestThumbnailService_HandleShutdown_NotRunning(t *testing.T) {
	svc, _ := newThumbnailServiceForTest(t)

	err := svc.HandleShutdown()
	require.NoError(t, err)
}

func TestThumbnailService_GetBackgroundLogs_Empty(t *testing.T) {
	svc, _ := newThumbnailServiceForTest(t)
	logs := svc.GetBackgroundLogs()
	assert.Empty(t, logs)
}
