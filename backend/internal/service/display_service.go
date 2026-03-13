package service

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"sort"
	"strings"
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
	PreviewPhotos(cfg *model.DisplayStrategyConfig, previewDate *time.Time) ([]*model.Photo, error)

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
	return s.previewPhotosWithExcludes(cfg, previewDate, nil)
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
		Algorithm:            "on_this_day",
		MinBeautyScore:       70,
		MinMemoryScore:       60,
		DailyCount:           3,
		CandidatePoolFactor:  5,
		MinTimeGapHours:      24,
		MaxPhotosPerEvent:    1,
		MaxPhotosPerLocation: 1,
		LocationBucketKM:     3,
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
	if cfg.CandidatePoolFactor <= 0 {
		cfg.CandidatePoolFactor = 5
	}
	if cfg.CandidatePoolFactor > 20 {
		cfg.CandidatePoolFactor = 20
	}
	if cfg.MinTimeGapHours < 0 {
		cfg.MinTimeGapHours = 0
	}
	if cfg.MinTimeGapHours == 0 {
		cfg.MinTimeGapHours = 24
	}
	if cfg.MaxPhotosPerEvent <= 0 {
		cfg.MaxPhotosPerEvent = 1
	}
	if cfg.MaxPhotosPerLocation <= 0 {
		cfg.MaxPhotosPerLocation = 1
	}
	if cfg.LocationBucketKM <= 0 {
		cfg.LocationBucketKM = 3
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

func selectOnThisDayPhotos(targetDate time.Time, photos []*model.Photo, limit int, cfg model.DisplayStrategyConfig) []*model.Photo {
	return selectDiversifiedRankedPhotos(rankOnThisDayCandidates(targetDate, photos), limit, cfg)
}

type diversitySelectionOptions struct {
	ignoreTimeGap      bool
	ignoreEventLimit   bool
	ignoreLocationCaps bool
}

func selectDiversifiedPhotos(photos []*model.Photo, limit int, cfg model.DisplayStrategyConfig) []*model.Photo {
	if len(photos) == 0 || limit <= 0 {
		return nil
	}

	return selectDiversifiedRankedPhotos(selectTopPhotos(photos, len(photos)), limit, cfg)
}

func selectDiversifiedRankedPhotos(ranked []*model.Photo, limit int, cfg model.DisplayStrategyConfig) []*model.Photo {
	if len(ranked) == 0 || limit <= 0 {
		return nil
	}

	poolSize := min(len(ranked), max(limit*cfg.CandidatePoolFactor, max(limit*2, 6)))
	primaryPool := ranked[:poolSize]
	remainder := ranked[poolSize:]
	selected := make([]*model.Photo, 0, min(limit, len(ranked)))
	selectedIDs := make(map[uint]struct{}, limit)
	passes := []diversitySelectionOptions{
		{},
		{ignoreLocationCaps: true},
		{ignoreLocationCaps: true, ignoreEventLimit: true},
		{ignoreLocationCaps: true, ignoreEventLimit: true, ignoreTimeGap: true},
	}

	for _, pass := range passes {
		selected = appendDiversePhotos(selected, primaryPool, limit, cfg, pass, selectedIDs)
		if len(selected) >= limit {
			return selected
		}
	}

	for _, pass := range passes {
		selected = appendDiversePhotos(selected, remainder, limit, cfg, pass, selectedIDs)
		if len(selected) >= limit {
			return selected
		}
	}

	return selected
}

func rankOnThisDayCandidates(targetDate time.Time, photos []*model.Photo) []*model.Photo {
	if len(photos) == 0 {
		return nil
	}

	ranked := append([]*model.Photo(nil), photos...)
	sort.SliceStable(ranked, func(i, j int) bool {
		left := ranked[i]
		right := ranked[j]

		leftDistance := onThisDayDateDistance(targetDate, left)
		rightDistance := onThisDayDateDistance(targetDate, right)
		if leftDistance != rightDistance {
			return leftDistance < rightDistance
		}

		leftYearGap := onThisDayYearGap(targetDate, left)
		rightYearGap := onThisDayYearGap(targetDate, right)
		if leftYearGap != rightYearGap {
			return leftYearGap < rightYearGap
		}

		if left.OverallScore != right.OverallScore {
			return left.OverallScore > right.OverallScore
		}
		if left.MemoryScore != right.MemoryScore {
			return left.MemoryScore > right.MemoryScore
		}
		if left.TakenAt == nil {
			return false
		}
		if right.TakenAt == nil {
			return true
		}
		return left.TakenAt.After(*right.TakenAt)
	})

	return ranked
}

func onThisDayDateDistance(targetDate time.Time, photo *model.Photo) int {
	if photo == nil || photo.TakenAt == nil {
		return math.MaxInt
	}
	anchor := time.Date(photo.TakenAt.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, time.Local)
	photoDate := time.Date(photo.TakenAt.Year(), photo.TakenAt.Month(), photo.TakenAt.Day(), 0, 0, 0, 0, time.Local)
	delta := photoDate.Sub(anchor)
	if delta < 0 {
		delta = -delta
	}
	return int(delta / (24 * time.Hour))
}

func onThisDayYearGap(targetDate time.Time, photo *model.Photo) int {
	if photo == nil || photo.TakenAt == nil {
		return math.MaxInt
	}
	gap := targetDate.Year() - photo.TakenAt.Year()
	if gap < 0 {
		gap = -gap
	}
	return gap
}

func appendDiversePhotos(selected []*model.Photo, candidates []*model.Photo, limit int, cfg model.DisplayStrategyConfig, options diversitySelectionOptions, selectedIDs map[uint]struct{}) []*model.Photo {
	for _, photo := range candidates {
		if len(selected) >= limit {
			return selected
		}
		if photo == nil {
			continue
		}
		if _, exists := selectedIDs[photo.ID]; exists {
			continue
		}
		if !options.ignoreTimeGap && hasTimeGapConflict(photo, selected, cfg.MinTimeGapHours) {
			continue
		}
		if !options.ignoreEventLimit && exceedsEventLimit(photo, selected, cfg) {
			continue
		}
		if !options.ignoreLocationCaps && exceedsLocationLimit(photo, selected, cfg) {
			continue
		}

		selected = append(selected, photo)
		selectedIDs[photo.ID] = struct{}{}
	}

	return selected
}

func hasTimeGapConflict(photo *model.Photo, selected []*model.Photo, minTimeGapHours int) bool {
	photoTime, ok := effectivePhotoTime(photo)
	if !ok || minTimeGapHours <= 0 {
		return false
	}
	minGap := time.Duration(minTimeGapHours) * time.Hour
	for _, existing := range selected {
		existingTime, exists := effectivePhotoTime(existing)
		if !exists {
			continue
		}
		delta := photoTime.Sub(existingTime)
		if delta < 0 {
			delta = -delta
		}
		if delta < minGap {
			return true
		}
	}
	return false
}

func exceedsEventLimit(photo *model.Photo, selected []*model.Photo, cfg model.DisplayStrategyConfig) bool {
	if photo == nil || cfg.MaxPhotosPerEvent <= 0 {
		return false
	}
	eventKey := buildPhotoEventKey(photo, cfg)
	if eventKey == "" {
		return false
	}
	count := 0
	for _, existing := range selected {
		if buildPhotoEventKey(existing, cfg) == eventKey {
			count++
		}
	}
	return count >= cfg.MaxPhotosPerEvent
}

func exceedsLocationLimit(photo *model.Photo, selected []*model.Photo, cfg model.DisplayStrategyConfig) bool {
	if photo == nil || cfg.MaxPhotosPerLocation <= 0 {
		return false
	}
	locationKey := buildPhotoLocationBucket(photo, cfg)
	if locationKey == "" {
		return false
	}
	count := 0
	for _, existing := range selected {
		if buildPhotoLocationBucket(existing, cfg) == locationKey {
			count++
		}
	}
	return count >= cfg.MaxPhotosPerLocation
}

func buildPhotoEventKey(photo *model.Photo, cfg model.DisplayStrategyConfig) string {
	if photo == nil {
		return ""
	}
	dateKey := "unknown-date"
	if photoTime, ok := effectivePhotoTime(photo); ok {
		dateKey = photoTime.In(time.Local).Format("2006-01-02")
	}
	if locationKey := buildPhotoLocationBucket(photo, cfg); locationKey != "" {
		return dateKey + "|" + locationKey
	}
	parentDir := strings.TrimSpace(filepath.Base(filepath.Dir(photo.FilePath)))
	if parentDir != "" && parentDir != "." && parentDir != string(filepath.Separator) {
		return dateKey + "|dir:" + strings.ToLower(parentDir)
	}
	return dateKey
}

func buildPhotoLocationBucket(photo *model.Photo, cfg model.DisplayStrategyConfig) string {
	if photo == nil {
		return ""
	}
	if photo.GPSLatitude != nil && photo.GPSLongitude != nil {
		bucketKM := cfg.LocationBucketKM
		if bucketKM <= 0 {
			bucketKM = 3
		}
		latStep := bucketKM / 111.0
		latRad := *photo.GPSLatitude * math.Pi / 180.0
		lonStep := bucketKM / (111.0 * math.Max(0.1, math.Cos(latRad)))
		latBucket := math.Round(*photo.GPSLatitude / latStep)
		lonBucket := math.Round(*photo.GPSLongitude / lonStep)
		return fmt.Sprintf("gps:%d:%d", int64(latBucket), int64(lonBucket))
	}
	location := strings.TrimSpace(strings.ToLower(photo.Location))
	if location == "" {
		return ""
	}
	return "loc:" + location
}

func appendUniquePhotos(dst []*model.Photo, incoming []*model.Photo, seen map[uint]struct{}) []*model.Photo {
	for _, photo := range incoming {
		if photo == nil {
			continue
		}
		if _, exists := seen[photo.ID]; exists {
			continue
		}
		seen[photo.ID] = struct{}{}
		dst = append(dst, photo)
	}
	return dst
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
		if photo.Status != model.PhotoStatusActive && photo.Status != "" {
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
			isLaterPhotoTime(photo, best) {
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
		leftTime, leftOK := effectivePhotoTime(result[i])
		rightTime, rightOK := effectivePhotoTime(result[j])
		if !leftOK {
			return false
		}
		if !rightOK {
			return true
		}
		return leftTime.After(rightTime)
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

func effectivePhotoTime(photo *model.Photo) (time.Time, bool) {
	if photo == nil {
		return time.Time{}, false
	}
	if photo.TakenAt != nil && !photo.TakenAt.IsZero() {
		return *photo.TakenAt, true
	}
	if photo.FileCreateTime != nil && !photo.FileCreateTime.IsZero() {
		return *photo.FileCreateTime, true
	}
	if photo.FileModTime != nil && !photo.FileModTime.IsZero() {
		return *photo.FileModTime, true
	}
	if !photo.CreatedAt.IsZero() {
		return photo.CreatedAt, true
	}
	return time.Time{}, false
}

func isLaterPhotoTime(left, right *model.Photo) bool {
	leftTime, leftOK := effectivePhotoTime(left)
	rightTime, rightOK := effectivePhotoTime(right)
	if !leftOK {
		return false
	}
	if !rightOK {
		return true
	}
	return leftTime.After(rightTime)
}
