// Package main 实现 relive-identity-profile-report 只读离线校准报告工具。
//
// 报告工具以 SQLite 只读方式（mode=ro + PRAGMA query_only=ON）打开显式指定的
// 数据库副本，聚合人工反馈、identity 决策遥测、合并建议命中情况与画像匹配分数分布，
// 为 shadow/rescue 阈值校准提供证据。本包不创建、合并、拆分或移动人物，不执行迁移，
// 不修改任何业务数据，不输出人名、文件路径、缩略图路径、embedding 或逐条人脸信息。
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// 报告工具识别的稳定枚举常量。直接复用 model/repository/service 中的字符串值，
// 不导入这些包以避免触发 database.Init 等副作用。任一来源常量变更时需同步本表。
const (
	eventTypeMergeConfirmed  = "merge_confirmed"
	eventTypeMergeRejected   = "merge_rejected"
	eventTypeFaceMoved       = "face_moved"
	eventTypePersonSplit     = "person_split"
	eventTypePersonDissolved = "person_dissolved"

	algorithmVersionSuggestionV1     = "suggestion-v1"
	algorithmVersionManual           = "manual"
	algorithmVersionIdentityProfile1 = "identity-profile-v1"

	modeLegacy  = "legacy"
	modeShadow  = "shadow"
	modeRescue  = "rescue"
	modePrimary = "primary"

	matchSourceLegacy          = "legacy"
	matchSourceIdentityProfile = "identity_profile"
)

// notAvailable 是覆盖率不足或样本不可靠指标的占位标记。
const notAvailable = "not_available"

// 分位数刻度。
const (
	percentileP10 = 10.0
	percentileP25 = 25.0
	percentileP50 = 50.0
	percentileP90 = 90.0
	percentileP95 = 95.0
	percentileP99 = 99.0
)

// minSampleForDistribution 是分数分布分位数被认为"有代表性"的最小样本数。
// 低于此值仍会输出原始分布，但附带 warning。
const minSampleForDistribution = 30

// percentiles 定义报告输出的固定分位数刻度集合。
var percentiles = []float64{percentileP50, percentileP90, percentileP95, percentileP99}

// marginPercentiles 定义 margin 的固定分位数刻度集合。
var marginPercentiles = []float64{percentileP10, percentileP25, percentileP50, percentileP90, percentileP95}

// percentile 是确定性纯函数分位数：升序排序后取 ceil(p/100 * n) 位（1-based），
// p 钳制到 [0,100]，NaN/Inf 过滤，空输入返回 0。与 service.percentileSimilarity 一致。
func percentile(values []float64, p float64) float64 {
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return 0
	}
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	valid := make([]float64, 0, len(values))
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		valid = append(valid, v)
	}
	if len(valid) == 0 {
		return 0
	}
	sort.Float64s(valid)
	rank := int(math.Ceil(p / 100.0 * float64(len(valid))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(valid) {
		rank = len(valid)
	}
	return valid[rank-1]
}

// distribution 从一组 float64 值构建分位数分布。返回样本数、被忽略数（nil/NaN/Inf）
// 与各分位数。values 中 nil 指针由调用方处理；本函数只处理已收集的 float64。
type distribution struct {
	Samples     int                `json:"samples"`
	Ignored     int                `json:"ignored"`
	Percentiles map[string]float64 `json:"percentiles"`
}

// buildDistribution 收集 values（先经 appendValidFloat 过滤），按 scales 计算分位数。
// ignored 计入被过滤的非有限值数量。空集合输出零值 Percentiles（空 map，非 nil）。
func buildDistribution(values []float64, ignored int, scales []float64) distribution {
	d := distribution{
		Percentiles: make(map[string]float64, len(scales)),
		Ignored:     ignored,
	}
	valid := make([]float64, 0, len(values))
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			d.Ignored++
			continue
		}
		valid = append(valid, v)
	}
	d.Samples = len(valid)
	for _, p := range scales {
		d.Percentiles[fmt.Sprintf("P%d", int(p))] = percentile(valid, p)
	}
	return d
}

// appendValidFloatPtr 将 *float64 解引用后追加到 dst，nil 或非有限值计入 *ignored。
func appendValidFloatPtr(dst []float64, v *float64, ignored *int) []float64 {
	if v == nil {
		*ignored++
		return dst
	}
	if math.IsNaN(*v) || math.IsInf(*v, 0) {
		*ignored++
		return dst
	}
	return append(dst, *v)
}

