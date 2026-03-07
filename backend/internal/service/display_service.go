package service

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
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
	PreviewPhotos(cfg *model.DisplayStrategyConfig, previewDate *time.Time) ([]*model.Photo, error)

	// 记录展示
	RecordDisplay(record *model.DisplayRecord) error

	// 往年今日算法
	GetOnThisDayPhoto(deviceID string) (*model.Photo, error)

	// 每日展示批次
	GenerateDailyBatch(date time.Time, force bool) (*model.DailyDisplayBatch, error)
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
	configService     ConfigService
	config            *config.Config
}

// NewDisplayService 创建展示服务
func NewDisplayService(
	db *gorm.DB,
	photoRepo repository.PhotoRepository,
	displayRecordRepo repository.DisplayRecordRepository,
	deviceRepo repository.DeviceRepository,
	configService ConfigService,
	cfg *config.Config,
) DisplayService {
	return &displayService{
		db:                db,
		photoRepo:         photoRepo,
		displayRecordRepo: displayRecordRepo,
		deviceRepo:        deviceRepo,
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
func (s *displayService) PreviewPhotos(cfg *model.DisplayStrategyConfig, previewDate *time.Time) ([]*model.Photo, error) {
	if cfg == nil {
		defaultCfg := defaultDisplayStrategyConfig()
		cfg = &defaultCfg
	}

	normalizeDisplayStrategyConfig(cfg)
	targetDate := resolvePreviewDate(previewDate)

	switch cfg.Algorithm {
	case "random":
		return s.photoRepo.GetRandom(cfg.DailyCount, cfg.MinBeautyScore, cfg.MinMemoryScore, nil)
	case "on_this_day":
		return s.getOnThisDayPhotos(targetDate, nil, *cfg, cfg.DailyCount)
	case "smart":
		return s.getOnThisDayPhotos(targetDate, nil, *cfg, cfg.DailyCount)
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

func (s *displayService) getSmartPhotos(cfg model.DisplayStrategyConfig, excludePhotoIDs []uint, limit int, referenceDate time.Time) ([]*model.Photo, error) {
	if limit <= 0 {
		limit = 1
	}

	photos, err := s.photoRepo.ListAll()
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}

	candidatesByMonthDay := make(map[string][]*model.Photo)
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

		if photo.TakenAt == nil {
			continue
		}
		if photo.MemoryScore < cfg.MinMemoryScore || photo.BeautyScore < cfg.MinBeautyScore {
			continue
		}

		monthDay := photo.TakenAt.Format("01-02")
		candidatesByMonthDay[monthDay] = append(candidatesByMonthDay[monthDay], photo)
	}

	for offset := 0; offset <= 365; offset++ {
		monthDay := referenceDate.AddDate(0, 0, -offset).Format("01-02")
		candidates := candidatesByMonthDay[monthDay]
		if len(candidates) > 0 {
			return selectSmartFallbackPhotos(candidates, limit), nil
		}
	}

	return nil, nil
}

func (s *displayService) getOnThisDayPhotos(targetDate time.Time, excludePhotoIDs []uint, cfg model.DisplayStrategyConfig, limit int) ([]*model.Photo, error) {
	if limit <= 0 {
		limit = 1
	}

	// 尝试多种降级策略
	fallbackDays := s.config.Display.FallbackDays // [3, 7, 30, 365]
	if len(fallbackDays) == 0 {
		fallbackDays = []int{3, 7, 30, 365}
	}

	for _, days := range fallbackDays {
		logger.Debugf("Trying on_this_day fallback: target=%s, ±%d days", targetDate.Format("2006-01-02"), days)

		// 逐年查找（从最近的年份开始）
		for year := 1; year <= 100; year++ {
			start := targetDate.AddDate(-year, 0, -days)
			end := targetDate.AddDate(-year, 0, days)

			photos, err := s.photoRepo.GetByDateRange(start, end)
			if err != nil {
				logger.Warnf("Get photos by date range failed: %v", err)
				continue
			}

			candidates := filterDisplayCandidates(photos, excludePhotoIDs, cfg, true)

			if len(candidates) > 0 {
				selected := selectOnThisDayPhotos(candidates, limit)
				logger.Infof(
					"Found on_this_day candidates with fallback ±%d days, year=%d, count=%d",
					days,
					year,
					len(selected),
				)
				return selected, nil
			}
		}
	}

	logger.Infof("No strict on_this_day match found, trying smart calendar fallback")
	smartFallbackPhotos, err := s.getSmartPhotos(cfg, excludePhotoIDs, limit, targetDate)
	if err != nil {
		return nil, fmt.Errorf("get smart fallback photos: %w", err)
	}
	if len(smartFallbackPhotos) > 0 {
		return smartFallbackPhotos, nil
	}

	logger.Warn("All on_this_day fallback strategies failed, selecting top scored photo")
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
	photos, err := s.photoRepo.ListAll()
	if err != nil {
		return nil, fmt.Errorf("list photos: %w", err)
	}

	candidates := filterDisplayCandidates(photos, excludePhotoIDs, cfg, false)
	if len(candidates) > 0 {
		return selectTopPhotos(candidates, limit), nil
	}

	if len(excludePhotoIDs) > 0 {
		candidates = filterDisplayCandidates(photos, nil, cfg, false)
		if len(candidates) > 0 {
			return selectTopPhotos(candidates, limit), nil
		}
	}

	var unrestricted []*model.Photo
	for _, photo := range photos {
		if photo != nil && photo.AIAnalyzed {
			unrestricted = append(unrestricted, photo)
		}
	}

	return selectTopPhotos(unrestricted, limit), nil
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
		Algorithm:      "on_this_day",
		MinBeautyScore: 70,
		MinMemoryScore: 60,
		DailyCount:     3,
	}
}

func normalizeDisplayStrategyConfig(cfg *model.DisplayStrategyConfig) {
	if cfg.Algorithm == "" {
		cfg.Algorithm = "on_this_day"
	}
	if cfg.Algorithm == "smart" {
		cfg.Algorithm = "on_this_day"
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

func selectOnThisDayPhotos(photos []*model.Photo, limit int) []*model.Photo {
	return selectTopPhotos(photos, limit)
}

func selectSmartFallbackPhotos(photos []*model.Photo, limit int) []*model.Photo {
	if len(photos) == 0 || limit <= 0 {
		return nil
	}

	ranked := selectTopPhotos(photos, len(photos))
	if len(ranked) <= limit*2 {
		return ranked[:min(limit, len(ranked))]
	}

	poolSize := min(len(ranked), max(limit*3, 6))
	pool := append([]*model.Photo(nil), ranked[:poolSize]...)
	rand.Shuffle(len(pool), func(i, j int) {
		pool[i], pool[j] = pool[j], pool[i]
	})

	selected := pool[:min(limit, len(pool))]
	return selectTopPhotos(selected, len(selected))
}

func filterDisplayCandidates(photos []*model.Photo, excludePhotoIDs []uint, cfg model.DisplayStrategyConfig, requireTakenAt bool) []*model.Photo {
	var candidates []*model.Photo
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
		if requireTakenAt && photo.TakenAt == nil {
			continue
		}
		if photo.MemoryScore < cfg.MinMemoryScore || photo.BeautyScore < cfg.MinBeautyScore {
			continue
		}

		candidates = append(candidates, photo)
	}

	return candidates
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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

func selectTopPhotos(photos []*model.Photo, limit int) []*model.Photo {
	if len(photos) == 0 || limit <= 0 {
		return nil
	}

	result := append([]*model.Photo(nil), photos...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].OverallScore != result[j].OverallScore {
			return result[i].OverallScore > result[j].OverallScore
		}
		if result[i].MemoryScore != result[j].MemoryScore {
			return result[i].MemoryScore > result[j].MemoryScore
		}
		if result[i].TakenAt == nil {
			return false
		}
		if result[j].TakenAt == nil {
			return true
		}
		return result[i].TakenAt.After(*result[j].TakenAt)
	})

	if limit >= len(result) {
		return result
	}

	return result[:limit]
}

func resolvePreviewDate(previewDate *time.Time) time.Time {
	if previewDate == nil || previewDate.IsZero() {
		return time.Now()
	}
	return *previewDate
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
