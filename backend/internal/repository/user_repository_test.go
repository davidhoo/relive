package repository

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserRepo_Create(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewUserRepository(db)

	u := &model.User{Username: "admin", PasswordHash: "hash123", IsFirstLogin: true}
	require.NoError(t, repo.Create(u))
	assert.NotZero(t, u.ID)
}

func TestUserRepo_GetByID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewUserRepository(db)

	u := &model.User{Username: "alice", PasswordHash: "hash"}
	require.NoError(t, repo.Create(u))

	got, err := repo.GetByID(u.ID)
	require.NoError(t, err)
	assert.Equal(t, "alice", got.Username)
}

func TestUserRepo_GetByUsername(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewUserRepository(db)

	require.NoError(t, repo.Create(&model.User{Username: "bob", PasswordHash: "hash"}))

	got, err := repo.GetByUsername("bob")
	require.NoError(t, err)
	assert.Equal(t, "bob", got.Username)
}

func TestUserRepo_GetByUsername_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewUserRepository(db)

	_, err := repo.GetByUsername("nobody")
	require.Error(t, err)
}

func TestUserRepo_Update(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewUserRepository(db)

	u := &model.User{Username: "old", PasswordHash: "hash"}
	require.NoError(t, repo.Create(u))

	u.Username = "new"
	require.NoError(t, repo.Update(u))

	got, _ := repo.GetByID(u.ID)
	assert.Equal(t, "new", got.Username)
}

func TestUserRepo_Exists(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewUserRepository(db)

	require.NoError(t, repo.Create(&model.User{Username: "alice", PasswordHash: "hash"}))

	ok, _ := repo.Exists("alice")
	assert.True(t, ok)
	ok2, _ := repo.Exists("nobody")
	assert.False(t, ok2)
}

func TestUserRepo_Count(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewUserRepository(db)

	require.NoError(t, repo.Create(&model.User{Username: "a", PasswordHash: "h1"}))
	require.NoError(t, repo.Create(&model.User{Username: "b", PasswordHash: "h2"}))

	c, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(2), c)
}

func TestUserRepo_UpdatePassword(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewUserRepository(db)

	u := &model.User{Username: "admin", PasswordHash: "oldhash"}
	require.NoError(t, repo.Create(u))

	require.NoError(t, repo.UpdatePassword(u.ID, "newhash"))

	got, _ := repo.GetByID(u.ID)
	assert.Equal(t, "newhash", got.PasswordHash)
}

func TestUserRepo_UpdateFirstLoginStatus(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewUserRepository(db)

	u := &model.User{Username: "admin", PasswordHash: "hash", IsFirstLogin: true}
	require.NoError(t, repo.Create(u))

	require.NoError(t, repo.UpdateFirstLoginStatus(u.ID, false))

	got, _ := repo.GetByID(u.ID)
	assert.False(t, got.IsFirstLogin)
}
