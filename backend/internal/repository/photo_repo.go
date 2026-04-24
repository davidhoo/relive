package repository

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/database"
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
	UpdateFields(id uint, fields map[string]interface{}) error
	Delete(id uint) error
	GetByID(id uint) (*model.Photo, error)
	GetByFilePath(filePath string) (*model.Photo, error)
	GetByFileHash(fileHash string) (*model.Photo, error)
	Exists(id uint) (bool, error)
	ExistsByFilePath(filePath string) (bool, error)

	// 列表查询
	List(page, pageSize int, analyzed *bool, hasThumbnail *bool, hasGPS *bool, location string, search string, category string, tag string, sortBy string, sortDesc bool, enabledPaths []string, status string) ([]*model.Photo, int64, error)
	ListAll() ([]*model.Photo, error)
	ListByIDs(ids []uint) ([]*model.Photo, error)
	ListPhotosByPersonID(personID uint) ([]*model.Photo, error) // 通过 JOIN 直接获取人物关联照片
	ListPhotoSummariesByPersonID(personID uint) ([]*model.Photo, error) // 精简列 + SQL 排序
	ListPhotoSummariesByPersonIDPaginated(personID uint, page, pageSize int) ([]*model.Photo, int64, error)

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
	GetOnThisDayCandidates(monthDayStart, monthDayEnd string, minBeauty, minMemory int, excludeIDs []uint, limit int) ([]*model.Photo, error)
	GetTopScoredCandidates(minBeauty, minMemory int, excludeIDs []uint, limit int) ([]*model.Photo, error)

	// 统计
	Count() (int64, error)
	CountByLocation() (map[string]int64, error)

	// 分类和标签
	GetCategories() ([]string, error)
	GetTags(query string, limit int) ([]model.TagWithCount, error)
	CountTags() (int64, error)

	// 批量操作
	BatchCreate(photos []*model.Photo, batchSize int) error
	BatchUpdate(photos []*model.Photo, batchSize int) error

	// 地理编码
	UpdateLocation(id uint, location string) error
	UpdateLocationFull(id uint, loc *model.LocationFields) error
	ListWithGPS() ([]*model.Photo, error) // 获取所有有GPS坐标的照片

	// 重建相关
	ListByPathPrefix(prefix string) ([]*model.Photo, error)

	// 路径统计
	CountByPathPrefix(prefix string) (int64, error)
	GetDerivedStatusByPathPrefix(prefix string) (*model.PathDerivedStatus, error)

	// 按状态计数
	CountByStatus() (*model.PhotoCountsResponse, error)

	// 批量路径派生状态
	GetDerivedStatusByPathPrefixes(prefixes []string) (map[string]*model.PathDerivedStatus, error)

	// 状态管理
	BatchUpdateStatus(ids []uint, status string) (int64, error)

	// 分类更新
	UpdateCategory(id uint, category string) error
	RecomputeTopPersonCategory(photoIDs []uint) error

	// 人物系统
	ListByFaceStatus(status string) ([]*model.Photo, error)

	// 手动旋转
	UpdateManualRotation(id uint, rotation int) error

	// 策展引擎：无事件高颜值散片
	GetScatteredHighQuality(minBeauty int, excludeIDs []uint, limit int) ([]*model.Photo, error)

	// 相邻照片查询
	GetAdjacent(id uint, analyzed *bool, hasThumbnail *bool, hasGPS *bool, location string, search string, category string, tag string, sortBy string, sortDesc bool, enabledPaths []string, status string) (*model.AdjacentPhotosResponse, error)
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

// UpdateFields 按字段更新照片
func (r *photoRepository) UpdateFields(id uint, fields map[string]interface{}) error {
	return r.db.Model(&model.Photo{}).Where("id = ?", id).Updates(fields).Error
}

// Delete 硬删除照片（永久移除数据）
// 设计意图：排除照片使用 status=excluded（可恢复），Delete 用于真正的数据清理
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

