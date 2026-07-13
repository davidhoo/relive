package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type stubPeopleService struct {
	task                  *model.PeopleTask
	stats                 *model.PeopleStatsResponse
	logs                  []string
	startResult           *model.PeopleTask
	startCalled           int
	startErr              error
	enqueueByPathPath     string
	enqueueByPathSource   string
	enqueueByPathPriority int
	enqueueByPathCount    int
	enqueueByPathErr      error
	updateCategoryPerson  uint
	updateCategoryValue   string
	updateNamePerson      uint
	updateNameValue       string
	updateAvatarPerson    uint
	updateAvatarFace      uint
	mergeTargetPerson     uint
	mergeSourcePeople     []uint
	splitFaceIDs          []uint
	splitSourcePerson     uint
	splitResult           *model.Person
	moveFaceIDs           []uint
	moveTargetPerson      uint
	err                   error
}

func (s *stubPeopleService) StartBackground() (*model.PeopleTask, error) {
	s.startCalled++
	if s.startErr != nil {
		return nil, s.startErr
	}
	if s.startResult != nil {
		return s.startResult, nil
	}
	return &model.PeopleTask{Status: model.TaskStatusRunning}, nil
}
func (s *stubPeopleService) StopBackground() error            { return nil }
func (s *stubPeopleService) GetTaskStatus() *model.PeopleTask { return s.task }
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
func (s *stubPeopleService) EnqueueByPath(path string, source string, priority int) (int, error) {
	s.enqueueByPathPath = path
	s.enqueueByPathSource = source
	s.enqueueByPathPriority = priority
	if s.enqueueByPathErr != nil {
		return 0, s.enqueueByPathErr
	}
	return s.enqueueByPathCount, nil
}
func (s *stubPeopleService) EnqueueUnprocessed() (int, error) {
	return 0, nil
}
func (s *stubPeopleService) MergePeople(targetPersonID uint, sourcePersonIDs []uint) (*model.ReclusterResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.mergeTargetPerson = targetPersonID
	s.mergeSourcePeople = append([]uint(nil), sourcePersonIDs...)
	return &model.ReclusterResult{}, nil
}
func (s *stubPeopleService) SplitPerson(sourcePersonID uint, faceIDs []uint) (*model.Person, *model.ReclusterResult, error) {
	s.splitSourcePerson = sourcePersonID
	if s.err != nil {
		return nil, nil, s.err
	}
	s.splitFaceIDs = append([]uint(nil), faceIDs...)
	if s.splitResult != nil {
		return s.splitResult, &model.ReclusterResult{}, nil
	}
	return &model.Person{ID: 99, Category: model.PersonCategoryStranger}, &model.ReclusterResult{}, nil
}
func (s *stubPeopleService) MoveFaces(faceIDs []uint, targetPersonID uint) (*model.ReclusterResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.moveFaceIDs = append([]uint(nil), faceIDs...)
	s.moveTargetPerson = targetPersonID
	return &model.ReclusterResult{}, nil
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
func (s *stubPeopleService) HandleShutdown() error        { return nil }
func (s *stubPeopleService) ResetAllPeople() (int, error) { return 0, nil }
func (s *stubPeopleService) DissolvePerson(_ uint) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	return 5, nil
}
func (s *stubPeopleService) ApplyDetectionResult(_ *model.PeopleJob, _ *model.Photo, _ *model.PeopleDetectionResult) error {
	return nil
}
func (s *stubPeopleService) MergePeopleAsync(targetPersonID uint, sourcePersonIDs []uint, jobType string) (uint, error) {
	if s.err != nil {
		return 0, s.err
	}
	s.mergeTargetPerson = targetPersonID
	s.mergeSourcePeople = append([]uint(nil), sourcePersonIDs...)
	return 1, nil
}
func (s *stubPeopleService) GetMergeJobStatus(jobID uint) (*model.PeopleMergeJob, error) {
	return &model.PeopleMergeJob{ID: jobID, Status: model.PeopleMergeJobStatusCompleted}, nil
}
func (s *stubPeopleService) AssignFacePerson(_ uint, _ model.FacePersonAssignmentRequest) (uint, error) {
	if s.err != nil {
		return 0, s.err
	}
	return 1, nil
}

func (s *stubPeopleService) UpdateFaceExclusion(_ []uint, _ bool, _ string) (*model.FaceExclusionResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &model.FaceExclusionResult{Updated: 0}, nil
}

// stubIdentityProfileService 是 PersonIdentityProfileService 的最小桩，用于 handler 测试。
// legacy 模式返回零值运行状态；可注入 err 触发 500。
type stubIdentityProfileService struct {
	mode           string
	stats          *model.IdentityProfileOperationalStatsResponse
	decisions      []model.IdentityDecisionResponse
	statsErr       error
	decisionsErr   error
	statsCalls     int
	decisionsCalls int
	lastLimit      int
}

