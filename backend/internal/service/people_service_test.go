package service

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
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
		repository.NewCannotLinkRepository(db),
		cfg,
		client,
		nil, // runtimeService not needed for these tests
	).(*peopleService)

	// Reset clustering task counter to ensure clustering runs on first job
	// This is needed because tests expect immediate clustering behavior
	svc.clusteringTaskCounter = peopleClusteringTaskInterval

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

func TestFaceClusterStatusFields(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	faceRepo := repository.NewFaceRepository(db)

	faceType := reflect.TypeOf(model.Face{})
	_, hasClusterStatus := faceType.FieldByName("ClusterStatus")
	_, hasClusterScore := faceType.FieldByName("ClusterScore")
	_, hasClusteredAt := faceType.FieldByName("ClusteredAt")

	assert.True(t, hasClusterStatus)
	assert.True(t, hasClusterScore)
	assert.True(t, hasClusteredAt)
	assert.True(t, db.Migrator().HasColumn(&model.Face{}, "cluster_status"))
	assert.True(t, db.Migrator().HasColumn(&model.Face{}, "cluster_score"))
	assert.True(t, db.Migrator().HasColumn(&model.Face{}, "clustered_at"))

	face := &model.Face{
		PhotoID:      1,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.95,
		QualityScore: 0.88,
	}
	require.NoError(t, faceRepo.Create(face))

	clusteredAt := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.Model(&model.Face{}).Where("id = ?", face.ID).Updates(map[string]interface{}{
		"cluster_status": "pending",
		"cluster_score":  0.91,
		"clustered_at":   clusteredAt,
	}).Error)

	var stored struct {
		ClusterStatus string
		ClusterScore  float64
		ClusteredAt   *time.Time
	}
	require.NoError(t, db.Table("faces").
		Select("cluster_status, cluster_score, clustered_at").
		Where("id = ?", face.ID).
		Scan(&stored).Error)

	assert.Equal(t, "pending", stored.ClusterStatus)
	assert.InDelta(t, 0.91, stored.ClusterScore, 0.0001)
	require.NotNil(t, stored.ClusteredAt)
	assert.WithinDuration(t, clusteredAt, *stored.ClusteredAt, time.Second)
}

func TestPeopleService_SelectPersonPrototypes(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type prototypeSelector interface {
		selectPersonPrototypes(faces []*model.Face, k int) map[uint][]*model.Face
	}

	selector, ok := any(svc).(prototypeSelector)
	require.True(t, ok)

	personOneID := uint(11)
	personTwoID := uint(22)
	faces := []*model.Face{
		{
			ID:           101,
			PersonID:     &personOneID,
			ManualLocked: false,
			QualityScore: 0.70,
			Confidence:   0.80,
		},
		{
			ID:           102,
			PersonID:     &personOneID,
			ManualLocked: true,
			QualityScore: 0.60,
			Confidence:   0.75,
		},
		{
			ID:           103,
			PersonID:     &personOneID,
			ManualLocked: false,
			QualityScore: 0.95,
			Confidence:   0.70,
		},
		{
			ID:           104,
			PersonID:     &personOneID,
			ManualLocked: false,
			QualityScore: 0.95,
			Confidence:   0.90,
		},
		{
			ID:           105,
			PersonID:     &personOneID,
			ManualLocked: false,
			QualityScore: 0.95,
			Confidence:   0.90,
		},
		{
			ID:           201,
			PersonID:     &personTwoID,
			ManualLocked: false,
			QualityScore: 0.88,
			Confidence:   0.82,
		},
		{
			ID:           202,
			PersonID:     &personTwoID,
			ManualLocked: false,
			QualityScore: 0.88,
			Confidence:   0.95,
		},
		{
			ID:           301,
			ManualLocked: true,
			QualityScore: 0.99,
			Confidence:   0.99,
		},
	}

	prototypes := selector.selectPersonPrototypes(faces, 3)
	require.Len(t, prototypes, 2)
	require.Len(t, prototypes[personOneID], 3)
	require.Len(t, prototypes[personTwoID], 2)

	assert.Equal(t, uint(102), prototypes[personOneID][0].ID) // manual-locked first
	assert.Equal(t, uint(103), prototypes[personOneID][1].ID) // diversity-selected (no embeddings, falls to quality order)
	assert.Equal(t, uint(104), prototypes[personOneID][2].ID)
	assert.Equal(t, uint(201), prototypes[personTwoID][0].ID) // same quality, lower ID first
	assert.Equal(t, uint(202), prototypes[personTwoID][1].ID)
}

func TestPeopleService_BuildFaceGraph(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type graphBuilder interface {
		buildFaceGraph(faces []*model.Face, linkThreshold float64) map[uint][]uint
	}

	builder, ok := any(svc).(graphBuilder)
	require.True(t, ok)

	// ArcFace cosine: same person ~0.4-0.7, different person ~0.0-0.3
	// Use 3D vectors for clear separation between groups
	faces := []*model.Face{
		{ID: 1, Embedding: encodeEmbedding(t, []float32{1, 0, 0})},
		{ID: 2, Embedding: encodeEmbedding(t, []float32{0.9, 0.1, 0})}, // cosine with 1 ≈ 0.99
		{ID: 3, Embedding: encodeEmbedding(t, []float32{0, 1, 0})},
		{ID: 4, Embedding: encodeEmbedding(t, []float32{0, 0.9, 0.1})}, // cosine with 3 ≈ 0.99
		{ID: 5, Embedding: encodeEmbedding(t, []float32{0, 0, 1})},     // orthogonal to both groups
	}

	graph := builder.buildFaceGraph(faces, 0.65) // defaultLinkThreshold
	require.Len(t, graph, 5)
	assert.Equal(t, []uint{2}, graph[1])
	assert.Equal(t, []uint{1}, graph[2])
	assert.Equal(t, []uint{4}, graph[3])
	assert.Equal(t, []uint{3}, graph[4])
	assert.Empty(t, graph[5])
}

