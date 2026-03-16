package handler

import (
	"net/http"
	"strconv"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// EventHandler 事件处理器
type EventHandler struct {
	clusteringService service.EventClusteringService
	eventRepo         repository.EventRepository
	db                *gorm.DB
}

// NewEventHandler 创建事件处理器
func NewEventHandler(clusteringService service.EventClusteringService, eventRepo repository.EventRepository, db *gorm.DB) *EventHandler {
	return &EventHandler{
		clusteringService: clusteringService,
		eventRepo:         eventRepo,
		db:                db,
	}
}

// ListEvents 事件列表（分页）
func (h *EventHandler) ListEvents(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	events, total, err := h.eventRepo.List(page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "LIST_FAILED", Message: err.Error()}})
		return
	}

	totalPages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		totalPages++
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: model.PagedResponse{
			Items:      events,
			Total:      total,
			Page:       page,
			PageSize:   pageSize,
			TotalPages: totalPages,
		},
	})
}

// GetEvent 事件详情（含照片列表）
func (h *EventHandler) GetEvent(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_ID", Message: "无效的事件 ID"}})
		return
	}

	event, err := h.eventRepo.GetByID(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, model.Response{Success: false, Error: &model.ErrorInfo{Code: "NOT_FOUND", Message: "事件不存在"}})
		return
	}

	// 查询事件内照片
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}

	var photos []*model.Photo
	var total int64
	h.db.Model(&model.Photo{}).Where("event_id = ? AND status = ?", event.ID, model.PhotoStatusActive).Count(&total)
	offset := (page - 1) * pageSize
	h.db.Where("event_id = ? AND status = ?", event.ID, model.PhotoStatusActive).
		Order("taken_at ASC").Offset(offset).Limit(pageSize).Find(&photos)

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: gin.H{
			"event": event,
			"photos": model.PagedResponse{
				Items:    photos,
				Total:    total,
				Page:     page,
				PageSize: pageSize,
			},
		},
	})
}

// StartClustering 启动增量聚类
func (h *EventHandler) StartClustering(c *gin.Context) {
	task, err := h.clusteringService.StartClustering()
	if err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "START_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "增量聚类任务已启动", Data: task})
}

// StartRebuild 启动全量重建
func (h *EventHandler) StartRebuild(c *gin.Context) {
	task, err := h.clusteringService.StartRebuild()
	if err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "START_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "全量重建聚类任务已启动", Data: task})
}

// GetClusteringTask 获取聚类任务状态
func (h *EventHandler) GetClusteringTask(c *gin.Context) {
	c.JSON(http.StatusOK, model.Response{Success: true, Data: h.clusteringService.GetTask()})
}

// StopClustering 停止聚类任务
func (h *EventHandler) StopClustering(c *gin.Context) {
	if err := h.clusteringService.StopTask(); err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "STOP_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "聚类任务停止请求已发送"})
}
