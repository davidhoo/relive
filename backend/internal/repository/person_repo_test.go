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
	// 仅来源人物涉及的照片需要重算；仅含目标人物的照片（11）不返回。
	assert.ElementsMatch(t, []uint{12}, affectedPhotoIDs)

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

// TestPersonRepository_MergeInto_OnlySourcePhotosAffected 覆盖单来源、多来源与照片重叠场景：
// 仅来源人物涉及的照片计入受影响集合；仅含目标人物的照片不返回；多个来源或来源与目标
// 出现在同一照片时去重。
func TestPersonRepository_MergeInto_OnlySourcePhotosAffected(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	faceRepo := NewFaceRepository(db)

	// 不同分类，验证重算范围与分类无关、仅按归属变化判定。
	target := &model.Person{Name: "Target", Category: model.PersonCategoryFamily}
	source1 := &model.Person{Name: "Source1", Category: model.PersonCategoryFriend}
	source2 := &model.Person{Name: "Source2", Category: model.PersonCategoryAcquaintance}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(source1))
	require.NoError(t, personRepo.Create(source2))

	const (
		photoTargetOnly uint = 100 // 仅目标人物 → 不应返回
		photoS1Only     uint = 101 // 仅来源1
		photoS2Only     uint = 102 // 仅来源2
		photoS1S2       uint = 103 // 来源1+来源2 同照 → 去重后一次
		photoTargetS1   uint = 104 // 目标+来源1 同照 → 含来源人脸，应返回
	)

	mkFace := func(photoID uint, personID uint) *model.Face {
		return &model.Face{
			PhotoID:  photoID,
			PersonID: &personID,
			BBoxX:    0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
			Confidence: 0.9, QualityScore: 0.8,
		}
	}
	require.NoError(t, faceRepo.Create(mkFace(photoTargetOnly, target.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoS1Only, source1.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoS2Only, source2.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoS1S2, source1.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoS1S2, source2.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoTargetS1, target.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoTargetS1, source1.ID)))

	affectedPhotoIDs, err := personRepo.MergeInto(target.ID, []uint{source1.ID, source2.ID})
	require.NoError(t, err)
	// 仅来源人物涉及的照片：101、102、103（去重）、104；100（仅目标）不返回。
	assert.ElementsMatch(t, []uint{photoS1Only, photoS2Only, photoS1S2, photoTargetS1}, affectedPhotoIDs)

	// 合并后所有来源人脸归属目标。目标原有 2 张人脸 + 5 张来源人脸 = 7。
	mergedFaces, err := faceRepo.ListByPersonID(target.ID)
	require.NoError(t, err)
	assert.Len(t, mergedFaces, 7)

	for _, sid := range []uint{source1.ID, source2.ID} {
		gone, err := personRepo.GetByID(sid)
		require.NoError(t, err)
		assert.Nil(t, gone)
	}
}

// TestPersonRepository_ListPeople_VisibilityFilter 验证可见性筛选与分类、搜索组合，
// 以及返回总数与筛选结果一致。
func TestPersonRepository_ListPeople_VisibilityFilter(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPersonRepository(db)

	visibleFamily := &model.Person{Name: "Alice", Category: model.PersonCategoryFamily}
	visibleFriend := &model.Person{Name: "Bob", Category: model.PersonCategoryFriend}
	hiddenFamily := &model.Person{Name: "Carol", Category: model.PersonCategoryFamily, Hidden: true}
	hiddenStranger := &model.Person{Name: "Dave", Category: model.PersonCategoryStranger, Hidden: true}
	for _, p := range []*model.Person{visibleFamily, visibleFriend, hiddenFamily, hiddenStranger} {
		require.NoError(t, repo.Create(p))
	}

	listOpts := func(visibility string) ListPeopleOptions {
		return ListPeopleOptions{Page: 1, PageSize: 100, Visibility: visibility}
	}

	// visible：仅 2 个显示中
	people, total, err := repo.ListPeople(listOpts(PersonVisibilityVisible))
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, people, 2)

	// hidden：仅 2 个已隐藏
	people, total, err = repo.ListPeople(listOpts(PersonVisibilityHidden))
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, people, 2)

	// all：全部 4 个
	people, total, err = repo.ListPeople(listOpts(PersonVisibilityAll))
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	assert.Len(t, people, 4)

	// 缺省按 all 处理
	people, total, err = repo.ListPeople(ListPeopleOptions{Page: 1, PageSize: 100})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)

	// 组合：hidden + category=family → 仅 Carol
	people, total, err = repo.ListPeople(ListPeopleOptions{Page: 1, PageSize: 100, Visibility: PersonVisibilityHidden, Category: model.PersonCategoryFamily})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, people, 1)
	assert.Equal(t, "Carol", people[0].Name)

	// 组合：visible + search="ali" → 仅 Alice
	people, total, err = repo.ListPeople(ListPeopleOptions{Page: 1, PageSize: 100, Visibility: PersonVisibilityVisible, Search: "ali"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, people, 1)
	assert.Equal(t, "Alice", people[0].Name)
}

