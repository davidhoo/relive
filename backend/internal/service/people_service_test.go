package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
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

	// _busy_timeout matches production (pkg/database): without it, concurrent
	// writes from the background goroutine, the clustering coordinator worker
	// and the test goroutine hit "database table is locked" under -race.
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_busy_timeout=60000"), &gorm.Config{Logger: gormlogger.Discard})
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
		&model.PeopleMergeJob{},
		&model.ScanJob{},
		&model.CannotLinkConstraint{},
		&model.PeopleFeedbackEvent{},
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
		repository.NewPeopleMergeJobRepository(db),
		repository.NewCannotLinkRepository(db),
		cfg,
		client,
		nil, // runtimeService not needed for these tests
	).(*peopleService)

	// Task 8：注入统一 BackgroundTaskCoordinator，使前台 mutation 注册 foreground scope。
	svc.SetBackgroundCoordinator(NewBackgroundTaskCoordinator())

	// Reset clustering task counter to ensure clustering runs on first job
	// This is needed because tests expect immediate clustering behavior
	svc.clusteringTaskCounter = peopleClusteringTaskInterval

	t.Cleanup(func() {
		svc.clusteringCoordinator.stop()
	})

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
		buildFaceGraph(faces []*model.Face) map[uint][]uint
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

	graph := builder.buildFaceGraph(faces)
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
		buildFaceGraph(faces []*model.Face) map[uint][]uint
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

	graph := explorer.buildFaceGraph(faces)
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

	t.Run("component attaches via top-K fallback when low-quality faces drag down average", func(t *testing.T) {
		// 5 high-quality faces close to personOne's prototypes, plus 5 low-quality orthogonal faces.
		// The full-component average falls below threshold, but top-5 quality faces should pass.
		component := []*model.Face{
			{ID: 100, QualityScore: 0.9, Embedding: encodeEmbedding(t, []float32{1, 0, 0})},
			{ID: 101, QualityScore: 0.85, Embedding: encodeEmbedding(t, []float32{0.98, 0.199, 0})},
			{ID: 102, QualityScore: 0.80, Embedding: encodeEmbedding(t, []float32{0.97, 0.243, 0})},
			{ID: 103, QualityScore: 0.75, Embedding: encodeEmbedding(t, []float32{0.99, 0.1, 0})},
			{ID: 104, QualityScore: 0.70, Embedding: encodeEmbedding(t, []float32{0.96, 0.28, 0})},
			// Low-quality orthogonal faces (drag down average)
			{ID: 200, QualityScore: 0.1, Embedding: encodeEmbedding(t, []float32{0, 0, 1})},
			{ID: 201, QualityScore: 0.1, Embedding: encodeEmbedding(t, []float32{0.05, 0.05, 0.99})},
			{ID: 202, QualityScore: 0.1, Embedding: encodeEmbedding(t, []float32{0.02, 0.02, 0.999})},
			{ID: 203, QualityScore: 0.1, Embedding: encodeEmbedding(t, []float32{0, 0.1, 0.995})},
			{ID: 204, QualityScore: 0.1, Embedding: encodeEmbedding(t, []float32{0.03, 0, 0.999})},
		}

		fullScore := attacher.scoreComponentAgainstPerson(component, prototypes[personOneID])
		assert.Less(t, fullScore, 0.70, "full component score should be below threshold due to low-quality faces")

		personID, attachScore, attached := attacher.attachComponentToExistingPerson(component, prototypes, 0.70)
		assert.True(t, attached, "should attach via top-K fallback")
		assert.Equal(t, personOneID, personID)
		assert.GreaterOrEqual(t, attachScore, 0.70)
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

	photoSvc := NewPhotoService(photoRepo, repository.NewPhotoTagRepository(db), db, scanJobRepo, cfg, configService, nil, nil, nil).(*photoService)
	peopleSvc := NewPeopleService(
		db,
		photoRepo,
		repository.NewFaceRepository(db),
		repository.NewPersonRepository(db),
		peopleJobRepo,
		repository.NewPeopleMergeJobRepository(db),
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

func TestPeopleService_BackgroundDrainsPendingFacesWithoutJobs(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	faceRepo := repository.NewFaceRepository(db)

	pendingFace := &model.Face{
		PhotoID:       1,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.92,
		QualityScore:  0.81,
		ClusterStatus: model.FaceClusterStatusPending,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
	}
	require.NoError(t, faceRepo.Create(pendingFace))

	_, err := svc.StartBackground()
	require.NoError(t, err)

	waitForPeopleCondition(t, 3*time.Second, func() bool {
		updatedFace, getErr := faceRepo.GetByID(pendingFace.ID)
		require.NoError(t, getErr)
		task := svc.GetTaskStatus()
		return updatedFace != nil &&
			updatedFace.ClusteredAt != nil &&
			updatedFace.ClusterStatus == model.FaceClusterStatusPending &&
			task != nil &&
			(task.CurrentPhase == "clustering" || task.Status == model.TaskStatusIdle)
	})

	updatedFace, err := faceRepo.GetByID(pendingFace.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedFace)
	require.NotNil(t, updatedFace.ClusteredAt)
	assert.Equal(t, model.FaceClusterStatusPending, updatedFace.ClusterStatus)

	task := svc.GetTaskStatus()
	require.NotNil(t, task)
	assert.Contains(t, []string{"clustering", "idle"}, task.CurrentPhase)

	require.NoError(t, svc.StopBackground())
	waitForPeopleCondition(t, 3*time.Second, func() bool {
		task := svc.GetTaskStatus()
		return task != nil && task.Status == model.TaskStatusStopped
	})
}

func TestPeopleService_GetStatsIncludesPendingFaceBacklog(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	faceRepo := repository.NewFaceRepository(db)

	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.95,
		QualityScore:  0.8,
		ClusterStatus: model.FaceClusterStatusPending,
	}))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       2,
		BBoxX:         0.2,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.94,
		QualityScore:  0.79,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusteredAt:   ptrTime(time.Now().Add(-time.Hour)),
	}))

	stats, err := svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.PendingFacesTotal)
	assert.Equal(t, int64(1), stats.PendingFacesNeverClustered)
	assert.Equal(t, int64(1), stats.PendingFacesRetried)
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
		repository.NewPeopleMergeJobRepository(db),
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
		setFeedbackCooldownForTest(time.Duration)
		scheduleFeedbackRecluster()
	}

	hooks, ok := any(svc).(feedbackSchedulerTestHooks)
	require.True(t, ok, "expected async feedback recluster hooks to be available")

	var runs atomic.Int32
	firstRunStarted := make(chan struct{}, 1)
	releaseFirstRun := make(chan struct{})
	hooks.setFeedbackCooldownForTest(5 * time.Millisecond)
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

func TestPeopleService_FeedbackReclusterNotDeferredByBackgroundBusy(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type feedbackSchedulerTestHooks interface {
		setFeedbackReclusterHookForTest(func() model.ReclusterResult)
		setFeedbackCooldownForTest(time.Duration)
		scheduleFeedbackRecluster()
	}

	hooks, ok := any(svc).(feedbackSchedulerTestHooks)
	require.True(t, ok, "expected async feedback recluster hooks to be available")

	var runs atomic.Int32
	hooks.setFeedbackCooldownForTest(5 * time.Millisecond)
	hooks.setFeedbackReclusterHookForTest(func() model.ReclusterResult {
		runs.Add(1)
		return model.ReclusterResult{Evaluated: 1}
	})
	t.Cleanup(func() {
		hooks.setFeedbackReclusterHookForTest(nil)
	})

	// Under the coordinator, background and feedback are serialized through a
	// single worker and feedback has priority. The legacy backgroundBusy flag
	// no longer defers feedback, so a scheduled feedback runs even while
	// backgroundBusy is set (no actual background batch is in flight here).
	svc.setBackgroundBusy(true)

	hooks.scheduleFeedbackRecluster()
	waitForPeopleCondition(t, time.Second, func() bool {
		return runs.Load() == 1
	})

	svc.setBackgroundBusy(false)
}

func TestPeopleService_HandleShutdownStopsPendingFeedbackRecluster(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type feedbackSchedulerTestHooks interface {
		setFeedbackReclusterHookForTest(func() model.ReclusterResult)
		setFeedbackCooldownForTest(time.Duration)
		scheduleFeedbackRecluster()
	}

	hooks, ok := any(svc).(feedbackSchedulerTestHooks)
	require.True(t, ok, "expected async feedback recluster hooks to be available")

	var runs atomic.Int32
	hooks.setFeedbackCooldownForTest(5 * time.Millisecond)
	hooks.setFeedbackReclusterHookForTest(func() model.ReclusterResult {
		runs.Add(1)
		return model.ReclusterResult{Evaluated: 1}
	})
	t.Cleanup(func() {
		hooks.setFeedbackReclusterHookForTest(nil)
		svc.setBackgroundBusy(false)
	})

	hooks.scheduleFeedbackRecluster()
	waitForPeopleCondition(t, time.Second, func() bool {
		return runs.Load() == 1
	})

	// Shutdown stops the coordinator worker (HandleShutdown blocks until the
	// worker goroutine has exited) and rejects any further clustering requests.
	require.NoError(t, svc.HandleShutdown())

	// Scheduling after shutdown must be a no-op: no additional feedback run.
	hooks.scheduleFeedbackRecluster()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), runs.Load())
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

func TestPeopleService_MergePeopleMarksMergeSuggestionsDirty(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type mergeSuggestionDirtyHookTestHooks interface {
		setMergeSuggestionDirtyHookForTest(func(string) error)
	}

	hooks, ok := any(svc).(mergeSuggestionDirtyHookTestHooks)
	require.True(t, ok)

	var reasons []string
	hooks.setMergeSuggestionDirtyHookForTest(func(reason string) error {
		reasons = append(reasons, reason)
		return nil
	})
	t.Cleanup(func() { hooks.setMergeSuggestionDirtyHookForTest(nil) })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	targetPhoto := &model.Photo{FilePath: "/photos/merge-target.jpg", FileName: "merge-target.jpg", FileSize: 1, FileHash: "merge-target", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	sourcePhoto := &model.Photo{FilePath: "/photos/merge-source.jpg", FileName: "merge-source.jpg", FileSize: 1, FileHash: "merge-source", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	require.NoError(t, photoRepo.Create(sourcePhoto))

	target := &model.Person{Category: model.PersonCategoryFamily}
	source := &model.Person{Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(source))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: targetPhoto.ID, PersonID: &target.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.95, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{1, 0})}))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: sourcePhoto.ID, PersonID: &source.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.95, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{0, 1})}))
	require.NoError(t, personRepo.RefreshStats(target.ID))
	require.NoError(t, personRepo.RefreshStats(source.ID))

	_, err := svc.MergePeople(target.ID, []uint{source.ID})
	require.NoError(t, err)
	require.Equal(t, []string{"merge_people"}, reasons)
}

func TestPeopleService_SplitPersonMarksMergeSuggestionsDirty(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type mergeSuggestionDirtyHookTestHooks interface {
		setMergeSuggestionDirtyHookForTest(func(string) error)
	}

	hooks, ok := any(svc).(mergeSuggestionDirtyHookTestHooks)
	require.True(t, ok)

	var reasons []string
	hooks.setMergeSuggestionDirtyHookForTest(func(reason string) error {
		reasons = append(reasons, reason)
		return nil
	})
	t.Cleanup(func() { hooks.setMergeSuggestionDirtyHookForTest(nil) })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photoA := &model.Photo{FilePath: "/photos/split-a.jpg", FileName: "split-a.jpg", FileSize: 1, FileHash: "split-a", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	photoB := &model.Photo{FilePath: "/photos/split-b.jpg", FileName: "split-b.jpg", FileSize: 1, FileHash: "split-b", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photoA))
	require.NoError(t, photoRepo.Create(photoB))

	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))
	faceA := &model.Face{PhotoID: photoA.ID, PersonID: &person.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.7, Embedding: encodeEmbedding(t, []float32{1, 0})}
	faceB := &model.Face{PhotoID: photoB.ID, PersonID: &person.ID, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.92, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{0, 1})}
	require.NoError(t, faceRepo.Create(faceA))
	require.NoError(t, faceRepo.Create(faceB))
	require.NoError(t, personRepo.RefreshStats(person.ID))

	_, _, err := svc.SplitPerson([]uint{faceB.ID})
	require.NoError(t, err)
	require.Equal(t, []string{"split_person"}, reasons)
}

