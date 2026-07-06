package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// createPopulatedDB 在 t.TempDir() 创建一个完整的可写 SQLite 库，迁移所需表，
// 并通过返回的写入句柄插入测试数据。返回库路径与已打开的可写 *sql.DB。
// 测试用完后关闭 db 即可；mode=ro 读取需另开连接。
func createPopulatedDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relive_test.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open writable db: %v", err)
	}
	schema := `
CREATE TABLE people_feedback_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at DATETIME NOT NULL,
  event_type TEXT NOT NULL,
  target_person_id INTEGER NOT NULL,
  source_person_ids TEXT,
  face_ids TEXT,
  algorithm_version TEXT,
  similarity_snapshot TEXT
);
CREATE TABLE people_identity_decisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at DATETIME NOT NULL,
  mode TEXT NOT NULL,
  component_hash TEXT NOT NULL,
  component_size INTEGER NOT NULL DEFAULT 0,
  component_face_ids TEXT,
  component_face_ids_truncated INTEGER NOT NULL DEFAULT 0,
  decision_key TEXT NOT NULL UNIQUE,
  legacy_target_person_id INTEGER,
  legacy_score REAL,
  profile_best_person_id INTEGER,
  profile_best_score REAL,
  profile_second_person_id INTEGER,
  profile_second_score REAL,
  margin REAL NOT NULL DEFAULT 0,
  center_ids TEXT,
  decision TEXT NOT NULL,
  reason TEXT,
  elapsed_milliseconds INTEGER NOT NULL DEFAULT 0,
  algorithm_version TEXT,
  index_generation INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE person_merge_suggestions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  target_person_id INTEGER NOT NULL,
  target_category_snapshot TEXT NOT NULL,
  status TEXT NOT NULL,
  candidate_count INTEGER NOT NULL DEFAULT 0,
  top_similarity REAL NOT NULL DEFAULT 0,
  reviewed_at DATETIME
);
CREATE TABLE person_merge_suggestion_items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  suggestion_id INTEGER NOT NULL,
  candidate_person_id INTEGER NOT NULL,
  similarity_score REAL NOT NULL,
  rank INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  match_source TEXT NOT NULL DEFAULT 'legacy',
  warning TEXT
);
`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return path, db
}

