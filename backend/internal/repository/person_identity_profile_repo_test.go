package repository

import (
	"errors"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func uintPtr(v uint) *uint { return &v }

// seedActiveGeneration creates a profile for personID with one active generation
// containing a single center and two accepted members, then returns the loaded build.
func seedActiveGeneration(t *testing.T, repo *personIdentityProfileRepository, personID uint) *model.PersonIdentityProfileBuild {
	t.Helper()
	require.NoError(t, repo.MarkDirty([]uint{personID}, "seed"))

	emb := model.EncodeEmbedding([]float32{1, 0, 0})
	build := &model.PersonIdentityProfileBuild{
		Profile: &model.PersonIdentityProfile{
			PersonID:         personID,
			AlgorithmVersion: "v1",
			EmbeddingModel:   "emb-v1",
		},
		Centers: []*model.PersonIdentityCenter{
			{
				PersonID:          personID,
				Ordinal:           1,
				CentroidEmbedding: emb,
				SumEmbedding:      emb,
				MedoidFaceID:      uintPtr(101),
				SupportCount:      2,
				TotalWeight:       2.0,
				SimilarityP10:     0.90,
				SimilarityP50:     0.95,
				Confirmed:         false,
			},
		},
		Members: []*model.PersonIdentityCenterMember{
			{PersonID: personID, FaceID: 101, PhotoID: 10, Similarity: 1.0, Weight: 1.0, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(1)},
			{PersonID: personID, FaceID: 102, PhotoID: 10, Similarity: 0.95, Weight: 1.0, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(1)},
		},
	}
	require.NoError(t, repo.ReplaceGeneration(personID, build))
	got, err := repo.GetActive(personID)
	require.NoError(t, err)
	require.NotNil(t, got.Profile)
	return got
}

func newProfileRepo(t *testing.T) (*personIdentityProfileRepository, *gorm.DB) {
	t.Helper()
	db := setupTestDB(t)
	return NewPersonIdentityProfileRepository(db).(*personIdentityProfileRepository), db
}

func TestPersonIdentityProfileRepository_MarkDirtyUpsertPreservesGeneration(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	old := seedActiveGeneration(t, repo, person.ID)
	require.Equal(t, 1, old.Profile.ActiveGeneration)

	// MarkDirty must not reset the active generation.
	require.NoError(t, repo.MarkDirty([]uint{person.ID}, "face_added"))
	got, err := repo.GetActive(person.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Profile.ActiveGeneration)
	assert.Equal(t, model.PersonIdentityProfileStatusDirty, got.Profile.Status)
	assert.Equal(t, "face_added", got.Profile.DirtyReason)
	// Centers/members of the active generation remain queryable.
	assert.Len(t, got.Centers, 1)
	assert.Len(t, got.Members, 2)
}

func TestPersonIdentityProfileRepository_MarkDirtyCreatesMissingProfile(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	require.NoError(t, repo.MarkDirty([]uint{person.ID}, "initial"))
	dirty, err := repo.ListDirty(0, 10)
	require.NoError(t, err)
	require.Len(t, dirty, 1)
	assert.Equal(t, person.ID, dirty[0].PersonID)
	assert.Equal(t, model.PersonIdentityProfileStatusDirty, dirty[0].Status)
	assert.Equal(t, 0, dirty[0].ActiveGeneration, "new profile has no active generation yet")
}

func TestPersonIdentityProfileRepository_ListDirtyDeterministicByPersonID(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	for i := 0; i < 3; i++ {
		p := &model.Person{Category: model.PersonCategoryFriend}
		require.NoError(t, personRepo.Create(p))
		require.NoError(t, repo.MarkDirty([]uint{p.ID}, "init"))
	}

	all, err := repo.ListDirty(0, 100)
	require.NoError(t, err)
	require.Len(t, all, 3)
	// Deterministic ascending by person ID.
	assert.Less(t, all[0].PersonID, all[1].PersonID)
	assert.Less(t, all[1].PersonID, all[2].PersonID)

	// Cursor pagination: skip the first.
	page, err := repo.ListDirty(all[0].PersonID, 100)
	require.NoError(t, err)
	require.Len(t, page, 2)
	assert.Equal(t, all[1].PersonID, page[0].PersonID)
	assert.Equal(t, all[2].PersonID, page[1].PersonID)

	// Limit honored.
	one, err := repo.ListDirty(0, 1)
	require.NoError(t, err)
	require.Len(t, one, 1)
}

func TestPersonIdentityProfileRepository_ReplaceGenerationActivatesNew(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	seedActiveGeneration(t, repo, person.ID) // generation 1

	// Build generation 2 with two centers.
	emb2 := model.EncodeEmbedding([]float32{0, 1, 0})
	newBuild := &model.PersonIdentityProfileBuild{
		Profile: &model.PersonIdentityProfile{
			PersonID:         person.ID,
			AlgorithmVersion: "v1",
			EmbeddingModel:   "emb-v1",
		},
		Centers: []*model.PersonIdentityCenter{
			{PersonID: person.ID, Ordinal: 1, CentroidEmbedding: emb2, SumEmbedding: emb2, MedoidFaceID: uintPtr(201), SupportCount: 1, TotalWeight: 1, SimilarityP10: 0.9, SimilarityP50: 0.9},
			{PersonID: person.ID, Ordinal: 2, CentroidEmbedding: emb2, SumEmbedding: emb2, MedoidFaceID: uintPtr(202), SupportCount: 1, TotalWeight: 1, SimilarityP10: 0.8, SimilarityP50: 0.8},
		},
		Members: []*model.PersonIdentityCenterMember{
			{PersonID: person.ID, FaceID: 201, PhotoID: 20, Similarity: 1.0, Weight: 1.0, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(1)},
			{PersonID: person.ID, FaceID: 202, PhotoID: 20, Similarity: 1.0, Weight: 1.0, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(2)},
			{PersonID: person.ID, FaceID: 203, PhotoID: 20, Similarity: 0.4, Weight: 0, State: model.PersonIdentityMemberStateCandidate},
		},
	}
	require.NoError(t, repo.ReplaceGeneration(person.ID, newBuild))

	got, err := repo.GetActive(person.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Profile.ActiveGeneration)
	assert.Equal(t, model.PersonIdentityProfileStatusReady, got.Profile.Status)
	assert.Len(t, got.Centers, 2)
	assert.Len(t, got.Members, 3)

	// Only active-generation rows are loaded: generation 1 must not leak.
	for _, c := range got.Centers {
		assert.Equal(t, 2, c.Generation)
	}
	for _, m := range got.Members {
		assert.Equal(t, 2, m.Generation)
	}

	// CenterID remapped from ordinal to a real persisted center ID (non-zero, points at a loaded center).
	centerIDs := map[uint]bool{}
	for _, c := range got.Centers {
		centerIDs[c.ID] = true
	}
	for _, m := range got.Members {
		if m.State == model.PersonIdentityMemberStateAccepted {
			require.NotNil(t, m.CenterID, "accepted member must reference a center")
			assert.True(t, centerIDs[*m.CenterID], "member center_id must reference a real center")
		} else {
			assert.Nil(t, m.CenterID, "candidate/excluded member has no center")
		}
	}
}

func TestPersonIdentityProfileRepository_ReplaceGenerationAtomicOnHookFailure(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	old := seedActiveGeneration(t, repo, person.ID)
	require.Equal(t, 1, old.Profile.ActiveGeneration)

	newBuild := &model.PersonIdentityProfileBuild{
		Profile: &model.PersonIdentityProfile{PersonID: person.ID, AlgorithmVersion: "v1"},
		Centers: []*model.PersonIdentityCenter{
			{PersonID: person.ID, Ordinal: 1, CentroidEmbedding: model.EncodeEmbedding([]float32{0, 1, 0})},
		},
		Members: []*model.PersonIdentityCenterMember{
			{PersonID: person.ID, FaceID: 201, PhotoID: 20, Similarity: 1.0, Weight: 1.0, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(1)},
		},
	}

	// Force a failure right before activation; the old generation must stay active.
	repo.setBeforeActivateHookForTest(func() error { return errors.New("forced") })
	err := repo.ReplaceGeneration(person.ID, newBuild)
	require.Error(t, err)

	got, err := repo.GetActive(person.ID)
	require.NoError(t, err)
	assert.Equal(t, old.Profile.ActiveGeneration, got.Profile.ActiveGeneration, "old generation remains active")
	assert.Len(t, got.Centers, 1, "old generation centers intact")
	assert.Len(t, got.Members, 2, "old generation members intact")
}

func TestPersonIdentityProfileRepository_ReplaceGenerationRejectsUnknownPerson(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	build := &model.PersonIdentityProfileBuild{
		Profile: &model.PersonIdentityProfile{PersonID: 9999},
		Centers: []*model.PersonIdentityCenter{{Ordinal: 1}},
	}
	err := repo.ReplaceGeneration(9999, build)
	require.Error(t, err)
}

func TestPersonIdentityProfileRepository_MarkFailedPreservesGeneration(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	seedActiveGeneration(t, repo, person.ID)
	require.NoError(t, repo.MarkFailed(person.ID, "boom"))

	got, err := repo.GetActive(person.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Profile.ActiveGeneration, "active generation preserved on failure")
	assert.Equal(t, model.PersonIdentityProfileStatusFailed, got.Profile.Status)
	assert.Equal(t, "boom", got.Profile.LastError)
}

func TestPersonIdentityProfileRepository_GetActiveEmptyWhenNoProfile(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	got, err := repo.GetActive(12345)
	require.NoError(t, err)
	assert.Nil(t, got, "no profile returns nil build, not an error")
}

func TestPersonIdentityProfileRepository_ListAllActiveCenters(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	p1 := &model.Person{Category: model.PersonCategoryFriend}
	p2 := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(p1))
	require.NoError(t, personRepo.Create(p2))

	seedActiveGeneration(t, repo, p1.ID) // p1: gen 1, 1 center
	seedActiveGeneration(t, repo, p2.ID) // p2: gen 1, 1 center

	centers, err := repo.ListAllActiveCenters()
	require.NoError(t, err)
	require.Len(t, centers, 2)

	owners := map[uint]bool{}
	for _, c := range centers {
		owners[c.PersonID] = true
		assert.Equal(t, 1, c.Generation, "only active generation centers returned")
	}
	assert.True(t, owners[p1.ID])
	assert.True(t, owners[p2.ID])
}

