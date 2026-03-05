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

	// CORS 中间件配置（仅开发环境使用，生产环境建议使用反向代理）
	corsConfig := cors.Config{
		AllowOrigins: []string{
			"http://localhost:5173",  // Vite 默认开发服务器
			"http://127.0.0.1:5173",  // IP 形式
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept", "X-Requested-With", "X-API-Key"},
		ExposeHeaders:    []string{"Content-Length", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
	r.Use(cors.New(corsConfig))

	// 提供前端静态文件（单镜像部署）
	// 在生产环境中，前端文件在 /app/frontend/dist
	// 在开发环境中，前端由 Vite 独立提供
	if cfg.Server.StaticPath != "" {
		r.Static("/assets", cfg.Server.StaticPath+"/assets")
		r.StaticFile("/", cfg.Server.StaticPath+"/index.html")
		r.StaticFile("/favicon.ico", cfg.Server.StaticPath+"/favicon.ico")
		// SPA fallback - 所有非 API 路径都返回 index.html
		r.NoRoute(func(c *gin.Context) {
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
			system.GET("/environment", handlers.System.Environment)
		}

		// 设备管理（JWT 认证 - 管理员操作）
		devicesManage := v1.Group("/devices")
		devicesManage.Use(middleware.JWTAuth(services.Auth))
		devicesManage.Use(middleware.FirstLoginCheck(services.Auth))
		{
			devicesManage.POST("", handlers.Device.CreateDevice)             // 创建设备
			devicesManage.DELETE("/:id", handlers.Device.DeleteDevice)       // 删除设备
			devicesManage.PUT("/:id/enabled", handlers.Device.UpdateDeviceEnabled) // 启用/禁用设备
			devicesManage.GET("/stats", handlers.Device.GetDeviceStats)
			devicesManage.GET("", handlers.Device.GetDevices)
			devicesManage.GET("/:device_id", handlers.Device.GetDeviceByID)
		}

		// 设备 API（API Key 认证 - 设备使用）
		// 认证中间件会自动更新设备的 IP 和最后请求时间
		devicesAPI := v1.Group("/devices")
		devicesAPI.Use(middleware.APIKeyAuth(services.Device))
		{
			devicesAPI.POST("/activate", handlers.Device.Activate)    // 设备激活
		}

		// ESP32 兼容路径（API Key 认证）
		esp32 := v1.Group("/esp32")
		esp32.Use(middleware.APIKeyAuth(services.Device))
		{
			esp32.POST("/activate", handlers.ESP32.Activate)      // 兼容：指向 Activate
		}

		// 展示相关（API Key 认证，设备获取照片）
		display := v1.Group("/display")
		display.Use(middleware.APIKeyAuth(services.Device))
		{
			display.GET("/photo", handlers.Display.GetDisplayPhoto)
			display.POST("/record", handlers.Display.RecordDisplay)
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
			authorized.POST("/system/reset", handlers.System.Reset)

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
				configGroup.GET("/cities-data/status", handlers.Config.GetCitiesDataStatus)
				configGroup.POST("/cities-data/download", handlers.Config.DownloadCitiesData)
			}
		}
	}

	return r, services
}
