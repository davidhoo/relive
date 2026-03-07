package repository

import "gorm.io/gorm"

// Repositories 所有仓库的集合
type Repositories struct {
	Photo         PhotoRepository
	DisplayRecord DisplayRecordRepository
	Device        DeviceRepository
	Config        ConfigRepository
	User          UserRepository
}

// NewRepositories 创建所有仓库
func NewRepositories(db *gorm.DB) *Repositories {
	deviceRepo := NewDeviceRepository(db)
	return &Repositories{
		Photo:         NewPhotoRepository(db),
		DisplayRecord: NewDisplayRecordRepository(db),
		Device:        deviceRepo,
		Config:        NewConfigRepository(db),
		User:          NewUserRepository(db),
	}
}
