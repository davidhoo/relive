package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/davidhoo/relive/pkg/geodata"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ConfigHandler 配置处理器
type ConfigHandler struct {
	service        service.ConfigService
	aiService      service.AIService
	runtimeService service.AnalysisRuntimeService
	photoService   service.PhotoService
	promptService  service.PromptService
	geocodeService service.GeocodeService
	cfg            *config.Config
	photoRepo      repository.PhotoRepository
	aiHandler      *AIHandler // 用于更新 AIHandler 的 aiService
	db             *gorm.DB
}

// NewConfigHandler 创建配置处理器
func NewConfigHandler(service service.ConfigService, aiService service.AIService, runtimeService service.AnalysisRuntimeService, photoService service.PhotoService, promptService service.PromptService, geocodeService service.GeocodeService, photoRepo repository.PhotoRepository, cfg *config.Config, db *gorm.DB) *ConfigHandler {
	return &ConfigHandler{
		service:        service,
		aiService:      aiService,
		runtimeService: runtimeService,
		photoService:   photoService,
		promptService:  promptService,
		geocodeService: geocodeService,
		photoRepo:      photoRepo,
		cfg:            cfg,
		db:             db,
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
			newAIService, err := service.NewAIService(h.photoRepo, h.cfg, h.service, h.runtimeService)
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

	// 检查是否是 Geocode 配置变更，如果是则重新加载 Geocode service
	if key == "geocode" {
		if h.geocodeService != nil {
			logger.Info("Geocode configuration changed, reloading geocode service...")
			// 将数据库中的 JSON 配置同步到内存 cfg，确保 Reload 使用最新配置
			var newGeocodeConfig config.GeocodeConfig
			if err := json.Unmarshal([]byte(req.Value), &newGeocodeConfig); err == nil {
				h.cfg.Geocode.Provider = newGeocodeConfig.Provider
				h.cfg.Geocode.Fallback = newGeocodeConfig.Fallback
				h.cfg.Geocode.CacheEnabled = newGeocodeConfig.CacheEnabled
				h.cfg.Geocode.CacheTTL = newGeocodeConfig.CacheTTL
				h.cfg.Geocode.AMapAPIKey = newGeocodeConfig.AMapAPIKey
				h.cfg.Geocode.AMapTimeout = newGeocodeConfig.AMapTimeout
				h.cfg.Geocode.NominatimEndpoint = newGeocodeConfig.NominatimEndpoint
				h.cfg.Geocode.NominatimTimeout = newGeocodeConfig.NominatimTimeout
				h.cfg.Geocode.OfflineMaxDistance = newGeocodeConfig.OfflineMaxDistance
				h.cfg.Geocode.WeiboAPIKey = newGeocodeConfig.WeiboAPIKey
				h.cfg.Geocode.WeiboTimeout = newGeocodeConfig.WeiboTimeout
				logger.Infof("Geocode config updated in memory: provider=%s, fallback=%s", newGeocodeConfig.Provider, newGeocodeConfig.Fallback)
			} else {
				logger.Warnf("Failed to parse geocode config from request: %v", err)
			}
			if err := h.geocodeService.Reload(h.db, h.cfg); err != nil {
				logger.Warnf("Failed to reload geocode service after config change: %v", err)
				c.JSON(http.StatusOK, model.Response{
					Success: true,
					Message: "Config saved, but failed to reload geocode service: " + err.Error(),
				})
				return
			}
			logger.Info("Geocode service reloaded successfully")
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
			newAIService, err := service.NewAIService(h.photoRepo, h.cfg, h.service, h.runtimeService)
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

	// 检查是否包含 Geocode 配置变更，如果是则重新加载 Geocode service
	if geocodeValue, hasGeocodeConfig := configs["geocode"]; hasGeocodeConfig {
		if h.geocodeService != nil {
			logger.Info("Geocode configuration changed, reloading geocode service...")
			// 将数据库中的 JSON 配置同步到内存 cfg，确保 Reload 使用最新配置
			var newGeocodeConfig config.GeocodeConfig
			if err := json.Unmarshal([]byte(geocodeValue), &newGeocodeConfig); err == nil {
				h.cfg.Geocode.Provider = newGeocodeConfig.Provider
				h.cfg.Geocode.Fallback = newGeocodeConfig.Fallback
				h.cfg.Geocode.CacheEnabled = newGeocodeConfig.CacheEnabled
				h.cfg.Geocode.CacheTTL = newGeocodeConfig.CacheTTL
				h.cfg.Geocode.AMapAPIKey = newGeocodeConfig.AMapAPIKey
				h.cfg.Geocode.AMapTimeout = newGeocodeConfig.AMapTimeout
				h.cfg.Geocode.NominatimEndpoint = newGeocodeConfig.NominatimEndpoint
				h.cfg.Geocode.NominatimTimeout = newGeocodeConfig.NominatimTimeout
				h.cfg.Geocode.OfflineMaxDistance = newGeocodeConfig.OfflineMaxDistance
				h.cfg.Geocode.WeiboAPIKey = newGeocodeConfig.WeiboAPIKey
				h.cfg.Geocode.WeiboTimeout = newGeocodeConfig.WeiboTimeout
				logger.Infof("Geocode config updated in memory: provider=%s, fallback=%s", newGeocodeConfig.Provider, newGeocodeConfig.Fallback)
			} else {
				logger.Warnf("Failed to parse geocode config from batch request: %v", err)
			}
			if err := h.geocodeService.Reload(h.db, h.cfg); err != nil {
				logger.Warnf("Failed to reload geocode service after config change: %v", err)
				c.JSON(http.StatusOK, model.Response{
					Success: true,
					Message: "Configs saved, but failed to reload geocode service: " + err.Error(),
				})
				return
			}
			logger.Info("Geocode service reloaded successfully")
		}
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Configs updated successfully",
	})
}

// 使用 model.ScanPathConfig 和 model.ScanPathsConfig

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

	var scanPathsConfig model.ScanPathsConfig
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
	var newPaths []model.ScanPathConfig
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

	// 删除缩略图文件
	thumbnailPath := h.cfg.Photos.ThumbnailPath
	if thumbnailPath == "" {
		thumbnailPath = "./data/thumbnails"
	}

	photos, err := h.photoService.GetPhotosByPathPrefix(targetPath)
	if err != nil {
		logger.Warnf("Failed to get photos for path %s: %v", targetPath, err)
	} else {
		for _, photo := range photos {
			if photo.ThumbnailPath == "" {
				continue
			}

			thumbnailFile := filepath.Join(thumbnailPath, photo.ThumbnailPath)
			if err := os.Remove(thumbnailFile); err != nil && !os.IsNotExist(err) {
				logger.Warnf("Failed to remove thumbnail for photo %d: %v", photo.ID, err)
			}
		}
	}

	// 删除该路径下的所有照片记录
	deletedCount, err := h.photoService.DeletePhotosByPathPrefix(targetPath)
	if err != nil {
		logger.Errorf("Failed to delete photos for path %s: %v", targetPath, err)
		// 继续执行，不中断流程
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

	if err := h.service.Delete("photos.scan_tree." + pathID); err != nil {
		logger.Warnf("Failed to delete scan tree snapshot for path %s: %v", pathID, err)
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

// GetPromptConfig 获取提示词配置
// @Summary 获取提示词配置
// @Description 获取 AI 分析的提示词配置
// @Tags Config
// @Produce json
// @Success 200 {object} model.Response{data=model.PromptConfig}
// @Failure 500 {object} model.Response
// @Router /api/v1/config/prompts [get]
func (h *ConfigHandler) GetPromptConfig(c *gin.Context) {
	config, err := h.promptService.GetPromptConfig()
	if err != nil {
		logger.Errorf("Failed to get prompt config: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "GET_PROMPT_CONFIG_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    config,
		Message: "Prompt config retrieved successfully",
	})
}

// SetPromptConfig 设置提示词配置
// @Summary 设置提示词配置
// @Description 设置或更新 AI 分析的提示词配置
// @Tags Config
// @Accept json
// @Produce json
// @Param request body model.PromptConfig true "提示词配置"
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/prompts [put]
func (h *ConfigHandler) SetPromptConfig(c *gin.Context) {
	var config model.PromptConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: err.Error(),
			},
		})
		return
	}

	if err := h.promptService.SetPromptConfig(&config); err != nil {
		logger.Errorf("Failed to set prompt config: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SET_PROMPT_CONFIG_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Prompt config updated successfully",
	})
}

