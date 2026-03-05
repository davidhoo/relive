package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
)

// PhotoHandler 照片处理器
type PhotoHandler struct {
	photoService  service.PhotoService
	configService service.ConfigService
	cfg           *config.Config
}

// NewPhotoHandler 创建照片处理器
func NewPhotoHandler(photoService service.PhotoService, configService service.ConfigService, cfg *config.Config) *PhotoHandler {
	return &PhotoHandler{
		photoService:  photoService,
		configService: configService,
		cfg:           cfg,
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
	} else {
		// Find pathID by path
		scanPathID = h.findPathIDByPath(c, scanPath)
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

// RebuildPhotos 重建照片（重新扫描文件、提取 EXIF、计算哈希、地理编码、生成缩略图）
// @Summary 重建照片
// @Description 重建指定目录的照片，包括：重新扫描文件、提取 EXIF、计算文件哈希、地理编码、生成缩略图（保留 AI 分析结果）
// @Tags photos
// @Accept json
// @Produce json
// @Param request body model.ScanPhotosRequest true "扫描请求"
// @Success 200 {object} model.Response{data=model.ScanPhotosResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/rebuild [post]
func (h *PhotoHandler) RebuildPhotos(c *gin.Context) {
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
	} else {
		// Find pathID by path
		scanPathID = h.findPathIDByPath(c, scanPath)
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

	// 重建照片
	resp, err := h.photoService.RebuildPhotos(scanPath)
	if err != nil {
		logger.Errorf("Rebuild photos failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "REBUILD_FAILED",
				Message: "Failed to rebuild photos: " + err.Error(),
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

// CleanupPhotos 清理数据库中所有文件已不存在的照片
// @Summary 清理不存在文件的照片
// @Description 遍历整个数据库，检查每个照片文件是否还存在，不存在的则软删除
// @Tags photos
// @Accept json
// @Produce json
// @Success 200 {object} model.Response{data=model.CleanupPhotosResponse}
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/cleanup [post]
func (h *PhotoHandler) CleanupPhotos(c *gin.Context) {
	logger.Info("Cleanup photos request received")

	// 清理不存在文件的照片
	resp, err := h.photoService.CleanupNonExistentPhotos()
	if err != nil {
		logger.Errorf("Cleanup photos failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "CLEANUP_FAILED",
				Message: "Failed to cleanup photos: " + err.Error(),
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

// findPathIDByPath finds pathID by path string
func (h *PhotoHandler) findPathIDByPath(c *gin.Context, path string) string {
	// Get config
	configValue, err := h.configService.Get("photos.scan_paths")
	if err != nil {
		return ""
	}

	// Parse JSON
	var pathsConfig model.ScanPathsConfig
	if err := json.Unmarshal([]byte(configValue.Value), &pathsConfig); err != nil {
		return ""
	}

	// Find path by path string
	for _, p := range pathsConfig.Paths {
		if p.Path == path {
			return p.ID
		}
	}

	return ""
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

// ListDirectories 列出目录内容
// @Summary 列出目录内容
// @Description 列出指定目录下的所有子目录（用于路径选择器）
// @Tags photos
// @Accept json
// @Produce json
// @Param request body model.ListDirectoriesRequest true "目录路径"
// @Success 200 {object} model.Response{data=model.ListDirectoriesResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/list-directories [post]
func (h *PhotoHandler) ListDirectories(c *gin.Context) {
	var req model.ListDirectoriesRequest
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

	// 确保路径是绝对路径
	path := req.Path
	if !filepath.IsAbs(path) {
		// 如果是相对路径，尝试从配置的基础路径解析
		path = filepath.Join(h.cfg.Photos.RootPath, path)
	}

	// 读取目录内容
	entries, err := os.ReadDir(path)
	if err != nil {
		logger.Errorf("Failed to read directory %s: %v", path, err)
		c.JSON(http.StatusOK, model.Response{
			Success: true,
			Data: model.ListDirectoriesResponse{
				Entries:     []model.DirectoryEntry{},
				CurrentPath: req.Path,
			},
		})
		return
	}

	// 获取父目录
	parentPath := filepath.Dir(path)
	if parentPath == path {
		parentPath = "" // 根目录没有父目录
	}

	// 构建响应
	var dirEntries []model.DirectoryEntry

	// 如果不是根目录，添加返回上级选项
	if parentPath != "" && parentPath != path {
		dirEntries = append(dirEntries, model.DirectoryEntry{
			Name:  "..",
			Path:  parentPath,
			IsDir: true,
		})
	}

	for _, entry := range entries {
		// 只显示目录
		if entry.IsDir() {
			// 跳过隐藏目录
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}

			fullPath := filepath.Join(path, name)
			dirEntries = append(dirEntries, model.DirectoryEntry{
				Name:  name,
				Path:  fullPath,
				IsDir: true,
			})
		}
	}

	resp := model.ListDirectoriesResponse{
		Entries:     dirEntries,
		ParentPath:  parentPath,
		CurrentPath: path,
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
// @Param search query string false "搜索关键词（路径、设备ID、标签）"
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
	search := c.Query("search")
	sortBy := c.DefaultQuery("sort_by", "taken_at")
	sortDesc := c.DefaultQuery("sort_desc", "true") == "true"

	// 构建请求
	req := &model.GetPhotosRequest{
		Page:     page,
		PageSize: pageSize,
		Analyzed: analyzed,
		Location: location,
		Search:   search,
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
		// 使用配置的缩略图路径
		thumbnailPath := h.cfg.Photos.ThumbnailPath
		if thumbnailPath == "" {
			thumbnailPath = "./data/thumbnails"
		}

		// 生成缩略图文件路径（使用分目录存储，避免单目录文件过多）
		// ID 12345 -> 十六进制 0x3039 -> 路径 thumbnails/30/39/12345.jpg
		idNum, _ := strconv.ParseUint(idStr, 10, 64)
		hexStr := fmt.Sprintf("%04x", idNum)
		subDir1 := hexStr[0:2]
		subDir2 := hexStr[2:4]
		thumbnailDir := filepath.Join(thumbnailPath, subDir1, subDir2)
		thumbnailFile := filepath.Join(thumbnailDir, idStr+".jpg")

		// 确保缩略图目录存在
		if err := os.MkdirAll(thumbnailDir, 0755); err != nil {
			logger.Warnf("Failed to create thumbnail directory: %v, trying direct serve", err)
			c.File(photo.FilePath)
			return
		}

		// 如果缩略图文件已存在，直接返回
		if _, err := os.Stat(thumbnailFile); err == nil {
			c.Header("Content-Type", "image/jpeg")
			c.File(thumbnailFile)
			return
		}

		// 使用 imaging 库转换 HEIC 到 JPEG（跨平台，支持 Docker）
		img, err := util.OpenImage(photo.FilePath)
		if err != nil {
			logger.Warnf("Failed to open HEIC image %s: %v, trying direct serve", photo.FilePath, err)
			c.File(photo.FilePath)
			return
		}

		// 保存为 JPEG
		if err := imaging.Save(img, thumbnailFile, imaging.JPEGQuality(85)); err != nil {
			logger.Warnf("Failed to save HEIC as JPEG %s: %v, trying direct serve", thumbnailFile, err)
			c.File(photo.FilePath)
			return
		}

		// 返回转换后的 JPEG
		c.Header("Content-Type", "image/jpeg")
		c.File(thumbnailFile)
		return
	}

	// 其他格式直接返回
	c.File(photo.FilePath)
}

// GetPhotoThumbnail 获取照片缩略图
// @Summary 获取照片缩略图
// @Description 返回预生成的缩略图，如果没有则返回原图
// @Tags photos
// @Accept json
// @Produce image/jpeg
// @Param id path int true "照片 ID"
// @Success 200 {file} binary
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/{id}/thumbnail [get]
func (h *PhotoHandler) GetPhotoThumbnail(c *gin.Context) {
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

	// 如果有预生成的缩略图，直接返回
	if photo.ThumbnailPath != "" {
		thumbnailFullPath := filepath.Join(h.cfg.Photos.ThumbnailPath, photo.ThumbnailPath)
		if _, err := os.Stat(thumbnailFullPath); err == nil {
			c.Header("Content-Type", "image/jpeg")
			c.File(thumbnailFullPath)
			return
		}
		// 缩略图文件不存在，记录警告并回退到原图
		logger.Warnf("Thumbnail file not found: %s, falling back to original", thumbnailFullPath)
	}

	// 没有缩略图或缩略图不存在，返回原图
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

// GetCategories 获取所有照片分类
// @Summary 获取所有照片分类
// @Description 获取系统中所有不重复的照片分类
// @Tags photos
// @Accept json
// @Produce json
// @Success 200 {object} model.Response{data=[]string}
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/categories [get]
func (h *PhotoHandler) GetCategories(c *gin.Context) {
	categories, err := h.photoService.GetCategories()
	if err != nil {
		logger.Errorf("Get categories failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get categories",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    categories,
	})
}

// GetTags 获取所有照片标签
// @Summary 获取所有照片标签
// @Description 获取系统中所有不重复的照片标签
// @Tags photos
// @Accept json
// @Produce json
// @Success 200 {object} model.Response{data=[]string}
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/tags [get]
func (h *PhotoHandler) GetTags(c *gin.Context) {
	tags, err := h.photoService.GetTags()
	if err != nil {
		logger.Errorf("Get tags failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get tags",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    tags,
	})
}

// CountPhotosByPaths 按路径统计照片数量
// @Summary 按路径统计照片数量
// @Description 统计多个扫描路径下的照片数量
// @Tags photos
// @Accept json
// @Produce json
// @Param request body model.CountPhotosByPathsRequest true "路径列表"
// @Success 200 {object} model.Response{data=model.CountPhotosByPathsResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/count-by-paths [post]
func (h *PhotoHandler) CountPhotosByPaths(c *gin.Context) {
	var req model.CountPhotosByPathsRequest
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

	counts := make(map[string]int64)
	for _, path := range req.Paths {
		count, err := h.photoService.CountPhotosByPathPrefix(path)
		if err != nil {
			logger.Errorf("Count photos by path prefix failed: %s, error: %v", path, err)
			counts[path] = 0
			continue
		}
		counts[path] = count
	}

	resp := model.CountPhotosByPathsResponse{
		Counts: counts,
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    resp,
	})
}

// StartScan 启动异步扫描任务
// @Summary 启动异步扫描任务
// @Description 启动后台扫描任务，立即返回任务 ID，通过 GetScanTask 查询进度
// @Tags photos
// @Accept json
// @Produce json
// @Param request body model.StartScanRequest true "扫描请求"
// @Success 200 {object} model.Response{data=model.StartScanResponse}
// @Failure 400 {object} model.Response
// @Failure 409 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/scan/async [post]
func (h *PhotoHandler) StartScan(c *gin.Context) {
	var req model.StartScanRequest
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

	task, err := h.photoService.StartScan(req.Path)
	if err != nil {
		if err.Error() == "scan task already running" {
			c.JSON(http.StatusConflict, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "TASK_RUNNING",
					Message: "扫描任务正在运行中",
				},
			})
			return
		}
		logger.Errorf("Start scan failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "START_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	resp := model.StartScanResponse{
		TaskID: task.ID,
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "扫描任务已启动",
		Data:    resp,
	})
}

// StartRebuild 启动异步重建任务
// @Summary 启动异步重建任务
// @Description 启动后台重建任务，立即返回任务 ID，通过 GetScanTask 查询进度
// @Tags photos
// @Accept json
// @Produce json
// @Param request body model.StartScanRequest true "重建请求"
// @Success 200 {object} model.Response{data=model.StartScanResponse}
// @Failure 400 {object} model.Response
// @Failure 409 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/rebuild/async [post]
func (h *PhotoHandler) StartRebuild(c *gin.Context) {
	var req model.StartScanRequest
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

	task, err := h.photoService.StartRebuild(req.Path)
	if err != nil {
		if err.Error() == "scan task already running" {
			c.JSON(http.StatusConflict, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "TASK_RUNNING",
					Message: "重建任务正在运行中",
				},
			})
			return
		}
		logger.Errorf("Start rebuild failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "START_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	resp := model.StartScanResponse{
		TaskID: task.ID,
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "重建任务已启动",
		Data:    resp,
	})
}

// GetScanTask 获取当前扫描任务状态
// @Summary 获取当前扫描任务状态
// @Description 查询当前正在运行或最后完成的扫描任务状态
// @Tags photos
// @Accept json
// @Produce json
// @Success 200 {object} model.Response{data=model.GetScanProgressResponse}
// @Router /api/v1/photos/scan/task [get]
func (h *PhotoHandler) GetScanTask(c *gin.Context) {
	task := h.photoService.GetScanTask()

	isRunning := task != nil && task.IsRunning()

	resp := model.GetScanProgressResponse{
		Task:      task,
		IsRunning: isRunning,
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    resp,
	})
}
