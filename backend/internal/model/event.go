package model

import "time"

// Event 事件模型 — 基于时空聚类的照片事件
type Event struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	StartTime     time.Time `gorm:"not null;index:idx_events_start_time" json:"start_time"`
	EndTime       time.Time `gorm:"not null" json:"end_time"`
	DurationHours float64   `gorm:"not null;default:0" json:"duration_hours"`
	PhotoCount    int       `gorm:"not null;default:0" json:"photo_count"`

	// 位置信息（簇内均值）
	GPSLatitude  *float64 `json:"gps_latitude,omitempty"`
	GPSLongitude *float64 `json:"gps_longitude,omitempty"`
	Location     string   `gorm:"default:''" json:"location"`

	// 画像
	CoverPhotoID    *uint  `json:"cover_photo_id,omitempty"`
	PrimaryCategory string `gorm:"default:'';index:idx_events_primary_category" json:"primary_category"`
	PrimaryTag      string `gorm:"default:''" json:"primary_tag"`

	// 展示权重
	EventScore      float64    `gorm:"not null;default:0;index:idx_events_event_score" json:"event_score"`
	DisplayCount    int        `gorm:"not null;default:0;index:idx_events_display_count" json:"display_count"`
	LastDisplayedAt *time.Time `gorm:"index:idx_events_last_displayed" json:"last_displayed_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名
func (Event) TableName() string {
	return "events"
}