func TestPeopleService_FindFaceComponents(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type graphExplorer interface {
		buildFaceGraph(faces []*model.Face, linkThreshold float64) map[uint][]uint
		findConnectedComponents(graph map[uint][]uint) [][]uint
	}

	explorer, ok := any(svc).(graphExplorer)
	require.True(t, ok)

	faces := []*model.Face{
		{ID: 1, Embedding: encodeEmbedding(t, []float32{1, 0, 0})},
		{ID: 2, Embedding: encodeEmbedding(t, []float32{0.9, 0.1, 0})},
		{ID: 3, Embedding: encodeEmbedding(t, []float32{0, 1, 0})},
		{ID: 4, Embedding: encodeEmbedding(t, []float32{0, 0.9, 0.1})},
		{ID: 5, Embedding: encodeEmbedding(t, []float32{0, 0, 1})},
	}

	graph := explorer.buildFaceGraph(faces, 0.65) // defaultLinkThreshold
	components := explorer.findConnectedComponents(graph)

	assert.Equal(t, []string{"1,2", "3,4", "5"}, normalizeFaceComponents(components))
}

func TestPeopleService_AttachComponentToExistingPerson(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type componentAttacher interface {
		scoreComponentAgainstPerson(component []*model.Face, prototypes []*model.Face) float64
		attachComponentToExistingPerson(component []*model.Face, prototypes map[uint][]*model.Face, attachThreshold float64) (uint, float64, bool)
	}

	attacher, ok := any(svc).(componentAttacher)
	require.True(t, ok)

	personOneID := uint(11)
	personTwoID := uint(22)
	prototypes := map[uint][]*model.Face{
		personOneID: {
			{ID: 101, PersonID: &personOneID, Embedding: encodeEmbedding(t, []float32{1, 0, 0})},
			{ID: 102, PersonID: &personOneID, Embedding: encodeEmbedding(t, []float32{0.97, 0.243, 0})},
		},
		personTwoID: {
			{ID: 201, PersonID: &personTwoID, Embedding: encodeEmbedding(t, []float32{0, 1, 0})},
		},
	}

	t.Run("component attaches when score clears threshold", func(t *testing.T) {
		component := []*model.Face{
			{ID: 1, Embedding: encodeEmbedding(t, []float32{1, 0, 0})},
			{ID: 2, Embedding: encodeEmbedding(t, []float32{0.92, 0.392, 0})},
		}

		score := attacher.scoreComponentAgainstPerson(component, prototypes[personOneID])
		assert.Greater(t, score, 0.70) // defaultAttachThreshold

		personID, attachScore, attached := attacher.attachComponentToExistingPerson(component, prototypes, 0.70) // defaultAttachThreshold
		assert.True(t, attached)
		assert.Equal(t, personOneID, personID)
		assert.InDelta(t, score, attachScore, 0.0001)
	})

	t.Run("component stays unattached below threshold", func(t *testing.T) {
		// {0, 0, 1} is orthogonal to both {1, 0, 0} and {0, 1, 0} — cosine = 0
		component := []*model.Face{
			{ID: 3, Embedding: encodeEmbedding(t, []float32{0, 0, 1})},
			{ID: 4, Embedding: encodeEmbedding(t, []float32{0.1, 0.1, 0.99})},
		}

		personOneScore := attacher.scoreComponentAgainstPerson(component, prototypes[personOneID])
		assert.Less(t, personOneScore, 0.70) // defaultAttachThreshold

		personID, attachScore, attached := attacher.attachComponentToExistingPerson(component, prototypes, 0.70) // defaultAttachThreshold
		assert.False(t, attached)
		assert.Zero(t, personID)
		assert.Less(t, attachScore, 0.70) // defaultAttachThreshold
	})
}

