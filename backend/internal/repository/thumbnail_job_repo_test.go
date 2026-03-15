package repository

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThumbnailJobRepo_Create(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewThumbnailJobRepository(db)

	job := &model.ThumbnailJob{PhotoID: 1, FilePath: "/photos/1.jpg", Status: model.ThumbnailJobStatusPending, Source: model.ThumbnailJobSourceScan, QueuedAt: time.Now()}
	require.NoError(t, repo.Create(job))
	assert.NotZero(t, job.ID)
}

func TestThumbnailJobRepo_GetByID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewThumbnailJobRepository(db)

	job := &model.ThumbnailJob{PhotoID: 1, FilePath: "/photos/1.jpg", Status: model.ThumbnailJobStatusQueued, Source: model.ThumbnailJobSourceManual, QueuedAt: time.Now()}
	require.NoError(t, repo.Create(job))

	got, err := repo.GetByID(job.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, model.ThumbnailJobStatusQueued, got.Status)
}

func TestThumbnailJobRepo_GetByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewThumbnailJobRepository(db)

	got, err := repo.GetByID(999)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestThumbnailJobRepo_GetActiveByPhotoID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewThumbnailJobRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.ThumbnailJob{PhotoID: 5, FilePath: "/p/5.jpg", Status: model.ThumbnailJobStatusQueued, Source: model.ThumbnailJobSourceScan, QueuedAt: now}))

	got, err := repo.GetActiveByPhotoID(5)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, uint(5), got.PhotoID)
}

func TestThumbnailJobRepo_GetActiveByPhotoID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewThumbnailJobRepository(db)

	got, err := repo.GetActiveByPhotoID(999)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestThumbnailJobRepo_ClaimNextJob(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewThumbnailJobRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.ThumbnailJob{PhotoID: 1, FilePath: "/p/1.jpg", Status: model.ThumbnailJobStatusQueued, Source: model.ThumbnailJobSourceScan, QueuedAt: now}))

	claimed, err := repo.ClaimNextJob()
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, model.ThumbnailJobStatusProcessing, claimed.Status)

	// Second claim returns nil (all claimed)
	claimed2, err := repo.ClaimNextJob()
	require.NoError(t, err)
	assert.Nil(t, claimed2)
}

func TestThumbnailJobRepo_CancelPendingJobs(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewThumbnailJobRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.ThumbnailJob{PhotoID: 1, FilePath: "/p/1.jpg", Status: model.ThumbnailJobStatusPending, Source: model.ThumbnailJobSourceScan, QueuedAt: now}))
	require.NoError(t, repo.Create(&model.ThumbnailJob{PhotoID: 2, FilePath: "/p/2.jpg", Status: model.ThumbnailJobStatusQueued, Source: model.ThumbnailJobSourceScan, QueuedAt: now}))
	require.NoError(t, repo.Create(&model.ThumbnailJob{PhotoID: 3, FilePath: "/p/3.jpg", Status: model.ThumbnailJobStatusCompleted, Source: model.ThumbnailJobSourceScan, QueuedAt: now}))

	count, err := repo.CancelPendingJobs()
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestThumbnailJobRepo_GetStats(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewThumbnailJobRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.ThumbnailJob{PhotoID: 1, FilePath: "/p/1.jpg", Status: model.ThumbnailJobStatusPending, Source: model.ThumbnailJobSourceScan, QueuedAt: now}))
	require.NoError(t, repo.Create(&model.ThumbnailJob{PhotoID: 2, FilePath: "/p/2.jpg", Status: model.ThumbnailJobStatusCompleted, Source: model.ThumbnailJobSourceScan, QueuedAt: now}))
	require.NoError(t, repo.Create(&model.ThumbnailJob{PhotoID: 3, FilePath: "/p/3.jpg", Status: model.ThumbnailJobStatusFailed, Source: model.ThumbnailJobSourceScan, QueuedAt: now}))

	stats, err := repo.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(3), stats.Total)
	assert.Equal(t, int64(1), stats.Pending)
	assert.Equal(t, int64(1), stats.Completed)
	assert.Equal(t, int64(1), stats.Failed)
}

func TestThumbnailJobRepo_UpdateFields(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewThumbnailJobRepository(db)

	now := time.Now()
	job := &model.ThumbnailJob{PhotoID: 1, FilePath: "/p/1.jpg", Status: model.ThumbnailJobStatusProcessing, Source: model.ThumbnailJobSourceScan, QueuedAt: now}
	require.NoError(t, repo.Create(job))

	require.NoError(t, repo.UpdateFields(job.ID, map[string]interface{}{
		"status":        model.ThumbnailJobStatusCompleted,
		"attempt_count": 1,
	}))

	got, _ := repo.GetByID(job.ID)
	assert.Equal(t, model.ThumbnailJobStatusCompleted, got.Status)
	assert.Equal(t, 1, got.AttemptCount)
}
