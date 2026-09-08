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

// 任务 2：人物管理人工排除在无质检服务时仍可独立完成；重检只认 face_exclusions。

func TestApplyDetectionResult_PreservesManualExclusionWithoutQuality(t *testing.T) {
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
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.8,
		ClusterStatus: model.FaceClusterStatusExcluded,
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
			BBox: model.BoundingBox{X: 0.11, Y: 0.11, Width: 0.19, Height: 0.19},
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
}

func TestApplyDetectionResult_LowQualityCountsButNotClustered(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "lq.jpg")

	svc, db := newPeopleServiceForTest(t, nil)
	svc.faceQualityRepo = nil
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	photo := &model.Photo{
		FilePath: photoPath, FileName: "lq.jpg", FileSize: 1, FileHash: "lq",
		Width: 320, Height: 320, Status: model.PhotoStatusActive, AIAnalyzed: true, MainCategory: "人物",
		FaceProcessStatus: model.FaceProcessStatusReady,
	}
	require.NoError(t, photoRepo.Create(photo))
	require.NoError(t, db.Create(&model.FaceExclusion{
		PhotoID: photo.ID, SourceFaceID: 1,
		Reason: model.ExclusionReasonLowQuality, Source: model.ExclusionSourceManual,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
	}).Error)

	job := &model.PeopleJob{
		PhotoID: photo.ID, FilePath: photo.FilePath,
		Status: model.PeopleJobStatusProcessing, Source: model.PeopleJobSourceManual,
		WorkerID: "w1", Priority: 10, QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	result := &model.PeopleDetectionResult{
		Faces: []model.PeopleDetectionFace{{
			BBox: model.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
			Confidence: 0.95, QualityScore: 0.9, Embedding: []float32{1, 0, 0},
		}},
	}
	require.NoError(t, svc.ApplyDetectionResult(job, photo, result))

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, model.FaceClusterStatusExcluded, faces[0].ClusterStatus)
	assert.Nil(t, faces[0].PersonID)

	updatedPhoto, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, updatedPhoto.FaceCount, "low_quality 计入 face_count")
}

func TestApplyDetectionResult_RestoreNotResurrectedByOldQualityEvent(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "restore.jpg")

	svc, db := newPeopleServiceForTest(t, nil)
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")
	// 故意保留 faceQualityRepo，验证旧质检事件不能复活已撤销排除。

	photoRepo := repository.NewPhotoRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	photo := &model.Photo{
		FilePath: photoPath, FileName: "restore.jpg", FileSize: 1, FileHash: "restore",
		Width: 320, Height: 320, Status: model.PhotoStatusActive, AIAnalyzed: true, MainCategory: "人物",
		FaceProcessStatus: model.FaceProcessStatusReady, FaceCount: 1,
	}
	require.NoError(t, photoRepo.Create(photo))

	face := &model.Face{
		PhotoID: photo.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.8, ClusterStatus: model.FaceClusterStatusPending,
	}
	require.NoError(t, faceRepo.Create(face))

	_, err := svc.UpdateFaceExclusion([]uint{face.ID}, true, model.ExclusionReasonNonFace)
	require.NoError(t, err)
	_, err = svc.UpdateFaceExclusion([]uint{face.ID}, false, "")
	require.NoError(t, err)

	fid := face.ID
	require.NoError(t, db.Create(&model.FaceQualityEvent{
		PhotoID: photo.ID, FaceID: &fid,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Decision: model.FaceQualityDecisionNonFace, Reason: model.ExclusionReasonNonFace,
		Source: model.FaceQualitySourceManual, RuleVersion: "v1", ModelVersion: "m1",
		ReviewAction: model.FaceQualityReviewActionMarkNonFace, IsCurrent: true,
	}).Error)

	job := &model.PeopleJob{
		PhotoID: photo.ID, FilePath: photo.FilePath,
		Status: model.PeopleJobStatusProcessing, Source: model.PeopleJobSourceManual,
		WorkerID: "w1", Priority: 10, QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	result := &model.PeopleDetectionResult{
		Faces: []model.PeopleDetectionFace{{
			BBox: model.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
			Confidence: 0.95, QualityScore: 0.9, Embedding: []float32{1, 0, 0},
		}},
	}
	require.NoError(t, svc.ApplyDetectionResult(job, photo, result))

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus,
		"人工恢复后旧质检排除事件不得重新施加")
}