func (s *stubIdentityProfileService) MarkDirty([]uint, string) error { return nil }
func (s *stubIdentityProfileService) Invalidate(service.IdentityProfileInvalidation) error {
	return nil
}
func (s *stubIdentityProfileService) RunBackgroundSlice() error { return nil }
func (s *stubIdentityProfileService) GetActive(uint) (*model.PersonIdentityProfileBuild, error) {
	return nil, nil
}
func (s *stubIdentityProfileService) GetStats() (*model.PersonIdentityProfileStats, error) {
	return &model.PersonIdentityProfileStats{}, nil
}
func (s *stubIdentityProfileService) GetOperationalStats(_ repository.PeopleIdentityDecisionRepository) (*model.IdentityProfileOperationalStatsResponse, error) {
	s.statsCalls++
	if s.statsErr != nil {
		return nil, s.statsErr
	}
	if s.stats != nil {
		return s.stats, nil
	}
	mode := s.mode
	if mode == "" {
		mode = "legacy"
	}
	return &model.IdentityProfileOperationalStatsResponse{Mode: mode}, nil
}
func (s *stubIdentityProfileService) ListRecentDecisions(limit int, _ repository.PeopleIdentityDecisionRepository) ([]model.IdentityDecisionResponse, error) {
	s.decisionsCalls++
	s.lastLimit = limit
	if s.decisionsErr != nil {
		return nil, s.decisionsErr
	}
	if s.decisions == nil {
		return []model.IdentityDecisionResponse{}, nil
	}
	return s.decisions, nil
}
func (s *stubIdentityProfileService) Mode() string {
	if s.mode == "" {
		return "legacy"
	}
	return s.mode
}

type stubMergeSuggestionService struct {
	task              *model.PersonMergeSuggestionTask
	stats             *model.PersonMergeSuggestionStatsResponse
	logs              []string
	pending           []model.PersonMergeSuggestionResponse
	pendingTotal      int64
	detail            *model.PersonMergeSuggestionResponse
	listPage          int
	listPageSize      int
	detailID          uint
	excludeID         uint
	excludeCandidates []uint
	applyID           uint
	applyCandidates   []uint
	pauseCalled       int
	resumeCalled      int
	rebuildCalled     int
	err               error
}

func (s *stubMergeSuggestionService) GetTask() *model.PersonMergeSuggestionTask {
	return s.task
}

func (s *stubMergeSuggestionService) GetStats() (*model.PersonMergeSuggestionStatsResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.stats, nil
}

func (s *stubMergeSuggestionService) GetBackgroundLogs() []string {
	return s.logs
}

func (s *stubMergeSuggestionService) Pause() error {
	s.pauseCalled++
	return s.err
}

func (s *stubMergeSuggestionService) Resume() error {
	s.resumeCalled++
	return s.err
}

func (s *stubMergeSuggestionService) Rebuild() error {
	s.rebuildCalled++
	return s.err
}

func (s *stubMergeSuggestionService) MarkDirty(string) error {
	return nil
}

func (s *stubMergeSuggestionService) RunBackgroundSlice() error {
	return nil
}

func (s *stubMergeSuggestionService) ExcludeCandidates(suggestionID uint, candidateIDs []uint) error {
	s.excludeID = suggestionID
	s.excludeCandidates = append([]uint(nil), candidateIDs...)
	return s.err
}

func (s *stubMergeSuggestionService) ApplySuggestion(suggestionID uint, candidateIDs []uint) error {
	s.applyID = suggestionID
	s.applyCandidates = append([]uint(nil), candidateIDs...)
	return s.err
}

func (s *stubMergeSuggestionService) ListPending(page, pageSize int) ([]model.PersonMergeSuggestionResponse, int64, error) {
	s.listPage = page
	s.listPageSize = pageSize
	if s.err != nil {
		return nil, 0, s.err
	}
	return append([]model.PersonMergeSuggestionResponse(nil), s.pending...), s.pendingTotal, nil
}

func (s *stubMergeSuggestionService) GetPendingByID(id uint) (*model.PersonMergeSuggestionResponse, error) {
	s.detailID = id
	if s.err != nil {
		return nil, s.err
	}
	return s.detail, nil
}

func (s *stubMergeSuggestionService) CalculateSimilarity(personID1, personID2 uint) (float64, error) {
	return 0.75, s.err
}

func (s *stubMergeSuggestionService) MergeSuggestionThreshold() float64 {
	return 0.62
}

func (s *stubMergeSuggestionService) AttachThreshold() float64 {
	return 0.70
}

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

type peopleRescanPayload struct {
	Count             int  `json:"count"`
	BackgroundStarted bool `json:"background_started"`
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

func newPeopleHandlerForTest(t *testing.T) (*PeopleHandler, *stubPeopleService, *stubMergeSuggestionService, *gorm.DB, *config.Config) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.Person{}, &model.Face{}, &model.PeopleJob{}, &model.AnalysisRuntimeLease{}, &model.PeopleIdentityDecision{}))

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
	mergeSuggestionStub := &stubMergeSuggestionService{
		task:  &model.PersonMergeSuggestionTask{Status: model.TaskStatusIdle, ProcessedPairs: 7},
		stats: &model.PersonMergeSuggestionStatsResponse{Total: 3, Pending: 1, Applied: 1, Dismissed: 1, PendingItems: 2},
		logs:  []string{"merge-line-1", "merge-line-2"},
	}

	handler := NewPeopleHandler(
		serviceStub,
		mergeSuggestionStub,
		repository.NewPersonRepository(db),
		repository.NewFaceRepository(db),
		repository.NewPhotoRepository(db),
		repository.NewPeopleJobRepository(db),
		&stubIdentityProfileService{},
		repository.NewPeopleIdentityDecisionRepository(db),
		cfg,
	)

	return handler, serviceStub, mergeSuggestionStub, db, cfg
}

