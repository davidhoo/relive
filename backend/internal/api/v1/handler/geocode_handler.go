package handler

import (
	"net/http"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

type GeocodeHandler struct {
	service      service.GeocodeTaskService
	photoService service.PhotoService
}

func NewGeocodeHandler(service service.GeocodeTaskService, photoService service.PhotoService) *GeocodeHandler {
	return &GeocodeHandler{service: service, photoService: photoService}
}

func (h *GeocodeHandler) StartBackground(c *gin.Context) {
	task, err := h.service.StartBackground()
	if err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "START_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "GPS 逆地理编码后台任务已启动", Data: task})
}

func (h *GeocodeHandler) StopBackground(c *gin.Context) {
	if err := h.service.StopBackground(); err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "STOP_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "GPS 逆地理编码后台任务停止请求已发送"})
}

func (h *GeocodeHandler) GetTask(c *gin.Context) {
	c.JSON(http.StatusOK, model.Response{Success: true, Data: h.service.GetTaskStatus()})
}

func (h *GeocodeHandler) GetBackgroundLogs(c *gin.Context) {
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "Success", Data: map[string]interface{}{"lines": h.service.GetBackgroundLogs()}})
}

func (h *GeocodeHandler) GetStats(c *gin.Context) {
	stats, err := h.service.GetStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "STATS_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: stats})
}

func (h *GeocodeHandler) RepairLegacyStatus(c *gin.Context) {
	count, err := h.service.RepairLegacyStatus()
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "REPAIR_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "历史 GPS 状态修复完成", Data: gin.H{"count": count}})
}

func (h *GeocodeHandler) Enqueue(c *gin.Context) {
	var req model.GeocodeEnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}
	if err := h.service.EnqueuePhoto(req.PhotoID, "manual", 80, req.Force); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "ENQUEUE_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "已加入 GPS 逆地理编码队列"})
}

func (h *GeocodeHandler) EnqueueByPath(c *gin.Context) {
	var req model.GeocodeBatchEnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}
	count, err := h.service.EnqueueByPath(req.Path, "manual", 80)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "ENQUEUE_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "已加入 GPS 逆地理编码队列", Data: gin.H{"count": count}})
}

func (h *GeocodeHandler) Geocode(c *gin.Context) {
	var req model.GeocodeEnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}
	if err := h.service.GeocodePhoto(req.PhotoID); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "GEOCODE_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "GPS 解析完成"})
}

func (h *GeocodeHandler) RegeocodeAll(c *gin.Context) {
	go func() {
		count, err := h.photoService.RegeocodeAllPhotos()
		if err != nil {
			logger.Errorf("RegeocodeAll failed: %v", err)
			return
		}
		logger.Infof("RegeocodeAll completed: %d photos updated", count)
	}()
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "全量重建解析已在后台启动"})
}
