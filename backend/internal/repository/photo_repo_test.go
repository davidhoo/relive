package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPhotoRepository_Create(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	// 创建测试照片
	now := time.Now()
	photo := &model.Photo{
		FilePath:    "/test/photos/IMG_0001.jpg",
		FileName:    "IMG_0001.jpg",
		FileSize:    1024000,
		FileHash:    "abc123",
		TakenAt:     &now,
		Width:       1920,
		Height:      1080,
		MemoryScore: 85,
		BeautyScore: 90,
	}

	// 执行创建
	err := repo.Create(photo)

	// 验证
	assert.NoError(t, err)
	assert.NotZero(t, photo.ID)
	assert.Equal(t, 86, photo.OverallScore) // 85*0.7 + 90*0.3 = 59.5 + 27 = 86.5 ≈ 86
}

func TestPhotoRepository_GetByFilePath(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	// 插入测试数据
	photo := &model.Photo{
		FilePath: "/test/photos/IMG_0001.jpg",
		FileName: "IMG_0001.jpg",
		FileSize: 1024000,
		FileHash: "abc123",
		Width:    1920,
		Height:   1080,
	}
	repo.Create(photo)

	// 查询
	found, err := repo.GetByFilePath("/test/photos/IMG_0001.jpg")

	// 验证
	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, photo.ID, found.ID)
	assert.Equal(t, "/test/photos/IMG_0001.jpg", found.FilePath)
}

func TestPhotoRepository_GetByFileHash(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	// 插入测试数据
	photo := &model.Photo{
		FilePath: "/test/photos/IMG_0001.jpg",
		FileName: "IMG_0001.jpg",
		FileSize: 1024000,
		FileHash: "unique-hash-123",
		Width:    1920,
		Height:   1080,
	}
	repo.Create(photo)

	// 查询
	found, err := repo.GetByFileHash("unique-hash-123")

	// 验证
	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, photo.ID, found.ID)
}

func TestPhotoRepository_List(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	// 插入测试数据
	for i := 0; i < 15; i++ {
		photo := &model.Photo{
			FilePath:    "/test/photos/IMG_" + string(rune(i)) + ".jpg",
			FileName:    "IMG_" + string(rune(i)) + ".jpg",
			FileSize:    1024000,
			FileHash:    "hash" + string(rune(i)),
			Width:       1920,
			Height:      1080,
			AIAnalyzed:  i%2 == 0, // 偶数索引已分析
			MemoryScore: 80 + i,
			BeautyScore: 85 + i,
		}
		repo.Create(photo)
	}

	// 测试分页
	photos, total, err := repo.List(1, 10, nil, nil, nil, "", "", "", "", "overall_score", true, nil, "")

	// 验证
	assert.NoError(t, err)
	assert.Equal(t, int64(15), total)
	assert.Equal(t, 10, len(photos))

	// 测试筛选已分析
	analyzed := true
	photos, total, err = repo.List(1, 10, &analyzed, nil, nil, "", "", "", "", "overall_score", true, nil, "")
	assert.NoError(t, err)
	assert.Equal(t, int64(8), total) // 8 个已分析（0,2,4,6,8,10,12,14）
}

func TestPhotoRepository_MarkAsAnalyzed(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	// 插入测试数据
	photo := &model.Photo{
		FilePath:   "/test/photos/IMG_0001.jpg",
		FileName:   "IMG_0001.jpg",
		FileSize:   1024000,
		FileHash:   "abc123",
		Width:      1920,
		Height:     1080,
		AIAnalyzed: false,
	}
	repo.Create(photo)

	// 标记为已分析
	description := "这是一张美丽的风景照片"
	caption := "日落时分的海滩"
	mainCategory := "landscape"
	tags := "sunset,beach,ocean"
	memoryScore := 95
	beautyScore := 88

	err := repo.MarkAsAnalyzed(photo.ID, description, caption, mainCategory, tags, memoryScore, beautyScore)
	assert.NoError(t, err)

	// 验证
	updated, _ := repo.GetByID(photo.ID)
	assert.True(t, updated.AIAnalyzed)
	assert.NotNil(t, updated.AnalyzedAt)
	assert.Equal(t, description, updated.Description)
	assert.Equal(t, memoryScore, updated.MemoryScore)
	assert.Equal(t, beautyScore, updated.BeautyScore)
	assert.Equal(t, mainCategory, updated.MainCategory)
	assert.Equal(t, tags, updated.Tags)
	// 验证综合评分计算：70% memory + 30% beauty
	expectedOverallScore := model.CalcOverallScore(memoryScore, beautyScore)
	assert.Equal(t, expectedOverallScore, updated.OverallScore)
}

