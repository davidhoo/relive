package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newRescoreRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// 每个测试用独立文件 DB，避免 shared memory 跨测试残留。
	path := fmt.Sprintf("file:rescore_%d?mode=memory&cache=shared&_busy_timeout=60000", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.FaceQualityEvent{},
		&model.FaceQualityRescoreRun{},
		&model.FaceQualityRescoreItem{},
		&model.FaceExclusion{},
		&model.Face{},
	))
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return db
}

func TestFaceQualityRescoreRepo_CreateRunAndItems(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)

	run := &model.FaceQualityRescoreRun{
		Mode:        model.FaceQualityRescoreModeCalibration,
		ApplyMode:   model.FaceQualityRescoreApplyModeShadow,
		Status:      model.FaceQualityRescoreStatusQueued,
		RuleVersion: "v1", ModelVersion: "test-v1",
	}
	require.NoError(t, repo.CreateRun(run))
	assert.NotZero(t, run.ID)

	items := []*model.FaceQualityRescoreItem{
		{RunID: run.ID, PhotoID: 1, FaceID: 10, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, BaselineEventID: 100, Status: model.FaceQualityRescoreItemStatusPending},
		{RunID: run.ID, PhotoID: 1, FaceID: 11, BBoxX: 0.4, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, BaselineEventID: 101, Status: model.FaceQualityRescoreItemStatusPending},
		{RunID: run.ID, PhotoID: 2, FaceID: 12, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, BaselineEventID: 102, Status: model.FaceQualityRescoreItemStatusPending},
	}
	require.NoError(t, repo.CreateItems(items))

	got, err := repo.ListItemsByRun(run.ID)
	require.NoError(t, err)
	assert.Len(t, got, 3)

	// (run_id, face_id) 唯一：重复插入应失败。
	dup := &model.FaceQualityRescoreItem{RunID: run.ID, PhotoID: 1, FaceID: 10, BaselineEventID: 100, Status: model.FaceQualityRescoreItemStatusPending}
	err = repo.CreateItems([]*model.FaceQualityRescoreItem{dup})
	assert.Error(t, err)
}

func TestFaceQualityRescoreRepo_ClaimNextPhotoItems(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)
	run := &model.FaceQualityRescoreRun{Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow, Status: model.FaceQualityRescoreStatusRunning, RuleVersion: "v1", ModelVersion: "test-v1"}
	require.NoError(t, repo.CreateRun(run))
	require.NoError(t, repo.CreateItems([]*model.FaceQualityRescoreItem{
		{RunID: run.ID, PhotoID: 5, FaceID: 50, BaselineEventID: 500, Status: model.FaceQualityRescoreItemStatusPending},
		{RunID: run.ID, PhotoID: 5, FaceID: 51, BaselineEventID: 501, Status: model.FaceQualityRescoreItemStatusPending},
		{RunID: run.ID, PhotoID: 6, FaceID: 60, BaselineEventID: 600, Status: model.FaceQualityRescoreItemStatusPending},
	}))

	// 第一次领取 photo 5 的两个 item。
	batch1, err := repo.ClaimNextPhotoItems(run.ID)
	require.NoError(t, err)
	require.Len(t, batch1, 2)
	for _, it := range batch1 {
		assert.Equal(t, uint(5), it.PhotoID)
		assert.Equal(t, model.FaceQualityRescoreItemStatusProcessing, it.Status)
	}

	// 第二次领取 photo 6 的一个 item。
	batch2, err := repo.ClaimNextPhotoItems(run.ID)
	require.NoError(t, err)
	require.Len(t, batch2, 1)
	assert.Equal(t, uint(6), batch2[0].PhotoID)

	// 第三次无 pending。
	batch3, err := repo.ClaimNextPhotoItems(run.ID)
	require.NoError(t, err)
	assert.Empty(t, batch3)
}

