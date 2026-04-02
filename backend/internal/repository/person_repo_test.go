package repository

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersonRepository_Create(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPersonRepository(db)

	person := &model.Person{
		Name:     "Alice",
		Category: model.PersonCategoryFamily,
	}
	require.NoError(t, repo.Create(person))
	assert.NotZero(t, person.ID)

	got, err := repo.GetByID(person.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Alice", got.Name)
	assert.Equal(t, model.PersonCategoryFamily, got.Category)
}

func TestPersonRepository_MergePeopleUpdatesFaces(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	faceRepo := NewFaceRepository(db)

	target := &model.Person{Name: "Target", Category: model.PersonCategoryFriend}
	source := &model.Person{Name: "Source", Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(source))

	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      11,
		PersonID:     &target.ID,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.9,
		QualityScore: 0.9,
	}))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      12,
		PersonID:     &source.ID,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.92,
		QualityScore: 0.85,
	}))

	affectedPhotoIDs, err := personRepo.MergeInto(target.ID, []uint{source.ID})
	require.NoError(t, err)
	assert.ElementsMatch(t, []uint{11, 12}, affectedPhotoIDs)

	mergedFaces, err := faceRepo.ListByPersonID(target.ID)
	require.NoError(t, err)
	require.Len(t, mergedFaces, 2)

	sourceAfter, err := personRepo.GetByID(source.ID)
	require.NoError(t, err)
	assert.Nil(t, sourceAfter)

	targetAfter, err := personRepo.GetByID(target.ID)
	require.NoError(t, err)
	require.NotNil(t, targetAfter)
	assert.Equal(t, 2, targetAfter.FaceCount)
	assert.Equal(t, 2, targetAfter.PhotoCount)
}