func TestPhotoRepository_GetUnanalyzed(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	// 插入测试数据
	for i := 0; i < 10; i++ {
		photo := &model.Photo{
			FilePath:        "/test/photos/IMG_" + string(rune(i)) + ".jpg",
			FileName:        "IMG_" + string(rune(i)) + ".jpg",
			FileSize:        1024000,
			FileHash:        "hash" + string(rune(i)),
			Width:           1920,
			Height:          1080,
			ThumbnailStatus: model.ThumbnailStatusReady,
			GeocodeStatus:   model.GeocodeStatusNone,
			AIAnalyzed:      i >= 5, // 前 5 个未分析
		}
		repo.Create(photo)
	}

	// 获取未分析照片
	photos, err := repo.GetUnanalyzed(3)

	// 验证
	assert.NoError(t, err)
	assert.Equal(t, 3, len(photos))
	for _, photo := range photos {
		assert.False(t, photo.AIAnalyzed)
	}
}

func TestPhotoRepository_ListByPathPrefix_RespectsDirectoryBoundary(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	photos := []*model.Photo{
		{FilePath: "/photos/trip/a.jpg", FileName: "a.jpg", FileSize: 1, FileHash: "hash-a", Width: 100, Height: 100},
		{FilePath: "/photos/trip/day1/b.jpg", FileName: "b.jpg", FileSize: 1, FileHash: "hash-b", Width: 100, Height: 100},
		{FilePath: "/photos/trip-old/c.jpg", FileName: "c.jpg", FileSize: 1, FileHash: "hash-c", Width: 100, Height: 100},
	}

	for _, photo := range photos {
		assert.NoError(t, repo.Create(photo))
	}

	matched, err := repo.ListByPathPrefix("/photos/trip")
	assert.NoError(t, err)
	assert.Len(t, matched, 2)

	count, err := repo.CountByPathPrefix("/photos/trip")
	assert.NoError(t, err)
	assert.Equal(t, int64(2), count)

	filtered, total, err := repo.List(1, 10, nil, nil, nil, "", "", "", "", "id", false, []string{"/photos/trip"}, "")
	assert.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, filtered, 2)

	for _, photo := range filtered {
		assert.NotContains(t, photo.FilePath, "/photos/trip-old/")
	}
}

func TestPhotoRepository_List_WithNoEnabledPaths_ReturnsEmpty(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)
	photo := &model.Photo{
		FilePath: "/photos/trip/a.jpg",
		FileName: "a.jpg",
		FileSize: 1,
		FileHash: "hash-a",
		Width:    100,
		Height:   100,
	}
	assert.NoError(t, repo.Create(photo))

	items, total, err := repo.List(1, 10, nil, nil, nil, "", "", "", "", "id", false, []string{}, "")
	assert.NoError(t, err)
	assert.Empty(t, items)
	assert.Equal(t, int64(0), total)

	items, total, err = repo.List(1, 10, nil, nil, nil, "", "", "", "", "id", false, nil, "")
	assert.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, int64(1), total)
}

func TestPhotoRepository_BatchCreate(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	// 准备批量数据
	photos := make([]*model.Photo, 100)
	for i := 0; i < 100; i++ {
		photos[i] = &model.Photo{
			FilePath: "/test/photos/IMG_" + string(rune(i)) + ".jpg",
			FileName: "IMG_" + string(rune(i)) + ".jpg",
			FileSize: 1024000,
			FileHash: "hash" + string(rune(i)),
			Width:    1920,
			Height:   1080,
		}
	}

	// 批量创建
	err := repo.BatchCreate(photos, 50)

	// 验证
	assert.NoError(t, err)

	count, _ := repo.Count()
	assert.Equal(t, int64(100), count)
}

// TestPhotoRepository_ListPhotoSummariesByPersonID_IncludesVersionFields 验证人物详情页分页查询
// 返回 updated_at / thumbnail_generated_at / manual_rotation，前端用这些字段构造版本化缩略图 URL。
// 缺失会导致版本参数固定（Go 零值 time），配合 immutable 长缓存，旋转/重建后长期展示旧图。
func TestPhotoRepository_ListPhotoSummariesByPersonID_IncludesVersionFields(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	photoRepo := NewPhotoRepository(db)
	faceRepo := NewFaceRepository(db)

	photo := &model.Photo{
		FilePath:             "/photos/ver.jpg",
		FileName:             "ver.jpg",
		FileSize:             1,
		FileHash:             "hash-ver",
		Width:                400,
		Height:               300,
		ManualRotation:       90,
		ThumbnailStatus:      model.ThumbnailStatusReady,
	}
	require.NoError(t, photoRepo.Create(photo))

	person := &model.Person{Name: "P", Category: model.PersonCategoryFamily, FaceCount: 1}
	require.NoError(t, db.Create(person).Error)

	face := &model.Face{PhotoID: photo.ID, PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.8}
	require.NoError(t, faceRepo.Create(face))

	items, total, err := photoRepo.ListPhotoSummariesByPersonIDPaginated(person.ID, 1, 30)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, items, 1)

	got := items[0]
	// updated_at 必须为真实时间（非 Go 零值 0001-01-01）
	assert.False(t, got.UpdatedAt.IsZero(), "updated_at must be selected for version param")
	assert.Equal(t, 90, got.ManualRotation, "manual_rotation must be selected")
	// thumbnail_generated_at 可为 nil（未生成时），但字段需可读不报错
	_ = got.ThumbnailGeneratedAt
}

