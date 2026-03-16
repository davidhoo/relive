package model

import (
	"time"

	"gorm.io/gorm"
)

// 照片状态常量
const (
	PhotoStatusActive   = "active"   // 正常状态
	PhotoStatusExcluded = "excluded" // 已排除
)

// 缩略图状态常量
const (
	ThumbnailStatusNone    = "none"
	ThumbnailStatusPending = "pending"
	ThumbnailStatusReady   = "ready"
	ThumbnailStatusFailed  = "failed"
)

// Geocode 状态常量
const (
	GeocodeStatusNone    = "none"
	GeocodeStatusPending = "pending"
	GeocodeStatusReady   = "ready"
	GeocodeStatusFailed  = "failed"
)

// Photo 照片模型
type Photo struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index;index:idx_status_deleted_taken,priority:2" json:"-"`

	// 状态
	Status string `gorm:"type:varchar(20);default:'active';index:idx_status;index:idx_status_deleted_taken,priority:1;check:chk_photo_status,status IN ('active','excluded')" json:"status"`

	// 文件信息
	FilePath       string     `gorm:"type:text;not null;uniqueIndex:idx_file_path" json:"file_path"` // 文件路径
	FileName       string     `gorm:"type:varchar(255);not null" json:"file_name"`                   // 文件名
	FileSize       int64      `gorm:"not null" json:"file_size"`                                     // 文件大小（字节）
	FileHash       string     `gorm:"type:varchar(64);index:idx_file_hash" json:"file_hash"`         // 文件哈希（SHA256）
	FileModTime    *time.Time `json:"file_mod_time"`                                                 // 文件修改时间（来自文件系统）
	FileCreateTime *time.Time `json:"file_create_time"`                                              // 文件创建时间（来自文件系统，可能为空）

	// 缩略图
	ThumbnailPath        string     `gorm:"type:varchar(500)" json:"thumbnail_path"` // 缩略图路径（相对于缩略图根目录）
	ThumbnailStatus      string     `gorm:"type:varchar(20);default:none;index:idx_thumbnail_status;check:chk_thumbnail_status,thumbnail_status IN ('none','pending','ready','failed')" json:"thumbnail_status"`
	ThumbnailGeneratedAt *time.Time `json:"thumbnail_generated_at"`

	// EXIF 信息
	TakenAt         *time.Time `gorm:"index:idx_taken_at;index:idx_status_deleted_taken,priority:3,sort:desc" json:"taken_at"` // 拍摄时间
	CameraModel     string     `gorm:"type:varchar(100)" json:"camera_model"`                // 相机型号
	Width           int        `gorm:"not null" json:"width"`                                // 宽度
	Height          int        `gorm:"not null" json:"height"`                               // 高度
	Orientation     int        `gorm:"default:1" json:"orientation"`                         // 方向（1-8）
	GPSLatitude     *float64   `json:"gps_latitude"`                                         // GPS 纬度
	GPSLongitude    *float64   `json:"gps_longitude"`                                        // GPS 经度
	Location        string     `gorm:"type:varchar(200);index:idx_location" json:"location"` // 位置（城市）
	Country         string     `gorm:"type:varchar(100)" json:"country"`                    // 国家
	Province        string     `gorm:"type:varchar(100)" json:"province"`                   // 省份
	City            string     `gorm:"type:varchar(100);index:idx_photo_city" json:"city"`  // 城市
	District        string     `gorm:"type:varchar(100)" json:"district"`                   // 区/县
	Street          string     `gorm:"type:varchar(200)" json:"street"`                     // 街道
	POI             string     `gorm:"type:varchar(200)" json:"poi"`                        // 商圈/地标
	GeocodeStatus   string     `gorm:"type:varchar(20);default:none;index:idx_geocode_status;check:chk_geocode_status,geocode_status IN ('none','pending','ready','failed')" json:"geocode_status"`
	GeocodeProvider string     `gorm:"column:geocode_provider;type:varchar(50)" json:"geocode_provider"`
	GeocodedAt      *time.Time `json:"geocoded_at"`

	// AI 分析结果
	AIAnalyzed bool       `gorm:"default:false;index:idx_ai_analyzed" json:"ai_analyzed"` // 是否已分析
	AnalyzedAt *time.Time `json:"analyzed_at"`                                            // 分析时间
	AIProvider string     `gorm:"column:ai_provider;type:varchar(50)" json:"ai_provider"` // AI 提供商（qwen/openai/ollama等）

	// 离线分析任务锁定（用于多分析器并发控制）
	AnalysisLockID        *string    `gorm:"type:varchar(64);index:idx_analysis_lock" json:"-"`      // 分析器实例ID（UUID）
	AnalysisLockExpiredAt *time.Time `json:"-"`                                                      // 锁过期时间
	AnalysisRetryCount    int        `gorm:"default:0" json:"-"`                                     // 分析重试次数
	Description           string     `gorm:"type:text" json:"description"`                           // 详细描述（80-200字）
	Caption               string     `gorm:"type:varchar(100)" json:"caption"`                       // 精美短句（8-30字）
	MemoryScore           int        `gorm:"default:0;index:idx_memory_score" json:"memory_score"`   // 回忆价值评分（0-100）
	BeautyScore           int        `gorm:"default:0;index:idx_beauty_score" json:"beauty_score"`   // 美观度评分（0-100）
	OverallScore          int        `gorm:"default:0;index:idx_overall_score" json:"overall_score"` // 综合评分（0-100）
	ScoreReason           string     `gorm:"type:varchar(200)" json:"score_reason"`                  // 评分理由

	// 分类标签
	MainCategory string   `gorm:"type:varchar(50);index:idx_main_category" json:"main_category"` // 主分类
	Tags         string   `gorm:"type:text" json:"-"`                                            // 标签（逗号分隔，保留双写）
	TagList      []string `gorm:"-" json:"tags"`                                                 // 标签列表（仅 JSON 输出）

	// 事件聚类
	EventID *uint `gorm:"index:idx_photos_event_id" json:"event_id,omitempty"` // 所属事件 ID

	// 瞬态字段（不持久化）
	CurationChannel string `gorm:"-" json:"-"` // 策展来源通道（仅批次生成时传递）

	// 关联
	DisplayRecords []DisplayRecord `gorm:"foreignKey:PhotoID" json:"-"` // 展示记录
}

