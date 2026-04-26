package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ann512 produces a 512-dim unit vector with a spike at dimension idx.
func ann512(idx int, scale float32) []float32 {
	v := make([]float32, 512)
	if idx >= 0 && idx < 512 {
		v[idx] = scale
	}
	return v
}

func TestBuildANNIndex_PopulatesGraphAndLookups(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	p1 := createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0), ann512(1, 1.0))
	p2 := createSuggestionTestPerson(t, repos, "stranger", ann512(2, 1.0), ann512(3, 1.0))
	p3 := createSuggestionTestPerson(t, repos, "friend", ann512(4, 1.0), ann512(5, 1.0))

	idx, err := inner.buildANNIndex()
	require.NoError(t, err)

	// 3 persons × 2 faces = 6 nodes in the graph.
	assert.Equal(t, 6, idx.graph.Len())

	// personProtos should have entries for all three persons.
	assert.Len(t, idx.personProtos[p1.ID], 2)
	assert.Len(t, idx.personProtos[p2.ID], 2)
	assert.Len(t, idx.personProtos[p3.ID], 2)

	// faceOwner should map all 6 face IDs back to their person.
	ownedByP1, ownedByP2, ownedByP3 := 0, 0, 0
	for _, ownerID := range idx.faceOwner {
		switch ownerID {
		case p1.ID:
			ownedByP1++
		case p2.ID:
			ownedByP2++
		case p3.ID:
			ownedByP3++
		}
	}
	assert.Equal(t, 2, ownedByP1)
	assert.Equal(t, 2, ownedByP2)
	assert.Equal(t, 2, ownedByP3)
}

func TestANNCandidates_ReturnsSimilarPersons(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	target := createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))
	similar := createSuggestionTestPerson(t, repos, "stranger", ann512(0, 0.99))
	// orthogonal — should not appear
	_ = createSuggestionTestPerson(t, repos, "stranger", ann512(100, 1.0))

	idx, err := inner.buildANNIndex()
	require.NoError(t, err)

	targetProtos := idx.personProtos[target.ID]
	require.NotEmpty(t, targetProtos)

	candidates := idx.annCandidates(target.ID, targetProtos, 10)

	assert.Contains(t, candidates, similar.ID)
}

func TestANNCandidates_ExcludesSelf(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	target := createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))
	_ = createSuggestionTestPerson(t, repos, "stranger", ann512(0, 0.99))

	idx, err := inner.buildANNIndex()
	require.NoError(t, err)

	candidates := idx.annCandidates(target.ID, idx.personProtos[target.ID], 50)

	assert.NotContains(t, candidates, target.ID)
}

func TestEnsureANNIndex_CachesAcrossMultipleCalls(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	first, err := inner.ensureANNIndex()
	require.NoError(t, err)

	second, err := inner.ensureANNIndex()
	require.NoError(t, err)

	assert.Same(t, first, second)
}

func TestEnsureANNIndex_RebuildAfterInvalidation(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	first, err := inner.ensureANNIndex()
	require.NoError(t, err)

	inner.annIdx = nil

	second, err := inner.ensureANNIndex()
	require.NoError(t, err)

	assert.NotSame(t, first, second)
}

func TestMarkDirty_MarksANNDirtyWithoutDestroyingIndex(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	_, err := inner.ensureANNIndex()
	require.NoError(t, err)
	require.NotNil(t, inner.annIdx)

	require.NoError(t, svc.MarkDirty("test-invalidation"))

	// index is preserved (used during cooldown), only dirty flag is set
	assert.NotNil(t, inner.annIdx)
	assert.True(t, inner.annDirty)
}

func TestANNIndex_UsesStaleOutsideWindow(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, config.PeopleConfig{
		MergeSuggestionThreshold:       0.62,
		AttachThreshold:                1.10,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
		ANNRebuildWindowStart:          0,
		ANNRebuildWindowEnd:            24,
	})
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	first, err := inner.ensureANNIndex()
	require.NoError(t, err)

	require.NoError(t, svc.MarkDirty("test"))

	// Already built this window → stale index returned
	second, err := inner.ensureANNIndex()
	require.NoError(t, err)

	assert.Same(t, first, second)
}

