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
	photoService       service.PhotoService
	thumbnailService   service.ThumbnailService
	geocodeTaskService service.GeocodeTaskService
	configService      service.ConfigService
	cfg                *config.Config
}

// NewPhotoHandler 创建照片处理器
func NewPhotoHandler(photoService service.PhotoService, thumbnailService service.ThumbnailService, geocodeTaskService service.GeocodeTaskService, configService service.ConfigService, cfg *config.Config) *PhotoHandler {
	return &PhotoHandler{
		photoService:       photoService,
		thumbnailService:   thumbnailService,
		geocodeTaskService: geocodeTaskService,
		configService:      configService,
		cfg:                cfg,
	}
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
	now := time.Now().Format(time.RFC3339)
	found := false
	for i := range pathsConfig.Paths {
		if pathsConfig.Paths[i].ID == pathID {
			pathsConfig.Paths[i].LastScannedAt = now
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
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "READ_DIRECTORY_FAILED",
				Message: fmt.Sprintf("failed to read directory %s: %v", path, err),
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

	var hasThumbnail *bool
	if str := c.Query("has_thumbnail"); str != "" {
		val := str == "true"
		hasThumbnail = &val
	}

	var hasGPS *bool
	if str := c.Query("has_gps"); str != "" {
		val := str == "true"
		hasGPS = &val
	}

	location := c.Query("location")
	search := c.Query("search")
	category := c.Query("category")
	tag := c.Query("tag")
	sortBy := c.DefaultQuery("sort_by", "taken_at")
	sortDesc := c.DefaultQuery("sort_desc", "true") == "true"
	status := c.Query("status") // active(默认)/excluded/all

	// 构建请求
	req := &model.GetPhotosRequest{
		Page:         page,
		PageSize:     pageSize,
		Analyzed:     analyzed,
		HasThumbnail: hasThumbnail,
		HasGPS:       hasGPS,
		Location:     location,
		Search:       search,
		Category:     category,
		Tag:          tag,
		SortBy:       sortBy,
		SortDesc:     sortDesc,
		Status:       status,
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

	if h.geocodeTaskService != nil {
		for _, photo := range photos {
			if photo.GPSLatitude == nil || photo.GPSLongitude == nil || strings.TrimSpace(photo.Location) != "" {
				continue
			}
			if err := h.geocodeTaskService.EnqueuePhoto(photo.ID, model.GeocodeJobSourcePassive, 100, false); err != nil {
				logger.Warnf("Passive geocode enqueue failed for photo %d: %v", photo.ID, err)
			}
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

	if h.geocodeTaskService != nil && photo.GPSLatitude != nil && photo.GPSLongitude != nil && strings.TrimSpace(photo.Location) == "" {
		if err := h.geocodeTaskService.EnqueuePhoto(photo.ID, model.GeocodeJobSourcePassive, 100, false); err != nil {
			logger.Warnf("Passive geocode enqueue failed for photo %d: %v", photo.ID, err)
		}
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
			if !util.ShouldRefreshThumbnailCache(photo.FilePath, thumbnailFile) {
				c.Header("Content-Type", "image/jpeg")
				c.File(thumbnailFile)
				return
			}
			_ = os.Remove(thumbnailFile)
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

func buildPhotoDisplayText(photo *model.Photo) (string, string) {
	title := strings.TrimSpace(photo.Caption)
	if title == "" {
		title = strings.TrimSuffix(photo.FileName, filepath.Ext(photo.FileName))
	}
	parts := make([]string, 0, 2)
	if photo.TakenAt != nil {
		parts = append(parts, photo.TakenAt.In(time.Local).Format("2006.01.02"))
	}
	if location := strings.TrimSpace(photo.Location); location != "" {
		parts = append(parts, location)
	}
	return title, strings.Join(parts, " · ")
}

// GetPhotoDevicePreview 获取照片设备预览图（实时缓存生成）
// @Summary 获取照片设备预览图
// @Description 按指定渲染规格实时生成 480x800 设备预览并返回缓存文件
// @Tags photos
// @Accept json
// @Produce image/jpeg
// @Param id path int true "照片 ID"
// @Param profile query string false "渲染规格名称"
// @Success 200 {file} binary
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/{id}/device-preview [get]
func (h *PhotoHandler) GetPhotoDevicePreview(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "Invalid photo ID"}})
		return
	}

	photo, err := h.photoService.GetPhotoByID(uint(id))
	if err != nil {
		logger.Errorf("Get photo by ID failed: %v", err)
		c.JSON(http.StatusNotFound, model.Response{Success: false, Error: &model.ErrorInfo{Code: "NOT_FOUND", Message: "Photo not found"}})
		return
	}

	profileName := c.Query("profile")
	if profileName == "" {
		profileName = util.DefaultRenderProfile()
	}
	previewPath, err := h.generateDevicePreviewFile(photo, profileName)
	if err != nil {
		logger.Errorf("Generate device preview failed for photo %d: %v", photo.ID, err)
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "DEVICE_PREVIEW_FAILED", Message: "Failed to generate device preview"}})
		return
	}

	c.Header("Content-Type", "image/jpeg")
	c.Header("Cache-Control", "private, max-age=86400")
	c.File(previewPath)
}

