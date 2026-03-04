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
	Path string `json:"path" binding:"omitempty"` // 扫描路径 (optional, uses config default if empty)
}

// ScanPhotosResponse 扫描照片响应
type ScanPhotosResponse struct {
	ScannedCount int `json:"scanned_count"` // 扫描数量
	NewCount     int `json:"new_count"`     // 新增数量
	UpdatedCount int `json:"updated_count"` // 更新数量
}

// RebuildPhotosResponse 重建照片响应
type RebuildPhotosResponse struct {
	ScannedCount int `json:"scanned_count"` // 扫描数量
	NewCount     int `json:"new_count"`     // 新增数量
	UpdatedCount int `json:"updated_count"` // 更新数量
	DeletedCount int `json:"deleted_count"` // 删除数量（数据库中已不存在于文件系统的照片）
}

// CleanupPhotosResponse 清理照片响应
type CleanupPhotosResponse struct {
	TotalCount   int `json:"total_count"`   // 检查总数
	DeletedCount int `json:"deleted_count"` // 删除数量
	SkippedCount int `json:"skipped_count"` // 跳过数量（无法访问的文件）
}

// GetPhotosRequest 获取照片列表请求
type GetPhotosRequest struct {
	Page     int    `form:"page" binding:"omitempty,min=1"`
	PageSize int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	Analyzed *bool  `form:"analyzed"` // 是否已分析（可选）
	Location string `form:"location"` // 位置筛选（可选）
	Search   string `form:"search"`   // 搜索关键词（可选，搜索路径、设备ID、标签）
	SortBy   string `form:"sort_by"`  // 排序字段（taken_at/overall_score）
	SortDesc bool   `form:"sort_desc"` // 是否降序
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

// DeviceRegisterRequest 设备注册请求
type DeviceRegisterRequest struct {
	DeviceID        string `json:"device_id" binding:"required"`
	Name            string `json:"name" binding:"required"`
	DeviceType      string `json:"device_type"`       // 设备类型：esp32/esp8266/android/ios/web（可选，默认 esp32）
	HardwareModel   string `json:"hardware_model"`    // 硬件型号：ESP32-S3/iPhone 15（可选）
	Platform        string `json:"platform"`          // 平台：embedded/mobile/web（可选，默认 embedded）
	ScreenWidth     int    `json:"screen_width" binding:"required,min=1"`
	ScreenHeight    int    `json:"screen_height" binding:"required,min=1"`
	FirmwareVersion string `json:"firmware_version"`  // 固件/应用版本
	IPAddress       string `json:"ip_address"`
	MACAddress      string `json:"mac_address"`
}

// DeviceRegisterResponse 设备注册响应
type DeviceRegisterResponse struct {
	DeviceID string                 `json:"device_id"`
	APIKey   string                 `json:"api_key"`
	Config   map[string]interface{} `json:"config"`
}

// DeviceHeartbeatRequest 设备心跳请求
type DeviceHeartbeatRequest struct {
	DeviceID            string `json:"device_id" binding:"required"`
	BatteryLevel        int    `json:"battery_level"`
	WiFiRSSI            int    `json:"wifi_rssi"`
	FreeHeap            int    `json:"free_heap"`
	LastDisplayPhotoID  int    `json:"last_display_photo_id"`
	FirmwareVersion     string `json:"firmware_version"`
}

// DeviceHeartbeatResponse 设备心跳响应
type DeviceHeartbeatResponse struct {
	ServerTime           time.Time `json:"server_time"`
	NextRefreshInSeconds int       `json:"next_refresh_in_seconds"`
	HasNewFirmware       bool      `json:"has_new_firmware"`
}

// DeviceStatsResponse 设备统计响应
type DeviceStatsResponse struct {
	Total      int64            `json:"total"`
	Online     int64            `json:"online"`
	ByType     map[string]int64 `json:"by_type"`     // 按设备类型统计
	ByPlatform map[string]int64 `json:"by_platform"` // 按平台统计
}

// ESP32RegisterRequest ESP32 注册请求（兼容旧代码）
// Deprecated: 使用 DeviceRegisterRequest 代替
type ESP32RegisterRequest = DeviceRegisterRequest

// ESP32RegisterResponse ESP32 注册响应（兼容旧代码）
// Deprecated: 使用 DeviceRegisterResponse 代替
type ESP32RegisterResponse = DeviceRegisterResponse

// ESP32HeartbeatRequest ESP32 心跳请求（兼容旧代码）
// Deprecated: 使用 DeviceHeartbeatRequest 代替
type ESP32HeartbeatRequest = DeviceHeartbeatRequest

// ESP32HeartbeatResponse ESP32 心跳响应（兼容旧代码）
// Deprecated: 使用 DeviceHeartbeatResponse 代替
type ESP32HeartbeatResponse = DeviceHeartbeatResponse

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

// AIAnalyzeRequest AI 分析请求
type AIAnalyzeRequest struct {
	PhotoID uint `json:"photo_id" binding:"required"` // 照片 ID
}

// AIAnalyzeBatchRequest AI 批量分析请求
type AIAnalyzeBatchRequest struct {
	Limit int `json:"limit"` // 分析数量限制（默认100）
}

// AIAnalyzeBatchResponse AI 批量分析响应
type AIAnalyzeBatchResponse struct {
	TotalCount   int     `json:"total_count"`   // 总数
	SuccessCount int     `json:"success_count"` // 成功数
	FailedCount  int     `json:"failed_count"`  // 失败数
	TotalCost    float64 `json:"total_cost"`    // 总成本（人民币）
	Duration     float64 `json:"duration"`      // 耗时（秒）
}

// AIAnalyzeProgressResponse AI 分析进度响应
type AIAnalyzeProgressResponse struct {
	Total         int64   `json:"total"`          // 照片总数
	Analyzed      int64   `json:"analyzed"`       // 已分析数
	Unanalyzed    int64   `json:"unanalyzed"`     // 未分析数
	Progress      float64 `json:"progress"`       // 进度百分比
	EstimatedCost float64 `json:"estimated_cost"` // 预估剩余成本
	Provider      string  `json:"provider"`       // 当前使用的 provider
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
	Status    string    `json:"status"`    // healthy / unhealthy
	Version   string    `json:"version"`
	Uptime    int64     `json:"uptime"`    // 运行时间（秒）
	Timestamp time.Time `json:"timestamp"` // 检查时间
}

// SystemResetRequest 系统还原请求
type SystemResetRequest struct {
	ConfirmText string `json:"confirm_text" binding:"required"` // 确认文本，必须为 "RESET"
}

// SystemResetResponse 系统还原响应
type SystemResetResponse struct {
	Success           bool   `json:"success"`
	Message           string `json:"message"`
	DatabaseCleared   bool   `json:"database_cleared"`   // 数据库是否已清除
	ThumbnailsCleared bool   `json:"thumbnails_cleared"` // 缩略图是否已清除
	CacheCleared      bool   `json:"cache_cleared"`      // 缓存是否已清除
	PasswordReset      bool   `json:"password_reset"`       // 密码是否已重置
}

// SystemStatsResponse 系统统计响应
type SystemStatsResponse struct {
	TotalPhotos      int64     `json:"total_photos"`
	AnalyzedPhotos   int64     `json:"analyzed_photos"`
	UnanalyzedPhotos int64     `json:"unanalyzed_photos"`
	TotalDevices     int64     `json:"total_devices"`
	OnlineDevices    int64     `json:"online_devices"`
	TotalDisplays    int64     `json:"total_displays"`
	StorageSize      int64     `json:"storage_size"`   // 存储空间（字节）
	DatabaseSize     int64     `json:"database_size"`  // 数据库大小（字节）
	GoVersion        string    `json:"go_version"`     // Go 版本
	Uptime           int64     `json:"uptime"`         // 运行时长（秒）
	Timestamp        time.Time `json:"timestamp"`      // 统计时间
}

// SystemEnvironmentResponse 系统环境信息响应
type SystemEnvironmentResponse struct {
	IsDocker    bool   `json:"is_docker"`     // 是否在 Docker 中运行
	DefaultPath string `json:"default_path"`  // 默认路径（Docker 中为 /app，否则为当前工作目录）
	WorkDir     string `json:"work_dir"`      // 当前工作目录
}

// ScanPathConfig represents a single scan path configuration
type ScanPathConfig struct {
	ID            string     `json:"id"`                         // UUID
	Name          string     `json:"name"`                       // User-friendly name
	Path          string     `json:"path"`                       // Absolute file path
	IsDefault     bool       `json:"is_default"`                 // Only one can be true
	Enabled       bool       `json:"enabled"`                    // Can be scanned
	CreatedAt     time.Time  `json:"created_at"`
	LastScannedAt *time.Time `json:"last_scanned_at,omitempty"` // Updated after each scan
}

// ScanPathsConfig represents the complete scan paths configuration
type ScanPathsConfig struct {
	Paths []ScanPathConfig `json:"paths"`
}

// ValidatePathRequest validates a scan path
type ValidatePathRequest struct {
	Path string `json:"path" binding:"required"`
}

// ValidatePathResponse returns validation result
type ValidatePathResponse struct {
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

// ListDirectoriesRequest 列出目录内容请求
type ListDirectoriesRequest struct {
	Path string `json:"path" binding:"required"`
}

// DirectoryEntry 目录条目
type DirectoryEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// ListDirectoriesResponse 列出目录内容响应
type ListDirectoriesResponse struct {
	Entries    []DirectoryEntry `json:"entries"`
	ParentPath string           `json:"parent_path,omitempty"`
	CurrentPath string          `json:"current_path"`
}

// CountPhotosByPathsRequest 按路径统计照片数量请求
type CountPhotosByPathsRequest struct {
	Paths []string `json:"paths" binding:"required"`
}

// CountPhotosByPathsResponse 按路径统计照片数量响应
type CountPhotosByPathsResponse struct {
	Counts map[string]int64 `json:"counts"` // key: path, value: count
}

// ScanTask 扫描任务状态
type ScanTask struct {
	ID            string     `json:"id"`
	Status        string     `json:"status"` // pending, running, completed, failed
	Type          string     `json:"type"`   // scan, rebuild
	Path          string     `json:"path"`
	TotalFiles    int        `json:"total_files"`
	ProcessedFiles int       `json:"processed_files"`
	NewPhotos     int        `json:"new_photos"`
	UpdatedPhotos int        `json:"updated_photos"`
	CurrentFile   string     `json:"current_file,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

// IsRunning 检查任务是否运行中
func (t *ScanTask) IsRunning() bool {
	return t.Status == "running"
}

// StartScanRequest 开始扫描请求
type StartScanRequest struct {
	Path string `json:"path,omitempty"`
}

// StartScanResponse 开始扫描响应
type StartScanResponse struct {
	TaskID string `json:"task_id"`
}

// GetScanProgressResponse 获取扫描进度响应
type GetScanProgressResponse struct {
	Task      *ScanTask `json:"task"`
	IsRunning bool      `json:"is_running"`
}

// ==================== Auth related DTOs ====================

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"Password" binding:"required"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	Token        string    `json:"token"`
	ExpiresAt    time.Time `json:"expires_at"`
	User         UserInfo  `json:"user"`
	IsFirstLogin bool      `json:"is_first_login"`
}

// UserInfo 用户信息
type UserInfo struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
}

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	OldPassword string `json:"old_Password" binding:"required"`
	NewPassword string `json:"new_Password" binding:"required,min=6"`
}

// UserInfoResponse 用户信息响应
type UserInfoResponse struct {
	ID           uint   `json:"id"`
	Username     string `json:"username"`
	IsFirstLogin bool   `json:"is_first_login"`
}