// applyPhotoFilters 构建照片列表的通用 WHERE 条件（List 和 GetAdjacent 共用）
func (r *photoRepository) applyPhotoFilters(query *gorm.DB, analyzed *bool, hasThumbnail *bool, hasGPS *bool, location string, search string, category string, tag string, enabledPaths []string, status string) *gorm.DB {
	// status 过滤
	switch status {
	case model.PhotoStatusExcluded:
		query = query.Where("status = ?", model.PhotoStatusExcluded)
	case "all":
		// 不加过滤
	default:
		query = query.Scopes(activeScope)
	}

	// 筛选启用的路径
	if enabledPaths != nil {
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
		if len(pathConditions) > 0 {
			query = query.Where(strings.Join(pathConditions, " OR "), pathValues...)
		}
	}

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
	if search != "" {
		if database.FTS5Available {
			ftsQuery := buildFTSQuery(search)
			query = query.Where("id IN (SELECT rowid FROM photos_fts WHERE photos_fts MATCH ?)", ftsQuery)
		} else {
			searchPattern := "%" + search + "%"
			query = query.Where(
				"file_path LIKE ? OR file_name LIKE ? OR main_category LIKE ? OR tags LIKE ? OR description LIKE ? OR caption LIKE ? OR location LIKE ?",
				searchPattern, searchPattern, searchPattern, searchPattern, searchPattern, searchPattern, searchPattern,
			)
		}
	}
	if category != "" {
		query = query.Where("main_category = ?", category)
	}
	if tag != "" {
		query = query.Where("id IN (?)",
			r.db.Model(&model.PhotoTag{}).Select("photo_id").Where("tag = ?", tag))
	}
	return query
}

// sanitizeSortBy 白名单校验排序字段
func sanitizeSortBy(sortBy string) string {
	allowedSortFields := map[string]bool{
		"taken_at": true, "overall_score": true, "beauty_score": true,
		"memory_score": true, "created_at": true, "file_name": true,
	}
	if !allowedSortFields[sortBy] {
		return "taken_at"
	}
	return sortBy
}