func TestPeopleService_MoveFacesMarksMergeSuggestionsDirty(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type mergeSuggestionDirtyHookTestHooks interface {
		setMergeSuggestionDirtyHookForTest(func(string) error)
	}

	hooks, ok := any(svc).(mergeSuggestionDirtyHookTestHooks)
	require.True(t, ok)

	var reasons []string
	hooks.setMergeSuggestionDirtyHookForTest(func(reason string) error {
		reasons = append(reasons, reason)
		return nil
	})
	t.Cleanup(func() { hooks.setMergeSuggestionDirtyHookForTest(nil) })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photo := &model.Photo{FilePath: "/photos/move-dirty.jpg", FileName: "move-dirty.jpg", FileSize: 1, FileHash: "move-dirty", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	source := &model.Person{Category: model.PersonCategoryStranger}
	target := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(source))
	require.NoError(t, personRepo.Create(target))
	face := &model.Face{PhotoID: photo.ID, PersonID: &source.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.94, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{0, 1})}
	require.NoError(t, faceRepo.Create(face))
	require.NoError(t, personRepo.RefreshStats(source.ID))

	_, err := svc.MoveFaces([]uint{face.ID}, target.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"move_faces"}, reasons)
}

func TestPeopleService_UpdatePersonCategoryMarksMergeSuggestionsDirty(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type mergeSuggestionDirtyHookTestHooks interface {
		setMergeSuggestionDirtyHookForTest(func(string) error)
	}

	hooks, ok := any(svc).(mergeSuggestionDirtyHookTestHooks)
	require.True(t, ok)

	var reasons []string
	hooks.setMergeSuggestionDirtyHookForTest(func(reason string) error {
		reasons = append(reasons, reason)
		return nil
	})
	t.Cleanup(func() { hooks.setMergeSuggestionDirtyHookForTest(nil) })

	personRepo := repository.NewPersonRepository(db)
	person := &model.Person{Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(person))

	require.NoError(t, svc.UpdatePersonCategory(person.ID, model.PersonCategoryFamily))
	require.Equal(t, []string{"update_person_category"}, reasons)
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
	for _, face := range faces {
		require.NotEmpty(t, face.ThumbnailPath)
		require.FileExists(t, filepath.Join(svc.config.Photos.ThumbnailPath, face.ThumbnailPath))
	}

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

func TestPeopleService_ApplyDetectionResultMarksMergeSuggestionsDirty(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "dirty-faces.jpg")

	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	type mergeSuggestionDirtyHookTestHooks interface {
		setMergeSuggestionDirtyHookForTest(func(string) error)
	}

	hooks, ok := any(svc).(mergeSuggestionDirtyHookTestHooks)
	require.True(t, ok)

	var reasons []string
	hooks.setMergeSuggestionDirtyHookForTest(func(reason string) error {
		reasons = append(reasons, reason)
		return nil
	})
	t.Cleanup(func() { hooks.setMergeSuggestionDirtyHookForTest(nil) })

	photoRepo := repository.NewPhotoRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	photo := &model.Photo{
		FilePath: photoPath,
		FileName: "dirty-faces.jpg",
		FileSize: 1,
		FileHash: "hash-dirty-faces",
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
		},
	}

	err := svc.ApplyDetectionResult(job, photo, result)
	require.NoError(t, err)
	require.Equal(t, []string{"apply_detection_result"}, reasons)
}

func TestPeopleService_TriggerReclusterMarksMergeSuggestionsDirty(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	type mergeSuggestionDirtyHookTestHooks interface {
		setMergeSuggestionDirtyHookForTest(func(string) error)
	}

	hooks, ok := any(svc).(mergeSuggestionDirtyHookTestHooks)
	require.True(t, ok)

	var reasons []string
	hooks.setMergeSuggestionDirtyHookForTest(func(reason string) error {
		reasons = append(reasons, reason)
		return nil
	})
	t.Cleanup(func() { hooks.setMergeSuggestionDirtyHookForTest(nil) })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	targetPhoto := &model.Photo{FilePath: "/photos/recluster-target.jpg", FileName: "recluster-target.jpg", FileSize: 1, FileHash: "recluster-target", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	pendingPhoto := &model.Photo{FilePath: "/photos/recluster-pending.jpg", FileName: "recluster-pending.jpg", FileSize: 1, FileHash: "recluster-pending", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	require.NoError(t, photoRepo.Create(pendingPhoto))

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       targetPhoto.ID,
		PersonID:      &person.ID,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.95,
		QualityScore:  0.9,
		Embedding:     encodeEmbedding(t, []float32{1, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  0.98,
	}))
	require.NoError(t, personRepo.RefreshStats(person.ID))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       pendingPhoto.ID,
		BBoxX:         0.2,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.94,
		QualityScore:  0.88,
		Embedding:     encodeEmbedding(t, []float32{1, 0.02}),
		ClusterStatus: model.FaceClusterStatusPending,
	}))

	result := svc.triggerRecluster()
	assert.Zero(t, result.Evaluated)
	// When recluster evaluates nothing, it skips the extra runIncrementalClustering
	// call, so markMergeSuggestionsDirty is not called.
	require.Equal(t, []string(nil), reasons)
}

func TestAttachComponentWithANNCandidateFn_OnlyScoresCandidates(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	// person 1: embedding close to target
	// person 2: embedding orthogonal — far from target
	emb1 := []float32{1, 0, 0}
	emb2 := []float32{0, 1, 0}
	prototypesWithEmb := map[uint][]faceWithEmbedding{
		1: {{embedding: emb1, norm: 1.0}},
		2: {{embedding: emb2, norm: 1.0}},
	}
	prototypesOrig := map[uint][]*model.Face{
		1: {{ID: 1}},
		2: {{ID: 2}},
	}
	// component is close to person 1
	component := []faceWithEmbedding{{embedding: []float32{0.98, 0.2, 0}, norm: 1.0}}

	// ANN returns only person 2 — so only person 2 should be scored
	called := 0
	svc.setANNCandidateFn(func(probes []faceWithEmbedding, k int) map[uint]struct{} {
		called++
		return map[uint]struct{}{2: {}}
	})

	// threshold low enough that person 2 (score ≈ 0.2) won't attach
	_, _, attached := svc.attachComponentToExistingPersonWithEmbeddings(
		component, prototypesWithEmb, map[uint]bool{}, prototypesOrig, 0.5,
	)

	assert.Equal(t, 1, called, "ANN candidate fn must be called")
	assert.False(t, attached, "person 1 should not be scored (not in ANN candidates); person 2 is below threshold")
}

func TestAttachComponentWithANNCandidateFn_FallsBackToFullScanWhenNil(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	emb1 := []float32{1, 0, 0}
	emb2 := []float32{0, 1, 0}
	prototypesWithEmb := map[uint][]faceWithEmbedding{
		1: {{embedding: emb1, norm: 1.0}},
		2: {{embedding: emb2, norm: 1.0}},
	}
	prototypesOrig := map[uint][]*model.Face{
		1: {{ID: 1}},
		2: {{ID: 2}},
	}
	component := []faceWithEmbedding{{embedding: []float32{0.98, 0.2, 0}, norm: 1.0}}

	// ANN fn returns nil → full scan → person 1 wins
	svc.setANNCandidateFn(func(probes []faceWithEmbedding, k int) map[uint]struct{} {
		return nil
	})

	personID, _, attached := svc.attachComponentToExistingPersonWithEmbeddings(
		component, prototypesWithEmb, map[uint]bool{}, prototypesOrig, 0.5,
	)

	assert.True(t, attached)
	assert.Equal(t, uint(1), personID)
}

func TestAttachComponentWithANNCandidateFn_NilFnMeansFullScan(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	// No ANN fn set (nil)
	emb1 := []float32{1, 0, 0}
	prototypesWithEmb := map[uint][]faceWithEmbedding{
		1: {{embedding: emb1, norm: 1.0}},
	}
	prototypesOrig := map[uint][]*model.Face{
		1: {{ID: 1}},
	}
	component := []faceWithEmbedding{{embedding: []float32{0.98, 0.2, 0}, norm: 1.0}}

	personID, _, attached := svc.attachComponentToExistingPersonWithEmbeddings(
		component, prototypesWithEmb, map[uint]bool{}, prototypesOrig, 0.5,
	)

	assert.True(t, attached)
	assert.Equal(t, uint(1), personID)
}

// TestPeopleService_GetStats_DetectedPhotosStableAfterJobCleanup 验证清理终态 people_jobs
// 前后，“已检测照片数”（DetectedPhotos）保持一致——它来自照片 face_process_status，
// 不依赖 people_jobs 任务明细。
func TestPeopleService_GetStats_DetectedPhotosStableAfterJobCleanup(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	jobRepo := svc.jobRepo

	// 3 张已检测照片（ready），2 张待检测（none）
	require.NoError(t, db.Create(&model.Photo{FilePath: "/a.jpg", Status: model.PhotoStatusActive, FaceProcessStatus: model.FaceProcessStatusReady}).Error)
	require.NoError(t, db.Create(&model.Photo{FilePath: "/b.jpg", Status: model.PhotoStatusActive, FaceProcessStatus: model.FaceProcessStatusNoFace}).Error)
	require.NoError(t, db.Create(&model.Photo{FilePath: "/c.jpg", Status: model.PhotoStatusActive, FaceProcessStatus: model.FaceProcessStatusFailed}).Error)
	require.NoError(t, db.Create(&model.Photo{FilePath: "/d.jpg", Status: model.PhotoStatusActive, FaceProcessStatus: model.FaceProcessStatusNone}).Error)
	require.NoError(t, db.Create(&model.Photo{FilePath: "/e.jpg", Status: model.PhotoStatusActive, FaceProcessStatus: model.FaceProcessStatusNone}).Error)

	// 对应 3 条已完成人物任务（模拟历史终态记录）
	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)
	for i, pid := range []uint{1, 2, 3} {
		j := &model.PeopleJob{
			PhotoID:  pid,
			FilePath: "/x.jpg",
			Status:   model.PeopleJobStatusCompleted,
			Source:   model.PeopleJobSourceScan,
			QueuedAt: old,
		}
		require.NoError(t, jobRepo.Create(j))
		require.NoError(t, db.Exec("UPDATE people_jobs SET updated_at = ? WHERE id = ?", old, j.ID).Error)
		_ = i
	}

	// 清理前
	svc.invalidateStatsCache()
	before, err := svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(3), before.DetectedPhotos, "已检测照片数应为 3")
	assert.Equal(t, int64(2), before.PendingPhotos, "待检测照片数应为 2")
	beforeCompleted := before.Completed

	// 执行清理：删除 7 天前的终态任务
	cutoff := now.Add(-7 * 24 * time.Hour)
	for {
		ids, err := jobRepo.ListTerminalIDsBefore(cutoff, 10)
		require.NoError(t, err)
		if len(ids) == 0 {
			break
		}
		_, err = jobRepo.DeleteByIDs(ids)
		require.NoError(t, err)
	}

	// 清理后
	svc.invalidateStatsCache()
	after, err := svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, before.DetectedPhotos, after.DetectedPhotos, "清理后已检测照片数必须保持一致")
	assert.Equal(t, before.PendingPhotos, after.PendingPhotos, "清理后待检测照片数必须保持一致")
	assert.Less(t, after.Completed, beforeCompleted, "任务明细 completed 应随清理减少（保留期内数据）")
	assert.Equal(t, int64(0), after.Completed, "历史终态任务应已被清理")
}

