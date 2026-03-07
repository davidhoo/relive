package database

import (
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// 全局数据库连接
var globalDB *gorm.DB

// Init 初始化数据库
func Init(cfg config.DatabaseConfig) (*gorm.DB, error) {
	var db *gorm.DB
	var err error

	// GORM 配置
	gormConfig := &gorm.Config{
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
		DisableForeignKeyConstraintWhenMigrating: false,
	}

	// 设置日志模式
	if cfg.LogMode {
		gormConfig.Logger = gormlogger.Default.LogMode(gormlogger.Info)
	} else {
		gormConfig.Logger = gormlogger.Default.LogMode(gormlogger.Silent)
	}

	// 根据数据库类型初始化
	switch cfg.Type {
	case "sqlite":
		db, err = gorm.Open(sqlite.Open(cfg.Path), gormConfig)
		if err != nil {
			return nil, fmt.Errorf("open sqlite database: %w", err)
		}

		// SQLite 优化配置
		sqlDB, err := db.DB()
		if err != nil {
			return nil, err
		}

		// 启用 WAL 模式（提升并发性能)
		db.Exec("PRAGMA journal_mode=WAL")

		// 设置 busy_timeout（避免 database locked 错误)
		db.Exec("PRAGMA busy_timeout=30000")

		// 启用外键约束
		db.Exec("PRAGMA foreign_keys=ON")

		// 设置连接池（SQLite 写是单线程，连接数不宜过多）
		sqlDB.SetMaxOpenConns(10)
		sqlDB.SetMaxIdleConns(3)
		sqlDB.SetConnMaxLifetime(5 * time.Minute)

	case "postgres":
		// TODO: PostgreSQL 支持（后续添加)
		return nil, fmt.Errorf("postgres not implemented yet")

	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}

	// 保存全局引用
	globalDB = db

	// 自动迁移
	if cfg.AutoMigrate {
		if err := AutoMigrate(db); err != nil {
			return nil, fmt.Errorf("auto migrate: %w", err)
		}
	}

	return db, nil
}

// GetDB returns the database connection
func GetDB() *gorm.DB {
	return globalDB
}

// AutoMigrate 自动迁移数据库表
func AutoMigrate(db *gorm.DB) error {
	models := []interface{}{
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
	}

	if err := db.AutoMigrate(models...); err != nil {
		return err
	}

	return nil
}
