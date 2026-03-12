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
		// SQLite 连接参数优化
		// _journal_mode=WAL: 启用 WAL 模式提升并发性能
		// _busy_timeout=30000: 30秒 busy timeout，自动重试锁定的操作
		// _synchronous=NORMAL: 在 WAL 模式下提供性能和持久性的平衡
		// _cache_size=-64000: 64MB 缓存（负值表示以 KB 为单位）
		// _temp_store=memory: 临时表存储在内存中
		sqlitePath := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=30000&_synchronous=NORMAL&_cache_size=-64000&_temp_store=memory",
			cfg.Path)
		db, err = gorm.Open(sqlite.Open(sqlitePath), gormConfig)
		if err != nil {
			return nil, fmt.Errorf("open sqlite database: %w", err)
		}

		// SQLite 优化配置
		sqlDB, err := db.DB()
		if err != nil {
			return nil, err
		}

		// 启用外键约束（其他参数已在连接字符串中设置）
		db.Exec("PRAGMA foreign_keys=ON")

		// 设置连接池（WAL 模式下支持并发读，写仍是串行的）
		// MaxOpenConns > 1 让读请求不被写事务阻塞
		sqlDB.SetMaxOpenConns(4)
		sqlDB.SetMaxIdleConns(2)
		sqlDB.SetConnMaxLifetime(time.Hour)

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
		&model.ScanJob{},
		&model.ThumbnailJob{},
		&model.GeocodeJob{},
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
	}

	if err := migrateDeviceLastSeenColumn(db); err != nil {
		return err
	}

	if err := db.AutoMigrate(models...); err != nil {
		return err
	}

	if err := cleanupObsoleteDeviceColumns(db); err != nil {
		return err
	}

	return nil
}

func migrateDeviceLastSeenColumn(db *gorm.DB) error {
	migrator := db.Migrator()
	if !migrator.HasTable(&model.Device{}) {
		return nil
	}
	if migrator.HasColumn(&model.Device{}, "last_seen") {
		return nil
	}
	if !migrator.HasColumn(&model.Device{}, "last_heartbeat") {
		return nil
	}
	return migrator.RenameColumn(&model.Device{}, "last_heartbeat", "last_seen")
}

func cleanupObsoleteDeviceColumns(db *gorm.DB) error {
	migrator := db.Migrator()
	obsoleteColumns := []string{"battery_level", "wifi_rssi"}
	for _, column := range obsoleteColumns {
		if migrator.HasColumn("devices", column) {
			if err := db.Exec(fmt.Sprintf("ALTER TABLE devices DROP COLUMN %s", column)).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
