package service

import (
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// Services 所有服务的集合
type Services struct {
	Photo     PhotoService
	Display   DisplayService
	ESP32     ESP32Service
	AI        AIService
	Export    ExportService
	Config    ConfigService
	Prompt    PromptService
	Geocode   GeocodeService
	Auth      AuthService
	APIKey    APIKeyService
	Analysis  AnalysisService
	Scheduler *TaskScheduler
}

// NewServices 创建所有服务
// NewServices 创建所有服务
func NewServices(repos *repository.Repositories, cfg *config.Config, db *gorm.DB) *Services {
	// 首先创建 Config 服务（其他服务可能需要访问配置）
	configService := NewConfigService(repos.Config)

	// 创建 AI 服务（可能失败，不阻塞其他服务）
	aiService, err := NewAIService(repos.Photo, cfg, configService)
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

	// 创建认证服务并初始化默认用户
	authService := NewAuthService(repos.User, cfg)
	if err := authService.InitializeDefaultUser(); err != nil {
		logger.Warnf("Failed to initialize default user: %v", err)
	}

	// 创建 API Key 服务并初始化默认Key
	apiKeyService := NewAPIKeyService(repos.APIKey, cfg)
	if err := apiKeyService.InitializeDefaultKey(); err != nil {
		logger.Warnf("Failed to initialize default API key: %v", err)
	}

	// 创建分析服务
	analysisService := NewAnalysisService(db, repos.Photo, cfg)

	// 创建定时任务调度器
	scheduler := NewTaskScheduler(analysisService)

	// 创建提示词配置服务
	promptService := NewPromptService(repos.Config)

	return &Services{
		Photo:    NewPhotoService(repos.Photo, cfg, configService, geocodeService),
		Display:  NewDisplayService(
			repos.Photo,
			repos.DisplayRecord,
			repos.ESP32Device,
			cfg,
		),
		ESP32:    NewESP32Service(repos.ESP32Device, cfg),
		AI:       aiService,
		Export:   NewExportService(repos.Photo),
		Config:   configService,
		Prompt:   promptService,
		Geocode:  geocodeService,
		Auth:      authService,
		APIKey:    apiKeyService,
		Analysis:  analysisService,
		Scheduler: scheduler,
	}
}
