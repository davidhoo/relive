package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// DeviceRepository 设备仓库接口
type DeviceRepository interface {
	// 基础 CRUD
	Create(device *model.Device) error
	Update(device *model.Device) error
	Delete(id uint) error
	GetByID(id uint) (*model.Device, error)
	GetByDeviceID(deviceID string) (*model.Device, error)
	GetByAPIKey(apiKey string) (*model.Device, error)
	List(page, pageSize int) ([]*model.Device, int64, error)
	ListAll() ([]*model.Device, error)

	// 查询
	Exists(id uint) (bool, error)
	ExistsByDeviceID(deviceID string) (bool, error)
	ExistsByAPIKey(apiKey string) (bool, error)

	// 按类型查询
	ListByDeviceType(deviceType string) ([]*model.Device, error)
	ListByPlatform(platform string) ([]*model.Device, error)

	// 在线状态
	GetOnlineDevices() ([]*model.Device, error)
	GetOfflineDevices() ([]*model.Device, error)
	UpdateStatus(deviceID string, online bool) error

	// 统计
	Count() (int64, error)
	CountOnline() (int64, error)
	CountOffline() (int64, error)
	CountByDeviceType(deviceType string) (int64, error)
	CountByPlatform(platform string) (int64, error)
}

// deviceRepository 设备仓库实现
type deviceRepository struct {
	db *gorm.DB
}

// NewDeviceRepository 创建设备仓库
func NewDeviceRepository(db *gorm.DB) DeviceRepository {
	return &deviceRepository{db: db}
}

// Create 创建设备
func (r *deviceRepository) Create(device *model.Device) error {
	return r.db.Create(device).Error
}

// Update 更新设备
func (r *deviceRepository) Update(device *model.Device) error {
	return r.db.Save(device).Error
}

// Delete 删除设备（软删除）
func (r *deviceRepository) Delete(id uint) error {
	return r.db.Delete(&model.Device{}, id).Error
}

// GetByID 根据 ID 获取设备
func (r *deviceRepository) GetByID(id uint) (*model.Device, error) {
	var device model.Device
	err := r.db.First(&device, id).Error
	if err != nil {
		return nil, err
	}
	return &device, nil
}

// GetByDeviceID 根据设备 ID 获取设备
func (r *deviceRepository) GetByDeviceID(deviceID string) (*model.Device, error) {
	var device model.Device
	err := r.db.Where("device_id = ?", deviceID).First(&device).Error
	if err != nil {
		return nil, err
	}
	return &device, nil
}

// GetByAPIKey 根据 API Key 获取设备
func (r *deviceRepository) GetByAPIKey(apiKey string) (*model.Device, error) {
	var device model.Device
	err := r.db.Where("api_key = ?", apiKey).First(&device).Error
	if err != nil {
		return nil, err
	}
	return &device, nil
}

// List 分页列表查询
func (r *deviceRepository) List(page, pageSize int) ([]*model.Device, int64, error) {
	var devices []*model.Device
	var total int64

	// 统计总数
	if err := r.db.Model(&model.Device{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 分页
	offset := (page - 1) * pageSize
	if err := r.db.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&devices).Error; err != nil {
		return nil, 0, err
	}

	return devices, total, nil
}

// ListAll 获取所有设备
func (r *deviceRepository) ListAll() ([]*model.Device, error) {
	var devices []*model.Device
	err := r.db.Find(&devices).Error
	return devices, err
}

// Exists 检查设备是否存在
func (r *deviceRepository) Exists(id uint) (bool, error) {
	var count int64
	err := r.db.Model(&model.Device{}).Where("id = ?", id).Count(&count).Error
	return count > 0, err
}

// ExistsByDeviceID 检查设备 ID 是否存在
func (r *deviceRepository) ExistsByDeviceID(deviceID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.Device{}).Where("device_id = ?", deviceID).Count(&count).Error
	return count > 0, err
}

// ExistsByAPIKey 检查 API Key 是否存在
func (r *deviceRepository) ExistsByAPIKey(apiKey string) (bool, error) {
	var count int64
	err := r.db.Model(&model.Device{}).Where("api_key = ?", apiKey).Count(&count).Error
	return count > 0, err
}

// ListByDeviceType 根据设备类型查询
func (r *deviceRepository) ListByDeviceType(deviceType string) ([]*model.Device, error) {
	var devices []*model.Device
	err := r.db.Where("device_type = ?", deviceType).Order("created_at DESC").Find(&devices).Error
	return devices, err
}

// ListByPlatform 根据平台查询
func (r *deviceRepository) ListByPlatform(platform string) ([]*model.Device, error) {
	var devices []*model.Device
	err := r.db.Where("platform = ?", platform).Order("created_at DESC").Find(&devices).Error
	return devices, err
}

// GetOnlineDevices 获取在线设备（5分钟内有心跳）
func (r *deviceRepository) GetOnlineDevices() ([]*model.Device, error) {
	var devices []*model.Device
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)

	err := r.db.Where("last_seen > ?", fiveMinutesAgo).
		Order("last_seen DESC").
		Find(&devices).Error
	return devices, err
}

// GetOfflineDevices 获取离线设备
func (r *deviceRepository) GetOfflineDevices() ([]*model.Device, error) {
	var devices []*model.Device
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)

	err := r.db.Where("last_seen IS NULL OR last_seen <= ?", fiveMinutesAgo).
		Order("last_seen DESC").
		Find(&devices).Error
	return devices, err
}

// UpdateStatus 更新在线状态
func (r *deviceRepository) UpdateStatus(deviceID string, online bool) error {
	return r.db.Model(&model.Device{}).
		Where("device_id = ?", deviceID).
		Update("online", online).Error
}

// Count 统计设备总数
func (r *deviceRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.Device{}).Count(&count).Error
	return count, err
}

// CountOnline 统计在线设备数
func (r *deviceRepository) CountOnline() (int64, error) {
	var count int64
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)

	err := r.db.Model(&model.Device{}).
		Where("last_seen > ?", fiveMinutesAgo).
		Count(&count).Error
	return count, err
}

// CountOffline 统计离线设备数
func (r *deviceRepository) CountOffline() (int64, error) {
	var count int64
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)

	err := r.db.Model(&model.Device{}).
		Where("last_seen IS NULL OR last_seen <= ?", fiveMinutesAgo).
		Count(&count).Error
	return count, err
}

// CountByDeviceType 根据设备类型统计
func (r *deviceRepository) CountByDeviceType(deviceType string) (int64, error) {
	var count int64
	err := r.db.Model(&model.Device{}).Where("device_type = ?", deviceType).Count(&count).Error
	return count, err
}

// CountByPlatform 根据平台统计
func (r *deviceRepository) CountByPlatform(platform string) (int64, error) {
	var count int64
	err := r.db.Model(&model.Device{}).Where("platform = ?", platform).Count(&count).Error
	return count, err
}

// ESP32DeviceRepository 类型别名，保持向后兼容
// Deprecated: 使用 DeviceRepository 代替
type ESP32DeviceRepository = DeviceRepository

// NewESP32DeviceRepository 创建设备仓库（兼容旧代码）
// Deprecated: 使用 NewDeviceRepository 代替
func NewESP32DeviceRepository(db *gorm.DB) DeviceRepository {
	return NewDeviceRepository(db)
}
