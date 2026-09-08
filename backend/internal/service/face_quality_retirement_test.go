package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanFaceQualityRetirement_ClassifiesConservatively(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	now := time.Now().UTC()

	photo := &model.Photo{FilePath: "/p.jpg", FileHash: "h1"}
	require.NoError(t, db.Create(photo).Error)

	// 1) manual exclusion → keep
	fManual := &model.Face{
		PhotoID: photo.ID, BBoxX: 0, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonNonFace, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(fManual).Error)
	exManual := &model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: fManual.ID, Reason: model.ExclusionReasonNonFace,
		Source: model.ExclusionSourceManual, BBoxX: 0, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
	}
	require.NoError(t, db.Create(exManual).Error)

	// 2) auto exclusion + auto event → clear
	fAuto := &model.Face{
		PhotoID: photo.ID, BBoxX: 20, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonNonFace, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(fAuto).Error)
	exAuto := &model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: fAuto.ID, Reason: model.ExclusionReasonNonFace,
		Source: model.ExclusionSourceAuto, BBoxX: 20, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
	}
	require.NoError(t, db.Create(exAuto).Error)
	fidAuto := fAuto.ID
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &fidAuto, Decision: model.FaceQualityDecisionNonFace,
		Reason: model.ExclusionReasonNonFace, Source: model.FaceQualitySourceAuto,
		RuleVersion: "v", ModelVersion: "m", EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		BBoxX: 20, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10, IsCurrent: true,
	}).Error)

	// 3) unknown exclusion + only auto event → unknown（硬约束：有 auto ≠ 可恢复）
	fUnk := &model.Face{
		PhotoID: photo.ID, BBoxX: 40, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonLowQuality, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(fUnk).Error)
	exUnk := &model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: fUnk.ID, Reason: model.ExclusionReasonLowQuality,
		Source: model.ExclusionSourceUnknown, BBoxX: 40, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
	}
	require.NoError(t, db.Create(exUnk).Error)
	fidUnk := fUnk.ID
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &fidUnk, Decision: model.FaceQualityDecisionLowQuality,
		Reason: model.ExclusionReasonLowQuality, Source: model.FaceQualitySourceAuto,
		RuleVersion: "v", ModelVersion: "m", EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		BBoxX: 40, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10, IsCurrent: true,
	}).Error)

	// 4) unknown exclusion + manual event → promote
	fPromo := &model.Face{
		PhotoID: photo.ID, BBoxX: 60, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonNonFace, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(fPromo).Error)
	exPromo := &model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: fPromo.ID, Reason: model.ExclusionReasonNonFace,
		Source: model.ExclusionSourceUnknown, BBoxX: 60, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
	}
	require.NoError(t, db.Create(exPromo).Error)
	fidPromo := fPromo.ID
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &fidPromo, Decision: model.FaceQualityDecisionNonFace,
		Reason: model.ExclusionReasonNonFace, Source: model.FaceQualitySourceManual,
		ReviewAction: model.FaceQualityReviewActionMarkNonFace,
		RuleVersion: "v", ModelVersion: "m", EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		BBoxX: 60, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10, IsCurrent: true,
	}).Error)

	// 5) auto review_required → clear review
	fRev := &model.Face{
		PhotoID: photo.ID, BBoxX: 80, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
		ClusterStatus: model.FaceClusterStatusReviewRequired,
	}
	require.NoError(t, db.Create(fRev).Error)
	fidRev := fRev.ID
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &fidRev, Decision: model.FaceQualityDecisionReviewRequired,
		Source: model.FaceQualitySourceAuto,
		RuleVersion: "v", ModelVersion: "m", EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		BBoxX: 80, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10, IsCurrent: true,
	}).Error)

	// 6) active rescore run → archive
	run := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeCalibration, ApplyMode: model.FaceQualityRescoreApplyModeShadow,
		Status: model.FaceQualityRescoreStatusPaused, RuleVersion: "v", ModelVersion: "m",
		PipelineVersion: model.FaceQualityRescorePipelineIndependentV2,
	}
	require.NoError(t, db.Create(run).Error)

	plan, err := PlanFaceQualityRetirement(db)
	require.NoError(t, err)

	byFace := map[uint]RetirementItem{}
	var archiveCount int
	for _, it := range plan.Items {
		if it.FaceID != 0 {
			byFace[it.FaceID] = it
		}
		if it.Category == RetirementCategoryArchiveRun {
			archiveCount++
		}
	}

	assert.Equal(t, RetirementCategoryKeepManual, byFace[fManual.ID].Category)
	assert.Equal(t, RetirementCategoryClearAutoExclude, byFace[fAuto.ID].Category)
	assert.Equal(t, RetirementCategoryUnknown, byFace[fUnk.ID].Category)
	assert.Equal(t, RetirementCategoryPromoteManual, byFace[fPromo.ID].Category)
	assert.Equal(t, RetirementCategoryClearAutoReview, byFace[fRev.ID].Category)
	assert.Equal(t, 1, archiveCount)
	assert.Equal(t, 1, plan.Summary[RetirementCategoryUnknown])
}