// TestPhotoRepository_ListPhotoSummariesByPersonIDCursor covers keyset pagination
// for person photos: dedup, stable ordering, NULL taken_at, has_more, no COUNT.
func TestPhotoRepository_ListPhotoSummariesByPersonIDCursor(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	photoRepo := NewPhotoRepository(db)
	faceRepo := NewFaceRepository(db)

	// Create a person
	person := &model.Person{Name: "Test", Category: model.PersonCategoryFamily, FaceCount: 0}
	require.NoError(t, db.Create(person).Error)

	// Create photos with varying taken_at, including NULL
	// Sort order is taken_at DESC, id DESC. NULLs sort last.
	t1 := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 3, 10, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 3, 5, 10, 0, 0, 0, time.UTC)

	photos := []*model.Photo{
		{FilePath: "/p1.jpg", FileName: "p1.jpg", FileSize: 1, FileHash: "h1", TakenAt: &t1},
		{FilePath: "/p2.jpg", FileName: "p2.jpg", FileSize: 1, FileHash: "h2", TakenAt: &t2},
		{FilePath: "/p3.jpg", FileName: "p3.jpg", FileSize: 1, FileHash: "h3", TakenAt: &t3},
		{FilePath: "/p4.jpg", FileName: "p4.jpg", FileSize: 1, FileHash: "h4", TakenAt: nil}, // NULL zone
		{FilePath: "/p5.jpg", FileName: "p5.jpg", FileSize: 1, FileHash: "h5", TakenAt: nil}, // NULL zone
	}
	for _, p := range photos {
		require.NoError(t, photoRepo.Create(p))
	}

	// Create faces: photo 1 has 2 faces (dedup test), others have 1
	for i, p := range photos {
		face1 := &model.Face{PhotoID: p.ID, PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.8}
		require.NoError(t, faceRepo.Create(face1))
		if i == 0 {
			// Second face on same photo — should not produce duplicate photo
			face2 := &model.Face{PhotoID: p.ID, PersonID: &person.ID, Confidence: 0.8, QualityScore: 0.7}
			require.NoError(t, faceRepo.Create(face2))
		}
	}

	// Page 1: limit=2, should get p1 (t1) and p2 (t2)
	items, hasMore, nextCursor, err := photoRepo.ListPhotoSummariesByPersonIDCursor(person.ID, nil, 2)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.True(t, hasMore)
	require.NotNil(t, nextCursor)
	assert.Equal(t, photos[1].ID, nextCursor.ID) // last item is p2

	// Page 2: cursor from p2, should get p3 (t3) and p4 (NULL)
	items, hasMore, nextCursor, err = photoRepo.ListPhotoSummariesByPersonIDCursor(person.ID, nextCursor, 2)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.True(t, hasMore)
	require.NotNil(t, nextCursor)
	// NULL zone: id DESC means p5 (ID=5) before p4 (ID=4)
	assert.Nil(t, nextCursor.TakenAt) // p5 has NULL taken_at, so cursor enters NULL zone
	assert.Equal(t, photos[4].ID, nextCursor.ID)

	// Page 3: cursor from p5 (NULL zone), should get p4 only
	items, hasMore, nextCursor, err = photoRepo.ListPhotoSummariesByPersonIDCursor(person.ID, nextCursor, 2)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.False(t, hasMore)
	assert.Nil(t, nextCursor)
}

