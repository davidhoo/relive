package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/mlclient"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type fakePeopleMLClient struct {
	responses map[string]*mlclient.DetectFacesResponse
	err       error
}

func (c *fakePeopleMLClient) DetectFaces(ctx context.Context, req mlclient.DetectFacesRequest) (*mlclient.DetectFacesResponse, error) {
	if c.err != nil {
		return nil, c.err
	}
	if resp, ok := c.responses[req.ImagePath]; ok {
		return resp, nil
	}
	return &mlclient.DetectFacesResponse{}, nil
}

func setupPeopleServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	require.NoError(t, db.AutoMigrate(
		&model.AppConfig{},
		&model.Photo{},
		&model.PhotoTag{},
		&model.Face{},
		&model.Person{},
		&model.PeopleJob{},
		&model.ScanJob{},
	))

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})

	return db
}

func newPeopleServiceForTest(t *testing.T, client PeopleMLClient) (*peopleService, *gorm.DB) {
	t.Helper()

	db := setupPeopleServiceTestDB(t)
	cfg := &config.Config{
		People: config.PeopleConfig{
			MLEndpoint: "http://ml-service",
			Timeout:    5,
		},
	}

	svc := NewPeopleService(
		db,
		repository.NewPhotoRepository(db),
		repository.NewFaceRepository(db),
		repository.NewPersonRepository(db),
		repository.NewPeopleJobRepository(db),
		cfg,
		client,
	).(*peopleService)

	return svc, db
}

func waitForPeopleCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestPeopleServiceBackground(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			"/photos/face.jpg": {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
						Confidence:   0.95,
						QualityScore: 0.88,
						Embedding:    []float32{0.1, 0.2, 0.3},
					},
				},
				ProcessingTimeMS: 8,
			},
		},
	})

	photoRepo := repository.NewPhotoRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photo := &model.Photo{
		FilePath: "/photos/face.jpg",
		FileName: "face.jpg",
		FileSize: 1,
		FileHash: "hash-face",
		Width:    100,
		Height:   100,
		Status:   model.PhotoStatusActive,
	}
	require.NoError(t, photoRepo.Create(photo))
	require.NoError(t, jobRepo.Create(&model.PeopleJob{
		PhotoID:  photo.ID,
		FilePath: photo.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}))

	task, err := svc.StartBackground()
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, model.TaskStatusRunning, task.Status)

	waitForPeopleCondition(t, 3*time.Second, func() bool {
		updated, err := photoRepo.GetByID(photo.ID)
		require.NoError(t, err)
		return updated.FaceProcessStatus == model.FaceProcessStatusReady && updated.FaceCount == 1
	})

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)

	stats, err := svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.Total)
	assert.Equal(t, int64(1), stats.Completed)

	assert.NotEmpty(t, svc.GetBackgroundLogs())
	require.NotNil(t, svc.GetTaskStatus())

	require.NoError(t, svc.StopBackground())
	waitForPeopleCondition(t, 3*time.Second, func() bool {
		task := svc.GetTaskStatus()
		return task != nil && task.Status == model.TaskStatusStopped
	})
}