func newPeopleHandlerWithRuntimeForTest(t *testing.T) (*PeopleHandler, service.AnalysisRuntimeService, *gorm.DB) {
	t.Helper()

	handler, _, _, db, _ := newPeopleHandlerForTest(t)
	runtimeService := service.NewAnalysisRuntimeService(db)
	handler.runtimeService = runtimeService
	return handler, runtimeService, db
}

func performWorkerRequest(
	t *testing.T,
	method string,
	path string,
	body []byte,
	params gin.Params,
	headers map[string]string,
	deviceID uint,
	fn func(*gin.Context),
) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = params
	ctx.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		ctx.Request.Header.Set(key, value)
	}
	if deviceID != 0 {
		ctx.Set("device_id", deviceID)
	}

	fn(ctx)
	return recorder
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
	handler, _, _, db, _ := newPeopleHandlerForTest(t)
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

func TestPeopleHandler_GetPeopleIncludesHasAvatar(t *testing.T) {
	handler, _, _, db, _ := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)

	noAvatar := model.Person{
		Name:       "NoAvatar",
		Category:   model.PersonCategoryStranger,
		FaceCount:  0,
		PhotoCount: 0,
	}
	require.NoError(t, db.Create(&noAvatar).Error)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people?page=1&page_size=20", nil, nil, handler.ListPeople)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	payload := decodeResponseData[peopleListPayload](t, resp)
	require.NotEmpty(t, payload.Items)

	itemsByID := make(map[uint]model.PersonResponse, len(payload.Items))
	for _, item := range payload.Items {
		itemsByID[item.ID] = item
	}

	require.Contains(t, itemsByID, fixture.FamilyPerson.ID)
	assert.True(t, itemsByID[fixture.FamilyPerson.ID].HasAvatar)
	require.Contains(t, itemsByID, noAvatar.ID)
	assert.False(t, itemsByID[noAvatar.ID].HasAvatar)
}

func TestPeopleHandlerGetPerson(t *testing.T) {
	handler, _, _, db, _ := newPeopleHandlerForTest(t)
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

func TestPeopleHandler_GetPersonIncludesHasAvatar(t *testing.T) {
	handler, _, _, db, _ := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)

	noAvatar := model.Person{
		Name:       "NoAvatar",
		Category:   model.PersonCategoryStranger,
		FaceCount:  0,
		PhotoCount: 0,
	}
	require.NoError(t, db.Create(&noAvatar).Error)

	t.Run("person with representative face has avatar", func(t *testing.T) {
		rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/1", nil, gin.Params{{Key: "id", Value: "1"}}, handler.GetPerson)

		require.Equal(t, http.StatusOK, rec.Code)
		resp := decodeAPIResponse(t, rec)
		require.True(t, resp.Success)
		person := decodeResponseData[model.PersonResponse](t, resp)
		assert.Equal(t, fixture.FamilyPerson.ID, person.ID)
		assert.True(t, person.HasAvatar)
	})

	t.Run("person without representative face has no avatar", func(t *testing.T) {
		rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/"+strconv.FormatUint(uint64(noAvatar.ID), 10), nil, gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(noAvatar.ID), 10)}}, handler.GetPerson)

		require.Equal(t, http.StatusOK, rec.Code)
		resp := decodeAPIResponse(t, rec)
		require.True(t, resp.Success)
		person := decodeResponseData[model.PersonResponse](t, resp)
		assert.Equal(t, noAvatar.ID, person.ID)
		assert.False(t, person.HasAvatar)
	})
}

func TestPeopleHandlerGetPersonPhotos(t *testing.T) {
	handler, _, _, db, _ := newPeopleHandlerForTest(t)
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
	handler, _, _, db, _ := newPeopleHandlerForTest(t)
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
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/7/category", []byte(`{"category":"friend"}`), gin.Params{{Key: "id", Value: "7"}}, handler.UpdatePersonCategory)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(7), svc.updateCategoryPerson)
	assert.Equal(t, model.PersonCategoryFriend, svc.updateCategoryValue)
}

func TestPeopleHandlerUpdateName(t *testing.T) {
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/7/name", []byte(`{"name":"Alice Zhang"}`), gin.Params{{Key: "id", Value: "7"}}, handler.UpdatePersonName)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(7), svc.updateNamePerson)
	assert.Equal(t, "Alice Zhang", svc.updateNameValue)
}

func TestPeopleHandlerUpdateAvatar(t *testing.T) {
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/7/avatar", []byte(`{"face_id":12}`), gin.Params{{Key: "id", Value: "7"}}, handler.UpdatePersonAvatar)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(7), svc.updateAvatarPerson)
	assert.Equal(t, uint(12), svc.updateAvatarFace)
}

func TestPeopleHandlerMerge(t *testing.T) {
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/merge", []byte(`{"target_person_id":3,"source_person_ids":[4,5]}`), nil, handler.MergePeople)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(3), svc.mergeTargetPerson)
	assert.Equal(t, []uint{4, 5}, svc.mergeSourcePeople)
}

