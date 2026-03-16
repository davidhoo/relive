package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// DisplayService 展示服务接口
type DisplayService interface {
	// 获取展示照片
	GetDisplayPhoto(deviceID string) (*model.Photo, error)

	// 预览展示策略结果
	PreviewPhotos(cfg *model.DisplayStrategyConfig, previewDate *time.Time, sessionExcludeIDs []uint) ([]*model.Photo, error)

	// 记录展示
	RecordDisplay(record *model.DisplayRecord) error

	// 往年今日算法
	GetOnThisDayPhoto(deviceID string) (*model.Photo, error)

	// 每日展示批次
	GenerateDailyBatch(date time.Time, force bool) (*model.DailyDisplayBatch, error)
	StartGenerateDailyBatch(date time.Time, force bool) (*model.DailyDisplayBatch, error)
	GetDailyBatch(date time.Time) (*model.DailyDisplayBatch, error)
	ListDailyBatches(limit int) ([]*model.DailyDisplayBatch, error)
	GetDeviceDisplay(deviceID uint, renderProfile string) (*model.DeviceDisplaySelection, error)
	GetDailyDisplayItem(id uint) (*model.DailyDisplayItem, error)
	GetDailyDisplayAsset(id uint) (*model.DailyDisplayAsset, error)
	GetRenderProfiles() []model.RenderProfileResponse
}

// displayService 展示服务实现
type displayService struct {
	db                *gorm.DB
	photoRepo         repository.PhotoRepository
	displayRecordRepo repository.DisplayRecordRepository
	deviceRepo        repository.DeviceRepository
	eventRepo         repository.EventRepository
	configService     ConfigService
	config            *config.Config

	batchGenMu      sync.Mutex
	batchGenRunning bool
}

// NewDisplayService 创建展示服务
func NewDisplayService(
	db *gorm.DB,
	photoRepo repository.PhotoRepository,
	displayRecordRepo repository.DisplayRecordRepository,
	deviceRepo repository.DeviceRepository,
	eventRepo repository.EventRepository,
	configService ConfigService,
	cfg *config.Config,
) DisplayService {
	return &displayService{
		db:                db,
		photoRepo:         photoRepo,
		displayRecordRepo: displayRecordRepo,
		deviceRepo:        deviceRepo,
		eventRepo:         eventRepo,
		configService:     configService,
		config:            cfg,
	}
}

