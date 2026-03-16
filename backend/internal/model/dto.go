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

// CleanupPhotosResponse 清理照片响应
type CleanupPhotosResponse struct {
	TotalCount   int `json:"total_count"`   // 检查总数
	DeletedCount int `json:"deleted_count"` // 删除数量
	SkippedCount int `json:"skipped_count"` // 跳过数量（无法访问的文件）
}

// GetPhotosRequest 获取照片列表请求
type GetPhotosRequest struct {
	Page         int    `form:"page" binding:"omitempty,min=1"`
	PageSize     int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	Analyzed     *bool  `form:"analyzed"`      // 是否已分析（可选）
	HasThumbnail *bool  `form:"has_thumbnail"` // 是否有缩略图（可选）
	HasGPS       *bool  `form:"has_gps"`       // 是否有GPS位置（可选）
	Location     string `form:"location"`      // 位置筛选（可选）
	Search       string `form:"search"`        // 搜索关键词（可选，搜索路径、设备ID、标签）
	Category     string `form:"category"`      // 分类精确筛选（可选）
	Tag          string `form:"tag"`           // 标签筛选（可选）
	SortBy       string `form:"sort_by"`       // 排序字段（taken_at/overall_score）
	SortDesc     bool   `form:"sort_desc"`     // 是否降序
	Status       string `form:"status"`        // 状态筛选：active(默认)/excluded/all
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

// DisplayStrategyConfig 展示策略配置
type DisplayStrategyConfig struct {
	Algorithm            string  `json:"algorithm"`
	MinBeautyScore       int     `json:"minBeautyScore"`
	MinMemoryScore       int     `json:"minMemoryScore"`
	DailyCount           int     `json:"dailyCount"`
	CandidatePoolFactor  int     `json:"candidatePoolFactor,omitempty"`
	MinTimeGapHours      int     `json:"minTimeGapHours,omitempty"`
	MaxPhotosPerEvent    int     `json:"maxPhotosPerEvent,omitempty"`
	MaxPhotosPerLocation int     `json:"maxPhotosPerLocation,omitempty"`
	LocationBucketKM     float64 `json:"locationBucketKm,omitempty"`

	// 策展引擎参数（Algorithm = "event_curated" 时使用）
	CurationTimeTunnelDays      int     `json:"curationTimeTunnelDays,omitempty"`      // 往年今日 ±N 天，默认 7
	CurationTopEventsLimit      int     `json:"curationTopEventsLimit,omitempty"`      // 巅峰回忆提名数，默认 20
	CurationGeoEventsLimit      int     `json:"curationGeoEventsLimit,omitempty"`      // 地理漂移提名数，默认 10
	CurationHiddenGemsMinBeauty int     `json:"curationHiddenGemsMinBeauty,omitempty"` // 角落遗珠最低美感分，默认 60
	CurationSeasonBoost         float64 `json:"curationSeasonBoost,omitempty"`         // 季节对齐加权，默认 1.2
	CurationFreshnessPenalty    float64 `json:"curationFreshnessPenalty,omitempty"`     // 近期展示惩罚，默认 0.1
	CurationPeopleBonus         float64 `json:"curationPeopleBonus,omitempty"`         // 人物偏好加分，默认 20
	CurationDisplayDecayFactor  float64 `json:"curationDisplayDecayFactor,omitempty"`  // 展示衰减因子，默认 0.1
	CurationFreshnessDays       int     `json:"curationFreshnessDays,omitempty"`       // 新鲜度窗口天数，默认 30
	CurationPeopleEventsLimit   int     `json:"curationPeopleEventsLimit,omitempty"`   // 人物专题提名数，默认 10
	CurationSeasonEventsLimit   int     `json:"curationSeasonEventsLimit,omitempty"`   // 季节专题提名数，默认 10
}

// PreviewDisplayPhotosRequest 展示策略预览请求
type PreviewDisplayPhotosRequest struct {
	Algorithm      string `json:"algorithm"`
	MinBeautyScore int    `json:"minBeautyScore"`
	MinMemoryScore int    `json:"minMemoryScore"`
	DailyCount     int    `json:"dailyCount"`
	PreviewDate    string `json:"previewDate"`
	ExcludeIDs     []uint `json:"excludeIds,omitempty"` // 前端会话级临时排除（预览过的照片 ID）
}

// PreviewDisplayPhotosResponse 展示策略预览响应
type PreviewDisplayPhotosResponse struct {
	Algorithm   string   `json:"algorithm"`
	Count       int      `json:"count"`
	PreviewDate string   `json:"previewDate,omitempty"`
	Photos      []*Photo `json:"photos"`
}

// DeviceStatsResponse 设备统计响应
type DeviceStatsResponse struct {
	Total  int64            `json:"total"`
	Online int64            `json:"online"`
	ByType map[string]int64 `json:"by_type"` // 按设备类型统计
}

// ==================== 设备管理 DTOs ====================

// CreateDeviceRequest 创建设备请求（管理员）
type CreateDeviceRequest struct {
	Name          string `json:"name" binding:"required"` // 设备名称（用户填写）
	DeviceType    string `json:"device_type"`             // 设备类型：embedded/mobile/web/offline/service
	Description   string `json:"description"`             // 描述/备注
	RenderProfile string `json:"render_profile"`          // 嵌入式渲染规格
}

// CreateDeviceResponse 创建设备响应（包含 API Key，仅创建时返回）
type CreateDeviceResponse struct {
	ID            uint      `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	DeviceID      string    `json:"device_id"`
	Name          string    `json:"name"`
	APIKey        string    `json:"api_key"` // ⚠️ 仅创建时返回，之后无法查看
	DeviceType    string    `json:"device_type"`
	Description   string    `json:"description"`
	RenderProfile string    `json:"render_profile"`
}

type UpdateDeviceRenderProfileRequest struct {
	RenderProfile string `json:"render_profile" binding:"required"`
}

// DeviceDetailResponse 设备详情响应（包含 API Key）
type DeviceDetailResponse struct {
	ID            uint      `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	DeviceID      string    `json:"device_id"`
	Name          string    `json:"name"`
	APIKey        string    `json:"api_key"` // 设备的 API Key
	IPAddress     string    `json:"ip_address"`
	DeviceType    string    `json:"device_type"`
	RenderProfile string    `json:"render_profile"`
	IsEnabled     bool      `json:"is_enabled"` // 是否可用
	Online        bool      `json:"online"`     // 是否在线（根据最近活跃时间计算）
	LastSeen      time.Time `json:"last_seen,omitempty"`
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
	Total      int64   `json:"total"`      // 照片总数
	Analyzed   int64   `json:"analyzed"`   // 已分析数
	Unanalyzed int64   `json:"unanalyzed"` // 未分析数
	Progress   float64 `json:"progress"`   // 进度百分比
	Provider   string  `json:"provider"`   // 当前使用的 provider
}

// SystemHealthResponse 系统健康检查响应
type SystemHealthResponse struct {
	Status    string    `json:"status"` // healthy / unhealthy
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
	PasswordReset     bool   `json:"password_reset"`     // 密码是否已重置
}

// SystemStatsResponse 系统统计响应
type SystemStatsResponse struct {
	TotalPhotos       int64      `json:"total_photos"`
	AnalyzedPhotos    int64      `json:"analyzed_photos"`
	UnanalyzedPhotos  int64      `json:"unanalyzed_photos"`
	TotalDevices      int64      `json:"total_devices"`
	OnlineDevices     int64      `json:"online_devices"`
	TotalDisplays     int64      `json:"total_displays"`
	StorageSize       int64      `json:"storage_size"`                  // 存储空间（字节）
	DatabaseSize      int64      `json:"database_size"`                 // 数据库大小（字节）
	DatabaseUpdatedAt *time.Time `json:"database_updated_at,omitempty"` // 数据库最后修改时间
	GoVersion         string     `json:"go_version"`                    // Go 版本
	Uptime            int64      `json:"uptime"`                        // 运行时长（秒）
	Timestamp         time.Time  `json:"timestamp"`                     // 统计时间
}

// SystemEnvironmentResponse 系统环境信息响应
type SystemEnvironmentResponse struct {
	IsDocker    bool   `json:"is_docker"`    // 是否在 Docker 中运行
	DefaultPath string `json:"default_path"` // 默认路径（Docker 中为 /app，否则为当前工作目录）
	WorkDir     string `json:"work_dir"`     // 当前工作目录
}

// ScanPathConfig 扫描路径配置
type ScanPathConfig struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Path            string `json:"path"`
	IsDefault       bool   `json:"is_default"`
	Enabled         bool   `json:"enabled"`
	AutoScanEnabled bool   `json:"auto_scan_enabled"`
	CreatedAt       string `json:"created_at"`
	LastScannedAt   string `json:"last_scanned_at,omitempty"`
}