func TestPeopleHandlerSplit(t *testing.T) {
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)
	svc.splitResult = &model.Person{ID: 55, Category: model.PersonCategoryAcquaintance}

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/split", []byte(`{"source_person_id":7,"face_ids":[8,9]}`), nil, handler.SplitPerson)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(7), svc.splitSourcePerson)
	assert.Equal(t, []uint{8, 9}, svc.splitFaceIDs)
	resp := decodeAPIResponse(t, rec)
	// Split now returns {person: ..., recluster_*: ...}
	dataMap := decodeResponseData[map[string]interface{}](t, resp)
	personJSON, _ := json.Marshal(dataMap["person"])
	var person model.PersonResponse
	require.NoError(t, json.Unmarshal(personJSON, &person))
	assert.Equal(t, uint(55), person.ID)
	assert.Equal(t, model.PersonCategoryAcquaintance, person.Category)
}

// TestPeopleHandlerSplit_MissingSourcePersonID 验证缺少 source_person_id 返回 400。
func TestPeopleHandlerSplit_MissingSourcePersonID(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/split", []byte(`{"face_ids":[8,9]}`), nil, handler.SplitPerson)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.False(t, resp.Success)
	assert.Equal(t, "INVALID_REQUEST", resp.Error.Code)
}

// TestPeopleHandlerSplit_ZeroSourcePersonID 验证 source_person_id=0 返回 400。
func TestPeopleHandlerSplit_ZeroSourcePersonID(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/split", []byte(`{"source_person_id":0,"face_ids":[8,9]}`), nil, handler.SplitPerson)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.False(t, resp.Success)
	assert.Equal(t, "INVALID_REQUEST", resp.Error.Code)
}

// TestPeopleHandlerSplit_ReplayReturnsExistingPerson 验证幂等重放返回 200 且复用已有目标人物。
// handler 用 stub service，无法区分首次与重放；这里通过让 stub 返回一个固定的已存在目标人物
// 来验证 handler 对"返回已有人物（而非新建）"路径的响应契约：仍 200、仍按 person 字段返回。
// 真正的幂等去重逻辑在 service 层（见 people_service_test.go）已充分覆盖。
func TestPeopleHandlerSplit_ReplayReturnsExistingPerson(t *testing.T) {
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)
	// 模拟 service 层幂等命中后返回的已有目标人物。
	svc.splitResult = &model.Person{ID: 42, Category: model.PersonCategoryFamily}

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/split", []byte(`{"source_person_id":7,"face_ids":[8,9]}`), nil, handler.SplitPerson)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)
	dataMap := decodeResponseData[map[string]interface{}](t, resp)
	personJSON, _ := json.Marshal(dataMap["person"])
	var person model.PersonResponse
	require.NoError(t, json.Unmarshal(personJSON, &person))
	assert.Equal(t, uint(42), person.ID, "replay must return the existing target person")
}

// TestPeopleHandlerSplit_ConflictMapsTo409 验证 errPeopleSplitConflict 返回 409
// 且 error code 为 SPLIT_ASSIGNMENT_CONFLICT。
func TestPeopleHandlerSplit_ConflictMapsTo409(t *testing.T) {
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)
	svc.err = service.ErrPeopleSplitConflict

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/split", []byte(`{"source_person_id":7,"face_ids":[8,9]}`), nil, handler.SplitPerson)

	require.Equal(t, http.StatusConflict, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.False(t, resp.Success)
	assert.Equal(t, "SPLIT_ASSIGNMENT_CONFLICT", resp.Error.Code)
}

// TestPeopleHandlerSplit_UnknownErrorMapsTo500 验证未知错误仍返回 500。
func TestPeopleHandlerSplit_UnknownErrorMapsTo500(t *testing.T) {
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)
	svc.err = errors.New("boom")

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/split", []byte(`{"source_person_id":7,"face_ids":[8,9]}`), nil, handler.SplitPerson)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.False(t, resp.Success)
	assert.Equal(t, "OPERATION_FAILED", resp.Error.Code)
}

func TestPeopleHandlerMoveFaces(t *testing.T) {
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/move-faces", []byte(`{"face_ids":[8,9],"target_person_id":6}`), nil, handler.MoveFaces)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []uint{8, 9}, svc.moveFaceIDs)
	assert.Equal(t, uint(6), svc.moveTargetPerson)
}

func TestPeopleHandlerTask(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/task", nil, nil, handler.GetTask)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	task := decodeResponseData[model.PeopleTask](t, resp)
	assert.Equal(t, model.TaskStatusRunning, task.Status)
	assert.Equal(t, int64(3), task.ProcessedJobs)
}

func TestPeopleHandlerStats(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/stats", nil, nil, handler.GetStats)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	stats := decodeResponseData[model.PeopleStatsResponse](t, resp)
	assert.Equal(t, int64(10), stats.Total)
	assert.Equal(t, int64(8), stats.Completed)
}

func TestPeopleHandlerBackgroundLogs(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/background/logs", nil, nil, handler.GetBackgroundLogs)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	payload := decodeResponseData[backgroundLogsPayload](t, resp)
	assert.Equal(t, []string{"line1", "line2"}, payload.Lines)
}

