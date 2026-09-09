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

// requestProfileANNRebuild 在「ANN 召回了人物但活动中心证据全部不可用」时请求重建，
// 等价于旧 IdentityProfileMatcher 在 centersByPerson 为空时的自愈路径。
func (e *IdentityMatchingEngine) requestProfileANNRebuild() {
	if e == nil || e.ann == nil {
		return
	}
	e.ann.RequestRebuild()
}

// IndexGeneration 返回当前 ANN 代次；不可用时返回 0。
func (e *IdentityMatchingEngine) IndexGeneration() int {
	if e == nil || e.ann == nil {
		return 0
	}
	return int(e.ann.Stats(e.cfg.EmbeddingModel).Generation)
}

// MatchComponent 对一个待聚类组件执行身份匹配（每个组件调用一次）。
// 适配为 IdentityEvidence 后走统一召回与 ScoreComponentAgainstPerson 打分核心；
// 不隐式调用旧 matcher 另算分数，也不把人物双向全覆盖强加到组件匹配。
func (e *IdentityMatchingEngine) MatchComponent(component []*model.Face, opts IdentityRecallOptions) IdentityMatchResult {
	opts = opts.normalized()
	if e == nil {
		return newIdentityMatchResult(IdentityMatchStatusUnavailable, blockIndexUnavailable)
	}
	ev, ok := buildComponentEvidence(component)
	if !ok {
		return newIdentityMatchResult(IdentityMatchStatusInvalid, blockInvalidQuery)
	}
	cands, ready := e.recallPersonCandidates(ev, 0, opts)
	if !ready {
		return newIdentityMatchResult(IdentityMatchStatusUnavailable, blockIndexUnavailable)
	}
	if len(cands) == 0 {
		return newIdentityMatchResult(IdentityMatchStatusNoCandidate, "")
	}
	candEvs, st, rs := e.loadPersonEvidences(cands)
	if st != IdentityMatchStatusMatch {
		return newIdentityMatchResult(st, rs)
	}

	var scored []IdentityCandidateResult
	missingEvidence := 0
	for _, cid := range cands {
		cev, ok := candEvs[cid]
		if !ok || len(cev.Units) == 0 {
			missingEvidence++
			continue
		}
		scored = append(scored, e.scoreComponentCandidate(ev, cid, cev))
	}
	if len(scored) == 0 {
		if missingEvidence > 0 {
			// 召回 ID 与活动中心脱节：请求 ANN 重建，避免永久不可用。
			e.requestProfileANNRebuild()
			return newIdentityMatchResult(IdentityMatchStatusUnavailable, blockProfileUnavailable)
		}
		return newIdentityMatchResult(IdentityMatchStatusNoCandidate, "")
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].PersonID < scored[j].PersonID
	})
	if opts.TopK > 0 && len(scored) > opts.TopK {
		scored = scored[:opts.TopK]
	}

	result := newIdentityMatchResult(IdentityMatchStatusNoCandidate, "")
	result.MarginApplicable = true
	result.IncompleteEvidence = missingEvidence > 0
	best := scored[0]
	// 负证据只对最佳候选 fail-closed（与旧 matcher 优先级一致）。
	if reason := e.componentHardBlock(ev, best.PersonID, scored); reason != "" {
		best.Status = IdentityMatchStatusHardConflict
		if reason == blockNegativeEvidenceUnavail {
			best.Status = IdentityMatchStatusUnavailable
		}
		best.BlockReason = reason
		result.Best = &best
		result.Candidates = scored
		result.Status = best.Status
		result.BlockReason = reason
		return result
	}
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

// buildComponentEvidence 把清洗后的查询脸适配为组件 IdentityEvidence。
func buildComponentEvidence(component []*model.Face) (IdentityEvidence, bool) {
	qfaces := cleanQueryFaces(component)
	if len(qfaces) == 0 {
		return IdentityEvidence{}, false
	}
	units := make([]IdentityEvidenceUnit, 0, len(qfaces))
	for _, qf := range qfaces {
		units = append(units, IdentityEvidenceUnit{
			FaceID:   qf.faceID,
			PhotoID:  qf.photo,
			PersonID: qf.person,
			Vector:   qf.emb,
			Weight:   qf.weight,
		})
	}
	return IdentityEvidence{
		Kind:            identityEvidenceKindComponent,
		Units:           units,
		SourcePersonIDs: collectSourcePersons(qfaces),
		PhotoIDs:        collectPhotoIDs(qfaces),
	}, true
}

