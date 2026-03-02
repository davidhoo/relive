package router

import (
	"time"

	"github.com/davidhoo/relive/internal/api/v1/handler"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Setup 设置路由
func Setup(db *gorm.DB, cfg *config.Config) *gin.Engine {
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
			"http://127.0.0.1:5173",
			"http://127.0.0.1:5174",
			"http://127.0.0.1:3000",
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept", "X-Requested-With"},
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
	handlers := handler.NewHandlers(db, services)

	// API 路由组
	v1 := r.Group("/api/v1")
	{
		// 系统相关
		system := v1.Group("/system")
		{
			system.GET("/health", handlers.System.Health)
			system.GET("/stats", handlers.System.Stats)
		}

		// 照片相关
		photos := v1.Group("/photos")
		{
			photos.POST("/scan", handlers.Photo.ScanPhotos)
			photos.POST("/validate-path", handlers.Photo.ValidatePath)
			photos.GET("/stats", handlers.Photo.GetPhotoStats) // 具体路径要在参数路径之前
			photos.GET("", handlers.Photo.GetPhotos)
			photos.GET("/:id", handlers.Photo.GetPhotoByID)
			photos.GET("/:id/image", handlers.Photo.GetPhotoImage)
		}

		// 展示相关
		display := v1.Group("/display")
		{
			display.GET("/photo", handlers.Display.GetDisplayPhoto)
			display.POST("/record", handlers.Display.RecordDisplay)
		}

		// ESP32 设备相关
		esp32 := v1.Group("/esp32")
		{
			esp32.POST("/register", handlers.ESP32.Register)
			esp32.POST("/heartbeat", handlers.ESP32.Heartbeat)
			esp32.GET("/stats", handlers.ESP32.GetDeviceStats) // 具体路径要在参数路径之前
			esp32.GET("/devices", handlers.ESP32.GetDevices)
			esp32.GET("/devices/:device_id", handlers.ESP32.GetDeviceByID)
		}

		// AI 分析相关
		ai := v1.Group("/ai")
		{
			if handlers.AI != nil {
				ai.POST("/analyze", handlers.AI.Analyze)
				ai.POST("/analyze/batch", handlers.AI.AnalyzeBatch)
				ai.GET("/progress", handlers.AI.GetProgress)
				ai.POST("/reanalyze/:id", handlers.AI.ReAnalyze)
				ai.GET("/provider", handlers.AI.GetProviderInfo)
			} else {
				// AI 服务未配置时，返回友好的错误信息
				aiNotAvailable := func(c *gin.Context) {
					c.JSON(503, gin.H{
						"success": false,
						"error": gin.H{
							"code":    "SERVICE_UNAVAILABLE",
							"message": "AI service is not configured or unavailable",
						},
						"message": "AI service is not available. Please check your configuration.",
					})
				}
				ai.POST("/analyze", aiNotAvailable)
				ai.POST("/analyze/batch", aiNotAvailable)
				ai.GET("/progress", aiNotAvailable)
				ai.POST("/reanalyze/:id", aiNotAvailable)
				ai.GET("/provider", aiNotAvailable)
			}
		}

		// 导出/导入相关
		v1.POST("/export", handlers.Export.Export)
		v1.POST("/import", handlers.Export.Import)
		v1.POST("/export/check", handlers.Export.CheckExport)

		// 配置管理相关
		configGroup := v1.Group("/config")
		{
			configGroup.GET("", handlers.Config.ListConfigs)           // 获取所有配置
			configGroup.POST("/batch", handlers.Config.SetBatchConfigs) // 批量设置配置
			configGroup.GET("/:key", handlers.Config.GetConfig)         // 获取配置
			configGroup.PUT("/:key", handlers.Config.SetConfig)         // 设置配置
			configGroup.DELETE("/:key", handlers.Config.DeleteConfig)   // 删除配置
		}
	}

	return r
}