// TestPhotoRepository_ListPhotoSummariesByPersonIDCursor_SameTakenAt uses multiple photos
// with identical taken_at to verify id DESC tiebreaker has no duplicates or gaps.
func TestPhotoRepository_ListPhotoSummariesByPersonIDCursor_SameTakenAt(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	photoRepo := NewPhotoRepository(db)
	faceRepo := NewFaceRepository(db)

	person := &model.Person{Name: "T", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	sameTime := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)

	var photoIDs []uint
	for i := 0; i < 5; i++ {
		p := &model.Photo{
			FilePath: fmt.Sprintf("/same_%d.jpg", i),
			FileName: fmt.Sprintf("same_%d.jpg", i),
			FileSize: 1,
			FileHash: fmt.Sprintf("hash_same_%d", i),
			TakenAt:  &sameTime,
		}
		require.NoError(t, photoRepo.Create(p))
		photoIDs = append(photoIDs, p.ID)
		face := &model.Face{PhotoID: p.ID, PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.8}
		require.NoError(t, faceRepo.Create(face))
	}

	// Expected order: id DESC (since same taken_at)
	// Page 1: limit=2 → ids[4], ids[3]
	items, hasMore, nextCursor, err := photoRepo.ListPhotoSummariesByPersonIDCursor(person.ID, nil, 2)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.True(t, hasMore)
	assert.Equal(t, photoIDs[4], items[0].ID)
	assert.Equal(t, photoIDs[3], items[1].ID)

	// Page 2: → ids[2], ids[1]
	items, hasMore, nextCursor, err = photoRepo.ListPhotoSummariesByPersonIDCursor(person.ID, nextCursor, 2)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.True(t, hasMore)
	assert.Equal(t, photoIDs[2], items[0].ID)
	assert.Equal(t, photoIDs[1], items[1].ID)

	// Page 3: → ids[0] only
	items, hasMore, _, err = photoRepo.ListPhotoSummariesByPersonIDCursor(person.ID, nextCursor, 2)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.False(t, hasMore)
	assert.Equal(t, photoIDs[0], items[0].ID)
}

// TestPhotoRepository_ListPhotoSummariesByPersonIDCursor_Empty verifies empty result set.
func TestPhotoRepository_ListPhotoSummariesByPersonIDCursor_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	photoRepo := NewPhotoRepository(db)
	person := &model.Person{Name: "Empty", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	items, hasMore, nextCursor, err := photoRepo.ListPhotoSummariesByPersonIDCursor(person.ID, nil, 30)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.False(t, hasMore)
	assert.Nil(t, nextCursor)
}

// TestPhotoRepository_ListPhotoSummariesByPersonIDCursor_OldPageModeStillWorks
// verifies backward compatibility: the old paginated method still returns correct results.
func TestPhotoRepository_ListPhotoSummariesByPersonIDCursor_OldPageModeStillWorks(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	photoRepo := NewPhotoRepository(db)
	faceRepo := NewFaceRepository(db)

	person := &model.Person{Name: "Compat", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	t1 := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	p := &model.Photo{FilePath: "/compat.jpg", FileName: "compat.jpg", FileSize: 1, FileHash: "hc", TakenAt: &t1}
	require.NoError(t, photoRepo.Create(p))
	face := &model.Face{PhotoID: p.ID, PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.8}
	require.NoError(t, faceRepo.Create(face))

	// Old method still works
	items, total, err := photoRepo.ListPhotoSummariesByPersonIDPaginated(person.ID, 1, 30)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, items, 1)
}

// TestFacePersonPhotoIndex_ColumnOrder verifies the (person_id, photo_id) composite index
// exists with person_id as the leading column, so a WHERE faces.person_id = ? predicate on
// the cursor query seeks the index instead of scanning all faces. Regression guard for the
// GORM priority-tag ordering (priority values are arbitrary sorting keys, NOT column order —
// the struct field declaration order determines the index column order).
func TestFacePersonPhotoIndex_ColumnOrder(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	type idxCol struct {
		Name string
		Cid  int
	}
	var cols []idxCol
	require.NoError(t, db.Raw(`SELECT name, cid FROM pragma_index_info('idx_face_person_photo') ORDER BY cid`).Scan(&cols).Error)
	require.Len(t, cols, 2, "idx_face_person_photo must have exactly 2 columns")
	assert.Equal(t, "person_id", cols[0].Name, "person_id must be the leading column for person lookups")
	assert.Equal(t, "photo_id", cols[1].Name, "photo_id must be the second column")
}

// TestPhotoRepository_UpdateManualRotation_BumpsUpdatedAt 验证旋转后 updated_at 刷新，
// 使前端 ?v=updated_at 版本参数变化，旧的 immutable 缓存失效。
func TestPhotoRepository_UpdateManualRotation_BumpsUpdatedAt(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)
	photo := &model.Photo{
		FilePath:       "/photos/rot.jpg",
		FileName:       "rot.jpg",
		FileSize:       1,
		FileHash:       "hash-rot",
		Width:          400,
		Height:         300,
		ManualRotation: 0,
	}
	require.NoError(t, repo.Create(photo))
	before := photo.UpdatedAt

	// GORM UpdatedAt 在 Create 时自动填充，确保 before 非零
	require.False(t, before.IsZero())

	// 旋转
	time.Sleep(10 * time.Millisecond) // 确保 time.Now() 推进
	require.NoError(t, repo.UpdateManualRotation(photo.ID, 90))

	reloaded, err := repo.GetByID(photo.ID)
	require.NoError(t, err)
	assert.Equal(t, 90, reloaded.ManualRotation)
	assert.True(t, reloaded.UpdatedAt.After(before), "updated_at must advance after rotation for cache invalidation")
}

