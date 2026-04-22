package service

import (
	"fmt"
	"time"

	"github.com/coder/hnsw"
)

const (
	annSearchK         = 50             // neighbors per prototype query
	annHNSWM           = 8              // max neighbors per node; 8 halves build time vs 16 with negligible recall loss at this scale
	annHNSWEfSearch    = 100            // search beam width; high value ensures recall near threshold boundary
	annRebuildCooldown = 30 * time.Minute // min interval between ANN rebuilds triggered by MarkDirty
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

	allPeople, err := s.personRepo.ListAll()
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

	protoFaces, err := s.faceRepo.ListTopByPersonIDs(personIDs, peoplePrototypeCandidates)
	if err != nil {
		return nil, fmt.Errorf("buildANNIndex: list prototype faces: %w", err)
	}

	protosByPerson := selectPersonPrototypesStatic(protoFaces, peoplePrototypeCount)

	personProtos := make(map[uint][]faceWithEmbedding, len(protosByPerson))
	for personID, faces := range protosByPerson {
		personProtos[personID] = decodeFacesWithEmbeddings(faces)
	}

	g := newHNSWGraph()
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
	if len(nodes) > 0 {
		// Use a lower efSearch during construction to reduce CPU cost.
		// EfSearch controls beam width for both build and query; the library default
		// is 20. We only need high recall at query time, so we build with 20 and
		// restore annHNSWEfSearch (100) afterward.
		g.EfSearch = 20
		g.Add(nodes...)
		g.EfSearch = annHNSWEfSearch
	}

	idx := &annIndex{
		graph:        g,
		faceOwner:    faceOwner,
		personProtos: personProtos,
	}

	s.appendANNBuildLog(len(personIDs), len(nodes), time.Since(start))
	return idx, nil
}

// ensureANNIndex returns the cached index, building it if necessary.
// If the index is dirty but was built recently (within annRebuildCooldown),
// the slightly stale index is returned to avoid thrashing during active detection.
// Thread-safe: may be called concurrently with FindCandidates and MarkDirty.
func (s *personMergeSuggestionService) ensureANNIndex() (*annIndex, error) {
	s.annMu.Lock()
	if s.annIdx != nil {
		if !s.annDirty {
			idx := s.annIdx
			s.annMu.Unlock()
			return idx, nil
		}
		if time.Since(s.annBuiltAt) < annRebuildCooldown {
			idx := s.annIdx
			s.annMu.Unlock()
			return idx, nil // dirty but within cooldown; use stale index
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