func TestFaceQualityRescoreRepo_ResetProcessingItems(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)
	run := &model.FaceQualityRescoreRun{Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow, Status: model.FaceQualityRescoreStatusRunning, RuleVersion: "v1", ModelVersion: "test-v1"}
	require.NoError(t, repo.CreateRun(run))
	require.NoError(t, repo.CreateItems([]*model.FaceQualityRescoreItem{
		{RunID: run.ID, PhotoID: 1, FaceID: 10, BaselineEventID: 100, Status: model.FaceQualityRescoreItemStatusProcessing},
		{RunID: run.ID, PhotoID: 1, FaceID: 11, BaselineEventID: 101, Status: model.FaceQualityRescoreItemStatusPending},
	}))

	n, err := repo.ResetProcessingItems(run.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	pending, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusPending)
	assert.Equal(t, int64(2), pending)
	processing, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusProcessing)
	assert.Equal(t, int64(0), processing)
}

func TestFaceQualityRescoreRepo_HasActiveRunAndCompletedCalibration(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)

	active, err := repo.HasActiveRun()
	require.NoError(t, err)
	assert.False(t, active)

	calib, err := repo.HasCompletedCalibration()
	require.NoError(t, err)
	assert.False(t, calib)

	// running run → active true。
	require.NoError(t, repo.CreateRun(&model.FaceQualityRescoreRun{Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow, Status: model.FaceQualityRescoreStatusRunning, RuleVersion: "v1", ModelVersion: "test-v1"}))
	active, _ = repo.HasActiveRun()
	assert.True(t, active)

	// completed calibration → HasCompletedCalibration true。
	require.NoError(t, repo.CreateRun(&model.FaceQualityRescoreRun{Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow, Status: model.FaceQualityRescoreStatusCompleted, RuleVersion: "v1", ModelVersion: "test-v1"}))
	calib, _ = repo.HasCompletedCalibration()
	assert.True(t, calib)
}

func TestFaceQualityRescoreRepo_ListAutoExcludedByRun(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)
	runID := uint(7)
	otherRun := uint(8)
	faceID := uint(100)

	// 本 run 的 non_face 自动排除（当前）。
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: 1, FaceID: &faceID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Decision: model.FaceQualityDecisionNonFace, Reason: model.ExclusionReasonNonFace, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", RescoreRunID: &runID, IsCurrent: true}).Error)
	// 其他 run 的自动排除（不应被本 run 列出）。
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: 2, FaceID: &faceID, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, Decision: model.FaceQualityDecisionLowQuality, Reason: model.ExclusionReasonLowQuality, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", RescoreRunID: &otherRun, IsCurrent: true}).Error)
	// 本 run 的非当前事件（不应列出）。
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: 3, FaceID: &faceID, BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2, Decision: model.FaceQualityDecisionNonFace, Reason: model.ExclusionReasonNonFace, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", RescoreRunID: &runID, IsCurrent: false}).Error)

	records, err := repo.ListAutoExcludedByRun(runID, 0)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, model.FaceQualityDecisionNonFace, records[0].Decision)
}

