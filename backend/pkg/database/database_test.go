package database

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateDeviceLastSeenColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	if err := db.Exec(`CREATE TABLE devices (
		id integer primary key autoincrement,
		device_id text,
		name text,
		api_key text,
		last_heartbeat datetime
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	if err := migrateDeviceLastSeenColumn(db); err != nil {
		t.Fatalf("migrate column: %v", err)
	}

	if !db.Migrator().HasColumn(&model.Device{}, "last_seen") {
		t.Fatal("expected last_seen column to exist after migration")
	}
	if db.Migrator().HasColumn(&model.Device{}, "last_heartbeat") {
		t.Fatal("expected last_heartbeat column to be renamed")
	}
}
