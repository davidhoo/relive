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

func TestPersonIdentityProfileRepository_ReplaceGenerationAtomicOnDuplicateMember(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	old := seedActiveGeneration(t, repo, person.ID)
	require.Equal(t, 1, old.Profile.ActiveGeneration)

	// A duplicate face_id within the same generation trips the
	// (person_id, generation, face_id) unique constraint mid-write, forcing a
	// real failure rather than a test-only production hook.
	newBuild := &model.PersonIdentityProfileBuild{
		Profile: &model.PersonIdentityProfile{PersonID: person.ID, AlgorithmVersion: "v1"},
		Centers: []*model.PersonIdentityCenter{
			{PersonID: person.ID, Ordinal: 1, CentroidEmbedding: model.EncodeEmbedding([]float32{0, 1, 0})},
		},
		Members: []*model.PersonIdentityCenterMember{
			{PersonID: person.ID, FaceID: 201, PhotoID: 20, Similarity: 1.0, Weight: 1.0, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(1)},
			{PersonID: person.ID, FaceID: 201, PhotoID: 20, Similarity: 0.8, Weight: 0.5, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(1)},
		},
	}
	err := repo.ReplaceGeneration(person.ID, newBuild)
	require.Error(t, err, "duplicate member must violate the unique constraint")

	// Old generation must remain fully active and intact after rollback.
	got, err := repo.GetActive(person.ID)
	require.NoError(t, err)
	assert.Equal(t, old.Profile.ActiveGeneration, got.Profile.ActiveGeneration, "old generation remains active")
	assert.Len(t, got.Centers, 1, "old generation centers intact")
	assert.Len(t, got.Members, 2, "old generation members intact")

	// No half-built generation 2 rows leak past the rollback.
	var gen2Centers []model.PersonIdentityCenter
	require.NoError(t, db.Where("person_id = ? AND generation = ?", person.ID, 2).Find(&gen2Centers).Error)
	assert.Empty(t, gen2Centers, "rolled-back generation leaves no center rows")
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

	centers, err := repo.ListAllActiveCenters("emb-v1")
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

// TestPersonIdentityProfileRepository_ListAllActiveCentersPrecisionGuard 验证
// ListAllActiveCenters 在数据库侧严格过滤：模型签名不匹配、人物已删除、历史 generation
// 均不返回；且只返回活动 generation 的中心。
func TestPersonIdentityProfileRepository_ListAllActiveCentersPrecisionGuard(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	p1 := &model.Person{Category: model.PersonCategoryFriend}
	p2 := &model.Person{Category: model.PersonCategoryFriend}
	p3 := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(p1))
	require.NoError(t, personRepo.Create(p2))
	require.NoError(t, personRepo.Create(p3))

	// p1/p2 用 emb-v1 构建；p3 用不同模型签名构建。
	seedActiveGeneration(t, repo, p1.ID) // emb-v1
	seedActiveGeneration(t, repo, p2.ID) // emb-v1
	seedActiveGenerationWithModel(t, repo, p3.ID, "emb-other")

	// 模型签名不匹配：只返回 p1/p2。
	centers, err := repo.ListAllActiveCenters("emb-v1")
	require.NoError(t, err)
	require.Len(t, centers, 2, "centers with mismatched embedding model must be excluded")
	for _, c := range centers {
		assert.NotEqual(t, p3.ID, c.PersonID)
	}

	// 硬删除 p1（orphan 其派生数据）→ 其中心不再返回（people JOIN 过滤）。
	require.NoError(t, db.Unscoped().Delete(&model.Person{}, p1.ID).Error)
	centers, err = repo.ListAllActiveCenters("emb-v1")
	require.NoError(t, err)
	require.Len(t, centers, 1, "centers of deleted people must be excluded")
	assert.Equal(t, p2.ID, centers[0].PersonID)

	// 查询另一模型签名只返回 p3。
	centers, err = repo.ListAllActiveCenters("emb-other")
	require.NoError(t, err)
	require.Len(t, centers, 1)
	assert.Equal(t, p3.ID, centers[0].PersonID)

	// 不存在的模型签名返回空集（而非错误）。
	centers, err = repo.ListAllActiveCenters("nonexistent")
	require.NoError(t, err)
	assert.Empty(t, centers)
}

