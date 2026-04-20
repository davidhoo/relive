package service

import (
	"time"

	"github.com/davidhoo/relive/internal/mlclient"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// Services 所有服务的集合
type Services struct {
	Photo           PhotoService
	People          PeopleService
	MergeSuggestion PersonMergeSuggestionService
	Thumbnail       ThumbnailService
	GeocodeTask     GeocodeTaskService
	Display         DisplayService
	Device          DeviceService
	AI              AIService
	AnalysisRuntime AnalysisRuntimeService
	Config          ConfigService
	Prompt          PromptService
	Geocode         GeocodeService
	Auth            AuthService
	Analysis        AnalysisService
	System          SystemService
	EventClustering EventClusteringService
	Scheduler       *TaskScheduler
	ResultQueue     *ResultQueue // 结果队列服务
}

// NewServices 创建所有服务
// NewServices 创建所有服务
func NewServices(repos *repository.Repositories, cfg *config.Config, db *gorm.DB) *Services {
	// 首先创建 Config 服务（其他服务可能需要访问配置）
	configService := NewConfigService(repos.Config)

	// 创建 AI 服务（可能失败，不阻塞其他服务）
	runtimeService := NewAnalysisRuntimeService(db)
	aiService, err := NewAIService(repos.Photo, repos.PhotoTag, cfg, configService, runtimeService)
	if err != nil {
		logger.Warnf("Failed to initialize AI service: %v", err)
		aiService = nil
	}

	// 创建 Geocode 服务（可能失败，不阻塞其他服务）
	// 传入 configService，以便优先从数据库读取用户保存的 geocode 配置
	geocodeService, err := NewGeocodeService(db, cfg, configService)
	if err != nil {
		logger.Warnf("Failed to initialize Geocode service: %v", err)
		geocodeService = nil
	}

	// 创建认证服务并初始化默认用户
	authService := NewAuthService(repos.User, cfg)
	if err := authService.InitializeDefaultUser(); err != nil {
		logger.Warnf("Failed to initialize default user: %v", err)
	}

	// 创建分析、照片与展示服务
	analysisService := NewAnalysisService(db, repos.Photo, repos.PhotoTag, cfg)
	thumbnailService := NewThumbnailService(db, repos.Photo, repos.ThumbnailJob, cfg)
	geocodeTaskService := NewGeocodeTaskService(db, repos.Photo, repos.GeocodeJob, geocodeService)
	photoService := NewPhotoService(repos.Photo, repos.PhotoTag, repos.ScanJob, cfg, configService, geocodeService, thumbnailService, geocodeTaskService)
	var peopleClient PeopleMLClient
	if cfg != nil && cfg.People.MLEndpoint != "" {
		peopleClient = mlclient.New(cfg.People.MLEndpoint, time.Duration(cfg.People.Timeout)*time.Second)
	}
	peopleSvc := NewPeopleService(db, repos.Photo, repos.Face, repos.Person, repos.PeopleJob, repos.CannotLink, cfg, peopleClient, runtimeService)
	mergeSuggestionService := NewPersonMergeSuggestionService(
		db,
		repos.Photo,
		repos.Face,
		repos.Person,
		repos.PeopleJob,
		repos.CannotLink,
		repos.MergeSuggestion,
		configService,
		cfg,
	)
	peopleSvc.(*peopleService).setMergeSuggestionDirtyHook(mergeSuggestionService.MarkDirty)
	photoService.SetPeopleService(peopleSvc)
	displayService := NewDisplayService(db, repos.Photo, repos.DisplayRecord, repos.Device, repos.Event, configService, cfg)

	// 创建事件聚类服务并注入到 photoService
	eventClusteringService := NewEventClusteringService(db, repos.Photo, repos.Event, repos.PhotoTag)
	photoService.SetEventClusteringService(eventClusteringService)

	// 创建定时任务调度器
	scheduler := NewTaskScheduler(analysisService, displayService, photoService, mergeSuggestionService, repos.ThumbnailJob, repos.GeocodeJob)

	// 创建提示词配置服务
	promptService := NewPromptService(repos.Config)

	// 创建设备服务
	deviceService := NewDeviceService(repos.Device, cfg)

	// 创建结果队列存储
	var resultStorage repository.ResultStorage
	if cfg.Database.Type == "sqlite" {
		// 使用数据库存储（表由主 AutoMigrate 统一迁移）
		resultStorage = repository.NewDBResultStorage(db)
	} else {
		// 使用文件存储（备用）
		resultStorage = repository.NewFileResultStorage(cfg.Database.Path)
	}

	// 创建结果队列
	resultQueue := NewResultQueue(resultStorage, analysisService, DefaultResultQueueConfig())

	// 将队列设置到分析服务
	analysisService.SetResultQueue(resultQueue)

	return &Services{
		Photo:           photoService,
		People:          peopleSvc,
		MergeSuggestion: mergeSuggestionService,
		Thumbnail:       thumbnailService,
		GeocodeTask:     geocodeTaskService,
		Display:         displayService,
		Device:          deviceService,
		AI:              aiService,
		AnalysisRuntime: runtimeService,
		Config:          configService,
		Prompt:          promptService,
		Geocode:         geocodeService,
		Auth:            authService,
		Analysis:        analysisService,
		System:          NewSystemService(db),
		EventClustering: eventClusteringService,
		Scheduler:       scheduler,
		ResultQueue:     resultQueue,
	}
}
