package service

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/coder/hnsw"
	"github.com/davidhoo/relive/internal/model"
)

// identityProfileANNM 是身份画像中心 HNSW 图每个节点的最大邻居数。
// 独立命名，避免与现有 person_merge_suggestion_ann.go 的 annHNSWM 冲突。
const identityProfileANNM = 16

// identityProfileANNEfSearch 是身份画像中心 HNSW 查询的搜索束宽。
const identityProfileANNEfSearch = 200

// identityProfileANNDeltaMax 是 delta 增量索引的内部上限。达到上限后不再继续无界增长，
// 而是标记 ANN 不可用并请求完整重建（fail-closed）。
const identityProfileANNDeltaMax = 256

// profileCenterVector 是单个身份中心的解码向量及其归属元数据。
type profileCenterVector struct {
	CenterID   uint
	PersonID   uint
	Generation int
	Embedding  []float32
}

// identityCenterIndex 是一次完整 snapshot 的不可变 HNSW 索引及其元数据。
// 发布后不得修改其 graph 与 map；查询只读访问。
type identityCenterIndex struct {
	graph       *hnsw.Graph[uint] // key = center ID
	centerOwner map[uint]uint     // center ID → person ID
	generation  map[uint]int      // center ID → generation
	model       string            // 构建时的 embedding 模型签名
	dim         int               // 向量维度（0 表示空 snapshot）
}

// identityProfileANN 维护人物多中心身份画像的可并发查询、可原子替换的 ANN 缓存。
//
// snapshot 承载最近一次完整索引（不可变，通过 atomic.Pointer 原子切换）；
// delta 承载 snapshot 之后激活的新中心；invalid 屏蔽旧 generation 与已删除人物的中心；
// activeGeneration 记录每个人物的当前活动 generation，用于过滤被新 generation 取代的旧中心。
//
// 任何不可证明完整的状态都返回 ready=false（Search 的第二个返回值为 false），
// 由调用方走精确回退。ANN 是派生缓存，数据库活动 generation 始终是权威数据。
type identityProfileANN struct {
	// model 是构造时绑定的 embedding 模型签名；Rebuild/Search 收到不一致签名时拒绝。
	model string

	snapshot atomic.Pointer[identityCenterIndex]

	// coder/hnsw 的 Search 不是并发安全的，snapshot 查询必须串行化。
	searchMu sync.Mutex

	deltaMu          sync.RWMutex
	delta            map[uint]profileCenterVector // center ID → 新中心向量（snapshot 之后激活）
	invalid          map[uint]struct{}            // center ID → 显式屏蔽（删除/失效）
	activeGeneration map[uint]int                 // person ID → 当前活动 generation
	revision         uint64                       // delta 变更计数，用于重建协调

	deltaMax int // delta 上限，默认 identityProfileANNDeltaMax

	unavailable      atomic.Bool
	rebuildRequested atomic.Bool

	// snapshotGeneration 在每次完整 snapshot 成功发布后加 1。只读 Stats 通过 atomic load 读取，
	// 用于监控 snapshot 是否推进。以下情况不得增加：构建失败、模型不匹配、输入校验失败、
	// 只更新 delta、只调用 RequestRebuild、InvalidatePerson、InvalidateAll。
	snapshotGeneration atomic.Uint64

	// buildHook 仅供测试注入“构建期间”的并发激活，以验证 preserve 语义；生产中始终为 nil。
	buildHook func()
}

// newIdentityProfileANN 构造绑定到指定 embedding 模型签名的 ANN 组件。
// 调用方负责在非 legacy 模式下首次构建前将 rebuildRequested 置为 true。
func newIdentityProfileANN(model string) *identityProfileANN {
	return &identityProfileANN{
		model:            model,
		delta:            make(map[uint]profileCenterVector),
		invalid:          make(map[uint]struct{}),
		activeGeneration: make(map[uint]int),
		deltaMax:         identityProfileANNDeltaMax,
	}
}

