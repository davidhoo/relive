package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
)

// MergeSuggestionDiagnoseRequest 限定诊断范围与成本。仅按目标 ID 跑；不写推荐表。
type MergeSuggestionDiagnoseRequest struct {
	// TargetIDs 必填；建议小批量（诊断基线为 2 个目标）。
	TargetIDs []uint
	// FocusCandidates 目标 → 重点候选（如排障四对）；即使 ANN 未召回也会做精确 ComparePeople。
	FocusCandidates map[uint][]uint
	// Strategy 人工推荐策略；空则用默认 suggest 策略字段。
	Strategy IdentityStrategy
	// Recall 召回预算；零值走 DefaultIdentityRecallOptions。
	Recall IdentityRecallOptions
}

// MergeSuggestionStageStats 阶段计数。单位在字段注释中标明。
type MergeSuggestionStageStats struct {
	EligibleTargets            int `json:"eligible_targets"`              // 目标
	IneligibleTargets          int `json:"ineligible_targets"`            // 目标
	ProfileReadyTargets        int `json:"profile_ready_targets"`         // 目标
	ProfileUnavailableTargets  int `json:"profile_unavailable_targets"`   // 目标
	IndexUnavailableTargets    int `json:"index_unavailable_targets"`     // 目标
	IncompleteEvidenceTargets  int `json:"incomplete_evidence_targets"`   // 目标（部分缺失）
	AllEvidenceMissingTargets  int `json:"all_evidence_missing_targets"`  // 目标（召回后全空→可触发重建）
	RecalledCandidatePairs     int `json:"recalled_candidate_pairs"`      // 人物对
	RankedCandidatePairs       int `json:"ranked_candidate_pairs"`        // 人物对
	HardBlockedPairs           int `json:"hard_blocked_pairs"`            // 人物对
	BelowSuggestThresholdPairs int `json:"below_suggest_threshold_pairs"` // 人物对
	InsufficientPairs          int `json:"insufficient_pairs"`            // 人物对
	TechUnavailablePairs       int `json:"tech_unavailable_pairs"`        // 人物对
	StrategyAcceptedPairs      int `json:"strategy_accepted_pairs"`       // 人物对
	FocusRecalled              int `json:"focus_recalled"`                // 人物对
	FocusNotRecalled           int `json:"focus_not_recalled"`            // 人物对
	FocusExactAccepted         int `json:"focus_exact_accepted"`          // 人物对
	FocusExactRejected         int `json:"focus_exact_rejected"`          // 人物对
}

// MergeSuggestionFocusPairDiag 单个重点人物对的逐阶段结果（不含 embedding）。
type MergeSuggestionFocusPairDiag struct {
	TargetID            uint    `json:"target_id"`
	CandidateID         uint    `json:"candidate_id"`
	TargetEligible      bool    `json:"target_eligible"`
	TargetIneligible    string  `json:"target_ineligible_reason,omitempty"`
	TargetProfileReady  bool    `json:"target_profile_ready"`
	TargetProfileGen    int     `json:"target_profile_generation,omitempty"`
	CandidateProfileGen int     `json:"candidate_profile_generation,omitempty"`
	IndexReady          bool    `json:"index_ready"`
	IndexGeneration     int     `json:"index_generation,omitempty"`
	Recalled            bool    `json:"recalled"`
	RecallRank          int     `json:"recall_rank,omitempty"` // 1-based；未召回为 0
	TruncatedOut        bool    `json:"truncated_out,omitempty"`
	ExactScore          float64 `json:"exact_score,omitempty"`
	ForwardScore        float64 `json:"forward_score,omitempty"`
	ReverseScore        float64 `json:"reverse_score,omitempty"`
	EngineStatus        string  `json:"engine_status,omitempty"`
	BlockReason         string  `json:"block_reason,omitempty"`
	MinSupportCount     int     `json:"min_support_count,omitempty"`
	StrategyAccepted    bool    `json:"strategy_accepted"`
	StrategyReason      string  `json:"strategy_reason,omitempty"`
	// DropStage 解释该对在哪一步丢失：eligible/profile/index/recall/hard_block/threshold/insufficient/strategy/accepted。
	DropStage string `json:"drop_stage"`
}