func TestPlanFaceQualityRetirement_DryRunZeroWrites(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	now := time.Now().UTC()
	photo := &model.Photo{FilePath: "/p2.jpg", FileHash: "h2"}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{
		PhotoID: photo.ID, BBoxX: 0, BBoxY: 0, BBoxWidth: 5, BBoxHeight: 5,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonNonFace, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: face.ID, Reason: model.ExclusionReasonNonFace,
		Source: model.ExclusionSourceAuto, BBoxX: 0, BBoxY: 0, BBoxWidth: 5, BBoxHeight: 5,
	}).Error)
	fid := face.ID
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &fid, Decision: model.FaceQualityDecisionNonFace,
		Reason: model.ExclusionReasonNonFace, Source: model.FaceQualitySourceAuto,
		RuleVersion: "v", ModelVersion: "m", EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		BBoxX: 0, BBoxY: 0, BBoxWidth: 5, BBoxHeight: 5, IsCurrent: true,
	}).Error)

	var beforeExcl, afterExcl int64
	require.NoError(t, db.Model(&model.FaceExclusion{}).Count(&beforeExcl).Error)
	_, err := PlanFaceQualityRetirement(db)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.FaceExclusion{}).Count(&afterExcl).Error)
	assert.Equal(t, beforeExcl, afterExcl)

	var faceAfter model.Face
	require.NoError(t, db.First(&faceAfter, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, faceAfter.ClusterStatus)

	var cfgCount int64
	require.NoError(t, db.Model(&model.AppConfig{}).Where("key = ?", FaceQualityRetirementMigrationKey).Count(&cfgCount).Error)
	assert.Equal(t, int64(0), cfgCount)
}

func TestApplyFaceQualityRetirement_ClearsAutoKeepsManualAndUnknown(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	now := time.Now().UTC()
	photo := &model.Photo{FilePath: "/p3.jpg", FileHash: "h3", FaceCount: 0}
	require.NoError(t, db.Create(photo).Error)

	fManual := &model.Face{
		PhotoID: photo.ID, BBoxX: 0, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonNonFace, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(fManual).Error)
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: fManual.ID, Reason: model.ExclusionReasonNonFace,
		Source: model.ExclusionSourceManual, BBoxX: 0, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
	}).Error)

	fAuto := &model.Face{
		PhotoID: photo.ID, BBoxX: 20, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonNonFace, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(fAuto).Error)
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: fAuto.ID, Reason: model.ExclusionReasonNonFace,
		Source: model.ExclusionSourceAuto, BBoxX: 20, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
	}).Error)
	fidAuto := fAuto.ID
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &fidAuto, Decision: model.FaceQualityDecisionNonFace,
		Reason: model.ExclusionReasonNonFace, Source: model.FaceQualitySourceAuto,
		RuleVersion: "v", ModelVersion: "m", EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		BBoxX: 20, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10, IsCurrent: true,
	}).Error)

	fUnk := &model.Face{
		PhotoID: photo.ID, BBoxX: 40, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonLowQuality, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(fUnk).Error)
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: fUnk.ID, Reason: model.ExclusionReasonLowQuality,
		Source: model.ExclusionSourceUnknown, BBoxX: 40, BBoxY: 0, BBoxWidth: 10, BBoxHeight: 10,
	}).Error)

	run := &model.FaceQualityRescoreRun{
		Mode: model.FaceQualityRescoreModeFull, ApplyMode: model.FaceQualityRescoreApplyModeEnforce,
		Status: model.FaceQualityRescoreStatusRunning, RuleVersion: "v", ModelVersion: "m",
		PipelineVersion: model.FaceQualityRescorePipelineIndependentV2,
	}
	require.NoError(t, db.Create(run).Error)

	plan, err := PlanFaceQualityRetirement(db)
	require.NoError(t, err)
	result, err := ApplyFaceQualityRetirement(db, plan)
	require.NoError(t, err)
	require.False(t, result.Idempotent)

	var manualFace, autoFace, unkFace model.Face
	require.NoError(t, db.First(&manualFace, fManual.ID).Error)
	require.NoError(t, db.First(&autoFace, fAuto.ID).Error)
	require.NoError(t, db.First(&unkFace, fUnk.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, manualFace.ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusPending, autoFace.ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusExcluded, unkFace.ClusterStatus)

	var autoExclCount int64
	require.NoError(t, db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", fAuto.ID).Count(&autoExclCount).Error)
	assert.Equal(t, int64(0), autoExclCount)

	var runAfter model.FaceQualityRescoreRun
	require.NoError(t, db.First(&runAfter, run.ID).Error)
	assert.Equal(t, model.FaceQualityRescoreStatusCancelled, runAfter.Status)

	var photoAfter model.Photo
	require.NoError(t, db.First(&photoAfter, photo.ID).Error)
	// pending(auto) + excluded low_quality(unk) 计入；manual non_face 不计
	assert.Equal(t, 2, photoAfter.FaceCount)

	// 幂等第二次
	result2, err := ApplyFaceQualityRetirement(db, plan)
	require.NoError(t, err)
	assert.True(t, result2.Idempotent)
}

func TestApplyFaceQualityRetirement_FingerprintMismatchAborts(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	now := time.Now().UTC()
	photo := &model.Photo{FilePath: "/p4.jpg", FileHash: "h4"}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{
		PhotoID: photo.ID, BBoxX: 0, BBoxY: 0, BBoxWidth: 8, BBoxHeight: 8,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonNonFace, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: face.ID, Reason: model.ExclusionReasonNonFace,
		Source: model.ExclusionSourceAuto, BBoxX: 0, BBoxY: 0, BBoxWidth: 8, BBoxHeight: 8,
	}).Error)
	fid := face.ID
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &fid, Decision: model.FaceQualityDecisionNonFace,
		Reason: model.ExclusionReasonNonFace, Source: model.FaceQualitySourceAuto,
		RuleVersion: "v", ModelVersion: "m", EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		BBoxX: 0, BBoxY: 0, BBoxWidth: 8, BBoxHeight: 8, IsCurrent: true,
	}).Error)

	plan, err := PlanFaceQualityRetirement(db)
	require.NoError(t, err)

	// 预演后人工改写：把 exclusion 标成 manual
	require.NoError(t, db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", face.ID).
		Update("source", model.ExclusionSourceManual).Error)

	_, err = ApplyFaceQualityRetirement(db, plan)
	require.Error(t, err)
	assert.ErrorIs(t, err, errRetirementFingerprintMismatch)

	var faceAfter model.Face
	require.NoError(t, db.First(&faceAfter, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, faceAfter.ClusterStatus)

	var cfgCount int64
	require.NoError(t, db.Model(&model.AppConfig{}).Where("key = ?", FaceQualityRetirementMigrationKey).Count(&cfgCount).Error)
	assert.Equal(t, int64(0), cfgCount)
}

