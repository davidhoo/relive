package handler

import (
	"errors"
	"image/color"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type stubPeopleService struct {
	task                 *model.PeopleTask
	stats                *model.PeopleStatsResponse
	logs                 []string
	updateCategoryPerson uint
	updateCategoryValue  string
	updateNamePerson     uint
	updateNameValue      string
	updateAvatarPerson   uint
	updateAvatarFace     uint
	mergeTargetPerson    uint
	mergeSourcePeople    []uint
	splitFaceIDs         []uint
	splitResult          *model.Person
	moveFaceIDs          []uint
	moveTargetPerson     uint
	err                  error
}

func (s *stubPeopleService) StartBackground() (*model.PeopleTask, error) { return nil, nil }
func (s *stubPeopleService) StopBackground() error                       { return nil }
func (s *stubPeopleService) GetTaskStatus() *model.PeopleTask            { return s.task }
func (s *stubPeopleService) GetStats() (*model.PeopleStatsResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.stats, nil
}
func (s *stubPeopleService) GetBackgroundLogs() []string { return s.logs }
func (s *stubPeopleService) EnqueuePhoto(_ uint, _ string, _ int, _ bool) error {
	return nil
}
func (s *stubPeopleService) EnqueueByPath(_ string, _ string, _ int) (int, error) { return 0, nil }
func (s *stubPeopleService) MergePeople(targetPersonID uint, sourcePersonIDs []uint) error {
	if s.err != nil {
		return s.err
	}
	s.mergeTargetPerson = targetPersonID
	s.mergeSourcePeople = append([]uint(nil), sourcePersonIDs...)
	return nil
}
func (s *stubPeopleService) SplitPerson(faceIDs []uint) (*model.Person, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.splitFaceIDs = append([]uint(nil), faceIDs...)
	if s.splitResult != nil {
		return s.splitResult, nil
	}
	return &model.Person{ID: 99, Category: model.PersonCategoryStranger}, nil
}
func (s *stubPeopleService) MoveFaces(faceIDs []uint, targetPersonID uint) error {
	if s.err != nil {
		return s.err
	}
	s.moveFaceIDs = append([]uint(nil), faceIDs...)
	s.moveTargetPerson = targetPersonID
	return nil
}
func (s *stubPeopleService) UpdatePersonCategory(personID uint, category string) error {
	if s.err != nil {
		return s.err
	}
	s.updateCategoryPerson = personID
	s.updateCategoryValue = category
	return nil
}
func (s *stubPeopleService) UpdatePersonName(personID uint, name string) error {
	if s.err != nil {
		return s.err
	}
	s.updateNamePerson = personID
	s.updateNameValue = name
	return nil
}
func (s *stubPeopleService) UpdatePersonAvatar(personID uint, faceID uint) error {
	if s.err != nil {
		return s.err
	}
	s.updateAvatarPerson = personID
	s.updateAvatarFace = faceID
	return nil
}
func (s *stubPeopleService) HandleShutdown() error { return nil }

type peopleListPayload struct {
	Items      []model.PersonResponse `json:"items"`
	Total      int64                  `json:"total"`
	Page       int                    `json:"page"`
	PageSize   int                    `json:"page_size"`
	TotalPages int                    `json:"total_pages"`
}

type backgroundLogsPayload struct {
	Lines []string `json:"lines"`
}

type peopleHandlerFixture struct {
	FamilyPerson model.Person
	FriendPerson model.Person
	PhotoOne     model.Photo
	PhotoTwo     model.Photo
	FaceOne      model.Face
	FaceTwo      model.Face
	FaceThree    model.Face
	FaceFour     model.Face
}

func newPeopleHandlerForTest(t *testing.T) (*PeopleHandler, *stubPeopleService, *gorm.DB, *config.Config) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.Person{}, &model.Face{}, &model.PeopleJob{}))

	cfg := &config.Config{
		Photos: config.PhotosConfig{
			ThumbnailPath: t.TempDir(),
		},
	}
	serviceStub := &stubPeopleService{
		task:  &model.PeopleTask{Status: model.TaskStatusRunning, ProcessedJobs: 3},
		stats: &model.PeopleStatsResponse{Total: 10, Pending: 2, Completed: 8},
		logs:  []string{"line1", "line2"},
	}

	handler := NewPeopleHandler(
		serviceStub,
		repository.NewPersonRepository(db),
		repository.NewFaceRepository(db),
		repository.NewPhotoRepository(db),
		cfg,
	)

	return handler, serviceStub, db, cfg
}

