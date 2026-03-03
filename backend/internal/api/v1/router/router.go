package router

import (
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

	// CORS 中间件配置
	corsConfig := cors.Config{
		AllowOrigins: []string{
			"http://localhost:5173",
			"http://localhost:5174",
			"http://localhost:3000",
			"http://localhost:8888",
			"http://127.0.0.1:5173",
			"http://127.0.0.1:5174",
			"http://127.0.0.1:3000",
			"http://127.0.0.1:8888",
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept", "X-Requested-With", "X-API-Key"},
		ExposeHeaders:    []string{"Content-Length", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
	r.Use(cors.New(corsConfig))

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
			auth.POST("/login", handlers.Auth.Login)
			auth.POST("/logout", handlers.Auth.Logout)
			// 以下接口需要 JWT 认证，但不需要检查首次登录
			auth.POST("/change-Password", middleware.JWTAuth(services.Auth), handlers.Auth.ChangePassword)
			auth.GET("/user", middleware.JWTAuth(services.Auth), handlers.Auth.GetUserInfo)
		}

		// 系统相关（公开接口）
		system := v1.Group("/system")
		{
			system.GET("/health", handlers.System.Health)
		}

		// ESP32 设备相关（API Key 认证）
		esp32 := v1.Group("/esp32")
		esp32.Use(middleware.APIKeyAuth(services.APIKey))
		{
			esp32.POST("/register", handlers.ESP32.Register)
			esp32.POST("/heartbeat", handlers.ESP32.Heartbeat)
		}

		// 展示相关（API Key 认证，设备获取照片）
		display := v1.Group("/display")
		display.Use(middleware.APIKeyAuth(services.APIKey))
		{
			display.GET("/photo", handlers.Display.GetDisplayPhoto)
			display.POST("/record", handlers.Display.RecordDisplay)
		}

		// 分析器相关（API Key 认证，离线分析器使用）
		analyzer := v1.Group("/analyzer")
		analyzer.Use(middleware.APIKeyAuth(services.APIKey))
		{
			analyzer.GET("/tasks", handlers.Analyzer.GetTasks)
			analyzer.POST("/tasks/:task_id/heartbeat", handlers.Analyzer.Heartbeat)
			analyzer.POST("/tasks/:task_id/release", handlers.Analyzer.ReleaseTask)
			analyzer.POST("/results", handlers.Analyzer.SubmitResults)
			analyzer.GET("/stats", handlers.Analyzer.GetStats)
			analyzer.POST("/clean-locks", handlers.Analyzer.CleanExpiredLocks)
		}

		// 图片访问（公开访问，不需要认证）
		v1.GET("/photos/:id/image", handlers.Photo.GetPhotoImage)
		v1.GET("/photos/:id/thumbnail", handlers.Photo.GetPhotoThumbnail)

		// 以下接口需要 JWT 认证
		authorized := v1.Group("")
		authorized.Use(middleware.JWTAuth(services.Auth))
		authorized.Use(middleware.FirstLoginCheck(services.Auth))
		{
			// 系统相关（需要认证）
			authorized.GET("/system/stats", handlers.System.Stats)

			// 照片相关
			photos := authorized.Group("/photos")
			{
				// 异步扫描（推荐）
				photos.POST("/scan/async", handlers.Photo.StartScan)
				photos.POST("/rebuild/async", handlers.Photo.StartRebuild)
				photos.GET("/scan/task", handlers.Photo.GetScanTask)
				// 同步扫描（已弃用，保留兼容）
				photos.POST("/scan", handlers.Photo.ScanPhotos)
				photos.POST("/rebuild", handlers.Photo.RebuildPhotos)
				photos.POST("/cleanup", handlers.Photo.CleanupPhotos)
				photos.POST("/validate-path", handlers.Photo.ValidatePath)
				photos.POST("/list-directories", handlers.Photo.ListDirectories)
				photos.POST("/count-by-paths", handlers.Photo.CountPhotosByPaths)
				photos.GET("/stats", handlers.Photo.GetPhotoStats)
				photos.GET("/categories", handlers.Photo.GetCategories)
				photos.GET("/tags", handlers.Photo.GetTags)
				photos.GET("", handlers.Photo.GetPhotos)
				photos.GET("/:id", handlers.Photo.GetPhotoByID)
			}

			// ESP32 设备管理（需要 JWT 认证）
			esp32Manage := authorized.Group("/esp32")
			{
				esp32Manage.GET("/stats", handlers.ESP32.GetDeviceStats)
				esp32Manage.GET("/devices", handlers.ESP32.GetDevices)
				esp32Manage.GET("/devices/:device_id", handlers.ESP32.GetDeviceByID)
			}

			// AI 分析相关
			ai := authorized.Group("/ai")
			{
				// AIHandler 现在总是存在，它会自己处理服务未配置的情况
				ai.POST("/analyze", handlers.AI.Analyze)
				ai.POST("/analyze/batch", handlers.AI.AnalyzeBatch)
				ai.GET("/progress", handlers.AI.GetProgress)
				ai.GET("/task", handlers.AI.GetTaskStatus)
				ai.POST("/reanalyze/:id", handlers.AI.ReAnalyze)
				ai.GET("/provider", handlers.AI.GetProviderInfo)
			}

			// 导出/导入相关
			authorized.POST("/export", handlers.Export.Export)
			authorized.POST("/import", handlers.Export.Import)
			authorized.POST("/export/check", handlers.Export.CheckExport)

			// 配置管理相关
			configGroup := authorized.Group("/config")
			{
				configGroup.GET("", handlers.Config.ListConfigs)
				configGroup.POST("/batch", handlers.Config.SetBatchConfigs)
				configGroup.GET("/:key", handlers.Config.GetConfig)
				configGroup.PUT("/:key", handlers.Config.SetConfig)
				configGroup.DELETE("/:key", handlers.Config.DeleteConfig)
				configGroup.DELETE("/scan-paths/:id", handlers.Config.DeleteScanPath)

				// API Key 管理
				configGroup.GET("/api-keys", handlers.APIKey.GetAPIKeys)
				configGroup.POST("/api-keys", handlers.APIKey.CreateAPIKey)
				configGroup.PUT("/api-keys/:id", handlers.APIKey.UpdateAPIKey)
				configGroup.DELETE("/api-keys/:id", handlers.APIKey.DeleteAPIKey)
				configGroup.POST("/api-keys/:id/regenerate", handlers.APIKey.RegenerateAPIKey)
			}
		}
	}

	return r, services
}
