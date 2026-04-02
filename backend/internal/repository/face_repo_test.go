package repository

import (
	"testing"

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