// Search 查询与 query 最相似的 k 个候选人物 ID，按相似度稳定排序（距离升序，
// 相同距离以 person_id ASC 打破平局）。第二个返回值为 ready 标志：false 表示状态
// 不可证明完整，调用方必须走精确回退。
//
// 查询流程：
//  1. 原子读取 snapshot；缺失返回 ready=false。
//  2. 校验请求模型签名与 snapshot 模型签名；不一致返回 ready=false。
//  3. 在短暂 RLock 下复制 delta/invalid/activeGeneration 元数据，随后释放锁。
//  4. 在 searchMu 下调用 HNSW Search（coder/hnsw Search 非并发安全）。
//  5. 对 delta 做精确 cosine 计算。
//  6. 合并两路结果，过滤 invalid、非活动 generation 与无效 owner，按人物去重并稳定排序。
func (a *identityProfileANN) Search(query []float32, k int, model string) ([]uint, bool) {
	if a.unavailable.Load() {
		return nil, false
	}
	if k <= 0 {
		return nil, false
	}
	if !validVector(query) {
		return nil, false
	}

	snap := a.snapshot.Load()
	if snap == nil {
		return nil, false
	}
	if snap.model != model {
		return nil, false
	}
	if snap.dim > 0 && len(query) != snap.dim {
		return nil, false
	}

	// 短暂 RLock 复制 delta/invalid/activeGeneration，避免在持锁时执行 HNSW Search
	// 或遍历大向量集合。
	a.deltaMu.RLock()
	if a.unavailable.Load() {
		a.deltaMu.RUnlock()
		return nil, false
	}
	deltaCopy := make(map[uint]profileCenterVector, len(a.delta))
	for id, v := range a.delta {
		deltaCopy[id] = v
	}
	invalidCopy := make(map[uint]struct{}, len(a.invalid))
	for id := range a.invalid {
		invalidCopy[id] = struct{}{}
	}
	activeGen := make(map[uint]int, len(a.activeGeneration))
	for pid, g := range a.activeGeneration {
		activeGen[pid] = g
	}
	a.deltaMu.RUnlock()

	type cand struct {
		personID uint
		dist     float32
	}
	seen := make(map[uint]struct{})
	cands := make([]cand, 0, k)

	// 4. HNSW Search（串行化）。
	if snap.graph != nil && snap.graph.Len() > 0 {
		a.searchMu.Lock()
		nodes := snap.graph.Search(query, k)
		a.searchMu.Unlock()
		for _, n := range nodes {
			centerID := n.Key
			if _, bad := invalidCopy[centerID]; bad {
				continue
			}
			personID, ok := snap.centerOwner[centerID]
			if !ok || personID == 0 {
				continue
			}
			if active, ok := activeGen[personID]; ok && snap.generation[centerID] != active {
				continue // 旧 generation 中心被新 generation 取代
			}
			if _, dup := seen[personID]; dup {
				continue
			}
			cands = append(cands, cand{personID: personID, dist: hnsw.CosineDistance(query, n.Value)})
			seen[personID] = struct{}{}
		}
	}

	// 5. delta 精确 cosine 计算。
	for id, v := range deltaCopy {
		if _, bad := invalidCopy[id]; bad {
			continue
		}
		if v.PersonID == 0 {
			continue
		}
		if active, ok := activeGen[v.PersonID]; ok && v.Generation != active {
			continue // delta 中被再次替换的旧中心
		}
		if _, dup := seen[v.PersonID]; dup {
			continue
		}
		if len(v.Embedding) != len(query) {
			continue
		}
		cands = append(cands, cand{personID: v.PersonID, dist: hnsw.CosineDistance(query, v.Embedding)})
		seen[v.PersonID] = struct{}{}
	}

	// 6. 稳定排序：距离升序，相同距离以 person_id ASC 打破平局。
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].personID < cands[j].personID
	})

	if k < len(cands) {
		cands = cands[:k]
	}
	out := make([]uint, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.personID)
	}
	return out, true
}

