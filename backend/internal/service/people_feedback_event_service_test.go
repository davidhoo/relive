package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingFeedbackEventRepo is a stub whose Create always fails, used to verify
// that an auxiliary feedback recording failure never breaks a committed operation.
type failingFeedbackEventRepo struct {
	calls int
	err   error
}

func (r *failingFeedbackEventRepo) Create(*model.PeopleFeedbackEvent) error {
	r.calls++
	return r.err
}

func (r *failingFeedbackEventRepo) ListForCalibration(uint, int) ([]*model.PeopleFeedbackEvent, error) {
	return nil, nil
}

func (r *failingFeedbackEventRepo) FindByEventTypeTargetAndFaceIDs(string, uint, string) ([]*model.PeopleFeedbackEvent, error) {
	return nil, nil
}

// failingRecomputePhotoRepo embeds a real PhotoRepository and overrides only
// RecomputeTopPersonCategory to fail, simulating a post-core-processing failure.
type failingRecomputePhotoRepo struct {
	repository.PhotoRepository
	fail bool
}

func (r *failingRecomputePhotoRepo) RecomputeTopPersonCategory(ids []uint) error {
	if r.fail {
		return errors.New("simulated recompute failure")
	}
	return r.PhotoRepository.RecomputeTopPersonCategory(ids)
}

// feedbackEventsFromSvc reads all persisted feedback events in ascending ID order.
func feedbackEventsFromSvc(t *testing.T, svc *peopleService) []*model.PeopleFeedbackEvent {
	t.Helper()
	repo := repository.NewPeopleFeedbackEventRepository(svc.db)
	events, err := repo.ListForCalibration(0, 1000)
	require.NoError(t, err)
	return events
}

// seedTwoPersons creates target+source persons each with one face on distinct photos.
func seedTwoPersons(t *testing.T, svc *peopleService) (target, source *model.Person, targetFace, sourceFace *model.Face) {
	t.Helper()
	photoRepo := repository.NewPhotoRepository(svc.db)
	personRepo := repository.NewPersonRepository(svc.db)
	faceRepo := repository.NewFaceRepository(svc.db)

	targetPhoto := &model.Photo{FilePath: "/fb/target.jpg", FileName: "target.jpg", FileSize: 1, FileHash: "target", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	sourcePhoto := &model.Photo{FilePath: "/fb/source.jpg", FileName: "source.jpg", FileSize: 1, FileHash: "source", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(targetPhoto))
	require.NoError(t, photoRepo.Create(sourcePhoto))

	target = &model.Person{Category: model.PersonCategoryFamily}
	source = &model.Person{Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(source))

	targetFace = &model.Face{
		PhotoID: targetPhoto.ID, PersonID: &target.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{1, 0, 0}),
	}
	sourceFace = &model.Face{
		PhotoID: sourcePhoto.ID, PersonID: &source.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.8, Embedding: encodeEmbedding(t, []float32{0, 1, 0}),
	}
	require.NoError(t, faceRepo.Create(targetFace))
	require.NoError(t, faceRepo.Create(sourceFace))
	require.NoError(t, personRepo.RefreshStats(target.ID))
	require.NoError(t, personRepo.RefreshStats(source.ID))
	return target, source, targetFace, sourceFace
}

func TestEmitsFeedbackMergeConfirmedManualMerge(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	target, source, _, _ := seedTwoPersons(t, svc)

	_, err := svc.MergePeople(target.ID, []uint{source.ID})
	require.NoError(t, err)

	events := feedbackEventsFromSvc(t, svc)
	require.Len(t, events, 1)
	assert.Equal(t, repository.PeopleFeedbackEventMergeConfirmed, events[0].EventType)
	assert.Equal(t, target.ID, events[0].TargetPersonID)
	assert.Equal(t, fmt.Sprintf("[%d]", source.ID), events[0].SourcePersonIDs)
	assert.Equal(t, "[]", events[0].FaceIDs)
	assert.Equal(t, "manual", events[0].AlgorithmVersion)
	assert.Equal(t, "{}", events[0].SimilaritySnapshot)
}

