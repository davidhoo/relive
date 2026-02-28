package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// ESP32DeviceRepository ESP32 设备仓库接口
type ESP32DeviceRepository interface {
	// 基础 CRUD
	Create(device *model.ESP32Device) error
	Update(device *model.ESP32Device) error
	Delete(id uint) error
	GetByID(id uint) (*model.ESP32Device, error)
	GetByDeviceID(deviceID string) (*model.ESP32Device, error)
	GetByAPIKey(apiKey string) (*model.ESP32Device, error)
	List(page, pageSize int) ([]*model.ESP32Device, int64, error)
	ListAll() ([]*model.ESP32Device, error)

	// 查询
	Exists(id uint) (bool, error)
	ExistsByDeviceID(deviceID string) (bool, error)
	ExistsByAPIKey(apiKey string) (bool, error)

	// 在线状态
	GetOnlineDevices() ([]*model.ESP32Device, error)
	GetOfflineDevices() ([]*model.ESP32Device, error)
	UpdateHeartbeat(deviceID string, batteryLevel int, wifiRSSI int) error
	UpdateStatus(deviceID string, online bool) error

	// 统计
	Count() (int64, error)
	CountOnline() (int64, error)
	CountOffline() (int64, error)
}

// esp32DeviceRepository ESP32 设备仓库实现
type esp32DeviceRepository struct {
	db *gorm.DB
}

// NewESP32DeviceRepository 创建 ESP32 设备仓库
func NewESP32DeviceRepository(db *gorm.DB) ESP32DeviceRepository {
	return &esp32DeviceRepository{db: db}
}

// Create 创建设备
func (r *esp32DeviceRepository) Create(device *model.ESP32Device) error {
	return r.db.Create(device).Error
}

// Update 更新设备
func (r *esp32DeviceRepository) Update(device *model.ESP32Device) error {
	return r.db.Save(device).Error
}

// Delete 删除设备（软删除）
func (r *esp32DeviceRepository) Delete(id uint) error {
	return r.db.Delete(&model.ESP32Device{}, id).Error
}

// GetByID 根据 ID 获取设备
func (r *esp32DeviceRepository) GetByID(id uint) (*model.ESP32Device, error) {
	var device model.ESP32Device
	err := r.db.First(&device, id).Error
	if err != nil {
		return nil, err
	}
	return &device, nil
}

// GetByDeviceID 根据设备 ID 获取设备
func (r *esp32DeviceRepository) GetByDeviceID(deviceID string) (*model.ESP32Device, error) {
	var device model.ESP32Device
	err := r.db.Where("device_id = ?", deviceID).First(&device).Error
	if err != nil {
		return nil, err
	}
	return &device, nil
}

// GetByAPIKey 根据 API Key 获取设备
func (r *esp32DeviceRepository) GetByAPIKey(apiKey string) (*model.ESP32Device, error) {
	var device model.ESP32Device
	err := r.db.Where("api_key = ?", apiKey).First(&device).Error
	if err != nil {
		return nil, err
	}
	return &device, nil
}

// List 分页列表查询
func (r *esp32DeviceRepository) List(page, pageSize int) ([]*model.ESP32Device, int64, error) {
	var devices []*model.ESP32Device
	var total int64

	// 统计总数
	if err := r.db.Model(&model.ESP32Device{}).Count(&total).Error; err != nil {
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
func (r *esp32DeviceRepository) ListAll() ([]*model.ESP32Device, error) {
	var devices []*model.ESP32Device
	err := r.db.Find(&devices).Error
	return devices, err
}

// Exists 检查设备是否存在
func (r *esp32DeviceRepository) Exists(id uint) (bool, error) {
	var count int64
	err := r.db.Model(&model.ESP32Device{}).Where("id = ?", id).Count(&count).Error
	return count > 0, err
}

// ExistsByDeviceID 检查设备 ID 是否存在
func (r *esp32DeviceRepository) ExistsByDeviceID(deviceID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.ESP32Device{}).Where("device_id = ?", deviceID).Count(&count).Error
	return count > 0, err
}

// ExistsByAPIKey 检查 API Key 是否存在
func (r *esp32DeviceRepository) ExistsByAPIKey(apiKey string) (bool, error) {
	var count int64
	err := r.db.Model(&model.ESP32Device{}).Where("api_key = ?", apiKey).Count(&count).Error
	return count > 0, err
}

// GetOnlineDevices 获取在线设备（5分钟内有心跳）
func (r *esp32DeviceRepository) GetOnlineDevices() ([]*model.ESP32Device, error) {
	var devices []*model.ESP32Device
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)

	err := r.db.Where("last_heartbeat > ?", fiveMinutesAgo).
		Order("last_heartbeat DESC").
		Find(&devices).Error
	return devices, err
}

// GetOfflineDevices 获取离线设备
func (r *esp32DeviceRepository) GetOfflineDevices() ([]*model.ESP32Device, error) {
	var devices []*model.ESP32Device
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)

	err := r.db.Where("last_heartbeat IS NULL OR last_heartbeat <= ?", fiveMinutesAgo).
		Order("last_heartbeat DESC").
		Find(&devices).Error
	return devices, err
}

// UpdateHeartbeat 更新心跳
func (r *esp32DeviceRepository) UpdateHeartbeat(deviceID string, batteryLevel int, wifiRSSI int) error {
	now := time.Now()
	return r.db.Model(&model.ESP32Device{}).
		Where("device_id = ?", deviceID).
		Updates(map[string]interface{}{
			"last_heartbeat": now,
			"battery_level":  batteryLevel,
			"wifi_rssi":      wifiRSSI,
			"online":         true,
		}).Error
}

// UpdateStatus 更新在线状态
func (r *esp32DeviceRepository) UpdateStatus(deviceID string, online bool) error {
	return r.db.Model(&model.ESP32Device{}).
		Where("device_id = ?", deviceID).
		Update("online", online).Error
}

// Count 统计设备总数
func (r *esp32DeviceRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.ESP32Device{}).Count(&count).Error
	return count, err
}

// CountOnline 统计在线设备数
func (r *esp32DeviceRepository) CountOnline() (int64, error) {
	var count int64
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)

	err := r.db.Model(&model.ESP32Device{}).
		Where("last_heartbeat > ?", fiveMinutesAgo).
		Count(&count).Error
	return count, err
}

// CountOffline 统计离线设备数
func (r *esp32DeviceRepository) CountOffline() (int64, error) {
	var count int64
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)

	err := r.db.Model(&model.ESP32Device{}).
		Where("last_heartbeat IS NULL OR last_heartbeat <= ?", fiveMinutesAgo).
		Count(&count).Error
	return count, err
}
