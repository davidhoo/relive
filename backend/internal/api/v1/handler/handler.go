package handler

import (
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"gorm.io/gorm"
)

// Handlers 所有处理器的集合
type Handlers struct {
	System    *SystemHandler
	Photo     *PhotoHandler
	Thumbnail *ThumbnailHandler
	Geocode   *GeocodeHandler
	Display   *DisplayHandler
	Device    *DeviceHandler
	AI        *AIHandler
	Config    *ConfigHandler
	Auth      *AuthHandler
	Analyzer  *AnalyzerHandler
}

// NewHandlers 创建所有处理器
func NewHandlers(db *gorm.DB, services *service.Services, repos *repository.Repositories, cfg *config.Config) *Handlers {
	// 创建设备处理器
	deviceHandler := NewDeviceHandler(services.Device)

	handlers := &Handlers{
		System:    NewSystemHandler(db, cfg, services),
		Photo:     NewPhotoHandler(services.Photo, services.Thumbnail, services.GeocodeTask, services.Config, cfg),
		Thumbnail: NewThumbnailHandler(services.Thumbnail),
		Geocode:   NewGeocodeHandler(services.GeocodeTask),
		Display:   NewDisplayHandler(services.Display, services.Device, cfg),
		Device:    deviceHandler,
		Config:    NewConfigHandler(services.Config, services.AI, services.AnalysisRuntime, services.Photo, services.Prompt, services.Geocode, repos.Photo, repos.PhotoTag, cfg, db),
		Auth:      NewAuthHandler(services.Auth, cfg.Server.Mode != "debug"),
		Analyzer:  NewAnalyzerHandler(services.Photo, services.Analysis, services.AnalysisRuntime),
	}

	// AI Handler - 即使 AI 服务未配置也创建，以便配置变更后动态更新
	handlers.AI = NewAIHandler(services.AI, services.AnalysisRuntime)

	// 设置 ConfigHandler 对 AIHandler 的引用，用于配置变更后热重载
	handlers.Config.SetAIHandler(handlers.AI)

	return handlers
}
