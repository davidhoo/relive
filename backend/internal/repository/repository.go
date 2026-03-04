package repository

import "gorm.io/gorm"

// Repositories 所有仓库的集合
type Repositories struct {
	Photo         PhotoRepository
	DisplayRecord DisplayRecordRepository
	Device        DeviceRepository        // 新名称
	ESP32Device   ESP32DeviceRepository   // 保留兼容（别名）
	Config        ConfigRepository
	User          UserRepository
	APIKey        APIKeyRepository
}

// NewRepositories 创建所有仓库
func NewRepositories(db *gorm.DB) *Repositories {
	deviceRepo := NewDeviceRepository(db)
	return &Repositories{
		Photo:         NewPhotoRepository(db),
		DisplayRecord: NewDisplayRecordRepository(db),
		Device:        deviceRepo,         // 新名称
		ESP32Device:   deviceRepo,         // 兼容旧代码（指向同一个实例）
		Config:        NewConfigRepository(db),
		User:          NewUserRepository(db),
		APIKey:        NewAPIKeyRepository(db),
	}
}
