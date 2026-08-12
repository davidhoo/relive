package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/mlclient"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRescoreTestSvc 构造一个装配好 rescore repo 与 coordinator 的测试服务，
// 并注入可控行为的 fakePeopleMLClient。
func newRescoreTestSvc(t *testing.T, client PeopleMLClient) (*faceQualityRescoreService, *peopleService, repository.FaceQualityRescoreRepository) {
	t.Helper()
	svc, db := newPeopleServiceForTest(t, client)
	repo := repository.NewFaceQualityRescoreRepository(db)
	coord := NewBackgroundTaskCoordinator()
	rs := NewFaceQualityRescoreService(svc, repo, coord).(*faceQualityRescoreService)
	return rs, svc, repo
}

// floatPtr
func floatPtr(v float64) *float64 { return &v }

func init() {
	// 测试用：photo 无真实文件，prepareScoreImageBase64 直接返回占位 base64，避免走磁盘 IO。
	imageForTest = func(photo *model.Photo) (string, error) {
		return "dGVzdA==", nil // "test"
	}
}

// TestRescore_CreateCalibrationFreezesTargets 校准 run 快照当前 historical_backfill+missing 目标，
// 强制 shadow，且 photo_limit 生效。
func TestRescore_CreateCalibrationFreezesTargets(t *testing.T) {
	rs, svc, repo := newRescoreTestSvc(t, nil)
	db := svc.db

	// 3 张照片各 1 个历史缺证据 Face。
	for i := 0; i < 3; i++ {
		photo := &model.Photo{FilePath: filepath.Join("/t", fmt.Sprintf("p%d.jpg", i)), FaceProcessStatus: model.FaceProcessStatusReady}
		face := &model.Face{PhotoID: 0, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusAssigned}
		// 先建 photo 再建 face（face.PhotoID 需要 photo.ID）。
		require.NoError(t, db.Create(photo).Error)
		face.PhotoID = photo.ID
		require.NoError(t, db.Create(face).Error)
		require.NoError(t, db.Create(&model.FaceQualityEvent{
			PhotoID: photo.ID, FaceID: &face.ID,
			BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
			Decision: model.FaceQualityDecisionReviewRequired,
			Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
			EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalBackfill,
			EvidenceState:  model.FaceQualityEvidenceStateMissing,
			IsCurrent:      true,
		}).Error)
	}

	// photo_limit=2：只快照前 2 张照片。
	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 2)
	require.NoError(t, err)
	assert.Equal(t, model.FaceQualityRescoreApplyModeShadow, run.ApplyMode)
	assert.Equal(t, 2, run.TargetPhotoCount)
	assert.Equal(t, 2, run.TargetFaceCount)

	items, err := repo.ListItemsByRun(run.ID)
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, it := range items {
		assert.Equal(t, model.FaceQualityRescoreItemStatusPending, it.Status)
	}
}

// TestRescore_FullEnforceRequiresCompletedCalibration 无 completed calibration 时 full/enforce 返回错误。
func TestRescore_FullEnforceRequiresCompletedCalibration(t *testing.T) {
	rs, _, _ := newRescoreTestSvc(t, nil)
	_, err := rs.CreateRun(model.FaceQualityRescoreModeFull, model.FaceQualityRescoreApplyModeEnforce, 0)
	assert.ErrorIs(t, err, errCalibrationRequired)
}

// TestRescore_SingleActiveRunConflict 已有 running run 时再创建返回冲突。
func TestRescore_SingleActiveRunConflict(t *testing.T) {
	rs, _, _ := newRescoreTestSvc(t, nil)
	_, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0)
	require.NoError(t, err)
	_, err = rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0)
	assert.ErrorIs(t, err, errRunConflict)
}

