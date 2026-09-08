package repository

import (
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PeopleIdentityAssignmentRepository persists complete primary assignment batches
// and supports atomic revoke with version checks.
type PeopleIdentityAssignmentRepository interface {
	CreateBatch(batch *model.PeopleIdentityAssignmentBatch) error
	UpdateBatch(batch *model.PeopleIdentityAssignmentBatch) error
	GetBatch(id uint) (*model.PeopleIdentityAssignmentBatch, error)
	GetBatchByOperationID(operationID string) (*model.PeopleIdentityAssignmentBatch, error)
	ListBatches(page, pageSize int) ([]model.PeopleIdentityAssignmentBatch, int64, error)
	ListChanges(batchID uint, page, pageSize int) ([]model.PeopleIdentityAssignmentChange, int64, error)
	ListChangesByBatch(batchID uint) ([]model.PeopleIdentityAssignmentChange, error)
	CreateChanges(tx *gorm.DB, changes []model.PeopleIdentityAssignmentChange) error
	MarkInterruptedRunning() (int64, error)

	CreateRevocation(rev *model.PeopleIdentityAssignmentRevocation) error
	UpdateRevocation(rev *model.PeopleIdentityAssignmentRevocation) error
	GetLatestRevocation(batchID uint) (*model.PeopleIdentityAssignmentRevocation, error)
	CreateRevocationItems(tx *gorm.DB, items []model.PeopleIdentityAssignmentRevocationItem) error
	WithTx(fn func(tx *gorm.DB) error) error
}

type peopleIdentityAssignmentRepository struct {
	db *gorm.DB
}

// NewPeopleIdentityAssignmentRepository constructs the assignment log repository.
func NewPeopleIdentityAssignmentRepository(db *gorm.DB) PeopleIdentityAssignmentRepository {
	return &peopleIdentityAssignmentRepository{db: db}
}

func (r *peopleIdentityAssignmentRepository) CreateBatch(batch *model.PeopleIdentityAssignmentBatch) error {
	return r.db.Create(batch).Error
}

func (r *peopleIdentityAssignmentRepository) UpdateBatch(batch *model.PeopleIdentityAssignmentBatch) error {
	return r.db.Save(batch).Error
}

func (r *peopleIdentityAssignmentRepository) GetBatch(id uint) (*model.PeopleIdentityAssignmentBatch, error) {
	var batch model.PeopleIdentityAssignmentBatch
	if err := r.db.First(&batch, id).Error; err != nil {
		return nil, err
	}
	return &batch, nil
}

func (r *peopleIdentityAssignmentRepository) GetBatchByOperationID(operationID string) (*model.PeopleIdentityAssignmentBatch, error) {
	var batch model.PeopleIdentityAssignmentBatch
	if err := r.db.Where("operation_id = ?", operationID).First(&batch).Error; err != nil {
		return nil, err
	}
	return &batch, nil
}

func (r *peopleIdentityAssignmentRepository) ListBatches(page, pageSize int) ([]model.PeopleIdentityAssignmentBatch, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var total int64
	if err := r.db.Model(&model.PeopleIdentityAssignmentBatch{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var batches []model.PeopleIdentityAssignmentBatch
	err := r.db.Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&batches).Error
	return batches, total, err
}

func (r *peopleIdentityAssignmentRepository) ListChanges(batchID uint, page, pageSize int) ([]model.PeopleIdentityAssignmentChange, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 500 {
		pageSize = 500
	}
	var total int64
	q := r.db.Model(&model.PeopleIdentityAssignmentChange{}).Where("batch_id = ?", batchID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var changes []model.PeopleIdentityAssignmentChange
	err := q.Order("component_key ASC, face_id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&changes).Error
	return changes, total, err
}

func (r *peopleIdentityAssignmentRepository) ListChangesByBatch(batchID uint) ([]model.PeopleIdentityAssignmentChange, error) {
	var changes []model.PeopleIdentityAssignmentChange
	err := r.db.Where("batch_id = ?", batchID).
		Order("component_key ASC, face_id ASC").
		Find(&changes).Error
	return changes, err
}

func (r *peopleIdentityAssignmentRepository) CreateChanges(tx *gorm.DB, changes []model.PeopleIdentityAssignmentChange) error {
	if len(changes) == 0 {
		return nil
	}
	db := r.db
	if tx != nil {
		db = tx
	}
	return db.Create(&changes).Error
}

func (r *peopleIdentityAssignmentRepository) MarkInterruptedRunning() (int64, error) {
	now := time.Now()
	res := r.db.Model(&model.PeopleIdentityAssignmentBatch{}).
		Where("status = ?", model.PeopleIdentityAssignmentBatchRunning).
		Updates(map[string]interface{}{
			"status":       model.PeopleIdentityAssignmentBatchInterrupted,
			"completed_at": now,
			"updated_at":   now,
		})
	return res.RowsAffected, res.Error
}

func (r *peopleIdentityAssignmentRepository) CreateRevocation(rev *model.PeopleIdentityAssignmentRevocation) error {
	return r.db.Create(rev).Error
}

func (r *peopleIdentityAssignmentRepository) UpdateRevocation(rev *model.PeopleIdentityAssignmentRevocation) error {
	return r.db.Save(rev).Error
}

func (r *peopleIdentityAssignmentRepository) GetLatestRevocation(batchID uint) (*model.PeopleIdentityAssignmentRevocation, error) {
	var rev model.PeopleIdentityAssignmentRevocation
	err := r.db.Where("batch_id = ?", batchID).
		Order("id DESC").
		First(&rev).Error
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

func (r *peopleIdentityAssignmentRepository) CreateRevocationItems(tx *gorm.DB, items []model.PeopleIdentityAssignmentRevocationItem) error {
	if len(items) == 0 {
		return nil
	}
	db := r.db
	if tx != nil {
		db = tx
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&items).Error
}

func (r *peopleIdentityAssignmentRepository) WithTx(fn func(tx *gorm.DB) error) error {
	if fn == nil {
		return fmt.Errorf("nil tx fn")
	}
	return r.db.Transaction(fn)
}
