package repository

import (
	"fmt"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// ScanJobRepository 扫描/重建任务仓库
// 当前系统仅允许一个任务运行，但保留历史记录用于前端展示和中断恢复。
type ScanJobRepository interface {
	Create(job *model.ScanJob) error
	Update(job *model.ScanJob) error
	UpdateFields(id string, fields map[string]interface{}) error
	GetLatest() (*model.ScanJob, error)
	GetByID(id string) (*model.ScanJob, error)
	GetActive() (*model.ScanJob, error)
	InterruptNonTerminal(message string) error
}

type scanJobRepository struct {
	db *gorm.DB
}

// NewScanJobRepository 创建扫描任务仓库
func NewScanJobRepository(db *gorm.DB) ScanJobRepository {
	return &scanJobRepository{db: db}
}

func (r *scanJobRepository) Create(job *model.ScanJob) error {
	return r.db.Create(job).Error
}

func (r *scanJobRepository) Update(job *model.ScanJob) error {
	return r.db.Save(job).Error
}

func (r *scanJobRepository) UpdateFields(id string, fields map[string]interface{}) error {
	return r.db.Model(&model.ScanJob{}).Where("id = ?", id).Updates(fields).Error
}

func (r *scanJobRepository) GetLatest() (*model.ScanJob, error) {
	var job model.ScanJob
	err := r.db.Order("started_at DESC").Order("created_at DESC").First(&job).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *scanJobRepository) GetByID(id string) (*model.ScanJob, error) {
	var job model.ScanJob
	err := r.db.Where("id = ?", id).First(&job).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *scanJobRepository) GetActive() (*model.ScanJob, error) {
	var job model.ScanJob
	err := r.db.Where("status IN ?", []string{model.ScanJobStatusPending, model.ScanJobStatusRunning, model.ScanJobStatusStopping}).
		Order("started_at DESC").Order("created_at DESC").First(&job).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *scanJobRepository) InterruptNonTerminal(message string) error {
	result := r.db.Model(&model.ScanJob{}).
		Where("status IN ?", []string{model.ScanJobStatusPending, model.ScanJobStatusRunning, model.ScanJobStatusStopping}).
		Updates(map[string]interface{}{
			"status":            model.ScanJobStatusInterrupted,
			"phase":             model.ScanJobPhaseStopping,
			"error_message":     message,
			"completed_at":      gorm.Expr("CURRENT_TIMESTAMP"),
			"last_heartbeat_at": gorm.Expr("CURRENT_TIMESTAMP"),
		})
	if result.Error != nil {
		return fmt.Errorf("interrupt non-terminal scan jobs: %w", result.Error)
	}
	return nil
}
