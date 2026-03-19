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
		// _busy_timeout=60000: 60秒 busy timeout，NAS 慢速 I/O 需要更长等待
		// _synchronous=NORMAL: 在 WAL 模式下提供性能和持久性的平衡
		// _cache_size=-64000: 64MB 缓存（负值表示以 KB 为单位）
		// _temp_store=memory: 临时表存储在内存中
		// _txlock=immediate: 写事务立即获取写锁，避免 deferred→write 升级死锁
		sqlitePath := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=60000&_synchronous=NORMAL&_cache_size=-64000&_temp_store=memory&_txlock=immediate",
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
		&model.Event{},
	}

	if err := migrateDeviceLastSeenColumn(db); err != nil {
		return err
	}

	// SQLite 不支持 ALTER TABLE ADD CHECK，GORM 会用临时表重建方式迁移，
	// DROP 原表时会触发外键约束失败，因此迁移期间临时关闭外键检查。
	db.Exec("PRAGMA foreign_keys=OFF")
	defer db.Exec("PRAGMA foreign_keys=ON")

	// 在 AutoMigrate 之前修复枚举字段无效值，
	// 否则 GORM 重建表复制数据时会违反新的 CHECK 约束。
	fixEnumBeforeMigrate(db)

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

	if err := migrateEnumValidation(db); err != nil {
		return err
	}

	if err := migrateFTS5ConditionalTrigger(db); err != nil {
		log.Printf("[database] warning: FTS5 conditional trigger migration failed: %v", err)
	}

	if err := migrateAnalysisPendingIndex(db); err != nil {
		log.Printf("[database] warning: analysis pending index migration failed: %v", err)
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

// fixEnumBeforeMigrate 在 AutoMigrate 之前修复所有枚举字段的无效值，
// 确保 GORM 重建表（添加 CHECK 约束）时复制的数据合法。
// 静默执行，表不存在时跳过。
func fixEnumBeforeMigrate(db *gorm.DB) {
	migrator := db.Migrator()

	// photos
	if migrator.HasTable("photos") {
		db.Exec("UPDATE photos SET status = ? WHERE status IS NULL OR status = ''", model.PhotoStatusActive)
		db.Exec("UPDATE photos SET thumbnail_status = ? WHERE thumbnail_status IS NULL OR thumbnail_status = ''", model.ThumbnailStatusNone)
		db.Exec("UPDATE photos SET geocode_status = ? WHERE geocode_status IS NULL OR geocode_status = ''", model.GeocodeStatusNone)
	}

	// analysis_runtime_leases
	if migrator.HasTable("analysis_runtime_leases") {
		db.Exec("UPDATE analysis_runtime_leases SET owner_type = ? WHERE owner_type IS NULL OR owner_type = ''", model.AnalysisRuntimeStatusIdle)
		db.Exec("UPDATE analysis_runtime_leases SET status = ? WHERE status IS NULL OR status = ''", model.AnalysisRuntimeStatusIdle)
	}

	// devices
	if migrator.HasTable("devices") {
		db.Exec("UPDATE devices SET device_type = ? WHERE device_type IS NULL OR device_type = ''", model.DeviceTypeEmbedded)
	}
}

// migrateFTS5ConditionalTrigger 将 FTS5 UPDATE 触发器改为条件触发
// 只在 FTS 索引字段（file_name, description, caption, location）变化时触发，
// 避免更新 analysis_lock_id 等非索引字段时产生不必要的 FTS5 写操作。
func migrateFTS5ConditionalTrigger(db *gorm.DB) error {
	if !FTS5Available {
		return nil
	}

	const migrationKey = "migration.fts5_conditional_trigger_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] updating FTS5 trigger to conditional mode...")

	// 删除旧的无条件触发器
	if err := db.Exec("DROP TRIGGER IF EXISTS photos_fts_update").Error; err != nil {
		return fmt.Errorf("drop old FTS5 trigger: %w", err)
	}

	// 创建新的条件触发器：只在 FTS 索引字段变化时触发
	conditionalTrigger := `CREATE TRIGGER IF NOT EXISTS photos_fts_update AFTER UPDATE ON photos
		WHEN old.file_name IS NOT new.file_name
		  OR old.description IS NOT new.description
		  OR old.caption IS NOT new.caption
		  OR old.location IS NOT new.location
		BEGIN
			INSERT INTO photos_fts(photos_fts, rowid, file_name, description, caption, location)
			VALUES ('delete', old.id, COALESCE(old.file_name,''), COALESCE(old.description,''), COALESCE(old.caption,''), COALESCE(old.location,''));
			INSERT INTO photos_fts(rowid, file_name, description, caption, location)
			VALUES (new.id, COALESCE(new.file_name,''), COALESCE(new.description,''), COALESCE(new.caption,''), COALESCE(new.location,''));
		END`

	if err := db.Exec(conditionalTrigger).Error; err != nil {
		return fmt.Errorf("create conditional FTS5 trigger: %w", err)
	}

	log.Printf("[database] FTS5 conditional trigger migration completed")
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// migrateAnalysisPendingIndex 为待分析查询添加复合索引
// 加速 GetPendingTasks 中按 status + ai_analyzed + thumbnail_status 的过滤查询
func migrateAnalysisPendingIndex(db *gorm.DB) error {
	const migrationKey = "migration.analysis_pending_index_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] creating analysis pending compound index...")

	indexSQL := `CREATE INDEX IF NOT EXISTS idx_photos_analysis_pending
		ON photos(status, ai_analyzed, thumbnail_status, analysis_lock_expired_at)
		WHERE deleted_at IS NULL`

	if err := db.Exec(indexSQL).Error; err != nil {
		return fmt.Errorf("create analysis pending index: %w", err)
	}

	log.Printf("[database] analysis pending index created")
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// migrateEnumValidation 修复枚举字段空值
func migrateEnumValidation(db *gorm.DB) error {
	const migrationKey = "migration.enum_validation_v1"

	// 检查是否已迁移
	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] running enum validation migration...")

	// 修复 thumbnail_status 空值
	if err := db.Exec("UPDATE photos SET thumbnail_status = ? WHERE thumbnail_status IS NULL OR thumbnail_status = ''", model.ThumbnailStatusNone).Error; err != nil {
		return fmt.Errorf("fix thumbnail_status: %w", err)
	}

	// 修复 geocode_status 空值
	if err := db.Exec("UPDATE photos SET geocode_status = ? WHERE geocode_status IS NULL OR geocode_status = ''", model.GeocodeStatusNone).Error; err != nil {
		return fmt.Errorf("fix geocode_status: %w", err)
	}

	log.Printf("[database] enum validation migration completed")
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}
