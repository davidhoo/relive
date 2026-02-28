package handler

import (
	"net/http"
	"strconv"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// PhotoHandler 照片处理器
type PhotoHandler struct {
	photoService service.PhotoService
}

// NewPhotoHandler 创建照片处理器
func NewPhotoHandler(photoService service.PhotoService) *PhotoHandler {
	return &PhotoHandler{
		photoService: photoService,
	}
}

// ScanPhotos 扫描照片
// @Summary 扫描照片
// @Description 扫描指定目录的照片
// @Tags photos
// @Accept json
// @Produce json
// @Param request body model.ScanPhotosRequest true "扫描请求"
// @Success 200 {object} model.Response{data=model.ScanPhotosResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/scan [post]
func (h *PhotoHandler) ScanPhotos(c *gin.Context) {
	var req model.ScanPhotosRequest
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

	// 验证路径
	if req.Path == "" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "Path is required",
			},
		})
		return
	}

	// 扫描照片
	resp, err := h.photoService.ScanPhotos(req.Path)
	if err != nil {
		logger.Errorf("Scan photos failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "SCAN_FAILED",
				Message: "Failed to scan photos: " + err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    resp,
	})
}

// GetPhotos 获取照片列表
// @Summary 获取照片列表
// @Description 分页获取照片列表，支持过滤和排序
// @Tags photos
// @Accept json
// @Produce json
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Param analyzed query bool false "是否已分析"
// @Param location query string false "位置过滤"
// @Param sort_by query string false "排序字段" default(taken_at)
// @Param sort_desc query bool false "降序排序" default(true)
// @Success 200 {object} model.Response{data=model.PagedResponse{items=[]model.Photo}}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos [get]
func (h *PhotoHandler) GetPhotos(c *gin.Context) {
	// 解析查询参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	var analyzed *bool
	if analyzedStr := c.Query("analyzed"); analyzedStr != "" {
		val := analyzedStr == "true"
		analyzed = &val
	}

	location := c.Query("location")
	sortBy := c.DefaultQuery("sort_by", "taken_at")
	sortDesc := c.DefaultQuery("sort_desc", "true") == "true"

	// 构建请求
	req := &model.GetPhotosRequest{
		Page:     page,
		PageSize: pageSize,
		Analyzed: analyzed,
		Location: location,
		SortBy:   sortBy,
		SortDesc: sortDesc,
	}

	// 查询照片
	photos, total, err := h.photoService.GetPhotos(req)
	if err != nil {
		logger.Errorf("Get photos failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get photos: " + err.Error(),
			},
		})
		return
	}

	// 构建分页响应
	pagedResp := model.PagedResponse{
		Items:    photos,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    pagedResp,
	})
}

// GetPhotoByID 根据 ID 获取照片
// @Summary 根据 ID 获取照片
// @Description 获取指定 ID 的照片详情
// @Tags photos
// @Accept json
// @Produce json
// @Param id path int true "照片 ID"
// @Success 200 {object} model.Response{data=model.Photo}
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/{id} [get]
func (h *PhotoHandler) GetPhotoByID(c *gin.Context) {
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

	// 查询照片
	photo, err := h.photoService.GetPhotoByID(uint(id))
	if err != nil {
		logger.Errorf("Get photo by ID failed: %v", err)
		c.JSON(http.StatusNotFound, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "NOT_FOUND",
				Message: "Photo not found",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    photo,
	})
}

// GetPhotoStats 获取照片统计
// @Summary 获取照片统计
// @Description 获取照片总数、已分析数、未分析数等统计信息
// @Tags photos
// @Accept json
// @Produce json
// @Success 200 {object} model.Response{data=model.PhotoStatsResponse}
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/stats [get]
func (h *PhotoHandler) GetPhotoStats(c *gin.Context) {
	// 获取统计信息
	total, err := h.photoService.CountAll()
	if err != nil {
		logger.Errorf("Count all photos failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get statistics",
			},
		})
		return
	}

	analyzed, err := h.photoService.CountAnalyzed()
	if err != nil {
		logger.Errorf("Count analyzed photos failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get statistics",
			},
		})
		return
	}

	unanalyzed, err := h.photoService.CountUnanalyzed()
	if err != nil {
		logger.Errorf("Count unanalyzed photos failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get statistics",
			},
		})
		return
	}

	stats := model.PhotoStatsResponse{
		Total:      total,
		Analyzed:   analyzed,
		Unanalyzed: unanalyzed,
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    stats,
	})
}
