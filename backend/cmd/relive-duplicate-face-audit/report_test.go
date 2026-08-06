package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// createAuditDB 在 t.TempDir() 创建一个可写 SQLite 库并建好 photos/faces/people 表。
// 返回库路径与已打开的可写 *sql.DB。
func createAuditDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relive_audit_test.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open writable db: %v", err)
	}
	schema := `
CREATE TABLE photos (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at DATETIME,
  updated_at DATETIME,
  deleted_at DATETIME,
  status TEXT NOT NULL DEFAULT 'active',
  file_path TEXT NOT NULL,
  file_name TEXT NOT NULL,
  file_hash TEXT NOT NULL DEFAULT ''
);
CREATE TABLE faces (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at DATETIME,
  updated_at DATETIME,
  person_id INTEGER,
  photo_id INTEGER NOT NULL,
  embedding BLOB,
  cluster_status TEXT NOT NULL DEFAULT '',
  manual_locked INTEGER NOT NULL DEFAULT 0,
  manual_lock_reason TEXT NOT NULL DEFAULT ''
);
CREATE TABLE people (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at DATETIME,
  updated_at DATETIME,
  name TEXT NOT NULL DEFAULT ''
);
`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return path, db
}

// photoInsert 插入一条活动照片。
func photoInsert(t *testing.T, db *sql.DB, id int, hash, name, path string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO photos (id, created_at, updated_at, status, file_path, file_name, file_hash) VALUES (?,?,?,?,?,?,?)`,
		id, "2026-01-01 00:00:00", "2026-01-02 00:00:00", "active", path, name, hash)
	if err != nil {
		t.Fatalf("insert photo %d: %v", id, err)
	}
}

// faceInsert 插入一条人脸。
func faceInsert(t *testing.T, db *sql.DB, id, photoID, personID int, embedding []byte, clusterStatus string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO faces (id, created_at, updated_at, person_id, photo_id, embedding, cluster_status, manual_locked, manual_lock_reason) VALUES (?,?,?,?,?,?,?,?,?)`,
		id, "2026-01-01 00:00:00", "2026-01-02 00:00:00", personID, photoID, embedding, clusterStatus, 0, "")
	if err != nil {
		t.Fatalf("insert face %d: %v", id, err)
	}
}

