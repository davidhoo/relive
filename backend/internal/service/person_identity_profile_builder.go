package service

import (
	"errors"
	"math"
	"sort"

	"github.com/davidhoo/relive/internal/model"
)

// identity-profile builder 算法版本。写入 build.Profile.AlgorithmVersion，
// 供下游比对与失效判定。
const identityProfileAlgorithmVersion = "identity-profile-v1"

// 权重上下限。命名常量，避免散落的魔法数字。
//
// ipWeightMax：人工确认人脸的权重（最高可信度）。
// ipAutoWeightCeil：自动归属人脸的权重上限，严格小于人工权重，
// 保证单张自动脸不会以异常大权重拖动成熟中心。
// ipWeightMin：自动归属人脸的权重下限。
const (
	ipWeightMin      = 0.10
	ipWeightMax      = 1.00
	ipAutoWeightCeil = 0.80
)

// identityProfileBuilderConfig 是 builder 的可注入配置。
// 本任务不读取全局生产配置；测试必须可以注入确定值。
type identityProfileBuilderConfig struct {
	MaxCenters               int
	MinCenterFaces           int
	MinCenterPhotos          int
	MaxIterations            int
	AssignmentThreshold      float64
	MergeThreshold           float64
	MinQuality               float64
	MinAutomaticClusterScore float64
}

// defaultIdentityProfileBuilderConfig 返回算法默认配置。零值字段会被填充，
// 保证部分配置也能工作，但测试应显式注入全部值以获得确定性。
func defaultIdentityProfileBuilderConfig() identityProfileBuilderConfig {
	return identityProfileBuilderConfig{
		MaxCenters:               6,
		MinCenterFaces:           3,
		MinCenterPhotos:          2,
		MaxIterations:            5,
		AssignmentThreshold:      0.50,
		MergeThreshold:           0.75,
		MinQuality:               0.30,
		MinAutomaticClusterScore: 0.50,
	}
}

// identityProfileBuilder 是纯算法组件：人物 ID + 已有人脸 → 多中心身份画像。
//
// 它不访问数据库、不读取全局可变状态、不调用 time.Now()、不修改输入 Face、
// 不启动 goroutine。相同输入（含顺序变化）产生完全相同的输出。
type identityProfileBuilder struct {
	cfg identityProfileBuilderConfig
}

// NewIdentityProfileBuilder 构造一个 builder。零值字段以默认值填充；
// 显式提供的值（含 NaN/负数等非法值）原样保留，由 validateConfig 拒绝。
func NewIdentityProfileBuilder(cfg identityProfileBuilderConfig) *identityProfileBuilder {
	d := defaultIdentityProfileBuilderConfig()
	if cfg.MaxCenters != 0 {
		d.MaxCenters = cfg.MaxCenters
	}
	if cfg.MinCenterFaces != 0 {
		d.MinCenterFaces = cfg.MinCenterFaces
	}
	if cfg.MinCenterPhotos != 0 {
		d.MinCenterPhotos = cfg.MinCenterPhotos
	}
	if cfg.MaxIterations != 0 {
		d.MaxIterations = cfg.MaxIterations
	}
	if cfg.AssignmentThreshold != 0 {
		d.AssignmentThreshold = cfg.AssignmentThreshold
	}
	if cfg.MergeThreshold != 0 {
		d.MergeThreshold = cfg.MergeThreshold
	}
	if cfg.MinQuality != 0 {
		d.MinQuality = cfg.MinQuality
	}
	if cfg.MinAutomaticClusterScore != 0 {
		d.MinAutomaticClusterScore = cfg.MinAutomaticClusterScore
	}
	return &identityProfileBuilder{cfg: d}
}

// profileMember 是 builder 内部的人脸视图，解耦于 model.Face。
// vec 为归一化向量；excluded 人脸的 vec 为 nil。
type profileMember struct {
	faceID    uint
	photoID   uint
	manual    bool
	quality   float64
	score     float64
	vec       []float32 // 归一化后；nil 表示 excluded（无效 embedding）
	weight    float64
	eligible  bool // 是否可参与中心形成/重分配（accepted 证据）
	state     string
	centerIdx int // 所属中心临时下标，-1 表示无
	sim       float64
}

