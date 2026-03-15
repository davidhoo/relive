package repository

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanJobRepo_Create(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewScanJobRepository(db)

	job := &model.ScanJob{ID: "job-001", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusPending, Phase: model.ScanJobPhasePending, Path: "/photos", StartedAt: time.Now()}
	require.NoError(t, repo.Create(job))
}

func TestScanJobRepo_GetByID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewScanJobRepository(db)

	job := &model.ScanJob{ID: "job-002", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusRunning, Phase: model.ScanJobPhaseDiscovering, Path: "/photos", StartedAt: time.Now()}
	require.NoError(t, repo.Create(job))

	got, err := repo.GetByID("job-002")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, model.ScanJobStatusRunning, got.Status)
}

func TestScanJobRepo_GetByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewScanJobRepository(db)

	got, err := repo.GetByID("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestScanJobRepo_Update(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewScanJobRepository(db)

	job := &model.ScanJob{ID: "job-003", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusRunning, Phase: model.ScanJobPhaseProcessing, Path: "/photos", StartedAt: time.Now()}
	require.NoError(t, repo.Create(job))

	job.Status = model.ScanJobStatusCompleted
	now := time.Now()
	job.CompletedAt = &now
	require.NoError(t, repo.Update(job))

	got, _ := repo.GetByID("job-003")
	assert.Equal(t, model.ScanJobStatusCompleted, got.Status)
}

func TestScanJobRepo_UpdateFields(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewScanJobRepository(db)

	job := &model.ScanJob{ID: "job-004", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusRunning, Phase: model.ScanJobPhaseProcessing, Path: "/photos", StartedAt: time.Now()}
	require.NoError(t, repo.Create(job))

	require.NoError(t, repo.UpdateFields("job-004", map[string]interface{}{
		"processed_files": 42,
		"new_photos":      10,
	}))

	got, _ := repo.GetByID("job-004")
	assert.Equal(t, 42, got.ProcessedFiles)
	assert.Equal(t, 10, got.NewPhotos)
}

func TestScanJobRepo_GetLatest(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewScanJobRepository(db)

	now := time.Now()
	require.NoError(t, repo.Create(&model.ScanJob{ID: "old", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusCompleted, Phase: model.ScanJobPhasePending, Path: "/a", StartedAt: now.Add(-time.Hour)}))
	require.NoError(t, repo.Create(&model.ScanJob{ID: "new", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusRunning, Phase: model.ScanJobPhaseDiscovering, Path: "/b", StartedAt: now}))

	got, err := repo.GetLatest()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "new", got.ID)
}

func TestScanJobRepo_GetLatest_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewScanJobRepository(db)

	got, err := repo.GetLatest()
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestScanJobRepo_GetActive(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewScanJobRepository(db)

	require.NoError(t, repo.Create(&model.ScanJob{ID: "done", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusCompleted, Phase: model.ScanJobPhasePending, Path: "/a", StartedAt: time.Now()}))
	require.NoError(t, repo.Create(&model.ScanJob{ID: "active", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusRunning, Phase: model.ScanJobPhaseDiscovering, Path: "/b", StartedAt: time.Now()}))

	got, err := repo.GetActive()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "active", got.ID)
}

func TestScanJobRepo_GetActive_None(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewScanJobRepository(db)

	require.NoError(t, repo.Create(&model.ScanJob{ID: "done", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusCompleted, Phase: model.ScanJobPhasePending, Path: "/a", StartedAt: time.Now()}))

	got, err := repo.GetActive()
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestScanJobRepo_InterruptNonTerminal(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewScanJobRepository(db)

	require.NoError(t, repo.Create(&model.ScanJob{ID: "j1", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusRunning, Phase: model.ScanJobPhaseProcessing, Path: "/a", StartedAt: time.Now()}))
	require.NoError(t, repo.Create(&model.ScanJob{ID: "j2", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusPending, Phase: model.ScanJobPhasePending, Path: "/b", StartedAt: time.Now()}))
	require.NoError(t, repo.Create(&model.ScanJob{ID: "j3", Type: model.ScanJobTypeScan, Status: model.ScanJobStatusCompleted, Phase: model.ScanJobPhasePending, Path: "/c", StartedAt: time.Now()}))

	require.NoError(t, repo.InterruptNonTerminal("server restart"))

	j1, _ := repo.GetByID("j1")
	assert.Equal(t, model.ScanJobStatusInterrupted, j1.Status)

	j2, _ := repo.GetByID("j2")
	assert.Equal(t, model.ScanJobStatusInterrupted, j2.Status)

	j3, _ := repo.GetByID("j3")
	assert.Equal(t, model.ScanJobStatusCompleted, j3.Status)
}
