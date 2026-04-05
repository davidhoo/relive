package repository

import (
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

type PeopleJobStats struct {
	Total      int64 `json:"total"`
	Pending    int64 `json:"pending"`
	Queued     int64 `json:"queued"`
	Processing int64 `json:"processing"`
	Completed  int64 `json:"completed"`
	Failed     int64 `json:"failed"`
	Cancelled  int64 `json:"cancelled"`
}

type PeopleJobRepository interface {
	Create(job *model.PeopleJob) error
	UpdateFields(id uint, fields map[string]interface{}) error
	GetByID(id uint) (*model.PeopleJob, error)
	GetActiveByPhotoID(photoID uint) (*model.PeopleJob, error)
	ClaimNextJob() (*model.PeopleJob, error)
	CancelPendingJobs() (int64, error)
	InterruptNonTerminal(message string) error
	GetStats() (*PeopleJobStats, error)
	DeleteTerminalBefore(cutoff time.Time) (int64, error)
}

type peopleJobRepository struct {
	db *gorm.DB
}

func NewPeopleJobRepository(db *gorm.DB) PeopleJobRepository {
	return &peopleJobRepository{db: db}
}

func (r *peopleJobRepository) Create(job *model.PeopleJob) error {
	return r.db.Create(job).Error
}

func (r *peopleJobRepository) UpdateFields(id uint, fields map[string]interface{}) error {
	return r.db.Model(&model.PeopleJob{}).Where("id = ?", id).Updates(fields).Error
}

func (r *peopleJobRepository) GetByID(id uint) (*model.PeopleJob, error) {
	var job model.PeopleJob
	if err := r.db.Where("id = ?", id).First(&job).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *peopleJobRepository) GetActiveByPhotoID(photoID uint) (*model.PeopleJob, error) {
	var job model.PeopleJob
	err := r.db.Where("photo_id = ? AND status IN ?", photoID, []string{model.PeopleJobStatusPending, model.PeopleJobStatusQueued, model.PeopleJobStatusProcessing}).
		Order("priority DESC").Order("queued_at ASC").First(&job).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *peopleJobRepository) ClaimNextJob() (*model.PeopleJob, error) {
	var job model.PeopleJob
	result := r.db.Where("status IN ?", []string{model.PeopleJobStatusPending, model.PeopleJobStatusQueued}).
		Order("priority DESC").Order("COALESCE(last_requested_at, queued_at) DESC").Order("queued_at ASC").
		Limit(1).Find(&job)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}

	now := time.Now()
	result = r.db.Model(&model.PeopleJob{}).
		Where("id = ? AND status IN ?", job.ID, []string{model.PeopleJobStatusPending, model.PeopleJobStatusQueued}).
		Updates(map[string]interface{}{
			"status":        model.PeopleJobStatusProcessing,
			"started_at":    &now,
			"attempt_count": gorm.Expr("attempt_count + 1"),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}

	job.Status = model.PeopleJobStatusProcessing
	job.StartedAt = &now
	job.AttemptCount++
	return &job, nil
}

func (r *peopleJobRepository) CancelPendingJobs() (int64, error) {
	now := time.Now()
	result := r.db.Model(&model.PeopleJob{}).
		Where("status IN ?", []string{model.PeopleJobStatusPending, model.PeopleJobStatusQueued}).
		Updates(map[string]interface{}{"status": model.PeopleJobStatusCancelled, "completed_at": &now})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func (r *peopleJobRepository) InterruptNonTerminal(message string) error {
	now := time.Now()
	result := r.db.Model(&model.PeopleJob{}).
		Where("status IN ?", []string{model.PeopleJobStatusPending, model.PeopleJobStatusQueued, model.PeopleJobStatusProcessing}).
		Updates(map[string]interface{}{
			"status":       model.PeopleJobStatusCancelled,
			"last_error":   message,
			"completed_at": &now,
		})
	if result.Error != nil {
		return fmt.Errorf("interrupt non-terminal people jobs: %w", result.Error)
	}
	return nil
}

func (r *peopleJobRepository) GetStats() (*PeopleJobStats, error) {
	stats := &PeopleJobStats{}
	if err := r.db.Model(&model.PeopleJob{}).Count(&stats.Total).Error; err != nil {
		return nil, fmt.Errorf("count people jobs: %w", err)
	}

	rows, err := r.db.Model(&model.PeopleJob{}).
		Select("status, COUNT(*) as count").
		Group("status").Rows()
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
		case model.PeopleJobStatusPending:
			stats.Pending = count
		case model.PeopleJobStatusQueued:
			stats.Queued = count
		case model.PeopleJobStatusProcessing:
			stats.Processing = count
		case model.PeopleJobStatusCompleted:
			stats.Completed = count
		case model.PeopleJobStatusFailed:
			stats.Failed = count
		case model.PeopleJobStatusCancelled:
			stats.Cancelled = count
		}
	}

	return stats, nil
}

func (r *peopleJobRepository) DeleteTerminalBefore(cutoff time.Time) (int64, error) {
	result := r.db.Where("status IN ? AND updated_at < ?", []string{model.PeopleJobStatusCompleted, model.PeopleJobStatusFailed, model.PeopleJobStatusCancelled}, cutoff).
		Delete(&model.PeopleJob{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
