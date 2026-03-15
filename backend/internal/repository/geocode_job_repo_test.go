package repository

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeocodeJobRepo_Create(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewGeocodeJobRepository(db)

	job := &model.GeocodeJob{PhotoID: 1, Status: model.GeocodeJobStatusPending, Source: model.GeocodeJobSourceScan, QueuedAt: time.Now()}
	require.NoError(t, repo.Create(job))
	assert.NotZero(t, job.ID)
}

func TestGeocodeJobRepo_GetActiveByPhotoID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewGeocodeJobRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.GeocodeJob{PhotoID: 1, Status: model.GeocodeJobStatusPending, Source: model.GeocodeJobSourceScan, QueuedAt: now}))

	got, err := repo.GetActiveByPhotoID(1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, uint(1), got.PhotoID)
}

func TestGeocodeJobRepo_GetActiveByPhotoID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewGeocodeJobRepository(db)

	got, err := repo.GetActiveByPhotoID(999)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGeocodeJobRepo_ClaimNextJob(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewGeocodeJobRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.GeocodeJob{PhotoID: 1, Status: model.GeocodeJobStatusPending, Source: model.GeocodeJobSourceScan, QueuedAt: now}))

	claimed, err := repo.ClaimNextJob()
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, model.GeocodeJobStatusProcessing, claimed.Status)
}

func TestGeocodeJobRepo_ClaimNextJob_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewGeocodeJobRepository(db)

	claimed, err := repo.ClaimNextJob()
	require.NoError(t, err)
	assert.Nil(t, claimed)
}

func TestGeocodeJobRepo_CancelPendingJobs(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewGeocodeJobRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.GeocodeJob{PhotoID: 1, Status: model.GeocodeJobStatusPending, Source: model.GeocodeJobSourceScan, QueuedAt: now}))
	require.NoError(t, repo.Create(&model.GeocodeJob{PhotoID: 2, Status: model.GeocodeJobStatusQueued, Source: model.GeocodeJobSourceScan, QueuedAt: now}))
	require.NoError(t, repo.Create(&model.GeocodeJob{PhotoID: 3, Status: model.GeocodeJobStatusProcessing, Source: model.GeocodeJobSourceScan, QueuedAt: now}))

	count, err := repo.CancelPendingJobs()
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestGeocodeJobRepo_GetStats(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewGeocodeJobRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.GeocodeJob{PhotoID: 1, Status: model.GeocodeJobStatusPending, Source: model.GeocodeJobSourceScan, QueuedAt: now}))
	require.NoError(t, repo.Create(&model.GeocodeJob{PhotoID: 2, Status: model.GeocodeJobStatusCompleted, Source: model.GeocodeJobSourceScan, QueuedAt: now}))
	require.NoError(t, repo.Create(&model.GeocodeJob{PhotoID: 3, Status: model.GeocodeJobStatusCompleted, Source: model.GeocodeJobSourceScan, QueuedAt: now}))

	stats, err := repo.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(3), stats.Total)
	assert.Equal(t, int64(1), stats.Pending)
	assert.Equal(t, int64(2), stats.Completed)
}