func TestPersonIdentityProfileRepository_DeleteByPersonIDs(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	p1 := &model.Person{Category: model.PersonCategoryFriend}
	p2 := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(p1))
	require.NoError(t, personRepo.Create(p2))

	seedActiveGeneration(t, repo, p1.ID)
	seedActiveGeneration(t, repo, p2.ID)

	require.NoError(t, repo.DeleteByPersonIDs([]uint{p1.ID}))

	got, err := repo.GetActive(p1.ID)
	require.NoError(t, err)
	assert.Nil(t, got, "deleted person has no profile")

	got2, err := repo.GetActive(p2.ID)
	require.NoError(t, err)
	assert.NotNil(t, got2, "other person profile intact")
}

func TestPersonIdentityProfileRepository_InvalidateDeletedPeople(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	p1 := &model.Person{Category: model.PersonCategoryFriend}
	p2 := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(p1))
	require.NoError(t, personRepo.Create(p2))

	seedActiveGeneration(t, repo, p1.ID)
	seedActiveGeneration(t, repo, p2.ID)

	// Hard-delete the person row (orphans the derived profile/center/member rows).
	require.NoError(t, db.Unscoped().Delete(&model.Person{}, p1.ID).Error)

	require.NoError(t, repo.InvalidateDeletedPeople())

	got, err := repo.GetActive(p1.ID)
	require.NoError(t, err)
	assert.Nil(t, got, "orphaned profile removed")

	got2, err := repo.GetActive(p2.ID)
	require.NoError(t, err)
	assert.NotNil(t, got2, "person still present keeps its profile")
}

