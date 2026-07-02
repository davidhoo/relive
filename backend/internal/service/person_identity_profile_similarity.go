package service

import (
	"sort"

	"github.com/davidhoo/relive/internal/model"
)

// PersonPair 标识一对目标人物与候选人物，用于批量精确比较。
type PersonPair struct {
	TargetID    uint
	CandidateID uint
}

// PersonProfileSimilarityProvider 为合并建议服务提供基于身份画像中心的批量相似度查询。
//
// 合并建议服务不直接访问 identityProfileANN 或 matcher 内部字段，仅通过该批量接口获取
// 候选召回与精确评分。任一方法返回 bool=false 表示画像数据不可安全使用（ANN 未 ready、
// 模型签名错误或数据库查询失败），调用方必须整体回退 legacy prototype 路径。
//
// 语义：
//   - SimilarPeople：为每个目标人物返回按精确中心分数排序的候选人物（召回 + 中心评分）。
//   - ComparePeople：对给定人物对批量做活动中心与真实 medoid 验证，返回最终分数。
//   - 空输入返回空结果和 true。
//   - Person ID 去重、过滤 0；结果排序稳定。
//
// Provider 复用 Task 8 matcher 的 embedding 校验、中心解码、cosine similarity 与 fail-closed
// 规则，不复制第二套向量校验逻辑。
type PersonProfileSimilarityProvider interface {
	SimilarPeople(personIDs []uint, k int) (map[uint][]IdentityProfileMatch, bool)
	ComparePeople(pairs []PersonPair) (map[PersonPair]IdentityProfileMatch, bool)
}

// profileSimilarityANNK 是每个中心向 ANN 请求的最大候选人物数。
const profileSimilarityANNK = 50

// profileSimilarityMaxPerTarget 是单个目标召回后保留的最大候选数（按中心分排序截断）。
const profileSimilarityMaxPerTarget = 200

// profileSimilarityFaceRepo 仅消费 provider 实际需要的人脸仓库方法。
type profileSimilarityFaceRepo interface {
	ListByIDs(ids []uint) ([]*model.Face, error)
}

// personProfileSimilarityProvider 是 PersonProfileSimilarityProvider 的生产实现。
// 依赖 Task 7 的 identityProfileANN（召回）、profileRepo（批量活动中心）与 faceRepo
// （medoid 人脸验证）。embeddingModel 用于数据库侧过滤与 ANN 查询签名校验。
type personProfileSimilarityProvider struct {
	ann            *identityProfileANN
	profileRepo    matcherProfileRepo
	faceRepo       profileSimilarityFaceRepo
	embeddingModel string
}

// NewPersonProfileSimilarityProvider 构造基于身份画像中心的相似度 provider。
// ann 为 Task 7 的 ANN 缓存；profileRepo/faceRepo 提供批量中心与 medoid 数据；
// embeddingModel 为当前服务 embedding 模型签名。
func NewPersonProfileSimilarityProvider(
	ann *identityProfileANN,
	profileRepo matcherProfileRepo,
	faceRepo profileSimilarityFaceRepo,
	embeddingModel string,
) PersonProfileSimilarityProvider {
	return &personProfileSimilarityProvider{
		ann:            ann,
		profileRepo:    profileRepo,
		faceRepo:       faceRepo,
		embeddingModel: embeddingModel,
	}
}

// decodedCenter 是解码后的活动中心向量及其元数据，用于精确 cosine 评分。
type decodedCenter struct {
	id      uint
	emb     []float32
	medoid  uint // MedoidFaceID，0 表示无 medoid
	person  uint
	ordinal int
}

