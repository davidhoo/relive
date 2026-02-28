package handler

import (
	"net/http"
	"strconv"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// AIHandler AI 分析处理器
type AIHandler struct {
	aiService service.AIService
}

// NewAIHandler 创建 AI 分析处理器
func NewAIHandler(aiService service.AIService) *AIHandler {
	return &AIHandler{
		aiService: aiService,
	}
}

// Analyze 分析单张照片
// @Summary 分析照片
// @Description 使用 AI 分析单张照片
// @Tags ai
// @Accept json
// @Produce json
// @Param request body model.AIAnalyzeRequest true "分析请求"
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/ai/analyze [post]
func (h *AIHandler) Analyze(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SERVICE_UNAVAILABLE",
				Message: "AI service not configured",
			},
		})
		return
	}

	var req model.AIAnalyzeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warnf("Invalid request: %v", err)
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "Invalid request",
			},
		})
		return
	}

	// 分析照片
	if err := h.aiService.AnalyzePhoto(req.PhotoID); err != nil {
		logger.Errorf("Analyze photo failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "ANALYZE_FAILED",
				Message: "Failed to analyze photo: " + err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Photo analyzed successfully",
	})
}

// AnalyzeBatch 批量分析照片
// @Summary 批量分析照片
// @Description 批量分析未分析的照片
// @Tags ai
// @Accept json
// @Produce json
// @Param request body model.AIAnalyzeBatchRequest true "批量分析请求"
// @Success 200 {object} model.Response{data=model.AIAnalyzeBatchResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/ai/analyze/batch [post]
func (h *AIHandler) AnalyzeBatch(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SERVICE_UNAVAILABLE",
				Message: "AI service not configured",
			},
		})
		return
	}

	var req model.AIAnalyzeBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warnf("Invalid request: %v", err)
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "Invalid request",
			},
		})
		return
	}

	// 设置默认限制
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.Limit > 1000 {
		req.Limit = 1000
	}

	// 批量分析
	result, err := h.aiService.AnalyzeBatch(req.Limit)
	if err != nil {
		logger.Errorf("Batch analyze failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "ANALYZE_FAILED",
				Message: "Failed to batch analyze: " + err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Batch analysis completed",
		Data:    result,
	})
}

// GetProgress 获取分析进度
// @Summary 获取分析进度
// @Description 获取 AI 分析的进度和统计信息
// @Tags ai
// @Accept json
// @Produce json
// @Success 200 {object} model.Response{data=model.AIAnalyzeProgressResponse}
// @Failure 500 {object} model.Response
// @Router /api/v1/ai/progress [get]
func (h *AIHandler) GetProgress(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SERVICE_UNAVAILABLE",
				Message: "AI service not configured",
			},
		})
		return
	}

	// 获取进度
	progress, err := h.aiService.GetAnalyzeProgress()
	if err != nil {
		logger.Errorf("Get progress failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get progress: " + err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    progress,
	})
}

// ReAnalyze 重新分析照片
// @Summary 重新分析照片
// @Description 重新分析已分析的照片
// @Tags ai
// @Accept json
// @Produce json
// @Param id path int true "照片 ID"
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/ai/reanalyze/{id} [post]
func (h *AIHandler) ReAnalyze(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SERVICE_UNAVAILABLE",
				Message: "AI service not configured",
			},
		})
		return
	}

	// 解析 ID
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "Invalid photo ID",
			},
		})
		return
	}

	// 重新分析（直接调用 AnalyzePhoto，不检查是否已分析）
	if err := h.aiService.AnalyzePhoto(uint(id)); err != nil {
		logger.Errorf("Re-analyze photo failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "ANALYZE_FAILED",
				Message: "Failed to re-analyze photo: " + err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Photo re-analyzed successfully",
	})
}

// GetProviderInfo 获取 Provider 信息
// @Summary 获取 Provider 信息
// @Description 获取当前使用的 AI Provider 信息
// @Tags ai
// @Accept json
// @Produce json
// @Success 200 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/ai/provider [get]
func (h *AIHandler) GetProviderInfo(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SERVICE_UNAVAILABLE",
				Message: "AI service not configured",
			},
		})
		return
	}

	provider, err := h.aiService.GetProvider()
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	info := map[string]interface{}{
		"name":            provider.Name(),
		"cost":            provider.Cost(),
		"available":       provider.IsAvailable(),
		"max_concurrency": provider.MaxConcurrency(),
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    info,
	})
}
