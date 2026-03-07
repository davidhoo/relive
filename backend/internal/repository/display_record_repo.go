package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// DisplayRecordRepository 展示记录仓库接口
type DisplayRecordRepository interface {
	// 基础 CRUD
	Create(record *model.DisplayRecord) error
	GetByID(id uint) (*model.DisplayRecord, error)
	List(page, pageSize int, deviceID *uint, photoID *uint) ([]*model.DisplayRecord, int64, error)

	// 查询
	GetByDeviceID(deviceID uint, limit int) ([]*model.DisplayRecord, error)
	GetByPhotoID(photoID uint) ([]*model.DisplayRecord, error)
	GetRecentByDevice(deviceID uint, days int) ([]*model.DisplayRecord, error)
	GetLastDisplayedPhoto(deviceID uint) (*model.DisplayRecord, error)

	// 检查
	WasDisplayedRecently(photoID uint, deviceID uint, days int) (bool, error)
	GetDisplayedPhotoIDs(deviceID uint, days int) ([]uint, error)
	GetDisplayedPhotoIDsAll(days int) ([]uint, error)

	// 统计
	Count() (int64, error)
	CountByDevice(deviceID uint) (int64, error)
	CountByPhoto(photoID uint) (int64, error)
	CountByDateRange(start, end time.Time) (int64, error)
}

// displayRecordRepository 展示记录仓库实现
type displayRecordRepository struct {
	db *gorm.DB
}

// NewDisplayRecordRepository 创建展示记录仓库
func NewDisplayRecordRepository(db *gorm.DB) DisplayRecordRepository {
	return &displayRecordRepository{db: db}
}

// Create 创建展示记录
func (r *displayRecordRepository) Create(record *model.DisplayRecord) error {
	return r.db.Create(record).Error
}

// GetByID 根据 ID 获取展示记录
func (r *displayRecordRepository) GetByID(id uint) (*model.DisplayRecord, error) {
	var record model.DisplayRecord
	err := r.db.Preload("Photo").Preload("Device").First(&record, id).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// List 分页列表查询
func (r *displayRecordRepository) List(page, pageSize int, deviceID *uint, photoID *uint) ([]*model.DisplayRecord, int64, error) {
	var records []*model.DisplayRecord
	var total int64

	// 构建查询
	query := r.db.Model(&model.DisplayRecord{}).Preload("Photo").Preload("Device")

	// 筛选条件
	if deviceID != nil {
		query = query.Where("device_id = ?", *deviceID)
	}
	if photoID != nil {
		query = query.Where("photo_id = ?", *photoID)
	}

	// 统计总数
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 分页
	offset := (page - 1) * pageSize
	if err := query.Order("displayed_at DESC").Offset(offset).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, 0, err
	}

	return records, total, nil
}

// GetByDeviceID 根据设备 ID 获取展示记录
func (r *displayRecordRepository) GetByDeviceID(deviceID uint, limit int) ([]*model.DisplayRecord, error) {
	var records []*model.DisplayRecord
	err := r.db.Where("device_id = ?", deviceID).
		Preload("Photo").
		Order("displayed_at DESC").
		Limit(limit).
		Find(&records).Error
	return records, err
}

// GetByPhotoID 根据照片 ID 获取展示记录
func (r *displayRecordRepository) GetByPhotoID(photoID uint) ([]*model.DisplayRecord, error) {
	var records []*model.DisplayRecord
	err := r.db.Where("photo_id = ?", photoID).
		Preload("Device").
		Order("displayed_at DESC").
		Find(&records).Error
	return records, err
}

// GetRecentByDevice 获取设备最近的展示记录
func (r *displayRecordRepository) GetRecentByDevice(deviceID uint, days int) ([]*model.DisplayRecord, error) {
	var records []*model.DisplayRecord
	cutoffTime := time.Now().AddDate(0, 0, -days)

	err := r.db.Where("device_id = ? AND displayed_at >= ?", deviceID, cutoffTime).
		Preload("Photo").
		Order("displayed_at DESC").
		Find(&records).Error
	return records, err
}

// GetLastDisplayedPhoto 获取设备最后展示的照片
func (r *displayRecordRepository) GetLastDisplayedPhoto(deviceID uint) (*model.DisplayRecord, error) {
	var record model.DisplayRecord
	err := r.db.Where("device_id = ?", deviceID).
		Preload("Photo").
		Order("displayed_at DESC").
		First(&record).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// WasDisplayedRecently 检查照片是否最近被展示过
func (r *displayRecordRepository) WasDisplayedRecently(photoID uint, deviceID uint, days int) (bool, error) {
	var count int64
	cutoffTime := time.Now().AddDate(0, 0, -days)

	err := r.db.Model(&model.DisplayRecord{}).
		Where("photo_id = ? AND device_id = ? AND displayed_at >= ?", photoID, deviceID, cutoffTime).
		Count(&count).Error

	return count > 0, err
}

// GetDisplayedPhotoIDs 获取最近展示过的照片 ID 列表
func (r *displayRecordRepository) GetDisplayedPhotoIDs(deviceID uint, days int) ([]uint, error) {
	var photoIDs []uint
	cutoffTime := time.Now().AddDate(0, 0, -days)

	err := r.db.Model(&model.DisplayRecord{}).
		Where("device_id = ? AND displayed_at >= ?", deviceID, cutoffTime).
		Distinct("photo_id").
		Pluck("photo_id", &photoIDs).Error

	return photoIDs, err
}

// GetDisplayedPhotoIDsAll 获取最近在任意设备上展示过的照片 ID 列表
func (r *displayRecordRepository) GetDisplayedPhotoIDsAll(days int) ([]uint, error) {
	var photoIDs []uint
	cutoffTime := time.Now().AddDate(0, 0, -days)

	err := r.db.Model(&model.DisplayRecord{}).
		Where("displayed_at >= ?", cutoffTime).
		Distinct("photo_id").
		Pluck("photo_id", &photoIDs).Error

	return photoIDs, err
}

// Count 统计展示记录总数
func (r *displayRecordRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.DisplayRecord{}).Count(&count).Error
	return count, err
}

// CountByDevice 统计设备的展示记录数
func (r *displayRecordRepository) CountByDevice(deviceID uint) (int64, error) {
	var count int64
	err := r.db.Model(&model.DisplayRecord{}).Where("device_id = ?", deviceID).Count(&count).Error
	return count, err
}

// CountByPhoto 统计照片的展示次数
func (r *displayRecordRepository) CountByPhoto(photoID uint) (int64, error) {
	var count int64
	err := r.db.Model(&model.DisplayRecord{}).Where("photo_id = ?", photoID).Count(&count).Error
	return count, err
}

// CountByDateRange 统计日期范围内的展示记录数
func (r *displayRecordRepository) CountByDateRange(start, end time.Time) (int64, error) {
	var count int64
	err := r.db.Model(&model.DisplayRecord{}).
		Where("displayed_at BETWEEN ? AND ?", start, end).
		Count(&count).Error
	return count, err
}
