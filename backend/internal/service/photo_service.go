package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

// PhotoService 照片服务接口
type PhotoService interface {
	ScanDirectory(dir string) ([]*model.Photo, error)
	CleanupNonExistentPhotos() (*model.CleanupPhotosResponse, error) // 清理数据库中所有不存在的照片

	// 异步扫描
	StartScan(path string) (*model.ScanTask, error)
	StartRebuild(path string) (*model.ScanTask, error)
	StopScanTask(id string) (*model.ScanTask, error)
	GetScanTask() *model.ScanTask
	HandleShutdown() error
	RunAutoScanCheck() error

	// 查询
	GetPhotoByID(id uint) (*model.Photo, error)
	GetPhotos(req *model.GetPhotosRequest) ([]*model.Photo, int64, error)

	// 统计
	CountAll() (int64, error)
	CountAnalyzed() (int64, error)
	CountUnanalyzed() (int64, error)

	// 分类和标签
	GetCategories() ([]string, error)
	GetTags(query string, limit int) ([]model.TagWithCount, int64, error)

	// 地理编码
	GeocodePhotoIfNeeded(photo *model.Photo) error
	RegeocodeAllPhotos() (int, error) // 重新解析所有有GPS照片的位置

	// 删除路径相关
	DeletePhotosByPathPrefix(pathPrefix string) (int64, error)
	GetPhotoIDsByPathPrefix(pathPrefix string) ([]uint, error)
	GetPhotosByPathPrefix(pathPrefix string) ([]*model.Photo, error)

	// 路径统计
	CountPhotosByPathPrefix(pathPrefix string) (int64, error)
	GetPathDerivedStatus(pathPrefix string) (*model.PathDerivedStatus, error)
	GetPathDerivedStatusBatch(prefixes []string) (map[string]*model.PathDerivedStatus, error)

	// 按状态计数
	CountByStatus() (*model.PhotoCountsResponse, error)

	// 照片状态管理
	BatchUpdateStatus(req *model.BatchUpdateStatusRequest) (int64, error)

	// 分类更新
	UpdateCategory(id uint, category string) error

	// 方向覆盖（更新 orientation 并重新生成缩略图）
	UpdateOrientation(id uint, orientation int) error

	// 事件聚类服务注入（解决循环初始化）
	SetEventClusteringService(EventClusteringService)
}

// photoService 照片服务实现
type photoService struct {
	repo                    repository.PhotoRepository
	photoTagRepo            repository.PhotoTagRepository
	scanJobRepo             repository.ScanJobRepository
	config                  *config.Config
	configService           ConfigService
	geocodeService          GeocodeService
	thumbnailGenerator      *util.ThumbnailGenerator
	thumbnailService        ThumbnailService
	geocodeTaskService      GeocodeTaskService
	eventClusteringService  EventClusteringService
	processPhotoFunc        func(string, os.FileInfo) (*model.Photo, error)
	activeJob               *activeScanJob
	taskMutex               sync.RWMutex
	autoScanMutex           sync.Mutex
	lastAutoScanCheck       time.Time
}

// NewPhotoService 创建照片服务
func NewPhotoService(repo repository.PhotoRepository, photoTagRepo repository.PhotoTagRepository, scanJobRepo repository.ScanJobRepository, cfg *config.Config, configService ConfigService, geocodeService GeocodeService, thumbnailService ThumbnailService, geocodeTaskService GeocodeTaskService) PhotoService {
	// 初始化缩略图生成器（1024px，兼顾展示和 AI 理解）
	thumbnailGenerator := util.NewThumbnailGenerator(1024, 1024, 90, cfg.Photos.ThumbnailPath)

	service := &photoService{
		repo:               repo,
		photoTagRepo:       photoTagRepo,
		scanJobRepo:        scanJobRepo,
		config:             cfg,
		configService:      configService,
		geocodeService:     geocodeService,
		thumbnailGenerator: thumbnailGenerator,
		thumbnailService:   thumbnailService,
		geocodeTaskService: geocodeTaskService,
	}
	service.processPhotoFunc = service.processPhoto

	if service.scanJobRepo != nil {
		if err := service.scanJobRepo.InterruptNonTerminal("task interrupted because service restarted"); err != nil {
			logger.Warnf("Interrupt stale scan jobs failed: %v", err)
		}
	}

	return service
}

