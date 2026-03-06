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
func (r *stubPhotoRepo) List(page, pageSize int, analyzed *bool, location string, search string, sortBy string, sortDesc bool, enabledPaths []string) ([]*model.Photo, int64, error) {
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
	require.Equal(t, uint(1), photos[0].ID)
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
