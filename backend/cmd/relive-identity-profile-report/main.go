// Package main 实现 relive-identity-profile-report CLI 入口。
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// requiredTables 是报告必须存在的表。缺失任一返回清晰错误，不尝试修复。
var requiredTables = []string{
	"people_feedback_events",
	"people_identity_decisions",
	"person_merge_suggestions",
	"person_merge_suggestion_items",
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 是可测试的入口：参数解析、只读打开、报告生成与输出。返回退出码。
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("relive-identity-profile-report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "Path to a copied SQLite database (required, opened read-only)")
	format := fs.String("format", "text", "Output format: text or json")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if strings.TrimSpace(*dbPath) == "" {
		fmt.Fprintln(stderr, "error: -db is required")
		printUsage(stderr)
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "error: invalid -format %q (allowed: text, json)\n", *format)
		return 2
	}

	report, err := buildReport(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	var out string
	switch *format {
	case "json":
		b, err := report.marshalJSON()
		if err != nil {
			fmt.Fprintf(stderr, "error: marshal report: %v\n", err)
			return 1
		}
		out = string(b)
	case "text":
		out = report.renderText()
	}
	if _, err := fmt.Fprint(stdout, out); err != nil {
		fmt.Fprintf(stderr, "error: write output: %v\n", err)
		return 1
	}
	if !strings.HasSuffix(out, "\n") {
		fmt.Fprintln(stdout)
	}
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `
Usage:
  relive-identity-profile-report -db /path/to/copied-relive.db -format [text|json]

Flags:
  -db      Path to a copied SQLite database (required, opened read-only)
  -format  Output format: text (default) or json

The tool opens the database in read-only mode (mode=ro + query_only=ON) and
never creates, migrates, or modifies any data. Only aggregate statistics are
reported; no names, paths, embeddings, or per-face details are emitted.`)
}

// openReadOnly 以 mode=ro 打开 SQLite，并启用 query_only=ON 双保险。
// 文件不存在时直接失败（mode=ro 不创建空库）。
func openReadOnly(dbPath string) (*sql.DB, error) {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("resolve db path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		// 文件不存在（或不可访问）：mode=ro 本会创建空库，此处显式拒绝。
		return nil, fmt.Errorf("database file not found: %s", abs)
	}
	// mode=ro 必须配合 file: URI 才生效。
	dsn := fmt.Sprintf("file:%s?mode=ro&_query_only=true&_busy_timeout=60000", abs)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// 单连接，避免在只读库上开并发。
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
		// sqlite_master 查询本身在 query_only 下允许。
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, t,
		).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("required table missing: %s (database is not a relive calibration source)", t)
		}
		if err != nil {
			return fmt.Errorf("check table %s: %w", t, err)
		}
	}
	return nil
}

// feedbackRow 是 people_feedback_events 的读取视图。
type feedbackRow struct {
	ID               int64
	CreatedAt        time.Time
	EventType        string
	TargetPersonID   int64
	SourcePersonIDs  string
	AlgorithmVersion string
}

// decisionRow 是 people_identity_decisions 的读取视图。
type decisionRow struct {
	Mode               string
	Decision           string
	Reason             string
	LegacyScore        *float64
	ProfileBestScore   *float64
	ProfileSecondScore *float64
	Margin             float64
	ElapsedMS          int
	AlgorithmVersion   string
	CreatedAt          time.Time
}

// suggestionItemRow 是 person_merge_suggestion_items 的读取视图。
type suggestionItemRow struct {
	SuggestionID      int64
	CandidatePersonID int64
	Rank              int
	MatchSource       string
}

// suggestionRow 是 person_merge_suggestions 的读取视图。
type suggestionRow struct {
	ID             int64
	TargetPersonID int64
	Status         string
	CreatedAt      time.Time
}

// itemKey 是 (suggestionID, candidatePersonID) 复合键，用于查 rank。
type itemKey struct {
	sugID     int64
	candidate int64
}

