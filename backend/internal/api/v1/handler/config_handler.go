package handler

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
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

// CitiesDataStatus 城市数据状态
type CitiesDataStatus struct {
	Exists      bool   `json:"exists"`
	FilePath    string `json:"file_path"`
	FileSize    int64  `json:"file_size,omitempty"`
	CityCount   int    `json:"city_count,omitempty"`
	HasZHNames  bool   `json:"has_zh_names"`
	DownloadURL string `json:"download_url"`
}

// GetCitiesDataStatus 获取城市数据状态
// @Summary 获取城市数据状态
// @Description 检查离线地理编码城市数据是否存在以及数据库中城市数量
// @Tags Config
// @Produce json
// @Success 200 {object} model.Response{data=CitiesDataStatus}
// @Router /api/v1/config/cities-data/status [get]
func (h *ConfigHandler) GetCitiesDataStatus(c *gin.Context) {
	// 使用数据库路径作为数据目录
	dataDir := filepath.Dir(h.cfg.Database.Path)
	if dataDir == "" || dataDir == "." {
		dataDir = "./data"
	}

	// 检查 cities500.txt 文件
	citiesFile := filepath.Join(dataDir, "cities500.txt")
	status := CitiesDataStatus{
		FilePath:    citiesFile,
		DownloadURL: "https://download.geonames.org/export/dump/cities500.zip",
	}

	// 检查文件是否存在
	fileExists := false
	if info, err := os.Stat(citiesFile); err == nil {
		fileExists = true
		status.FileSize = info.Size()
	}

	// 查询数据库中的城市数量
	db := database.GetDB()
	if db != nil {
		var count int64
		if err := db.Model(&model.City{}).Count(&count).Error; err == nil {
			status.CityCount = int(count)
		}
		// 检查是否有中文名数据
		var zhCount int64
		if err := db.Model(&model.City{}).Where("name_zh != '' AND name_zh IS NOT NULL").Count(&zhCount).Error; err == nil {
			status.HasZHNames = zhCount > 0
		}
	}

	// 文件存在或数据库有数据都认为已就绪
	status.Exists = fileExists || status.CityCount > 0

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    status,
	})
}

// DownloadCitiesData 下载城市数据
// @Summary 下载城市数据
// @Description 下载并解压 cities500.zip 城市数据
// @Tags Config
// @Produce json
// @Success 200 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/cities-data/download [post]
func (h *ConfigHandler) DownloadCitiesData(c *gin.Context) {
	// 使用数据库路径作为数据目录
	dataDir := filepath.Dir(h.cfg.Database.Path)
	if dataDir == "" || dataDir == "." {
		dataDir = "./data"
	}

	citiesFile := filepath.Join(dataDir, "cities500.txt")
	zipFile := filepath.Join(dataDir, "cities500.zip")

	// 确保目录存在
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logger.Errorf("Failed to create data directory: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "CREATE_DIR_FAILED",
				Message: fmt.Sprintf("Failed to create data directory: %v", err),
			},
		})
		return
	}

	// 下载文件
	logger.Info("Downloading cities500.zip...")
	downloadURL := "https://download.geonames.org/export/dump/cities500.zip"

	resp, err := http.Get(downloadURL)
	if err != nil {
		logger.Errorf("Failed to download cities data: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "DOWNLOAD_FAILED",
				Message: fmt.Sprintf("Failed to download cities data: %v", err),
			},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Errorf("Download returned status: %d", resp.StatusCode)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "DOWNLOAD_FAILED",
				Message: fmt.Sprintf("Download returned status: %d", resp.StatusCode),
			},
		})
		return
	}

	// 保存 zip 文件
	out, err := os.Create(zipFile)
	if err != nil {
		logger.Errorf("Failed to create zip file: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "CREATE_FILE_FAILED",
				Message: fmt.Sprintf("Failed to create zip file: %v", err),
			},
		})
		return
	}

	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		logger.Errorf("Failed to save zip file: %v", err)
		os.Remove(zipFile)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SAVE_FILE_FAILED",
				Message: fmt.Sprintf("Failed to save zip file: %v", err),
			},
		})
		return
	}

	// 解压文件
	logger.Info("Extracting cities500.zip...")
	if err := unzipFile(zipFile, dataDir); err != nil {
		logger.Errorf("Failed to extract zip file: %v", err)
		os.Remove(zipFile)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "EXTRACT_FAILED",
				Message: fmt.Sprintf("Failed to extract zip file: %v", err),
			},
		})
		return
	}

	// 删除 zip 文件
	os.Remove(zipFile)

	// 检查解压后的文件
	if info, err := os.Stat(citiesFile); err != nil {
		logger.Errorf("Cities file not found after extraction: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "EXTRACT_FAILED",
				Message: "Cities file not found after extraction",
			},
		})
		return
	} else {
		logger.Infof("Cities data downloaded successfully: %s (%d bytes)", citiesFile, info.Size())
	}

	// 自动导入城市数据
	logger.Info("Importing cities data into database...")
	importedCount, err := h.importCitiesFromFile(citiesFile)
	if err != nil {
		logger.Errorf("Failed to import cities data: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "IMPORT_FAILED",
				Message: fmt.Sprintf("Downloaded successfully but failed to import: %v", err),
			},
		})
		return
	}

	// 下载并导入中文城市名（alternateNamesV2.zip）
	zhImported := 0
	altNamesFile := filepath.Join(dataDir, "alternateNamesV2.txt")
	altNamesZip := filepath.Join(dataDir, "alternateNamesV2.zip")

	logger.Info("Downloading alternateNamesV2.zip for Chinese city names...")
	altResp, altErr := http.Get("https://download.geonames.org/export/dump/alternateNamesV2.zip")
	if altErr != nil {
		logger.Warnf("Failed to download alternate names (non-fatal): %v", altErr)
	} else {
		defer altResp.Body.Close()
		if altResp.StatusCode == http.StatusOK {
			altOut, err := os.Create(altNamesZip)
			if err == nil {
				_, err = io.Copy(altOut, altResp.Body)
				altOut.Close()
				if err == nil {
					if err := unzipFile(altNamesZip, dataDir); err == nil {
						if count, err := h.importAlternateNames(altNamesFile); err == nil {
							zhImported = count
						} else {
							logger.Warnf("Failed to import alternate names (non-fatal): %v", err)
						}
					} else {
						logger.Warnf("Failed to extract alternate names (non-fatal): %v", err)
					}
				}
				os.Remove(altNamesZip)
			}
		}
	}

	message := fmt.Sprintf("Cities data downloaded and imported successfully. Total %d cities in database.", importedCount)
	if zhImported > 0 {
		message += fmt.Sprintf(" %d Chinese city names imported.", zhImported)
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: message,
	})
}

