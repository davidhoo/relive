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
