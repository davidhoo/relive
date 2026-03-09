package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config 应用配置
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Database    DatabaseConfig    `yaml:"database"`
	Photos      PhotosConfig      `yaml:"photos"`
	Performance PerformanceConfig `yaml:"performance"`
	AI          AIConfig          `yaml:"ai"`
	Display     DisplayConfig     `yaml:"display"`
	Geocode     GeocodeConfig     `yaml:"geocode"` // 新增：地理编码配置
	Logging     LoggingConfig     `yaml:"logging"`
	Security    SecurityConfig    `yaml:"security"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Mode        string `yaml:"mode"`         // debug / release
	ExternalURL string `yaml:"external_url"` // 外部访问地址，用于生成下载链接
	StaticPath  string `yaml:"static_path"`  // 前端静态文件路径（单镜像部署）
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Type        string `yaml:"type"`         // sqlite / postgres
	Path        string `yaml:"path"`         // SQLite 文件路径
	AutoMigrate bool   `yaml:"auto_migrate"` // 是否自动迁移
	LogMode     bool   `yaml:"log_mode"`     // 是否打印 SQL
}

// PhotosConfig 照片目录配置
type PhotosConfig struct {
	RootPath         string   `yaml:"root_path"`
	ThumbnailPath    string   `yaml:"thumbnail_path"` // 缩略图存储路径
	ExcludeDirs      []string `yaml:"exclude_dirs"`
	SupportedFormats []string `yaml:"supported_formats"`
}

// PerformanceConfig 性能相关配置
type PerformanceConfig struct {
	MaxScanWorkers      int `yaml:"max_scan_workers"`
	MaxAnalyzeWorkers   int `yaml:"max_analyze_workers"`
	MaxThumbnailWorkers int `yaml:"max_thumbnail_workers"`
	MaxGeocodeWorkers   int `yaml:"max_geocode_workers"`
	CacheSize           int `yaml:"cache_size"`
}

// AIConfig AI 配置
type AIConfig struct {
	Provider    string               `yaml:"provider"`    // ollama / qwen / openai / vllm / hybrid
	Timeout     int                  `yaml:"timeout"`     // 超时时间（秒）
	Temperature float64              `yaml:"temperature"` // 温度参数
	Ollama      OllamaConfig         `yaml:"ollama"`
	Qwen        QwenConfig           `yaml:"qwen"`
	OpenAI      OpenAIConfig         `yaml:"openai"`
	VLLM        VLLMConfig           `yaml:"vllm"`
	Hybrid      HybridProviderConfig `yaml:"hybrid"`
}

// OllamaConfig Ollama 配置
type OllamaConfig struct {
	Endpoint    string  `yaml:"endpoint"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	Timeout     int     `yaml:"timeout"`
}

// QwenConfig Qwen API 配置
type QwenConfig struct {
	APIKey      string  `yaml:"api_key"`
	Endpoint    string  `yaml:"endpoint"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	Timeout     int     `yaml:"timeout"`
}

// OpenAIConfig OpenAI API 配置
type OpenAIConfig struct {
	APIKey      string  `yaml:"api_key"`
	Endpoint    string  `yaml:"endpoint"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	MaxTokens   int     `yaml:"max_tokens"`
	Timeout     int     `yaml:"timeout"`
}

// VLLMConfig vLLM 配置
type VLLMConfig struct {
	Endpoint       string  `yaml:"endpoint"`
	Model          string  `yaml:"model"`
	Temperature    float64 `yaml:"temperature"`
	MaxTokens      int     `yaml:"max_tokens"`
	Timeout        int     `yaml:"timeout"`
	EnableThinking bool    `yaml:"enable_thinking"` // 是否启用思考，默认 false
}

// HybridProviderConfig 混合模式配置
type HybridProviderConfig struct {
	Primary      string `yaml:"primary"`        // 主提供者
	Fallback     string `yaml:"fallback"`       // 备用提供者
	RetryOnError bool   `yaml:"retry_on_error"` // 失败时切换
}

// DisplayConfig 展示策略配置
type DisplayConfig struct {
	Algorithm       string `yaml:"algorithm"`         // on_this_day
	FallbackDays    []int  `yaml:"fallback_days"`     // [3, 7, 30, 365]
	AvoidRepeatDays int    `yaml:"avoid_repeat_days"` // 避免重复展示的天数
}

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level      string `yaml:"level"`       // debug / info / warn / error
	File       string `yaml:"file"`        // 日志文件路径
	MaxSize    int    `yaml:"max_size"`    // 最大大小（MB）
	MaxBackups int    `yaml:"max_backups"` // 最大备份数
	MaxAge     int    `yaml:"max_age"`     // 最大保留天数
	Console    bool   `yaml:"console"`     // 是否输出到控制台
}