func (e *IdentityMatchingEngine) scoreComponentCandidate(component IdentityEvidence, personID uint, personEv IdentityEvidence) IdentityCandidateResult {
	pair := ScoreComponentAgainstPerson(component, personEv)
	cand := IdentityCandidateResult{
		PersonID:          personID,
		Score:             pair.Score,
		Boundary:          pair.Boundary,
		CenterFitOK:       pair.CenterFitOK,
		CenterIDs:         append([]uint{}, pair.CenterIDs...),
		SupportingUnits:   pair.SupportingUnits,
		MinSupportCount:   pair.MinSupportCount,
		StableCenters:     pair.StableCenters,
		ProfileGeneration: personEv.Generation,
		Status:            IdentityMatchStatusMatch,
	}
	if pair.Status == IdentityMatchStatusInvalid {
		cand.Status = IdentityMatchStatusInvalid
		cand.BlockReason = blockInvalidQuery
		return cand
	}
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
	return cand
}

// componentHardBlock 对最佳候选做 cannot-link / 同照片共现检查；关键负证据读取失败时 fail closed。
func (e *IdentityMatchingEngine) componentHardBlock(component IdentityEvidence, bestPersonID uint, all []IdentityCandidateResult) string {
	cannotLinkErr := false
	blockedByCannotLink := false
	if e.cannotLinkRepo != nil && len(component.SourcePersonIDs) > 0 {
		blockedSet := make(map[uint]struct{})
		for _, sp := range component.SourcePersonIDs {
			ids, err := e.cannotLinkRepo.ListByPersonID(sp)
			if err != nil {
				cannotLinkErr = true
				break
			}
			for _, id := range ids {
				blockedSet[id] = struct{}{}
			}
		}
		if !cannotLinkErr {
			if _, ok := blockedSet[bestPersonID]; ok {
				blockedByCannotLink = true
			}
		}
	}

	cooccurErr := false
	blockedByCooccurrence := false
	if e.faceRepo != nil && len(component.PhotoIDs) > 0 && len(all) > 0 {
		candIDs := make([]uint, 0, len(all))
		for _, c := range all {
			candIDs = append(candIDs, c.PersonID)
		}
		cooccur, err := e.faceRepo.ListPersonIDsCooccurringWithPhotos(component.PhotoIDs, candIDs)
		if err != nil {
			cooccurErr = true
		} else {
			for _, id := range cooccur {
				if id == bestPersonID {
					blockedByCooccurrence = true
					break
				}
			}
		}
	}

	if !cannotLinkErr && blockedByCannotLink {
		return blockCannotLink
	}
	if !cooccurErr && blockedByCooccurrence {
		return blockSamePhotoCooccurrence
	}
	if cannotLinkErr || cooccurErr {
		return blockNegativeEvidenceUnavail
	}
	return ""
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
			// 目标无活动中心/画像不可读：技术不可用，禁止伪装成「确实无候选」。
			out[targetID] = newIdentityMatchResult(IdentityMatchStatusUnavailable, blockProfileUnavailable)
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
			out[pr] = newIdentityMatchResult(IdentityMatchStatusUnavailable, blockProfileUnavailable)
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
		PersonID:    raw.PersonID,
		Score:       raw.Score,
		CenterIDs:   append([]uint{}, raw.CenterIDs...),
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
		invalid := false
		for _, c := range centers {
			if c == nil {
				continue
			}
			emb := model.DecodeEmbedding(c.CentroidEmbedding)
			if !validVector(emb) {
				// 单人非法向量：跳过该人，不拖垮整批候选加载。
				invalid = true
				break
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
		if invalid {
			continue
		}
		generation := 0
		for _, c := range centers {
			if c != nil && c.Generation > 0 {
				generation = c.Generation
				break
			}
		}
		sort.SliceStable(units, func(i, j int) bool { return units[i].CenterID < units[j].CenterID })
		out[pid] = IdentityEvidence{
			Kind:       identityEvidenceKindPerson,
			PersonID:   pid,
			Generation: generation,
			Units:      units,
		}
	}
	return out, IdentityMatchStatusMatch, ""
}

// personRecallHit 记录 ANN 并集中某人物的召回质量，用于截断排序。
type personRecallHit struct {
	personID uint
	minRank  int
	hitCount int
}

// selectPersonIDsByRecallRank 按 (minRank ASC, hitCount DESC, personID ASC) 截断。
// personID 只用于同分稳定排序，不得决定谁进入上限。
//
// annK/exactReserve：精确补召命中的 minRank >= annK。先保留最多 exactReserve 个这类命中，
// 再按总排序填充剩余名额，避免 ANN 并集噪声把阈值邻近的精确补召挤出 MaxCandidates。
func selectPersonIDsByRecallRank(hits []personRecallHit, maxCandidates, annK, exactReserve int) []uint {
	if len(hits) == 0 {
		return nil
	}
	sorted := append([]personRecallHit(nil), hits...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].minRank != sorted[j].minRank {
			return sorted[i].minRank < sorted[j].minRank
		}
		if sorted[i].hitCount != sorted[j].hitCount {
			return sorted[i].hitCount > sorted[j].hitCount
		}
		return sorted[i].personID < sorted[j].personID
	})

	out := make([]uint, 0, len(sorted))
	seen := make(map[uint]struct{}, len(sorted))
	appendHit := func(h personRecallHit) bool {
		if _, ok := seen[h.personID]; ok {
			return true
		}
		if maxCandidates > 0 && len(out) >= maxCandidates {
			return false
		}
		seen[h.personID] = struct{}{}
		out = append(out, h.personID)
		return true
	}

	if exactReserve > 0 && annK > 0 {
		reserved := 0
		for _, h := range sorted {
			if h.minRank < annK {
				continue
			}
			if reserved >= exactReserve {
				break
			}
			if !appendHit(h) {
				return out
			}
			reserved++
		}
	}
	for _, h := range sorted {
		if !appendHit(h) {
			break
		}
	}
	return out
}

