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

// globalWriteDB is a dedicated single-connection write database for SQLite serialized writes.
var globalWriteDB *gorm.DB

// FTS5Available indicates whether FTS5 full-text search is available
var FTS5Available bool

// Init initializes the database with a three-tier connection architecture:
//   - Main pool (4 connections): API read queries (SELECT)
//   - WriteQueue (1 connection): all DB writes, serialized via globalWriteDB
//   - Background pool (1 connection): merge suggestion reads (created separately via NewBackgroundDB)
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
		// _journal_mode=WAL: 启用 WAL 模式提升并发性能（读写互不阻塞）
		// _busy_timeout=60000: 60秒 busy timeout，NAS 慢速 I/O 需要更长等待
		// _synchronous=NORMAL: 在 WAL 模式下提供性能和持久性的平衡
		// _cache_size=-64000: 64MB 缓存（负值表示以 KB 为单位）
		// _temp_store=memory: 临时表存储在内存中
		// 注意：不设置 _txlock=immediate，使用 SQLite 默认的 deferred 模式。
		// WAL 模式下 immediate 会让所有事务（包括只读查询）竞争写锁，
		// 导致后台长任务持锁期间 4 个连接全部阻塞、连接池耗尽、API 无响应。
		sqlitePath := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=60000&_synchronous=NORMAL&_cache_size=-64000&_temp_store=memory",
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

		// Main read pool: serves API SELECT queries (4 connections for concurrency)
		// WriteQueue handles all writes separately via globalWriteDB
		// MaxOpenConns > 1 lets read requests proceed while writes are in progress
		sqlDB.SetMaxOpenConns(4)
		sqlDB.SetMaxIdleConns(2)
		sqlDB.SetConnMaxLifetime(time.Hour)

		// WriteQueue write connection: single connection for serialized writes
		// All DB writes go through WriteQueue → globalWriteDB, never through the main pool
		writePath := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=60000&_synchronous=NORMAL&_cache_size=-64000&_temp_store=memory",
			cfg.Path)
		writeDB, wErr := gorm.Open(sqlite.Open(writePath), gormConfig)
		if wErr != nil {
			return nil, fmt.Errorf("open write connection: %w", wErr)
		}
		writeSQL, wErr := writeDB.DB()
		if wErr != nil {
			return nil, wErr
		}
		writeDB.Exec("PRAGMA foreign_keys=ON")
		writeSQL.SetMaxOpenConns(1)
		writeSQL.SetMaxIdleConns(1)
		writeSQL.SetConnMaxLifetime(time.Hour)
		globalWriteDB = writeDB

		// 初始化 WriteQueue
		wq := InitWriteQueue()
		wq.SetBatchFlushFn(func(ops []WriteOp) error {
			return writeDB.Transaction(func(tx *gorm.DB) error {
				for _, op := range ops {
					if err := op.Fn(); err != nil {
						return err
					}
				}
				return nil
			})
		})

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

// GetWriteDB returns the dedicated write connection (single connection, serialized access)
func GetWriteDB() *gorm.DB {
	return globalWriteDB
}

// NewBackgroundDB creates a dedicated SQLite connection pool for background read-only tasks
// (e.g. personMergeSuggestionService, identityProfileCoordinator). With all writes going
// through WriteQueue, this pool only performs reads, so it may hold multiple connections
// to allow concurrent read-only workers (identity profile coordinator runs up to 4 build
// workers, each calling ListProfileFaces concurrently). SQLite in WAL mode supports
// concurrent readers without locking each other.
//
// maxConns is sized to cover the identity profile build workers (up to 4) plus a margin
// for the merge-suggestion background reads, so a worker never blocks on the pool.
const backgroundDBMaxOpenConns = 8