// List 分页列表查询
func (r *photoRepository) List(page, pageSize int, analyzed *bool, hasThumbnail *bool, hasGPS *bool, location string, search string, category string, tag string, sortBy string, sortDesc bool, enabledPaths []string, status string) ([]*model.Photo, int64, error) {
	var photos []*model.Photo
	var total int64

	query := r.db.Model(&model.Photo{})

	// enabledPaths 为空数组时直接返回
	if enabledPaths != nil && len(enabledPaths) == 0 {
		return []*model.Photo{}, 0, nil
	}

	query = r.applyPhotoFilters(query, analyzed, hasThumbnail, hasGPS, location, search, category, tag, enabledPaths, status)

	// 统计总数
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 排序
	sortBy = sanitizeSortBy(sortBy)
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

// GetAdjacent 获取指定照片在同一筛选/排序条件下的前后相邻照片 ID
func (r *photoRepository) GetAdjacent(id uint, analyzed *bool, hasThumbnail *bool, hasGPS *bool, location string, search string, category string, tag string, sortBy string, sortDesc bool, enabledPaths []string, status string) (*model.AdjacentPhotosResponse, error) {
	sortBy = sanitizeSortBy(sortBy)

	// 先获取当前照片的排序字段值
	var current model.Photo
	if err := r.db.Select("id, "+sortBy).First(&current, id).Error; err != nil {
		return nil, err
	}

	// 获取排序字段的值（通过 map 提取）
	var sortVal interface{}
	row := r.db.Model(&model.Photo{}).Select(sortBy).Where("id = ?", id).Row()
	if err := row.Scan(&sortVal); err != nil {
		return nil, err
	}

	resp := &model.AdjacentPhotosResponse{}

	// 查找上一张（排序方向相反的第一个）
	// 如果 sortDesc=true，原排序是 sortBy DESC，"上一张"是排序值更大的那个
	// 用 (sortBy, id) 复合排序保证稳定性
	{
		q := r.db.Model(&model.Photo{})
		q = r.applyPhotoFilters(q, analyzed, hasThumbnail, hasGPS, location, search, category, tag, enabledPaths, status)
		if sortDesc {
			// 原序: sortBy DESC, id DESC → prev 是 (sortBy > val) OR (sortBy = val AND id > currentID)
			q = q.Where(fmt.Sprintf("(%s > ? OR (%s = ? AND id > ?))", sortBy, sortBy), sortVal, sortVal, id)
			q = q.Order(sortBy + " ASC, id ASC")
		} else {
			// 原序: sortBy ASC, id ASC → prev 是 (sortBy < val) OR (sortBy = val AND id < currentID)
			q = q.Where(fmt.Sprintf("(%s < ? OR (%s = ? AND id < ?))", sortBy, sortBy), sortVal, sortVal, id)
			q = q.Order(sortBy + " DESC, id DESC")
		}
		var prev model.Photo
		if err := q.Select("id").Limit(1).Find(&prev).Error; err == nil && prev.ID != 0 {
			resp.PrevID = &prev.ID
		}
	}

	// 查找下一张
	{
		q := r.db.Model(&model.Photo{})
		q = r.applyPhotoFilters(q, analyzed, hasThumbnail, hasGPS, location, search, category, tag, enabledPaths, status)
		if sortDesc {
			// 原序: sortBy DESC, id DESC → next 是 (sortBy < val) OR (sortBy = val AND id < currentID)
			q = q.Where(fmt.Sprintf("(%s < ? OR (%s = ? AND id < ?))", sortBy, sortBy), sortVal, sortVal, id)
			q = q.Order(sortBy + " DESC, id DESC")
		} else {
			// 原序: sortBy ASC, id ASC → next 是 (sortBy > val) OR (sortBy = val AND id > currentID)
			q = q.Where(fmt.Sprintf("(%s > ? OR (%s = ? AND id > ?))", sortBy, sortBy), sortVal, sortVal, id)
			q = q.Order(sortBy + " ASC, id ASC")
		}
		var next model.Photo
		if err := q.Select("id").Limit(1).Find(&next).Error; err == nil && next.ID != 0 {
			resp.NextID = &next.ID
		}
	}

	return resp, nil
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
	if len(ids) == 0 {
		return nil, nil
	}
	var allPhotos []*model.Photo
	for _, chunk := range chunkIDs(ids) {
		var photos []*model.Photo
		if err := r.db.Where("id IN ?", chunk).Find(&photos).Error; err != nil {
			return nil, err
		}
		allPhotos = append(allPhotos, photos...)
	}
	return allPhotos, nil
}

// ListPhotosByPersonID 通过 JOIN 查询获取人物关联的所有照片（优化：避免先查 faces 再查 photos）
func (r *photoRepository) ListPhotosByPersonID(personID uint) ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Distinct().
		Select("photos.*").
		Joins("INNER JOIN faces ON faces.photo_id = photos.id").
		Where("faces.person_id = ?", personID).
		Find(&photos).Error
	return photos, err
}

// ListPhotoSummariesByPersonID 精简列查询 + SQL 排序，用于人物详情页
func (r *photoRepository) ListPhotoSummariesByPersonID(personID uint) ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Distinct().
		Select("photos.id, photos.file_name, photos.caption, photos.taken_at, photos.created_at").
		Joins("INNER JOIN faces ON faces.photo_id = photos.id").
		Where("faces.person_id = ?", personID).
		Order("photos.taken_at DESC, photos.id DESC").
		Find(&photos).Error
	return photos, err
}

