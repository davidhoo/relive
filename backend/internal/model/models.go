package model

import (
	"time"

	"gorm.io/gorm"
)

// DisplayRecord 展示记录
type DisplayRecord struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 关联信息
	PhotoID  uint `gorm:"not null;index;index:idx_display_record_lookup,priority:1" json:"photo_id"`  // 照片 ID
	DeviceID uint `gorm:"not null;index;index:idx_display_record_lookup,priority:2" json:"device_id"` // 设备 ID

	// 展示信息
	DisplayedAt     time.Time `gorm:"not null;index;index:idx_display_record_lookup,priority:3" json:"displayed_at"`            // 展示时间
	DisplayDuration int       `gorm:"default:0" json:"display_duration"`             // 展示时长（秒）
	TriggerType     string    `gorm:"type:varchar(20);not null" json:"trigger_type"` // 触发类型（scheduled/manual/boot）
}

// TableName 指定表名
func (DisplayRecord) TableName() string {
	return "display_records"
}

// Device 设备（电子相框、手机、平板等）
type Device struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 设备信息
	DeviceID  string `gorm:"type:varchar(50);not null;uniqueIndex:idx_device_id" json:"device_id"` // 设备 ID
	Name      string `gorm:"type:varchar(100);not null" json:"name"`                               // 设备名称
	APIKey    string `gorm:"type:varchar(100);not null;uniqueIndex:idx_api_key" json:"-"`          // API Key（不返回）
	IPAddress string `gorm:"type:varchar(50)" json:"ip_address"`                                   // IP 地址

	// 设备类型：embedded/mobile/web/offline/service
	DeviceType string `gorm:"type:varchar(20);default:'embedded';index:idx_device_type" json:"device_type"`

	// 描述/备注
	Description string `gorm:"type:varchar(500)" json:"description"`

	// 状态信息
	IsEnabled bool       `gorm:"default:true" json:"is_enabled"`                        // 是否可用（服务端控制）
	Online    bool       `gorm:"default:false" json:"online"`                           // 是否在线（根据最近活跃时间计算/缓存）
	LastSeen  *time.Time `gorm:"column:last_seen;index:idx_last_seen" json:"last_seen"` // 最近活跃时间

	// 配置信息
	Config string `gorm:"type:text" json:"config"` // 设备配置（JSON）

	// 渲染规格（嵌入式设备预生成资产使用）
	RenderProfile string `gorm:"type:varchar(100)" json:"render_profile"`

	// 关联
	DisplayRecords []DisplayRecord `gorm:"foreignKey:DeviceID" json:"-"` // 展示记录
}

// TableName 指定表名
func (Device) TableName() string {
	return "devices"
}

// IsOnline 判断设备是否在线（5分钟内有最近活跃记录）
func (d *Device) IsOnline() bool {
	if d.LastSeen == nil {
		return false
	}
	return time.Since(*d.LastSeen) < 5*time.Minute
}

// AppConfig 应用配置
type AppConfig struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	Key   string `gorm:"type:varchar(100);not null;uniqueIndex:idx_key" json:"key"` // 配置键
	Value string `gorm:"type:text;not null" json:"value"`                           // 配置值（JSON）
}

// TableName 指定表名
func (AppConfig) TableName() string {
	return "app_config"
}

// City 城市信息（用于 GPS 转城市名）
type City struct {
	ID        uint    `gorm:"primarykey" json:"id"`
	GeonameID int     `gorm:"not null;uniqueIndex:idx_geoname_id" json:"geoname_id"` // GeoNames ID
	Name      string  `gorm:"type:varchar(200);not null;index:idx_name" json:"name"` // 城市名
	NameZH    string  `gorm:"type:varchar(200)" json:"name_zh"`                      // 中文名
	AdminName string  `gorm:"type:varchar(200)" json:"admin_name"`                   // 省/州名
	Country   string  `gorm:"type:varchar(100);not null" json:"country"`             // 国家
	Latitude  float64 `gorm:"not null;index:idx_lat" json:"latitude"`                // 纬度
	Longitude float64 `gorm:"not null;index:idx_lon" json:"longitude"`               // 经度
}

// TableName 指定表名
func (City) TableName() string {
	return "cities"
}

// ResultQueueItem 分析结果队列项
type ResultQueueItem struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`

	Data       string `gorm:"type:text;not null" json:"data"`       // JSON 序列化的 QueuedResult
	Priority   int    `gorm:"default:0" json:"priority"`            // 优先级
	RetryCount int    `gorm:"default:0" json:"retry_count"`         // 重试次数
	Processed  bool   `gorm:"default:false;index" json:"processed"` // 是否已处理
}

// TableName 指定表名
func (ResultQueueItem) TableName() string {
	return "result_queue"
}
