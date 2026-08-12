package database

import (
	"strings"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openMigratedTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	return db
}

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
		last_heartbeat datetime,
		battery_level integer,
		wifi_rssi integer
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

	if err := cleanupObsoleteDeviceColumns(db); err != nil {
		t.Fatalf("cleanup columns: %v", err)
	}
	if db.Migrator().HasColumn(&model.Device{}, "battery_level") {
		t.Fatal("expected battery_level column to be removed")
	}
	if db.Migrator().HasColumn(&model.Device{}, "wifi_rssi") {
		t.Fatal("expected wifi_rssi column to be removed")
	}
}

func TestAutoMigrateAddsPeopleTables(t *testing.T) {
	db := openMigratedTestDB(t)

	for _, table := range []string{"faces", "people", "people_jobs"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected %s table to exist after migration", table)
		}
	}

	if err := db.Exec("INSERT INTO people DEFAULT VALUES").Error; err != nil {
		t.Fatalf("insert default person: %v", err)
	}

	var category string
	if err := db.Raw("SELECT category FROM people LIMIT 1").Scan(&category).Error; err != nil {
		t.Fatalf("query default people category: %v", err)
	}
	if category != "stranger" {
		t.Fatalf("expected default people category stranger, got %q", category)
	}

	queuedAt := time.Now().UTC()
	validStatuses := []string{"pending", "queued", "processing", "completed", "failed", "cancelled"}
	for i, status := range validStatuses {
		err := db.Exec(
			"INSERT INTO people_jobs (photo_id, file_path, status, priority, source, queued_at) VALUES (?, ?, ?, ?, ?, ?)",
			i+1,
			"/tmp/photo.jpg",
			status,
			0,
			"scan",
			queuedAt,
		).Error
		if err != nil {
			t.Fatalf("expected people_jobs status %q to be accepted: %v", status, err)
		}
	}

	if err := db.Exec(
		"INSERT INTO people_jobs (photo_id, file_path, status, priority, source, queued_at) VALUES (?, ?, ?, ?, ?, ?)",
		999,
		"/tmp/photo.jpg",
		"unknown",
		0,
		"scan",
		queuedAt,
	).Error; err == nil {
		t.Fatal("expected invalid people_jobs status to be rejected")
	}
}

func TestAutoMigrateAddsPeopleColumns(t *testing.T) {
	db := openMigratedTestDB(t)

	for _, column := range []string{"face_process_status", "face_count", "top_person_category"} {
		if !db.Migrator().HasColumn(&model.Photo{}, column) {
			t.Fatalf("expected photos.%s column to exist after migration", column)
		}
	}
}

func TestAutoMigrateAddsPhotoPeopleExclusion(t *testing.T) {
	db := openMigratedTestDB(t)

	for _, column := range []string{"people_excluded", "people_exclusion_reason"} {
		if !db.Migrator().HasColumn(&model.Photo{}, column) {
			t.Fatalf("expected photos.%s column to exist after migration", column)
		}
	}

	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", "idx_people_excluded").Scan(&count).Error; err != nil {
		t.Fatalf("query idx_people_excluded: %v", err)
	}
	if count != 1 {
		t.Fatal("expected idx_people_excluded to exist after migration")
	}
}

func TestAutoMigrateAddsPeopleFeedbackIndexes(t *testing.T) {
	db := openMigratedTestDB(t)

	for _, indexName := range []string{
		"idx_faces_feedback_candidates",
		"idx_faces_person_prototypes",
	} {
		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", indexName).Scan(&count).Error; err != nil {
			t.Fatalf("query index %s: %v", indexName, err)
		}
		if count != 1 {
			t.Fatalf("expected index %s to exist after migration", indexName)
		}
	}
}

func TestAutoMigrateAddsPeopleJobsCleanupIndex(t *testing.T) {
	db := openMigratedTestDB(t)

	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", "idx_people_jobs_cleanup").Scan(&count).Error; err != nil {
		t.Fatalf("query idx_people_jobs_cleanup: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected idx_people_jobs_cleanup to exist after migration")
	}
}

func TestAutoMigrateAddsPersonMergeSuggestionTables(t *testing.T) {
	db := openMigratedTestDB(t)

	for _, table := range []string{
		"person_merge_suggestions",
		"person_merge_suggestion_items",
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected %s table to exist after migration", table)
		}
	}
}

