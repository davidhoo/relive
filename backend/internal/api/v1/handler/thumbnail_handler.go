package handler

import (
	"net/http"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/gin-gonic/gin"
)

type ThumbnailHandler struct {
	service service.ThumbnailService
}

func NewThumbnailHandler(service service.ThumbnailService) *ThumbnailHandler {
	return &ThumbnailHandler{service: service}
}

func (h *ThumbnailHandler) StartBackground(c *gin.Context) {
	task, err := h.service.StartBackground()
	if err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "START_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "缩略图后台生成已启动", Data: task})
}

func (h *ThumbnailHandler) StopBackground(c *gin.Context) {
	if err := h.service.StopBackground(); err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "STOP_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "缩略图后台生成停止请求已发送"})
}

func (h *ThumbnailHandler) GetTask(c *gin.Context) {
	task := h.service.GetTaskStatus()
	c.JSON(http.StatusOK, model.Response{Success: true, Data: task})
}

func (h *ThumbnailHandler) GetBackgroundLogs(c *gin.Context) {
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "Success", Data: map[string]interface{}{"lines": h.service.GetBackgroundLogs()}})
}

func (h *ThumbnailHandler) GetStats(c *gin.Context) {
	stats, err := h.service.GetStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "STATS_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: stats})
}

func (h *ThumbnailHandler) Enqueue(c *gin.Context) {
	var req model.ThumbnailEnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}
	if err := h.service.EnqueuePhoto(req.PhotoID, "manual", 80); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "ENQUEUE_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "已加入缩略图队列"})
}

func (h *ThumbnailHandler) EnqueueByPath(c *gin.Context) {
	var req model.ThumbnailBatchEnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}
	count, err := h.service.EnqueueByPath(req.Path, "manual", 80)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "ENQUEUE_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "已加入缩略图队列", Data: gin.H{"count": count}})
}