func TestPhotoScanStartsPeopleBackground(t *testing.T) {
	rootDir := t.TempDir()
	activePath := filepath.Join(rootDir, "active.jpg")
	excludedPath := filepath.Join(rootDir, "excluded.jpg")

	require.NoError(t, os.WriteFile(activePath, []byte("active"), 0o644))
	require.NoError(t, os.WriteFile(excludedPath, []byte("excluded"), 0o644))

	db := setupPeopleServiceTestDB(t)
	configRepo := repository.NewConfigRepository(db)
	configService := NewConfigService(configRepo)
	photoRepo := repository.NewPhotoRepository(db)
	scanJobRepo := repository.NewScanJobRepository(db)
	peopleJobRepo := repository.NewPeopleJobRepository(db)

	cfg := &config.Config{}
	cfg.Photos.RootPath = rootDir
	cfg.Photos.SupportedFormats = []string{".jpg"}
	cfg.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")
	cfg.Performance.MaxScanWorkers = 1
	cfg.People.MLEndpoint = "http://ml-service"
	cfg.People.Timeout = 5

	photoSvc := NewPhotoService(photoRepo, repository.NewPhotoTagRepository(db), scanJobRepo, cfg, configService, nil, nil, nil).(*photoService)
	peopleSvc := NewPeopleService(
		db,
		photoRepo,
		repository.NewFaceRepository(db),
		repository.NewPersonRepository(db),
		peopleJobRepo,
		cfg,
		&fakePeopleMLClient{
			responses: map[string]*mlclient.DetectFacesResponse{
				activePath: {Faces: nil, ProcessingTimeMS: 3},
			},
		},
	).(*peopleService)
	photoSvc.SetPeopleService(peopleSvc)

	excludedInfo, err := os.Stat(excludedPath)
	require.NoError(t, err)
	excludedPhoto := &model.Photo{
		FilePath:          excludedPath,
		FileName:          filepath.Base(excludedPath),
		FileSize:          excludedInfo.Size(),
		FileHash:          "excluded-hash",
		Width:             100,
		Height:            100,
		Status:            model.PhotoStatusExcluded,
		FileModTime:       ptrTime(excludedInfo.ModTime()),
		FaceProcessStatus: model.FaceProcessStatusNone,
	}
	require.NoError(t, photoRepo.Create(excludedPhoto))

	_, err = photoSvc.StartScan(rootDir)
	require.NoError(t, err)
	waitForTaskStatus(t, photoSvc, map[string]bool{model.ScanJobStatusCompleted: true}, 3*time.Second)

	waitForPeopleCondition(t, 3*time.Second, func() bool {
		task := peopleSvc.GetTaskStatus()
		stats, statsErr := peopleSvc.GetStats()
		require.NoError(t, statsErr)
		return task != nil && task.Status == model.TaskStatusRunning && stats.Total == 1 && stats.Completed == 1
	})

	activePhoto, err := photoRepo.GetByFilePath(activePath)
	require.NoError(t, err)
	require.NotNil(t, activePhoto)
	assert.Equal(t, model.FaceProcessStatusNoFace, activePhoto.FaceProcessStatus)

	excludedAfter, err := photoRepo.GetByID(excludedPhoto.ID)
	require.NoError(t, err)
	require.NotNil(t, excludedAfter)
	assert.Equal(t, model.FaceProcessStatusNone, excludedAfter.FaceProcessStatus)

	stats, err := peopleSvc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.Total)

	require.NoError(t, peopleSvc.StopBackground())
	waitForPeopleCondition(t, 3*time.Second, func() bool {
		task := peopleSvc.GetTaskStatus()
		return task != nil && task.Status == model.TaskStatusStopped
	})
}

func TestPeopleServiceMarksNoFaceReady(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			"/photos/empty.jpg": {Faces: nil, ProcessingTimeMS: 2},
		},
	})

	photoRepo := repository.NewPhotoRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photo := &model.Photo{
		FilePath: "/photos/empty.jpg",
		FileName: "empty.jpg",
		FileSize: 1,
		FileHash: "hash-empty",
		Width:    100,
		Height:   100,
		Status:   model.PhotoStatusActive,
	}
	require.NoError(t, photoRepo.Create(photo))
	require.NoError(t, jobRepo.Create(&model.PeopleJob{
		PhotoID:  photo.ID,
		FilePath: photo.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}))

	_, err := svc.StartBackground()
	require.NoError(t, err)

	waitForPeopleCondition(t, 3*time.Second, func() bool {
		updated, getErr := photoRepo.GetByID(photo.ID)
		require.NoError(t, getErr)
		return updated.FaceProcessStatus == model.FaceProcessStatusNoFace
	})

	updated, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, model.FaceProcessStatusNoFace, updated.FaceProcessStatus)
	assert.Equal(t, 0, updated.FaceCount)

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	assert.Empty(t, faces)

	stats, err := svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.Completed)
	assert.Equal(t, int64(0), stats.Pending+stats.Queued+stats.Processing)

	require.NoError(t, svc.StopBackground())
}