// SecurityConfig 安全配置
type SecurityConfig struct {
	JWTSecret    string `yaml:"jwt_Secret"`     // JWT 密钥
	APIKeyPrefix string `yaml:"api_key_prefix"` // API Key 前缀
}

// AMapNestedConfig 高德地图嵌套配置
type AMapNestedConfig struct {
	APIKey  string `yaml:"api_key"`
	Timeout int    `yaml:"timeout"`
}

// NominatimNestedConfig Nominatim 嵌套配置
type NominatimNestedConfig struct {
	Endpoint string `yaml:"endpoint"`
	Timeout  int    `yaml:"timeout"`
}

// OfflineNestedConfig 离线模式嵌套配置
type OfflineNestedConfig struct {
	MaxDistance float64 `yaml:"max_distance"`
}

// WeiboNestedConfig 微博地图RGC嵌套配置
type WeiboNestedConfig struct {
	APIKey  string `yaml:"api_key"`
	Timeout int    `yaml:"timeout"`
}

// HybridNestedConfig 混合模式嵌套配置
type HybridNestedConfig struct {
	Primary  string `yaml:"primary"`
	Fallback string `yaml:"fallback"`
}

// GeocodeConfig 地理编码配置
type GeocodeConfig struct {
	Provider     string `yaml:"provider"      json:"provider"`      // 主要提供商：amap / nominatim / offline / weibo
	Fallback     string `yaml:"fallback"      json:"fallback"`      // 备用提供商
	CacheEnabled bool   `yaml:"cache_enabled" json:"cache_enabled"` // 是否启用缓存
	CacheTTL     int    `yaml:"cache_ttl"     json:"cache_ttl"`     // 缓存有效期（秒）

	// AMap 高德地图（扁平结构，兼容旧配置）
	AMapAPIKey  string `yaml:"amap_api_key" json:"amap_api_key"`
	AMapTimeout int    `yaml:"amap_timeout" json:"amap_timeout"`

	// Nominatim (OpenStreetMap)（扁平结构，兼容旧配置）
	NominatimEndpoint string `yaml:"nominatim_endpoint" json:"nominatim_endpoint"`
	NominatimTimeout  int    `yaml:"nominatim_timeout"  json:"nominatim_timeout"`

	// Offline（扁平结构，兼容旧配置）
	OfflineMaxDistance float64 `yaml:"offline_max_distance" json:"offline_max_distance"` // 最大搜索距离（km）

	// Weibo 微博地图RGC（扁平结构，兼容旧配置）
	WeiboAPIKey  string `yaml:"weibo_api_key" json:"weibo_api_key"` // 微博API Key
	WeiboTimeout int    `yaml:"weibo_timeout" json:"weibo_timeout"` // 超时时间（秒）

	// 嵌套结构（兼容生产配置文件，不参与 JSON 序列化）
	AMap      AMapNestedConfig      `yaml:"amap"      json:"-"`
	Nominatim NominatimNestedConfig `yaml:"nominatim" json:"-"`
	Offline   OfflineNestedConfig   `yaml:"offline"   json:"-"`
	Weibo     WeiboNestedConfig     `yaml:"weibo"     json:"-"`
	Hybrid    HybridNestedConfig    `yaml:"hybrid"    json:"-"`
}

// GetAMapAPIKey 获取高德API Key（优先扁平结构，其次嵌套结构）
func (g *GeocodeConfig) GetAMapAPIKey() string {
	if g.AMapAPIKey != "" {
		return g.AMapAPIKey
	}
	return g.AMap.APIKey
}

// GetAMapTimeout 获取高德超时时间
func (g *GeocodeConfig) GetAMapTimeout() int {
	if g.AMapTimeout > 0 {
		return g.AMapTimeout
	}
	if g.AMap.Timeout > 0 {
		return g.AMap.Timeout
	}
	return 10 // 默认10秒
}

// GetNominatimEndpoint 获取Nominatim端点
func (g *GeocodeConfig) GetNominatimEndpoint() string {
	if g.NominatimEndpoint != "" {
		return g.NominatimEndpoint
	}
	if g.Nominatim.Endpoint != "" {
		return g.Nominatim.Endpoint
	}
	return "https://nominatim.openstreetmap.org/reverse"
}