func TestPeopleService_PendingComponent(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	faceRepo := repository.NewFaceRepository(db)
	const minClusterFaces = 2

	type pendingMarker interface {
		markComponentPending(component []*model.Face, score float64) error
	}

	marker, ok := any(svc).(pendingMarker)
	require.True(t, ok)

	face := &model.Face{
		PhotoID:      1,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.91,
		QualityScore: 0.83,
		Embedding:    encodeEmbedding(t, []float32{0.7, 0.7}),
	}
	require.NoError(t, faceRepo.Create(face))

	component := []*model.Face{face}
	require.Less(t, len(component), minClusterFaces)
	require.NoError(t, marker.markComponentPending(component, 0.41))

	stored, err := faceRepo.GetByID(face.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Nil(t, stored.PersonID)
	assert.Equal(t, model.FaceClusterStatusPending, stored.ClusterStatus)
	assert.InDelta(t, 0.41, stored.ClusterScore, 0.0001)
	require.NotNil(t, stored.ClusteredAt)
}

func TestPeopleService_CreatePersonFromComponent(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	faceRepo := repository.NewFaceRepository(db)
	personRepo := repository.NewPersonRepository(db)
	const minClusterFaces = 2

	type personCreator interface {
		createPersonFromComponent(component []*model.Face, score float64) (*model.Person, error)
	}

	creator, ok := any(svc).(personCreator)
	require.True(t, ok)

	faceOne := &model.Face{
		PhotoID:      1,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.95,
		QualityScore: 0.88,
		Embedding:    encodeEmbedding(t, []float32{1, 0}),
	}
	faceTwo := &model.Face{
		PhotoID:      2,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.94,
		QualityScore: 0.90,
		Embedding:    encodeEmbedding(t, []float32{0.98, 0.2}),
	}
	require.NoError(t, faceRepo.Create(faceOne))
	require.NoError(t, faceRepo.Create(faceTwo))

	component := []*model.Face{faceOne, faceTwo}
	require.GreaterOrEqual(t, len(component), minClusterFaces)

	person, err := creator.createPersonFromComponent(component, 0.63)
	require.NoError(t, err)
	require.NotNil(t, person)
	assert.Equal(t, model.PersonCategoryStranger, person.Category)

	storedOne, err := faceRepo.GetByID(faceOne.ID)
	require.NoError(t, err)
	storedTwo, err := faceRepo.GetByID(faceTwo.ID)
	require.NoError(t, err)
	require.NotNil(t, storedOne.PersonID)
	require.NotNil(t, storedTwo.PersonID)
	assert.Equal(t, person.ID, *storedOne.PersonID)
	assert.Equal(t, person.ID, *storedTwo.PersonID)
	assert.Equal(t, model.FaceClusterStatusAssigned, storedOne.ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusAssigned, storedTwo.ClusterStatus)
	assert.InDelta(t, 0.63, storedOne.ClusterScore, 0.0001)
	assert.InDelta(t, 0.63, storedTwo.ClusterScore, 0.0001)
	require.NotNil(t, storedOne.ClusteredAt)
	require.NotNil(t, storedTwo.ClusteredAt)

	storedPerson, err := personRepo.GetByID(person.ID)
	require.NoError(t, err)
	require.NotNil(t, storedPerson)
	assert.Equal(t, 2, storedPerson.FaceCount)
	assert.Equal(t, 2, storedPerson.PhotoCount)
	require.NotNil(t, storedPerson.RepresentativeFaceID)
	assert.Equal(t, faceTwo.ID, *storedPerson.RepresentativeFaceID)
}

func TestPeopleService_ComponentPhotoCount(t *testing.T) {
	t.Run("same photo counted once", func(t *testing.T) {
		component := []*model.Face{
			{ID: 1, PhotoID: 101},
			{ID: 2, PhotoID: 101},
			{ID: 3, PhotoID: 101},
			nil,
		}
		assert.Equal(t, 1, componentPhotoCount(component))
	})

	t.Run("cross photo counted distinctly", func(t *testing.T) {
		component := []*model.Face{
			{ID: 4, PhotoID: 101},
			{ID: 5, PhotoID: 102},
			{ID: 6, PhotoID: 101},
			{ID: 7, PhotoID: 102},
			{ID: 8, PhotoID: 0},
			nil,
		}
		assert.Equal(t, 2, componentPhotoCount(component))
	})
}

func TestPeopleService_ProcessJobUsesIncrementalClustering(t *testing.T) {
	rootDir := t.TempDir()
	oldPhotoPath := createTestImageFile(t, rootDir, "old.jpg")
	newPhotoPath := createTestImageFile(t, rootDir, "new.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			newPhotoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
						Confidence:   0.98,
						QualityScore: 0.89,
						Embedding:    []float32{1, 0},
					},
				},
			},
		},
	})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	oldPhoto := &model.Photo{FilePath: oldPhotoPath, FileName: "old.jpg", FileSize: 1, FileHash: "old-process-job", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	newPhoto := &model.Photo{FilePath: newPhotoPath, FileName: "new.jpg", FileSize: 1, FileHash: "new-process-job", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(oldPhoto))
	require.NoError(t, photoRepo.Create(newPhoto))

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       oldPhoto.ID,
		PersonID:      &person.ID,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.96,
		QualityScore:  0.84,
		Embedding:     encodeEmbedding(t, []float32{1, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  1,
	}))
	require.NoError(t, personRepo.RefreshStats(person.ID))
	require.NoError(t, svc.syncPersonState(person.ID))

	job := &model.PeopleJob{
		PhotoID:  newPhoto.ID,
		FilePath: newPhoto.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	require.NoError(t, svc.processJob(job))

	faces, err := faceRepo.ListByPhotoID(newPhoto.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	require.NotNil(t, faces[0].PersonID)
	assert.Equal(t, person.ID, *faces[0].PersonID)
	assert.Equal(t, model.FaceClusterStatusAssigned, faces[0].ClusterStatus)
	assert.GreaterOrEqual(t, faces[0].ClusterScore, 0.70) // defaultAttachThreshold
	require.NotNil(t, faces[0].ClusteredAt)

	updatedPhoto, err := photoRepo.GetByID(newPhoto.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedPhoto)
	assert.Equal(t, model.FaceProcessStatusReady, updatedPhoto.FaceProcessStatus)
	assert.Equal(t, model.PersonCategoryFamily, updatedPhoto.TopPersonCategory)

	updatedJob, err := jobRepo.GetByID(job.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedJob)
	assert.Equal(t, model.PeopleJobStatusCompleted, updatedJob.Status)

	people, err := personRepo.ListAll()
	require.NoError(t, err)
	assert.Len(t, people, 1)
}

func TestPeopleService_SingleUncertainFaceStaysPending(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "uncertain.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			photoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.2, Y: 0.2, Width: 0.2, Height: 0.2},
						Confidence:   0.92,
						QualityScore: 0.78,
						Embedding:    []float32{0, 1},
					},
				},
			},
		},
	})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	photo := &model.Photo{FilePath: photoPath, FileName: "uncertain.jpg", FileSize: 1, FileHash: "uncertain-process-job", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))

	job := &model.PeopleJob{
		PhotoID:  photo.ID,
		FilePath: photo.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	require.NoError(t, svc.processJob(job))

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Nil(t, faces[0].PersonID)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus)
	assert.Less(t, faces[0].ClusterScore, 0.70) // defaultAttachThreshold
	require.NotNil(t, faces[0].ClusteredAt)

	updatedPhoto, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedPhoto)
	assert.Equal(t, model.FaceProcessStatusReady, updatedPhoto.FaceProcessStatus)
	assert.Equal(t, "", updatedPhoto.TopPersonCategory)

	updatedJob, err := jobRepo.GetByID(job.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedJob)
	assert.Equal(t, model.PeopleJobStatusCompleted, updatedJob.Status)

	people, err := personRepo.ListAll()
	require.NoError(t, err)
	assert.Empty(t, people)
}

