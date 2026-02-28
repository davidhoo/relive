package repository

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDB 创建测试数据库
func setupTestDB(t *testing.T) *gorm.DB {
	// 使用内存数据库
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}

	// 自动迁移
	err = db.AutoMigrate(
		&model.Photo{},
		&model.DisplayRecord{},
		&model.ESP32Device{},
		&model.AppConfig{},
		&model.City{},
	)
	if err != nil {
		t.Fatalf("Failed to migrate test database: %v", err)
	}

	return db
}

// teardownTestDB 清理测试数据库
func teardownTestDB(db *gorm.DB) {
	sqlDB, _ := db.DB()
	if sqlDB != nil {
		sqlDB.Close()
	}
}
