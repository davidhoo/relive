package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

func (h *DisplayHandler) GetDeviceDisplay(c *gin.Context) {
	selection, ok := h.resolveDeviceDisplaySelection(c)
	if !ok {
		return
	}

	resp := model.DeviceDisplayResponse{
		BatchDate:     selection.BatchDate,
		Sequence:      selection.Sequence,
		TotalCount:    selection.TotalCount,
		PhotoID:       selection.Item.PhotoID,
		ItemID:        selection.Item.ID,
		AssetID:       selection.Asset.ID,
		RenderProfile: selection.Asset.RenderProfile,
		BinURL:        fmt.Sprintf("/api/v1/display/assets/%d/bin", selection.Asset.ID),
		Checksum:      selection.Asset.Checksum,
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "Success", Data: resp})
}

func (h *DisplayHandler) GetDeviceDisplayBin(c *gin.Context) {
	selection, ok := h.resolveDeviceDisplaySelection(c)
	if !ok {
		return
	}

	fullPath, ok := h.prepareDeviceDisplayBinResponse(c, selection)
	if !ok {
		return
	}
	c.File(fullPath)
}

func (h *DisplayHandler) HeadDeviceDisplayBin(c *gin.Context) {
	selection, ok := h.resolveDeviceDisplaySelection(c)
	if !ok {
		return
	}

	if _, ok := h.prepareDeviceDisplayBinResponse(c, selection); !ok {
		return
	}

	c.Status(http.StatusOK)
}

func (h *DisplayHandler) prepareDeviceDisplayBinResponse(c *gin.Context, selection *model.DeviceDisplaySelection) (string, bool) {
	asset := selection.Asset
	fullPath := filepath.Join(util.DisplayBatchRoot(h.cfg.Photos.ThumbnailPath), asset.BinPath)
	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		logger.Errorf("Get device display bin failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "DISPLAY_FAILED", Message: err.Error()},
		})
		return "", false
	}

	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
	c.Header("X-Asset-ID", strconv.FormatUint(uint64(asset.ID), 10))
	c.Header("X-Checksum", asset.Checksum)
	c.Header("X-Photo-ID", strconv.FormatUint(uint64(selection.Item.PhotoID), 10))
	c.Header("X-Render-Profile", asset.RenderProfile)
	c.Header("X-Batch-Date", selection.BatchDate)
	c.Header("X-Sequence", strconv.Itoa(selection.Sequence))
	c.Header("X-Server-Time", fmt.Sprintf("%d", time.Now().Unix()))

	return fullPath, true
}

func (h *DisplayHandler) resolveDeviceDisplaySelection(c *gin.Context) (*model.DeviceDisplaySelection, bool) {
	deviceIDValue, exists := c.Get("device_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "UNAUTHORIZED", Message: "Device context missing"},
		})
		return nil, false
	}
	deviceID, ok := deviceIDValue.(uint)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "UNAUTHORIZED", Message: "Invalid device context"},
		})
		return nil, false
	}

	device, err := h.deviceService.GetByID(deviceID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "UNAUTHORIZED", Message: "Device not found"},
		})
		return nil, false
	}

	renderProfile := device.RenderProfile
	if device.DeviceType == model.DeviceTypeEmbedded && renderProfile == "" {
		renderProfile = util.DefaultRenderProfile()
	}
	selection, err := h.displayService.GetDeviceDisplay(deviceID, renderProfile)
	if err != nil {
		logger.Errorf("Get device display failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "DISPLAY_FAILED", Message: err.Error()},
		})
		return nil, false
	}

	return selection, true
}

func (h *DisplayHandler) GenerateDailyBatchAsync(c *gin.Context) {
	var req model.GenerateDailyDisplayBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "Invalid request"}})
		return
	}

	targetDate := time.Now()
	if req.Date != "" {
		parsedDate, err := time.ParseInLocation("2006-01-02", req.Date, time.Local)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "date format must be YYYY-MM-DD"}})
			return
		}
		targetDate = parsedDate
	}

	batch, err := h.displayService.StartGenerateDailyBatch(targetDate, req.Force)
	if err != nil {
		if err.Error() == "batch generation already running" {
			c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "TASK_RUNNING", Message: "批次生成任务正在运行中"}})
			return
		}
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "GENERATE_FAILED", Message: err.Error()}})
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "批次生成已启动", Data: h.toDailyBatchResponse(batch)})
}

func (h *DisplayHandler) GenerateDailyBatch(c *gin.Context) {
	var req model.GenerateDailyDisplayBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "Invalid request"}})
		return
	}

	targetDate := time.Now()
	if req.Date != "" {
		parsedDate, err := time.ParseInLocation("2006-01-02", req.Date, time.Local)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "date format must be YYYY-MM-DD"}})
			return
		}
		targetDate = parsedDate
	}

	batch, err := h.displayService.GenerateDailyBatch(targetDate, req.Force)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "GENERATE_FAILED", Message: err.Error()}})
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "Success", Data: h.toDailyBatchResponse(batch)})
}