func TestPeopleService_ManualLockedFacesAreStable(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "manual.jpg")
	rivalPhotoPath := createTestImageFile(t, rootDir, "rival.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			photoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.3, Y: 0.3, Width: 0.2, Height: 0.2},
						Confidence:   0.97,
						QualityScore: 0.82,
						Embedding:    []float32{0, 1},
					},
				},
			},
		},
	})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	photo := &model.Photo{FilePath: photoPath, FileName: "manual.jpg", FileSize: 1, FileHash: "manual-locked", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	rivalPhoto := &model.Photo{FilePath: rivalPhotoPath, FileName: "rival.jpg", FileSize: 1, FileHash: "manual-rival", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	require.NoError(t, photoRepo.Create(rivalPhoto))

	source := &model.Person{Category: model.PersonCategoryStranger}
	target := &model.Person{Category: model.PersonCategoryFamily}
	rival := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(source))
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(rival))

	face := &model.Face{
		PhotoID:      photo.ID,
		PersonID:     &source.ID,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.95,
		QualityScore: 0.80,
		Embedding:    encodeEmbedding(t, []float32{1, 0}),
	}
	require.NoError(t, faceRepo.Create(face))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       rivalPhoto.ID,
		PersonID:      &rival.ID,
		BBoxX:         0.2,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.96,
		QualityScore:  0.81,
		Embedding:     encodeEmbedding(t, []float32{0, 1}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  1,
	}))
	require.NoError(t, personRepo.RefreshStats(source.ID))
	require.NoError(t, personRepo.RefreshStats(rival.ID))
	_, err := svc.MoveFaces([]uint{face.ID}, target.ID)
	require.NoError(t, err)

	movedFace, err := faceRepo.GetByID(face.ID)
	require.NoError(t, err)
	require.NotNil(t, movedFace)
	require.NotNil(t, movedFace.PersonID)
	assert.Equal(t, target.ID, *movedFace.PersonID)
	assert.True(t, movedFace.ManualLocked)
	assert.Equal(t, model.FaceClusterStatusManual, movedFace.ClusterStatus)

	job := &model.PeopleJob{
		PhotoID:  photo.ID,
		FilePath: photo.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	require.NoError(t, svc.processJob(job))

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, movedFace.ID, faces[0].ID)
	require.NotNil(t, faces[0].PersonID)
	assert.Equal(t, target.ID, *faces[0].PersonID)
	assert.True(t, faces[0].ManualLocked)
	assert.Equal(t, model.FaceClusterStatusManual, faces[0].ClusterStatus)
}

func TestPeopleService_PrototypeRefreshAfterManualOps(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	targetPhoto := &model.Photo{FilePath: "/photos/manual-target.jpg", FileName: "manual-target.jpg", FileSize: 1, FileHash: "manual-target", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	sourcePhoto := &model.Photo{FilePath: "/photos/manual-source.jpg", FileName: "manual-source.jpg", FileSize: 1, FileHash: "manual-source", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	require.NoError(t, photoRepo.Create(sourcePhoto))

	target := &model.Person{Category: model.PersonCategoryFamily}
	source := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(source))

	targetFace := &model.Face{
		PhotoID:       targetPhoto.ID,
		PersonID:      &target.ID,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.90,
		QualityScore:  0.70,
		Embedding:     encodeEmbedding(t, []float32{1, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  1,
	}
	mergedFace := &model.Face{
		PhotoID:      sourcePhoto.ID,
		PersonID:     &source.ID,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.96,
		QualityScore: 0.95,
		Embedding:    encodeEmbedding(t, []float32{0, 1}),
	}
	require.NoError(t, faceRepo.Create(targetFace))
	require.NoError(t, faceRepo.Create(mergedFace))
	require.NoError(t, personRepo.RefreshStats(target.ID))
	require.NoError(t, personRepo.RefreshStats(source.ID))
	require.NoError(t, svc.syncPersonState(target.ID))
	require.NoError(t, svc.syncPersonState(source.ID))

	_, err := svc.MergePeople(target.ID, []uint{source.ID})
	require.NoError(t, err)

	updatedMergedFace, err := faceRepo.GetByID(mergedFace.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedMergedFace)
	require.NotNil(t, updatedMergedFace.PersonID)
	assert.Equal(t, target.ID, *updatedMergedFace.PersonID)
	assert.True(t, updatedMergedFace.ManualLocked)
	assert.Equal(t, model.FaceClusterStatusManual, updatedMergedFace.ClusterStatus)

	updatedTarget, err := personRepo.GetByID(target.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedTarget)
	require.NotNil(t, updatedTarget.RepresentativeFaceID)
	assert.Equal(t, mergedFace.ID, *updatedTarget.RepresentativeFaceID)

	targetFaces, err := faceRepo.ListByPersonID(target.ID)
	require.NoError(t, err)
	prototypes := svc.selectPersonPrototypes(targetFaces, peoplePrototypeCount)
	require.Len(t, prototypes[target.ID], 2)
	assert.Equal(t, mergedFace.ID, prototypes[target.ID][0].ID)
}

func TestPeopleService_TwoSimilarSamePhotoFacesStayPending(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "pair.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			photoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
						Confidence:   0.95,
						QualityScore: 0.87,
						Embedding:    []float32{1, 0},
					},
					{
						BBox:         mlclient.BoundingBox{X: 0.4, Y: 0.1, Width: 0.2, Height: 0.2},
						Confidence:   0.94,
						QualityScore: 0.85,
						Embedding:    []float32{0.97, 0.243},
					},
				},
			},
		},
	})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	photo := &model.Photo{FilePath: photoPath, FileName: "pair.jpg", FileSize: 1, FileHash: "pair-regression", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	job := &model.PeopleJob{
		PhotoID:  photo.ID,
		FilePath: photo.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	require.NoError(t, svc.processJob(job))

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 2)
	assert.Nil(t, faces[0].PersonID)
	assert.Nil(t, faces[1].PersonID)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusPending, faces[1].ClusterStatus)

	people, err := personRepo.ListAll()
	require.NoError(t, err)
	assert.Empty(t, people)

	updatedPhoto, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedPhoto)
	assert.Equal(t, "", updatedPhoto.TopPersonCategory)
}

