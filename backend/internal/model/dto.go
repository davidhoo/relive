package model

import "time"

// Response 统一响应格式
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorInfo  `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

// ErrorInfo 错误信息
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PagedResponse 分页响应
type PagedResponse struct {
	Items      interface{} `json:"items"`
	Total      int64       `json:"total"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalPages int         `json:"total_pages"`
}

// ScanPhotosRequest 扫描照片请求
type ScanPhotosRequest struct {
	Path string `json:"path" binding:"required"` // 扫描路径
}

// ScanPhotosResponse 扫描照片响应
type ScanPhotosResponse struct {
	ScannedCount int `json:"scanned_count"` // 扫描数量
	NewCount     int `json:"new_count"`     // 新增数量
	UpdatedCount int `json:"updated_count"` // 更新数量
}

// GetPhotosRequest 获取照片列表请求
type GetPhotosRequest struct {
	Page     int    `form:"page" binding:"omitempty,min=1"`
	PageSize int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	Analyzed *bool  `form:"analyzed"` // 是否已分析（可选）
	Location string `form:"location"` // 位置筛选（可选）
	SortBy   string `form:"sort_by"`  // 排序字段（taken_at/overall_score）
	SortDesc bool   `form:"sort_desc"` // 是否降序
}

// AIAnalyzeRequest AI 分析请求
type AIAnalyzeRequest struct {
	PhotoID int `json:"photo_id" binding:"required"` // 照片 ID
}

// AIAnalyzeResponse AI 分析响应
type AIAnalyzeResponse struct {
	PhotoID      int    `json:"photo_id"`
	Description  string `json:"description"`
	Caption      string `json:"caption"`
	MemoryScore  int    `json:"memory_score"`
	BeautyScore  int    `json:"beauty_score"`
	OverallScore int    `json:"overall_score"`
	MainCategory string `json:"main_category"`
	Tags         string `json:"tags"`
}

// GetDisplayPhotoRequest 获取展示照片请求
type GetDisplayPhotoRequest struct {
	DeviceID string `form:"device_id"` // 设备 ID（可选）
}

// GetDisplayPhotoResponse 获取展示照片响应
type GetDisplayPhotoResponse struct {
	PhotoID      uint      `json:"photo_id"`
	FilePath     string    `json:"file_path"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	TakenAt      time.Time `json:"taken_at"`
	Location     string    `json:"location"`
	MemoryScore  int       `json:"memory_score"`
	BeautyScore  int       `json:"beauty_score"`
	OverallScore int       `json:"overall_score"`
}

// ESP32RegisterRequest ESP32 注册请求
type ESP32RegisterRequest struct {
	DeviceID        string `json:"device_id" binding:"required"`
	Name            string `json:"name" binding:"required"`
	ScreenWidth     int    `json:"screen_width" binding:"required,min=1"`
	ScreenHeight    int    `json:"screen_height" binding:"required,min=1"`
	FirmwareVersion string `json:"firmware_version"`
	IPAddress       string `json:"ip_address"`
	MACAddress      string `json:"mac_address"`
}

// ESP32RegisterResponse ESP32 注册响应
type ESP32RegisterResponse struct {
	DeviceID string                 `json:"device_id"`
	APIKey   string                 `json:"api_key"`
	Config   map[string]interface{} `json:"config"`
}

// ESP32HeartbeatRequest ESP32 心跳请求
type ESP32HeartbeatRequest struct {
	DeviceID             string `json:"device_id" binding:"required"`
	BatteryLevel         int    `json:"battery_level"`
	WiFiRSSI             int    `json:"wifi_rssi"`
	FreeHeap             int    `json:"free_heap"`
	LastDisplayPhotoID   int    `json:"last_display_photo_id"`
	FirmwareVersion      string `json:"firmware_version"`
}

// ESP32HeartbeatResponse ESP32 心跳响应
type ESP32HeartbeatResponse struct {
	ServerTime          time.Time `json:"server_time"`
	NextRefreshInSeconds int       `json:"next_refresh_in_seconds"`
	HasNewFirmware      bool      `json:"has_new_firmware"`
}

// RecordDisplayRequest 上报展示记录请求
type RecordDisplayRequest struct {
	DeviceID  string `json:"device_id" binding:"required"`
	PhotoID   uint   `json:"photo_id" binding:"required"`
	Algorithm string `json:"algorithm"`
}

// PhotoStatsResponse 照片统计响应
type PhotoStatsResponse struct {
	Total      int64 `json:"total"`
	Analyzed   int64 `json:"analyzed"`
	Unanalyzed int64 `json:"unanalyzed"`
}

// ESP32StatsResponse ESP32 设备统计响应
type ESP32StatsResponse struct {
	Total  int64 `json:"total"`
	Online int64 `json:"online"`
}

// ExportRequest 导出请求
type ExportRequest struct {
	OutputPath string `json:"output_path" binding:"required"` // 导出路径
	Analyzed   *bool  `json:"analyzed"`                       // 只导出已分析/未分析的照片
}

// ExportResponse 导出响应
type ExportResponse struct {
	OutputPath   string `json:"output_path"`
	PhotoCount   int    `json:"photo_count"`
	DatabaseSize int64  `json:"database_size"`
	ThumbnailDir string `json:"thumbnail_dir"`
}

// ImportRequest 导入请求
type ImportRequest struct {
	InputPath string `json:"input_path" binding:"required"` // 导入路径
}

// ImportResponse 导入响应
type ImportResponse struct {
	UpdatedCount int `json:"updated_count"` // 更新数量
	FailedCount  int `json:"failed_count"`  // 失败数量
}

// SystemHealthResponse 系统健康检查响应
type SystemHealthResponse struct {
	Status  string    `json:"status"`  // healthy / unhealthy
	Version string    `json:"version"`
	Uptime  int64     `json:"uptime"` // 运行时间（秒）
	Time    time.Time `json:"time"`
}

// SystemStatsResponse 系统统计响应
type SystemStatsResponse struct {
	TotalPhotos      int64 `json:"total_photos"`
	AnalyzedPhotos   int64 `json:"analyzed_photos"`
	UnanalyzedPhotos int64 `json:"unanalyzed_photos"`
	TotalDevices     int64 `json:"total_devices"`
	OnlineDevices    int64 `json:"online_devices"`
	TotalDisplays    int64 `json:"total_displays"`
}