// DownloadAlternateNames 下载并导入中文城市名数据
// @Summary 下载中文城市名数据
// @Description 下载 alternateNamesV2.zip 并导入中文城市名
// @Tags Config
// @Produce json
// @Success 200 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/cities-data/download-zh-names [post]
func (h *ConfigHandler) DownloadAlternateNames(c *gin.Context) {
	dataDir := filepath.Dir(h.cfg.Database.Path)
	if dataDir == "" || dataDir == "." {
		dataDir = "./data"
	}

	altNamesFile := filepath.Join(dataDir, "alternateNamesV2.txt")
	altNamesZip := filepath.Join(dataDir, "alternateNamesV2.zip")

	// 确保目录存在
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "CREATE_DIR_FAILED", Message: err.Error()},
		})
		return
	}

	// 下载
	logger.Info("Downloading alternateNamesV2.zip...")
	resp, err := http.Get("https://download.geonames.org/export/dump/alternateNamesV2.zip")
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "DOWNLOAD_FAILED", Message: fmt.Sprintf("Failed to download: %v", err)},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "DOWNLOAD_FAILED", Message: fmt.Sprintf("HTTP status: %d", resp.StatusCode)},
		})
		return
	}

	out, err := os.Create(altNamesZip)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "CREATE_FILE_FAILED", Message: err.Error()},
		})
		return
	}

	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		os.Remove(altNamesZip)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "SAVE_FILE_FAILED", Message: err.Error()},
		})
		return
	}

	// 解压
	if err := unzipFile(altNamesZip, dataDir); err != nil {
		os.Remove(altNamesZip)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "EXTRACT_FAILED", Message: err.Error()},
		})
		return
	}
	os.Remove(altNamesZip)

	// 导入
	count, err := h.importAlternateNames(altNamesFile)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "IMPORT_FAILED", Message: err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: fmt.Sprintf("Chinese city names imported successfully. %d cities updated.", count),
	})
}

