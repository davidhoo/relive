package repository

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFaceRepository_CreateAndList(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	face1 := &model.Face{
		PhotoID:       1,
		PersonID:      &person.ID,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.95,
		QualityScore:  0.90,
		ThumbnailPath: "faces/1.jpg",
	}
	face2 := &model.Face{
		PhotoID:      1,
		BBoxX:        0.5,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.88,
		QualityScore: 0.80,
	}
	require.NoError(t, faceRepo.Create(face1))
	require.NoError(t, faceRepo.Create(face2))

	byPhoto, err := faceRepo.ListByPhotoID(1)
	require.NoError(t, err)
	require.Len(t, byPhoto, 2)

	byPerson, err := faceRepo.ListByPersonID(person.ID)
	require.NoError(t, err)
	require.Len(t, byPerson, 1)
	assert.Equal(t, face1.ID, byPerson[0].ID)
}

func TestFaceRepository_ListPending(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	type pendingLister interface {
		ListPending(limit int) ([]*model.Face, error)
	}

	faceRepo, ok := NewFaceRepository(db).(pendingLister)
	require.True(t, ok)

	pendingOne := &model.Face{
		PhotoID:       1,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.95,
		QualityScore:  0.90,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusterScore:  0.77,
		ClusteredAt:   ptrTime(time.Now().Add(-2 * time.Hour)),
		ThumbnailPath: "faces/pending-1.jpg",
	}
	pendingTwo := &model.Face{
		PhotoID:       2,
		BBoxX:         0.2,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.94,
		QualityScore:  0.89,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusterScore:  0.78,
		ClusteredAt:   ptrTime(time.Now().Add(-1 * time.Hour)),
	}
	assignedPersonID := uint(9)
	assigned := &model.Face{
		PhotoID:       3,
		PersonID:      &assignedPersonID,
		BBoxX:         0.3,
		BBoxY:         0.3,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.93,
		QualityScore:  0.88,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}

	require.NoError(t, db.Create(pendingOne).Error)
	require.NoError(t, db.Create(pendingTwo).Error)
	require.NoError(t, db.Create(assigned).Error)

	faces, err := faceRepo.ListPending(1)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, pendingOne.ID, faces[0].ID)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus)
}

func TestFaceRepository_ListPending_PrioritizesNeverClusteredFaces(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	type pendingLister interface {
		ListPending(limit int) ([]*model.Face, error)
	}

	faceRepo, ok := NewFaceRepository(db).(pendingLister)
	require.True(t, ok)

	retriedOld := &model.Face{
		PhotoID:       1,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.95,
		QualityScore:  0.90,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusterScore:  0.10,
		ClusteredAt:   ptrTime(time.Now().Add(-3 * time.Hour)),
	}
	neverClustered := &model.Face{
		PhotoID:       2,
		BBoxX:         0.2,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.94,
		QualityScore:  0.89,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusterScore:  0.0,
		ClusteredAt:   nil,
	}
	retriedRecent := &model.Face{
		PhotoID:       3,
		BBoxX:         0.3,
		BBoxY:         0.3,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.93,
		QualityScore:  0.88,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusterScore:  0.20,
		ClusteredAt:   ptrTime(time.Now().Add(-1 * time.Hour)),
	}

	require.NoError(t, db.Create(retriedOld).Error)
	require.NoError(t, db.Create(neverClustered).Error)
	require.NoError(t, db.Create(retriedRecent).Error)

	faces, err := faceRepo.ListPending(2)
	require.NoError(t, err)
	require.Len(t, faces, 2)
	assert.Equal(t, neverClustered.ID, faces[0].ID)
	assert.Equal(t, retriedOld.ID, faces[1].ID)
}

func TestFaceRepository_GetPendingStats(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	type pendingStatsGetter interface {
		GetPendingStats() (*PendingFaceStats, error)
	}

	faceRepo, ok := NewFaceRepository(db).(pendingStatsGetter)
	require.True(t, ok)

	require.NoError(t, db.Create(&model.Face{
		PhotoID:       1,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.95,
		QualityScore:  0.9,
		ClusterStatus: model.FaceClusterStatusPending,
	}).Error)
	require.NoError(t, db.Create(&model.Face{
		PhotoID:       2,
		BBoxX:         0.2,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.94,
		QualityScore:  0.89,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusteredAt:   ptrTime(time.Now().Add(-time.Hour)),
	}).Error)
	require.NoError(t, db.Create(&model.Face{
		PhotoID:       3,
		BBoxX:         0.3,
		BBoxY:         0.3,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.93,
		QualityScore:  0.88,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}).Error)

	stats, err := faceRepo.GetPendingStats()
	require.NoError(t, err)
	require.NotNil(t, stats)
	assert.Equal(t, int64(2), stats.Total)
	assert.Equal(t, int64(1), stats.NeverClustered)
	assert.Equal(t, int64(1), stats.Retried)
}

func TestFaceRepository_UpdateClusterFields(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	type clusterUpdater interface {
		UpdateClusterFields(ids []uint, fields map[string]interface{}) error
	}

	faceRepo, ok := NewFaceRepository(db).(clusterUpdater)
	require.True(t, ok)

	faceOne := &model.Face{
		PhotoID:      1,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.95,
		QualityScore: 0.90,
	}
	faceTwo := &model.Face{
		PhotoID:      2,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.94,
		QualityScore: 0.89,
	}
	faceThree := &model.Face{
		PhotoID:      3,
		BBoxX:        0.3,
		BBoxY:        0.3,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.93,
		QualityScore: 0.88,
	}
	require.NoError(t, db.Create(faceOne).Error)
	require.NoError(t, db.Create(faceTwo).Error)
	require.NoError(t, db.Create(faceThree).Error)

	clusteredAt := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, faceRepo.UpdateClusterFields([]uint{faceOne.ID, faceTwo.ID}, map[string]interface{}{
		"cluster_status": model.FaceClusterStatusAssigned,
		"cluster_score":  0.96,
		"clustered_at":   clusteredAt,
	}))

	var updated []*model.Face
	require.NoError(t, db.Order("id ASC").Find(&updated).Error)
	require.Len(t, updated, 3)

	assert.Equal(t, model.FaceClusterStatusAssigned, updated[0].ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusAssigned, updated[1].ClusterStatus)
	assert.InDelta(t, 0.96, updated[0].ClusterScore, 0.0001)
	assert.InDelta(t, 0.96, updated[1].ClusterScore, 0.0001)
	require.NotNil(t, updated[0].ClusteredAt)
	require.NotNil(t, updated[1].ClusteredAt)
	assert.WithinDuration(t, clusteredAt, *updated[0].ClusteredAt, time.Second)
	assert.WithinDuration(t, clusteredAt, *updated[1].ClusteredAt, time.Second)
	assert.Empty(t, updated[2].ClusterStatus)
	assert.Zero(t, updated[2].ClusterScore)
	assert.Nil(t, updated[2].ClusteredAt)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
