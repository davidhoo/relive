package service

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
)

// curationCandidate 策展候选
type curationCandidate struct {
	photo    *model.Photo
	event    *model.Event // nil for scattered photos
	channel  string       // "time_tunnel" / "peak_memory" / "geo_drift" / "hidden_gem"
	rawScore float64
	adjScore float64 // 修正后得分
}

// curateEventPhotos 策展引擎入口：多通道提名 → 评分修正 → 多样性选择 → 序列编排
func (s *displayService) curateEventPhotos(
	targetDate time.Time, excludePhotoIDs []uint,
	cfg model.DisplayStrategyConfig, limit int,
) ([]*model.Photo, error) {
	// 1. 计算常驻地
	homeLat, homeLon, _ := s.curationHomeBase()

	// 2. 获取近期展示事件 ID
	recentEventIDs := make(map[uint]bool)
	if ids, err := s.eventRepo.GetRecentlyDisplayedEventIDs(cfg.CurationFreshnessDays); err == nil {
		for _, id := range ids {
			recentEventIDs[id] = true
		}
	}

	// 3. 四通道提名
	var candidates []curationCandidate

	timeTunnelCandidates, err := s.nominateTimeTunnel(targetDate, recentEventIDs, excludePhotoIDs, cfg)
	if err != nil {
		logger.Warnf("Time tunnel nomination failed: %v", err)
	} else {
		candidates = append(candidates, timeTunnelCandidates...)
	}

	peakCandidates, err := s.nominatePeakMemories(recentEventIDs, excludePhotoIDs, cfg)
	if err != nil {
		logger.Warnf("Peak memories nomination failed: %v", err)
	} else {
		candidates = append(candidates, peakCandidates...)
	}

	if homeLat != 0 || homeLon != 0 {
		geoCandidates, err := s.nominateGeoDrift(homeLat, homeLon, recentEventIDs, excludePhotoIDs, cfg)
		if err != nil {
			logger.Warnf("Geo drift nomination failed: %v", err)
		} else {
			candidates = append(candidates, geoCandidates...)
		}
	}

	gemCandidates, err := s.nominateHiddenGems(recentEventIDs, excludePhotoIDs, cfg)
	if err != nil {
		logger.Warnf("Hidden gems nomination failed: %v", err)
	} else {
		candidates = append(candidates, gemCandidates...)
	}

	// 若完全没有候选，fallback 到 on_this_day
	if len(candidates) == 0 {
		logger.Info("No curation candidates found, falling back to on_this_day")
		return s.getOnThisDayPhotos(targetDate, excludePhotoIDs, cfg, limit)
	}

	logger.Infof("Curation collected %d candidates across channels", len(candidates))

	// 4. 动态评分修正
	applyCurationScoreAdjustments(candidates, targetDate, recentEventIDs, cfg)

	// 5. 多样性选择
	selected := selectCuratedPhotos(candidates, limit)

	// 6. 序列编排
	photos := make([]*model.Photo, 0, len(selected))
	for _, c := range selected {
		c.photo.CurationChannel = c.channel
		photos = append(photos, c.photo)
	}

	photos = arrangeCuratedSequence(photos)

	if len(photos) == 0 {
		logger.Info("Curation selection empty after diversity filter, falling back to on_this_day")
		return s.getOnThisDayPhotos(targetDate, excludePhotoIDs, cfg, limit)
	}

	return photos, nil
}

// --- 四通道提名 ---

// nominateTimeTunnel 时光隧道：往年同月日 ±N 天事件
func (s *displayService) nominateTimeTunnel(targetDate time.Time, recentEventIDs map[uint]bool, excludePhotoIDs []uint, cfg model.DisplayStrategyConfig) ([]curationCandidate, error) {
	monthDay := targetDate.Format("01-02")
	excludeIDs := mapKeysToSlice(recentEventIDs)

	events, err := s.eventRepo.GetOnThisDayEvents(monthDay, cfg.CurationTimeTunnelDays, excludeIDs, 30)
	if err != nil {
		return nil, err
	}

	return s.eventsToCandidates(events, "time_tunnel", excludePhotoIDs)
}

// nominatePeakMemories 巅峰回忆：全库 top event_score 事件
func (s *displayService) nominatePeakMemories(recentEventIDs map[uint]bool, excludePhotoIDs []uint, cfg model.DisplayStrategyConfig) ([]curationCandidate, error) {
	excludeIDs := mapKeysToSlice(recentEventIDs)

	events, err := s.eventRepo.GetTopScoredEvents(excludeIDs, cfg.CurationTopEventsLimit)
	if err != nil {
		return nil, err
	}

	return s.eventsToCandidates(events, "peak_memory", excludePhotoIDs)
}