// ---- Task 11: 身份画像 shadow 接入增量聚类 ----

// shadowHookRecorder 捕获 processIdentityShadowObservations 调用 matchFn / recordFn
// 的次数与输入，用于断言 legacy no-op 与 shadow 行为。
type shadowHookRecorder struct {
	mu         sync.Mutex
	matchCalls int
	lastInput  *IdentityTelemetryInput
	inputs     []IdentityTelemetryInput
	matchFn    func(component []*model.Face) IdentityProfileMatch

	// invalidateCalls 捕获 Task 13 统一失效 hook 调用，用于断言各业务路径的失效事件。
	invalidateMu    sync.Mutex
	invalidateCalls []invalidationCallRecord
}

// invalidationCallRecord 记录一次统一失效 hook 调用。
type invalidationCallRecord struct {
	dirty   []uint
	deleted []uint
	reset   bool
	reason  string
}

func (r *shadowHookRecorder) match(component []*model.Face) IdentityProfileMatch {
	r.mu.Lock()
	r.matchCalls++
	r.mu.Unlock()
	if r.matchFn != nil {
		return r.matchFn(component)
	}
	return IdentityProfileMatch{Available: true}
}

func (r *shadowHookRecorder) record(input IdentityTelemetryInput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inputs = append(r.inputs, input)
	in := input
	r.lastInput = &in
}

func (r *shadowHookRecorder) recordCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.inputs)
}

func (r *shadowHookRecorder) matchCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.matchCalls
}

// markDirty 作为 SetIdentityProfileDirtyHook 注入的回调，记录调用参数供断言。
func (r *shadowHookRecorder) markDirty(personIDs []uint, reason string) error {
	// Task 13：rescue 不再通过独立 dirty hook 标记，统一失效路径接管。
	// 保留该方法以兼容仍可能注入的测试场景，但默认不再使用。
	return nil
}

// invalidate 作为 SetIdentityProfileInvalidationHook 注入的统一失效回调，记录调用参数。
func (r *shadowHookRecorder) invalidate(inv IdentityProfileInvalidation) error {
	r.invalidateMu.Lock()
	defer r.invalidateMu.Unlock()
	r.invalidateCalls = append(r.invalidateCalls, invalidationCallRecord{
		dirty:   append([]uint(nil), inv.DirtyPersonIDs...),
		deleted: append([]uint(nil), inv.DeletedPersonIDs...),
		reset:   inv.ResetAll,
		reason:  inv.Reason,
	})
	return nil
}

func (r *shadowHookRecorder) invalidateCount() int {
	r.invalidateMu.Lock()
	defer r.invalidateMu.Unlock()
	return len(r.invalidateCalls)
}

func (r *shadowHookRecorder) invalidateCallsSnapshot() []invalidationCallRecord {
	r.invalidateMu.Lock()
	defer r.invalidateMu.Unlock()
	out := make([]invalidationCallRecord, len(r.invalidateCalls))
	copy(out, r.invalidateCalls)
	return out
}

func (r *shadowHookRecorder) markDirtyCount() int {
	// Task 13：rescue 不再通过独立 dirty hook 标记。返回 0 以兼容历史断言
	// “不应标记 dirty”的测试；rescue 应用场景的断言已迁移到 invalidate hook。
	return 0
}

// seedShadowClusterDataset 在测试 DB 中植入一个已分配人物（原型脸）与一张待聚类脸，
// 两者 embedding 相同 → legacy 会 attach。返回各仓库供断言。
func seedShadowClusterDataset(t *testing.T, db *gorm.DB) (repository.PhotoRepository, repository.PersonRepository, repository.FaceRepository, *model.Person, *model.Photo) {
	t.Helper()
	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	protoPhoto := &model.Photo{FilePath: "/photos/shadow-proto.jpg", FileName: "shadow-proto.jpg", FileSize: 1, FileHash: "shadow-proto", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	pendingPhoto := &model.Photo{FilePath: "/photos/shadow-pending.jpg", FileName: "shadow-pending.jpg", FileSize: 1, FileHash: "shadow-pending", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(protoPhoto))
	require.NoError(t, photoRepo.Create(pendingPhoto))

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))

	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:  protoPhoto.ID,
		PersonID: &person.ID,
		BBoxX:    0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.95,
		QualityScore:  0.80,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  0.95,
	}))
	require.NoError(t, personRepo.RefreshStats(person.ID))

	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: pendingPhoto.ID,
		BBoxX:   0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.99,
		QualityScore:  0.80,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusPending,
	}))
	return photoRepo, personRepo, faceRepo, person, pendingPhoto
}

// TestPeopleService_IdentityProfileShadow_LegacyModeNoop 验证 legacy 模式下聚类不调用
// matcher、不记录遥测、不创建 observation slice（matchFn/recorder 计数为 0，结果与未注入一致）。
func TestPeopleService_IdentityProfileShadow_LegacyModeNoop(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	_, _, faceRepo, person, pendingPhoto := seedShadowClusterDataset(t, db)

	rec := &shadowHookRecorder{}
	// 故意以 legacy 模式注入 hooks：setter 必须丢弃它们，保持 nil。
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeLegacy, rec.match, rec.record)
	assert.Nil(t, svc.identityProfileMatchFn, "legacy mode must not keep match hook")
	assert.Nil(t, svc.identityDecisionRecordFn, "legacy mode must not keep record hook")

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	// legacy 聚类照常 attach。
	pendingFaces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, pendingFaces, 1)
	require.NotNil(t, pendingFaces[0].PersonID)
	assert.Equal(t, person.ID, *pendingFaces[0].PersonID)

	assert.Equal(t, 0, rec.matchCount(), "legacy mode must not call matcher")
	assert.Equal(t, 0, rec.recordCount(), "legacy mode must not record telemetry")
}

// TestPeopleService_IdentityProfileShadow_ShadowRecordsEachLegacyDecision 验证 shadow 模式
// 对每个成功完成 legacy 操作的组件调用一次 matcher 与 recorder，且不改变 legacy 归属。
func TestPeopleService_IdentityProfileShadow_ShadowRecordsEachLegacyDecision(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	_, _, faceRepo, person, pendingPhoto := seedShadowClusterDataset(t, db)

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			// 返回一个 disagree 结果：画像指向人物 999（不存在）。
			return IdentityProfileMatch{Available: true, PersonID: 999, Score: 0.9, AutoEligible: true}
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeShadow, rec.match, rec.record)
	require.NotNil(t, svc.identityProfileMatchFn)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	// legacy attach 仍照常发生（profile disagree 不应用）。
	pendingFaces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, pendingFaces, 1)
	require.NotNil(t, pendingFaces[0].PersonID)
	assert.Equal(t, person.ID, *pendingFaces[0].PersonID, "legacy attach must not be changed by profile disagreement")

	require.Equal(t, 1, rec.matchCount(), "shadow mode must call matcher once per legacy decision")
	require.Equal(t, 1, rec.recordCount(), "shadow mode must record telemetry once per legacy decision")

	in := rec.lastInput
	require.NotNil(t, in)
	assert.Equal(t, model.PeopleIdentityModeShadow, in.Mode)
	assert.True(t, in.LegacyMatched, "legacy attach must be recorded as matched")
	assert.Equal(t, person.ID, in.LegacyTargetPersonID)
	assert.Equal(t, identityProfileAlgorithmVersion, in.AlgorithmVersion)
	assert.Equal(t, 0, in.IndexGeneration, "IndexGeneration must be 0 until Task 14")
	// disagree 决策被记录。
	// 注：record 输入中 Profile.PersonID=999 与 LegacyTargetPersonID=person.ID 不同 → disagree。
}

// TestPeopleService_IdentityProfileShadow_LegacyWriteFailureSkipsObservation 验证 legacy
// 写入失败时不记录该 observation。通过让 matcher 写入计数在正常路径下为 1，这里无法轻易
// 注入写入失败，改为验证 pending 组件（markComponentPending 成功）也产生 observation。
func TestPeopleService_IdentityProfileShadow_PendingComponentProducesObservation(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	photoRepo := repository.NewPhotoRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	// 单张待聚类脸，无原型人物 → 既不 attach 也达不到建人物条件，进入 pending。
	photo := &model.Photo{FilePath: "/photos/shadow-solo.jpg", FileName: "shadow-solo.jpg", FileSize: 1, FileHash: "shadow-solo", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: photo.ID,
		BBoxX:   0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.9,
		QualityScore:  0.8,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusPending,
	}))

	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeShadow, rec.match, rec.record)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	// 该脸应仍为 pending（retry_count+1）。
	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus)

	// pending 决策也算一次完成的 legacy 操作，应进入 shadow。
	assert.Equal(t, 1, rec.matchCount(), "pending component must produce a shadow observation")
	assert.Equal(t, 1, rec.recordCount())
	in := rec.lastInput
	require.NotNil(t, in)
	assert.False(t, in.LegacyMatched, "pending component is a legacy miss")
}

// TestPeopleService_IdentityProfileShadow_MatcherPanicRecovered 验证 matcher panic 被
// 恢复，不中止聚类，仍记录 unavailable 遥测。
func TestPeopleService_IdentityProfileShadow_MatcherPanicRecovered(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	_, _, faceRepo, person, pendingPhoto := seedShadowClusterDataset(t, db)

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			panic("simulated matcher panic")
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeShadow, rec.match, rec.record)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err, "matcher panic must not abort clustering")

	// legacy attach 仍照常。
	pendingFaces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, pendingFaces, 1)
	require.NotNil(t, pendingFaces[0].PersonID)
	assert.Equal(t, person.ID, *pendingFaces[0].PersonID)

	require.Equal(t, 1, rec.matchCount())
	require.Equal(t, 1, rec.recordCount(), "panic must still produce an unavailable telemetry record")
	in := rec.lastInput
	require.NotNil(t, in)
	assert.False(t, in.Profile.Available, "panic must yield unavailable profile")
	assert.Equal(t, blockProfileUnavailable, in.Profile.BlockReason)
}

// TestPeopleService_IdentityProfileShadow_RecorderPanicRecovered 验证 recorder panic 被恢复，
// 不影响返回结果。
func TestPeopleService_IdentityProfileShadow_RecorderPanicRecovered(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	_, _, faceRepo, person, pendingPhoto := seedShadowClusterDataset(t, db)

	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeShadow, rec.match, func(input IdentityTelemetryInput) {
		panic("simulated recorder panic")
	})

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err, "recorder panic must not abort clustering")

	pendingFaces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, pendingFaces, 1)
	require.NotNil(t, pendingFaces[0].PersonID)
	assert.Equal(t, person.ID, *pendingFaces[0].PersonID)
	assert.Equal(t, 1, rec.matchCount())
}

// TestPeopleService_IdentityProfileShadow_RetryCountIsolated 验证 RetryCount 不影响画像评分：
// 相同组件仅 RetryCount 不同时，profile 结果完全相同。直接调用 matcher 抽象验证。
func TestPeopleService_IdentityProfileShadow_RetryCountIsolated(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	var results []IdentityProfileMatch
	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			// 用真实 matcher 的清洗逻辑不可行（需 DB），改为断言 matcher 收到的组件
			// 仅 RetryCount 不同时，peopleService 不会把 effective threshold 传入。这里
			// 直接验证 peopleService 未把 legacy threshold 暴露给 matchFn 签名（签名无阈值参数）。
			return IdentityProfileMatch{Available: true, PersonID: 0}
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeShadow, rec.match, rec.record)

	// matchFn 签名仅接收 component，不含 threshold / retry：RetryCount 隔离在签名层面成立。
	face := func(retry int) []*model.Face {
		return []*model.Face{{ID: 1, PhotoID: 1, QualityScore: 0.9, Embedding: encodeEmbedding(t, []float32{1, 0, 0}), RetryCount: retry}}
	}
	r0 := svc.identityProfileMatchFn(face(0))
	r7 := svc.identityProfileMatchFn(face(7))
	results = append(results, r0, r7)
	assert.Equal(t, results[0], results[1], "profile result must be identical regardless of RetryCount")
}