// importCitiesFromFile 从文件导入城市数据到数据库
func (h *ConfigHandler) importCitiesFromFile(filePath string) (int, error) {
	// 获取数据库连接
	db := database.GetDB()
	if db == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// 先解析全部数据，再在事务中执行导入
	logger.Info("Parsing cities data...")
	scanner := bufio.NewScanner(file)
	var allCities []model.City
	totalCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		totalCount++

		city, err := parseCityLine(line)
		if err != nil {
			continue // 跳过无效行
		}

		allCities = append(allCities, *city)
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading file: %w", err)
	}

	logger.Infof("Parsed %d cities from %d lines, importing...", len(allCities), totalCount)

	// 在事务中执行清空和批量插入，确保原子性
	insertedCount := 0
	batchSize := 1000
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM cities").Error; err != nil {
			return fmt.Errorf("failed to clear cities table: %w", err)
		}

		for i := 0; i < len(allCities); i += batchSize {
			end := i + batchSize
			if end > len(allCities) {
				end = len(allCities)
			}
			batch := allCities[i:end]
			if err := tx.Create(&batch).Error; err != nil {
				return fmt.Errorf("failed to insert batch at offset %d: %w", i, err)
			}
			insertedCount += len(batch)
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	logger.Infof("Import completed: %d cities imported", insertedCount)
	return insertedCount, nil
}

// parseCityLine 解析 GeoNames cities500.txt 文件的一行
func parseCityLine(line string) (*model.City, error) {
	fields := strings.Split(line, "\t")
	if len(fields) < 19 {
		return nil, fmt.Errorf("invalid line format: expected 19 fields, got %d", len(fields))
	}

	// 解析 geonameid
	geonameID, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("invalid geoname_id: %s", fields[0])
	}

	// 解析纬度
	latitude, err := strconv.ParseFloat(fields[4], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid latitude: %s", fields[4])
	}

	// 解析经度
	longitude, err := strconv.ParseFloat(fields[5], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid longitude: %s", fields[5])
	}

	// 获取 admin1 (省/州) 名称
	adminName := fields[10] // admin1 code

	city := &model.City{
		GeonameID: geonameID,
		Name:      fields[1], // name
		AdminName: adminName,
		Country:   fields[8], // country code
		Latitude:  latitude,
		Longitude: longitude,
	}

	return city, nil
}

// importAlternateNames 从 alternateNamesV2.txt 导入中文城市名
func (h *ConfigHandler) importAlternateNames(filePath string) (int, error) {
	db := database.GetDB()
	if db == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	// 获取数据库中所有城市的 geoname_id
	var geonameIDs []int
	if err := db.Model(&model.City{}).Pluck("geoname_id", &geonameIDs).Error; err != nil {
		return 0, fmt.Errorf("failed to get geoname IDs: %w", err)
	}
	geonameIDSet := make(map[int]bool, len(geonameIDs))
	for _, id := range geonameIDs {
		geonameIDSet[id] = true
	}

	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// 解析中文名：geonameID -> 中文名（优先级：zh-CN > zh > zh-TW）
	zhNames := make(map[int]string)
	zhPriority := make(map[int]int)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 5 {
			continue
		}

		lang := fields[2]
		var priority int
		switch lang {
		case "zh-CN":
			priority = 3
		case "zh":
			priority = 2
		case "zh-TW":
			priority = 1
		default:
			continue
		}

		geonameID, err := strconv.Atoi(fields[1])
		if err != nil || !geonameIDSet[geonameID] {
			continue
		}

		name := strings.TrimSpace(fields[3])
		if name == "" {
			continue
		}

		isPreferred := fields[4] == "1"
		existingPri := zhPriority[geonameID]
		if priority < existingPri {
			continue
		}
		if priority == existingPri && !isPreferred {
			continue
		}

		zhNames[geonameID] = name
		zhPriority[geonameID] = priority
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading file: %w", err)
	}

	if len(zhNames) == 0 {
		return 0, nil
	}

	// 批量更新
	batchSize := 1000
	type item struct {
		id   int
		name string
	}
	var items []item
	for id, name := range zhNames {
		items = append(items, item{id: id, name: name})
	}

	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		batch := items[i:end]
		if err := db.Transaction(func(tx *gorm.DB) error {
			for _, it := range batch {
				if err := tx.Model(&model.City{}).Where("geoname_id = ?", it.id).Update("name_zh", it.name).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return 0, fmt.Errorf("failed to update at offset %d: %w", i, err)
		}
	}

	logger.Infof("Imported %d Chinese city names", len(items))
	return len(items), nil
}

// unzipFile 解压 zip 文件
func unzipFile(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	// 允许解压的文件名集合
	allowedFiles := map[string]bool{
		"cities500.txt":        true,
		"alternateNamesV2.txt": true,
	}

	for _, f := range r.File {
		if !allowedFiles[f.Name] {
			continue
		}

		dstPath := filepath.Join(dest, f.Name)

		// 创建文件
		dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		// 解压
		src, err := f.Open()
		if err != nil {
			dst.Close()
			return err
		}

		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()

		if err != nil {
			return err
		}
	}

	return nil
}