// nominateGeoDrift 地理漂移：距常驻地最远事件
func (s *displayService) nominateGeoDrift(homeLat, homeLon float64, recentEventIDs map[uint]bool, excludePhotoIDs []uint, cfg model.DisplayStrategyConfig) ([]curationCandidate, error) {
	excludeIDs := mapKeysToSlice(recentEventIDs)

	events, err := s.eventRepo.GetFarthestEvents(homeLat, homeLon, excludeIDs, cfg.CurationGeoEventsLimit)
	if err != nil {
		return nil, err
	}

	return s.eventsToCandidates(events, "geo_drift", excludePhotoIDs)
}

// nominateHiddenGems 角落遗珠：从未展示的事件 + 无事件高颜值散片
func (s *displayService) nominateHiddenGems(recentEventIDs map[uint]bool, excludePhotoIDs []uint, cfg model.DisplayStrategyConfig) ([]curationCandidate, error) {
	var candidates []curationCandidate
	excludeIDs := mapKeysToSlice(recentEventIDs)

	// 未展示事件
	events, err := s.eventRepo.GetNeverDisplayedEvents(0, excludeIDs, 20)
	if err != nil {
		logger.Warnf("GetNeverDisplayedEvents failed: %v", err)
	} else {
		eventCandidates, err := s.eventsToCandidates(events, "hidden_gem", excludePhotoIDs)
		if err != nil {
			logger.Warnf("Hidden gem event candidates failed: %v", err)
		} else {
			candidates = append(candidates, eventCandidates...)
		}
	}

	// 无事件高颜值散片
	scatteredPhotos, err := s.photoRepo.GetScatteredHighQuality(cfg.CurationHiddenGemsMinBeauty, excludePhotoIDs, 20)
	if err != nil {
		logger.Warnf("GetScatteredHighQuality failed: %v", err)
	} else {
		for _, p := range scatteredPhotos {
			candidates = append(candidates, curationCandidate{
				photo:    p,
				event:    nil,
				channel:  "hidden_gem",
				rawScore: float64(p.BeautyScore),
				adjScore: float64(p.BeautyScore),
			})
		}
	}

	return candidates, nil
}

// eventsToCandidates 将事件列表转为候选（批量加载 cover photo）
func (s *displayService) eventsToCandidates(events []*model.Event, channel string, excludePhotoIDs []uint) ([]curationCandidate, error) {
	if len(events) == 0 {
		return nil, nil
	}

	// 收集所有 cover photo ID
	excludeSet := make(map[uint]bool, len(excludePhotoIDs))
	for _, id := range excludePhotoIDs {
		excludeSet[id] = true
	}

	var coverIDs []uint
	for _, e := range events {
		if e.CoverPhotoID != nil && !excludeSet[*e.CoverPhotoID] {
			coverIDs = append(coverIDs, *e.CoverPhotoID)
		}
	}

	if len(coverIDs) == 0 {
		return nil, nil
	}

	// 批量加载照片
	photos, err := s.photoRepo.ListByIDs(coverIDs)
	if err != nil {
		return nil, err
	}
	photoMap := make(map[uint]*model.Photo, len(photos))
	for _, p := range photos {
		if p.Status == model.PhotoStatusActive {
			photoMap[p.ID] = p
		}
	}

	var candidates []curationCandidate
	for _, e := range events {
		if e.CoverPhotoID == nil {
			continue
		}
		photo, ok := photoMap[*e.CoverPhotoID]
		if !ok {
			continue
		}
		candidates = append(candidates, curationCandidate{
			photo:    photo,
			event:    e,
			channel:  channel,
			rawScore: e.EventScore,
			adjScore: e.EventScore,
		})
	}

	return candidates, nil
}

// --- 常驻地计算 ---

type homeBaseCache struct {
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	City       string  `json:"city"`
	ComputedAt string  `json:"computed_at"`
}

