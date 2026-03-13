package repository

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// activeScope 只查询 active 状态的照片
func activeScope(db *gorm.DB) *gorm.DB {
	return db.Where("status = ?", model.PhotoStatusActive)
}

// PhotoRepository 照片仓库接口
type PhotoRepository interface {
	// 基础 CRUD
	Create(photo *model.Photo) error
	Update(photo *model.Photo) error
	Delete(id uint) error
	GetByID(id uint) (*model.Photo, error)
	GetByFilePath(filePath string) (*model.Photo, error)
	GetByFileHash(fileHash string) (*model.Photo, error)
	Exists(id uint) (bool, error)
	ExistsByFilePath(filePath string) (bool, error)

	// 列表查询
	List(page, pageSize int, analyzed *bool, hasThumbnail *bool, hasGPS *bool, location string, search string, sortBy string, sortDesc bool, enabledPaths []string, status string) ([]*model.Photo, int64, error)
	ListAll() ([]*model.Photo, error)
	ListByIDs(ids []uint) ([]*model.Photo, error)

	// AI 分析相关
	GetUnanalyzed(limit int) ([]*model.Photo, error)
	MarkAsAnalyzed(id uint, description, caption, mainCategory, tags string, memoryScore, beautyScore int) error
	CountAnalyzed() (int64, error)
	CountUnanalyzed() (int64, error)

	// 展示策略相关
	GetByDateRange(start, end time.Time) ([]*model.Photo, error)
	GetTopByScore(limit int, excludePhotoIDs []uint) ([]*model.Photo, error)
	GetRandom(limit, minBeautyScore, minMemoryScore int, excludePhotoIDs []uint) ([]*model.Photo, error)
	GetByLocation(location string, limit int) ([]*model.Photo, error)

	// 统计
	Count() (int64, error)
	CountByLocation() (map[string]int64, error)

	// 分类和标签
	GetCategories() ([]string, error)
	GetTags() ([]string, error)

	// 批量操作
	BatchCreate(photos []*model.Photo, batchSize int) error
	BatchUpdate(photos []*model.Photo, batchSize int) error

	// 地理编码
	UpdateLocation(id uint, location string) error
	ListWithGPS() ([]*model.Photo, error) // 获取所有有GPS坐标的照片

	// 重建相关
	ListByPathPrefix(prefix string) ([]*model.Photo, error)
	SoftDeleteByPathPrefix(prefix string) error

	// 路径统计
	CountByPathPrefix(prefix string) (int64, error)
	GetDerivedStatusByPathPrefix(prefix string) (*model.PathDerivedStatus, error)

	// 状态管理
	BatchUpdateStatus(ids []uint, status string) (int64, error)

	// 分类更新
	UpdateCategory(id uint, category string) error
}

// photoRepository 照片仓库实现
type photoRepository struct {
	db *gorm.DB
}

// NewPhotoRepository 创建照片仓库
func NewPhotoRepository(db *gorm.DB) PhotoRepository {
	return &photoRepository{db: db}
}

// Create 创建照片
func (r *photoRepository) Create(photo *model.Photo) error {
	return r.db.Create(photo).Error
}

// Update 更新照片
func (r *photoRepository) Update(photo *model.Photo) error {
	return r.db.Save(photo).Error
}

// Delete 删除照片（硬删除）
func (r *photoRepository) Delete(id uint) error {
	return r.db.Unscoped().Delete(&model.Photo{}, "id = ?", id).Error
}

// GetByID 根据 ID 获取照片
func (r *photoRepository) GetByID(id uint) (*model.Photo, error) {
	var photo model.Photo
	err := r.db.First(&photo, id).Error
	if err != nil {
		return nil, err
	}
	return &photo, nil
}

// GetByFilePath 根据文件路径获取照片
func (r *photoRepository) GetByFilePath(filePath string) (*model.Photo, error) {
	var photo model.Photo
	err := r.db.Where("file_path = ?", filePath).First(&photo).Error
	if err != nil {
		return nil, err
	}
	return &photo, nil
}

// GetByFileHash 根据文件哈希获取照片
func (r *photoRepository) GetByFileHash(fileHash string) (*model.Photo, error) {
	var photo model.Photo
	err := r.db.Where("file_hash = ?", fileHash).First(&photo).Error
	if err != nil {
		return nil, err
	}
	return &photo, nil
}

// Exists 检查照片是否存在
func (r *photoRepository) Exists(id uint) (bool, error) {
	var count int64
	err := r.db.Model(&model.Photo{}).Where("id = ?", id).Count(&count).Error
	return count > 0, err
}

// ExistsByFilePath 检查文件路径是否存在
func (r *photoRepository) ExistsByFilePath(filePath string) (bool, error) {
	var count int64
	err := r.db.Model(&model.Photo{}).Where("file_path = ?", filePath).Count(&count).Error
	return count > 0, err
}

