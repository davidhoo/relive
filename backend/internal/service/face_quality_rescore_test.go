package service

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"os"
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
	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 2, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	assert.Equal(t, model.FaceQualityRescoreApplyModeShadow, run.ApplyMode)
	assert.Equal(t, 2, run.TargetPhotoCount)
	assert.Equal(t, 2, run.TargetFaceCount)

	items, err := repo.ListItemsByRun(run.ID)
	require.NoError(t, err)
	require.Len(t, items, 2)
	// 每个 item 的四个 BBox 必须与 baseline event 完全一致且 width/height > 0（复现零框 bug）。
	for _, it := range items {
		assert.Equal(t, model.FaceQualityRescoreItemStatusPending, it.Status)
		assert.Equal(t, 0.2, it.BBoxX, "bbox_x must match baseline, got zero-frame bug")
		assert.Equal(t, 0.2, it.BBoxY, "bbox_y must match baseline")
		assert.Equal(t, 0.2, it.BBoxWidth, "bbox_width must be > 0, not zero")
		assert.Equal(t, 0.2, it.BBoxHeight, "bbox_height must be > 0, not zero")
		// 与 baseline 事件逐字段一致。
		var baseline model.FaceQualityEvent
		require.NoError(t, db.First(&baseline, it.BaselineEventID).Error)
		assert.Equal(t, baseline.BBoxX, it.BBoxX)
		assert.Equal(t, baseline.BBoxY, it.BBoxY)
		assert.Equal(t, baseline.BBoxWidth, it.BBoxWidth)
		assert.Equal(t, baseline.BBoxHeight, it.BBoxHeight)
	}
}

// TestRescore_InvalidBBoxBlockedBeforeML 非法归一化 BBox 在调用 ML 前被阻断：
// fake ML client 调用次数为 0；item 写 retryable_error；事件转 historical_rescore + retryable_error；
// Face 人物归属与聚类状态不变。
func TestRescore_InvalidBBoxBlockedBeforeML(t *testing.T) {
	client := &rescoreFakeMLClient{resultsByFace: map[uint]mlclient.ScoreKnownFaceResult{}}
	rs, svc, repo := newRescoreTestSvc(t, client)
	db := svc.db

	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	// Face 持有合法归一化框，但 baseline 事件故意写零框（模拟历史坏数据 / 零框 bug 残留）。
	face := &model.Face{
		PhotoID: photo.ID, PersonID: &person.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.0, BBoxY: 0.0, BBoxWidth: 0.0, BBoxHeight: 0.0, // 非法零框
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalBackfill,
		EvidenceState:  model.FaceQualityEvidenceStateMissing,
		IsCurrent:      true,
	}).Error)

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)

	// 处理批次：非法 BBox 应在调 ML 前被阻断。
	processed := rs.processOneBatch(run)
	assert.True(t, processed)

	// ML 从未被调用（无 resultsByFace 命中也无 unmatched 兜底）。
	assert.Equal(t, 0, client.callCount, "ML must not be called for invalid bbox")

	// item 为 retryable_error，last_error 提示非法 bbox。
	items, err := repo.ListItemsByRun(run.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, model.FaceQualityRescoreItemStatusRetryableError, items[0].Status)
	assert.Contains(t, items[0].LastError, "invalid normalized bbox")

	// 当前事件转 historical_rescore + retryable_error。
	var cur model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ? AND is_current = ?", photo.ID, true).First(&cur).Error)
	assert.Equal(t, model.FaceQualityEvidenceOriginHistoricalRescore, cur.EvidenceOrigin)
	assert.Equal(t, model.FaceQualityEvidenceStateRetryableError, cur.EvidenceState)

	// Face 人物归属与聚类状态不变。
	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusAssigned, updated.ClusterStatus)
	require.NotNil(t, updated.PersonID)
	assert.Equal(t, person.ID, *updated.PersonID)
	var excCount int64
	db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", face.ID).Count(&excCount)
	assert.Equal(t, int64(0), excCount)
}

