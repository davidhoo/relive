package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AnalyzerHandler 分析器处理器
type AnalyzerHandler struct {
	photoService    service.PhotoService
	analysisService service.AnalysisService
}

// NewAnalyzerHandler 创建分析器处理器
func NewAnalyzerHandler(photoService service.PhotoService, analysisService service.AnalysisService) *AnalyzerHandler {
	return &AnalyzerHandler{
		photoService:    photoService,
		analysisService: analysisService,
	}
}

// GetTasks 获取待分析任务列表
// @Summary 获取待分析任务
// @Description 获取待分析的 photo 任务列表，自动锁定任务防止重复分配
// @Tags analyzer
// @Produce json
// @Param limit query int false "获取任务数量（默认10，最大50）"
// @Param X-Analyzer-ID header string false "分析器实例标识"
// @Success 200 {object} model.Response{data=model.AnalyzerTasksResponse}
// @Failure 401 {object} model.Response
// @Failure 429 {object} model.Response
// @Failure 503 {object} model.Response
// @Router /api/v1/analyzer/tasks [get]
func (h *AnalyzerHandler) GetTasks(c *gin.Context) {
	// 获取限制数量
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	// 获取分析器实例ID
	analyzerID := c.GetHeader("X-Analyzer-ID")
	if analyzerID == "" {
		analyzerID = uuid.New().String()
	}

	// 获取设备信息
	deviceID, _ := c.Get("device_id")
	deviceName, _ := c.Get("device_name")

	logger.Infof("Analyzer %s requesting %d tasks (Device: %v)", analyzerID, limit, deviceName)

	// 获取待分析任务
	tasks, totalRemaining, err := h.analysisService.GetPendingTasks(limit, analyzerID)
	if err != nil {
		logger.Errorf("Failed to get pending tasks: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INTERNAL_ERROR",
				Message: "Failed to get tasks",
			},
		})
		return
	}

	// 如果没有任务了
	if len(tasks) == 0 {
		c.JSON(http.StatusServiceUnavailable, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "NO_TASKS_AVAILABLE",
				Message: "No tasks available",
			},
			Data: gin.H{
				"total_remaining": totalRemaining,
			},
		})
		return
	}

	// 设置响应头
	c.Header("X-Lock-Timeout", "300") // 5分钟锁定期
	c.Header("X-Analyzer-ID", analyzerID)

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Tasks retrieved successfully",
		Data: model.AnalyzerTasksResponse{
			Tasks:          tasks,
			TotalRemaining: totalRemaining,
			LockDuration:   300,
			AnalyzerID:     analyzerID,
			DeviceID:       deviceID.(uint),
		},
	})
}

// Heartbeat 任务心跳续期
// @Summary 任务心跳续期
// @Description 续期任务锁，防止长时间分析的任务被重新分配
// @Tags analyzer
// @Accept json
// @Produce json
// @Param task_id path string true "任务ID"
// @Param request body model.HeartbeatRequest false "心跳请求"
// @Success 200 {object} model.Response{data=model.HeartbeatResponse}
// @Failure 404 {object} model.Response
// @Failure 409 {object} model.Response
// @Router /api/v1/analyzer/tasks/{task_id}/heartbeat [post]
func (h *AnalyzerHandler) Heartbeat(c *gin.Context) {
	taskID := c.Param("task_id")
	analyzerID := c.GetHeader("X-Analyzer-ID")
	if analyzerID == "" {
		analyzerID = c.Query("analyzer_id")
	}

	var req model.HeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 心跳请求体是可选的，绑定失败继续处理
		req = model.HeartbeatRequest{}
	}

	// 续期任务锁
	lockExpiresAt, err := h.analysisService.ExtendTaskLock(taskID, analyzerID)
	if err != nil {
		switch err {
		case service.ErrTaskNotFound:
			c.JSON(http.StatusNotFound, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "TASK_NOT_FOUND",
					Message: "Task not found or expired",
				},
			})
		case service.ErrTaskLockedByOther:
			c.JSON(http.StatusConflict, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "TASK_LOCKED_BY_OTHER",
					Message: "Task locked by another analyzer",
				},
			})
		default:
			logger.Errorf("Failed to extend task lock: %v", err)
			c.JSON(http.StatusInternalServerError, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "INTERNAL_ERROR",
					Message: "Failed to extend lock",
				},
			})
		}
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Lock extended successfully",
		Data: model.HeartbeatResponse{
			LockExpiresAt: lockExpiresAt,
			LockDuration:  300,
		},
	})
}

