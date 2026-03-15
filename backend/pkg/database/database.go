package database

import (
	"fmt"
	"log"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/geodata"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// 全局数据库连接
var globalDB *gorm.DB

// FTS5Available indicates whether FTS5 full-text search is available
var FTS5Available bool

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

	// 确保城市数据已加载（从嵌入数据自动导入）
	if err := geodata.EnsureCitiesLoaded(db); err != nil {
		log.Printf("[database] warning: failed to load embedded cities data: %v", err)
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
		&model.PhotoTag{},
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

	if err := migratePhotoStatusColumn(db); err != nil {
		return err
	}

	if err := cleanupObsoleteDeviceColumns(db); err != nil {
		return err
	}

	if err := migratePhotoTagsTable(db); err != nil {
		return err
	}

	if err := migrateFTS5Table(db); err != nil {
		// FTS5 迁移失败不阻塞启动，降级为 LIKE 搜索
		log.Printf("[database] warning: FTS5 migration failed: %v, falling back to LIKE search", err)
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

// migratePhotoStatusColumn 将旧照片的 status 字段设为 active
func migratePhotoStatusColumn(db *gorm.DB) error {
	return db.Exec("UPDATE photos SET status = ? WHERE status IS NULL OR status = ''", model.PhotoStatusActive).Error
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

// migratePhotoTagsTable 从 photos.tags 列迁移数据到 photo_tags 表
func migratePhotoTagsTable(db *gorm.DB) error {
	const migrationKey = "migration.photo_tags_v1"

	// 检查是否已迁移
	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil // 已迁移
	}

	// 批量迁移：从 photos.tags 拆分写入 photo_tags
	log.Printf("[database] migrating photo tags to photo_tags table...")

	const batchSize = 500
	var total int64
	var lastID uint

	for {
		var photos []model.Photo
		err := db.Select("id, tags").
			Where("id > ? AND tags IS NOT NULL AND tags != ''", lastID).
			Order("id ASC").
			Limit(batchSize).
			Find(&photos).Error
		if err != nil {
			return err
		}
		if len(photos) == 0 {
			break
		}

		var records []model.PhotoTag
		for _, p := range photos {
			for _, tag := range model.SplitTags(p.Tags) {
				records = append(records, model.PhotoTag{PhotoID: p.ID, Tag: tag})
			}
			lastID = p.ID
		}
		if len(records) > 0 {
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&records).Error; err != nil {
				return err
			}
			total += int64(len(records))
		}
	}

	log.Printf("[database] migrated %d photo tag records", total)

	// 标记已迁移
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// migrateFTS5Table 创建 FTS5 全文搜索虚拟表和同步触发器
func migrateFTS5Table(db *gorm.DB) error {
	const migrationKey = "migration.photos_fts5_v1"

	// 检查是否已迁移
	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		FTS5Available = true
		return nil
	}

	log.Printf("[database] creating FTS5 full-text search index...")

	// 创建 FTS5 虚拟表（external content 模式）
	fts5SQL := `CREATE VIRTUAL TABLE IF NOT EXISTS photos_fts USING fts5(
		file_name,
		description,
		caption,
		location,
		content='photos',
		content_rowid='id',
		tokenize='unicode61'
	)`
	if err := db.Exec(fts5SQL).Error; err != nil {
		log.Printf("[database] FTS5 not available (SQLite compiled without FTS5 support): %v", err)
		return nil // 不返回错误，降级为 LIKE
	}

	// 全量索引现有数据
	indexSQL := `INSERT INTO photos_fts(rowid, file_name, description, caption, location)
		SELECT id, COALESCE(file_name,''), COALESCE(description,''), COALESCE(caption,''), COALESCE(location,'')
		FROM photos WHERE deleted_at IS NULL`
	if err := db.Exec(indexSQL).Error; err != nil {
		return fmt.Errorf("FTS5 initial index: %w", err)
	}

	// 创建同步触发器
	triggers := []string{
		// INSERT 触发器
		`CREATE TRIGGER IF NOT EXISTS photos_fts_insert AFTER INSERT ON photos BEGIN
			INSERT INTO photos_fts(rowid, file_name, description, caption, location)
			VALUES (new.id, COALESCE(new.file_name,''), COALESCE(new.description,''), COALESCE(new.caption,''), COALESCE(new.location,''));
		END`,
		// UPDATE 触发器（FTS5 external content: 先删旧行再插新行）
		`CREATE TRIGGER IF NOT EXISTS photos_fts_update AFTER UPDATE ON photos BEGIN
			INSERT INTO photos_fts(photos_fts, rowid, file_name, description, caption, location)
			VALUES ('delete', old.id, COALESCE(old.file_name,''), COALESCE(old.description,''), COALESCE(old.caption,''), COALESCE(old.location,''));
			INSERT INTO photos_fts(rowid, file_name, description, caption, location)
			VALUES (new.id, COALESCE(new.file_name,''), COALESCE(new.description,''), COALESCE(new.caption,''), COALESCE(new.location,''));
		END`,
		// DELETE 触发器
		`CREATE TRIGGER IF NOT EXISTS photos_fts_delete AFTER DELETE ON photos BEGIN
			INSERT INTO photos_fts(photos_fts, rowid, file_name, description, caption, location)
			VALUES ('delete', old.id, COALESCE(old.file_name,''), COALESCE(old.description,''), COALESCE(old.caption,''), COALESCE(old.location,''));
		END`,
	}

	for _, trigger := range triggers {
		if err := db.Exec(trigger).Error; err != nil {
			return fmt.Errorf("FTS5 trigger creation: %w", err)
		}
	}

	FTS5Available = true
	log.Printf("[database] FTS5 migration completed")

	// 标记已迁移
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}