// TestRescore_FullEnforceRequiresCompletedCalibration 无 completed calibration 时 full/enforce 返回错误。
func TestRescore_FullEnforceRequiresCompletedCalibration(t *testing.T) {
	rs, _, _ := newRescoreTestSvc(t, nil)
	_, err := rs.CreateRun(model.FaceQualityRescoreModeFull, model.FaceQualityRescoreApplyModeEnforce, 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	assert.ErrorIs(t, err, errCalibrationRequired)
}

// TestRescore_SingleActiveRunConflict 已有 running run 时再创建返回冲突。
func TestRescore_SingleActiveRunConflict(t *testing.T) {
	rs, _, _ := newRescoreTestSvc(t, nil)
	_, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	_, err = rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
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

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
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
	calib, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	calib.Status = model.FaceQualityRescoreStatusCompleted
	// 伪造一个合格校准：target=1, processed=1, retryable=0, 无 pending/processing。
	// 该 calibration 的 item 仍是 pending（未真正处理），为了让 full 通过门禁，
	// 这里把 item 标 processed 以满足「无 pending/processing」与计数闭合。
	calibItems, _ := repo.ListItemsByRun(calib.ID)
	for _, it := range calibItems {
		it.Status = model.FaceQualityRescoreItemStatusProcessed
		require.NoError(t, repo.UpdateItem(it))
	}
	calib.TargetFaceCount = len(calibItems)
	calib.ProcessedFaceCount = len(calibItems)
	calib.RetryableCount = 0
	now := time.Now().UTC()
	calib.CompletedAt = &now
	require.NoError(t, repo.UpdateRun(calib))

	// full/enforce run 引用该合格校准 run。
	calibID := calib.ID

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

	run, err := rs.CreateRun(model.FaceQualityRescoreModeFull, model.FaceQualityRescoreApplyModeEnforce, 0, calibID, model.FaceQualityRescorePipelineLegacyV1)
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

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
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
		BBoxX:   0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
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

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
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
		Status: model.FaceQualityRescoreStatusCompleted, RuleVersion: "face_quality_v2", ModelVersion: "yunet-v1",
		PipelineVersion: model.FaceQualityRescorePipelineIndependentV2, TargetScope: model.RescoreTargetScopeV2,
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

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
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

// setupTwoPhotoRescoreRun 建两张照片各 1 个 historical_backfill+missing 目标，创建 v1 calibration run。
// 供并发暂停/取消竞态测试复用。
func setupTwoPhotoRescoreRun(t *testing.T, rs *faceQualityRescoreService, svc *peopleService) *model.FaceQualityRescoreRun {
	t.Helper()
	db := svc.db
	for i := 0; i < 2; i++ {
		photo := &model.Photo{FilePath: filepath.Join("/t", fmt.Sprintf("p%d.jpg", i)), FaceProcessStatus: model.FaceProcessStatusReady}
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
	}
	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	require.Equal(t, 2, run.TargetFaceCount)
	return run
}

// TestRescore_PauseDuringInFlightBatch 复现并验证暂停竞态修复：worker 持有首批 item 为 processing
// 期间并发暂停，首批完成到终态并触发 refreshRunCounts 后，paused 不得被覆盖回 running；
// 暂停中的 run 不得再领取第二张照片的 pending item。
func TestRescore_PauseDuringInFlightBatch(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	client := &rescoreFakeMLClient{block: block, entered: entered}
	rs, svc, repo := newRescoreTestSvc(t, client)
	run := setupTwoPhotoRescoreRun(t, rs, svc)

	// 启动首批处理：ClaimNextPhotoItemsWhenRunning 领取 photo1（processing），ML 阻塞。
	done := make(chan struct{})
	go func() {
		rs.processOneBatch(run)
		close(done)
	}()

	// 确定性等待 item 已被领取为 processing（ML 进入即说明 claim 已完成），再触发暂停。
	<-entered
	// 多核下 goroutine 可能恰好在 entered 发信号后；补一次 DB 断言巩固不变式。
	require.Eventually(t, func() bool {
		processing, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusProcessing)
		return processing == 1
	}, 2*time.Second, 5*time.Millisecond)

	// 并发暂停：DB run 必须为 paused。
	require.NoError(t, rs.Pause(run.ID))
	latest, err := repo.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, model.FaceQualityRescoreStatusPaused, latest.Status)

	// 放行 ML，让首批完成到终态（unmatched）并触发 refreshRunCounts。
	close(block)
	<-done

	// 首批完成后的进度刷新不得把 paused 改回 running。
	latest, err = repo.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, model.FaceQualityRescoreStatusPaused, latest.Status, "refreshRunCounts 不得覆盖 paused")

	// 第二张照片仍 pending（未被领取）。
	pending, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusPending)
	assert.Equal(t, int64(1), pending, "暂停中的 run 不得领取下一批")

	// 再次 processOneBatch：ClaimNextPhotoItemsWhenRunning 见 paused 返回空集，不领取。
	rs.processOneBatch(run)
	pending2, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusPending)
	assert.Equal(t, int64(1), pending2, "暂停中的 run 不得领取下一批照片")
}

