package service

import (
	"os"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	_ = logger.Init(config.LoggingConfig{Level: "error", Console: true})
	code := m.Run()
	logger.Sync()
	os.Exit(code)
}

type stubPhotoRepo struct {
	listAllPhotos      []*model.Photo
	getByDateRangeFunc func(start, end time.Time) ([]*model.Photo, error)
}

func (r *stubPhotoRepo) Create(photo *model.Photo) error                     { return nil }
func (r *stubPhotoRepo) Update(photo *model.Photo) error                     { return nil }
func (r *stubPhotoRepo) Delete(id uint) error                                { return nil }
func (r *stubPhotoRepo) GetByID(id uint) (*model.Photo, error)               { return nil, nil }
func (r *stubPhotoRepo) GetByFilePath(filePath string) (*model.Photo, error) { return nil, nil }
func (r *stubPhotoRepo) GetByFileHash(fileHash string) (*model.Photo, error) { return nil, nil }
func (r *stubPhotoRepo) Exists(id uint) (bool, error)                        { return false, nil }
func (r *stubPhotoRepo) ExistsByFilePath(filePath string) (bool, error)      { return false, nil }
func (r *stubPhotoRepo) List(page, pageSize int, analyzed *bool, hasThumbnail *bool, hasGPS *bool, location string, search string, sortBy string, sortDesc bool, enabledPaths []string) ([]*model.Photo, int64, error) {
	return nil, 0, nil
}
func (r *stubPhotoRepo) ListAll() ([]*model.Photo, error)                { return r.listAllPhotos, nil }
func (r *stubPhotoRepo) ListByIDs(ids []uint) ([]*model.Photo, error)    { return nil, nil }
func (r *stubPhotoRepo) GetUnanalyzed(limit int) ([]*model.Photo, error) { return nil, nil }
func (r *stubPhotoRepo) MarkAsAnalyzed(id uint, description, caption, mainCategory, tags string, memoryScore, beautyScore int) error {
	return nil
}
func (r *stubPhotoRepo) CountAnalyzed() (int64, error)   { return 0, nil }
func (r *stubPhotoRepo) CountUnanalyzed() (int64, error) { return 0, nil }
func (r *stubPhotoRepo) GetByDateRange(start, end time.Time) ([]*model.Photo, error) {
	if r.getByDateRangeFunc != nil {
		return r.getByDateRangeFunc(start, end)
	}
	return nil, nil
}
func (r *stubPhotoRepo) GetTopByScore(limit int, excludePhotoIDs []uint) ([]*model.Photo, error) {
	return nil, nil
}
func (r *stubPhotoRepo) GetRandom(limit, minBeautyScore, minMemoryScore int, excludePhotoIDs []uint) ([]*model.Photo, error) {
	return nil, nil
}
func (r *stubPhotoRepo) GetByLocation(location string, limit int) ([]*model.Photo, error) {
	return nil, nil
}
func (r *stubPhotoRepo) Count() (int64, error)                                  { return 0, nil }
func (r *stubPhotoRepo) CountByLocation() (map[string]int64, error)             { return nil, nil }
func (r *stubPhotoRepo) GetCategories() ([]string, error)                       { return nil, nil }
func (r *stubPhotoRepo) GetTags() ([]string, error)                             { return nil, nil }
func (r *stubPhotoRepo) BatchCreate(photos []*model.Photo, batchSize int) error { return nil }
func (r *stubPhotoRepo) BatchUpdate(photos []*model.Photo, batchSize int) error { return nil }
func (r *stubPhotoRepo) UpdateLocation(id uint, location string) error          { return nil }
func (r *stubPhotoRepo) ListWithGPS() ([]*model.Photo, error)                   { return nil, nil }
func (r *stubPhotoRepo) ListByPathPrefix(prefix string) ([]*model.Photo, error) { return nil, nil }
func (r *stubPhotoRepo) SoftDeleteByPathPrefix(prefix string) error             { return nil }
func (r *stubPhotoRepo) CountByPathPrefix(prefix string) (int64, error)         { return 0, nil }
func (r *stubPhotoRepo) GetDerivedStatusByPathPrefix(prefix string) (*model.PathDerivedStatus, error) {
	return &model.PathDerivedStatus{}, nil
}