// profileCenter 是 builder 内部的中心视图。
type profileCenter struct {
	confirmed    bool
	members      []int // 指向 profileMember 切片的索引
	centroid     []float32
	weightedSum  []float32
	totalWeight  float64
	medoidFaceID uint
	ordinal      int
}

// Build 根据人物 ID 与已有人脸构建多中心身份画像。
//
// 纯函数：不访问数据库、不修改输入、不依赖时间或全局状态。
// 输入顺序变化不会改变中心、成员或 ordinal。
func (b *identityProfileBuilder) Build(personID uint, faces []*model.Face) (*model.PersonIdentityProfileBuild, error) {
	if err := b.validateConfig(); err != nil {
		return nil, err
	}

	// 复制并排序，绝不原地改变输入。
	sorted := make([]*model.Face, len(faces))
	copy(sorted, faces)
	sortFacesStable(sorted)

	members := b.preprocess(personID, sorted)
	centers := b.seedCenters(members)
	centers = b.reassign(members, centers)
	centers = b.enforceMaxCenters(members, centers)
	b.finalize(members, centers)
	b.assignOrdinals(centers)
	return b.assemble(personID, members, centers), nil
}

// validateConfig 校验配置数值合法，拒绝 NaN/Inf 与非正限制。
func (b *identityProfileBuilder) validateConfig() error {
	c := b.cfg
	if math.IsNaN(c.AssignmentThreshold) || math.IsInf(c.AssignmentThreshold, 0) ||
		math.IsNaN(c.MergeThreshold) || math.IsInf(c.MergeThreshold, 0) ||
		math.IsNaN(c.MinQuality) || math.IsInf(c.MinQuality, 0) ||
		math.IsNaN(c.MinAutomaticClusterScore) || math.IsInf(c.MinAutomaticClusterScore, 0) {
		return errors.New("identity profile builder: threshold config contains NaN/Inf")
	}
	if c.AssignmentThreshold <= 0 || c.AssignmentThreshold >= 1 {
		return errors.New("identity profile builder: assignment_threshold must be in (0,1)")
	}
	if c.MergeThreshold <= 0 || c.MergeThreshold >= 1 {
		return errors.New("identity profile builder: merge_threshold must be in (0,1)")
	}
	if c.MaxCenters < 1 || c.MinCenterFaces < 1 || c.MinCenterPhotos < 1 || c.MaxIterations < 1 {
		return errors.New("identity profile builder: integer limits must be positive")
	}
	return nil
}

