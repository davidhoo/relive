package service

import (
	"context"
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/mlclient"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/disintegration/imaging"
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

func encodeEmbedding(t *testing.T, embedding []float32) []byte {
	t.Helper()
	payload, err := json.Marshal(embedding)
	require.NoError(t, err)
	return payload
}

func createTestImageFile(t *testing.T, dir string, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, imaging.Save(imaging.New(320, 320, color.NRGBA{R: 180, G: 120, B: 90, A: 255}), path))
	return path
}

func TestPeopleServiceBackground(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "face.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			photoPath: {
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
		FilePath: photoPath,
		FileName: filepath.Base(photoPath),
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

func TestPeopleServiceGeneratesFaceThumbnail(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := filepath.Join(rootDir, "face-source.jpg")
	require.NoError(t, imaging.Save(imaging.New(400, 400, color.NRGBA{R: 180, G: 120, B: 90, A: 255}), photoPath))

	db := setupPeopleServiceTestDB(t)
	cfg := &config.Config{
		People: config.PeopleConfig{
			MLEndpoint: "http://ml-service",
			Timeout:    5,
		},
		Photos: config.PhotosConfig{
			ThumbnailPath: filepath.Join(rootDir, ".thumbnails"),
		},
	}

	svc := NewPeopleService(
		db,
		repository.NewPhotoRepository(db),
		repository.NewFaceRepository(db),
		repository.NewPersonRepository(db),
		repository.NewPeopleJobRepository(db),
		cfg,
		&fakePeopleMLClient{
			responses: map[string]*mlclient.DetectFacesResponse{
				photoPath: {
					Faces: []mlclient.DetectedFace{
						{
							BBox:         mlclient.BoundingBox{X: 0.2, Y: 0.2, Width: 0.3, Height: 0.3},
							Confidence:   0.96,
							QualityScore: 0.9,
							Embedding:    []float32{0.1, 0.2, 0.3},
						},
					},
					ProcessingTimeMS: 4,
				},
			},
		},
	).(*peopleService)

	photoRepo := repository.NewPhotoRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photo := &model.Photo{
		FilePath: photoPath,
		FileName: filepath.Base(photoPath),
		FileSize: 1,
		FileHash: "face-source",
		Width:    400,
		Height:   400,
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
		return updated.FaceProcessStatus == model.FaceProcessStatusReady
	})

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	require.NotEmpty(t, faces[0].ThumbnailPath)
	require.FileExists(t, filepath.Join(cfg.Photos.ThumbnailPath, faces[0].ThumbnailPath))

	require.NoError(t, svc.StopBackground())
}