// TestPersonRepository_UpdateVisibility 验证批量隐藏/恢复、去重、返回更新数。
func TestPersonRepository_UpdateVisibility(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPersonRepository(db)

	p1 := &model.Person{Name: "A", Category: model.PersonCategoryFamily}
	p2 := &model.Person{Name: "B", Category: model.PersonCategoryFriend}
	p3 := &model.Person{Name: "C", Category: model.PersonCategoryStranger}
	for _, p := range []*model.Person{p1, p2, p3} {
		require.NoError(t, repo.Create(p))
	}

	// 隐藏 p1、p2，并重复 p1 验证去重
	updated, err := repo.UpdateVisibility([]uint{p1.ID, p2.ID, p1.ID}, true)
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated)

	got1, err := repo.GetByID(p1.ID)
	require.NoError(t, err)
	assert.True(t, got1.Hidden)
	got3, err := repo.GetByID(p3.ID)
	require.NoError(t, err)
	assert.False(t, got3.Hidden)

	// 隐藏分类不应被修改
	assert.Equal(t, model.PersonCategoryFamily, got1.Category)

	// 恢复 p1
	updated, err = repo.UpdateVisibility([]uint{p1.ID}, false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), updated)
	got1, err = repo.GetByID(p1.ID)
	require.NoError(t, err)
	assert.False(t, got1.Hidden)

	// 空切片安全
	updated, err = repo.UpdateVisibility(nil, true)
	require.NoError(t, err)
	assert.Equal(t, int64(0), updated)

	// 全部不存在的 ID 返回 0（不报错）
	updated, err = repo.UpdateVisibility([]uint{999999}, true)
	require.NoError(t, err)
	assert.Equal(t, int64(0), updated)
}

// TestPersonRepository_MergeInto_PreservesTargetHidden 验证合并始终保留目标人物原有隐藏状态。
func TestPersonRepository_MergeInto_PreservesTargetHidden(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	faceRepo := NewFaceRepository(db)

	t.Run("hidden target stays hidden after merging visible source", func(t *testing.T) {
		target := &model.Person{Name: "Target", Category: model.PersonCategoryFamily, Hidden: true}
		source := &model.Person{Name: "Source", Category: model.PersonCategoryFriend, Hidden: false}
		require.NoError(t, personRepo.Create(target))
		require.NoError(t, personRepo.Create(source))
		require.NoError(t, faceRepo.Create(&model.Face{PhotoID: 1, PersonID: &source.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.9}))

		_, err := personRepo.MergeInto(target.ID, []uint{source.ID})
		require.NoError(t, err)

		targetAfter, err := personRepo.GetByID(target.ID)
		require.NoError(t, err)
		require.NotNil(t, targetAfter)
		assert.True(t, targetAfter.Hidden, "hidden target must stay hidden after merge")
	})

	t.Run("visible target stays visible after merging hidden source", func(t *testing.T) {
		target := &model.Person{Name: "Target2", Category: model.PersonCategoryFamily, Hidden: false}
		source := &model.Person{Name: "Source2", Category: model.PersonCategoryFriend, Hidden: true}
		require.NoError(t, personRepo.Create(target))
		require.NoError(t, personRepo.Create(source))
		require.NoError(t, faceRepo.Create(&model.Face{PhotoID: 2, PersonID: &source.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.9}))

		_, err := personRepo.MergeInto(target.ID, []uint{source.ID})
		require.NoError(t, err)

		targetAfter, err := personRepo.GetByID(target.ID)
		require.NoError(t, err)
		require.NotNil(t, targetAfter)
		assert.False(t, targetAfter.Hidden, "visible target must stay visible after merge")
	})
}

