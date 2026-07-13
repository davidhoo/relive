package repository

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFaceExclusionRepo_CreateAndList(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewFaceExclusionRepository(db)

	rec := &model.FaceExclusion{
		PhotoID:      1,
		SourceFaceID: 100,
		Reason:       model.ExclusionReasonNonFace,
		BBoxX:        0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
	}
	require.NoError(t, repo.Create(rec))
	assert.NotZero(t, rec.ID)

	records, err := repo.ListByPhotoID(1)
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Equal(t, model.ExclusionReasonNonFace, records[0].Reason)
}

func TestFaceExclusionRepo_DeleteByID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewFaceExclusionRepository(db)

	rec := &model.FaceExclusion{
		PhotoID:      1,
		SourceFaceID: 100,
		Reason:       model.ExclusionReasonLowQuality,
		BBoxX:        0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
	}
	require.NoError(t, repo.Create(rec))

	require.NoError(t, repo.DeleteByID(rec.ID))

	records, err := repo.ListByPhotoID(1)
	require.NoError(t, err)
	assert.Len(t, records, 0)
}

func TestFaceRepository_ExcludedFaceNotInListPending(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	// Create a pending face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusPending,
	}))

	// Create an excluded face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		BBoxX:         0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
	}))

	pending, err := faceRepo.ListPending(10)
	require.NoError(t, err)
	assert.Len(t, pending, 1, "excluded face should not appear in ListPending")
	assert.NotEqual(t, model.FaceClusterStatusExcluded, pending[0].ClusterStatus)
}

func TestFaceRepository_ExcludedFaceNotInListAssigned(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	pid := uint(1)
	// Normal assigned face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &pid,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}))

	// Excluded face (still has person_id temporarily, but cluster_status=excluded)
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &pid,
		BBoxX:         0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
	}))

	assigned, err := faceRepo.ListAssigned()
	require.NoError(t, err)
	assert.Len(t, assigned, 1, "excluded face should not appear in ListAssigned")
}

func TestFaceRepository_ExcludedFaceNotInListAssignedPersonIDs(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	pid := uint(1)
	// Excluded face with person_id
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &pid,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
	}))

	ids, err := faceRepo.ListAssignedPersonIDs()
	require.NoError(t, err)
	assert.Len(t, ids, 0, "excluded face should not contribute to assigned person IDs")
}

func TestFaceRepository_ExcludedFaceNotInListProfileFaces(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	pid := uint(1)
	// Normal face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &pid,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}))

	// Excluded face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       2,
		PersonID:      &pid,
		BBoxX:         0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
	}))

	profileFaces, err := faceRepo.ListProfileFaces(pid)
	require.NoError(t, err)
	assert.Len(t, profileFaces, 1, "excluded face should not appear in ListProfileFaces")
	assert.NotEqual(t, model.FaceClusterStatusExcluded, profileFaces[0].ClusterStatus)
}

func TestFaceRepository_ExcludedFaceNotInListByPersonIDPaginated(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	pid := uint(1)
	// Normal face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &pid,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}))

	// Excluded face with same person_id
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       2,
		PersonID:      &pid,
		BBoxX:         0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
	}))

	faces, total, err := faceRepo.ListByPersonIDPaginated(pid, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "excluded face should not be counted")
	assert.Len(t, faces, 1)
}

func TestFaceRepository_ExcludedFaceNotInListPrototypeEmbeddings(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	p := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(p))

	// Normal face with embedding
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &p.ID,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
		Embedding:     []byte{1, 2, 3, 4},
	}))

	// Excluded face with embedding
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       2,
		PersonID:      &p.ID,
		BBoxX:         0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
		Embedding:     []byte{5, 6, 7, 8},
	}))

	protos, err := faceRepo.ListPrototypeEmbeddings([]uint{p.ID}, 5)
	require.NoError(t, err)
	assert.Len(t, protos, 1, "excluded face should not appear in prototype embeddings")
}