// GetPhotoByID 根据 ID 获取照片
func (s *photoService) GetPhotoByID(id uint) (*model.Photo, error) {
	photo, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	s.enrichPhotoTags([]*model.Photo{photo})
	return photo, nil
}

// GetPhotos 获取照片列表
func (s *photoService) GetPhotos(req *model.GetPhotosRequest) ([]*model.Photo, int64, error) {
	// 设置默认值
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 1 {
		req.PageSize = 20
	}
	if req.PageSize > 100 {
		req.PageSize = 100
	}

	// 获取启用的扫描路径
	enabledPaths, err := s.getEnabledScanPaths()
	if err != nil {
		logger.Warnf("Failed to get enabled scan paths: %v", err)
		// 如果获取失败，仍然返回结果，但不过滤路径
		enabledPaths = nil
	}

	// 调用 Repository
	photos, total, err := s.repo.List(req.Page, req.PageSize, req.Analyzed, req.HasThumbnail, req.HasGPS, req.Location, req.Search, req.Category, req.Tag, req.SortBy, req.SortDesc, enabledPaths, req.Status)
	if err != nil {
		return nil, 0, err
	}
	s.enrichPhotoTags(photos)
	return photos, total, nil
}

// CountAll 统计照片总数
func (s *photoService) CountAll() (int64, error) {
	return s.repo.Count()
}

// CountAnalyzed 统计已分析照片数
func (s *photoService) CountAnalyzed() (int64, error) {
	return s.repo.CountAnalyzed()
}

// CountUnanalyzed 统计未分析照片数
func (s *photoService) CountUnanalyzed() (int64, error) {
	return s.repo.CountUnanalyzed()
}

// GetCategories 获取所有分类
func (s *photoService) GetCategories() ([]string, error) {
	return s.repo.GetCategories()
}

// GetTags 获取热门标签
func (s *photoService) GetTags(query string, limit int) ([]model.TagWithCount, int64, error) {
	tags, err := s.repo.GetTags(query, limit)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountTags()
	if err != nil {
		return tags, 0, err
	}
	return tags, total, nil
}

// enrichPhotoTags 从 photo_tags 表批量加载标签填充到 TagList 字段
func (s *photoService) enrichPhotoTags(photos []*model.Photo) {
	if len(photos) == 0 || s.photoTagRepo == nil {
		return
	}
	ids := make([]uint, 0, len(photos))
	for _, p := range photos {
		ids = append(ids, p.ID)
	}
	tagMap, err := s.photoTagRepo.GetTagsByPhotoIDs(ids)
	if err != nil {
		logger.Warnf("Failed to load photo tags: %v", err)
		return
	}
	for _, p := range photos {
		if tags, ok := tagMap[p.ID]; ok {
			p.TagList = tags
		}
	}
}

// DeletePhotosByPathPrefix 根据路径前缀删除照片
func (s *photoService) DeletePhotosByPathPrefix(pathPrefix string) (int64, error) {
	photos, err := s.repo.ListByPathPrefix(pathPrefix)
	if err != nil {
		return 0, fmt.Errorf("list photos by path prefix: %w", err)
	}

	count := int64(0)
	for _, photo := range photos {
		if err := s.repo.Delete(photo.ID); err != nil {
			logger.Warnf("Failed to delete photo %d: %v", photo.ID, err)
			continue
		}
		count++
	}

	logger.Infof("Deleted %d photos with path prefix: %s", count, pathPrefix)
	return count, nil
}

// GetPhotoIDsByPathPrefix 根据路径前缀获取照片ID列表
func (s *photoService) GetPhotoIDsByPathPrefix(pathPrefix string) ([]uint, error) {
	photos, err := s.repo.ListByPathPrefix(pathPrefix)
	if err != nil {
		return nil, fmt.Errorf("list photos by path prefix: %w", err)
	}

	ids := make([]uint, 0, len(photos))
	for _, photo := range photos {
		ids = append(ids, photo.ID)
	}

	return ids, nil
}

