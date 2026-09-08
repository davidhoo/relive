package service

import (
	"sort"

	"github.com/davidhoo/relive/internal/model"
)

// IdentityMatchingEngine 是统一身份匹配核心：候选召回、证据聚合、打分、约束与状态分类。
// 自动归属与合并推荐共用；策略阈值由 ApplyIdentityStrategy 应用。
type IdentityMatchingEngine struct {
	ann            *identityProfileANN
	profileRepo    matcherProfileRepo
	faceRepo       matcherFaceRepo
	cannotLinkRepo matcherCannotLinkRepo
	cfg            IdentityProfileMatcherConfig
	sharingRepo    identitySharingRepo
}

// identitySharingRepo 人物对同照片共现查询（合并推荐硬阻断）。
type identitySharingRepo interface {
	ListPersonIDsSharingPhotos(targetPersonID uint, candidatePersonIDs []uint) ([]uint, error)
}

// NewIdentityMatchingEngine 构造统一引擎。sharingRepo 可为与 faceRepo 同一实现。
func NewIdentityMatchingEngine(
	ann *identityProfileANN,
	profileRepo matcherProfileRepo,
	faceRepo matcherFaceRepo,
	cannotLinkRepo matcherCannotLinkRepo,
	sharingRepo identitySharingRepo,
	cfg IdentityProfileMatcherConfig,
) *IdentityMatchingEngine {
	return &IdentityMatchingEngine{
		ann:            ann,
		profileRepo:    profileRepo,
		faceRepo:       faceRepo,
		cannotLinkRepo: cannotLinkRepo,
		sharingRepo:    sharingRepo,
		cfg:            cfg,
	}
}

// EngineVersion 返回引擎版本常量。
func (e *IdentityMatchingEngine) EngineVersion() string {
	return identityEngineVersion
}

// IndexGeneration 返回当前 ANN 代次；不可用时返回 0。
func (e *IdentityMatchingEngine) IndexGeneration() int {
	if e == nil || e.ann == nil {
		return 0
	}
	return int(e.ann.Stats(e.cfg.EmbeddingModel).Generation)
}

// MatchComponent 对一个待聚类组件执行身份匹配（每个组件调用一次）。
// 复用 IdentityProfileMatcher.Match 管道以保持 rescue/shadow 分数契约，再映射为显式 Status。
func (e *IdentityMatchingEngine) MatchComponent(component []*model.Face, opts IdentityRecallOptions) IdentityMatchResult {
	_ = opts.normalized()
	if e == nil {
		return newIdentityMatchResult(IdentityMatchStatusUnavailable, blockIndexUnavailable)
	}
	matcher := &IdentityProfileMatcher{
		ann:            e.ann,
		profileRepo:    e.profileRepo,
		faceRepo:       e.faceRepo,
		cannotLinkRepo: e.cannotLinkRepo,
		cfg:            e.cfg,
	}
	return mapMatcherToEngineResult(matcher.Match(component))
}

// SimilarPeople 为每个目标返回候选匹配结果；每个规范化目标必有 entry。
func (e *IdentityMatchingEngine) SimilarPeople(personIDs []uint, opts IdentityRecallOptions) map[uint]IdentityMatchResult {
	opts = opts.normalized()
	if opts.TopK <= 0 {
		opts.TopK = mergeSuggestionProfileK
	}
	out := make(map[uint]IdentityMatchResult)
	targets := dedupSortUint(personIDs)
	for _, id := range targets {
		out[id] = newIdentityMatchResult(IdentityMatchStatusUnavailable, blockIndexUnavailable)
	}
	if e == nil || len(targets) == 0 {
		return out
	}

	evidences, status, reason := e.loadPersonEvidences(targets)
	if status != IdentityMatchStatusMatch {
		for _, id := range targets {
			out[id] = newIdentityMatchResult(status, reason)
		}
		return out
	}

	for _, targetID := range targets {
		ev, ok := evidences[targetID]
		if !ok || len(ev.Units) == 0 {
			out[targetID] = newIdentityMatchResult(IdentityMatchStatusNoCandidate, "")
			continue
		}
		cands, ok := e.recallPersonCandidates(ev, targetID, opts)
		if !ok {
			out[targetID] = newIdentityMatchResult(IdentityMatchStatusUnavailable, blockIndexUnavailable)
			continue
		}
		if len(cands) == 0 {
			out[targetID] = newIdentityMatchResult(IdentityMatchStatusNoCandidate, "")
			continue
		}
		candEvidences, st, rs := e.loadPersonEvidences(cands)
		if st != IdentityMatchStatusMatch {
			out[targetID] = newIdentityMatchResult(st, rs)
			continue
		}
		out[targetID] = e.rankPersonCandidates(targetID, ev, cands, candEvidences, opts.TopK)
	}
	return out
}