// sortFacesStable 按确定性键排序：manual_locked DESC, cluster_score DESC,
// quality_score DESC, confidence DESC, id ASC。
func sortFacesStable(faces []*model.Face) {
	sort.SliceStable(faces, func(i, j int) bool {
		a, b := faces[i], faces[j]
		if a.ManualLocked != b.ManualLocked {
			return a.ManualLocked // true 在前
		}
		if a.ClusterScore != b.ClusterScore {
			return a.ClusterScore > b.ClusterScore
		}
		if a.QualityScore != b.QualityScore {
			return a.QualityScore > b.QualityScore
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		return a.ID < b.ID
	})
}

// preprocess 解码、校验、归一化每张人脸，并分类 eligible/excluded。
func (b *identityProfileBuilder) preprocess(personID uint, faces []*model.Face) []profileMember {
	members := make([]profileMember, len(faces))

	// 第一遍：解码 + 人物匹配 + 归一化，确定期望维度。
	expectedDim := 0
	for i, f := range faces {
		m := profileMember{
			faceID:    f.ID,
			photoID:   f.PhotoID,
			manual:    f.ManualLocked,
			quality:   f.QualityScore,
			score:     f.ClusterScore,
			state:     model.PersonIdentityMemberStateCandidate,
			centerIdx: -1,
		}

		personMatch := f.PersonID != nil && *f.PersonID == personID
		if !personMatch {
			m.state = model.PersonIdentityMemberStateExcluded
			members[i] = m
			continue
		}

		raw := model.DecodeEmbedding(f.Embedding)
		vec, ok := normalizeEmbedding(raw)
		if !ok {
			// 空向量、零范数、NaN、Inf。
			m.state = model.PersonIdentityMemberStateExcluded
			members[i] = m
			continue
		}
		m.vec = vec
		if expectedDim == 0 {
			expectedDim = len(vec)
		}
		members[i] = m
	}

	// 第二遍：维度一致性 + 证据/权重分类。
	for i := range members {
		m := &members[i]
		if m.vec == nil {
			continue // 已 excluded
		}
		if len(m.vec) != expectedDim {
			// 维度不一致：不参与中心计算。
			m.vec = nil
			m.state = model.PersonIdentityMemberStateExcluded
			continue
		}

		w, ok := faceWeight(m.manual, m.quality, m.score)
		if !ok {
			// 权重非法（NaN/Inf/负值的 quality/score）：拒绝。
			m.vec = nil
			m.state = model.PersonIdentityMemberStateExcluded
			continue
		}
		m.weight = w

		if m.manual {
			// 人工确认且 embedding 有效：最高可信度，可形成 confirmed 中心。
			m.eligible = true
			continue
		}

		// 自动归属：质量明显不合格 → excluded。
		if m.quality < b.cfg.MinQuality {
			m.vec = nil
			m.weight = 0
			m.state = model.PersonIdentityMemberStateExcluded
			continue
		}
		// 高质量 + 高聚类分数 → accepted 证据（eligible）。
		// embedding 有效但证据不足 → candidate（eligible=false）。
		if m.score >= b.cfg.MinAutomaticClusterScore {
			m.eligible = true
		}
		// 否则保持 candidate、eligible=false。
	}
	return members
}

// seedCenters 生成初始中心：
//   - 每张人工确认人脸 → confirmed 单样本中心。
//   - 自动证据人脸 → 贪心就近聚合为 auto 种子（最近且 sim≥AssignmentThreshold）。
//
// 随后立即解散不满足 MinCenterFaces/MinCenterPhotos 的自动种子，
// 其成员回到 candidate 池（仍 eligible，可加入其它存活中心）。
func (b *identityProfileBuilder) seedCenters(members []profileMember) []*profileCenter {
	var centers []*profileCenter

	// 人工 confirmed 中心。members 已按确定性顺序排列。
	for i := range members {
		m := &members[i]
		if !m.eligible || !m.manual {
			continue
		}
		c := &profileCenter{
			confirmed:    true,
			members:      []int{i},
			medoidFaceID: m.faceID,
		}
		c.centroid, c.weightedSum, c.totalWeight, _ = weightedCentroid([]profileMember{*m})
		m.centerIdx = len(centers)
		centers = append(centers, c)
	}

	// 自动种子：贪心就近聚合到最近的活动非 confirmed 中心。
	for i := range members {
		m := &members[i]
		if !m.eligible || m.manual {
			continue
		}
		bestIdx, bestSim := -1, -2.0
		for j, c := range centers {
			if c.confirmed {
				continue
			}
			sim := cosineSimilarity(m.vec, c.centroid)
			if sim > bestSim {
				bestSim, bestIdx = sim, j
			}
		}
		if bestIdx >= 0 && bestSim >= b.cfg.AssignmentThreshold {
			c := centers[bestIdx]
			c.members = append(c.members, i)
			c.centroid, c.weightedSum, c.totalWeight, _ = weightedCentroid(collectMembers(members, c.members))
			m.centerIdx = bestIdx
		} else {
			c := &profileCenter{
				members:      []int{i},
				medoidFaceID: m.faceID,
			}
			c.centroid, c.weightedSum, c.totalWeight, _ = weightedCentroid([]profileMember{*m})
			m.centerIdx = len(centers)
			centers = append(centers, c)
		}
	}

	// 解散不达标的自动种子并重建索引。
	for _, c := range centers {
		if c.confirmed {
			continue
		}
		if len(c.members) < b.cfg.MinCenterFaces || distinctPhotos(members, c.members) < b.cfg.MinCenterPhotos {
			c.members = nil
		}
	}
	centers = rebuildIndex(members, centers)
	return centers
}

// collectMembers 按索引从 members 切片收集成员副本。
func collectMembers(members []profileMember, idxs []int) []profileMember {
	out := make([]profileMember, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, members[i])
	}
	return out
}