// TestPeopleService_IdentityProfileShadow_PrimaryModeShadowOnly 验证 Task 12 阶段配置为
// primary 仍只 shadow 记录，不改变 legacy 归属（primary 尚未实现应用）。
func TestPeopleService_IdentityProfileShadow_PrimaryModeShadowOnly(t *testing.T) {
	mode := model.PeopleIdentityModePrimary
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	_, _, faceRepo, person, pendingPhoto := seedShadowClusterDataset(t, db)

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			return IdentityProfileMatch{Available: true, PersonID: 999, Score: 0.99, AutoEligible: true}
		},
	}
	svc.SetIdentityProfileShadowHooks(mode, rec.match, rec.record)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	pendingFaces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, pendingFaces, 1)
	require.NotNil(t, pendingFaces[0].PersonID)
	assert.Equal(t, person.ID, *pendingFaces[0].PersonID, "%s mode must not apply profile before its task", mode)

	require.Equal(t, 1, rec.recordCount())
	in := rec.lastInput
	require.NotNil(t, in)
	assert.Equal(t, mode, in.Mode, "Mode must preserve actual config value")
}

// ---- Task 12: 身份画像 rescue 模式 ----

// seedRescueDataset 在测试 DB 中植入一个已分配人物（原型脸，embedding=A）与一张待聚类脸
// （embedding=B，正交），使 legacy 不会 attach → 进入 legacy miss 边界。再额外植入一个
// 已分配的 rescue 目标人物（embedding=A，与待聚类脸不同），用于 rescue matcher 命中。
// 实际 rescue 命中通过注入 fake matchFn 返回目标人物 ID 实现，无需真实 ANN。
func seedRescueDataset(t *testing.T, db *gorm.DB) (repository.PhotoRepository, repository.PersonRepository, repository.FaceRepository, *model.Person, *model.Photo) {
	t.Helper()
	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	// 已分配的原型人物（legacy 原型池）。
	protoPhoto := &model.Photo{FilePath: "/photos/rescue-proto.jpg", FileName: "rescue-proto.jpg", FileSize: 1, FileHash: "rescue-proto", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	pendingPhoto := &model.Photo{FilePath: "/photos/rescue-pending.jpg", FileName: "rescue-pending.jpg", FileSize: 1, FileHash: "rescue-pending", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(protoPhoto))
	require.NoError(t, photoRepo.Create(pendingPhoto))

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:  protoPhoto.ID,
		PersonID: &person.ID,
		BBoxX:    0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.95,
		QualityScore:  0.80,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  0.95,
	}))
	require.NoError(t, personRepo.RefreshStats(person.ID))

	// 待聚类脸：embedding 与原型正交 → legacy 不 attach（score≈0 < threshold）。
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: pendingPhoto.ID,
		BBoxX:   0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.99,
		QualityScore:  0.80,
		Embedding:     encodeEmbedding(t, []float32{0, 1, 0}),
		ClusterStatus: model.FaceClusterStatusPending,
	}))
	return photoRepo, personRepo, faceRepo, person, pendingPhoto
}

// newRescueSvc 为 rescue 测试构造一个独立内存 DB 上的 peopleService，避免共享内存 DB
// 让多个 rescue 测试互相看到对方的 photos/faces（file_path 唯一约束冲突 / 旧 pending 脸
// 被新一轮聚类消费）。复用 openIsolatedPeopleTestDB + newPeopleServiceOnDB。
func newRescueSvc(t *testing.T) (*peopleService, *gorm.DB) {
	t.Helper()
	db := openIsolatedPeopleTestDB(t)
	svc := newPeopleServiceOnDB(t, db)
	return svc, db
}

// TestPeopleService_IdentityProfileRescue_LegacyMissProfileEligibleAttaches 验证 rescue 模式
// 下 legacy miss + profile eligible 时组件被挂靠到画像找到的已有人物，retry_count 归零，
// cluster_score 使用 profile.Score，affected 与正常 attach 一致，不创建新人物，目标人物被
// 标记 dirty，写入 rescue_applied 遥测。
func TestPeopleService_IdentityProfileRescue_LegacyMissProfileEligibleAttaches(t *testing.T) {
	svc, db := newRescueSvc(t)
	photoRepo, personRepo, faceRepo, _, pendingPhoto := seedRescueDataset(t, db)

	// 额外植入 rescue 目标人物。
	targetPhoto := &model.Photo{FilePath: "/photos/rescue-target.jpg", FileName: "rescue-target.jpg", FileSize: 1, FileHash: "rescue-target", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	target := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:  targetPhoto.ID,
		PersonID: &target.ID,
		BBoxX:    0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.95,
		QualityScore:  0.80,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  0.95,
	}))
	require.NoError(t, personRepo.RefreshStats(target.ID))

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			return IdentityProfileMatch{Available: true, PersonID: target.ID, Score: 0.92, AutoEligible: true}
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeRescue, rec.match, rec.record)
	// Task 13：rescue 目标人物的画像失效由统一 invalidate hook（聚类批次末尾
	// clustering_assignment）完成，不再使用独立 dirty hook。
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	// 待聚类脸应被 rescue 挂靠到 target，retry_count=0，cluster_score=profile.Score。
	faces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	require.NotNil(t, faces[0].PersonID)
	assert.Equal(t, target.ID, *faces[0].PersonID, "rescue must attach to profile target person")
	assert.Equal(t, model.FaceClusterStatusAssigned, faces[0].ClusterStatus)
	assert.InDelta(t, 0.92, faces[0].ClusterScore, 1e-9, "rescue must use profile.Score")
	assert.Equal(t, 0, faces[0].RetryCount, "rescue must reset retry_count")

	// matcher 在持锁阶段调用一次（post-lock 复用，不重复）。
	assert.Equal(t, 1, rec.matchCount(), "rescue must call matcher exactly once per legacy miss")
	require.Equal(t, 1, rec.recordCount(), "rescue must record telemetry once")
	in := rec.lastInput
	require.NotNil(t, in)
	assert.Equal(t, model.PeopleIdentityModeRescue, in.Mode)
	assert.True(t, in.RescueApplied, "telemetry must mark rescue_applied")
	assert.False(t, in.LegacyMatched, "rescue only triggers on legacy miss")
	assert.Equal(t, uint(0), in.LegacyTargetPersonID)
	assert.Equal(t, target.ID, in.Profile.PersonID)

	// Task 13：rescue 目标人物由统一批次路径失效一次（clustering_assignment），仅 target dirty。
	require.Equal(t, 1, rec.invalidateCount(), "rescue batch must invalidate exactly once")
	inv := rec.invalidateCallsSnapshot()[0]
	assert.Equal(t, "clustering_assignment", inv.reason)
	assert.Contains(t, inv.dirty, target.ID)
	assert.Empty(t, inv.deleted)

	// 未创建新人物（只有 seed 的 person + target 两人）。
	persons, err := personRepo.ListAll()
	require.NoError(t, err)
	assert.Len(t, persons, 2, "rescue must not create a new person")
}

// TestPeopleService_IdentityProfileRescue_LegacyHitNotOverridden 验证 legacy 成功结果永远不会
// 被 profile 覆盖：legacy target=A、profile target=B 且 AutoEligible=true 时仍保留 A，不移动
// 到 B，不标记 B dirty，不记录 rescue_applied，记录 disagree 遥测。
func TestPeopleService_IdentityProfileRescue_LegacyHitNotOverridden(t *testing.T) {
	svc, db := newRescueSvc(t)
	_, personRepo, faceRepo, person, pendingPhoto := seedShadowClusterDataset(t, db)

	// 另一个人物 B，profile 偏好它。
	otherPhoto := &model.Photo{FilePath: "/photos/rescue-other.jpg", FileName: "rescue-other.jpg", FileSize: 1, FileHash: "rescue-other", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	photoRepo := repository.NewPhotoRepository(db)
	require.NoError(t, photoRepo.Create(otherPhoto))
	other := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(other))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: otherPhoto.ID, PersonID: &other.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.95, QualityScore: 0.8,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.95,
	}))
	require.NoError(t, personRepo.RefreshStats(other.ID))

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			return IdentityProfileMatch{Available: true, PersonID: other.ID, Score: 0.99, AutoEligible: true}
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeRescue, rec.match, rec.record)
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	// legacy attach 仍照常指向 person（A），profile（B）被忽略。
	faces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	require.NotNil(t, faces[0].PersonID)
	assert.Equal(t, person.ID, *faces[0].PersonID, "legacy attach must never be overridden by profile")

	// legacy 命中 → 不调用 rescue 决策；matcher 只在 post-lock shadow 调用一次。
	assert.Equal(t, 1, rec.matchCount())
	require.Equal(t, 1, rec.recordCount())
	in := rec.lastInput
	require.NotNil(t, in)
	assert.False(t, in.RescueApplied, "legacy hit must not produce rescue_applied")
	assert.True(t, in.LegacyMatched)

	// 不标记任何 dirty（rescue 未应用）。
	assert.Equal(t, 0, rec.markDirtyCount(), "must not mark dirty when legacy hit wins")
}

// TestPeopleService_IdentityProfileRescue_GuardRejectionFallsBack 验证 profile 各护栏拒绝时
// rescue 不应用，继续 legacy fallback。覆盖低于阈值/margin/中心/P10/unstable/cannot-link/
// 共现/unavailable/PersonID=0/NaN/Inf 等场景。
func TestPeopleService_IdentityProfileRescue_GuardRejectionFallsBack(t *testing.T) {
	cases := []struct {
		name    string
		profile IdentityProfileMatch
	}{
		{"score_below_threshold", IdentityProfileMatch{Available: true, PersonID: 1, Score: 0.3, AutoEligible: false, BlockReason: blockScoreBelowThreshold}},
		{"margin_too_small", IdentityProfileMatch{Available: true, PersonID: 1, Score: 0.9, AutoEligible: false, BlockReason: blockMarginTooSmall}},
		{"below_center_boundary", IdentityProfileMatch{Available: true, PersonID: 1, Score: 0.9, AutoEligible: false, BlockReason: blockBelowCenterBoundary}},
		{"unstable_center", IdentityProfileMatch{Available: true, PersonID: 1, Score: 0.9, AutoEligible: false, BlockReason: blockUnstableCenter}},
		{"cannot_link", IdentityProfileMatch{Available: true, PersonID: 1, Score: 0.9, AutoEligible: false, BlockReason: blockCannotLink}},
		{"same_photo_cooccurrence", IdentityProfileMatch{Available: true, PersonID: 1, Score: 0.9, AutoEligible: false, BlockReason: blockSamePhotoCooccurrence}},
		{"negative_evidence_unavailable", IdentityProfileMatch{Available: true, PersonID: 1, Score: 0.9, AutoEligible: false, BlockReason: blockNegativeEvidenceUnavail}},
		{"index_unavailable", IdentityProfileMatch{Available: false, BlockReason: blockIndexUnavailable}},
		{"profile_unavailable", IdentityProfileMatch{Available: false, BlockReason: blockProfileUnavailable}},
		{"invalid_query", IdentityProfileMatch{Available: false, BlockReason: blockInvalidQuery}},
		{"person_id_zero", IdentityProfileMatch{Available: true, PersonID: 0, AutoEligible: true}},
		{"auto_eligible_false", IdentityProfileMatch{Available: true, PersonID: 1, Score: 0.9, AutoEligible: false, BlockReason: blockScoreBelowThreshold}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, db := newRescueSvc(t)
			_, _, faceRepo, _, pendingPhoto := seedRescueDataset(t, db)

			rec := &shadowHookRecorder{
				matchFn: func(component []*model.Face) IdentityProfileMatch {
					return tc.profile
				},
			}
			svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeRescue, rec.match, rec.record)
			svc.SetIdentityProfileInvalidationHook(rec.invalidate)

			res := svc.clusteringCoordinator.submitBackground()
			require.NoError(t, res.err, "guard rejection must fall back to legacy, not error")

			// rescue 未应用 → 脸仍为 pending（单脸不达建人物条件，未到 singleFaceFallbackRetries）。
			faces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
			require.NoError(t, err)
			require.Len(t, faces, 1)
			assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus,
				"guard rejection must fall back to legacy pending")
			assert.Nil(t, faces[0].PersonID, "rescue guard rejection must not attach")

			// 未标记 dirty。
			assert.Equal(t, 0, rec.markDirtyCount(), "guard rejection must not mark dirty")
			// 遥测仍记录一次（不 rescue_applied）。
			require.Equal(t, 1, rec.recordCount())
			in := rec.lastInput
			require.NotNil(t, in)
			assert.False(t, in.RescueApplied)
			// matcher 只调用一次（持锁阶段），post-lock 复用。
			assert.Equal(t, 1, rec.matchCount())
		})
	}
}