// TestRescore_CancelDuringInFlightBatch 取消竞态同构：批次完成后的进度刷新不得把 cancelled
// 覆盖回 running；cancelled 的 run 不再领取 pending item。
func TestRescore_CancelDuringInFlightBatch(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	client := &rescoreFakeMLClient{block: block, entered: entered}
	rs, svc, repo := newRescoreTestSvc(t, client)
	run := setupTwoPhotoRescoreRun(t, rs, svc)

	done := make(chan struct{})
	go func() {
		rs.processOneBatch(run)
		close(done)
	}()

	<-entered
	require.Eventually(t, func() bool {
		processing, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusProcessing)
		return processing == 1
	}, 2*time.Second, 5*time.Millisecond)

	require.NoError(t, rs.Cancel(run.ID))
	latest, err := repo.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, model.FaceQualityRescoreStatusCancelled, latest.Status)

	close(block)
	<-done

	latest, err = repo.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, model.FaceQualityRescoreStatusCancelled, latest.Status, "refreshRunCounts 不得覆盖 cancelled")

	// cancelled 的 run 不得领取 pending item。
	rs.processOneBatch(run)
	pending, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusPending)
	assert.Equal(t, int64(1), pending, "cancelled 的 run 不得领取 pending item")
}

// TestRescore_AllFailureRunIsCompletedWithErrors 全部 item retryable 的 run：
// processed_face_count=0、review_required_count=0、retryable_count=target、last_error 非空、终态 completed_with_errors。
func TestRescore_AllFailureRunIsCompletedWithErrors(t *testing.T) {
	// ML 返回 error，使每个 item 走 retryable_error。
	client := &rescoreFakeMLClient{resultsByFace: map[uint]mlclient.ScoreKnownFaceResult{}, err: fmt.Errorf("ml service 422 invalid bbox")}
	rs, svc, repo := newRescoreTestSvc(t, client)
	db := svc.db

	for i := 0; i < 3; i++ {
		photo := &model.Photo{FilePath: filepath.Join("/t", fmt.Sprintf("p%d.jpg", i)), FaceProcessStatus: model.FaceProcessStatusReady}
		require.NoError(t, db.Create(photo).Error)
		face := &model.Face{PhotoID: photo.ID, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusAssigned}
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

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	require.Equal(t, 3, run.TargetFaceCount)

	// 处理全部 3 个照片批次（每张照片 1 个 item）。
	for i := 0; i < 3; i++ {
		rs.processOneBatch(run)
	}
	// 触发完成判定。
	rs.processOneBatch(run)

	latest, err := repo.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, model.FaceQualityRescoreStatusCompletedWithError, latest.Status, "全失败 run 应为 completed_with_errors，不是 completed")
	assert.Equal(t, 0, latest.ProcessedFaceCount, "已获证据应为 0")
	assert.Equal(t, 0, latest.ReviewRequiredCount, "真实灰区应为 0（失败项不计入）")
	assert.Equal(t, 3, latest.RetryableCount, "待重试应为 target=3")
	assert.NotEmpty(t, latest.LastError, "last_error 应从失败 item 回填")
}

// TestRescore_RetryableNotCountedAsReviewRequired retryable_error + decision=review_required 不计入灰区：
// 只有 evidence_state=available 的 review_required 才算。
func TestRescore_RetryableNotCountedAsReviewRequired(t *testing.T) {
	client := &rescoreFakeMLClient{resultsByFace: map[uint]mlclient.ScoreKnownFaceResult{}, err: fmt.Errorf("ml down")}
	rs, svc, repo := newRescoreTestSvc(t, client)
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

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	rs.processOneBatch(run)

	latest, err := repo.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, latest.ReviewRequiredCount, "retryable 的 review_required 不应计入灰区")
	assert.Equal(t, 1, latest.RetryableCount)
}

