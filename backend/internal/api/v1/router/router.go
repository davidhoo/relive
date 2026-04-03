package router

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/api/v1/handler"
	"github.com/davidhoo/relive/internal/middleware"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Setup 设置路由，返回 gin 引擎和服务集合
func Setup(db *gorm.DB, cfg *config.Config) (*gin.Engine, *service.Services) {
	r := gin.New()

	// 中间件
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// CORS 中间件：仅开发环境需要（前后端不同端口）
	// 生产环境前端和后端同源，无需 CORS
	if cfg.Server.Mode == "debug" {
		r.Use(cors.New(cors.Config{
			AllowOrigins:     []string{"http://localhost:5173", "http://127.0.0.1:5173"},
			AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept", "X-Requested-With", "X-API-Key"},
			ExposeHeaders:    []string{"Content-Length", "Content-Type"},
			AllowCredentials: true,
			MaxAge:           12 * time.Hour,
		}))
	}

	// 提供前端静态文件（单镜像部署）
	// 在生产环境中，前端文件在 /app/frontend/dist
	// 在开发环境中，前端由 Vite 独立提供
	if cfg.Server.StaticPath != "" {
		r.Static("/assets", cfg.Server.StaticPath+"/assets")
		// SPA fallback - 静态文件优先，其余返回 index.html
		r.NoRoute(func(c *gin.Context) {
			p := c.Request.URL.Path
			// 跳过 API 路径
			if strings.HasPrefix(p, "/api/") {
				c.JSON(404, gin.H{"error": "not found"})
				return
			}
			// 尝试提供根目录下的静态文件（favicon、logo 等）
			filePath := filepath.Join(cfg.Server.StaticPath, filepath.Clean(p))
			if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
				c.File(filePath)
				return
			}
			// SPA fallback
			c.File(cfg.Server.StaticPath + "/index.html")
		})
	}

	// 初始化 Repositories
	repos := repository.NewRepositories(db)

	// 初始化 Services
	services := service.NewServices(repos, cfg, db)

	// 初始化 Handlers
	handlers := handler.NewHandlers(db, services, repos, cfg)

	// API 路由组
	v1 := r.Group("/api/v1")
	{
		// 认证相关（公开接口）
		auth := v1.Group("/auth")
		{
			auth.POST("/login", middleware.LoginRateLimit(), handlers.Auth.Login)
			auth.POST("/logout", handlers.Auth.Logout)
			// 以下接口需要 JWT 认证，但不需要检查首次登录
			auth.POST("/change-Password", middleware.JWTAuth(services.Auth), handlers.Auth.ChangePassword)
			auth.GET("/user", middleware.JWTAuth(services.Auth), handlers.Auth.GetUserInfo)
		}

		// 系统相关（公开接口）
		system := v1.Group("/system")
		{
			system.GET("/health", handlers.System.Health)
			system.GET("/environment", handlers.System.Environment)
		}

		// 设备管理（JWT 认证 - 管理员操作）
		devicesManage := v1.Group("/devices")
		devicesManage.Use(middleware.JWTAuth(services.Auth))
		devicesManage.Use(middleware.FirstLoginCheck(services.Auth))
		{
			devicesManage.POST("", handlers.Device.CreateDevice)                   // 创建设备
			devicesManage.DELETE("/:id", handlers.Device.DeleteDevice)             // 删除设备
			devicesManage.PUT("/:id/enabled", handlers.Device.UpdateDeviceEnabled) // 启用/禁用设备
			devicesManage.PUT("/:id/render-profile", handlers.Device.UpdateDeviceRenderProfile)
			devicesManage.GET("/stats", handlers.Device.GetDeviceStats)
			devicesManage.GET("", handlers.Device.GetDevices)
			devicesManage.GET("/:device_id", handlers.Device.GetDeviceByID)
		}

		// 展示相关（API Key 认证，兼容旧接口）
		display := v1.Group("/display")
		display.Use(middleware.APIKeyAuth(services.Device))
		{
			display.GET("/photo", handlers.Display.GetDisplayPhoto)
			display.POST("/record", handlers.Display.RecordDisplay)
		}

		deviceDisplay := v1.Group("/device")
		deviceDisplay.Use(middleware.APIKeyAuth(services.Device))
		{
			deviceDisplay.GET("/display", handlers.Display.GetDeviceDisplay)
			deviceDisplay.HEAD("/display.bin", handlers.Display.HeadDeviceDisplayBin)
			deviceDisplay.GET("/display.bin", handlers.Display.GetDeviceDisplayBin)
		}

		// 分析器相关（API Key 认证，离线分析器使用）
		analyzer := v1.Group("/analyzer")
		analyzer.Use(middleware.APIKeyAuth(services.Device))
		{
			analyzer.GET("/tasks", handlers.Analyzer.GetTasks)
			analyzer.POST("/tasks/:task_id/heartbeat", handlers.Analyzer.Heartbeat)
			analyzer.POST("/tasks/:task_id/release", handlers.Analyzer.ReleaseTask)
			analyzer.POST("/results", handlers.Analyzer.SubmitResults)
			analyzer.GET("/stats", handlers.Analyzer.GetStats)
			analyzer.POST("/clean-locks", handlers.Analyzer.CleanExpiredLocks)
			analyzer.POST("/runtime/acquire", handlers.Analyzer.AcquireRuntime)
			analyzer.POST("/runtime/heartbeat", handlers.Analyzer.HeartbeatRuntime)
			analyzer.POST("/runtime/release", handlers.Analyzer.ReleaseRuntime)
		}

		// 图片访问（JWT 或 API Key 认证）
		photoAuth := v1.Group("")
		photoAuth.Use(middleware.PhotoAuth(services.Auth, services.Device))
		{
			photoAuth.GET("/faces/:id/thumbnail", handlers.People.GetFaceThumbnail)
			photoAuth.GET("/photos/:id/image", handlers.Photo.GetPhotoImage)
			photoAuth.GET("/photos/:id/thumbnail", handlers.Photo.GetPhotoThumbnail)
			photoAuth.GET("/photos/:id/frame-preview", handlers.Photo.GetPhotoFramePreview)
			photoAuth.GET("/photos/:id/device-preview", handlers.Photo.GetPhotoDevicePreview)
			photoAuth.GET("/display/items/:id/preview", handlers.Display.GetDailyDisplayPreview)
			photoAuth.GET("/display/assets/:id/preview", handlers.Display.GetDailyDisplayAssetPreview)
			photoAuth.GET("/display/assets/:id/bin", handlers.Display.GetDailyDisplayAssetBin)
			photoAuth.GET("/display/assets/:id/header", handlers.Display.GetDailyDisplayAssetHeader)
		}

		// 以下接口需要 JWT 认证
		authorized := v1.Group("")
		authorized.Use(middleware.JWTAuth(services.Auth))
		authorized.Use(middleware.FirstLoginCheck(services.Auth))
		{
			// 系统相关（需要认证）
			authorized.GET("/system/stats", handlers.System.Stats)
			authorized.POST("/system/reset", handlers.System.Reset)
			authorized.POST("/display/preview", handlers.Display.PreviewPhotos)
			authorized.GET("/display/batch", handlers.Display.GetDailyBatch)
			authorized.GET("/display/history", handlers.Display.ListDailyBatches)
			authorized.POST("/display/batch/generate", handlers.Display.GenerateDailyBatch)
			authorized.POST("/display/batch/generate/async", handlers.Display.GenerateDailyBatchAsync)
			authorized.GET("/display/render-profiles", handlers.Display.GetRenderProfiles)

			// 照片相关
			photos := authorized.Group("/photos")
			{
				// 异步扫描
				photos.POST("/scan/async", handlers.Photo.StartScan)
				photos.POST("/rebuild/async", handlers.Photo.StartRebuild)
				photos.POST("/tasks/:id/stop", handlers.Photo.StopScanTask)
				photos.GET("/scan/task", handlers.Photo.GetScanTask)
				photos.POST("/cleanup", handlers.Photo.CleanupPhotos)
				photos.POST("/validate-path", handlers.Photo.ValidatePath)
				photos.POST("/list-directories", handlers.Photo.ListDirectories)
				photos.POST("/count-by-paths", handlers.Photo.CountPhotosByPaths)
				photos.POST("/derived-status-by-paths", handlers.Photo.CountDerivedStatusByPaths)
				photos.PATCH("/batch-status", handlers.Photo.BatchUpdateStatus)
				photos.PATCH("/batch-rotation", handlers.Photo.BatchRotate)
				photos.GET("/counts", handlers.Photo.GetPhotoCounts)
				photos.GET("/stats", handlers.Photo.GetPhotoStats)
				photos.GET("/categories", handlers.Photo.GetCategories)
				photos.GET("/tags", handlers.Photo.GetTags)
				photos.GET("", handlers.Photo.GetPhotos)
				photos.GET("/:id", handlers.Photo.GetPhotoByID)
				photos.GET("/:id/adjacent", handlers.Photo.GetAdjacentPhotos)
				photos.GET("/:id/people", handlers.People.GetPhotoPeople)
				photos.PATCH("/:id/category", handlers.Photo.UpdateCategory)
				photos.PATCH("/:id/location", handlers.Photo.SetManualLocation)
				photos.PATCH("/:id/rotation", handlers.Photo.UpdateRotation)
				photos.PATCH("/:id/orientation", handlers.Photo.UpdateRotation) // 兼容旧路由
			}

			people := authorized.Group("/people")
			{
				people.POST("/rescan-by-path", handlers.People.RescanByPath)
				people.GET("/task", handlers.People.GetTask)
				people.GET("/stats", handlers.People.GetStats)
				people.GET("/background/logs", handlers.People.GetBackgroundLogs)
				people.POST("/merge", handlers.People.MergePeople)
				people.POST("/split", handlers.People.SplitPerson)
				people.POST("/move-faces", handlers.People.MoveFaces)
				people.GET("", handlers.People.ListPeople)
				people.GET("/:id", handlers.People.GetPerson)
				people.GET("/:id/photos", handlers.People.GetPersonPhotos)
				people.GET("/:id/faces", handlers.People.GetPersonFaces)
				people.PATCH("/:id/category", handlers.People.UpdatePersonCategory)
				people.PATCH("/:id/name", handlers.People.UpdatePersonName)
				people.PATCH("/:id/avatar", handlers.People.UpdatePersonAvatar)
			}

			thumbnails := authorized.Group("/thumbnails")
			{
				thumbnails.POST("/background/start", handlers.Thumbnail.StartBackground)
				thumbnails.POST("/background/stop", handlers.Thumbnail.StopBackground)
				thumbnails.GET("/background/logs", handlers.Thumbnail.GetBackgroundLogs)
				thumbnails.GET("/task", handlers.Thumbnail.GetTask)
				thumbnails.GET("/stats", handlers.Thumbnail.GetStats)
				thumbnails.POST("/enqueue", handlers.Thumbnail.Enqueue)
				thumbnails.POST("/enqueue-by-path", handlers.Thumbnail.EnqueueByPath)
				thumbnails.POST("/generate", handlers.Thumbnail.Generate)
			}

			geocode := authorized.Group("/geocode")
			{
				geocode.POST("/background/start", handlers.Geocode.StartBackground)
				geocode.POST("/background/stop", handlers.Geocode.StopBackground)
				geocode.GET("/background/logs", handlers.Geocode.GetBackgroundLogs)
				geocode.GET("/task", handlers.Geocode.GetTask)
				geocode.GET("/stats", handlers.Geocode.GetStats)
				geocode.POST("/repair-legacy-status", handlers.Geocode.RepairLegacyStatus)
				geocode.POST("/enqueue", handlers.Geocode.Enqueue)
				geocode.POST("/enqueue-by-path", handlers.Geocode.EnqueueByPath)
				geocode.POST("/geocode", handlers.Geocode.Geocode)
				geocode.POST("/regeocode-all", handlers.Geocode.RegeocodeAll)
			}

			// AI 分析相关
			ai := authorized.Group("/ai")
			{
				// AIHandler 现在总是存在，它会自己处理服务未配置的情况
				ai.POST("/analyze", handlers.AI.Analyze)
				ai.POST("/analyze/batch", handlers.AI.AnalyzeBatch)
				ai.POST("/background/start", handlers.AI.StartBackground)
				ai.POST("/background/stop", handlers.AI.StopBackground)
				ai.GET("/background/logs", handlers.AI.GetBackgroundLogs)
				ai.GET("/progress", handlers.AI.GetProgress)
				ai.GET("/task", handlers.AI.GetTaskStatus)
				ai.GET("/runtime", handlers.AI.GetRuntimeStatus)
				ai.POST("/reanalyze/:id", handlers.AI.ReAnalyze)
				ai.GET("/provider", handlers.AI.GetProviderInfo)
			}

			// 事件聚类相关
			events := authorized.Group("/events")
			{
				events.GET("", handlers.Event.ListEvents)
				events.GET("/:id", handlers.Event.GetEvent)
				events.POST("/cluster", handlers.Event.StartClustering)
				events.POST("/rebuild", handlers.Event.StartRebuild)
				events.GET("/cluster/task", handlers.Event.GetClusteringTask)
				events.POST("/cluster/stop", handlers.Event.StopClustering)
			}

			// 配置管理相关
			configGroup := authorized.Group("/config")
			{
				configGroup.GET("", handlers.Config.ListConfigs)
				configGroup.POST("/batch", handlers.Config.SetBatchConfigs)
				configGroup.GET("/:key", handlers.Config.GetConfig)
				configGroup.PUT("/:key", handlers.Config.SetConfig)
				configGroup.DELETE("/:key", handlers.Config.DeleteConfig)
				configGroup.DELETE("/scan-paths/:id", handlers.Config.DeleteScanPath)

				// 提示词配置管理
				configGroup.GET("/prompts", handlers.Config.GetPromptConfig)
				configGroup.PUT("/prompts", handlers.Config.SetPromptConfig)
				configGroup.POST("/prompts/reset", handlers.Config.ResetPromptConfig)

				// 城市数据管理
				configGroup.POST("/cities-data/reload", handlers.Config.ReloadCitiesData)
			}
		}
	}

	return r, services
}
