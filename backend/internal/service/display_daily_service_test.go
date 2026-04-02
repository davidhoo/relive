package service

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/disintegration/imaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDisplayService_GenerateDailyBatchAndServeIndependently(t *testing.T) {
	db := setupDisplayServiceTestDB(t)
	defer closeDisplayServiceTestDB(db)

	tempDir := t.TempDir()
	displayService, photoRepo, deviceRepo, configService := buildTestDisplayService(t, db, tempDir)
	targetDate := time.Now()
	createBatchPhotos(t, photoRepo, tempDir, targetDate, 3)
	setDisplayStrategy(t, configService, model.DisplayStrategyConfig{Algorithm: "random", MinBeautyScore: 60, MinMemoryScore: 60, DailyCount: 3})

	batch, err := displayService.GenerateDailyBatch(targetDate, true)
	require.NoError(t, err)
	require.Equal(t, 3, batch.ItemCount)
	require.Len(t, batch.Items, 3)

	deviceA := &model.Device{DeviceID: "DEV-A", Name: "Device A", APIKey: "key-a", DeviceType: model.DeviceTypeEmbedded, RenderProfile: "gdem075f52_480x800_4color", IsEnabled: true}
	deviceB := &model.Device{DeviceID: "DEV-B", Name: "Device B", APIKey: "key-b", DeviceType: model.DeviceTypeEmbedded, RenderProfile: "gdem075f52_480x800_4color", IsEnabled: true}
	require.NoError(t, deviceRepo.Create(deviceA))
	require.NoError(t, deviceRepo.Create(deviceB))

	firstA, err := displayService.GetDeviceDisplay(deviceA.ID, deviceA.RenderProfile)
	require.NoError(t, err)
	secondA, err := displayService.GetDeviceDisplay(deviceA.ID, deviceA.RenderProfile)
	require.NoError(t, err)
	thirdA, err := displayService.GetDeviceDisplay(deviceA.ID, deviceA.RenderProfile)
	require.NoError(t, err)
	loopA, err := displayService.GetDeviceDisplay(deviceA.ID, deviceA.RenderProfile)
	require.NoError(t, err)
	firstB, err := displayService.GetDeviceDisplay(deviceB.ID, deviceB.RenderProfile)
	require.NoError(t, err)

	assert.Equal(t, 1, firstA.Sequence)
	assert.Equal(t, 2, secondA.Sequence)
	assert.Equal(t, 3, thirdA.Sequence)
	assert.Equal(t, 1, loopA.Sequence)
	assert.Equal(t, 1, firstB.Sequence)

	for _, item := range batch.Items {
		require.FileExists(t, filepath.Join(tempDir, "display-batches", item.PreviewJPGPath))
		require.NotEmpty(t, item.Assets)
		for _, asset := range item.Assets {
			require.FileExists(t, filepath.Join(tempDir, "display-batches", asset.DitherPreviewPath))
			require.FileExists(t, filepath.Join(tempDir, "display-batches", asset.BinPath))
			require.FileExists(t, filepath.Join(tempDir, "display-batches", asset.HeaderPath))
		}
	}
}

func TestDisplayService_ForceRegenerateResetsPlaybackState(t *testing.T) {
	db := setupDisplayServiceTestDB(t)
	defer closeDisplayServiceTestDB(db)

	tempDir := t.TempDir()
	displayService, photoRepo, deviceRepo, configService := buildTestDisplayService(t, db, tempDir)
	targetDate := time.Now()
	createBatchPhotos(t, photoRepo, tempDir, targetDate, 6)
	setDisplayStrategy(t, configService, model.DisplayStrategyConfig{Algorithm: "random", MinBeautyScore: 60, MinMemoryScore: 60, DailyCount: 2})

	_, err := displayService.GenerateDailyBatch(targetDate, true)
	require.NoError(t, err)

	device := &model.Device{DeviceID: "DEV-RESET", Name: "Device Reset", APIKey: "key-reset", DeviceType: model.DeviceTypeEmbedded, RenderProfile: "gdem075f52_480x800_4color", IsEnabled: true}
	require.NoError(t, deviceRepo.Create(device))

	first, err := displayService.GetDeviceDisplay(device.ID, device.RenderProfile)
	require.NoError(t, err)
	second, err := displayService.GetDeviceDisplay(device.ID, device.RenderProfile)
	require.NoError(t, err)
	assert.Equal(t, 1, first.Sequence)
	assert.Equal(t, 2, second.Sequence)

	_, err = displayService.GenerateDailyBatch(targetDate, true)
	require.NoError(t, err)

	afterReset, err := displayService.GetDeviceDisplay(device.ID, device.RenderProfile)
	require.NoError(t, err)
	assert.Equal(t, 1, afterReset.Sequence)
}

