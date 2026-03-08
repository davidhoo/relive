package model

import (
	"time"

	"gorm.io/gorm"
)

// Photo 照片模型
type Photo struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 文件信息
	FilePath       string     `gorm:"type:text;not null;uniqueIndex:idx_file_path" json:"file_path"` // 文件路径
	FileName       string     `gorm:"type:varchar(255);not null" json:"file_name"`                   // 文件名
	FileSize       int64      `gorm:"not null" json:"file_size"`                                     // 文件大小（字节）
	FileHash       string     `gorm:"type:varchar(64);index:idx_file_hash" json:"file_hash"`         // 文件哈希（SHA256）
	FileModTime    *time.Time `json:"file_mod_time"`                                                 // 文件修改时间（来自文件系统）
	FileCreateTime *time.Time `json:"file_create_time"`                                              // 文件创建时间（来自文件系统，可能为空）

	// 缩略图
	ThumbnailPath        string     `gorm:"type:varchar(500)" json:"thumbnail_path"` // 缩略图路径（相对于缩略图根目录）
	ThumbnailStatus      string     `gorm:"type:varchar(20);default:none;index:idx_thumbnail_status" json:"thumbnail_status"`
	ThumbnailGeneratedAt *time.Time `json:"thumbnail_generated_at"`

	// EXIF 信息
	TakenAt         *time.Time `gorm:"index:idx_taken_at" json:"taken_at"`                   // 拍摄时间
	CameraModel     string     `gorm:"type:varchar(100)" json:"camera_model"`                // 相机型号
	Width           int        `gorm:"not null" json:"width"`                                // 宽度
	Height          int        `gorm:"not null" json:"height"`                               // 高度
	Orientation     int        `gorm:"default:1" json:"orientation"`                         // 方向（1-8）
	GPSLatitude     *float64   `json:"gps_latitude"`                                         // GPS 纬度
	GPSLongitude    *float64   `json:"gps_longitude"`                                        // GPS 经度
	Location        string     `gorm:"type:varchar(200);index:idx_location" json:"location"` // 位置（城市）
	GeocodeStatus   string     `gorm:"type:varchar(20);default:none;index:idx_geocode_status" json:"geocode_status"`
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
	MainCategory string `gorm:"type:varchar(50);index:idx_main_category" json:"main_category"` // 主分类
	Tags         string `gorm:"type:text" json:"tags"`                                         // 标签（JSON数组）

	// 关联
	DisplayRecords []DisplayRecord `gorm:"foreignKey:PhotoID" json:"-"` // 展示记录
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

// CalculateOverallScore 计算综合评分（70% 回忆 + 30% 美观）
func (p *Photo) CalculateOverallScore() {
	p.OverallScore = int(float64(p.MemoryScore)*0.7 + float64(p.BeautyScore)*0.3)
}

// IsAnalyzed 是否已分析
func (p *Photo) IsAnalyzed() bool {
	return p.AIAnalyzed && p.AnalyzedAt != nil
}

// HasGPS 是否有 GPS 信息
func (p *Photo) HasGPS() bool {
	return p.GPSLatitude != nil && p.GPSLongitude != nil
}
