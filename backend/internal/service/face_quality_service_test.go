package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }

// evidenceFor 构造一个可控的质检证据，便于触发各类策略分支。
func evidenceFor(validity float64, sharpness float64, pixelW int, reasons ...string) *model.FaceQualityEvidence {
	return &model.FaceQualityEvidence{
		FaceValidityScore:     validity,
		PixelWidth:            pixelW,
		PixelHeight:           pixelW,
		Sharpness:             sharpness,
		LandmarkCompleteness:  1.0,
		LandmarkGeometryScore: 1.0,
		QualityReasons:        reasons,
		RuleVersion:           "v1",
		ModelVersion:          "test-v1",
	}
}

func TestEvaluateFaceQuality_HighConfidenceNonFaceAutoExclude(t *testing.T) {
	ev := evidenceFor(0.2, 50, 80, "invalid_landmarks", "bad_geometry")
	out := evaluateFaceQuality(ev, "enforce")
	assert.Equal(t, model.FaceQualityActionExclude, out.Action)
	assert.Equal(t, model.FaceQualityDecisionNonFace, out.Decision)
	assert.Equal(t, model.ExclusionReasonNonFace, out.Reason)
}

func TestEvaluateFaceQuality_HighConfidenceNonFaceShadowStaysReview(t *testing.T) {
	ev := evidenceFor(0.2, 50, 80, "bad_geometry")
	out := evaluateFaceQuality(ev, "shadow")
	assert.Equal(t, model.FaceQualityActionReviewRequired, out.Action)
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, out.Decision)
}

func TestEvaluateFaceQuality_SevereLowQualityAutoExclude(t *testing.T) {
	// validity 通过（>= 0.6）但严重模糊
	ev := evidenceFor(0.85, 10, 80)
	out := evaluateFaceQuality(ev, "enforce")
	assert.Equal(t, model.FaceQualityActionExclude, out.Action)
	assert.Equal(t, model.ExclusionReasonLowQuality, out.Reason)
}

func TestEvaluateFaceQuality_TooSmallAutoExclude(t *testing.T) {
	ev := evidenceFor(0.85, 200, 16) // 像素过小
	out := evaluateFaceQuality(ev, "enforce")
	assert.Equal(t, model.FaceQualityActionExclude, out.Action)
	assert.Equal(t, model.ExclusionReasonLowQuality, out.Reason)
}

func TestEvaluateFaceQuality_GrayZoneReviewRequired(t *testing.T) {
	// validity 在 0.5-0.6 之间 → 灰区
	ev := evidenceFor(0.55, 200, 80)
	out := evaluateFaceQuality(ev, "enforce")
	assert.Equal(t, model.FaceQualityActionReviewRequired, out.Action)
}

func TestEvaluateFaceQuality_NilEvidenceFailClosedReview(t *testing.T) {
	out := evaluateFaceQuality(nil, "enforce")
	assert.Equal(t, model.FaceQualityActionReviewRequired, out.Action)
	// fail-closed：不伪装 non_face
	assert.NotEqual(t, model.FaceQualityDecisionNonFace, out.Decision)
}

func TestEvaluateFaceQuality_DisabledModeAcceptsAll(t *testing.T) {
	// 即使是高确定性非人脸，disabled 也接受
	ev := evidenceFor(0.1, 5, 10, "bad_geometry")
	out := evaluateFaceQuality(ev, "disabled")
	assert.Equal(t, model.FaceQualityActionAccept, out.Action)
}

func TestEvaluateFaceQuality_AcceptGoodSample(t *testing.T) {
	ev := evidenceFor(0.9, 300, 100)
	out := evaluateFaceQuality(ev, "enforce")
	assert.Equal(t, model.FaceQualityActionAccept, out.Action)
	assert.Equal(t, model.FaceQualityDecisionAccepted, out.Decision)
}