func (s *displayService) curationHomeBase() (lat, lon float64, err error) {
	const configKey = "curation.home_base"

	// 尝试读取缓存
	if s.configService != nil {
		value, err := s.configService.GetWithDefault(configKey, "")
		if err == nil && value != "" {
			var cache homeBaseCache
			if json.Unmarshal([]byte(value), &cache) == nil {
				// 检查有效期（7 天）
				if computedAt, parseErr := time.Parse(time.RFC3339, cache.ComputedAt); parseErr == nil {
					if time.Since(computedAt) < 7*24*time.Hour && (cache.Lat != 0 || cache.Lon != 0) {
						return cache.Lat, cache.Lon, nil
					}
				}
			}
		}
	}

	// 计算常驻地：照片最多的城市的平均 GPS
	type cityAgg struct {
		City string  `gorm:"column:city"`
		Lat  float64 `gorm:"column:avg_lat"`
		Lon  float64 `gorm:"column:avg_lon"`
		Cnt  int64   `gorm:"column:cnt"`
	}
	var result cityAgg
	err = s.db.Table("photos").
		Select("city, AVG(gps_latitude) as avg_lat, AVG(gps_longitude) as avg_lon, COUNT(*) as cnt").
		Where("city != '' AND status = 'active' AND gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL").
		Group("city").
		Order("cnt DESC").
		Limit(1).
		Scan(&result).Error
	if err != nil || result.City == "" {
		return 0, 0, err
	}

	// 写入缓存
	if s.configService != nil {
		cache := homeBaseCache{
			Lat:        result.Lat,
			Lon:        result.Lon,
			City:       result.City,
			ComputedAt: time.Now().Format(time.RFC3339),
		}
		if data, marshalErr := json.Marshal(cache); marshalErr == nil {
			_ = s.configService.Set(configKey, string(data))
		}
	}

	return result.Lat, result.Lon, nil
}

// --- 动态评分修正 ---

func applyCurationScoreAdjustments(candidates []curationCandidate, targetDate time.Time, recentEventIDs map[uint]bool, cfg model.DisplayStrategyConfig) {
	for i := range candidates {
		c := &candidates[i]
		c.adjScore = c.rawScore

		// 季节对齐：photo.TakenAt 月份 == targetDate 月份
		if c.photo != nil && c.photo.TakenAt != nil {
			if c.photo.TakenAt.Month() == targetDate.Month() {
				c.adjScore *= cfg.CurationSeasonBoost
			}
		}

		// 新鲜度抑制：事件在 recentEventIDs 中
		if c.event != nil && recentEventIDs[c.event.ID] {
			c.adjScore *= cfg.CurationFreshnessPenalty
		}

		// 人物偏好：event.PrimaryTag 含人物关键词
		if c.event != nil && isPeopleRelated(c.event.PrimaryTag) {
			c.adjScore += cfg.CurationPeopleBonus
		}

		// 标签季节匹配
		if c.photo != nil && matchesCurrentSeason(c.photo, targetDate) {
			c.adjScore *= 1.15
		}

		// 展示衰减：event.DisplayCount > 0
		if c.event != nil && c.event.DisplayCount > 0 {
			c.adjScore *= 1.0 / (1.0 + float64(c.event.DisplayCount)*cfg.CurationDisplayDecayFactor)
		}
	}
}

// isPeopleRelated 判断标签是否与人物相关
func isPeopleRelated(tag string) bool {
	tag = strings.ToLower(tag)
	peopleKeywords := []string{
		"人物", "人像", "合影", "家人", "朋友", "孩子", "婚礼", "聚会",
		"portrait", "people", "family", "friend", "wedding", "group",
	}
	for _, kw := range peopleKeywords {
		if strings.Contains(tag, kw) {
			return true
		}
	}
	return false
}

// matchesCurrentSeason 判断照片标签是否匹配当前季节
func matchesCurrentSeason(photo *model.Photo, date time.Time) bool {
	month := date.Month()
	var seasonKeywords []string
	switch {
	case month >= 3 && month <= 5:
		seasonKeywords = []string{"春", "花", "spring", "blossom", "cherry"}
	case month >= 6 && month <= 8:
		seasonKeywords = []string{"夏", "海", "summer", "beach", "pool"}
	case month >= 9 && month <= 11:
		seasonKeywords = []string{"秋", "枫", "autumn", "fall", "harvest"}
	default:
		seasonKeywords = []string{"冬", "雪", "winter", "snow", "christmas"}
	}

	tags := strings.ToLower(photo.Tags)
	caption := strings.ToLower(photo.Caption)
	for _, kw := range seasonKeywords {
		if strings.Contains(tags, kw) || strings.Contains(caption, kw) {
			return true
		}
	}
	return false
}

// --- 多样性选择 ---