// ScanPathsConfig 扫描路径配置集合
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
	Entries     []DirectoryEntry `json:"entries"`
	ParentPath  string           `json:"parent_path,omitempty"`
	CurrentPath string           `json:"current_path"`
}

// CountPhotosByPathsRequest 按路径统计照片数量请求
type CountPhotosByPathsRequest struct {
	Paths []string `json:"paths" binding:"required"`
}

// CountPhotosByPathsResponse 按路径统计照片数量响应
type CountPhotosByPathsResponse struct {
	Counts map[string]int64 `json:"counts"` // key: path, value: count
}

type PathDerivedStatus struct {
	PhotoTotal       int64 `json:"photo_total"`
	AnalyzedTotal    int64 `json:"analyzed_total"`
	ThumbnailTotal   int64 `json:"thumbnail_total"`
	ThumbnailReady   int64 `json:"thumbnail_ready"`
	ThumbnailFailed  int64 `json:"thumbnail_failed"`
	ThumbnailPending int64 `json:"thumbnail_pending"`
	GeocodeTotal     int64 `json:"geocode_total"`
	GeocodeReady     int64 `json:"geocode_ready"`
	GeocodeFailed    int64 `json:"geocode_failed"`
	GeocodePending   int64 `json:"geocode_pending"`
}