func TestAutoMigrateAddsPersonMergeSuggestionConstraints(t *testing.T) {
	db := openMigratedTestDB(t)

	if err := db.Exec(
		"INSERT INTO person_merge_suggestions (target_person_id, target_category_snapshot, status, candidate_count, top_similarity) VALUES (?, ?, ?, ?, ?)",
		1, "family", "pending", 2, 0.62,
	).Error; err != nil {
		t.Fatalf("expected pending suggestion insert to succeed: %v", err)
	}

	if err := db.Exec(
		"INSERT INTO person_merge_suggestions (target_person_id, target_category_snapshot, status, candidate_count, top_similarity) VALUES (?, ?, ?, ?, ?)",
		2, "friend", "bad_status", 1, 0.72,
	).Error; err == nil {
		t.Fatal("expected invalid person_merge_suggestions status to be rejected")
	}

	if err := db.Exec(
		"INSERT INTO person_merge_suggestion_items (suggestion_id, candidate_person_id, similarity_score, rank, status) VALUES (?, ?, ?, ?, ?)",
		1, 3, 0.66, 1, "pending",
	).Error; err != nil {
		t.Fatalf("expected pending suggestion item insert to succeed: %v", err)
	}

	if err := db.Exec(
		"INSERT INTO person_merge_suggestion_items (suggestion_id, candidate_person_id, similarity_score, rank, status) VALUES (?, ?, ?, ?, ?)",
		1, 3, 0.67, 2, "pending",
	).Error; err == nil {
		t.Fatal("expected duplicate (suggestion_id, candidate_person_id) insert to be rejected")
	}

	if err := db.Exec(
		"INSERT INTO person_merge_suggestion_items (suggestion_id, candidate_person_id, similarity_score, rank, status) VALUES (?, ?, ?, ?, ?)",
		1, 4, 0.65, 2, "bad_status",
	).Error; err == nil {
		t.Fatal("expected invalid person_merge_suggestion_items status to be rejected")
	}
}

func TestPeopleConfigHasMergeSuggestionField(t *testing.T) {
	cfg := config.PeopleConfig{
		MergeSuggestionThreshold:       0.55,
		MergeSuggestionMaxPairsPerRun:  200,
		MergeSuggestionBatchSize:       100,
		MergeSuggestionCooldownSeconds: 300,
	}

	if cfg.MergeSuggestionThreshold <= 0 {
		t.Fatal("expected MergeSuggestionThreshold field to exist and hold value")
	}
}

func TestAutoMigrateAddsPersonIdentity(t *testing.T) {
	db := openMigratedTestDB(t)

	// 五张表存在
	for _, table := range []string{
		"person_identity_profiles",
		"person_identity_centers",
		"person_identity_center_members",
		"people_feedback_events",
		"people_identity_decisions",
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected %s table to exist after migration", table)
		}
	}

	now := time.Now().UTC()

	// person_id 画像唯一
	if err := db.Exec(
		`INSERT INTO person_identity_profiles (person_id, active_generation, next_generation, status, updated_at) VALUES (1, 0, 1, 'dirty', ?)`,
		now,
	).Error; err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO person_identity_profiles (person_id, active_generation, next_generation, status, updated_at) VALUES (1, 0, 1, 'dirty', ?)`,
		now,
	).Error; err == nil {
		t.Fatal("expected duplicate person_id profile to be rejected")
	}

	// center 唯一键 (person_id, generation, ordinal)
	if err := db.Exec(
		`INSERT INTO person_identity_centers (person_id, generation, ordinal, updated_at) VALUES (1, 1, 0, ?)`,
		now,
	).Error; err != nil {
		t.Fatalf("insert center: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO person_identity_centers (person_id, generation, ordinal, updated_at) VALUES (1, 1, 0, ?)`,
		now,
	).Error; err == nil {
		t.Fatal("expected duplicate (person_id, generation, ordinal) center to be rejected")
	}

	// member 唯一键 (person_id, generation, face_id)
	if err := db.Exec(
		`INSERT INTO person_identity_center_members (person_id, generation, face_id, photo_id, state, updated_at) VALUES (1, 1, 100, 10, 'candidate', ?)`,
		now,
	).Error; err != nil {
		t.Fatalf("insert member: %v", err)
	}
	if err := db.Exec(
		`INSERT INTO person_identity_center_members (person_id, generation, face_id, photo_id, state, updated_at) VALUES (1, 1, 100, 10, 'candidate', ?)`,
		now,
	).Error; err == nil {
		t.Fatal("expected duplicate (person_id, generation, face_id) member to be rejected")
	}

	// candidate member 可以使用空 center_id
	if err := db.Exec(
		`INSERT INTO person_identity_center_members (person_id, generation, center_id, face_id, photo_id, state, updated_at) VALUES (1, 1, NULL, 101, 11, 'candidate', ?)`,
		now,
	).Error; err != nil {
		t.Fatalf("insert candidate member with null center_id: %v", err)
	}

	// 非法 profile status 被拒绝
	if err := db.Exec(
		`INSERT INTO person_identity_profiles (person_id, active_generation, next_generation, status, updated_at) VALUES (2, 0, 1, 'bad_status', ?)`,
		now,
	).Error; err == nil {
		t.Fatal("expected invalid profile status to be rejected")
	}

	// 非法 member state 被拒绝
	if err := db.Exec(
		`INSERT INTO person_identity_center_members (person_id, generation, face_id, photo_id, state, updated_at) VALUES (1, 1, 102, 12, 'bad_state', ?)`,
		now,
	).Error; err == nil {
		t.Fatal("expected invalid member state to be rejected")
	}

	// 四个指定复合索引真实存在于 sqlite_master
	for _, indexName := range []string{
		"idx_pip_status_updated",
		"idx_pic_person_generation",
		"idx_pfe_event_created",
		"idx_pid_mode_created",
	} {
		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", indexName).Scan(&count).Error; err != nil {
			t.Fatalf("query index %s: %v", indexName, err)
		}
		if count != 1 {
			t.Fatalf("expected index %s to exist after migration", indexName)
		}
	}
}

