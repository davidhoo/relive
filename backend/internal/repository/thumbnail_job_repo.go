package repository

import (
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

type ThumbnailJobStats struct {
	Total      int64 `json:"total"`
	Pending    int64 `json:"pending"`
	Queued     int64 `json:"queued"`
	Processing int64 `json:"processing"`
	Completed  int64 `json:"completed"`
	Failed     int64 `json:"failed"`
	Cancelled  int64 `json:"cancelled"`
}

type ThumbnailJobRepository interface {
	Create(job *model.ThumbnailJob) error
	Update(job *model.ThumbnailJob) error
	UpdateFields(id uint, fields map[string]interface{}) error
	GetByID(id uint) (*model.ThumbnailJob, error)
	GetActiveByPhotoID(photoID uint) (*model.ThumbnailJob, error)
	ClaimNextJob() (*model.ThumbnailJob, error)
	CancelPendingJobs() (int64, error)
	GetStats() (*ThumbnailJobStats, error)
	DeleteTerminalBefore(cutoff time.Time) (int64, error)
}

type thumbnailJobRepository struct {
	db *gorm.DB
}

func NewThumbnailJobRepository(db *gorm.DB) ThumbnailJobRepository {
	return &thumbnailJobRepository{db: db}
}

func (r *thumbnailJobRepository) Create(job *model.ThumbnailJob) error {
	return r.db.Create(job).Error
}

func (r *thumbnailJobRepository) Update(job *model.ThumbnailJob) error {
	return r.db.Save(job).Error
}

func (r *thumbnailJobRepository) UpdateFields(id uint, fields map[string]interface{}) error {
	return r.db.Model(&model.ThumbnailJob{}).Where("id = ?", id).Updates(fields).Error
}

func (r *thumbnailJobRepository) GetByID(id uint) (*model.ThumbnailJob, error) {
	var job model.ThumbnailJob
	if err := r.db.Where("id = ?", id).First(&job).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *thumbnailJobRepository) GetActiveByPhotoID(photoID uint) (*model.ThumbnailJob, error) {
	var job model.ThumbnailJob
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

func (r *thumbnailJobRepository) ClaimNextJob() (*model.ThumbnailJob, error) {
	// 使用原子更新避免事务锁竞争
	// 先找到下一个可执行的任务（不使用事务，减少锁持有时间）
	var job model.ThumbnailJob
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

	result = r.db.Model(&model.ThumbnailJob{}).
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

func (r *thumbnailJobRepository) CancelPendingJobs() (int64, error) {
	now := time.Now()
	result := r.db.Model(&model.ThumbnailJob{}).
		Where("status IN ?", []string{"pending", "queued"}).
		Updates(map[string]interface{}{"status": "cancelled", "completed_at": &now})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func (r *thumbnailJobRepository) GetStats() (*ThumbnailJobStats, error) {
	stats := &ThumbnailJobStats{}
	if err := r.db.Model(&model.ThumbnailJob{}).Count(&stats.Total).Error; err != nil {
		return nil, fmt.Errorf("count thumbnail jobs: %w", err)
	}
	rows, err := r.db.Model(&model.ThumbnailJob{}).
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

func (r *thumbnailJobRepository) DeleteTerminalBefore(cutoff time.Time) (int64, error) {
	result := r.db.Where("status IN ? AND updated_at < ?", []string{"completed", "failed", "cancelled"}, cutoff).
		Delete(&model.ThumbnailJob{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
