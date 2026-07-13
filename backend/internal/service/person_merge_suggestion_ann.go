package service

import (
	"fmt"
	"time"

	"github.com/coder/hnsw"
)

const (
	annSearchK      = 200 // neighbors per prototype query; 72K+ person pool needs wider net than 50
	annHNSWM        = 16  // max neighbors per node; 16 gives better recall at 60K+ scale vs 8
	annHNSWEfSearch = 200 // search beam width; must be >= annSearchK
)

// annIndex is a cached HNSW nearest-neighbor index over all person prototype embeddings.
// It is NOT thread-safe; Search calls must be serialized by the caller.
type annIndex struct {
	graph        *hnsw.Graph[uint]            // key = face ID
	faceOwner    map[uint]uint                // face ID → person ID
	personProtos map[uint][]faceWithEmbedding // person ID → decoded prototype embeddings
}

// annCandidates queries the HNSW index with each of targetProtos and returns the union
// of candidate person IDs, excluding targetPersonID itself.
func (idx *annIndex) annCandidates(targetPersonID uint, targetProtos []faceWithEmbedding, k int) map[uint]struct{} {
	candidates := make(map[uint]struct{})
	for _, proto := range targetProtos {
		if proto.embedding == nil {
			continue
		}
		neighbors := idx.graph.Search(proto.embedding, k)
		for _, n := range neighbors {
			personID := idx.faceOwner[n.Key]
			if personID == 0 || personID == targetPersonID {
				continue
			}
			candidates[personID] = struct{}{}
		}
	}
	return candidates
}

// buildANNIndex loads all person prototype embeddings from the database and builds
// a fresh HNSW index. This is the expensive operation; results are cached in s.annIdx.
func (s *personMergeSuggestionService) buildANNIndex() (*annIndex, error) {
	start := time.Now()

	bgPersonRepo, bgFaceRepo, _, _ := s.bgRepos()

	allPeople, err := bgPersonRepo.ListAll()
	if err != nil {
		return nil, fmt.Errorf("buildANNIndex: list people: %w", err)
	}

	personIDs := make([]uint, 0, len(allPeople))
	for _, p := range allPeople {
		if p != nil && p.FaceCount > 0 {
			personIDs = append(personIDs, p.ID)
		}
	}
	if len(personIDs) == 0 {
		return &annIndex{
			graph:        newHNSWGraph(),
			faceOwner:    make(map[uint]uint),
			personProtos: make(map[uint][]faceWithEmbedding),
		}, nil
	}

	protoFaces, err := bgFaceRepo.ListPrototypeEmbeddings(personIDs, peoplePrototypeCandidates)
	if err != nil {
		return nil, fmt.Errorf("buildANNIndex: list prototype faces: %w", err)
	}

	personProtos := make(map[uint][]faceWithEmbedding, len(personIDs))
	for pid, faces := range selectPersonPrototypesStatic(protoFaces, peoplePrototypeCount) {
		personProtos[pid] = decodeFacesWithEmbeddings(faces)
	}

	// annBuildHook 仅供测试注入“构建期间”的并发 MarkDirty，以确定性验证 generation 协调：
	// 构建期间推进 annGeneration 后，本 build 完成时应保持 pending。生产中始终为 nil。
	if s.annBuildHook != nil {
		s.annBuildHook()
	}

	g := newHNSWGraph()
	// Use lower efSearch during construction for speed; graph quality is
	// primarily determined by M. Use 2*M as efConstruction per HNSW guidelines.
	// Restore to annHNSWEfSearch before returning so search queries use full beam width.
	g.EfSearch = 32
	faceOwner := make(map[uint]uint, len(personProtos)*peoplePrototypeCount)

	nodes := make([]hnsw.Node[uint], 0, len(personProtos)*peoplePrototypeCount)
	for personID, protos := range personProtos {
		for _, fw := range protos {
			if fw.embedding == nil || fw.face == nil {
				continue
			}
			nodes = append(nodes, hnsw.MakeNode(fw.face.ID, fw.embedding))
			faceOwner[fw.face.ID] = personID
		}
	}

	// Insert in batches with adaptive throttling: measure actual CPU time per
	// batch and sleep proportionally to maintain the target CPU duty cycle.
	// This prevents the HNSW build from pinning the CPU on NAS devices while
	// adding only ~2x overhead vs unchecked full-speed construction.
	batchSize := s.annBuildBatchSize()
	targetDuty := s.annBuildCPUDuty()
	for i := 0; i < len(nodes); i += batchSize {
		end := i + batchSize
		if end > len(nodes) {
			end = len(nodes)
		}
		batchStart := time.Now()
		g.Add(nodes[i:end]...)
		if end < len(nodes) && targetDuty < 1.0 {
			batchElapsed := time.Since(batchStart)
			// Dilate to target duty cycle: work / (work + sleep) = duty
			// → sleep = work * (1/duty - 1)
			sleepDuration := time.Duration(float64(batchElapsed) * (1/targetDuty - 1))
			if sleepDuration > 0 {
				time.Sleep(sleepDuration)
			}
		}
	}
	g.EfSearch = annHNSWEfSearch

	idx := &annIndex{
		graph:        g,
		faceOwner:    faceOwner,
		personProtos: personProtos,
	}

	s.appendANNBuildLog(len(personIDs), len(nodes), time.Since(start))
	return idx, nil
}