// SimilarPeople 为每个目标人物返回按精确中心分数排序的候选人物。
//
// 流程：批量加载目标活动中心 → 每个中心查询 ANN 召回候选 → 合并去重 → 批量加载候选中心 →
// 计算最佳中心对 cosine 作为精确分数 → 按 (分数 DESC, 候选 ID ASC) 截断到 k。
//
// 整批回退（返回 false）：ANN 未注入/未 ready/模型签名错误、活动中心查询失败。
// 逐目标跳过：目标无活动中心或中心全部非法（不产生 entry，调用方对该目标回退 legacy）。
func (p *personProfileSimilarityProvider) SimilarPeople(personIDs []uint, k int) (map[uint][]IdentityProfileMatch, bool) {
	out := make(map[uint][]IdentityProfileMatch)
	if p == nil || p.ann == nil {
		return out, false
	}
	targets := dedupSortUint(personIDs)
	if len(targets) == 0 {
		return out, true
	}
	if k <= 0 {
		return out, true
	}

	// 批量加载目标活动中心。
	targetCenters, ok := p.loadCenters(targets)
	if !ok {
		return out, false
	}

	// 每个目标用每个有效中心查询 ANN，合并候选人物（排除自身）。
	candidatesByTarget := make(map[uint]map[uint]struct{}, len(targetCenters))
	allCandidateIDs := make([]uint, 0)
	for _, targetID := range targets {
		centers := targetCenters[targetID]
		if len(centers) == 0 {
			continue // 该目标无 ready profile → 跳过，调用方回退 legacy
		}
		seen := make(map[uint]struct{})
		for _, c := range centers {
			ids, ready := p.ann.Search(c.emb, profileSimilarityANNK, p.embeddingModel)
			if !ready {
				// ANN 不可用或模型签名错误 → 整批回退 legacy。
				return out, false
			}
			for _, pid := range ids {
				if pid == 0 || pid == targetID {
					continue
				}
				if _, dup := seen[pid]; dup {
					continue
				}
				seen[pid] = struct{}{}
			}
		}
		if len(seen) == 0 {
			continue
		}
		candidatesByTarget[targetID] = seen
		for cid := range seen {
			allCandidateIDs = append(allCandidateIDs, cid)
		}
	}

	if len(allCandidateIDs) == 0 {
		return out, true
	}

	// 批量加载候选活动中心。
	candidateCenters, ok := p.loadCenters(dedupSortUint(allCandidateIDs))
	if !ok {
		return out, false
	}

	// 对每个 (目标, 候选) 计算最佳中心对精确分数。
	for _, targetID := range targets {
		seen := candidatesByTarget[targetID]
		if len(seen) == 0 {
			continue
		}
		tCenters := targetCenters[targetID]
		matches := make([]IdentityProfileMatch, 0, len(seen))
		for candidateID := range seen {
			cCenters := candidateCenters[candidateID]
			if len(cCenters) == 0 {
				continue // 候选无 ready profile（generation/model/person 不匹配）→ 丢弃
			}
			score, _, ok := bestCenterPair(tCenters, cCenters)
			if !ok {
				continue
			}
			matches = append(matches, IdentityProfileMatch{
				Available: true,
				PersonID:  candidateID,
				Score:     score,
			})
		}
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].Score != matches[j].Score {
				return matches[i].Score > matches[j].Score
			}
			return matches[i].PersonID < matches[j].PersonID
		})
		if len(matches) > profileSimilarityMaxPerTarget {
			matches = matches[:profileSimilarityMaxPerTarget]
		}
		if k < len(matches) {
			matches = matches[:k]
		}
		// 仅记录有存活候选的目标；无候选的目标不产生 entry，调用方对该目标回退 legacy。
		if len(matches) > 0 {
			out[targetID] = matches
		}
	}
	return out, true
}

// ComparePeople 对给定人物对批量做活动中心与真实 medoid 验证。
//
// 流程：批量加载所有相关人物活动中心 → 批量加载所有 medoid 人脸 → 对每对选择最佳中心对
// （sim DESC, targetCenterID ASC, candidateCenterID ASC），逐对尝试 medoid 验证（最佳中心
// medoid 无效则尝试次佳中心对）→ finalScore = min(centerSim, medoidSim)。
//
// 整批回退（返回 false）：活动中心或 medoid 查询失败。
// 逐对回退（Available=false）：无有效中心对，或所有中心对的 medoid 验证均失败 → 调用方对该对回退 legacy。
// 中心 ANN 距离不得直接作为最终分数。
func (p *personProfileSimilarityProvider) ComparePeople(pairs []PersonPair) (map[PersonPair]IdentityProfileMatch, bool) {
	out := make(map[PersonPair]IdentityProfileMatch)
	if p == nil {
		return out, false
	}
	// 去重、过滤 0；保持确定性顺序。
	deduped := dedupPairs(pairs)
	if len(deduped) == 0 {
		return out, true
	}

	personIDs := make([]uint, 0, len(deduped)*2)
	for _, pr := range deduped {
		personIDs = append(personIDs, pr.TargetID, pr.CandidateID)
	}
	centersByPerson, ok := p.loadCenters(dedupSortUint(personIDs))
	if !ok {
		return out, false
	}

	// 收集所有 medoid face ID，批量加载。
	medoidIDs := make([]uint, 0)
	for _, centers := range centersByPerson {
		for _, c := range centers {
			if c.medoid != 0 {
				medoidIDs = append(medoidIDs, c.medoid)
			}
		}
	}
	faceByID, ok := p.loadMedoidFaces(dedupSortUint(medoidIDs))
	if !ok {
		return out, false
	}

	for _, pr := range deduped {
		tCenters := centersByPerson[pr.TargetID]
		cCenters := centersByPerson[pr.CandidateID]
		if len(tCenters) == 0 || len(cCenters) == 0 {
			out[pr] = IdentityProfileMatch{Available: false}
			continue
		}
		score, pair, ok := bestCenterPair(tCenters, cCenters)
		if !ok {
			out[pr] = IdentityProfileMatch{Available: false}
			continue
		}
		// 尝试 medoid 验证：从最佳中心对开始，失败则尝试次佳中心对。
		finalScore, verified := verifyMedoidChain(tCenters, cCenters, faceByID, pair)
		if !verified {
			out[pr] = IdentityProfileMatch{Available: false}
			continue
		}
		out[pr] = IdentityProfileMatch{
			Available: true,
			PersonID:  pr.CandidateID,
			Score:     finalScore,
		}
		_ = score // finalScore 已是 min(centerSim, medoidSim)，中心分仅在链中用于排序
	}
	return out, true
}

