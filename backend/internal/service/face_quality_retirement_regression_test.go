package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReview_RestoreAfterRedetectionRemovesExclusion(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "keep-exclusion.jpg")

	svc, db := newPeopleServiceForTest(t, nil)
	svc.faceQualityRepo = nil
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	photo := &model.Photo{
		FilePath: photoPath, FileName: "keep-exclusion.jpg", FileSize: 1, FileHash: "keep-excl",
		Width: 320, Height: 320, Status: model.PhotoStatusActive, AIAnalyzed: true, MainCategory: "人物",
		FaceProcessStatus: model.FaceProcessStatusReady, FaceCount: 0,
	}
	require.NoError(t, photoRepo.Create(photo))

	oldFace := &model.Face{
		PhotoID: photo.ID,
		BBoxX:   0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.8,
		ClusterStatus:   model.FaceClusterStatusExcluded,
		ExclusionReason: model.ExclusionReasonNonFace,
	}
	require.NoError(t, faceRepo.Create(oldFace))
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: oldFace.ID,
		Reason: model.ExclusionReasonNonFace, Source: model.ExclusionSourceManual,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
	}).Error)

	job := &model.PeopleJob{
		PhotoID: photo.ID, FilePath: photo.FilePath,
		Status: model.PeopleJobStatusProcessing, Source: model.PeopleJobSourceManual,
		WorkerID: "w1", Priority: 10, QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	// 重检：框略有偏移但仍高于 IoU 0.3，face ID 会变。
	result := &model.PeopleDetectionResult{
		Faces: []model.PeopleDetectionFace{{
			BBox:       model.BoundingBox{X: 0.11, Y: 0.11, Width: 0.19, Height: 0.19},
			Confidence: 0.95, QualityScore: 0.9, Embedding: []float32{1, 0, 0},
		}},
	}
	require.NoError(t, svc.ApplyDetectionResult(job, photo, result))

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.NotEqual(t, oldFace.ID, faces[0].ID)
	assert.Equal(t, model.FaceClusterStatusExcluded, faces[0].ClusterStatus)
	assert.Equal(t, model.ExclusionReasonNonFace, faces[0].ExclusionReason)

	updatedPhoto, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, updatedPhoto.FaceCount, "non_face 不计入 face_count")
	_, err = svc.UpdateFaceExclusion([]uint{faces[0].ID}, false, "")
	require.NoError(t, err)
	var remaining int64
	require.NoError(t, db.Model(&model.FaceExclusion{}).Where("photo_id = ?", photo.ID).Count(&remaining).Error)
	assert.Equal(t, int64(0), remaining, "restoring redetected face must remove old bbox exclusion")

	nextJob := &model.PeopleJob{PhotoID: photo.ID, FilePath: photo.FilePath, Status: model.PeopleJobStatusProcessing, Source: model.PeopleJobSourceManual, WorkerID: "w1", Priority: 10, QueuedAt: time.Now()}
	require.NoError(t, jobRepo.Create(nextJob))
	require.NoError(t, svc.ApplyDetectionResult(nextJob, photo, result))
	finalFaces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, finalFaces, 1)
	assert.Equal(t, model.FaceClusterStatusPending, finalFaces[0].ClusterStatus)

}

func TestReview_OrphanManualExclusionNotClearedAsAuto(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	photo := &model.Photo{FilePath: "/review.jpg", FileHash: "review"}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{PhotoID: photo.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonNonFace}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceExclusion{PhotoID: photo.ID, SourceFaceID: 999999, Source: model.ExclusionSourceManual, Reason: model.ExclusionReasonNonFace, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}).Error)
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: photo.ID, FaceID: &face.ID, Source: model.FaceQualitySourceAuto, Decision: model.FaceQualityDecisionNonFace, RuleVersion: "v1", ModelVersion: "m", EvidenceOrigin: model.FaceQualityEvidenceOriginHistoricalBackfill, IsCurrent: true}).Error)
	plan, err := PlanFaceQualityRetirement(db)
	require.NoError(t, err)
	for _, item := range plan.Items {
		if item.FaceID == face.ID {
			assert.NotEqual(t, RetirementCategoryClearAutoExclude, item.Category, "manual bbox exclusion must protect replacement face")
		}
	}
}

