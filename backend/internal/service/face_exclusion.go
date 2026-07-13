package service

import (
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// exclusionIoUThreshold is the minimum IoU for matching a new detection bbox
// to an existing face_exclusion record during re-detection.
const exclusionIoUThreshold = 0.3

// bboxIoU computes Intersection-over-Union between two axis-aligned bounding boxes.
func bboxIoU(ax, ay, aw, ah, bx, by, bw, bh float64) float64 {
	if aw <= 0 || ah <= 0 || bw <= 0 || bh <= 0 {
		return 0
	}
	interX1 := max(ax, bx)
	interY1 := max(ay, by)
	interX2 := min(ax+aw, bx+bw)
	interY2 := min(ay+ah, by+bh)
	if interX2 <= interX1 || interY2 <= interY1 {
		return 0
	}
	interArea := (interX2 - interX1) * (interY2 - interY1)
	unionArea := aw*ah + bw*bh - interArea
	if unionArea <= 0 {
		return 0
	}
	return interArea / unionArea
}

// matchExclusionRecords matches new detection bboxes against existing exclusion records
// for a photo. Returns a map of detection index → exclusion record.
// Each exclusion record can match at most one detection (highest IoU wins).
func matchExclusionRecords(
	detections []bboxCandidate,
	records []*model.FaceExclusion,
) map[int]*model.FaceExclusion {
	if len(detections) == 0 || len(records) == 0 {
		return nil
	}
	matched := make(map[int]*model.FaceExclusion)
	usedRecords := make(map[uint]struct{})

	// For each detection, find the best matching unused record.
	for i, det := range detections {
		bestIoU := exclusionIoUThreshold
		var bestRecord *model.FaceExclusion
		for _, rec := range records {
			if _, used := usedRecords[rec.ID]; used {
				continue
			}
			iou := bboxIoU(det.x, det.y, det.w, det.h, rec.BBoxX, rec.BBoxY, rec.BBoxWidth, rec.BBoxHeight)
			if iou > bestIoU {
				bestIoU = iou
				bestRecord = rec
			}
		}
		if bestRecord != nil {
			matched[i] = bestRecord
			usedRecords[bestRecord.ID] = struct{}{}
		}
	}
	return matched
}

type bboxCandidate struct {
	x, y, w, h float64
}

// UpdateFaceExclusion marks faces as excluded or restores them.
// When excluded=true, reason must be a valid exclusion reason ("non_face" or "low_quality").
// The operation is atomic within a single write transaction.
func (s *peopleService) UpdateFaceExclusion(faceIDs []uint, excluded bool, reason string) (*model.FaceExclusionResult, error) {
	if len(faceIDs) == 0 {
		return nil, fmt.Errorf("face_ids must not be empty")
	}
	if excluded && !model.IsValidExclusionReason(reason) {
		return nil, fmt.Errorf("invalid exclusion reason: %s", reason)
	}

	// Load all faces
	faces, err := s.faceRepo.ListByIDs(faceIDs)
	if err != nil {
		return nil, fmt.Errorf("load faces: %w", err)
	}
	if len(faces) != len(faceIDs) {
		found := make(map[uint]struct{}, len(faces))
		for _, f := range faces {
			found[f.ID] = struct{}{}
		}
		var missing []uint
		for _, id := range faceIDs {
			if _, ok := found[id]; !ok {
				missing = append(missing, id)
			}
		}
		return nil, fmt.Errorf("faces not found: %v", missing)
	}

	// Track affected photo IDs and person IDs
	affectedPhotoIDs := make(map[uint]struct{})
	affectedPersonIDs := make(map[uint]struct{})
	for _, face := range faces {
		affectedPhotoIDs[face.PhotoID] = struct{}{}
		if face.PersonID != nil && *face.PersonID != 0 {
			affectedPersonIDs[*face.PersonID] = struct{}{}
		}
	}

	now := time.Now()

	// Perform the update in a transaction
	if err := s.executeWrite(func() error {
		return s.db.Transaction(func(tx *gorm.DB) error {
			for _, face := range faces {
				if excluded {
					// Idempotent: already excluded with same reason
					if face.ClusterStatus == model.FaceClusterStatusExcluded && face.ExclusionReason == reason {
						continue
					}

					// Create or update face_exclusion record
					var existing model.FaceExclusion
					if err := tx.Where("photo_id = ? AND source_face_id = ?", face.PhotoID, face.ID).
						First(&existing).Error; err != nil {
						if err == gorm.ErrRecordNotFound {
							// Create new exclusion record
							existing = model.FaceExclusion{
								PhotoID:      face.PhotoID,
								SourceFaceID: face.ID,
								Reason:       reason,
								BBoxX:        face.BBoxX,
								BBoxY:        face.BBoxY,
								BBoxWidth:    face.BBoxWidth,
								BBoxHeight:   face.BBoxHeight,
							}
							if err := tx.Create(&existing).Error; err != nil {
								return fmt.Errorf("create exclusion record for face %d: %w", face.ID, err)
							}
						} else {
							return fmt.Errorf("query exclusion record for face %d: %w", face.ID, err)
						}
					} else {
						// Update existing record
						existing.Reason = reason
						existing.BBoxX = face.BBoxX
						existing.BBoxY = face.BBoxY
						existing.BBoxWidth = face.BBoxWidth
						existing.BBoxHeight = face.BBoxHeight
						if err := tx.Save(&existing).Error; err != nil {
							return fmt.Errorf("update exclusion record for face %d: %w", face.ID, err)
						}
					}

					// Update face: clear person, set excluded
					if err := tx.Model(&model.Face{}).Where("id = ?", face.ID).Updates(map[string]interface{}{
						"person_id":          nil,
						"cluster_status":     model.FaceClusterStatusExcluded,
						"cluster_score":      0,
						"manual_locked":      false,
						"manual_lock_reason": "",
						"manual_locked_at":   nil,
						"exclusion_reason":   reason,
						"excluded_at":        &now,
					}).Error; err != nil {
						return fmt.Errorf("update face %d exclusion: %w", face.ID, err)
					}
				} else {
					// Restore: idempotent if already non-excluded
					if face.ClusterStatus != model.FaceClusterStatusExcluded {
						continue
					}

					// Delete face_exclusion record
					if err := tx.Where("photo_id = ? AND source_face_id = ?", face.PhotoID, face.ID).
						Delete(&model.FaceExclusion{}).Error; err != nil {
						return fmt.Errorf("delete exclusion record for face %d: %w", face.ID, err)
					}

					// Reset face to pending
					if err := tx.Model(&model.Face{}).Where("id = ?", face.ID).Updates(map[string]interface{}{
						"person_id":         nil,
						"cluster_status":    model.FaceClusterStatusPending,
						"cluster_score":     0,
						"manual_locked":     false,
						"manual_lock_reason": "",
						"manual_locked_at":  nil,
						"exclusion_reason":  "",
						"excluded_at":       nil,
						"retry_count":       0,
						"clustered_at":      nil,
					}).Error; err != nil {
						return fmt.Errorf("restore face %d: %w", face.ID, err)
					}
				}
			}
			return nil
		})
	}); err != nil {
		return nil, err
	}

	// Post-commit side effects

	// Sync affected persons (may clean up empty persons, re-select avatars)
	for personID := range affectedPersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			logger.Warnf("syncPersonState after face exclusion for person %d: %v", personID, err)
		}
	}

	// Recompute face_count and top_person_category on affected photos
	photoIDList := make([]uint, 0, len(affectedPhotoIDs))
	for pid := range affectedPhotoIDs {
		photoIDList = append(photoIDList, pid)
	}
	if err := s.executeWrite(func() error {
		return s.photoRepo.RecomputeTopPersonCategory(photoIDList)
	}); err != nil {
		logger.Warnf("recompute top person category after face exclusion: %v", err)
	}

	// Invalidate identity profiles for affected persons
	personIDList := make([]uint, 0, len(affectedPersonIDs))
	for pid := range affectedPersonIDs {
		personIDList = append(personIDList, pid)
	}
	if len(personIDList) > 0 {
		s.invalidateIdentityProfiles(IdentityProfileInvalidation{
			DirtyPersonIDs: personIDList,
			Reason:          "face_exclusion_update",
		})
	}

	// Mark merge suggestions dirty
	s.markMergeSuggestionsDirty("face_exclusion_update")

	// Mark protoCache dirty for affected persons
	if len(personIDList) > 0 {
		s.markProtoCacheDirty(personIDList, nil, "face_exclusion_update")
	}

	// Build response with updated photo summaries
	photos := make([]model.PhotoPersonResponse, 0, len(photoIDList))
	for _, photoID := range photoIDList {
		photo, err := s.photoRepo.GetByID(photoID)
		if err != nil {
			logger.Warnf("get photo %d for exclusion response: %v", photoID, err)
			continue
		}
		if photo == nil {
			continue
		}

		photoFaces, err := s.faceRepo.ListByPhotoID(photoID)
		if err != nil {
			logger.Warnf("list faces for photo %d: %v", photoID, err)
			continue
		}

		var excludedFaces []model.FaceResponse
		for _, face := range photoFaces {
			if face.ClusterStatus == model.FaceClusterStatusExcluded {
				excludedFaces = append(excludedFaces, model.FaceResponse{
					ID:              face.ID,
					PhotoID:         face.PhotoID,
					BBoxX:           face.BBoxX,
					BBoxY:           face.BBoxY,
					BBoxWidth:       face.BBoxWidth,
					BBoxHeight:      face.BBoxHeight,
					Confidence:      face.Confidence,
					QualityScore:    face.QualityScore,
					ThumbnailPath:   face.ThumbnailPath,
					ClusterStatus:   face.ClusterStatus,
					ExclusionReason: face.ExclusionReason,
					ExcludedAt:      face.ExcludedAt,
				})
			}
		}

		photos = append(photos, model.PhotoPersonResponse{
			PhotoID:           photo.ID,
			FaceProcessStatus: photo.FaceProcessStatus,
			FaceCount:         photo.FaceCount,
			TopPersonCategory: photo.TopPersonCategory,
			ExcludedFaces:     excludedFaces,
		})
	}

	return &model.FaceExclusionResult{
		Updated: len(faces),
		Photos:  photos,
	}, nil
}
