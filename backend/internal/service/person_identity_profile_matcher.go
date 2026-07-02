package service

import (
	"math"
	"sort"

	"github.com/davidhoo/relive/internal/model"
)

// IdentityProfileMatcher 内部固定常量。独立前缀，避免与其他组件常量冲突。
const (
	// identityProfileMatcherANNK 是每张查询脸向 ANN 请求的最大候选人物数。
	identityProfileMatcherANNK = 50
	// identityProfileMatcherMaxCandidates 是组件候选并集的最大保留数。
	identityProfileMatcherMaxCandidates = 200
)

// 固定 BlockReason 枚举。稳定字符串，不拼接人名、路径、embedding 或错误全文。
const (
	blockIndexUnavailable        = "index_unavailable"
	blockInvalidQuery            = "invalid_query"
	blockProfileUnavailable      = "profile_unavailable"
	blockScoreBelowThreshold     = "score_below_threshold"
	blockMarginTooSmall          = "margin_too_small"
	blockBelowCenterBoundary     = "below_center_boundary"
	blockUnstableCenter          = "unstable_center"
	blockCannotLink              = "cannot_link"
	blockSamePhotoCooccurrence   = "same_photo_cooccurrence"
	blockNegativeEvidenceUnavail = "negative_evidence_unavailable"
)

// IdentityProfileMatch 是身份画像匹配器的结果。
//
// 语义：
//   - Available=false：画像索引、活动中心或关键负证据不可安全使用，调用方必须回退 legacy。
//   - Available=true, PersonID=0：匹配正常完成，但没有达到可报告的候选。
//   - PersonID!=0, AutoEligible=false：只可用于 shadow 分析或人工建议，禁止自动聚合。
//   - CenterIDs：最佳人物实际参与每张脸最佳匹配的中心 ID，去重后升序排列。
//   - BlockReason：稳定枚举字符串，使用上述 block* 常量。
type IdentityProfileMatch struct {
	Available      bool
	PersonID       uint
	Score          float64
	SecondPersonID uint
	SecondScore    float64
	Margin         float64
	CenterIDs      []uint
	AutoEligible   bool
	BlockReason    string
}

// IdentityProfileMatcherConfig 是构造 matcher 所需的配置子集。使用 Task 1 已有配置，
// 不新增未经校准的运行时配置项。
type IdentityProfileMatcherConfig struct {
	EmbeddingModel  string
	RescueThreshold float64 // IdentityProfileRescueThreshold
	Margin          float64 // IdentityProfileMargin
	MinCenterFaces  int     // IdentityProfileMinCenterFaces
}

// IdentityProfileMatcher 在 ANN 候选召回后从数据库批量读取活动中心做精确评分，
// 并以全局阈值、margin、中心 P10 边界、中心稳定性、cannot-link 与同照片共现共同
// 守住自动聚合精度。任一关键数据异常时 fail closed（Available=false 或 AutoEligible=false）。
//
// matcher 不接入现有聚类或合并建议，不写 faces.person_id，不更新画像 generation 或 ANN delta。
// 仅在 ANN ready、活动中心可批量读取且无非法中心时才给出可用结果。
//
// 依赖以窄接口声明（accept interfaces），便于测试注入失败 fake；生产接线（Task 10/11）
// 传入 repository 包的具体实现即可满足。
type IdentityProfileMatcher struct {
	ann            *identityProfileANN
	profileRepo    matcherProfileRepo
	faceRepo       matcherFaceRepo
	cannotLinkRepo matcherCannotLinkRepo
	cfg            IdentityProfileMatcherConfig
}

// matcherProfileRepo 仅消费 matcher 实际需要的画像仓库方法，避免测试实现全部接口。
type matcherProfileRepo interface {
	ListActiveCentersByPersonIDs(personIDs []uint, embeddingModel string) (map[uint][]*model.PersonIdentityCenter, error)
}

// matcherFaceRepo 仅消费同照片共现查询方法。
type matcherFaceRepo interface {
	ListPersonIDsCooccurringWithPhotos(photoIDs []uint, candidatePersonIDs []uint) ([]uint, error)
}

// matcherCannotLinkRepo 仅消费 cannot-link 列表查询方法。
type matcherCannotLinkRepo interface {
	ListByPersonID(personID uint) ([]uint, error)
}

