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
	GetScanTask() *model.ScanTask
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
	GetTags() ([]string, error)

	// 地理编码
	GeocodePhotoIfNeeded(photo *model.Photo) error
	RegeocodeAllPhotos() (int, error) // 重新解析所有有GPS照片的位置

	// 删除路径相关
	DeletePhotosByPathPrefix(pathPrefix string) (int64, error)
	GetPhotoIDsByPathPrefix(pathPrefix string) ([]uint, error)
	GetPhotosByPathPrefix(pathPrefix string) ([]*model.Photo, error)

	// 路径统计
	CountPhotosByPathPrefix(pathPrefix string) (int64, error)
}

// photoService 照片服务实现
type photoService struct {
	repo               repository.PhotoRepository
	config             *config.Config
	configService      ConfigService
	geocodeService     GeocodeService
	thumbnailGenerator *util.ThumbnailGenerator
	scanTask           *model.ScanTask
	taskMutex          sync.RWMutex
	autoScanMutex      sync.Mutex
	lastAutoScanCheck  time.Time
}

// NewPhotoService 创建照片服务
func NewPhotoService(repo repository.PhotoRepository, cfg *config.Config, configService ConfigService, geocodeService GeocodeService) PhotoService {
	// 初始化缩略图生成器（1024px，兼顾展示和 AI 理解）
	thumbnailGenerator := util.NewThumbnailGenerator(1024, 1024, 90, cfg.Photos.ThumbnailPath)

	return &photoService{
		repo:               repo,
		config:             cfg,
		configService:      configService,
		geocodeService:     geocodeService,
		thumbnailGenerator: thumbnailGenerator,
	}
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

func (s *photoService) scanTreeChanged(snapshot *scanTreeSnapshot) (bool, error) {
	if snapshot == nil {
		return false, nil
	}
	for _, node := range snapshot.Nodes {
		info, err := os.Stat(node.Path)
		if os.IsNotExist(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() {
			continue
		}
		if info.ModTime().UnixNano() != node.ModTime {
			return true, nil
		}
	}
	return false, nil
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

		changed, err := s.scanTreeChanged(snapshot)
		if err != nil {
			logger.Warnf("Check scan tree changes failed for %s: %v", path.Path, err)
			continue
		}
		if changed {
			if _, err := s.StartScan(path.Path); err != nil {
				logger.Warnf("Auto scan start failed for %s: %v", path.Path, err)
			}
			return nil
		}
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

	// 如果有 GPS 坐标，尝试进行地理编码
	if photo.GPSLatitude != nil && photo.GPSLongitude != nil && s.geocodeService != nil {
		location, err := s.geocodeService.ReverseGeocode(*photo.GPSLatitude, *photo.GPSLongitude)
		if err != nil {
			logger.Warnf("Geocode failed for (%f, %f): %v", *photo.GPSLatitude, *photo.GPSLongitude, err)
		} else {
			// 设置位置信息 - 使用标准显示格式（城市+区县+商圈/街道）
			photo.Location = location.FormatDisplay()
			logger.Debugf("Geocoded: (%f, %f) -> %s", *photo.GPSLatitude, *photo.GPSLongitude, photo.Location)
		}
	}

	// 生成缩略图（如果尚未存在）
	if s.thumbnailGenerator != nil {
		thumbnailPath, err := s.thumbnailGenerator.GenerateThumbnailIfNotExists(filePath)
		if err != nil {
			logger.Warnf("Generate thumbnail failed: %s, error: %v", filePath, err)
		} else {
			photo.ThumbnailPath = thumbnailPath
			logger.Debugf("Thumbnail generated: %s -> %s", filePath, thumbnailPath)
		}
	}

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
	return s.repo.GetByID(id)
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
	return s.repo.List(req.Page, req.PageSize, req.Analyzed, req.Location, req.Search, req.SortBy, req.SortDesc, enabledPaths)
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
	logger.Debugf("Real-time geocoded photo %d: (%f, %f) -> %s",
		photo.ID, *photo.GPSLatitude, *photo.GPSLongitude, photo.Location)

	// 异步回写到数据库
	go func() {
		if err := s.repo.UpdateLocation(photo.ID, photo.Location); err != nil {
			logger.Errorf("Failed to update location for photo %d: %v", photo.ID, err)
		} else {
			logger.Debugf("Location saved to database for photo %d: %s", photo.ID, photo.Location)
		}
	}()

	return nil
}

// GetCategories 获取所有分类
func (s *photoService) GetCategories() ([]string, error) {
	return s.repo.GetCategories()
}

// GetTags 获取所有标签
func (s *photoService) GetTags() ([]string, error) {
	return s.repo.GetTags()
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

// StartScan 启动异步扫描任务
func (s *photoService) StartScan(path string) (*model.ScanTask, error) {
	// 检查是否已有运行中的任务
	s.taskMutex.Lock()
	if s.scanTask != nil && s.scanTask.IsRunning() {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("scan task already running")
	}

	// 检查路径是否存在
	if _, err := os.Stat(path); os.IsNotExist(err) {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("path does not exist: %s", path)
	}

	// 创建新任务
	task := &model.ScanTask{
		ID:        fmt.Sprintf("scan_%d", time.Now().Unix()),
		Status:    "running",
		Type:      "scan",
		Path:      path,
		StartedAt: time.Now(),
	}
	s.scanTask = task
	s.taskMutex.Unlock()

	logger.Infof("Starting async scan: path=%s, task_id=%s", path, task.ID)

	// 异步执行扫描
	go s.runScanTask(task, path, false)

	return task, nil
}

// StartRebuild 启动异步重建任务
func (s *photoService) StartRebuild(path string) (*model.ScanTask, error) {
	// 检查是否已有运行中的任务
	s.taskMutex.Lock()
	if s.scanTask != nil && s.scanTask.IsRunning() {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("scan task already running")
	}

	// 检查路径是否存在
	if _, err := os.Stat(path); os.IsNotExist(err) {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("path does not exist: %s", path)
	}

	// 创建新任务
	task := &model.ScanTask{
		ID:        fmt.Sprintf("rebuild_%d", time.Now().Unix()),
		Status:    "running",
		Type:      "rebuild",
		Path:      path,
		StartedAt: time.Now(),
	}
	s.scanTask = task
	s.taskMutex.Unlock()

	logger.Infof("Starting async rebuild: path=%s, task_id=%s", path, task.ID)

	// 异步执行重建
	go s.runScanTask(task, path, true)

	return task, nil
}

// GetScanTask 获取当前扫描任务
func (s *photoService) GetScanTask() *model.ScanTask {
	s.taskMutex.RLock()
	defer s.taskMutex.RUnlock()
	return s.scanTask
}

// runScanTask 后台执行扫描任务
func (s *photoService) runScanTask(task *model.ScanTask, path string, rebuild bool) {
	// 第一步：遍历目录统计文件总数
	totalFiles, fileList := s.countAndListFiles(path)

	s.taskMutex.Lock()
	task.TotalFiles = totalFiles
	s.taskMutex.Unlock()

	logger.Infof("[Task %s] Found %d files to process", task.ID, totalFiles)

	// 第二步：逐个处理文件
	newCount, updatedCount := 0, 0
	processedCount := 0
	currentFiles := make(map[string]struct{}, len(fileList))

	for _, filePath := range fileList {
		currentFiles[filePath] = struct{}{}
		// 更新当前文件
		s.taskMutex.Lock()
		task.CurrentFile = filepath.Base(filePath)
		task.ProcessedFiles = processedCount
		s.taskMutex.Unlock()

		// 获取文件信息
		info, err := os.Stat(filePath)
		if err != nil {
			logger.Warnf("[Task %s] Stat file failed: %s, error: %v", task.ID, filePath, err)
			processedCount++
			continue
		}

		// 处理照片
		photo, err := s.processPhoto(filePath, info)
		if err != nil {
			logger.Warnf("[Task %s] Process photo failed: %s, error: %v", task.ID, filePath, err)
			processedCount++
			continue
		}

		// 检查是否已存在
		exists, _ := s.repo.ExistsByFilePath(photo.FilePath)

		if !exists {
			// 新照片
			if err := s.repo.Create(photo); err != nil {
				logger.Errorf("[Task %s] Create photo failed: %v", task.ID, err)
			} else {
				newCount++
			}
		} else if rebuild {
			// 重建模式：更新现有照片
			existing, _ := s.repo.GetByFilePath(photo.FilePath)
			if existing != nil {
				photo.ID = existing.ID
				// 保留 AI 分析结果
				if existing.Description != "" {
					photo.Description = existing.Description
					photo.MainCategory = existing.MainCategory
					photo.Tags = existing.Tags
				}
				if err := s.repo.Update(photo); err != nil {
					logger.Errorf("[Task %s] Update photo failed: %v", task.ID, err)
				} else {
					updatedCount++
				}
			}
		} else {
			// 扫描模式：检查文件哈希是否变化
			existing, _ := s.repo.GetByFilePath(photo.FilePath)
			if existing != nil && existing.FileHash != photo.FileHash {
				photo.ID = existing.ID
				// 保留 AI 分析结果
				if existing.Description != "" {
					photo.Description = existing.Description
					photo.MainCategory = existing.MainCategory
					photo.Tags = existing.Tags
				}
				if err := s.repo.Update(photo); err != nil {
					logger.Errorf("[Task %s] Update photo failed: %v", task.ID, err)
				} else {
					updatedCount++
				}
			}
		}

		processedCount++

		// 更新进度
		s.taskMutex.Lock()
		task.NewPhotos = newCount
		task.UpdatedPhotos = updatedCount
		task.ProcessedFiles = processedCount
		s.taskMutex.Unlock()
	}

	// 更新任务完成状态
	s.taskMutex.Lock()
	task.Status = "completed"
	task.NewPhotos = newCount
	task.UpdatedPhotos = updatedCount
	task.ProcessedFiles = processedCount
	task.CurrentFile = ""
	now := time.Now()
	task.CompletedAt = &now
	s.taskMutex.Unlock()

	deletedCount := 0
	existingPhotos, err := s.repo.ListByPathPrefix(path)
	if err != nil {
		logger.Warnf("[Task %s] List existing photos for deletion check failed: %v", task.ID, err)
	} else {
		for _, existing := range existingPhotos {
			if _, ok := currentFiles[existing.FilePath]; !ok {
				if err := s.repo.Delete(existing.ID); err != nil {
					logger.Warnf("[Task %s] Delete missing photo failed: %v", task.ID, err)
				} else {
					deletedCount++
				}
			}
		}
	}

	logger.Infof("[Task %s] Completed: total=%d, new=%d, updated=%d, deleted=%d",
		task.ID, totalFiles, newCount, updatedCount, deletedCount)

	if err := s.updateScanPathTimestamp(path); err != nil {
		logger.Warnf("[Task %s] Failed to update scan path timestamp: %v", task.ID, err)
	}
	if err := s.updateScanTreeSnapshot(path); err != nil {
		logger.Warnf("[Task %s] Failed to update scan tree snapshot: %v", task.ID, err)
	}
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
	pathsCfg, err := s.loadScanPathsConfig()
	if err != nil {
		return fmt.Errorf("load scan paths config: %w", err)
	}
	for _, path := range pathsCfg.Paths {
		if path.Path != scanPath {
			continue
		}
		snapshot, err := s.buildScanTreeSnapshot(scanPath)
		if err != nil {
			return err
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

		// 重新解析位置
		location, err := s.geocodeService.ReverseGeocode(*photo.GPSLatitude, *photo.GPSLongitude)
		if err != nil {
			logger.Warnf("Re-geocode failed for photo %d: %v", photo.ID, err)
			failed++
			continue
		}

		newLocation := location.FormatDisplay()
		if newLocation == photo.Location {
			// 位置没变，跳过
			continue
		}

		// 更新数据库
		if err := s.repo.UpdateLocation(photo.ID, newLocation); err != nil {
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