// MergeSuggestionTargetDiag 单个目标摘要。
type MergeSuggestionTargetDiag struct {
	TargetID              uint   `json:"target_id"`
	Eligible              bool   `json:"eligible"`
	IneligibleReason      string `json:"ineligible_reason,omitempty"`
	Category              string `json:"category,omitempty"`
	Hidden                bool   `json:"hidden,omitempty"`
	FaceCount             int    `json:"face_count,omitempty"`
	ProfileReady          bool   `json:"profile_ready"`
	ProfileGeneration     int    `json:"profile_generation,omitempty"`
	EvidenceUnitCount     int    `json:"evidence_unit_count,omitempty"`
	IndexReady            bool   `json:"index_ready"`
	RecalledCount         int    `json:"recalled_count"`
	RankedCount           int    `json:"ranked_count"`
	IncompleteEvidence    bool   `json:"incomplete_evidence,omitempty"`
	EngineStatus          string `json:"engine_status,omitempty"`
	BlockReason           string `json:"block_reason,omitempty"`
	StrategyAcceptedCount int    `json:"strategy_accepted_count"`
}

// MergeSuggestionDiagnoseReport 只读诊断报告。
type MergeSuggestionDiagnoseReport struct {
	CollectedAt      time.Time                      `json:"collected_at"`
	EngineVersion    string                         `json:"engine_version"`
	StrategyVersion  string                         `json:"strategy_version"`
	SuggestThreshold float64                        `json:"suggest_threshold"`
	IndexGeneration  int                            `json:"index_generation"`
	IndexReady       bool                           `json:"index_ready"`
	StageStats       MergeSuggestionStageStats      `json:"stage_stats"`
	Targets          []MergeSuggestionTargetDiag    `json:"targets"`
	FocusPairs       []MergeSuggestionFocusPairDiag `json:"focus_pairs"`
	Notes            []string                       `json:"notes,omitempty"`
}

// personLookup 诊断用人物读取；与生产 PersonRepository 对齐最小接口。
type personLookup interface {
	ListByIDs(ids []uint) ([]*model.Person, error)
}