func NewBackgroundDB(cfg config.DatabaseConfig) (*gorm.DB, error) {
	if cfg.Type != "sqlite" {
		return nil, fmt.Errorf("NewBackgroundDB: unsupported database type: %s", cfg.Type)
	}

	gormConfig := &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
		Logger:  gormlogger.Default.LogMode(gormlogger.Silent), // background pool: silent logging
	}

	sqlitePath := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=60000&_synchronous=NORMAL&_cache_size=-64000&_temp_store=memory",
		cfg.Path)
	db, err := gorm.Open(sqlite.Open(sqlitePath), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("open background sqlite: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	db.Exec("PRAGMA foreign_keys=ON")
	sqlDB.SetMaxOpenConns(backgroundDBMaxOpenConns)
	sqlDB.SetMaxIdleConns(backgroundDBMaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Hour)

	return db, nil
}

// AutoMigrate 自动迁移数据库表
func AutoMigrate(db *gorm.DB) error {
	models := []interface{}{
		&model.Photo{},
		&model.PhotoTag{},
		&model.PhotoTagStats{},
		&model.Person{},
		&model.Face{},
		&model.PeopleJob{},
		&model.PeopleMergeJob{},
		&model.PersonMergeSuggestion{},
		&model.PersonMergeSuggestionItem{},
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
		&model.CannotLinkConstraint{},
		&model.PersonIdentityProfile{},
		&model.PersonIdentityCenter{},
		&model.PersonIdentityCenterMember{},
		&model.PeopleFeedbackEvent{},
		&model.FaceExclusion{},
		&model.FaceQualityEvent{},
		&model.FaceQualityRescoreRun{},
		&model.FaceQualityRescoreItem{},
		&model.PeopleIdentityDecision{},
		&model.PersonPhoto{},
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

	if err := migratePhotoTagStatsTable(db); err != nil {
		log.Printf("[database] warning: photo_tag_stats migration failed: %v", err)
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

	if err := migratePeopleFeedbackIndexes(db); err != nil {
		log.Printf("[database] warning: people feedback index migration failed: %v", err)
	}

	if err := migrateFaceRetryCount(db); err != nil {
		log.Printf("[database] warning: face retry_count migration failed: %v", err)
	}

	if err := migrateFaceEmbeddingBinary(db); err != nil {
		log.Printf("[database] warning: face embedding binary migration failed: %v", err)
	}

	if err := migrateMergeSuggestionIndex(db); err != nil {
		log.Printf("[database] warning: merge suggestion index migration failed: %v", err)
	}

	if err := migratePeopleJobsCleanupIndex(db); err != nil {
		log.Printf("[database] warning: people jobs cleanup index migration failed: %v", err)
	}

	if err := migrateFaceExclusionColumns(db); err != nil {
		log.Printf("[database] warning: face exclusion columns migration failed: %v", err)
	}

	if err := migrateFaceQualityColumns(db); err != nil {
		log.Printf("[database] warning: face quality columns migration failed: %v", err)
	}

	if err := migrateFaceQualityEvidenceOrigin(db); err != nil {
		log.Printf("[database] warning: face quality evidence origin migration failed: %v", err)
	}

	if err := migrateFaceQualityRescoreRunMeta(db); err != nil {
		log.Printf("[database] warning: face quality rescore run meta migration failed: %v", err)
	}

	if err := migrateFaceQualityEvidencePipeline(db); err != nil {
		log.Printf("[database] warning: face quality evidence pipeline migration failed: %v", err)
	}

	if err := migrateFaceQualityRescoreRunPipeline(db); err != nil {
		log.Printf("[database] warning: face quality rescore run pipeline migration failed: %v", err)
	}

	if err := migratePersonPhotosTable(db); err != nil {
		log.Printf("[database] warning: person_photos migration failed: %v", err)
	}

	if err := migratePhotosCursorIndex(db); err != nil {
		log.Printf("[database] warning: photos cursor index migration failed: %v", err)
	}

	if err := migratePersonFaceCursorIndex(db); err != nil {
		log.Printf("[database] warning: person face cursor index migration failed: %v", err)
	}

	return nil
}

// migrateFaceExclusionColumns adds exclusion_reason and excluded_at columns to faces table
// for existing databases. New databases get these columns via AutoMigrate.
func migrateFaceExclusionColumns(db *gorm.DB) error {
	const migrationKey = "migration.face_exclusion_columns_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] adding face exclusion columns...")

	if !db.Migrator().HasColumn(&model.Face{}, "exclusion_reason") {
		if err := db.Exec("ALTER TABLE faces ADD COLUMN exclusion_reason VARCHAR(20) DEFAULT ''").Error; err != nil {
			return fmt.Errorf("add exclusion_reason column: %w", err)
		}
	}

	if !db.Migrator().HasColumn(&model.Face{}, "excluded_at") {
		if err := db.Exec("ALTER TABLE faces ADD COLUMN excluded_at DATETIME").Error; err != nil {
			return fmt.Errorf("add excluded_at column: %w", err)
		}
	}

	log.Printf("[database] face exclusion columns added")
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// migrateFaceQualityColumns 给 faces 表补充质检证据快照列，供审核页直接读取。
// face_quality_events 表由 AutoMigrate 创建；此迁移仅补列，幂等。
func migrateFaceQualityColumns(db *gorm.DB) error {
	const migrationKey = "migration.face_quality_columns_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] adding face quality columns...")

	type colSpec struct {
		name string
		ddl  string
	}
	specs := []colSpec{
		{"face_validity_score", "ALTER TABLE faces ADD COLUMN face_validity_score REAL NOT NULL DEFAULT 0"},
		{"quality_reasons", "ALTER TABLE faces ADD COLUMN quality_reasons VARCHAR(255) DEFAULT ''"},
		{"quality_rule_version", "ALTER TABLE faces ADD COLUMN quality_rule_version VARCHAR(20) DEFAULT ''"},
		{"quality_model_version", "ALTER TABLE faces ADD COLUMN quality_model_version VARCHAR(40) DEFAULT ''"},
	}
	for _, s := range specs {
		if !db.Migrator().HasColumn(&model.Face{}, s.name) {
			if err := db.Exec(s.ddl).Error; err != nil {
				return fmt.Errorf("add %s column: %w", s.name, err)
			}
		}
	}

	log.Printf("[database] face quality columns added")
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// migrateFaceQualityEvidenceOrigin 给 face_quality_events 增加 evidence_origin / evidence_state /
// rescore_run_id 列，建复合索引与 rescore_run_id 索引，并一次性把“历史回填缺证据”的旧记录
// 标记为 historical_backfill/missing，使其从“待人工审核”移入“历史人脸待补证据”。
//
// 一次性标记的精确条件（与计划 §3.2 一致，不得用分数/规则版本/时间范围推断）：
//   is_current=1 AND source='auto' AND decision='review_required'
//   AND TRIM(COALESCE(evidence_json,''))='' AND evidence_origin=''
//
// 幂等：app_config 标记 migration.face_quality_evidence_origin_v1。索引重建为自愈式（DROP IF EXISTS + CREATE）。
func migrateFaceQualityEvidenceOrigin(db *gorm.DB) error {
	const migrationKey = "migration.face_quality_evidence_origin_v1"

	// 迁移标记存在则直接跳过（避免在已迁移库上重复 DROP/CREATE 索引与重复写标记）。
	var existing model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&existing).Error; err == nil {
		return nil
	}

	// 1) 补列（新库由 AutoMigrate 创建，这里只补旧库）。
	type colSpec struct {
		name string
		ddl  string
	}
	specs := []colSpec{
		{"evidence_origin", "ALTER TABLE face_quality_events ADD COLUMN evidence_origin VARCHAR(32) DEFAULT ''"},
		{"evidence_state", "ALTER TABLE face_quality_events ADD COLUMN evidence_state VARCHAR(24) DEFAULT ''"},
		{"rescore_run_id", "ALTER TABLE face_quality_events ADD COLUMN rescore_run_id INTEGER"},
	}
	for _, s := range specs {
		if !db.Migrator().HasColumn(&model.FaceQualityEvent{}, s.name) {
			if err := db.Exec(s.ddl).Error; err != nil {
				return fmt.Errorf("add %s column: %w", s.name, err)
			}
		}
	}

	// 2) 复合索引 (is_current, evidence_origin, evidence_state, id DESC) 与 rescore_run_id 索引。
	//    自愈式：DROP IF EXISTS + CREATE，避免 GORM AutoMigrate 对已存在索引报错或方向不符。
	idxComposite := "idx_fqe_evidence_origin_state"
	idxRescore := "idx_fqe_rescore_run"
	db.Exec("DROP INDEX IF EXISTS " + idxComposite)
	db.Exec("DROP INDEX IF EXISTS " + idxRescore)
	if err := db.Exec("CREATE INDEX " + idxComposite +
		" ON face_quality_events (is_current, evidence_origin, evidence_state, id DESC)").Error; err != nil {
		return fmt.Errorf("create %s index: %w", idxComposite, err)
	}
	if err := db.Exec("CREATE INDEX " + idxRescore + " ON face_quality_events (rescore_run_id)").Error; err != nil {
		return fmt.Errorf("create %s index: %w", idxRescore, err)
	}

	// 3) 一次性标记历史回填缺证据记录（幂等：evidence_origin='' 才更新，已标记的不再动）。
	res := db.Exec(`UPDATE face_quality_events
		SET evidence_origin = ?, evidence_state = ?
		WHERE is_current = 1
		  AND source = ?
		  AND decision = ?
		  AND TRIM(COALESCE(evidence_json, '')) = ''
		  AND evidence_origin = ''`,
		model.FaceQualityEvidenceOriginHistoricalBackfill,
		model.FaceQualityEvidenceStateMissing,
		model.FaceQualitySourceAuto,
		model.FaceQualityDecisionReviewRequired)
	if res.Error != nil {
		return fmt.Errorf("backfill historical missing evidence marker: %w", res.Error)
	}

	// 4) 回填旧实时记录的 evidence_state：有可解析 evidence_json 但 evidence_state='' 的
	//    is_current 记录，一律标为 available（这些是早期实时检测写入、未填新字段的行；
	//    它们有真实模型证据，应留在 pending_review，而非因空 state 漏出所有队列）。
	//    evidence_origin 不猜（留空），仅修复队列分流所需的 state。
	res2 := db.Exec(`UPDATE face_quality_events
		SET evidence_state = ?
		WHERE is_current = 1
		  AND TRIM(COALESCE(evidence_json, '')) != ''
		  AND evidence_state = ''`,
		model.FaceQualityEvidenceStateAvailable)
	if res2.Error != nil {
		return fmt.Errorf("backfill available evidence state for legacy rows: %w", res2.Error)
	}

	// 写迁移标记（OnConflict 兜底，防止软删除残留导致唯一索引冲突）。
	marker := model.AppConfig{Key: migrationKey, Value: "done"}
	if err := db.Where("key = ?", migrationKey).FirstOrCreate(&marker).Error; err != nil {
		return fmt.Errorf("write migration marker: %w", err)
	}
	return nil
}

// migrateFaceQualityRescoreRunMeta 修复既有运行元数据，使其反映真实的完成状态与最近错误。
// 只更新 face_quality_rescore_runs 元数据，不触碰 Face、排除或审计证据。
//
// 修复内容（计划 §3.1，幂等）：
//  1. status=completed 且 retryable_count>0 的运行 → completed_with_errors。
//  2. last_error 为空时，从该运行任一失败 item 回填最近错误。
//
// 该修复必须使线上 #1（全失败、retryable_count=4733）在发布后显示为“完成但有错误”。
func migrateFaceQualityRescoreRunMeta(db *gorm.DB) error {
	const migrationKey = "migration.face_quality_rescore_run_meta_v1"

	var existing model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&existing).Error; err == nil {
		return nil
	}

	if !db.Migrator().HasTable(&model.FaceQualityRescoreRun{}) {
		return nil
	}

	log.Printf("[database] fixing face_quality_rescore_runs terminal status and last_error...")

	// 1) status=completed 且 retryable_count>0 → completed_with_errors。
	res := db.Exec(`UPDATE face_quality_rescore_runs
		SET status = ?
		WHERE status = ? AND retryable_count > 0`,
		model.FaceQualityRescoreStatusCompletedWithError,
		model.FaceQualityRescoreStatusCompleted)
	if res.Error != nil {
		return fmt.Errorf("fix completed_with_errors status: %w", res.Error)
	}

	// 2) last_error 为空的运行，从其最近一条失败 item 回填错误（子查询取 max(id) 的 last_error）。
	res2 := db.Exec(`UPDATE face_quality_rescore_runs
		SET last_error = (
			SELECT last_error FROM face_quality_rescore_items
			WHERE run_id = face_quality_rescore_runs.id AND last_error != ''
			ORDER BY id DESC LIMIT 1
		)
		WHERE (last_error IS NULL OR last_error = '')
		  AND EXISTS (
			SELECT 1 FROM face_quality_rescore_items
			WHERE run_id = face_quality_rescore_runs.id AND last_error != ''
		  )`)
	if res2.Error != nil {
		return fmt.Errorf("backfill run last_error from items: %w", res2.Error)
	}

	marker := model.AppConfig{Key: migrationKey, Value: "done"}
	if err := db.Where("key = ?", migrationKey).FirstOrCreate(&marker).Error; err != nil {
		return fmt.Errorf("write migration marker: %w", err)
	}
	return nil
}

// migrateFaceQualityEvidencePipeline 给 face_quality_events 增加 evidence_pipeline 列，
// 并把既有行标记为 legacy_v1（v1 同源启发式证据，仅保留供历史追溯）。
//
// 幂等：app_config 标记 migration.face_quality_evidence_pipeline_v1。
// 新实时检测与历史复核路径必须显式填写该列，不允许留空；本迁移只补列与回填旧行。
func migrateFaceQualityEvidencePipeline(db *gorm.DB) error {
	const migrationKey = "migration.face_quality_evidence_pipeline_v1"

	var existing model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&existing).Error; err == nil {
		return nil
	}

	if !db.Migrator().HasTable(&model.FaceQualityEvent{}) {
		return nil
	}

	// 1) 补列（新库由 AutoMigrate 创建，这里只补旧库）。
	if !db.Migrator().HasColumn(&model.FaceQualityEvent{}, "evidence_pipeline") {
		if err := db.Exec("ALTER TABLE face_quality_events ADD COLUMN evidence_pipeline VARCHAR(20) DEFAULT ''").Error; err != nil {
			return fmt.Errorf("add evidence_pipeline column: %w", err)
		}
	}

	// 2) 回填旧行为 legacy_v1（仅 evidence_pipeline='' 的行）。
	//    v1 的 score-known-faces 在已旋转展示缩略图上复用同一套 InsightFace 检测，
	//    属同源启发式证据，不得作为 v2 自动隔离或人工判断依据。
	res := db.Exec(`UPDATE face_quality_events
		SET evidence_pipeline = ?
		WHERE evidence_pipeline = '' OR evidence_pipeline IS NULL`,
		model.FaceQualityEvidencePipelineLegacyV1)
	if res.Error != nil {
		return fmt.Errorf("backfill legacy_v1 evidence_pipeline: %w", res.Error)
	}

	// 3) evidence_pipeline 索引（便于 v2 选目标查询排除已有 independent_v2 事件的 Face）。
	idxPipeline := "idx_fqe_evidence_pipeline"
	db.Exec("DROP INDEX IF EXISTS " + idxPipeline)
	if err := db.Exec("CREATE INDEX " + idxPipeline +
		" ON face_quality_events (face_id, evidence_pipeline, is_current)").Error; err != nil {
		return fmt.Errorf("create %s index: %w", idxPipeline, err)
	}

	marker := model.AppConfig{Key: migrationKey, Value: "done"}
	if err := db.Where("key = ?", migrationKey).FirstOrCreate(&marker).Error; err != nil {
		return fmt.Errorf("write migration marker: %w", err)
	}
	return nil
}