func seedPeopleHandlerFixture(t *testing.T, db *gorm.DB) peopleHandlerFixture {
	t.Helper()

	now := time.Now().UTC()
	photoOne := model.Photo{
		FilePath:          "/photos/one.jpg",
		FileName:          "one.jpg",
		FileSize:          1024,
		Width:             800,
		Height:            600,
		Status:            model.PhotoStatusActive,
		FaceProcessStatus: model.FaceProcessStatusReady,
		FaceCount:         3,
		TopPersonCategory: model.PersonCategoryFamily,
		TakenAt:           &now,
		ThumbnailStatus:   model.ThumbnailStatusReady,
		GeocodeStatus:     model.GeocodeStatusNone,
	}
	photoTwo := model.Photo{
		FilePath:          "/photos/two.jpg",
		FileName:          "two.jpg",
		FileSize:          2048,
		Width:             1024,
		Height:            768,
		Status:            model.PhotoStatusActive,
		FaceProcessStatus: model.FaceProcessStatusReady,
		FaceCount:         1,
		TopPersonCategory: model.PersonCategoryFamily,
		TakenAt:           ptrTime(now.Add(-time.Hour)),
		ThumbnailStatus:   model.ThumbnailStatusReady,
		GeocodeStatus:     model.GeocodeStatusNone,
	}
	require.NoError(t, db.Create(&photoOne).Error)
	require.NoError(t, db.Create(&photoTwo).Error)

	family := model.Person{
		Name:       "Alice",
		Category:   model.PersonCategoryFamily,
		FaceCount:  3,
		PhotoCount: 2,
	}
	friend := model.Person{
		Name:       "Bob",
		Category:   model.PersonCategoryFriend,
		FaceCount:  1,
		PhotoCount: 1,
	}
	require.NoError(t, db.Create(&family).Error)
	require.NoError(t, db.Create(&friend).Error)

	faceOne := model.Face{
		PhotoID:       photoOne.ID,
		PersonID:      &family.ID,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.99,
		QualityScore:  0.95,
		ThumbnailPath: "faces/face-1.jpg",
	}
	faceTwo := model.Face{
		PhotoID:       photoOne.ID,
		PersonID:      &family.ID,
		BBoxX:         0.4,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.98,
		QualityScore:  0.88,
		ThumbnailPath: "faces/face-2.jpg",
	}
	faceThree := model.Face{
		PhotoID:       photoTwo.ID,
		PersonID:      &family.ID,
		BBoxX:         0.2,
		BBoxY:         0.3,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.97,
		QualityScore:  0.90,
		ThumbnailPath: "faces/face-3.jpg",
	}
	faceFour := model.Face{
		PhotoID:       photoOne.ID,
		PersonID:      &friend.ID,
		BBoxX:         0.6,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.96,
		QualityScore:  0.87,
		ThumbnailPath: "faces/face-4.jpg",
	}
	require.NoError(t, db.Create(&faceOne).Error)
	require.NoError(t, db.Create(&faceTwo).Error)
	require.NoError(t, db.Create(&faceThree).Error)
	require.NoError(t, db.Create(&faceFour).Error)

	family.RepresentativeFaceID = &faceOne.ID
	friend.RepresentativeFaceID = &faceFour.ID
	require.NoError(t, db.Save(&family).Error)
	require.NoError(t, db.Save(&friend).Error)

	return peopleHandlerFixture{
		FamilyPerson: family,
		FriendPerson: friend,
		PhotoOne:     photoOne,
		PhotoTwo:     photoTwo,
		FaceOne:      faceOne,
		FaceTwo:      faceTwo,
		FaceThree:    faceThree,
		FaceFour:     faceFour,
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func TestPeopleHandlerListPeople(t *testing.T) {
	handler, _, db, _ := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people?search=Ali&category=family&page=1&page_size=10", nil, nil, handler.ListPeople)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	payload := decodeResponseData[peopleListPayload](t, resp)
	require.Len(t, payload.Items, 1)
	assert.Equal(t, fixture.FamilyPerson.ID, payload.Items[0].ID)
	assert.Equal(t, int64(1), payload.Total)
}

func TestPeopleHandlerGetPerson(t *testing.T) {
	handler, _, db, _ := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/1", nil, gin.Params{{Key: "id", Value: "1"}}, handler.GetPerson)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	person := decodeResponseData[model.PersonResponse](t, resp)
	assert.Equal(t, fixture.FamilyPerson.ID, person.ID)
	assert.Equal(t, "Alice", person.Name)
	assert.Equal(t, model.PersonCategoryFamily, person.Category)
	assert.Equal(t, fixture.FaceOne.ID, *person.RepresentativeFaceID)
}

func TestPeopleHandlerGetPersonPhotos(t *testing.T) {
	handler, _, db, _ := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/1/photos", nil, gin.Params{{Key: "id", Value: "1"}}, handler.GetPersonPhotos)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	photos := decodeResponseData[[]model.Photo](t, resp)
	require.Len(t, photos, 2)
	assert.ElementsMatch(t, []uint{fixture.PhotoOne.ID, fixture.PhotoTwo.ID}, []uint{photos[0].ID, photos[1].ID})
}

func TestPeopleHandlerGetPersonFaces(t *testing.T) {
	handler, _, db, _ := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/1/faces", nil, gin.Params{{Key: "id", Value: "1"}}, handler.GetPersonFaces)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	faces := decodeResponseData[[]model.FaceResponse](t, resp)
	require.Len(t, faces, 3)
	assert.ElementsMatch(t, []uint{fixture.FaceOne.ID, fixture.FaceTwo.ID, fixture.FaceThree.ID}, []uint{faces[0].ID, faces[1].ID, faces[2].ID})
}

func TestPeopleHandlerUpdateCategory(t *testing.T) {
	handler, svc, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/7/category", []byte(`{"category":"friend"}`), gin.Params{{Key: "id", Value: "7"}}, handler.UpdatePersonCategory)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(7), svc.updateCategoryPerson)
	assert.Equal(t, model.PersonCategoryFriend, svc.updateCategoryValue)
}

func TestPeopleHandlerUpdateName(t *testing.T) {
	handler, svc, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/7/name", []byte(`{"name":"Alice Zhang"}`), gin.Params{{Key: "id", Value: "7"}}, handler.UpdatePersonName)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(7), svc.updateNamePerson)
	assert.Equal(t, "Alice Zhang", svc.updateNameValue)
}

func TestPeopleHandlerUpdateAvatar(t *testing.T) {
	handler, svc, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/7/avatar", []byte(`{"face_id":12}`), gin.Params{{Key: "id", Value: "7"}}, handler.UpdatePersonAvatar)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(7), svc.updateAvatarPerson)
	assert.Equal(t, uint(12), svc.updateAvatarFace)
}

func TestPeopleHandlerMerge(t *testing.T) {
	handler, svc, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/merge", []byte(`{"target_person_id":3,"source_person_ids":[4,5]}`), nil, handler.MergePeople)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(3), svc.mergeTargetPerson)
	assert.Equal(t, []uint{4, 5}, svc.mergeSourcePeople)
}

func TestPeopleHandlerSplit(t *testing.T) {
	handler, svc, _, _ := newPeopleHandlerForTest(t)
	svc.splitResult = &model.Person{ID: 55, Category: model.PersonCategoryAcquaintance}

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/split", []byte(`{"face_ids":[8,9]}`), nil, handler.SplitPerson)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []uint{8, 9}, svc.splitFaceIDs)
	resp := decodeAPIResponse(t, rec)
	person := decodeResponseData[model.PersonResponse](t, resp)
	assert.Equal(t, uint(55), person.ID)
	assert.Equal(t, model.PersonCategoryAcquaintance, person.Category)
}

func TestPeopleHandlerMoveFaces(t *testing.T) {
	handler, svc, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/move-faces", []byte(`{"face_ids":[8,9],"target_person_id":6}`), nil, handler.MoveFaces)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []uint{8, 9}, svc.moveFaceIDs)
	assert.Equal(t, uint(6), svc.moveTargetPerson)
}

func TestPeopleHandlerTask(t *testing.T) {
	handler, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/task", nil, nil, handler.GetTask)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	task := decodeResponseData[model.PeopleTask](t, resp)
	assert.Equal(t, model.TaskStatusRunning, task.Status)
	assert.Equal(t, int64(3), task.ProcessedJobs)
}

func TestPeopleHandlerStats(t *testing.T) {
	handler, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/stats", nil, nil, handler.GetStats)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	stats := decodeResponseData[model.PeopleStatsResponse](t, resp)
	assert.Equal(t, int64(10), stats.Total)
	assert.Equal(t, int64(8), stats.Completed)
}

func TestPeopleHandlerBackgroundLogs(t *testing.T) {
	handler, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/background/logs", nil, nil, handler.GetBackgroundLogs)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	payload := decodeResponseData[backgroundLogsPayload](t, resp)
	assert.Equal(t, []string{"line1", "line2"}, payload.Lines)
}

func TestPeopleHandlerGetPhotoPeople(t *testing.T) {
	handler, _, db, _ := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/photos/1/people", nil, gin.Params{{Key: "id", Value: "1"}}, handler.GetPhotoPeople)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	payload := decodeResponseData[model.PhotoPersonResponse](t, resp)
	assert.Equal(t, fixture.PhotoOne.ID, payload.PhotoID)
	assert.Equal(t, model.FaceProcessStatusReady, payload.FaceProcessStatus)
	assert.Equal(t, 3, payload.FaceCount)
	require.Len(t, payload.People, 2)
	assert.Equal(t, fixture.FamilyPerson.ID, payload.People[0].ID)
	assert.Len(t, payload.People[0].Faces, 2)
}

func TestPeopleHandlerGetFaceThumbnail(t *testing.T) {
	handler, _, db, cfg := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)
	thumbnailPath := filepath.Join(cfg.Photos.ThumbnailPath, fixture.FaceOne.ThumbnailPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(thumbnailPath), 0o755))
	require.NoError(t, os.WriteFile(thumbnailPath, []byte("face-thumb"), 0o644))

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/faces/1/thumbnail", nil, gin.Params{{Key: "id", Value: "1"}}, handler.GetFaceThumbnail)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "face-thumb", rec.Body.String())
}

