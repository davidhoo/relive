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
	// 重要：先迁移旧表（esp32_devices → devices），再执行 AutoMigrate
	// 因为 DisplayRecord 表有外键引用 devices 表
	if err := migrateESP32DevicesToDevices(db); err != nil {
		return err
	}

	models := []interface{}{
		&model.Photo{},
		&model.DisplayRecord{},
		&model.Device{}, // 改为 Device
		&model.AppConfig{},
		&model.City{},
		&model.User{},
		&model.APIKey{},
	}

	if err := db.AutoMigrate(models...); err != nil {
		return err
	}

	return nil
}

// migrateESP32DevicesToDevices 迁移旧的 esp32_devices 表到 devices 表
func migrateESP32DevicesToDevices(db *gorm.DB) error {
	// 检查旧表是否存在
	var oldTableExists bool
	err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='esp32_devices'").Scan(&oldTableExists).Error
	if err != nil {
		return err
	}

	if !oldTableExists {
		return nil // 旧表不存在，无需迁移
	}

	// 检查新表是否存在
	var newTableExists bool
	err = db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='devices'").Scan(&newTableExists).Error
	if err != nil {
		return err
	}

	if newTableExists {
		// 新表已存在，可能是已迁移过，跳过
		return nil
	}

	// 重命名表
	if err := db.Exec("ALTER TABLE esp32_devices RENAME TO devices").Error; err != nil {
		return fmt.Errorf("rename table esp32_devices to devices: %w", err)
	}

	// 添加新字段（使用默认值）
	if err := db.Exec("ALTER TABLE devices ADD COLUMN device_type VARCHAR(20) DEFAULT 'esp32'").Error; err != nil {
		// 字段可能已存在，忽略错误
		if err.Error() != "duplicate column name: device_type" {
			return fmt.Errorf("add column device_type: %w", err)
		}
	}

	if err := db.Exec("ALTER TABLE devices ADD COLUMN hardware_model VARCHAR(50)").Error; err != nil {
		if err.Error() != "duplicate column name: hardware_model" {
			return fmt.Errorf("add column hardware_model: %w", err)
		}
	}

	if err := db.Exec("ALTER TABLE devices ADD COLUMN platform VARCHAR(20) DEFAULT 'embedded'").Error; err != nil {
		if err.Error() != "duplicate column name: platform" {
			return fmt.Errorf("add column platform: %w", err)
		}
	}

	return nil
}