// GetDisplayPhoto 获取展示照片
func (s *displayService) GetDisplayPhoto(deviceIDStr string) (*model.Photo, error) {
	// 获取设备信息
	device, err := s.deviceRepo.GetByDeviceID(deviceIDStr)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	strategyConfig := s.getDisplayStrategyConfig()

	var photo *model.Photo
	switch strategyConfig.Algorithm {
	case "random":
		photo, err = s.getRandomPhoto(deviceIDStr, strategyConfig)
	case "on_this_day":
		photo, err = s.GetOnThisDayPhoto(deviceIDStr)
	case "event_curated":
		photo, err = s.getEventCuratedPhoto(deviceIDStr, strategyConfig)
	case "smart":
		logger.Infof("Display algorithm smart is merged into on_this_day, using unified on_this_day flow")
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
func (s *displayService) PreviewPhotos(cfg *model.DisplayStrategyConfig, previewDate *time.Time, sessionExcludeIDs []uint) ([]*model.Photo, error) {
	// 合并 DisplayRecord 历史排除 + 前端会话级排除
	var excludeIDs []uint
	if s.config.Display.AvoidRepeatDays > 0 {
		historicIDs, err := s.displayRecordRepo.GetDisplayedPhotoIDsAll(s.config.Display.AvoidRepeatDays)
		if err != nil {
			logger.Warnf("Get displayed photo IDs for preview failed: %v", err)
		} else {
			excludeIDs = historicIDs
		}
	}
	excludeIDs = append(excludeIDs, sessionExcludeIDs...)
	return s.previewPhotosWithExcludes(cfg, previewDate, excludeIDs)
}

func (s *displayService) previewPhotosWithExcludes(cfg *model.DisplayStrategyConfig, previewDate *time.Time, excludePhotoIDs []uint) ([]*model.Photo, error) {
	if cfg == nil {
		defaultCfg := defaultDisplayStrategyConfig()
		cfg = &defaultCfg
	}

	normalizeDisplayStrategyConfig(cfg)
	targetDate := resolvePreviewDate(previewDate)

	switch cfg.Algorithm {
	case "random":
		return s.photoRepo.GetRandom(cfg.DailyCount, cfg.MinBeautyScore, cfg.MinMemoryScore, excludePhotoIDs)
	case "on_this_day":
		return s.getOnThisDayPhotos(targetDate, excludePhotoIDs, *cfg, cfg.DailyCount)
	case "event_curated":
		return s.curateEventPhotos(targetDate, excludePhotoIDs, *cfg, cfg.DailyCount)
	case "smart":
		return s.getOnThisDayPhotos(targetDate, excludePhotoIDs, *cfg, cfg.DailyCount)
	default:
		return nil, fmt.Errorf("preview for algorithm %s is not implemented", cfg.Algorithm)
	}
}

// GetOnThisDayPhoto 往年今日算法
func (s *displayService) GetOnThisDayPhoto(deviceIDStr string) (*model.Photo, error) {
	// 获取设备
	device, err := s.deviceRepo.GetByDeviceID(deviceIDStr)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	// 获取最近已展示的照片 ID（避免重复）
	excludePhotoIDs, err := s.displayRecordRepo.GetDisplayedPhotoIDs(device.ID, s.config.Display.AvoidRepeatDays)
	if err != nil {
		logger.Warnf("Get displayed photo IDs failed: %v", err)
		excludePhotoIDs = []uint{}
	}

	strategyConfig := s.getDisplayStrategyConfig()

	photos, err := s.getOnThisDayPhotos(time.Now(), excludePhotoIDs, strategyConfig, 1)
	if err != nil {
		return nil, err
	}
	if len(photos) == 0 {
		return nil, fmt.Errorf("no photos available")
	}

	return photos[0], nil
}

// getEventCuratedPhoto 策展引擎获取单张展示照片
func (s *displayService) getEventCuratedPhoto(deviceIDStr string, cfg model.DisplayStrategyConfig) (*model.Photo, error) {
	device, err := s.deviceRepo.GetByDeviceID(deviceIDStr)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	excludePhotoIDs, err := s.displayRecordRepo.GetDisplayedPhotoIDs(device.ID, s.config.Display.AvoidRepeatDays)
	if err != nil {
		logger.Warnf("Get displayed photo IDs failed: %v", err)
		excludePhotoIDs = []uint{}
	}

	photos, err := s.curateEventPhotos(time.Now(), excludePhotoIDs, cfg, 1)
	if err != nil {
		return nil, err
	}
	if len(photos) == 0 {
		return nil, fmt.Errorf("no photos available")
	}

	return photos[0], nil
}

func (s *displayService) getRandomPhoto(deviceIDStr string, cfg model.DisplayStrategyConfig) (*model.Photo, error) {
	device, err := s.deviceRepo.GetByDeviceID(deviceIDStr)
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

// RecordDisplay 记录展示
func (s *displayService) RecordDisplay(record *model.DisplayRecord) error {
	return s.displayRecordRepo.Create(record)
}

func (s *displayService) getOnThisDayPhotos(targetDate time.Time, excludePhotoIDs []uint, cfg model.DisplayStrategyConfig, limit int) ([]*model.Photo, error) {
	normalizeDisplayStrategyConfig(&cfg)
	if limit <= 0 {
		limit = 1
	}

	// 第1层：on_this_day — 按月日匹配，窗口 [3, 7, 30]
	fallbackDays := s.config.Display.FallbackDays
	if len(fallbackDays) == 0 {
		fallbackDays = []int{3, 7, 30}
	}
	// 去掉 365 天窗口（语义不合理，用全局兜底替代）
	var effectiveDays []int
	for _, d := range fallbackDays {
		if d < 365 {
			effectiveDays = append(effectiveDays, d)
		}
	}
	if len(effectiveDays) == 0 {
		effectiveDays = []int{3, 7, 30}
	}

	targetPoolSize := max(limit*cfg.CandidatePoolFactor, max(limit*2, 6))
	collectedAll := make([]*model.Photo, 0, targetPoolSize)
	collectedSeen := make(map[uint]struct{}, targetPoolSize)
	bestSelected := make([]*model.Photo, 0, limit)

	for _, days := range effectiveDays {
		logger.Debugf("Trying on_this_day fallback: target=%s, ±%d days", targetDate.Format("2006-01-02"), days)

		// 计算月日窗口
		startDate := targetDate.AddDate(0, 0, -days)
		endDate := targetDate.AddDate(0, 0, days)
		monthDayStart := startDate.Format("01-02")
		monthDayEnd := endDate.Format("01-02")

		candidates, err := s.photoRepo.GetOnThisDayCandidates(
			monthDayStart, monthDayEnd,
			cfg.MinBeautyScore, cfg.MinMemoryScore,
			excludePhotoIDs, targetPoolSize,
		)
		if err != nil {
			logger.Warnf("GetOnThisDayCandidates failed: %v", err)
			continue
		}

		if len(candidates) > 0 {
			collectedAll = appendUniquePhotos(collectedAll, candidates, collectedSeen)
			selected := selectOnThisDayPhotos(targetDate, collectedAll, limit, cfg)
			logger.Infof(
				"Found on_this_day candidates with fallback ±%d days, window_candidates=%d, total_candidates=%d, selected=%d",
				days, len(candidates), len(collectedAll), len(selected),
			)
			if len(selected) > len(bestSelected) {
				bestSelected = append([]*model.Photo(nil), selected...)
			}
			if len(selected) >= limit {
				return selected, nil
			}
		}
	}

	if len(bestSelected) > 0 {
		return bestSelected, nil
	}

	// 第2层：全局兜底 — 按分数排序取 top N
	logger.Infof("No on_this_day match found, selecting top scored photos as global fallback")
	topPhotos, err := s.selectGlobalFallbackPhotos(excludePhotoIDs, cfg, limit)
	if err != nil {
		return nil, fmt.Errorf("get top scored photo: %w", err)
	}
	if len(topPhotos) > 0 {
		return topPhotos, nil
	}

	return nil, nil
}

func (s *displayService) selectGlobalFallbackPhotos(excludePhotoIDs []uint, cfg model.DisplayStrategyConfig, limit int) ([]*model.Photo, error) {
	poolSize := max(limit*cfg.CandidatePoolFactor, max(limit*2, 6))

	// 先用阈值过滤
	candidates, err := s.photoRepo.GetTopScoredCandidates(cfg.MinBeautyScore, cfg.MinMemoryScore, excludePhotoIDs, poolSize)
	if err != nil {
		return nil, fmt.Errorf("get top scored candidates: %w", err)
	}
	if len(candidates) > 0 {
		return selectDiversifiedPhotos(candidates, limit, cfg), nil
	}

	// 降低阈值到 0，忽略 exclude
	candidates, err = s.photoRepo.GetTopScoredCandidates(0, 0, nil, poolSize)
	if err != nil {
		return nil, fmt.Errorf("get unrestricted candidates: %w", err)
	}

	return selectDiversifiedPhotos(candidates, limit, cfg), nil
}