func TestPhotoRepository_GetOnThisDayCandidates(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	// 创建不同年份、相同月日附近的照片
	takenAt1 := time.Date(2024, 3, 6, 10, 0, 0, 0, time.Local)  // 3月6日
	takenAt2 := time.Date(2023, 3, 8, 10, 0, 0, 0, time.Local)  // 3月8日（±3天窗口内）
	takenAt3 := time.Date(2022, 3, 20, 10, 0, 0, 0, time.Local) // 3月20日（±3天窗口外）
	takenAt4 := time.Date(2021, 3, 5, 10, 0, 0, 0, time.Local)  // 3月5日（低分）

	testPhotos := []*model.Photo{
		{FilePath: "/p1.jpg", FileName: "p1.jpg", FileSize: 1, FileHash: "h1", Width: 100, Height: 100, TakenAt: &takenAt1, AIAnalyzed: true, MemoryScore: 80, BeautyScore: 80, OverallScore: 80},
		{FilePath: "/p2.jpg", FileName: "p2.jpg", FileSize: 1, FileHash: "h2", Width: 100, Height: 100, TakenAt: &takenAt2, AIAnalyzed: true, MemoryScore: 75, BeautyScore: 75, OverallScore: 75},
		{FilePath: "/p3.jpg", FileName: "p3.jpg", FileSize: 1, FileHash: "h3", Width: 100, Height: 100, TakenAt: &takenAt3, AIAnalyzed: true, MemoryScore: 90, BeautyScore: 90, OverallScore: 90},
		{FilePath: "/p4.jpg", FileName: "p4.jpg", FileSize: 1, FileHash: "h4", Width: 100, Height: 100, TakenAt: &takenAt4, AIAnalyzed: true, MemoryScore: 50, BeautyScore: 50, OverallScore: 50},
		{FilePath: "/p5.jpg", FileName: "p5.jpg", FileSize: 1, FileHash: "h5", Width: 100, Height: 100, TakenAt: &takenAt1, AIAnalyzed: false, MemoryScore: 90, BeautyScore: 90, OverallScore: 90}, // 未分析
	}
	for _, p := range testPhotos {
		assert.NoError(t, repo.Create(p))
	}

	// ±3天窗口: 03-03 到 03-09
	photos, err := repo.GetOnThisDayCandidates("03-03", "03-09", 70, 70, nil, 10)
	assert.NoError(t, err)
	assert.Len(t, photos, 2) // p1(03-06) 和 p2(03-08)，p4 分数不够，p5 未分析

	// 验证按 overall_score DESC 排序
	assert.Equal(t, 80, photos[0].OverallScore)
	assert.Equal(t, 75, photos[1].OverallScore)

	// 测试 excludeIDs
	photos, err = repo.GetOnThisDayCandidates("03-03", "03-09", 70, 70, []uint{testPhotos[0].ID}, 10)
	assert.NoError(t, err)
	assert.Len(t, photos, 1) // 排除了 p1

	// 测试 limit
	photos, err = repo.GetOnThisDayCandidates("03-03", "03-09", 70, 70, nil, 1)
	assert.NoError(t, err)
	assert.Len(t, photos, 1)
}

func TestPhotoRepository_GetOnThisDayCandidates_CrossYear(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	dec30 := time.Date(2024, 12, 30, 10, 0, 0, 0, time.Local)
	jan02 := time.Date(2023, 1, 2, 10, 0, 0, 0, time.Local)
	jun15 := time.Date(2024, 6, 15, 10, 0, 0, 0, time.Local)

	testPhotos := []*model.Photo{
		{FilePath: "/d1.jpg", FileName: "d1.jpg", FileSize: 1, FileHash: "dh1", Width: 100, Height: 100, TakenAt: &dec30, AIAnalyzed: true, MemoryScore: 80, BeautyScore: 80, OverallScore: 80},
		{FilePath: "/d2.jpg", FileName: "d2.jpg", FileSize: 1, FileHash: "dh2", Width: 100, Height: 100, TakenAt: &jan02, AIAnalyzed: true, MemoryScore: 75, BeautyScore: 75, OverallScore: 75},
		{FilePath: "/d3.jpg", FileName: "d3.jpg", FileSize: 1, FileHash: "dh3", Width: 100, Height: 100, TakenAt: &jun15, AIAnalyzed: true, MemoryScore: 90, BeautyScore: 90, OverallScore: 90},
	}
	for _, p := range testPhotos {
		assert.NoError(t, repo.Create(p))
	}

	// 跨年窗口: 12-28 到 01-04（monthDayStart > monthDayEnd）
	photos, err := repo.GetOnThisDayCandidates("12-28", "01-04", 70, 70, nil, 10)
	assert.NoError(t, err)
	assert.Len(t, photos, 2) // dec30 和 jan02，不含 jun15
}