// DiagnoseMergeSuggestionTargets 复用生产引擎召回/精排/策略，输出有界阶段诊断。
// 只读：不写 person_merge_suggestions，不触发生产任务。ANN 重建请求不会从本函数发起
// （诊断走 ComparePeople/SimilarPeople 只读路径；全空证据时 SimilarPeople 内部仍可能 RequestRebuild——
// 若调用方需要绝对只读索引，应传入不可变 ANN 或在副本进程使用）。
func DiagnoseMergeSuggestionTargets(
	engine *IdentityMatchingEngine,
	people personLookup,
	req MergeSuggestionDiagnoseRequest,
) (*MergeSuggestionDiagnoseReport, error) {
	if engine == nil {
		return nil, fmt.Errorf("identity matching engine is nil")
	}
	if people == nil {
		return nil, fmt.Errorf("person lookup is nil")
	}
	targetIDs := dedupSortUint(req.TargetIDs)
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("target_ids required")
	}

	strategy := req.Strategy
	if strategy.Name == "" {
		strategy = NewIdentitySuggestStrategy(config.PeopleConfig{
			MergeSuggestionThreshold:      0.55,
			IdentityProfileMinCenterFaces: 3,
		})
	}
	opts := req.Recall.normalized()
	if opts.TopK <= 0 {
		opts.TopK = mergeSuggestionProfileK
	}

	report := &MergeSuggestionDiagnoseReport{
		CollectedAt:      time.Now().UTC(),
		EngineVersion:    engine.EngineVersion(),
		StrategyVersion:  strategy.Version,
		SuggestThreshold: strategy.ScoreThreshold,
		IndexGeneration:  engine.IndexGeneration(),
		IndexReady:       engine.ann != nil && engine.ann.Ready(engine.cfg.EmbeddingModel),
		Notes: []string{
			"只读诊断：不写推荐表；单位见 stage_stats 字段注释。",
			"部分证据缺失与索引脱节（全空→RequestRebuild）分开计数。",
			"Focus 对即使未召回也会做精确 ComparePeople，用于对照 ANN 漏召回。",
		},
	}

	peopleRows, err := people.ListByIDs(targetIDs)
	if err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}
	byID := make(map[uint]*model.Person, len(peopleRows))
	for _, p := range peopleRows {
		if p != nil {
			byID[p.ID] = p
		}
	}

	// 召回排序位置：在 SimilarPeople 之前对每个合格目标跑一遍 recall，记录 rank。
	recallRankByTarget := make(map[uint]map[uint]int, len(targetIDs))
	preTruncationHit := make(map[uint]map[uint]bool, len(targetIDs))

	for _, tid := range targetIDs {
		td := MergeSuggestionTargetDiag{TargetID: tid, IndexReady: report.IndexReady}
		p := byID[tid]
		ok, reason := mergeSuggestionTargetEligible(p)
		td.Eligible = ok
		td.IneligibleReason = reason
		if p != nil {
			td.Category = p.Category
			td.Hidden = p.Hidden
			td.FaceCount = p.FaceCount
		}
		if !ok {
			report.StageStats.IneligibleTargets++
			report.Targets = append(report.Targets, td)
			continue
		}
		report.StageStats.EligibleTargets++

		evs, st, rs := engine.loadPersonEvidences([]uint{tid})
		ev, has := evs[tid]
		if st != IdentityMatchStatusMatch || !has || len(ev.Units) == 0 {
			td.ProfileReady = false
			td.EngineStatus = string(IdentityMatchStatusUnavailable)
			td.BlockReason = rs
			if td.BlockReason == "" {
				td.BlockReason = blockProfileUnavailable
			}
			report.StageStats.ProfileUnavailableTargets++
			report.Targets = append(report.Targets, td)
			continue
		}
		td.ProfileReady = true
		td.ProfileGeneration = ev.Generation
		td.EvidenceUnitCount = len(ev.Units)
		report.StageStats.ProfileReadyTargets++

		if !report.IndexReady {
			td.EngineStatus = string(IdentityMatchStatusUnavailable)
			td.BlockReason = blockIndexUnavailable
			report.StageStats.IndexUnavailableTargets++
			report.Targets = append(report.Targets, td)
			continue
		}

		// 截断前全量 hits（仅诊断；生产截断用 MaxCandidates）。
		rawHits, recallReady := engine.recallPersonHits(ev, tid, opts)
		if !recallReady {
			td.EngineStatus = string(IdentityMatchStatusUnavailable)
			td.BlockReason = blockIndexUnavailable
			report.StageStats.IndexUnavailableTargets++
			report.Targets = append(report.Targets, td)
			continue
		}
		rankMap := make(map[uint]int, len(rawHits))
		for i, h := range rawHits {
			rankMap[h.personID] = i + 1
		}
		preTruncationHit[tid] = make(map[uint]bool, len(rawHits))
		for pid := range rankMap {
			preTruncationHit[tid][pid] = true
		}
		recalled := selectPersonIDsByRecallRank(rawHits, opts.MaxCandidates, opts.ANNK, opts.ExactK)
		recallRankByTarget[tid] = make(map[uint]int, len(recalled))
		for i, cid := range recalled {
			recallRankByTarget[tid][cid] = i + 1
		}
		td.RecalledCount = len(recalled)
		report.StageStats.RecalledCandidatePairs += len(recalled)

		sim := engine.SimilarPeople([]uint{tid}, opts)[tid]
		td.EngineStatus = string(sim.Status)
		td.BlockReason = sim.BlockReason
		td.IncompleteEvidence = sim.IncompleteEvidence
		td.RankedCount = len(sim.Candidates)
		report.StageStats.RankedCandidatePairs += len(sim.Candidates)
		if sim.IncompleteEvidence {
			report.StageStats.IncompleteEvidenceTargets++
		}
		if sim.Status == IdentityMatchStatusUnavailable && sim.BlockReason == blockProfileUnavailable && td.RecalledCount > 0 && len(sim.Candidates) == 0 {
			report.StageStats.AllEvidenceMissingTargets++
		}

		accepted := 0
		for _, c := range sim.Candidates {
			cmp := engine.ComparePeople([]PersonPair{{TargetID: tid, CandidateID: c.PersonID}})[PersonPair{TargetID: tid, CandidateID: c.PersonID}]
			switch cmp.Status {
			case IdentityMatchStatusHardConflict:
				report.StageStats.HardBlockedPairs++
			case IdentityMatchStatusUnavailable, IdentityMatchStatusInvalid:
				report.StageStats.TechUnavailablePairs++
			default:
				// match / insufficient / no_candidate：人工推荐策略可在 insufficient 上接受（RequireEngineMatch=false）。
				if cmp.Status == IdentityMatchStatusInsufficient {
					report.StageStats.InsufficientPairs++
				}
				dec := ApplyIdentityStrategy(cmp, strategy)
				if dec.Accepted {
					accepted++
					report.StageStats.StrategyAcceptedPairs++
				} else if cmp.Best != nil && cmp.Best.Score < strategy.ScoreThreshold {
					report.StageStats.BelowSuggestThresholdPairs++
				}
			}
		}
		td.StrategyAcceptedCount = accepted
		report.Targets = append(report.Targets, td)
	}

	// 重点人物对：精确路径 + 召回对照。
	for _, tid := range targetIDs {
		cands := dedupSortUint(req.FocusCandidates[tid])
		for _, cid := range cands {
			pair := MergeSuggestionFocusPairDiag{
				TargetID:        tid,
				CandidateID:     cid,
				IndexReady:      report.IndexReady,
				IndexGeneration: report.IndexGeneration,
			}
			p := byID[tid]
			ok, reason := mergeSuggestionTargetEligible(p)
			pair.TargetEligible = ok
			pair.TargetIneligible = reason
			if !ok {
				pair.DropStage = "eligible"
				report.FocusPairs = append(report.FocusPairs, pair)
				continue
			}

			evs, _, _ := engine.loadPersonEvidences([]uint{tid, cid})
			tev, tok := evs[tid]
			cev, cok := evs[cid]
			pair.TargetProfileReady = tok && len(tev.Units) > 0
			if pair.TargetProfileReady {
				pair.TargetProfileGen = tev.Generation
			}
			if cok && len(cev.Units) > 0 {
				pair.CandidateProfileGen = cev.Generation
			}
			if !pair.TargetProfileReady || !cok || len(cev.Units) == 0 {
				pair.DropStage = "profile"
				pair.EngineStatus = string(IdentityMatchStatusUnavailable)
				pair.BlockReason = blockProfileUnavailable
				report.FocusPairs = append(report.FocusPairs, pair)
				continue
			}
			if !report.IndexReady {
				pair.DropStage = "index"
				pair.EngineStatus = string(IdentityMatchStatusUnavailable)
				pair.BlockReason = blockIndexUnavailable
				report.FocusPairs = append(report.FocusPairs, pair)
				continue
			}

			if ranks := recallRankByTarget[tid]; ranks != nil {
				if r, ok := ranks[cid]; ok {
					pair.Recalled = true
					pair.RecallRank = r
					report.StageStats.FocusRecalled++
				} else {
					report.StageStats.FocusNotRecalled++
					if preTruncationHit[tid] != nil && preTruncationHit[tid][cid] {
						pair.TruncatedOut = true
					}
				}
			} else {
				report.StageStats.FocusNotRecalled++
			}

			cmp := engine.ComparePeople([]PersonPair{{TargetID: tid, CandidateID: cid}})[PersonPair{TargetID: tid, CandidateID: cid}]
			pair.EngineStatus = string(cmp.Status)
			pair.BlockReason = cmp.BlockReason
			sc := ScoreIdentityEvidence(tev, cev)
			pair.ForwardScore = sc.ForwardScore
			pair.ReverseScore = sc.ReverseScore
			pair.ExactScore = sc.Score
			if cmp.Best != nil {
				pair.MinSupportCount = cmp.Best.MinSupportCount
			}

			dec := ApplyIdentityStrategy(cmp, strategy)
			pair.StrategyAccepted = dec.Accepted
			pair.StrategyReason = dec.Reason

			switch {
			case !pair.Recalled && pair.TruncatedOut:
				pair.DropStage = "recall_truncated"
			case !pair.Recalled:
				pair.DropStage = "recall"
			case cmp.Status == IdentityMatchStatusHardConflict:
				pair.DropStage = "hard_block"
			case cmp.Status == IdentityMatchStatusUnavailable || cmp.Status == IdentityMatchStatusInvalid:
				pair.DropStage = "tech_unavailable"
			case dec.Accepted:
				pair.DropStage = "accepted"
			case cmp.Best != nil && cmp.Best.Score < strategy.ScoreThreshold:
				pair.DropStage = "threshold"
			default:
				pair.DropStage = "strategy"
			}

			if dec.Accepted {
				report.StageStats.FocusExactAccepted++
			} else if pair.DropStage != "hard_block" && pair.DropStage != "tech_unavailable" {
				report.StageStats.FocusExactRejected++
			}
			report.FocusPairs = append(report.FocusPairs, pair)
		}
	}

	sort.SliceStable(report.FocusPairs, func(i, j int) bool {
		if report.FocusPairs[i].TargetID != report.FocusPairs[j].TargetID {
			return report.FocusPairs[i].TargetID < report.FocusPairs[j].TargetID
		}
		return report.FocusPairs[i].CandidateID < report.FocusPairs[j].CandidateID
	})
	return report, nil
}