func TestApplyDetectionResult_NoAutoReviewRequiredEvenWithEnforceConfig(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "noforce.jpg")

	svc, db := newPeopleServiceForTest(t, nil)
	svc.faceQualityRepo = nil
	svc.config.People.FaceQualityMode = "enforce"
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photoRepo := repository.NewPhotoRepository(db)
	faceRepo := repository.NewFaceRepository(db)
	jobRepo := repository.NewPeopleJobRepository(db)

	photo := &model.Photo{
		FilePath: photoPath, FileName: "noforce.jpg", FileSize: 1, FileHash: "noforce",
		Width: 320, Height: 320, Status: model.PhotoStatusActive, AIAnalyzed: true, MainCategory: "人物",
	}
	require.NoError(t, photoRepo.Create(photo))

	job := &model.PeopleJob{
		PhotoID: photo.ID, FilePath: photo.FilePath,
		Status: model.PeopleJobStatusProcessing, Source: model.PeopleJobSourceManual,
		WorkerID: "w1", Priority: 10, QueuedAt: time.Now(),
	}
	require.NoError(t, jobRepo.Create(job))

	result := &model.PeopleDetectionResult{
		Faces: []model.PeopleDetectionFace{{
			BBox: model.BoundingBox{X: 0.1, Y: 0.1, Width: 0.2, Height: 0.2},
			Confidence: 0.95, QualityScore: 0.9, Embedding: []float32{1, 0, 0},
			VerifierStatus: "no_face",
		}},
	}
	require.NoError(t, svc.ApplyDetectionResult(job, photo, result))

	faces, err := faceRepo.ListByPhotoID(photo.ID)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus,
		"下线自动质检后不得再因 verifier 强制 review_required")

	updatedPhoto, err := photoRepo.GetByID(photo.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, updatedPhoto.FaceCount)

	var evtCount int64
	db.Model(&model.FaceQualityEvent{}).Count(&evtCount)
	assert.Equal(t, int64(0), evtCount, "正常检测不得再写自动质检事件")
}

// 质检页人工排除 → 写入 face_exclusions(source=manual) → 重检后仍排除。
// 故意保留 faceQualityRepo，并在重检前清空 is_current 质检事件，证明闭环只靠 exclusion 表。
func TestApplyQualityDecision_ExcludeSurvivesRedetectionViaFaceExclusions(t *testing.T) {
	rootDir := t.TempDir()
	photoPath := createTestImageFile(t, rootDir, "q-exclude.jpg")

	svc, db := newPeopleServiceForTest(t, nil)
	svc.config.Photos.ThumbnailPath = filepath.Join(rootDir, ".thumbnails")

	photo := &model.Photo{
		FilePath: photoPath, FileName: "q-exclude.jpg", FileSize: 1, FileHash: "q-excl",
		Width: 320, Height: 320, Status: model.PhotoStatusActive, AIAnalyzed: true, MainCategory: "人物",
		FaceProcessStatus: model.FaceProcessStatusReady, FaceCount: 1,
	}
	require.NoError(t, db.Create(photo).Error)

	face := &model.Face{
		PhotoID: photo.ID,
		BBoxX: 0.2, BBoxY: 0.2, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.8,
		ClusterStatus: model.FaceClusterStatusPending,
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

	var exclusion model.FaceExclusion
	require.NoError(t, db.Where("source_face_id = ?", face.ID).First(&exclusion).Error)
	assert.Equal(t, model.ExclusionReasonNonFace, exclusion.Reason)
	assert.Equal(t, model.ExclusionSourceManual, exclusion.Source)

	// 清掉全部质检事件，逼路径只能走 face_exclusions。
	require.NoError(t, db.Where("photo_id = ?", photo.ID).Delete(&model.FaceQualityEvent{}).Error)

	job := &model.PeopleJob{
		PhotoID: photo.ID, FilePath: photo.FilePath,
		Status: model.PeopleJobStatusProcessing, Source: model.PeopleJobSourceManual,
		WorkerID: "w1", Priority: 10, QueuedAt: time.Now(),
	}
	require.NoError(t, db.Create(job).Error)

	result := &model.PeopleDetectionResult{
		Faces: []model.PeopleDetectionFace{{
			BBox: model.BoundingBox{X: 0.21, Y: 0.21, Width: 0.19, Height: 0.19},
			Confidence: 0.95, QualityScore: 0.9, Embedding: []float32{1, 0, 0},
		}},
	}
	require.NoError(t, svc.ApplyDetectionResult(job, photo, result))

	var faces []model.Face
	require.NoError(t, db.Where("photo_id = ?", photo.ID).Find(&faces).Error)
	require.Len(t, faces, 1)
	assert.NotEqual(t, face.ID, faces[0].ID)
	assert.Equal(t, model.FaceClusterStatusExcluded, faces[0].ClusterStatus)
	assert.Equal(t, model.ExclusionReasonNonFace, faces[0].ExclusionReason)

	var p model.Photo
	require.NoError(t, db.First(&p, photo.ID).Error)
	assert.Equal(t, 0, p.FaceCount)
}
