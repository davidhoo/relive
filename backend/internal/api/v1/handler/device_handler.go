package handler

import (
	"net/http"
	"strconv"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// DeviceHandler 设备处理器
type DeviceHandler struct {
	deviceService service.DeviceService
}

// NewDeviceHandler 创建设备处理器
func NewDeviceHandler(deviceService service.DeviceService) *DeviceHandler {
	return &DeviceHandler{
		deviceService: deviceService,
	}
}

// Register 注册设备
// @Summary 注册设备
// @Description 设备注册到系统（支持多种设备类型）
// @Tags devices
// @Accept json
// @Produce json
// @Param request body model.DeviceRegisterRequest true "注册请求"
// @Success 200 {object} model.Response{data=model.DeviceRegisterResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/devices/register [post]
// @Router /api/v1/esp32/register [post]
func (h *DeviceHandler) Register(c *gin.Context) {
	var req model.DeviceRegisterRequest
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

	// 验证必填字段
	if req.DeviceID == "" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "device_id is required",
			},
		})
		return
	}

	// 注册设备
	resp, err := h.deviceService.Register(&req)
	if err != nil {
		logger.Errorf("Register device failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "CREATE_FAILED",
				Message: "Failed to register device: " + err.Error(),
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

// Heartbeat 设备心跳
// @Summary 设备心跳
// @Description 设备发送心跳
// @Tags devices
// @Accept json
// @Produce json
// @Param request body model.DeviceHeartbeatRequest true "心跳请求"
// @Success 200 {object} model.Response{data=model.DeviceHeartbeatResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/devices/heartbeat [post]
// @Router /api/v1/esp32/heartbeat [post]
func (h *DeviceHandler) Heartbeat(c *gin.Context) {
	var req model.DeviceHeartbeatRequest
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

	// 验证设备 ID
	if req.DeviceID == "" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "device_id is required",
			},
		})
		return
	}

	// 处理心跳
	resp, err := h.deviceService.Heartbeat(&req)
	if err != nil {
		logger.Errorf("Heartbeat failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "UPDATE_FAILED",
				Message: "Failed to process heartbeat: " + err.Error(),
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

// GetDevices 获取设备列表
// @Summary 获取设备列表
// @Description 分页获取设备列表，可按设备类型或平台筛选
// @Tags devices
// @Accept json
// @Produce json
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Param device_type query string false "设备类型筛选（esp32/android/ios等）"
// @Param platform query string false "平台筛选（embedded/mobile/web）"
// @Success 200 {object} model.Response{data=model.PagedResponse{items=[]model.Device}}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/devices [get]
// @Router /api/v1/esp32/devices [get]
func (h *DeviceHandler) GetDevices(c *gin.Context) {
	// 解析查询参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	deviceType := c.Query("device_type")
	platform := c.Query("platform")

	var devices []*model.Device
	var total int64
	var err error

	// 根据筛选条件查询
	if deviceType != "" {
		// 按设备类型查询
		devices, err = h.deviceService.ListByDeviceType(deviceType)
		if err != nil {
			logger.Errorf("Get devices by type failed: %v", err)
			c.JSON(http.StatusInternalServerError, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "QUERY_FAILED",
					Message: "Failed to get devices: " + err.Error(),
				},
			})
			return
		}
		total = int64(len(devices))
		// 手动分页
		start := (page - 1) * pageSize
		end := start + pageSize
		if start < len(devices) {
			if end > len(devices) {
				end = len(devices)
			}
			devices = devices[start:end]
		} else {
			devices = []*model.Device{}
		}
	} else if platform != "" {
		// 按平台查询
		devices, err = h.deviceService.ListByPlatform(platform)
		if err != nil {
			logger.Errorf("Get devices by platform failed: %v", err)
			c.JSON(http.StatusInternalServerError, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "QUERY_FAILED",
					Message: "Failed to get devices: " + err.Error(),
				},
			})
			return
		}
		total = int64(len(devices))
		// 手动分页
		start := (page - 1) * pageSize
		end := start + pageSize
		if start < len(devices) {
			if end > len(devices) {
				end = len(devices)
			}
			devices = devices[start:end]
		} else {
			devices = []*model.Device{}
		}
	} else {
		// 查询所有设备
		devices, total, err = h.deviceService.List(page, pageSize)
		if err != nil {
			logger.Errorf("Get devices failed: %v", err)
			c.JSON(http.StatusInternalServerError, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "QUERY_FAILED",
					Message: "Failed to get devices: " + err.Error(),
				},
			})
			return
		}
	}

	// 构建分页响应
	pagedResp := model.PagedResponse{
		Items:    devices,
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

// GetDeviceByID 根据 ID 获取设备
// @Summary 根据 ID 获取设备
// @Description 获取指定设备 ID 的详细信息
// @Tags devices
// @Accept json
// @Produce json
// @Param device_id path string true "设备 ID"
// @Success 200 {object} model.Response{data=model.Device}
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/devices/{device_id} [get]
// @Router /api/v1/esp32/devices/{device_id} [get]
func (h *DeviceHandler) GetDeviceByID(c *gin.Context) {
	deviceID := c.Param("device_id")
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

	// 查询设备
	device, err := h.deviceService.GetByDeviceID(deviceID)
	if err != nil {
		logger.Errorf("Get device by ID failed: %v", err)
		c.JSON(http.StatusNotFound, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "NOT_FOUND",
				Message: "Device not found",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    device,
	})
}

// GetDeviceStats 获取设备统计
// @Summary 获取设备统计
// @Description 获取设备总数、在线数、按类型和平台统计
// @Tags devices
// @Accept json
// @Produce json
// @Success 200 {object} model.Response{data=model.DeviceStatsResponse}
// @Failure 500 {object} model.Response
// @Router /api/v1/devices/stats [get]
// @Router /api/v1/esp32/stats [get]
func (h *DeviceHandler) GetDeviceStats(c *gin.Context) {
	// 获取统计信息
	total, err := h.deviceService.CountAll()
	if err != nil {
		logger.Errorf("Count all devices failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get statistics",
			},
		})
		return
	}

	online, err := h.deviceService.CountOnline()
	if err != nil {
		logger.Errorf("Count online devices failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get statistics",
			},
		})
		return
	}

	// 按设备类型统计
	byType := make(map[string]int64)
	deviceTypes := []string{"esp32", "esp8266", "stm32", "android", "ios", "web"}
	for _, dt := range deviceTypes {
		count, err := h.deviceService.CountByDeviceType(dt)
		if err == nil && count > 0 {
			byType[dt] = count
		}
	}

	// 按平台统计
	byPlatform := make(map[string]int64)
	platforms := []string{"embedded", "mobile", "web"}
	for _, p := range platforms {
		count, err := h.deviceService.CountByPlatform(p)
		if err == nil && count > 0 {
			byPlatform[p] = count
		}
	}

	stats := model.DeviceStatsResponse{
		Total:      total,
		Online:     online,
		ByType:     byType,
		ByPlatform: byPlatform,
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    stats,
	})
}

// ============= 向后兼容 =============

// ESP32Handler 类型别名，保持向后兼容
// Deprecated: 使用 DeviceHandler 代替
type ESP32Handler = DeviceHandler

// NewESP32Handler 创建设备处理器（兼容旧代码）
// Deprecated: 使用 NewDeviceHandler 代替
func NewESP32Handler(deviceService service.DeviceService) *DeviceHandler {
	return NewDeviceHandler(deviceService)
}