// distinctPhotos 统计中心成员来自多少张不同照片。
func distinctPhotos(members []profileMember, idxs []int) int {
	seen := make(map[uint]struct{}, len(idxs))
	for _, i := range idxs {
		seen[members[i].photoID] = struct{}{}
	}
	return len(seen)
}

// rebuildIndex 重建中心切片：丢弃无成员的中心，重置所有成员的 centerIdx 并按
// 存活中心的新下标重新挂载。任何结构变更（合并/解散）后调用以保持索引一致。
func rebuildIndex(members []profileMember, centers []*profileCenter) []*profileCenter {
	out := make([]*profileCenter, 0, len(centers))
	for _, c := range centers {
		if c != nil && len(c.members) > 0 {
			out = append(out, c)
		}
	}
	for i := range members {
		members[i].centerIdx = -1
	}
	for k, c := range out {
		for _, i := range c.members {
			members[i].centerIdx = k
		}
	}
	return out
}

// reassign 执行球面重分配，最多 MaxIterations 轮。
//
// 每轮：将 eligible 非锁定成员分配到最近存活中心 → 重算加权质心 → 重选 Medoid →
// 将低于中心边界的成员移回 candidate → 解散不达标自动中心 → 合并兼容中心。
// 成员分组签名不变时提前终止，禁止浮点微小变化造成无限迭代。
func (b *identityProfileBuilder) reassign(members []profileMember, centers []*profileCenter) []*profileCenter {
	prevSig := membershipSignature(members, centers)
	for round := 0; round < b.cfg.MaxIterations; round++ {
		b.reassignRound(members, centers)
		b.pruneAndDissolve(members, centers)
		centers = b.mergePass(members, centers)
		centers = rebuildIndex(members, centers)
		curSig := membershipSignature(members, centers)
		if curSig == prevSig {
			return centers
		}
		prevSig = curSig
	}
	return centers
}

// reassignRound 将 eligible 非锁定成员分配到最近存活中心，并重算质心/medoid。
func (b *identityProfileBuilder) reassignRound(members []profileMember, centers []*profileCenter) {
	for _, c := range centers {
		c.members = c.members[:0]
	}
	for i := range members {
		m := &members[i]
		if !m.eligible {
			continue
		}
		// 人工锁定成员回到其 confirmed 中心。
		if m.manual && m.centerIdx >= 0 && m.centerIdx < len(centers) && centers[m.centerIdx].confirmed {
			centers[m.centerIdx].members = append(centers[m.centerIdx].members, i)
			continue
		}
		// 非锁定 eligible：找最近存活中心。
		bestIdx, bestSim := -1, -2.0
		for j, c := range centers {
			if len(c.centroid) == 0 {
				continue
			}
			sim := cosineSimilarity(m.vec, c.centroid)
			if sim > bestSim {
				bestSim, bestIdx = sim, j
			}
		}
		if bestIdx >= 0 && bestSim >= b.cfg.AssignmentThreshold {
			m.centerIdx = bestIdx
			centers[bestIdx].members = append(centers[bestIdx].members, i)
		} else {
			m.centerIdx = -1
		}
	}
	for _, c := range centers {
		if len(c.members) == 0 {
			c.centroid, c.weightedSum, c.totalWeight = nil, nil, 0
			c.medoidFaceID = 0
			continue
		}
		c.centroid, c.weightedSum, c.totalWeight, _ = weightedCentroid(collectMembers(members, c.members))
		c.medoidFaceID = centerMedoid(c.centroid, collectMembers(members, c.members))
	}
}

// pruneAndDissolve 将低于中心边界的成员移回 candidate，并解散不达标自动中心。
func (b *identityProfileBuilder) pruneAndDissolve(members []profileMember, centers []*profileCenter) {
	for _, c := range centers {
		if len(c.centroid) == 0 {
			continue
		}
		kept := c.members[:0]
		for _, i := range c.members {
			m := &members[i]
			if m.manual && c.confirmed {
				// 人工锁定成员永不被剪枝。
				kept = append(kept, i)
				continue
			}
			if cosineSimilarity(m.vec, c.centroid) >= b.cfg.AssignmentThreshold {
				kept = append(kept, i)
			} else {
				m.centerIdx = -1
			}
		}
		c.members = kept
	}
	for _, c := range centers {
		if c.confirmed {
			continue
		}
		if len(c.members) < b.cfg.MinCenterFaces || distinctPhotos(members, c.members) < b.cfg.MinCenterPhotos {
			c.members = nil
		}
	}
}