// migrateFaceQualityRescoreRunPipeline 给 face_quality_rescore_runs 增加 pipeline_version 与 target_scope 列，
// 并把既有运行标记为 legacy_v1 + historical_backfill_missing。
//
// 幂等：app_config 标记 migration.face_quality_rescore_run_pipeline_v1。
// v2 任务创建的运行固定为 independent_v2 + all_non_manual_faces_without_independent_v2。
func migrateFaceQualityRescoreRunPipeline(db *gorm.DB) error {
	const migrationKey = "migration.face_quality_rescore_run_pipeline_v1"

	var existing model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&existing).Error; err == nil {
		return nil
	}

	if !db.Migrator().HasTable(&model.FaceQualityRescoreRun{}) {
		return nil
	}

	// 1) 补列。
	if !db.Migrator().HasColumn(&model.FaceQualityRescoreRun{}, "pipeline_version") {
		if err := db.Exec("ALTER TABLE face_quality_rescore_runs ADD COLUMN pipeline_version VARCHAR(20) NOT NULL DEFAULT 'legacy_v1'").Error; err != nil {
			return fmt.Errorf("add pipeline_version column: %w", err)
		}
	}
	if !db.Migrator().HasColumn(&model.FaceQualityRescoreRun{}, "target_scope") {
		if err := db.Exec("ALTER TABLE face_quality_rescore_runs ADD COLUMN target_scope VARCHAR(64) NOT NULL DEFAULT ''").Error; err != nil {
			return fmt.Errorf("add target_scope column: %w", err)
		}
	}

	// 2) 回填既有运行为 legacy_v1 + historical_backfill_missing。
	//    v1 只从 historical_backfill + missing 事件选目标，成功补证据后事件转 historical_rescore+available，
	//    后续 full 运行无法复用同一批目标——这正是 v2 要修正的语义。
	res := db.Exec(`UPDATE face_quality_rescore_runs
		SET pipeline_version = ?,
		    target_scope = CASE WHEN target_scope = '' OR target_scope IS NULL THEN ? ELSE target_scope END`,
		model.FaceQualityRescorePipelineLegacyV1,
		model.RescoreTargetScopeV1)
	if res.Error != nil {
		return fmt.Errorf("backfill legacy run pipeline: %w", res.Error)
	}

	marker := model.AppConfig{Key: migrationKey, Value: "done"}
	if err := db.Where("key = ?", migrationKey).FirstOrCreate(&marker).Error; err != nil {
		return fmt.Errorf("write migration marker: %w", err)
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

// migratePhotoTagStatsTable 从 photo_tags 全量聚合生成 photo_tag_stats 预聚合统计表。
// 幂等：已迁移则跳过；ON CONFLICT 保证重复执行也安全。
func migratePhotoTagStatsTable(db *gorm.DB) error {
	const migrationKey = "migration.photo_tag_stats_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil // 已迁移
	}

	log.Printf("[database] building photo_tag_stats from photo_tags...")

	// 全量聚合：每标签一行，photo_count = COUNT(*)
	if err := db.Exec(
		`INSERT INTO photo_tag_stats(tag, photo_count, updated_at)
		 SELECT tag, COUNT(*), ?
		   FROM photo_tags
		  GROUP BY tag
		 ON CONFLICT(tag) DO UPDATE SET photo_count = excluded.photo_count, updated_at = excluded.updated_at`,
		time.Now(),
	).Error; err != nil {
		return err
	}

	// 清理可能的零计数残留
	if err := db.Exec(`DELETE FROM photo_tag_stats WHERE photo_count <= 0`).Error; err != nil {
		return err
	}

	log.Printf("[database] photo_tag_stats built")

	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// RebuildPhotoTagStats 全量重建标签统计表，用于修复历史数据或异常漂移。可重复执行。
// 在单一事务内清空并重新聚合，保证与 photo_tags 完全一致。
func RebuildPhotoTagStats(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM photo_tag_stats`).Error; err != nil {
			return err
		}
		return tx.Exec(
			`INSERT INTO photo_tag_stats(tag, photo_count, updated_at)
			 SELECT tag, COUNT(*), ?
			   FROM photo_tags
			  GROUP BY tag`,
			time.Now(),
		).Error
	})
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
// 加速 GetPendingTasks 中按 status + ai_analyzed + thumbnail_status 的过滤查询。
// v2 增补 analysis_next_retry_at 与 analysis_retry_count 列，覆盖 retry_wait / max-attempt 谓词。
func migrateAnalysisPendingIndex(db *gorm.DB) error {
	const migrationKey = "migration.analysis_pending_index_v1"
	const migrationKeyV2 = "migration.analysis_pending_index_v2"

	// v1 索引（旧库可能已存在）。
	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		// 落到 v2 检查。
	} else {
		log.Printf("[database] creating analysis pending compound index...")
		indexSQL := `CREATE INDEX IF NOT EXISTS idx_photos_analysis_pending
			ON photos(status, ai_analyzed, thumbnail_status, analysis_lock_expired_at)
			WHERE deleted_at IS NULL`
		if err := db.Exec(indexSQL).Error; err != nil {
			return fmt.Errorf("create analysis pending index: %w", err)
		}
		log.Printf("[database] analysis pending index created")
		db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	}

	// v2：重建为覆盖 next_retry_at + retry_count 的更宽复合索引。
	var cfgV2 model.AppConfig
	if err := db.Where("key = ?", migrationKeyV2).First(&cfgV2).Error; err == nil {
		return nil
	}

	log.Printf("[database] rebuilding analysis pending index to cover retry columns...")

	// 删除旧索引再建新索引。SQLite 不支持 DROP IF EXISTS on index 直接重建，
	// 用 DROP INDEX IF EXISTS 安全幂等。
	db.Exec("DROP INDEX IF EXISTS idx_photos_analysis_pending")
	// 删除 GORM tag 历史遗留的单列索引 idx_analysis_retry_ready（v2 复合索引已覆盖该列）。
	db.Exec("DROP INDEX IF EXISTS idx_analysis_retry_ready")

	indexSQLV2 := `CREATE INDEX IF NOT EXISTS idx_photos_analysis_pending
		ON photos(status, ai_analyzed, thumbnail_status, analysis_next_retry_at, analysis_retry_count, analysis_lock_expired_at)
		WHERE deleted_at IS NULL`
	if err := db.Exec(indexSQLV2).Error; err != nil {
		return fmt.Errorf("create analysis pending index v2: %w", err)
	}

	log.Printf("[database] analysis pending index v2 created")
	db.Create(&model.AppConfig{Key: migrationKeyV2, Value: "done"})
	return nil
}

func migratePeopleFeedbackIndexes(db *gorm.DB) error {
	const migrationKey = "migration.people_feedback_indexes_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] creating people feedback indexes...")

	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_faces_feedback_candidates
			ON faces(manual_locked, cluster_status, recluster_generation, cluster_score)`,
		`CREATE INDEX IF NOT EXISTS idx_faces_person_prototypes
			ON faces(person_id, manual_locked DESC, quality_score DESC, confidence DESC, id ASC)`,
	}

	for _, indexSQL := range indexes {
		if err := db.Exec(indexSQL).Error; err != nil {
			return fmt.Errorf("create people feedback index: %w", err)
		}
	}

	log.Printf("[database] people feedback indexes created")
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

func migrateMergeSuggestionIndex(db *gorm.DB) error {
	const migrationKey = "migration.merge_suggestion_index_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] creating merge suggestion targets index...")

	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_people_merge_suggestion_targets
		ON people(category, face_count, id)`).Error; err != nil {
		return fmt.Errorf("create merge suggestion index: %w", err)
	}

	log.Printf("[database] merge suggestion targets index created")
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// migratePeopleJobsCleanupIndex 创建 (status, updated_at) 复合索引，供终态任务清理查询使用，
// 避免扫描全部 people_jobs 记录。GORM tag 也会在新库上创建同名索引，这里用 app_config 标记
// 保证已存在的线上库也能补建。
func migratePeopleJobsCleanupIndex(db *gorm.DB) error {
	const migrationKey = "migration.people_jobs_cleanup_index_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] creating people_jobs cleanup index (status, updated_at)...")

	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_people_jobs_cleanup
		ON people_jobs(status, updated_at)`).Error; err != nil {
		return fmt.Errorf("create people_jobs cleanup index: %w", err)
	}

	log.Printf("[database] people_jobs cleanup index created")
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// migrateFaceRetryCount 添加 faces.retry_count 字段用于聚类退避策略
func migrateFaceRetryCount(db *gorm.DB) error {
	const migrationKey = "migration.face_retry_count_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] adding retry_count column to faces table...")

	// 检查列是否已存在
	if !db.Migrator().HasColumn(&model.Face{}, "retry_count") {
		if err := db.Exec("ALTER TABLE faces ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0").Error; err != nil {
			return fmt.Errorf("add retry_count column: %w", err)
		}
	}

	log.Printf("[database] retry_count column added")
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// migrateFaceEmbeddingBinary converts stored face embeddings from JSON text to raw
// little-endian float32 binary. Binary decoding is ~10x faster and uses ~50% less
// storage, which meaningfully reduces CPU and NAS I/O during ANN index rebuilds.
func migrateFaceEmbeddingBinary(db *gorm.DB) error {
	const migrationKey = "migration.face_embedding_binary_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] converting face embeddings from JSON to binary...")

	type faceRow struct {
		ID        uint
		Embedding []byte
	}

	const batchSize = 200
	var total int64
	var lastID uint

	for {
		var rows []faceRow
		if err := db.Table("faces").
			Select("id, embedding").
			Where("id > ? AND embedding IS NOT NULL AND length(embedding) > 0", lastID).
			Order("id ASC").
			Limit(batchSize).
			Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		// Wrap the batch in a single transaction: one fsync per 200 rows instead
		// of one per row, which makes a large difference on NAS-backed SQLite.
		var batchConverted int64
		if err := db.Transaction(func(tx *gorm.DB) error {
			for _, row := range rows {
				lastID = row.ID
				if len(row.Embedding) == 0 || row.Embedding[0] != '[' {
					continue
				}
				emb := model.DecodeEmbedding(row.Embedding)
				if emb == nil {
					continue
				}
				bin := model.EncodeEmbedding(emb)
				if err := tx.Exec("UPDATE faces SET embedding = ? WHERE id = ?", bin, row.ID).Error; err != nil {
					return err
				}
				batchConverted++
			}
			return nil
		}); err != nil {
			return err
		}
		total += batchConverted
		time.Sleep(10 * time.Millisecond)
	}

	log.Printf("[database] converted %d face embeddings to binary", total)
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// migratePersonPhotosTable 创建 person_photos 派生表、cursor 索引和增量维护 trigger，
// 但不在启动阶段同步回填全部历史数据（回填由后台 P2 任务异步进行，进度记在 app_config）。
//
// 表与索引幂等创建。trigger 在回填开始前安装，接住回填期间所有 faces/photos 写入路径，
// 保证派生表与业务写入在同一事务内更新，不存在“人物修改成功但分页索引未更新”窗口。
// 迁移失败不阻止服务启动（caller 记录 warning）。
func migratePersonPhotosTable(db *gorm.DB) error {
	const tableKey = "migration.person_photos_table_v1"

	// 建表（WITHOUT ROWID：主键即聚簇索引）。
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS person_photos (
		person_id INTEGER NOT NULL,
		photo_id  INTEGER NOT NULL,
		taken_at  DATETIME NULL,
		PRIMARY KEY (person_id, photo_id)
	) WITHOUT ROWID`).Error; err != nil {
		return fmt.Errorf("create person_photos table: %w", err)
	}

	// cursor 索引：(person_id, taken_at DESC, photo_id DESC)，支撑 keyset 分页。
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_person_photos_cursor
		ON person_photos(person_id, taken_at DESC, photo_id DESC)`).Error; err != nil {
		return fmt.Errorf("create idx_person_photos_cursor: %w", err)
	}

	// === 增量维护 trigger ===
	// 只收录 person_id 非空、非 0、cluster_status != 'excluded' 的人脸关联。
	// 多张脸同照片去重由主键 ON CONFLICT 处理。
	// 1) Face INSERT
	if err := db.Exec(`CREATE TRIGGER IF NOT EXISTS person_photos_face_insert
		AFTER INSERT ON faces
		WHEN NEW.person_id IS NOT NULL AND NEW.person_id != 0 AND NEW.cluster_status != 'excluded'
		BEGIN
			INSERT INTO person_photos(person_id, photo_id, taken_at)
			SELECT NEW.person_id, NEW.photo_id, p.taken_at
			FROM photos p WHERE p.id = NEW.photo_id
			ON CONFLICT(person_id, photo_id) DO NOTHING;
		END`).Error; err != nil {
		return fmt.Errorf("create person_photos_face_insert trigger: %w", err)
	}

	// 2) Face UPDATE：person_id / photo_id / cluster_status 任一变化，都可能需要移除旧关联、
	//    插入新关联。条件触发仅在这三字段变化时执行，避免无关字段更新（如 retry_count）产生噪音。
	if err := db.Exec(`CREATE TRIGGER IF NOT EXISTS person_photos_face_update
		AFTER UPDATE OF person_id, photo_id, cluster_status ON faces
		BEGIN
			-- 旧关联：若旧 face 曾有效（person_id 非空非 0 且非 excluded），且同人物同照片
			-- 不再有其他有效 face，则删除派生记录。
			DELETE FROM person_photos
			WHERE person_id = OLD.person_id AND photo_id = OLD.photo_id
			  AND OLD.person_id IS NOT NULL AND OLD.person_id != 0
			  AND OLD.cluster_status != 'excluded'
			  AND NOT EXISTS (
				SELECT 1 FROM faces f
				WHERE f.person_id = OLD.person_id AND f.photo_id = OLD.photo_id
				  AND f.id != OLD.id AND f.cluster_status != 'excluded'
			  );
			-- 新关联：若新 face 有效，插入（ON CONFLICT 去重）。
			INSERT INTO person_photos(person_id, photo_id, taken_at)
			SELECT NEW.person_id, NEW.photo_id, p.taken_at
			FROM photos p WHERE p.id = NEW.photo_id
			  AND NEW.person_id IS NOT NULL AND NEW.person_id != 0
			  AND NEW.cluster_status != 'excluded'
			ON CONFLICT(person_id, photo_id) DO NOTHING;
		END`).Error; err != nil {
		return fmt.Errorf("create person_photos_face_update trigger: %w", err)
	}

	// 3) Face DELETE：删除 face 后，若同人物同照片无其他有效 face，移除派生记录。
	if err := db.Exec(`CREATE TRIGGER IF NOT EXISTS person_photos_face_delete
		AFTER DELETE ON faces
		WHEN OLD.person_id IS NOT NULL AND OLD.person_id != 0 AND OLD.cluster_status != 'excluded'
		BEGIN
			DELETE FROM person_photos
			WHERE person_id = OLD.person_id AND photo_id = OLD.photo_id
			  AND NOT EXISTS (
				SELECT 1 FROM faces f
				WHERE f.person_id = OLD.person_id AND f.photo_id = OLD.photo_id
				  AND f.cluster_status != 'excluded'
			  );
		END`).Error; err != nil {
		return fmt.Errorf("create person_photos_face_delete trigger: %w", err)
	}

	// 4) Photo taken_at UPDATE：同步刷新 person_photos.taken_at。
	if err := db.Exec(`CREATE TRIGGER IF NOT EXISTS person_photos_photo_takenat_update
		AFTER UPDATE OF taken_at ON photos
		BEGIN
			UPDATE person_photos SET taken_at = NEW.taken_at
			WHERE person_photos.photo_id = NEW.id;
		END`).Error; err != nil {
		return fmt.Errorf("create person_photos_photo_takenat_update trigger: %w", err)
	}

	// 5) Photo DELETE：清理对应派生记录。
	if err := db.Exec(`CREATE TRIGGER IF NOT EXISTS person_photos_photo_delete
		AFTER DELETE ON photos
		BEGIN
			DELETE FROM person_photos WHERE photo_id = OLD.id;
		END`).Error; err != nil {
		return fmt.Errorf("create person_photos_photo_delete trigger: %w", err)
	}

	// 标记表/trigger 已安装（与回填进度 status 分开记录）。
	var cfg model.AppConfig
	if err := db.Where("key = ?", tableKey).First(&cfg).Error; err == nil {
		return nil // 已安装
	}
	db.Create(&model.AppConfig{Key: tableKey, Value: "done"})
	return nil
}

