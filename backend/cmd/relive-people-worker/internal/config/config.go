package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config People Worker 配置
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	PeopleWorker PeopleWorkerConfig `yaml:"people_worker"`
	ML           MLConfig           `yaml:"ml"`
	Download     DownloadConfig     `yaml:"download"`
	Logging      LoggingConfig      `yaml:"logging"`
}

// ServerConfig 服务端配置
type ServerConfig struct {
	Endpoint string `yaml:"endpoint"` // 服务端地址，如 http://nas:8080
	APIKey   string `yaml:"api_key"`  // API Key
	Timeout  int    `yaml:"timeout"`  // 请求超时（秒）
}

// PeopleWorkerConfig People Worker 配置
type PeopleWorkerConfig struct {
	WorkerID   string `yaml:"worker_id"`   // Worker 实例ID
	Workers    int    `yaml:"workers"`     // 并发处理数
	FetchLimit int    `yaml:"fetch_limit"` // 每批获取任务数
	RetryCount int    `yaml:"retry_count"` // 重试次数
	RetryDelay int    `yaml:"retry_delay"` // 重试延迟（秒）
}

// MLConfig ML 服务配置
type MLConfig struct {
	Endpoint string `yaml:"endpoint"` // ML 服务端地址
	Timeout  int    `yaml:"timeout"`  // 请求超时（秒）
}

// DownloadConfig 下载配置
type DownloadConfig struct {
	TempDir       string `yaml:"temp_dir"`       // 临时目录
	Timeout       int    `yaml:"timeout"`        // 下载超时（秒）
	MaxConcurrent int    `yaml:"max_concurrent"` // 最大并发下载
	RetryCount    int    `yaml:"retry_count"`    // 下载重试次数
}

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level   string `yaml:"level"`   // 日志级别
	Console bool   `yaml:"console"` // 是否输出到控制台
	File    string `yaml:"file"`    // 日志文件路径
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Endpoint: "http://localhost:8080",
			Timeout:  30,
		},
		PeopleWorker: PeopleWorkerConfig{
			WorkerID:   "",
			Workers:    4,
			FetchLimit: 10,
			RetryCount: 3,
			RetryDelay: 5,
		},
		ML: MLConfig{
			Endpoint: "http://localhost:5050",
			Timeout:  15,
		},
		Download: DownloadConfig{
			TempDir:       "~/.relive-people-worker/temp",
			Timeout:       30,
			MaxConcurrent: 4,
			RetryCount:    3,
		},
		Logging: LoggingConfig{
			Level:   "info",
			Console: true,
			File:    "",
		},
	}
}

// Load 从文件加载配置
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，返回默认配置
			return cfg, nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	// 展开环境变量
	cfg.expandEnv()

	// 验证配置
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// Save 保存配置到文件
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return nil
}

// expandEnv 展开环境变量
func (c *Config) expandEnv() {
	c.Server.APIKey = expandEnvVar(c.Server.APIKey)
	c.Server.Endpoint = expandEnvVar(c.Server.Endpoint)
	c.PeopleWorker.WorkerID = expandEnvVar(c.PeopleWorker.WorkerID)
	c.Download.TempDir = expandEnvVar(c.Download.TempDir)
	c.Logging.File = expandEnvVar(c.Logging.File)
	c.ML.Endpoint = expandEnvVar(c.ML.Endpoint)
}

// expandEnvVar 展开单个环境变量
func expandEnvVar(value string) string {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		varName := value[2 : len(value)-1]
		if envVal := os.Getenv(varName); envVal != "" {
			return envVal
		}
	}
	return value
}

// Validate 验证配置
func (c *Config) Validate() error {
	if c.Server.Endpoint == "" {
		return fmt.Errorf("server.endpoint is required")
	}

	if c.Server.APIKey == "" {
		return fmt.Errorf("server.api_key is required")
	}

	if c.PeopleWorker.Workers <= 0 {
		c.PeopleWorker.Workers = 4
	}

	if c.PeopleWorker.FetchLimit <= 0 {
		c.PeopleWorker.FetchLimit = 10
	}

	if c.ML.Endpoint == "" {
		return fmt.Errorf("ml.endpoint is required")
	}

	return nil
}

// GetServerTimeout 获取服务端超时
func (c *Config) GetServerTimeout() time.Duration {
	return time.Duration(c.Server.Timeout) * time.Second
}

// GetRetryDelay 获取重试延迟
func (c *Config) GetRetryDelay() time.Duration {
	return time.Duration(c.PeopleWorker.RetryDelay) * time.Second
}

// GetDownloadTimeout 获取下载超时
func (c *Config) GetDownloadTimeout() time.Duration {
	return time.Duration(c.Download.Timeout) * time.Second
}

// GetMLTimeout 获取 ML 超时
func (c *Config) GetMLTimeout() time.Duration {
	return time.Duration(c.ML.Timeout) * time.Second
}

// GenerateSampleConfig 生成示例配置文件
func GenerateSampleConfig() string {
	return `# relive-people-worker 配置示例
# 用于 Mac M4 等高性能设备离线执行人脸检测

server:
  endpoint: "http://nas:8080"           # NAS 服务端地址
  api_key: "${RELIVE_API_KEY}"          # API Key（从环境变量读取）
  timeout: 30                           # API 请求超时（秒）

people_worker:
  worker_id: "mac-m4"                   # Worker 实例ID（用于标识）
  workers: 4                            # 并发处理数（M4 Mac 推荐 4）
  fetch_limit: 10                       # 每批获取任务数
  retry_count: 3                        # 重试次数
  retry_delay: 5                        # 重试延迟（秒）

ml:
  endpoint: "http://localhost:5050"     # relive-ml 服务地址
  timeout: 15                           # 人脸检测超时（秒）

download:
  temp_dir: "~/.relive-people-worker/temp"  # 临时文件目录
  timeout: 30                           # 下载超时（秒）
  max_concurrent: 4                     # 最大并发下载
  retry_count: 3                        # 下载重试次数

logging:
  level: "info"                         # 日志级别: debug, info, warn, error
  console: true                         # 输出到控制台
  file: ""                              # 日志文件路径（空表示不写入文件）
`
}