func mergeSuggestionTargetEligible(p *model.Person) (bool, string) {
	if p == nil {
		return false, "not_found"
	}
	if p.Hidden {
		return false, "hidden"
	}
	if p.FaceCount <= 0 {
		return false, "no_faces"
	}
	switch p.Category {
	case model.PersonCategoryFamily, model.PersonCategoryFriend, model.PersonCategoryAcquaintance:
		return true, ""
	default:
		return false, "category"
	}
}

// recallPersonHits 返回截断前的召回命中（含 rank 信息）。
// ready=false 表示索引/Search 不可用；ready=true 且 hits 为空表示真的没有候选（禁止与不可用混淆）。
// ExactK>0 时并入有界精确 cosine 补召（名次接在 ANNK 之后），不依赖 HNSW 图稳定性。
func (e *IdentityMatchingEngine) recallPersonHits(ev IdentityEvidence, targetID uint, opts IdentityRecallOptions) (hits []personRecallHit, ready bool) {
	opts = opts.normalized()
	if e == nil || e.ann == nil {
		return nil, false
	}
	minRank := make(map[uint]int)
	hitCount := make(map[uint]int)
	noteHit := func(pid uint, rank int) {
		if pid == 0 || pid == targetID {
			return
		}
		if r, exists := minRank[pid]; !exists || rank < r {
			minRank[pid] = rank
		}
		hitCount[pid]++
	}
	for _, u := range ev.Units {
		ids, ok := e.ann.Search(u.Vector, opts.ANNK, e.cfg.EmbeddingModel)
		if !ok {
			return nil, false
		}
		for rank, pid := range ids {
			noteHit(pid, rank)
		}
		if opts.ExactK > 0 {
			exactIDs, ok := e.ann.ExactTopPeople(u.Vector, opts.ExactK, e.cfg.EmbeddingModel)
			if !ok {
				return nil, false
			}
			for rank, pid := range exactIDs {
				noteHit(pid, opts.ANNK+rank)
			}
		}
	}
	// 显式分配空 slice，避免 append(nil, ...) 在零命中时返回 nil 被误判为未就绪。
	out := make([]personRecallHit, 0, len(minRank))
	for pid, rank := range minRank {
		out = append(out, personRecallHit{
			personID: pid,
			minRank:  rank,
			hitCount: hitCount[pid],
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].minRank != out[j].minRank {
			return out[i].minRank < out[j].minRank
		}
		if out[i].hitCount != out[j].hitCount {
			return out[i].hitCount > out[j].hitCount
		}
		return out[i].personID < out[j].personID
	})
	return out, true
}