// TestRescore_RetryRunSnapshotsOnlyFailedEvents 来源 run 有两个 retryable、一个成功、一个 superseded：
// 重试只快照当前 retryable 事件，新 run 为 shadow 且 retry_of_run_id 指向来源。
func TestRescore_RetryRunSnapshotsOnlyFailedEvents(t *testing.T) {
	rs, svc, repo := newRescoreTestSvc(t, nil)
	db := svc.db

	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	f1 := &model.Face{PhotoID: photo.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending}
	f2 := &model.Face{PhotoID: photo.ID, BBoxX: 0.4, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending}
	f3 := &model.Face{PhotoID: photo.ID, BBoxX: 0.1, BBoxY: 0.4, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending}
	require.NoError(t, db.Create(f1).Error)
	require.NoError(t, db.Create(f2).Error)
	require.NoError(t, db.Create(f3).Error)

	srcRun := uint(0)
	// 两个当前失败事件（应被 retry 快照）。
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: photo.ID, FaceID: &f1.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Decision: model.FaceQualityDecisionReviewRequired, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore, EvidenceState: model.FaceQualityEvidenceStateRetryableError, RescoreRunID: &srcRun, IsCurrent: true}).Error)
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: photo.ID, FaceID: &f2.ID, BBoxX: 0.4, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Decision: model.FaceQualityDecisionReviewRequired, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore, EvidenceState: model.FaceQualityEvidenceStateUnmatched, RescoreRunID: &srcRun, IsCurrent: true}).Error)
	// 一个成功事件（不应被 retry 快照）。
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: photo.ID, FaceID: &f3.ID, BBoxX: 0.1, BBoxY: 0.4, BBoxWidth: 0.2, BBoxHeight: 0.2, Decision: model.FaceQualityDecisionAccepted, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore, EvidenceState: model.FaceQualityEvidenceStateAvailable, RescoreRunID: &srcRun, IsCurrent: true}).Error)

	// 先建来源 run（calibration + completed_with_errors）。
	src, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	src.Status = model.FaceQualityRescoreStatusCompletedWithError
	require.NoError(t, repo.UpdateRun(src))
	// 把来源 run 的失败事件 rescore_run_id 更新为来源 run ID（创建时未关联）。
	srcID := src.ID
	for _, fid := range []uint{f1.ID, f2.ID, f3.ID} {
		require.NoError(t, db.Model(&model.FaceQualityEvent{}).Where("face_id = ? AND rescore_run_id = 0", fid).Update("rescore_run_id", srcID).Error)
	}

	retry, err := rs.RetryRun(src.ID)
	require.NoError(t, err)
	assert.Equal(t, model.FaceQualityRescoreModeCalibration, retry.Mode)
	assert.Equal(t, model.FaceQualityRescoreApplyModeShadow, retry.ApplyMode)
	require.NotNil(t, retry.RetryOfRunID)
	assert.Equal(t, src.ID, *retry.RetryOfRunID)
	assert.Equal(t, 2, retry.TargetFaceCount, "只快照 2 个失败事件")
	assert.Equal(t, 1, retry.TargetPhotoCount)

	items, err := repo.ListItemsByRun(retry.ID)
	require.NoError(t, err)
	require.Len(t, items, 2)
	// 验证 item 的 BBox 与原失败事件一致（非零框）。
	for _, it := range items {
		assert.Greater(t, it.BBoxWidth, 0.0)
		assert.Greater(t, it.BBoxHeight, 0.0)
	}
}

// TestRescore_RetryRunRejectsInvalidSource 来源不是 calibration / 仍在运行 / 无失败集合时返回错误，不创建 run。
func TestRescore_RetryRunRejectsInvalidSource(t *testing.T) {
	rs, svc, repo := newRescoreTestSvc(t, nil)
	db := svc.db

	// 1) 不存在 → errRunNotFound。
	_, err := rs.RetryRun(999)
	assert.ErrorIs(t, err, errRunNotFound)

	// 2) full run（非 calibration）→ 拒绝。直接构造一个 full run 写入 DB，绕过 CreateRun 门禁。
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	full := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeFull, ApplyMode: model.FaceQualityRescoreApplyModeEnforce,
		Status: model.FaceQualityRescoreStatusCompleted, RuleVersion: "v1", ModelVersion: "test-v1",
	}
	require.NoError(t, repo.CreateRun(full))
	_, err = rs.RetryRun(full.ID)
	assert.ErrorIs(t, err, errRetrySourceNotCalibration)

	// 3) 仍在运行的 calibration → 拒绝。
	src2, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	// src2 是 running 状态。
	_, err = rs.RetryRun(src2.ID)
	assert.ErrorIs(t, err, errRetrySourceNotTerminal)

	// 4) completed 但无失败事件 → errRetryNoTargets。
	require.NoError(t, rs.Cancel(src2.ID))
	src3, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	src3.Status = model.FaceQualityRescoreStatusCompleted
	require.NoError(t, repo.UpdateRun(src3))
	require.NoError(t, rs.Cancel(src3.ID))
	src3.Status = model.FaceQualityRescoreStatusCompleted
	require.NoError(t, repo.UpdateRun(src3))
	_, err = rs.RetryRun(src3.ID)
	assert.ErrorIs(t, err, errRetryNoTargets)
}