// NewIdentityProfileMatcher 构造身份画像匹配器。ann 为 Task 7 的 ANN 缓存；
// profileRepo/faceRepo/cannotLinkRepo 提供批量数据与负证据查询；cfg 提供模型签名与护栏阈值。
func NewIdentityProfileMatcher(
	ann *identityProfileANN,
	profileRepo matcherProfileRepo,
	faceRepo matcherFaceRepo,
	cannotLinkRepo matcherCannotLinkRepo,
	cfg IdentityProfileMatcherConfig,
) *IdentityProfileMatcher {
	return &IdentityProfileMatcher{
		ann:            ann,
		profileRepo:    profileRepo,
		faceRepo:       faceRepo,
		cannotLinkRepo: cannotLinkRepo,
		cfg:            cfg,
	}
}

// queryFace 是清洗后的查询人脸：解码向量、质量权重、来源人物与照片。
type queryFace struct {
	faceID uint
	emb    []float32
	weight float64
	person uint // 来源 PersonID（0 表示未指派）
	photo  uint
}

// annCandidate 是 ANN 召回的候选人物及其召回统计，用于稳定截断。
type annCandidate struct {
	personID uint
	minRank  int // 该人物在所有查询脸 ANN 结果中的最小 rank
	hitCount int // 命中该人物的查询脸数量
}

// candidateScore 是某候选人物的精确评分结果。
type candidateScore struct {
	personID      uint
	score         float64
	boundary      float64
	centerFitOK   bool
	stable        bool
	perFaceCenter []uint // 每张查询脸命中的最佳中心 ID（按查询脸顺序）
}

// Match 对一个查询组件（一组人脸）执行身份画像匹配。
//
// 流程：清洗查询脸 → ANN 召回候选 → 批量读取活动中心 → 精确评分与聚合 →
// 最佳/次佳 margin → cannot-link 与同照片共现负证据 → 自动资格判定。
func (m *IdentityProfileMatcher) Match(component []*model.Face) IdentityProfileMatch {
	qfaces := cleanQueryFaces(component)
	if len(qfaces) == 0 {
		return IdentityProfileMatch{Available: false, BlockReason: blockInvalidQuery}
	}

	cands, ok := m.recallCandidates(qfaces)
	if !ok {
		return IdentityProfileMatch{Available: false, BlockReason: blockIndexUnavailable}
	}
	if len(cands) == 0 {
		return IdentityProfileMatch{Available: true}
	}

	candIDs := make([]uint, 0, len(cands))
	for _, c := range cands {
		candIDs = append(candIDs, c.personID)
	}
	centersByPerson, err := m.profileRepo.ListActiveCentersByPersonIDs(candIDs, m.cfg.EmbeddingModel)
	if err != nil {
		return IdentityProfileMatch{Available: false, BlockReason: blockProfileUnavailable}
	}
	if len(centersByPerson) == 0 {
		// ANN 候选全部因 generation/model/person 不合法而消失：索引与数据库不一致，
		// 请求 ANN 重建并按 unavailable 处理，不得当作正常 miss。
		if m.ann != nil {
			m.ann.RequestRebuild()
		}
		return IdentityProfileMatch{Available: false, BlockReason: blockProfileUnavailable}
	}

	scores, profileUnavailable := m.scoreCandidates(qfaces, centersByPerson)
	if profileUnavailable {
		return IdentityProfileMatch{Available: false, BlockReason: blockProfileUnavailable}
	}
	if len(scores) == 0 {
		return IdentityProfileMatch{Available: true}
	}

	// 候选按 score DESC, person_id ASC 排序，确定最佳与次佳。
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].score != scores[j].score {
			return scores[i].score > scores[j].score
		}
		return scores[i].personID < scores[j].personID
	})
	best := scores[0]

	result := IdentityProfileMatch{
		Available: true,
		PersonID:  best.personID,
		Score:     best.score,
		CenterIDs: dedupSortUint(best.perFaceCenter),
	}
	if len(scores) >= 2 {
		second := scores[1]
		result.SecondPersonID = second.personID
		result.SecondScore = second.score
		result.Margin = best.score - second.score
	} else {
		// 没有次佳人物时 SecondScore=-1，Margin=Score-(-1)。
		result.SecondScore = -1
		result.Margin = best.score - (-1)
	}

	if reason := m.evaluateAutoEligible(qfaces, best, scores, result.Margin); reason != "" {
		result.AutoEligible = false
		result.BlockReason = reason
	} else {
		result.AutoEligible = true
	}
	return result
}