// GetNominatimTimeout 获取Nominatim超时时间
func (g *GeocodeConfig) GetNominatimTimeout() int {
	if g.NominatimTimeout > 0 {
		return g.NominatimTimeout
	}
	if g.Nominatim.Timeout > 0 {
		return g.Nominatim.Timeout
	}
	return 10
}

// GetOfflineMaxDistance 获取离线最大搜索距离
func (g *GeocodeConfig) GetOfflineMaxDistance() float64 {
	if g.OfflineMaxDistance > 0 {
		return g.OfflineMaxDistance
	}
	if g.Offline.MaxDistance > 0 {
		return g.Offline.MaxDistance
	}
	return 100 // 默认100km
}

// GetWeiboAPIKey 获取微博API Key
func (g *GeocodeConfig) GetWeiboAPIKey() string {
	if g.WeiboAPIKey != "" {
		return g.WeiboAPIKey
	}
	return g.Weibo.APIKey
}

// GetWeiboTimeout 获取微博超时时间
func (g *GeocodeConfig) GetWeiboTimeout() int {
	if g.WeiboTimeout > 0 {
		return g.WeiboTimeout
	}
	if g.Weibo.Timeout > 0 {
		return g.Weibo.Timeout
	}
	return 10
}

// Load 加载配置文件
func Load(path string) (*Config, error) {
	var cfg Config

	basePath := filepath.Join(filepath.Dir(path), "config.base.yaml")
	if filepath.Base(path) != "config.base.yaml" {
		if baseData, baseErr := os.ReadFile(basePath); baseErr == nil {
			if err := yaml.Unmarshal(baseData, &cfg); err != nil {
				return nil, fmt.Errorf("parse base config file: %w", err)
			}
		} else if !os.IsNotExist(baseErr) {
			return nil, fmt.Errorf("read base config file: %w", baseErr)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	// 从环境变量覆盖敏感配置
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		cfg.Security.JWTSecret = secret
	}
	if value := os.Getenv("RELIVE_EXTERNAL_URL"); value != "" {
		cfg.Server.ExternalURL = value
	}
	if apiKey := os.Getenv("QWEN_API_KEY"); apiKey != "" {
		cfg.AI.Qwen.APIKey = apiKey
	}
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		cfg.AI.OpenAI.APIKey = apiKey
	}
	if value := os.Getenv("MAX_SCAN_WORKERS"); value != "" {
		workers, convErr := strconv.Atoi(value)
		if convErr != nil {
			return nil, fmt.Errorf("invalid MAX_SCAN_WORKERS: %w", convErr)
		}
		cfg.Performance.MaxScanWorkers = workers
	}
	if value := os.Getenv("MAX_THUMBNAIL_WORKERS"); value != "" {
		workers, convErr := strconv.Atoi(value)
		if convErr != nil {
			return nil, fmt.Errorf("invalid MAX_THUMBNAIL_WORKERS: %w", convErr)
		}
		cfg.Performance.MaxThumbnailWorkers = workers
	}
	if value := os.Getenv("MAX_GEOCODE_WORKERS"); value != "" {
		workers, convErr := strconv.Atoi(value)
		if convErr != nil {
			return nil, fmt.Errorf("invalid MAX_GEOCODE_WORKERS: %w", convErr)
		}
		cfg.Performance.MaxGeocodeWorkers = workers
	}

	// 验证配置
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

// Validate 验证配置
func (c *Config) Validate() error {
	// 验证服务器配置
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	// 验证数据库配置
	if c.Database.Type != "sqlite" && c.Database.Type != "postgres" {
		return fmt.Errorf("invalid database type: %s", c.Database.Type)
	}

	// 验证照片目录
	if c.Photos.RootPath == "" {
		return fmt.Errorf("photos.root_path is required")
	}

	// 验证 AI 提供者
	validProviders := map[string]bool{
		"ollama": true,
		"qwen":   true,
		"openai": true,
		"vllm":   true,
		"hybrid": true,
		"":       true, // 允许为空（后续通过 Web 界面配置）
	}
	if !validProviders[c.AI.Provider] {
		return fmt.Errorf("invalid AI provider: %s", c.AI.Provider)
	}

	// 验证 JWT 密钥
	if c.Security.JWTSecret == "" {
		return fmt.Errorf("security.jwt_Secret is required")
	}

	return nil
}