// mergePass 合并兼容中心：质心相似度 ≥ MergeThreshold 且合并后分布仍紧凑。
// confirmed 中心可参与合并（合并后保持 confirmed），但永不被静默丢弃。
func (b *identityProfileBuilder) mergePass(members []profileMember, centers []*profileCenter) []*profileCenter {
	for {
		// 按质心相似度降序收集候选对。
		type pair struct {
			i, j int
			sim  float64
		}
		var pairs []pair
		for i := 0; i < len(centers); i++ {
			if len(centers[i].centroid) == 0 {
				continue
			}
			for j := i + 1; j < len(centers); j++ {
				if len(centers[j].centroid) == 0 {
					continue
				}
				sim := cosineSimilarity(centers[i].centroid, centers[j].centroid)
				if sim >= b.cfg.MergeThreshold {
					pairs = append(pairs, pair{i, j, sim})
				}
			}
		}
		sort.SliceStable(pairs, func(a, c int) bool {
			if pairs[a].sim != pairs[c].sim {
				return pairs[a].sim > pairs[c].sim
			}
			if pairs[a].i != pairs[c].i {
				return pairs[a].i < pairs[c].i
			}
			return pairs[a].j < pairs[c].j
		})
		merged := false
		for _, p := range pairs {
			c1, c2 := centers[p.i], centers[p.j]
			if len(c1.centroid) == 0 || len(c2.centroid) == 0 {
				continue
			}
			if !b.centersMergeable(c1, c2, members) {
				continue
			}
			mergeInto(c1, c2, members)
			merged = true
		}
		if !merged {
			return centers
		}
	}
}

// centersMergeable 判断两个中心是否可合并：
//   - 质心相似度 ≥ MergeThreshold。
//   - 合并后成员分布仍满足中心紧密度（每个成员到合并质心 sim ≥ AssignmentThreshold）。
//   - 紧密度不足即拒绝，不会为了遵守最大中心数而强行合并明显不同的模式。
func (b *identityProfileBuilder) centersMergeable(c1, c2 *profileCenter, members []profileMember) bool {
	if cosineSimilarity(c1.centroid, c2.centroid) < b.cfg.MergeThreshold {
		return false
	}
	combined := append(collectMembers(members, c1.members), collectMembers(members, c2.members)...)
	centroid, _, _, ok := weightedCentroid(combined)
	if !ok {
		return false
	}
	for _, m := range combined {
		if len(m.vec) == 0 {
			continue
		}
		if cosineSimilarity(m.vec, centroid) < b.cfg.AssignmentThreshold {
			return false
		}
	}
	return true
}

// mergeInto 将 c2 的成员并入 c1，重算质心/medoid，并清空 c2。
func mergeInto(c1, c2 *profileCenter, members []profileMember) {
	c1.members = append(c1.members, c2.members...)
	if c2.confirmed {
		c1.confirmed = true
	}
	c2.members = nil
	c2.centroid, c2.weightedSum, c2.totalWeight = nil, nil, 0
	c2.medoidFaceID = 0
	c1.centroid, c1.weightedSum, c1.totalWeight, _ = weightedCentroid(collectMembers(members, c1.members))
	c1.medoidFaceID = centerMedoid(c1.centroid, collectMembers(members, c1.members))
}

// enforceMaxCenters 强制 MaxCenters 约束：
//  1. 优先合并最接近且兼容的中心。
//  2. 无法安全合并的弱中心成员退回 candidate。
//  3. 不得把弱中心强塞入不兼容中心。
//  4. confirmed 中心不得静默丢弃。
func (b *identityProfileBuilder) enforceMaxCenters(members []profileMember, centers []*profileCenter) []*profileCenter {
	for {
		centers = rebuildIndex(members, centers)
		if countActive(centers) <= b.cfg.MaxCenters {
			return centers
		}
		// 尝试合并最接近且兼容的一对。
		i, j, sim := closestPair(centers)
		if i >= 0 && sim >= b.cfg.MergeThreshold && b.centersMergeable(centers[i], centers[j], members) {
			mergeInto(centers[i], centers[j], members)
			continue
		}
		// 无法安全合并：解散最弱的非 confirmed 中心。
		weak := weakestAutoCenter(centers)
		if weak < 0 {
			// 仅剩 confirmed 中心，不得丢弃。
			return centers
		}
		centers[weak].members = nil
	}
}