// List 分页列表查询
func (r *photoRepository) List(page, pageSize int, analyzed *bool, hasThumbnail *bool, hasGPS *bool, location string, search string, sortBy string, sortDesc bool, enabledPaths []string, status string) ([]*model.Photo, int64, error) {
	var photos []*model.Photo
	var total int64

	// 构建查询
	query := r.db.Model(&model.Photo{})

	// status 过滤：默认只查 active，支持 excluded 和 all
	switch status {
	case "excluded":
		query = query.Where("status = ?", model.PhotoStatusExcluded)
	case "all":
		// 不加过滤
	default:
		query = query.Scopes(activeScope)
	}

	// 筛选启用的路径
	if enabledPaths != nil {
		if len(enabledPaths) == 0 {
			return []*model.Photo{}, 0, nil
		}

		var pathConditions []string
		var pathValues []interface{}
		for _, path := range enabledPaths {
			condition, values := buildPathPrefixCondition(path)
			if condition == "" {
				continue
			}

			pathConditions = append(pathConditions, condition)
			pathValues = append(pathValues, values...)
		}

		if len(pathConditions) == 0 {
			return []*model.Photo{}, 0, nil
		}

		query = query.Where(strings.Join(pathConditions, " OR "), pathValues...)
	}

	// 筛选条件
	if analyzed != nil {
		query = query.Where("ai_analyzed = ?", *analyzed)
	}
	if hasThumbnail != nil {
		if *hasThumbnail {
			query = query.Where("thumbnail_status = 'ready'")
		} else {
			query = query.Where("thumbnail_status != 'ready' OR thumbnail_status IS NULL")
		}
	}
	if hasGPS != nil {
		if *hasGPS {
			query = query.Where("gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL")
		} else {
			query = query.Where("gps_latitude IS NULL OR gps_longitude IS NULL")
		}
	}
	if location != "" {
		query = query.Where("location LIKE ?", "%"+location+"%")
	}
	// 搜索关键词（搜索路径、文件名、分类、标签、描述、标题、位置）
	if search != "" {
		searchPattern := "%" + search + "%"
		query = query.Where(
			"file_path LIKE ? OR file_name LIKE ? OR main_category LIKE ? OR tags LIKE ? OR description LIKE ? OR caption LIKE ? OR location LIKE ?",
			searchPattern, searchPattern, searchPattern, searchPattern, searchPattern, searchPattern, searchPattern,
		)
	}

	// 统计总数
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 排序
	if sortBy == "" {
		sortBy = "taken_at"
	}
	orderClause := sortBy
	if sortDesc {
		orderClause += " DESC"
	} else {
		orderClause += " ASC"
	}
	query = query.Order(orderClause)

	// 分页
	offset := (page - 1) * pageSize
	if err := query.Offset(offset).Limit(pageSize).Find(&photos).Error; err != nil {
		return nil, 0, err
	}

	return photos, total, nil
}

func normalizePathPrefix(prefix string) string {
	if prefix == "" {
		return ""
	}

	cleaned := filepath.Clean(prefix)
	if cleaned == "." {
		return ""
	}

	return cleaned
}

func buildPathPrefixCondition(prefix string) (string, []interface{}) {
	normalized := normalizePathPrefix(prefix)
	if normalized == "" {
		return "", nil
	}

	separator := string(filepath.Separator)
	childPattern := normalized + separator + "%"
	if normalized == separator {
		childPattern = normalized + "%"
	}

	return "(file_path = ? OR file_path LIKE ?)", []interface{}{normalized, childPattern}
}

// ListAll 获取所有照片
func (r *photoRepository) ListAll() ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Scopes(activeScope).Find(&photos).Error
	return photos, err
}

// ListByIDs 根据 ID 列表获取照片
func (r *photoRepository) ListByIDs(ids []uint) ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Where("id IN ?", ids).Find(&photos).Error
	return photos, err
}

// GetUnanalyzed 获取未分析的照片
func (r *photoRepository) GetUnanalyzed(limit int) ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Scopes(activeScope).Where(`ai_analyzed = ?
		AND thumbnail_status = ?
		AND (gps_latitude IS NULL OR gps_longitude IS NULL OR geocode_status = ?)`, false, "ready", "ready").
		Order("taken_at DESC").
		Limit(limit).
		Find(&photos).Error
	return photos, err
}

