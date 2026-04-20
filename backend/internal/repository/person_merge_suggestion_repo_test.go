package repository

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersonMergeSuggestionRepository_ReplacePendingForTarget(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPersonMergeSuggestionRepository(db)

	require.NoError(t, repo.ReplacePendingForTarget(100, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: 201, SimilarityScore: 0.87, Rank: 2},
		{CandidatePersonID: 202, SimilarityScore: 0.93, Rank: 1},
	}))

	firstPage, firstTotal, err := repo.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), firstTotal)
	require.Len(t, firstPage, 1)
	firstID := firstPage[0].ID

	require.NoError(t, repo.ReplacePendingForTarget(100, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: 203, SimilarityScore: 0.91, Rank: 1},
	}))

	currentPage, currentTotal, err := repo.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), currentTotal)
	require.Len(t, currentPage, 1)
	assert.NotEqual(t, firstID, currentPage[0].ID)
	assert.Equal(t, 100, int(currentPage[0].TargetPersonID))
	assert.Equal(t, 1, currentPage[0].CandidateCount)
	assert.Equal(t, 0.91, currentPage[0].TopSimilarity)

	obsoleted, err := repo.GetByID(firstID)
	require.NoError(t, err)
	require.NotNil(t, obsoleted)
	assert.Equal(t, model.PersonMergeSuggestionStatusObsolete, obsoleted.Status)
}

func TestPersonMergeSuggestionRepository_ListPendingWithItems(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPersonMergeSuggestionRepository(db)

	require.NoError(t, repo.ReplacePendingForTarget(100, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: 301, SimilarityScore: 0.72, Rank: 3},
		{CandidatePersonID: 302, SimilarityScore: 0.81, Rank: 1},
		{CandidatePersonID: 303, SimilarityScore: 0.78, Rank: 2},
	}))

	got, total, err := repo.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, got, 1)

	items, err := repo.GetItems(got[0].ID, model.PersonMergeSuggestionItemStatusPending)
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.Equal(t, uint(302), items[0].CandidatePersonID)
	assert.Equal(t, uint(303), items[1].CandidatePersonID)
	assert.Equal(t, uint(301), items[2].CandidatePersonID)
	assert.Equal(t, 1, items[0].Rank)
	assert.Equal(t, 2, items[1].Rank)
	assert.Equal(t, 3, items[2].Rank)
}

func TestPersonMergeSuggestionRepository_MarkItemsExcluded(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPersonMergeSuggestionRepository(db)

	require.NoError(t, repo.ReplacePendingForTarget(100, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: 401, SimilarityScore: 0.88, Rank: 1},
		{CandidatePersonID: 402, SimilarityScore: 0.84, Rank: 2},
	}))

	suggestions, _, err := repo.ListPending(1, 10)
	require.NoError(t, err)
	require.Len(t, suggestions, 1)
	suggestionID := suggestions[0].ID

	require.NoError(t, repo.MarkItemsStatus(suggestionID, []uint{401}, model.PersonMergeSuggestionItemStatusExcluded))
	current, err := repo.GetByID(suggestionID)
	require.NoError(t, err)
	require.NotNil(t, current)
	assert.Equal(t, model.PersonMergeSuggestionStatusPending, current.Status)

	require.NoError(t, repo.MarkItemsStatus(suggestionID, []uint{402}, model.PersonMergeSuggestionItemStatusExcluded))
	terminal, err := repo.GetByID(suggestionID)
	require.NoError(t, err)
	require.NotNil(t, terminal)
	assert.Equal(t, model.PersonMergeSuggestionStatusDismissed, terminal.Status)
	require.NotNil(t, terminal.ReviewedAt)

	excludedItems, err := repo.GetItems(suggestionID, model.PersonMergeSuggestionItemStatusExcluded)
	require.NoError(t, err)
	require.Len(t, excludedItems, 2)
}

func TestPersonMergeSuggestionRepository_MarkItemsMerged(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPersonMergeSuggestionRepository(db)

	require.NoError(t, repo.ReplacePendingForTarget(100, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: 501, SimilarityScore: 0.95, Rank: 1},
		{CandidatePersonID: 502, SimilarityScore: 0.89, Rank: 2},
	}))

	suggestions, _, err := repo.ListPending(1, 10)
	require.NoError(t, err)
	require.Len(t, suggestions, 1)
	suggestionID := suggestions[0].ID

	require.NoError(t, repo.MarkItemsStatus(suggestionID, []uint{501}, model.PersonMergeSuggestionItemStatusMerged))

	applied, err := repo.GetByID(suggestionID)
	require.NoError(t, err)
	require.NotNil(t, applied)
	assert.Equal(t, model.PersonMergeSuggestionStatusApplied, applied.Status)
	require.NotNil(t, applied.ReviewedAt)

	mergedItems, err := repo.GetItems(suggestionID, model.PersonMergeSuggestionItemStatusMerged)
	require.NoError(t, err)
	require.Len(t, mergedItems, 1)
	assert.Equal(t, uint(501), mergedItems[0].CandidatePersonID)

	obsoleteItems, err := repo.GetItems(suggestionID, model.PersonMergeSuggestionItemStatusObsolete)
	require.NoError(t, err)
	require.Len(t, obsoleteItems, 1)
	assert.Equal(t, uint(502), obsoleteItems[0].CandidatePersonID)

	pendingItems, err := repo.GetItems(suggestionID, model.PersonMergeSuggestionItemStatusPending)
	require.NoError(t, err)
	assert.Empty(t, pendingItems)
}

func TestPersonMergeSuggestionRepository_CandidateCanOnlyBelongToOnePendingSuggestion(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPersonMergeSuggestionRepository(db)

	require.NoError(t, repo.ReplacePendingForTarget(100, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: 601, SimilarityScore: 0.92, Rank: 1},
	}))

	pendingFor601, err := repo.FindPendingSuggestionByCandidate(601)
	require.NoError(t, err)
	require.NotNil(t, pendingFor601)
	assert.Equal(t, uint(100), pendingFor601.TargetPersonID)

	require.NoError(t, repo.ReplacePendingForTarget(101, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: 601, SimilarityScore: 0.91, Rank: 1},
	}))

	currentFor601, err := repo.FindPendingSuggestionByCandidate(601)
	require.NoError(t, err)
	require.NotNil(t, currentFor601)
	assert.Equal(t, uint(101), currentFor601.TargetPersonID)

	itemsForOld, err := repo.GetItems(pendingFor601.ID, model.PersonMergeSuggestionItemStatusPending)
	require.NoError(t, err)
	assert.Empty(t, itemsForOld)
}