func TestPhotoRepository_GetTopScoredCandidates(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	takenAt := time.Date(2024, 6, 1, 10, 0, 0, 0, time.Local)
	testPhotos := []*model.Photo{
		{FilePath: "/t1.jpg", FileName: "t1.jpg", FileSize: 1, FileHash: "th1", Width: 100, Height: 100, TakenAt: &takenAt, AIAnalyzed: true, MemoryScore: 90, BeautyScore: 90},
		{FilePath: "/t2.jpg", FileName: "t2.jpg", FileSize: 1, FileHash: "th2", Width: 100, Height: 100, TakenAt: &takenAt, AIAnalyzed: true, MemoryScore: 80, BeautyScore: 80},
		{FilePath: "/t3.jpg", FileName: "t3.jpg", FileSize: 1, FileHash: "th3", Width: 100, Height: 100, TakenAt: &takenAt, AIAnalyzed: true, MemoryScore: 50, BeautyScore: 50},
		{FilePath: "/t4.jpg", FileName: "t4.jpg", FileSize: 1, FileHash: "th4", Width: 100, Height: 100, TakenAt: &takenAt, AIAnalyzed: false, MemoryScore: 95, BeautyScore: 95},
	}
	for _, p := range testPhotos {
		assert.NoError(t, repo.Create(p))
	}

	// 带阈值
	photos, err := repo.GetTopScoredCandidates(70, 70, nil, 10)
	assert.NoError(t, err)
	assert.Len(t, photos, 2) // t1 和 t2（t3 分数不够，t4 未分析）
	assert.True(t, photos[0].OverallScore >= photos[1].OverallScore, "should be sorted by overall_score DESC")

	// 带 excludeIDs
	photos, err = repo.GetTopScoredCandidates(70, 70, []uint{testPhotos[0].ID}, 10)
	assert.NoError(t, err)
	assert.Len(t, photos, 1)

	// 带 limit
	photos, err = repo.GetTopScoredCandidates(0, 0, nil, 2)
	assert.NoError(t, err)
	assert.Len(t, photos, 2)
}

func TestPhotoRepositoryRecomputeTopPersonCategory(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	photoRepo := NewPhotoRepository(db)
	personRepo := NewPersonRepository(db)
	faceRepo := NewFaceRepository(db)

	photos := []*model.Photo{
		{FilePath: "/photos/family.jpg", FileName: "family.jpg", FileSize: 1, FileHash: "hash-family", Width: 100, Height: 100},
		{FilePath: "/photos/friend.jpg", FileName: "friend.jpg", FileSize: 1, FileHash: "hash-friend", Width: 100, Height: 100},
		{FilePath: "/photos/empty.jpg", FileName: "empty.jpg", FileSize: 1, FileHash: "hash-empty", Width: 100, Height: 100},
	}
	for _, photo := range photos {
		require.NoError(t, photoRepo.Create(photo))
	}

	family := &model.Person{Category: model.PersonCategoryFamily}
	stranger := &model.Person{Category: model.PersonCategoryStranger}
	friend := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(family))
	require.NoError(t, personRepo.Create(stranger))
	require.NoError(t, personRepo.Create(friend))

	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      photos[0].ID,
		PersonID:     &stranger.ID,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.9,
		QualityScore: 0.8,
	}))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      photos[0].ID,
		PersonID:     &family.ID,
		BBoxX:        0.4,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.95,
		QualityScore: 0.9,
	}))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      photos[1].ID,
		PersonID:     &friend.ID,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.92,
		QualityScore: 0.85,
	}))

	require.NoError(t, photoRepo.RecomputeTopPersonCategory([]uint{photos[0].ID, photos[1].ID, photos[2].ID}))

	updatedFamily, err := photoRepo.GetByID(photos[0].ID)
	require.NoError(t, err)
	assert.Equal(t, model.PersonCategoryFamily, updatedFamily.TopPersonCategory)

	updatedFriend, err := photoRepo.GetByID(photos[1].ID)
	require.NoError(t, err)
	assert.Equal(t, model.PersonCategoryFriend, updatedFriend.TopPersonCategory)

	updatedEmpty, err := photoRepo.GetByID(photos[2].ID)
	require.NoError(t, err)
	assert.Equal(t, "", updatedEmpty.TopPersonCategory)
}