// reopenReadOnly 以 mode=ro 重新打开库，模拟报告工具的真实打开方式。
func reopenReadOnly(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=ro&_query_only=true&_busy_timeout=60000", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open ro db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// insertFeedback 插入一条反馈事件。
func insertFeedback(t *testing.T, db *sql.DB, eventType string, target int64, sources []int64, algVer string) {
	t.Helper()
	src := "[]"
	if len(sources) > 0 {
		b, _ := json.Marshal(sources)
		src = string(b)
	}
	_, err := db.Exec(`INSERT INTO people_feedback_events (created_at, event_type, target_person_id, source_person_ids, face_ids, algorithm_version, similarity_snapshot) VALUES (?,?,?,?,?,?,?)`,
		"2026-01-01 00:00:00", eventType, target, src, "[]", algVer, "{}")
	if err != nil {
		t.Fatalf("insert feedback: %v", err)
	}
}

func insertDecision(t *testing.T, db *sql.DB, mode, decision, reason string, legacy, best, second *float64, margin float64, elapsed int, algVer string) {
	t.Helper()
	key := fmt.Sprintf("k_%s_%s_%s_%d", mode, decision, reason, elapsed)
	_, err := db.Exec(`INSERT INTO people_identity_decisions (created_at, mode, component_hash, decision_key, legacy_score, profile_best_score, profile_second_score, margin, decision, reason, elapsed_milliseconds, algorithm_version) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		"2026-01-01 00:00:00", mode, "hash", key, legacy, best, second, margin, decision, reason, elapsed, algVer)
	if err != nil {
		t.Fatalf("insert decision: %v", err)
	}
}

func insertSuggestion(t *testing.T, db *sql.DB, id, target int64, status string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO person_merge_suggestions (id, created_at, updated_at, target_person_id, target_category_snapshot, status, candidate_count, top_similarity) VALUES (?,?,?,?,?,?,?,?)`,
		id, "2026-01-01 00:00:00", "2026-01-01 00:00:00", target, "family", status, 0, 0)
	if err != nil {
		t.Fatalf("insert suggestion: %v", err)
	}
}

func insertSuggestionItem(t *testing.T, db *sql.DB, sugID, candidate, rank int64, source string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO person_merge_suggestion_items (created_at, updated_at, suggestion_id, candidate_person_id, similarity_score, rank, status, match_source) VALUES (?,?,?,?,?,?,?,?)`,
		"2026-01-01 00:00:00", "2026-01-01 00:00:00", sugID, candidate, 0.9, rank, "pending", source)
	if err != nil {
		t.Fatalf("insert suggestion item: %v", err)
	}
}

// ---- 1. 空数据库或缺表错误 ----

func TestIdentityProfileReport_MissingTableErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// 仅创建一张无关表，缺少所有必要表。
	if _, err := db.Exec(`CREATE TABLE foo (id INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	db.Close()

	_, err = buildReport(path)
	if err == nil {
		t.Fatal("expected error for missing tables, got nil")
	}
	if !strings.Contains(err.Error(), "required table missing") {
		t.Fatalf("expected 'required table missing' error, got: %v", err)
	}
}

// ---- 2. 参数和 format 校验 ----

func TestIdentityProfileReport_ArgValidation(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"missing db", []string{"-format", "text"}, 2, "-db is required"},
		{"empty db", []string{"-db", "  ", "-format", "text"}, 2, "-db is required"},
		{"invalid format", []string{"-db", "x", "-format", "xml"}, 2, "invalid -format"},
		{"valid format default", []string{"-db", "x"}, 1, "database file not found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(tc.args, &out, &errOut)
			if code != tc.wantCode {
				t.Fatalf("code=%d want %d, stderr=%s", code, tc.wantCode, errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.wantErr) {
				t.Fatalf("stderr=%q want contains %q", errOut.String(), tc.wantErr)
			}
		})
	}
}

// ---- 3. 只读打开与禁止写入 ----

func TestIdentityProfileReport_ReadOnlyOpen(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 1, []int64{2}, algorithmVersionSuggestionV1)

	ro := reopenReadOnly(t, path)

	// 尝试 INSERT 必须失败。
	_, err := ro.Exec(`INSERT INTO people_feedback_events (created_at, event_type, target_person_id) VALUES (?,?,?)`,
		"2026-01-01", "merge_confirmed", 1)
	if err == nil {
		t.Fatal("INSERT succeeded on read-only db; expected failure")
	}
	// 尝试 UPDATE 必须失败。
	_, err = ro.Exec(`UPDATE people_feedback_events SET event_type='x' WHERE id=1`)
	if err == nil {
		t.Fatal("UPDATE succeeded on read-only db; expected failure")
	}
}

func TestIdentityProfileReport_NonExistentPathDoesNotCreateDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does_not_exist.db")
	_, err := buildReport(path)
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
	// 文件不应被创建。
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("non-existent path was created; mode=ro should not create a database")
	}
}

func TestIdentityProfileReport_BusinessTablesUnchanged(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 1, []int64{2}, algorithmVersionSuggestionV1)
	insertFeedback(t, wdb, eventTypeMergeRejected, 1, []int64{3}, algorithmVersionSuggestionV1)
	insertDecision(t, wdb, modeShadow, "disagree", "", p64(0.5), p64(0.6), p64(0.4), 0.1, 10, algorithmVersionIdentityProfile1)

	before := countRows(t, wdb)
	_, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	after := countRows(t, wdb)
	for tb, c := range before {
		if after[tb] != c {
			t.Fatalf("row count changed for %s: before=%d after=%d", tb, c, after[tb])
		}
	}
}

func countRows(t *testing.T, db *sql.DB) map[string]int64 {
	t.Helper()
	tables := []string{"people_feedback_events", "people_identity_decisions", "person_merge_suggestions", "person_merge_suggestion_items"}
	out := map[string]int64{}
	for _, tb := range tables {
		var c int64
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tb)).Scan(&c); err != nil {
			t.Fatalf("count %s: %v", tb, err)
		}
		out[tb] = c
	}
	return out
}

