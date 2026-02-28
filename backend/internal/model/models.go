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
	PhotoID  uint   `gorm:"not null;index" json:"photo_id"`         // 照片 ID
	DeviceID uint   `gorm:"not null;index" json:"device_id"`       // 设备 ID
	Photo    *Photo `gorm:"foreignKey:PhotoID;references:ID" json:"photo,omitempty"`
	Device   *ESP32Device `gorm:"foreignKey:DeviceID;references:ID" json:"device,omitempty"`

	// 展示信息
	DisplayedAt     time.Time `gorm:"not null;index" json:"displayed_at"` // 展示时间
	DisplayDuration int       `gorm:"default:0" json:"display_duration"`                   // 展示时长（秒）
	TriggerType     string    `gorm:"type:varchar(20);not null" json:"trigger_type"`       // 触发类型（scheduled/manual/boot）
}

// TableName 指定表名
func (DisplayRecord) TableName() string {
	return "display_records"
}

// ESP32Device ESP32 设备
type ESP32Device struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 设备信息
	DeviceID  string `gorm:"type:varchar(50);not null;uniqueIndex:idx_device_id" json:"device_id"` // 设备 ID
	Name      string `gorm:"type:varchar(100);not null" json:"name"`                               // 设备名称
	APIKey    string `gorm:"type:varchar(100);not null;uniqueIndex:idx_api_key" json:"-"`          // API Key（不返回）
	IPAddress string `gorm:"type:varchar(50)" json:"ip_address"`                                   // IP 地址

	// 硬件信息
	ScreenWidth     int    `gorm:"not null" json:"screen_width"`                  // 屏幕宽度
	ScreenHeight    int    `gorm:"not null" json:"screen_height"`                 // 屏幕高度
	FirmwareVersion string `gorm:"type:varchar(20)" json:"firmware_version"`      // 固件版本
	MACAddress      string `gorm:"type:varchar(20)" json:"mac_address"`           // MAC 地址

	// 状态信息
	Online        bool       `gorm:"default:false" json:"online"`              // 是否在线
	LastHeartbeat *time.Time `gorm:"index:idx_last_heartbeat" json:"last_heartbeat"` // 最后心跳时间
	BatteryLevel  int        `gorm:"default:0" json:"battery_level"`           // 电池电量（0-100）
	WiFiRSSI      int        `gorm:"default:0" json:"wifi_rssi"`               // WiFi 信号强度（dBm）

	// 配置信息
	Config string `gorm:"type:text" json:"config"` // 设备配置（JSON）

	// 关联
	DisplayRecords []DisplayRecord `gorm:"foreignKey:DeviceID" json:"-"` // 展示记录
}

// TableName 指定表名
func (ESP32Device) TableName() string {
	return "esp32_devices"
}

// IsOnline 判断设备是否在线（5分钟内有心跳）
func (d *ESP32Device) IsOnline() bool {
	if d.LastHeartbeat == nil {
		return false
	}
	return time.Since(*d.LastHeartbeat) < 5*time.Minute
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
	ID        uint   `gorm:"primarykey" json:"id"`
	GeonameID int    `gorm:"not null;uniqueIndex:idx_geoname_id" json:"geoname_id"` // GeoNames ID
	Name      string `gorm:"type:varchar(200);not null;index:idx_name" json:"name"` // 城市名
	Country   string `gorm:"type:varchar(100);not null" json:"country"`             // 国家
	Latitude  float64 `gorm:"not null;index:idx_lat" json:"latitude"`               // 纬度
	Longitude float64 `gorm:"not null;index:idx_lon" json:"longitude"`              // 经度
}

// TableName 指定表名
func (City) TableName() string {
	return "cities"
}