// countActive 返回有成员的中心数量。
func countActive(centers []*profileCenter) int {
	n := 0
	for _, c := range centers {
		if c != nil && len(c.members) > 0 {
			n++
		}
	}
	return n
}

// closestPair 返回质心相似度最高的一对活动中心下标及其相似度。
func closestPair(centers []*profileCenter) (int, int, float64) {
	bestI, bestJ, best := -1, -1, -2.0
	for i := 0; i < len(centers); i++ {
		if len(centers[i].centroid) == 0 {
			continue
		}
		for j := i + 1; j < len(centers); j++ {
			if len(centers[j].centroid) == 0 {
				continue
			}
			sim := cosineSimilarity(centers[i].centroid, centers[j].centroid)
			if sim > best {
				best, bestI, bestJ = sim, i, j
			}
		}
	}
	return bestI, bestJ, best
}

// weakestAutoCenter 返回支持数最少（其次总权重最低）的非 confirmed 活动中心下标。
func weakestAutoCenter(centers []*profileCenter) int {
	idx, weakSupp, weakW := -1, math.MaxInt32, math.MaxFloat64
	for i, c := range centers {
		if c == nil || c.confirmed || len(c.members) == 0 {
			continue
		}
		supp := len(c.members)
		if supp < weakSupp || (supp == weakSupp && c.totalWeight < weakW) {
			weakSupp, weakW, idx = supp, c.totalWeight, i
		}
	}
	return idx
}

// finalize 重算所有存活中心的质心/medoid 与成员 sim，并确定最终状态。
func (b *identityProfileBuilder) finalize(members []profileMember, centers []*profileCenter) {
	for _, c := range centers {
		if len(c.members) == 0 {
			c.centroid, c.weightedSum, c.totalWeight = nil, nil, 0
			c.medoidFaceID = 0
			continue
		}
		c.centroid, c.weightedSum, c.totalWeight, _ = weightedCentroid(collectMembers(members, c.members))
		c.medoidFaceID = centerMedoid(c.centroid, collectMembers(members, c.members))
		for _, i := range c.members {
			mm := &members[i]
			mm.sim = cosineSimilarity(mm.vec, c.centroid)
			mm.state = model.PersonIdentityMemberStateAccepted
		}
	}
	// 未入中心的 eligible 成员 → candidate；excluded 保持。
	for i := range members {
		m := &members[i]
		if m.state == model.PersonIdentityMemberStateAccepted {
			continue
		}
		if m.state != model.PersonIdentityMemberStateExcluded {
			m.state = model.PersonIdentityMemberStateCandidate
		}
	}
}

// assignOrdinals 按内容驱动顺序分配 ordinal：confirmed DESC, support_count DESC,
// medoid_face_id ASC。保证输入顺序变化不改变 ordinal。
func (b *identityProfileBuilder) assignOrdinals(centers []*profileCenter) {
	active := make([]*profileCenter, 0, len(centers))
	for _, c := range centers {
		if c != nil && len(c.members) > 0 {
			active = append(active, c)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		a, bb := active[i], active[j]
		if a.confirmed != bb.confirmed {
			return a.confirmed
		}
		if len(a.members) != len(bb.members) {
			return len(a.members) > len(bb.members)
		}
		return a.medoidFaceID < bb.medoidFaceID
	})
	for i, c := range active {
		c.ordinal = i
	}
	for i, c := range active {
		centers[i] = c
	}
	for i := len(active); i < len(centers); i++ {
		centers[i] = nil
	}
}

