package service

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBBoxIoU(t *testing.T) {
	tests := []struct {
		name     string
		ax, ay, aw, ah float64
		bx, by, bw, bh float64
		expected float64
	}{
		{
			name:     "identical boxes",
			ax: 0.1, ay: 0.1, aw: 0.2, ah: 0.2,
			bx: 0.1, by: 0.1, bw: 0.2, bh: 0.2,
			expected: 1.0,
		},
		{
			name:     "no overlap",
			ax: 0.0, ay: 0.0, aw: 0.1, ah: 0.1,
			bx: 0.5, by: 0.5, bw: 0.1, bh: 0.1,
			expected: 0.0,
		},
		{
			name:     "partial overlap",
			ax: 0.0, ay: 0.0, aw: 0.2, ah: 0.2,
			bx: 0.1, by: 0.1, bw: 0.2, bh: 0.2,
			expected: 0.142857, // 0.04 / 0.28 ≈ 0.142857
		},
		{
			name:     "zero area",
			ax: 0.0, ay: 0.0, aw: 0.0, ah: 0.0,
			bx: 0.1, by: 0.1, bw: 0.2, bh: 0.2,
			expected: 0.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := bboxIoU(tt.ax, tt.ay, tt.aw, tt.ah, tt.bx, tt.by, tt.bw, tt.bh)
			if tt.expected == 0 {
				assert.Equal(t, 0.0, result)
			} else {
				assert.InDelta(t, tt.expected, result, 0.001)
			}
		})
	}
}

func TestMatchExclusionRecords_Basic(t *testing.T) {
	records := []*model.FaceExclusion{
		{ID: 1, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Reason: model.ExclusionReasonNonFace},
		{ID: 2, BBoxX: 0.5, BBoxY: 0.5, BBoxWidth: 0.1, BBoxHeight: 0.1, Reason: model.ExclusionReasonLowQuality},
	}

	// Detections: one matches record 1 closely, one doesn't match anything
	detections := []bboxCandidate{
		{x: 0.11, y: 0.11, w: 0.19, h: 0.19}, // IoU with record 1 > 0.3
		{x: 0.8, y: 0.8, w: 0.1, h: 0.1},     // no match
	}

	matches := matchExclusionRecords(detections, records)
	require.Contains(t, matches, 0)
	assert.Equal(t, uint(1), matches[0].ID)
	assert.NotContains(t, matches, 1)
}

func TestMatchExclusionRecords_NoDuplicates(t *testing.T) {
	// Two exclusion records near the same detection
	records := []*model.FaceExclusion{
		{ID: 1, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2, Reason: model.ExclusionReasonNonFace},
		{ID: 2, BBoxX: 0.12, BBoxY: 0.12, BBoxWidth: 0.2, BBoxHeight: 0.2, Reason: model.ExclusionReasonLowQuality},
	}

	// One detection near both - should match only the better one (record 2 has slightly closer center)
	detections := []bboxCandidate{
		{x: 0.12, y: 0.12, w: 0.2, h: 0.2},
	}

	matches := matchExclusionRecords(detections, records)
	require.Len(t, matches, 1)
	assert.Contains(t, matches, 0)
	// Only one record should be matched
	matchedIDs := make(map[uint]struct{})
	for _, rec := range matches {
		matchedIDs[rec.ID] = struct{}{}
	}
	assert.Len(t, matchedIDs, 1)
}

func TestMatchExclusionRecords_EmptyInputs(t *testing.T) {
	assert.Nil(t, matchExclusionRecords(nil, nil))
	assert.Nil(t, matchExclusionRecords(nil, []*model.FaceExclusion{{ID: 1}}))
	assert.Nil(t, matchExclusionRecords([]bboxCandidate{{x: 0, y: 0, w: 0.1, h: 0.1}}, nil))
}

func TestMatchExclusionRecords_BelowThreshold(t *testing.T) {
	records := []*model.FaceExclusion{
		{ID: 1, BBoxX: 0.0, BBoxY: 0.0, BBoxWidth: 0.1, BBoxHeight: 0.1, Reason: model.ExclusionReasonNonFace},
	}
	// Detection with IoU < 0.3
	detections := []bboxCandidate{
		{x: 0.3, y: 0.3, w: 0.1, h: 0.1},
	}
	matches := matchExclusionRecords(detections, records)
	assert.Empty(t, matches)
}

