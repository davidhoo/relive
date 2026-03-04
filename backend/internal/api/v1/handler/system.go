package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// SystemHandler 系统处理器
type SystemHandler struct {
	db        *gorm.DB
	cfg       *config.Config
	startTime time.Time
}

// NewSystemHandler 创建系统处理器
func NewSystemHandler(db *gorm.DB, cfg *config.Config, services interface{}) *SystemHandler {
	return &SystemHandler{
		db:        db,
		cfg:       cfg,
		startTime: time.Now(),
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
			Status:    "healthy",
			Version:   "1.0.0",
			Uptime:    uptime,
			Timestamp: time.Now(),
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

	// 统计存储空间（所有照片文件大小之和）
	var totalSize int64
	h.db.Model(&model.Photo{}).Select("COALESCE(SUM(file_size), 0)").Scan(&totalSize)
	stats.StorageSize = totalSize

	// 获取数据库文件大小
	dbPath := "./data/relive.db"
	if fileInfo, err := os.Stat(dbPath); err == nil {
		stats.DatabaseSize = fileInfo.Size()
	}

	// 获取 Go 版本
	stats.GoVersion = runtime.Version()

	// 获取运行时长
	stats.Uptime = int64(time.Since(h.startTime).Seconds())

	// 统计时间
	stats.Timestamp = time.Now()

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    stats,
		Message: "Stats retrieved successfully",
	})
}

// Reset 系统还原
// @Summary 系统还原
// @Description 清除所有数据，将系统还原到初始化状态（需要管理员权限）
// @Tags system
// @Accept json
// @Produce json
// @Param request body model.SystemResetRequest true "还原请求"
// @Success 200 {object} model.Response{data=model.SystemResetResponse}
// @Router /api/v1/system/reset [post]
func (h *SystemHandler) Reset(c *gin.Context) {
	var req model.SystemResetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "Invalid request: " + err.Error(),
			},
		})
		return
	}

	// 验证确认文本
	if req.ConfirmText != "RESET" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_CONFIRMATION",
				Message: "Confirmation text must be 'RESET'",
			},
		})
		return
	}

	response := model.SystemResetResponse{
		Success: true,
	}

	// 1. 清除数据库表数据（保留 cities 表，因为城市数据是离线地理编码的基础）
	tables := []string{"display_records", "esp32_devices", "photos", "app_config"}
	for _, table := range tables {
		if err := h.db.Exec(fmt.Sprintf("DELETE FROM %s", table)).Error; err != nil {
			logger.Errorf("Failed to clear table %s: %v", table, err)
			response.DatabaseCleared = false
			c.JSON(http.StatusInternalServerError, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "DATABASE_ERROR",
					Message: fmt.Sprintf("Failed to clear table %s", table),
				},
			})
			return
		}
		logger.Infof("Table %s cleared", table)
	}
	response.DatabaseCleared = true

	// 2. 清除缩略图
	if h.cfg.Photos.ThumbnailPath != "" {
		if err := h.clearThumbnails(h.cfg.Photos.ThumbnailPath); err != nil {
			logger.Errorf("Failed to clear thumbnails: %v", err)
			response.ThumbnailsCleared = false
		} else {
			response.ThumbnailsCleared = true
			logger.Info("Thumbnails cleared")
		}
	}

	// 3. 清除缓存目录（如果有）
	cachePath := filepath.Join(h.cfg.Database.Path, "..", "cache")
	if err := h.clearCache(cachePath); err != nil {
		logger.Errorf("Failed to clear cache: %v", err)
		response.CacheCleared = false
	} else {
		response.CacheCleared = true
		logger.Info("Cache cleared")
	}

	// 4. 重置用户密码为 admin/admin
	if err := h.resetUserPassword(); err != nil {
		logger.Errorf("Failed to reset user password: %v", err)
		response.PasswordReset = false
	} else {
		response.PasswordReset = true
		logger.Info("User password reset to admin/admin")
	}

	response.Success = true
	response.Message = "System has been reset to initial state. Please login with admin/admin"

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    response,
		Message: response.Message,
	})
}

// clearThumbnails 清除缩略图目录
func (h *SystemHandler) clearThumbnails(thumbnailPath string) error {
	// 检查目录是否存在
	if _, err := os.Stat(thumbnailPath); os.IsNotExist(err) {
		// 目录不存在，无需清理
		return nil
	}

	// 读取目录内容
	entries, err := os.ReadDir(thumbnailPath)
	if err != nil {
		return fmt.Errorf("read thumbnail directory: %w", err)
	}

	// 删除所有子目录和文件
	for _, entry := range entries {
		fullPath := filepath.Join(thumbnailPath, entry.Name())
		if err := os.RemoveAll(fullPath); err != nil {
			logger.Errorf("Failed to remove %s: %v", fullPath, err)
			// 继续删除其他文件
		}
	}

	return nil
}

// clearCache 清除缓存目录
func (h *SystemHandler) clearCache(cachePath string) error {
	// 检查目录是否存在
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		// 目录不存在，无需清理
		return nil
	}

	// 读取目录内容
	entries, err := os.ReadDir(cachePath)
	if err != nil {
		return fmt.Errorf("read cache directory: %w", err)
	}

	// 删除所有子目录和文件
	for _, entry := range entries {
		fullPath := filepath.Join(cachePath, entry.Name())
		if err := os.RemoveAll(fullPath); err != nil {
			logger.Errorf("Failed to remove %s: %v", fullPath, err)
			// 继续删除其他文件
		}
	}

	return nil
}

// resetUserPassword 重置用户密码为 admin/admin
func (h *SystemHandler) resetUserPassword() error {
	// 生成默认密码哈希（admin）
	PasswordHash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash default Password: %w", err)
	}

	// 删除所有现有用户
	if err := h.db.Exec("DELETE FROM users").Error; err != nil {
		return fmt.Errorf("failed to clear users table: %w", err)
	}

	// 创建新的默认用户
	defaultUser := &model.User{
		Username:     "admin",
		PasswordHash: string(PasswordHash),
		IsFirstLogin: true,
	}

	if err := h.db.Create(defaultUser).Error; err != nil {
		return fmt.Errorf("failed to create default user: %w", err)
	}

	return nil
}
