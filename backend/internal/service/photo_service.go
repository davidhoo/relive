package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/analyzer"
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
}

// photoService 照片服务实现
type photoService struct {
	repo               repository.PhotoRepository
	photoTagRepo       repository.PhotoTagRepository
	scanJobRepo        repository.ScanJobRepository
	config             *config.Config
	configService      ConfigService
	geocodeService     GeocodeService
	thumbnailGenerator *util.ThumbnailGenerator
	thumbnailService   ThumbnailService
	geocodeTaskService GeocodeTaskService
	processPhotoFunc   func(string, os.FileInfo) (*model.Photo, error)
	activeJob          *activeScanJob
	taskMutex          sync.RWMutex
	autoScanMutex      sync.Mutex
	lastAutoScanCheck  time.Time
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

type activeScanJob struct {
	id              string
	taskType        string
	path            string
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	terminalStatus  string
	terminalMessage string
	mu              sync.RWMutex
}

func (j *activeScanJob) setTerminal(status, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.terminalStatus = status
	j.terminalMessage = message
}

func (j *activeScanJob) terminal() (string, string) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.terminalStatus, j.terminalMessage
}

type autoScanConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
}

type scanPathConfig struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Path            string     `json:"path"`
	IsDefault       bool       `json:"is_default"`
	Enabled         bool       `json:"enabled"`
	AutoScanEnabled *bool      `json:"auto_scan_enabled,omitempty"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
	LastScannedAt   *time.Time `json:"last_scanned_at,omitempty"`
}

type scanPathsConfig struct {
	Paths []scanPathConfig `json:"paths"`
}

type scanTreeNode struct {
	Path    string `json:"path"`
	ModTime int64  `json:"mod_time"`
}

type scanTreeSnapshot struct {
	RootPath    string         `json:"root_path"`
	GeneratedAt time.Time      `json:"generated_at"`
	Nodes       []scanTreeNode `json:"nodes"`
}

func defaultAutoScanConfig() autoScanConfig {
	return autoScanConfig{Enabled: false, IntervalMinutes: 60}
}

func (s *photoService) loadAutoScanConfig() (autoScanConfig, error) {
	if s.configService == nil {
		return defaultAutoScanConfig(), nil
	}
	value, err := s.configService.GetWithDefault("photos.auto_scan", "")
	if err != nil || value == "" {
		return defaultAutoScanConfig(), nil
	}
	cfg := defaultAutoScanConfig()
	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		return defaultAutoScanConfig(), err
	}
	if cfg.IntervalMinutes <= 0 {
		cfg.IntervalMinutes = 60
	}
	return cfg, nil
}

func (s *photoService) loadScanPathsConfig() (scanPathsConfig, error) {
	var cfg scanPathsConfig
	if s.configService == nil {
		return cfg, nil
	}
	value, err := s.configService.GetWithDefault("photos.scan_paths", "")
	if err != nil || value == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		return scanPathsConfig{}, err
	}
	for i := range cfg.Paths {
		if cfg.Paths[i].AutoScanEnabled == nil {
			enabled := true
			cfg.Paths[i].AutoScanEnabled = &enabled
		}
	}
	return cfg, nil
}

func (s *photoService) saveScanPathsConfig(cfg scanPathsConfig) error {
	if s.configService == nil {
		return nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.configService.Set("photos.scan_paths", string(data))
}

func (s *photoService) scanTreeConfigKey(pathID string) string {
	return "photos.scan_tree." + pathID
}

func (s *photoService) loadScanTreeSnapshot(pathID string) (*scanTreeSnapshot, error) {
	if s.configService == nil {
		return nil, nil
	}
	value, err := s.configService.GetWithDefault(s.scanTreeConfigKey(pathID), "")
	if err != nil || value == "" {
		return nil, nil
	}
	var snapshot scanTreeSnapshot
	if err := json.Unmarshal([]byte(value), &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *photoService) saveScanTreeSnapshot(pathID string, snapshot *scanTreeSnapshot) error {
	if s.configService == nil || snapshot == nil {
		return nil
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return s.configService.Set(s.scanTreeConfigKey(pathID), string(data))
}

func (s *photoService) buildScanTreeSnapshot(rootPath string) (*scanTreeSnapshot, error) {
	nodes := make([]scanTreeNode, 0, 32)
	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if path != rootPath && s.shouldExcludeDir(info.Name()) {
			return filepath.SkipDir
		}
		nodes = append(nodes, scanTreeNode{Path: path, ModTime: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &scanTreeSnapshot{RootPath: rootPath, GeneratedAt: time.Now(), Nodes: nodes}, nil
}

func (s *photoService) scanTreeChangedDirs(snapshot *scanTreeSnapshot) ([]string, error) {
	if snapshot == nil {
		return nil, nil
	}

	changedDirs := make([]string, 0)
	for _, node := range snapshot.Nodes {
		info, err := os.Stat(node.Path)
		if os.IsNotExist(err) {
			changedDirs = append(changedDirs, nearestExistingAncestor(node.Path, snapshot.RootPath))
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		if info.ModTime().UnixNano() != node.ModTime {
			changedDirs = append(changedDirs, node.Path)
		}
	}

	return compressChangedDirs(changedDirs, snapshot.RootPath), nil
}

func nearestExistingAncestor(path string, rootPath string) string {
	current := path
	for {
		if _, err := os.Stat(current); err == nil {
			return current
		}
		if current == rootPath {
			return rootPath
		}
		parent := filepath.Dir(current)
		if parent == current {
			return rootPath
		}
		current = parent
	}
}

func compressChangedDirs(changedDirs []string, rootPath string) []string {
	if len(changedDirs) == 0 {
		return nil
	}

	unique := make(map[string]struct{}, len(changedDirs))
	for _, dir := range changedDirs {
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		unique[clean] = struct{}{}
	}

	dirs := make([]string, 0, len(unique))
	for dir := range unique {
		dirs = append(dirs, dir)
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) < len(dirs[j])
	})

	result := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		covered := false
		for _, existing := range result {
			if dir == existing || strings.HasPrefix(dir, existing+string(os.PathSeparator)) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, dir)
		}
	}

	if len(result) == 0 {
		return []string{rootPath}
	}
	return result
}

func (s *photoService) shouldRunAutoScan(intervalMinutes int) bool {
	s.autoScanMutex.Lock()
	defer s.autoScanMutex.Unlock()
	if intervalMinutes <= 0 {
		intervalMinutes = 60
	}
	now := time.Now()
	if s.lastAutoScanCheck.IsZero() || now.Sub(s.lastAutoScanCheck) >= time.Duration(intervalMinutes)*time.Minute {
		s.lastAutoScanCheck = now
		return true
	}
	return false
}

func (s *photoService) RunAutoScanCheck() error {
	cfg, err := s.loadAutoScanConfig()
	if err != nil {
		return err
	}
	if !cfg.Enabled || !s.shouldRunAutoScan(cfg.IntervalMinutes) {
		return nil
	}

	task := s.GetScanTask()
	if task != nil && task.IsRunning() {
		logger.Infof("Skipping auto scan check because a scan task is already running")
		return nil
	}

	pathsCfg, err := s.loadScanPathsConfig()
	if err != nil {
		return err
	}

	for _, path := range pathsCfg.Paths {
		if !path.Enabled || path.AutoScanEnabled == nil || !*path.AutoScanEnabled || path.LastScannedAt == nil {
			continue
		}
		if _, err := os.Stat(path.Path); os.IsNotExist(err) {
			logger.Warnf("Auto scan skipped for missing path: %s", path.Path)
			continue
		}

		snapshot, err := s.loadScanTreeSnapshot(path.ID)
		if err != nil {
			logger.Warnf("Load scan tree snapshot failed for %s: %v", path.Path, err)
			continue
		}
		if snapshot == nil {
			snapshot, err = s.buildScanTreeSnapshot(path.Path)
			if err != nil {
				logger.Warnf("Build initial scan tree snapshot failed for %s: %v", path.Path, err)
				continue
			}
			if err := s.saveScanTreeSnapshot(path.ID, snapshot); err != nil {
				logger.Warnf("Save initial scan tree snapshot failed for %s: %v", path.Path, err)
			}
			continue
		}

		changedDirs, err := s.scanTreeChangedDirs(snapshot)
		if err != nil {
			logger.Warnf("Check scan tree changes failed for %s: %v", path.Path, err)
			continue
		}
		if len(changedDirs) == 0 {
			continue
		}

		scanRoot := path.Path
		if len(changedDirs) == 1 {
			scanRoot = changedDirs[0]
			logger.Infof("Auto scan detected single changed subtree for %s: %s", path.Path, scanRoot)
		} else {
			logger.Infof("Auto scan detected multiple changed subtrees for %s, falling back to full path scan: %v", path.Path, changedDirs)
		}

		if _, err := s.StartScan(scanRoot); err != nil {
			logger.Warnf("Auto scan start failed for %s: %v", scanRoot, err)
		}
		return nil
	}

	return nil
}

// CleanupNonExistentPhotos 清理数据库中所有文件已不存在的照片
// 遍历整个数据库，检查每个照片文件是否还存在，不存在的则软删除
func (s *photoService) CleanupNonExistentPhotos() (*model.CleanupPhotosResponse, error) {
	logger.Info("Starting cleanup of non-existent photos")

	// 1. 获取数据库中的所有照片
	allPhotos, err := s.repo.ListAll()
	if err != nil {
		return nil, fmt.Errorf("list all photos: %w", err)
	}

	totalCount := len(allPhotos)
	deletedCount := 0
	skippedCount := 0

	logger.Infof("Found %d photos in database to check", totalCount)

	// 2. 检查每张照片的文件是否存在
	for _, photo := range allPhotos {
		// 检查文件是否存在
		if _, err := os.Stat(photo.FilePath); os.IsNotExist(err) {
			// 文件已不存在，软删除数据库记录
			if err := s.repo.Delete(photo.ID); err != nil {
				logger.Errorf("Soft delete photo failed: id=%d, path=%s, error=%v", photo.ID, photo.FilePath, err)
				continue
			}
			deletedCount++
			logger.Infof("Soft deleted photo (file not exists): id=%d, path=%s", photo.ID, photo.FilePath)
		} else if err != nil {
			// 其他错误（如权限问题），记录警告但不删除
			logger.Warnf("Cannot access photo file: id=%d, path=%s, error=%v", photo.ID, photo.FilePath, err)
			skippedCount++
		}
	}

	logger.Infof("Photo cleanup completed: total=%d, deleted=%d, skipped=%d", totalCount, deletedCount, skippedCount)

	return &model.CleanupPhotosResponse{
		TotalCount:   totalCount,
		DeletedCount: deletedCount,
		SkippedCount: skippedCount,
	}, nil
}

// ScanDirectory 扫描目录
func (s *photoService) ScanDirectory(dir string) ([]*model.Photo, error) {
	var photos []*model.Photo

	// 遍历目录
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 跳过目录
		if info.IsDir() {
			// 检查是否是排除目录
			if s.shouldExcludeDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// 检查文件格式
		if !s.isSupportedFormat(path) {
			return nil
		}

		// 处理照片
		photo, err := s.processPhoto(path, info)
		if err != nil {
			logger.Warnf("Process photo failed: %s, error: %v", path, err)
			return nil // 继续处理其他文件
		}

		photos = append(photos, photo)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return photos, nil
}

// processPhoto 处理单张照片
func (s *photoService) processPhoto(filePath string, info os.FileInfo) (*model.Photo, error) {
	// 计算文件哈希
	fileHash, err := util.HashFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("hash file: %w", err)
	}

	// 提取 EXIF 信息
	exifData, err := util.ExtractEXIF(filePath)
	if err != nil {
		logger.Warnf("Extract EXIF failed: %s, error: %v", filePath, err)
		exifData = &util.EXIFData{} // 使用空数据
	}
	if exifData == nil {
		exifData = &util.EXIFData{}
	}

	// 获取图片尺寸（如果 EXIF 中没有）
	width := exifData.Width
	height := exifData.Height
	if width == 0 || height == 0 {
		width, height, err = util.GetImageSize(filePath)
		if err != nil {
			logger.Warnf("Get image size failed: %s, error: %v", filePath, err)
			// 使用默认值
			width = 0
			height = 0
		}
	}

	// 获取文件时间
	fileTimes := util.GetFileTimes(info)

	// 构建 Photo 对象
	now := time.Now()
	photo := &model.Photo{
		FilePath:       filePath,
		FileName:       filepath.Base(filePath),
		FileSize:       info.Size(),
		FileHash:       fileHash,
		FileModTime:    &fileTimes.ModTime,
		FileCreateTime: fileTimes.CreateTime,
		TakenAt:        exifData.TakenAt,
		CameraModel:    exifData.CameraModel,
		Width:          width,
		Height:         height,
		Orientation:    exifData.Orientation,
		GPSLatitude:    exifData.GPSLatitude,
		GPSLongitude:   exifData.GPSLongitude,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if photo.GPSLatitude != nil && photo.GPSLongitude != nil {
		photo.GeocodeStatus = "pending"
	} else {
		photo.GeocodeStatus = "none"
	}
	photo.GeocodeProvider = ""
	photo.GeocodedAt = nil

	photo.ThumbnailPath = util.GenerateDerivedImagePath(filePath)
	photo.ThumbnailStatus = "pending"
	photo.ThumbnailGeneratedAt = nil

	return photo, nil
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

// ==================== Async Scan Methods ====================

type scanProgress struct {
	mu sync.Mutex

	phase           string
	totalFiles      int
	discoveredFiles int
	processedFiles  int
	newPhotos       int
	updatedPhotos   int
	deletedPhotos   int
	skippedFiles    int
	currentFile     string
	dirty           bool
}

type scanFileTask struct {
	path string
	info os.FileInfo
}

func (s *photoService) StartScan(path string) (*model.ScanTask, error) {
	return s.startScanJob(path, "scan", false)
}

func (s *photoService) StartRebuild(path string) (*model.ScanTask, error) {
	return s.startScanJob(path, "rebuild", true)
}

func (s *photoService) StopScanTask(id string) (*model.ScanTask, error) {
	s.taskMutex.RLock()
	active := s.activeJob
	s.taskMutex.RUnlock()

	if active == nil {
		return nil, fmt.Errorf("no active scan task")
	}
	if id != "" && active.id != id {
		return nil, fmt.Errorf("scan task not found")
	}

	now := time.Now()
	active.setTerminal("stopped", "task stopped by user")
	if err := s.scanJobRepo.UpdateFields(active.id, map[string]interface{}{
		"status":            "stopping",
		"phase":             "stopping",
		"stop_requested_at": &now,
		"last_heartbeat_at": &now,
	}); err != nil {
		return nil, err
	}
	active.cancel()

	job, err := s.scanJobRepo.GetByID(active.id)
	if err != nil {
		return nil, err
	}
	return scanJobToTask(job), nil
}

func (s *photoService) GetScanTask() *model.ScanTask {
	if s.scanJobRepo == nil {
		return nil
	}
	job, err := s.scanJobRepo.GetLatest()
	if err != nil {
		logger.Warnf("Get latest scan task failed: %v", err)
		return nil
	}
	return scanJobToTask(job)
}

func (s *photoService) HandleShutdown() error {
	s.taskMutex.RLock()
	active := s.activeJob
	s.taskMutex.RUnlock()
	if active == nil {
		if s.scanJobRepo == nil {
			return nil
		}
		return s.scanJobRepo.InterruptNonTerminal("task interrupted by service shutdown")
	}

	now := time.Now()
	active.setTerminal("interrupted", "task interrupted by service shutdown")
	if err := s.scanJobRepo.UpdateFields(active.id, map[string]interface{}{
		"status":            "interrupted",
		"phase":             "stopping",
		"error_message":     "task interrupted by service shutdown",
		"completed_at":      &now,
		"last_heartbeat_at": &now,
	}); err != nil {
		return err
	}
	active.cancel()
	return nil
}

func (s *photoService) startScanJob(path string, taskType string, rebuild bool) (*model.ScanTask, error) {
	s.taskMutex.Lock()
	if s.activeJob != nil {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("scan task already running")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("path does not exist: %s", path)
	}

	now := time.Now()
	job := &model.ScanJob{
		ID:              fmt.Sprintf("%s_%d", taskType, now.UnixNano()),
		Type:            taskType,
		Status:          "pending",
		Path:            path,
		Phase:           "pending",
		StartedAt:       now,
		LastHeartbeatAt: &now,
	}
	if err := s.scanJobRepo.Create(job); err != nil {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("create scan job: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runtime := &activeScanJob{
		id:             job.ID,
		taskType:       taskType,
		path:           path,
		ctx:            ctx,
		cancel:         cancel,
		done:           make(chan struct{}),
		terminalStatus: "completed",
	}
	s.activeJob = runtime
	s.taskMutex.Unlock()

	logger.Infof("Starting async %s: path=%s, task_id=%s", taskType, path, job.ID)
	go s.runScanTask(runtime, path, rebuild)

	return scanJobToTask(job), nil
}

func (s *photoService) runScanTask(runtime *activeScanJob, path string, rebuild bool) {
	defer func() {
		close(runtime.done)
		s.clearActiveJob(runtime.id)
	}()

	now := time.Now()
	if err := s.scanJobRepo.UpdateFields(runtime.id, map[string]interface{}{
		"status":            "running",
		"phase":             "discovering",
		"last_heartbeat_at": &now,
	}); err != nil {
		logger.Errorf("[Task %s] Update start status failed: %v", runtime.id, err)
		return
	}

	workers := s.config.Performance.MaxScanWorkers
	if workers <= 0 {
		workers = 1
	}

	existingPhotos, err := s.repo.ListByPathPrefix(path)
	if err != nil {
		logger.Warnf("[Task %s] Load existing photos failed: %v", runtime.id, err)
		existingPhotos = nil
	}
	existingByPath := make(map[string]*model.Photo, len(existingPhotos))
	for _, photo := range existingPhotos {
		existingByPath[photo.FilePath] = photo
	}

	seenFiles := struct {
		sync.Mutex
		items map[string]struct{}
	}{items: make(map[string]struct{}, workers*2)}

	progress := &scanProgress{phase: "discovering", dirty: true}
	flushStop := make(chan struct{})
	flushDone := make(chan struct{})
	go s.flushScanProgressLoop(runtime.id, progress, flushStop, flushDone)

	var scanNodes []scanTreeNode
	pool := analyzer.NewWorkerPool(workers)
	pool.Start()

	walkErr := filepath.Walk(path, func(currentPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			progress.incrementSkipped(1)
			return nil
		}

		select {
		case <-runtime.ctx.Done():
			return runtime.ctx.Err()
		default:
		}

		if info.IsDir() {
			if currentPath != path && s.shouldExcludeDir(info.Name()) {
				return filepath.SkipDir
			}
			scanNodes = append(scanNodes, scanTreeNode{Path: currentPath, ModTime: info.ModTime().UnixNano()})
			return nil
		}

		if !s.isSupportedFormat(currentPath) {
			return nil
		}

		progress.onDiscovered(filepath.Base(currentPath))
		task := scanFileTask{path: currentPath, info: info}
		if err := pool.Submit(func(ctx context.Context) error {
			return s.processScanFile(ctx, runtime.id, task, rebuild, existingByPath, &seenFiles, progress)
		}); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			progress.incrementSkipped(1)
			logger.Warnf("[Task %s] Submit scan task failed for %s: %v", runtime.id, currentPath, err)
		}
		return nil
	})

	if walkErr == nil {
		progress.setPhase("processing")
	}

	pool.Wait()
	close(flushStop)
	<-flushDone

	if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		logger.Errorf("[Task %s] Walk scan path failed: %v", runtime.id, walkErr)
		s.finishScanTask(runtime, progress, "failed", walkErr.Error(), false, nil)
		return
	}

	if errors.Is(runtime.ctx.Err(), context.Canceled) {
		status, message := runtime.terminal()
		if status == "" {
			status = "stopped"
			message = "task cancelled"
		}
		s.finishScanTask(runtime, progress, status, message, false, nil)
		return
	}

	progress.setPhase("finalizing")
	if len(existingPhotos) > 0 {
		for _, existing := range existingPhotos {
			seenFiles.Lock()
			_, ok := seenFiles.items[existing.FilePath]
			seenFiles.Unlock()
			if ok {
				continue
			}
			if err := s.repo.Delete(existing.ID); err != nil {
				logger.Warnf("[Task %s] Delete missing photo failed: %v", runtime.id, err)
				continue
			}
			progress.incrementDeleted(1)
		}
	}

	if err := s.updateScanPathTimestamp(path); err != nil {
		logger.Warnf("[Task %s] Failed to update scan path timestamp: %v", runtime.id, err)
	}
	if err := s.updateScanTreeSnapshotWithSnapshot(path, &scanTreeSnapshot{RootPath: path, GeneratedAt: time.Now(), Nodes: scanNodes}); err != nil {
		logger.Warnf("[Task %s] Failed to update scan tree snapshot: %v", runtime.id, err)
	}

	logger.Infof("[Task %s] Completed: total=%d, new=%d, updated=%d, deleted=%d, skipped=%d",
		runtime.id, progress.totalFilesSnapshot(), progress.newPhotosSnapshot(), progress.updatedPhotosSnapshot(), progress.deletedPhotosSnapshot(), progress.skippedFilesSnapshot())

	if s.thumbnailService != nil {
		if task := s.thumbnailService.GetTaskStatus(); task == nil || (task.Status != "running" && task.Status != "stopping") {
			if _, err := s.thumbnailService.StartBackground(); err != nil {
				logger.Warnf("[Task %s] Auto start thumbnail background failed: %v", runtime.id, err)
			} else {
				logger.Infof("[Task %s] Thumbnail background started automatically after scan completion", runtime.id)
			}
		}
	}
	if s.geocodeTaskService != nil {
		if task := s.geocodeTaskService.GetTaskStatus(); task == nil || (task.Status != "running" && task.Status != "stopping") {
			if _, err := s.geocodeTaskService.StartBackground(); err != nil {
				logger.Warnf("[Task %s] Auto start geocode background failed: %v", runtime.id, err)
			} else {
				logger.Infof("[Task %s] Geocode background started automatically after scan completion", runtime.id)
			}
		}
	}

	s.finishScanTask(runtime, progress, "completed", "", true, nil)
}

func (s *photoService) processScanFile(ctx context.Context, jobID string, task scanFileTask, rebuild bool, existingByPath map[string]*model.Photo, seenFiles *struct {
	sync.Mutex
	items map[string]struct{}
}, progress *scanProgress) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	progress.setCurrentFile(filepath.Base(task.path))
	existing := existingByPath[task.path]

	// 跳过 excluded 照片，不更新不恢复
	if existing != nil && existing.Status == model.PhotoStatusExcluded {
		seenFiles.Lock()
		seenFiles.items[existing.FilePath] = struct{}{}
		seenFiles.Unlock()
		progress.incrementProcessed(1)
		return nil
	}

	if s.canReuseExistingPhoto(existing, task.info, rebuild) {
		seenFiles.Lock()
		seenFiles.items[existing.FilePath] = struct{}{}
		seenFiles.Unlock()
		progress.incrementProcessed(1)
		return nil
	}

	photo, err := s.processPhotoFunc(task.path, task.info)
	if err != nil {
		logger.Warnf("[Task %s] Process photo failed: %s, error: %v", jobID, task.path, err)
		progress.incrementProcessed(1)
		progress.incrementSkipped(1)
		return nil
	}

	seenFiles.Lock()
	seenFiles.items[photo.FilePath] = struct{}{}
	seenFiles.Unlock()

	existing = existingByPath[photo.FilePath]
	if existing == nil {
		if err := s.repo.Create(photo); err != nil {
			logger.Errorf("[Task %s] Create photo failed: %v", jobID, err)
			progress.incrementSkipped(1)
		} else {
			progress.incrementNew(1)
			s.enqueueThumbnailForPhoto(photo, thumbnailSourceScan, thumbnailPriorityScan)
			s.enqueueGeocodeForPhoto(photo, geocodeSourceScan, geocodePriorityScan)
		}
		progress.incrementProcessed(1)
		return nil
	}

	if rebuild {
		photo.ID = existing.ID
		s.preserveAnalysisFields(existing, photo)
		if err := s.repo.Update(photo); err != nil {
			logger.Errorf("[Task %s] Update photo failed: %v", jobID, err)
			progress.incrementSkipped(1)
		} else {
			progress.incrementUpdated(1)
			s.enqueueThumbnailForPhoto(photo, thumbnailSourceScan, thumbnailPriorityScan)
			s.enqueueGeocodeForPhoto(photo, geocodeSourceScan, geocodePriorityScan)
		}
		progress.incrementProcessed(1)
		return nil
	}

	if existing.FileHash != photo.FileHash {
		photo.ID = existing.ID
		s.preserveAnalysisFields(existing, photo)
		if err := s.repo.Update(photo); err != nil {
			logger.Errorf("[Task %s] Update photo failed: %v", jobID, err)
			progress.incrementSkipped(1)
		} else {
			progress.incrementUpdated(1)
			s.enqueueThumbnailForPhoto(photo, thumbnailSourceScan, thumbnailPriorityScan)
			s.enqueueGeocodeForPhoto(photo, geocodeSourceScan, geocodePriorityScan)
		}
	}

	progress.incrementProcessed(1)
	return nil
}

func (s *photoService) canReuseExistingPhoto(existing *model.Photo, info os.FileInfo, rebuild bool) bool {
	if rebuild || existing == nil || existing.FileModTime == nil {
		return false
	}
	if existing.FileSize != info.Size() {
		return false
	}
	return existing.FileModTime.Equal(info.ModTime())
}

func (s *photoService) preserveAnalysisFields(existing, photo *model.Photo) {
	if existing == nil || photo == nil {
		return
	}
	if existing.Description != "" {
		photo.Description = existing.Description
		photo.MainCategory = existing.MainCategory
		photo.Tags = existing.Tags
	}
	photo.AIAnalyzed = existing.AIAnalyzed
	photo.AnalyzedAt = existing.AnalyzedAt
	photo.AIProvider = existing.AIProvider
	photo.Caption = existing.Caption
	photo.MemoryScore = existing.MemoryScore
	photo.BeautyScore = existing.BeautyScore
	photo.OverallScore = existing.OverallScore
	photo.ScoreReason = existing.ScoreReason
}

func (s *photoService) enqueueGeocodeForPhoto(photo *model.Photo, source string, priority int) {
	if s.geocodeTaskService == nil || photo == nil || photo.ID == 0 {
		return
	}
	if err := s.geocodeTaskService.EnqueuePhoto(photo.ID, source, priority, false); err != nil {
		logger.Warnf("enqueue geocode failed for photo %d: %v", photo.ID, err)
	}
}

func (s *photoService) enqueueThumbnailForPhoto(photo *model.Photo, source string, priority int) {
	if s.thumbnailService == nil || photo == nil || photo.ID == 0 {
		return
	}
	if err := s.thumbnailService.EnqueuePhoto(photo.ID, source, priority, false); err != nil {
		logger.Warnf("enqueue thumbnail failed for photo %d: %v", photo.ID, err)
	}
}

func (s *photoService) clearActiveJob(jobID string) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.activeJob != nil && s.activeJob.id == jobID {
		s.activeJob = nil
	}
}

func (s *photoService) flushScanProgressLoop(jobID string, progress *scanProgress, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.flushScanProgress(jobID, progress, false)
		case <-stop:
			s.flushScanProgress(jobID, progress, true)
			return
		}
	}
}

func (s *photoService) flushScanProgress(jobID string, progress *scanProgress, force bool) {
	fields, ok := progress.snapshotFields(force)
	if !ok {
		return
	}
	now := time.Now()
	fields["last_heartbeat_at"] = &now
	if err := s.scanJobRepo.UpdateFields(jobID, fields); err != nil {
		logger.Warnf("[Task %s] Flush scan progress failed: %v", jobID, err)
	}
}

func (s *photoService) finishScanTask(runtime *activeScanJob, progress *scanProgress, status string, message string, clearError bool, completedAt *time.Time) {
	now := time.Now()
	if completedAt == nil {
		completedAt = &now
	}
	fields, _ := progress.snapshotFields(true)
	fields["status"] = status
	fields["phase"] = progress.phaseSnapshot()
	fields["completed_at"] = completedAt
	fields["last_heartbeat_at"] = completedAt
	fields["current_file"] = ""
	if clearError {
		fields["error_message"] = ""
	} else if message != "" {
		fields["error_message"] = message
	}
	if status == "stopped" {
		fields["stop_requested_at"] = completedAt
	}
	if err := s.scanJobRepo.UpdateFields(runtime.id, fields); err != nil {
		logger.Warnf("[Task %s] Finalize scan task failed: %v", runtime.id, err)
	}
}

func scanJobToTask(job *model.ScanJob) *model.ScanTask {
	if job == nil {
		return nil
	}
	return &model.ScanTask{
		ID:              job.ID,
		Status:          job.Status,
		Type:            job.Type,
		Path:            job.Path,
		Phase:           job.Phase,
		TotalFiles:      job.TotalFiles,
		DiscoveredFiles: job.DiscoveredFiles,
		ProcessedFiles:  job.ProcessedFiles,
		NewPhotos:       job.NewPhotos,
		UpdatedPhotos:   job.UpdatedPhotos,
		DeletedPhotos:   job.DeletedPhotos,
		SkippedFiles:    job.SkippedFiles,
		CurrentFile:     job.CurrentFile,
		ErrorMessage:    job.ErrorMessage,
		StartedAt:       job.StartedAt,
		StopRequestedAt: job.StopRequestedAt,
		CompletedAt:     job.CompletedAt,
	}
}

func (p *scanProgress) onDiscovered(fileName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.discoveredFiles++
	p.totalFiles = p.discoveredFiles
	p.currentFile = fileName
	p.dirty = true
}

func (p *scanProgress) incrementProcessed(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.processedFiles += n
	p.dirty = true
}

func (p *scanProgress) incrementNew(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.newPhotos += n
	p.dirty = true
}

func (p *scanProgress) incrementUpdated(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.updatedPhotos += n
	p.dirty = true
}

func (p *scanProgress) incrementDeleted(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deletedPhotos += n
	p.dirty = true
}

func (p *scanProgress) incrementSkipped(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.skippedFiles += n
	p.dirty = true
}

func (p *scanProgress) setCurrentFile(fileName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentFile = fileName
	p.dirty = true
}

func (p *scanProgress) setPhase(phase string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phase = phase
	p.dirty = true
}

func (p *scanProgress) snapshotFields(force bool) (map[string]interface{}, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !force && !p.dirty {
		return nil, false
	}
	fields := map[string]interface{}{
		"phase":            p.phase,
		"total_files":      p.totalFiles,
		"discovered_files": p.discoveredFiles,
		"processed_files":  p.processedFiles,
		"new_photos":       p.newPhotos,
		"updated_photos":   p.updatedPhotos,
		"deleted_photos":   p.deletedPhotos,
		"skipped_files":    p.skippedFiles,
		"current_file":     p.currentFile,
	}
	p.dirty = false
	return fields, true
}

func (p *scanProgress) phaseSnapshot() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.phase
}

func (p *scanProgress) totalFilesSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.totalFiles
}

func (p *scanProgress) newPhotosSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.newPhotos
}

func (p *scanProgress) updatedPhotosSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.updatedPhotos
}

func (p *scanProgress) deletedPhotosSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deletedPhotos
}

func (p *scanProgress) skippedFilesSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.skippedFiles
}

// countAndListFiles 统计并列出所有需要处理的文件
func (s *photoService) countAndListFiles(dir string) (int, []string) {
	var files []string

	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// 跳过目录
		if info.IsDir() {
			if s.shouldExcludeDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// 检查文件格式
		if s.isSupportedFormat(path) {
			files = append(files, path)
		}

		return nil
	})

	return len(files), files
}

// updateScanPathTimestamp 更新扫描路径的 last_scanned_at 时间戳
func (s *photoService) updateScanPathTimestamp(scanPath string) error {
	// 获取当前扫描路径配置
	configValue, err := s.configService.GetWithDefault("photos.scan_paths", "")
	if err != nil {
		return fmt.Errorf("get scan paths config: %w", err)
	}

	if configValue == "" {
		// 没有配置扫描路径，直接返回
		return nil
	}

	var pathsConfig scanPathsConfig

	if err := json.Unmarshal([]byte(configValue), &pathsConfig); err != nil {
		return fmt.Errorf("parse scan paths config: %w", err)
	}

	// 找到匹配的扫描路径并更新时间戳
	now := time.Now()
	updated := false
	for i := range pathsConfig.Paths {
		if pathsConfig.Paths[i].Path == scanPath {
			pathsConfig.Paths[i].LastScannedAt = &now
			updated = true
			break
		}
	}

	if !updated {
		// 没有找到匹配的路径，可能是通过直接路径扫描而非配置的路径
		return nil
	}

	// 保存更新后的配置
	newConfigValue, err := json.Marshal(pathsConfig)
	if err != nil {
		return fmt.Errorf("marshal scan paths config: %w", err)
	}

	if err := s.configService.Set("photos.scan_paths", string(newConfigValue)); err != nil {
		return fmt.Errorf("save scan paths config: %w", err)
	}

	logger.Infof("Updated last_scanned_at for scan path: %s", scanPath)
	return nil
}

func (s *photoService) updateScanTreeSnapshot(scanPath string) error {
	return s.updateScanTreeSnapshotWithSnapshot(scanPath, nil)
}

func (s *photoService) updateScanTreeSnapshotWithSnapshot(scanPath string, snapshot *scanTreeSnapshot) error {
	pathsCfg, err := s.loadScanPathsConfig()
	if err != nil {
		return fmt.Errorf("load scan paths config: %w", err)
	}
	for _, path := range pathsCfg.Paths {
		if path.Path != scanPath {
			continue
		}
		if snapshot == nil {
			snapshot, err = s.buildScanTreeSnapshot(scanPath)
			if err != nil {
				return err
			}
		}
		return s.saveScanTreeSnapshot(path.ID, snapshot)
	}
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
