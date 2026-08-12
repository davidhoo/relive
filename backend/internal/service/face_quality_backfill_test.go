package service

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 存量审计：未处理 Face 应被生成候选审计事件，不改 Face 排除态/人物归属。
func TestFaceQualityBackfill_GeneratesCandidatesWithoutChangingOwnership(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady, FaceCount: 1}
	require.NoError(t, db.Create(photo).Error)

	// 一张已聚类人脸（有证据快照，validity 高 → shadow 回放为 accepted）
	face := &model.Face{
		PhotoID: photo.ID, PersonID: &person.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
		FaceValidityScore: 0.9, QualityRuleVersion: "v1", QualityModelVersion: "test-v1",
	}
	require.NoError(t, db.Create(face).Error)

	cfgRepo := repository.NewConfigRepository(db)
	b := NewFaceQualityBackfill(svc, nil, cfgRepo)

	done, err := b.runOnce()
	require.NoError(t, err)
	assert.False(t, done) // 还有下一批要确认

	// 应生成一条 auto accepted 当前事件
	var evts []model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ?", photo.ID).Find(&evts).Error)
	require.Len(t, evts, 1)
	assert.Equal(t, model.FaceQualitySourceAuto, evts[0].Source)
	assert.True(t, evts[0].IsCurrent)

	// Face 排除态与人物归属未变
	var updated model.Face
	require.NoError(t, db.First(&updated, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusAssigned, updated.ClusterStatus)
	require.NotNil(t, updated.PersonID)
	assert.Equal(t, person.ID, *updated.PersonID)

	// 进度已推进
	assert.Equal(t, uint64(face.ID), b.Progress())
}

// 存量审计：无证据快照的旧 Face → review_required（fail-closed，交人工）。
func TestFaceQualityBackfill_LegacyFaceWithoutEvidenceGoesReview(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	// 旧 Face：无证据快照字段
	face := &model.Face{
		PhotoID: photo.ID,
		BBoxX: 0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}
	require.NoError(t, db.Create(face).Error)

	cfgRepo := repository.NewConfigRepository(db)
	b := NewFaceQualityBackfill(svc, nil, cfgRepo)
	_, err := b.runOnce()
	require.NoError(t, err)

	var evt model.FaceQualityEvent
	require.NoError(t, db.Where("photo_id = ? AND is_current = ?", photo.ID, true).First(&evt).Error)
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, evt.Decision)
	assert.Equal(t, model.FaceQualitySourceAuto, evt.Source)
}

// 暂停/继续：Pause 后 runOnce 不处理新批次。
func TestFaceQualityBackfill_PauseResume(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	photo := &model.Photo{FilePath: "/t/p.jpg", FaceProcessStatus: model.FaceProcessStatusReady}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{
		PhotoID: photo.ID, BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusPending,
	}
	require.NoError(t, db.Create(face).Error)

	cfgRepo := repository.NewConfigRepository(db)
	b := NewFaceQualityBackfill(svc, nil, cfgRepo)
	b.Pause()
	assert.True(t, b.IsPaused())

	// 暂停态下 runOnce 应被跳过（不产生事件）
	_, err := b.runOnce()
	require.NoError(t, err)
	var cnt int64
	db.Model(&model.FaceQualityEvent{}).Where("photo_id = ?", photo.ID).Count(&cnt)
	assert.Equal(t, int64(0), cnt)

	b.Resume()
	assert.False(t, b.IsPaused())
	_, err = b.runOnce()
	require.NoError(t, err)
	db.Model(&model.FaceQualityEvent{}).Where("photo_id = ?", photo.ID).Count(&cnt)
	assert.Equal(t, int64(1), cnt)
}

// 进度持久化：重启后从 app_config 恢复。
func TestFaceQualityBackfill_ProgressPersists(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	cfgRepo := repository.NewConfigRepository(db)
	b1 := NewFaceQualityBackfill(svc, nil, cfgRepo)
	b1.saveProgress(42)

	// 模拟重启：新实例加载进度
	b2 := NewFaceQualityBackfill(svc, nil, cfgRepo)
	b2.loadProgress()
	assert.Equal(t, uint64(42), b2.Progress())
}
