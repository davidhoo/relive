package model

import "time"

type GenerateDailyDisplayBatchRequest struct {
	Date  string `json:"date"`
	Force bool   `json:"force"`
}

type DailyDisplayAssetResponse struct {
	ID               uint   `json:"id"`
	RenderProfile    string `json:"render_profile"`
	DitherPreviewURL string `json:"dither_preview_url,omitempty"`
	BinURL           string `json:"bin_url,omitempty"`
	HeaderURL        string `json:"header_url,omitempty"`
	Checksum         string `json:"checksum"`
	FileSize         int64  `json:"file_size"`
}

type DailyDisplayItemResponse struct {
	ID              uint                        `json:"id"`
	Sequence        int                         `json:"sequence"`
	PhotoID         uint                        `json:"photo_id"`
	PreviewURL      string                      `json:"preview_url"`
	CurationChannel string                      `json:"curation_channel,omitempty"`
	Photo           *Photo                      `json:"photo,omitempty"`
	Assets          []DailyDisplayAssetResponse `json:"assets,omitempty"`
}

type DailyDisplayBatchResponse struct {
	ID               uint                       `json:"id"`
	BatchDate        string                     `json:"batch_date"`
	Status           string                     `json:"status"`
	ItemCount        int                        `json:"item_count"`
	CanvasTemplate   string                     `json:"canvas_template"`
	StrategySnapshot string                     `json:"strategy_snapshot"`
	ErrorMessage     string                     `json:"error_message,omitempty"`
	GeneratedAt      *time.Time                 `json:"generated_at,omitempty"`
	UpdatedAt        time.Time                  `json:"updated_at"`
	Items            []DailyDisplayItemResponse `json:"items,omitempty"`
}

type DailyDisplayBatchListResponse struct {
	Items []*DailyDisplayBatchResponse `json:"items"`
}

type DeviceDisplayResponse struct {
	BatchDate     string `json:"batch_date"`
	Sequence      int    `json:"sequence"`
	TotalCount    int    `json:"total_count"`
	PhotoID       uint   `json:"photo_id"`
	ItemID        uint   `json:"item_id"`
	AssetID       uint   `json:"asset_id"`
	RenderProfile string `json:"render_profile"`
	BinURL        string `json:"bin_url"`
	Checksum      string `json:"checksum"`
}

type RenderProfileResponse struct {
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	Palette          string `json:"palette"`
	DitherMode       string `json:"dither_mode"`
	CanvasTemplate   string `json:"canvas_template"`
	DefaultForDevice bool   `json:"default_for_device"`
}

// DeviceDisplaySelection 服务端内部使用的设备展示选择结果。
type DeviceDisplaySelection struct {
	BatchDate  string
	TotalCount int
	Sequence   int
	Item       *DailyDisplayItem
	Asset      *DailyDisplayAsset
}