func TestPeopleService_PendingFacesBecomeAssignedWhenMoreEvidenceArrives(t *testing.T) {
	rootDir := t.TempDir()
	firstPhotoPath := createTestImageFile(t, rootDir, "first.jpg")
	secondPhotoPath := createTestImageFile(t, rootDir, "second.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			firstPhotoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
						Confidence:   0.93,
						QualityScore: 0.81,
						Embedding:    []float32{1, 0},
					},
				},
			},
			secondPhotoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.2, Y: 0.2, Width: 0.2, Height: 0.2},
						Confidence:   0.94,
						QualityScore: 0.82,
						Embedding:    []float32{0.97, 0.243},
					},
				},
			},
		},
	})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	firstPhoto := &model.Photo{FilePath: firstPhotoPath, FileName: "first.jpg", FileSize: 1, FileHash: "pending-first", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	secondPhoto := &model.Photo{FilePath: secondPhotoPath, FileName: "second.jpg", FileSize: 1, FileHash: "pending-second", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(firstPhoto))
	require.NoError(t, photoRepo.Create(secondPhoto))

	firstJob := &model.PeopleJob{
		PhotoID:  firstPhoto.ID,
		FilePath: firstPhoto.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}
	secondJob := &model.PeopleJob{
		PhotoID:  secondPhoto.ID,
		FilePath: secondPhoto.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(firstJob))
	require.NoError(t, jobRepo.Create(secondJob))

	require.NoError(t, svc.processJob(firstJob))

	firstFaces, err := faceRepo.ListByPhotoID(firstPhoto.ID)
	require.NoError(t, err)
	require.Len(t, firstFaces, 1)
	assert.Nil(t, firstFaces[0].PersonID)
	assert.Equal(t, model.FaceClusterStatusPending, firstFaces[0].ClusterStatus)

	// Reset clustering counter to ensure second job also triggers clustering
	// This is needed because the test expects faces to be linked across jobs
	svc.clusteringTaskCounter = peopleClusteringTaskInterval

	require.NoError(t, svc.processJob(secondJob))

	firstFaces, err = faceRepo.ListByPhotoID(firstPhoto.ID)
	require.NoError(t, err)
	secondFaces, err := faceRepo.ListByPhotoID(secondPhoto.ID)
	require.NoError(t, err)
	require.Len(t, firstFaces, 1)
	require.Len(t, secondFaces, 1)
	require.NotNil(t, firstFaces[0].PersonID)
	require.NotNil(t, secondFaces[0].PersonID)
	assert.Equal(t, *firstFaces[0].PersonID, *secondFaces[0].PersonID)
	assert.Equal(t, model.FaceClusterStatusAssigned, firstFaces[0].ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusAssigned, secondFaces[0].ClusterStatus)

	people, err := personRepo.ListAll()
	require.NoError(t, err)
	require.Len(t, people, 1)

	updatedFirstPhoto, err := photoRepo.GetByID(firstPhoto.ID)
	require.NoError(t, err)
	updatedSecondPhoto, err := photoRepo.GetByID(secondPhoto.ID)
	require.NoError(t, err)
	assert.Equal(t, model.PersonCategoryStranger, updatedFirstPhoto.TopPersonCategory)
	assert.Equal(t, model.PersonCategoryStranger, updatedSecondPhoto.TopPersonCategory)
}

func TestPeopleService_SamePhotoComponentStaysPending(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "same-photo-pending.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			photoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
						Confidence:   0.95,
						QualityScore: 0.87,
						Embedding:    []float32{1, 0},
					},
					{
						BBox:         mlclient.BoundingBox{X: 0.45, Y: 0.1, Width: 0.2, Height: 0.2},
						Confidence:   0.94,
						QualityScore: 0.85,
						Embedding:    []float32{0.97, 0.243},
					},
				},
			},
		},
	})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	photo := &model.Photo{FilePath: photoPath, FileName: "same-photo-pending.jpg", FileSize: 1, FileHash: "same-photo-pending", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	job := &model.PeopleJob{
		PhotoID:  photo.ID,
		FilePath: photo.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	require.NoError(t, svc.processJob(job))

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 2)
	assert.Nil(t, faces[0].PersonID)
	assert.Nil(t, faces[1].PersonID)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusPending, faces[1].ClusterStatus)

	people, err := personRepo.ListAll()
	require.NoError(t, err)
	assert.Empty(t, people)

	updatedPhoto, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedPhoto)
	assert.Equal(t, "", updatedPhoto.TopPersonCategory)
}

func TestPeopleService_SamePhotoComponentCanStillAttach(t *testing.T) {
	rootDir := t.TempDir()
	oldPhotoPath := createTestImageFile(t, rootDir, "same-photo-attach-old.jpg")
	newPhotoPath := createTestImageFile(t, rootDir, "same-photo-attach-new.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			newPhotoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
						Confidence:   0.98,
						QualityScore: 0.88,
						Embedding:    []float32{1, 0},
					},
					{
						BBox:         mlclient.BoundingBox{X: 0.45, Y: 0.1, Width: 0.2, Height: 0.2},
						Confidence:   0.97,
						QualityScore: 0.87,
						Embedding:    []float32{0.97, 0.243},
					},
				},
			},
		},
	})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	oldPhoto := &model.Photo{FilePath: oldPhotoPath, FileName: "same-photo-attach-old.jpg", FileSize: 1, FileHash: "same-photo-attach-old", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	newPhoto := &model.Photo{FilePath: newPhotoPath, FileName: "same-photo-attach-new.jpg", FileSize: 1, FileHash: "same-photo-attach-new", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(oldPhoto))
	require.NoError(t, photoRepo.Create(newPhoto))

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       oldPhoto.ID,
		PersonID:      &person.ID,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.96,
		QualityScore:  0.84,
		Embedding:     encodeEmbedding(t, []float32{1, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  1,
	}))
	require.NoError(t, personRepo.RefreshStats(person.ID))
	require.NoError(t, svc.syncPersonState(person.ID))

	job := &model.PeopleJob{
		PhotoID:  newPhoto.ID,
		FilePath: newPhoto.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	require.NoError(t, svc.processJob(job))

	faces, err := faceRepo.ListByPhotoID(newPhoto.ID)
	require.NoError(t, err)
	require.Len(t, faces, 2)
	require.NotNil(t, faces[0].PersonID)
	require.NotNil(t, faces[1].PersonID)
	assert.Equal(t, person.ID, *faces[0].PersonID)
	assert.Equal(t, person.ID, *faces[1].PersonID)
	assert.Equal(t, model.FaceClusterStatusAssigned, faces[0].ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusAssigned, faces[1].ClusterStatus)
}