// GetPhotosByPathPrefix 根据路径前缀获取照片列表
func (s *photoService) GetPhotosByPathPrefix(pathPrefix string) ([]*model.Photo, error) {
	photos, err := s.repo.ListByPathPrefix(pathPrefix)
	if err != nil {
		return nil, fmt.Errorf("list photos by path prefix: %w", err)
	}

	return photos, nil
}

// CountPhotosByPathPrefix 根据路径前缀统计照片数量
func (s *photoService) CountPhotosByPathPrefix(pathPrefix string) (int64, error) {
	count, err := s.repo.CountByPathPrefix(pathPrefix)
	if err != nil {
		return 0, fmt.Errorf("count photos by path prefix: %w", err)
	}
	return count, nil
}

func (s *photoService) GetPathDerivedStatus(pathPrefix string) (*model.PathDerivedStatus, error) {
	status, err := s.repo.GetDerivedStatusByPathPrefix(pathPrefix)
	if err != nil {
		return nil, fmt.Errorf("get derived status by path prefix: %w", err)
	}
	return status, nil
}

func (s *photoService) GetPathDerivedStatusBatch(prefixes []string) (map[string]*model.PathDerivedStatus, error) {
	return s.repo.GetDerivedStatusByPathPrefixes(prefixes)
}

func (s *photoService) CountByStatus() (*model.PhotoCountsResponse, error) {
	return s.repo.CountByStatus()
}

// BatchUpdateStatus 批量更新照片状态
func (s *photoService) BatchUpdateStatus(req *model.BatchUpdateStatusRequest) (int64, error) {
	return s.repo.BatchUpdateStatus(req.PhotoIDs, req.Status)
}

// UpdateCategory 更新照片分类
func (s *photoService) UpdateCategory(id uint, category string) error {
	return s.repo.UpdateCategory(id, category)
}

// UpdateOrientation 手动覆盖方向并重新生成缩略图
func (s *photoService) UpdateOrientation(id uint, orientation int) error {
	if err := s.repo.UpdateOrientation(id, orientation); err != nil {
		return err
	}
	// 异步重新生成缩略图（force=true 强制覆盖）
	go func() {
		if err := s.thumbnailService.GeneratePhoto(id, true); err != nil {
			logger.Warnf("Regenerate thumbnail after orientation update (photo %d): %v", id, err)
		}
	}()
	return nil
}

// SetEventClusteringService 注入事件聚类服务（避免循环初始化）
func (s *photoService) SetEventClusteringService(svc EventClusteringService) {
	s.eventClusteringService = svc
}

// getEnabledScanPaths 获取启用的扫描路径列表
func (s *photoService) getEnabledScanPaths() ([]string, error) {
	configValue, err := s.configService.GetWithDefault("photos.scan_paths", "")
	if err != nil {
		return nil, fmt.Errorf("get scan paths config: %w", err)
	}

	if configValue == "" {
		return []string{}, nil
	}

	var scanPathsConfig struct {
		Paths []struct {
			Path    string `json:"path"`
			Enabled bool   `json:"enabled"`
		} `json:"paths"`
	}

	if err := json.Unmarshal([]byte(configValue), &scanPathsConfig); err != nil {
		return nil, fmt.Errorf("parse scan paths config: %w", err)
	}

	var enabledPaths []string
	for _, p := range scanPathsConfig.Paths {
		if p.Enabled {
			enabledPaths = append(enabledPaths, p.Path)
		}
	}

	return enabledPaths, nil
}