// GetPhotoFramePreview 获取相框预览图
// @Summary 获取相框预览图
// @Description 返回为相框预览生成的 480x640 JPEG 图片
// @Tags photos
// @Accept json
// @Produce image/jpeg
// @Param id path int true "照片 ID"
// @Success 200 {file} binary
// @Failure 400 {object} model.Response
// @Failure 404 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/{id}/frame-preview [get]
func (h *PhotoHandler) GetPhotoFramePreview(c *gin.Context) {
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

	framePreviewPath, err := h.generateFramePreviewFile(photo, 480, 640)
	if err != nil {
		logger.Errorf("Generate frame preview failed for photo %d: %v", photo.ID, err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "FRAME_PREVIEW_FAILED",
				Message: "Failed to generate frame preview",
			},
		})
		return
	}

	c.Header("Content-Type", "image/jpeg")
	c.Header("Cache-Control", "private, max-age=86400")
	c.File(framePreviewPath)
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
			if !util.ShouldRefreshThumbnailCacheWithOrientation(photo.FilePath, thumbnailFullPath, photo.Orientation) {
				c.Header("Content-Type", "image/jpeg")
				c.Header("Cache-Control", "no-cache")
				c.File(thumbnailFullPath)
				return
			}
		}
		logger.Warnf("Thumbnail file unavailable or stale: %s, enqueue passive generation", thumbnailFullPath)
	}

	if h.thumbnailService != nil {
		if err := h.thumbnailService.EnqueuePhoto(photo.ID, model.ThumbnailJobSourcePassive, 100, false); err != nil {
			logger.Warnf("Passive thumbnail enqueue failed for photo %d: %v", photo.ID, err)
		}
	}

	// 没有缩略图或缩略图不存在，返回原图
	c.File(photo.FilePath)
}

func (h *PhotoHandler) generateFramePreviewFile(photo *model.Photo, targetWidth, targetHeight int) (string, error) {
	fileInfo, err := os.Stat(photo.FilePath)
	if err != nil {
		return "", fmt.Errorf("stat photo file: %w", err)
	}

	cacheKey := fmt.Sprintf(
		"frame-preview:v3:%s:%d:%d:%dx%d",
		photo.FilePath,
		fileInfo.Size(),
		fileInfo.ModTime().UnixNano(),
		targetWidth,
		targetHeight,
	)
	cachePath := filepath.Join(
		h.getThumbnailRoot(),
		"frame-previews",
		util.GenerateDerivedImagePath(cacheKey),
	)

	if _, err := os.Stat(cachePath); err == nil {
		return cachePath, nil
	}

	img, err := util.OpenImage(photo.FilePath)
	if err != nil {
		return "", fmt.Errorf("open image: %w", err)
	}

	orientation := photo.Orientation
	if exifData, exifErr := util.ExtractEXIF(photo.FilePath); exifErr != nil {
		logger.Debugf("Extract orientation for frame preview failed: %v", exifErr)
	} else if exifData != nil && exifData.Orientation > 0 {
		orientation = exifData.Orientation
	}

	if !util.IsHEIC(photo.FilePath) {
		img = util.NormalizeOrientation(img, orientation)
	}
	framePreview := util.GenerateFramePreview(img, targetWidth, targetHeight)

	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return "", fmt.Errorf("create frame preview directory: %w", err)
	}
	if err := imaging.Save(framePreview, cachePath, imaging.JPEGQuality(88)); err != nil {
		return "", fmt.Errorf("save frame preview: %w", err)
	}

	return cachePath, nil
}