// migratePhotosCursorIndex 为照片管理页连续浏览的 keyset 分页查询创建复合索引。
//
// 连续浏览固定排序 (taken_at DESC, id DESC)，并按 status / deleted_at 过滤。
// 该索引覆盖过滤谓词 + 排序键，避免 ORDER BY 产生临时排序树（EXPLAIN QUERY PLAN
// 应出现 idx_photos_cursor）。
//
// 索引为全量（不含 WHERE 子句）：active 与 excluded（回收站）照片都纳入，
// 这样回收站连续浏览同样命中该索引，不会退化为全表扫描。
//
// 用 CREATE INDEX IF NOT EXISTS 幂等创建，不重建业务数据、不阻塞启动。失败仅 warning。
func migratePhotosCursorIndex(db *gorm.DB) error {
	const migrationKey = "migration.photos_cursor_index_v1"

	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err == nil {
		return nil
	}

	log.Printf("[database] creating photos cursor index (status, deleted_at, taken_at DESC, id DESC)...")

	// 全量索引：active 与 excluded 照片均收录。连续浏览既覆盖默认 active，也覆盖
	// 回收站 excluded 筛选，两者都能命中该索引。
	indexSQL := `CREATE INDEX IF NOT EXISTS idx_photos_cursor
		ON photos(status, deleted_at, taken_at DESC, id DESC)`
	if err := db.Exec(indexSQL).Error; err != nil {
		return fmt.Errorf("create photos cursor index: %w", err)
	}

	log.Printf("[database] photos cursor index created")
	db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	return nil
}

