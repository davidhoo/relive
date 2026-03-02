package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

// PhotoService 照片服务接口
type PhotoService interface {
	// 扫描
	ScanPhotos(path string) (*model.ScanPhotosResponse, error)
	ScanDirectory(dir string) ([]*model.Photo, error)
	RebuildPhotos(path string) (*model.RebuildPhotosResponse, error) // 重建照片（重新扫描文件、提取 EXIF、计算哈希、地理编码）
	CleanupNonExistentPhotos() (*model.CleanupPhotosResponse, error) // 清理数据库中所有不存在的照片

	// 查询
	GetPhotoByID(id uint) (*model.Photo, error)
	GetPhotos(req *model.GetPhotosRequest) ([]*model.Photo, int64, error)

	// 统计
	CountAll() (int64, error)
	CountAnalyzed() (int64, error)
	CountUnanalyzed() (int64, error)

	// 地理编码
	GeocodePhotoIfNeeded(photo *model.Photo) error
}

// photoService 照片服务实现
type photoService struct {
	repo           repository.PhotoRepository
	config         *config.Config
	geocodeService GeocodeService
}

// NewPhotoService 创建照片服务
func NewPhotoService(repo repository.PhotoRepository, cfg *config.Config, geocodeService GeocodeService) PhotoService {
	return &photoService{
		repo:           repo,
		config:         cfg,
		geocodeService: geocodeService,
	}
}

// ScanPhotos 扫描照片
func (s *photoService) ScanPhotos(path string) (*model.ScanPhotosResponse, error) {
	logger.Infof("Starting photo scan: %s", path)

	// 检查路径是否存在
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("path does not exist: %s", path)
	}

	// 扫描目录
	photos, err := s.ScanDirectory(path)
	if err != nil {
		return nil, fmt.Errorf("scan directory: %w", err)
	}

	// 统计结果
	scannedCount := len(photos)
	newCount := 0
	updatedCount := 0

	// 处理每张照片
	for _, photo := range photos {
		// 检查是否已存在（根据文件路径）
		exists, err := s.repo.ExistsByFilePath(photo.FilePath)
		if err != nil {
			logger.Errorf("Check photo exists failed: %v", err)
			continue
		}

		if !exists {
			// 新照片，创建
			if err := s.repo.Create(photo); err != nil {
				logger.Errorf("Create photo failed: %v", err)
				continue
			}
			newCount++
		} else {
			// 已存在，检查是否需要更新
			existing, err := s.repo.GetByFilePath(photo.FilePath)
			if err != nil {
				logger.Errorf("Get existing photo failed: %v", err)
				continue
			}

			// 比较文件哈希，如果不同则更新
			if existing.FileHash != photo.FileHash {
				photo.ID = existing.ID
				if err := s.repo.Update(photo); err != nil {
					logger.Errorf("Update photo failed: %v", err)
					continue
				}
				updatedCount++
			}
		}
	}

	logger.Infof("Photo scan completed: scanned=%d, new=%d, updated=%d", scannedCount, newCount, updatedCount)

	return &model.ScanPhotosResponse{
		ScannedCount: scannedCount,
		NewCount:     newCount,
		UpdatedCount: updatedCount,
	}, nil
}