// assemble 组装最终 PersonIdentityProfileBuild。
func (b *identityProfileBuilder) assemble(personID uint, members []profileMember, centers []*profileCenter) *model.PersonIdentityProfileBuild {
	out := &model.PersonIdentityProfileBuild{
		Profile: &model.PersonIdentityProfile{
			PersonID:         personID,
			AlgorithmVersion: identityProfileAlgorithmVersion,
			Status:           model.PersonIdentityProfileStatusReady,
		},
		Centers: []*model.PersonIdentityCenter{},
		Members: []*model.PersonIdentityCenterMember{},
	}

	for _, c := range centers {
		if c == nil || len(c.members) == 0 {
			continue
		}
		sims := make([]float64, 0, len(c.members))
		for _, i := range c.members {
			sims = append(sims, members[i].sim)
		}
		medoid := c.medoidFaceID
		center := &model.PersonIdentityCenter{
			PersonID:          personID,
			Ordinal:           c.ordinal,
			CentroidEmbedding: model.EncodeEmbedding(c.centroid),
			SumEmbedding:      model.EncodeEmbedding(c.weightedSum),
			MedoidFaceID:      &medoid,
			SupportCount:      len(c.members),
			TotalWeight:       c.totalWeight,
			SimilarityP10:     percentileSimilarity(sims, 10),
			SimilarityP50:     percentileSimilarity(sims, 50),
			Confirmed:         c.confirmed,
		}
		out.Centers = append(out.Centers, center)
		for _, i := range c.members {
			m := &members[i]
			ordinal := uint(c.ordinal)
			out.Members = append(out.Members, &model.PersonIdentityCenterMember{
				PersonID:   personID,
				CenterID:   &ordinal,
				FaceID:     m.faceID,
				PhotoID:    m.photoID,
				Similarity: m.sim,
				Weight:     m.weight,
				State:      model.PersonIdentityMemberStateAccepted,
			})
		}
	}

	// 剩余成员：candidate 或 excluded。
	for i := range members {
		m := &members[i]
		if m.state == model.PersonIdentityMemberStateAccepted {
			continue
		}
		out.Members = append(out.Members, &model.PersonIdentityCenterMember{
			PersonID:   personID,
			CenterID:   nil,
			FaceID:     m.faceID,
			PhotoID:    m.photoID,
			Similarity: 0,
			Weight:     m.weight,
			State:      m.state,
		})
	}

	// 成员按 face_id 升序输出，保证确定性。
	sort.SliceStable(out.Members, func(i, j int) bool {
		return out.Members[i].FaceID < out.Members[j].FaceID
	})
	out.Profile.FaceCountSnapshot = len(out.Members)
	return out
}

// membershipSignature 生成成员分组的稳定签名，用于检测重分配是否收敛。
// 签名基于每个成员所在中心的最小 faceID（内容驱动），与中心下标重排无关。
func membershipSignature(members []profileMember, centers []*profileCenter) string {
	centerRep := make(map[int]uint, len(centers))
	for k, c := range centers {
		if c == nil || len(c.members) == 0 {
			continue
		}
		min := ^uint(0)
		for _, i := range c.members {
			if members[i].faceID < min {
				min = members[i].faceID
			}
		}
		centerRep[k] = min
	}
	type kv struct {
		id  uint
		rep uint
	}
	pairs := make([]kv, 0, len(members))
	for i := range members {
		if members[i].vec == nil {
			continue
		}
		rep := uint(0)
		if members[i].centerIdx >= 0 {
			rep = centerRep[members[i].centerIdx]
		}
		pairs = append(pairs, kv{members[i].faceID, rep})
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].id < pairs[j].id })
	var buf []byte
	for _, p := range pairs {
		buf = appendUint(buf, uint64(p.id))
		buf = append(buf, ':')
		buf = appendUint(buf, uint64(p.rep))
		buf = append(buf, ',')
	}
	return string(buf)
}

func appendUint(buf []byte, v uint64) []byte {
	if v == 0 {
		return append(buf, '0')
	}
	var tmp [20]byte
	n := len(tmp)
	for v > 0 {
		n--
		tmp[n] = byte('0' + v%10)
		v /= 10
	}
	return append(buf, tmp[n:]...)
}

// ---- 纯函数辅助 ----

// normalizeEmbedding 归一化向量，拒绝空、零范数、NaN、Inf。
func normalizeEmbedding(vec []float32) ([]float32, bool) {
	if len(vec) == 0 {
		return nil, false
	}
	var sum float64
	for _, v := range vec {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false
		}
		sum += f * f
	}
	if sum == 0 || math.IsNaN(sum) || math.IsInf(sum, 0) {
		return nil, false
	}
	n := math.Sqrt(sum)
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = float32(float64(v) / n)
	}
	return out, true
}