func (h *PhotoHandler) generateDevicePreviewFile(photo *model.Photo, profileName string) (string, error) {
	profile, ok := util.GetRenderProfile(profileName)
	if !ok {
		return "", fmt.Errorf("unsupported render profile: %s", profileName)
	}

	fileInfo, err := os.Stat(photo.FilePath)
	if err != nil {
		return "", fmt.Errorf("stat photo file: %w", err)
	}

	title, subtitle := buildPhotoDisplayText(photo)
	cacheKey := fmt.Sprintf(
		"device-preview:v2:%s:%d:%d:%d:%s:%s:%s",
		photo.FilePath,
		fileInfo.Size(),
		fileInfo.ModTime().UnixNano(),
		photo.UpdatedAt.UnixNano(),
		profile.Name,
		title,
		subtitle,
	)

	cacheJPG := util.GenerateDerivedImagePath(cacheKey)
	baseName := strings.TrimSuffix(filepath.Base(cacheJPG), filepath.Ext(cacheJPG))
	cacheDir := filepath.Join(h.getThumbnailRoot(), "device-previews", filepath.Dir(cacheJPG))
	ditherPreviewPath := filepath.Join(cacheDir, baseName+"-preview.jpg")
	previewPath := filepath.Join(cacheDir, baseName+"-frame.jpg")
	binPath := filepath.Join(cacheDir, baseName+".bin")
	headerPath := filepath.Join(cacheDir, baseName+".h")

	if _, err := os.Stat(ditherPreviewPath); err == nil {
		return ditherPreviewPath, nil
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create device preview directory: %w", err)
	}

	if err := util.GenerateDisplayPreview(photo.FilePath, previewPath, profile.Width, profile.Height, title, subtitle); err != nil {
		return "", fmt.Errorf("generate display preview: %w", err)
	}

	canvas, err := util.BuildDisplayCanvas(photo.FilePath, profile.Width, profile.Height, title, subtitle)
	if err != nil {
		return "", fmt.Errorf("build display canvas: %w", err)
	}
	if _, _, err := util.BuildRenderArtifacts(canvas, profile, ditherPreviewPath, binPath, headerPath); err != nil {
		return "", fmt.Errorf("build render artifacts: %w", err)
	}
	return ditherPreviewPath, nil
}

func (h *PhotoHandler) getThumbnailRoot() string {
	if h.cfg.Photos.ThumbnailPath != "" {
		return h.cfg.Photos.ThumbnailPath
	}
	return "./data/thumbnails"
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

// GetTags 获取热门标签（支持搜索）
// @Summary 获取热门标签
// @Description 获取按照片数量排序的热门标签，支持关键词搜索
// @Tags photos
// @Accept json
// @Produce json
// @Param q query string false "搜索关键词"
// @Param limit query int false "返回数量限制" default(15)
// @Success 200 {object} model.Response{data=model.TagsResponse}
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/tags [get]
func (h *PhotoHandler) GetTags(c *gin.Context) {
	query := c.Query("q")
	limit := 15
	if limitStr := c.Query("limit"); limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			if v > 50 {
				v = 50
			}
			limit = v
		}
	}

	tags, total, err := h.photoService.GetTags(query, limit)
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
		Data:    model.TagsResponse{Items: tags, Total: total},
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

// GetPhotoCounts 获取照片按状态计数
func (h *PhotoHandler) GetPhotoCounts(c *gin.Context) {
	counts, err := h.photoService.CountByStatus()
	if err != nil {
		logger.Errorf("Count photos by status failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "QUERY_FAILED",
				Message: "Failed to get photo counts",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    counts,
	})
}

// CountDerivedStatusByPaths 按路径统计缩略图与 GPS 派生状态
func (h *PhotoHandler) CountDerivedStatusByPaths(c *gin.Context) {
	var req model.CountDerivedStatusByPathsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}
	statsMap, err := h.photoService.GetPathDerivedStatusBatch(req.Paths)
	if err != nil {
		logger.Errorf("Batch count derived status failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "QUERY_FAILED", Message: "Failed to get derived status"}})
		return
	}
	// 转换为值类型 map
	stats := make(map[string]model.PathDerivedStatus, len(statsMap))
	for k, v := range statsMap {
		if v != nil {
			stats[k] = *v
		}
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "Success", Data: model.CountDerivedStatusByPathsResponse{Stats: stats}})
}

// BatchUpdateStatus 批量更新照片状态（排除/恢复）
// @Summary 批量更新照片状态
// @Description 批量将照片标记为排除或恢复为正常状态
// @Tags photos
// @Accept json
// @Produce json
// @Param request body model.BatchUpdateStatusRequest true "批量更新状态请求"
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/batch-status [patch]
func (h *PhotoHandler) BatchUpdateStatus(c *gin.Context) {
	var req model.BatchUpdateStatusRequest
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

	affected, err := h.photoService.BatchUpdateStatus(&req)
	if err != nil {
		logger.Errorf("Batch update status failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "UPDATE_FAILED",
				Message: "Failed to update photo status: " + err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: fmt.Sprintf("Successfully updated %d photos", affected),
		Data:    map[string]int64{"affected": affected},
	})
}