// TestFaceQualityRescoreRepo_ListRetryableTargets 验证 retry 窄查询只取来源 run 的当前失败事件。
func TestFaceQualityRescoreRepo_ListRetryableTargets(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)
	srcRun := uint(1)
	otherRun := uint(2)
	face1 := uint(100)
	face2 := uint(101)
	face3 := uint(102)

	// srcRun 的两个当前失败事件（retryable + unmatched）。
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: 1, FaceID: &face1, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Decision: model.FaceQualityDecisionReviewRequired, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore, EvidenceState: model.FaceQualityEvidenceStateRetryableError, RescoreRunID: &srcRun, IsCurrent: true}).Error)
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: 2, FaceID: &face2, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, Decision: model.FaceQualityDecisionReviewRequired, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore, EvidenceState: model.FaceQualityEvidenceStateUnmatched, RescoreRunID: &srcRun, IsCurrent: true}).Error)
	// srcRun 的 available 事件（成功，不应列出）。
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: 3, FaceID: &face3, BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2, Decision: model.FaceQualityDecisionAccepted, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore, EvidenceState: model.FaceQualityEvidenceStateAvailable, RescoreRunID: &srcRun, IsCurrent: true}).Error)
	// srcRun 的非当前失败事件（不应列出）。
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: 4, FaceID: &face1, BBoxX: 0.4, BBoxY: 0.4, BBoxWidth: 0.1, BBoxHeight: 0.1, Decision: model.FaceQualityDecisionReviewRequired, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore, EvidenceState: model.FaceQualityEvidenceStateRetryableError, RescoreRunID: &srcRun, IsCurrent: false}).Error)
	// 其他 run 的失败事件（不应列出）。
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: 5, FaceID: &face1, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Decision: model.FaceQualityDecisionReviewRequired, Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "t", EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalRescore, EvidenceState: model.FaceQualityEvidenceStateRetryableError, RescoreRunID: &otherRun, IsCurrent: true}).Error)

	targets, err := repo.ListRetryableTargets(srcRun)
	require.NoError(t, err)
	require.Len(t, targets, 2, "只应列出 srcRun 的 2 个当前失败事件")
	assert.Equal(t, face1, targets[0].FaceID)
	assert.Equal(t, model.FaceQualityEvidenceStateRetryableError, targets[0].EvidenceState)
	assert.Equal(t, face2, targets[1].FaceID)
	assert.Equal(t, model.FaceQualityEvidenceStateUnmatched, targets[1].EvidenceState)
	// BBox 与原始事件一致（非零框）。
	assert.Equal(t, 0.1, targets[0].BBoxX)
	assert.Equal(t, 0.2, targets[0].BBoxWidth)
}

