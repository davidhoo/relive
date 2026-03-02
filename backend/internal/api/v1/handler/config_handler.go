package handler

import (
	"net/http"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// ConfigHandler 配置处理器
type ConfigHandler struct {
	service     service.ConfigService
	aiService   service.AIService
}

// NewConfigHandler 创建配置处理器
func NewConfigHandler(service service.ConfigService, aiService service.AIService) *ConfigHandler {
	return &ConfigHandler{
		service:   service,
		aiService: aiService,
	}
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
	if key == "ai" && h.aiService != nil {
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
	if _, hasAIConfig := configs["ai"]; hasAIConfig && h.aiService != nil {
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
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Configs updated successfully",
	})
}