// SetManualLocation 手动设置照片位置
// @Summary 手动设置照片 GPS 位置
// @Description 手动指定照片的经纬度坐标，后端自动反向解析填充结构化位置字段
// @Tags photos
// @Accept json
// @Produce json
// @Param id path int true "照片 ID"
// @Param request body model.SetManualLocationRequest true "位置坐标"
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/{id}/location [patch]
func (h *PhotoHandler) SetManualLocation(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "Invalid photo ID"},
		})
		return
	}

	var req model.SetManualLocationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()},
		})
		return
	}

	if h.geocodeTaskService == nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "SERVICE_UNAVAILABLE", Message: "Geocode service not available"},
		})
		return
	}

	location, err := h.geocodeTaskService.SetManualLocation(uint(id), req.Latitude, req.Longitude)
	if err != nil {
		logger.Errorf("Set manual location failed for photo %d: %v", id, err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "UPDATE_FAILED", Message: err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Location updated successfully",
		Data:    map[string]string{"location": location},
	})
}

// UpdateCategory 更新照片分类
// @Summary 更新照片分类
// @Description 更新指定 ID 照片的主分类
// @Tags photos
// @Accept json
// @Produce json
// @Param id path int true "照片 ID"
// @Param request body model.UpdateCategoryRequest true "更新分类请求"
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/photos/{id}/category [patch]
func (h *PhotoHandler) UpdateCategory(c *gin.Context) {
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

	// 解析请求体
	var req model.UpdateCategoryRequest
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

	// 更新分类
	if err := h.photoService.UpdateCategory(uint(id), req.Category); err != nil {
		logger.Errorf("Update category failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "UPDATE_FAILED",
				Message: "Failed to update category: " + err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Category updated successfully",
	})
}

// UpdateOrientation 手动覆盖照片方向
func (h *PhotoHandler) UpdateOrientation(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "Invalid photo ID"},
		})
		return
	}

	var req model.UpdateOrientationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "orientation must be 1-8"},
		})
		return
	}

	if err := h.photoService.UpdateOrientation(uint(id), req.Orientation); err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "UPDATE_FAILED", Message: err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Orientation updated, thumbnail regenerating",
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
	h.startScanTask(c, h.photoService.StartScan, "扫描")
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
	h.startScanTask(c, h.photoService.StartRebuild, "重建")
}

// startScanTask 扫描/重建任务的公共逻辑
func (h *PhotoHandler) startScanTask(c *gin.Context, startFn func(string) (*model.ScanTask, error), taskName string) {
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

	scanPath, _, err := h.resolveScanPath(req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_PATH",
				Message: err.Error(),
			},
		})
		return
	}

	task, err := startFn(scanPath)
	if err != nil {
		if err.Error() == "scan task already running" {
			c.JSON(http.StatusConflict, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "TASK_RUNNING",
					Message: taskName + "任务正在运行中",
				},
			})
			return
		}
		logger.Errorf("Start %s failed: %v", taskName, err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "START_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: taskName + "任务已启动",
		Data:    model.StartScanResponse{TaskID: task.ID},
	})
}

func (h *PhotoHandler) resolveScanPath(requestedPath string) (string, string, error) {
	scanPath := requestedPath
	var scanPathID string

	if scanPath == "" {
		var err error
		scanPath, scanPathID, err = h.getDefaultScanPath(nil)
		if err != nil {
			return "", "", err
		}
	} else {
		scanPathID = h.findPathIDByPath(nil, scanPath)
	}

	if err := validateScanPath(scanPath); err != nil {
		return "", "", fmt.Errorf("invalid scan path: %w", err)
	}

	return scanPath, scanPathID, nil
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

// StopScanTask 停止当前扫描/重建任务
// @Summary 停止当前扫描/重建任务
// @Description 请求后台任务优雅停止，等待当前文件处理结束
// @Tags photos
// @Accept json
// @Produce json
// @Param id path string true "任务 ID"
// @Success 200 {object} model.Response{data=model.StartScanResponse}
// @Failure 404 {object} model.Response
// @Failure 409 {object} model.Response
// @Router /api/v1/photos/tasks/{id}/stop [post]
func (h *PhotoHandler) StopScanTask(c *gin.Context) {
	taskID := c.Param("id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "INVALID_TASK_ID", Message: "任务 ID 不能为空"},
		})
		return
	}

	task, err := h.photoService.StopScanTask(taskID)
	if err != nil {
		statusCode := http.StatusConflict
		if strings.Contains(err.Error(), "not found") {
			statusCode = http.StatusNotFound
		}
		c.JSON(statusCode, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "STOP_FAILED", Message: err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "扫描任务停止请求已发送",
		Data:    task,
	})
}