func TestPeopleService_CrossPhotoComponentCreatesPerson(t *testing.T) {
	rootDir := t.TempDir()
	firstPhotoPath := createTestImageFile(t, rootDir, "cross-photo-first.jpg")
	secondPhotoPath := createTestImageFile(t, rootDir, "cross-photo-second.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
		responses: map[string]*mlclient.DetectFacesResponse{
			firstPhotoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
						Confidence:   0.93,
						QualityScore: 0.81,
						Embedding:    []float32{1, 0},
					},
				},
			},
			secondPhotoPath: {
				Faces: []mlclient.DetectedFace{
					{
						BBox:         mlclient.BoundingBox{X: 0.2, Y: 0.2, Width: 0.2, Height: 0.2},
						Confidence:   0.94,
						QualityScore: 0.82,
						Embedding:    []float32{0.97, 0.243},
					},
				},
			},
		},
	})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	firstPhoto := &model.Photo{FilePath: firstPhotoPath, FileName: "cross-photo-first.jpg", FileSize: 1, FileHash: "cross-photo-first", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	secondPhoto := &model.Photo{FilePath: secondPhotoPath, FileName: "cross-photo-second.jpg", FileSize: 1, FileHash: "cross-photo-second", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(firstPhoto))
	require.NoError(t, photoRepo.Create(secondPhoto))

	firstJob := &model.PeopleJob{
		PhotoID:  firstPhoto.ID,
		FilePath: firstPhoto.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}
	secondJob := &model.PeopleJob{
		PhotoID:  secondPhoto.ID,
		FilePath: secondPhoto.FilePath,
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(firstJob))
	require.NoError(t, jobRepo.Create(secondJob))

	require.NoError(t, svc.processJob(firstJob))

	// Reset clustering counter to ensure second job also triggers clustering
	// This is needed because the test expects faces to be linked across jobs
	svc.clusteringTaskCounter = peopleClusteringTaskInterval

	require.NoError(t, svc.processJob(secondJob))

	firstFaces, err := faceRepo.ListByPhotoID(firstPhoto.ID)
	require.NoError(t, err)
	secondFaces, err := faceRepo.ListByPhotoID(secondPhoto.ID)
	require.NoError(t, err)
	require.Len(t, firstFaces, 1)
	require.Len(t, secondFaces, 1)
	require.NotNil(t, firstFaces[0].PersonID)
	require.NotNil(t, secondFaces[0].PersonID)
	assert.Equal(t, *firstFaces[0].PersonID, *secondFaces[0].PersonID)
	assert.Equal(t, model.FaceClusterStatusAssigned, firstFaces[0].ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusAssigned, secondFaces[0].ClusterStatus)

	people, err := personRepo.ListAll()
	require.NoError(t, err)
	require.Len(t, people, 1)
}