// ResetPromptConfig 重置提示词配置为默认值
// @Summary 重置提示词配置
// @Description 将提示词配置重置为系统默认值
// @Tags Config
// @Produce json
// @Success 200 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/prompts/reset [post]
func (h *ConfigHandler) ResetPromptConfig(c *gin.Context) {
	if err := h.promptService.ResetToDefaults(); err != nil {
		logger.Errorf("Failed to reset prompt config: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "RESET_PROMPT_CONFIG_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	// 返回重置后的默认配置
	config, _ := h.promptService.GetPromptConfig()

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    config,
		Message: "Prompt config reset to defaults successfully",
	})
}

// ReloadCitiesData 从嵌入数据重新导入城市数据
// @Summary 重新导入城市数据
// @Description 从嵌入的城市数据重新导入到数据库（用于修复损坏的情况）
// @Tags Config
// @Produce json
// @Success 200 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/cities-data/reload [post]
func (h *ConfigHandler) ReloadCitiesData(c *gin.Context) {
	db := database.GetDB()
	if db == nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "DB_NOT_INITIALIZED",
				Message: "Database not initialized",
			},
		})
		return
	}

	logger.Info("Reloading cities data from embedded data...")
	if err := geodata.ImportEmbeddedCities(db); err != nil {
		logger.Errorf("Failed to reload cities data: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "RELOAD_FAILED",
				Message: fmt.Sprintf("Failed to reload cities data: %v", err),
			},
		})
		return
	}

	// 查询导入后的数量
	var count int64
	db.Model(&model.City{}).Count(&count)

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: fmt.Sprintf("Cities data reloaded successfully. Total %d cities in database.", count),
	})
}
