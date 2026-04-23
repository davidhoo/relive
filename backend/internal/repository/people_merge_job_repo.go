package repository

import (
	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// PeopleMergeJobRepository 人物合并任务仓库接口
type PeopleMergeJobRepository interface {
	Create(job *model.PeopleMergeJob) error
	GetByID(id uint) (*model.PeopleMergeJob, error)
	GetPending(limit int) ([]*model.PeopleMergeJob, error)
	UpdateStatus(id uint, status string, errorMsg string) error
	Complete(id uint, result string) error
	Fail(id uint, errorMsg string) error
}

// peopleMergeJobRepository 实现
type peopleMergeJobRepository struct {
	db *gorm.DB
}

// NewPeopleMergeJobRepository 创建仓库实例
func NewPeopleMergeJobRepository(db *gorm.DB) PeopleMergeJobRepository {
	return &peopleMergeJobRepository{db: db}
}

func (r *peopleMergeJobRepository) Create(job *model.PeopleMergeJob) error {
	return r.db.Create(job).Error
}

func (r *peopleMergeJobRepository) GetByID(id uint) (*model.PeopleMergeJob, error) {
	var job model.PeopleMergeJob
	err := r.db.First(&job, id).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *peopleMergeJobRepository) GetPending(limit int) ([]*model.PeopleMergeJob, error) {
	var jobs []*model.PeopleMergeJob
	err := r.db.Where("status = ?", model.PeopleMergeJobStatusPending).
		Order("created_at ASC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

func (r *peopleMergeJobRepository) UpdateStatus(id uint, status string, errorMsg string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if errorMsg != "" {
		updates["error_message"] = errorMsg
	}
	return r.db.Model(&model.PeopleMergeJob{}).Where("id = ?", id).Updates(updates).Error
}

func (r *peopleMergeJobRepository) Complete(id uint, result string) error {
	now := r.db.NowFunc()
	return r.db.Model(&model.PeopleMergeJob{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":        model.PeopleMergeJobStatusCompleted,
		"result":        result,
		"completed_at":  &now,
	}).Error
}

func (r *peopleMergeJobRepository) Fail(id uint, errorMsg string) error {
	now := r.db.NowFunc()
	return r.db.Model(&model.PeopleMergeJob{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":        model.PeopleMergeJobStatusFailed,
		"error_message": errorMsg,
		"completed_at":  &now,
	}).Error
}