// ensureANNIndex returns the cached index, building it if necessary.
// Rebuild happens when: (1) no index exists yet (first call after restart), or
// (2) index is dirty (data changed since last build).
// CPU throttling (annBuildCPUDuty) prevents NAS CPU overload during rebuild.
//
// 单实例 + generation 协调：
//   - 同一时刻最多一个 rebuild 在进行（annBuilding）。已有 rebuild 运行时，后续调用者直接
//     返回当前（可能为旧/stale 的）索引或等待其结果，绝不启动第二个构建，避免重复 DB 读取与 HNSW 建图。
//   - rebuild 启动时记录 targetGeneration = 当前的 annGeneration。
//   - 构建成功后，仅当 targetGeneration == annGeneration（构建期间没有新的 MarkDirty）时才清除 dirty；
//     否则允许发布已完成索引，但保持 dirty（pending），下一次 ensureANNIndex 会再次重建。
//   - 构建失败：保留旧 annIdx 供查询降级使用，保持 dirty，进入现有后台冷却（不发布半成品）。
//
// 状态读取/翻转统一在 annMu 短临界区内完成，避免 check/build/publish 之间丢失更新。
func (s *personMergeSuggestionService) ensureANNIndex() (*annIndex, error) {
	s.annMu.Lock()
	// Clean & present: 直接返回缓存索引。
	if s.annIdx != nil && !s.annDirty && !s.annBuilding {
		idx := s.annIdx
		s.annMu.Unlock()
		return idx, nil
	}
	// 已有 rebuild 在进行：不启动第二个构建。在条件变量上等待其完成，避免返回 stale/nil。
	// 重复 DB 读取与建图被抑制；等待者复用第一个调用者的构建结果。
	if s.annBuilding {
		for s.annBuilding {
			s.annBuildCond.Wait()
		}
		// 构建已完成（成功或失败）：返回当前缓存的索引与状态。
		idx := s.annIdx
		dirty := s.annDirty
		s.annMu.Unlock()
		// 若构建失败 dirty 仍为 true，调用方拿到的是旧索引（可能 nil）。保持与首次构建失败
		// 一致的语义：返回旧索引 + 让调用方按需降级。不在此处重试，避免调用者线程递归构建。
		if dirty && idx == nil {
			return nil, nil
		}
		return idx, nil
	}
	// 没有 build 在跑且索引 dirty（或不存在）：由本调用者启动一次 rebuild。
	// 记录 target generation 并置位 building，然后释放锁执行耗时构建。
	targetGen := s.annGeneration
	s.annBuilding = true
	s.annMu.Unlock()

	idx, err := s.buildANNIndex()

	s.annMu.Lock()
	s.annBuilding = false
	if err != nil {
		// 构建失败：保留旧 annIdx（继续服务降级查询），保持 dirty，进入现有后台冷却。
		// 不发布半成品、不清除 dirty。annGeneration 不回退——新的 MarkDirty 仍可推进。
		oldIdx := s.annIdx
		// 唤醒所有等待者：它们将复用旧索引（或 nil 降级），不会递归启动新构建。
		s.annBuildCond.Broadcast()
		s.annMu.Unlock()
		s.appendANNBuildFailureLog(err, targetGen)
		return oldIdx, err
	}
	// 构建成功：发布新索引。仅当构建期间没有新的 MarkDirty（targetGen 仍是最新）时清除 dirty。
	// 否则保持 dirty（pending），下一次 ensureANNIndex 会重建到最新 generation。
	pending := s.annGeneration != targetGen
	s.annIdx = idx
	s.annBuiltAt = time.Now()
	s.annDirty = pending
	// 唤醒所有等待者：它们拿到刚发布的索引。
	s.annBuildCond.Broadcast()
	s.annMu.Unlock()

	s.appendANNBuildGenerationLog(targetGen, s.annGeneration, pending)
	return idx, nil
}