func TestNormalizeDisplayStrategyConfig_MergesSmartIntoOnThisDay(t *testing.T) {
	cfg := &model.DisplayStrategyConfig{Algorithm: "smart"}

	normalizeDisplayStrategyConfig(cfg)

	require.Equal(t, "on_this_day", cfg.Algorithm)
}

func TestGetOnThisDayPhotos_PrefersStrictMatchWithThresholds(t *testing.T) {
	targetDate := time.Date(2026, 3, 6, 10, 0, 0, 0, time.Local)
	strictTakenAtA := time.Date(2025, 3, 8, 9, 0, 0, 0, time.Local)
	strictTakenAtB := time.Date(2025, 3, 6, 9, 0, 0, 0, time.Local)
	strictTakenAtFiltered := time.Date(2025, 3, 7, 9, 0, 0, 0, time.Local)

	repo := &stubPhotoRepo{
		getByDateRangeFunc: func(start, end time.Time) ([]*model.Photo, error) {
			return []*model.Photo{
				{ID: 1, TakenAt: &strictTakenAtA, AIAnalyzed: true, MemoryScore: 88, BeautyScore: 82, OverallScore: 86},
				{ID: 2, TakenAt: &strictTakenAtB, AIAnalyzed: true, MemoryScore: 78, BeautyScore: 76, OverallScore: 77},
				{ID: 3, TakenAt: &strictTakenAtFiltered, AIAnalyzed: true, MemoryScore: 55, BeautyScore: 95, OverallScore: 67},
			}, nil
		},
	}

	svc := &displayService{
		photoRepo: repo,
		config: &config.Config{
			Display: config.DisplayConfig{FallbackDays: []int{3, 7, 30, 365}},
		},
	}

	cfg := model.DisplayStrategyConfig{Algorithm: "on_this_day", MinBeautyScore: 70, MinMemoryScore: 60, DailyCount: 1}
	photos, err := svc.getOnThisDayPhotos(targetDate, nil, cfg, 1)

	require.NoError(t, err)
	require.Len(t, photos, 1)
	require.Equal(t, uint(2), photos[0].ID)
}

func TestGetOnThisDayPhotos_FallsBackToCalendarMemoryWhenNoStrictMatch(t *testing.T) {
	targetDate := time.Date(2026, 3, 6, 10, 0, 0, 0, time.Local)
	calendarTakenAtA := time.Date(2020, 3, 4, 9, 0, 0, 0, time.Local)
	calendarTakenAtB := time.Date(2021, 3, 4, 9, 0, 0, 0, time.Local)
	filteredTakenAt := time.Date(2019, 3, 4, 9, 0, 0, 0, time.Local)

	repo := &stubPhotoRepo{
		getByDateRangeFunc: func(start, end time.Time) ([]*model.Photo, error) {
			return nil, nil
		},
		listAllPhotos: []*model.Photo{
			{ID: 11, TakenAt: &calendarTakenAtA, AIAnalyzed: true, MemoryScore: 92, BeautyScore: 83, OverallScore: 89},
			{ID: 12, TakenAt: &calendarTakenAtB, AIAnalyzed: true, MemoryScore: 80, BeautyScore: 79, OverallScore: 79},
			{ID: 13, TakenAt: &filteredTakenAt, AIAnalyzed: true, MemoryScore: 40, BeautyScore: 95, OverallScore: 56},
		},
	}

	svc := &displayService{
		photoRepo: repo,
		config: &config.Config{
			Display: config.DisplayConfig{FallbackDays: []int{3, 7, 30, 365}},
		},
	}

	cfg := model.DisplayStrategyConfig{Algorithm: "on_this_day", MinBeautyScore: 70, MinMemoryScore: 60, DailyCount: 1}
	photos, err := svc.getOnThisDayPhotos(targetDate, nil, cfg, 1)

	require.NoError(t, err)
	require.Len(t, photos, 1)
	require.Equal(t, uint(11), photos[0].ID)
}