func TestReview_NewItemAfterPreviewRejected(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	plan, err := PlanFaceQualityRetirement(db)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.Face{PhotoID: 1, ClusterStatus: model.FaceClusterStatusReviewRequired}).Error)
	_, err = ApplyFaceQualityRetirement(db, plan)
	require.ErrorIs(t, err, errRetirementFingerprintMismatch)
	var count int64
	require.NoError(t, db.Model(&model.AppConfig{}).Where("key = ?", FaceQualityRetirementMigrationKey).Count(&count).Error)
	assert.Zero(t, count)
}

func TestRetirementRestoreLegacyOrphan(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)
	photo := &model.Photo{FilePath: "/legacy.jpg", FileHash: "legacy"}
	require.NoError(t, db.Create(photo).Error)
	face := &model.Face{PhotoID: photo.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonLowQuality}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceExclusion{PhotoID: photo.ID, SourceFaceID: 999999, Source: model.ExclusionSourceManual, Reason: model.ExclusionReasonLowQuality, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}).Error)
	_, err := svc.UpdateFaceExclusion([]uint{face.ID}, false, "")
	require.NoError(t, err)
	var count int64
	require.NoError(t, db.Model(&model.FaceExclusion{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestRetirementRejectsEditedAction(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	face := &model.Face{PhotoID: 1, ClusterStatus: model.FaceClusterStatusExcluded, ExclusionReason: model.ExclusionReasonLowQuality}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.FaceQualityEvent{PhotoID: 1, FaceID: &face.ID, Source: model.FaceQualitySourceManual, Decision: model.FaceQualityDecisionLowQuality, RuleVersion: "v1", ModelVersion: "m", IsCurrent: true}).Error)
	plan, err := PlanFaceQualityRetirement(db)
	require.NoError(t, err)
	require.Len(t, plan.Items, 1)
	plan.Items[0].ExclusionReason = model.ExclusionReasonNonFace
	_, err = ApplyFaceQualityRetirement(db, plan)
	require.ErrorIs(t, err, errRetirementFingerprintMismatch)
}

func TestRetirementRefreshDerivedState(t *testing.T) {
	db := setupPeopleServiceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.PersonIdentityProfile{}))
	photo := &model.Photo{FilePath: "/derived.jpg", FileHash: "derived", FaceCount: 99}
	require.NoError(t, db.Create(photo).Error)
	person := &model.Person{Category: model.PersonCategoryFamily, FaceCount: 99, PhotoCount: 99}
	require.NoError(t, db.Create(person).Error)
	face := &model.Face{PhotoID: photo.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusManual, QualityScore: 0.8}
	require.NoError(t, db.Create(face).Error)
	require.NoError(t, db.Create(&model.AppConfig{Key: personMergeSuggestionStateKey, Value: `{"paused":true,"dirty":false,"cursor_target_id":123,"dirty_generation":3}`}).Error)
	require.NoError(t, refreshRetirementDerivedState(db, []RetirementItem{{OldPersonID: &person.ID}}, []uint{photo.ID}))
	require.NoError(t, db.First(person, person.ID).Error)
	assert.Equal(t, 1, person.FaceCount)
	assert.Equal(t, 1, person.PhotoCount)
	require.NotNil(t, person.RepresentativeFaceID)
	assert.Equal(t, face.ID, *person.RepresentativeFaceID)
	require.NoError(t, db.First(photo, photo.ID).Error)
	assert.Equal(t, 1, photo.FaceCount)
	var profile model.PersonIdentityProfile
	require.NoError(t, db.Where("person_id = ?", person.ID).First(&profile).Error)
	assert.Equal(t, model.PersonIdentityProfileStatusDirty, profile.Status)
	var state model.AppConfig
	require.NoError(t, db.Where("key = ?", personMergeSuggestionStateKey).First(&state).Error)
	assert.Contains(t, state.Value, `"paused":true`)
	assert.Contains(t, state.Value, `"dirty":true`)
	assert.Contains(t, state.Value, `"cursor_target_id":0`)
}