func TestPeopleServiceCluster(t *testing.T) {
	t.Run("高置信度并入已有人物", func(t *testing.T) {
		rootDir := t.TempDir()
		newPhotoPath := createTestImageFile(t, rootDir, "new.jpg")

		svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
			responses: map[string]*mlclient.DetectFacesResponse{
				newPhotoPath: {
					Faces: []mlclient.DetectedFace{
						{
							BBox:         mlclient.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
							Confidence:   0.99,
							QualityScore: 0.80,
							Embedding:    []float32{1, 0, 0},
						},
					},
					ProcessingTimeMS: 2,
				},
			},
		})

		photoRepo := repository.NewPhotoRepository(db)
		personRepo := repository.NewPersonRepository(db)
		faceRepo := repository.NewFaceRepository(db)
		jobRepo := repository.NewPeopleJobRepository(db)

		oldPhoto := &model.Photo{FilePath: filepath.Join(rootDir, "old.jpg"), FileName: "old.jpg", FileSize: 1, FileHash: "old", Width: 100, Height: 100, Status: model.PhotoStatusActive}
		newPhoto := &model.Photo{FilePath: newPhotoPath, FileName: filepath.Base(newPhotoPath), FileSize: 1, FileHash: "new", Width: 100, Height: 100, Status: model.PhotoStatusActive}
		require.NoError(t, photoRepo.Create(oldPhoto))
		require.NoError(t, photoRepo.Create(newPhoto))

		person := &model.Person{Category: model.PersonCategoryFamily}
		require.NoError(t, personRepo.Create(person))

		require.NoError(t, faceRepo.Create(&model.Face{
			PhotoID:      oldPhoto.ID,
			PersonID:     &person.ID,
			BBoxX:        0.1,
			BBoxY:        0.1,
			BBoxWidth:    0.2,
			BBoxHeight:   0.2,
			Confidence:   0.95,
			QualityScore: 0.70,
			Embedding:    encodeEmbedding(t, []float32{1, 0, 0}),
		}))
		require.NoError(t, personRepo.RefreshStats(person.ID))
		require.NoError(t, jobRepo.Create(&model.PeopleJob{
			PhotoID:  newPhoto.ID,
			FilePath: newPhoto.FilePath,
			Status:   model.PeopleJobStatusQueued,
			Source:   model.PeopleJobSourceScan,
			Priority: 10,
			QueuedAt: time.Now(),
		}))

		_, err := svc.StartBackground()
		require.NoError(t, err)

		waitForPeopleCondition(t, 3*time.Second, func() bool {
			updated, getErr := photoRepo.GetByID(newPhoto.ID)
			require.NoError(t, getErr)
			return updated.FaceProcessStatus == model.FaceProcessStatusReady
		})

		faces, err := faceRepo.ListByPhotoID(newPhoto.ID)
		require.NoError(t, err)
		require.Len(t, faces, 1)
		require.NotNil(t, faces[0].PersonID)
		assert.Equal(t, person.ID, *faces[0].PersonID)

		updatedPhoto, err := photoRepo.GetByID(newPhoto.ID)
		require.NoError(t, err)
		assert.Equal(t, model.PersonCategoryFamily, updatedPhoto.TopPersonCategory)

		people, err := personRepo.ListAll()
		require.NoError(t, err)
		assert.Len(t, people, 1)

		require.NoError(t, svc.StopBackground())
	})

	t.Run("边界样本新建人物", func(t *testing.T) {
		rootDir := t.TempDir()
		newPhotoPath := createTestImageFile(t, rootDir, "uncertain.jpg")

		svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
			responses: map[string]*mlclient.DetectFacesResponse{
				newPhotoPath: {
					Faces: []mlclient.DetectedFace{
						{
							BBox:         mlclient.BoundingBox{X: 0.2, Y: 0.2, Width: 0.2, Height: 0.2},
							Confidence:   0.93,
							QualityScore: 0.75,
							Embedding:    []float32{0, 1, 0},
						},
					},
					ProcessingTimeMS: 2,
				},
			},
		})

		photoRepo := repository.NewPhotoRepository(db)
		personRepo := repository.NewPersonRepository(db)
		faceRepo := repository.NewFaceRepository(db)
		jobRepo := repository.NewPeopleJobRepository(db)

		oldPhoto := &model.Photo{FilePath: filepath.Join(rootDir, "existing.jpg"), FileName: "existing.jpg", FileSize: 1, FileHash: "existing", Width: 100, Height: 100, Status: model.PhotoStatusActive}
		newPhoto := &model.Photo{FilePath: newPhotoPath, FileName: filepath.Base(newPhotoPath), FileSize: 1, FileHash: "uncertain", Width: 100, Height: 100, Status: model.PhotoStatusActive}
		require.NoError(t, photoRepo.Create(oldPhoto))
		require.NoError(t, photoRepo.Create(newPhoto))

		existingPerson := &model.Person{Category: model.PersonCategoryFriend}
		require.NoError(t, personRepo.Create(existingPerson))
		require.NoError(t, faceRepo.Create(&model.Face{
			PhotoID:      oldPhoto.ID,
			PersonID:     &existingPerson.ID,
			BBoxX:        0.1,
			BBoxY:        0.1,
			BBoxWidth:    0.2,
			BBoxHeight:   0.2,
			Confidence:   0.97,
			QualityScore: 0.8,
			Embedding:    encodeEmbedding(t, []float32{1, 0, 0}),
		}))
		require.NoError(t, personRepo.RefreshStats(existingPerson.ID))
		require.NoError(t, jobRepo.Create(&model.PeopleJob{
			PhotoID:  newPhoto.ID,
			FilePath: newPhoto.FilePath,
			Status:   model.PeopleJobStatusQueued,
			Source:   model.PeopleJobSourceScan,
			Priority: 10,
			QueuedAt: time.Now(),
		}))

		_, err := svc.StartBackground()
		require.NoError(t, err)

		waitForPeopleCondition(t, 3*time.Second, func() bool {
			updated, getErr := photoRepo.GetByID(newPhoto.ID)
			require.NoError(t, getErr)
			return updated.FaceProcessStatus == model.FaceProcessStatusReady
		})

		faces, err := faceRepo.ListByPhotoID(newPhoto.ID)
		require.NoError(t, err)
		require.Len(t, faces, 1)
		require.NotNil(t, faces[0].PersonID)
		assert.NotEqual(t, existingPerson.ID, *faces[0].PersonID)

		newPerson, err := personRepo.GetByID(*faces[0].PersonID)
		require.NoError(t, err)
		require.NotNil(t, newPerson)
		assert.Equal(t, model.PersonCategoryStranger, newPerson.Category)

		updatedPhoto, err := photoRepo.GetByID(newPhoto.ID)
		require.NoError(t, err)
		assert.Equal(t, model.PersonCategoryStranger, updatedPhoto.TopPersonCategory)

		require.NoError(t, svc.StopBackground())
	})
}

