package handler

import (
	"fmt"
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

// SetAIService 动态更新 AI 服务（用于配置变更后热重载）
func (h *AIHandler) SetAIService(aiService service.AIService) {
	h.aiService = aiService
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

// AnalyzeBatch 批量分析照片（异步）
// @Summary 批量分析照片（异步）
// @Description 异步启动批量分析任务，立即返回任务ID
// @Tags ai
// @Accept json
// @Produce json
// @Param request body model.AIAnalyzeBatchRequest true "批量分析请求"
// @Success 200 {object} model.Response{data=map[string]interface{}}
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

	// 异步启动批量分析
	task, err := h.aiService.AnalyzeBatch(req.Limit)
	if err != nil {
		logger.Errorf("Batch analyze failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "ANALYZE_FAILED",
				Message: "Failed to start batch analyze: " + err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Batch analysis task started",
		Data: map[string]interface{}{
			"task_id":     task.ID,
			"status":      task.Status,
			"total_count": task.TotalCount,
			"queued":      task.TotalCount,
		},
	})
}

// GetProgress 获取分析进度
// @Summary 获取分析进度
// @Description 获取 AI 分析的进度和统计信息（包含任务状态）
// @Tags ai
// @Accept json
// @Produce json
// @Success 200 {object} model.Response{data=map[string]interface{}}
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

	// 获取总体进度
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

	// 获取当前任务状态
	task := h.aiService.GetTaskStatus()

	// 构建响应，兼容前端期望的格式
	responseData := map[string]interface{}{
		"total":          progress.Total,
		"completed":      progress.Analyzed,
		"failed":         0, // 从任务状态获取
		"is_running":     false,
		"current_photo_id": nil,
		"started_at":     nil,
		"provider":       progress.Provider,
		"estimated_cost": progress.EstimatedCost,
	}

	if task != nil {
		responseData["failed"] = task.FailedCount
		responseData["is_running"] = task.IsRunning()
		responseData["started_at"] = task.StartedAt
		// 当前处理的照片ID可以从task推断
		if task.IsRunning() && task.CurrentIndex > 0 && task.CurrentIndex <= task.TotalCount {
			// 这里简化处理，实际可能需要更精确的状态
		}
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    responseData,
	})
}

// GetTaskStatus 获取当前任务状态
// @Summary 获取当前任务状态
// @Description 获取正在运行的批量分析任务状态
// @Tags ai
// @Accept json
// @Produce json
// @Success 200 {object} model.Response{data=service.AnalyzeTask}
// @Failure 500 {object} model.Response
// @Router /api/v1/ai/task [get]
func (h *AIHandler) GetTaskStatus(c *gin.Context) {
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

	task := h.aiService.GetTaskStatus()
	if task == nil {
		c.JSON(http.StatusOK, model.Response{
			Success: true,
			Message: "No active task",
			Data:    nil,
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    task,
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
		"is_available":    provider.IsAvailable(), // 兼容前端字段名
		"max_concurrency": provider.MaxConcurrency(),
		"supports_batch":  provider.SupportsBatch(),
		"max_batch_size":  provider.MaxBatchSize(),
	}

	// 计算预估成本
	progress, _ := h.aiService.GetAnalyzeProgress()
	if progress != nil && progress.EstimatedCost > 0 {
		info["estimated_cost"] = fmt.Sprintf("¥%.2f", progress.EstimatedCost)
	} else {
		info["estimated_cost"] = "免费"
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    info,
	})
}
