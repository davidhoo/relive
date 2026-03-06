package geocode

import (
	"time"
)

// Provider 地理编码提供商接口
type Provider interface {
	// Name 返回提供商名称
	Name() string

	// ReverseGeocode 反向地理编码：GPS 坐标 -> 位置名称
	ReverseGeocode(lat, lon float64) (*Location, error)

	// IsAvailable 检查服务是否可用
	IsAvailable() bool

	// Priority 返回优先级（数字越小优先级越高）
	Priority() int
}

// Location 位置信息
type Location struct {
	City     string  `json:"city"`      // 城市
	District string  `json:"district"`  // 区/县
	Province string  `json:"province"`  // 省份
	Country  string  `json:"country"`   // 国家
	FullName string  `json:"full_name"` // 完整地址
	Street   string  `json:"street"`    // 街道/街区（如：三里屯街区）
	POI      string  `json:"poi"`       // 商圈/地标（如：东大桥）
	Latitude float64 `json:"latitude"`  // 纬度
	Longitude float64 `json:"longitude"` // 经度

	// 元数据
	Provider string        `json:"provider"` // 提供商名称
	Duration time.Duration `json:"duration"` // 查询耗时
}

// FormatShort 返回简短的位置名称
func (l *Location) FormatShort() string {
	if l.City != "" && l.City != "[]" {
		if l.District != "" {
			return l.City + l.District
		}
		return l.City
	}
	if l.Province != "" {
		return l.Province
	}
	if l.Country != "" {
		return l.Country
	}
	return ""
}

// FormatFull 返回完整的位置名称（国家，省，市，区）
func (l *Location) FormatFull() string {
	if l.FullName != "" {
		return l.FullName
	}

	parts := []string{}
	if l.Country != "" {
		parts = append(parts, l.Country)
	}
	if l.Province != "" {
		parts = append(parts, l.Province)
	}
	if l.City != "" && l.City != "[]" {
		parts = append(parts, l.City)
	}
	if l.District != "" {
		parts = append(parts, l.District)
	}

	result := ""
	for _, p := range parts {
		if result != "" {
			result += "，"
		}
		result += p
	}
	return result
}

// FormatDisplay 返回标准显示格式：城市 + 区县 + 商圈（街道）
// 例如：北京市朝阳区东大桥（三里屯街区）
// 对于离线数据库（无详细地址信息），优先使用 FullName
func (l *Location) FormatDisplay() string {
	// 离线数据库：如果 FullName 已设置且没有详细的街道/商圈信息，直接使用 FullName
	if l.FullName != "" && l.Provider == "offline" {
		return l.FullName
	}

	// 1. 基础部分：城市 + 区县
	var result string
	if l.City != "" && l.City != "[]" {
		result = l.City
		if l.District != "" {
			result += l.District
		}
	} else if l.Province != "" {
		result = l.Province
		if l.District != "" {
			result += l.District
		}
	} else if l.District != "" {
		result = l.District
	}

	if result == "" {
		// 降级到原有逻辑
		return l.FormatShort()
	}

	// 2. 添加商圈/地标
	if l.POI != "" {
		result += l.POI
	}

	// 3. 添加街道（括号内）
	if l.Street != "" {
		result += "（" + l.Street + "）"
	}

	return result
}

// Request 地理编码请求
type Request struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Language  string  `json:"language,omitempty"` // 语言偏好（如 zh-CN, en）
}

// Config 地理编码配置
type Config struct {
	Provider string `yaml:"provider"` // 主要提供商
	Fallback string `yaml:"fallback"` // 备用提供商

	// AMap 高德地图
	AMapAPIKey string `yaml:"amap_api_key"`
	AMapTimeout int   `yaml:"amap_timeout"` // 超时时间（秒）

	// Nominatim
	NominatimEndpoint string `yaml:"nominatim_endpoint"`
	NominatimTimeout  int    `yaml:"nominatim_timeout"`

	// Offline
	OfflineDBPath      string  `yaml:"offline_db_path"`      // 城市数据库路径
	OfflineMaxDistance float64 `yaml:"offline_max_distance"` // 最大搜索距离（km）

	// Weibo 微博地图RGC
	WeiboAPIKey string `yaml:"weibo_api_key"` // 微博API Key
	WeiboTimeout int   `yaml:"weibo_timeout"` // 超时时间（秒）

	// 通用设置
	CacheEnabled bool `yaml:"cache_enabled"` // 是否启用缓存
	CacheTTL     int  `yaml:"cache_ttl"`     // 缓存有效期（秒）
}