func selectCuratedPhotos(candidates []curationCandidate, limit int) []*curationCandidate {
	if len(candidates) == 0 || limit <= 0 {
		return nil
	}

	// 按 adjScore 降序排列
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].adjScore > candidates[j].adjScore
	})

	selected := make([]*curationCandidate, 0, limit)
	selectedEventIDs := make(map[uint]bool)
	selectedCategories := make(map[string]int)

	// 第一轮：严格隔离
	for i := range candidates {
		if len(selected) >= limit {
			break
		}
		c := &candidates[i]

		// 事件隔离：同 event_id 只选 1 张
		if c.event != nil {
			if selectedEventIDs[c.event.ID] {
				continue
			}
		}

		// 时间隔离：已选照片 taken_at ±24h 内跳过
		if hasTimeTunnelConflict(c, selected) {
			continue
		}

		// 内容隔离：同类别的降权（这里直接跳过超过 2 张的同类别）
		if c.event != nil && c.event.PrimaryCategory != "" {
			if selectedCategories[c.event.PrimaryCategory] >= 2 {
				continue
			}
		}

		selected = append(selected, c)
		if c.event != nil {
			selectedEventIDs[c.event.ID] = true
			if c.event.PrimaryCategory != "" {
				selectedCategories[c.event.PrimaryCategory]++
			}
		}
	}

	// 第二轮：若不够，放松时间和内容隔离
	if len(selected) < limit {
		selectedPhotoIDs := make(map[uint]bool)
		for _, c := range selected {
			selectedPhotoIDs[c.photo.ID] = true
		}

		for i := range candidates {
			if len(selected) >= limit {
				break
			}
			c := &candidates[i]
			if selectedPhotoIDs[c.photo.ID] {
				continue
			}
			// 仍保留事件隔离
			if c.event != nil && selectedEventIDs[c.event.ID] {
				continue
			}

			selected = append(selected, c)
			selectedPhotoIDs[c.photo.ID] = true
			if c.event != nil {
				selectedEventIDs[c.event.ID] = true
			}
		}
	}

	return selected
}

// hasTimeTunnelConflict 检查候选照片与已选照片是否在 ±24h 内
func hasTimeTunnelConflict(candidate *curationCandidate, selected []*curationCandidate) bool {
	if candidate.photo == nil || candidate.photo.TakenAt == nil {
		return false
	}
	candidateTime := *candidate.photo.TakenAt
	for _, s := range selected {
		if s.photo == nil || s.photo.TakenAt == nil {
			continue
		}
		delta := candidateTime.Sub(*s.photo.TakenAt)
		if delta < 0 {
			delta = -delta
		}
		if delta < 24*time.Hour {
			return true
		}
	}
	return false
}

// --- 序列编排 ---

func arrangeCuratedSequence(photos []*model.Photo) []*model.Photo {
	if len(photos) <= 2 {
		return photos
	}

	// 找出 BeautyScore 最高（首张）和 MemoryScore 最高（末张）
	bestBeautyIdx := 0
	bestMemoryIdx := 0
	for i, p := range photos {
		if p.BeautyScore > photos[bestBeautyIdx].BeautyScore {
			bestBeautyIdx = i
		}
		if p.MemoryScore > photos[bestMemoryIdx].MemoryScore {
			bestMemoryIdx = i
		}
	}

	// 如果最高美感和最高记忆是同一张照片，末张选次高记忆
	if bestBeautyIdx == bestMemoryIdx && len(photos) > 2 {
		secondMemoryIdx := -1
		for i, p := range photos {
			if i == bestBeautyIdx {
				continue
			}
			if secondMemoryIdx == -1 || p.MemoryScore > photos[secondMemoryIdx].MemoryScore {
				secondMemoryIdx = i
			}
		}
		if secondMemoryIdx >= 0 {
			bestMemoryIdx = secondMemoryIdx
		}
	}

	result := make([]*model.Photo, 0, len(photos))
	result = append(result, photos[bestBeautyIdx])

	// 中间照片按 MainCategory 交叉排列
	middle := make([]*model.Photo, 0, len(photos)-2)
	for i, p := range photos {
		if i == bestBeautyIdx || i == bestMemoryIdx {
			continue
		}
		middle = append(middle, p)
	}

	// 简单交叉：按 category 分组后交替取
	if len(middle) > 1 {
		sort.SliceStable(middle, func(i, j int) bool {
			return middle[i].MainCategory < middle[j].MainCategory
		})
	}

	result = append(result, middle...)
	result = append(result, photos[bestMemoryIdx])

	return result
}

// --- 辅助函数 ---

func mapKeysToSlice(m map[uint]bool) []uint {
	if len(m) == 0 {
		return nil
	}
	result := make([]uint, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	return result
}
