package service

import (
	"fmt"
	"time"

	"github.com/coder/hnsw"
)

const (
	annSearchK      = 50  // neighbors per prototype query
	annHNSWM        = 8   // max neighbors per node; 8 halves build time vs 16 with negligible recall loss at this scale
	annHNSWEfSearch = 100 // search beam width; high value ensures recall near threshold boundary
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

	protoFaces, err := bgFaceRepo.ListPrototypeEmbeddings(personIDs, peoplePrototypeCount)
	if err != nil {
		return nil, fmt.Errorf("buildANNIndex: list prototype faces: %w", err)
	}

	personProtos := make(map[uint][]faceWithEmbedding, len(personIDs))
	for _, f := range protoFaces {
		if f == nil || f.PersonID == nil {
			continue
		}
		emb := decodeEmbedding(f.Embedding)
		personProtos[*f.PersonID] = append(personProtos[*f.PersonID], faceWithEmbedding{
			face:      f,
			embedding: emb,
		})
	}
	// Pre-compute norms
	for pid, protos := range personProtos {
		for i := range protos {
			if protos[i].embedding != nil {
				protos[i].norm = calculateNorm(protos[i].embedding)
			}
		}
		personProtos[pid] = protos
	}

	g := newHNSWGraph()
	// Use lower efSearch during construction for speed; the graph quality is
	// primarily determined by M=8. Restore to annHNSWEfSearch before returning
	// so search queries use full beam width.
	g.EfSearch = 20
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
// Rebuild only happens when: (1) no index exists yet (first call after restart), or
// (2) index is dirty AND current time is within the configured rebuild window AND
// the index was not already built during this window cycle.
// Thread-safe: may be called concurrently with FindCandidates and MarkDirty.
func (s *personMergeSuggestionService) ensureANNIndex() (*annIndex, error) {
	s.annMu.Lock()
	if s.annIdx != nil {
		if !s.annDirty {
			idx := s.annIdx
			s.annMu.Unlock()
			return idx, nil
		}
		if !s.withinRebuildWindow() || s.alreadyBuiltThisWindow() {
			idx := s.annIdx
			s.annMu.Unlock()
			return idx, nil // dirty but outside window or already built this window; use stale index
		}
	}
	s.annMu.Unlock()

	// Build outside the lock — this is a long DB + CPU operation.
	// Two concurrent callers may both build; the second write is benign.
	idx, err := s.buildANNIndex()
	if err != nil {
		return nil, err
	}

	s.annMu.Lock()
	s.annIdx = idx
	s.annDirty = false
	s.annBuiltAt = time.Now()
	s.annMu.Unlock()
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

// withinRebuildWindow returns true if the current hour is within the configured
// ANN rebuild window. Must be called with annMu held.
func (s *personMergeSuggestionService) withinRebuildWindow() bool {
	start, end := s.annRebuildWindow()
	hour := time.Now().Hour()
	return hour >= start && hour < end
}

// alreadyBuiltThisWindow returns true if the ANN index was already built during
// the current rebuild window cycle. Must be called with annMu held.
func (s *personMergeSuggestionService) alreadyBuiltThisWindow() bool {
	if s.annBuiltAt.IsZero() {
		return false
	}
	start, end := s.annRebuildWindow()
	now := time.Now()
	windowStart := time.Date(now.Year(), now.Month(), now.Day(), start, 0, 0, 0, now.Location())
	return !s.annBuiltAt.Before(windowStart) && s.annBuiltAt.Hour() < end
}

func (s *personMergeSuggestionService) annRebuildWindow() (start, end int) {
	if s.config != nil && s.config.People.ANNRebuildWindowStart >= 0 && s.config.People.ANNRebuildWindowEnd > 0 {
		return s.config.People.ANNRebuildWindowStart, s.config.People.ANNRebuildWindowEnd
	}
	return 2, 5
}