// ---- 4. 空数据报告 ----

func TestIdentityProfileReport_EmptyDataReport(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()

	report, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if report.Coverage.FeedbackTotal != 0 {
		t.Fatalf("expected 0 feedback, got %d", report.Coverage.FeedbackTotal)
	}
	if report.Coverage.DecisionTotal != 0 {
		t.Fatalf("expected 0 decisions, got %d", report.Coverage.DecisionTotal)
	}
	// 空集合必须输出 [] 而非 null。
	b, err := report.marshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(b, []byte("null")) {
		t.Fatalf("JSON contains null; empty collections must be [] or {}:\n%s", b)
	}
	// Warnings 应为 []。
	var w []string
	_ = json.Unmarshal([]byte(extractField(t, b, "warnings")), &w)
	// 空数据应有多个 warning。
	if len(report.Warnings) == 0 {
		t.Fatal("expected warnings for empty data")
	}
}

// ---- 5. feedback 分类统计 ----

func TestIdentityProfileReport_FeedbackClassification(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 1, []int64{2}, algorithmVersionSuggestionV1)
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 1, []int64{3}, algorithmVersionManual)
	insertFeedback(t, wdb, eventTypeMergeRejected, 1, []int64{4}, algorithmVersionSuggestionV1)
	insertFeedback(t, wdb, eventTypeFaceMoved, 1, []int64{5}, algorithmVersionManual)
	insertFeedback(t, wdb, eventTypePersonSplit, 1, []int64{6}, "unknown-ver")

	report, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if report.Feedback.ByEvent[eventTypeMergeConfirmed] != 2 {
		t.Fatalf("merge_confirmed count=%d want 2", report.Feedback.ByEvent[eventTypeMergeConfirmed])
	}
	if report.Feedback.ByEvent[eventTypeMergeRejected] != 1 {
		t.Fatalf("merge_rejected count=%d want 1", report.Feedback.ByEvent[eventTypeMergeRejected])
	}
	if report.Feedback.ManualTotal != 2 {
		t.Fatalf("manual total=%d want 2", report.Feedback.ManualTotal)
	}
	if report.Feedback.SuggestionTotal != 2 {
		t.Fatalf("suggestion total=%d want 2", report.Feedback.SuggestionTotal)
	}
	if report.Feedback.UnknownVersion != 1 {
		t.Fatalf("unknown version=%d want 1", report.Feedback.UnknownVersion)
	}
}

// ---- 6. manual 反馈不进入建议接受率 ----

func TestIdentityProfileReport_ManualFeedbackExcludedFromAcceptance(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	// 3 manual confirmed + 1 suggestion confirmed + 1 suggestion rejected.
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 1, []int64{2}, algorithmVersionManual)
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 1, []int64{3}, algorithmVersionManual)
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 1, []int64{4}, algorithmVersionManual)
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 10, []int64{11}, algorithmVersionSuggestionV1)
	insertFeedback(t, wdb, eventTypeMergeRejected, 10, []int64{12}, algorithmVersionSuggestionV1)
	insertSuggestion(t, wdb, 1, 10, "applied")
	insertSuggestionItem(t, wdb, 1, 11, 1, matchSourceLegacy)

	report, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	// 分母应为 2（仅 suggestion 驱动），而非 5。
	if report.SuggestionEffect.AcceptanceRate.Denominator != 2 {
		t.Fatalf("acceptance denominator=%d want 2 (manual excluded)", report.SuggestionEffect.AcceptanceRate.Denominator)
	}
	if report.SuggestionEffect.SuggestionConfirmed != 1 {
		t.Fatalf("suggestion confirmed=%d want 1", report.SuggestionEffect.SuggestionConfirmed)
	}
}

// ---- 7. suggestion feedback 与 rank 正确关联 ----

