package handler

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/davidhoo/relive/pkg/version"
	"github.com/gin-gonic/gin"
)

// SystemHandler 系统处理器
type SystemHandler struct {
	systemService service.SystemService
	cfg           *config.Config
	startTime     time.Time
}

// NewSystemHandler 创建系统处理器
func NewSystemHandler(systemService service.SystemService, cfg *config.Config) *SystemHandler {
	return &SystemHandler{
		systemService: systemService,
		cfg:           cfg,
		startTime:     time.Now(),
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
	if err := h.systemService.Ping(); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "DATABASE_ERROR",
				Message: "Database ping failed",
			},
		})
		return
	}

	uptime := int64(time.Since(h.startTime).Seconds())

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: model.SystemHealthResponse{
			Status:    "healthy",
			Version:   version.Version,
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
	stats, _, err := h.systemService.GetStats()
	if err != nil {
		logger.Errorf("Failed to query stats: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "DATABASE_ERROR",
				Message: "Failed to query statistics",
			},
		})
		return
	}

	// 数据库文件大小
	stats.DatabaseSize = h.getDatabaseSize()
	stats.DatabaseUpdatedAt = h.getDatabaseUpdatedAt()

	// 运行时信息
	stats.GoVersion = runtime.Version()
	stats.Uptime = int64(time.Since(h.startTime).Seconds())
	stats.Timestamp = time.Now()

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    stats,
		Message: "Stats retrieved successfully",
	})
}

func (h *SystemHandler) getDatabaseSize() int64 {
	if h.cfg == nil {
		return 0
	}

	if strings.ToLower(strings.TrimSpace(h.cfg.Database.Type)) != "sqlite" {
		return 0
	}

	dbPath := strings.TrimSpace(h.cfg.Database.Path)
	if dbPath == "" {
		dbPath = "./data/relive.db"
	}

	dbPath = filepath.Clean(dbPath)

	var totalSize int64
	for _, path := range []string{dbPath, dbPath + "-wal"} {
		if fileInfo, err := os.Stat(path); err == nil {
			totalSize += fileInfo.Size()
		}
	}

	return totalSize
}

func (h *SystemHandler) getDatabaseUpdatedAt() *time.Time {
	if h.cfg == nil {
		return nil
	}

	if strings.ToLower(strings.TrimSpace(h.cfg.Database.Type)) != "sqlite" {
		return nil
	}

	dbPath := strings.TrimSpace(h.cfg.Database.Path)
	if dbPath == "" {
		dbPath = "./data/relive.db"
	}

	dbPath = filepath.Clean(dbPath)

	var latest time.Time
	for _, path := range []string{dbPath, dbPath + "-wal"} {
		fileInfo, err := os.Stat(path)
		if err != nil {
			continue
		}
		if fileInfo.ModTime().After(latest) {
			latest = fileInfo.ModTime()
		}
	}

	if latest.IsZero() {
		return nil
	}

	updatedAt := latest
	return &updatedAt
}

// Environment 获取系统环境信息
// @Summary 获取系统环境信息
// @Description 获取运行环境信息，包括是否在 Docker 中运行、默认路径等
// @Tags system
// @Produce json
// @Success 200 {object} model.Response{data=model.SystemEnvironmentResponse}
// @Router /api/v1/system/environment [get]
func (h *SystemHandler) Environment(c *gin.Context) {
	isDocker := checkIsDocker()

	// 获取当前工作目录
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}

	// 默认路径：Docker 中使用 /app，否则使用当前工作目录
	defaultPath := workDir
	if isDocker {
		defaultPath = "/app"
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: model.SystemEnvironmentResponse{
			IsDocker:    isDocker,
			DefaultPath: defaultPath,
			WorkDir:     workDir,
		},
		Message: "Environment info retrieved successfully",
	})
}

// checkIsDocker 检查是否在 Docker 容器中运行
func checkIsDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		if strings.Contains(string(data), "docker") {
			return true
		}
	}

	return false
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
	generatedFilesCleared := true

	if err := h.systemService.ResetSystem(); err != nil {
		logger.Errorf("Failed to reset database state: %v", err)
		response.DatabaseCleared = false
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "DATABASE_ERROR",
				Message: "Failed to reset database state",
			},
		})
		return
	}
	response.DatabaseCleared = true

	// 清除缩略图
	if h.cfg.Photos.ThumbnailPath != "" {
		if err := clearDirectoryContents(h.cfg.Photos.ThumbnailPath); err != nil {
			logger.Errorf("Failed to clear thumbnails: %v", err)
			response.ThumbnailsCleared = false
		} else {
			response.ThumbnailsCleared = true
			logger.Info("Thumbnails cleared")
		}
	}

	// 清除展示批次文件
	displayBatchPath := util.DisplayBatchRoot(h.cfg.Photos.ThumbnailPath)
	if err := clearDirectoryContents(displayBatchPath); err != nil {
		logger.Errorf("Failed to clear display batch assets: %v", err)
		generatedFilesCleared = false
	} else {
		logger.Info("Display batch assets cleared")
	}

	// 清除缓存目录
	cachePath := h.getCachePath()
	if err := clearDirectoryContents(cachePath); err != nil {
		logger.Errorf("Failed to clear cache: %v", err)
		response.CacheCleared = false
	} else {
		response.CacheCleared = true
		logger.Info("Cache cleared")
	}

	response.PasswordReset = true

	response.Success = true
	response.Message = "System has been reset to initial state. Please login with admin/admin"
	if !response.ThumbnailsCleared || !response.CacheCleared || !generatedFilesCleared {
		response.Message = "System data has been reset, but some generated files could not be removed completely"
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    response,
		Message: response.Message,
	})
}

// clearDirectoryContents 清除目录内所有文件和子目录，但保留目录本身
func clearDirectoryContents(dirPath string) error {
	if dirPath == "" {
		return nil
	}
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("read directory: %w", err)
	}

	var removeErrs []error
	for _, entry := range entries {
		fullPath := filepath.Join(dirPath, entry.Name())
		if err := os.RemoveAll(fullPath); err != nil {
			logger.Errorf("Failed to remove %s: %v", fullPath, err)
			removeErrs = append(removeErrs, fmt.Errorf("remove %s: %w", fullPath, err))
		}
	}

	return errors.Join(removeErrs...)
}

func (h *SystemHandler) getCachePath() string {
	if h.cfg == nil {
		return ""
	}

	dbPath := strings.TrimSpace(h.cfg.Database.Path)
	if dbPath == "" {
		dbPath = "./data/relive.db"
	}

	return filepath.Join(filepath.Dir(filepath.Clean(dbPath)), "cache")
}