func TestApplyFaceQualityRetirement_TransfersManualOrphanExclusion(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	now := time.Now().UTC()
	photo := &model.Photo{FilePath: "/orphan.jpg", FileHash: "ho"}
	require.NoError(t, db.Create(photo).Error)

	face := &model.Face{
		PhotoID: photo.ID, BBoxX: 1, BBoxY: 1, BBoxWidth: 9, BBoxHeight: 9,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonNonFace, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(face).Error)
	fid := face.ID
	// 故意不写 face_exclusions：模拟质检页人工排除孤儿。
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &fid, Decision: model.FaceQualityDecisionNonFace,
		Reason: model.ExclusionReasonNonFace, Source: model.FaceQualitySourceManual,
		ReviewAction: model.FaceQualityReviewActionMarkNonFace,
		RuleVersion: "v", ModelVersion: "m", EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		BBoxX: 1, BBoxY: 1, BBoxWidth: 9, BBoxHeight: 9, IsCurrent: true,
	}).Error)

	plan, err := PlanFaceQualityRetirement(db)
	require.NoError(t, err)
	require.Equal(t, 1, plan.Summary[RetirementCategoryTransferManualOrphan])

	_, err = ApplyFaceQualityRetirement(db, plan)
	require.NoError(t, err)

	var excl model.FaceExclusion
	require.NoError(t, db.Where("source_face_id = ?", face.ID).First(&excl).Error)
	assert.Equal(t, model.ExclusionSourceManual, excl.Source)
	assert.Equal(t, model.ExclusionReasonNonFace, excl.Reason)

	var faceAfter model.Face
	require.NoError(t, db.First(&faceAfter, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, faceAfter.ClusterStatus)
}

func TestPlanFaceQualityRetirement_RestoreAutoManualEventNotTransferred(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	now := time.Now().UTC()
	photo := &model.Photo{FilePath: "/restore.jpg", FileHash: "hr"}
	require.NoError(t, db.Create(photo).Error)

	// 脸仍 excluded，但当前事件是 RestoreAuto 写的 source=manual + restore。
	// 不得当成人工排除孤儿转存；应 unknown（冲突）。
	face := &model.Face{
		PhotoID: photo.ID, BBoxX: 2, BBoxY: 2, BBoxWidth: 8, BBoxHeight: 8,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonNonFace, ExcludedAt: &now,
	}
	require.NoError(t, db.Create(face).Error)
	fid := face.ID
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &fid, Decision: model.FaceQualityDecisionAccepted,
		Source: model.FaceQualitySourceManual, ReviewAction: model.FaceQualityReviewActionRestore,
		RuleVersion: "v", ModelVersion: "m", EvidencePipeline: model.FaceQualityEvidencePipelineIndependentV2,
		BBoxX: 2, BBoxY: 2, BBoxWidth: 8, BBoxHeight: 8, IsCurrent: true,
	}).Error)

	plan, err := PlanFaceQualityRetirement(db)
	require.NoError(t, err)
	require.Equal(t, 0, plan.Summary[RetirementCategoryTransferManualOrphan])
	require.Equal(t, 1, plan.Summary[RetirementCategoryUnknown])
}