// buildReport 打开只读 DB，聚合所有指标，生成完整报告。
func buildReport(dbPath string) (*Report, error) {
	db, err := openReadOnly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := checkRequiredTables(db); err != nil {
		return nil, err
	}

	report := newEmptyReport(dbPath, nowUTC())
	if err := collectFeedback(db, report); err != nil {
		return nil, fmt.Errorf("collect feedback: %w", err)
	}
	if err := collectDecisions(db, report); err != nil {
		return nil, fmt.Errorf("collect decisions: %w", err)
	}
	if err := collectSuggestions(db, report); err != nil {
		return nil, fmt.Errorf("collect suggestions: %w", err)
	}
	collectWarnings(report)
	return report, nil
}

// nowUTC 返回稳定的 UTC 时间戳（用于测试可注入时替换为固定值）。
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// collectFeedback 读取并聚合人工反馈事件。
func collectFeedback(db *sql.DB, report *Report) error {
	rows, err := db.Query(`SELECT id, created_at, event_type, target_person_id, source_person_ids, algorithm_version FROM people_feedback_events ORDER BY id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var minT, maxT time.Time
	for rows.Next() {
		var r feedbackRow
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.EventType, &r.TargetPersonID, &r.SourcePersonIDs, &r.AlgorithmVersion); err != nil {
			report.Coverage.SkippedRecords++
			continue
		}
		report.Coverage.FeedbackTotal++
		report.Coverage.FeedbackByEvent[r.EventType]++
		report.Feedback.ByEvent[r.EventType]++
		if report.Feedback.ByEventAlgorithm[r.EventType] == nil {
			report.Feedback.ByEventAlgorithm[r.EventType] = map[string]int{}
		}
		algVer := normalizeAlgorithmVersion(r.AlgorithmVersion)
		report.Feedback.ByEventAlgorithm[r.EventType][algVer]++
		report.Coverage.AlgorithmVersion[algVer]++
		switch algVer {
		case algorithmVersionManual:
			report.Feedback.ManualTotal++
		case algorithmVersionSuggestionV1:
			report.Feedback.SuggestionTotal++
		default:
			report.Feedback.UnknownVersion++
		}
		if minT.IsZero() || r.CreatedAt.Before(minT) {
			minT = r.CreatedAt
		}
		if maxT.IsZero() || r.CreatedAt.After(maxT) {
			maxT = r.CreatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !minT.IsZero() {
		report.Coverage.TimeRangeStart = minT.UTC().Format(time.RFC3339)
		report.Coverage.TimeRangeEnd = maxT.UTC().Format(time.RFC3339)
	}
	return nil
}

// normalizeAlgorithmVersion 把空串归为 unknown，其余原样返回。
func normalizeAlgorithmVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unknown"
	}
	return v
}

// collectDecisions 读取并聚合 identity 决策遥测与分数分布。
func collectDecisions(db *sql.DB, report *Report) error {
	rows, err := db.Query(`SELECT mode, decision, reason, legacy_score, profile_best_score, profile_second_score, margin, elapsed_milliseconds, algorithm_version, created_at FROM people_identity_decisions ORDER BY id ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var legacyVals, bestVals, secondVals []float64
	var marginVals []float64
	var elapsedVals []int
	var legacyIgnored, bestIgnored, secondIgnored, marginIgnored, elapsedIgnored int
	var minT, maxT time.Time

	for rows.Next() {
		var d decisionRow
		if err := rows.Scan(&d.Mode, &d.Decision, &d.Reason, &d.LegacyScore, &d.ProfileBestScore, &d.ProfileSecondScore, &d.Margin, &d.ElapsedMS, &d.AlgorithmVersion, &d.CreatedAt); err != nil {
			report.Coverage.SkippedRecords++
			continue
		}
		report.Coverage.DecisionTotal++
		report.Coverage.DecisionByMode[d.Mode]++
		report.IdentityDecision.ByDecision[d.Decision]++
		report.IdentityDecision.ByMode[d.Mode]++
		if d.Reason != "" {
			report.IdentityDecision.ByReason[d.Reason]++
		}
		algVer := normalizeAlgorithmVersion(d.AlgorithmVersion)
		report.Coverage.AlgorithmVersion[algVer]++

		legacyVals = appendValidFloatPtr(legacyVals, d.LegacyScore, &legacyIgnored)
		bestVals = appendValidFloatPtr(bestVals, d.ProfileBestScore, &bestIgnored)
		secondVals = appendValidFloatPtr(secondVals, d.ProfileSecondScore, &secondIgnored)
		if math.IsNaN(d.Margin) || math.IsInf(d.Margin, 0) {
			marginIgnored++
		} else {
			marginVals = append(marginVals, d.Margin)
		}
		if d.ElapsedMS < 0 {
			elapsedIgnored++
		} else {
			elapsedVals = append(elapsedVals, d.ElapsedMS)
		}

		if minT.IsZero() || d.CreatedAt.Before(minT) {
			minT = d.CreatedAt
		}
		if maxT.IsZero() || d.CreatedAt.After(maxT) {
			maxT = d.CreatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	report.ScoreDistribution.LegacyScore = summarizeDistribution(buildDistribution(legacyVals, legacyIgnored, percentiles))
	report.ScoreDistribution.ProfileBestScore = summarizeDistribution(buildDistribution(bestVals, bestIgnored, percentiles))
	report.ScoreDistribution.ProfileSecondScore = summarizeDistribution(buildDistribution(secondVals, secondIgnored, percentiles))
	report.ScoreDistribution.Margin = summarizeDistribution(buildDistribution(marginVals, marginIgnored, marginPercentiles))
	report.ScoreDistribution.ElapsedMS = buildElapsedDistribution(elapsedVals, elapsedIgnored)

	// 比例计算。分母为 decision 总数。
	total := report.Coverage.DecisionTotal
	disagree := report.IdentityDecision.ByDecision["disagree"]
	legacyMissHit := report.IdentityDecision.ByDecision["legacy_miss_profile_hit"]
	profileUnavailable := report.IdentityDecision.ByDecision["profile_unavailable"]
	profileBlocked := report.IdentityDecision.ByDecision["profile_blocked"]
	rescueApplied := report.IdentityDecision.ByDecision["rescue_applied"]
	report.IdentityDecision.DisagreementRate = newRate(disagree, total)
	report.IdentityDecision.LegacyMissProfileHit = newRate(legacyMissHit, total)
	report.IdentityDecision.ProfileUnavailableRate = newRate(profileUnavailable, total)
	report.IdentityDecision.ProfileBlockedRate = newRate(profileBlocked, total)
	report.IdentityDecision.RescueAppliedCount = rescueApplied

	// decision 的时间范围也纳入 coverage（若 feedback 为空时仍提供时间范围）。
	if report.Coverage.TimeRangeStart == "" && !minT.IsZero() {
		report.Coverage.TimeRangeStart = minT.UTC().Format(time.RFC3339)
		report.Coverage.TimeRangeEnd = maxT.UTC().Format(time.RFC3339)
	}
	return nil
}

// collectSuggestions 读取建议与建议项，并计算合并建议效果与 Recall@K。
func collectSuggestions(db *sql.DB, report *Report) error {
	// 建议主表计数。
	sugRows, err := db.Query(`SELECT id, target_person_id, status, created_at FROM person_merge_suggestions ORDER BY id ASC`)
	if err != nil {
		return err
	}
	var suggestions []suggestionRow
	for sugRows.Next() {
		var s suggestionRow
		if err := sugRows.Scan(&s.ID, &s.TargetPersonID, &s.Status, &s.CreatedAt); err != nil {
			report.Coverage.SkippedRecords++
			continue
		}
		suggestions = append(suggestions, s)
		report.Coverage.SuggestionTotal++
	}
	sugRows.Close()
	if err := sugRows.Err(); err != nil {
		return err
	}

	// 建议项：构建 (suggestionID, candidatePersonID) -> rank 映射，并按来源计数。
	itemRows, err := db.Query(`SELECT suggestion_id, candidate_person_id, rank, match_source FROM person_merge_suggestion_items ORDER BY id ASC`)
	if err != nil {
		return err
	}
	rankByKey := make(map[itemKey]int)
	for itemRows.Next() {
		var it suggestionItemRow
		if err := itemRows.Scan(&it.SuggestionID, &it.CandidatePersonID, &it.Rank, &it.MatchSource); err != nil {
			report.Coverage.SkippedRecords++
			continue
		}
		report.Coverage.SuggestionItems++
		rankByKey[itemKey{it.SuggestionID, it.CandidatePersonID}] = it.Rank
		switch it.MatchSource {
		case matchSourceIdentityProfile:
			report.SuggestionEffect.IdentityProfileHits++
		case matchSourceLegacy:
			report.SuggestionEffect.LegacyHits++
		}
	}
	itemRows.Close()
	if err := itemRows.Err(); err != nil {
		return err
	}

	// 关联 feedback 事件与建议项计算 Recall@K。
	// 关联策略：对每条 merge_confirmed / merge_rejected 事件，按 (target_person_id, candidate)
	// 在所有建议项中查找 rank。一条事件可能涉及多个候选（SourcePersonIDs JSON 数组）。
	// 取该事件所有候选中可关联的最小 rank 作为该事件的命中 rank。
	// 仅 suggestion-v1 事件参与（manual 不计入分母）。
	fbRows, err := db.Query(`SELECT event_type, target_person_id, source_person_ids, algorithm_version FROM people_feedback_events ORDER BY id ASC`)
	if err != nil {
		return err
	}
	defer fbRows.Close()

	var suggestionDrivenConfirmed, suggestionDrivenRejected int
	var recallHits [21]int // recallHits[k] = 命中 rank<=k 的事件数（k=0..20）
	var evaluableConfirmed int
	var unmatched int

	for fbRows.Next() {
		var eventType string
		var targetPersonID int64
		var sourcePersonIDs, algVer string
		if err := fbRows.Scan(&eventType, &targetPersonID, &sourcePersonIDs, &algVer); err != nil {
			report.Coverage.SkippedRecords++
			continue
		}
		if normalizeAlgorithmVersion(algVer) != algorithmVersionSuggestionV1 {
			continue
		}
		if eventType != eventTypeMergeConfirmed && eventType != eventTypeMergeRejected {
			continue
		}
		if eventType == eventTypeMergeConfirmed {
			suggestionDrivenConfirmed++
		} else {
			suggestionDrivenRejected++
		}

		candidates, ok := parseFeedbackIDs(sourcePersonIDs)
		if !ok || len(candidates) == 0 {
			unmatched++
			continue
		}
		// 在所有建议项中查找 (targetPersonID, candidate) 对应的 rank。
		hitRank, found := lookupMinRank(rankByKey, suggestions, targetPersonID, candidates)
		if !found {
			unmatched++
			continue
		}
		// 仅 merge_confirmed 事件计入 Recall 分母（Recall 衡量被确认合并的候选是否排在前 K）。
		if eventType != eventTypeMergeConfirmed {
			continue
		}
		evaluableConfirmed++
		for k := 1; k <= 20; k++ {
			if hitRank <= k {
				recallHits[k]++
			}
		}
	}
	if err := fbRows.Err(); err != nil {
		return err
	}

	report.SuggestionEffect.SuggestionConfirmed = suggestionDrivenConfirmed
	report.SuggestionEffect.SuggestionRejected = suggestionDrivenRejected
	totalSuggestionDriven := suggestionDrivenConfirmed + suggestionDrivenRejected
	report.SuggestionEffect.AcceptanceRate = newRate(suggestionDrivenConfirmed, totalSuggestionDriven)
	report.SuggestionEffect.RejectionRate = newRate(suggestionDrivenRejected, totalSuggestionDriven)
	report.SuggestionEffect.UnmatchedFeedbackCount = unmatched
	// Recall@K：hits/evaluable（Recall），evaluable/totalSuggestionDriven（覆盖率）。
	report.SuggestionEffect.RecallAt1 = newCoverage(recallHits[1], evaluableConfirmed, suggestionDrivenConfirmed)
	report.SuggestionEffect.RecallAt5 = newCoverage(recallHits[5], evaluableConfirmed, suggestionDrivenConfirmed)
	report.SuggestionEffect.RecallAt10 = newCoverage(recallHits[10], evaluableConfirmed, suggestionDrivenConfirmed)
	report.SuggestionEffect.RecallAt20 = newCoverage(recallHits[20], evaluableConfirmed, suggestionDrivenConfirmed)
	return nil
}

// parseFeedbackIDs 解析 SourcePersonIDs 的 JSON 数组字符串（如 "[1,2,3]"）。
// 返回候选 ID 列表与解析是否成功。
func parseFeedbackIDs(s string) ([]int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	var ids []int64
	if err := json.Unmarshal([]byte(s), &ids); err != nil {
		return nil, false
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			out = append(out, id)
		}
	}
	return out, true
}

// lookupMinRank 在建议项映射中查找 (targetPersonID, candidate) 的最小 rank。
// 一条建议的 TargetPersonID 与事件一致，且该候选出现在其 items 中。
// 若同一 (target, candidate) 在多条建议中出现，取最小 rank（最靠前的排序）。
func lookupMinRank(rankByKey map[itemKey]int, suggestions []suggestionRow, targetPersonID int64, candidates []int64) (int, bool) {
	// 找出该 target 的所有 suggestion id。
	sugIDs := make([]int64, 0)
	for _, s := range suggestions {
		if s.TargetPersonID == targetPersonID {
			sugIDs = append(sugIDs, s.ID)
		}
	}
	if len(sugIDs) == 0 {
		return 0, false
	}
	minRank := math.MaxInt32
	found := false
	for _, cand := range candidates {
		for _, sid := range sugIDs {
			r, ok := rankByKey[itemKey{sid, cand}]
			if !ok {
				continue
			}
			found = true
			if r < minRank {
				minRank = r
			}
		}
	}
	if !found {
		return 0, false
	}
	return minRank, true
}

// collectWarnings 根据数据覆盖情况生成数据不足 warning。报告只提供证据，不输出
// safe_to_enable_rescue=true 之类的自动结论。
func collectWarnings(report *Report) {
	warnings := []string{}

	shadowDecisions := report.Coverage.DecisionByMode[modeShadow]
	if shadowDecisions == 0 {
		warnings = append(warnings, "no shadow decisions: shadow telemetry not yet collected")
	}

	positiveFeedback := report.Feedback.ByEvent[eventTypeMergeConfirmed]
	negativeFeedback := report.Feedback.ByEvent[eventTypeMergeRejected]
	if positiveFeedback == 0 {
		warnings = append(warnings, "no positive feedback (merge_confirmed): acceptance cannot be evaluated")
	}
	if negativeFeedback == 0 {
		warnings = append(warnings, "no negative feedback (merge_rejected): rejection cannot be evaluated")
	}

	// Recall 覆盖率：evaluable / suggestionDrivenConfirmed。
	if report.SuggestionEffect.RecallAt1.Total == 0 || report.SuggestionEffect.RecallAt1.Evaluable == 0 {
		warnings = append(warnings, "Recall coverage insufficient: no suggestion-driven confirmed merges with rank linkage")
	} else {
		// 覆盖率低于 50% 视为不足。
		r := float64(report.SuggestionEffect.RecallAt1.Evaluable) / float64(report.SuggestionEffect.RecallAt1.Total)
		if r < 0.5 {
			warnings = append(warnings, fmt.Sprintf("Recall coverage low: %.2f of confirmed merges linkable to suggestions", r))
		}
	}

	if report.Coverage.DecisionTotal < minSampleForDistribution {
		warnings = append(warnings, fmt.Sprintf("identity decision sample size %d < %d: distributions not representative", report.Coverage.DecisionTotal, minSampleForDistribution))
	}

	legacyMissHit := report.IdentityDecision.ByDecision["legacy_miss_profile_hit"]
	legacyMissMiss := report.IdentityDecision.ByDecision["legacy_miss_profile_miss"]
	if legacyMissHit+legacyMissMiss == 0 {
		warnings = append(warnings, "no representative legacy miss events: profile-vs-legacy gain cannot be measured")
	}

	// 仅 legacy 模式数据。
	if report.Coverage.DecisionTotal > 0 && shadowDecisions == 0 && report.Coverage.DecisionByMode[modePrimary] == 0 && report.Coverage.DecisionByMode[modeRescue] == 0 {
		warnings = append(warnings, "data only from legacy mode: shadow/rescue comparison unavailable")
	}

	if report.SuggestionEffect.UnmatchedFeedbackCount > 0 {
		warnings = append(warnings, fmt.Sprintf("%d feedback events could not be linked to suggestions (counted as unmatched, not used for Recall)", report.SuggestionEffect.UnmatchedFeedbackCount))
	}

	sort.Strings(warnings)
	report.Warnings = warnings
}

// 保留 strconv 引用（用于未来扩展）。
var _ = strconv.Itoa