func TestANNIndex_RebuildsInNewWindowAfterDirty(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, config.PeopleConfig{
		MergeSuggestionThreshold:       0.62,
		AttachThreshold:                1.10,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
		ANNRebuildWindowStart:          0,
		ANNRebuildWindowEnd:            24,
	})
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	first, err := inner.ensureANNIndex()
	require.NoError(t, err)

	require.NoError(t, svc.MarkDirty("test"))

	// Simulate window cycle rolling over: set annBuiltAt to before current window
	inner.annMu.Lock()
	inner.annBuiltAt = time.Now().Add(-25 * time.Hour)
	inner.annMu.Unlock()

	second, err := inner.ensureANNIndex()
	require.NoError(t, err)

	assert.NotSame(t, first, second)
	assert.False(t, inner.annDirty)
}

func TestRebuild_InvalidatesANNIndex(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	_, err := inner.ensureANNIndex()
	require.NoError(t, err)
	require.NotNil(t, inner.annIdx)

	require.NoError(t, svc.Rebuild())

	assert.Nil(t, inner.annIdx)
}

func TestFindCandidates_ReturnsNilWhenIndexNotBuilt(t *testing.T) {
	svc, _, _, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	// No persons created, index never built
	require.Nil(t, inner.annIdx, "precondition: index not built")

	result := inner.FindCandidates([]faceWithEmbedding{}, annSearchK)
	assert.Nil(t, result, "should return nil when index is not built (caller falls back to full scan)")
}

func TestFindCandidates_ReturnsCandidatesFromBuiltIndex(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	similar := createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))
	// orthogonal — should not appear as candidate
	_ = createSuggestionTestPerson(t, repos, "stranger", ann512(100, 1.0))

	idx, err := inner.buildANNIndex()
	require.NoError(t, err)
	inner.annMu.Lock()
	inner.annIdx = idx
	inner.annMu.Unlock()

	// Query with a probe embedding very close to the "similar" person
	probes := []faceWithEmbedding{{embedding: ann512(0, 1.0)}}
	result := inner.FindCandidates(probes, annSearchK)

	require.NotNil(t, result)
	assert.Contains(t, result, similar.ID)
}

func TestFindCandidates_IsSafeToCallConcurrently(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	idx, err := inner.buildANNIndex()
	require.NoError(t, err)
	inner.annMu.Lock()
	inner.annIdx = idx
	inner.annMu.Unlock()

	probes := []faceWithEmbedding{{embedding: ann512(0, 1.0)}}

	// Fire N concurrent FindCandidates calls — should not panic or race
	const goroutines = 4
	done := make(chan struct{}, goroutines)
	for range goroutines {
		go func() {
			inner.FindCandidates(probes, annSearchK)
			done <- struct{}{}
		}()
	}
	for range goroutines {
		<-done
	}
}

func TestANNIndex_NoRebuildOutsideWindow(t *testing.T) {
	// Use a narrow window that is unlikely to contain current hour
	now := time.Now()
	windowStart := (now.Hour() + 12) % 24
	windowEnd := windowStart + 1

	svc, _, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, config.PeopleConfig{
		MergeSuggestionThreshold:       0.62,
		AttachThreshold:                1.10,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
		ANNRebuildWindowStart:          windowStart,
		ANNRebuildWindowEnd:            windowEnd,
	})
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	// First call: annIdx is nil → always builds regardless of window
	first, err := inner.ensureANNIndex()
	require.NoError(t, err)
	require.NotNil(t, first)

	require.NoError(t, svc.MarkDirty("test"))

	// Set annBuiltAt far back so alreadyBuiltThisWindow returns false,
	// but current hour is outside window → should return stale index
	inner.annMu.Lock()
	inner.annBuiltAt = time.Now().Add(-25 * time.Hour)
	inner.annMu.Unlock()

	second, err := inner.ensureANNIndex()
	require.NoError(t, err)

	assert.Same(t, first, second, "should return stale index when outside rebuild window")
	assert.True(t, inner.annDirty, "dirty flag should remain set")
}
