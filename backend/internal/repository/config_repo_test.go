package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigRepo_SetAndGet(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewConfigRepository(db)

	require.NoError(t, repo.Set("theme", `"dark"`))

	got, err := repo.Get("theme")
	require.NoError(t, err)
	assert.Equal(t, `"dark"`, got.Value)
}

func TestConfigRepo_Set_Overwrite(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewConfigRepository(db)

	require.NoError(t, repo.Set("key", "v1"))
	require.NoError(t, repo.Set("key", "v2"))

	got, err := repo.Get("key")
	require.NoError(t, err)
	assert.Equal(t, "v2", got.Value)
}

func TestConfigRepo_Get_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewConfigRepository(db)

	_, err := repo.Get("nonexistent")
	require.Error(t, err)
}

func TestConfigRepo_Delete(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewConfigRepository(db)

	require.NoError(t, repo.Set("tmp", "val"))
	require.NoError(t, repo.Delete("tmp"))

	_, err := repo.Get("tmp")
	require.Error(t, err)
}

func TestConfigRepo_Exists(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewConfigRepository(db)

	require.NoError(t, repo.Set("key", "val"))

	ok, err := repo.Exists("key")
	require.NoError(t, err)
	assert.True(t, ok)

	ok2, err := repo.Exists("nope")
	require.NoError(t, err)
	assert.False(t, ok2)
}

func TestConfigRepo_GetAll(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewConfigRepository(db)

	require.NoError(t, repo.Set("a", "1"))
	require.NoError(t, repo.Set("b", "2"))

	all, err := repo.GetAll()
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestConfigRepo_GetByKeys(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewConfigRepository(db)

	require.NoError(t, repo.Set("a", "1"))
	require.NoError(t, repo.Set("b", "2"))
	require.NoError(t, repo.Set("c", "3"))

	result, err := repo.GetByKeys([]string{"a", "c"})
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "1", result["a"])
	assert.Equal(t, "3", result["c"])
}

func TestConfigRepo_SetBatch(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewConfigRepository(db)

	require.NoError(t, repo.SetBatch(map[string]string{
		"x": "10",
		"y": "20",
	}))

	got, _ := repo.Get("x")
	assert.Equal(t, "10", got.Value)
	got2, _ := repo.Get("y")
	assert.Equal(t, "20", got2.Value)
}

func TestConfigRepo_List_Pagination(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewConfigRepository(db)

	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Set("k"+string(rune('0'+i)), "v"))
	}

	configs, total, err := repo.List(1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, configs, 3)
}

func TestConfigRepo_Count(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewConfigRepository(db)

	require.NoError(t, repo.Set("a", "1"))
	require.NoError(t, repo.Set("b", "2"))

	c, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(2), c)
}