func TestPhotoAnalysisFieldsMigrated(t *testing.T) {
	db := openMigratedTestDB(t)

	required := []string{
		"analysis_lock_version",
		"analysis_next_retry_at",
		"analysis_last_error_code",
		"analysis_last_error",
		"analysis_last_failed_at",
	}
	for _, col := range required {
		if !db.Migrator().HasColumn(&model.Photo{}, col) {
			t.Fatalf("expected photos.%s column after migration", col)
		}
	}

	// 复合索引 idx_photos_analysis_pending 应存在（v2 migration 创建），
	// 覆盖 analysis_next_retry_at；单列 idx_analysis_retry_ready 应被删除。
	indexes, err := db.Migrator().GetIndexes(&model.Photo{})
	if err != nil {
		t.Fatalf("get indexes: %v", err)
	}
	compoundFound := false
	singleFound := false
	for _, idx := range indexes {
		if idx.Name() == "idx_photos_analysis_pending" {
			compoundFound = true
		}
		if idx.Name() == "idx_analysis_retry_ready" {
			singleFound = true
		}
	}
	if !compoundFound {
		t.Fatal("expected idx_photos_analysis_pending compound index after migration")
	}
	if singleFound {
		t.Fatal("idx_analysis_retry_ready should be dropped (covered by compound index)")
	}
}

// TestAutoMigrateAddsPersonFaceCursorIndex 验证人物人脸 cursor 分页专用 partial index
// 在 AutoMigrate 后存在，且 sqlite_master 中字段顺序和 partial 条件正确。
func TestAutoMigrateAddsPersonFaceCursorIndex(t *testing.T) {
	db := openMigratedTestDB(t)

	const indexName = "idx_faces_person_quality_cursor"

	var sql string
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", indexName).Scan(&sql).Error; err != nil {
		t.Fatalf("query index %s: %v", indexName, err)
	}
	if sql == "" {
		t.Fatalf("expected index %s to exist after migration", indexName)
	}

	// 字段顺序必须为 person_id, quality_score DESC, id ASC
	if !strings.Contains(sql, "person_id") || !strings.Contains(sql, "quality_score DESC") || !strings.Contains(sql, "id ASC") {
		t.Fatalf("index %s has wrong column order/direction: %s", indexName, sql)
	}

	// 必须 partial：仅收录非 excluded 人脸
	if !strings.Contains(sql, "cluster_status != 'excluded'") {
		t.Fatalf("index %s should be a partial index excluding cluster_status='excluded': %s", indexName, sql)
	}
}

