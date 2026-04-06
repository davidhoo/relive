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

	// Remote worker lease methods
	ClaimNextRemote(workerID string, limit int, lockUntil time.Time) ([]*model.PeopleJob, error)
	HeartbeatRemote(id uint, workerID string, progress int, statusMsg string, lockUntil time.Time) error
	ReleaseRemote(id uint, workerID string, reason string, retryLater bool) error
	CompleteRemote(id uint, workerID string) error
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

	// Step 1: Cancel pending/queued jobs and local processing jobs (worker_id = "")
	result := r.db.Model(&model.PeopleJob{}).
		Where("(status IN ?) OR (status = ? AND (worker_id = ? OR worker_id IS NULL))",
			[]string{model.PeopleJobStatusPending, model.PeopleJobStatusQueued},
			model.PeopleJobStatusProcessing,
			"",
		).
		Updates(map[string]interface{}{
			"status":       model.PeopleJobStatusCancelled,
			"last_error":   message,
			"completed_at": &now,
		})
	if result.Error != nil {
		return fmt.Errorf("interrupt non-terminal people jobs: %w", result.Error)
	}

	// Step 2: Reset expired remote processing jobs to queued
	// (unexpired remote jobs are left untouched - worker will continue)
	result = r.db.Model(&model.PeopleJob{}).
		Where("status = ? AND worker_id != ? AND lock_expires_at < ?",
			model.PeopleJobStatusProcessing,
			"",
			now,
		).
		Updates(map[string]interface{}{
			"status":            model.PeopleJobStatusQueued,
			"worker_id":         "",
			"lock_expires_at":   nil,
			"last_heartbeat_at": nil,
			"status_message":    "lock expired, reset to queued",
		})
	if result.Error != nil {
		return fmt.Errorf("reset expired remote people jobs: %w", result.Error)
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

// ClaimNextRemote claims up to limit jobs for a remote worker.
// Only claims pending/queued jobs, or expired remote processing jobs (not local processing jobs).
func (r *peopleJobRepository) ClaimNextRemote(workerID string, limit int, lockUntil time.Time) ([]*model.PeopleJob, error) {
	now := time.Now()

	// Find claimable jobs:
	// 1. pending/queued status
	// 2. OR processing status with non-empty worker_id AND expired lock
	// Note: processing jobs with empty worker_id are local backend tasks, never claimable
	var jobs []*model.PeopleJob
	err := r.db.Where(
		"(status IN ?) OR (status = ? AND worker_id != ? AND lock_expires_at < ?)",
		[]string{model.PeopleJobStatusPending, model.PeopleJobStatusQueued},
		model.PeopleJobStatusProcessing,
		"",
		now,
	).Order("priority DESC").Order("COALESCE(last_requested_at, queued_at) DESC").Order("queued_at ASC").
		Limit(limit).Find(&jobs).Error
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, nil
	}

	// Atomically claim each job
	claimed := make([]*model.PeopleJob, 0, len(jobs))
	for _, job := range jobs {
		result := r.db.Model(&model.PeopleJob{}).
			Where("id = ? AND ((status IN ?) OR (status = ? AND worker_id != ? AND lock_expires_at < ?))",
				job.ID,
				[]string{model.PeopleJobStatusPending, model.PeopleJobStatusQueued},
				model.PeopleJobStatusProcessing,
				"",
				now,
			).
			Updates(map[string]interface{}{
				"status":             model.PeopleJobStatusProcessing,
				"worker_id":          workerID,
				"lock_expires_at":    lockUntil,
				"last_heartbeat_at":  now,
				"started_at":         &now,
				"attempt_count":      gorm.Expr("attempt_count + 1"),
				"status_message":     "claimed by " + workerID,
			})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected > 0 {
			job.Status = model.PeopleJobStatusProcessing
			job.WorkerID = workerID
			job.LockExpiresAt = &lockUntil
			job.LastHeartbeatAt = &now
			job.StartedAt = &now
			job.AttemptCount++
			claimed = append(claimed, job)
		}
	}

	return claimed, nil
}

// HeartbeatRemote extends the lock for a remote job.
func (r *peopleJobRepository) HeartbeatRemote(id uint, workerID string, progress int, statusMsg string, lockUntil time.Time) error {
	result := r.db.Model(&model.PeopleJob{}).
		Where("id = ? AND worker_id = ? AND status = ?", id, workerID, model.PeopleJobStatusProcessing).
		Updates(map[string]interface{}{
			"last_heartbeat_at": lockUntil,
			"lock_expires_at":   lockUntil,
			"progress":          progress,
			"status_message":    statusMsg,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("job %d not found or not owned by worker %s", id, workerID)
	}
	return nil
}

// ReleaseRemote releases a job back to the queue.
func (r *peopleJobRepository) ReleaseRemote(id uint, workerID string, reason string, retryLater bool) error {
	now := time.Now()

	updates := map[string]interface{}{
		"worker_id":         "",
		"lock_expires_at":   nil,
		"last_heartbeat_at": nil,
		"status_message":    reason,
	}

	if retryLater {
		// Reset to queued for retry
		updates["status"] = model.PeopleJobStatusQueued
	} else {
		// Mark as failed
		updates["status"] = model.PeopleJobStatusFailed
		updates["last_error"] = reason
		updates["completed_at"] = &now
	}

	result := r.db.Model(&model.PeopleJob{}).
		Where("id = ? AND worker_id = ?", id, workerID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("job %d not found or not owned by worker %s", id, workerID)
	}
	return nil
}

// CompleteRemote marks a remote job as completed.
func (r *peopleJobRepository) CompleteRemote(id uint, workerID string) error {
	now := time.Now()
	result := r.db.Model(&model.PeopleJob{}).
		Where("id = ? AND worker_id = ? AND status = ?", id, workerID, model.PeopleJobStatusProcessing).
		Updates(map[string]interface{}{
			"status":            model.PeopleJobStatusCompleted,
			"worker_id":         "",
			"lock_expires_at":   nil,
			"last_heartbeat_at": nil,
			"completed_at":      &now,
			"status_message":    "completed",
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("job %d not found or not owned by worker %s", id, workerID)
	}
	return nil
}