// TestPhotoRepositoryRecomputeTopPersonCategory_MultiBatch 覆盖跨批次批量更新：照片数
// 超过单批次上限（50）时，分多条 SQL 写入且全部提交，结果与逐张逻辑一致。
func TestPhotoRepositoryRecomputeTopPersonCategory_MultiBatch(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	photoRepo := NewPhotoRepository(db)
	personRepo := NewPersonRepository(db)
	faceRepo := NewFaceRepository(db)

	const total = 75 // 跨两个批次（50 + 25）
	photos := make([]*model.Photo, 0, total)
	for i := 0; i < total; i++ {
		p := &model.Photo{
			FilePath: fmt.Sprintf("/photos/%d.jpg", i),
			FileName: fmt.Sprintf("%d.jpg", i), FileSize: 1, FileHash: fmt.Sprintf("h%d", i),
			Width: 100, Height: 100,
		}
		require.NoError(t, photoRepo.Create(p))
		photos = append(photos, p)
	}

	// 不同分类交替，验证 top_person_category 与 face_count 在各批次都正确。
	categories := []string{
		model.PersonCategoryFamily,
		model.PersonCategoryFriend,
		model.PersonCategoryAcquaintance,
		model.PersonCategoryStranger,
	}
	persons := make([]*model.Person, len(categories))
	for i, cat := range categories {
		persons[i] = &model.Person{Category: cat}
		require.NoError(t, personRepo.Create(persons[i]))
	}
	for i, p := range photos {
		require.NoError(t, faceRepo.Create(&model.Face{
			PhotoID: p.ID, PersonID: &persons[i%len(categories)].ID,
			BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
			Confidence: 0.9, QualityScore: 0.8,
		}))
		// 部分照片额外多一张同人物人脸，验证 face_count 聚合。
		if i%2 == 0 {
			require.NoError(t, faceRepo.Create(&model.Face{
				PhotoID: p.ID, PersonID: &persons[i%len(categories)].ID,
				BBoxX: 0.4, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
				Confidence: 0.9, QualityScore: 0.8,
			}))
		}
	}

	before := make(map[uint]time.Time, len(photos))
	for _, p := range photos {
		got, err := photoRepo.GetByID(p.ID)
		require.NoError(t, err)
		before[p.ID] = got.UpdatedAt
	}

	allIDs := make([]uint, len(photos))
	for i, p := range photos {
		allIDs[i] = p.ID
	}
	require.NoError(t, photoRepo.RecomputeTopPersonCategory(allIDs))

	for i, p := range photos {
		got, err := photoRepo.GetByID(p.ID)
		require.NoError(t, err)
		expectedFaceCount := 1
		if i%2 == 0 {
			expectedFaceCount = 2
		}
		assert.Equal(t, expectedFaceCount, got.FaceCount, "photo %d face_count", i)
		assert.Equal(t, categories[i%len(categories)], got.TopPersonCategory, "photo %d top_person_category", i)
		assert.True(t, got.UpdatedAt.After(before[p.ID]), "photo %d updated_at should advance", i)
	}
}

// TestPhotoRepositoryRecomputeTopPersonCategory_Rollback 验证事务性：当某个批次的
// UPDATE 失败时，此前已执行的批次更新完整回滚，所有照片数据及 updated_at 不变。
func TestPhotoRepositoryRecomputeTopPersonCategory_Rollback(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	photoRepo := NewPhotoRepository(db)
	personRepo := NewPersonRepository(db)
	faceRepo := NewFaceRepository(db)

	const total = 55 // 第一批 50 张，第二批 5 张
	photos := make([]*model.Photo, 0, total)
	for i := 0; i < total; i++ {
		p := &model.Photo{
			FilePath: fmt.Sprintf("/photos/r%d.jpg", i),
			FileName: fmt.Sprintf("r%d.jpg", i), FileSize: 1, FileHash: fmt.Sprintf("rh%d", i),
			Width: 100, Height: 100,
		}
		require.NoError(t, photoRepo.Create(p))
		photos = append(photos, p)
	}
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	for _, p := range photos {
		require.NoError(t, faceRepo.Create(&model.Face{
			PhotoID: p.ID, PersonID: &person.ID,
			BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
			Confidence: 0.9, QualityScore: 0.8,
		}))
	}

	// 记录回滚前的快照。
	snapshot := make(map[uint]*model.Photo, len(photos))
	for _, p := range photos {
		got, err := photoRepo.GetByID(p.ID)
		require.NoError(t, err)
		snapshot[p.ID] = got
	}

	// 在第二批首张照片（第 51 张）上挂一个触发器，使其 UPDATE 触发 ABORT，
	// 迫使事务整体回滚（第一批 50 张的写入必须随之撤销）。SQLite 触发器不允许
	// 使用绑定变量，因此直接内联整数 ID。
	triggerPhotoID := photos[50].ID
	triggerSQL := fmt.Sprintf(
		`CREATE TRIGGER trg_recompute_rollback AFTER UPDATE ON photos `+
			`WHEN NEW.id = %d BEGIN SELECT RAISE(ABORT, 'forced failure'); END`,
		triggerPhotoID,
	)
	require.NoError(t, db.Exec(triggerSQL).Error)

	allIDs := make([]uint, len(photos))
	for i, p := range photos {
		allIDs[i] = p.ID
	}
	err := photoRepo.RecomputeTopPersonCategory(allIDs)
	require.Error(t, err)

	// 回滚后：所有照片数据与快照一致，updated_at 未变。
	for _, p := range photos {
		got, err := photoRepo.GetByID(p.ID)
		require.NoError(t, err)
		snap := snapshot[p.ID]
		assert.Equal(t, snap.FaceCount, got.FaceCount, "photo %d face_count should be rolled back", p.ID)
		assert.Equal(t, snap.TopPersonCategory, got.TopPersonCategory, "photo %d top_person_category should be rolled back", p.ID)
		assert.Equal(t, snap.UpdatedAt, got.UpdatedAt, "photo %d updated_at should be unchanged", p.ID)
	}
}

