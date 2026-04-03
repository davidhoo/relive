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

func TestFaceRepository_ListTopByPersonIDs(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	type topByPersonLister interface {
		ListTopByPersonIDs(personIDs []uint, perPerson int) ([]*model.Face, error)
	}

	faceRepo, ok := NewFaceRepository(db).(topByPersonLister)
	require.True(t, ok)

	personOne := &model.Person{Category: model.PersonCategoryFamily}
	personTwo := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, db.Create(personOne).Error)
	require.NoError(t, db.Create(personTwo).Error)

	faces := []*model.Face{
		{
			PhotoID:       1,
			PersonID:      &personOne.ID,
			BBoxX:         0.1,
			BBoxY:         0.1,
			BBoxWidth:     0.2,
			BBoxHeight:    0.2,
			Confidence:    0.80,
			QualityScore:  0.70,
			ManualLocked:  false,
			ThumbnailPath: "faces/1.jpg",
		},
		{
			PhotoID:       2,
			PersonID:      &personOne.ID,
			BBoxX:         0.1,
			BBoxY:         0.1,
			BBoxWidth:     0.2,
			BBoxHeight:    0.2,
			Confidence:    0.85,
			QualityScore:  0.70,
			ManualLocked:  true,
			ThumbnailPath: "faces/2.jpg",
		},
		{
			PhotoID:       3,
			PersonID:      &personOne.ID,
			BBoxX:         0.1,
			BBoxY:         0.1,
			BBoxWidth:     0.2,
			BBoxHeight:    0.2,
			Confidence:    0.92,
			QualityScore:  0.95,
			ManualLocked:  false,
			ThumbnailPath: "faces/3.jpg",
		},
		{
			PhotoID:       4,
			PersonID:      &personOne.ID,
			BBoxX:         0.1,
			BBoxY:         0.1,
			BBoxWidth:     0.2,
			BBoxHeight:    0.2,
			Confidence:    0.90,
			QualityScore:  0.95,
			ManualLocked:  false,
			ThumbnailPath: "faces/4.jpg",
		},
		{
			PhotoID:       5,
			PersonID:      &personTwo.ID,
			BBoxX:         0.1,
			BBoxY:         0.1,
			BBoxWidth:     0.2,
			BBoxHeight:    0.2,
			Confidence:    0.80,
			QualityScore:  0.60,
			ManualLocked:  false,
			ThumbnailPath: "faces/5.jpg",
		},
		{
			PhotoID:       6,
			PersonID:      &personTwo.ID,
			BBoxX:         0.1,
			BBoxY:         0.1,
			BBoxWidth:     0.2,
			BBoxHeight:    0.2,
			Confidence:    0.90,
			QualityScore:  0.60,
			ManualLocked:  false,
			ThumbnailPath: "faces/6.jpg",
		},
	}
	for _, face := range faces {
		require.NoError(t, db.Create(face).Error)
	}

	topFaces, err := faceRepo.ListTopByPersonIDs([]uint{personOne.ID, personTwo.ID}, 2)
	require.NoError(t, err)
	require.Len(t, topFaces, 4)

	require.NotNil(t, topFaces[0].PersonID)
	require.NotNil(t, topFaces[1].PersonID)
	require.NotNil(t, topFaces[2].PersonID)
	require.NotNil(t, topFaces[3].PersonID)

	assert.Equal(t, personOne.ID, *topFaces[0].PersonID)
	assert.Equal(t, faces[1].ID, topFaces[0].ID)
	assert.Equal(t, faces[2].ID, topFaces[1].ID)
	assert.Equal(t, personTwo.ID, *topFaces[2].PersonID)
	assert.Equal(t, faces[5].ID, topFaces[2].ID)
	assert.Equal(t, faces[4].ID, topFaces[3].ID)
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