// TestRescore_ShadowNeverExcludes shadow 运行即使策略判 exclude 也只写 review_required，不产生 face_exclusions。
func TestRescore_ShadowNeverExcludes(t *testing.T) {
	client := &rescoreFakeMLClient{
		resultsByFace: map[uint]mlclient.ScoreKnownFaceResult{},
	}
	rs, svc, repo := newRescoreTestSvc(t, client)
	db := svc.db

	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	face := &model.Face{
		PhotoID: photo.ID, PersonID: &person.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}
	require.NoError(t, db.Create(face).Error)
	// baseline 缺证据事件。
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalBackfill,
		EvidenceState:  model.FaceQualityEvidenceStateMissing,
		IsCurrent:      true,
	}).Error)

	// ML 返回高确定性非人脸证据（应触发 exclude，但 shadow 降级为 review_required）。
	client.resultsByFace[face.ID] = mlclient.ScoreKnownFaceResult{
		FaceID:     face.ID,
		Status:     "matched",
		MatchedIoU: floatPtr(0.9),
		Evidence: &mlclient.FaceQualityEvidence{
			FaceValidityScore:     0.1,
			LandmarkGeometryScore: 0.1,
			LandmarkCompleteness:  0.0,
			QualityReasons:        []string{"invalid_landmarks", "bad_geometry"},
			RuleVersion:           "v1",
			ModelVersion:          "test-v1",
		},
		QualityScore: floatPtr(0.1),
	}

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0)
	require.NoError(t, err)

	processed := rs.processOneBatch(run)
	assert.True(t, processed)

	// Face 仍是 assigned，person_id 未变，无 face_exclusions。
	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusAssigned, updated.ClusterStatus)
	require.NotNil(t, updated.PersonID)
	assert.Equal(t, person.ID, *updated.PersonID)
	var excCount int64
	db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", face.ID).Count(&excCount)
	assert.Equal(t, int64(0), excCount)

	// 当前事件为 rescore + review_required（shadow 降级）。
	var cur model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ? AND is_current = ?", photo.ID, true).First(&cur).Error)
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, cur.Decision)
	assert.Equal(t, model.FaceQualityEvidenceOriginHistoricalRescore, cur.EvidenceOrigin)
	assert.Equal(t, model.FaceQualityEvidenceStateAvailable, cur.EvidenceState)
	require.NotNil(t, cur.RescoreRunID)
	assert.Equal(t, run.ID, *cur.RescoreRunID)

	// baseline 事件已失活。
	var baseline model.FaceQualityEvent
	items, _ := repo.ListItemsByRun(run.ID)
	require.Len(t, items, 1)
	require.NoError(t, db.First(&baseline, items[0].BaselineEventID).Error)
	assert.False(t, baseline.IsCurrent)

	// item processed。
	items2, _ := repo.ListItemsByRun(run.ID)
	assert.Equal(t, model.FaceQualityRescoreItemStatusProcessed, items2[0].Status)
}