func TestUpdateFaceExclusion_NonFace(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)

	// Create a photo and face
	photo := &model.Photo{
		FilePath:          "/test/photo.jpg",
		FaceProcessStatus: model.FaceProcessStatusReady,
		FaceCount:         1,
	}
	require.NoError(t, db.Create(photo).Error)

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	face := &model.Face{
		PhotoID:       photo.ID,
		PersonID:      &person.ID,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.9,
		QualityScore:  0.8,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}
	require.NoError(t, db.Create(face).Error)

	// Mark as non_face
	result, err := svc.UpdateFaceExclusion([]uint{face.ID}, true, model.ExclusionReasonNonFace)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Updated)

	// Verify face is excluded
	var updatedFace model.Face
	require.NoError(t, db.First(&updatedFace, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, updatedFace.ClusterStatus)
	assert.Equal(t, model.ExclusionReasonNonFace, updatedFace.ExclusionReason)
	assert.Nil(t, updatedFace.PersonID)
	assert.False(t, updatedFace.ManualLocked)
	assert.NotNil(t, updatedFace.ExcludedAt)

	// Verify face_exclusion record exists
	var exclusion model.FaceExclusion
	require.NoError(t, db.Where("source_face_id = ?", face.ID).First(&exclusion).Error)
	assert.Equal(t, model.ExclusionReasonNonFace, exclusion.Reason)
	assert.Equal(t, photo.ID, exclusion.PhotoID)

	// Verify photo face_count was decremented (non_face doesn't count)
	var updatedPhoto model.Photo
	require.NoError(t, db.First(&updatedPhoto, photo.ID).Error)
	assert.Equal(t, 0, updatedPhoto.FaceCount)
}

func TestUpdateFaceExclusion_LowQuality(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)

	photo := &model.Photo{
		FilePath:          "/test/photo.jpg",
		FaceProcessStatus: model.FaceProcessStatusReady,
		FaceCount:         1,
	}
	require.NoError(t, db.Create(photo).Error)

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	face := &model.Face{
		PhotoID:       photo.ID,
		PersonID:      &person.ID,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.9,
		QualityScore:  0.8,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}
	require.NoError(t, db.Create(face).Error)

	// Mark as low_quality
	result, err := svc.UpdateFaceExclusion([]uint{face.ID}, true, model.ExclusionReasonLowQuality)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Updated)

	// Verify face is excluded
	var updatedFace model.Face
	require.NoError(t, db.First(&updatedFace, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusExcluded, updatedFace.ClusterStatus)
	assert.Equal(t, model.ExclusionReasonLowQuality, updatedFace.ExclusionReason)
	assert.Nil(t, updatedFace.PersonID)

	// Verify photo face_count is still 1 (low_quality counts)
	var updatedPhoto model.Photo
	require.NoError(t, db.First(&updatedPhoto, photo.ID).Error)
	assert.Equal(t, 1, updatedPhoto.FaceCount)
}