func TestPeopleHandlerGetFaceThumbnailGeneratesMissingCrop(t *testing.T) {
	handler, _, db, cfg := newPeopleHandlerForTest(t)
	sourceDir := t.TempDir()
	photoPath := filepath.Join(sourceDir, "photo.jpg")
	require.NoError(t, imaging.Save(imaging.New(320, 320, color.NRGBA{R: 120, G: 80, B: 40, A: 255}), photoPath))

	photo := &model.Photo{
		FilePath: photoPath,
		FileName: filepath.Base(photoPath),
		FileSize: 1,
		FileHash: "handler-face-thumb",
		Width:    320,
		Height:   320,
		Status:   model.PhotoStatusActive,
	}
	require.NoError(t, db.Create(photo).Error)

	face := &model.Face{
		PhotoID:      photo.ID,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.3,
		BBoxHeight:   0.3,
		Confidence:   0.95,
		QualityScore: 0.9,
	}
	require.NoError(t, db.Create(face).Error)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/faces/1/thumbnail", nil, gin.Params{{Key: "id", Value: "1"}}, handler.GetFaceThumbnail)

	require.Equal(t, http.StatusOK, rec.Code)

	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	require.NotEmpty(t, updated.ThumbnailPath)
	require.FileExists(t, filepath.Join(cfg.Photos.ThumbnailPath, updated.ThumbnailPath))
}

func TestPeopleHandlerStatsError(t *testing.T) {
	handler, svc, _, _ := newPeopleHandlerForTest(t)
	svc.err = errors.New("stats failed")

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/stats", nil, nil, handler.GetStats)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
