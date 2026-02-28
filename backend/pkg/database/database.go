package database

import (
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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
		gormConfig.Logger = logger.Default.LogMode(logger.Info)
	} else {
		gormConfig.Logger = logger.Default.LogMode(logger.Silent)
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

		// 启用 WAL 模式（提升并发性能）
		db.Exec("PRAGMA journal_mode=WAL")

		// 设置 busy_timeout（避免 database locked 错误）
		db.Exec("PRAGMA busy_timeout=5000")

		// 启用外键约束
		db.Exec("PRAGMA foreign_keys=ON")

		// 设置连接池
		sqlDB.SetMaxOpenConns(25)
		sqlDB.SetMaxIdleConns(5)
		sqlDB.SetConnMaxLifetime(5 * time.Minute)

	case "postgres":
		// TODO: PostgreSQL 支持（后续添加）
		return nil, fmt.Errorf("postgres not implemented yet")

	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}

	// 自动迁移
	if cfg.AutoMigrate {
		if err := AutoMigrate(db); err != nil {
			return nil, fmt.Errorf("auto migrate: %w", err)
		}
	}

	return db, nil
}

// AutoMigrate 自动迁移数据库表
func AutoMigrate(db *gorm.DB) error {
	models := []interface{}{
		&model.Photo{},
		&model.DisplayRecord{},
		&model.ESP32Device{},
		&model.AppConfig{},
		&model.City{},
	}

	if err := db.AutoMigrate(models...); err != nil {
		return err
	}

	return nil
}