// TestPhotoRepository_ListSummaries_NoTotal 验证 withTotal=false 时不执行 COUNT、total 恒为 0。
func TestPhotoRepository_ListSummaries_NoTotal(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPhotoRepository(db)

	for i := 0; i < 15; i++ {
		require.NoError(t, repo.Create(&model.Photo{
			FilePath:   fmt.Sprintf("/test/photos/IMG_%02d.jpg", i),
			FileName:   fmt.Sprintf("IMG_%02d.jpg", i),
			FileSize:   1024,
			FileHash:   fmt.Sprintf("hash%02d", i),
			Width:      1920,
			Height:     1080,
			AIAnalyzed: i%2 == 0,
		}))
	}

	// withTotal=false：不统计总数
	summaries, total, err := repo.ListSummaries(1, 10, nil, nil, nil, "", "", "", "", "id", false, nil, "", false)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total, "withTotal=false should skip COUNT")
	assert.Len(t, summaries, 10)

	// withTotal=true：独立 COUNT 查询返回正确总数（不再使用 COUNT(*) OVER()）
	summaries, total, err = repo.ListSummaries(1, 10, nil, nil, nil, "", "", "", "", "id", false, nil, "", true)
	require.NoError(t, err)
	assert.Equal(t, int64(15), total)
	assert.Len(t, summaries, 10)
}

// TestPhotoRepository_ListSummaries_WithTotal_Filtered 验证筛选条件下 withTotal 的独立 COUNT 正确。
func TestPhotoRepository_ListSummaries_WithTotal_Filtered(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPhotoRepository(db)

	for i := 0; i < 10; i++ {
		require.NoError(t, repo.Create(&model.Photo{
			FilePath:   fmt.Sprintf("/test/photos/IMG_%02d.jpg", i),
			FileName:   fmt.Sprintf("IMG_%02d.jpg", i),
			FileSize:   1024,
			FileHash:   fmt.Sprintf("hash%02d", i),
			Width:      1920,
			Height:     1080,
			AIAnalyzed: i < 4, // 4 张已分析
		}))
	}

	analyzed := true
	_, total, err := repo.ListSummaries(1, 10, &analyzed, nil, nil, "", "", "", "", "id", false, nil, "", true)
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
}

// TestPhotoRepository_CountWithFilters 验证按筛选条件独立 COUNT。
func TestPhotoRepository_CountWithFilters(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPhotoRepository(db)

	for i := 0; i < 10; i++ {
		require.NoError(t, repo.Create(&model.Photo{
			FilePath:   fmt.Sprintf("/test/photos/IMG_%02d.jpg", i),
			FileName:   fmt.Sprintf("IMG_%02d.jpg", i),
			FileSize:   1024,
			FileHash:   fmt.Sprintf("hash%02d", i),
			Width:      1920,
			Height:     1080,
			AIAnalyzed: i < 3, // 3 张已分析
		}))
	}

	// 无筛选：全部 10 张
	total, err := repo.CountWithFilters(nil, nil, nil, "", "", "", "", nil, "")
	require.NoError(t, err)
	assert.Equal(t, int64(10), total)

	// 已分析筛选：3 张
	analyzed := true
	total, err = repo.CountWithFilters(&analyzed, nil, nil, "", "", "", "", nil, "")
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)

	// 空路径数组：0
	total, err = repo.CountWithFilters(nil, nil, nil, "", "", "", "", []string{}, "")
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

// TestPhotoRepository_GetPhotoStats 验证一次聚合查询返回总数/已分析/占用。
func TestPhotoRepository_GetPhotoStats(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPhotoRepository(db)

	for i := 0; i < 6; i++ {
		require.NoError(t, repo.Create(&model.Photo{
			FilePath:   fmt.Sprintf("/test/photos/IMG_%02d.jpg", i),
			FileName:   fmt.Sprintf("IMG_%02d.jpg", i),
			FileSize:   1000,
			FileHash:   fmt.Sprintf("hash%02d", i),
			Width:      1920,
			Height:     1080,
			AIAnalyzed: i < 2, // 2 张已分析
		}))
	}

	total, analyzed, size, err := repo.GetPhotoStats()
	require.NoError(t, err)
	assert.Equal(t, int64(6), total)
	assert.Equal(t, int64(2), analyzed)
	assert.Equal(t, int64(6000), size)
}
