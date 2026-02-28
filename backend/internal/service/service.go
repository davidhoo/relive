package service

import (
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
)

// Services 所有服务的集合
type Services struct {
	Photo   PhotoService
	Display DisplayService
	ESP32   ESP32Service
}

// NewServices 创建所有服务
func NewServices(repos *repository.Repositories, cfg *config.Config) *Services {
	return &Services{
		Photo: NewPhotoService(repos.Photo, cfg),
		Display: NewDisplayService(
			repos.Photo,
			repos.DisplayRecord,
			repos.ESP32Device,
			cfg,
		),
		ESP32: NewESP32Service(repos.ESP32Device, cfg),
	}
}