// diagnoseActiveCenterLister 诊断建索引所需的最小仓库能力。
type diagnoseActiveCenterLister interface {
	ListAllActiveCenters(embeddingModel string) ([]*model.PersonIdentityCenter, error)
	ListActiveCentersByPersonIDs(personIDs []uint, embeddingModel string) (map[uint][]*model.PersonIdentityCenter, error)
}

// BuildMergeSuggestionDiagnoseEngine 在副本库上构建与生产一致的匹配引擎（含 ANN 全量重建）。
// 仅供只读诊断 CLI/测试；内存与中心数量成正比。返回 centerCount 便于成本说明。
func BuildMergeSuggestionDiagnoseEngine(
	profileRepo diagnoseActiveCenterLister,
	faceRepo matcherFaceRepo,
	cannotLinkRepo matcherCannotLinkRepo,
	sharingRepo identitySharingRepo,
	embeddingModel string,
	cfg IdentityProfileMatcherConfig,
) (*IdentityMatchingEngine, int, error) {
	if profileRepo == nil {
		return nil, 0, fmt.Errorf("profile repo is nil")
	}
	modelSig := strings.TrimSpace(embeddingModel)
	if modelSig == "" {
		return nil, 0, fmt.Errorf("embedding model required")
	}
	centers, err := profileRepo.ListAllActiveCenters(modelSig)
	if err != nil {
		return nil, 0, fmt.Errorf("list active centers: %w", err)
	}
	cfg.EmbeddingModel = modelSig
	ann := newIdentityProfileANN(modelSig)
	if err := ann.Rebuild(centers, modelSig); err != nil {
		return nil, 0, fmt.Errorf("ann rebuild: %w", err)
	}
	return NewIdentityMatchingEngine(ann, profileRepo, faceRepo, cannotLinkRepo, sharingRepo, cfg), len(centers), nil
}
