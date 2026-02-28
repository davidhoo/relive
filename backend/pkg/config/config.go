package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 应用配置
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Photos   PhotosConfig   `yaml:"photos"`
	AI       AIConfig       `yaml:"ai"`
	Display  DisplayConfig  `yaml:"display"`
	Logging  LoggingConfig  `yaml:"logging"`
	Security SecurityConfig `yaml:"security"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"` // debug / release
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
	ExcludeDirs      []string `yaml:"exclude_dirs"`
	SupportedFormats []string `yaml:"supported_formats"`
}

// AIConfig AI 配置
type AIConfig struct {
	Provider string              `yaml:"provider"` // ollama / qwen / openai / vllm / hybrid
	Timeout  int                 `yaml:"timeout"`  // 超时时间（秒）
	Ollama   OllamaConfig        `yaml:"ollama"`
	Qwen     QwenConfig          `yaml:"qwen"`
	OpenAI   OpenAIConfig        `yaml:"openai"`
	VLLM     VLLMConfig          `yaml:"vllm"`
	Hybrid   HybridProviderConfig `yaml:"hybrid"`
}

// OllamaConfig Ollama 配置
type OllamaConfig struct {
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
	Timeout  int    `yaml:"timeout"`
}

// QwenConfig Qwen API 配置
type QwenConfig struct {
	APIKey   string `yaml:"api_key"`
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
	Timeout  int    `yaml:"timeout"`
}

// OpenAIConfig OpenAI API 配置
type OpenAIConfig struct {
	APIKey   string `yaml:"api_key"`
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
	Timeout  int    `yaml:"timeout"`
}

// VLLMConfig vLLM 配置
type VLLMConfig struct {
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
	Timeout  int    `yaml:"timeout"`
}

// HybridProviderConfig 混合模式配置
type HybridProviderConfig struct {
	Primary      string `yaml:"primary"`        // 主提供者
	Fallback     string `yaml:"fallback"`       // 备用提供者
	RetryOnError bool   `yaml:"retry_on_error"` // 失败时切换
}

// DisplayConfig 展示策略配置
type DisplayConfig struct {
	Algorithm        string `yaml:"algorithm"`          // on_this_day
	FallbackDays     []int  `yaml:"fallback_days"`      // [3, 7, 30, 365]
	AvoidRepeatDays  int    `yaml:"avoid_repeat_days"`  // 避免重复展示的天数
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
	JWTSecret    string `yaml:"jwt_secret"`     // JWT 密钥
	APIKeyPrefix string `yaml:"api_key_prefix"` // API Key 前缀
}

// Load 加载配置文件
func Load(path string) (*Config, error) {
	// 读取配置文件
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	// 解析 YAML
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	// 从环境变量覆盖敏感配置
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		cfg.Security.JWTSecret = secret
	}
	if apiKey := os.Getenv("QWEN_API_KEY"); apiKey != "" {
		cfg.AI.Qwen.APIKey = apiKey
	}
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		cfg.AI.OpenAI.APIKey = apiKey
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
		return fmt.Errorf("security.jwt_secret is required")
	}

	return nil
}