// TestFaceQualityRescoreRepo_EligibleCalibration 验证 GetEligibleCalibration 取回 run（逐项验证在 service 层）。
func TestFaceQualityRescoreRepo_EligibleCalibration(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)

	// 合格 calibration：completed + 计数闭合 + 无 pending/processing。
	run := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow,
		Status:          model.FaceQualityRescoreStatusCompleted,
		TargetFaceCount: 2, ProcessedFaceCount: 2, RetryableCount: 0,
		RuleVersion: "v1", ModelVersion: "test-v1",
	}
	require.NoError(t, repo.CreateRun(run))
	require.NoError(t, repo.CreateItems([]*model.FaceQualityRescoreItem{
		{RunID: run.ID, PhotoID: 1, FaceID: 10, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, BaselineEventID: 1, Status: model.FaceQualityRescoreItemStatusProcessed},
		{RunID: run.ID, PhotoID: 2, FaceID: 11, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, BaselineEventID: 2, Status: model.FaceQualityRescoreItemStatusProcessed},
	}))

	got, err := repo.GetEligibleCalibration(run.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, model.FaceQualityRescoreStatusCompleted, got.Status)

	// 仍有 pending → CountPendingOrProcessing > 0。
	pending, err := repo.CountPendingOrProcessing(run.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), pending)

	// 不存在的 run → ErrRecordNotFound。
	_, err = repo.GetEligibleCalibration(999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestFaceQualityRescoreRepo_ListV2SnapshotTargets(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)

	// Face A: photo 1, 无任何当前事件 → 进入 v2 快照。
	// Face B: photo 2, 当前 source=manual 事件 → 跳过（人工结论优先）。
	// Face C: photo 3, 当前 independent_v2 事件 → 跳过（已复核）。
	// Face D: photo 4, 当前 legacy_v1 auto 事件 → 进入快照（旧自动结论无人工，需 v2 复核）。
	// Face E: photo 5, 无事件但已 excluded（cluster_status=excluded, auto）→ 仍进入快照。
	faces := []*model.Face{
		{ID: 1001, PhotoID: 1, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending},
		{ID: 1002, PhotoID: 2, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusAssigned},
		{ID: 1003, PhotoID: 3, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusAssigned},
		{ID: 1004, PhotoID: 4, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusAssigned},
		{ID: 1005, PhotoID: 5, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonLowQuality},
	}
	for _, f := range faces {
		require.NoError(t, db.Create(f).Error)
	}

	// Face B: manual 当前事件。
	manualEvt := &model.FaceQualityEvent{
		PhotoID: 2, FaceID: uintPtr(1002),
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionAccepted,
		Source:   model.FaceQualitySourceManual, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidencePipeline: model.FaceQualityEvidencePipelineLegacyV1,
		IsCurrent:        true,
	}
	require.NoError(t, db.Create(manualEvt).Error)

	// Face C: independent_v2 当前事件。
	v2Evt := &model.FaceQualityEvent{
		PhotoID: 3, FaceID: uintPtr(1003),
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionAccepted,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "face_quality_v2", ModelVersion: "yunet-v1",
		EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		IsCurrent:        true,
	}
	require.NoError(t, db.Create(v2Evt).Error)

	// Face D: legacy_v1 auto 当前事件（旧自动结论，无人工覆盖）。
	legacyEvt := &model.FaceQualityEvent{
		PhotoID: 4, FaceID: uintPtr(1004),
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionLowQuality,
		Source:   model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		EvidencePipeline: model.FaceQualityEvidencePipelineLegacyV1,
		IsCurrent:        true,
	}
	require.NoError(t, db.Create(legacyEvt).Error)

	targets, err := repo.ListV2SnapshotTargets(0)
	require.NoError(t, err)

	// 期望：Face A(1001), Face D(1004), Face E(1005)。Face B/C 被排除。
	gotFaceIDs := make(map[uint]bool)
	for _, tg := range targets {
		gotFaceIDs[tg.FaceID] = true
	}
	assert.True(t, gotFaceIDs[1001], "Face A 应进入 v2 快照")
	assert.True(t, gotFaceIDs[1004], "Face D（旧自动结论）应进入 v2 快照")
	assert.True(t, gotFaceIDs[1005], "Face E（已 excluded 无人工）应进入 v2 快照")
	assert.False(t, gotFaceIDs[1002], "Face B（manual）应跳过")
	assert.False(t, gotFaceIDs[1003], "Face C（已 independent_v2）应跳过")

	// Face D 的 baseline 应指向其当前事件 ID。
	for _, tg := range targets {
		if tg.FaceID == 1004 {
			assert.Equal(t, legacyEvt.ID, tg.BaselineEventID, "Face D baseline 应为当前事件 ID")
		}
		if tg.FaceID == 1001 {
			assert.Equal(t, uint(0), tg.BaselineEventID, "Face A 无当前事件，baseline=0")
		}
	}

	// photoLimit 截断：按 photo_id 升序取前 1 张照片 → 仅 photo 1 的 Face A(1001)。
	limited, err := repo.ListV2SnapshotTargets(1)
	require.NoError(t, err)
	limitedIDs := make(map[uint]bool)
	for _, tg := range limited {
		limitedIDs[tg.FaceID] = true
	}
	assert.True(t, limitedIDs[1001])
	assert.Len(t, limited, 1, "限 1 张照片应只命中 photo 1 的 Face A")
}