type CountDerivedStatusByPathsRequest struct {
	Paths []string `json:"paths" binding:"required"`
}

type CountDerivedStatusByPathsResponse struct {
	Stats map[string]PathDerivedStatus `json:"stats"`
}

// In-memory 任务状态常量（ThumbnailTask / GeocodeTask 共用）
const (
	TaskStatusRunning  = "running"
	TaskStatusStopping = "stopping"
	TaskStatusStopped  = "stopped"
)

// ScanTask 扫描任务状态
type ScanTask struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"` // pending, running, completed, failed
	Type            string     `json:"type"`   // scan, rebuild
	Path            string     `json:"path"`
	Phase           string     `json:"phase,omitempty"`
	TotalFiles      int        `json:"total_files"`
	DiscoveredFiles int        `json:"discovered_files,omitempty"`
	ProcessedFiles  int        `json:"processed_files"`
	NewPhotos       int        `json:"new_photos"`
	UpdatedPhotos   int        `json:"updated_photos"`
	DeletedPhotos   int        `json:"deleted_photos,omitempty"`
	SkippedFiles    int        `json:"skipped_files,omitempty"`
	CurrentFile     string     `json:"current_file,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	StopRequestedAt *time.Time `json:"stop_requested_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

// IsRunning 检查任务是否运行中
func (t *ScanTask) IsRunning() bool {
	return t.Status == ScanJobStatusRunning || t.Status == ScanJobStatusStopping
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

type ThumbnailTask struct {
	Status         string     `json:"status"`
	CurrentPhotoID uint       `json:"current_photo_id,omitempty"`
	CurrentFile    string     `json:"current_file,omitempty"`
	ProcessedJobs  int64      `json:"processed_jobs"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty"`
}

type ThumbnailStatsResponse struct {
	Total      int64 `json:"total"`
	Pending    int64 `json:"pending"`
	Queued     int64 `json:"queued"`
	Processing int64 `json:"processing"`
	Completed  int64 `json:"completed"`
	Failed     int64 `json:"failed"`
	Cancelled  int64 `json:"cancelled"`
}

type ThumbnailEnqueueRequest struct {
	PhotoID uint `json:"photo_id" binding:"required"`
	Force   bool `json:"force"`
}

type ThumbnailBatchEnqueueRequest struct {
	Path string `json:"path" binding:"required"`
}

type GeocodeTask struct {
	Status         string     `json:"status"`
	CurrentPhotoID uint       `json:"current_photo_id,omitempty"`
	ProcessedJobs  int64      `json:"processed_jobs"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty"`
}

type GeocodeStatsResponse struct {
	Total      int64 `json:"total"`
	Pending    int64 `json:"pending"`
	Queued     int64 `json:"queued"`
	Processing int64 `json:"processing"`
	Completed  int64 `json:"completed"`
	Failed     int64 `json:"failed"`
	Cancelled  int64 `json:"cancelled"`
}

type GeocodeEnqueueRequest struct {
	PhotoID uint `json:"photo_id" binding:"required"`
	Force   bool `json:"force"`
}

type GeocodeBatchEnqueueRequest struct {
	Path string `json:"path" binding:"required"`
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
	NewUsername string `json:"new_username" binding:"omitempty,min=3,max=32"` // 可选：同时修改用户名
}

// UserInfoResponse 用户信息响应
type UserInfoResponse struct {
	ID           uint   `json:"id"`
	Username     string `json:"username"`
	IsFirstLogin bool   `json:"is_first_login"`
}

// UpdateDeviceEnabledRequest 更新设备可用状态请求
// 注意：布尔值不使用 required binding，因为 false 也是合法值
type UpdateDeviceEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// TagWithCount 标签及其照片数量
type TagWithCount struct {
	Tag   string `json:"tag" gorm:"column:tag"`
	Count int    `json:"count" gorm:"column:count"`
}

// TagsResponse 标签列表响应（含总数）
type TagsResponse struct {
	Items []TagWithCount `json:"items"`
	Total int64          `json:"total"`
}

// PhotoCountsResponse 照片按状态计数响应
type PhotoCountsResponse struct {
	ActiveCount   int64 `json:"active_count"`
	ExcludedCount int64 `json:"excluded_count"`
}

// BatchUpdateStatusRequest 批量更新照片状态请求
type BatchUpdateStatusRequest struct {
	PhotoIDs []uint `json:"photo_ids" binding:"required,min=1"`
	Status   string `json:"status" binding:"required,oneof=active excluded"`
}
