package repository

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
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

func TestPhotoRepository_GetOnThisDayCandidates(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	// 创建不同年份、相同月日附近的照片
	takenAt1 := time.Date(2024, 3, 6, 10, 0, 0, 0, time.Local) // 3月6日
	takenAt2 := time.Date(2023, 3, 8, 10, 0, 0, 0, time.Local) // 3月8日（±3天窗口内）
	takenAt3 := time.Date(2022, 3, 20, 10, 0, 0, 0, time.Local) // 3月20日（±3天窗口外）
	takenAt4 := time.Date(2021, 3, 5, 10, 0, 0, 0, time.Local) // 3月5日（低分）

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
