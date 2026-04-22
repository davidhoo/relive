package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCannotLinkRepo_CreateAndExists(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewCannotLinkRepository(db)

	err := repo.Create(1, 2)
	require.NoError(t, err)

	exists, err := repo.ExistsBetween(1, 2)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestCannotLinkRepo_ExistsBetween_Normalized(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewCannotLinkRepository(db)

	// Create with A > B — should be stored as (2,5) after normalization
	err := repo.Create(5, 2)
	require.NoError(t, err)

	// Query both orderings should find it
	exists, err := repo.ExistsBetween(2, 5)
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = repo.ExistsBetween(5, 2)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestCannotLinkRepo_CreateDuplicate_NoError(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewCannotLinkRepository(db)

	require.NoError(t, repo.Create(1, 2))
	require.NoError(t, repo.Create(1, 2)) // duplicate should be a no-op
	require.NoError(t, repo.Create(2, 1)) // reversed order duplicate

	// Still only one entry
	all, err := repo.ListAll()
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestCannotLinkRepo_CreateSameID_NoOp(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewCannotLinkRepository(db)

	require.NoError(t, repo.Create(3, 3))
	all, err := repo.ListAll()
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestCannotLinkRepo_ExistsBetween_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewCannotLinkRepository(db)

	exists, err := repo.ExistsBetween(10, 20)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestCannotLinkRepo_ListByPersonID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewCannotLinkRepository(db)

	require.NoError(t, repo.Create(1, 2))
	require.NoError(t, repo.Create(1, 3))
	require.NoError(t, repo.Create(4, 5))

	// Person 1 is linked to 2 and 3
	ids, err := repo.ListByPersonID(1)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uint{2, 3}, ids)

	// Person 4 is linked to 5
	ids, err = repo.ListByPersonID(4)
	require.NoError(t, err)
	assert.ElementsMatch(t, []uint{5}, ids)
}

func TestCannotLinkRepo_ListByPersonID_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewCannotLinkRepository(db)

	ids, err := repo.ListByPersonID(99)
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestCannotLinkRepo_DeleteByPersonID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewCannotLinkRepository(db)

	require.NoError(t, repo.Create(1, 2))
	require.NoError(t, repo.Create(1, 3))
	require.NoError(t, repo.Create(4, 5))

	require.NoError(t, repo.DeleteByPersonID(1))

	all, err := repo.ListAll()
	require.NoError(t, err)
	assert.Len(t, all, 1)
	assert.Equal(t, uint(4), all[0].PersonIDA)
	assert.Equal(t, uint(5), all[0].PersonIDB)
}

func TestCannotLinkRepo_ListAll_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewCannotLinkRepository(db)

	all, err := repo.ListAll()
	require.NoError(t, err)
	assert.Empty(t, all)
}