func TestUpdateFaceExclusion_Idempotent(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)

	photo := &model.Photo{
		FilePath:          "/test/photo.jpg",
		FaceProcessStatus: model.FaceProcessStatusReady,
		FaceCount:         1,
	}
	require.NoError(t, db.Create(photo).Error)

	face := &model.Face{
		PhotoID:       photo.ID,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.9,
		QualityScore:  0.8,
		ClusterStatus: model.FaceClusterStatusPending,
	}
	require.NoError(t, db.Create(face).Error)

	// First exclusion
	_, err := svc.UpdateFaceExclusion([]uint{face.ID}, true, model.ExclusionReasonNonFace)
	require.NoError(t, err)

	// Second exclusion with same reason - should be idempotent
	_, err = svc.UpdateFaceExclusion([]uint{face.ID}, true, model.ExclusionReasonNonFace)
	require.NoError(t, err)

	// Verify only one exclusion record exists
	var count int64
	db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", face.ID).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestUpdateFaceExclusion_ChangeReason(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)

	photo := &model.Photo{
		FilePath:          "/test/photo.jpg",
		FaceProcessStatus: model.FaceProcessStatusReady,
		FaceCount:         1,
	}
	require.NoError(t, db.Create(photo).Error)

	face := &model.Face{
		PhotoID:       photo.ID,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.9,
		QualityScore:  0.8,
		ClusterStatus: model.FaceClusterStatusPending,
	}
	require.NoError(t, db.Create(face).Error)

	// First: non_face
	_, err := svc.UpdateFaceExclusion([]uint{face.ID}, true, model.ExclusionReasonNonFace)
	require.NoError(t, err)

	// face_count should be 0 (non_face doesn't count)
	var photo1 model.Photo
	require.NoError(t, db.First(&photo1, photo.ID).Error)
	assert.Equal(t, 0, photo1.FaceCount)

	// Change to low_quality
	_, err = svc.UpdateFaceExclusion([]uint{face.ID}, true, model.ExclusionReasonLowQuality)
	require.NoError(t, err)

	// Verify reason changed
	var updatedFace model.Face
	require.NoError(t, db.First(&updatedFace, face.ID).Error)
	assert.Equal(t, model.ExclusionReasonLowQuality, updatedFace.ExclusionReason)

	// face_count should now be 1 (low_quality counts)
	var photo2 model.Photo
	require.NoError(t, db.First(&photo2, photo.ID).Error)
	assert.Equal(t, 1, photo2.FaceCount)
}

func TestUpdateFaceExclusion_Restore(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, nil)

	photo := &model.Photo{
		FilePath:          "/test/photo.jpg",
		FaceProcessStatus: model.FaceProcessStatusReady,
		FaceCount:         1,
	}
	require.NoError(t, db.Create(photo).Error)

	face := &model.Face{
		PhotoID:       photo.ID,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.9,
		QualityScore:  0.8,
		ClusterStatus: model.FaceClusterStatusPending,
	}
	require.NoError(t, db.Create(face).Error)

	// Exclude as non_face
	_, err := svc.UpdateFaceExclusion([]uint{face.ID}, true, model.ExclusionReasonNonFace)
	require.NoError(t, err)

	// face_count should be 0
	var photo1 model.Photo
	require.NoError(t, db.First(&photo1, photo.ID).Error)
	assert.Equal(t, 0, photo1.FaceCount)

	// Restore
	_, err = svc.UpdateFaceExclusion([]uint{face.ID}, false, "")
	require.NoError(t, err)

	// Verify face is back to pending
	var updatedFace model.Face
	require.NoError(t, db.First(&updatedFace, face.ID).Error)
	assert.Equal(t, model.FaceClusterStatusPending, updatedFace.ClusterStatus)
	assert.Equal(t, "", updatedFace.ExclusionReason)
	assert.Nil(t, updatedFace.ExcludedAt)

	// Verify exclusion record deleted
	var count int64
	db.Model(&model.FaceExclusion{}).Where("source_face_id = ?", face.ID).Count(&count)
	assert.Equal(t, int64(0), count)

	// Verify face_count restored (non_face restore adds back)
	var photo2 model.Photo
	require.NoError(t, db.First(&photo2, photo.ID).Error)
	assert.Equal(t, 1, photo2.FaceCount)

	// 排除/恢复后 updated_at 必须推进：前端用 face.updated_at 作为缩略图 URL 版本参数，
	// 恢复时 excluded_at 置 nil，若 updated_at 不变则版本参数回退到旧值，命中 immutable 长缓存展示旧图。
	assert.True(t, updatedFace.UpdatedAt.After(face.UpdatedAt),
		"updated_at must advance after restore for cache invalidation")
}

func TestUpdateFaceExclusion_InvalidReason(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, nil)

	_, err := svc.UpdateFaceExclusion([]uint{1}, true, "invalid_reason")
	assert.Error(t, err)
}

func TestUpdateFaceExclusion_EmptyFaceIDs(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, nil)

	_, err := svc.UpdateFaceExclusion([]uint{}, true, model.ExclusionReasonNonFace)
	assert.Error(t, err)
}

func TestUpdateFaceExclusion_FaceNotFound(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, nil)

	_, err := svc.UpdateFaceExclusion([]uint{99999}, true, model.ExclusionReasonNonFace)
	assert.Error(t, err)
}