// cosineSimilarity 已在 people_service.go 中定义（同包复用）。
// 对归一化向量等价于点积；维度不一致或空向量返回 -1。

// faceWeight 计算人脸权重。
//   - 人工确认：返回 ipWeightMax（最高可信度）。
//   - 自动归属：综合质量与聚类分数，上限 ipAutoWeightCeil（< 人工权重）。
//   - NaN/Inf/负值的 quality/score 被拒绝（返回 ok=false）。
func faceWeight(manual bool, quality, score float64) (float64, bool) {
	if manual {
		return ipWeightMax, true
	}
	if math.IsNaN(quality) || math.IsInf(quality, 0) || quality < 0 ||
		math.IsNaN(score) || math.IsInf(score, 0) || score < 0 {
		return 0, false
	}
	q := math.Min(quality, 1)
	s := math.Min(score, 1)
	w := ipWeightMin + (ipAutoWeightCeil-ipWeightMin)*math.Sqrt(q*s)
	return w, true
}

// weightedCentroid 计算成员的加权质心。
//   - 先归一化单脸向量（调用方保证）。
//   - weightedSum 保存原始加权和。
//   - centroid 为加权和归一化结果。
//   - 维度不一致返回 ok=false。
//   - 加权和为零时不生成中心。
//   - 按稳定 Face ID 顺序累加，保证浮点结果可重复。
func weightedCentroid(members []profileMember) (centroid, weightedSum []float32, totalWeight float64, ok bool) {
	if len(members) == 0 {
		return nil, nil, 0, false
	}
	idxs := make([]int, len(members))
	for i := range idxs {
		idxs[i] = i
	}
	sort.SliceStable(idxs, func(a, c int) bool {
		return members[idxs[a]].faceID < members[idxs[c]].faceID
	})
	dim := len(members[idxs[0]].vec)
	if dim == 0 {
		return nil, nil, 0, false
	}
	sum := make([]float64, dim)
	for _, i := range idxs {
		m := members[i]
		if len(m.vec) != dim {
			return nil, nil, 0, false
		}
		w := m.weight
		if w < 0 || math.IsNaN(w) || math.IsInf(w, 0) {
			return nil, nil, 0, false
		}
		for j, v := range m.vec {
			sum[j] += float64(v) * w
		}
		totalWeight += w
	}
	if totalWeight == 0 || math.IsNaN(totalWeight) || math.IsInf(totalWeight, 0) {
		return nil, nil, 0, false
	}
	var norm float64
	weightedSum = make([]float32, dim)
	for j, s := range sum {
		weightedSum[j] = float32(s)
		norm += s * s
	}
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, nil, 0, false
	}
	n := math.Sqrt(norm)
	centroid = make([]float32, dim)
	for j, s := range sum {
		centroid[j] = float32(s / n)
	}
	return centroid, weightedSum, totalWeight, true
}

// centerMedoid 选择与质心余弦相似度最高的真实人脸。
// 同分时使用最小 Face ID，保证确定性。
func centerMedoid(centroid []float32, members []profileMember) uint {
	if len(members) == 0 || len(centroid) == 0 {
		return 0
	}
	sorted := make([]profileMember, len(members))
	copy(sorted, members)
	sort.SliceStable(sorted, func(a, c int) bool {
		return sorted[a].faceID < sorted[c].faceID
	})
	bestSim := -2.0
	var bestID uint
	found := false
	for _, m := range sorted {
		if len(m.vec) == 0 {
			continue
		}
		sim := cosineSimilarity(centroid, m.vec)
		if !found || sim > bestSim {
			bestSim = sim
			bestID = m.faceID
			found = true
		}
	}
	if !found {
		return 0
	}
	return bestID
}

// percentileSimilarity 以 nearest-rank 规则计算分位数。
//   - 不修改原 slice。
//   - 空输入（或过滤后为空）返回 0。
//   - 拒绝 NaN/Inf（过滤后计算）。
//   - p 被钳制到 [0,100]。
func percentileSimilarity(values []float64, p float64) float64 {
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