// Rebuild 从给定中心集合构建完整 snapshot 并原子切换。构建（BLOB 解码、校验、建图）
// 在锁外完成；只有所有节点与元数据校验通过后才允许切换 snapshot。
// 构建失败不发布半成品，旧 snapshot 保留供诊断，但对外 ready=false（由 unavailable 标记）。
func (a *identityProfileANN) Rebuild(centers []*model.PersonIdentityCenter, model string) error {
	if model != a.model {
		return errANNModelMismatch
	}

	// 完整重建开始时记录 revision（构建在锁外进行，期间 Activate 可能变更 delta）。
	a.deltaMu.RLock()
	startRev := a.revision
	a.deltaMu.RUnlock()

	// buildHook 仅供测试注入“构建期间”的并发激活，以确定性地验证 preserve 语义；
	// 生产中始终为 nil。
	if a.buildHook != nil {
		a.buildHook()
	}

	// 构建在锁外完成：BLOB 解码、校验、HNSW 建图。
	idx, err := a.buildIndex(centers, model)
	if err != nil {
		// 构建失败：不发布半成品，标记不可用并请求重建。旧 snapshot 保留但不对外声称可用。
		a.unavailable.Store(true)
		a.rebuildRequested.Store(true)
		return err
	}

	// 短临界区：原子切换 snapshot 并处理 delta/invalid。
	// snapshot.Store 与 delta 清理由同一 deltaMu.Lock 串行化，使读者在 RLock 下看到
	// 一致的 (snapshot, delta) 组合：要么完整旧 snapshot + 旧 delta，要么完整新 snapshot + 清空后的 delta。
	a.deltaMu.Lock()
	a.snapshot.Store(idx)
	if a.revision == startRev {
		// 构建期间无 delta 变更：snapshot 已覆盖全部 delta/invalid，安全清空。
		a.delta = make(map[uint]profileCenterVector)
		a.invalid = make(map[uint]struct{})
		a.activeGeneration = deriveActiveGeneration(idx)
	}
	// 构建期间有 delta 变更：保留 delta/invalid/activeGeneration（含更新数据），下一次干净重建再压缩。
	a.unavailable.Store(false)
	a.rebuildRequested.Store(false)
	// 完整 snapshot 成功发布，推进内部 generation（仅在成功发布后增加）。
	a.snapshotGeneration.Add(1)
	a.deltaMu.Unlock()

	return nil
}

// Activate 在 snapshot 之后为 personID 激活新 generation 的中心。无需完整重建即可被查询。
// 同一人物旧 generation 的中心（snapshot 或 delta 中）立即失效：通过 activeGeneration 过滤。
// delta 达到上限时拒绝继续增长，标记不可用并请求重建（fail-closed）。
func (a *identityProfileANN) Activate(personID uint, generation int, centers []*model.PersonIdentityCenter) error {
	if personID == 0 {
		return errANNInvalidCenter
	}
	if generation <= 0 {
		return errANNInvalidCenter
	}

	// 锁外解码并校验所有中心。
	vecs := make([]profileCenterVector, 0, len(centers))
	seen := make(map[uint]struct{})
	for _, c := range centers {
		if c == nil {
			return errANNInvalidCenter
		}
		if c.ID == 0 || c.PersonID == 0 || c.PersonID != personID {
			return errANNInvalidCenter
		}
		if c.Generation != generation {
			return errANNInvalidCenter
		}
		if _, dup := seen[c.ID]; dup {
			return errANNInvalidCenter
		}
		emb := model.DecodeEmbedding(c.CentroidEmbedding)
		if !validVector(emb) {
			return errANNInvalidCenter
		}
		seen[c.ID] = struct{}{}
		vecs = append(vecs, profileCenterVector{
			CenterID:   c.ID,
			PersonID:   personID,
			Generation: generation,
			Embedding:  emb,
		})
	}

	a.deltaMu.Lock()
	defer a.deltaMu.Unlock()

	// 移除该人物在 delta 中的旧中心（被新 generation 取代，不再需要）。
	for id, v := range a.delta {
		if v.PersonID == personID {
			delete(a.delta, id)
		}
	}

	// 容量检查：不允许无界增长。
	if len(a.delta)+len(vecs) > a.deltaMax {
		a.unavailable.Store(true)
		a.rebuildRequested.Store(true)
		return errANNDeltaFull
	}

	for _, v := range vecs {
		a.delta[v.CenterID] = v
	}
	a.activeGeneration[personID] = generation
	a.revision++

	return nil
}