func TestPeopleHandler_GetMergeSuggestionTask(t *testing.T) {
	handler, _, mergeSvc, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/merge-suggestions/task", nil, nil, handler.GetMergeSuggestionTask)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	task := decodeResponseData[model.PersonMergeSuggestionTask](t, resp)
	assert.Equal(t, mergeSvc.task.Status, task.Status)
	assert.Equal(t, mergeSvc.task.ProcessedPairs, task.ProcessedPairs)
}

func TestPeopleHandler_ListMergeSuggestions(t *testing.T) {
	handler, _, mergeSvc, _, _ := newPeopleHandlerForTest(t)
	mergeSvc.pending = []model.PersonMergeSuggestionResponse{
		{ID: 11, Status: model.PersonMergeSuggestionStatusPending, CandidateCount: 2},
	}
	mergeSvc.pendingTotal = 1

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/merge-suggestions?page=2&page_size=5", nil, nil, handler.ListMergeSuggestions)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	payload := decodeResponseData[model.PagedResponse](t, resp)
	itemsJSON, err := json.Marshal(payload.Items)
	require.NoError(t, err)
	var items []model.PersonMergeSuggestionResponse
	require.NoError(t, json.Unmarshal(itemsJSON, &items))
	require.Len(t, items, 1)
	assert.Equal(t, uint(11), items[0].ID)
	assert.Equal(t, 2, mergeSvc.listPage)
	assert.Equal(t, 5, mergeSvc.listPageSize)
}

func TestPeopleHandler_GetMergeSuggestionDetail(t *testing.T) {
	handler, _, mergeSvc, _, _ := newPeopleHandlerForTest(t)
	mergeSvc.detail = &model.PersonMergeSuggestionResponse{
		ID:             21,
		Status:         model.PersonMergeSuggestionStatusPending,
		CandidateCount: 1,
		Items: []model.PersonMergeSuggestionItemResponse{
			{CandidatePersonID: 31, Status: model.PersonMergeSuggestionItemStatusPending},
		},
	}

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/merge-suggestions/21", nil, gin.Params{{Key: "id", Value: "21"}}, handler.GetMergeSuggestion)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	item := decodeResponseData[model.PersonMergeSuggestionResponse](t, resp)
	assert.Equal(t, uint(21), item.ID)
	assert.Equal(t, uint(21), mergeSvc.detailID)
}

func TestPeopleHandler_ExcludeMergeSuggestionCandidates(t *testing.T) {
	handler, _, mergeSvc, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/merge-suggestions/33/exclude", []byte(`{"candidate_person_ids":[7,8]}`), gin.Params{{Key: "id", Value: "33"}}, handler.ExcludeMergeSuggestionCandidates)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(33), mergeSvc.excludeID)
	assert.Equal(t, []uint{7, 8}, mergeSvc.excludeCandidates)
}

func TestPeopleHandler_ApplyMergeSuggestion(t *testing.T) {
	handler, _, mergeSvc, _, _ := newPeopleHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/merge-suggestions/44/apply", []byte(`{"candidate_person_ids":[9,10]}`), gin.Params{{Key: "id", Value: "44"}}, handler.ApplyMergeSuggestion)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, uint(44), mergeSvc.applyID)
	assert.Equal(t, []uint{9, 10}, mergeSvc.applyCandidates)
}

func TestPeopleHandlerRescanByPath(t *testing.T) {
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)
	svc.task = nil
	svc.enqueueByPathCount = 12

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/rescan-by-path", []byte(`{"path":"/photos/family"}`), nil, handler.RescanByPath)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	assert.Equal(t, "/photos/family", svc.enqueueByPathPath)
	assert.Equal(t, model.PeopleJobSourceManual, svc.enqueueByPathSource)
	assert.Equal(t, 80, svc.enqueueByPathPriority)
	payload := decodeResponseData[peopleRescanPayload](t, resp)
	assert.Equal(t, 12, payload.Count)
}

func TestPeopleHandlerGetPhotoPeople(t *testing.T) {
	handler, _, _, db, _ := newPeopleHandlerForTest(t)
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
	handler, _, _, db, cfg := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)
	thumbnailPath := filepath.Join(cfg.Photos.ThumbnailPath, fixture.FaceOne.ThumbnailPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(thumbnailPath), 0o755))
	require.NoError(t, os.WriteFile(thumbnailPath, []byte("face-thumb"), 0o644))

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/faces/1/thumbnail", nil, gin.Params{{Key: "id", Value: "1"}}, handler.GetFaceThumbnail)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "face-thumb", rec.Body.String())
	// 人脸缩略图内容稳定（文件名由 face id + bbox + 原图路径派生），返回长期私有浏览器缓存。
	// private 防止共享缓存泄露受保护图片；immutable 配合前端版本化 URL。
	assert.Equal(t, "private, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
}

