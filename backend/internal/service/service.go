package service

import (
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// Services 所有服务的集合
type Services struct {
	Photo   PhotoService
	Display DisplayService
	ESP32   ESP32Service
	AI      AIService
	Export  ExportService
	Config  ConfigService
	Geocode GeocodeService
}

// NewServices 创建所有服务
func NewServices(repos *repository.Repositories, cfg *config.Config, db *gorm.DB) *Services {
	// 创建 AI 服务（可能失败，不阻塞其他服务）
	aiService, err := NewAIService(repos.Photo, cfg)
	if err != nil {
		logger.Warnf("Failed to initialize AI service: %v", err)
		aiService = nil
	}

	// 创建 Geocode 服务（可能失败，不阻塞其他服务）
	geocodeService, err := NewGeocodeService(db, cfg)
	if err != nil {
		logger.Warnf("Failed to initialize Geocode service: %v", err)
		geocodeService = nil
	}

	return &Services{
		Photo: NewPhotoService(repos.Photo, cfg, geocodeService),
		Display: NewDisplayService(
			repos.Photo,
			repos.DisplayRecord,
			repos.ESP32Device,
			cfg,
		),
		ESP32:   NewESP32Service(repos.ESP32Device, cfg),
		AI:      aiService,
		Export:  NewExportService(repos.Photo),
		Config:  NewConfigService(repos.Config),
		Geocode: geocodeService,
	}
}