// TestRescore_EnforceExcludesHighConfidenceNonFace enforce 高置信非人脸自动排除，
// 只影响关联人物（局部刷新），不触发重检/全库聚类。
func TestRescore_EnforceExcludesHighConfidenceNonFace(t *testing.T) {
	client := &rescoreFakeMLClient{resultsByFace: map[uint]mlclient.ScoreKnownFaceResult{}}
	rs, svc, repo := newRescoreTestSvc(t, client)
	db := svc.db

	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	face := &model.Face{
		PhotoID: photo.ID, PersonID: &person.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalBackfill,
		EvidenceState:  model.FaceQualityEvidenceStateMissing,
		IsCurrent:      true,
	}).Error)

	client.resultsByFace[face.ID] = mlclient.ScoreKnownFaceResult{
		FaceID:     face.ID,
		Status:     "matched",
		MatchedIoU: floatPtr(0.9),
		Evidence: &mlclient.FaceQualityEvidence{
			FaceValidityScore:     0.1,
			LandmarkGeometryScore: 0.1,
			LandmarkCompleteness:  0.0,
			QualityReasons:        []string{"invalid_landmarks", "bad_geometry"},
			RuleVersion:           "v1",
			ModelVersion:          "test-v1",
		},
		QualityScore: floatPtr(0.1),
	}

	// 先建一个 completed calibration（前置条件）。
	// 注意：不调用 processOneBatch——calibration shadow 会把 face 的 baseline 失活并写成
	// available review_required，导致 full run 没法再从 missing 取到该目标。这里只把 run 标完成，
	// 不处理 item，使 full run 仍能快照到原 missing 目标。
	calib, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0)
	require.NoError(t, err)
	calib.Status = model.FaceQualityRescoreStatusCompleted
	now := time.Now().UTC()
	calib.CompletedAt = &now
	require.NoError(t, repo.UpdateRun(calib))

	// full/enforce run 重新快照当前 historical_backfill+missing 目标（即上面的 face）。
	// 为同时验证「不影响其他 run」，再造一个属于 photo2 的 missing 目标，full run 会一并快照。
	photo2 := &model.Photo{FilePath: "/t/p2.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo2).Error)
	person2 := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person2).Error)
	face2 := &model.Face{
		PhotoID: photo2.ID, PersonID: &person2.ID,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}
	require.NoError(t, db.Create(face2).Error)
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo2.ID, FaceID: &face2.ID,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalBackfill,
		EvidenceState:  model.FaceQualityEvidenceStateMissing,
		IsCurrent:      true,
	}).Error)
	// 给两个 face 都配 ML 返回高确定性非人脸。
	client.resultsByFace[face.ID] = mlclient.ScoreKnownFaceResult{
		FaceID:     face.ID,
		Status:     "matched",
		MatchedIoU: floatPtr(0.9),
		Evidence: &mlclient.FaceQualityEvidence{
			FaceValidityScore:     0.1,
			LandmarkGeometryScore: 0.1,
			LandmarkCompleteness:  0.0,
			QualityReasons:        []string{"invalid_landmarks", "bad_geometry"},
			RuleVersion:           "v1",
			ModelVersion:          "test-v1",
		},
		QualityScore: floatPtr(0.1),
	}
	client.resultsByFace[face2.ID] = mlclient.ScoreKnownFaceResult{
		FaceID:     face2.ID,
		Status:     "matched",
		MatchedIoU: floatPtr(0.9),
		Evidence: &mlclient.FaceQualityEvidence{
			FaceValidityScore:     0.1,
			LandmarkGeometryScore: 0.1,
			LandmarkCompleteness:  0.0,
			QualityReasons:        []string{"invalid_landmarks", "bad_geometry"},
			RuleVersion:           "v1",
			ModelVersion:          "test-v1",
		},
		QualityScore: floatPtr(0.1),
	}

	run, err := rs.CreateRun(model.FaceQualityRescoreModeFull, model.FaceQualityRescoreApplyModeEnforce, 0)
	require.NoError(t, err)
	assert.Equal(t, model.FaceQualityRescoreApplyModeEnforce, run.ApplyMode)
	require.Equal(t, 2, run.TargetFaceCount, "full run 应快照到 photo1+photo2 的 missing 目标")

	// 处理第一个照片批次（photo1）。
	processed := rs.processOneBatch(run)
	assert.True(t, processed)
	// 处理第二个照片批次（photo2）。
	rs.processOneBatch(run)

	// face1 被排除。
	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, updated.ClusterStatus)
	assert.Nil(t, updated.PersonID)
	var exc1 int64
	db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", face.ID).Count(&exc1)
	assert.Equal(t, int64(1), exc1)

	// face2 也被排除。
	var updated2 model.Face
	require.NoError(t, db.First(&updated2, face2.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, updated2.ClusterStatus)
	assert.Nil(t, updated2.PersonID)
	var exc2 int64
	db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", face2.ID).Count(&exc2)
	assert.Equal(t, int64(1), exc2)

	// photo2 的当前事件为 rescore + non_face。
	var cur model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ? AND is_current = ?", photo2.ID, true).First(&cur).Error)
	assert.Equal(t, model.FaceQualityDecisionNonFace, cur.Decision)
	assert.Equal(t, model.FaceQualityEvidenceOriginHistoricalRescore, cur.EvidenceOrigin)
	require.NotNil(t, cur.RescoreRunID)
	assert.Equal(t, run.ID, *cur.RescoreRunID)
}

// TestRescore_UnmatchedDoesNotExclude ML 未匹配 → item unmatched，baseline 事件标 unmatched，不排除、不改 Face 快照。
func TestRescore_UnmatchedDoesNotExclude(t *testing.T) {
	client := &rescoreFakeMLClient{resultsByFace: map[uint]mlclient.ScoreKnownFaceResult{}, unmatched: true}
	rs, svc, repo := newRescoreTestSvc(t, client)
	db := svc.db

	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	face := &model.Face{
		PhotoID: photo.ID, PersonID: &person.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned, FaceValidityScore: 0.0,
	}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalBackfill,
		EvidenceState:  model.FaceQualityEvidenceStateMissing,
		IsCurrent:      true,
	}).Error)

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0)
	require.NoError(t, err)
	rs.processOneBatch(run)

	// Face 未变。
	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusAssigned, updated.ClusterStatus)
	require.NotNil(t, updated.PersonID)
	// 无 face_exclusions。
	var excCount int64
	db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", face.ID).Count(&excCount)
	assert.Equal(t, int64(0), excCount)

	// item unmatched。
	items, _ := repo.ListItemsByRun(run.ID)
	require.Len(t, items, 1)
	assert.Equal(t, model.FaceQualityRescoreItemStatusUnmatched, items[0].Status)

	// baseline 事件标 rescore + unmatched。
	var cur model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ? AND is_current = ?", photo.ID, true).First(&cur).Error)
	assert.Equal(t, model.FaceQualityEvidenceOriginHistoricalRescore, cur.EvidenceOrigin)
	assert.Equal(t, model.FaceQualityEvidenceStateUnmatched, cur.EvidenceState)
}