func TestGetOnThisDayPhotos_DiversifiesTimeAndLocationAcrossYears(t *testing.T) {
	targetDate := time.Date(2026, 3, 6, 10, 0, 0, 0, time.Local)
	closeTakenAtA := time.Date(2025, 3, 6, 10, 0, 0, 0, time.Local)
	closeTakenAtB := time.Date(2025, 3, 6, 10, 10, 0, 0, time.Local)
	diverseTakenAt := time.Date(2024, 3, 5, 8, 0, 0, 0, time.Local)
	otherYearTakenAt := time.Date(2023, 3, 7, 7, 30, 0, 0, time.Local)

	repo := &stubPhotoRepo{
		getByDateRangeFunc: func(start, end time.Time) ([]*model.Photo, error) {
			switch start.Year() {
			case 2025:
				lat := 39.9000
				lon := 116.3900
				latClose := 39.9004
				lonClose := 116.3903
				return []*model.Photo{
					{ID: 21, FilePath: "/photos/trip-a/1.jpg", TakenAt: &closeTakenAtA, AIAnalyzed: true, MemoryScore: 92, BeautyScore: 91, OverallScore: 92, GPSLatitude: &lat, GPSLongitude: &lon, Location: "北京"},
					{ID: 22, FilePath: "/photos/trip-a/2.jpg", TakenAt: &closeTakenAtB, AIAnalyzed: true, MemoryScore: 90, BeautyScore: 90, OverallScore: 91, GPSLatitude: &latClose, GPSLongitude: &lonClose, Location: "北京"},
				}, nil
			case 2024:
				lat := 31.2304
				lon := 121.4737
				return []*model.Photo{{ID: 23, FilePath: "/photos/trip-b/1.jpg", TakenAt: &diverseTakenAt, AIAnalyzed: true, MemoryScore: 84, BeautyScore: 83, OverallScore: 84, GPSLatitude: &lat, GPSLongitude: &lon, Location: "上海"}}, nil
			case 2023:
				return []*model.Photo{{ID: 24, FilePath: "/photos/trip-c/1.jpg", TakenAt: &otherYearTakenAt, AIAnalyzed: true, MemoryScore: 80, BeautyScore: 82, OverallScore: 81, Location: "杭州"}}, nil
			default:
				return nil, nil
			}
		},
	}

	svc := &displayService{
		photoRepo: repo,
		config: &config.Config{
			Display: config.DisplayConfig{FallbackDays: []int{3, 7, 30, 365}},
		},
	}

	cfg := model.DisplayStrategyConfig{Algorithm: "on_this_day", MinBeautyScore: 70, MinMemoryScore: 60, DailyCount: 2}
	photos, err := svc.getOnThisDayPhotos(targetDate, nil, cfg, 2)

	require.NoError(t, err)
	require.Len(t, photos, 2)
	require.Equal(t, uint(21), photos[0].ID)
	require.Equal(t, uint(23), photos[1].ID)
}

func TestGetOnThisDayPhotos_PrefersCloserCalendarDateForAdjacentPreviewDays(t *testing.T) {
	previewMarch5 := time.Date(2026, 3, 5, 10, 0, 0, 0, time.Local)
	previewMarch6 := time.Date(2026, 3, 6, 10, 0, 0, 0, time.Local)
	march5Photo := time.Date(2025, 3, 5, 9, 0, 0, 0, time.Local)
	march6Photo := time.Date(2025, 3, 6, 9, 0, 0, 0, time.Local)
	march8HighScore := time.Date(2025, 3, 8, 9, 0, 0, 0, time.Local)

	repo := &stubPhotoRepo{
		getByDateRangeFunc: func(start, end time.Time) ([]*model.Photo, error) {
			return []*model.Photo{
				{ID: 31, FilePath: "/photos/day-5.jpg", TakenAt: &march5Photo, AIAnalyzed: true, MemoryScore: 80, BeautyScore: 80, OverallScore: 80, Location: "苏州"},
				{ID: 32, FilePath: "/photos/day-6.jpg", TakenAt: &march6Photo, AIAnalyzed: true, MemoryScore: 82, BeautyScore: 82, OverallScore: 82, Location: "苏州"},
				{ID: 33, FilePath: "/photos/day-8.jpg", TakenAt: &march8HighScore, AIAnalyzed: true, MemoryScore: 95, BeautyScore: 95, OverallScore: 95, Location: "苏州"},
			}, nil
		},
	}

	svc := &displayService{
		photoRepo: repo,
		config: &config.Config{
			Display: config.DisplayConfig{FallbackDays: []int{3, 7, 30, 365}},
		},
	}

	cfg := model.DisplayStrategyConfig{Algorithm: "on_this_day", MinBeautyScore: 70, MinMemoryScore: 60, DailyCount: 1}

	photosMarch5, err := svc.getOnThisDayPhotos(previewMarch5, nil, cfg, 1)
	require.NoError(t, err)
	require.Len(t, photosMarch5, 1)
	require.Equal(t, uint(31), photosMarch5[0].ID)

	photosMarch6, err := svc.getOnThisDayPhotos(previewMarch6, nil, cfg, 1)
	require.NoError(t, err)
	require.Len(t, photosMarch6, 1)
	require.Equal(t, uint(32), photosMarch6[0].ID)
}

