package router

import (
	"time"

	"github.com/davidhoo/relive/internal/api/v1/handler"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var startTime = time.Now()

// Setup 设置路由
func Setup(db *gorm.DB, cfg *config.Config) *gin.Engine {
	r := gin.New()

	// 中间件
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	// TODO: 添加 CORS、认证等中间件

	// 初始化处理器
	systemHandler := handler.NewSystemHandler(db, cfg, startTime)

	// API 路由组
	v1 := r.Group("/api/v1")
	{
		// 系统相关
		system := v1.Group("/system")
		{
			system.GET("/health", systemHandler.Health)
			system.GET("/stats", systemHandler.Stats)
		}

		// TODO: 其他路由组
		// photos := v1.Group("/photos")
		// ai := v1.Group("/ai")
		// display := v1.Group("/display")
		// esp32 := v1.Group("/esp32")
		// export := v1.Group("/export")
		// config := v1.Group("/config")
	}

	return r
}
