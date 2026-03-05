package handler

import (
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"gorm.io/gorm"
)

// Handlers 所有处理器的集合
type Handlers struct {
	System   *SystemHandler
	Photo    *PhotoHandler
	Display  *DisplayHandler
	Device   *DeviceHandler // 新名称
	ESP32    *ESP32Handler  // 保留兼容（别名）
	AI       *AIHandler
	Export   *ExportHandler
	Config   *ConfigHandler
	Auth     *AuthHandler
	Analyzer *AnalyzerHandler
}

// NewHandlers 创建所有处理器
func NewHandlers(db *gorm.DB, services *service.Services, repos *repository.Repositories, cfg *config.Config) *Handlers {
	// 创建设备处理器
	deviceHandler := NewDeviceHandler(services.Device)

	handlers := &Handlers{
		System:   NewSystemHandler(db, cfg, services),
		Photo:    NewPhotoHandler(services.Photo, services.Config, cfg),
		Display:  NewDisplayHandler(services.Display, services.Device),
		Device:   deviceHandler,
		ESP32:    deviceHandler,
		Export:   NewExportHandler(services.Export),
		Config:   NewConfigHandler(services.Config, services.AI, services.Photo, services.Prompt, repos.Photo, cfg),
		Auth:     NewAuthHandler(services.Auth),
		Analyzer: NewAnalyzerHandler(services.Photo, services.Analysis),
	}

	// AI Handler - 即使 AI 服务未配置也创建，以便配置变更后动态更新
	handlers.AI = NewAIHandler(services.AI)

	// 设置 ConfigHandler 对 AIHandler 的引用，用于配置变更后热重载
	handlers.Config.SetAIHandler(handlers.AI)

	return handlers
}
