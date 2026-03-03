package handler

import (
	"net/http"
	"strconv"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// APIKeyHandler API Key处理器
type APIKeyHandler struct {
	service service.APIKeyService
}

// NewAPIKeyHandler 创建API Key处理器
func NewAPIKeyHandler(service service.APIKeyService) *APIKeyHandler {
	return &APIKeyHandler{
		service: service,
	}
}

// GetAPIKeys 获取所有API Key
// @Summary 获取所有API Key
// @Description 获取系统中所有的API Key列表
// @Tags APIKey
// @Produce json
// @Success 200 {object} model.Response{data=[]model.APIKeyResponse}
// @Failure 500 {object} model.Response
// @Router /api/v1/config/api-keys [get]
func (h *APIKeyHandler) GetAPIKeys(c *gin.Context) {
	apiKeys, err := h.service.GetAll()
	if err != nil {
		logger.Errorf("Get api keys failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get API keys",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    apiKeys,
	})
}

// CreateAPIKey 创建新的API Key
// @Summary 创建新的API Key
// @Description 创建新的API Key，返回包含Key值的响应（仅创建时可见）
// @Tags APIKey
// @Accept json
// @Produce json
// @Param request body model.CreateAPIKeyRequest true "创建请求"
// @Success 200 {object} model.Response{data=model.APIKeyResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/api-keys [post]
func (h *APIKeyHandler) CreateAPIKey(c *gin.Context) {
	var req model.CreateAPIKeyRequest
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

	resp, err := h.service.Create(&req)
	if err != nil {
		logger.Errorf("Create api key failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "CREATE_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "API Key created successfully. Please save the key value, it will not be shown again.",
		Data:    resp,
	})
}

// UpdateAPIKey 更新API Key
// @Summary 更新API Key
// @Description 更新指定API Key的信息
// @Tags APIKey
// @Accept json
// @Produce json
// @Param id path int true "API Key ID"
// @Param request body model.UpdateAPIKeyRequest true "更新请求"
// @Success 200 {object} model.Response{data=model.APIKeyResponse}
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/api-keys/{id} [put]
func (h *APIKeyHandler) UpdateAPIKey(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_ID",
				Message: "Invalid API Key ID",
			},
		})
		return
	}

	var req model.UpdateAPIKeyRequest
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

	resp, err := h.service.Update(uint(id), &req)
	if err != nil {
		logger.Errorf("Update api key failed: %v", err)
		c.JSON(http.StatusNotFound, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "NOT_FOUND",
				Message: "API Key not found",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "API Key updated successfully",
		Data:    resp,
	})
}

// DeleteAPIKey 删除API Key
// @Summary 删除API Key
// @Description 删除指定的API Key
// @Tags APIKey
// @Produce json
// @Param id path int true "API Key ID"
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/api-keys/{id} [delete]
func (h *APIKeyHandler) DeleteAPIKey(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_ID",
				Message: "Invalid API Key ID",
			},
		})
		return
	}

	if err := h.service.Delete(uint(id)); err != nil {
		logger.Errorf("Delete api key failed: %v", err)
		c.JSON(http.StatusNotFound, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "NOT_FOUND",
				Message: "API Key not found",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "API Key deleted successfully",
	})
}

// RegenerateAPIKey 重新生成API Key
// @Summary 重新生成API Key
// @Description 重新生成指定API Key的Key值
// @Tags APIKey
// @Produce json
// @Param id path int true "API Key ID"
// @Success 200 {object} model.Response{data=model.RegenerateAPIKeyResponse}
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/config/api-keys/{id}/regenerate [post]
func (h *APIKeyHandler) RegenerateAPIKey(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_ID",
				Message: "Invalid API Key ID",
			},
		})
		return
	}

	resp, err := h.service.Regenerate(uint(id))
	if err != nil {
		logger.Errorf("Regenerate api key failed: %v", err)
		c.JSON(http.StatusNotFound, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "NOT_FOUND",
				Message: "API Key not found",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "API Key regenerated successfully. Please save the new key value, it will not be shown again.",
		Data:    resp,
	})
}