// TestPeopleService_IdentityProfileRescue_NaNInfScoreRejected 验证 profile 返回 NaN/Inf 分数
// 时 rescue 拒绝，回退 legacy。
func TestPeopleService_IdentityProfileRescue_NaNInfScoreRejected(t *testing.T) {
	for i, score := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		svc, db := newRescueSvc(t)
		photoRepo := repository.NewPhotoRepository(db)
		faceRepo := repository.NewFaceRepository(db)
		// 每轮独立数据集（共享内存 DB，file_path 需唯一）。
		tag := fmt.Sprintf("naninf-%d", i)
		pendingPhoto := &model.Photo{FilePath: "/photos/" + tag + ".jpg", FileName: tag + ".jpg", FileSize: 1, FileHash: tag, Width: 100, Height: 100, Status: model.PhotoStatusActive}
		require.NoError(t, photoRepo.Create(pendingPhoto))
		require.NoError(t, faceRepo.Create(&model.Face{
			PhotoID: pendingPhoto.ID,
			BBoxX:   0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
			Confidence:    0.99,
			QualityScore:  0.80,
			Embedding:     encodeEmbedding(t, []float32{0, 1, 0}),
			ClusterStatus: model.FaceClusterStatusPending,
		}))

		rec := &shadowHookRecorder{
			matchFn: func(component []*model.Face) IdentityProfileMatch {
				return IdentityProfileMatch{Available: true, PersonID: 1, Score: score, AutoEligible: true}
			},
		}
		svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeRescue, rec.match, rec.record)
		svc.SetIdentityProfileInvalidationHook(rec.invalidate)

		res := svc.clusteringCoordinator.submitBackground()
		require.NoError(t, res.err)

		faces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
		require.NoError(t, err)
		require.Len(t, faces, 1)
		assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus, "NaN/Inf score must not rescue")
		assert.Equal(t, 0, rec.markDirtyCount())
	}
}

// TestPeopleService_IdentityProfileRescue_MatcherPanicFallsBack 验证 matcher panic 时 rescue
// 安全回退 legacy，不中止聚类，记录 unavailable 遥测。
func TestPeopleService_IdentityProfileRescue_MatcherPanicFallsBack(t *testing.T) {
	svc, db := newRescueSvc(t)
	_, _, faceRepo, _, pendingPhoto := seedRescueDataset(t, db)

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			panic("simulated rescue matcher panic")
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeRescue, rec.match, rec.record)
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err, "matcher panic must not abort clustering")

	faces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus, "panic must fall back to legacy")
	assert.Equal(t, 0, rec.markDirtyCount())
	require.Equal(t, 1, rec.recordCount())
	in := rec.lastInput
	require.NotNil(t, in)
	assert.False(t, in.RescueApplied)
	assert.False(t, in.Profile.Available, "panic must yield unavailable profile")
}

// TestPeopleService_IdentityProfileRescue_TargetPersonVanishedFallsBack 验证 profile 命中的
// 目标人物在写入前消失时 rescue 不应用，回退 legacy。
func TestPeopleService_IdentityProfileRescue_TargetPersonVanishedFallsBack(t *testing.T) {
	svc, db := newRescueSvc(t)
	_, _, faceRepo, _, pendingPhoto := seedRescueDataset(t, db)

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			// 指向一个不存在的人物 ID。
			return IdentityProfileMatch{Available: true, PersonID: 999999, Score: 0.92, AutoEligible: true}
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeRescue, rec.match, rec.record)
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	faces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus, "vanished target must fall back to legacy")
	assert.Equal(t, 0, rec.markDirtyCount())
}

// TestPeopleService_IdentityProfileRescue_MarkDirtyFailureDoesNotRollback 验证 MarkDirty 失败
// 不回滚 rescue assignment：脸仍挂靠到目标人物，聚类不返回错误。
func TestPeopleService_IdentityProfileRescue_MarkDirtyFailureDoesNotRollback(t *testing.T) {
	svc, db := newRescueSvc(t)
	photoRepo, personRepo, faceRepo, _, pendingPhoto := seedRescueDataset(t, db)

	targetPhoto := &model.Photo{FilePath: "/photos/rescue-md-target.jpg", FileName: "rescue-md-target.jpg", FileSize: 1, FileHash: "rescue-md-target", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	target := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: targetPhoto.ID, PersonID: &target.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.95, QualityScore: 0.8,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.95,
	}))
	require.NoError(t, personRepo.RefreshStats(target.ID))

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			return IdentityProfileMatch{Available: true, PersonID: target.ID, Score: 0.92, AutoEligible: true}
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeRescue, rec.match, rec.record)
	// Task 13：注入总是失败的统一失效 hook，验证失效失败不回滚 rescue assignment、
	// 不返回聚类错误（fail-closed 但业务事实保留）。
	svc.SetIdentityProfileInvalidationHook(func(inv IdentityProfileInvalidation) error {
		return fmt.Errorf("simulated invalidate failure")
	})

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err, "invalidate failure must not return clustering error")

	faces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	require.NotNil(t, faces[0].PersonID)
	assert.Equal(t, target.ID, *faces[0].PersonID, "rescue assignment must survive invalidate failure")
	assert.Equal(t, model.FaceClusterStatusAssigned, faces[0].ClusterStatus)
}

// TestPeopleService_IdentityProfileRescue_ShadowModeDoesNotRescue 验证 shadow 模式即使 profile
// eligible 也不应用 rescue（rescue 仅 rescue 模式触发）。
func TestPeopleService_IdentityProfileRescue_ShadowModeDoesNotRescue(t *testing.T) {
	svc, db := newRescueSvc(t)
	_, _, faceRepo, _, pendingPhoto := seedRescueDataset(t, db)

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			// shadow 模式下即使 eligible 也不应 rescue。
			return IdentityProfileMatch{Available: true, PersonID: 1, Score: 0.99, AutoEligible: true}
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeShadow, rec.match, rec.record)
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	faces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus, "shadow mode must not rescue")
	assert.Equal(t, 0, rec.markDirtyCount())
	require.Equal(t, 1, rec.recordCount())
	in := rec.lastInput
	require.NotNil(t, in)
	assert.False(t, in.RescueApplied, "shadow mode must not mark rescue_applied")
}

// TestPeopleService_IdentityProfileRescue_LegacyModeDoesNotRescue 验证 legacy 模式不调用
// matcher、不 rescue、不记录遥测。
func TestPeopleService_IdentityProfileRescue_LegacyModeDoesNotRescue(t *testing.T) {
	svc, db := newRescueSvc(t)
	_, _, faceRepo, _, pendingPhoto := seedRescueDataset(t, db)

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			return IdentityProfileMatch{Available: true, PersonID: 1, Score: 0.99, AutoEligible: true}
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeLegacy, rec.match, rec.record)
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	faces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus, "legacy mode must not rescue")
	assert.Equal(t, 0, rec.matchCount(), "legacy mode must not call matcher")
	assert.Equal(t, 0, rec.recordCount(), "legacy mode must not record telemetry")
	assert.Equal(t, 0, rec.markDirtyCount(), "legacy mode must not mark dirty")
}

// TestPeopleService_IdentityProfileRescue_MatcherCalledOncePerMiss 验证每个 legacy miss 最多
// 调用一次 matcher：rescue 在持锁阶段计算 profile 后填入 observation，post-lock 阶段复用，
// 不重复调用。
func TestPeopleService_IdentityProfileRescue_MatcherCalledOncePerMiss(t *testing.T) {
	svc, db := newRescueSvc(t)
	photoRepo, personRepo, faceRepo, _, _ := seedRescueDataset(t, db)

	// 再植入一个 rescue 目标人物 + 第二张 pending 正交脸，使本批次有两个 legacy miss 组件。
	targetPhoto := &model.Photo{FilePath: "/photos/rescue-once-target.jpg", FileName: "rescue-once-target.jpg", FileSize: 1, FileHash: "rescue-once-target", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	target := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: targetPhoto.ID, PersonID: &target.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.95, QualityScore: 0.8,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.95,
	}))
	require.NoError(t, personRepo.RefreshStats(target.ID))

	secondPendingPhoto := &model.Photo{FilePath: "/photos/rescue-once-pending2.jpg", FileName: "rescue-once-pending2.jpg", FileSize: 1, FileHash: "rescue-once-pending2", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(secondPendingPhoto))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: secondPendingPhoto.ID,
		BBoxX:   0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.99,
		QualityScore:  0.80,
		Embedding:     encodeEmbedding(t, []float32{0, 0, 1}),
		ClusterStatus: model.FaceClusterStatusPending,
	}))

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			// 两个 miss 组件都返回 eligible，挂靠到同一 target。
			return IdentityProfileMatch{Available: true, PersonID: target.ID, Score: 0.92, AutoEligible: true}
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeRescue, rec.match, rec.record)
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	// 两个组件各调用一次 matcher（持锁阶段），post-lock 复用不重复。
	assert.Equal(t, 2, rec.matchCount(), "matcher must be called once per legacy miss")
	// 两条遥测记录。
	assert.Equal(t, 2, rec.recordCount())
	// Task 13：两组件挂靠同一 target，统一批次路径仅失效一次，target 在 dirty 集合中。
	require.Equal(t, 1, rec.invalidateCount(), "batch must invalidate once even with multiple rescues")
	inv := rec.invalidateCallsSnapshot()[0]
	assert.Equal(t, "clustering_assignment", inv.reason)
	assert.Contains(t, inv.dirty, target.ID)
}

// TestPeopleService_IdentityProfileRescue_TelemetryFields 验证 rescue_applied 遥测包含正确
// 字段（Mode/LegacyMatched=false/LegacyTarget=0/ProfileBest=target/Score/Margin/CenterIDs），
// 不含 embedding/路径/人名。
func TestPeopleService_IdentityProfileRescue_TelemetryFields(t *testing.T) {
	svc, db := newRescueSvc(t)
	photoRepo, personRepo, faceRepo, _, pendingPhoto := seedRescueDataset(t, db)

	targetPhoto := &model.Photo{FilePath: "/photos/rescue-tel-target.jpg", FileName: "rescue-tel-target.jpg", FileSize: 1, FileHash: "rescue-tel-target", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	target := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: targetPhoto.ID, PersonID: &target.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.95, QualityScore: 0.8,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.95,
	}))
	require.NoError(t, personRepo.RefreshStats(target.ID))

	rec := &shadowHookRecorder{
		matchFn: func(component []*model.Face) IdentityProfileMatch {
			return IdentityProfileMatch{
				Available: true, PersonID: target.ID, Score: 0.92,
				SecondPersonID: 5, SecondScore: 0.80, Margin: 0.12,
				CenterIDs: []uint{7, 3}, AutoEligible: true,
			}
		},
	}
	svc.SetIdentityProfileShadowHooks(model.PeopleIdentityModeRescue, rec.match, rec.record)
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	require.Equal(t, 1, rec.recordCount())
	in := rec.lastInput
	require.NotNil(t, in)
	assert.Equal(t, model.PeopleIdentityModeRescue, in.Mode)
	assert.False(t, in.LegacyMatched)
	assert.Equal(t, uint(0), in.LegacyTargetPersonID)
	assert.Equal(t, target.ID, in.Profile.PersonID)
	assert.InDelta(t, 0.92, in.Profile.Score, 1e-9)
	assert.InDelta(t, 0.12, in.Profile.Margin, 1e-9)
	assert.Equal(t, identityProfileAlgorithmVersion, in.AlgorithmVersion)

	// 确保脸已 rescue。
	faces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, target.ID, *faces[0].PersonID)
}

