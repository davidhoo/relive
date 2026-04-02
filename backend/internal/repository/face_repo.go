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
	GetByID(id uint) (*model.Face, error)
	DeleteByPhotoID(photoID uint) error
	ListByPhotoID(photoID uint) ([]*model.Face, error)
	ListByPersonID(personID uint) ([]*model.Face, error)
	ListByIDs(ids []uint) ([]*model.Face, error)
	ListAssigned() ([]*model.Face, error)
	ReassignFaces(faceIDs []uint, personID uint, reason string) error
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

func (r *faceRepository) ReassignFaces(faceIDs []uint, personID uint, reason string) error {
	if len(faceIDs) == 0 {
		return nil
	}
	now := time.Now()
	return r.db.Model(&model.Face{}).Where("id IN ?", faceIDs).Updates(map[string]interface{}{
		"person_id":          personID,
		"manual_locked":      true,
		"manual_lock_reason": reason,
		"manual_locked_at":   &now,
	}).Error
}
