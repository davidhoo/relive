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
	photos, total, err := repo.List(1, 10, nil, "", "overall_score", true)

	// 验证
	assert.NoError(t, err)
	assert.Equal(t, int64(15), total)
	assert.Equal(t, 10, len(photos))

	// 测试筛选已分析
	analyzed := true
	photos, total, err = repo.List(1, 10, &analyzed, "", "overall_score", true)
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
	result := &model.AIAnalyzeResponse{
		PhotoID:      int(photo.ID),
		Description:  "这是一张美丽的风景照片",
		Caption:      "日落时分的海滩",
		MemoryScore:  95,
		BeautyScore:  88,
		OverallScore: 93,
		MainCategory: "landscape",
		Tags:         `["sunset","beach","ocean"]`,
	}

	err := repo.MarkAsAnalyzed(photo.ID, result)
	assert.NoError(t, err)

	// 验证
	updated, _ := repo.GetByID(photo.ID)
	assert.True(t, updated.AIAnalyzed)
	assert.NotNil(t, updated.AnalyzedAt)
	assert.Equal(t, "这是一张美丽的风景照片", updated.Description)
	assert.Equal(t, 95, updated.MemoryScore)
	assert.Equal(t, 88, updated.BeautyScore)
}

func TestPhotoRepository_GetUnanalyzed(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPhotoRepository(db)

	// 插入测试数据
	for i := 0; i < 10; i++ {
		photo := &model.Photo{
			FilePath:   "/test/photos/IMG_" + string(rune(i)) + ".jpg",
			FileName:   "IMG_" + string(rune(i)) + ".jpg",
			FileSize:   1024000,
			FileHash:   "hash" + string(rune(i)),
			Width:      1920,
			Height:     1080,
			AIAnalyzed: i >= 5, // 前 5 个未分析
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