// TestPeopleHandlerGetFaceThumbnailCachePrivate 断言人脸缩略图缓存为私有浏览器缓存，
// 不会进入共享缓存，避免受保护图片被中间代理缓存泄露。
func TestPeopleHandlerGetFaceThumbnailCachePrivate(t *testing.T) {
	handler, _, _, db, cfg := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)
	thumbnailPath := filepath.Join(cfg.Photos.ThumbnailPath, fixture.FaceOne.ThumbnailPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(thumbnailPath), 0o755))
	require.NoError(t, os.WriteFile(thumbnailPath, []byte("face-thumb"), 0o644))

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/faces/1/thumbnail", nil, gin.Params{{Key: "id", Value: "1"}}, handler.GetFaceThumbnail)
	require.Equal(t, http.StatusOK, rec.Code)
	cacheControl := rec.Header().Get("Cache-Control")
	assert.Contains(t, cacheControl, "private")
	assert.NotContains(t, cacheControl, "public")
}

func TestPeopleHandlerGetFaceThumbnailGeneratesMissingCrop(t *testing.T) {
	handler, _, _, db, cfg := newPeopleHandlerForTest(t)
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
	handler, svc, _, _, _ := newPeopleHandlerForTest(t)
	svc.err = errors.New("stats failed")

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/stats", nil, nil, handler.GetStats)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPeopleHandlerAcquirePeopleRuntime(t *testing.T) {
	handler, runtimeService, _ := newPeopleHandlerWithRuntimeForTest(t)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/runtime/acquire", []byte(`{"worker_id":"worker-1"}`), nil, handler.AcquirePeopleRuntime)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	lease := decodeResponseData[model.PeopleWorkerRuntimeLeaseResponse](t, resp)
	assert.False(t, lease.LeaseExpiresAt.IsZero())

	status, err := runtimeService.GetStatus(model.GlobalPeopleResourceKey)
	require.NoError(t, err)
	require.True(t, status.IsActive)
	assert.Equal(t, model.AnalysisOwnerTypePeopleWorker, status.OwnerType)
	assert.Equal(t, "worker-1", status.OwnerID)
}