// TestMigratePersonFaceCursorIndex_IdempotentAndSelfHealing 验证：
//   - 连续执行迁移两次不报错、不产生重复索引；
//   - 迁移标记保留；
//   - 手动删除索引后再执行迁移能够重建索引（自愈）。
func TestMigratePersonFaceCursorIndex_IdempotentAndSelfHealing(t *testing.T) {
	db := openMigratedTestDB(t)

	const indexName = "idx_faces_person_quality_cursor"
	const migrationKey = "migration.person_face_cursor_index_v1"

	countIndex := func() int64 {
		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", indexName).Scan(&count).Error; err != nil {
			t.Fatalf("count index: %v", err)
		}
		return count
	}

	// AutoMigrate 已执行过一次，索引应已存在且标记已写入。
	if got := countIndex(); got != 1 {
		t.Fatalf("expected 1 index after initial migration, got %d", got)
	}
	var cfg model.AppConfig
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err != nil {
		t.Fatalf("expected migration marker %s to exist: %v", migrationKey, err)
	}

	// 再次执行迁移：幂等，不应报错，不应产生重复索引。
	if err := migratePersonFaceCursorIndex(db); err != nil {
		t.Fatalf("second migration should be idempotent: %v", err)
	}
	if got := countIndex(); got != 1 {
		t.Fatalf("expected 1 index after second migration, got %d", got)
	}

	// 手动删除索引后，再执行迁移必须能重建。
	if err := db.Exec("DROP INDEX IF EXISTS " + indexName).Error; err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if got := countIndex(); got != 0 {
		t.Fatalf("expected 0 index after drop, got %d", got)
	}
	if err := migratePersonFaceCursorIndex(db); err != nil {
		t.Fatalf("self-healing migration should rebuild index: %v", err)
	}
	if got := countIndex(); got != 1 {
		t.Fatalf("expected 1 index after self-healing migration, got %d", got)
	}

	// 迁移标记仍应存在（不应被删除）。
	if err := db.Where("key = ?", migrationKey).First(&cfg).Error; err != nil {
		t.Fatalf("migration marker should still exist after self-healing: %v", err)
	}
}

// TestMigrateFaceQualityEvidenceOrigin 验证历史回填缺证据记录被一次性标记为
// historical_backfill/missing，且不应误标：有 evidence 的 review_required、人工事件、
// 已标记的记录均不受影响。索引应存在，迁移幂等。
func TestMigrateFaceQualityEvidenceOrigin(t *testing.T) {
	// 用仅 AutoMigrate FaceQualityEvent 的独立库，避免完整 AutoMigrate 流水线已写迁移标记
	// 导致本测试调用 migrateFaceQualityEvidenceOrigin 时早退。
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.FaceQualityEvent{}, &model.AppConfig{}))

	// 1) 历史缺证据：auto + review_required + 空 evidence_json + 空 evidence_origin。
	histMissing := &model.FaceQualityEvent{
		PhotoID: 1, FaceID: nil,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceJSON:   "",
		EvidenceOrigin: "",
		IsCurrent:      true,
	}
	require.NoError(t, db.Create(histMissing).Error)

	// 2) 真实模型 0 分但有 evidence：不应被标 missing。
	realZero := &model.FaceQualityEvent{
		PhotoID: 2,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceJSON:   `{"face_validity_score":0,"pixel_width":50}`,
		EvidenceOrigin: "",
		IsCurrent:      true,
	}
	require.NoError(t, db.Create(realZero).Error)

	// 3) 人工事件：source=manual，不应被该迁移动。
	manualEvt := &model.FaceQualityEvent{
		PhotoID: 3,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionAccepted,
		Source:   model.FaceQualitySourceManual, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceJSON:   "",
		EvidenceOrigin: "",
		IsCurrent:      true,
	}
	require.NoError(t, db.Create(manualEvt).Error)

	// 4) 非当前历史缺证据：is_current=false，不应被标（只处理 is_current=1）。
	notCurrent := &model.FaceQualityEvent{
		PhotoID: 4,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceJSON:   "",
		EvidenceOrigin: "",
		IsCurrent:      false,
	}
	require.NoError(t, db.Create(notCurrent).Error)

	// 5) 早期实时记录：有 evidence_json、decision=review_required、evidence_state=''（旧库遗留）。
	//    迁移应把 state 回填为 available，使其留在 pending_review，不被漏出所有队列。
	legacyRealtime := &model.FaceQualityEvent{
		PhotoID: 5,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceJSON:   `{"face_validity_score":0.55,"pixel_width":60}`,
		EvidenceOrigin: "",
		EvidenceState:  "",
		IsCurrent:      true,
	}
	require.NoError(t, db.Create(legacyRealtime).Error)

	require.NoError(t, migrateFaceQualityEvidenceOrigin(db))

	// 历史缺证据 → historical_backfill/missing
	var got model.FaceQualityEvent
	require.NoError(t, db.First(&got, histMissing.ID).Error)
	assert.Equal(t, model.FaceQualityEvidenceOriginHistoricalBackfill, got.EvidenceOrigin)
	assert.Equal(t, model.FaceQualityEvidenceStateMissing, got.EvidenceState)

	// 真实 0 分有证据：origin 不猜（留空），但 evidence_state 应回填为 available，
	// 使其留在 pending_review，而非因空 state 漏出所有队列。
	got = model.FaceQualityEvent{}
	require.NoError(t, db.First(&got, realZero.ID).Error)
	assert.Equal(t, "", got.EvidenceOrigin)
	assert.Equal(t, model.FaceQualityEvidenceStateAvailable, got.EvidenceState)

	// 早期实时记录：state 回填 available，origin 仍空（不猜）。
	got = model.FaceQualityEvent{}
	require.NoError(t, db.First(&got, legacyRealtime.ID).Error)
	assert.Equal(t, "", got.EvidenceOrigin)
	assert.Equal(t, model.FaceQualityEvidenceStateAvailable, got.EvidenceState)

	// 人工事件：有 evidence_json 时 state 也回填 available；origin 不猜。
	got = model.FaceQualityEvent{}
	require.NoError(t, db.First(&got, manualEvt.ID).Error)
	assert.Equal(t, "", got.EvidenceOrigin)
	// manualEvt 的 evidence_json 为空，state 保持空（不属 available/missing 任一标记集）。
	assert.Equal(t, "", got.EvidenceState)

	// 非当前不受影响。
	got = model.FaceQualityEvent{}
	require.NoError(t, db.First(&got, notCurrent.ID).Error)
	assert.Equal(t, "", got.EvidenceOrigin)

	// 索引存在。
	for _, idx := range []string{"idx_fqe_evidence_origin_state", "idx_fqe_rescore_run"} {
		var cnt int64
		require.NoError(t, db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&cnt).Error)
		assert.Equal(t, int64(1), cnt, "index %s should exist", idx)
	}

	// 迁移标记已写。
	var cfg model.AppConfig
	require.NoError(t, db.Where("key = ?", "migration.face_quality_evidence_origin_v1").First(&cfg).Error)

	// 幂等：再跑一次早退不报错，已标记行不变。
	require.NoError(t, migrateFaceQualityEvidenceOrigin(db))
	got = model.FaceQualityEvent{}
	require.NoError(t, db.First(&got, histMissing.ID).Error)
	assert.Equal(t, model.FaceQualityEvidenceOriginHistoricalBackfill, got.EvidenceOrigin)
}