func TestPersonIdentityProfileRepository_DeleteInactiveGenerations(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	seedActiveGeneration(t, repo, person.ID) // gen 1 active

	// Activate gen 2; gen 1 rows remain until pruned.
	emb := model.EncodeEmbedding([]float32{0, 1, 0})
	gen2 := &model.PersonIdentityProfileBuild{
		Profile: &model.PersonIdentityProfile{PersonID: person.ID, AlgorithmVersion: "v1"},
		Centers: []*model.PersonIdentityCenter{{PersonID: person.ID, Ordinal: 1, CentroidEmbedding: emb, SumEmbedding: emb}},
		Members: []*model.PersonIdentityCenterMember{
			{PersonID: person.ID, FaceID: 301, PhotoID: 30, Similarity: 1, Weight: 1, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(1)},
		},
	}
	require.NoError(t, repo.ReplaceGeneration(person.ID, gen2))

	got, err := repo.GetActive(person.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Profile.ActiveGeneration)

	// keep=1: only the highest (active) generation is retained; gen 1 pruned.
	require.NoError(t, repo.DeleteInactiveGenerations(person.ID, 1))

	var remainingCenters []model.PersonIdentityCenter
	require.NoError(t, db.Where("person_id = ?", person.ID).Find(&remainingCenters).Error)
	require.Len(t, remainingCenters, 1)
	assert.Equal(t, 2, remainingCenters[0].Generation)

	var remainingMembers []model.PersonIdentityCenterMember
	require.NoError(t, db.Where("person_id = ?", person.ID).Find(&remainingMembers).Error)
	require.Len(t, remainingMembers, 1)
	assert.Equal(t, 2, remainingMembers[0].Generation)

	// Active build still loads after pruning.
	got, err = repo.GetActive(person.ID)
	require.NoError(t, err)
	assert.Len(t, got.Centers, 1)
}
