package main

import (
	"database/sql"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// requiredTables 是 P0 审计必须存在的表。缺失任一返回清晰错误，不尝试修复。
var requiredTables = []string{
	"photos",
	"faces",
	"people",
}

// options 是解析后的命令行参数。
type options struct {
	dbPath       string
	format       string
	includePaths bool
}

// parseArgs 解析命令行参数。返回退出码 2 风格的错误（已打印 usage）。
func parseArgs(args []string, stderr io.Writer) (options, error) {
	fs := flag.NewFlagSet("relive-duplicate-face-audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "Path to a copied SQLite database (required, opened read-only)")
	format := fs.String("format", "markdown", "Output format: markdown or json")
	includePaths := fs.Bool("include-paths", false, "Include original file paths in the report (default false)")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	opts := options{
		dbPath:       strings.TrimSpace(*dbPath),
		format:       *format,
		includePaths: *includePaths,
	}

	if opts.dbPath == "" {
		fmt.Fprintln(stderr, "error: -db is required")
		printUsage(stderr)
		return options{}, errors.New("missing -db")
	}
	if opts.format != "markdown" && opts.format != "json" {
		fmt.Fprintf(stderr, "error: invalid -format %q (allowed: markdown, json)\n", opts.format)
		return options{}, errors.New("invalid -format")
	}
	return opts, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `
Usage:
  relive-duplicate-face-audit -db /path/to/copied-relive.db -format [markdown|json] [-include-paths]

Flags:
  -db             Path to a copied SQLite database (required, opened read-only)
  -format         Output format: markdown (default) or json
  -include-paths  Include original file paths in the report (default false)

The tool opens the database in read-only mode (mode=ro + query_only=ON) and
never creates, migrates, or modifies any data. Only duplicate-file hash groups
are scanned; embeddings are reduced to a SHA-256 fingerprint and never emitted.`)
}

// openReadOnly 以 mode=ro 打开 SQLite，并启用 query_only=ON 双保险。
// 文件不存在时直接失败（mode=ro 本会创建空库，此处显式拒绝）。
func openReadOnly(dbPath string) (*sql.DB, error) {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("resolve db path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("database file not found: %s", abs)
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_query_only=true&_busy_timeout=60000", abs)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set query_only: %w", err)
	}
	return db, nil
}

// checkRequiredTables 校验必要表存在，缺失返回清晰错误。
func checkRequiredTables(db *sql.DB) error {
	for _, t := range requiredTables {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, t,
		).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("required table missing: %s (database is not a relive source)", t)
		}
		if err != nil {
			return fmt.Errorf("check table %s: %w", t, err)
		}
	}
	return nil
}

// ---- 报告数据结构 ----

// Summary 是报告总览统计。所有数值字段零值即代表零计数；列表字段预初始化为空 slice。
type Summary struct {
	DuplicateHashGroupCount  int `json:"duplicate_hash_group_count"`
	DuplicatePhotoRecordCount int `json:"duplicate_photo_record_count"`
	ReadAssignedFaceCount    int `json:"read_assigned_face_count"`
	SkippedMissingEmbedding  int `json:"skipped_missing_embedding"`
	ConflictGroupCount       int `json:"conflict_group_count"`
	InvolvedPersonCount      int `json:"involved_person_count"`
	InvolvedPhotoCount       int `json:"involved_photo_count"`
	InvolvedFaceCount        int `json:"involved_face_count"`
}

// PersonRef 是去重后的人物引用。
type PersonRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Evidence 是一条 P0 冲突证据。
type Evidence struct {
	PhotoID         int64     `json:"photo_id"`
	FaceID          int64     `json:"face_id"`
	PersonID        int64     `json:"person_id"`
	PersonName      string    `json:"person_name"`
	FileName        string    `json:"file_name"`
	FilePath        string    `json:"file_path,omitempty"`
	ManualLocked    bool      `json:"manual_locked"`
	ManualLockReason string   `json:"manual_lock_reason"`
	UpdatedAt       string    `json:"updated_at"`
}

// ConflictGroup 是一个 (file_hash, embedding fingerprint) 跨人物冲突组。
type ConflictGroup struct {
	FileHash    string      `json:"file_hash"`
	Fingerprint string      `json:"fingerprint"`
	Persons     []PersonRef `json:"persons"`
	Evidence    []Evidence  `json:"evidence"`
}

// Report 是完整 P0 审计报告。
type Report struct {
	DatabasePath   string          `json:"database_path"`
	GeneratedAt    string          `json:"generated_at"`
	Summary        Summary         `json:"summary"`
	ConflictGroups []ConflictGroup `json:"conflict_groups"`
}

