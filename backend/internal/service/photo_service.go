package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	// 查询
	GetPhotoByID(id uint) (*model.Photo, error)
	GetPhotos(req *model.GetPhotosRequest) ([]*model.Photo, int64, error)

	// 统计
	CountAll() (int64, error)
	CountAnalyzed() (int64, error)
	CountUnanalyzed() (int64, error)
}

// photoService 照片服务实现
type photoService struct {
	repo   repository.PhotoRepository
	config *config.Config
}

// NewPhotoService 创建照片服务
func NewPhotoService(repo repository.PhotoRepository, cfg *config.Config) PhotoService {
	return &photoService{
		repo:   repo,
		config: cfg,
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

	// 构建 Photo 对象
	photo := &model.Photo{
		FilePath:     filePath,
		FileName:     filepath.Base(filePath),
		FileSize:     info.Size(),
		FileHash:     fileHash,
		TakenAt:      exifData.TakenAt,
		CameraModel:  exifData.CameraModel,
		Width:        width,
		Height:       height,
		Orientation:  exifData.Orientation,
		GPSLatitude:  exifData.GPSLatitude,
		GPSLongitude: exifData.GPSLongitude,
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