func buildTestDisplayService(t *testing.T, db *gorm.DB, tempDir string) (DisplayService, repository.PhotoRepository, repository.DeviceRepository, ConfigService) {
	t.Helper()
	photoRepo := repository.NewPhotoRepository(db)
	displayRecordRepo := repository.NewDisplayRecordRepository(db)
	deviceRepo := repository.NewDeviceRepository(db)
	configRepo := repository.NewConfigRepository(db)
	configService := NewConfigService(configRepo)
	eventRepo := repository.NewEventRepository(db)
	cfg := &config.Config{}
	cfg.Photos.ThumbnailPath = filepath.Join(tempDir, "thumbnails")
	cfg.Display.FallbackDays = []int{3, 7, 30, 365}
	cfg.Display.AvoidRepeatDays = 7
	return NewDisplayService(db, photoRepo, displayRecordRepo, deviceRepo, eventRepo, configService, cfg), photoRepo, deviceRepo, configService
}

func setupDisplayServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Photo{},
		&model.Person{},
		&model.Face{},
		&model.AnalysisRuntimeLease{},
		&model.DisplayRecord{},
		&model.Device{},
		&model.DailyDisplayBatch{},
		&model.DailyDisplayItem{},
		&model.DailyDisplayAsset{},
		&model.DevicePlaybackState{},
		&model.AppConfig{},
		&model.City{},
		&model.User{},
		&model.Event{},
	))
	return db
}

func closeDisplayServiceTestDB(db *gorm.DB) {
	sqlDB, _ := db.DB()
	if sqlDB != nil {
		sqlDB.Close()
	}
}

func setDisplayStrategy(t *testing.T, configService ConfigService, cfg model.DisplayStrategyConfig) {
	t.Helper()
	payload, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, configService.Set("display.strategy", string(payload)))
}

func createBatchPhotos(t *testing.T, photoRepo repository.PhotoRepository, tempDir string, targetDate time.Time, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		filePath := filepath.Join(tempDir, "photos", targetDate.Format("2006-01-02"), fmt.Sprintf("photo-%d.jpg", index))
		require.NoError(t, os.MkdirAll(filepath.Dir(filePath), 0o755))
		img := imaging.New(1200, 1600, color.NRGBA{R: uint8(40 * index), G: uint8(80 + 20*index), B: uint8(120 + 10*index), A: 255})
		require.NoError(t, imaging.Save(img, filePath))
		takenAt := targetDate.AddDate(-1-index, 0, 0)
		photo := &model.Photo{
			FilePath:     filePath,
			FileName:     filepath.Base(filePath),
			FileSize:     1024,
			FileHash:     filepath.Base(filePath),
			TakenAt:      &takenAt,
			Width:        1200,
			Height:       1600,
			AIAnalyzed:   true,
			MemoryScore:  85 - index,
			BeautyScore:  88 - index,
			OverallScore: 87 - index,
		}
		require.NoError(t, photoRepo.Create(photo))
	}
}

func TestDisplayService_GenerateDailyBatchAvoidsRecentlyDisplayedPhotos(t *testing.T) {
	db := setupDisplayServiceTestDB(t)
	defer closeDisplayServiceTestDB(db)

	tempDir := t.TempDir()
	displayService, photoRepo, deviceRepo, configService := buildTestDisplayService(t, db, tempDir)
	targetDate := time.Now()
	createBatchPhotos(t, photoRepo, tempDir, targetDate, 4)
	setDisplayStrategy(t, configService, model.DisplayStrategyConfig{Algorithm: "random", MinBeautyScore: 60, MinMemoryScore: 60, DailyCount: 3})

	device := &model.Device{DeviceID: "DEV-HISTORY", Name: "History Device", APIKey: "key-history", DeviceType: model.DeviceTypeEmbedded, RenderProfile: "gdem075f52_480x800_4color", IsEnabled: true}
	require.NoError(t, deviceRepo.Create(device))

	allPhotos, err := photoRepo.ListAll()
	require.NoError(t, err)
	require.Len(t, allPhotos, 4)

	recentRecord := &model.DisplayRecord{
		PhotoID:     allPhotos[0].ID,
		DeviceID:    device.ID,
		DisplayedAt: time.Now().AddDate(0, 0, -1),
		TriggerType: model.TriggerTypeScheduled,
	}
	require.NoError(t, db.Create(recentRecord).Error)

	batch, err := displayService.GenerateDailyBatch(targetDate, true)
	require.NoError(t, err)
	require.Len(t, batch.Items, 3)

	for _, item := range batch.Items {
		assert.NotEqual(t, allPhotos[0].ID, item.PhotoID, "recently displayed photo should be excluded from generated batch")
	}
}