// TestRescore_FullRejectsCompletedWithErrors #1 形态（completed_with_errors 或 legacy completed+retryable>0）拒绝 full。
func TestRescore_FullRejectsCompletedWithErrors(t *testing.T) {
	rs, svc, repo := newRescoreTestSvc(t, nil)
	_ = svc
	// 直接构造一个 completed_with_errors 的 calibration。
	src, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	src.Status = model.FaceQualityRescoreStatusCompletedWithError
	src.RetryableCount = 100
	require.NoError(t, repo.UpdateRun(src))

	_, err = rs.CreateRun(model.FaceQualityRescoreModeFull, model.FaceQualityRescoreApplyModeEnforce, 0, src.ID, model.FaceQualityRescorePipelineLegacyV1)
	assert.ErrorIs(t, err, errCalibrationRequired)

	// legacy completed + retryable>0 也拒绝。
	src2, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	src2.Status = model.FaceQualityRescoreStatusCompleted
	src2.RetryableCount = 5
	require.NoError(t, repo.UpdateRun(src2))
	_, err = rs.CreateRun(model.FaceQualityRescoreModeFull, model.FaceQualityRescoreApplyModeEnforce, 0, src2.ID, model.FaceQualityRescorePipelineLegacyV1)
	assert.ErrorIs(t, err, errCalibrationRequired)
}

// TestRescore_FullAcceptsEligibleCalibration 合格 shadow calibration 可创建 full，且 calibration_run_id 被持久化。
func TestRescore_FullAcceptsEligibleCalibration(t *testing.T) {
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

	calib, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	// 伪造合格：处理 item 为 processed，计数闭合。
	items, _ := repo.ListItemsByRun(calib.ID)
	for _, it := range items {
		it.Status = model.FaceQualityRescoreItemStatusProcessed
		require.NoError(t, repo.UpdateItem(it))
	}
	calib.Status = model.FaceQualityRescoreStatusCompleted
	calib.TargetFaceCount = len(items)
	calib.ProcessedFaceCount = len(items)
	calib.RetryableCount = 0
	require.NoError(t, repo.UpdateRun(calib))

	// full 引用合格 calibration。
	full, err := rs.CreateRun(model.FaceQualityRescoreModeFull, model.FaceQualityRescoreApplyModeEnforce, 0, calib.ID, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	require.NotNil(t, full.CalibrationRunID)
	assert.Equal(t, calib.ID, *full.CalibrationRunID)
}

// TestRescore_FullRejectsEmptyOrUnclosedCalibration 空校准 / 仍有 pending / 计数不闭合均拒绝。
func TestRescore_FullRejectsEmptyOrUnclosedCalibration(t *testing.T) {
	rs, _, repo := newRescoreTestSvc(t, nil)

	// 1) 空校准（target=0）：建一个 completed calibration 但 target_face_count=0。
	empty, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	empty.Status = model.FaceQualityRescoreStatusCompleted
	empty.TargetFaceCount = 0
	empty.ProcessedFaceCount = 0
	empty.RetryableCount = 0
	require.NoError(t, repo.UpdateRun(empty))
	_, err = rs.CreateRun(model.FaceQualityRescoreModeFull, model.FaceQualityRescoreApplyModeEnforce, 0, empty.ID, model.FaceQualityRescorePipelineLegacyV1)
	assert.ErrorIs(t, err, errCalibrationRequired, "空校准应拒绝")

	// 2) 仍有 pending item。
	unclosed := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow,
		Status: model.FaceQualityRescoreStatusCompleted, TargetFaceCount: 1, ProcessedFaceCount: 1,
		RetryableCount: 0, RuleVersion: "v1", ModelVersion: "test-v1",
	}
	require.NoError(t, repo.CreateRun(unclosed))
	require.NoError(t, repo.CreateItems([]*model.FaceQualityRescoreItem{
		{RunID: unclosed.ID, PhotoID: 1, FaceID: 10, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, BaselineEventID: 1, Status: model.FaceQualityRescoreItemStatusPending},
	}))
	_, err = rs.CreateRun(model.FaceQualityRescoreModeFull, model.FaceQualityRescoreApplyModeEnforce, 0, unclosed.ID, model.FaceQualityRescorePipelineLegacyV1)
	assert.ErrorIs(t, err, errCalibrationRequired, "仍有 pending 应拒绝")
}