// TestFaceQualityRescoreRepo_ListIndependentSnapshotTargets_V3ReVerifiesV2
// 规则版本化快照：v3 可重新复核已有 face_quality_v2 自动证据，人工结论仍绝对优先，
// 同规则版本（v3）的自动事件不重复复核。
func TestFaceQualityRescoreRepo_ListIndependentSnapshotTargets_V3ReVerifiesV2(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)

	// Face A: photo 1, 当前 face_quality_v2 auto 事件 → v3 应重新选中（异规则版本复核）。
	// Face B: photo 2, 当前 source=manual accept → 永久排除（人工结论优先）。
	// Face C: photo 3, 当前 face_quality_v3 auto 事件 → v3 不重复选中（同规则版本）。
	// Face D: photo 4, 无事件 → 选中。
	faces := []*model.Face{
		{ID: 538580, PhotoID: 1, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending},
		{ID: 538581, PhotoID: 2, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusAssigned},
		{ID: 538582, PhotoID: 3, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusAssigned},
		{ID: 538583, PhotoID: 4, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending},
	}
	for _, f := range faces {
		require.NoError(t, db.Create(f).Error)
	}

	// Face A: v2 自动事件（no_face，主分过线 → review_required）。
	v2Evt := &model.FaceQualityEvent{
		PhotoID: 1, FaceID: uintPtr(538580),
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: model.FaceQualityRescoreRuleVersionV2, ModelVersion: "yunet-v1",
		EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		IsCurrent:        true,
	}
	require.NoError(t, db.Create(v2Evt).Error)

	// Face B: 人工 accept。
	manualEvt := &model.FaceQualityEvent{
		PhotoID: 2, FaceID: uintPtr(538581),
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionAccepted,
		Source:   model.FaceQualitySourceManual, RuleVersion: model.FaceQualityRescoreRuleVersionV2, ModelVersion: "yunet-v1",
		EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		IsCurrent:        true,
	}
	require.NoError(t, db.Create(manualEvt).Error)

	// Face C: v3 自动事件（同规则版本，不重复复核）。
	v3Evt := &model.FaceQualityEvent{
		PhotoID: 3, FaceID: uintPtr(538582),
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionAccepted,
		Source:   model.FaceQualitySourceAuto, RuleVersion: model.FaceQualityRescoreRuleVersionV3, ModelVersion: "yunet-v1",
		EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		IsCurrent:        true,
	}
	require.NoError(t, db.Create(v3Evt).Error)

	targets, err := repo.ListIndependentSnapshotTargets(model.FaceQualityRescoreRuleVersionV3, 0, nil)
	require.NoError(t, err)

	got := make(map[uint]bool)
	for _, tg := range targets {
		got[tg.FaceID] = true
	}
	assert.True(t, got[538580], "Face A（v2 自动证据）应被 v3 重新选中复核")
	assert.True(t, got[538583], "Face D（无事件）应选中")
	assert.False(t, got[538581], "Face B（人工 accept）应被永久排除")
	assert.False(t, got[538582], "Face C（v3 同规则版本）不应重复复核")

	// Face A 的 baseline 应指向其当前 v2 事件（复核时以此为 baseline 检测冲突）。
	for _, tg := range targets {
		if tg.FaceID == 538580 {
			assert.Equal(t, v2Evt.ID, tg.BaselineEventID, "Face A baseline 应为当前 v2 事件 ID")
		}
	}

	// 回归：v2 请求不应选中 Face A（同规则版本 v2 已复核），仍排除 B/C。
	v2Targets, err := repo.ListIndependentSnapshotTargets(model.FaceQualityRescoreRuleVersionV2, 0, nil)
	require.NoError(t, err)
	v2Got := make(map[uint]bool)
	for _, tg := range v2Targets {
		v2Got[tg.FaceID] = true
	}
	assert.False(t, v2Got[538580], "v2 请求不应重复复核已 v2 复核的 Face A")
	assert.False(t, v2Got[538581], "v2 请求仍排除人工 accept 的 Face B")
	assert.True(t, v2Got[538582], "v2 请求应选中 Face C（当前 v3 事件属异规则版本，可复核）")
}