func TestPeopleHandlerAcquirePeopleRuntimeConflict(t *testing.T) {
	handler, runtimeService, _ := newPeopleHandlerWithRuntimeForTest(t)
	_, err := runtimeService.Acquire(model.GlobalPeopleResourceKey, model.AnalysisOwnerTypeBackground, "local", "local background task")
	require.NoError(t, err)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/runtime/acquire", []byte(`{"worker_id":"worker-1"}`), nil, handler.AcquirePeopleRuntime)

	require.Equal(t, http.StatusConflict, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.False(t, resp.Success)
	status := decodeResponseData[model.AnalysisRuntimeStatusResponse](t, resp)
	assert.Equal(t, model.AnalysisOwnerTypeBackground, status.OwnerType)
	assert.Equal(t, "local", status.OwnerID)
}

func TestPeopleHandlerPeopleRuntimeHeartbeatRequiresOwner(t *testing.T) {
	handler, runtimeService, _ := newPeopleHandlerWithRuntimeForTest(t)
	_, err := runtimeService.Acquire(model.GlobalPeopleResourceKey, model.AnalysisOwnerTypePeopleWorker, "worker-1", "worker one")
	require.NoError(t, err)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/runtime/heartbeat", []byte(`{"worker_id":"worker-2"}`), nil, handler.HeartbeatPeopleRuntime)

	require.Equal(t, http.StatusConflict, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.False(t, resp.Success)
	status := decodeResponseData[model.AnalysisRuntimeStatusResponse](t, resp)
	assert.Equal(t, "worker-1", status.OwnerID)
}

func TestPeopleHandlerPeopleRuntimeReleaseRequiresOwner(t *testing.T) {
	handler, runtimeService, _ := newPeopleHandlerWithRuntimeForTest(t)
	_, err := runtimeService.Acquire(model.GlobalPeopleResourceKey, model.AnalysisOwnerTypePeopleWorker, "worker-1", "worker one")
	require.NoError(t, err)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/people/runtime/release", []byte(`{"worker_id":"worker-2"}`), nil, handler.ReleasePeopleRuntime)

	require.Equal(t, http.StatusConflict, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.False(t, resp.Success)

	status, err := runtimeService.GetStatus(model.GlobalPeopleResourceKey)
	require.NoError(t, err)
	require.True(t, status.IsActive)
	assert.Equal(t, "worker-1", status.OwnerID)
}

func TestPeopleHandlerGetWorkerTasksRequiresRuntimeLease(t *testing.T) {
	handler, _, _ := newPeopleHandlerWithRuntimeForTest(t)

	rec := performWorkerRequest(
		t,
		http.MethodGet,
		"/api/v1/people/worker/tasks?limit=1",
		nil,
		nil,
		map[string]string{"X-Worker-ID": "worker-1"},
		1,
		handler.GetWorkerTasks,
	)

	require.Equal(t, http.StatusConflict, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.False(t, resp.Success)
}

// TestPeopleHandler_ListPeopleVisibility 验证 visibility 查询参数与响应 hidden 字段。
func TestPeopleHandler_ListPeopleVisibility(t *testing.T) {
	handler, _, _, db, _ := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)

	// 将 friend 标记为隐藏
	require.NoError(t, db.Model(&model.Person{}).Where("id = ?", fixture.FriendPerson.ID).Update("hidden", true).Error)

	// visible：仅返回 family（显示中）
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people?visibility=visible&page=1&page_size=20", nil, nil, handler.ListPeople)
	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	payload := decodeResponseData[peopleListPayload](t, resp)
	assert.Equal(t, int64(1), payload.Total)
	require.Len(t, payload.Items, 1)
	assert.Equal(t, fixture.FamilyPerson.ID, payload.Items[0].ID)
	assert.False(t, payload.Items[0].Hidden)

	// hidden：仅返回 friend（已隐藏）
	rec = performJSONRequest(t, http.MethodGet, "/api/v1/people?visibility=hidden&page=1&page_size=20", nil, nil, handler.ListPeople)
	require.Equal(t, http.StatusOK, rec.Code)
	resp = decodeAPIResponse(t, rec)
	payload = decodeResponseData[peopleListPayload](t, resp)
	assert.Equal(t, int64(1), payload.Total)
	require.Len(t, payload.Items, 1)
	assert.Equal(t, fixture.FriendPerson.ID, payload.Items[0].ID)
	assert.True(t, payload.Items[0].Hidden)

	// all：返回全部
	rec = performJSONRequest(t, http.MethodGet, "/api/v1/people?visibility=all&page=1&page_size=20", nil, nil, handler.ListPeople)
	require.Equal(t, http.StatusOK, rec.Code)
	resp = decodeAPIResponse(t, rec)
	payload = decodeResponseData[peopleListPayload](t, resp)
	assert.Equal(t, int64(2), payload.Total)
	assert.Len(t, payload.Items, 2)

	// 缺省按 all 处理
	rec = performJSONRequest(t, http.MethodGet, "/api/v1/people?page=1&page_size=20", nil, nil, handler.ListPeople)
	require.Equal(t, http.StatusOK, rec.Code)
	resp = decodeAPIResponse(t, rec)
	payload = decodeResponseData[peopleListPayload](t, resp)
	assert.Equal(t, int64(2), payload.Total)
}

// TestPeopleHandler_UpdateVisibility 验证批量隐藏/恢复、参数校验、不存在 ID 行为及副作用隔离。
func TestPeopleHandler_UpdateVisibility(t *testing.T) {
	handler, _, _, db, _ := newPeopleHandlerForTest(t)
	fixture := seedPeopleHandlerFixture(t, db)

	// 记录 family 原始分类，用于验证隐藏操作不修改分类
	originalCategory := fixture.FamilyPerson.Category

	t.Run("batch hide then restore", func(t *testing.T) {
		body := []byte(`{"person_ids":[` + strconv.FormatUint(uint64(fixture.FamilyPerson.ID), 10) + `],"hidden":true}`)
		rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/visibility", body, nil, handler.UpdateVisibility)
		require.Equal(t, http.StatusOK, rec.Code)
		resp := decodeAPIResponse(t, rec)
		require.True(t, resp.Success)
		data := decodeResponseData[map[string]interface{}](t, resp)
		assert.EqualValues(t, 1, data["updated"])

		// 验证 DB：hidden=true，category 未变
		var got model.Person
		require.NoError(t, db.First(&got, fixture.FamilyPerson.ID).Error)
		assert.True(t, got.Hidden)
		assert.Equal(t, originalCategory, got.Category)

		// 恢复
		body = []byte(`{"person_ids":[` + strconv.FormatUint(uint64(fixture.FamilyPerson.ID), 10) + `],"hidden":false}`)
		rec = performJSONRequest(t, http.MethodPatch, "/api/v1/people/visibility", body, nil, handler.UpdateVisibility)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, db.First(&got, fixture.FamilyPerson.ID).Error)
		assert.False(t, got.Hidden)
	})

	t.Run("dedup and missing ids", func(t *testing.T) {
		// 部分不存在：更新存在的，返回 updated=1，missing_count=1
		body := []byte(`{"person_ids":[` + strconv.FormatUint(uint64(fixture.FriendPerson.ID), 10) + `,999999],"hidden":true}`)
		rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/visibility", body, nil, handler.UpdateVisibility)
		require.Equal(t, http.StatusOK, rec.Code)
		resp := decodeAPIResponse(t, rec)
		require.True(t, resp.Success)
		data := decodeResponseData[map[string]interface{}](t, resp)
		assert.EqualValues(t, 1, data["updated"])
		assert.EqualValues(t, 1, data["missing_count"])

		// 全部不存在：404
		body = []byte(`{"person_ids":[999998,999999],"hidden":true}`)
		rec = performJSONRequest(t, http.MethodPatch, "/api/v1/people/visibility", body, nil, handler.UpdateVisibility)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("empty person_ids rejected", func(t *testing.T) {
		body := []byte(`{"person_ids":[],"hidden":true}`)
		rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/visibility", body, nil, handler.UpdateVisibility)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing hidden field rejected", func(t *testing.T) {
		body := []byte(`{"person_ids":[` + strconv.FormatUint(uint64(fixture.FamilyPerson.ID), 10) + `]}`)
		rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/visibility", body, nil, handler.UpdateVisibility)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("hidden does not change top_person_category", func(t *testing.T) {
		// 记录 photoOne 的 top_person_category，隐藏 family 后应保持不变
		var photoBefore model.Photo
		require.NoError(t, db.First(&photoBefore, fixture.PhotoOne.ID).Error)
		categoryBefore := photoBefore.TopPersonCategory

		body := []byte(`{"person_ids":[` + strconv.FormatUint(uint64(fixture.FamilyPerson.ID), 10) + `],"hidden":true}`)
		rec := performJSONRequest(t, http.MethodPatch, "/api/v1/people/visibility", body, nil, handler.UpdateVisibility)
		require.Equal(t, http.StatusOK, rec.Code)

		var photoAfter model.Photo
		require.NoError(t, db.First(&photoAfter, fixture.PhotoOne.ID).Error)
		assert.Equal(t, categoryBefore, photoAfter.TopPersonCategory)
	})
}

// ==================== Identity Profile operational stats (Task 14) ====================

func TestPeopleHandler_GetIdentityProfileStats_Legacy(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)
	ips := &stubIdentityProfileService{mode: "legacy"}
	handler.identityProfileService = ips

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/identity-profiles/stats", nil, nil, handler.GetIdentityProfileStats)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	stats := decodeResponseData[model.IdentityProfileOperationalStatsResponse](t, resp)
	assert.Equal(t, "legacy", stats.Mode)
	assert.Zero(t, stats.Profiles.Total)
	assert.False(t, stats.ANN.Ready)
	assert.Equal(t, 1, ips.statsCalls)
}

func TestPeopleHandler_GetIdentityProfileStats_Error(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)
	ips := &stubIdentityProfileService{statsErr: errors.New("db down")}
	handler.identityProfileService = ips

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/identity-profiles/stats", nil, nil, handler.GetIdentityProfileStats)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.False(t, resp.Success)
	assert.Equal(t, "IDENTITY_PROFILE_STATS_FAILED", resp.Error.Code)
	// 不暴露原始错误。
	assert.NotContains(t, resp.Error.Message, "db down")
}

func TestPeopleHandler_ListIdentityProfileDecisions_DefaultLimit(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)
	ips := &stubIdentityProfileService{
		decisions: []model.IdentityDecisionResponse{
			{ID: 1, Decision: "agree", CenterIDs: []uint{1, 2}},
			{ID: 2, Decision: "disagree"},
		},
	}
	handler.identityProfileService = ips

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/identity-profiles/decisions", nil, nil, handler.ListIdentityProfileDecisions)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	data := decodeResponseData[model.IdentityDecisionListResponse](t, resp)
	assert.Equal(t, 50, data.Limit, "default limit is 50")
	assert.Len(t, data.Items, 2)
	assert.Equal(t, []uint{1, 2}, data.Items[0].CenterIDs)
}

func TestPeopleHandler_ListIdentityProfileDecisions_LimitClampedTo200(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)
	ips := &stubIdentityProfileService{}
	handler.identityProfileService = ips

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/identity-profiles/decisions?limit=201", nil, nil, handler.ListIdentityProfileDecisions)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
	data := decodeResponseData[model.IdentityDecisionListResponse](t, resp)
	assert.Equal(t, 200, data.Limit, "limit 201 clamped to 200")
	assert.Equal(t, 200, ips.lastLimit)
}

func TestPeopleHandler_ListIdentityProfileDecisions_Limit1(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)
	ips := &stubIdentityProfileService{}
	handler.identityProfileService = ips

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/identity-profiles/decisions?limit=1", nil, nil, handler.ListIdentityProfileDecisions)

	require.Equal(t, http.StatusOK, rec.Code)
	data := decodeResponseData[model.IdentityDecisionListResponse](t, decodeAPIResponse(t, rec))
	assert.Equal(t, 1, data.Limit)
}

