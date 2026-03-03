package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// ConfigHandler 配置处理器
type ConfigHandler struct {
	service       service.ConfigService
	aiService     service.AIService
	photoService  service.PhotoService
	cfg           *config.Config
	photoRepo     repository.PhotoRepository
	aiHandler     *AIHandler // 用于更新 AIHandler 的 aiService
}

// NewConfigHandler 创建配置处理器
func NewConfigHandler(service service.ConfigService, aiService service.AIService, photoService service.PhotoService, photoRepo repository.PhotoRepository, cfg *config.Config) *ConfigHandler {
	return &ConfigHandler{
		service:      service,
		aiService:    aiService,
		photoService: photoService,
		photoRepo:    photoRepo,
		cfg:          cfg,
	}
}

// SetAIHandler 设置 AIHandler 引用（用于动态更新 AI 服务）
func (h *ConfigHandler) SetAIHandler(aiHandler *AIHandler) {
	h.aiHandler = aiHandler
}

// GetConfig 获取配置
// @Summary 获取配置
// @Description 根据键获取配置值
// @Tags Config
// @Produce json
// @Param key path string true "配置键"
// @Success 200 {object} model.Response{data=model.AppConfig}
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/{key} [get]
func (h *ConfigHandler) GetConfig(c *gin.Context) {
	key := c.Param("key")

	if key == "" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_KEY",
				Message: "Config key is required",
			},
		})
		return
	}

	config, err := h.service.Get(key)
	if err != nil {
		logger.Warnf("Config not found: %s", key)
		c.JSON(http.StatusNotFound, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "CONFIG_NOT_FOUND",
				Message: "Config not found",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    config,
		Message: "Config retrieved successfully",
	})
}

