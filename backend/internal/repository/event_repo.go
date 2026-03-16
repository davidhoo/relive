package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// EventRepository 事件仓储接口
type EventRepository interface {
	Create(event *model.Event) error
	Update(event *model.Event) error
	Delete(id uint) error
	GetByID(id uint) (*model.Event, error)
	List(page, pageSize int) ([]*model.Event, int64, error)
	DeleteAll() error
	GetByTimeRange(start, end time.Time) ([]*model.Event, error)
	UpdateProfileFields(id uint, fields map[string]interface{}) error
}

type eventRepository struct {
	db *gorm.DB
}

// NewEventRepository 创建事件仓储
func NewEventRepository(db *gorm.DB) EventRepository {
	return &eventRepository{db: db}
}

func (r *eventRepository) Create(event *model.Event) error {
	return r.db.Create(event).Error
}

func (r *eventRepository) Update(event *model.Event) error {
	return r.db.Save(event).Error
}

func (r *eventRepository) Delete(id uint) error {
	return r.db.Delete(&model.Event{}, id).Error
}

func (r *eventRepository) GetByID(id uint) (*model.Event, error) {
	var event model.Event
	if err := r.db.First(&event, id).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

func (r *eventRepository) List(page, pageSize int) ([]*model.Event, int64, error) {
	var events []*model.Event
	var total int64

	if err := r.db.Model(&model.Event{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := r.db.Order("start_time DESC").Offset(offset).Limit(pageSize).Find(&events).Error; err != nil {
		return nil, 0, err
	}

	return events, total, nil
}

func (r *eventRepository) DeleteAll() error {
	return r.db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Event{}).Error
}

func (r *eventRepository) GetByTimeRange(start, end time.Time) ([]*model.Event, error) {
	var events []*model.Event
	if err := r.db.Where("start_time <= ? AND end_time >= ?", end, start).Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

func (r *eventRepository) UpdateProfileFields(id uint, fields map[string]interface{}) error {
	return r.db.Model(&model.Event{}).Where("id = ?", id).Updates(fields).Error
}
