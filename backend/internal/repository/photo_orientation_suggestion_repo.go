package repository

import (
	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

type PhotoOrientationSuggestionRepository interface {
	Create(suggestion *model.PhotoOrientationSuggestion) error
	GetByPhotoID(photoID uint) (*model.PhotoOrientationSuggestion, error)
	GetByID(id uint) (*model.PhotoOrientationSuggestion, error)
	UpdateStatus(id uint, status string) error
	DeleteByPhotoID(photoID uint) error
	ListPending(page, pageSize int) ([]*model.PhotoOrientationSuggestion, int64, error)
	ListPendingByRotation(rotation int, page, pageSize int) ([]*model.PhotoOrientationSuggestion, int64, error)
	GetGroups() ([]model.OrientationSuggestionGroup, error)
	GetStats() (*model.OrientationSuggestionStats, error)
	BatchCreate(suggestions []*model.PhotoOrientationSuggestion) error
	BatchUpdateStatus(ids []uint, status string) error
}

type photoOrientationSuggestionRepository struct {
	db *gorm.DB
}

func NewPhotoOrientationSuggestionRepository(db *gorm.DB) PhotoOrientationSuggestionRepository {
	return &photoOrientationSuggestionRepository{db: db}
}

func (r *photoOrientationSuggestionRepository) Create(suggestion *model.PhotoOrientationSuggestion) error {
	return r.db.Create(suggestion).Error
}

func (r *photoOrientationSuggestionRepository) GetByPhotoID(photoID uint) (*model.PhotoOrientationSuggestion, error) {
	var suggestion model.PhotoOrientationSuggestion
	if err := r.db.Where("photo_id = ?", photoID).First(&suggestion).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &suggestion, nil
}

func (r *photoOrientationSuggestionRepository) GetByID(id uint) (*model.PhotoOrientationSuggestion, error) {
	var suggestion model.PhotoOrientationSuggestion
	if err := r.db.Where("id = ?", id).First(&suggestion).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &suggestion, nil
}

func (r *photoOrientationSuggestionRepository) UpdateStatus(id uint, status string) error {
	return r.db.Model(&model.PhotoOrientationSuggestion{}).
		Where("id = ?", id).
		Update("status", status).Error
}

func (r *photoOrientationSuggestionRepository) DeleteByPhotoID(photoID uint) error {
	return r.db.Where("photo_id = ?", photoID).Delete(&model.PhotoOrientationSuggestion{}).Error
}

func (r *photoOrientationSuggestionRepository) ListPending(page, pageSize int) ([]*model.PhotoOrientationSuggestion, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	var total int64
	if err := r.db.Model(&model.PhotoOrientationSuggestion{}).
		Where("status = ?", model.OrientationSuggestionStatusPending).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var suggestions []*model.PhotoOrientationSuggestion
	offset := (page - 1) * pageSize
	if err := r.db.Where("status = ?", model.OrientationSuggestionStatusPending).
		Order("confidence DESC, id ASC").
		Offset(offset).
		Limit(pageSize).
		Find(&suggestions).Error; err != nil {
		return nil, 0, err
	}

	return suggestions, total, nil
}

func (r *photoOrientationSuggestionRepository) ListPendingByRotation(rotation int, page, pageSize int) ([]*model.PhotoOrientationSuggestion, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	var total int64
	if err := r.db.Model(&model.PhotoOrientationSuggestion{}).
		Where("status = ? AND suggested_rotation = ?", model.OrientationSuggestionStatusPending, rotation).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var suggestions []*model.PhotoOrientationSuggestion
	offset := (page - 1) * pageSize
	if err := r.db.Where("status = ? AND suggested_rotation = ?", model.OrientationSuggestionStatusPending, rotation).
		Order("confidence DESC, id ASC").
		Offset(offset).
		Limit(pageSize).
		Find(&suggestions).Error; err != nil {
		return nil, 0, err
	}

	return suggestions, total, nil
}

func (r *photoOrientationSuggestionRepository) GetGroups() ([]model.OrientationSuggestionGroup, error) {
	var groups []model.OrientationSuggestionGroup

	type groupResult struct {
		SuggestedRotation  int
		Count              int
		AvgConfidence      float64
		LowConfidenceCount int
	}

	var results []groupResult
	if err := r.db.Model(&model.PhotoOrientationSuggestion{}).
		Select(`
			suggested_rotation,
			COUNT(*) as count,
			AVG(confidence) as avg_confidence,
			SUM(CASE WHEN low_confidence = 1 THEN 1 ELSE 0 END) as low_confidence_count
		`).
		Where("status = ?", model.OrientationSuggestionStatusPending).
		Group("suggested_rotation").
		Order("count DESC").
		Scan(&results).Error; err != nil {
		return nil, err
	}

	for _, r := range results {
		groups = append(groups, model.OrientationSuggestionGroup{
			SuggestedRotation:  r.SuggestedRotation,
			Count:              r.Count,
			AvgConfidence:      r.AvgConfidence,
			LowConfidenceCount: r.LowConfidenceCount,
		})
	}

	return groups, nil
}

func (r *photoOrientationSuggestionRepository) GetStats() (*model.OrientationSuggestionStats, error) {
	stats := &model.OrientationSuggestionStats{}

	rows, err := r.db.Model(&model.PhotoOrientationSuggestion{}).
		Select("status, COUNT(*) as count, SUM(CASE WHEN low_confidence = 1 THEN 1 ELSE 0 END) as low_confidence_count").
		Group("status").
		Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int64
		var lowConfidenceCount int64
		if err := rows.Scan(&status, &count, &lowConfidenceCount); err != nil {
			return nil, err
		}
		stats.Total += count
		switch status {
		case model.OrientationSuggestionStatusPending:
			stats.Pending = count
		case model.OrientationSuggestionStatusApplied:
			stats.Applied = count
		case model.OrientationSuggestionStatusDismissed:
			stats.Dismissed = count
		}
		stats.LowConfidence += lowConfidenceCount
	}

	return stats, nil
}

func (r *photoOrientationSuggestionRepository) BatchCreate(suggestions []*model.PhotoOrientationSuggestion) error {
	if len(suggestions) == 0 {
		return nil
	}
	return r.db.Create(&suggestions).Error
}

func (r *photoOrientationSuggestionRepository) BatchUpdateStatus(ids []uint, status string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.Model(&model.PhotoOrientationSuggestion{}).
		Where("id IN ?", ids).
		Update("status", status).Error
}
