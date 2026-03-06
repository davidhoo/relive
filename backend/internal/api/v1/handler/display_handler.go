package handler

import (
	"net/http"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// DisplayHandler 展示处理器
type DisplayHandler struct {
	displayService service.DisplayService
	esp32Service   service.ESP32Service
}

// NewDisplayHandler 创建展示处理器
func NewDisplayHandler(displayService service.DisplayService, esp32Service service.ESP32Service) *DisplayHandler {
	return &DisplayHandler{
		displayService: displayService,
		esp32Service:   esp32Service,
	}
}

// GetDisplayPhoto 获取展示照片
// @Summary 获取展示照片
// @Description ESP32 设备获取要展示的照片
// @Tags display
// @Accept json
// @Produce json
// @Param device_id query string true "设备 ID"
// @Success 200 {object} model.Response{data=model.GetDisplayPhotoResponse}
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/display/photo [get]
func (h *DisplayHandler) GetDisplayPhoto(c *gin.Context) {
	// 获取设备 ID
	deviceID := c.Query("device_id")
	if deviceID == "" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "device_id is required",
			},
		})
		return
	}

	// 验证设备是否存在
	device, err := h.esp32Service.GetByDeviceID(deviceID)
	if err != nil {
		logger.Warnf("Device not found: %s", deviceID)
		c.JSON(http.StatusNotFound, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "NOT_FOUND",
				Message: "Device not found",
			},
		})
		return
	}

	// 获取展示照片
	photo, err := h.displayService.GetDisplayPhoto(deviceID)
	if err != nil {
		logger.Errorf("Get display photo failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get display photo: " + err.Error(),
			},
		})
		return
	}

	// 记录展示
	record := &model.DisplayRecord{
		PhotoID:     photo.ID,
		DeviceID:    device.ID,
		DisplayedAt: time.Now(),
		TriggerType: "scheduled",
	}
	if err := h.displayService.RecordDisplay(record); err != nil {
		logger.Errorf("Record display failed: %v", err)
		// 不影响响应，只记录日志
	}

	// 构建响应
	var takenAt time.Time
	if photo.TakenAt != nil {
		takenAt = *photo.TakenAt
	}

	resp := model.GetDisplayPhotoResponse{
		PhotoID:      photo.ID,
		FilePath:     photo.FilePath,
		Width:        photo.Width,
		Height:       photo.Height,
		TakenAt:      takenAt,
		Location:     photo.Location,
		MemoryScore:  photo.MemoryScore,
		BeautyScore:  photo.BeautyScore,
		OverallScore: photo.OverallScore,
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    resp,
	})
}

// PreviewPhotos 预览展示策略结果
func (h *DisplayHandler) PreviewPhotos(c *gin.Context) {
	var req model.PreviewDisplayPhotosRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "Invalid request",
			},
		})
		return
	}

	cfg := &model.DisplayStrategyConfig{
		Algorithm:      req.Algorithm,
		MinBeautyScore: req.MinBeautyScore,
		MinMemoryScore: req.MinMemoryScore,
		DailyCount:     req.DailyCount,
	}

	var previewDate *time.Time
	if req.PreviewDate != "" {
		parsedDate, err := time.ParseInLocation("2006-01-02", req.PreviewDate, time.Local)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "INVALID_REQUEST",
					Message: "Invalid previewDate, expected format YYYY-MM-DD",
				},
			})
			return
		}
		previewDate = &parsedDate
	}

	photos, err := h.displayService.PreviewPhotos(cfg, previewDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "PREVIEW_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data: model.PreviewDisplayPhotosResponse{
			Algorithm:   cfg.Algorithm,
			Count:       len(photos),
			PreviewDate: req.PreviewDate,
			Photos:      photos,
		},
	})
}

// RecordDisplay 记录展示
// @Summary 记录展示
// @Description 记录照片在设备上的展示
// @Tags display
// @Accept json
// @Produce json
// @Param request body model.RecordDisplayRequest true "展示记录"
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/display/record [post]
func (h *DisplayHandler) RecordDisplay(c *gin.Context) {
	var req model.RecordDisplayRequest
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

	// 验证设备
	device, err := h.esp32Service.GetByDeviceID(req.DeviceID)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "Device not found",
			},
		})
		return
	}

	// 创建展示记录
	record := &model.DisplayRecord{
		PhotoID:     req.PhotoID,
		DeviceID:    device.ID,
		DisplayedAt: time.Now(),
		TriggerType: "manual", // API 手动上报
	}

	if err := h.displayService.RecordDisplay(record); err != nil {
		logger.Errorf("Record display failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "CREATE_FAILED",
				Message: "Failed to record display",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
	})
}
