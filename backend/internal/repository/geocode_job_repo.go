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
	DeleteTerminalBefore(cutoff time.Time) (int64, error)
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
	// 使用原子更新避免事务锁竞争
	var job model.GeocodeJob
	result := r.db.Where("status IN ?", []string{"pending", "queued"}).
		Order("priority DESC").Order("COALESCE(last_requested_at, queued_at) DESC").Order("queued_at ASC").
		Limit(1).Find(&job)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	// 使用乐观锁方式原子更新状态
	now := time.Now()
	updates := map[string]interface{}{
		"status":        "processing",
		"started_at":    &now,
		"attempt_count": gorm.Expr("attempt_count + 1"),
	}
	result = r.db.Model(&model.GeocodeJob{}).
		Where("id = ? AND status IN ?", job.ID, []string{"pending", "queued"}).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		// 其他进程已经认领了这个任务
		return nil, nil
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

func (r *geocodeJobRepository) DeleteTerminalBefore(cutoff time.Time) (int64, error) {
	result := r.db.Where("status IN ? AND updated_at < ?", []string{"completed", "failed", "cancelled"}, cutoff).
		Delete(&model.GeocodeJob{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