// ApplyQualityDecision 集成测试：人工接受优先于自动排除。
func TestApplyQualityDecision_ManualAcceptOverridesAutoExclude(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady, FaceCount: 0}
	require.NoError(t, db.Create(photo).Error)

	// 一张已被自动排除（non_face）的人脸
	face := &model.Face{
		PhotoID:          photo.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus:    model.FaceClusterStatusExcluded,
		ExclusionReason:  model.ExclusionReasonNonFace,
		QualityRuleVersion: "v1",
	}
	require.NoError(t, db.Create(face).Error)

	// 对应的自动质检事件（is_current）
	autoEvent := &model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionNonFace, Reason: model.ExclusionReasonNonFace,
		Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		IsCurrent: true,
	}
	require.NoError(t, db.Create(autoEvent).Error)

	// face_exclusions 记录（与自动排除对应）
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: face.ID, Reason: model.ExclusionReasonNonFace,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
	}).Error)

	fqs := NewFaceQualityService(svc)
	res, err := fqs.ApplyQualityDecision(model.FaceQualityDecisionRequest{
		EventIDs: []uint{autoEvent.ID},
		Action:   model.FaceQualityReviewActionAccept,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Processed)

	// Face 应回到 pending（人工接受 > 自动排除）
	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusPending, updated.ClusterStatus)
	assert.Empty(t, updated.ExclusionReason)

	// face_exclusions 应被删除
	var count int64
	db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", face.ID).Count(&count)
	assert.Equal(t, int64(0), count)

	// 新的人工事件为当前态，自动事件失活
	var current model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ? AND is_current = ?", photo.ID, true).First(&current).Error)
	assert.Equal(t, model.FaceQualitySourceManual, current.Source)
	assert.Equal(t, model.FaceQualityDecisionAccepted, current.Decision)
}

// ApplyQualityDecision 人工排除优先于自动接受。
func TestApplyQualityDecision_ManualExcludeOverridesAutoAccept(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady, FaceCount: 1}
	require.NoError(t, db.Create(photo).Error)

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	face := &model.Face{
		PhotoID: photo.ID, PersonID: &person.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
		QualityRuleVersion: "v1",
	}
	require.NoError(t, db.Create(face).Error)

	autoEvent := &model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionAccepted, Source: model.FaceQualitySourceAuto,
		RuleVersion: "v1", ModelVersion: "test-v1", IsCurrent: true,
	}
	require.NoError(t, db.Create(autoEvent).Error)

	fqs := NewFaceQualityService(svc)
	_, err := fqs.ApplyQualityDecision(model.FaceQualityDecisionRequest{
		EventIDs: []uint{autoEvent.ID},
		Action:   model.FaceQualityReviewActionMarkNonFace,
	})
	require.NoError(t, err)

	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, updated.ClusterStatus)
	assert.Equal(t, model.ExclusionReasonNonFace, updated.ExclusionReason)
	assert.Nil(t, updated.PersonID)

	// P0 回归：受影响人物应被刷新——原 person 已无任何 face 归属（syncPersonState 已重算）。
	var cnt int64
	db.Model(&model.Face{}).Where("person_id = ?", person.ID).Count(&cnt)
	assert.Equal(t, int64(0), cnt)
}

// ApplyQualityDecision 恢复动作：写 manual 恢复事件，Face 回 pending。
func TestApplyQualityDecision_RestoreWritesManualEvent(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)

	face := &model.Face{
		PhotoID: photo.ID,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonLowQuality,
	}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: face.ID, Reason: model.ExclusionReasonLowQuality,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
	}).Error)
	manEvent := &model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionLowQuality, Reason: model.ExclusionReasonLowQuality,
		Source: model.FaceQualitySourceManual, RuleVersion: "v1", ModelVersion: "test-v1",
		IsCurrent: true,
	}
	require.NoError(t, db.Create(manEvent).Error)

	fqs := NewFaceQualityService(svc)
	_, err := fqs.ApplyQualityDecision(model.FaceQualityDecisionRequest{
		EventIDs: []uint{manEvent.ID}, Action: model.FaceQualityReviewActionRestore,
	})
	require.NoError(t, err)

	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusPending, updated.ClusterStatus)

	// 恢复事件应带 restored_at
	var cur model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ? AND is_current = ?", photo.ID, true).First(&cur).Error)
	assert.Equal(t, model.FaceQualityReviewActionRestore, cur.ReviewAction)
	assert.NotNil(t, cur.RestoredAt)
}