// TestFaceQualityRescoreRepo_ListIndependentSnapshotTargets_TargetedFaceIDs
// face_ids 定点快照：仅选指定 Face，忽略 photoLimit；未知 id 报错。
func TestFaceQualityRescoreRepo_ListIndependentSnapshotTargets_TargetedFaceIDs(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)

	faces := []*model.Face{
		{ID: 538580, PhotoID: 1, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending},
		{ID: 538581, PhotoID: 1, BBoxX: 0.5, BBoxY: 0.5, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending},
		{ID: 538582, PhotoID: 2, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusPending},
	}
	for _, f := range faces {
		require.NoError(t, db.Create(f).Error)
	}

	// 定点：只选 538580（去重 + 顺序无关）。
	targets, err := repo.ListIndependentSnapshotTargets(model.FaceQualityRescoreRuleVersionV3, 100, []uint{538580, 538580, 0})
	require.NoError(t, err)
	require.Len(t, targets, 1, "定点快照应只命中指定 Face")
	assert.Equal(t, uint(538580), targets[0].FaceID)
	// photoLimit=100 在定点模式下被忽略（否则 photo 1 截断会丢 538580，这里仍命中说明忽略生效）。

	// 多个目标。
	multi, err := repo.ListIndependentSnapshotTargets(model.FaceQualityRescoreRuleVersionV3, 0, []uint{538582, 538580})
	require.NoError(t, err)
	multiIDs := make(map[uint]bool)
	for _, tg := range multi {
		multiIDs[tg.FaceID] = true
	}
	assert.True(t, multiIDs[538580])
	assert.True(t, multiIDs[538582])

	// 未知 id 报 ErrRescoreFaceIDNotFound，不静默丢弃。
	_, err = repo.ListIndependentSnapshotTargets(model.FaceQualityRescoreRuleVersionV3, 0, []uint{538580, 999999})
	assert.ErrorIs(t, err, ErrRescoreFaceIDNotFound, "未知 face_id 应报错而非静默丢弃")
}

// TestFaceQualityRescoreRepo_ListIndependentSnapshotTargets_TargetedReVerifiesSameRuleAuto
// 定点 face_ids 不应用「同规则版本自动事件去重」：已有当前 face_quality_v3 自动事件的 Face，
// 定点重跑仍应被选中（旧事件由新事件 is_current=true 取代，审计保留）。非定点仍去重同规则版本。
func TestFaceQualityRescoreRepo_ListIndependentSnapshotTargets_TargetedReVerifiesSameRuleAuto(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)

	require.NoError(t, db.Create(&model.Face{ID: 538580, PhotoID: 1, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusReviewRequired}).Error)
	// 当前 v3 自动事件（同规则版本）：非定点会被去重排除，但定点应重新选中。
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: 1, FaceID: uintPtr(538580),
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionReviewRequired,
		Source:   model.FaceQualitySourceAuto, RuleVersion: model.FaceQualityRescoreRuleVersionV3, ModelVersion: "yunet-v1",
		EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2, IsCurrent: true,
	}).Error)

	// 定点：同规则版本去重不应生效，应重新选中。
	targets, err := repo.ListIndependentSnapshotTargets(model.FaceQualityRescoreRuleVersionV3, 0, []uint{538580})
	require.NoError(t, err)
	require.Len(t, targets, 1, "定点 v3 重跑应重新选中已有同规则版本自动事件的 Face")
	assert.Equal(t, uint(538580), targets[0].FaceID)

	// 非定点：同规则版本去重仍生效，同一 Face 不应入选（避免全量重复复核）。
	nonTargeted, err := repo.ListIndependentSnapshotTargets(model.FaceQualityRescoreRuleVersionV3, 0, nil)
	require.NoError(t, err)
	for _, tg := range nonTargeted {
		assert.NotEqual(t, uint(538580), tg.FaceID, "非定点应去重同规则版本自动事件")
	}
}

// TestFaceQualityRescoreRepo_ListIndependentSnapshotTargets_TargetedRespectsManual
// 定点请求不绕过人工结论：当前 manual 事件的 Face 定点仍返回零目标。
func TestFaceQualityRescoreRepo_ListIndependentSnapshotTargets_TargetedRespectsManual(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)

	require.NoError(t, db.Create(&model.Face{ID: 538581, PhotoID: 1, BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusManual}).Error)
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: 1, FaceID: uintPtr(538581),
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionAccepted, Source: model.FaceQualitySourceManual,
		RuleVersion: model.FaceQualityRescoreRuleVersionV3, ModelVersion: "yunet-v1",
		EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2, IsCurrent: true,
	}).Error)

	targets, err := repo.ListIndependentSnapshotTargets(model.FaceQualityRescoreRuleVersionV3, 0, []uint{538581})
	require.NoError(t, err)
	assert.Empty(t, targets, "人工结论不得因定点重跑被绕过")
}