// InvalidatePerson 屏蔽指定人物的全部中心（snapshot 与 delta），用于人物删除。
// 不删除 snapshot（不可变），而是将其中心 ID 加入 invalid 集合；delta 中的旧条目一并移除。
func (a *identityProfileANN) InvalidatePerson(personID uint) {
	if personID == 0 {
		return
	}
	a.deltaMu.Lock()
	defer a.deltaMu.Unlock()

	if snap := a.snapshot.Load(); snap != nil {
		for centerID, pid := range snap.centerOwner {
			if pid == personID {
				a.invalid[centerID] = struct{}{}
			}
		}
	}
	for id, v := range a.delta {
		if v.PersonID == personID {
			a.invalid[id] = struct{}{}
			delete(a.delta, id)
		}
	}
	delete(a.activeGeneration, personID)
}

// InvalidateAll 使整个 ANN snapshot 不可查询，用于 ResetAllPeople 等清空全部派生画像的操作。
//
// 行为：
//   - snapshot 不再 ready：先标记 unavailable，再清空 delta/invalid/activeGeneration，
//     并请求未来完整重建。
//   - 并发 Search 只能看到旧完整 snapshot（ready=true）或 unavailable（ready=false），
//     不能看到半清空状态：清空在 deltaMu.Lock 下完成；Search 在 RLock 下读取元数据后
//     释放锁，若期间 unavailable 被置位则 Search 在持锁检查时直接返回 false。
//   - 不删除 snapshot 指针本身（保留供诊断/旧 generation 查询），但通过 unavailable 标志
//     使其对外声称不可用。
func (a *identityProfileANN) InvalidateAll() {
	// 先标记 unavailable，使后续/并发 Search 在 RLock 下读到 unavailable=true 时直接 fail closed。
	a.unavailable.Store(true)
	a.rebuildRequested.Store(true)

	a.deltaMu.Lock()
	a.delta = make(map[uint]profileCenterVector)
	a.invalid = make(map[uint]struct{})
	a.activeGeneration = make(map[uint]int)
	a.revision++
	a.deltaMu.Unlock()
}

// RequestRebuild 标记需要完整重建。
func (a *identityProfileANN) RequestRebuild() {
	a.rebuildRequested.Store(true)
}

// RebuildRequested 返回是否需要完整重建（供后台切片轮询）。
func (a *identityProfileANN) RebuildRequested() bool {
	return a.rebuildRequested.Load()
}

// Ready 返回当前 ANN 是否可对外提供查询。仅供诊断/测试；查询应直接使用 Search 的返回值。
func (a *identityProfileANN) Ready(model string) bool {
	if a.unavailable.Load() {
		return false
	}
	snap := a.snapshot.Load()
	if snap == nil {
		return false
	}
	return snap.model == model
}

// IdentityANNStatsSnapshot 是 ANN 内部运行快照的只读视图，由 Stats 填充。
// 不含 embedding、向量或路径，仅聚合计数与状态标志。
type IdentityANNStatsSnapshot struct {
	Ready             bool
	Generation        uint64
	SnapshotNodes     int
	DeltaNodes        int
	InvalidNodes      int
	DeltaMax          int
	ActiveGenerations int
	RebuildRequested  bool
	Unavailable       bool
}

// annBuildStatus* 是脱敏的最近一次构建状态类别，用于 stats 与日志。
const (
	annBuildStatusNever   = "never"
	annBuildStatusSuccess = "success"
	annBuildStatusFailed  = "failed"
)

// Stats 返回 ANN 的只读、线程安全运行快照。snapshot 通过 atomic load 读取；
// delta/invalid/activeGeneration 在短 RLock 下读取长度，不遍历或复制 embedding，
// 不调用 HNSW Search，不触发 rebuild。nil ANN 返回零值且 Ready=false；模型不匹配时
// Ready=false（其余计数仍来自 snapshot）。
func (a *identityProfileANN) Stats(model string) IdentityANNStatsSnapshot {
	if a == nil {
		return IdentityANNStatsSnapshot{}
	}
	out := IdentityANNStatsSnapshot{
		Unavailable:      a.unavailable.Load(),
		RebuildRequested: a.rebuildRequested.Load(),
		Generation:       a.snapshotGeneration.Load(),
	}

	snap := a.snapshot.Load()
	if snap != nil {
		if snap.graph != nil {
			out.SnapshotNodes = snap.graph.Len()
		}
		out.Ready = !out.Unavailable && snap.model == model
	}

	a.deltaMu.RLock()
	out.DeltaNodes = len(a.delta)
	out.InvalidNodes = len(a.invalid)
	out.ActiveGenerations = len(a.activeGeneration)
	out.DeltaMax = a.deltaMax
	a.deltaMu.RUnlock()

	return out
}

