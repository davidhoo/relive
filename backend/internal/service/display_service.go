package service

import (
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

// DisplayService 展示服务接口
type DisplayService interface {
	// 获取展示照片
	GetDisplayPhoto(deviceID string) (*model.Photo, error)

	// 记录展示
	RecordDisplay(record *model.DisplayRecord) error

	// 往年今日算法
	GetOnThisDayPhoto(deviceID string) (*model.Photo, error)
}

// displayService 展示服务实现
type displayService struct {
	photoRepo         repository.PhotoRepository
	displayRecordRepo repository.DisplayRecordRepository
	esp32DeviceRepo   repository.ESP32DeviceRepository
	config            *config.Config
}

// NewDisplayService 创建展示服务
func NewDisplayService(
	photoRepo repository.PhotoRepository,
	displayRecordRepo repository.DisplayRecordRepository,
	esp32DeviceRepo repository.ESP32DeviceRepository,
	cfg *config.Config,
) DisplayService {
	return &displayService{
		photoRepo:         photoRepo,
		displayRecordRepo: displayRecordRepo,
		esp32DeviceRepo:   esp32DeviceRepo,
		config:            cfg,
	}
}

// GetDisplayPhoto 获取展示照片
func (s *displayService) GetDisplayPhoto(deviceIDStr string) (*model.Photo, error) {
	// 获取设备信息
	device, err := s.esp32DeviceRepo.GetByDeviceID(deviceIDStr)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	// 使用往年今日算法
	photo, err := s.GetOnThisDayPhoto(deviceIDStr)
	if err != nil {
		return nil, err
	}

	logger.Infof("Selected display photo for device %s: photo_id=%d", device.DeviceID, photo.ID)
	return photo, nil
}

// GetOnThisDayPhoto 往年今日算法
func (s *displayService) GetOnThisDayPhoto(deviceIDStr string) (*model.Photo, error) {
	// 获取设备
	device, err := s.esp32DeviceRepo.GetByDeviceID(deviceIDStr)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	// 获取最近已展示的照片 ID（避免重复）
	excludePhotoIDs, err := s.displayRecordRepo.GetDisplayedPhotoIDs(device.ID, s.config.Display.AvoidRepeatDays)
	if err != nil {
		logger.Warnf("Get displayed photo IDs failed: %v", err)
		excludePhotoIDs = []uint{}
	}

	// 当前日期
	now := time.Now()

	// 尝试多种降级策略
	fallbackDays := s.config.Display.FallbackDays // [3, 7, 30, 365]

	for _, days := range fallbackDays {
		logger.Debugf("Trying fallback: ±%d days", days)

		// 逐年查找（从最近的年份开始）
		for year := 1; year <= 100; year++ {
			start := now.AddDate(-year, 0, -days)
			end := now.AddDate(-year, 0, days)

			// 查询该日期范围的照片
			photos, err := s.photoRepo.GetByDateRange(start, end)
			if err != nil {
				logger.Warnf("Get photos by date range failed: %v", err)
				continue
			}

			// 过滤已分析且未被最近展示的照片
			var candidates []*model.Photo
			for _, photo := range photos {
				if photo.AIAnalyzed && !contains(excludePhotoIDs, photo.ID) {
					candidates = append(candidates, photo)
				}
			}

			if len(candidates) > 0 {
				// 找到候选照片，选择评分最高的
				bestPhoto := s.selectBestPhoto(candidates)
				logger.Infof("Found photo with fallback ±%d days, year=%d, photo_id=%d", days, year, bestPhoto.ID)
				return bestPhoto, nil
			}
		}
	}

	// 所有降级策略都失败，返回评分最高的照片
	logger.Warn("All fallback strategies failed, selecting top scored photo")
	topPhotos, err := s.photoRepo.GetTopByScore(1, excludePhotoIDs)
	if err != nil {
		return nil, fmt.Errorf("get top scored photo: %w", err)
	}

	if len(topPhotos) == 0 {
		return nil, fmt.Errorf("no photos available")
	}

	return topPhotos[0], nil
}

// selectBestPhoto 选择最佳照片（评分最高）
func (s *displayService) selectBestPhoto(photos []*model.Photo) *model.Photo {
	if len(photos) == 0 {
		return nil
	}

	best := photos[0]
	for _, photo := range photos {
		if photo.OverallScore > best.OverallScore {
			best = photo
		}
	}

	return best
}

// RecordDisplay 记录展示
func (s *displayService) RecordDisplay(record *model.DisplayRecord) error {
	return s.displayRecordRepo.Create(record)
}

// contains 检查切片中是否包含元素
func contains(slice []uint, item uint) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