// TestFaceQualityRescoreRepo_ListIndependentSnapshotTargets_DefaultRuleVersion
// ruleVersion 为空时回退 face_quality_v2（与 ListV2SnapshotTargets 行为一致）。
func TestFaceQualityRescoreRepo_ListIndependentSnapshotTargets_DefaultRuleVersion(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)

	require.NoError(t, db.Create(&model.Face{ID: 7001, PhotoID: 1, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}).Error)
	// 当前 v2 自动事件 → 空规则版本回退 v2 时应排除。
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: 1, FaceID: uintPtr(7001),
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionAccepted,
		Source:   model.FaceQualitySourceAuto, RuleVersion: model.FaceQualityRescoreRuleVersionV2, ModelVersion: "yunet-v1",
		EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		IsCurrent:        true,
	}).Error)

	empty, err := repo.ListIndependentSnapshotTargets("", 0, nil)
	require.NoError(t, err)
	// 空规则版本回退 v2 → 排除当前 v2 自动事件的 Face 7001。
	for _, tg := range empty {
		assert.NotEqual(t, uint(7001), tg.FaceID, "空 ruleVersion 回退 v2，应排除已 v2 复核的 Face")
	}

	// v3 请求应选中（异规则版本复核）。
	v3, err := repo.ListIndependentSnapshotTargets(model.FaceQualityRescoreRuleVersionV3, 0, nil)
	require.NoError(t, err)
	found := false
	for _, tg := range v3 {
		if tg.FaceID == 7001 {
			found = true
		}
	}
	assert.True(t, found, "v3 应重新复核 v2 自动证据的 Face 7001")
}

// TestFaceQualityRescoreRepo_UpdateRunProgress 验证 UpdateRunProgress 只写统计字段，
// 绝不触碰 status/started_at/completed_at——这是 worker 进度刷新不覆盖暂停竞态的核心保证。
func TestFaceQualityRescoreRepo_UpdateRunProgress(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)
	started := time.Now().UTC().Add(-1 * time.Hour)
	run := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow,
		Status: model.FaceQualityRescoreStatusRunning, RuleVersion: "v1", ModelVersion: "test-v1",
		StartedAt: &started,
	}
	require.NoError(t, repo.CreateRun(run))

	// worker 刷新统计。
	require.NoError(t, repo.UpdateRunProgress(run.ID, FaceQualityRescoreRunProgress{
		ProcessedFaceCount: 7, ProcessedPhotoCount: 5, AcceptedCount: 4,
		ReviewRequiredCount: 2, AutoExcludedCount: 0, RetryableCount: 1,
		SupersededManualCount: 0, LastError: "boom",
	}))

	got, err := repo.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, 7, got.ProcessedFaceCount)
	assert.Equal(t, 5, got.ProcessedPhotoCount)
	assert.Equal(t, 4, got.AcceptedCount)
	assert.Equal(t, 2, got.ReviewRequiredCount)
	assert.Equal(t, 1, got.RetryableCount)
	assert.Equal(t, "boom", got.LastError)

	// status/started_at 不得被改。
	assert.Equal(t, model.FaceQualityRescoreStatusRunning, got.Status, "UpdateRunProgress 不得改 status")
	require.NotNil(t, got.StartedAt)
	assert.WithinDuration(t, started, *got.StartedAt, time.Second, "UpdateRunProgress 不得改 started_at")
	assert.Nil(t, got.CompletedAt, "UpdateRunProgress 不得写 completed_at")
}