// seedActiveGenerationWithModel 与 seedActiveGeneration 相同，但允许指定 embedding_model，
// 用于测试 ListAllActiveCenters 的模型签名过滤。
func seedActiveGenerationWithModel(t *testing.T, repo *personIdentityProfileRepository, personID uint, embeddingModel string) *model.PersonIdentityProfileBuild {
	t.Helper()
	require.NoError(t, repo.MarkDirty([]uint{personID}, "seed"))

	emb := model.EncodeEmbedding([]float32{1, 0, 0})
	build := &model.PersonIdentityProfileBuild{
		Profile: &model.PersonIdentityProfile{
			PersonID:         personID,
			AlgorithmVersion: "v1",
			EmbeddingModel:   embeddingModel,
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
		},
	}
	require.NoError(t, repo.ReplaceGeneration(personID, build))
	got, err := repo.GetActive(personID)
	require.NoError(t, err)
	require.NotNil(t, got.Profile)
	return got
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

	// keep=1 retains the single most recent non-active generation (gen 1).
	require.NoError(t, repo.DeleteInactiveGenerations(person.ID, 1))
	var remainingCenters []model.PersonIdentityCenter
	require.NoError(t, db.Where("person_id = ?", person.ID).Find(&remainingCenters).Error)
	require.Len(t, remainingCenters, 2, "keep=1 retains the non-active generation")

	// keep=0 prunes ALL non-active generations; the active generation is never deleted.
	require.NoError(t, repo.DeleteInactiveGenerations(person.ID, 0))
	require.NoError(t, db.Where("person_id = ?", person.ID).Find(&remainingCenters).Error)
	require.Len(t, remainingCenters, 1)
	assert.Equal(t, 2, remainingCenters[0].Generation, "active generation retained")

	var remainingMembers []model.PersonIdentityCenterMember
	require.NoError(t, db.Where("person_id = ?", person.ID).Find(&remainingMembers).Error)
	require.Len(t, remainingMembers, 1)
	assert.Equal(t, 2, remainingMembers[0].Generation)

	// Active build still loads after pruning.
	got, err = repo.GetActive(person.ID)
	require.NoError(t, err)
	assert.Len(t, got.Centers, 1)
}

// TestPersonIdentityProfileRepository_MarkDirtyEdgeCases covers empty, duplicate and
// zero IDs: the call is a no-op success and never creates a profile for id 0.
func TestPersonIdentityProfileRepository_MarkDirtyEdgeCases(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	// Empty input succeeds without writing anything.
	require.NoError(t, repo.MarkDirty(nil, "empty"))
	require.NoError(t, repo.MarkDirty([]uint{}, "empty"))
	dirty, err := repo.ListDirty(0, 10)
	require.NoError(t, err)
	assert.Empty(t, dirty)

	// Zero IDs are ignored; duplicates collapse to a single profile.
	require.NoError(t, repo.MarkDirty([]uint{0, person.ID, person.ID, 0}, "dup"))
	dirty, err = repo.ListDirty(0, 10)
	require.NoError(t, err)
	require.Len(t, dirty, 1)
	assert.Equal(t, person.ID, dirty[0].PersonID)

	// No profile for person_id 0 is ever created.
	var zeroProfile model.PersonIdentityProfile
	err = db.Where("person_id = ?", 0).First(&zeroProfile).Error
	assert.True(t, gorm.ErrRecordNotFound == err || errors.Is(err, gorm.ErrRecordNotFound))
}