// recallCandidates 对每张有效查询脸调用 ANN 召回候选人物并集。
// 任一查询返回 ready=false 时整体返回 ok=false。超 200 时按
// (最小 rank ASC, 命中脸数 DESC, person_id ASC) 稳定截断。
func (m *IdentityProfileMatcher) recallCandidates(qfaces []queryFace) ([]annCandidate, bool) {
	if m.ann == nil {
		return nil, false
	}
	minRank := make(map[uint]int)
	hitCount := make(map[uint]int)
	for _, qf := range qfaces {
		ids, ready := m.ann.Search(qf.emb, identityProfileMatcherANNK, m.cfg.EmbeddingModel)
		if !ready {
			return nil, false
		}
		for rank, pid := range ids {
			if pid == 0 {
				continue
			}
			if r, ok := minRank[pid]; !ok || rank < r {
				minRank[pid] = rank
			}
			hitCount[pid]++
		}
	}
	cands := make([]annCandidate, 0, len(minRank))
	for pid := range minRank {
		cands = append(cands, annCandidate{personID: pid, minRank: minRank[pid], hitCount: hitCount[pid]})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].minRank != cands[j].minRank {
			return cands[i].minRank < cands[j].minRank
		}
		if cands[i].hitCount != cands[j].hitCount {
			return cands[i].hitCount > cands[j].hitCount
		}
		return cands[i].personID < cands[j].personID
	})
	if len(cands) > identityProfileMatcherMaxCandidates {
		cands = cands[:identityProfileMatcherMaxCandidates]
	}
	return cands, true
}

// scoreCandidates 对每个候选人物：解码并验证全部活动中心向量，为每张查询脸选择最佳中心，
// 再按组件大小聚合分数与 P10 边界。任一中心 embedding 非法时返回 profileUnavailable=true
// （fail closed，不静默忽略）。
func (m *IdentityProfileMatcher) scoreCandidates(qfaces []queryFace, centersByPerson map[uint][]*model.PersonIdentityCenter) ([]candidateScore, bool) {
	type decoded struct {
		emb    []float32
		center *model.PersonIdentityCenter
	}
	decodedByPerson := make(map[uint][]decoded, len(centersByPerson))
	for pid, centers := range centersByPerson {
		ds := make([]decoded, 0, len(centers))
		for _, c := range centers {
			emb := model.DecodeEmbedding(c.CentroidEmbedding)
			if !validVector(emb) {
				// 任一中心数据非法：不得静默忽略并继续自动判断。
				return nil, true
			}
			ds = append(ds, decoded{emb: emb, center: c})
		}
		// 按 center ID 升序，保证同分时选择较小 center ID（迭代用严格 > 更新）。
		sort.SliceStable(ds, func(i, j int) bool { return ds[i].center.ID < ds[j].center.ID })
		decodedByPerson[pid] = ds
	}

	out := make([]candidateScore, 0, len(decodedByPerson))
	// 按 person ID 升序处理，使结果顺序确定（最终仍会按 score 排序）。
	personIDs := make([]uint, 0, len(decodedByPerson))
	for pid := range decodedByPerson {
		personIDs = append(personIDs, pid)
	}
	sort.Slice(personIDs, func(i, j int) bool { return personIDs[i] < personIDs[j] })

	for _, pid := range personIDs {
		ds := decodedByPerson[pid]
		items := make([]aggregateInput, len(qfaces))
		p10Items := make([]aggregateInput, len(qfaces))
		perFaceCenter := make([]uint, len(qfaces))
		perFaceSupport := make([]int, len(qfaces))

		for i, qf := range qfaces {
			bestIdx := -1
			bestSim := -2.0
			for k, d := range ds {
				sim := cosineSimilarity(qf.emb, d.emb)
				if sim > bestSim {
					bestSim = sim
					bestIdx = k
				}
			}
			d := ds[bestIdx]
			perFaceCenter[i] = d.center.ID
			perFaceSupport[i] = d.center.SupportCount
			items[i] = aggregateInput{value: bestSim, weight: qf.weight, faceID: qf.faceID}
			p10Items[i] = aggregateInput{value: d.center.SimilarityP10, weight: qf.weight, faceID: qf.faceID}
		}

		score, contrib := aggregateWeighted(items)
		boundary, _ := aggregateWeighted(p10Items)
		centerFitOK := score >= boundary

		stable := true
		for _, ci := range contrib {
			if perFaceSupport[ci] < m.cfg.MinCenterFaces {
				stable = false
				break
			}
		}

		out = append(out, candidateScore{
			personID:      pid,
			score:         score,
			boundary:      boundary,
			centerFitOK:   centerFitOK,
			stable:        stable,
			perFaceCenter: perFaceCenter,
		})
	}
	return out, false
}

