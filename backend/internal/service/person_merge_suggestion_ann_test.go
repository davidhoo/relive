package service

import (
	"testing"

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

func TestMarkDirty_InvalidatesANNIndex(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	inner := svc.(*personMergeSuggestionService)

	createSuggestionTestPerson(t, repos, "family", ann512(0, 1.0))

	_, err := inner.ensureANNIndex()
	require.NoError(t, err)
	require.NotNil(t, inner.annIdx)

	require.NoError(t, svc.MarkDirty("test-invalidation"))

	assert.Nil(t, inner.annIdx)
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
