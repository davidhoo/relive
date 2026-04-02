package repository

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeopleJobRepository_ClaimNextJob(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()

	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:  1,
		FilePath: "/photos/1.jpg",
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 1,
		QueuedAt: now,
	}))
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:  2,
		FilePath: "/photos/2.jpg",
		Status:   model.PeopleJobStatusPending,
		Source:   model.PeopleJobSourceManual,
		Priority: 10,
		QueuedAt: now.Add(time.Minute),
	}))

	claimed, err := repo.ClaimNextJob()
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, uint(2), claimed.PhotoID)
	assert.Equal(t, model.PeopleJobStatusProcessing, claimed.Status)
	assert.Equal(t, 1, claimed.AttemptCount)
}