// SetConfig 设置配置
// @Summary 设置配置
// @Description 设置或更新配置值
// @Tags Config
// @Accept json
// @Produce json
// @Param key path string true "配置键"
// @Param request body map[string]string true "配置值" example({"value": "new_value"})
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/{key} [put]
func (h *ConfigHandler) SetConfig(c *gin.Context) {
	key := c.Param("key")

	if key == "" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_KEY",
				Message: "Config key is required",
			},
		})
		return
	}

	var req struct {
		Value string `json:"value" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: err.Error(),
			},
		})
		return
	}

	if err := h.service.Set(key, req.Value); err != nil {
		logger.Errorf("Failed to set config %s: %v", key, err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SET_CONFIG_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	// 检查是否是 AI 配置变更，如果是则重新加载 AI provider
	if key == "ai" {
		if h.aiService != nil {
			logger.Info("AI configuration changed, reloading AI provider...")
			if err := h.aiService.ReloadProvider(); err != nil {
				logger.Warnf("Failed to reload AI provider after config change: %v", err)
				// 配置已保存，但 AI provider 重载失败，返回警告信息
				c.JSON(http.StatusOK, model.Response{
					Success: true,
					Message: "Config saved, but failed to reload AI provider: " + err.Error(),
				})
				return
			}
			logger.Info("AI provider reloaded successfully")
			// 同时更新 AIHandler 中的 aiService
			if h.aiHandler != nil {
				h.aiHandler.SetAIService(h.aiService)
			}
		} else {
			// AI service 为 nil，尝试重新初始化
			logger.Info("AI service not initialized, trying to initialize...")
			newAIService, err := service.NewAIService(h.photoRepo, h.cfg, h.service)
			if err != nil {
				logger.Warnf("Failed to initialize AI service after config change: %v", err)
				c.JSON(http.StatusOK, model.Response{
					Success: true,
					Message: "Config saved, but failed to initialize AI service: " + err.Error(),
				})
				return
			}
			h.aiService = newAIService
			logger.Info("AI service initialized successfully")
			// 同时更新 AIHandler 中的 aiService
			if h.aiHandler != nil {
				h.aiHandler.SetAIService(newAIService)
			}
		}
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Config updated successfully",
	})
}

// DeleteConfig 删除配置（重置为默认值）
// @Summary 删除配置
// @Description 删除配置项，系统将使用默认值
// @Tags Config
// @Produce json
// @Param key path string true "配置键"
// @Success 200 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/{key} [delete]
func (h *ConfigHandler) DeleteConfig(c *gin.Context) {
	key := c.Param("key")

	if key == "" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_KEY",
				Message: "Config key is required",
			},
		})
		return
	}

	if err := h.service.Delete(key); err != nil {
		logger.Errorf("Failed to delete config %s: %v", key, err)
		c.JSON(http.StatusNotFound, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "DELETE_CONFIG_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Config deleted successfully",
	})
}

// ListConfigs 获取所有配置
// @Summary 获取所有配置
// @Description 获取系统中的所有配置项
// @Tags Config
// @Produce json
// @Success 200 {object} model.Response{data=[]model.AppConfig}
// @Failure 500 {object} model.Response
// @Router /api/v1/config [get]
func (h *ConfigHandler) ListConfigs(c *gin.Context) {
	configs, err := h.service.List()
	if err != nil {
		logger.Errorf("Failed to list configs: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "LIST_CONFIGS_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    configs,
		Message: "Configs retrieved successfully",
	})
}

// SetBatchConfigs 批量设置配置
// @Summary 批量设置配置
// @Description 批量设置多个配置项
// @Tags Config
// @Accept json
// @Produce json
// @Param request body map[string]string true "配置键值对" example({"display.algorithm": "on_this_day", "display.refresh_interval": "3600"})
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/batch [post]
func (h *ConfigHandler) SetBatchConfigs(c *gin.Context) {
	var configs map[string]string

	if err := c.ShouldBindJSON(&configs); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: err.Error(),
			},
		})
		return
	}

	if len(configs) == 0 {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "EMPTY_CONFIGS",
				Message: "No configs provided",
			},
		})
		return
	}

	if err := h.service.SetBatch(configs); err != nil {
		logger.Errorf("Failed to set batch configs: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SET_BATCH_CONFIGS_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	// 检查是否包含 AI 配置变更，如果是则重新加载 AI provider
	if _, hasAIConfig := configs["ai"]; hasAIConfig {
		if h.aiService != nil {
			logger.Info("AI configuration changed, reloading AI provider...")
			if err := h.aiService.ReloadProvider(); err != nil {
				logger.Warnf("Failed to reload AI provider after config change: %v", err)
				// 配置已保存，但 AI provider 重载失败，返回警告信息
				c.JSON(http.StatusOK, model.Response{
					Success: true,
					Message: "Configs saved, but failed to reload AI provider: " + err.Error(),
				})
				return
			}
			logger.Info("AI provider reloaded successfully")
			// 同时更新 AIHandler 中的 aiService
			if h.aiHandler != nil {
				h.aiHandler.SetAIService(h.aiService)
			}
		} else {
			// AI service 为 nil，尝试重新初始化
			logger.Info("AI service not initialized, trying to initialize...")
			newAIService, err := service.NewAIService(h.photoRepo, h.cfg, h.service)
			if err != nil {
				logger.Warnf("Failed to initialize AI service after config change: %v", err)
				c.JSON(http.StatusOK, model.Response{
					Success: true,
					Message: "Configs saved, but failed to initialize AI service: " + err.Error(),
				})
				return
			}
			h.aiService = newAIService
			logger.Info("AI service initialized successfully")
			// 同时更新 AIHandler 中的 aiService
			if h.aiHandler != nil {
				h.aiHandler.SetAIService(newAIService)
			}
		}
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Configs updated successfully",
	})
}

// ScanPathConfig 扫描路径配置（用于解析）
type ScanPathConfig struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	IsDefault     bool   `json:"is_default"`
	Enabled       bool   `json:"enabled"`
	CreatedAt     string `json:"created_at"`
	LastScannedAt string `json:"last_scanned_at,omitempty"`
}

// ScanPathsConfig 扫描路径配置集合
type ScanPathsConfig struct {
	Paths []ScanPathConfig `json:"paths"`
}

// DeleteScanPath 删除扫描路径及其关联数据
// @Summary 删除扫描路径及其关联数据
// @Description 删除指定的扫描路径配置，同时删除该路径下所有照片的数据库记录和缩略图文件
// @Tags Config
// @Produce json
// @Param id path string true "路径 ID"
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/scan-paths/{id} [delete]
func (h *ConfigHandler) DeleteScanPath(c *gin.Context) {
	pathID := c.Param("id")
	if pathID == "" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_ID",
				Message: "Path ID is required",
			},
		})
		return
	}

	// 获取当前扫描路径配置
	configValue, err := h.service.GetWithDefault("photos.scan_paths", "")
	if err != nil {
		logger.Errorf("Failed to get scan paths config: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "GET_CONFIG_FAILED",
				Message: "Failed to get scan paths configuration",
			},
		})
		return
	}

	var scanPathsConfig ScanPathsConfig
	if err := json.Unmarshal([]byte(configValue), &scanPathsConfig); err != nil {
		logger.Errorf("Failed to parse scan paths config: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "PARSE_CONFIG_FAILED",
				Message: "Failed to parse scan paths configuration",
			},
		})
		return
	}

	// 查找要删除的路径
	var targetPath string
	var newPaths []ScanPathConfig
	found := false
	for _, path := range scanPathsConfig.Paths {
		if path.ID == pathID {
			targetPath = path.Path
			found = true
			continue
		}
		newPaths = append(newPaths, path)
	}

	if !found {
		c.JSON(http.StatusNotFound, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "PATH_NOT_FOUND",
				Message: "Scan path not found",
			},
		})
		return
	}

	// 删除该路径下的所有照片记录
	deletedCount, err := h.photoService.DeletePhotosByPathPrefix(targetPath)
	if err != nil {
		logger.Errorf("Failed to delete photos for path %s: %v", targetPath, err)
		// 继续执行，不中断流程
	}

	// 删除缩略图文件
	thumbnailPath := h.cfg.Photos.ThumbnailPath
	if thumbnailPath == "" {
		thumbnailPath = "./data/thumbnails"
	}

	// 遍历缩略图目录，删除与该路径相关的缩略图
	// 由于缩略图是以 photo ID 命名的，我们需要先获取该路径下的所有照片 ID
	// 然后通过 photoService 来获取这些 ID
	photoIDs, err := h.photoService.GetPhotoIDsByPathPrefix(targetPath)
	if err != nil {
		logger.Warnf("Failed to get photo IDs for path %s: %v", targetPath, err)
	} else {
		for _, id := range photoIDs {
			// 删除缩略图文件
			hexStr := fmt.Sprintf("%04x", id)
			subDir1 := hexStr[0:2]
			subDir2 := hexStr[2:4]
			thumbnailFile := filepath.Join(thumbnailPath, subDir1, subDir2, strconv.FormatUint(uint64(id), 10)+".jpg")
			if err := os.Remove(thumbnailFile); err != nil && !os.IsNotExist(err) {
				logger.Warnf("Failed to remove thumbnail for photo %d: %v", id, err)
			}
		}
	}

	// 更新扫描路径配置
	scanPathsConfig.Paths = newPaths
	newConfigValue, err := json.Marshal(scanPathsConfig)
	if err != nil {
		logger.Errorf("Failed to marshal scan paths config: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "MARSHAL_CONFIG_FAILED",
				Message: "Failed to serialize scan paths configuration",
			},
		})
		return
	}

	if err := h.service.Set("photos.scan_paths", string(newConfigValue)); err != nil {
		logger.Errorf("Failed to save scan paths config: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SAVE_CONFIG_FAILED",
				Message: "Failed to save scan paths configuration",
			},
		})
		return
	}

	message := "Scan path deleted successfully"
	if deletedCount > 0 {
		message = fmt.Sprintf("Scan path deleted successfully. Removed %d photos and their thumbnails.", deletedCount)
	}

	logger.Infof("Scan path %s (%s) deleted. Removed %d photos.", pathID, targetPath, deletedCount)
	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: message,
	})
}
