package service

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
)

// --- On-this-day selection ---

func selectOnThisDayPhotos(targetDate time.Time, photos []*model.Photo, limit int, cfg model.DisplayStrategyConfig) []*model.Photo {
	return selectDiversifiedRankedPhotos(rankOnThisDayCandidates(targetDate, photos), limit, cfg)
}

// --- Diversity selection ---

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

// --- Ranking ---

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

		leftWeighted := weightedDisplayScore(left)
		rightWeighted := weightedDisplayScore(right)
		if leftWeighted != rightWeighted {
			return leftWeighted > rightWeighted
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

// --- Utility functions ---

func selectTopPhotos(photos []*model.Photo, limit int) []*model.Photo {
	if len(photos) == 0 || limit <= 0 {
		return nil
	}

	result := append([]*model.Photo(nil), photos...)
	sort.SliceStable(result, func(i, j int) bool {
		leftWeighted := weightedDisplayScore(result[i])
		rightWeighted := weightedDisplayScore(result[j])
		if leftWeighted != rightWeighted {
			return leftWeighted > rightWeighted
		}
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

func weightedDisplayScore(photo *model.Photo) float64 {
	if photo == nil {
		return 0
	}
	return float64(photo.OverallScore) + photoPeoplePriorityBonus(photo.TopPersonCategory)
}

func photoPeoplePriorityBonus(category string) float64 {
	switch category {
	case model.PersonCategoryFamily:
		return 3
	case model.PersonCategoryFriend:
		return 2
	case model.PersonCategoryAcquaintance:
		return 1
	default:
		return 0
	}
}