func TestPeopleHandler_ListIdentityProfileDecisions_InvalidLimit400(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)
	ips := &stubIdentityProfileService{}
	handler.identityProfileService = ips

	for _, q := range []string{"0", "-1", "abc", "1.5"} {
		rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/identity-profiles/decisions?limit="+q, nil, nil, handler.ListIdentityProfileDecisions)
		require.Equalf(t, http.StatusBadRequest, rec.Code, "limit=%s should be 400", q)
		resp := decodeAPIResponse(t, rec)
		assert.Equal(t, "INVALID_LIMIT", resp.Error.Code)
	}
	assert.Equal(t, 0, ips.decisionsCalls, "invalid limit must not call service")
}

func TestPeopleHandler_ListIdentityProfileDecisions_EmptyTableReturnsEmptyArray(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)
	ips := &stubIdentityProfileService{}
	handler.identityProfileService = ips

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/identity-profiles/decisions", nil, nil, handler.ListIdentityProfileDecisions)

	require.Equal(t, http.StatusOK, rec.Code)
	data := decodeResponseData[model.IdentityDecisionListResponse](t, decodeAPIResponse(t, rec))
	assert.NotNil(t, data.Items)
	assert.Empty(t, data.Items)
}

func TestPeopleHandler_ListIdentityProfileDecisions_RepositoryError500(t *testing.T) {
	handler, _, _, _, _ := newPeopleHandlerForTest(t)
	ips := &stubIdentityProfileService{decisionsErr: errors.New("repo down")}
	handler.identityProfileService = ips

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/people/identity-profiles/decisions", nil, nil, handler.ListIdentityProfileDecisions)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.Equal(t, "IDENTITY_DECISIONS_FAILED", resp.Error.Code)
	assert.NotContains(t, resp.Error.Message, "repo down")
}
