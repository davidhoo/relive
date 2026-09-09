// Package main 实现 relive-merge-suggestion-diagnose：有界只读合并推荐链路诊断。
//
// 在数据库副本上按目标 ID 复用生产 IdentityMatchingEngine（召回/精排/策略），
// 输出阶段统计与重点人物对 DropStage。不写推荐表、不触发生产任务、不输出 embedding。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("relive-merge-suggestion-diagnose", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "Path to a copied SQLite database (required, opened read-only)")
	targets := fs.String("targets", "", "Comma-separated target person IDs (required, keep small)")
	focus := fs.String("focus", "", "Focus pairs: target:cand1,cand2;target2:cand3")
	configPath := fs.String("config", "", "Optional production YAML (people.* thresholds); prefer over hardcoded defaults")
	threshold := fs.Float64("threshold", -1, "Suggest score threshold; default 0.55 or people.merge_suggestion_threshold from -config")
	rescue := fs.Float64("rescue-threshold", -1, "Engine rescue/auto score threshold; default 0.65 or from -config")
	margin := fs.Float64("margin", -1, "Engine/auto margin; default 0.05 or from -config")
	minCenterFaces := fs.Int("min-center-faces", -1, "Min center support faces; default 3 or from -config")
	embModel := fs.String("embedding-model", "", "Embedding model signature; empty = infer from ready profiles")
	maxCand := fs.Int("max-candidates", 200, "ANN person-candidate union cap (production default 200)")
	annK := fs.Int("ann-k", 50, "Per-center ANN Search K (production default 50)")
	exactK := fs.Int("exact-k", -2, "Per-center exact cosine boost K; -2=engine default(100), -1=off, >=0 explicit")
	format := fs.String("format", "json", "Output format: json or text")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *annK <= 0 {
		fmt.Fprintln(stderr, "error: -ann-k must be > 0")
		return 2
	}
	if strings.TrimSpace(*dbPath) == "" || strings.TrimSpace(*targets) == "" {
		fmt.Fprintln(stderr, "error: -db and -targets are required")
		printUsage(stderr)
		return 2
	}
	if *format != "json" && *format != "text" {
		fmt.Fprintf(stderr, "error: invalid -format %q\n", *format)
		return 2
	}

	peopleCfg := config.PeopleConfig{
		MergeSuggestionThreshold:       0.55,
		IdentityProfileRescueThreshold: 0.65,
		IdentityProfileMargin:          0.05,
		IdentityProfileMinCenterFaces:  3,
	}
	if path := strings.TrimSpace(*configPath); path != "" {
		loaded, err := loadPeopleConfigForDiagnose(path)
		if err != nil {
			fmt.Fprintf(stderr, "error: load -config: %v\n", err)
			return 1
		}
		peopleCfg = mergePeopleDiagnoseDefaults(loaded)
		fmt.Fprintf(stderr, "config: loaded people thresholds from %s (no full-server validate)\n", path)
	}
	if *threshold >= 0 {
		peopleCfg.MergeSuggestionThreshold = *threshold
	}
	if *rescue >= 0 {
		peopleCfg.IdentityProfileRescueThreshold = *rescue
	}
	if *margin >= 0 {
		peopleCfg.IdentityProfileMargin = *margin
	}
	if *minCenterFaces > 0 {
		peopleCfg.IdentityProfileMinCenterFaces = *minCenterFaces
	}

	targetIDs, err := parseUintList(*targets)
	if err != nil {
		fmt.Fprintf(stderr, "error: -targets: %v\n", err)
		return 2
	}
	focusMap, err := parseFocus(*focus)
	if err != nil {
		fmt.Fprintf(stderr, "error: -focus: %v\n", err)
		return 2
	}

	recallExactK := 0
	switch {
	case *exactK == -2:
		recallExactK = 0 // normalized() → default 100
	case *exactK == -1:
		recallExactK = -1 // disable
	default:
		recallExactK = *exactK
	}
	fmt.Fprintf(stderr, "scope: targets=%v focus_pairs≈%d ann_k=%d exact_k_flag=%d max_candidates=%d\n",
		targetIDs, countFocus(focusMap), *annK, *exactK, *maxCand)
	fmt.Fprintf(stderr, "strategy: suggest_threshold=%.4f rescue=%.4f margin=%.4f min_center_faces=%d\n",
		peopleCfg.MergeSuggestionThreshold, peopleCfg.IdentityProfileRescueThreshold, peopleCfg.IdentityProfileMargin, peopleCfg.IdentityProfileMinCenterFaces)
	fmt.Fprintf(stderr, "cost: rebuild ANN from all active centers for chosen model; memory scales with center count\n")
	fmt.Fprintf(stderr, "safety: read-only DB; does not write person_merge_suggestions\n")

	db, err := openReadOnlyGorm(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: open db: %v\n", err)
		return 1
	}
	sqlDB, err := db.DB()
	if err != nil {
		fmt.Fprintf(stderr, "error: sql db: %v\n", err)
		return 1
	}
	defer sqlDB.Close()

	modelSig := strings.TrimSpace(*embModel)
	if modelSig == "" {
		modelSig, err = inferEmbeddingModel(db)
		if err != nil {
			fmt.Fprintf(stderr, "error: infer embedding model: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(stderr, "ann: rebuilding model=%s at %s\n", modelSig, time.Now().UTC().Format(time.RFC3339))

	profileRepo := repository.NewPersonIdentityProfileRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	engine, centerCount, err := service.BuildMergeSuggestionDiagnoseEngine(
		profileRepo,
		faceRepo,
		repository.NewCannotLinkRepository(db),
		faceRepo,
		modelSig,
		service.IdentityProfileMatcherConfig{
			EmbeddingModel:  modelSig,
			RescueThreshold: peopleCfg.IdentityProfileRescueThreshold,
			Margin:          peopleCfg.IdentityProfileMargin,
			MinCenterFaces:  peopleCfg.IdentityProfileMinCenterFaces,
		},
	)
	if err != nil {
		fmt.Fprintf(stderr, "error: build engine: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "ann: ready centers=%d\n", centerCount)

	strategy := service.NewIdentitySuggestStrategy(peopleCfg)
	report, err := service.DiagnoseMergeSuggestionTargets(engine, repository.NewPersonRepository(db), service.MergeSuggestionDiagnoseRequest{
		TargetIDs:       targetIDs,
		FocusCandidates: focusMap,
		Strategy:        strategy,
		Recall: service.IdentityRecallOptions{
			ANNK:          *annK,
			ExactK:        recallExactK,
			MaxCandidates: *maxCand,
			TopK:          20,
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: diagnose: %v\n", err)
		return 1
	}

	var out string
	switch *format {
	case "json":
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: marshal: %v\n", err)
			return 1
		}
		out = string(b)
	default:
		out = renderText(report)
	}
	if _, err := fmt.Fprint(stdout, out); err != nil {
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
  relive-merge-suggestion-diagnose -db /path/to/copy.db -targets 265250,267444 \
    -focus '265250:265274,285426,382656;267444:272927' \
    [-config /path/to/config.prod.yaml] [-threshold 0.55] \
    [-rescue-threshold 0.65] [-margin 0.05] [-min-center-faces 3] \
    [-ann-k 50] [-exact-k -2] [-max-candidates 200] [-format json|text]

Opens the DB read-only, rebuilds ANN from active centers on the copy, runs the
production matching engine for the listed targets only, and prints stage stats.
Prefer -config so rescue/margin/min_center_faces match production.
Never writes suggestions. Do not point -db at the live production file.`)
}

func openReadOnlyGorm(path string) (*gorm.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil {
		return nil, err
	} else if st.IsDir() {
		return nil, errors.New("db path is a directory")
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000&_query_only=true", abs)
	return gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}

func parseUintList(raw string) ([]uint, error) {
	parts := strings.Split(raw, ",")
	out := make([]uint, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, uint(v))
	}
	if len(out) == 0 {
		return nil, errors.New("empty list")
	}
	return out, nil
}

func parseFocus(raw string) (map[uint][]uint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[uint][]uint{}, nil
	}
	out := make(map[uint][]uint)
	for _, group := range strings.Split(raw, ";") {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		kv := strings.SplitN(group, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("bad focus group %q", group)
		}
		tid, err := strconv.ParseUint(strings.TrimSpace(kv[0]), 10, 64)
		if err != nil {
			return nil, err
		}
		cands, err := parseUintList(kv[1])
		if err != nil {
			return nil, err
		}
		out[uint(tid)] = cands
	}
	return out, nil
}

func countFocus(m map[uint][]uint) int {
	n := 0
	for _, v := range m {
		n += len(v)
	}
	return n
}

// loadPeopleConfigForDiagnose 只解析 YAML 的 people 段，不做全量 Server Validate。
// 诊断工具常只挂载 config.prod.yaml，缺少 server.port 等字段。
func loadPeopleConfigForDiagnose(path string) (config.PeopleConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config.PeopleConfig{}, err
	}
	var wrap struct {
		People config.PeopleConfig `yaml:"people"`
	}
	if err := yaml.Unmarshal(data, &wrap); err != nil {
		return config.PeopleConfig{}, err
	}
	return wrap.People, nil
}

func mergePeopleDiagnoseDefaults(in config.PeopleConfig) config.PeopleConfig {
	out := config.PeopleConfig{
		MergeSuggestionThreshold:       0.55,
		IdentityProfileRescueThreshold: 0.65,
		IdentityProfileMargin:          0.05,
		IdentityProfileMinCenterFaces:  3,
	}
	if in.MergeSuggestionThreshold > 0 {
		out.MergeSuggestionThreshold = in.MergeSuggestionThreshold
	}
	if in.IdentityProfileRescueThreshold > 0 {
		out.IdentityProfileRescueThreshold = in.IdentityProfileRescueThreshold
	}
	if in.IdentityProfileMargin > 0 {
		out.IdentityProfileMargin = in.IdentityProfileMargin
	}
	if in.IdentityProfileMinCenterFaces > 0 {
		out.IdentityProfileMinCenterFaces = in.IdentityProfileMinCenterFaces
	}
	out.IdentityProfileMode = in.IdentityProfileMode
	return out
}

func inferEmbeddingModel(db *gorm.DB) (string, error) {
	var model string
	err := db.Raw(`SELECT embedding_model FROM person_identity_profiles WHERE embedding_model != '' AND status = 'ready' LIMIT 1`).Scan(&model).Error
	if err != nil {
		return "", err
	}
	if model == "" {
		return "", errors.New("no embedding_model found in ready profiles")
	}
	return model, nil
}

func renderText(r *service.MergeSuggestionDiagnoseReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "collected_at=%s engine=%s strategy=%s threshold=%.3f index_gen=%d ready=%v\n",
		r.CollectedAt.Format(time.RFC3339), r.EngineVersion, r.StrategyVersion, r.SuggestThreshold, r.IndexGeneration, r.IndexReady)
	s := r.StageStats
	fmt.Fprintf(&b, "stages(targets): eligible=%d ineligible=%d profile_ready=%d profile_unavail=%d index_unavail=%d incomplete=%d all_missing=%d\n",
		s.EligibleTargets, s.IneligibleTargets, s.ProfileReadyTargets, s.ProfileUnavailableTargets, s.IndexUnavailableTargets, s.IncompleteEvidenceTargets, s.AllEvidenceMissingTargets)
	fmt.Fprintf(&b, "stages(pairs): recalled=%d ranked=%d hard=%d below_thr=%d insufficient=%d tech=%d accepted=%d\n",
		s.RecalledCandidatePairs, s.RankedCandidatePairs, s.HardBlockedPairs, s.BelowSuggestThresholdPairs, s.InsufficientPairs, s.TechUnavailablePairs, s.StrategyAcceptedPairs)
	fmt.Fprintf(&b, "focus: recalled=%d not_recalled=%d exact_accepted=%d exact_rejected=%d\n",
		s.FocusRecalled, s.FocusNotRecalled, s.FocusExactAccepted, s.FocusExactRejected)
	for _, fp := range r.FocusPairs {
		fmt.Fprintf(&b, "pair %d→%d recalled=%v rank=%d trunc=%v score=%.4f fwd=%.4f rev=%.4f status=%s accept=%v drop=%s reason=%s\n",
			fp.TargetID, fp.CandidateID, fp.Recalled, fp.RecallRank, fp.TruncatedOut, fp.ExactScore, fp.ForwardScore, fp.ReverseScore, fp.EngineStatus, fp.StrategyAccepted, fp.DropStage, fp.StrategyReason)
	}
	return b.String()
}