func TestIdentityProfileReport_SuggestionFeedbackRankLinkage(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	// suggestion for target=10, candidates 11(rank1), 12(rank5), 13(rank15).
	insertSuggestion(t, wdb, 1, 10, "applied")
	insertSuggestionItem(t, wdb, 1, 11, 1, matchSourceIdentityProfile)
	insertSuggestionItem(t, wdb, 1, 12, 5, matchSourceLegacy)
	insertSuggestionItem(t, wdb, 1, 13, 15, matchSourceLegacy)
	// confirmed candidate 11 (rank1) -> Recall@1 hit, evaluable=1
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 10, []int64{11}, algorithmVersionSuggestionV1)
	// confirmed candidate 13 (rank15) -> Recall@20 hit only, evaluable=2
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 10, []int64{13}, algorithmVersionSuggestionV1)

	report, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if report.SuggestionEffect.RecallAt1.Evaluable != 2 {
		t.Fatalf("Recall@1 evaluable=%d want 2 (both confirmed events linkable)", report.SuggestionEffect.RecallAt1.Evaluable)
	}
	// candidate 11 rank1: Recall@1 命中；candidate 13 rank15: Recall@1 不命中 -> hits=1, 1/2=0.5000
	if report.SuggestionEffect.RecallAt1.Value != "0.5000" {
		t.Fatalf("Recall@1 value=%s want 0.5000", report.SuggestionEffect.RecallAt1.Value)
	}
	if report.SuggestionEffect.RecallAt20.Value != "1.0000" {
		t.Fatalf("Recall@20 value=%s want 1.0000 (both rank<=20)", report.SuggestionEffect.RecallAt20.Value)
	}
	if report.SuggestionEffect.RecallAt20.Value != "1.0000" {
		t.Fatalf("Recall@20 value=%s want 1.0000 (both rank<=20)", report.SuggestionEffect.RecallAt20.Value)
	}
	if report.SuggestionEffect.RecallAt1.Value == notAvailable {
		t.Fatal("Recall@1 should be available")
	}
	if report.SuggestionEffect.RecallAt20.Value == notAvailable {
		t.Fatal("Recall@20 should be available")
	}
	if report.SuggestionEffect.IdentityProfileHits != 1 {
		t.Fatalf("identity_profile hits=%d want 1", report.SuggestionEffect.IdentityProfileHits)
	}
	if report.SuggestionEffect.LegacyHits != 2 {
		t.Fatalf("legacy hits=%d want 2", report.SuggestionEffect.LegacyHits)
	}
}

// ---- 8. 无法关联时不伪造 Recall ----

func TestIdentityProfileReport_UnlinkedFeedbackNoFabricatedRecall(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	// confirmed event with no matching suggestion item.
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 99, []int64{100}, algorithmVersionSuggestionV1)
	insertSuggestion(t, wdb, 1, 10, "applied")
	insertSuggestionItem(t, wdb, 1, 11, 1, matchSourceLegacy)

	report, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if report.SuggestionEffect.UnmatchedFeedbackCount != 1 {
		t.Fatalf("unmatched=%d want 1", report.SuggestionEffect.UnmatchedFeedbackCount)
	}
	// Recall 分母为 0（无 evaluable confirmed），应输出 not_available。
	if report.SuggestionEffect.RecallAt1.Value != notAvailable {
		t.Fatalf("Recall@1 should be not_available, got %s", report.SuggestionEffect.RecallAt1.Value)
	}
	if report.SuggestionEffect.RecallAt1.Evaluable != 0 {
		t.Fatalf("Recall@1 evaluable=%d want 0", report.SuggestionEffect.RecallAt1.Evaluable)
	}
}

// ---- 9. decision 分类与比例 ----