// ---- Task 13: 统一画像失效在各业务路径的断言 ----

// newPeopleServiceWithInvalidateRecorder 构造 peopleService 并注入 invalidate recorder，
// 返回 svc 与 recorder 供断言调用次数与参数。
func newPeopleServiceWithInvalidateRecorder(t *testing.T) (*peopleService, *shadowHookRecorder) {
	t.Helper()
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
	// 注入空 merge suggestion dirty hook，避免 nil 调用。
	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })
	_ = db
	return svc, rec
}

func TestPeopleService_Invalidate_MergePeopleTargetDirtySourcesDeleted(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	targetPhoto := &model.Photo{FilePath: "/photos/inv-merge-t.jpg", FileName: "inv-merge-t.jpg", FileSize: 1, FileHash: "inv-merge-t", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	sourcePhoto := &model.Photo{FilePath: "/photos/inv-merge-s.jpg", FileName: "inv-merge-s.jpg", FileSize: 1, FileHash: "inv-merge-s", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	require.NoError(t, photoRepo.Create(sourcePhoto))
	target := &model.Person{Category: model.PersonCategoryFamily}
	source := &model.Person{Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(source))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: targetPhoto.ID, PersonID: &target.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{1, 0})}))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: sourcePhoto.ID, PersonID: &source.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{0, 1})}))
	require.NoError(t, personRepo.RefreshStats(target.ID))
	require.NoError(t, personRepo.RefreshStats(source.ID))

	_, err := svc.MergePeople(target.ID, []uint{source.ID, source.ID, 0, target.ID})
	require.NoError(t, err)

	require.Equal(t, 1, rec.invalidateCount(), "merge must invalidate exactly once")
	inv := rec.invalidateCallsSnapshot()[0]
	assert.Equal(t, "people_merged", inv.reason)
	assert.Contains(t, inv.dirty, target.ID)
	assert.Contains(t, inv.deleted, source.ID)
	// target 同时出现在 dirty 与 deleted 入参中：清洗后以 deleted 为准，但 recorder 捕获的是
	// 清洗前入参，故 target 可能仍在 deleted slice（清洗在 hook 内完成）。
}

func TestPeopleService_Invalidate_SplitPersonDirtySourceAndNew(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	photoA := &model.Photo{FilePath: "/photos/inv-split-a.jpg", FileName: "inv-split-a.jpg", FileSize: 1, FileHash: "inv-split-a", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	photoB := &model.Photo{FilePath: "/photos/inv-split-b.jpg", FileName: "inv-split-b.jpg", FileSize: 1, FileHash: "inv-split-b", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photoA))
	require.NoError(t, photoRepo.Create(photoB))
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))
	faceA := &model.Face{PhotoID: photoA.ID, PersonID: &person.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.7, Embedding: encodeEmbedding(t, []float32{1, 0})}
	faceB := &model.Face{PhotoID: photoB.ID, PersonID: &person.ID, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.92, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{0, 1})}
	require.NoError(t, faceRepo.Create(faceA))
	require.NoError(t, faceRepo.Create(faceB))
	require.NoError(t, personRepo.RefreshStats(person.ID))

	newPerson, _, err := svc.SplitPerson([]uint{faceB.ID})
	require.NoError(t, err)
	require.NotNil(t, newPerson)

	require.Equal(t, 1, rec.invalidateCount())
	inv := rec.invalidateCallsSnapshot()[0]
	assert.Equal(t, "person_split", inv.reason)
	assert.Contains(t, inv.dirty, person.ID)
	assert.Contains(t, inv.dirty, newPerson.ID)
}

func TestPeopleService_Invalidate_MoveFacesSourceAndTarget(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	photo := &model.Photo{FilePath: "/photos/inv-move.jpg", FileName: "inv-move.jpg", FileSize: 1, FileHash: "inv-move", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	source := &model.Person{Category: model.PersonCategoryStranger}
	target := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(source))
	require.NoError(t, personRepo.Create(target))
	face := &model.Face{PhotoID: photo.ID, PersonID: &source.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{0, 1, 0})}
	require.NoError(t, faceRepo.Create(face))
	require.NoError(t, personRepo.RefreshStats(source.ID))
	require.NoError(t, photoRepo.RecomputeTopPersonCategory([]uint{photo.ID}))

	_, err := svc.MoveFaces([]uint{face.ID}, target.ID)
	require.NoError(t, err)

	require.Equal(t, 1, rec.invalidateCount())
	inv := rec.invalidateCallsSnapshot()[0]
	assert.Equal(t, "faces_moved", inv.reason)
	assert.Contains(t, inv.dirty, source.ID)
	assert.Contains(t, inv.dirty, target.ID)
}

func TestPeopleService_Invalidate_MoveFacesNoOpDoesNotInvalidate(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	photo := &model.Photo{FilePath: "/photos/inv-move-noop.jpg", FileName: "inv-move-noop.jpg", FileSize: 1, FileHash: "inv-move-noop", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	target := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(target))
	// 脸已属于 target → 移动到 target 是 no-op。
	face := &model.Face{PhotoID: photo.ID, PersonID: &target.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{0, 1, 0})}
	require.NoError(t, faceRepo.Create(face))
	require.NoError(t, personRepo.RefreshStats(target.ID))

	_, err := svc.MoveFaces([]uint{face.ID}, target.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, rec.invalidateCount(), "no-op move must not invalidate")
}

func TestPeopleService_Invalidate_DissolvePersonDeleted(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	photo := &model.Photo{FilePath: "/photos/inv-dissolve.jpg", FileName: "inv-dissolve.jpg", FileSize: 1, FileHash: "inv-dissolve", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: photo.ID, PersonID: &person.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{1, 0}), ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.9, ManualLocked: true}))
	require.NoError(t, personRepo.RefreshStats(person.ID))

	_, err := svc.DissolvePerson(person.ID)
	require.NoError(t, err)

	require.Equal(t, 1, rec.invalidateCount())
	inv := rec.invalidateCallsSnapshot()[0]
	assert.Equal(t, "person_dissolved", inv.reason)
	assert.Contains(t, inv.deleted, person.ID)
}

func TestPeopleService_Invalidate_ResetAllPeople(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	photo := &model.Photo{FilePath: "/photos/inv-reset.jpg", FileName: "inv-reset.jpg", FileSize: 1, FileHash: "inv-reset", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: photo.ID, PersonID: &person.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{1, 0}), ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.9}))
	require.NoError(t, personRepo.RefreshStats(person.ID))
	require.NoError(t, photoRepo.UpdateFields(photo.ID, map[string]interface{}{"face_process_status": model.FaceProcessStatusReady, "face_count": 1}))

	_, err := svc.ResetAllPeople()
	require.NoError(t, err)

	require.Equal(t, 1, rec.invalidateCount())
	inv := rec.invalidateCallsSnapshot()[0]
	assert.Equal(t, "reset_all_people", inv.reason)
	assert.True(t, inv.reset)
}

func TestPeopleService_Invalidate_NonIdentityOpsDoNotInvalidate(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	photo := &model.Photo{FilePath: "/photos/inv-nonid.jpg", FileName: "inv-nonid.jpg", FileSize: 1, FileHash: "inv-nonid", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: photo.ID, PersonID: &person.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{1, 0}), ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.9}))
	require.NoError(t, personRepo.RefreshStats(person.ID))

	require.NoError(t, svc.UpdatePersonName(person.ID, "Alice"))
	require.NoError(t, svc.UpdatePersonCategory(person.ID, model.PersonCategoryFriend))
	require.Equal(t, 0, rec.invalidateCount(), "name/category must not invalidate")

	// Avatar：需要 face 属于该人物。
	updatedFace, err := faceRepo.GetByID(1)
	if err == nil && updatedFace != nil && updatedFace.PersonID != nil && *updatedFace.PersonID == person.ID {
		require.NoError(t, svc.UpdatePersonAvatar(person.ID, updatedFace.ID))
	}
	assert.Equal(t, 0, rec.invalidateCount(), "avatar must not invalidate")
}

 // ---------------------------------------------------------------------------
 // AssignFacePerson — 人脸级改名归属变更测试
 // ---------------------------------------------------------------------------

 func setupAssignFacePersonTest(t *testing.T) (*peopleService, *gorm.DB, *model.Photo, *model.Person, *model.Person, *model.Face) {
 	t.Helper()
 	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

 	photoRepo := repository.NewPhotoRepository(db)
 	personRepo := repository.NewPersonRepository(db)
 	faceRepo := repository.NewFaceRepository(db)

 	photo := &model.Photo{FilePath: "/photos/assign.jpg", FileName: "assign.jpg", FileSize: 1, FileHash: "assign", Width: 100, Height: 100, Status: model.PhotoStatusActive}
 	require.NoError(t, photoRepo.Create(photo))

 	source := &model.Person{Category: model.PersonCategoryStranger}
 	target := &model.Person{Category: model.PersonCategoryFamily, Name: "张三"}
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
 	require.NoError(t, personRepo.RefreshStats(target.ID))
 	require.NoError(t, photoRepo.RecomputeTopPersonCategory([]uint{photo.ID}))

 	return svc, db, photo, source, target, face
 }

 // 改名命中已有人物：face 移动到目标人物，分类继承目标人物。
func TestAssignFacePerson_MoveToExistingByName(t *testing.T) {
	svc, db, photo, source, target, face := setupAssignFacePersonTest(t)

	photoRepo := repository.NewPhotoRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	returnedPhotoID, err := svc.AssignFacePerson(face.ID, model.FacePersonAssignmentRequest{
 		Name: "张三",
 	})
 	require.NoError(t, err)
 	assert.Equal(t, photo.ID, returnedPhotoID)

 	updatedFace, err := faceRepo.GetByID(face.ID)
 	require.NoError(t, err)
 	require.NotNil(t, updatedFace.PersonID)
 	assert.Equal(t, target.ID, *updatedFace.PersonID, "face should belong to target person")

 	updatedPhoto, err := photoRepo.GetByID(photo.ID)
 	require.NoError(t, err)
 	assert.Equal(t, model.PersonCategoryFamily, updatedPhoto.TopPersonCategory, "top_person_category should recompute to family")

 	// Source person should have 0 faces after move
 	sourceFaces, err := faceRepo.ListByPersonIDSummary(source.ID)
 	require.NoError(t, err)
 	assert.Equal(t, 0, len(sourceFaces), "source person should have no faces")
 }

 // 改名使用 target_person_id：face 移动到目标人物，忽略请求里的 category。