func TestEmitsFeedbackMergeConfirmedNoDuplicateOnAsyncMerge(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	target, source, _, _ := seedTwoPersons(t, svc)

	jobID, err := svc.MergePeopleAsync(target.ID, []uint{source.ID}, model.PeopleMergeJobTypeMergeInto)
	require.NoError(t, err)

	// 异步合并最终调用 MergePeople，只记录一条事件，不重复写第二条。
	require.Eventually(t, func() bool {
		job, statErr := svc.GetMergeJobStatus(jobID)
		if statErr != nil {
			return false
		}
		return job.Status == model.PeopleMergeJobStatusCompleted
	}, 3*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		return len(feedbackEventsFromSvc(t, svc)) >= 1
	}, 3*time.Second, 20*time.Millisecond)

	events := feedbackEventsFromSvc(t, svc)
	require.Len(t, events, 1, "async merge must emit exactly one merge_confirmed event")
	assert.Equal(t, repository.PeopleFeedbackEventMergeConfirmed, events[0].EventType)
}

func TestEmitsFeedbackCoreFailureRecordsNothing(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	// 目标人物不存在 → MergeInto 返回 ErrRecordNotFound → 核心未提交 → 无事件。
	_, err := svc.MergePeople(99999, []uint{88888})
	require.Error(t, err)

	events := feedbackEventsFromSvc(t, svc)
	assert.Empty(t, events, "no event when core change never committed")
}

func TestEmitsFeedbackRecordsEventEvenWhenPostProcessingFails(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	target, source, _, _ := seedTwoPersons(t, svc)

	// 注入在核心合并提交后才触发的后处理失败。
	originalPhotoRepo := svc.photoRepo
	svc.photoRepo = &failingRecomputePhotoRepo{PhotoRepository: originalPhotoRepo, fail: true}
	t.Cleanup(func() { svc.photoRepo = originalPhotoRepo })

	_, err := svc.MergePeople(target.ID, []uint{source.ID})
	require.Error(t, err, "post-processing RecomputeTopPersonCategory fails")

	events := feedbackEventsFromSvc(t, svc)
	require.Len(t, events, 1, "core committed → event recorded despite post-processing failure")
	assert.Equal(t, repository.PeopleFeedbackEventMergeConfirmed, events[0].EventType)
}

func TestEmitsFeedbackRepositoryFailureDoesNotBreakBusiness(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	failing := &failingFeedbackEventRepo{err: errors.New("disk full")}
	svc.SetFeedbackEventRepo(failing)

	target, source, sourceFace, _ := seedTwoPersons(t, svc)

	_, err := svc.MergePeople(target.ID, []uint{source.ID})
	require.NoError(t, err, "business result unaffected by feedback repo failure")
	assert.GreaterOrEqual(t, failing.calls, 1, "feedback Create was attempted")

	// 业务事实仍然成立：source 人脸已归属 target。
	faceRepo := repository.NewFaceRepository(svc.db)
	updated, err := faceRepo.GetByID(sourceFace.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.PersonID)
	assert.Equal(t, target.ID, *updated.PersonID)
}

func TestEmitsFeedbackMoveFacesRecordsSourceTargetAndFaceIDs(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	target, source, _, sourceFace := seedTwoPersons(t, svc)

	_, err := svc.MoveFaces([]uint{sourceFace.ID}, target.ID)
	require.NoError(t, err)

	events := feedbackEventsFromSvc(t, svc)
	require.Len(t, events, 1)
	ev := events[0]
	assert.Equal(t, repository.PeopleFeedbackEventFaceMoved, ev.EventType)
	assert.Equal(t, target.ID, ev.TargetPersonID)
	assert.Equal(t, fmt.Sprintf("[%d]", source.ID), ev.SourcePersonIDs)
	assert.Equal(t, fmt.Sprintf("[%d]", sourceFace.ID), ev.FaceIDs)
	assert.Equal(t, "manual", ev.AlgorithmVersion)
}

func TestEmitsFeedbackMoveFacesNoChangeNoEvent(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	target, _, targetFace, _ := seedTwoPersons(t, svc)

	// 人脸本来就属于 target，无实际变化 → 不产生事件。
	_, err := svc.MoveFaces([]uint{targetFace.ID}, target.ID)
	require.NoError(t, err)

	events := feedbackEventsFromSvc(t, svc)
	assert.Empty(t, events)
}