func normalizeFaceComponents(components [][]uint) []string {
	normalized := make([]string, 0, len(components))
	for _, component := range components {
		ids := append([]uint(nil), component...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			parts = append(parts, fmt.Sprintf("%d", id))
		}
		normalized = append(normalized, strings.Join(parts, ","))
	}
	sort.Strings(normalized)
	return normalized
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

	// Wait for job to be marked as completed (processJob may still be running after photo is updated)
	waitForPeopleCondition(t, 3*time.Second, func() bool {
		stats, err := svc.GetStats()
		require.NoError(t, err)
		return stats.Completed == 1
	})

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
		repository.NewCannotLinkRepository(db),
		cfg,
		&fakePeopleMLClient{
			responses: map[string]*mlclient.DetectFacesResponse{
				activePath: {Faces: nil, ProcessingTimeMS: 3},
			},
		},
		nil,
	).(*peopleService)
	// Reset clustering counter to ensure clustering runs
	peopleSvc.clusteringTaskCounter = peopleClusteringTaskInterval
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

	task, err := photoSvc.StartScan(rootDir)
	require.NoError(t, err)
	require.NotNil(t, task)
	t.Logf("Started scan, task ID=%s, status=%s, waiting for completion...", task.ID, task.Status)

	// Give goroutine time to start and update status
	time.Sleep(200 * time.Millisecond)

	// Check current status
	currentTask := photoSvc.GetScanTask()
	if currentTask != nil {
		t.Logf("After sleep, scan task status: %s", currentTask.Status)
	} else {
		t.Logf("After sleep, GetScanTask returned nil")
	}

	waitForTaskStatus(t, photoSvc, map[string]bool{model.ScanJobStatusCompleted: true}, 3*time.Second)

	waitForPeopleCondition(t, 3*time.Second, func() bool {
		task := peopleSvc.GetTaskStatus()
		stats, statsErr := peopleSvc.GetStats()
		require.NoError(t, statsErr)
		return task != nil && (task.Status == model.TaskStatusRunning || task.Status == model.TaskStatusIdle) && stats.Total == 1 && stats.Completed == 1
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
		repository.NewCannotLinkRepository(db),
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
		nil,
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
			return updated.FaceProcessStatus == model.FaceProcessStatusReady &&
				updated.TopPersonCategory == model.PersonCategoryFamily
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

	t.Run("中等相似度并入已有人物", func(t *testing.T) {
		rootDir := t.TempDir()
		newPhotoPath := createTestImageFile(t, rootDir, "medium-similarity.jpg")

		svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{
			responses: map[string]*mlclient.DetectFacesResponse{
				newPhotoPath: {
					Faces: []mlclient.DetectedFace{
						{
							BBox:         mlclient.BoundingBox{X: 0.15, Y: 0.15, Width: 0.2, Height: 0.2},
							Confidence:   0.97,
							QualityScore: 0.79,
							Embedding:    []float32{0.89, 0.4559605, 0},
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

		oldPhoto := &model.Photo{FilePath: filepath.Join(rootDir, "existing.jpg"), FileName: "existing.jpg", FileSize: 1, FileHash: "existing-medium", Width: 100, Height: 100, Status: model.PhotoStatusActive}
		newPhoto := &model.Photo{FilePath: newPhotoPath, FileName: filepath.Base(newPhotoPath), FileSize: 1, FileHash: "medium-similarity", Width: 100, Height: 100, Status: model.PhotoStatusActive}
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
			Confidence:   0.96,
			QualityScore: 0.82,
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

		people, err := personRepo.ListAll()
		require.NoError(t, err)
		assert.Len(t, people, 1)

		require.NoError(t, svc.StopBackground())
	})

	t.Run("边界单脸保持待聚类", func(t *testing.T) {
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
		assert.Nil(t, faces[0].PersonID)
		assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus)

		people, err := personRepo.ListAll()
		require.NoError(t, err)
		assert.Len(t, people, 1)

		updatedPhoto, err := photoRepo.GetByID(newPhoto.ID)
		require.NoError(t, err)
		assert.Equal(t, "", updatedPhoto.TopPersonCategory)

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

	_, err := svc.MergePeople(target.ID, []uint{source.ID})
	require.NoError(t, err)

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

	newPerson, _, err := svc.SplitPerson([]uint{faceB.ID})
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

	_, err := svc.MoveFaces([]uint{face.ID}, target.ID)
	require.NoError(t, err)

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

func TestPeopleService_MergePeopleSchedulesFeedbackReclusterAsync(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type feedbackSchedulerTestHooks interface {
		setFeedbackReclusterHookForTest(func() model.ReclusterResult)
		setFeedbackReclusterPollIntervalForTest(time.Duration)
		scheduleFeedbackRecluster()
	}

	hooks, ok := any(svc).(feedbackSchedulerTestHooks)
	require.True(t, ok, "expected async feedback recluster hooks to be available")

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	targetPhoto := &model.Photo{FilePath: "/photos/manual-target.jpg", FileName: "manual-target.jpg", FileSize: 1, FileHash: "manual-target", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	sourcePhoto := &model.Photo{FilePath: "/photos/manual-source.jpg", FileName: "manual-source.jpg", FileSize: 1, FileHash: "manual-source", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	require.NoError(t, photoRepo.Create(sourcePhoto))

	target := &model.Person{Category: model.PersonCategoryFamily}
	source := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(source))

	targetFace := &model.Face{
		PhotoID:       targetPhoto.ID,
		PersonID:      &target.ID,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.90,
		QualityScore:  0.70,
		Embedding:     encodeEmbedding(t, []float32{1, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  0.95,
	}
	mergedFace := &model.Face{
		PhotoID:       sourcePhoto.ID,
		PersonID:      &source.ID,
		BBoxX:         0.2,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.96,
		QualityScore:  0.95,
		Embedding:     encodeEmbedding(t, []float32{0, 1}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  0.92,
	}
	require.NoError(t, faceRepo.Create(targetFace))
	require.NoError(t, faceRepo.Create(mergedFace))
	require.NoError(t, personRepo.RefreshStats(target.ID))
	require.NoError(t, personRepo.RefreshStats(source.ID))

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	hooks.setFeedbackReclusterPollIntervalForTest(5 * time.Millisecond)
	hooks.setFeedbackReclusterHookForTest(func() model.ReclusterResult {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return model.ReclusterResult{Evaluated: 9, Reassigned: 3, Iterations: 1}
	})
	t.Cleanup(func() {
		hooks.setFeedbackReclusterHookForTest(nil)
		select {
		case <-release:
		default:
			close(release)
		}
	})

	begin := time.Now()
	rc, err := svc.MergePeople(target.ID, []uint{source.ID})
	elapsed := time.Since(begin)
	require.NoError(t, err)
	require.NotNil(t, rc)
	assert.Zero(t, rc.Evaluated)
	assert.Zero(t, rc.Reassigned)
	assert.Zero(t, rc.Iterations)
	assert.Less(t, elapsed, 100*time.Millisecond)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected background feedback recluster to start")
	}

	updatedMergedFace, err := faceRepo.GetByID(mergedFace.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedMergedFace)
	require.NotNil(t, updatedMergedFace.PersonID)
	assert.Equal(t, target.ID, *updatedMergedFace.PersonID)
	assert.True(t, updatedMergedFace.ManualLocked)

	select {
	case <-release:
	default:
		close(release)
	}
}

func TestPeopleService_FeedbackReclusterCoalescesRequests(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type feedbackSchedulerTestHooks interface {
		setFeedbackReclusterHookForTest(func() model.ReclusterResult)
		setFeedbackReclusterPollIntervalForTest(time.Duration)
		scheduleFeedbackRecluster()
	}

	hooks, ok := any(svc).(feedbackSchedulerTestHooks)
	require.True(t, ok, "expected async feedback recluster hooks to be available")

	var runs atomic.Int32
	firstRunStarted := make(chan struct{}, 1)
	releaseFirstRun := make(chan struct{})
	hooks.setFeedbackReclusterPollIntervalForTest(5 * time.Millisecond)
	hooks.setFeedbackReclusterHookForTest(func() model.ReclusterResult {
		run := runs.Add(1)
		if run == 1 {
			select {
			case firstRunStarted <- struct{}{}:
			default:
			}
			<-releaseFirstRun
		}
		return model.ReclusterResult{Evaluated: 1}
	})
	t.Cleanup(func() {
		hooks.setFeedbackReclusterHookForTest(nil)
		select {
		case <-releaseFirstRun:
		default:
			close(releaseFirstRun)
		}
	})

	hooks.scheduleFeedbackRecluster()

	select {
	case <-firstRunStarted:
	case <-time.After(time.Second):
		t.Fatal("expected first feedback recluster run to start")
	}

	hooks.scheduleFeedbackRecluster()
	hooks.scheduleFeedbackRecluster()

	close(releaseFirstRun)
	waitForPeopleCondition(t, time.Second, func() bool {
		return runs.Load() >= 2
	})
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(2), runs.Load())
}

func TestPeopleService_FeedbackReclusterDefersWhileBackgroundRunning(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type feedbackSchedulerTestHooks interface {
		setFeedbackReclusterHookForTest(func() model.ReclusterResult)
		setFeedbackReclusterPollIntervalForTest(time.Duration)
		scheduleFeedbackRecluster()
	}

	hooks, ok := any(svc).(feedbackSchedulerTestHooks)
	require.True(t, ok, "expected async feedback recluster hooks to be available")

	var runs atomic.Int32
	hooks.setFeedbackReclusterPollIntervalForTest(5 * time.Millisecond)
	hooks.setFeedbackReclusterHookForTest(func() model.ReclusterResult {
		runs.Add(1)
		return model.ReclusterResult{Evaluated: 1}
	})
	t.Cleanup(func() {
		hooks.setFeedbackReclusterHookForTest(nil)
	})

	svc.setBackgroundBusy(true)

	hooks.scheduleFeedbackRecluster()
	time.Sleep(40 * time.Millisecond)
	assert.Zero(t, runs.Load())

	svc.setBackgroundBusy(false)

	waitForPeopleCondition(t, time.Second, func() bool {
		return runs.Load() == 1
	})
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

// TestPeopleService_ApplyDetectionResult_EmptyFaceListCompletesSuccessfully verifies that photos with no faces
// are properly marked as no_face and job is completed.
func TestPeopleService_ApplyDetectionResult_EmptyFaceListCompletesSuccessfully(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	photoRepo := repository.NewPhotoRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photo := &model.Photo{
		FilePath: "/photos/no-face.jpg",
		FileName: "no-face.jpg",
		FileSize: 1,
		FileHash: "hash-no-face",
		Width:    100,
		Height:   100,
		Status:   model.PhotoStatusActive,
	}
	require.NoError(t, photoRepo.Create(photo))

	job := &model.PeopleJob{
		PhotoID:  photo.ID,
		FilePath: photo.FilePath,
		Status:   model.PeopleJobStatusProcessing,
		Source:   model.PeopleJobSourceManual,
		WorkerID: "worker-1",
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	result := &model.PeopleDetectionResult{
		Faces: []model.PeopleDetectionFace{},
	}

	err := svc.ApplyDetectionResult(job, photo, result)
	require.NoError(t, err)

	updatedPhoto, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedPhoto)
	assert.Equal(t, model.FaceProcessStatusNoFace, updatedPhoto.FaceProcessStatus)
	assert.Equal(t, 0, updatedPhoto.FaceCount)

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	assert.Empty(t, faces)

	updatedJob, err := jobRepo.GetByID(job.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedJob)
	assert.Equal(t, model.PeopleJobStatusCompleted, updatedJob.Status)
	assert.NotNil(t, updatedJob.CompletedAt)
}

// TestPeopleService_ApplyDetectionResult_WithFacesCreatesFacesAndCompletes verifies that detection results
// with faces properly create face records and complete the job.
func TestPeopleService_ApplyDetectionResult_WithFacesCreatesFacesAndCompletes(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "with-faces.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photo := &model.Photo{
		FilePath: photoPath,
		FileName: "with-faces.jpg",
		FileSize: 1,
		FileHash: "hash-with-faces",
		Width:    320,
		Height:   320,
		Status:   model.PhotoStatusActive,
	}
	require.NoError(t, photoRepo.Create(photo))

	job := &model.PeopleJob{
		PhotoID:  photo.ID,
		FilePath: photo.FilePath,
		Status:   model.PeopleJobStatusProcessing,
		Source:   model.PeopleJobSourceManual,
		WorkerID: "worker-1",
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	result := &model.PeopleDetectionResult{
		Faces: []model.PeopleDetectionFace{
			{
				BBox:         model.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
				Confidence:   0.95,
				QualityScore: 0.88,
				Embedding:    []float32{1, 0, 0},
			},
			{
				BBox:         model.BoundingBox{X: 0.5, Y: 0.5, Width: 0.2, Height: 0.2},
				Confidence:   0.93,
				QualityScore: 0.85,
				Embedding:    []float32{0, 1, 0},
			},
		},
	}

	err := svc.ApplyDetectionResult(job, photo, result)
	require.NoError(t, err)

	updatedPhoto, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedPhoto)
	assert.Equal(t, model.FaceProcessStatusReady, updatedPhoto.FaceProcessStatus)
	assert.Equal(t, 2, updatedPhoto.FaceCount)

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 2)

	updatedJob, err := jobRepo.GetByID(job.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedJob)
	assert.Equal(t, model.PeopleJobStatusCompleted, updatedJob.Status)
}

// TestPeopleService_ApplyDetectionResult_CleansUpOldFaces verifies that old faces are deleted
// and person state is synced when new detection result is applied.
func TestPeopleService_ApplyDetectionResult_CleansUpOldFaces(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "cleanup-test.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	photo := &model.Photo{
		FilePath: photoPath,
		FileName: "cleanup-test.jpg",
		FileSize: 1,
		FileHash: "hash-cleanup",
		Width:    320,
		Height:   320,
		Status:   model.PhotoStatusActive,
	}
	require.NoError(t, photoRepo.Create(photo))

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))

	oldFace := &model.Face{
		PhotoID:       photo.ID,
		PersonID:      &person.ID,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.90,
		QualityScore:  0.80,
		Embedding:     encodeEmbedding(t, []float32{0.5, 0.5}),
		ClusterStatus: model.FaceClusterStatusAssigned,
	}
	require.NoError(t, faceRepo.Create(oldFace))
	require.NoError(t, personRepo.RefreshStats(person.ID))

	job := &model.PeopleJob{
		PhotoID:  photo.ID,
		FilePath: photo.FilePath,
		Status:   model.PeopleJobStatusProcessing,
		Source:   model.PeopleJobSourceManual,
		WorkerID: "worker-1",
		Priority: 10,
		QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	result := &model.PeopleDetectionResult{
		Faces: []model.PeopleDetectionFace{
			{
				BBox:         model.BoundingBox{X: 0.3, Y: 0.3, Width: 0.2, Height: 0.2},
				Confidence:   0.97,
				QualityScore: 0.90,
				Embedding:    []float32{1, 0, 0},
			},
		},
	}

	err := svc.ApplyDetectionResult(job, photo, result)
	require.NoError(t, err)

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.NotEqual(t, oldFace.ID, faces[0].ID)

	// Person should be deleted because all faces were removed and syncPersonState cleans up empty persons
	updatedPerson, err := personRepo.GetByID(person.ID)
	require.NoError(t, err)
	assert.Nil(t, updatedPerson)
}