// MarkAsAnalyzed 标记为已分析
func (r *photoRepository) MarkAsAnalyzed(id uint, description, caption, mainCategory, tags string, memoryScore, beautyScore int) error {
	now := time.Now()
	overallScore := model.CalcOverallScore(memoryScore, beautyScore)
	return r.db.Model(&model.Photo{}).Where("id = ?", id).Updates(map[string]interface{}{
		"ai_analyzed":   true,
		"analyzed_at":   now,
		"description":   description,
		"caption":       caption,
		"memory_score":  memoryScore,
		"beauty_score":  beautyScore,
		"overall_score": overallScore,
		"main_category": mainCategory,
		"tags":          tags,
	}).Error
}

// CountAnalyzed 统计已分析照片数
func (r *photoRepository) CountAnalyzed() (int64, error) {
	var count int64
	err := r.db.Model(&model.Photo{}).Scopes(activeScope).Where("ai_analyzed = ?", true).Count(&count).Error
	return count, err
}

// CountUnanalyzed 统计未分析照片数
func (r *photoRepository) CountUnanalyzed() (int64, error) {
	var count int64
	err := r.db.Model(&model.Photo{}).Scopes(activeScope).Where("ai_analyzed = ?", false).Count(&count).Error
	return count, err
}

// GetByDateRange 根据日期范围获取照片
func (r *photoRepository) GetByDateRange(start, end time.Time) ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Scopes(activeScope).Where("taken_at BETWEEN ? AND ?", start, end).
		Order("taken_at DESC").
		Find(&photos).Error
	return photos, err
}

// GetTopByScore 获取评分最高的照片
func (r *photoRepository) GetTopByScore(limit int, excludePhotoIDs []uint) ([]*model.Photo, error) {
	var photos []*model.Photo
	query := r.db.Scopes(activeScope).Where("ai_analyzed = ?", true).
		Order("overall_score DESC, taken_at DESC")

	if len(excludePhotoIDs) > 0 {
		query = query.Where("id NOT IN ?", excludePhotoIDs)
	}

	err := query.Limit(limit).Find(&photos).Error
	return photos, err
}

// GetRandom 随机获取满足阈值的照片
func (r *photoRepository) GetRandom(limit, minBeautyScore, minMemoryScore int, excludePhotoIDs []uint) ([]*model.Photo, error) {
	var photos []*model.Photo

	query := r.db.Scopes(activeScope).Where(
		"ai_analyzed = ? AND beauty_score >= ? AND memory_score >= ?",
		true,
		minBeautyScore,
		minMemoryScore,
	).Order("RANDOM()")

	if len(excludePhotoIDs) > 0 {
		query = query.Where("id NOT IN ?", excludePhotoIDs)
	}

	err := query.Limit(limit).Find(&photos).Error
	return photos, err
}

// GetByLocation 根据位置获取照片
func (r *photoRepository) GetByLocation(location string, limit int) ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Scopes(activeScope).Where("location LIKE ?", "%"+location+"%").
		Order("taken_at DESC").
		Limit(limit).
		Find(&photos).Error
	return photos, err
}

// Count 统计照片总数
func (r *photoRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.Photo{}).Scopes(activeScope).Count(&count).Error
	return count, err
}

// CountByLocation 统计各位置的照片数
func (r *photoRepository) CountByLocation() (map[string]int64, error) {
	type Result struct {
		Location string
		Count    int64
	}

	var results []Result
	err := r.db.Model(&model.Photo{}).Scopes(activeScope).
		Select("location, COUNT(*) as count").
		Where("location != ''").
		Group("location").
		Order("count DESC").
		Find(&results).Error

	if err != nil {
		return nil, err
	}

	// 转换为 map
	locationMap := make(map[string]int64)
	for _, result := range results {
		locationMap[result.Location] = result.Count
	}

	return locationMap, nil
}

// BatchCreate 批量创建照片
func (r *photoRepository) BatchCreate(photos []*model.Photo, batchSize int) error {
	return r.db.CreateInBatches(photos, batchSize).Error
}

// BatchUpdate 批量更新照片
func (r *photoRepository) BatchUpdate(photos []*model.Photo, batchSize int) error {
	// GORM 不支持直接批量更新，需要分批处理
	for i := 0; i < len(photos); i += batchSize {
		end := i + batchSize
		if end > len(photos) {
			end = len(photos)
		}

		batch := photos[i:end]

		// 使用事务批量更新
		err := r.db.Transaction(func(tx *gorm.DB) error {
			for _, photo := range batch {
				if err := tx.Save(photo).Error; err != nil {
					return err
				}
			}
			return nil
		})

		if err != nil {
			return err
		}
	}

	return nil
}

// UpdateLocation 更新照片的位置信息
func (r *photoRepository) UpdateLocation(id uint, location string) error {
	return r.db.Model(&model.Photo{}).
		Where("id = ?", id).
		Update("location", location).Error
}

