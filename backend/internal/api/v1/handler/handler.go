package handler

import (
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"gorm.io/gorm"
)

// Handlers 所有处理器的集合
type Handlers struct {
	System   *SystemHandler
	Photo    *PhotoHandler
	Display  *DisplayHandler
	ESP32    *ESP32Handler
	AI       *AIHandler
	Export   *ExportHandler
	Config   *ConfigHandler
	Auth     *AuthHandler
	APIKey   *APIKeyHandler
	Analyzer *AnalyzerHandler
}

// NewHandlers 创建所有处理器
func NewHandlers(db *gorm.DB, services *service.Services, cfg *config.Config) *Handlers {
	handlers := &Handlers{
		System:   NewSystemHandler(db, services),
		Photo:    NewPhotoHandler(services.Photo, services.Config, cfg),
		Display:  NewDisplayHandler(services.Display, services.ESP32),
		ESP32:    NewESP32Handler(services.ESP32),
		Export:   NewExportHandler(services.Export),
		Config:   NewConfigHandler(services.Config, services.AI, services.Photo, cfg),
		Auth:     NewAuthHandler(services.Auth),
		APIKey:   NewAPIKeyHandler(services.APIKey),
		Analyzer: NewAnalyzerHandler(services.Photo, services.Analysis),
	}

	// AI Handler 可能为 nil（如果 AI 服务未配置）
	if services.AI != nil {
		handlers.AI = NewAIHandler(services.AI)
	}

	return handlers
}