// ComparePeople 对人物对做精确比较；每对必有 entry。
func (e *IdentityMatchingEngine) ComparePeople(pairs []PersonPair) map[PersonPair]IdentityMatchResult {
	out := make(map[PersonPair]IdentityMatchResult)
	deduped := dedupPairs(pairs)
	for _, pr := range deduped {
		out[pr] = newIdentityMatchResult(IdentityMatchStatusUnavailable, blockIndexUnavailable)
	}
	if e == nil || len(deduped) == 0 {
		return out
	}

	personIDs := make([]uint, 0, len(deduped)*2)
	for _, pr := range deduped {
		personIDs = append(personIDs, pr.TargetID, pr.CandidateID)
	}
	evidences, status, reason := e.loadPersonEvidences(dedupSortUint(personIDs))
	if status != IdentityMatchStatusMatch {
		for _, pr := range deduped {
			out[pr] = newIdentityMatchResult(status, reason)
		}
		return out
	}

	for _, pr := range deduped {
		left, lok := evidences[pr.TargetID]
		right, rok := evidences[pr.CandidateID]
		if !lok || !rok || len(left.Units) == 0 || len(right.Units) == 0 {
			out[pr] = newIdentityMatchResult(IdentityMatchStatusNoCandidate, "")
			continue
		}
		out[pr] = e.comparePersonPair(pr.TargetID, pr.CandidateID, left, right)
	}
	return out
}

func mapMatcherToEngineResult(raw IdentityProfileMatch) IdentityMatchResult {
	if !raw.Available {
		status := IdentityMatchStatusUnavailable
		if raw.BlockReason == blockInvalidQuery {
			status = IdentityMatchStatusInvalid
		}
		return newIdentityMatchResult(status, raw.BlockReason)
	}
	if raw.PersonID == 0 {
		return newIdentityMatchResult(IdentityMatchStatusNoCandidate, "")
	}

	best := &IdentityCandidateResult{
		PersonID:  raw.PersonID,
		Score:     raw.Score,
		CenterIDs: append([]uint{}, raw.CenterIDs...),
		BlockReason: raw.BlockReason,
	}
	result := IdentityMatchResult{
		EngineVersion:    identityEngineVersion,
		Best:             best,
		Margin:           raw.Margin,
		MarginApplicable: true,
		BlockReason:      raw.BlockReason,
	}
	if raw.SecondPersonID != 0 {
		result.Second = &IdentityCandidateResult{
			PersonID: raw.SecondPersonID,
			Score:    raw.SecondScore,
		}
	}

	switch {
	case raw.BlockReason == blockCannotLink || raw.BlockReason == blockSamePhotoCooccurrence:
		result.Status = IdentityMatchStatusHardConflict
		best.Status = IdentityMatchStatusHardConflict
	case raw.BlockReason == blockNegativeEvidenceUnavail:
		result.Status = IdentityMatchStatusUnavailable
		best.Status = IdentityMatchStatusUnavailable
	case raw.AutoEligible:
		result.Status = IdentityMatchStatusMatch
		best.Status = IdentityMatchStatusMatch
		best.StableCenters = true
		best.SupportingUnits = 1
		best.MinSupportCount = 1
		best.CenterFitOK = true
		result.BlockReason = ""
		best.BlockReason = ""
	default:
		result.Status = IdentityMatchStatusInsufficient
		best.Status = IdentityMatchStatusInsufficient
		best.StableCenters = raw.BlockReason != blockUnstableCenter
		best.SupportingUnits = 1
		if raw.BlockReason == blockUnstableCenter {
			best.MinSupportCount = 0
		} else {
			best.MinSupportCount = 1
		}
		best.CenterFitOK = raw.BlockReason != blockBelowCenterBoundary
	}
	result.Candidates = []IdentityCandidateResult{*best}
	return result
}