// 重检后人工结论仍生效：旧 Face 被删除、新 Face 创建，按 bbox IoU 回填人工接受。
func TestApplyDetectionResult_ManualDecisionSurvivesRedetection(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "survive.jpg")
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photo := &model.Photo{
		FilePath: photoPath, FileName: "survive.jpg", FileSize: 1, FileHash: "h-survive",
		Width: 100, Height: 100, Status: model.PhotoStatusActive, AIAnalyzed: true, MainCategory: "人物",
		FaceProcessStatus: model.FaceProcessStatusReady, FaceCount: 0,
	}
	require.NoError(t, db.Create(photo).Error)

	// 人工接受事件（is_current，manual accepted）
	manEvent := &model.FaceQualityEvent{
		PhotoID: photo.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionAccepted, Source: model.FaceQualitySourceManual,
		RuleVersion: "v1", ModelVersion: "test-v1", IsCurrent: true,
	}
	require.NoError(t, db.Create(manEvent).Error)

	// 模拟重检：返回同位置的人脸，但无 evidence（disabled 模式下也应被人工结论覆盖为接受）
	result := &model.PeopleDetectionResult{
		Faces: []model.PeopleDetectionFace{
			{BBox: model.BoundingBox{X: 0.2, Y: 0.2, Width: 0.2, Height: 0.2}, Confidence: 0.9, QualityScore: 0.8, Embedding: []float32{0, 1}},
		},
	}
	job := &model.PeopleJob{PhotoID: photo.ID, FilePath: photo.FilePath, Status: model.PeopleJobStatusQueued, Source: model.PeopleJobSourceScan, Priority: 10, QueuedAt: time.Now()}
	require.NoError(t, db.Create(job).Error)

	require.NoError(t, svc.ApplyDetectionResult(job, photo, result))

	// 新 Face 应为 pending（人工接受），而非 review_required（无 evidence 灰区）
	var faces []model.Face
	require.NoError(t, db.Where("photo_id = ?", photo.ID).Find(&faces).Error)
	require.Len(t, faces, 1)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus)

	// 照片 face_count 应为 1（人工接受计入）
	var p model.Photo
	require.NoError(t, db.First(&p, photo.ID).Error)
	assert.Equal(t, 1, p.FaceCount)
}

// confirm_exclude 沿用原事件 reason：low_quality 排除不应被误标为 non_face。
func TestApplyQualityDecision_ConfirmExcludePreservesLowQualityReason(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady, FaceCount: 1}
	require.NoError(t, db.Create(photo).Error)
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	face := &model.Face{
		PhotoID: photo.ID, PersonID: &person.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned, QualityRuleVersion: "v1",
	}
	require.NoError(t, db.Create(face).Error)
	// 原自动事件为 low_quality
	autoEvent := &model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionLowQuality, Reason: model.ExclusionReasonLowQuality,
		Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		IsCurrent: true,
	}
	require.NoError(t, db.Create(autoEvent).Error)

	fqs := NewFaceQualityService(svc)
	_, err := fqs.ApplyQualityDecision(model.FaceQualityDecisionRequest{
		EventIDs: []uint{autoEvent.ID},
		Action:   model.FaceQualityReviewActionConfirmExclude,
		// 不带 reason，应沿用原事件 low_quality
	})
	require.NoError(t, err)

	// Face 应被排除为 low_quality，不是 non_face
	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, updated.ClusterStatus)
	assert.Equal(t, model.ExclusionReasonLowQuality, updated.ExclusionReason)

	// 新人工事件 decision=low_quality
	var cur model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ? AND is_current = ?", photo.ID, true).First(&cur).Error)
	assert.Equal(t, model.FaceQualityDecisionLowQuality, cur.Decision)
	assert.Equal(t, model.ExclusionReasonLowQuality, cur.Reason)
	assert.Equal(t, model.FaceQualitySourceManual, cur.Source)
}

// RestoreAuto 事务原子性回归：Face 恢复与人工恢复审计事件在同一事务内，
// 且旧自动事件失活、新 manual 事件为当前态。
func TestRestoreAuto_AtomicRestoreWithAudit(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{
		PhotoID: photo.ID,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonLowQuality,
		QualityRuleVersion: "v1",
	}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: face.ID, Reason: model.ExclusionReasonLowQuality,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
	}).Error)
	autoEvent := &model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &face.ID,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionLowQuality, Reason: model.ExclusionReasonLowQuality,
		Source: model.FaceQualitySourceAuto, RuleVersion: "v1", ModelVersion: "test-v1",
		IsCurrent: true,
	}
	require.NoError(t, db.Create(autoEvent).Error)

	fqs := NewFaceQualityService(svc)
	res, err := fqs.RestoreAuto("v1", 10)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Restored)

	// Face 回 pending
	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusPending, updated.ClusterStatus)

	// face_exclusions 已删
	var excCount int64
	db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", face.ID).Count(&excCount)
	assert.Equal(t, int64(0), excCount)

	// 旧自动事件失活
	var oldEvt model.FaceQualityEvent
	require.NoError(t, db.First(&oldEvt, autoEvent.ID).Error)
	assert.False(t, oldEvt.IsCurrent)

	// 新 manual 恢复事件为当前态且带 restored_at
	var cur model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ? AND is_current = ?", photo.ID, true).First(&cur).Error)
	assert.Equal(t, model.FaceQualitySourceManual, cur.Source)
	assert.Equal(t, model.FaceQualityReviewActionRestore, cur.ReviewAction)
	assert.NotNil(t, cur.RestoredAt)
}

// 临时变量防 lint。
var _ = time.Now