// FindCandidates queries the cached ANN index for persons whose prototype embeddings
// are nearest to probes. Returns nil if the index has not been built yet
// (caller should fall back to a full scan). Thread-safe.
func (s *personMergeSuggestionService) FindCandidates(probes []faceWithEmbedding, k int) map[uint]struct{} {
	s.annMu.Lock()
	defer s.annMu.Unlock()
	if s.annIdx == nil {
		return nil
	}
	return s.annIdx.annCandidates(0, probes, k)
}

func newHNSWGraph() *hnsw.Graph[uint] {
	g := hnsw.NewGraph[uint]()
	g.Distance = hnsw.CosineDistance
	g.M = annHNSWM
	g.EfSearch = annHNSWEfSearch
	return g
}

func (s *personMergeSuggestionService) appendANNBuildLog(persons, nodes int, elapsed time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendBackgroundLogLocked(
		fmt.Sprintf("ANN 索引重建完成：%d 人物 / %d 面部向量 / 耗时 %s", persons, nodes, elapsed.Round(time.Millisecond)),
	)
}

// appendANNBuildGenerationLog 记录带 generation 的构建完成状态。不输出人物 ID 或 embedding。
// pending=true 表示构建期间又有新的 MarkDirty 推进了 generation，已完成索引已发布但 dirty 保持。
func (s *personMergeSuggestionService) appendANNBuildGenerationLog(targetGen, currentGen uint64, pending bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendBackgroundLogLocked(
		fmt.Sprintf("ANN rebuild 完成: targetGeneration=%d currentGeneration=%d pending=%t", targetGen, currentGen, pending),
	)
}

// appendANNBuildFailureLog 记录构建失败。保留旧索引继续服务，保持 dirty 进入后台冷却。
func (s *personMergeSuggestionService) appendANNBuildFailureLog(err error, targetGen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendBackgroundLogLocked(
		fmt.Sprintf("ANN rebuild 失败: targetGeneration=%d 保留旧索引, err=%v", targetGen, err),
	)
}

func (s *personMergeSuggestionService) annBuildBatchSize() int {
	if s.config != nil && s.config.People.ANNBuildBatchSize > 0 {
		return s.config.People.ANNBuildBatchSize
	}
	return 100
}

func (s *personMergeSuggestionService) annBuildCPUDuty() float64 {
	if s.config != nil && s.config.People.ANNBuildCPUDuty > 0 {
		return s.config.People.ANNBuildCPUDuty
	}
	return 0.5
}