// --- Hidden person filtering tests ---

func TestPersonRepo_ListHiddenPersonIDs(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPersonRepository(db)

	visible := &model.Person{Category: model.PersonCategoryFriend}
	hidden1 := &model.Person{Category: model.PersonCategoryFriend, Hidden: true}
	hidden2 := &model.Person{Category: model.PersonCategoryStranger, Hidden: true}
	require.NoError(t, repo.Create(visible))
	require.NoError(t, repo.Create(hidden1))
	require.NoError(t, repo.Create(hidden2))

	ids, err := repo.ListHiddenPersonIDs()
	require.NoError(t, err)
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, hidden1.ID)
	assert.Contains(t, ids, hidden2.ID)
	assert.NotContains(t, ids, visible.ID)
}

func TestPersonRepo_HiddenFilteredFromMergeSuggestionTargets(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPersonRepository(db)

	visible := &model.Person{Category: model.PersonCategoryFamily, FaceCount: 1}
	hidden := &model.Person{Category: model.PersonCategoryFamily, FaceCount: 1, Hidden: true}
	require.NoError(t, repo.Create(visible))
	require.NoError(t, repo.Create(hidden))

	targets, err := repo.ListMergeSuggestionTargets(0, 100)
	require.NoError(t, err)
	ids := make(map[uint]bool)
	for _, p := range targets {
		ids[p.ID] = true
	}
	assert.True(t, ids[visible.ID])
	assert.False(t, ids[hidden.ID])
}

func TestPersonRepo_HiddenFilteredFromListWithAvatar(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPersonRepository(db)

	visible := &model.Person{Category: model.PersonCategoryFriend, RepresentativeFaceID: uintPtrPerson(1)}
	hidden := &model.Person{Category: model.PersonCategoryFriend, RepresentativeFaceID: uintPtrPerson(2), Hidden: true}
	require.NoError(t, repo.Create(visible))
	require.NoError(t, repo.Create(hidden))

	people, err := repo.ListWithAvatar()
	require.NoError(t, err)
	ids := make(map[uint]bool)
	for _, p := range people {
		ids[p.ID] = true
	}
	assert.True(t, ids[visible.ID])
	assert.False(t, ids[hidden.ID])
}

func TestPersonRepo_HiddenFilteredListByNameExact(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPersonRepository(db)

	visible := &model.Person{Name: "Alice", Category: model.PersonCategoryFriend}
	hidden := &model.Person{Name: "Alice", Category: model.PersonCategoryFriend, Hidden: true}
	require.NoError(t, repo.Create(visible))
	require.NoError(t, repo.Create(hidden))

	people, err := repo.ListByNameExact("Alice")
	require.NoError(t, err)
	ids := make(map[uint]bool)
	for _, p := range people {
		ids[p.ID] = true
	}
	assert.True(t, ids[visible.ID])
	assert.False(t, ids[hidden.ID])
}

func TestPersonRepo_RestoreReappearsInCandidates(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPersonRepository(db)

	person := &model.Person{Name: "Bob", Category: model.PersonCategoryFamily, FaceCount: 1, Hidden: true}
	require.NoError(t, repo.Create(person))

	// Hidden: should not appear.
	targets, err := repo.ListMergeSuggestionTargets(0, 100)
	require.NoError(t, err)
	for _, p := range targets {
		assert.NotEqual(t, person.ID, p.ID)
	}

	// Restore.
	_, err = repo.UpdateVisibility([]uint{person.ID}, false)
	require.NoError(t, err)

	// Should reappear.
	targets, err = repo.ListMergeSuggestionTargets(0, 100)
	require.NoError(t, err)
	found := false
	for _, p := range targets {
		if p.ID == person.ID {
			found = true
		}
	}
	assert.True(t, found, "restored person should reappear in merge suggestion targets")
}

func uintPtrPerson(v uint) *uint { return &v }