// distributionSummary 是 JSON 输出中分数分布的稳定结构。null 永不出现：
// 空集合时 Percentiles 为 {} 而非 null，samples/ignored 为 0。
type distributionSummary struct {
	Samples     int                `json:"samples"`
	Ignored     int                `json:"ignored"`
	Percentiles map[string]float64 `json:"percentiles"`
}

// summarizeDistribution 把内部 distribution 收敛为 JSON 摘要。
func summarizeDistribution(d distribution) distributionSummary {
	return distributionSummary{
		Samples:     d.Samples,
		Ignored:     d.Ignored,
		Percentiles: d.Percentiles,
	}
}

// ratio 计算分子/分母比例，分母为 0 返回 notAvailable 字符串。结果保留 4 位小数。
func ratio(num, denom int) string {
	if denom == 0 {
		return notAvailable
	}
	return fmt.Sprintf("%.4f", float64(num)/float64(denom))
}

// rate 是带分子分母的比率结构，便于 JSON 同时输出 numerator/denominator/value。
type rate struct {
	Numerator   int    `json:"numerator"`
	Denominator int    `json:"denominator"`
	Value       string `json:"value"` // notAvailable 或 "0.1234"
}

func newRate(num, denom int) rate {
	return rate{Numerator: num, Denominator: denom, Value: ratio(num, denom)}
}

// elapsedDistribution 是耗时分布，额外携带 max（而非 P99 之外的额外刻度）。
type elapsedDistribution struct {
	Samples     int                `json:"samples"`
	Ignored     int                `json:"ignored"`
	Percentiles map[string]float64 `json:"percentiles"`
	Max         float64            `json:"max"`
}

func buildElapsedDistribution(values []int, ignored int) elapsedDistribution {
	d := elapsedDistribution{
		Percentiles: make(map[string]float64, len(percentiles)),
		Ignored:     ignored,
	}
	floats := make([]float64, 0, len(values))
	var maxV float64
	for _, v := range values {
		if v < 0 {
			d.Ignored++
			continue
		}
		f := float64(v)
		if f > maxV {
			maxV = f
		}
		floats = append(floats, f)
	}
	d.Samples = len(floats)
	d.Max = maxV
	for _, p := range percentiles {
		d.Percentiles[fmt.Sprintf("P%d", int(p))] = percentile(floats, p)
	}
	return d
}

// coverage 是 Recall@K 等指标的命中比例与覆盖率。
//   - Hits：rank <= K 的可关联确认事件数
//   - Evaluable：可关联到建议项的确认事件总数（Recall 分母）
//   - Total：suggestion 驱动的确认事件总数（覆盖率分母，含未关联）
//   - Value：Hits / Evaluable，分母为 0 时 notAvailable
//   - Coverage：Evaluable / Total，衡量 Recall 可计算的比例
type coverage struct {
	Hits      int    `json:"hits"`
	Evaluable int    `json:"evaluable"`
	Total     int    `json:"total"`
	Value     string `json:"value"`    // notAvailable 或 "0.1234"
	Coverage  string `json:"coverage"` // notAvailable 或覆盖率比例
}

func newCoverage(hits, evaluable, total int) coverage {
	return coverage{
		Hits:      hits,
		Evaluable: evaluable,
		Total:     total,
		Value:     ratio(hits, evaluable),
		Coverage:  ratio(evaluable, total),
	}
}

// ---- 报告数据结构 ----

// CoverageReport 是第六节"数据覆盖范围"的聚合。
type CoverageReport struct {
	TimeRangeStart   string         `json:"time_range_start"`
	TimeRangeEnd     string         `json:"time_range_end"`
	FeedbackTotal    int            `json:"feedback_total"`
	FeedbackByEvent  map[string]int `json:"feedback_by_event"`
	DecisionTotal    int            `json:"decision_total"`
	DecisionByMode   map[string]int `json:"decision_by_mode"`
	SuggestionTotal  int            `json:"suggestion_total"`
	SuggestionItems  int            `json:"suggestion_items"`
	AlgorithmVersion map[string]int `json:"algorithm_version"`
	SkippedRecords   int            `json:"skipped_records"`
}