// migratePersonFaceCursorIndex 为人物人脸详情页 cursor 分页创建专用 partial index。
//
// 人物详情页第一页查询固定排序 (quality_score DESC, id ASC) 并按 person_id 等值、
// cluster_status != 'excluded' 过滤。冷缓存下该人物全部人脸需读出后临时排序
// (USE TEMP B-TREE FOR ORDER BY)，导致首屏数秒至数十秒耗时。
//
// 该 partial index 覆盖 person_id 等值谓词 + 排序键，且仅收录非 excluded 人脸：
//
//	CREATE INDEX idx_faces_person_quality_cursor
//	ON faces(person_id, quality_score DESC, id ASC)
//	WHERE cluster_status != 'excluded'
//
// 第一页可直接按索引顺序读前 page_size+1 条，后续页的 keyset 谓词
// (quality_score < ? OR (quality_score = ? AND id > ?)) 也可走同一索引范围扫描，
// 不再创建临时排序树。EXPLAIN QUERY PLAN 应出现 idx_faces_person_quality_cursor，
// 且不得出现 USE TEMP B-TREE。
//
// 迁移标记 migration.person_face_cursor_index_v1 仅用于记录是否首次完成日志，
// 不作为跳过 CREATE INDEX 的条件：CREATE INDEX IF NOT EXISTS 始终执行，
// 索引被意外删除后下次启动可自愈重建。失败仅 warning，不阻塞启动。
func migratePersonFaceCursorIndex(db *gorm.DB) error {
	const migrationKey = "migration.person_face_cursor_index_v1"

	var cfg model.AppConfig
	alreadyMarked := db.Where("key = ?", migrationKey).First(&cfg).Error == nil

	if !alreadyMarked {
		log.Printf("[database] creating person face cursor index (person_id, quality_score DESC, id ASC)...")
	}

	start := time.Now()
	indexSQL := `CREATE INDEX IF NOT EXISTS idx_faces_person_quality_cursor
		ON faces(person_id, quality_score DESC, id ASC)
		WHERE cluster_status != 'excluded'`
	if err := db.Exec(indexSQL).Error; err != nil {
		return fmt.Errorf("create person face cursor index: %w", err)
	}
	elapsed := time.Since(start)

	if !alreadyMarked {
		log.Printf("[database] person face cursor index created (elapsed=%s)", elapsed)
		db.Create(&model.AppConfig{Key: migrationKey, Value: "done"})
	} else {
		log.Printf("[database] person face cursor index verified (elapsed=%s)", elapsed)
	}
	return nil
}