// ListPhotoSummariesByPersonIDPaginated 精简列 + 分页查询，用于人物详情页
func (r *photoRepository) ListPhotoSummariesByPersonIDPaginated(personID uint, page, pageSize int) ([]*model.Photo, int64, error) {
	var total int64
	if err := r.db.Model(&model.Photo{}).
		Joins("INNER JOIN faces ON faces.photo_id = photos.id").
		Where("faces.person_id = ?", personID).
		Distinct("photos.id").
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var photos []*model.Photo
	offset := (page - 1) * pageSize
	err := r.db.Distinct().
		Select("photos.id, photos.file_name, photos.caption, photos.taken_at, photos.created_at").
		Joins("INNER JOIN faces ON faces.photo_id = photos.id").
		Where("faces.person_id = ?", personID).
		Order("photos.taken_at DESC, photos.id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&photos).Error
	return photos, total, err
}

// GetUnanalyzed 获取未分析的照片
func (r *photoRepository) GetUnanalyzed(limit int) ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Scopes(activeScope).Where(`ai_analyzed = ?
		AND thumbnail_status = ?
		AND (gps_latitude IS NULL OR gps_longitude IS NULL OR geocode_status = ?)`, false, model.ThumbnailStatusReady, model.GeocodeStatusReady).
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
		Order(topPersonWeightedScoreSQL() + " DESC").
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

// GetOnThisDayCandidates 按月日范围查找"往年今日"候选照片，单条 SQL 替代逐年循环
// monthDayStart/monthDayEnd 格式为 "01-02"（MM-DD）
// 自动处理跨年边界（如 12-30 到 01-03）
func (r *photoRepository) GetOnThisDayCandidates(monthDayStart, monthDayEnd string, minBeauty, minMemory int, excludeIDs []uint, limit int) ([]*model.Photo, error) {
	var photos []*model.Photo
	query := r.db.Scopes(activeScope).
		Where("ai_analyzed = ?", true).
		Where("taken_at IS NOT NULL").
		Where("beauty_score >= ? AND memory_score >= ?", minBeauty, minMemory)

	if monthDayStart > monthDayEnd {
		// 跨年边界：如 12-28 到 01-04
		query = query.Where("(strftime('%m-%d', taken_at) >= ? OR strftime('%m-%d', taken_at) <= ?)", monthDayStart, monthDayEnd)
	} else {
		query = query.Where("strftime('%m-%d', taken_at) BETWEEN ? AND ?", monthDayStart, monthDayEnd)
	}

	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}

	err := query.Order(topPersonWeightedScoreSQL() + " DESC").
		Order("overall_score DESC, taken_at DESC").
		Limit(limit).Find(&photos).Error
	return photos, err
}

// GetTopScoredCandidates 按综合评分排序获取候选照片，替代 ListAll + 内存过滤
func (r *photoRepository) GetTopScoredCandidates(minBeauty, minMemory int, excludeIDs []uint, limit int) ([]*model.Photo, error) {
	var photos []*model.Photo
	query := r.db.Scopes(activeScope).
		Where("ai_analyzed = ?", true).
		Where("beauty_score >= ? AND memory_score >= ?", minBeauty, minMemory)

	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}

	err := query.Order(topPersonWeightedScoreSQL() + " DESC").
		Order("overall_score DESC, taken_at DESC").
		Limit(limit).Find(&photos).Error
	return photos, err
}