func TestCurationPeopleSpotlightUsesRealPeopleData(t *testing.T) {
	db := setupDisplayServiceTestDB(t)
	defer closeDisplayServiceTestDB(db)

	tempDir := t.TempDir()
	svcIface, photoRepo, _, _ := buildTestDisplayService(t, db, tempDir)
	svc := svcIface.(*displayService)

	realPath := filepath.Join(tempDir, "photos", "real-people.jpg")
	heuristicPath := filepath.Join(tempDir, "photos", "heuristic-people.jpg")
	require.NoError(t, os.MkdirAll(filepath.Dir(realPath), 0o755))
	require.NoError(t, os.WriteFile(realPath, []byte("real"), 0o644))
	require.NoError(t, os.WriteFile(heuristicPath, []byte("heuristic"), 0o644))

	takenAt := time.Date(2025, 4, 2, 10, 0, 0, 0, time.Local)
	realPhoto := &model.Photo{
		FilePath:          realPath,
		FileName:          filepath.Base(realPath),
		FileSize:          1024,
		FileHash:          "real-people",
		TakenAt:           &takenAt,
		Width:             1200,
		Height:            1600,
		Status:            model.PhotoStatusActive,
		AIAnalyzed:        true,
		MemoryScore:       86,
		BeautyScore:       80,
		OverallScore:      84,
		TopPersonCategory: model.PersonCategoryFamily,
	}
	heuristicPhoto := &model.Photo{
		FilePath:          heuristicPath,
		FileName:          filepath.Base(heuristicPath),
		FileSize:          1024,
		FileHash:          "heuristic-people",
		TakenAt:           &takenAt,
		Width:             1200,
		Height:            1600,
		Status:            model.PhotoStatusActive,
		AIAnalyzed:        true,
		MemoryScore:       96,
		BeautyScore:       94,
		OverallScore:      95,
		TopPersonCategory: "",
	}
	require.NoError(t, photoRepo.Create(realPhoto))
	require.NoError(t, photoRepo.Create(heuristicPhoto))

	realEvent := &model.Event{
		StartTime:       takenAt,
		EndTime:         takenAt.Add(2 * time.Hour),
		DurationHours:   2,
		PhotoCount:      1,
		CoverPhotoID:    &realPhoto.ID,
		PrimaryCategory: "travel",
		PrimaryTag:      "旅行",
		EventScore:      70,
	}
	heuristicEvent := &model.Event{
		StartTime:       takenAt,
		EndTime:         takenAt.Add(2 * time.Hour),
		DurationHours:   2,
		PhotoCount:      1,
		CoverPhotoID:    &heuristicPhoto.ID,
		PrimaryCategory: "portrait",
		PrimaryTag:      "people",
		EventScore:      95,
	}
	require.NoError(t, db.Create(realEvent).Error)
	require.NoError(t, db.Create(heuristicEvent).Error)
	require.NoError(t, photoRepo.UpdateFields(realPhoto.ID, map[string]interface{}{"event_id": realEvent.ID}))
	require.NoError(t, photoRepo.UpdateFields(heuristicPhoto.ID, map[string]interface{}{"event_id": heuristicEvent.ID}))

	cfg := defaultDisplayStrategyConfig()
	cfg.CurationPeopleEventsLimit = 5

	candidates, err := svc.nominatePeopleSpotlight(map[uint]bool{}, nil, cfg)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Equal(t, uint(realPhoto.ID), candidates[0].photo.ID, "real people-backed event should be nominated before tag-only heuristic event")
	require.Equal(t, uint(heuristicPhoto.ID), candidates[1].photo.ID)
}