func TestIdentityProfileReport_DecisionClassification(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	insertDecision(t, wdb, modeShadow, "agree", "", p64(0.8), p64(0.8), p64(0.5), 0.3, 5, algorithmVersionIdentityProfile1)
	insertDecision(t, wdb, modeShadow, "disagree", "", p64(0.7), p64(0.6), p64(0.4), 0.1, 6, algorithmVersionIdentityProfile1)
	insertDecision(t, wdb, modeShadow, "disagree", "", p64(0.7), p64(0.55), p64(0.4), 0.1, 7, algorithmVersionIdentityProfile1)
	insertDecision(t, wdb, modeShadow, "legacy_miss_profile_hit", "", p64(0.3), p64(0.6), p64(0.4), 0.2, 8, algorithmVersionIdentityProfile1)
	insertDecision(t, wdb, modeShadow, "profile_unavailable", "", p64(0.5), nil, nil, 0, 9, algorithmVersionIdentityProfile1)
	insertDecision(t, wdb, modeRescue, "rescue_applied", "", p64(0.4), p64(0.6), p64(0.4), 0.2, 10, algorithmVersionIdentityProfile1)

	report, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if report.IdentityDecision.ByDecision["disagree"] != 2 {
		t.Fatalf("disagree=%d want 2", report.IdentityDecision.ByDecision["disagree"])
	}
	// disagreement rate = 2/6.
	if report.IdentityDecision.DisagreementRate.Value != "0.3333" {
		t.Fatalf("disagreement rate=%s want 0.3333", report.IdentityDecision.DisagreementRate.Value)
	}
	if report.IdentityDecision.RescueAppliedCount != 1 {
		t.Fatalf("rescue applied=%d want 1", report.IdentityDecision.RescueAppliedCount)
	}
	if report.IdentityDecision.ProfileUnavailableRate.Numerator != 1 || report.IdentityDecision.ProfileUnavailableRate.Denominator != 6 {
		t.Fatalf("profile unavailable rate=%+v want 1/6", report.IdentityDecision.ProfileUnavailableRate)
	}
}

// ---- 10. nil、NaN、Inf 分数过滤 ----

func TestIdentityProfileReport_NilNaNInfScoreFiltering(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	nan := math.NaN()
	inf := math.Inf(1)
	insertDecision(t, wdb, modeShadow, "agree", "", p64(0.5), p64(0.5), p64(0.3), 0.2, 1, algorithmVersionIdentityProfile1)
	insertDecision(t, wdb, modeShadow, "agree", "", nil, nil, nil, 0.2, 2, algorithmVersionIdentityProfile1)    // nil scores (3 nil)
	insertDecision(t, wdb, modeShadow, "agree", "", &nan, &inf, &nan, 0.2, 3, algorithmVersionIdentityProfile1) // NaN/Inf (3 ignored)
	insertDecision(t, wdb, modeShadow, "agree", "", p64(0.9), p64(0.9), p64(0.8), 0.1, 4, algorithmVersionIdentityProfile1)

	report, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	// legacy_score: 有效 2 个 (0.5, 0.9)，nil 1 + nan 1 = 2 ignored。
	if report.ScoreDistribution.LegacyScore.Samples != 2 {
		t.Fatalf("legacy score samples=%d want 2", report.ScoreDistribution.LegacyScore.Samples)
	}
	if report.ScoreDistribution.LegacyScore.Ignored != 2 {
		t.Fatalf("legacy score ignored=%d want 2 (1 nil + 1 nan)", report.ScoreDistribution.LegacyScore.Ignored)
	}
}

// ---- 11. 分位数边界 ----

func TestIdentityProfileReport_PercentileBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		p      float64
		want   float64
	}{
		{"empty", nil, 50, 0},
		{"single", []float64{7}, 50, 7},
		{"single p99", []float64{7}, 99, 7},
		{"two even", []float64{1, 2}, 50, 1}, // ceil(0.5*2)=1 -> index0=1
		{"two p100", []float64{1, 2}, 100, 2},
		{"three odd", []float64{1, 2, 3}, 50, 2},
		{"three p90", []float64{1, 2, 3}, 90, 3}, // ceil(0.9*3)=3 -> index2=3
		{"four p95", []float64{1, 2, 3, 4}, 95, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := percentile(tc.values, tc.p)
			if got != tc.want {
				t.Fatalf("percentile(%v, %v)=%v want %v", tc.values, tc.p, got, tc.want)
			}
		})
	}
}

// ---- 12. JSON 输出确定性 ----

func TestIdentityProfileReport_JSONDeterminism(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 1, []int64{2}, algorithmVersionSuggestionV1)
	insertDecision(t, wdb, modeShadow, "agree", "", p64(0.5), p64(0.5), p64(0.3), 0.2, 1, algorithmVersionIdentityProfile1)

	r1, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport 1: %v", err)
	}
	r2, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport 2: %v", err)
	}
	// 清除 generatedAt（时间戳）后再比较。
	r1.GeneratedAt = "T"
	r2.GeneratedAt = "T"
	b1, _ := r1.marshalJSON()
	b2, _ := r2.marshalJSON()
	if !bytes.Equal(b1, b2) {
		t.Fatalf("JSON not deterministic:\n%s\n---\n%s", b1, b2)
	}
}