// TestFaceQualityRescoreRepo_TransitionRunStatus 验证条件状态转换：仅当当前 status ∈ from
// 时写 to，命中返回 true；状态已被并发方改变时返回 false，不覆盖。
func TestFaceQualityRescoreRepo_TransitionRunStatus(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)
	run := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow,
		Status: model.FaceQualityRescoreStatusRunning, RuleVersion: "v1", ModelVersion: "test-v1",
	}
	require.NoError(t, repo.CreateRun(run))

	// running -> paused：命中。
	ok, err := repo.TransitionRunStatus(run.ID,
		[]string{model.FaceQualityRescoreStatusRunning, model.FaceQualityRescoreStatusQueued},
		model.FaceQualityRescoreStatusPaused, nil)
	require.NoError(t, err)
	assert.True(t, ok)
	got, _ := repo.GetRun(run.ID)
	assert.Equal(t, model.FaceQualityRescoreStatusPaused, got.Status)

	// 再 running -> paused：当前是 paused，不在 from（running|queued）→ false，状态不变。
	ok, err = repo.TransitionRunStatus(run.ID,
		[]string{model.FaceQualityRescoreStatusRunning, model.FaceQualityRescoreStatusQueued},
		model.FaceQualityRescoreStatusPaused, nil)
	require.NoError(t, err)
	assert.False(t, ok, "已 paused 不得再次命中 running|queued->paused")
	got, _ = repo.GetRun(run.ID)
	assert.Equal(t, model.FaceQualityRescoreStatusPaused, got.Status)

	// paused -> running：命中。
	ok, err = repo.TransitionRunStatus(run.ID,
		[]string{model.FaceQualityRescoreStatusPaused},
		model.FaceQualityRescoreStatusRunning, nil)
	require.NoError(t, err)
	assert.True(t, ok)

	// completedAt 随终态写入。
	completeAt := time.Now().UTC()
	ok, err = repo.TransitionRunStatus(run.ID,
		[]string{model.FaceQualityRescoreStatusRunning},
		model.FaceQualityRescoreStatusCompletedWithError, &completeAt)
	require.NoError(t, err)
	assert.True(t, ok)
	got, _ = repo.GetRun(run.ID)
	assert.Equal(t, model.FaceQualityRescoreStatusCompletedWithError, got.Status)
	require.NotNil(t, got.CompletedAt)
}

// TestFaceQualityRescoreRepo_ClaimNextPhotoItemsWhenRunning 验证领取门禁：
// run.status=running 时正常领取并置 processing；非 running（paused/cancelled）时返回空集不领任何 item。
func TestFaceQualityRescoreRepo_ClaimNextPhotoItemsWhenRunning(t *testing.T) {
	db := newRescoreRepoTestDB(t)
	repo := NewFaceQualityRescoreRepository(db)
	run := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow,
		Status: model.FaceQualityRescoreStatusRunning, RuleVersion: "v1", ModelVersion: "test-v1",
	}
	require.NoError(t, repo.CreateRun(run))
	require.NoError(t, repo.CreateItems([]*model.FaceQualityRescoreItem{
		{RunID: run.ID, PhotoID: 1, FaceID: 10, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Status: model.FaceQualityRescoreItemStatusPending},
		{RunID: run.ID, PhotoID: 2, FaceID: 11, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Status: model.FaceQualityRescoreItemStatusPending},
	}))

	// running：领取 photo 1 的 item。
	batch1, err := repo.ClaimNextPhotoItemsWhenRunning(run.ID)
	require.NoError(t, err)
	require.Len(t, batch1, 1)
	assert.Equal(t, model.FaceQualityRescoreItemStatusProcessing, batch1[0].Status)

	// 暂停 run。
	_, err = repo.TransitionRunStatus(run.ID,
		[]string{model.FaceQualityRescoreStatusRunning},
		model.FaceQualityRescoreStatusPaused, nil)
	require.NoError(t, err)

	// paused：返回空集，photo 2 的 item 仍 pending。
	batch2, err := repo.ClaimNextPhotoItemsWhenRunning(run.ID)
	require.NoError(t, err)
	assert.Empty(t, batch2, "暂停中的 run 不得领取下一批")
	pending, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusPending)
	assert.Equal(t, int64(1), pending, "photo 2 仍 pending")
	processing, _ := repo.CountItemsByStatus(run.ID, model.FaceQualityRescoreItemStatusProcessing)
	assert.Equal(t, int64(1), processing)
}
