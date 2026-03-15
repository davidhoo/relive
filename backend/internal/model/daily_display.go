package model

import (
	"time"

	"gorm.io/gorm"
)

const (
	DailyDisplayBatchStatusPending = "pending"
	DailyDisplayBatchStatusRunning = "running"
	DailyDisplayBatchStatusReady   = "ready"
	DailyDisplayBatchStatusFailed  = "failed"
)

// DailyDisplayBatch 每日展示批次（每天仅一套）。
type DailyDisplayBatch struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	BatchDate        string     `gorm:"type:date;not null;uniqueIndex:idx_batch_date" json:"batch_date"`
	Status           string     `gorm:"type:varchar(20);not null;index:idx_batch_status;check:chk_batch_status,status IN ('pending','running','ready','failed')" json:"status"`
	ItemCount        int        `gorm:"default:0" json:"item_count"`
	CanvasTemplate   string     `gorm:"type:varchar(100);not null" json:"canvas_template"`
	StrategySnapshot string     `gorm:"type:text" json:"strategy_snapshot"`
	ErrorMessage     string     `gorm:"type:text" json:"error_message"`
	GeneratedAt      *time.Time `json:"generated_at"`

	Items []DailyDisplayItem `gorm:"foreignKey:BatchID;constraint:OnDelete:CASCADE" json:"items,omitempty"`
}

func (DailyDisplayBatch) TableName() string {
	return "daily_display_batches"
}

// DailyDisplayItem 每日展示批次中的一个有序项目。
type DailyDisplayItem struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	BatchID        uint   `gorm:"not null;index;uniqueIndex:idx_batch_sequence" json:"batch_id"`
	Sequence       int    `gorm:"not null;uniqueIndex:idx_batch_sequence" json:"sequence"`
	PhotoID        uint   `gorm:"not null;index" json:"photo_id"`
	PreviewJPGPath string `gorm:"type:varchar(500);not null" json:"preview_jpg_path"`
	PreviewWidth   int    `gorm:"default:0" json:"preview_width"`
	PreviewHeight  int    `gorm:"default:0" json:"preview_height"`
	CanvasTemplate string `gorm:"type:varchar(100);not null" json:"canvas_template"`

	Photo  Photo               `gorm:"foreignKey:PhotoID" json:"photo,omitempty"`
	Assets []DailyDisplayAsset `gorm:"foreignKey:ItemID;constraint:OnDelete:CASCADE" json:"assets,omitempty"`
}

func (DailyDisplayItem) TableName() string {
	return "daily_display_items"
}

// DailyDisplayAsset 每个展示项针对不同渲染规格生成的资产。
type DailyDisplayAsset struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	ItemID            uint   `gorm:"not null;index;uniqueIndex:idx_item_profile" json:"item_id"`
	RenderProfile     string `gorm:"type:varchar(100);not null;uniqueIndex:idx_item_profile;index:idx_render_profile" json:"render_profile"`
	DitherPreviewPath string `gorm:"type:varchar(500);not null;default:''" json:"dither_preview_path"`
	BinPath           string `gorm:"type:varchar(500);not null" json:"bin_path"`
	HeaderPath        string `gorm:"type:varchar(500);not null" json:"header_path"`
	Checksum          string `gorm:"type:varchar(64);not null" json:"checksum"`
	FileSize          int64  `gorm:"default:0" json:"file_size"`
}

func (DailyDisplayAsset) TableName() string {
	return "daily_display_assets"
}

// DevicePlaybackState 设备独立播放状态。
type DevicePlaybackState struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	DeviceID         uint       `gorm:"not null;uniqueIndex:idx_device_playback" json:"device_id"`
	BatchID          uint       `gorm:"not null;index" json:"batch_id"`
	BatchDate        string     `gorm:"type:date;not null;index" json:"batch_date"`
	CurrentSequence  int        `gorm:"not null;default:1" json:"current_sequence"`
	LastServedItemID *uint      `gorm:"index" json:"last_served_item_id,omitempty"`
	LastServedAt     *time.Time `json:"last_served_at,omitempty"`
}

func (DevicePlaybackState) TableName() string {
	return "device_playback_states"
}