// TestRescore_IsEligibleForEnforce 报告合格/不合格校准。
func TestRescore_IsEligibleForEnforce(t *testing.T) {
	rs, svc, repo := newRescoreTestSvc(t, nil)
	db := svc.db

	// 合格 v2 calibration：target=1, processed=1, retryable=0, 无 pending。
	// 仅 independent_v2 校准可作为 v2 enforce 来源（任务 §3）。
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{PhotoID: photo.ID, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending}
	require.NoError(t, db.Create(face).Error)
	// v2 选目标：Face 无 manual、无 independent_v2 事件即可入选，无需 historical_backfill 事件。
	calib, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineIndependentV2)
	require.NoError(t, err)
	items, _ := repo.ListItemsByRun(calib.ID)
	for _, it := range items {
		it.Status = model.FaceQualityRescoreItemStatusProcessed
		require.NoError(t, repo.UpdateItem(it))
	}
	calib.Status = model.FaceQualityRescoreStatusCompleted
	calib.TargetFaceCount = len(items)
	calib.ProcessedFaceCount = len(items)
	calib.RetryableCount = 0
	require.NoError(t, repo.UpdateRun(calib))
	assert.True(t, rs.IsEligibleForEnforce(calib.ID))

	// completed_with_errors 不合格。
	bad, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineIndependentV2)
	require.NoError(t, err)
	bad.Status = model.FaceQualityRescoreStatusCompletedWithError
	bad.RetryableCount = 10
	require.NoError(t, repo.UpdateRun(bad))
	assert.False(t, rs.IsEligibleForEnforce(bad.ID))

	// v1 calibration 不可作为 v2 enforce 来源（任务 §3）。
	v1Calib, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineLegacyV1)
	require.NoError(t, err)
	v1Calib.Status = model.FaceQualityRescoreStatusCompleted
	v1Calib.TargetFaceCount = 1
	v1Calib.ProcessedFaceCount = 1
	v1Calib.RetryableCount = 0
	require.NoError(t, repo.UpdateRun(v1Calib))
	assert.False(t, rs.IsEligibleForEnforce(v1Calib.ID), "v1 calibration 不可作为 v2 enforce 校准")

	// 不存在 → false。
	assert.False(t, rs.IsEligibleForEnforce(99999))
}

// rescoreFakeMLClient 可控的 ScoreKnownFaces 桩：按 face_id 返回预设结果，或全部 unmatched。
// block 非空时 ScoreKnownFaces 在返回前等待其关闭，用于复现 worker 持有首批 item 为 processing
// 期间并发暂停/取消的竞态。entered 非空时进入 ScoreKnownFaces 即非阻塞发一笔信号，供测试
// 确定性等待「item 已领取为 processing」后再触发暂停/取消，避免轮询竞态。
type rescoreFakeMLClient struct {
	resultsByFace       map[uint]mlclient.ScoreKnownFaceResult
	verifyResultsByFace map[uint]mlclient.VerifyKnownFaceCropResult
	unmatched           bool
	err                 error
	callCount           int
	block               chan struct{}
	entered             chan struct{}
}

func (c *rescoreFakeMLClient) DetectFaces(ctx context.Context, req mlclient.DetectFacesRequest) (*mlclient.DetectFacesResponse, error) {
	return &mlclient.DetectFacesResponse{}, nil
}

