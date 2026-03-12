package testutil

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// SetupTestDB creates an in-memory SQLite database with all tables migrated.
// It registers t.Cleanup to close the database automatically.
func SetupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("testutil: open in-memory db: %v", err)
	}

	err = db.AutoMigrate(
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
		&model.ScanJob{},
		&model.ThumbnailJob{},
		&model.GeocodeJob{},
	)
	if err != nil {
		t.Fatalf("testutil: auto-migrate: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})

	return db
}

// SuppressLogger initializes the logger at error level to suppress noise in tests.
// Call this in TestMain or init().
func SuppressLogger() {
	_ = logger.Init(config.LoggingConfig{Level: "error", Console: true})
}
