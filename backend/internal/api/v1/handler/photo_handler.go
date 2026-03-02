package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// PhotoHandler 照片处理器
type PhotoHandler struct {
	photoService  service.PhotoService
	configService service.ConfigService
}

// NewPhotoHandler 创建照片处理器
func NewPhotoHandler(photoService service.PhotoService, configService service.ConfigService) *PhotoHandler {
	return &PhotoHandler{
		photoService:  photoService,
		configService: configService,
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

	// If no path provided, load from config
	scanPath := req.Path
	var scanPathID string

	if scanPath == "" {
		// Load scan paths from config
		pathConfig, pathID, err := h.getDefaultScanPath(c)
		if err != nil {
			logger.Errorf("Failed to get default scan path: %v", err)
			c.JSON(http.StatusBadRequest, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "NO_DEFAULT_PATH",
					Message: err.Error(),
				},
			})
			return
		}
		scanPath = pathConfig
		scanPathID = pathID
	}

	// Validate path
	if err := validateScanPath(scanPath); err != nil {
		logger.Errorf("Invalid scan path: %v", err)
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_PATH",
				Message: "Invalid scan path: " + err.Error(),
			},
		})
		return
	}

	// 扫描照片
	resp, err := h.photoService.ScanPhotos(scanPath)
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

	// Update last scanned timestamp if using config path
	if scanPathID != "" {
		if err := h.updateLastScannedAt(c, scanPathID); err != nil {
			logger.Warnf("Failed to update last scanned timestamp: %v", err)
			// Don't fail the scan, just log warning
		}
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    resp,
	})
}

// getDefaultScanPath retrieves the default scan path from config
func (h *PhotoHandler) getDefaultScanPath(c *gin.Context) (string, string, error) {
	// Get config
	configValue, err := h.configService.Get("photos.scan_paths")
	if err != nil {
		return "", "", fmt.Errorf("no scan paths configured. Please configure scan paths in Settings")
	}

	// Parse JSON
	var pathsConfig model.ScanPathsConfig
	if err := json.Unmarshal([]byte(configValue.Value), &pathsConfig); err != nil {
		return "", "", fmt.Errorf("invalid scan paths configuration")
	}

	// Find default enabled path
	for _, p := range pathsConfig.Paths {
		if p.IsDefault && p.Enabled {
			return p.Path, p.ID, nil
		}
	}

	// Fallback to first enabled path
	for _, p := range pathsConfig.Paths {
		if p.Enabled {
			return p.Path, p.ID, nil
		}
	}

	return "", "", fmt.Errorf("no enabled scan path found. Please enable at least one path in Settings")
}

// updateLastScannedAt updates the last scanned timestamp for a path
func (h *PhotoHandler) updateLastScannedAt(c *gin.Context, pathID string) error {
	// Get config
	configValue, err := h.configService.Get("photos.scan_paths")
	if err != nil {
		return err
	}

	// Parse JSON
	var pathsConfig model.ScanPathsConfig
	if err := json.Unmarshal([]byte(configValue.Value), &pathsConfig); err != nil {
		return err
	}

	// Find and update path
	now := time.Now()
	found := false
	for i := range pathsConfig.Paths {
		if pathsConfig.Paths[i].ID == pathID {
			pathsConfig.Paths[i].LastScannedAt = &now
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("path not found")
	}

	// Save back
	updatedJSON, err := json.Marshal(pathsConfig)
	if err != nil {
		return err
	}

	return h.configService.Set("photos.scan_paths", string(updatedJSON))
}

// validateScanPath validates that a path exists and is readable
func validateScanPath(path string) error {
	// Check if path exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("path does not exist")
		}
		return fmt.Errorf("cannot access path: %w", err)
	}

	// Check if it's a directory
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}

	// Check read permissions by attempting to open
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("path is not readable: %w", err)
	}
	defer file.Close()

	return nil
}

// ValidatePath validates a scan path
// @Summary Validate scan path
// @Description Validates if a path exists and is accessible
// @Tags photos
// @Accept json
// @Produce json
// @Param request body model.ValidatePathRequest true "Path to validate"
// @Success 200 {object} model.Response{data=model.ValidatePathResponse}
// @Router /api/v1/photos/validate-path [post]
func (h *PhotoHandler) ValidatePath(c *gin.Context) {
	var req model.ValidatePathRequest
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

	resp := model.ValidatePathResponse{
		Valid: true,
	}

	if err := validateScanPath(req.Path); err != nil {
		resp.Valid = false
		resp.Error = err.Error()
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
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

	// 对每张照片进行实时地理编码（如果需要）
	for _, photo := range photos {
		if err := h.photoService.GeocodePhotoIfNeeded(photo); err != nil {
			logger.Warnf("Real-time geocoding failed for photo %d: %v", photo.ID, err)
			// 不阻止返回，继续显示照片
		}
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

	// 进行实时地理编码（如果需要）
	if err := h.photoService.GeocodePhotoIfNeeded(photo); err != nil {
		logger.Warnf("Real-time geocoding failed for photo %d: %v", photo.ID, err)
		// 不阻止返回，继续显示照片
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    photo,
	})
}

// GetPhotoImage 获取照片文件
// @Summary 获取照片文件
// @Description 返回照片的原始文件或缩略图，自动处理 HEIC 格式转换
// @Tags photos
// @Accept json
// @Produce image/jpeg
// @Param id path int true "照片 ID"
// @Param thumbnail query bool false "是否返回缩略图" default(false)
// @Success 200 {file} binary
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/{id}/image [get]
func (h *PhotoHandler) GetPhotoImage(c *gin.Context) {
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

	// 检查是否是 HEIC/HEIF 格式
	ext := strings.ToLower(filepath.Ext(photo.FilePath))
	if ext == ".heic" || ext == ".heif" {
		// 尝试使用 sips 转换 HEIC 到 JPEG（macOS 自带工具）
		// 创建临时文件路径
		tempJpeg := "/tmp/relive_" + idStr + ".jpg"

		// 使用 sips 转换
		cmd := exec.Command("sips", "-s", "format", "jpeg", photo.FilePath, "--out", tempJpeg)
		if err := cmd.Run(); err != nil {
			logger.Warnf("Failed to convert HEIC to JPEG with sips: %v, trying direct serve", err)
			// 如果转换失败，尝试直接返回原文件（某些浏览器可能支持）
			c.File(photo.FilePath)
			return
		}

		// 返回转换后的 JPEG
		c.Header("Content-Type", "image/jpeg")
		c.File(tempJpeg)
		return
	}

	// 其他格式直接返回
	c.File(photo.FilePath)
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