func (c *rescoreFakeMLClient) ScoreKnownFaces(ctx context.Context, req mlclient.ScoreKnownFacesRequest) (*mlclient.ScoreKnownFacesResponse, error) {
	c.callCount++
	if c.err != nil {
		return nil, c.err
	}
	// 确定性同步：进入 ML 调用即通知测试（此时首批 item 必已领取为 processing）。
	if c.entered != nil {
		select {
		case c.entered <- struct{}{}:
		default:
		}
	}
	// 阻塞直到测试放行：此时首批 item 已被领取为 processing，可并发触发 Pause/Cancel。
	if c.block != nil {
		<-c.block
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

// VerifyKnownFaceCrops v2 桩：按 face_id 返回预设验证结果。
// verifyResultsByFace 非空时按 face_id 查；否则默认返回 no_face（用于 v2 worker 测试）。
func (c *rescoreFakeMLClient) VerifyKnownFaceCrops(ctx context.Context, req mlclient.VerifyKnownFaceCropsRequest) (*mlclient.VerifyKnownFaceCropsResponse, error) {
	c.callCount++
	if c.err != nil {
		return nil, c.err
	}
	results := make([]mlclient.VerifyKnownFaceCropResult, 0, len(req.Targets))
	for _, t := range req.Targets {
		if r, ok := c.verifyResultsByFace[t.FaceID]; ok {
			results = append(results, r)
			continue
		}
		// 默认 no_face，供 v2 non_face 路径测试。
		results = append(results, mlclient.VerifyKnownFaceCropResult{
			FaceID:                t.FaceID,
			VerificationStatus:    "no_face",
			VerifierName:          "yunet",
			VerifierVersion:       "opencv-yunet-2023mar",
			PrimaryDetectorScore:  t.PrimaryDetectorScore,
			FaceBoxWidthPx:        t.FaceBoxWidthPx,
			FaceBoxHeightPx:       t.FaceBoxHeightPx,
			EvidenceSchemaVersion: "independent_v2",
		})
	}
	return &mlclient.VerifyKnownFaceCropsResponse{Results: results, RuleVersion: "face_quality_v2", ModelVersion: "opencv-yunet-2023mar"}, nil
}

// writeTestJPEG 写一张指定尺寸的纯 JPEG 到 path（供 v2 PrepareV2FaceCrops 读取）。
func writeTestJPEG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, jpeg.Encode(f, img, &jpeg.Options{Quality: 95}))
}

// TestRescore_V2ShadowWritesIndependentV2Evidence v2 shadow 运行：
// - 选目标走 ListV2SnapshotTargets（Face 无 manual/v2 事件即入选，无需 historical_backfill）；
// - worker 调 VerifyKnownFaceCrops，写 evidence_pipeline=independent_v2 的审计事件；
// - shadow 不自动排除（review_required），不改 person_id/cluster_status。
func TestRescore_V2ShadowWritesIndependentV2Evidence(t *testing.T) {
	dir := t.TempDir()
	jpgPath := filepath.Join(dir, "p.jpg")
	// 200×200 图，人脸框 (0.25,0.25,0.25,0.25) → 50×50px（>=48）。
	writeTestJPEG(t, jpgPath, 200, 200)

	// 默认 verify 返回 no_face + 主检测分低（Face.Confidence=0.3）→ 期望 non_face 候选。
	client := &rescoreFakeMLClient{}
	rs, svc, repo := newRescoreTestSvc(t, client)
	db := svc.db

	photo := &model.Photo{FilePath: jpgPath, FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{PhotoID: photo.ID, BBoxX: 0.25, BBoxY: 0.25, BBoxWidth: 0.25, BBoxHeight: 0.25, Confidence: 0.3, ClusterStatus: model.FaceClusterStatusAssigned}
	require.NoError(t, db.Create(face).Error)

	run, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineIndependentV2)
	require.NoError(t, err)
	assert.Equal(t, model.FaceQualityRescorePipelineIndependentV2, run.PipelineVersion)
	assert.Equal(t, model.RescoreTargetScopeV2, run.TargetScope)
	assert.Equal(t, 1, run.TargetFaceCount, "v2 应选入无 manual/v2 事件的 Face")

	processed := rs.processOneBatch(run)
	assert.True(t, processed)
	assert.Equal(t, 1, client.callCount, "v2 worker 应调 VerifyKnownFaceCrops")

	items, _ := repo.ListItemsByRun(run.ID)
	require.Len(t, items, 1)
	assert.Equal(t, model.FaceQualityRescoreItemStatusProcessed, items[0].Status)

	// 审计事件为 independent_v2 + review_required（shadow 不自动排除）。
	var evt model.FaceQualityEvent
	require.NoError(t, db.Where("face_id = ? AND is_current = ?", face.ID, true).First(&evt).Error)
	assert.Equal(t, model.FaceQualityEvidencePipelineIndependentV2, evt.EvidencePipeline)
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, evt.Decision)
	assert.Contains(t, evt.EvidenceJSON, "independent_v2")
	assert.Contains(t, evt.EvidenceJSON, "face_box_width_px")

	// Face 仍 assigned，未被排除。
	var f model.Face
	require.NoError(t, db.First(&f, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusAssigned, f.ClusterStatus)
}