// ReleaseTask 释放任务
// @Summary 释放任务
// @Description 当分析器无法处理某张照片时，主动释放任务
// @Tags analyzer
// @Accept json
// @Produce json
// @Param task_id path string true "任务ID"
// @Param request body model.ReleaseTaskRequest true "释放请求"
// @Success 200 {object} model.Response
// @Failure 404 {object} model.Response
// @Router /api/v1/analyzer/tasks/{task_id}/release [post]
func (h *AnalyzerHandler) ReleaseTask(c *gin.Context) {
	taskID := c.Param("task_id")
	analyzerID := c.GetHeader("X-Analyzer-ID")

	var req model.ReleaseTaskRequest
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

	err := h.analysisService.ReleaseTask(taskID, analyzerID, req.Reason, req.ErrorMsg, req.RetryLater)
	if err != nil {
		switch err {
		case service.ErrTaskNotFound:
			c.JSON(http.StatusNotFound, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "TASK_NOT_FOUND",
					Message: "Task not found",
				},
			})
		default:
			logger.Errorf("Failed to release task: %v", err)
			c.JSON(http.StatusInternalServerError, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "INTERNAL_ERROR",
					Message: "Failed to release task",
				},
			})
		}
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Task released successfully",
		Data: gin.H{
			"task_id":    taskID,
			"new_status": "pending",
		},
	})
}

// SubmitResults 提交分析结果
// @Summary 提交分析结果
// @Description 批量提交照片分析结果（幂等性处理）
// @Tags analyzer
// @Accept json
// @Produce json
// @Param request body model.SubmitResultsRequest true "提交结果请求"
// @Success 200 {object} model.Response{data=model.SubmitResultsResponse}
// @Failure 400 {object} model.Response
// @Failure 413 {object} model.Response
// @Router /api/v1/analyzer/results [post]
func (h *AnalyzerHandler) SubmitResults(c *gin.Context) {
	var req model.SubmitResultsRequest
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

	// 验证批量大小
	if len(req.Results) == 0 {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "EMPTY_RESULTS",
				Message: "Results cannot be empty",
			},
		})
		return
	}

	if len(req.Results) > 50 {
		c.JSON(http.StatusRequestEntityTooLarge, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "BATCH_TOO_LARGE",
				Message: "Batch size too large (max 50)",
			},
			Data: gin.H{
				"max_allowed": 50,
				"current":     len(req.Results),
			},
		})
		return
	}

	// 获取设备信息
	deviceID, _ := c.Get("device_id")
	deviceName, _ := c.Get("device_name")

	logger.Infof("Submitting %d results from analyzer (Device: %v)", len(req.Results), deviceName)

	// 提交结果
	resp, err := h.analysisService.SubmitResults(req.Results, deviceID.(uint))
	if err != nil {
		logger.Errorf("Failed to submit results: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INTERNAL_ERROR",
				Message: "Failed to submit results",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Results submitted successfully",
		Data:    resp,
	})
}

// GetStats 获取分析统计信息
// @Summary 获取分析统计
// @Description 获取照片分析任务的统计信息
// @Tags analyzer
// @Produce json
// @Success 200 {object} model.Response{data=model.AnalyzerStatsResponse}
// @Router /api/v1/analyzer/stats [get]
func (h *AnalyzerHandler) GetStats(c *gin.Context) {
	// 获取设备信息
	deviceID, deviceIDExists := c.Get("device_id")
	var deviceIDUint uint
	if deviceIDExists {
		deviceIDUint = deviceID.(uint)
	}

	stats, err := h.analysisService.GetStats(deviceIDUint)
	if err != nil {
		logger.Errorf("Failed to get stats: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INTERNAL_ERROR",
				Message: "Failed to get stats",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Stats retrieved successfully",
		Data:    stats,
	})
}

// CleanExpiredLocks 清理过期锁（内部使用或管理接口）
// @Summary 清理过期锁
// @Description 手动触发清理过期的任务锁（通常由定时任务自动执行）
// @Tags analyzer
// @Produce json
// @Success 200 {object} model.Response
// @Router /api/v1/analyzer/clean-locks [post]
func (h *AnalyzerHandler) CleanExpiredLocks(c *gin.Context) {
	count, err := h.analysisService.CleanExpiredLocks()
	if err != nil {
		logger.Errorf("Failed to clean expired locks: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INTERNAL_ERROR",
				Message: "Failed to clean locks",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Expired locks cleaned successfully",
		Data: gin.H{
			"cleaned_count": count,
		},
	})
}

// AnalysisService 接口定义（用于编译检查）
// 实际实现在 service/analysis_service.go
type AnalysisService interface {
	GetPendingTasks(limit int, analyzerID string) ([]model.AnalysisTask, int64, error)
	ExtendTaskLock(taskID, analyzerID string) (time.Time, error)
	ReleaseTask(taskID, analyzerID, reason, errorMsg string, retryLater bool) error
	SubmitResults(results []model.AnalysisResult, deviceID uint) (*model.SubmitResultsResponse, error)
	GetStats(deviceID uint) (*model.AnalyzerStatsResponse, error)
	CleanExpiredLocks() (int64, error)
}
