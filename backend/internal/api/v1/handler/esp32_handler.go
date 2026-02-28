package handler

import (
	"net/http"
	"strconv"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// ESP32Handler ESP32 设备处理器
type ESP32Handler struct {
	esp32Service service.ESP32Service
}

// NewESP32Handler 创建 ESP32 设备处理器
func NewESP32Handler(esp32Service service.ESP32Service) *ESP32Handler {
	return &ESP32Handler{
		esp32Service: esp32Service,
	}
}

// Register 注册设备
// @Summary 注册设备
// @Description ESP32 设备注册到系统
// @Tags esp32
// @Accept json
// @Produce json
// @Param request body model.ESP32RegisterRequest true "注册请求"
// @Success 200 {object} model.Response{data=model.ESP32RegisterResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/esp32/register [post]
func (h *ESP32Handler) Register(c *gin.Context) {
	var req model.ESP32RegisterRequest
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
	resp, err := h.esp32Service.Register(&req)
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
// @Description ESP32 设备发送心跳
// @Tags esp32
// @Accept json
// @Produce json
// @Param request body model.ESP32HeartbeatRequest true "心跳请求"
// @Success 200 {object} model.Response{data=model.ESP32HeartbeatResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/esp32/heartbeat [post]
func (h *ESP32Handler) Heartbeat(c *gin.Context) {
	var req model.ESP32HeartbeatRequest
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
	resp, err := h.esp32Service.Heartbeat(&req)
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
// @Description 分页获取 ESP32 设备列表
// @Tags esp32
// @Accept json
// @Produce json
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Success 200 {object} model.Response{data=model.PagedResponse{items=[]model.ESP32Device}}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/esp32/devices [get]
func (h *ESP32Handler) GetDevices(c *gin.Context) {
	// 解析查询参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	// 查询设备
	devices, total, err := h.esp32Service.List(page, pageSize)
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
// @Tags esp32
// @Accept json
// @Produce json
// @Param device_id path string true "设备 ID"
// @Success 200 {object} model.Response{data=model.ESP32Device}
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/esp32/devices/{device_id} [get]
func (h *ESP32Handler) GetDeviceByID(c *gin.Context) {
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
	device, err := h.esp32Service.GetByDeviceID(deviceID)
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
// @Description 获取设备总数、在线数等统计信息
// @Tags esp32
// @Accept json
// @Produce json
// @Success 200 {object} model.Response{data=model.ESP32StatsResponse}
// @Failure 500 {object} model.Response
// @Router /api/v1/esp32/stats [get]
func (h *ESP32Handler) GetDeviceStats(c *gin.Context) {
	// 获取统计信息
	total, err := h.esp32Service.CountAll()
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

	online, err := h.esp32Service.CountOnline()
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

	stats := model.ESP32StatsResponse{
		Total:  total,
		Online: online,
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    stats,
	})
}