func TestPeopleServiceMerge(t *testing.T) {
	rootDir := t.TempDir()
	newPhotoPath := createTestImageFile(t, rootDir, "merged-new.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			newPhotoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.3, Y: 0.3, Width: 0.2, Height: 0.2},
						Confidence:   0.97,
						QualityScore: 0.84,
						Embedding:    []float32{0, 1, 0},
					},
				},
				ProcessingTimeMS: 2,
			},
		},
	})

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	targetPhoto := &model.Photo{FilePath: filepath.Join(rootDir, "target.jpg"), FileName: "target.jpg", FileSize: 1, FileHash: "target", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	sourcePhoto := &model.Photo{FilePath: filepath.Join(rootDir, "source.jpg"), FileName: "source.jpg", FileSize: 1, FileHash: "source", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	newPhoto := &model.Photo{FilePath: newPhotoPath, FileName: filepath.Base(newPhotoPath), FileSize: 1, FileHash: "merged-new", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	require.NoError(t, photoRepo.Create(sourcePhoto))
	require.NoError(t, photoRepo.Create(newPhoto))

	target := &model.Person{Category: model.PersonCategoryFamily}
	source := &model.Person{Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(source))

	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      targetPhoto.ID,
		PersonID:     &target.ID,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.96,
		QualityScore: 0.8,
		Embedding:    encodeEmbedding(t, []float32{1, 0, 0}),
	}))
	sourceFace := &model.Face{
		PhotoID:      sourcePhoto.ID,
		PersonID:     &source.ID,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.97,
		QualityScore: 0.82,
		Embedding:    encodeEmbedding(t, []float32{0, 1, 0}),
	}
	require.NoError(t, faceRepo.Create(sourceFace))
	require.NoError(t, personRepo.RefreshStats(target.ID))
	require.NoError(t, personRepo.RefreshStats(source.ID))

	require.NoError(t, svc.MergePeople(target.ID, []uint{source.ID}))

	mergedFace, err := faceRepo.GetByID(sourceFace.ID)
	require.NoError(t, err)
	require.NotNil(t, mergedFace)
	require.NotNil(t, mergedFace.PersonID)
	assert.Equal(t, target.ID, *mergedFace.PersonID)
	assert.True(t, mergedFace.ManualLocked)
	assert.Equal(t, "merge", mergedFace.ManualLockReason)

	missingSource, err := personRepo.GetByID(source.ID)
	require.NoError(t, err)
	assert.Nil(t, missingSource)

	require.NoError(t, jobRepo.Create(&model.PeopleJob{
		PhotoID:  newPhoto.ID,
		FilePath: newPhoto.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}))

	_, err = svc.StartBackground()
	require.NoError(t, err)

	waitForPeopleCondition(t, 3*time.Second, func() bool {
		updated, getErr := photoRepo.GetByID(newPhoto.ID)
		require.NoError(t, getErr)
		return updated.FaceProcessStatus == model.FaceProcessStatusReady
	})

	newFaces, err := faceRepo.ListByPhotoID(newPhoto.ID)
	require.NoError(t, err)
	require.Len(t, newFaces, 1)
	require.NotNil(t, newFaces[0].PersonID)
	assert.Equal(t, target.ID, *newFaces[0].PersonID)

	require.NoError(t, svc.StopBackground())
}

