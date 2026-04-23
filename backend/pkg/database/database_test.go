package database

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
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