// FeedbackReport 是人工反馈分类统计。
type FeedbackReport struct {
	ByEvent map[string]int `json:"by_event"`
	// ByEventAlgorithm 按 event_type → algorithm_version 计数。
	ByEventAlgorithm map[string]map[string]int `json:"by_event_algorithm"`
	ManualTotal      int                       `json:"manual_total"`
	SuggestionTotal  int                       `json:"suggestion_total"`
	UnknownVersion   int                       `json:"unknown_version"`
}

// SuggestionEffectReport 是合并建议效果统计。
type SuggestionEffectReport struct {
	SuggestionConfirmed int  `json:"suggestion_confirmed"`
	SuggestionRejected  int  `json:"suggestion_rejected"`
	AcceptanceRate      rate `json:"acceptance_rate"`
	RejectionRate       rate `json:"rejection_rate"`
	IdentityProfileHits int  `json:"identity_profile_hits"`
	LegacyHits          int  `json:"legacy_hits"`
	// Recall@K 基于 person_merge_suggestion_items.rank。
	RecallAt1  coverage `json:"recall_at_1"`
	RecallAt5  coverage `json:"recall_at_5"`
	RecallAt10 coverage `json:"recall_at_10"`
	RecallAt20 coverage `json:"recall_at_20"`
	// UnmatchedFeedbackCount 是无法关联到建议的确认事件数。
	UnmatchedFeedbackCount int `json:"unmatched_feedback_count"`
}

// IdentityDecisionReport 是 identity decision 分类与比例统计。
type IdentityDecisionReport struct {
	ByDecision map[string]int `json:"by_decision"`
	ByMode     map[string]int `json:"by_mode"`
	ByReason   map[string]int `json:"by_reason"`
	// 比例，分母为 decision 总数。
	DisagreementRate       rate `json:"disagreement_rate"`
	LegacyMissProfileHit   rate `json:"legacy_miss_profile_hit_rate"`
	ProfileUnavailableRate rate `json:"profile_unavailable_rate"`
	ProfileBlockedRate     rate `json:"profile_blocked_rate"`
	RescueAppliedCount     int  `json:"rescue_applied_count"`
}

// ScoreDistributionReport 是分数与 margin 分布。
type ScoreDistributionReport struct {
	LegacyScore        distributionSummary `json:"legacy_score"`
	ProfileBestScore   distributionSummary `json:"profile_best_score"`
	ProfileSecondScore distributionSummary `json:"profile_second_score"`
	Margin             distributionSummary `json:"margin"`
	ElapsedMS          elapsedDistribution `json:"elapsed_ms"`
}

// Report 是完整校准报告。
type Report struct {
	DatabasePath      string                  `json:"database_path"`
	GeneratedAt       string                  `json:"generated_at"`
	Coverage          CoverageReport          `json:"coverage"`
	Feedback          FeedbackReport          `json:"feedback"`
	SuggestionEffect  SuggestionEffectReport  `json:"suggestion_effect"`
	IdentityDecision  IdentityDecisionReport  `json:"identity_decision"`
	ScoreDistribution ScoreDistributionReport `json:"score_distribution"`
	Warnings          []string                `json:"warnings"`
}