// ListByPathPrefix 根据路径前缀获取所有照片（用于重建时找出已删除的文件）
func (r *photoRepository) ListByPathPrefix(prefix string) ([]*model.Photo, error) {
	var photos []*model.Photo
	condition, values := buildPathPrefixCondition(prefix)
	if condition == "" {
		return photos, nil
	}

	err := r.db.Where(condition, values...).Find(&photos).Error
	return photos, err
}

// SoftDeleteByPathPrefix 软删除指定路径前缀的所有照片
func (r *photoRepository) SoftDeleteByPathPrefix(prefix string) error {
	condition, values := buildPathPrefixCondition(prefix)
	if condition == "" {
		return nil
	}

	return r.db.Where(condition, values...).Delete(&model.Photo{}).Error
}

// CountByPathPrefix 统计指定路径前缀的照片数量
func (r *photoRepository) CountByPathPrefix(prefix string) (int64, error) {
	var count int64
	condition, values := buildPathPrefixCondition(prefix)
	if condition == "" {
		return 0, nil
	}

	err := r.db.Model(&model.Photo{}).Scopes(activeScope).Where(condition, values...).Count(&count).Error
	return count, err
}

// GetDerivedStatusByPathPrefix 使用 SQL 聚合统计路径下照片的派生状态
func (r *photoRepository) GetDerivedStatusByPathPrefix(prefix string) (*model.PathDerivedStatus, error) {
	condition, values := buildPathPrefixCondition(prefix)
	if condition == "" {
		return &model.PathDerivedStatus{}, nil
	}

	var result model.PathDerivedStatus
	err := r.db.Model(&model.Photo{}).Scopes(activeScope).
		Where(condition, values...).
		Select(`
			COUNT(*) as photo_total,
			COUNT(*) as thumbnail_total,
			SUM(CASE WHEN ai_analyzed = 1 THEN 1 ELSE 0 END) as analyzed_total,
			SUM(CASE WHEN thumbnail_status = 'ready' THEN 1 ELSE 0 END) as thumbnail_ready,
			SUM(CASE WHEN thumbnail_status = 'failed' THEN 1 ELSE 0 END) as thumbnail_failed,
			SUM(CASE WHEN thumbnail_status NOT IN ('ready', 'failed') OR thumbnail_status IS NULL THEN 1 ELSE 0 END) as thumbnail_pending,
			SUM(CASE WHEN gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL THEN 1 ELSE 0 END) as geocode_total,
			SUM(CASE WHEN (gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL) AND (geocode_status = 'ready' OR (COALESCE(TRIM(location), '') != '')) THEN 1 ELSE 0 END) as geocode_ready,
			SUM(CASE WHEN (gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL) AND geocode_status = 'failed' THEN 1 ELSE 0 END) as geocode_failed,
			SUM(CASE WHEN (gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL) AND geocode_status != 'ready' AND geocode_status != 'failed' AND (COALESCE(TRIM(location), '') = '') THEN 1 ELSE 0 END) as geocode_pending
		`).
		Scan(&result).Error

	return &result, err
}

// GetCategories 获取所有分类
func (r *photoRepository) GetCategories() ([]string, error) {
	var categories []string
	err := r.db.Model(&model.Photo{}).Scopes(activeScope).
		Where("main_category != ? AND main_category IS NOT NULL", "").
		Distinct("main_category").
		Pluck("main_category", &categories).Error
	return categories, err
}

// GetTags 获取所有标签
func (r *photoRepository) GetTags() ([]string, error) {
	var tagRows []struct {
		Tags string
	}
	err := r.db.Model(&model.Photo{}).Scopes(activeScope).
		Where("tags != ? AND tags IS NOT NULL", "").
		Pluck("tags", &tagRows).Error
	if err != nil {
		return nil, err
	}

	// 解析所有标签并去重
	tagMap := make(map[string]bool)
	for _, row := range tagRows {
		tags := strings.Split(row.Tags, ",")
		for _, tag := range tags {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				tagMap[tag] = true
			}
		}
	}

	// 转换为切片
	var result []string
	for tag := range tagMap {
		result = append(result, tag)
	}

	// 排序
	sort.Strings(result)

	return result, nil
}

// ListWithGPS 获取所有有GPS坐标的照片
func (r *photoRepository) ListWithGPS() ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Scopes(activeScope).Where("gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL").Find(&photos).Error
	return photos, err
}

// BatchUpdateStatus 批量更新照片状态
func (r *photoRepository) BatchUpdateStatus(ids []uint, status string) (int64, error) {
	result := r.db.Model(&model.Photo{}).Where("id IN ?", ids).Update("status", status)
	return result.RowsAffected, result.Error
}

// UpdateCategory 更新照片分类
func (r *photoRepository) UpdateCategory(id uint, category string) error {
	return r.db.Model(&model.Photo{}).Where("id = ?", id).Update("main_category", category).Error
}
