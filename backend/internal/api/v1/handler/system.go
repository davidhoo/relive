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
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/davidhoo/relive/pkg/version"
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
	var stats model.SystemStatsResponse

	// 统计照片总数
	h.db.Model(&model.Photo{}).Where("status = ?", model.PhotoStatusActive).Count(&stats.TotalPhotos)

	// 统计已分析照片
	h.db.Model(&model.Photo{}).Where("status = ? AND ai_analyzed = ?", model.PhotoStatusActive, true).Count(&stats.AnalyzedPhotos)

	// 统计未分析照片
	stats.UnanalyzedPhotos = stats.TotalPhotos - stats.AnalyzedPhotos

	// 统计设备总数
	h.db.Model(&model.Device{}).Count(&stats.TotalDevices)

	// 统计在线设备（5分钟内有最近活跃记录）
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)
	h.db.Model(&model.Device{}).
		Where("last_seen > ?", fiveMinutesAgo).
		Count(&stats.OnlineDevices)

	// 统计展示记录总数
	h.db.Model(&model.DisplayRecord{}).Count(&stats.TotalDisplays)

	// 统计存储空间（所有照片文件大小之和）
	var totalSize int64
	h.db.Model(&model.Photo{}).Where("status = ?", model.PhotoStatusActive).Select("COALESCE(SUM(file_size), 0)").Scan(&totalSize)
	stats.StorageSize = totalSize

	// 获取数据库文件大小
	stats.DatabaseSize = h.getDatabaseSize()
	stats.DatabaseUpdatedAt = h.getDatabaseUpdatedAt()

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
	// 方法1: 检查 /.dockerenv 文件是否存在
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// 方法2: 检查 /proc/1/cgroup 是否包含 "docker"
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
	generatedFilesCleared := true

	if err := h.resetDatabaseState(); err != nil {
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

	// 2. 清除缩略图
	if h.cfg.Photos.ThumbnailPath != "" {
		if err := h.clearDirectoryContents(h.cfg.Photos.ThumbnailPath); err != nil {
			logger.Errorf("Failed to clear thumbnails: %v", err)
			response.ThumbnailsCleared = false
		} else {
			response.ThumbnailsCleared = true
			logger.Info("Thumbnails cleared")
		}
	}

	// 3. 清除展示批次文件
	displayBatchPath := util.DisplayBatchRoot(h.cfg.Photos.ThumbnailPath)
	if err := h.clearDirectoryContents(displayBatchPath); err != nil {
		logger.Errorf("Failed to clear display batch assets: %v", err)
		generatedFilesCleared = false
	} else {
		logger.Info("Display batch assets cleared")
	}

	// 4. 清除缓存目录（如果有）
	cachePath := h.getCachePath()
	if err := h.clearDirectoryContents(cachePath); err != nil {
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

func (h *SystemHandler) resetDatabaseState() error {
	return h.db.Transaction(func(tx *gorm.DB) error {
		for _, table := range h.resettableNames() {
			exists, err := h.tableExists(tx, table)
			if err != nil {
				return fmt.Errorf("check table %s exists: %w", table, err)
			}
			if !exists {
				continue
			}

			if err := tx.Exec(fmt.Sprintf("DELETE FROM %s", table)).Error; err != nil {
				return fmt.Errorf("clear table %s: %w", table, err)
			}
			logger.Infof("Table %s cleared", table)
		}

		if err := h.resetSQLiteSequences(tx); err != nil {
			return err
		}

		if err := h.resetUserPasswordTx(tx); err != nil {
			return err
		}

		return nil
	})
}

func (h *SystemHandler) resettableNames() []string {
	return []string{
		"result_queue",
		"display_records",
		"daily_display_assets",
		"daily_display_items",
		"device_playback_states",
		"daily_display_batches",
		"analysis_runtime_leases",
		"devices",
		"photos",
		"app_config",
		"api_keys",
	}
}

func (h *SystemHandler) tableExists(tx *gorm.DB, table string) (bool, error) {
	var count int64
	typeCheckQuery := "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?"
	if err := tx.Raw(typeCheckQuery, table).Scan(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (h *SystemHandler) resetSQLiteSequences(tx *gorm.DB) error {
	if h.cfg == nil || strings.ToLower(strings.TrimSpace(h.cfg.Database.Type)) != "sqlite" {
		return nil
	}

	exists, err := h.tableExists(tx, "sqlite_sequence")
	if err != nil || !exists {
		return err
	}

	tableNames := append(h.resettableNames(), "users")
	placeholders := make([]string, 0, len(tableNames))
	args := make([]interface{}, 0, len(tableNames))
	for _, tableName := range tableNames {
		placeholders = append(placeholders, "?")
		args = append(args, tableName)
	}

	query := fmt.Sprintf("DELETE FROM sqlite_sequence WHERE name IN (%s)", strings.Join(placeholders, ","))
	if err := tx.Exec(query, args...).Error; err != nil {
		return fmt.Errorf("reset sqlite sequences: %w", err)
	}

	return nil
}

// clearDirectoryContents 清除目录内所有文件和子目录，但保留目录本身
func (h *SystemHandler) clearDirectoryContents(dirPath string) error {
	// 检查目录是否存在
	if dirPath == "" {
		return nil
	}
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		// 目录不存在，无需清理
		return nil
	}

	// 读取目录内容
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("read directory: %w", err)
	}

	var removeErrs []error
	// 删除所有子目录和文件
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

// resetUserPasswordTx 重置用户密码为 admin/admin
func (h *SystemHandler) resetUserPasswordTx(tx *gorm.DB) error {
	// 生成默认密码哈希（admin）
	PasswordHash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash default Password: %w", err)
	}

	// 删除所有现有用户
	if err := tx.Exec("DELETE FROM users").Error; err != nil {
		return fmt.Errorf("failed to clear users table: %w", err)
	}

	// 创建新的默认用户
	defaultUser := &model.User{
		Username:     "admin",
		PasswordHash: string(PasswordHash),
		IsFirstLogin: true,
	}

	if err := tx.Create(defaultUser).Error; err != nil {
		return fmt.Errorf("failed to create default user: %w", err)
	}

	logger.Info("User password reset to admin/admin")

	return nil
}