// newEmptyReport 返回空数据报告，所有 map 预初始化为空 map（非 nil），
// 列表预初始化为空 slice（非 nil），保证 JSON 不出现 null。
func newEmptyReport(dbPath, generatedAt string) *Report {
	return &Report{
		DatabasePath: dbPath,
		GeneratedAt:  generatedAt,
		Coverage: CoverageReport{
			FeedbackByEvent:  map[string]int{},
			DecisionByMode:   map[string]int{},
			AlgorithmVersion: map[string]int{},
		},
		Feedback: FeedbackReport{
			ByEvent:          map[string]int{},
			ByEventAlgorithm: map[string]map[string]int{},
		},
		SuggestionEffect: SuggestionEffectReport{
			AcceptanceRate: newRate(0, 0),
			RejectionRate:  newRate(0, 0),
			RecallAt1:      newCoverage(0, 0, 0),
			RecallAt5:      newCoverage(0, 0, 0),
			RecallAt10:     newCoverage(0, 0, 0),
			RecallAt20:     newCoverage(0, 0, 0),
		},
		IdentityDecision: IdentityDecisionReport{
			ByDecision:             map[string]int{},
			ByMode:                 map[string]int{},
			ByReason:               map[string]int{},
			DisagreementRate:       newRate(0, 0),
			LegacyMissProfileHit:   newRate(0, 0),
			ProfileUnavailableRate: newRate(0, 0),
			ProfileBlockedRate:     newRate(0, 0),
		},
		ScoreDistribution: ScoreDistributionReport{
			LegacyScore:        summarizeDistribution(buildDistribution(nil, 0, percentiles)),
			ProfileBestScore:   summarizeDistribution(buildDistribution(nil, 0, percentiles)),
			ProfileSecondScore: summarizeDistribution(buildDistribution(nil, 0, percentiles)),
			Margin:             summarizeDistribution(buildDistribution(nil, 0, marginPercentiles)),
			ElapsedMS:          buildElapsedDistribution(nil, 0),
		},
		Warnings: []string{},
	}
}