// GeocodePhotoIfNeeded 如果照片有GPS但没有location，则进行地理编码
// 这个方法会实时获取位置并异步回写到数据库
func (s *photoService) GeocodePhotoIfNeeded(photo *model.Photo) error {
	// 检查是否需要地理编码
	if photo.GPSLatitude == nil || photo.GPSLongitude == nil {
		return nil // 没有GPS坐标
	}
	// 排除无效坐标 0,0
	if *photo.GPSLatitude == 0 && *photo.GPSLongitude == 0 {
		return nil
	}

	if photo.Location != "" {
		return nil // 已经有位置信息
	}

	if s.geocodeService == nil {
		logger.Debug("Geocode service not available")
		return nil // 地理编码服务不可用
	}

	// 实时进行地理编码
	location, err := s.geocodeService.ReverseGeocode(*photo.GPSLatitude, *photo.GPSLongitude)
	if err != nil {
		logger.Warnf("Real-time geocode failed for photo %d: %v", photo.ID, err)
		return nil // 不返回错误，允许继续显示照片
	}

	// 设置位置信息（立即返回给前端）- 使用标准显示格式
	photo.Location = location.FormatDisplay()
	photo.Country = location.Country
	photo.Province = location.Province
	photo.City = location.City
	photo.District = location.District
	photo.Street = location.Street
	photo.POI = location.POI
	logger.Debugf("Real-time geocoded photo %d: (%f, %f) -> %s",
		photo.ID, *photo.GPSLatitude, *photo.GPSLongitude, photo.Location)

	// 异步回写到数据库
	loc := &model.LocationFields{
		Location: photo.Location,
		Country:  location.Country,
		Province: location.Province,
		City:     location.City,
		District: location.District,
		Street:   location.Street,
		POI:      location.POI,
	}
	go func() {
		if err := s.repo.UpdateLocationFull(photo.ID, loc); err != nil {
			logger.Errorf("Failed to update location for photo %d: %v", photo.ID, err)
		} else {
			logger.Debugf("Location saved to database for photo %d: %s", photo.ID, loc.Location)
		}
	}()

	return nil
}

// RegeocodeAllPhotos 重新解析所有有GPS照片的位置
// 返回成功更新的照片数量
func (s *photoService) RegeocodeAllPhotos() (int, error) {
	if s.geocodeService == nil {
		return 0, fmt.Errorf("geocode service not available")
	}

	// 获取所有有GPS坐标的照片
	photos, err := s.repo.ListWithGPS()
	if err != nil {
		return 0, fmt.Errorf("list photos with GPS: %w", err)
	}

	logger.Infof("Starting re-geocoding for %d photos", len(photos))

	updated := 0
	failed := 0
	for _, photo := range photos {
		if photo.GPSLatitude == nil || photo.GPSLongitude == nil {
			continue
		}
		// 排除无效坐标 0,0
		if *photo.GPSLatitude == 0 && *photo.GPSLongitude == 0 {
			continue
		}

		// 重新解析位置
		location, err := s.geocodeService.ReverseGeocode(*photo.GPSLatitude, *photo.GPSLongitude)
		if err != nil {
			logger.Warnf("Re-geocode failed for photo %d: %v", photo.ID, err)
			failed++
			continue
		}

		newLocation := location.FormatDisplay()

		// 更新数据库（强制覆盖所有位置字段，包括结构化字段回填）
		loc := &model.LocationFields{
			Location: newLocation,
			Country:  location.Country,
			Province: location.Province,
			City:     location.City,
			District: location.District,
			Street:   location.Street,
			POI:      location.POI,
		}
		if err := s.repo.UpdateLocationFull(photo.ID, loc); err != nil {
			logger.Errorf("Failed to update location for photo %d: %v", photo.ID, err)
			failed++
			continue
		}

		logger.Debugf("Re-geocoded photo %d: %s -> %s", photo.ID, photo.Location, newLocation)
		updated++
	}

	logger.Infof("Re-geocoding completed: updated=%d, failed=%d, total=%d", updated, failed, len(photos))
	return updated, nil
}

// shouldExcludeDir 检查是否应该排除目录
func (s *photoService) shouldExcludeDir(dirName string) bool {
	for _, exclude := range s.config.Photos.ExcludeDirs {
		if dirName == exclude {
			return true
		}
	}
	return false
}

// isSupportedFormat 检查是否是支持的格式
func (s *photoService) isSupportedFormat(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	for _, format := range s.config.Photos.SupportedFormats {
		if ext == format {
			return true
		}
	}
	return false
}
