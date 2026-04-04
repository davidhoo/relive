package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

type FaceRepository interface {
	Create(face *model.Face) error
	Update(face *model.Face) error
	UpdateFields(id uint, fields map[string]interface{}) error
	UpdateClusterFields(ids []uint, fields map[string]interface{}) error
	GetByID(id uint) (*model.Face, error)
	DeleteByPhotoID(photoID uint) error
	ListByPhotoID(photoID uint) ([]*model.Face, error)
	ListByPersonID(personID uint) ([]*model.Face, error)
	ListByIDs(ids []uint) ([]*model.Face, error)
	ListAssigned() ([]*model.Face, error)
	ListAssignedPersonIDs() ([]uint, error)
	ListPending(limit int) ([]*model.Face, error)
	ListTopByPersonIDs(personIDs []uint, perPerson int) ([]*model.Face, error)
	ReassignFaces(faceIDs []uint, personID uint, reason string) error
	ListLowConfidence(threshold float64, maxGeneration int) ([]*model.Face, error)
	ResetForRecluster(ids []uint) error
}

type faceRepository struct {
	db *gorm.DB
}

func NewFaceRepository(db *gorm.DB) FaceRepository {
	return &faceRepository{db: db}
}

func (r *faceRepository) Create(face *model.Face) error {
	return r.db.Create(face).Error
}

func (r *faceRepository) Update(face *model.Face) error {
	return r.db.Save(face).Error
}

func (r *faceRepository) UpdateFields(id uint, fields map[string]interface{}) error {
	return r.db.Model(&model.Face{}).Where("id = ?", id).Updates(fields).Error
}

func (r *faceRepository) UpdateClusterFields(ids []uint, fields map[string]interface{}) error {
	if len(ids) == 0 || len(fields) == 0 {
		return nil
	}
	return r.db.Model(&model.Face{}).Where("id IN ?", ids).Updates(fields).Error
}

func (r *faceRepository) GetByID(id uint) (*model.Face, error) {
	var face model.Face
	if err := r.db.Where("id = ?", id).First(&face).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &face, nil
}

func (r *faceRepository) DeleteByPhotoID(photoID uint) error {
	return r.db.Where("photo_id = ?", photoID).Delete(&model.Face{}).Error
}

func (r *faceRepository) ListByPhotoID(photoID uint) ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Where("photo_id = ?", photoID).Order("id ASC").Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListByPersonID(personID uint) ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Where("person_id = ?", personID).Order("id ASC").Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListByIDs(ids []uint) ([]*model.Face, error) {
	var faces []*model.Face
	if len(ids) == 0 {
		return faces, nil
	}
	err := r.db.Where("id IN ?", ids).Order("id ASC").Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListAssigned() ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Where("person_id IS NOT NULL").Order("id ASC").Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListAssignedPersonIDs() ([]uint, error) {
	var ids []uint
	err := r.db.Model(&model.Face{}).
		Where("person_id IS NOT NULL").
		Distinct("person_id").
		Pluck("person_id", &ids).Error
	return ids, err
}

func (r *faceRepository) ListPending(limit int) ([]*model.Face, error) {
	var faces []*model.Face
	query := r.db.Where("cluster_status = ?", model.FaceClusterStatusPending).Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListTopByPersonIDs(personIDs []uint, perPerson int) ([]*model.Face, error) {
	var faces []*model.Face
	if len(personIDs) == 0 {
		return faces, nil
	}

	err := r.db.
		Where("person_id IN ?", personIDs).
		Order("person_id ASC").
		Order("manual_locked DESC").
		Order("quality_score DESC").
		Order("confidence DESC").
		Order("id ASC").
		Find(&faces).Error
	if err != nil {
		return nil, err
	}

	if perPerson <= 0 {
		return faces, nil
	}

	topFaces := make([]*model.Face, 0, len(faces))
	counts := make(map[uint]int, len(personIDs))
	for _, face := range faces {
		if face == nil || face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		personID := *face.PersonID
		if counts[personID] >= perPerson {
			continue
		}
		topFaces = append(topFaces, face)
		counts[personID]++
	}
	return topFaces, nil
}

func (r *faceRepository) ReassignFaces(faceIDs []uint, personID uint, reason string) error {
	if len(faceIDs) == 0 {
		return nil
	}
	now := time.Now()
	return r.db.Model(&model.Face{}).Where("id IN ?", faceIDs).Updates(map[string]interface{}{
		"person_id":          personID,
		"cluster_status":     model.FaceClusterStatusManual,
		"cluster_score":      1.0,
		"manual_locked":      true,
		"manual_lock_reason": reason,
		"manual_locked_at":   &now,
		"clustered_at":       &now,
	}).Error
}

func (r *faceRepository) ListLowConfidence(threshold float64, maxGeneration int) ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Where("manual_locked = ? AND cluster_status = ? AND cluster_score < ? AND recluster_generation < ?",
		false, model.FaceClusterStatusAssigned, threshold, maxGeneration).
		Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ResetForRecluster(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.Model(&model.Face{}).
		Where("id IN ? AND manual_locked = ?", ids, false).
		Updates(map[string]interface{}{
			"person_id":            nil,
			"cluster_status":       model.FaceClusterStatusPending,
			"recluster_generation": gorm.Expr("recluster_generation + 1"),
		}).Error
}