// evaluateAutoEligible 判定最佳候选是否可自动聚合，返回空串表示可自动聚合，
// 否则返回固定 BlockReason。最高原始身份候选被阻断时仅返回原因，不会退而选择次佳。
//
// 优先级（含负证据不可用）：
//
//	cannot_link > same_photo_cooccurrence > negative_evidence_unavailable
//	> unstable_center > below_center_boundary > score_below_threshold > margin_too_small
func (m *IdentityProfileMatcher) evaluateAutoEligible(qfaces []queryFace, best candidateScore, allScores []candidateScore, margin float64) string {
	// ---- cannot-link 负证据 ----
	sourcePersons := collectSourcePersons(qfaces)
	cannotLinkErr := false
	blockedByCannotLink := false
	if len(sourcePersons) > 0 {
		blockedSet := make(map[uint]struct{})
		for _, sp := range sourcePersons {
			ids, err := m.cannotLinkRepo.ListByPersonID(sp)
			if err != nil {
				cannotLinkErr = true
				break
			}
			for _, id := range ids {
				blockedSet[id] = struct{}{}
			}
		}
		if !cannotLinkErr {
			if _, ok := blockedSet[best.personID]; ok {
				blockedByCannotLink = true
			}
		}
	}

	// ---- 同照片共现负证据 ----
	photoIDs := collectPhotoIDs(qfaces)
	candIDs := make([]uint, 0, len(allScores))
	for _, s := range allScores {
		candIDs = append(candIDs, s.personID)
	}
	cooccurErr := false
	blockedByCooccurrence := false
	if len(photoIDs) > 0 && len(candIDs) > 0 {
		cooccur, err := m.faceRepo.ListPersonIDsCooccurringWithPhotos(photoIDs, candIDs)
		if err != nil {
			cooccurErr = true
		} else {
			for _, id := range cooccur {
				if id == best.personID {
					blockedByCooccurrence = true
					break
				}
			}
		}
	}

	// ---- 优先级判定 ----
	if !cannotLinkErr && blockedByCannotLink {
		return blockCannotLink
	}
	if !cooccurErr && blockedByCooccurrence {
		return blockSamePhotoCooccurrence
	}
	if cannotLinkErr || cooccurErr {
		// 关键负证据不可用：fail closed，禁止自动吸附。
		return blockNegativeEvidenceUnavail
	}
	if !best.stable {
		return blockUnstableCenter
	}
	if !best.centerFitOK {
		return blockBelowCenterBoundary
	}
	if best.score < m.cfg.RescueThreshold {
		return blockScoreBelowThreshold
	}
	if margin < m.cfg.Margin {
		return blockMarginTooSmall
	}
	return ""
}

// aggregateInput 是聚合函数的单个输入：值、质量权重与 Face ID（同分稳定次序）。
type aggregateInput struct {
	value  float64
	weight float64
	faceID uint
}

// aggregateWeighted 按组件大小应用稳健聚合规则，返回聚合值与对最终分数有有效权重贡献的
// 输入索引（用于中心稳定性检查）。同分时以 Face ID 升序作为稳定次序。
//
//	1 张脸：直接使用该脸分数。
//	2–4 张脸：质量加权中位数（所有脸均视为有效贡献）。
//	5 张及以上：按值排序，从权重分布两端各截去 10%，对剩余（含边界样本的剩余部分权重）
//	            计算加权均值。
//
// 截尾允许边界样本只贡献剩余部分权重，而非因样本少就跳过截尾。所有排序同分以 Face ID 打破。
func aggregateWeighted(items []aggregateInput) (float64, []int) {
	n := len(items)
	if n == 0 {
		return 0, nil
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool {
		a, b := items[idx[i]], items[idx[j]]
		if a.value != b.value {
			return a.value < b.value
		}
		return a.faceID < b.faceID
	})

	if n == 1 {
		return items[0].value, []int{0}
	}

	if n <= 4 {
		// 质量加权中位数：累计权重首次达到或超过总权重一半的值。
		totalW := 0.0
		for _, it := range items {
			totalW += it.weight
		}
		half := totalW / 2.0
		cum := 0.0
		for _, i := range idx {
			cum += items[i].weight
			if cum >= half {
				return items[i].value, allIndices(n)
			}
		}
		last := idx[len(idx)-1]
		return items[last].value, allIndices(n)
	}

	// n >= 5：双侧 10% 截尾加权均值。
	totalW := 0.0
	for _, it := range items {
		totalW += it.weight
	}
	trim := 0.1 * totalW
	kept := make([]float64, n)
	for i := range items {
		kept[i] = items[i].weight
	}
	// 截尾低端：沿排序顺序消耗 trim 权重，边界样本只贡献剩余部分。
	low := trim
	for k := 0; k < n && low > 0; k++ {
		i := idx[k]
		if kept[i] <= low {
			low -= kept[i]
			kept[i] = 0
		} else {
			kept[i] -= low
			low = 0
		}
	}
	// 截尾高端：从末尾消耗 trim 权重。
	high := trim
	for k := n - 1; k >= 0 && high > 0; k-- {
		i := idx[k]
		if kept[i] <= high {
			high -= kept[i]
			kept[i] = 0
		} else {
			kept[i] -= high
			high = 0
		}
	}
	sumW, sumWV := 0.0, 0.0
	var contrib []int
	for k := 0; k < n; k++ {
		i := idx[k]
		if kept[i] > 0 {
			sumW += kept[i]
			sumWV += kept[i] * items[i].value
			contrib = append(contrib, i)
		}
	}
	if sumW == 0 {
		// 退化保护：截尾后无剩余权重（理论上 10%+10%<100% 不会发生），回退普通加权均值。
		sumW, sumWV = 0.0, 0.0
		for _, it := range items {
			sumW += it.weight
			sumWV += it.weight * it.value
		}
		if sumW == 0 {
			return items[idx[0]].value, allIndices(n)
		}
		return sumWV / sumW, allIndices(n)
	}
	return sumWV / sumW, contrib
}

