package handler

import (
	"github.com/davidhoo/relive/internal/lifecycle"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"gorm.io/gorm"
)

// Handlers 所有处理器的集合
type Handlers struct {
	System     *SystemHandler
	Photo      *PhotoHandler
	People     *PeopleHandler
	Thumbnail  *ThumbnailHandler
	Geocode    *GeocodeHandler
	Display    *DisplayHandler
	Device     *DeviceHandler
	AI         *AIHandler
	Config     *ConfigHandler
	Auth       *AuthHandler
	Analyzer   *AnalyzerHandler
	Event      *EventHandler
	Background *BackgroundHandler
}

// NewHandlers 创建所有处理器
func NewHandlers(db *gorm.DB, services *service.Services, repos *repository.Repositories, cfg *config.Config, appState *lifecycle.State) *Handlers {
	// 创建设备处理器
	deviceHandler := NewDeviceHandler(services.Device)

	handlers := &Handlers{
		System:     NewSystemHandler(services.System, cfg, appState),
		Photo:      NewPhotoHandler(services.Photo, services.Thumbnail, services.GeocodeTask, services.Config, cfg),
		People:     NewPeopleHandler(services.People, services.MergeSuggestion, repos.Person, repos.Face, repos.Photo, repos.PeopleJob, services.IdentityProfile, repos.IdentityDecision, cfg),
		Thumbnail:  NewThumbnailHandler(services.Thumbnail),
		Geocode:    NewGeocodeHandler(services.GeocodeTask),
		Display:    NewDisplayHandler(services.Display, services.Device, cfg),
		Device:     deviceHandler,
		Config:     NewConfigHandler(services.Config, services.AI, services.AnalysisRuntime, services.Photo, services.Prompt, services.Geocode, repos.Photo, repos.PhotoTag, cfg, db),
		Auth:       NewAuthHandler(services.Auth),
		Analyzer:   NewAnalyzerHandler(services.Photo, services.Analysis, services.AnalysisRuntime),
		Event:      NewEventHandler(services.EventClustering, repos.Event, db),
		Background: NewBackgroundHandler(services.BackgroundCoordinator, services.BackgroundLoadSampler, services.ProtoCacheRebuildStatus),
	}

	// AI Handler - 即使 AI 服务未配置也创建，以便配置变更后动态更新
	handlers.AI = NewAIHandler(services.AI, services.AnalysisRuntime)
	handlers.People.SetRuntimeService(services.AnalysisRuntime)
	// 人物详情读请求注册 foreground scope，让 P2 后台任务在用户浏览详情时让路。
	handlers.People.SetBackgroundCoordinator(services.BackgroundCoordinator)
	// 注入人物照片派生表仓库，cursor 分页在迁移完成后走 person_photos 索引。
	handlers.People.SetPersonPhotoRepo(repos.PersonPhoto)
	// 注入人脸质检审核服务。
	handlers.People.SetFaceQualityService(services.FaceQuality)
	handlers.People.SetFaceQualityBackfill(services.FaceQualityBackfill)
	handlers.People.SetFaceQualityRescore(services.FaceQualityRescore)

	// 设置 ConfigHandler 对 AIHandler 的引用，用于配置变更后热重载
	handlers.Config.SetAIHandler(handlers.AI)
	handlers.Config.SetAnalysisCompletedHandler(services.People)

	return handlers
}