// buildIndex 在锁外解码、校验中心并构建 HNSW 图。返回不可变 identityCenterIndex。
// 校验失败（重复/零 ID、非法 generation、解码失败、维度不一致、NaN/Inf、零范数）返回错误。
func (a *identityProfileANN) buildIndex(centers []*model.PersonIdentityCenter, embeddingModel string) (*identityCenterIndex, error) {
	if embeddingModel != a.model {
		return nil, errANNModelMismatch
	}

	var dim int
	nodes := make([]hnsw.Node[uint], 0, len(centers))
	centerOwner := make(map[uint]uint, len(centers))
	generation := make(map[uint]int, len(centers))
	seen := make(map[uint]struct{})

	for _, c := range centers {
		if c == nil {
			return nil, errors.New("identity profile ANN: nil center")
		}
		if c.ID == 0 {
			return nil, errors.New("identity profile ANN: zero center id")
		}
		if c.PersonID == 0 {
			return nil, errors.New("identity profile ANN: zero person id")
		}
		if c.Generation <= 0 {
			return nil, fmt.Errorf("identity profile ANN: invalid generation %d for center %d", c.Generation, c.ID)
		}
		if _, dup := seen[c.ID]; dup {
			return nil, fmt.Errorf("identity profile ANN: duplicate center id %d", c.ID)
		}
		emb := model.DecodeEmbedding(c.CentroidEmbedding)
		if emb == nil {
			return nil, fmt.Errorf("identity profile ANN: center %d embedding decode failed", c.ID)
		}
		if !validVector(emb) {
			return nil, fmt.Errorf("identity profile ANN: center %d invalid embedding (NaN/Inf/zero-norm)", c.ID)
		}
		if dim == 0 {
			dim = len(emb)
		}
		if len(emb) != dim {
			return nil, fmt.Errorf("identity profile ANN: center %d dim mismatch (%d != %d)", c.ID, len(emb), dim)
		}
		seen[c.ID] = struct{}{}
		nodes = append(nodes, hnsw.MakeNode(c.ID, emb))
		centerOwner[c.ID] = c.PersonID
		generation[c.ID] = c.Generation
	}

	g := hnsw.NewGraph[uint]()
	g.Distance = hnsw.CosineDistance
	g.M = identityProfileANNM
	g.EfSearch = identityProfileANNEfSearch
	if len(nodes) > 0 {
		g.Add(nodes...)
	}

	return &identityCenterIndex{
		graph:       g,
		centerOwner: centerOwner,
		generation:  generation,
		model:       embeddingModel,
		dim:         dim,
	}, nil
}

// deriveActiveGeneration 从 snapshot 索引派生 person ID → 活动 generation 映射。
// snapshot 中每个人物的所有中心共享同一活动 generation。
func deriveActiveGeneration(idx *identityCenterIndex) map[uint]int {
	if idx == nil {
		return make(map[uint]int)
	}
	out := make(map[uint]int, len(idx.centerOwner))
	for centerID, personID := range idx.centerOwner {
		out[personID] = idx.generation[centerID]
	}
	return out
}

// validVector 校验向量非空、无 NaN/Inf、范数非零。
func validVector(v []float32) bool {
	if len(v) == 0 {
		return false
	}
	var sum float64
	for _, x := range v {
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
		sum += f * f
	}
	return sum > 0
}

// ANN 错误哨兵。
var (
	errANNModelMismatch = errors.New("identity profile ANN: embedding model mismatch")
	errANNInvalidCenter = errors.New("identity profile ANN: invalid center input")
	errANNDeltaFull     = errors.New("identity profile ANN: delta full, rebuild requested")
)