func TestPeopleServiceSplit(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photoA := &model.Photo{FilePath: "/photos/a.jpg", FileName: "a.jpg", FileSize: 1, FileHash: "a", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	photoB := &model.Photo{FilePath: "/photos/b.jpg", FileName: "b.jpg", FileSize: 1, FileHash: "b", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photoA))
	require.NoError(t, photoRepo.Create(photoB))

	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	faceA := &model.Face{
		PhotoID:      photoA.ID,
		PersonID:     &person.ID,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.9,
		QualityScore: 0.7,
		Embedding:    encodeEmbedding(t, []float32{1, 0, 0}),
	}
	faceB := &model.Face{
		PhotoID:      photoB.ID,
		PersonID:     &person.ID,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.92,
		QualityScore: 0.8,
		Embedding:    encodeEmbedding(t, []float32{0, 1, 0}),
	}
	require.NoError(t, faceRepo.Create(faceA))
	require.NoError(t, faceRepo.Create(faceB))
	require.NoError(t, personRepo.RefreshStats(person.ID))
	require.NoError(t, photoRepo.RecomputeTopPersonCategory([]uint{photoA.ID, photoB.ID}))

	newPerson, err := svc.SplitPerson([]uint{faceB.ID})
	require.NoError(t, err)
	require.NotNil(t, newPerson)
	assert.NotEqual(t, person.ID, newPerson.ID)
	assert.Equal(t, model.PersonCategoryFriend, newPerson.Category)

	updatedFaceB, err := faceRepo.GetByID(faceB.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedFaceB)
	require.NotNil(t, updatedFaceB.PersonID)
	assert.Equal(t, newPerson.ID, *updatedFaceB.PersonID)
	assert.True(t, updatedFaceB.ManualLocked)
	assert.Equal(t, "split", updatedFaceB.ManualLockReason)

	oldPerson, err := personRepo.GetByID(person.ID)
	require.NoError(t, err)
	require.NotNil(t, oldPerson)
	assert.Equal(t, 1, oldPerson.FaceCount)

	reloadedNewPerson, err := personRepo.GetByID(newPerson.ID)
	require.NoError(t, err)
	require.NotNil(t, reloadedNewPerson)
	assert.Equal(t, 1, reloadedNewPerson.FaceCount)
}

func TestPeopleServiceMoveFaces(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photo := &model.Photo{FilePath: "/photos/move.jpg", FileName: "move.jpg", FileSize: 1, FileHash: "move", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))

	source := &model.Person{Category: model.PersonCategoryStranger}
	target := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(source))
	require.NoError(t, personRepo.Create(target))

	face := &model.Face{
		PhotoID:      photo.ID,
		PersonID:     &source.ID,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.94,
		QualityScore: 0.8,
		Embedding:    encodeEmbedding(t, []float32{0, 1, 0}),
	}
	require.NoError(t, faceRepo.Create(face))
	require.NoError(t, personRepo.RefreshStats(source.ID))
	require.NoError(t, photoRepo.RecomputeTopPersonCategory([]uint{photo.ID}))

	require.NoError(t, svc.MoveFaces([]uint{face.ID}, target.ID))

	updatedFace, err := faceRepo.GetByID(face.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedFace)
	require.NotNil(t, updatedFace.PersonID)
	assert.Equal(t, target.ID, *updatedFace.PersonID)
	assert.True(t, updatedFace.ManualLocked)
	assert.Equal(t, "move", updatedFace.ManualLockReason)

	updatedPhoto, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	assert.Equal(t, model.PersonCategoryFamily, updatedPhoto.TopPersonCategory)
}