func TestGetOnThisDayPhotos_FillsRemainingSlotsFromWiderFallbackWindow(t *testing.T) {
	targetDate := time.Date(2026, 3, 6, 10, 0, 0, 0, time.Local)
	exactA := time.Date(2025, 3, 6, 9, 0, 0, 0, time.Local)
	exactB := time.Date(2024, 3, 6, 9, 0, 0, 0, time.Local)
	nearby := time.Date(2023, 3, 2, 9, 0, 0, 0, time.Local)

	repo := &stubPhotoRepo{
		getByDateRangeFunc: func(start, end time.Time) ([]*model.Photo, error) {
			windowDays := int(end.Sub(start) / (24 * time.Hour) / 2)
			if windowDays <= 3 {
				return []*model.Photo{
					{ID: 41, FilePath: "/photos/exact-a.jpg", TakenAt: &exactA, AIAnalyzed: true, MemoryScore: 86, BeautyScore: 78, OverallScore: 82, Location: "广州"},
					{ID: 42, FilePath: "/photos/exact-b.jpg", TakenAt: &exactB, AIAnalyzed: true, MemoryScore: 86, BeautyScore: 78, OverallScore: 82, Location: "深圳"},
				}, nil
			}
			return []*model.Photo{
				{ID: 41, FilePath: "/photos/exact-a.jpg", TakenAt: &exactA, AIAnalyzed: true, MemoryScore: 86, BeautyScore: 78, OverallScore: 82, Location: "广州"},
				{ID: 42, FilePath: "/photos/exact-b.jpg", TakenAt: &exactB, AIAnalyzed: true, MemoryScore: 86, BeautyScore: 78, OverallScore: 82, Location: "深圳"},
				{ID: 43, FilePath: "/photos/nearby.jpg", TakenAt: &nearby, AIAnalyzed: true, MemoryScore: 91, BeautyScore: 79, OverallScore: 85, Location: "杭州"},
			}, nil
		},
	}

	svc := &displayService{
		photoRepo: repo,
		config: &config.Config{
			Display: config.DisplayConfig{FallbackDays: []int{3, 7, 30, 365}},
		},
	}

	cfg := model.DisplayStrategyConfig{Algorithm: "on_this_day", MinBeautyScore: 70, MinMemoryScore: 85, DailyCount: 3}
	photos, err := svc.getOnThisDayPhotos(targetDate, nil, cfg, 3)

	require.NoError(t, err)
	require.Len(t, photos, 3)
	require.Equal(t, []uint{41, 42, 43}, []uint{photos[0].ID, photos[1].ID, photos[2].ID})
}

func TestSelectDiversifiedPhotos_UsesEffectiveTimeWhenTakenAtMissing(t *testing.T) {
	baseTime := time.Date(2024, 6, 1, 10, 0, 0, 0, time.Local)
	closeCreateA := baseTime
	closeCreateB := baseTime.Add(10 * time.Minute)
	farCreate := baseTime.Add(48 * time.Hour)

	photos := []*model.Photo{
		{ID: 51, FilePath: "/photos/scan-a.jpg", FileCreateTime: &closeCreateA, AIAnalyzed: true, MemoryScore: 95, BeautyScore: 90, OverallScore: 93, Location: "成都"},
		{ID: 52, FilePath: "/photos/scan-b.jpg", FileCreateTime: &closeCreateB, AIAnalyzed: true, MemoryScore: 94, BeautyScore: 89, OverallScore: 92, Location: "成都"},
		{ID: 53, FilePath: "/photos/scan-c.jpg", FileCreateTime: &farCreate, AIAnalyzed: true, MemoryScore: 90, BeautyScore: 88, OverallScore: 90, Location: "重庆"},
	}

	cfg := defaultDisplayStrategyConfig()
	selected := selectDiversifiedPhotos(photos, 2, cfg)

	require.Len(t, selected, 2)
	require.Equal(t, uint(51), selected[0].ID)
	require.Equal(t, uint(53), selected[1].ID)
}