func (e *IdentityMatchingEngine) loadPersonEvidences(personIDs []uint) (map[uint]IdentityEvidence, IdentityMatchStatus, string) {
	if e.profileRepo == nil {
		return nil, IdentityMatchStatusUnavailable, blockProfileUnavailable
	}
	centersByPerson, err := e.profileRepo.ListActiveCentersByPersonIDs(personIDs, e.cfg.EmbeddingModel)
	if err != nil {
		return nil, IdentityMatchStatusUnavailable, blockProfileUnavailable
	}
	out := make(map[uint]IdentityEvidence, len(personIDs))
	for _, pid := range personIDs {
		centers := centersByPerson[pid]
		units := make([]IdentityEvidenceUnit, 0, len(centers))
		for _, c := range centers {
			if c == nil {
				continue
			}
			emb := model.DecodeEmbedding(c.CentroidEmbedding)
			if !validVector(emb) {
				return nil, IdentityMatchStatusUnavailable, blockProfileUnavailable
			}
			medoid := uint(0)
			if c.MedoidFaceID != nil {
				medoid = *c.MedoidFaceID
			}
			units = append(units, IdentityEvidenceUnit{
				CenterID:      c.ID,
				FaceID:        medoid,
				PersonID:      pid,
				Vector:        emb,
				Weight:        1.0,
				SupportCount:  c.SupportCount,
				SimilarityP10: c.SimilarityP10,
			})
		}
		sort.SliceStable(units, func(i, j int) bool { return units[i].CenterID < units[j].CenterID })
		out[pid] = IdentityEvidence{
			Kind:     identityEvidenceKindPerson,
			PersonID: pid,
			Units:    units,
		}
	}
	return out, IdentityMatchStatusMatch, ""
}

func (e *IdentityMatchingEngine) recallPersonCandidates(ev IdentityEvidence, targetID uint, opts IdentityRecallOptions) ([]uint, bool) {
	if e.ann == nil {
		return nil, false
	}
	seen := make(map[uint]struct{})
	for _, u := range ev.Units {
		ids, ready := e.ann.Search(u.Vector, opts.ANNK, e.cfg.EmbeddingModel)
		if !ready {
			return nil, false
		}
		for _, pid := range ids {
			if pid == 0 || pid == targetID {
				continue
			}
			seen[pid] = struct{}{}
		}
	}
	cands := make([]uint, 0, len(seen))
	for pid := range seen {
		cands = append(cands, pid)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i] < cands[j] })
	if len(cands) > opts.MaxCandidates {
		cands = cands[:opts.MaxCandidates]
	}
	return cands, true
}