func TestPeopleServiceCategoryBackfillsPhotos(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photoA := &model.Photo{FilePath: "/photos/cat-a.jpg", FileName: "cat-a.jpg", FileSize: 1, FileHash: "cat-a", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	photoB := &model.Photo{FilePath: "/photos/cat-b.jpg", FileName: "cat-b.jpg", FileSize: 1, FileHash: "cat-b", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photoA))
	require.NoError(t, photoRepo.Create(photoB))

	person := &model.Person{Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(person))

	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      photoA.ID,
		PersonID:     &person.ID,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.95,
		QualityScore: 0.8,
		Embedding:    encodeEmbedding(t, []float32{1, 0, 0}),
	}))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      photoB.ID,
		PersonID:     &person.ID,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.95,
		QualityScore: 0.8,
		Embedding:    encodeEmbedding(t, []float32{1, 0, 0}),
	}))
	require.NoError(t, personRepo.RefreshStats(person.ID))
	require.NoError(t, photoRepo.RecomputeTopPersonCategory([]uint{photoA.ID, photoB.ID}))

	require.NoError(t, svc.UpdatePersonCategory(person.ID, model.PersonCategoryFamily))

	updatedA, err := photoRepo.GetByID(photoA.ID)
	require.NoError(t, err)
	updatedB, err := photoRepo.GetByID(photoB.ID)
	require.NoError(t, err)
	assert.Equal(t, model.PersonCategoryFamily, updatedA.TopPersonCategory)
	assert.Equal(t, model.PersonCategoryFamily, updatedB.TopPersonCategory)
}

func TestPeopleServiceManualAvatarWins(t *testing.T) {
	rootDir := t.TempDir()
	newPhotoPath := createTestImageFile(t, rootDir, "avatar-new.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			newPhotoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.3, Y: 0.3, Width: 0.2, Height: 0.2},
						Confidence:   0.99,
						QualityScore: 0.99,
						Embedding:    []float32{1, 0, 0},
					},
				},
				ProcessingTimeMS: 2,
			},
		},
	})

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	oldPhoto := &model.Photo{FilePath: filepath.Join(rootDir, "avatar-old.jpg"), FileName: "avatar-old.jpg", FileSize: 1, FileHash: "avatar-old", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	newPhoto := &model.Photo{FilePath: newPhotoPath, FileName: filepath.Base(newPhotoPath), FileSize: 1, FileHash: "avatar-new", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(oldPhoto))
	require.NoError(t, photoRepo.Create(newPhoto))

	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	oldFace := &model.Face{
		PhotoID:      oldPhoto.ID,
		PersonID:     &person.ID,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.96,
		QualityScore: 0.70,
		Embedding:    encodeEmbedding(t, []float32{1, 0, 0}),
	}
	require.NoError(t, faceRepo.Create(oldFace))
	require.NoError(t, personRepo.RefreshStats(person.ID))
	require.NoError(t, svc.UpdatePersonAvatar(person.ID, oldFace.ID))

	require.NoError(t, jobRepo.Create(&model.PeopleJob{
		PhotoID:  newPhoto.ID,
		FilePath: newPhoto.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}))

	_, err := svc.StartBackground()
	require.NoError(t, err)

	waitForPeopleCondition(t, 3*time.Second, func() bool {
		updated, getErr := photoRepo.GetByID(newPhoto.ID)
		require.NoError(t, getErr)
		return updated.FaceProcessStatus == model.FaceProcessStatusReady
	})

	updatedPerson, err := personRepo.GetByID(person.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedPerson)
	require.NotNil(t, updatedPerson.RepresentativeFaceID)
	assert.Equal(t, oldFace.ID, *updatedPerson.RepresentativeFaceID)
	assert.True(t, updatedPerson.AvatarLocked)

	require.NoError(t, svc.StopBackground())
}