func (e *IdentityMatchingEngine) recallPersonCandidates(ev IdentityEvidence, targetID uint, opts IdentityRecallOptions) ([]uint, bool) {
	if e == nil || e.ann == nil {
		return nil, false
	}
	opts = opts.normalized()
	hits, ready := e.recallPersonHits(ev, targetID, opts)
	if !ready {
		return nil, false
	}
	return selectPersonIDsByRecallRank(hits, opts.MaxCandidates, opts.ANNK, opts.ExactK), true
}

func (e *IdentityMatchingEngine) rankPersonCandidates(
	targetID uint,
	targetEv IdentityEvidence,
	candIDs []uint,
	candEvs map[uint]IdentityEvidence,
	topK int,
) IdentityMatchResult {
	var scored []IdentityCandidateResult
	missingEvidence := 0
	for _, cid := range candIDs {
		cev, ok := candEvs[cid]
		if !ok || len(cev.Units) == 0 {
			missingEvidence++
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

	if len(scored) == 0 {
		// 召回了候选但证据全部不可读：技术不可用，禁止伪装成「确实无候选」。
		if missingEvidence > 0 {
			e.requestProfileANNRebuild()
			return newIdentityMatchResult(IdentityMatchStatusUnavailable, blockProfileUnavailable)
		}
		return newIdentityMatchResult(IdentityMatchStatusNoCandidate, "")
	}

	result := newIdentityMatchResult(IdentityMatchStatusNoCandidate, "")
	result.MarginApplicable = true
	result.TargetProfileGeneration = targetEv.Generation
	result.IncompleteEvidence = missingEvidence > 0
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
		PersonID:          rightID,
		Score:             pair.Score,
		Boundary:          pair.Boundary,
		CenterFitOK:       pair.CenterFitOK,
		CenterIDs:         append([]uint{}, pair.CenterIDs...),
		SupportingUnits:   pair.SupportingUnits,
		MinSupportCount:   pair.MinSupportCount,
		StableCenters:     pair.StableCenters,
		ProfileGeneration: right.Generation,
		Status:            IdentityMatchStatusMatch,
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
	result.TargetProfileGeneration = left.Generation
	result.Candidates = []IdentityCandidateResult{cand}
	return result
}