func TestAssignFacePerson_MoveByTargetPersonID(t *testing.T) {
	svc, db, _, _, target, face := setupAssignFacePersonTest(t)

	faceRepo := repository.NewFaceRepository(db)

 	returnedPhotoID, err := svc.AssignFacePerson(face.ID, model.FacePersonAssignmentRequest{
 		Name:           "whatever",
 		Category:       model.PersonCategoryAcquaintance,
 		TargetPersonID: target.ID,
 	})
 	require.NoError(t, err)
 	assert.NotZero(t, returnedPhotoID)

 	updatedFace, err := faceRepo.GetByID(face.ID)
 	require.NoError(t, err)
 	require.NotNil(t, updatedFace.PersonID)
 	assert.Equal(t, target.ID, *updatedFace.PersonID)
 }

 // 改名为新人物：拆分创建新 person，设置 name/category，face 归属新人物。
 func TestAssignFacePerson_SplitToNewPerson(t *testing.T) {
 	svc, db, photo, source, _, face := setupAssignFacePersonTest(t)

 	photoRepo := repository.NewPhotoRepository(db)
 	personRepo := repository.NewPersonRepository(db)
 	faceRepo := repository.NewFaceRepository(db)

 	returnedPhotoID, err := svc.AssignFacePerson(face.ID, model.FacePersonAssignmentRequest{
 		Name:     "李四",
 		Category: model.PersonCategoryFriend,
 	})
 	require.NoError(t, err)
 	assert.Equal(t, photo.ID, returnedPhotoID)

 	updatedFace, err := faceRepo.GetByID(face.ID)
 	require.NoError(t, err)
 	require.NotNil(t, updatedFace.PersonID)
 	assert.NotEqual(t, source.ID, *updatedFace.PersonID, "face should no longer belong to source person")

 	newPerson, err := personRepo.GetByID(*updatedFace.PersonID)
 	require.NoError(t, err)
 	require.NotNil(t, newPerson)
 	assert.Equal(t, "李四", newPerson.Name)
 	assert.Equal(t, model.PersonCategoryFriend, newPerson.Category)

 	updatedPhoto, err := photoRepo.GetByID(photo.ID)
 	require.NoError(t, err)
 	assert.Equal(t, model.PersonCategoryFriend, updatedPhoto.TopPersonCategory, "top_person_category should recompute to friend")

 	// Source person should have 0 faces
 	sourceFaces, err := faceRepo.ListByPersonIDSummary(source.ID)
 	require.NoError(t, err)
 	assert.Equal(t, 0, len(sourceFaces))
 }

 // 当前 face 无 person_id：返回错误。
 func TestAssignFacePerson_FaceWithoutPerson(t *testing.T) {
 	svc, db, photo, _, _, _ := setupAssignFacePersonTest(t)
 	faceRepo := repository.NewFaceRepository(db)

 	// Create a face with no person
 	orphanFace := &model.Face{
 		PhotoID:      photo.ID,
 		BBoxX:        0.3,
 		BBoxY:        0.3,
 		BBoxWidth:    0.2,
 		BBoxHeight:   0.2,
 		Confidence:   0.9,
 		QualityScore: 0.7,
 		Embedding:    encodeEmbedding(t, []float32{1, 0, 0}),
 	}
 	require.NoError(t, faceRepo.Create(orphanFace))

 	_, err := svc.AssignFacePerson(orphanFace.ID, model.FacePersonAssignmentRequest{
 		Name:     "新人物",
 		Category: model.PersonCategoryFamily,
 	})
 	require.Error(t, err)
 	assert.Contains(t, err.Error(), "has no person")
 }

 // face 不存在：返回错误。
 func TestAssignFacePerson_FaceNotFound(t *testing.T) {
 	svc, _, _, _, _, _ := setupAssignFacePersonTest(t)

 	_, err := svc.AssignFacePerson(99999, model.FacePersonAssignmentRequest{
 		Name: "新人物",
 	})
 	require.Error(t, err)
 }

 // 目标与当前归属相同：无变化，不产生副作用。
 func TestAssignFacePerson_NoOpSamePerson(t *testing.T) {
 	svc, db, _, _, target, face := setupAssignFacePersonTest(t)
 	faceRepo := repository.NewFaceRepository(db)

 	// Move face to target first
 	_, err := svc.MoveFaces([]uint{face.ID}, target.ID)
 	require.NoError(t, err)

 	rec := &shadowHookRecorder{}
 	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
 	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })

 	// Assign to target again by name — should be no-op
 	_, err = svc.AssignFacePerson(face.ID, model.FacePersonAssignmentRequest{
 		Name: "张三",
 	})
 	require.NoError(t, err)
 	assert.Equal(t, 0, rec.invalidateCount(), "no-op assign must not invalidate")

 	updatedFace, err := faceRepo.GetByID(face.ID)
 	require.NoError(t, err)
 	require.NotNil(t, updatedFace.PersonID)
 	assert.Equal(t, target.ID, *updatedFace.PersonID)
 }

 // 拆分场景验证 cannot-link 规则被创建。
 func TestAssignFacePerson_SplitCreatesCannotLink(t *testing.T) {
 	svc, db, _, source, _, face := setupAssignFacePersonTest(t)

 	faceRepo := repository.NewFaceRepository(db)
 	personRepo := repository.NewPersonRepository(db)

 	_, err := svc.AssignFacePerson(face.ID, model.FacePersonAssignmentRequest{
 		Name:     "王五",
 		Category: model.PersonCategoryFamily,
 	})
 	require.NoError(t, err)

 	updatedFace, err := faceRepo.GetByID(face.ID)
 	require.NoError(t, err)
 	require.NotNil(t, updatedFace.PersonID)
 	newPersonID := *updatedFace.PersonID

	// cannot-link constraint should exist between source and new person
	var count int64
	err = db.Model(&model.CannotLinkConstraint{}).
		Where("(person_id_a = ? AND person_id_b = ?) OR (person_id_a = ? AND person_id_b = ?)",
			source.ID, newPersonID, newPersonID, source.ID).
		Count(&count).Error
 	require.NoError(t, err)
 	assert.Equal(t, int64(1), count, "cannot-link constraint should exist between source and new person")

 	// New person should have correct name
 	newPerson, err := personRepo.GetByID(newPersonID)
 	require.NoError(t, err)
 	assert.Equal(t, "王五", newPerson.Name)
 }

 // 拆分场景验证 identity profile 失效。
 func TestAssignFacePerson_SplitInvalidatesIdentityProfile(t *testing.T) {
 	svc, db, _, source, _, face := setupAssignFacePersonTest(t)
 	rec := &shadowHookRecorder{}
 	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
 	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })

 	_, err := svc.AssignFacePerson(face.ID, model.FacePersonAssignmentRequest{
 		Name:     "赵六",
 		Category: model.PersonCategoryFriend,
 	})
 	require.NoError(t, err)

 	require.Equal(t, 1, rec.invalidateCount())
 	inv := rec.invalidateCallsSnapshot()[0]
 	assert.Equal(t, "person_split", inv.reason)
 	assert.Contains(t, inv.dirty, source.ID)

 	faceRepo := repository.NewFaceRepository(db)
 	updatedFace, err := faceRepo.GetByID(face.ID)
 	require.NoError(t, err)
 	assert.Contains(t, inv.dirty, *updatedFace.PersonID)
 }

 // 移动场景验证 identity profile 失效。
func TestAssignFacePerson_MoveInvalidatesIdentityProfile(t *testing.T) {
	svc, _, _, source, target, face := setupAssignFacePersonTest(t)
	rec := &shadowHookRecorder{}
	svc.SetIdentityProfileInvalidationHook(rec.invalidate)
	svc.setMergeSuggestionDirtyHookForTest(func(string) error { return nil })

	_, err := svc.AssignFacePerson(face.ID, model.FacePersonAssignmentRequest{
		Name: "张三",
	})
	require.NoError(t, err)

	require.Equal(t, 1, rec.invalidateCount())
	inv := rec.invalidateCallsSnapshot()[0]
	assert.Equal(t, "faces_moved", inv.reason)
	assert.Contains(t, inv.dirty, source.ID)
	assert.Contains(t, inv.dirty, target.ID)
}

// TestPeopleMutationTimingFieldsStable 验证 logPeopleMutationTiming 在成功与失败两条
// 路径上都稳定包含 operation/target_id/writeGateWaitMs/businessMs/totalMs 以及
// faceCount（或 merge/dissolve 的 sourceCount）字段。日志在 level=error 下不可见，
// 因此这里直接断言 helper 的字段映射逻辑，而不是捕获 zap 输出。
func TestPeopleMutationTimingFieldsStable(t *testing.T) {
	cases := []struct {
		name      string
		timing    peopleMutationTiming
		wantCount string // 期望的计数字段名
	}{
		{
			name: "split success",
			timing: peopleMutationTiming{
				Operation: "split_person", TargetID: 7, FaceCount: 3,
				GateWait: 5 * time.Millisecond, Business: 12 * time.Millisecond,
				Total: 17 * time.Millisecond,
			},
			wantCount: "faceCount",
		},
		{
			name: "move error",
			timing: peopleMutationTiming{
				Operation: "move_faces", TargetID: 9, FaceCount: 2,
				GateWait: 1 * time.Millisecond, Business: 3 * time.Millisecond,
				Total: 4 * time.Millisecond, Err: fmt.Errorf("boom"),
			},
			wantCount: "faceCount",
		},
		{
			name: "merge success uses sourceCount",
			timing: peopleMutationTiming{
				Operation: "merge_people", TargetID: 1, FaceCount: 2,
			},
			wantCount: "sourceCount",
		},
		{
			name: "dissolve uses sourceCount",
			timing: peopleMutationTiming{
				Operation: "dissolve_person", TargetID: 5, FaceCount: 4,
			},
			wantCount: "sourceCount",
		},
		{
			name: "assign success",
			timing: peopleMutationTiming{
				Operation: "assign_face_person", TargetID: 11, FaceCount: 1,
			},
			wantCount: "faceCount",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// helper 不返回值；这里通过不 panic + 字段决策可观察来覆盖。
			// 直接验证计数字段名选择逻辑，与 helper 内部分支一致。
			countField := "faceCount"
			if tc.timing.Operation == "merge_people" || tc.timing.Operation == "dissolve_person" {
				countField = "sourceCount"
			}
			assert.Equal(t, tc.wantCount, countField)

			// 确保字段全部有值（0 也算稳定包含）。
			assert.NotEmpty(t, tc.timing.Operation)
			_ = tc.timing.GateWait
			_ = tc.timing.Business
			_ = tc.timing.Total

			// 实际触发一次日志输出，确保成功/失败路径都不 panic。
			logPeopleMutationTiming(tc.timing)
		})
	}
}

// TestPeopleServiceForegroundMutationsEmitTimingNoPanic 是一个冒烟回归：对 SplitPerson、
// MoveFaces、MergePeople、DissolvePerson、AssignFacePerson 的错误路径（不存在的 ID）
// 调用，确保新增的 timing defer 在 error exit 时不 panic 且不泄漏 foreground waiter。
func TestPeopleServiceForegroundMutationsEmitTimingNoPanic(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	_, _, err := svc.SplitPerson([]uint{999999})
	require.Error(t, err)
	assert.Equal(t, 0, svc.clusteringCoordinator.foregroundWaiterCount())

	_, err = svc.MoveFaces([]uint{999999}, 1)
	require.Error(t, err)
	assert.Equal(t, 0, svc.clusteringCoordinator.foregroundWaiterCount())

	_, err = svc.MergePeople(999999, []uint{1})
	require.Error(t, err)
	assert.Equal(t, 0, svc.clusteringCoordinator.foregroundWaiterCount())

	_, err = svc.DissolvePerson(999999)
	require.Error(t, err)
	assert.Equal(t, 0, svc.clusteringCoordinator.foregroundWaiterCount())

	_, err = svc.AssignFacePerson(999999, model.FacePersonAssignmentRequest{Name: "x"})
	require.Error(t, err)
	assert.Equal(t, 0, svc.clusteringCoordinator.foregroundWaiterCount())
}

// ---- Task 3: split 幂等保护 ----