func topPersonWeightedScoreSQL() string {
	return `overall_score + CASE top_person_category
		WHEN 'family' THEN 3
		WHEN 'friend' THEN 2
		WHEN 'acquaintance' THEN 1
		ELSE 0
	END`
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

// UpdateLocationFull 更新照片的完整位置信息（含结构化字段）
func (r *photoRepository) UpdateLocationFull(id uint, loc *model.LocationFields) error {
	return r.db.Model(&model.Photo{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"location": loc.Location,
			"country":  loc.Country,
			"province": loc.Province,
			"city":     loc.City,
			"district": loc.District,
			"street":   loc.Street,
			"poi":      loc.POI,
		}).Error
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

func (r *photoRepository) ListByFaceStatus(status string) ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Where("face_process_status = ? AND status = ?", status, model.PhotoStatusActive).Find(&photos).Error
	return photos, err
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
	err := r.db.Model(&model.Photo{}).
		Where("main_category != ? AND main_category IS NOT NULL", "").
		Distinct("main_category").
		Pluck("main_category", &categories).Error
	return categories, err
}

// GetTags 获取热门标签（从 photo_tags 表查询，支持搜索和限制数量）
func (r *photoRepository) GetTags(query string, limit int) ([]model.TagWithCount, error) {
	var results []model.TagWithCount
	db := r.db.Table("photo_tags").
		Select("tag, COUNT(*) as count").
		Group("tag").
		Order("count DESC, tag ASC")

	if query != "" {
		db = db.Where("tag LIKE ?", "%"+query+"%")
	}
	if limit > 0 {
		db = db.Limit(limit)
	}

	err := db.Scan(&results).Error
	return results, err
}

// CountTags 统计不同标签总数
func (r *photoRepository) CountTags() (int64, error) {
	var count int64
	err := r.db.Table("photo_tags").
		Distinct("tag").
		Count(&count).Error
	return count, err
}

// ListWithGPS 获取所有有GPS坐标的照片
func (r *photoRepository) ListWithGPS() ([]*model.Photo, error) {
	var photos []*model.Photo
	err := r.db.Scopes(activeScope).Where("gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL").Find(&photos).Error
	return photos, err
}

// BatchUpdateStatus 批量更新照片状态
func (r *photoRepository) BatchUpdateStatus(ids []uint, status string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var total int64
	for _, chunk := range chunkIDs(ids) {
		result := r.db.Model(&model.Photo{}).Where("id IN ?", chunk).Update("status", status)
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
	}
	return total, nil
}

// UpdateCategory 更新照片分类
func (r *photoRepository) UpdateCategory(id uint, category string) error {
	return r.db.Model(&model.Photo{}).Where("id = ?", id).Update("main_category", category).Error
}

// RecomputeTopPersonCategory 根据照片关联人物重新计算最高人物类别
func (r *photoRepository) RecomputeTopPersonCategory(photoIDs []uint) error {
	if len(photoIDs) == 0 {
		return nil
	}
	dedupedIDs := make([]uint, 0, len(photoIDs))
	seen := make(map[uint]struct{}, len(photoIDs))
	for _, photoID := range photoIDs {
		if photoID == 0 {
			continue
		}
		if _, ok := seen[photoID]; ok {
			continue
		}
		seen[photoID] = struct{}{}
		dedupedIDs = append(dedupedIDs, photoID)
	}
	if len(dedupedIDs) == 0 {
		return nil
	}

	type row struct {
		PhotoID      uint `gorm:"column:photo_id"`
		FaceCount    int  `gorm:"column:face_count"`
		CategoryRank int  `gorm:"column:category_rank"`
	}

	var allRows []row
	for i := 0; i < len(dedupedIDs); i += sqliteVarLimit {
		end := i + sqliteVarLimit
		if end > len(dedupedIDs) {
			end = len(dedupedIDs)
		}
		chunk := dedupedIDs[i:end]

		var rows []row
		err := r.db.Model(&model.Photo{}).
			Select(`
				photos.id AS photo_id,
				COUNT(faces.id) AS face_count,
				MAX(CASE people.category
					WHEN 'family' THEN 4
					WHEN 'friend' THEN 3
					WHEN 'acquaintance' THEN 2
					WHEN 'stranger' THEN 1
					ELSE 0
				END) AS category_rank
			`).
			Joins("LEFT JOIN faces ON faces.photo_id = photos.id").
			Joins("LEFT JOIN people ON people.id = faces.person_id").
			Where("photos.id IN ?", chunk).
			Group("photos.id").
			Scan(&rows).Error
		if err != nil {
			return err
		}
		allRows = append(allRows, rows...)
	}

	byPhotoID := make(map[uint]row, len(allRows))
	for _, row := range allRows {
		byPhotoID[row.PhotoID] = row
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, photoID := range dedupedIDs {
			current, ok := byPhotoID[photoID]
			updates := map[string]interface{}{
				"face_count":          0,
				"top_person_category": "",
			}
			if ok {
				updates["face_count"] = current.FaceCount
				updates["top_person_category"] = personCategoryFromRank(current.CategoryRank)
			}

			if err := tx.Model(&model.Photo{}).Where("id = ?", photoID).Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateManualRotation 更新照片手动旋转角度
func (r *photoRepository) UpdateManualRotation(id uint, rotation int) error {
	return r.db.Model(&model.Photo{}).Where("id = ?", id).Update("manual_rotation", rotation).Error
}

// CountByStatus 按状态统计照片数量（单条 SQL）
func (r *photoRepository) CountByStatus() (*model.PhotoCountsResponse, error) {
	var result model.PhotoCountsResponse
	err := r.db.Model(&model.Photo{}).
		Select(`
			SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END) as active_count,
			SUM(CASE WHEN status = 'excluded' THEN 1 ELSE 0 END) as excluded_count
		`).
		Scan(&result).Error
	return &result, err
}

// GetDerivedStatusByPathPrefixes 批量按路径前缀统计派生状态（单条 SQL）
func (r *photoRepository) GetDerivedStatusByPathPrefixes(prefixes []string) (map[string]*model.PathDerivedStatus, error) {
	result := make(map[string]*model.PathDerivedStatus)
	if len(prefixes) == 0 {
		return result, nil
	}

	// 构建 CASE WHEN 表达式和 WHERE 条件
	var caseWhenParts []string
	var whereConditions []string
	var args []interface{}

	for _, prefix := range prefixes {
		normalized := normalizePathPrefix(prefix)
		if normalized == "" {
			continue
		}

		separator := string(filepath.Separator)
		childPattern := normalized + separator + "%"
		if normalized == separator {
			childPattern = normalized + "%"
		}

		caseWhenParts = append(caseWhenParts,
			fmt.Sprintf("WHEN file_path = '%s' OR file_path LIKE '%s' THEN '%s'",
				strings.ReplaceAll(normalized, "'", "''"),
				strings.ReplaceAll(childPattern, "'", "''"),
				strings.ReplaceAll(prefix, "'", "''")))

		whereConditions = append(whereConditions, "(file_path = ? OR file_path LIKE ?)")
		args = append(args, normalized, childPattern)
	}

	if len(caseWhenParts) == 0 {
		return result, nil
	}

	caseExpr := "CASE " + strings.Join(caseWhenParts, " ") + " END"
	whereExpr := strings.Join(whereConditions, " OR ")

	type row struct {
		PathGroup        string `gorm:"column:path_group"`
		PhotoTotal       int64  `gorm:"column:photo_total"`
		AnalyzedTotal    int64  `gorm:"column:analyzed_total"`
		ThumbnailTotal   int64  `gorm:"column:thumbnail_total"`
		ThumbnailReady   int64  `gorm:"column:thumbnail_ready"`
		ThumbnailFailed  int64  `gorm:"column:thumbnail_failed"`
		ThumbnailPending int64  `gorm:"column:thumbnail_pending"`
		GeocodeTotal     int64  `gorm:"column:geocode_total"`
		GeocodeReady     int64  `gorm:"column:geocode_ready"`
		GeocodeFailed    int64  `gorm:"column:geocode_failed"`
		GeocodePending   int64  `gorm:"column:geocode_pending"`
	}

	var rows []row
	selectExpr := caseExpr + ` as path_group,
		COUNT(*) as photo_total,
		SUM(CASE WHEN ai_analyzed = 1 THEN 1 ELSE 0 END) as analyzed_total,
		COUNT(*) as thumbnail_total,
		SUM(CASE WHEN thumbnail_status = 'ready' THEN 1 ELSE 0 END) as thumbnail_ready,
		SUM(CASE WHEN thumbnail_status = 'failed' THEN 1 ELSE 0 END) as thumbnail_failed,
		SUM(CASE WHEN thumbnail_status NOT IN ('ready', 'failed') OR thumbnail_status IS NULL THEN 1 ELSE 0 END) as thumbnail_pending,
		SUM(CASE WHEN gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL THEN 1 ELSE 0 END) as geocode_total,
		SUM(CASE WHEN (gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL) AND (geocode_status = 'ready' OR (COALESCE(TRIM(location), '') != '')) THEN 1 ELSE 0 END) as geocode_ready,
		SUM(CASE WHEN (gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL) AND geocode_status = 'failed' THEN 1 ELSE 0 END) as geocode_failed,
		SUM(CASE WHEN (gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL) AND geocode_status != 'ready' AND geocode_status != 'failed' AND (COALESCE(TRIM(location), '') = '') THEN 1 ELSE 0 END) as geocode_pending`

	err := r.db.Model(&model.Photo{}).Scopes(activeScope).
		Select(selectExpr).
		Where(whereExpr, args...).
		Group("path_group").
		Find(&rows).Error

	if err != nil {
		return nil, err
	}

	for _, r := range rows {
		if r.PathGroup == "" {
			continue
		}
		result[r.PathGroup] = &model.PathDerivedStatus{
			PhotoTotal:       r.PhotoTotal,
			AnalyzedTotal:    r.AnalyzedTotal,
			ThumbnailTotal:   r.ThumbnailTotal,
			ThumbnailReady:   r.ThumbnailReady,
			ThumbnailFailed:  r.ThumbnailFailed,
			ThumbnailPending: r.ThumbnailPending,
			GeocodeTotal:     r.GeocodeTotal,
			GeocodeReady:     r.GeocodeReady,
			GeocodeFailed:    r.GeocodeFailed,
			GeocodePending:   r.GeocodePending,
		}
	}

	// 确保所有请求的路径都有结果
	for _, prefix := range prefixes {
		if _, ok := result[prefix]; !ok {
			result[prefix] = &model.PathDerivedStatus{}
		}
	}

	return result, nil
}

// buildFTSQuery 构建 FTS5 MATCH 查询字符串
// 按空格分词，每个词用双引号包裹防止 FTS5 语法冲突，词间隐式 AND
func buildFTSQuery(search string) string {
	words := strings.Fields(search)
	if len(words) == 0 {
		return `""`
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		// 转义双引号
		w = strings.ReplaceAll(w, `"`, `""`)
		quoted[i] = `"` + w + `"`
	}
	return strings.Join(quoted, " ")
}

func personCategoryFromRank(rank int) string {
	switch rank {
	case 4:
		return model.PersonCategoryFamily
	case 3:
		return model.PersonCategoryFriend
	case 2:
		return model.PersonCategoryAcquaintance
	case 1:
		return model.PersonCategoryStranger
	default:
		return ""
	}
}

// GetScatteredHighQuality 获取无事件、高颜值、从未展示的散片（角落遗珠）
func (r *photoRepository) GetScatteredHighQuality(minBeauty int, excludeIDs []uint, limit int) ([]*model.Photo, error) {
	var photos []*model.Photo
	query := r.db.Scopes(activeScope).
		Where("event_id IS NULL").
		Where("ai_analyzed = ?", true).
		Where("beauty_score >= ?", minBeauty).
		Where("id NOT IN (SELECT DISTINCT photo_id FROM display_records)")

	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}

	err := query.Order("beauty_score DESC").Limit(limit).Find(&photos).Error
	return photos, err
}
