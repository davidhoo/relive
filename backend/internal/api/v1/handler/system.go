package handler

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/factoryreset"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
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
	scheduleExit  func(time.Duration)
}

// NewSystemHandler 创建系统处理器
func NewSystemHandler(systemService service.SystemService, cfg *config.Config) *SystemHandler {
	return &SystemHandler{
		systemService: systemService,
		cfg:           cfg,
		startTime:     time.Now(),
		scheduleExit: func(delay time.Duration) {
			go func() {
				time.Sleep(delay)
				os.Exit(0)
			}()
		},
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

	if err := factoryreset.Schedule(h.cfg); err != nil {
		status := http.StatusInternalServerError
		code := "RESET_SCHEDULE_FAILED"
		message := "Failed to schedule factory reset"
		if errors.Is(err, factoryreset.ErrUnsupportedDatabase) {
			status = http.StatusNotImplemented
			code = "UNSUPPORTED_DATABASE"
			message = err.Error()
		}
		c.JSON(status, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    code,
				Message: message,
			},
		})
		return
	}

	response := model.SystemResetResponse{
		Success:          true,
		Message:          "Factory reset scheduled. The service will restart and return to admin/admin.",
		RestartScheduled: true,
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    response,
		Message: response.Message,
	})

	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	if h.scheduleExit != nil {
		h.scheduleExit(500 * time.Millisecond)
	}
}
