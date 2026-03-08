package repository

import (
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

type GeocodeJobStats struct {
	Total      int64 `json:"total"`
	Pending    int64 `json:"pending"`
	Queued     int64 `json:"queued"`
	Processing int64 `json:"processing"`
	Completed  int64 `json:"completed"`
	Failed     int64 `json:"failed"`
	Cancelled  int64 `json:"cancelled"`
}

type GeocodeJobRepository interface {
	Create(job *model.GeocodeJob) error
	UpdateFields(id uint, fields map[string]interface{}) error
	GetActiveByPhotoID(photoID uint) (*model.GeocodeJob, error)
	ClaimNextJob() (*model.GeocodeJob, error)
	CancelPendingJobs() (int64, error)
	GetStats() (*GeocodeJobStats, error)
}

type geocodeJobRepository struct {
	db *gorm.DB
}

func NewGeocodeJobRepository(db *gorm.DB) GeocodeJobRepository {
	return &geocodeJobRepository{db: db}
}

func (r *geocodeJobRepository) Create(job *model.GeocodeJob) error {
	return r.db.Create(job).Error
}

func (r *geocodeJobRepository) UpdateFields(id uint, fields map[string]interface{}) error {
	return r.db.Model(&model.GeocodeJob{}).Where("id = ?", id).Updates(fields).Error
}

func (r *geocodeJobRepository) GetActiveByPhotoID(photoID uint) (*model.GeocodeJob, error) {
	var job model.GeocodeJob
	err := r.db.Where("photo_id = ? AND status IN ?", photoID, []string{"pending", "queued", "processing"}).
		Order("priority DESC").Order("queued_at ASC").First(&job).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *geocodeJobRepository) ClaimNextJob() (*model.GeocodeJob, error) {
	tx := r.db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	defer func() {
		if rv := recover(); rv != nil {
			tx.Rollback()
			panic(rv)
		}
	}()
	var job model.GeocodeJob
	err := tx.Where("status IN ?", []string{"pending", "queued"}).
		Order("priority DESC").Order("COALESCE(last_requested_at, queued_at) DESC").Order("queued_at ASC").
		First(&job).Error
	if err != nil {
		tx.Rollback()
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	now := time.Now()
	updates := map[string]interface{}{"status": "processing", "started_at": &now, "attempt_count": job.AttemptCount + 1}
	if err := tx.Model(&model.GeocodeJob{}).Where("id = ?", job.ID).Updates(updates).Error; err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	job.Status = "processing"
	job.StartedAt = &now
	job.AttemptCount++
	return &job, nil
}

func (r *geocodeJobRepository) CancelPendingJobs() (int64, error) {
	now := time.Now()
	result := r.db.Model(&model.GeocodeJob{}).Where("status IN ?", []string{"pending", "queued"}).
		Updates(map[string]interface{}{"status": "cancelled", "completed_at": &now})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func (r *geocodeJobRepository) GetStats() (*GeocodeJobStats, error) {
	stats := &GeocodeJobStats{}
	if err := r.db.Model(&model.GeocodeJob{}).Count(&stats.Total).Error; err != nil {
		return nil, fmt.Errorf("count geocode jobs: %w", err)
	}
	rows, err := r.db.Model(&model.GeocodeJob{}).Select("status, COUNT(*) as count").Group("status").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		switch status {
		case "pending":
			stats.Pending = count
		case "queued":
			stats.Queued = count
		case "processing":
			stats.Processing = count
		case "completed":
			stats.Completed = count
		case "failed":
			stats.Failed = count
		case "cancelled":
			stats.Cancelled = count
		}
	}
	return stats, nil
}
