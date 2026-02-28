package handler

import (
	"net/http"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SystemHandler 系统处理器
type SystemHandler struct {
	db        *gorm.DB
	cfg       *config.Config
	startTime time.Time
}

// NewSystemHandler 创建系统处理器
func NewSystemHandler(db *gorm.DB, cfg *config.Config, startTime time.Time) *SystemHandler {
	return &SystemHandler{
		db:        db,
		cfg:       cfg,
		startTime: startTime,
	}
}

// Health 健康检查
// @Summary 健康检查
// @Description 检查系统健康状态
// @Tags system
// @Produce json
// @Success 200 {object} model.Response{data=model.SystemHealthResponse}
// @Router /api/v1/system/health [get]
func (h *SystemHandler) Health(c *gin.Context) {
	// 检查数据库连接
	sqlDB, err := h.db.DB()
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "DATABASE_ERROR",
				Message: "Failed to get database connection",
			},
		})
		return
	}

	if err := sqlDB.Ping(); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "DATABASE_ERROR",
				Message: "Database ping failed",
			},
		})
		return
	}

	// 计算运行时间
	uptime := int64(time.Since(h.startTime).Seconds())

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: model.SystemHealthResponse{
			Status:  "healthy",
			Version: "0.1.0", // TODO: 从编译时注入
			Uptime:  uptime,
			Time:    time.Now(),
		},
		Message: "System is healthy",
	})
}

// Stats 系统统计
// @Summary 系统统计
// @Description 获取系统统计信息
// @Tags system
// @Produce json
// @Success 200 {object} model.Response{data=model.SystemStatsResponse}
// @Router /api/v1/system/stats [get]
func (h *SystemHandler) Stats(c *gin.Context) {
	var stats model.SystemStatsResponse

	// 统计照片总数
	h.db.Model(&model.Photo{}).Count(&stats.TotalPhotos)

	// 统计已分析照片
	h.db.Model(&model.Photo{}).Where("ai_analyzed = ?", true).Count(&stats.AnalyzedPhotos)

	// 统计未分析照片
	stats.UnanalyzedPhotos = stats.TotalPhotos - stats.AnalyzedPhotos

	// 统计设备总数
	h.db.Model(&model.ESP32Device{}).Count(&stats.TotalDevices)

	// 统计在线设备（5分钟内有心跳）
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)
	h.db.Model(&model.ESP32Device{}).
		Where("last_heartbeat > ?", fiveMinutesAgo).
		Count(&stats.OnlineDevices)

	// 统计展示记录总数
	h.db.Model(&model.DisplayRecord{}).Count(&stats.TotalDisplays)

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    stats,
		Message: "Stats retrieved successfully",
	})
}