// newEmptyReport 返回空数据报告，所有 slice 预初始化为空 slice（非 nil），保证 JSON 不出现 null。
func newEmptyReport(dbPath, generatedAt string) *Report {
	return &Report{
		DatabasePath:   dbPath,
		GeneratedAt:    generatedAt,
		ConflictGroups: []ConflictGroup{},
	}
}

// nowUTC 返回稳定的 UTC 时间戳（测试可注入）。
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// faceScanRow 是流式扫描的逐行人脸记录，按 file_hash, photo_id, face_id 排序到达。
type faceScanRow struct {
	FileHash        string
	PhotoID         int64
	FaceID          int64
	PersonID        int64
	PersonName      string
	FileName        string
	FilePath        string
	ManualLocked    bool
	ManualLockReason string
	UpdatedAt       time.Time
	Embedding       []byte
}

// buildReport 打开只读 DB，执行轻量聚合与流式扫描，生成完整报告。
func buildReport(opts options) (*Report, error) {
	return buildReportAt(opts, nowUTC())
}

// buildReportAt 用注入的时间戳构建报告，便于测试消除时间不确定性。
func buildReportAt(opts options, generatedAt string) (*Report, error) {
	db, err := openReadOnly(opts.dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := checkRequiredTables(db); err != nil {
		return nil, err
	}

	report := newEmptyReport(opts.dbPath, generatedAt)

	// 1. 轻量聚合：活动、未删除、非空 file_hash 的重复哈希组数与重复照片记录数。
	if err := collectDuplicateSummary(db, report); err != nil {
		return nil, fmt.Errorf("collect duplicate summary: %w", err)
	}

	// 2. 流式扫描相关人脸，按 file_hash 窗口聚合跨人物冲突。
	if err := collectConflicts(db, report, opts); err != nil {
		return nil, fmt.Errorf("collect conflicts: %w", err)
	}

	finalizeSummary(report)
	sortConflictGroups(report)
	return report, nil
}

// collectDuplicateSummary 统计重复 file_hash 组数与重复照片记录数。
// 仅计入 status='active'、deleted_at IS NULL、file_hash 非空的 photos。
func collectDuplicateSummary(db *sql.DB, report *Report) error {
	// 重复哈希组数：出现 >1 次的 file_hash 数。
	const groupSQL = `
SELECT COUNT(*) FROM (
  SELECT file_hash
  FROM photos
  WHERE status='active' AND deleted_at IS NULL AND file_hash <> ''
  GROUP BY file_hash
  HAVING COUNT(*) > 1
)`
	if err := db.QueryRow(groupSQL).Scan(&report.Summary.DuplicateHashGroupCount); err != nil {
		return err
	}
	// 重复照片记录数：上述组内所有照片行数。
	const photoSQL = `
SELECT COUNT(*) FROM photos
WHERE status='active' AND deleted_at IS NULL AND file_hash <> ''
  AND file_hash IN (
    SELECT file_hash FROM (
      SELECT file_hash
      FROM photos
      WHERE status='active' AND deleted_at IS NULL AND file_hash <> ''
      GROUP BY file_hash
      HAVING COUNT(*) > 1
    )
  )`
	if err := db.QueryRow(photoSQL).Scan(&report.Summary.DuplicatePhotoRecordCount); err != nil {
		return err
	}
	return nil
}

// collectConflicts 流式扫描重复哈希组中的有效、已归属人脸，按 file_hash 窗口聚合。
//
// 查询仅选取属于重复哈希组的活动照片，并 join 已归属、未 excluded 的人脸与人物；
// 结果按 file_hash, photo_id, face_id 排序，使同一 file_hash 的所有行连续到达。
// 当 file_hash 变化时，立即对前一窗口按 embedding fingerprint 聚合并输出跨人物冲突，
// 随后释放该窗口，避免全库 BLOB 自连接与全量常驻内存。
func collectConflicts(db *sql.DB, report *Report, opts options) error {
	const query = `
SELECT
  p.file_hash,
  p.id           AS photo_id,
  p.file_name,
  p.file_path,
  f.id           AS face_id,
  f.person_id,
  pe.name        AS person_name,
  f.manual_locked,
  f.manual_lock_reason,
  f.updated_at,
  f.embedding
FROM photos p
JOIN faces f
  ON f.photo_id = p.id
  AND f.person_id IS NOT NULL
  AND f.person_id <> 0
  AND f.cluster_status <> 'excluded'
JOIN people pe
  ON pe.id = f.person_id
WHERE p.status = 'active'
  AND p.deleted_at IS NULL
  AND p.file_hash <> ''
  AND p.file_hash IN (
    SELECT file_hash FROM (
      SELECT file_hash
      FROM photos
      WHERE status='active' AND deleted_at IS NULL AND file_hash <> ''
      GROUP BY file_hash
      HAVING COUNT(*) > 1
    )
  )
ORDER BY p.file_hash ASC, p.id ASC, f.id ASC`

	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	// fingerprint -> within-window aggregation
	type windowEntry struct {
		persons  map[int64]string // person_id -> name
		evidence []Evidence
	}

	var (
		curHash      string
		curWindow    = make(map[string]*windowEntry)
		emptyEmbedSkip int
		readAssigned    int
	)
	flush := func(hash string) {
		if hash == "" || len(curWindow) == 0 {
			return
		}
		// 收集该窗口中跨人物的 fingerprint，按 fingerprint 升序输出。
		var fps []string
		for fp := range curWindow {
			fps = append(fps, fp)
		}
		sort.Strings(fps)
		for _, fp := range fps {
			e := curWindow[fp]
			if len(e.persons) < 2 {
				continue
			}
			persons := make([]PersonRef, 0, len(e.persons))
			for pid, name := range e.persons {
				persons = append(persons, PersonRef{ID: pid, Name: name})
			}
			sort.Slice(persons, func(i, j int) bool { return persons[i].ID < persons[j].ID })
			// 证据排序：按 photo_id, face_id。
			sort.SliceStable(e.evidence, func(i, j int) bool {
				if e.evidence[i].PhotoID != e.evidence[j].PhotoID {
					return e.evidence[i].PhotoID < e.evidence[j].PhotoID
				}
				return e.evidence[i].FaceID < e.evidence[j].FaceID
			})
			group := ConflictGroup{
				FileHash:    hash,
				Fingerprint: fp,
				Persons:     persons,
				Evidence:    e.evidence,
			}
			if !opts.includePaths {
				for i := range group.Evidence {
					group.Evidence[i].FilePath = ""
				}
			}
			report.ConflictGroups = append(report.ConflictGroups, group)
		}
		curWindow = make(map[string]*windowEntry)
	}

	for rows.Next() {
		var r faceScanRow
		if err := rows.Scan(
			&r.FileHash, &r.PhotoID, &r.FileName, &r.FilePath,
			&r.FaceID, &r.PersonID, &r.PersonName,
			&r.ManualLocked, &r.ManualLockReason, &r.UpdatedAt, &r.Embedding,
		); err != nil {
			return err
		}
		readAssigned++

		if r.FileHash != curHash {
			flush(curHash)
			curHash = r.FileHash
		}

		// 空 embedding 跳过计数，不进入窗口聚合。
		if len(r.Embedding) == 0 {
			emptyEmbedSkip++
			continue
		}
		fp := sha256Fingerprint(r.Embedding)
		e, ok := curWindow[fp]
		if !ok {
			e = &windowEntry{
				persons: make(map[int64]string),
			}
			curWindow[fp] = e
		}
		e.persons[r.PersonID] = r.PersonName
		e.evidence = append(e.evidence, Evidence{
			PhotoID:          r.PhotoID,
			FaceID:           r.FaceID,
			PersonID:         r.PersonID,
			PersonName:       r.PersonName,
			FileName:         r.FileName,
			FilePath:         r.FilePath,
			ManualLocked:     r.ManualLocked,
			ManualLockReason: r.ManualLockReason,
			UpdatedAt:        r.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	flush(curHash)

	report.Summary.ReadAssignedFaceCount = readAssigned
	report.Summary.SkippedMissingEmbedding = emptyEmbedSkip
	return nil
}

// finalizeSummary 根据冲突组回填涉及人物/照片/人脸数（去重）。
func finalizeSummary(report *Report) {
	report.Summary.ConflictGroupCount = len(report.ConflictGroups)
	personSet := make(map[int64]struct{})
	photoSet := make(map[int64]struct{})
	faceSet := make(map[int64]struct{})
	for _, g := range report.ConflictGroups {
		for _, p := range g.Persons {
			personSet[p.ID] = struct{}{}
		}
		for _, e := range g.Evidence {
			photoSet[e.PhotoID] = struct{}{}
			faceSet[e.FaceID] = struct{}{}
		}
	}
	report.Summary.InvolvedPersonCount = len(personSet)
	report.Summary.InvolvedPhotoCount = len(photoSet)
	report.Summary.InvolvedFaceCount = len(faceSet)
}

// sortConflictGroups 按设计契约稳定排序：file_hash 升序，fingerprint 升序。
func sortConflictGroups(report *Report) {
	sort.SliceStable(report.ConflictGroups, func(i, j int) bool {
		if report.ConflictGroups[i].FileHash != report.ConflictGroups[j].FileHash {
			return report.ConflictGroups[i].FileHash < report.ConflictGroups[j].FileHash
		}
		return report.ConflictGroups[i].Fingerprint < report.ConflictGroups[j].Fingerprint
	})
}

// sha256Fingerprint 返回 embedding 原始字节的 SHA-256 hex 摘要。不可逆。
func sha256Fingerprint(embedding []byte) string {
	sum := sha256.Sum256(embedding)
	return hex.EncodeToString(sum[:])
}

// render 按 format 渲染报告。
func render(report *Report, opts options) (string, error) {
	switch opts.format {
	case "json":
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal report: %w", err)
		}
		return string(b), nil
	case "markdown":
		return renderMarkdown(report, opts), nil
	default:
		return "", fmt.Errorf("invalid -format %q", opts.format)
	}
}

// renderMarkdown 输出人类可读的 Markdown 报告。
func renderMarkdown(report *Report, opts options) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Relive P0 重复人脸跨人物归属审计报告\n\n")
	fmt.Fprintf(&sb, "- Database: `%s`\n", report.DatabasePath)
	fmt.Fprintf(&sb, "- Generated: %s\n\n", report.GeneratedAt)

	fmt.Fprintf(&sb, "## 总览\n\n")
	s := report.Summary
	fmt.Fprintf(&sb, "| 指标 | 值 |\n|---|---|\n")
	fmt.Fprintf(&sb, "| 重复文件哈希组数 | %d |\n", s.DuplicateHashGroupCount)
	fmt.Fprintf(&sb, "| 重复照片记录数 | %d |\n", s.DuplicatePhotoRecordCount)
	fmt.Fprintf(&sb, "| 读取已归属有效人脸数 | %d |\n", s.ReadAssignedFaceCount)
	fmt.Fprintf(&sb, "| 缺失 embedding 跳过数 | %d |\n", s.SkippedMissingEmbedding)
	fmt.Fprintf(&sb, "| P0 冲突组数 | %d |\n", s.ConflictGroupCount)
	fmt.Fprintf(&sb, "| 涉及人物数 | %d |\n", s.InvolvedPersonCount)
	fmt.Fprintf(&sb, "| 涉及照片数 | %d |\n", s.InvolvedPhotoCount)
	fmt.Fprintf(&sb, "| 涉及人脸数 | %d |\n\n", s.InvolvedFaceCount)

	if len(report.ConflictGroups) == 0 {
		fmt.Fprintf(&sb, "## 冲突明细\n\n未发现 P0 跨人物归属冲突。\n")
		return sb.String()
	}

	fmt.Fprintf(&sb, "## 冲突明细\n\n")
	for _, g := range report.ConflictGroups {
		fmt.Fprintf(&sb, "### %s\n\n", shortHash(g.FileHash))
		fmt.Fprintf(&sb, "- file_hash: `%s`\n", g.FileHash)
		fmt.Fprintf(&sb, "- embedding fingerprint: `%s`\n", g.Fingerprint)
		fmt.Fprintf(&sb, "- 涉及人物:\n")
		for _, p := range g.Persons {
			fmt.Fprintf(&sb, "  - person_id=%d name=%s\n", p.ID, mdEscapeCell(p.Name))
		}
		fmt.Fprintf(&sb, "\n| photo_id | face_id | person_id | person_name | file_name | manual_locked | manual_lock_reason | updated_at")
		if opts.includePaths {
			fmt.Fprintf(&sb, " | file_path")
		}
		fmt.Fprintf(&sb, " |\n")
		fmt.Fprintf(&sb, "|---|---|---|---|---|---|---|---")
		if opts.includePaths {
			fmt.Fprintf(&sb, " |---")
		}
		fmt.Fprintf(&sb, " |\n")
		for _, e := range g.Evidence {
			locked := "false"
			if e.ManualLocked {
				locked = "true"
			}
			fmt.Fprintf(&sb, "| %d | %d | %d | %s | %s | %s | %s | %s",
				e.PhotoID, e.FaceID, e.PersonID,
				mdEscapeCell(e.PersonName), mdEscapeCell(e.FileName),
				locked, mdEscapeCell(e.ManualLockReason), e.UpdatedAt)
			if opts.includePaths {
				fmt.Fprintf(&sb, " | %s", mdEscapeCell(e.FilePath))
			}
			fmt.Fprintf(&sb, " |\n")
		}
		fmt.Fprintf(&sb, "\n")
	}
	return sb.String()
}

// shortHash 返回 file_hash 的前 12 字符作为标题简写，仅用于可读性；完整值仍在明细中给出。
func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// mdEscapeCell 转义 Markdown 表格单元格中的管道符与换行，避免断列。
// 空值返回占位符，保持表格可读。
func mdEscapeCell(s string) string {
	if s == "" {
		return "-"
	}
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