// TestPersonIdentityProfileRepository_MarkDirtyLargeBatch verifies chunked writes handle
// more IDs than the SQLite parameter limit without error.
func TestPersonIdentityProfileRepository_MarkDirtyLargeBatch(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	const n = sqliteVarLimit + 50
	ids := make([]uint, 0, n)
	for i := 0; i < n; i++ {
		p := &model.Person{Category: model.PersonCategoryFriend}
		require.NoError(t, personRepo.Create(p))
		ids = append(ids, p.ID)
	}

	require.NoError(t, repo.MarkDirty(ids, "bulk"))
	dirty, err := repo.ListDirty(0, n+10)
	require.NoError(t, err)
	require.Len(t, dirty, n, "all bulk-marked profiles are retrievable")
}

// TestPersonIdentityProfileRepository_GetActiveNilWhenNoActiveGeneration asserts a profile
// with active_generation=0 (only MarkDirty'd) returns a nil build per the spec.
func TestPersonIdentityProfileRepository_GetActiveNilWhenNoActiveGeneration(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	require.NoError(t, repo.MarkDirty([]uint{person.ID}, "init"))
	got, err := repo.GetActive(person.ID)
	require.NoError(t, err)
	assert.Nil(t, got, "profile with no active generation returns nil build")
}

// TestPersonIdentityProfileRepository_GetActiveMembersOrderedByFaceID verifies members are
// returned ordered by face_id (not by row id), and that historical generations never leak.
func TestPersonIdentityProfileRepository_GetActiveMembersOrderedByFaceID(t *testing.T) {
	repo, db := newProfileRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))
	require.NoError(t, repo.MarkDirty([]uint{person.ID}, "seed"))

	// Insert faces with non-monotonic IDs so face_id ordering is distinguishable from id ordering.
	emb := model.EncodeEmbedding([]float32{1, 0, 0})
	build := &model.PersonIdentityProfileBuild{
		Profile: &model.PersonIdentityProfile{PersonID: person.ID, AlgorithmVersion: "v1"},
		Centers: []*model.PersonIdentityCenter{
			{PersonID: person.ID, Ordinal: 1, CentroidEmbedding: emb, SumEmbedding: emb, SupportCount: 3},
		},
		Members: []*model.PersonIdentityCenterMember{
			{PersonID: person.ID, FaceID: 300, PhotoID: 10, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(1)},
			{PersonID: person.ID, FaceID: 100, PhotoID: 10, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(1)},
			{PersonID: person.ID, FaceID: 200, PhotoID: 10, State: model.PersonIdentityMemberStateAccepted, CenterID: uintPtr(1)},
		},
	}
	require.NoError(t, repo.ReplaceGeneration(person.ID, build))

	got, err := repo.GetActive(person.ID)
	require.NoError(t, err)
	require.Len(t, got.Members, 3)
	assert.Equal(t, uint(100), got.Members[0].FaceID)
	assert.Equal(t, uint(200), got.Members[1].FaceID)
	assert.Equal(t, uint(300), got.Members[2].FaceID)

	// No historical generation rows exist for a fresh single-build profile.
	var histCenters int64
	require.NoError(t, db.Model(&model.PersonIdentityCenter{}).
		Where("person_id = ? AND generation != ?", person.ID, got.Profile.ActiveGeneration).
		Count(&histCenters).Error)
	assert.Zero(t, histCenters)
}

// TestRepositories_IdentityProfileInitialized confirms the aggregate Repositories struct
// wires the identity profile repository on construction.
func TestRepositories_IdentityProfileInitialized(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repos := NewRepositories(db)
	assert.NotNil(t, repos.IdentityProfile, "IdentityProfile repository is wired in NewRepositories")

	// A round-trip through the aggregate confirms it is usable end-to-end.
	personRepo := repos.Person
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	require.NoError(t, repos.IdentityProfile.MarkDirty([]uint{person.ID}, "via_aggregate"))
	dirty, err := repos.IdentityProfile.ListDirty(0, 10)
	require.NoError(t, err)
	require.Len(t, dirty, 1)
	assert.Equal(t, person.ID, dirty[0].PersonID)
}