func TestEmitsFeedbackSplitPersonRecordsSourceNewAndFaceIDs(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	target, _, _, _ := seedTwoPersons(t, svc)
	// 给 target 再加一张人脸用于拆分。
	photoRepo := repository.NewPhotoRepository(svc.db)
	faceRepo := repository.NewFaceRepository(svc.db)
	personRepo := repository.NewPersonRepository(svc.db)
	photo := &model.Photo{FilePath: "/fb/split.jpg", FileName: "split.jpg", FileSize: 1, FileHash: "split", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	splitFace := &model.Face{
		PhotoID: photo.ID, PersonID: &target.ID,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.7, Embedding: encodeEmbedding(t, []float32{0, 1, 0}),
	}
	require.NoError(t, faceRepo.Create(splitFace))
	require.NoError(t, personRepo.RefreshStats(target.ID))

	newPerson, _, err := svc.SplitPerson([]uint{splitFace.ID})
	require.NoError(t, err)
	require.NotNil(t, newPerson)

	events := feedbackEventsFromSvc(t, svc)
	require.Len(t, events, 1)
	ev := events[0]
	assert.Equal(t, repository.PeopleFeedbackEventPersonSplit, ev.EventType)
	assert.Equal(t, newPerson.ID, ev.TargetPersonID)
	assert.Equal(t, fmt.Sprintf("[%d]", target.ID), ev.SourcePersonIDs)
	assert.Equal(t, fmt.Sprintf("[%d]", splitFace.ID), ev.FaceIDs)
	assert.Equal(t, "manual", ev.AlgorithmVersion)
}

func TestEmitsFeedbackDissolvePersonRecordsReleasedFaces(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	target, _, targetFace, _ := seedTwoPersons(t, svc)
	// 再加一张人脸，验证多张人脸都被记录且升序排序。
	photoRepo := repository.NewPhotoRepository(svc.db)
	faceRepo := repository.NewFaceRepository(svc.db)
	personRepo := repository.NewPersonRepository(svc.db)
	photo := &model.Photo{FilePath: "/fb/dissolve.jpg", FileName: "dissolve.jpg", FileSize: 1, FileHash: "dissolve", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(photo))
	secondFace := &model.Face{
		PhotoID: photo.ID, PersonID: &target.ID,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.7, Embedding: encodeEmbedding(t, []float32{0, 1, 0}),
	}
	require.NoError(t, faceRepo.Create(secondFace))
	require.NoError(t, personRepo.RefreshStats(target.ID))

	released, err := svc.DissolvePerson(target.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, released)

	events := feedbackEventsFromSvc(t, svc)
	require.Len(t, events, 1)
	ev := events[0]
	assert.Equal(t, repository.PeopleFeedbackEventPersonDissolved, ev.EventType)
	assert.Equal(t, target.ID, ev.TargetPersonID, "event keeps original person ID even after deletion")
	assert.Equal(t, "[]", ev.SourcePersonIDs)
	// FaceIDs 升序：targetFace.ID 先于 secondFace.ID（后创建）。
	assert.Equal(t, fmt.Sprintf("[%d,%d]", targetFace.ID, secondFace.ID), ev.FaceIDs)
	assert.Equal(t, "manual", ev.AlgorithmVersion)
}

func TestEmitsFeedbackIDsAreSortedDedupedZeroFiltered(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	target, _, _, sourceFace := seedTwoPersons(t, svc)
	// 传入乱序 + 重复 + 0；MoveFaces 的 face_ids 经 helper 规范化。
	_, err := svc.MoveFaces([]uint{0, sourceFace.ID, sourceFace.ID}, target.ID)
	require.NoError(t, err)

	events := feedbackEventsFromSvc(t, svc)
	require.Len(t, events, 1)
	assert.Equal(t, fmt.Sprintf("[%d]", sourceFace.ID), events[0].FaceIDs, "zero dropped, deduped, ascending")
}

func TestEmitsFeedbackEventsContainNoSensitiveData(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	svc.SetFeedbackEventRepo(repository.NewPeopleFeedbackEventRepository(svc.db))

	target, source, _, _ := seedTwoPersons(t, svc)
	_, err := svc.MergePeople(target.ID, []uint{source.ID})
	require.NoError(t, err)

	events := feedbackEventsFromSvc(t, svc)
	require.Len(t, events, 1)
	// 校验落库事件文本不含 embedding/路径/缩略图/api key。
	blob := fmt.Sprintf("%+v", events[0])
	lower := strings.ToLower(blob)
	assert.NotContains(t, lower, "embedding")
	assert.NotContains(t, lower, "thumbnail")
	assert.NotContains(t, lower, "file_path")
	assert.NotContains(t, lower, "filepath")
	assert.NotContains(t, lower, "api_key")
}