func (h *DisplayHandler) GetDailyBatch(c *gin.Context) {
	dateStr := c.Query("date")
	targetDate := time.Now()
	if dateStr != "" {
		parsedDate, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "date format must be YYYY-MM-DD"}})
			return
		}
		targetDate = parsedDate
	}
	batch, err := h.displayService.GetDailyBatch(targetDate)
	if err != nil {
		c.JSON(http.StatusNotFound, model.Response{Success: false, Error: &model.ErrorInfo{Code: "NOT_FOUND", Message: "Daily batch not found"}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "Success", Data: h.toDailyBatchResponse(batch)})
}

func (h *DisplayHandler) ListDailyBatches(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	batches, err := h.displayService.ListDailyBatches(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "QUERY_FAILED", Message: err.Error()}})
		return
	}
	items := make([]*model.DailyDisplayBatchResponse, 0, len(batches))
	for _, batch := range batches {
		resp := h.toDailyBatchResponse(batch)
		items = append(items, &resp)
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "Success", Data: model.DailyDisplayBatchListResponse{Items: items}})
}

func (h *DisplayHandler) GetRenderProfiles(c *gin.Context) {
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "Success", Data: h.displayService.GetRenderProfiles()})
}

func (h *DisplayHandler) GetDailyDisplayPreview(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "Invalid item ID"}})
		return
	}
	item, err := h.displayService.GetDailyDisplayItem(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, model.Response{Success: false, Error: &model.ErrorInfo{Code: "NOT_FOUND", Message: "Item not found"}})
		return
	}
	fullPath := filepath.Join(util.DisplayBatchRoot(h.cfg.Photos.ThumbnailPath), item.PreviewJPGPath)
	c.Header("Content-Type", "image/jpeg")
	c.File(fullPath)
}

func (h *DisplayHandler) GetDailyDisplayAssetBin(c *gin.Context) {
	h.serveDisplayAssetFile(c, "bin")
}

func (h *DisplayHandler) GetDailyDisplayAssetPreview(c *gin.Context) {
	h.serveDisplayAssetFile(c, "preview")
}

func (h *DisplayHandler) GetDailyDisplayAssetHeader(c *gin.Context) {
	h.serveDisplayAssetFile(c, "header")
}

func (h *DisplayHandler) serveDisplayAssetFile(c *gin.Context, kind string) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "Invalid asset ID"}})
		return
	}
	asset, err := h.displayService.GetDailyDisplayAsset(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, model.Response{Success: false, Error: &model.ErrorInfo{Code: "NOT_FOUND", Message: "Asset not found"}})
		return
	}

	var fullPath, contentType string
	switch kind {
	case "header":
		fullPath = filepath.Join(util.DisplayBatchRoot(h.cfg.Photos.ThumbnailPath), asset.HeaderPath)
		contentType = "text/plain; charset=utf-8"
	case "preview":
		if asset.DitherPreviewPath == "" {
			c.JSON(http.StatusNotFound, model.Response{Success: false, Error: &model.ErrorInfo{Code: "NOT_FOUND", Message: "Dither preview not found"}})
			return
		}
		fullPath = filepath.Join(util.DisplayBatchRoot(h.cfg.Photos.ThumbnailPath), asset.DitherPreviewPath)
		contentType = "image/jpeg"
	default:
		fullPath = filepath.Join(util.DisplayBatchRoot(h.cfg.Photos.ThumbnailPath), asset.BinPath)
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.File(fullPath)
}

func (h *DisplayHandler) toDailyBatchResponse(batch *model.DailyDisplayBatch) model.DailyDisplayBatchResponse {
	resp := model.DailyDisplayBatchResponse{
		ID:               batch.ID,
		BatchDate:        batch.BatchDate,
		Status:           batch.Status,
		ItemCount:        batch.ItemCount,
		CanvasTemplate:   batch.CanvasTemplate,
		StrategySnapshot: batch.StrategySnapshot,
		ErrorMessage:     batch.ErrorMessage,
		GeneratedAt:      batch.GeneratedAt,
		UpdatedAt:        batch.UpdatedAt,
	}
	if len(batch.Items) == 0 {
		return resp
	}
	resp.Items = make([]model.DailyDisplayItemResponse, 0, len(batch.Items))
	for _, item := range batch.Items {
		itemResp := model.DailyDisplayItemResponse{
			ID:              item.ID,
			Sequence:        item.Sequence,
			PhotoID:         item.PhotoID,
			PreviewURL:      fmt.Sprintf("/api/v1/display/items/%d/preview", item.ID),
			CurationChannel: item.CurationChannel,
			Photo:           &item.Photo,
		}
		if len(item.Assets) > 0 {
			itemResp.Assets = make([]model.DailyDisplayAssetResponse, 0, len(item.Assets))
			for _, asset := range item.Assets {
				itemResp.Assets = append(itemResp.Assets, model.DailyDisplayAssetResponse{
					ID:            asset.ID,
					RenderProfile: asset.RenderProfile,
					BinURL:        fmt.Sprintf("/api/v1/display/assets/%d/bin", asset.ID),
					HeaderURL:     fmt.Sprintf("/api/v1/display/assets/%d/header", asset.ID),
					Checksum:      asset.Checksum,
					FileSize:      asset.FileSize,
				})
				if asset.DitherPreviewPath != "" {
					itemResp.Assets[len(itemResp.Assets)-1].DitherPreviewURL = fmt.Sprintf("/api/v1/display/assets/%d/preview", asset.ID)
				}
			}
		}
		resp.Items = append(resp.Items, itemResp)
	}
	return resp
}