// TestRescore_ManualConclusionSupersedesItem 运行中该 Face 被人工接受后，worker 跳过（superseded_manual）。
func TestRescore_ManualConclusionSupersedesItem(t *testing.T) {
	client := &rescoreFakeMLClient{resultsByFace: map[uint]mlclient.ScoreKnownFaceResult{}}
	rs, svc, repo := newRescoreTestSvc(t, client)
	db := svc.db

	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{
		PhotoID: photo.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusPending,
	}
	require.NoError(t, db.Create(face).Error)
	baseline := &model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalBackfill,
		EvidenceState:  model.FaceQualityEvidenceStateMissing,
		IsCurrent:      true,
	}
	require.NoError(t, db.Create(baseline).Error)

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0)
	require.NoError(t, err)

	// 在 worker 处理前，人工把该 Face 接受（写 manual accepted 当前事件，baseline 失活）。
	fqs := NewFaceQualityService(svc).(*faceQualityService)
	_, err = fqs.ApplyQualityDecision(model.FaceQualityDecisionRequest{
		EventIDs: []uint{baseline.ID},
		Action:   model.FaceQualityReviewActionAccept,
	})
	require.NoError(t, err)

	// ML 仍会返回 matched，但 worker 应识别 superseded 并跳过。
	client.resultsByFace[face.ID] = mlclient.ScoreKnownFaceResult{
		FaceID: face.ID, Status: "matched", MatchedIoU: floatPtr(0.9),
		Evidence: &mlclient.FaceQualityEvidence{
			FaceValidityScore: 0.95, LandmarkCompleteness: 1.0, LandmarkGeometryScore: 1.0,
			RuleVersion: "v1", ModelVersion: "test-v1",
		},
		QualityScore: floatPtr(0.9),
	}
	rs.processOneBatch(run)

	items, _ := repo.ListItemsByRun(run.ID)
	require.Len(t, items, 1)
	assert.Equal(t, model.FaceQualityRescoreItemStatusSupersededManual, items[0].Status)

	// 当前事件仍是 manual accepted，未被 rescore 覆盖。
	var cur model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ? AND is_current = ?", photo.ID, true).First(&cur).Error)
	assert.Equal(t, model.FaceQualitySourceManual, cur.Source)
	assert.Equal(t, model.FaceQualityDecisionAccepted, cur.Decision)
}