func (e *IdentityMatchingEngine) rankPersonCandidates(
	targetID uint,
	targetEv IdentityEvidence,
	candIDs []uint,
	candEvs map[uint]IdentityEvidence,
	topK int,
) IdentityMatchResult {
	var scored []IdentityCandidateResult
	for _, cid := range candIDs {
		cev, ok := candEvs[cid]
		if !ok || len(cev.Units) == 0 {
			continue
		}
		pair := e.comparePersonPair(targetID, cid, targetEv, cev)
		if pair.Best == nil {
			continue
		}
		scored = append(scored, *pair.Best)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].PersonID < scored[j].PersonID
	})
	if topK > 0 && len(scored) > topK {
		scored = scored[:topK]
	}

	result := newIdentityMatchResult(IdentityMatchStatusNoCandidate, "")
	result.MarginApplicable = true
	if len(scored) == 0 {
		return result
	}
	best := scored[0]
	result.Best = &best
	result.Candidates = scored
	result.Status = best.Status
	result.BlockReason = best.BlockReason
	if len(scored) >= 2 {
		second := scored[1]
		result.Second = &second
		result.Margin = best.Score - second.Score
	} else {
		result.Margin = best.Score - (-1)
	}
	return result
}

func (e *IdentityMatchingEngine) comparePersonPair(leftID, rightID uint, left, right IdentityEvidence) IdentityMatchResult {
	result := newIdentityMatchResult(IdentityMatchStatusUnavailable, "")
	result.MarginApplicable = false // 显式人物对比较无候选集合 margin

	if e.cannotLinkRepo != nil {
		blocked, err := e.cannotLinkRepo.ListByPersonID(leftID)
		if err != nil {
			result.Status = IdentityMatchStatusUnavailable
			result.BlockReason = blockNegativeEvidenceUnavail
			return result
		}
		for _, id := range blocked {
			if id == rightID {
				result.Status = IdentityMatchStatusHardConflict
				result.BlockReason = blockCannotLink
				result.Best = &IdentityCandidateResult{
					PersonID:    rightID,
					Status:      IdentityMatchStatusHardConflict,
					BlockReason: blockCannotLink,
				}
				return result
			}
		}
	}

	if e.sharingRepo != nil {
		sharing, err := e.sharingRepo.ListPersonIDsSharingPhotos(leftID, []uint{rightID})
		if err != nil {
			result.Status = IdentityMatchStatusUnavailable
			result.BlockReason = blockNegativeEvidenceUnavail
			return result
		}
		for _, id := range sharing {
			if id == rightID {
				result.Status = IdentityMatchStatusHardConflict
				result.BlockReason = blockSamePhotoCooccurrence
				result.Best = &IdentityCandidateResult{
					PersonID:    rightID,
					Status:      IdentityMatchStatusHardConflict,
					BlockReason: blockSamePhotoCooccurrence,
				}
				return result
			}
		}
	}

	pair := ScoreIdentityEvidence(left, right)
	if pair.Status == IdentityMatchStatusInvalid {
		result.Status = IdentityMatchStatusInvalid
		result.BlockReason = blockInvalidQuery
		return result
	}

	cand := IdentityCandidateResult{
		PersonID:        rightID,
		Score:           pair.Score,
		Boundary:        pair.Boundary,
		CenterFitOK:     pair.CenterFitOK,
		CenterIDs:       append([]uint{}, pair.CenterIDs...),
		SupportingUnits: pair.SupportingUnits,
		MinSupportCount: pair.MinSupportCount,
		StableCenters:   pair.StableCenters,
		Status:          IdentityMatchStatusMatch,
	}

	// 引擎级护栏分类（与 matcher AutoEligible 对齐的证据门槛），策略层再应用阈值。
	switch {
	case !pair.StableCenters || pair.MinSupportCount < e.cfg.MinCenterFaces:
		cand.Status = IdentityMatchStatusInsufficient
		cand.BlockReason = blockUnstableCenter
	case !pair.CenterFitOK:
		cand.Status = IdentityMatchStatusInsufficient
		cand.BlockReason = blockBelowCenterBoundary
	case pair.Score < e.cfg.RescueThreshold:
		cand.Status = IdentityMatchStatusInsufficient
		cand.BlockReason = blockScoreBelowThreshold
	}

	result.Best = &cand
	result.Status = cand.Status
	result.BlockReason = cand.BlockReason
	result.Candidates = []IdentityCandidateResult{cand}
	return result
}
