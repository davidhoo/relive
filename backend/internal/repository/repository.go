package repository

import "gorm.io/gorm"

// Repositories 所有仓库的集合
type Repositories struct {
	Photo         PhotoRepository
	DisplayRecord DisplayRecordRepository
	ESP32Device   ESP32DeviceRepository
	Config        ConfigRepository
	User          UserRepository
	APIKey        APIKeyRepository
}

// NewRepositories 创建所有仓库
func NewRepositories(db *gorm.DB) *Repositories {
	return &Repositories{
		Photo:         NewPhotoRepository(db),
		DisplayRecord: NewDisplayRecordRepository(db),
		ESP32Device:   NewESP32DeviceRepository(db),
		Config:        NewConfigRepository(db),
		User:          NewUserRepository(db),
		APIKey:        NewAPIKeyRepository(db),
	}
}