// ---- 13. 输出隐私字段扫描 ----

func TestIdentityProfileReport_PrivacyFieldScan(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	// 插入带相似度快照、component hash、decision key 的数据。
	insertFeedback(t, wdb, eventTypeMergeConfirmed, 1, []int64{2}, algorithmVersionSuggestionV1)
	insertDecision(t, wdb, modeShadow, "agree", "score_below_threshold", p64(0.5), p64(0.5), p64(0.3), 0.2, 1, algorithmVersionIdentityProfile1)
	insertSuggestion(t, wdb, 1, 1, "applied")
	insertSuggestionItem(t, wdb, 1, 2, 1, matchSourceIdentityProfile)

	report, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	// 手动注入敏感字段到原始数据，验证报告输出不含。
	// 这里直接验证报告 text + JSON 不含敏感关键词。
	textOut := report.renderText()
	jsonOut, _ := report.marshalJSON()
	// 隐私扫描：报告只输出聚合统计，不应出现逐条 ID 明细、路径、embedding、原始快照 JSON 等。
	forbidden := []string{
		"embedding", "thumbnail", "/usr/", ".jpg", ".png",
		"component_hash", "decision_key", "ComponentHash", "DecisionKey",
		"similarity_snapshot", "component_face_ids",
		"person_1", "person_2", "person_99",
	}
	for _, f := range forbidden {
		if strings.Contains(strings.ToLower(textOut), strings.ToLower(f)) {
			t.Fatalf("text output contains forbidden field %q", f)
		}
		if strings.Contains(strings.ToLower(string(jsonOut)), strings.ToLower(f)) {
			t.Fatalf("JSON output contains forbidden field %q", f)
		}
	}
	// face_ids 不应作为逐条明细输出（仅聚合计数允许）。检查不出现 "face_ids" 字段名。
	if strings.Contains(textOut, "face_ids") {
		t.Fatalf("text output leaks face_ids field name")
	}
	if strings.Contains(string(jsonOut), `"face_ids"`) {
		t.Fatalf("JSON output leaks face_ids field")
	}
}

// ---- 14. 数据不足 warning ----

func TestIdentityProfileReport_InsufficientDataWarnings(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	// 仅 legacy decision，无 shadow，无反馈。
	insertDecision(t, wdb, modeLegacy, "agree", "", p64(0.5), p64(0.5), p64(0.3), 0.2, 1, algorithmVersionIdentityProfile1)

	report, err := buildReport(path)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	joined := strings.Join(report.Warnings, "|")
	if !strings.Contains(joined, "no shadow decisions") {
		t.Fatalf("missing 'no shadow decisions' warning: %v", report.Warnings)
	}
	if !strings.Contains(joined, "no positive feedback") {
		t.Fatalf("missing 'no positive feedback' warning: %v", report.Warnings)
	}
	if !strings.Contains(joined, "only from legacy mode") {
		t.Fatalf("missing 'only legacy mode' warning: %v", report.Warnings)
	}
	if !strings.Contains(joined, "no representative legacy miss") {
		t.Fatalf("missing 'no legacy miss' warning: %v", report.Warnings)
	}
}

// ---- 15. 数据库查询失败时返回非零错误 ----

func TestIdentityProfileReport_QueryFailureNonZeroExit(t *testing.T) {
	path, wdb := createPopulatedDB(t)
	defer wdb.Close()
	// 删除一张必要表，模拟查询失败。
	if _, err := wdb.Exec(`DROP TABLE people_identity_decisions`); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-db", path, "-format", "text"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("expected non-zero exit code, got 0; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "required table missing") {
		t.Fatalf("expected missing table error, got: %s", errOut.String())
	}
}

// ---- 辅助函数 ----

func p64(v float64) *float64 { return &v }

// extractField 从 JSON 字节中提取顶层字段值（简易实现，仅供测试）。
func extractField(t *testing.T, b []byte, field string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return string(m[field])
}