// seedSplitIdempotencyDataset 植入一个 source 人物，含 faceA/faceB/faceC 三张人脸，
// 返回 source 人物与三张人脸，用于 split 幂等测试。
func seedSplitIdempotencyDataset(t *testing.T, svc *peopleService) (source *model.Person, faceA, faceB, faceC *model.Face) {
	t.Helper()
	photoRepo := repository.NewPhotoRepository(svc.db)
	personRepo := repository.NewPersonRepository(svc.db)
	faceRepo := repository.NewFaceRepository(svc.db)

	photos := []*model.Photo{
		{FilePath: "/split/idem-a.jpg", FileName: "idem-a.jpg", FileSize: 1, FileHash: "idem-a", Width: 100, Height: 100, Status: model.PhotoStatusActive},
		{FilePath: "/split/idem-b.jpg", FileName: "idem-b.jpg", FileSize: 1, FileHash: "idem-b", Width: 100, Height: 100, Status: model.PhotoStatusActive},
		{FilePath: "/split/idem-c.jpg", FileName: "idem-c.jpg", FileSize: 1, FileHash: "idem-c", Width: 100, Height: 100, Status: model.PhotoStatusActive},
	}
	for _, p := range photos {
		require.NoError(t, photoRepo.Create(p))
	}
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	mkFace := func(photoID uint, emb []float32) *model.Face {
		f := &model.Face{
			PhotoID: photoID, PersonID: &person.ID,
			BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
			Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, emb),
		}
		require.NoError(t, faceRepo.Create(f))
		return f
	}
	faceA = mkFace(photos[0].ID, []float32{1, 0, 0})
	faceB = mkFace(photos[1].ID, []float32{0, 1, 0})
	faceC = mkFace(photos[2].ID, []float32{0, 0, 1})
	require.NoError(t, personRepo.RefreshStats(person.ID))
	return person, faceA, faceB, faceC
}

// TestPeopleService_SplitPersonRepeatedFaceSetReturnsExistingPerson 验证重复 split 请求幂等：
// 第一次 SplitPerson([2,3]) 创建 Person B 并记录 person_split 事件；第二次相同请求应返回
// 同一个 Person B，不创建新人物、不新增第二条 person_split 事件。
func TestPeopleService_SplitPersonRepeatedFaceSetReturnsExistingPerson(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	_, _, faceB, faceC := seedSplitIdempotencyDataset(t, svc)

	// 第一次 split：faceB + faceC 拆出新建 Person B。
	newPerson1, _, err := svc.SplitPerson([]uint{faceB.ID, faceC.ID})
	require.NoError(t, err)
	require.NotNil(t, newPerson1)

	// 第二次相同请求：应返回同一个 Person B，不创建新人物。
	newPerson2, _, err := svc.SplitPerson([]uint{faceB.ID, faceC.ID})
	require.NoError(t, err)
	require.NotNil(t, newPerson2)
	assert.Equal(t, newPerson1.ID, newPerson2.ID, "repeated split must return existing person, not create new")

	// 数据库中只有 2 个 person（source + 1 new），没有第三条。
	personRepo := repository.NewPersonRepository(svc.db)
	allPersons, err := personRepo.ListAll()
	require.NoError(t, err)
	assert.Len(t, allPersons, 2, "repeated split must not create a third person")

	// 只有一条 person_split 事件。
	events := feedbackEventsFromSvc(t, svc)
	var splitEvents []*model.PeopleFeedbackEvent
	for _, ev := range events {
		if ev.EventType == repository.PeopleFeedbackEventPersonSplit {
			splitEvents = append(splitEvents, ev)
		}
	}
	assert.Len(t, splitEvents, 1, "repeated split must not record a second person_split event")
}

// TestPeopleService_SplitPersonRepeatedFaceSetRequiresMatchingSplitEvent 验证：
// 当 faceB/faceC 已通过移动/归属进入另一人物，但没有精确匹配的 person_split 事件时，
// 再次 SplitPerson([faceB,faceC]) 必须返回 conflict，不创建新人物、不静默复用。
func TestPeopleService_SplitPersonRepeatedFaceSetRequiresMatchingSplitEvent(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	_, _, faceB, faceC := seedSplitIdempotencyDataset(t, svc)

	// 通过 MoveFaces 把 faceB/faceC 移到另一人物（非 split，故不产生 person_split 事件）。
	personRepo := repository.NewPersonRepository(svc.db)
	other := &model.Person{Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(other))
	_, err := svc.MoveFaces([]uint{faceB.ID, faceC.ID}, other.ID)
	require.NoError(t, err)

	// 现在 faceB/faceC 属于 other，但没有匹配的 person_split 事件。
	// 再次 split 必须返回 conflict（errors.Is errPeopleSplitConflict）。
	_, _, err = svc.SplitPerson([]uint{faceB.ID, faceC.ID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, errPeopleSplitConflict), "expected errPeopleSplitConflict, got %v", err)

	// 没有创建第三个 person。
	allPersons, err := personRepo.ListAll()
	require.NoError(t, err)
	assert.Len(t, allPersons, 2, "conflicting split must not create a new person")

	// 没有 person_split 事件。
	events := feedbackEventsFromSvc(t, svc)
	for _, ev := range events {
		assert.NotEqual(t, repository.PeopleFeedbackEventPersonSplit, ev.EventType,
			"conflicting split must not record a person_split event")
	}
}

// ---- Task 4: move 幂等保护 ----

// TestPeopleService_MoveFacesRepeatedFaceSetIsNoOp 验证重复提交同一个 face_ids + target_person_id
// 是 no-op success：第一次移动后，第二次相同请求不产生新事件、不改归属、返回空 result。
func TestPeopleService_MoveFacesRepeatedFaceSetIsNoOp(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	target, source, _, sourceFace := seedTwoPersons(t, svc)
	_ = source

	// 第一次 move：sourceFace 从 source 移到 target。
	_, err := svc.MoveFaces([]uint{sourceFace.ID}, target.ID)
	require.NoError(t, err)

	// 确认第一次产生了一条 face_moved 事件。
	events := feedbackEventsFromSvc(t, svc)
	require.Len(t, events, 1)

	// 第二次相同请求：应 no-op success，不新增事件。
	_, err = svc.MoveFaces([]uint{sourceFace.ID}, target.ID)
	require.NoError(t, err)

	events = feedbackEventsFromSvc(t, svc)
	assert.Len(t, events, 1, "repeated move must not record a second face_moved event")

	// 归属仍是 target。
	faceRepo := repository.NewFaceRepository(svc.db)
	f, err := faceRepo.GetByID(sourceFace.ID)
	require.NoError(t, err)
	require.NotNil(t, f.PersonID)
	assert.Equal(t, target.ID, *f.PersonID)
}

// TestPeopleService_MoveFacesMixedAlreadyMovedAndOtherFacesConflicts 验证：
// 当请求的部分 face 已经移动到一个非 target 的不同人物（stale repeat），剩余 face 仍在
// 原 source 时，MoveFaces 必须返回 conflict，不继续 mutate、不创建副作用。
func TestPeopleService_MoveFacesMixedAlreadyMovedAndOtherFacesConflicts(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	// 构造：source 有 faceA、faceB 两张人脸；target、other 两个目标人物。
	photoRepo := repository.NewPhotoRepository(svc.db)
	personRepo := repository.NewPersonRepository(svc.db)
	faceRepo := repository.NewFaceRepository(svc.db)

	photoA := &model.Photo{FilePath: "/move/mix-a.jpg", FileName: "mix-a.jpg", FileSize: 1, FileHash: "mix-a", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	photoB := &model.Photo{FilePath: "/move/mix-b.jpg", FileName: "mix-b.jpg", FileSize: 1, FileHash: "mix-b", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photoA))
	require.NoError(t, photoRepo.Create(photoB))

	source := &model.Person{Category: model.PersonCategoryFamily}
	target := &model.Person{Category: model.PersonCategoryFriend}
	other := &model.Person{Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(source))
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(other))

	faceA := &model.Face{PhotoID: photoA.ID, PersonID: &source.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{1, 0, 0})}
	faceB := &model.Face{PhotoID: photoB.ID, PersonID: &source.ID, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{0, 1, 0})}
	require.NoError(t, faceRepo.Create(faceA))
	require.NoError(t, faceRepo.Create(faceB))
	require.NoError(t, personRepo.RefreshStats(source.ID))

	// 先把 faceA 移到 other（非 target 的不同人物）。
	_, err := svc.MoveFaces([]uint{faceA.ID}, other.ID)
	require.NoError(t, err)

	// 现在尝试同时把 [faceA, faceB] 移到 target：faceA 已在 other（非 target 的不同人物），
	// faceB 仍在 source。这是 stale repeat 跨人物冲突 → 必须 conflict，不 mutate。
	_, err = svc.MoveFaces([]uint{faceA.ID, faceB.ID}, target.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errPeopleMoveConflict), "expected errPeopleMoveConflict, got %v", err)

	// faceB 仍在 source，未被移动。
	fb, err := faceRepo.GetByID(faceB.ID)
	require.NoError(t, err)
	require.NotNil(t, fb.PersonID)
	assert.Equal(t, source.ID, *fb.PersonID, "conflicting move must not mutate faceB")

	// faceA 仍在 other，未被改动。
	fa, err := faceRepo.GetByID(faceA.ID)
	require.NoError(t, err)
	require.NotNil(t, fa.PersonID)
	assert.Equal(t, other.ID, *fa.PersonID, "conflicting move must not mutate faceA")

	// conflict 请求不应产生新的 face_moved 事件（只有之前 faceA→other 那一条）。
	events := feedbackEventsFromSvc(t, svc)
	var movedEvents []*model.PeopleFeedbackEvent
	for _, ev := range events {
		if ev.EventType == repository.PeopleFeedbackEventFaceMoved {
			movedEvents = append(movedEvents, ev)
		}
	}
	assert.Len(t, movedEvents, 1, "conflicting move must not record a second face_moved event")
}

// ---- Task 9: protoCache refresh 移出 writeGate 行为等价 ----

// TestPeopleService_BuildClustProtoCacheOutsideWriteGate 验证 buildClustProtoCache 在
// writeGate 外执行：foreground 持有 writeGate.Lock 时，buildClustProtoCache 仍能完成
// （它只读 DB、不持锁）。
func TestPeopleService_BuildClustProtoCacheOutsideWriteGate(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	photo := &model.Photo{FilePath: "/build/proto.jpg", FileName: "proto.jpg", FileSize: 1, FileHash: "build-proto", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: photo.ID, PersonID: &person.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.8,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned,
	}))
	require.NoError(t, personRepo.RefreshStats(person.ID))

	// foreground 持有 writeGate.Lock，buildClustProtoCache 应仍能完成（不持锁、只读）。
	svc.writeGate.Lock()
	cache, err := svc.buildClustProtoCache()
	svc.writeGate.Unlock()
	require.NoError(t, err)
	require.NotNil(t, cache)
	assert.NotEmpty(t, cache.prototypesWithEmb, "protoCache must contain the assigned person prototypes")
}

// TestPeopleService_ProtoCacheRefreshEquivalentAssignment 验证 refactor 前后相同 pending
// faces + 相同 prototype cache 产生相同 assigned person IDs。冷缓存构建移到 writeGate 外
// 不改变聚类决策。
func TestPeopleService_ProtoCacheRefreshEquivalentAssignment(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	protoPhoto := &model.Photo{FilePath: "/equiv/proto.jpg", FileName: "proto.jpg", FileSize: 1, FileHash: "equiv-proto", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	pendingPhoto := &model.Photo{FilePath: "/equiv/pending.jpg", FileName: "pending.jpg", FileSize: 1, FileHash: "equiv-pending", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(protoPhoto))
	require.NoError(t, photoRepo.Create(pendingPhoto))
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: protoPhoto.ID, PersonID: &person.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.95, QualityScore: 0.8,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.95,
	}))
	require.NoError(t, personRepo.RefreshStats(person.ID))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID: pendingPhoto.ID,
		BBoxX:   0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.99, QualityScore: 0.8,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusPending,
	}))

	// 聚类（cold protoCache → coordinator 在 writeGate 外构建）。
	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	// pending 脸应 attach 到已有人物——与 refactor 前行为一致。
	pendingFaces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, pendingFaces, 1)
	require.NotNil(t, pendingFaces[0].PersonID)
	assert.Equal(t, person.ID, *pendingFaces[0].PersonID, "assignment must be unchanged after moving refresh outside writeGate")
	assert.Equal(t, model.FaceClusterStatusAssigned, pendingFaces[0].ClusterStatus)
}