// loadCenters 批量加载活动中心并解码、过滤非法向量。返回 false 表示数据库查询失败，
// 调用方必须整批回退 legacy。
func (p *personProfileSimilarityProvider) loadCenters(personIDs []uint) (map[uint][]decodedCenter, bool) {
	out := make(map[uint][]decodedCenter)
	if len(personIDs) == 0 {
		return out, true
	}
	raw, err := p.profileRepo.ListActiveCentersByPersonIDs(personIDs, p.embeddingModel)
	if err != nil {
		return out, false
	}
	for pid, centers := range raw {
		decoded := make([]decodedCenter, 0, len(centers))
		for _, c := range centers {
			if c == nil {
				continue
			}
			emb := model.DecodeEmbedding(c.CentroidEmbedding)
			if !validVector(emb) {
				continue // 跳过非法中心向量，不让单条坏数据拖垮整批
			}
			var medoid uint
			if c.MedoidFaceID != nil {
				medoid = *c.MedoidFaceID
			}
			decoded = append(decoded, decodedCenter{
				id:      c.ID,
				emb:     emb,
				medoid:  medoid,
				person:  pid,
				ordinal: c.Ordinal,
			})
		}
		// 按 center ID 升序，保证同分时迭代顺序确定。
		sort.SliceStable(decoded, func(i, j int) bool { return decoded[i].id < decoded[j].id })
		out[pid] = decoded
	}
	return out, true
}

// loadMedoidFaces 批量加载 medoid 人脸。返回 false 表示查询失败，调用方整批回退。
func (p *personProfileSimilarityProvider) loadMedoidFaces(faceIDs []uint) (map[uint]*model.Face, bool) {
	out := make(map[uint]*model.Face)
	if len(faceIDs) == 0 {
		return out, true
	}
	faces, err := p.faceRepo.ListByIDs(faceIDs)
	if err != nil {
		return out, false
	}
	for _, f := range faces {
		if f == nil {
			continue
		}
		out[f.ID] = f
	}
	return out, true
}

// centerPairScore 是一对 (目标中心, 候选中心) 的精确 cosine 分数与中心 ID。
type centerPairScore struct {
	score          float64
	targetCenterID uint
	candCenterID   uint
	targetMedoid   uint
	candMedoid     uint
}

// bestCenterPair 计算目标与候选所有中心组合的精确 cosine，返回得分最高的中心对。
// 同分时按 (target_center_id ASC, candidate_center_id ASC) 选择。返回 ok=false 表示
// 无可用中心对。
func bestCenterPair(targetCenters, candidateCenters []decodedCenter) (float64, centerPairScore, bool) {
	if len(targetCenters) == 0 || len(candidateCenters) == 0 {
		return 0, centerPairScore{}, false
	}
	best := centerPairScore{score: -2}
	found := false
	for _, tc := range targetCenters {
		for _, cc := range candidateCenters {
			if len(tc.emb) != len(cc.emb) {
				continue // 维度不一致，跳过该组合
			}
			sim := cosineSimilarity(tc.emb, cc.emb)
			if !found || sim > best.score ||
				(sim == best.score && centerPairLess(tc, cc, best)) {
				best = centerPairScore{
					score:          sim,
					targetCenterID: tc.id,
					candCenterID:   cc.id,
					targetMedoid:   tc.medoid,
					candMedoid:     cc.medoid,
				}
				found = true
			}
		}
	}
	if !found {
		return 0, centerPairScore{}, false
	}
	return best.score, best, true
}