// TestMigrateFaceQualityRescoreRunMeta 验证既有运行元数据修复：
//  1. status=completed 且 retryable_count>0 → completed_with_errors；
//  2. last_error 为空时从失败 item 回填；
//  3. 无失败的 completed 不变；幂等。
func TestMigrateFaceQualityRescoreRunMeta(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.FaceQualityRescoreRun{}, &model.FaceQualityRescoreItem{}, &model.AppConfig{}))

	// 1) 线上 #1 形态：completed + retryable_count=4733 + 空 last_error + 失败 item。
	run1 := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow,
		Status: model.FaceQualityRescoreStatusCompleted, RetryableCount: 4733,
		TargetFaceCount: 4733, RuleVersion: "v1", ModelVersion: "test-v1",
	}
	require.NoError(t, db.Create(run1).Error)
	require.NoError(t, db.Create(&model.FaceQualityRescoreItem{
		RunID: run1.ID, PhotoID: 1, FaceID: 100,
		BBoxX: 0, BBoxY: 0, BBoxWidth: 0, BBoxHeight: 0,
		Status: model.FaceQualityRescoreItemStatusRetryableError,
		LastError: "score known faces returned status 422",
	}).Error)

	// 2) 干净的 completed 校准（无失败）—— 不应变。
	run2 := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow,
		Status: model.FaceQualityRescoreStatusCompleted, RetryableCount: 0,
		TargetFaceCount: 5, ProcessedFaceCount: 5, RuleVersion: "v1", ModelVersion: "test-v1",
		LastError: "ok",
	}
	require.NoError(t, db.Create(run2).Error)

	require.NoError(t, migrateFaceQualityRescoreRunMeta(db))

	// run1 → completed_with_errors，last_error 从 item 回填。
	var got1 model.FaceQualityRescoreRun
	require.NoError(t, db.First(&got1, run1.ID).Error)
	assert.Equal(t, model.FaceQualityRescoreStatusCompletedWithError, got1.Status)
	assert.Equal(t, "score known faces returned status 422", got1.LastError)

	// run2 不变。
	var got2 model.FaceQualityRescoreRun
	require.NoError(t, db.First(&got2, run2.ID).Error)
	assert.Equal(t, model.FaceQualityRescoreStatusCompleted, got2.Status)
	assert.Equal(t, "ok", got2.LastError)

	// 幂等：再跑一次不报错、不变。
	require.NoError(t, migrateFaceQualityRescoreRunMeta(db))
	got1 = model.FaceQualityRescoreRun{}
	require.NoError(t, db.First(&got1, run1.ID).Error)
	assert.Equal(t, model.FaceQualityRescoreStatusCompletedWithError, got1.Status)
}