// UpdateCategoryRequest 更新分类请求
type UpdateCategoryRequest struct {
	Category string `json:"category"`
}

// SetManualLocationRequest 手动设置照片位置请求
type SetManualLocationRequest struct {
	Latitude  float64 `json:"latitude" binding:"required"`
	Longitude float64 `json:"longitude" binding:"required"`
}

// TableName 指定表名
func (Photo) TableName() string {
	return "photos"
}

// BeforeCreate GORM 钩子：创建前
func (p *Photo) BeforeCreate(tx *gorm.DB) error {
	// 计算综合评分
	if p.MemoryScore > 0 || p.BeautyScore > 0 {
		p.CalculateOverallScore()
	}
	return nil
}

// BeforeUpdate GORM 钩子：更新前
func (p *Photo) BeforeUpdate(tx *gorm.DB) error {
	// 重新计算综合评分
	if p.MemoryScore > 0 || p.BeautyScore > 0 {
		p.CalculateOverallScore()
	}
	return nil
}

// CalcOverallScore 计算综合评分（70% 回忆 + 30% 美观）
func CalcOverallScore(memoryScore, beautyScore int) int {
	return int(float64(memoryScore)*0.7 + float64(beautyScore)*0.3)
}

// CalculateOverallScore 计算综合评分（70% 回忆 + 30% 美观）
func (p *Photo) CalculateOverallScore() {
	p.OverallScore = CalcOverallScore(p.MemoryScore, p.BeautyScore)
}

// LocationFields 位置信息结构体（用于 UpdateLocationFull 参数传递）
type LocationFields struct {
	Location string
	Country  string
	Province string
	City     string
	District string
	Street   string
	POI      string
}

// IsAnalyzed 是否已分析
func (p *Photo) IsAnalyzed() bool {
	return p.AIAnalyzed && p.AnalyzedAt != nil
}

// HasGPS 是否有 GPS 信息
func (p *Photo) HasGPS() bool {
	return p.GPSLatitude != nil && p.GPSLongitude != nil
}

// IsActive 是否为正常状态
func (p *Photo) IsActive() bool {
	return p.Status == PhotoStatusActive
}