// centerPairLess 判断 (tc, cc) 是否在同分时优先于 best（target_center_id ASC, candidate_center_id ASC）。
func centerPairLess(tc, cc decodedCenter, best centerPairScore) bool {
	if tc.id != best.targetCenterID {
		return tc.id < best.targetCenterID
	}
	return cc.id < best.candCenterID
}

// orderedCenterPairs 返回所有中心对按 (sim DESC, target_center_id ASC, candidate_center_id ASC)
// 排序的结果，用于 medoid 链式验证。
func orderedCenterPairs(targetCenters, candidateCenters []decodedCenter) []centerPairScore {
	pairs := make([]centerPairScore, 0, len(targetCenters)*len(candidateCenters))
	for _, tc := range targetCenters {
		for _, cc := range candidateCenters {
			if len(tc.emb) != len(cc.emb) {
				continue
			}
			pairs = append(pairs, centerPairScore{
				score:          cosineSimilarity(tc.emb, cc.emb),
				targetCenterID: tc.id,
				candCenterID:   cc.id,
				targetMedoid:   tc.medoid,
				candMedoid:     cc.medoid,
			})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		if pairs[i].targetCenterID != pairs[j].targetCenterID {
			return pairs[i].targetCenterID < pairs[j].targetCenterID
		}
		return pairs[i].candCenterID < pairs[j].candCenterID
	})
	return pairs
}

// verifyMedoidChain 从最佳中心对开始，逐对尝试真实 medoid 验证。最佳中心对 medoid 无效时
// 尝试次佳中心对。返回最终分数 min(centerSim, medoidSim) 与是否验证通过。
//
// medoid 验证：双方 medoid 均存在、仍属于对应人物、embedding 有效、维度一致、非零范数且无
// NaN/Inf；计算两个 medoid 的精确 cosine similarity。
func verifyMedoidChain(targetCenters, candidateCenters []decodedCenter, faceByID map[uint]*model.Face, _ centerPairScore) (float64, bool) {
	pairs := orderedCenterPairs(targetCenters, candidateCenters)
	for _, pr := range pairs {
		tFace := faceByID[pr.targetMedoid]
		cFace := faceByID[pr.candMedoid]
		if tFace == nil || cFace == nil {
			continue
		}
		// 校验 medoid 仍属于对应人物。
		if personIDOf(tFace) != pr.targetCenterPersonID(targetCenters) {
			continue
		}
		if personIDOf(cFace) != pr.candidateCenterPersonID(candidateCenters) {
			continue
		}
		tEmb := model.DecodeEmbedding(tFace.Embedding)
		cEmb := model.DecodeEmbedding(cFace.Embedding)
		if !validVector(tEmb) || !validVector(cEmb) {
			continue
		}
		if len(tEmb) != len(cEmb) {
			continue
		}
		medoidSim := cosineSimilarity(tEmb, cEmb)
		return minFloat64(pr.score, medoidSim), true
	}
	return 0, false
}

// targetCenterPersonID 返回该中心对的目标中心所属人物 ID（通过 center ID 反查）。
func (ps centerPairScore) targetCenterPersonID(targetCenters []decodedCenter) uint {
	for _, c := range targetCenters {
		if c.id == ps.targetCenterID {
			return c.person
		}
	}
	return 0
}

// candidateCenterPersonID 返回该中心对的候选中心所属人物 ID。
func (ps centerPairScore) candidateCenterPersonID(candidateCenters []decodedCenter) uint {
	for _, c := range candidateCenters {
		if c.id == ps.candCenterID {
			return c.person
		}
	}
	return 0
}

// minFloat64 返回两个 float64 的较小值（不使用 math.Min 以避免对 NaN 的非预期行为）。
func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// dedupPairs 去重 PersonPair（过滤 0），保持首次出现顺序。
func dedupPairs(pairs []PersonPair) []PersonPair {
	seen := make(map[PersonPair]struct{}, len(pairs))
	out := make([]PersonPair, 0, len(pairs))
	for _, pr := range pairs {
		if pr.TargetID == 0 || pr.CandidateID == 0 || pr.TargetID == pr.CandidateID {
			continue
		}
		if _, dup := seen[pr]; dup {
			continue
		}
		seen[pr] = struct{}{}
		out = append(out, pr)
	}
	return out
}