func personInsert(t *testing.T, db *sql.DB, id int, name string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO people (id, created_at, updated_at, name) VALUES (?,?,?,?)`,
		id, "2026-01-01 00:00:00", "2026-01-02 00:00:00", name)
	if err != nil {
		t.Fatalf("insert person %d: %v", id, err)
	}
}

func runOpts(dbPath, format string, includePaths bool) (string, int, string) {
	var stdout, stderr bytes.Buffer
	args := []string{"-db", dbPath, "-format", format}
	if includePaths {
		args = append(args, "-include-paths")
	}
	code := run(args, &stdout, &stderr)
	return stdout.String(), code, stderr.String()
}

// ---- Task 1: 参数与只读保护 ----

func TestMissingDBArgReturns2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-db is required") {
		t.Fatalf("stderr missing -db required hint: %q", stderr.String())
	}
}

func TestInvalidFormatReturns2(t *testing.T) {
	path, _ := createAuditDB(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-db", path, "-format", "xml"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "invalid -format") {
		t.Fatalf("stderr missing invalid -format: %q", stderr.String())
	}
}

func TestNonExistentDBNotCreated(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does_not_exist.db")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-db", missing, "-format", "markdown"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 for missing db, got %d", code)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatalf("missing db was created; tool must not create databases")
	}
	if !strings.Contains(stderr.String(), "database file not found") {
		t.Fatalf("stderr missing not-found hint: %q", stderr.String())
	}
}

func TestMissingRequiredTables(t *testing.T) {
	// 建一个没有 photos/faces/people 的空库。
	path := filepath.Join(t.TempDir(), "empty.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE unrelated (id INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	db.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"-db", path, "-format", "markdown"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 for missing tables, got %d", code)
	}
	if !strings.Contains(stderr.String(), "required table missing") {
		t.Fatalf("stderr missing table hint: %q", stderr.String())
	}
}

// ---- Task 2: P0 跨人物相同 embedding ----

func TestP0CrossPersonExactEmbeddingReported(t *testing.T) {
	path, db := createAuditDB(t)
	personInsert(t, db, 1, "Alice")
	personInsert(t, db, 2, "Bob")
	// 两张活动照片，file_hash 相同。
	photoInsert(t, db, 10, "HASHAAAA", "a.jpg", "/photos/a.jpg")
	photoInsert(t, db, 11, "HASHAAAA", "b.jpg", "/photos/b.jpg")
	emb := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	// 两张人脸 embedding 相同，分别归属不同人物。
	faceInsert(t, db, 100, 10, 1, emb, "")
	faceInsert(t, db, 101, 11, 2, emb, "")

	out, code, stderr := runOpts(path, "json", false)
	if code != 0 {
		t.Fatalf("unexpected exit %d: %s", code, stderr)
	}

	var rep Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	if len(rep.ConflictGroups) != 1 {
		t.Fatalf("expected 1 conflict group, got %d", len(rep.ConflictGroups))
	}
	g := rep.ConflictGroups[0]
	if g.FileHash != "HASHAAAA" {
		t.Fatalf("expected file_hash HASHAAAA, got %q", g.FileHash)
	}
	if len(g.Persons) != 2 {
		t.Fatalf("expected 2 persons, got %d", len(g.Persons))
	}
	if len(g.Evidence) != 2 {
		t.Fatalf("expected 2 evidence, got %d", len(g.Evidence))
	}
	// 不应出现原始 embedding 字节。
	if strings.Contains(out, `"embedding"`) {
		t.Fatalf("report leaked embedding field")
	}
	if strings.Contains(out, "1,2,3,4,5,6,7,8") {
		t.Fatalf("report leaked raw embedding bytes")
	}
	// 人脸与照片 ID 应正确列出。
	gotFaces := map[int64]bool{g.Evidence[0].FaceID: true, g.Evidence[1].FaceID: true}
	gotPhotos := map[int64]bool{g.Evidence[0].PhotoID: true, g.Evidence[1].PhotoID: true}
	if !gotFaces[100] || !gotFaces[101] {
		t.Fatalf("missing face ids in evidence: %+v", g.Evidence)
	}
	if !gotPhotos[10] || !gotPhotos[11] {
		t.Fatalf("missing photo ids in evidence: %+v", g.Evidence)
	}
	if rep.Summary.ConflictGroupCount != 1 {
		t.Fatalf("expected conflict count 1, got %d", rep.Summary.ConflictGroupCount)
	}
}

// ---- Task 3: 误报边界、路径脱敏、稳定排序 ----

func TestSamePersonDuplicateNotConflict(t *testing.T) {
	path, db := createAuditDB(t)
	personInsert(t, db, 1, "Alice")
	photoInsert(t, db, 10, "HASHSAME", "a.jpg", "/photos/a.jpg")
	photoInsert(t, db, 11, "HASHSAME", "b.jpg", "/photos/b.jpg")
	emb := []byte{9, 9, 9}
	// 同一人物在两张重复照片中相同 embedding —— 正常数据，不命中。
	faceInsert(t, db, 100, 10, 1, emb, "")
	faceInsert(t, db, 101, 11, 1, emb, "")

	out, code, _ := runOpts(path, "json", false)
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rep.ConflictGroups) != 0 {
		t.Fatalf("same-person duplicate must not conflict, got %d groups", len(rep.ConflictGroups))
	}
}

func TestGroupPhotoDifferentEmbeddingsNotConflict(t *testing.T) {
	path, db := createAuditDB(t)
	personInsert(t, db, 1, "Alice")
	personInsert(t, db, 2, "Bob")
	// 同一张照片（单 file_hash，无重复）—— 不会进入重复组。
	photoInsert(t, db, 10, "UNIQUEHASH", "group.jpg", "/photos/group.jpg")
	// 同一照片两张不同 embedding、两个不同人物（普通合照），但照片非重复。
	faceInsert(t, db, 100, 10, 1, []byte{1, 1, 1}, "")
	faceInsert(t, db, 101, 10, 2, []byte{2, 2, 2}, "")

	out, code, _ := runOpts(path, "json", false)
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rep.ConflictGroups) != 0 {
		t.Fatalf("group photo with no duplicate file must not conflict, got %d", len(rep.ConflictGroups))
	}
}

// TestDuplicateGroupDifferentEmbeddingsNotConflict 覆盖关键边界：
// 两张照片 file_hash 相同（重复组），各有一张归属不同人物的人脸，
// 但 embedding 不同 —— 应当不构成 P0 冲突。
func TestDuplicateGroupDifferentEmbeddingsNotConflict(t *testing.T) {
	path, db := createAuditDB(t)
	personInsert(t, db, 1, "Alice")
	personInsert(t, db, 2, "Bob")
	photoInsert(t, db, 10, "HASHDUP", "a.jpg", "/photos/a.jpg")
	photoInsert(t, db, 11, "HASHDUP", "b.jpg", "/photos/b.jpg")
	// 重复组内不同 embedding、不同人物 —— 不同 fingerprint 不会聚合，不冲突。
	faceInsert(t, db, 100, 10, 1, []byte{1, 1, 1}, "")
	faceInsert(t, db, 101, 11, 2, []byte{2, 2, 2}, "")

	out, code, _ := runOpts(path, "json", false)
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rep.ConflictGroups) != 0 {
		t.Fatalf("duplicate group with different embeddings must not conflict, got %d", len(rep.ConflictGroups))
	}
	// 仍应正确统计读取的人脸数。
	if rep.Summary.ReadAssignedFaceCount != 2 {
		t.Fatalf("expected 2 read assigned faces, got %d", rep.Summary.ReadAssignedFaceCount)
	}
}

func TestExcludedFaceIgnored(t *testing.T) {
	path, db := createAuditDB(t)
	personInsert(t, db, 1, "Alice")
	personInsert(t, db, 2, "Bob")
	photoInsert(t, db, 10, "HASHEXC", "a.jpg", "/photos/a.jpg")
	photoInsert(t, db, 11, "HASHEXC", "b.jpg", "/photos/b.jpg")
	emb := []byte{7, 7, 7}
	// 一张人脸 excluded —— 不应进入聚合。
	faceInsert(t, db, 100, 10, 1, emb, "excluded")
	faceInsert(t, db, 101, 11, 2, emb, "")

	out, code, _ := runOpts(path, "json", false)
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// excluded 被过滤后只剩一个人物，不构成冲突。
	if len(rep.ConflictGroups) != 0 {
		t.Fatalf("excluded face should be ignored, got %d groups", len(rep.ConflictGroups))
	}
}

func TestMissingEmbeddingSkipped(t *testing.T) {
	path, db := createAuditDB(t)
	personInsert(t, db, 1, "Alice")
	personInsert(t, db, 2, "Bob")
	photoInsert(t, db, 10, "HASHMISS", "a.jpg", "/photos/a.jpg")
	photoInsert(t, db, 11, "HASHMISS", "b.jpg", "/photos/b.jpg")
	// 缺失 embedding 的人脸 + 一条正常不同人物 —— 但正常那条只有一条 face 无法跨人物。
	faceInsert(t, db, 100, 10, 1, nil, "")
	faceInsert(t, db, 101, 11, 2, []byte{3, 3, 3}, "")

	out, code, _ := runOpts(path, "json", false)
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rep.Summary.SkippedMissingEmbedding != 1 {
		t.Fatalf("expected 1 skipped missing embedding, got %d", rep.Summary.SkippedMissingEmbedding)
	}
	if len(rep.ConflictGroups) != 0 {
		t.Fatalf("missing embedding must not create conflict, got %d", len(rep.ConflictGroups))
	}
}

func TestPathPrivacyOffByDefault(t *testing.T) {
	path, db := createAuditDB(t)
	personInsert(t, db, 1, "Alice")
	personInsert(t, db, 2, "Bob")
	photoInsert(t, db, 10, "HASHPATH", "a.jpg", "/secret/photos/a.jpg")
	photoInsert(t, db, 11, "HASHPATH", "b.jpg", "/secret/photos/b.jpg")
	emb := []byte{1, 2, 3}
	faceInsert(t, db, 100, 10, 1, emb, "")
	faceInsert(t, db, 101, 11, 2, emb, "")

	md, code, _ := runOpts(path, "markdown", false)
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	if strings.Contains(md, "/secret/photos/") {
		t.Fatalf("markdown default leaked file path:\n%s", md)
	}
	js, _, _ := runOpts(path, "json", false)
	var rep Report
	if err := json.Unmarshal([]byte(js), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, g := range rep.ConflictGroups {
		for _, e := range g.Evidence {
			if e.FilePath != "" {
				t.Fatalf("json default leaked file_path: %q", e.FilePath)
			}
		}
	}

	// 开启 -include-paths 后路径应出现。
	mdOn, _, _ := runOpts(path, "markdown", true)
	if !strings.Contains(mdOn, "/secret/photos/a.jpg") {
		t.Fatalf("include-paths did not emit path in markdown")
	}
}

func TestStableOrderingAcrossRuns(t *testing.T) {
	path, db := createAuditDB(t)
	personInsert(t, db, 1, "Zoe")
	personInsert(t, db, 2, "Amy")
	personInsert(t, db, 3, "Mike")
	// 两个不同 file_hash 组，每组跨人物冲突；故意打乱插入顺序。
	photoInsert(t, db, 20, "HASH-B", "b1.jpg", "/p/b1.jpg")
	photoInsert(t, db, 10, "HASH-A", "a1.jpg", "/p/a1.jpg")
	photoInsert(t, db, 21, "HASH-B", "b2.jpg", "/p/b2.jpg")
	photoInsert(t, db, 11, "HASH-A", "a2.jpg", "/p/a2.jpg")
	embA := []byte{1, 1, 1}
	embB := []byte{2, 2, 2}
	// 组 A：person 3 与 2（ID 降序插入）
	faceInsert(t, db, 201, 20, 3, embB, "")
	faceInsert(t, db, 101, 10, 3, embA, "")
	faceInsert(t, db, 211, 21, 2, embB, "")
	faceInsert(t, db, 111, 11, 2, embA, "")

	// 用固定时间戳构建两份报告比较字节，消除 nowUTC 跨秒不确定性，
	// 同时验证数据部分（ConflictGroups/Summary）跨运行稳定。
	rep1, err := buildReportAt(options{dbPath: path, format: "json"}, "T0")
	if err != nil {
		t.Fatalf("buildReportAt 1: %v", err)
	}
	rep2, err := buildReportAt(options{dbPath: path, format: "json"}, "T0")
	if err != nil {
		t.Fatalf("buildReportAt 2: %v", err)
	}
	b1, _ := json.Marshal(rep1)
	b2, _ := json.Marshal(rep2)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("report not stable across runs with fixed timestamp")
	}
	var rep Report
	if err := json.Unmarshal(b1, &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rep.ConflictGroups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(rep.ConflictGroups))
	}
	// 组按 file_hash 升序。
	if rep.ConflictGroups[0].FileHash != "HASH-A" || rep.ConflictGroups[1].FileHash != "HASH-B" {
		t.Fatalf("groups not sorted by file_hash: %+v", rep.ConflictGroups)
	}
	// 人物按 ID 升序。
	for _, g := range rep.ConflictGroups {
		for i := 1; i < len(g.Persons); i++ {
			if g.Persons[i-1].ID >= g.Persons[i].ID {
				t.Fatalf("persons not sorted by id asc: %+v", g.Persons)
			}
		}
		// 证据按 photo_id, face_id 升序。
		for i := 1; i < len(g.Evidence); i++ {
			a, b := g.Evidence[i-1], g.Evidence[i]
			if a.PhotoID > b.PhotoID || (a.PhotoID == b.PhotoID && a.FaceID >= b.FaceID) {
				t.Fatalf("evidence not sorted: %+v", g.Evidence)
			}
		}
	}
}

// ---- Task 4: 只读性与业务表不变 ----

func TestReadOnlyConnectionRejectsWrites(t *testing.T) {
	path, db := createAuditDB(t)
	personInsert(t, db, 1, "Alice")
	photoInsert(t, db, 10, "HASHRO", "a.jpg", "/p/a.jpg")
	photoInsert(t, db, 11, "HASHRO", "b.jpg", "/p/b.jpg")
	emb := []byte{1, 2, 3}
	faceInsert(t, db, 100, 10, 1, emb, "")
	faceInsert(t, db, 101, 11, 2, emb, "")
	// 用工具同款 mode=ro 连接尝试写。
	ro := openROForTest(t, path)
	defer ro.Close()

	for _, q := range []string{
		`INSERT INTO people (name) VALUES ('spy')`,
		`UPDATE people SET name='hacked' WHERE id=1`,
		`DELETE FROM photos WHERE id=10`,
	} {
		if _, err := ro.Exec(q); err == nil {
			t.Fatalf("write succeeded on read-only connection: %s", q)
		}
	}
}

func TestBusinessTablesUnchanged(t *testing.T) {
	path, db := createAuditDB(t)
	personInsert(t, db, 1, "Alice")
	personInsert(t, db, 2, "Bob")
	photoInsert(t, db, 10, "HASHUC", "a.jpg", "/p/a.jpg")
	photoInsert(t, db, 11, "HASHUC", "b.jpg", "/p/b.jpg")
	emb := []byte{1, 2, 3}
	faceInsert(t, db, 100, 10, 1, emb, "")
	faceInsert(t, db, 101, 11, 2, emb, "")

	counts := func() (int, int, int) {
		var p, f, pe int
		db.QueryRow(`SELECT COUNT(*) FROM photos`).Scan(&p)
		db.QueryRow(`SELECT COUNT(*) FROM faces`).Scan(&f)
		db.QueryRow(`SELECT COUNT(*) FROM people`).Scan(&pe)
		return p, f, pe
	}
	beforeP, beforeF, beforePe := counts()

	if _, code, _ := runOpts(path, "markdown", false); code != 0 {
		t.Fatalf("run failed")
	}
	afterP, afterF, afterPe := counts()
	if beforeP != afterP || beforeF != afterF || beforePe != afterPe {
		t.Fatalf("business table counts changed: before=(%d,%d,%d) after=(%d,%d,%d)",
			beforeP, beforeF, beforePe, afterP, afterF, afterPe)
	}
}

func TestEmptyDBProducesNoConflicts(t *testing.T) {
	path, _ := createAuditDB(t)
	out, code, _ := runOpts(path, "json", false)
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rep.ConflictGroups) != 0 {
		t.Fatalf("expected no conflicts on empty db, got %d", len(rep.ConflictGroups))
	}
	// JSON 不应含 null 数组。
	if strings.Contains(out, "null") {
		t.Fatalf("json contained null: %s", out)
	}
}

// openROForTest 用工具同款 DSN 以只读方式打开库。
func openROForTest(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=ro&_query_only=true&_busy_timeout=60000", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open ro: %v", err)
	}
	if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatalf("set query_only: %v", err)
	}
	return db
}

// 编译期引用 time，避免未使用（部分测试组合下）。
var _ = time.Now