// RebuildPhotos 重建照片，包括：重新扫描文件、提取 EXIF、计算哈希、地理编码
// 会删除数据库中已不存在的文件记录，保留 AI 分析结果
func (s *photoService) RebuildPhotos(path string) (*model.RebuildPhotosResponse, error) {
	logger.Infof("Starting photo rebuild: %s", path)

	// 检查路径是否存在
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("path does not exist: %s", path)
	}

	// 1. 获取数据库中该路径下的所有现有照片
	existingPhotos, err := s.repo.ListByPathPrefix(path)
	if err != nil {
		return nil, fmt.Errorf("list existing photos: %w", err)
	}

	// 构建现有照片的文件路径映射（用于后续对比）
	existingPathMap := make(map[string]*model.Photo)
	for _, photo := range existingPhotos {
		existingPathMap[photo.FilePath] = photo
	}

	// 2. 扫描目录获取文件系统上的所有照片
	scannedPhotos, err := s.ScanDirectory(path)
	if err != nil {
		return nil, fmt.Errorf("scan directory: %w", err)
	}

	// 统计结果
	scannedCount := len(scannedPhotos)
	newCount := 0
	updatedCount := 0
	deletedCount := 0

	// 3. 处理每张照片 - 强制重建
	scannedPathMap := make(map[string]bool)
	for _, photo := range scannedPhotos {
		scannedPathMap[photo.FilePath] = true

		// 检查是否已存在（根据文件路径）
		exists, err := s.repo.ExistsByFilePath(photo.FilePath)
		if err != nil {
			logger.Errorf("Check photo exists failed: %v", err)
			continue
		}

		if !exists {
			// 新照片，创建
			if err := s.repo.Create(photo); err != nil {
				logger.Errorf("Create photo failed: %v", err)
				continue
			}
			newCount++
		} else {
			// 已存在，强制重建（重新提取所有信息，但保留 AI 分析结果）
			existing, err := s.repo.GetByFilePath(photo.FilePath)
			if err != nil {
				logger.Errorf("Get existing photo failed: %v", err)
				continue
			}

			// 保留原有 ID 和 AI 分析相关字段
			photo.ID = existing.ID
			photo.AIAnalyzed = existing.AIAnalyzed
			photo.AnalyzedAt = existing.AnalyzedAt
			photo.AIProvider = existing.AIProvider
			photo.Description = existing.Description
			photo.Caption = existing.Caption
			photo.MemoryScore = existing.MemoryScore
			photo.BeautyScore = existing.BeautyScore
			photo.OverallScore = existing.OverallScore
			photo.MainCategory = existing.MainCategory
			photo.Tags = existing.Tags

			if err := s.repo.Update(photo); err != nil {
				logger.Errorf("Update photo failed: %v", err)
				continue
			}
			updatedCount++
		}
	}

	// 4. 找出已删除的文件（在数据库中但文件已不存在于文件系统）并软删除
	// 不仅检查扫描结果，还要实际检查文件是否存在（处理移动或删除的情况）
	for filePath, photo := range existingPathMap {
		// 如果不在扫描结果中，或者文件实际不存在
		if !scannedPathMap[filePath] {
			// 双重确认：检查文件是否真的不存在
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				// 文件已不存在，软删除数据库记录
				if err := s.repo.Delete(photo.ID); err != nil {
					logger.Errorf("Soft delete photo failed: id=%d, path=%s, error=%v", photo.ID, filePath, err)
					continue
				}
				deletedCount++
				logger.Infof("Soft deleted photo (file not exists): id=%d, path=%s", photo.ID, filePath)
			} else {
				// 文件存在但不在扫描结果中（可能是格式不支持或其他原因）
				logger.Debugf("Photo file exists but not in scan result: id=%d, path=%s", photo.ID, filePath)
			}
		}
	}

	logger.Infof("Photo rebuild completed: scanned=%d, new=%d, updated=%d, deleted=%d", scannedCount, newCount, updatedCount, deletedCount)

	return &model.RebuildPhotosResponse{
		ScannedCount: scannedCount,
		NewCount:     newCount,
		UpdatedCount: updatedCount,
		DeletedCount: deletedCount,
	}, nil
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
			// 设置位置信息 - 使用完整格式（国家 省 市 区）
			photo.Location = location.FormatFull()
			logger.Debugf("Geocoded: (%f, %f) -> %s", *photo.GPSLatitude, *photo.GPSLongitude, photo.Location)
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

	// 调用 Repository
	return s.repo.List(req.Page, req.PageSize, req.Analyzed, req.Location, req.SortBy, req.SortDesc)
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

	// 设置位置信息（立即返回给前端）- 使用完整格式
	photo.Location = location.FormatFull()
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
