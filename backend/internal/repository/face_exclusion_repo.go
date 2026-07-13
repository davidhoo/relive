package repository

import (
	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// FaceExclusionRepository manages persistent face exclusion records.
type FaceExclusionRepository interface {
	Create(record *model.FaceExclusion) error
	Update(record *model.FaceExclusion) error
	DeleteByID(id uint) error
	DeleteByPhotoIDAndBBox(photoID uint, bboxX, bboxY, bboxWidth, bboxHeight float64) error
	ListByPhotoID(photoID uint) ([]*model.FaceExclusion, error)
	GetByPhotoIDAndBBox(photoID uint, bboxX, bboxY, bboxWidth, bboxHeight float64) (*model.FaceExclusion, error)
}

type faceExclusionRepository struct {
	db *gorm.DB
}

func NewFaceExclusionRepository(db *gorm.DB) FaceExclusionRepository {
	return &faceExclusionRepository{db: db}
}

func (r *faceExclusionRepository) Create(record *model.FaceExclusion) error {
	return r.db.Create(record).Error
}

func (r *faceExclusionRepository) Update(record *model.FaceExclusion) error {
	return r.db.Save(record).Error
}

func (r *faceExclusionRepository) DeleteByID(id uint) error {
	return r.db.Where("id = ?", id).Delete(&model.FaceExclusion{}).Error
}

func (r *faceExclusionRepository) DeleteByPhotoIDAndBBox(photoID uint, bboxX, bboxY, bboxWidth, bboxHeight float64) error {
	return r.db.Where(
		"photo_id = ? AND ABS(bbox_x - ?) < 0.001 AND ABS(bbox_y - ?) < 0.001 AND ABS(bbox_width - ?) < 0.001 AND ABS(bbox_height - ?) < 0.001",
		photoID, bboxX, bboxY, bboxWidth, bboxHeight,
	).Delete(&model.FaceExclusion{}).Error
}

func (r *faceExclusionRepository) ListByPhotoID(photoID uint) ([]*model.FaceExclusion, error) {
	var records []*model.FaceExclusion
	err := r.db.Where("photo_id = ?", photoID).Order("id ASC").Find(&records).Error
	return records, err
}

func (r *faceExclusionRepository) GetByPhotoIDAndBBox(photoID uint, bboxX, bboxY, bboxWidth, bboxHeight float64) (*model.FaceExclusion, error) {
	var record model.FaceExclusion
	err := r.db.Where(
		"photo_id = ? AND ABS(bbox_x - ?) < 0.001 AND ABS(bbox_y - ?) < 0.001 AND ABS(bbox_width - ?) < 0.001 AND ABS(bbox_height - ?) < 0.001",
		photoID, bboxX, bboxY, bboxWidth, bboxHeight,
	).First(&record).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &record, nil
}