// allIndices 返回 [0..n-1]。
func allIndices(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// cleanQueryFaces 清洗查询组件：忽略 nil、空 embedding、解码失败、维度不一致、
// NaN/Inf、零范数人脸；相同 Face ID 只计一次；按 Face ID 升序保证浮点累加确定性。
// 质量为 NaN/Inf/负数的 automatic 人脸视为无效；manual_locked 权重固定 1.0。
// RetryCount 与 ClusterScore 不参与任何清洗、权重或筛选。
func cleanQueryFaces(component []*model.Face) []queryFace {
	seen := make(map[uint]struct{})
	var faces []queryFace
	dim := 0
	for _, f := range component {
		if f == nil {
			continue
		}
		if f.ID != 0 {
			if _, dup := seen[f.ID]; dup {
				continue
			}
			seen[f.ID] = struct{}{}
		}
		emb := model.DecodeEmbedding(f.Embedding)
		if !validVector(emb) {
			continue
		}
		if dim == 0 {
			dim = len(emb)
		}
		if len(emb) != dim {
			continue // 维度不一致，忽略
		}
		var w float64
		if f.ManualLocked {
			w = 1.0
		} else {
			q := f.QualityScore
			if math.IsNaN(q) || math.IsInf(q, 0) || q < 0 {
				continue
			}
			w = clampFloat64(q, 0.05, 1.0)
		}
		faces = append(faces, queryFace{
			faceID: f.ID,
			emb:    emb,
			weight: w,
			person: personIDOf(f),
			photo:  f.PhotoID,
		})
	}
	sort.SliceStable(faces, func(i, j int) bool { return faces[i].faceID < faces[j].faceID })
	return faces
}

// clampFloat64 将 v 限制到 [lo, hi]。
func clampFloat64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// personIDOf 返回 Face 的 PersonID，nil 视为 0。
func personIDOf(f *model.Face) uint {
	if f == nil || f.PersonID == nil {
		return 0
	}
	return *f.PersonID
}

// collectSourcePersons 收集查询组件中非零的来源 PersonID，去重升序。
func collectSourcePersons(qfaces []queryFace) []uint {
	seen := make(map[uint]struct{})
	var out []uint
	for _, qf := range qfaces {
		if qf.person == 0 {
			continue
		}
		if _, ok := seen[qf.person]; ok {
			continue
		}
		seen[qf.person] = struct{}{}
		out = append(out, qf.person)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// collectPhotoIDs 收集查询组件中非零的 PhotoID，去重升序。
func collectPhotoIDs(qfaces []queryFace) []uint {
	seen := make(map[uint]struct{})
	var out []uint
	for _, qf := range qfaces {
		if qf.photo == 0 {
			continue
		}
		if _, ok := seen[qf.photo]; ok {
			continue
		}
		seen[qf.photo] = struct{}{}
		out = append(out, qf.photo)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// dedupSortUint 去重并升序排序 uint 切片。
func dedupSortUint(in []uint) []uint {
	seen := make(map[uint]struct{}, len(in))
	out := make([]uint, 0, len(in))
	for _, v := range in {
		if v == 0 {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