// TestRescore_V2EnforceAutoExcludesNonFace v2 enforce + 低分 no_face → 自动 non_face 隔离。
func TestRescore_V2EnforceAutoExcludesNonFace(t *testing.T) {
	dir := t.TempDir()
	jpgPath := filepath.Join(dir, "p.jpg")
	writeTestJPEG(t, jpgPath, 200, 200)

	client := &rescoreFakeMLClient{} // 默认 no_face
	rs, svc, repo := newRescoreTestSvc(t, client)
	db := svc.db

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	photo := &model.Photo{FilePath: jpgPath, FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{PhotoID: photo.ID, PersonID: &person.ID, BBoxX: 0.25, BBoxY: 0.25, BBoxWidth: 0.25, BBoxHeight: 0.25, Confidence: 0.3, ClusterStatus: model.FaceClusterStatusAssigned}
	require.NoError(t, db.Create(face).Error)

	// 先建一个合格 v2 calibration（前置条件）。
	calib, err := rs.CreateRun(model.FaceQualityRescoreModeCalibration, "", 0, 0, model.FaceQualityRescorePipelineIndependentV2)
	require.NoError(t, err)
	rs.processOneBatch(calib) // shadow 写 review_required（v2 事件），item processed
	calibItems, _ := repo.ListItemsByRun(calib.ID)
	for _, it := range calibItems {
		it.Status = model.FaceQualityRescoreItemStatusProcessed
		require.NoError(t, repo.UpdateItem(it))
	}
	calib.Status = model.FaceQualityRescoreStatusCompleted
	calib.TargetFaceCount = len(calibItems)
	calib.ProcessedFaceCount = len(calibItems)
	calib.RetryableCount = 0
	require.NoError(t, repo.UpdateRun(calib))
	require.True(t, rs.IsEligibleForEnforce(calib.ID), "v2 calibration 应合格")

	// full/enforce 引用该 v2 calibration。注意：calibration 已为 face 写了 independent_v2 事件，
	// 故 full run 的 v2 选目标会排除该 face（已有 v2 事件）。为测试 enforce 排除，新建第二个 face。
	face2 := &model.Face{PhotoID: photo.ID, PersonID: &person.ID, BBoxX: 0.5, BBoxY: 0.5, BBoxWidth: 0.25, BBoxHeight: 0.25, Confidence: 0.3, ClusterStatus: model.FaceClusterStatusAssigned}
	require.NoError(t, db.Create(face2).Error)

	full, err := rs.CreateRun(model.FaceQualityRescoreModeFull, model.FaceQualityRescoreApplyModeEnforce, 0, calib.ID, model.FaceQualityRescorePipelineIndependentV2)
	require.NoError(t, err)
	assert.Equal(t, 1, full.TargetFaceCount, "full 应只选入 face2（face 已有 v2 事件）")

	rs.processOneBatch(full)

	// face2 被 non_face 自动排除。
	var f2 model.Face
	require.NoError(t, db.First(&f2, face2.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, f2.ClusterStatus)
	assert.Equal(t, model.ExclusionReasonNonFace, f2.ExclusionReason)
	assert.Nil(t, f2.PersonID, "排除后 person_id 清空")

	var evt model.FaceQualityEvent
	require.NoError(t, db.Where("face_id = ? AND is_current = ?", face2.ID, true).First(&evt).Error)
	assert.Equal(t, model.FaceQualityDecisionNonFace, evt.Decision)
	assert.Equal(t, model.FaceQualityEvidencePipelineIndependentV2, evt.EvidencePipeline)

	// run 级恢复（v2 允许）后 face2 回 pending，不恢复旧 person_id。
	res, err := rs.RestoreAuto(full.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Restored)
	var f2b model.Face
	require.NoError(t, db.First(&f2b, face2.ID).Error)
	assert.Equal(t, model.FaceClusterStatusPending, f2b.ClusterStatus)
	assert.Nil(t, f2b.PersonID, "恢复后不自动回到旧 person_id")
}

// TestRescore_RestoreAutoRejectsLegacyV1Run v1 run 的 restore-auto 被拒绝。
func TestRescore_RestoreAutoRejectsLegacyV1Run(t *testing.T) {
	rs, svc, repo := newRescoreTestSvc(t, nil)
	db := svc.db
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	run := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeFull, ApplyMode: model.FaceQualityRescoreApplyModeEnforce,
		Status: model.FaceQualityRescoreStatusCompleted, RuleVersion: "v1", ModelVersion: "test-v1",
		PipelineVersion: model.FaceQualityRescorePipelineLegacyV1, TargetScope: model.RescoreTargetScopeV1,
	}
	require.NoError(t, repo.CreateRun(run))
	_, err := rs.RestoreAuto(run.ID, 0)
	assert.ErrorIs(t, err, errRestoreLegacyV1NotAllowed)
}