// marshalJSON 稳定序列化报告，按固定字段顺序输出，确保两次运行字节级一致。
func (r *Report) marshalJSON() ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// renderText 输出人类可读的 text 报告。所有比例同时显示分子/分母。
func (r *Report) renderText() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Relive Identity Profile Calibration Report\n")
	fmt.Fprintf(&sb, "==========================================\n")
	fmt.Fprintf(&sb, "Database: %s\n", r.DatabasePath)
	fmt.Fprintf(&sb, "Generated: %s\n\n", r.GeneratedAt)

	// 1. 数据覆盖范围
	fmt.Fprintf(&sb, "[1] Data Coverage\n")
	fmt.Fprintf(&sb, "----------------\n")
	if r.Coverage.TimeRangeStart != "" && r.Coverage.TimeRangeEnd != "" {
		fmt.Fprintf(&sb, "Time range: %s ~ %s\n", r.Coverage.TimeRangeStart, r.Coverage.TimeRangeEnd)
	} else {
		fmt.Fprintf(&sb, "Time range: %s\n", notAvailable)
	}
	fmt.Fprintf(&sb, "Feedback total: %d\n", r.Coverage.FeedbackTotal)
	fmt.Fprintf(&sb, "Feedback by event:\n")
	for _, k := range sortedKeys(r.Coverage.FeedbackByEvent) {
		fmt.Fprintf(&sb, "  %s: %d\n", k, r.Coverage.FeedbackByEvent[k])
	}
	fmt.Fprintf(&sb, "Decision total: %d\n", r.Coverage.DecisionTotal)
	fmt.Fprintf(&sb, "Decision by mode:\n")
	for _, k := range sortedKeys(r.Coverage.DecisionByMode) {
		fmt.Fprintf(&sb, "  %s: %d\n", k, r.Coverage.DecisionByMode[k])
	}
	fmt.Fprintf(&sb, "Suggestions: %d (items: %d)\n", r.Coverage.SuggestionTotal, r.Coverage.SuggestionItems)
	fmt.Fprintf(&sb, "Algorithm versions:\n")
	for _, k := range sortedKeys(r.Coverage.AlgorithmVersion) {
		fmt.Fprintf(&sb, "  %s: %d\n", k, r.Coverage.AlgorithmVersion[k])
	}
	fmt.Fprintf(&sb, "Skipped records: %d\n\n", r.Coverage.SkippedRecords)

	// 2. 人工反馈统计
	fmt.Fprintf(&sb, "[2] Human Feedback\n")
	fmt.Fprintf(&sb, "------------------\n")
	fmt.Fprintf(&sb, "By event:\n")
	for _, k := range sortedKeys(r.Feedback.ByEvent) {
		fmt.Fprintf(&sb, "  %s: %d\n", k, r.Feedback.ByEvent[k])
	}
	fmt.Fprintf(&sb, "By event & algorithm:\n")
	for _, k := range sortedNestedKeys(r.Feedback.ByEventAlgorithm) {
		inner := r.Feedback.ByEventAlgorithm[k]
		for _, a := range sortedKeys(inner) {
			fmt.Fprintf(&sb, "  %s / %s: %d\n", k, a, inner[a])
		}
	}
	fmt.Fprintf(&sb, "Manual total: %d\n", r.Feedback.ManualTotal)
	fmt.Fprintf(&sb, "Suggestion-driven total: %d\n", r.Feedback.SuggestionTotal)
	fmt.Fprintf(&sb, "Unknown algorithm version: %d\n\n", r.Feedback.UnknownVersion)

	// 3. 合并建议效果
	fmt.Fprintf(&sb, "[3] Suggestion Effect\n")
	fmt.Fprintf(&sb, "---------------------\n")
	fmt.Fprintf(&sb, "Suggestion-driven confirmed: %d\n", r.SuggestionEffect.SuggestionConfirmed)
	fmt.Fprintf(&sb, "Suggestion-driven rejected: %d\n", r.SuggestionEffect.SuggestionRejected)
	fmt.Fprintf(&sb, "Acceptance rate: %s (%d/%d)\n", r.SuggestionEffect.AcceptanceRate.Value,
		r.SuggestionEffect.AcceptanceRate.Numerator, r.SuggestionEffect.AcceptanceRate.Denominator)
	fmt.Fprintf(&sb, "Rejection rate: %s (%d/%d)\n", r.SuggestionEffect.RejectionRate.Value,
		r.SuggestionEffect.RejectionRate.Numerator, r.SuggestionEffect.RejectionRate.Denominator)
	fmt.Fprintf(&sb, "identity_profile hits: %d, legacy hits: %d\n",
		r.SuggestionEffect.IdentityProfileHits, r.SuggestionEffect.LegacyHits)
	fmt.Fprintf(&sb, "Recall@1: %s (hits=%d, evaluable=%d, total=%d, coverage=%s)\n",
		r.SuggestionEffect.RecallAt1.Value, r.SuggestionEffect.RecallAt1.Hits,
		r.SuggestionEffect.RecallAt1.Evaluable, r.SuggestionEffect.RecallAt1.Total,
		r.SuggestionEffect.RecallAt1.Coverage)
	fmt.Fprintf(&sb, "Recall@5: %s (hits=%d, evaluable=%d, total=%d, coverage=%s)\n",
		r.SuggestionEffect.RecallAt5.Value, r.SuggestionEffect.RecallAt5.Hits,
		r.SuggestionEffect.RecallAt5.Evaluable, r.SuggestionEffect.RecallAt5.Total,
		r.SuggestionEffect.RecallAt5.Coverage)
	fmt.Fprintf(&sb, "Recall@10: %s (hits=%d, evaluable=%d, total=%d, coverage=%s)\n",
		r.SuggestionEffect.RecallAt10.Value, r.SuggestionEffect.RecallAt10.Hits,
		r.SuggestionEffect.RecallAt10.Evaluable, r.SuggestionEffect.RecallAt10.Total,
		r.SuggestionEffect.RecallAt10.Coverage)
	fmt.Fprintf(&sb, "Recall@20: %s (hits=%d, evaluable=%d, total=%d, coverage=%s)\n",
		r.SuggestionEffect.RecallAt20.Value, r.SuggestionEffect.RecallAt20.Hits,
		r.SuggestionEffect.RecallAt20.Evaluable, r.SuggestionEffect.RecallAt20.Total,
		r.SuggestionEffect.RecallAt20.Coverage)
	fmt.Fprintf(&sb, "Unmatched feedback count: %d\n\n", r.SuggestionEffect.UnmatchedFeedbackCount)

	// 4. identity decision
	fmt.Fprintf(&sb, "[4] Identity Decision\n")
	fmt.Fprintf(&sb, "---------------------\n")
	fmt.Fprintf(&sb, "By decision:\n")
	for _, k := range sortedKeys(r.IdentityDecision.ByDecision) {
		fmt.Fprintf(&sb, "  %s: %d\n", k, r.IdentityDecision.ByDecision[k])
	}
	fmt.Fprintf(&sb, "By mode:\n")
	for _, k := range sortedKeys(r.IdentityDecision.ByMode) {
		fmt.Fprintf(&sb, "  %s: %d\n", k, r.IdentityDecision.ByMode[k])
	}
	fmt.Fprintf(&sb, "By reason:\n")
	for _, k := range sortedKeys(r.IdentityDecision.ByReason) {
		fmt.Fprintf(&sb, "  %s: %d\n", k, r.IdentityDecision.ByReason[k])
	}
	fmt.Fprintf(&sb, "Disagreement rate: %s (%d/%d)\n", r.IdentityDecision.DisagreementRate.Value,
		r.IdentityDecision.DisagreementRate.Numerator, r.IdentityDecision.DisagreementRate.Denominator)
	fmt.Fprintf(&sb, "Legacy miss / profile hit rate: %s (%d/%d)\n", r.IdentityDecision.LegacyMissProfileHit.Value,
		r.IdentityDecision.LegacyMissProfileHit.Numerator, r.IdentityDecision.LegacyMissProfileHit.Denominator)
	fmt.Fprintf(&sb, "Profile unavailable rate: %s (%d/%d)\n", r.IdentityDecision.ProfileUnavailableRate.Value,
		r.IdentityDecision.ProfileUnavailableRate.Numerator, r.IdentityDecision.ProfileUnavailableRate.Denominator)
	fmt.Fprintf(&sb, "Profile blocked rate: %s (%d/%d)\n", r.IdentityDecision.ProfileBlockedRate.Value,
		r.IdentityDecision.ProfileBlockedRate.Numerator, r.IdentityDecision.ProfileBlockedRate.Denominator)
	fmt.Fprintf(&sb, "Rescue applied count: %d\n\n", r.IdentityDecision.RescueAppliedCount)

	// 5. 分数分布
	fmt.Fprintf(&sb, "[5] Score & Margin Distribution\n")
	fmt.Fprintf(&sb, "-------------------------------\n")
	renderDistText(&sb, "Legacy score", r.ScoreDistribution.LegacyScore)
	renderDistText(&sb, "Profile best score", r.ScoreDistribution.ProfileBestScore)
	renderDistText(&sb, "Profile second score", r.ScoreDistribution.ProfileSecondScore)
	renderDistText(&sb, "Margin", r.ScoreDistribution.Margin)
	fmt.Fprintf(&sb, "Elapsed ms: samples=%d ignored=%d max=%.0f\n",
		r.ScoreDistribution.ElapsedMS.Samples, r.ScoreDistribution.ElapsedMS.Ignored, r.ScoreDistribution.ElapsedMS.Max)
	for _, k := range sortedKeysFloat(r.ScoreDistribution.ElapsedMS.Percentiles) {
		fmt.Fprintf(&sb, "  %s: %.2f\n", k, r.ScoreDistribution.ElapsedMS.Percentiles[k])
	}
	sb.WriteString("\n")

	// 6. 数据不足提示
	fmt.Fprintf(&sb, "[6] Warnings\n")
	fmt.Fprintf(&sb, "------------\n")
	if len(r.Warnings) == 0 {
		fmt.Fprintf(&sb, "(none)\n")
	} else {
		for _, w := range r.Warnings {
			fmt.Fprintf(&sb, "  - %s\n", w)
		}
	}
	sb.WriteString("\nNote: This report provides evidence only. It does not replace human gating review.\n")
	sb.WriteString("Task15 completion does NOT enable rescue. Rescue requires shadow data, calibration review, NAS performance confirmation and rollback rehearsal.\n")
	return sb.String()
}

// renderDistText 输出单个分布的 text 行。
func renderDistText(sb *strings.Builder, name string, d distributionSummary) {
	fmt.Fprintf(sb, "%s: samples=%d ignored=%d\n", name, d.Samples, d.Ignored)
	for _, k := range sortedKeysFloat(d.Percentiles) {
		fmt.Fprintf(sb, "  %s: %.6f\n", k, d.Percentiles[k])
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedNestedKeys 返回嵌套 map 的外层 key（已排序）。
func sortedNestedKeys(m map[string]map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedKeysFloat 按 P10 < P25 < P50 < P90 < P95 < P99 数值排序，避免字典序错乱。
func sortedKeysFloat(m map[string]float64) []string {
	type kv struct {
		k string
		v float64
	}
	pairs := make([]kv, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return percentileRank(pairs[i].k) < percentileRank(pairs[j].k)
	})
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.k
	}
	return out
}

func percentileRank(key string) int {
	var n int
	fmt.Sscanf(strings.TrimPrefix(key, "P"), "%d", &n)
	return n
}
