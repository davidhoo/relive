package service

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
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

	// 预览展示策略结果
	PreviewPhotos(cfg *model.DisplayStrategyConfig) ([]*model.Photo, error)

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
	configService     ConfigService
	config            *config.Config
}

// NewDisplayService 创建展示服务
func NewDisplayService(
	photoRepo repository.PhotoRepository,
	displayRecordRepo repository.DisplayRecordRepository,
	esp32DeviceRepo repository.ESP32DeviceRepository,
	configService ConfigService,
	cfg *config.Config,
) DisplayService {
	return &displayService{
		photoRepo:         photoRepo,
		displayRecordRepo: displayRecordRepo,
		esp32DeviceRepo:   esp32DeviceRepo,
		configService:     configService,
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

	strategyConfig := s.getDisplayStrategyConfig()

	var photo *model.Photo
	switch strategyConfig.Algorithm {
	case "random":
		photo, err = s.getRandomPhoto(deviceIDStr, strategyConfig)
	case "smart":
		photo, err = s.getSmartPhoto(deviceIDStr, strategyConfig)
	case "on_this_day":
		photo, err = s.GetOnThisDayPhoto(deviceIDStr)
	default:
		logger.Warnf("Display algorithm %s is not implemented, falling back to on_this_day", strategyConfig.Algorithm)
		photo, err = s.GetOnThisDayPhoto(deviceIDStr)
	}
	if err != nil {
		return nil, err
	}

	logger.Infof("Selected display photo for device %s: photo_id=%d", device.DeviceID, photo.ID)
	return photo, nil
}

// PreviewPhotos 预览展示策略结果
func (s *displayService) PreviewPhotos(cfg *model.DisplayStrategyConfig) ([]*model.Photo, error) {
	if cfg == nil {
		defaultCfg := defaultDisplayStrategyConfig()
		cfg = &defaultCfg
	}

	normalizeDisplayStrategyConfig(cfg)

	switch cfg.Algorithm {
	case "random":
		return s.photoRepo.GetRandom(cfg.DailyCount, cfg.MinBeautyScore, cfg.MinMemoryScore, nil)
	case "smart":
		return s.getSmartPhotos(*cfg, nil, cfg.DailyCount)
	default:
		return nil, fmt.Errorf("preview for algorithm %s is not implemented", cfg.Algorithm)
	}
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

func (s *displayService) getRandomPhoto(deviceIDStr string, cfg model.DisplayStrategyConfig) (*model.Photo, error) {
	device, err := s.esp32DeviceRepo.GetByDeviceID(deviceIDStr)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	excludePhotoIDs, err := s.displayRecordRepo.GetDisplayedPhotoIDs(device.ID, s.config.Display.AvoidRepeatDays)
	if err != nil {
		logger.Warnf("Get displayed photo IDs failed: %v", err)
		excludePhotoIDs = []uint{}
	}

	photos, err := s.photoRepo.GetRandom(1, cfg.MinBeautyScore, cfg.MinMemoryScore, excludePhotoIDs)
	if err != nil {
		return nil, fmt.Errorf("get random photo: %w", err)
	}
	if len(photos) > 0 {
		return photos[0], nil
	}

	logger.Warn("No photos matched random strategy thresholds, falling back to unrestricted random photo")
	photos, err = s.photoRepo.GetRandom(1, 0, 0, excludePhotoIDs)
	if err != nil {
		return nil, fmt.Errorf("get fallback random photo: %w", err)
	}
	if len(photos) == 0 {
		return nil, fmt.Errorf("no photos available")
	}

	return photos[0], nil
}

func (s *displayService) getSmartPhoto(deviceIDStr string, cfg model.DisplayStrategyConfig) (*model.Photo, error) {
	device, err := s.esp32DeviceRepo.GetByDeviceID(deviceIDStr)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	excludePhotoIDs, err := s.displayRecordRepo.GetDisplayedPhotoIDs(device.ID, s.config.Display.AvoidRepeatDays)
	if err != nil {
		logger.Warnf("Get displayed photo IDs failed: %v", err)
		excludePhotoIDs = []uint{}
	}

	photos, err := s.getSmartPhotos(cfg, excludePhotoIDs, 1)
	if err != nil {
		return nil, fmt.Errorf("get smart photo: %w", err)
	}
	if len(photos) == 0 {
		return nil, fmt.Errorf("no photos available")
	}

	return photos[0], nil
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

func (s *displayService) getSmartPhotos(cfg model.DisplayStrategyConfig, excludePhotoIDs []uint, limit int) ([]*model.Photo, error) {
	if limit <= 0 {
		limit = 1
	}

	photos, err := s.photoRepo.ListAll()
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}

	candidatesByMonthDay := make(map[string][]*model.Photo)
	var fallbackCandidates []*model.Photo
	excludeSet := make(map[uint]struct{}, len(excludePhotoIDs))
	for _, id := range excludePhotoIDs {
		excludeSet[id] = struct{}{}
	}

	for _, photo := range photos {
		if photo == nil || !photo.AIAnalyzed {
			continue
		}
		if _, excluded := excludeSet[photo.ID]; excluded {
			continue
		}

		fallbackCandidates = append(fallbackCandidates, photo)

		if photo.TakenAt == nil {
			continue
		}
		if photo.MemoryScore < cfg.MinMemoryScore || photo.BeautyScore < cfg.MinBeautyScore {
			continue
		}

		monthDay := photo.TakenAt.Format("01-02")
		candidatesByMonthDay[monthDay] = append(candidatesByMonthDay[monthDay], photo)
	}

	now := time.Now()
	for offset := 0; offset <= 365; offset++ {
		monthDay := now.AddDate(0, 0, -offset).Format("01-02")
		candidates := candidatesByMonthDay[monthDay]
		if len(candidates) > 0 {
			return pickRandomPhotos(candidates, limit), nil
		}
	}

	fallback := selectTopMemoryPhoto(fallbackCandidates)
	if fallback != nil {
		return []*model.Photo{fallback}, nil
	}

	if len(excludePhotoIDs) > 0 {
		var unrestricted []*model.Photo
		for _, photo := range photos {
			if photo != nil && photo.AIAnalyzed {
				unrestricted = append(unrestricted, photo)
			}
		}
		fallback = selectTopMemoryPhoto(unrestricted)
		if fallback != nil {
			return []*model.Photo{fallback}, nil
		}
	}

	return nil, nil
}

func (s *displayService) getDisplayStrategyConfig() model.DisplayStrategyConfig {
	cfg := defaultDisplayStrategyConfig()
	if s.configService == nil {
		return cfg
	}

	value, err := s.configService.GetWithDefault("display.strategy", "")
	if err != nil {
		logger.Warnf("Load display strategy config failed: %v", err)
		return cfg
	}
	if value == "" {
		return cfg
	}

	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		logger.Warnf("Parse display strategy config failed: %v", err)
		return defaultDisplayStrategyConfig()
	}

	normalizeDisplayStrategyConfig(&cfg)
	return cfg
}

func defaultDisplayStrategyConfig() model.DisplayStrategyConfig {
	return model.DisplayStrategyConfig{
		Algorithm:      "smart",
		MinBeautyScore: 70,
		MinMemoryScore: 60,
		DailyCount:     3,
	}
}

func normalizeDisplayStrategyConfig(cfg *model.DisplayStrategyConfig) {
	if cfg.Algorithm == "" {
		cfg.Algorithm = "smart"
	}
	if cfg.MinBeautyScore < 0 {
		cfg.MinBeautyScore = 0
	}
	if cfg.MinBeautyScore > 100 {
		cfg.MinBeautyScore = 100
	}
	if cfg.MinMemoryScore < 0 {
		cfg.MinMemoryScore = 0
	}
	if cfg.MinMemoryScore > 100 {
		cfg.MinMemoryScore = 100
	}
	if cfg.DailyCount <= 0 {
		cfg.DailyCount = 3
	}
	if cfg.DailyCount > 20 {
		cfg.DailyCount = 20
	}
}

func pickRandomPhotos(photos []*model.Photo, limit int) []*model.Photo {
	if len(photos) == 0 || limit <= 0 {
		return nil
	}

	result := append([]*model.Photo(nil), photos...)
	rand.Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})

	if limit >= len(result) {
		return result
	}

	return result[:limit]
}

func selectTopMemoryPhoto(photos []*model.Photo) *model.Photo {
	if len(photos) == 0 {
		return nil
	}

	best := photos[0]
	for _, photo := range photos[1:] {
		if photo.MemoryScore > best.MemoryScore {
			best = photo
			continue
		}
		if photo.MemoryScore == best.MemoryScore && photo.OverallScore > best.OverallScore {
			best = photo
			continue
		}
		if photo.MemoryScore == best.MemoryScore && photo.OverallScore == best.OverallScore &&
			photo.TakenAt != nil && (best.TakenAt == nil || photo.TakenAt.After(*best.TakenAt)) {
			best = photo
		}
	}

	return best
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
