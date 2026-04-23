package handler

import (
	"net/http"
	"strconv"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/gin-gonic/gin"
)

type OrientationSuggestionHandler struct {
	service service.OrientationSuggestionService
}

func NewOrientationSuggestionHandler(service service.OrientationSuggestionService) *OrientationSuggestionHandler {
	return &OrientationSuggestionHandler{
		service: service,
	}
}

// GetGroups returns orientation suggestion groups grouped by rotation angle
func (h *OrientationSuggestionHandler) GetGroups(c *gin.Context) {
	groups, err := h.service.GetGroups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "GET_GROUPS_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: gin.H{
			"groups": groups,
		},
	})
}

// GetDetail returns detailed suggestions for a specific rotation angle
func (h *OrientationSuggestionHandler) GetDetail(c *gin.Context) {
	rotationStr := c.Query("rotation")
	if rotationStr == "" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "MISSING_ROTATION",
				Message: "rotation parameter is required",
			},
		})
		return
	}

	rotation, err := strconv.Atoi(rotationStr)
	if err != nil || (rotation != 0 && rotation != 90 && rotation != 180 && rotation != 270) {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_ROTATION",
				Message: "rotation must be 0, 90, 180, or 270",
			},
		})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	detail, err := h.service.GetDetail(rotation, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "GET_DETAIL_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    detail,
	})
}

// Apply applies orientation suggestions to selected photos
func (h *OrientationSuggestionHandler) Apply(c *gin.Context) {
	var req struct {
		PhotoIDs []uint `json:"photo_ids" binding:"required"`
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

	if len(req.PhotoIDs) == 0 {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "EMPTY_PHOTO_IDS",
				Message: "photo_ids is required",
			},
		})
		return
	}

	applied, err := h.service.Apply(req.PhotoIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "APPLY_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: gin.H{
			"applied": applied,
		},
	})
}

// Dismiss dismisses orientation suggestions for selected photos
func (h *OrientationSuggestionHandler) Dismiss(c *gin.Context) {
	var req struct {
		PhotoIDs []uint `json:"photo_ids" binding:"required"`
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

	if len(req.PhotoIDs) == 0 {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "EMPTY_PHOTO_IDS",
				Message: "photo_ids is required",
			},
		})
		return
	}

	if err := h.service.Dismiss(req.PhotoIDs); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "DISMISS_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "已忽略",
	})
}

// GetTask returns the background task status
func (h *OrientationSuggestionHandler) GetTask(c *gin.Context) {
	task := h.service.GetTask()
	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    task,
	})
}

// GetStats returns orientation suggestion statistics
func (h *OrientationSuggestionHandler) GetStats(c *gin.Context) {
	stats, err := h.service.GetStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "GET_STATS_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    stats,
	})
}

// GetLogs returns background task logs
func (h *OrientationSuggestionHandler) GetLogs(c *gin.Context) {
	logs := h.service.GetBackgroundLogs()
	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: gin.H{
			"lines": logs,
		},
	})
}

// Pause pauses the background task
func (h *OrientationSuggestionHandler) Pause(c *gin.Context) {
	if err := h.service.Pause(); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "PAUSE_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "已暂停",
	})
}

// Resume resumes the background task
func (h *OrientationSuggestionHandler) Resume(c *gin.Context) {
	if err := h.service.Resume(); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "RESUME_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "已恢复",
	})
}

// Rebuild marks the task for rebuild
func (h *OrientationSuggestionHandler) Rebuild(c *gin.Context) {
	if err := h.service.Rebuild(); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "REBUILD_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "已标记重建",
	})
}