// TestRescore_RestoreAutoOnlyAffectsRun rescore run 级恢复只恢复本 run 的自动排除，
// 不影响其他 run / 实时自动排除 / 人工结论。
func TestRescore_RestoreAutoOnlyAffectsRun(t *testing.T) {
	rs, svc, repo := newRescoreTestSvc(t, nil)
	db := svc.db

	// 造一个 run，直接写两条 rescore 自动排除当前事件 + face_exclusions + Face excluded。
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	face1 := &model.Face{PhotoID: photo.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusExcluded}
	face2 := &model.Face{PhotoID: photo.ID, BBoxX: 0.4, BBoxY: 0.4, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusExcluded}
	require.NoError(t, db.Create(face1).Error)
	require.NoError(t, db.Create(face2).Error)
	run := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeFull, ApplyMode: model.FaceQualityRescoreApplyModeEnforce,
		Status: model.FaceQualityRescoreStatusCompleted, RuleVersion: "v1", ModelVersion: "test-v1",
	}
	require.NoError(t, repo.CreateRun(run))
	runID := run.ID
	for _, f := range []*model.Face{face1, face2} {
		require.NoError(t, db.Create(&model.FaceExclusion{
			PhotoID: photo.ID, SourceFaceID: f.ID, Reason: model.ExclusionReasonNonFace,
			BBoxX: f.BBoxX, BBoxY: f.BBoxY, BBoxWidth: f.BBoxWidth, BBoxHeight: f.BBoxHeight,
		}).Error)
		require.NoError(t, db.Create(&model.FaceQualityEvent{
			PhotoID: photo.ID, FaceID: &f.ID,
			BBoxX: f.BBoxX, BBoxY: f.BBoxY, BBoxWidth: f.BBoxWidth, BBoxHeight: f.BBoxHeight,
			Decision: model.FaceQualityDecisionNonFace, Reason: model.ExclusionReasonNonFace,
			Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
			EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore,
			EvidenceState:  model.FaceQualityEvidenceStateAvailable,
			RescoreRunID:   &runID, IsCurrent: true,
		}).Error)
	}

	// 另一个 run（不同 ID）的自动排除，不应被本 run 的 restore 恢复。
	run2 := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeFull, ApplyMode: model.FaceQualityRescoreApplyModeEnforce,
		Status: model.FaceQualityRescoreStatusCompleted, RuleVersion: "v1", ModelVersion: "test-v1",
	}
	require.NoError(t, repo.CreateRun(run2))
	face3 := &model.Face{PhotoID: photo.ID, BBoxX: 0.7, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusExcluded}
	require.NoError(t, db.Create(face3).Error)
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: face3.ID, Reason: model.ExclusionReasonNonFace,
		BBoxX: face3.BBoxX, BBoxY: face3.BBoxY, BBoxWidth: face3.BBoxWidth, BBoxHeight: face3.BBoxHeight,
	}).Error)
	run2ID := run2.ID
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face3.ID,
		BBoxX: face3.BBoxX, BBoxY: face3.BBoxY, BBoxWidth: face3.BBoxWidth, BBoxHeight: face3.BBoxHeight,
		Decision: model.FaceQualityDecisionNonFace, Reason: model.ExclusionReasonNonFace,
		Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore,
		EvidenceState:  model.FaceQualityEvidenceStateAvailable,
		RescoreRunID:   &run2ID, IsCurrent: true,
	}).Error)

	res, err := rs.RestoreAuto(runID, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Restored)

	// face1/face2 回 pending，face3 仍 excluded。
	var f1, f2, f3 model.Face
	require.NoError(t, db.First(&f1, face1.ID).Error)
	require.NoError(t, db.First(&f2, face2.ID).Error)
	require.NoError(t, db.First(&f3, face3.ID).Error)
	assert.Equal(t, model.FaceClusterStatusPending, f1.ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusPending, f2.ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusExcluded, f3.ClusterStatus)
}

// TestRescore_PauseResumeResetProcessing Pause→Resume 把 processing item 回到 pending。
func TestRescore_PauseResumeResetProcessing(t *testing.T) {
	rs, svc, repo := newRescoreTestSvc(t, nil)
	db := svc.db

	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{PhotoID: photo.ID, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalBackfill,
		EvidenceState:  model.FaceQualityEvidenceStateMissing,
		IsCurrent:      true,
	}).Error)

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0)
	require.NoError(t, err)

	// 领取一个 item（置 processing），模拟 worker 中途崩溃。
	items, err := repo.ClaimNextPhotoItems(run.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, model.FaceQualityRescoreItemStatusProcessing, items[0].Status)

	require.NoError(t, rs.Pause(run.ID))
	require.NoError(t, rs.Resume(run.ID))

	// processing item 回到 pending。
	pending, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusPending)
	assert.Equal(t, int64(1), pending)
	processing, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusProcessing)
	assert.Equal(t, int64(0), processing)
}

// rescoreFakeMLClient 可控的 ScoreKnownFaces 桩：按 face_id 返回预设结果，或全部 unmatched。
type rescoreFakeMLClient struct {
	resultsByFace map[uint]mlclient.ScoreKnownFaceResult
	unmatched     bool
	err           error
}

func (c *rescoreFakeMLClient) DetectFaces(ctx context.Context, req mlclient.DetectFacesRequest) (*mlclient.DetectFacesResponse, error) {
	return &mlclient.DetectFacesResponse{}, nil
}

func (c *rescoreFakeMLClient) ScoreKnownFaces(ctx context.Context, req mlclient.ScoreKnownFacesRequest) (*mlclient.ScoreKnownFacesResponse, error) {
	if c.err != nil {
		return nil, c.err
	}
	results := make([]mlclient.ScoreKnownFaceResult, 0, len(req.Targets))
	for _, t := range req.Targets {
		if r, ok := c.resultsByFace[t.FaceID]; ok {
			results = append(results, r)
			continue
		}
		status := "unmatched"
		if c.unmatched {
			status = "unmatched"
		}
		results = append(results, mlclient.ScoreKnownFaceResult{FaceID: t.FaceID, Status: status})
	}
	return &mlclient.ScoreKnownFacesResponse{Results: results}, nil
}
