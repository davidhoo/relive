package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 分析器配置
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Analyzer AnalyzerConfig `yaml:"analyzer"`
	AI       AIConfig       `yaml:"ai"`
	Download DownloadConfig `yaml:"download"`
	Batch    BatchConfig    `yaml:"batch"`
	Logging  LoggingConfig  `yaml:"logging"`
}

// ServerConfig 服务端配置
type ServerConfig struct {
	Endpoint string `yaml:"endpoint"`           // 服务端地址，如 http://nas:8080
	APIKey   string `yaml:"api_key"`            // API Key
	Timeout  int    `yaml:"timeout"`            // 请求超时（秒）
}

// AnalyzerConfig 分析器配置
type AnalyzerConfig struct {
	Workers          int    `yaml:"workers"`           // 并发分析数
	FetchLimit       int    `yaml:"fetch_limit"`       // 每批获取任务数
	RetryCount       int    `yaml:"retry_count"`       // AI 分析重试次数
	RetryDelay       int    `yaml:"retry_delay"`       // 重试延迟（秒）
	CheckpointFile   string `yaml:"checkpoint_file"`   // 断点续传文件
	AnalyzerID       string `yaml:"analyzer_id"`       // 分析器实例ID（可选，自动生成）
}

// AIConfig AI 配置
type AIConfig struct {
	Provider string         `yaml:"provider"` // ollama 或 vllm
	Ollama   OllamaConfig   `yaml:"ollama"`
	VLLM     VLLMConfig     `yaml:"vllm"`
}

// OllamaConfig Ollama 配置
type OllamaConfig struct {
	Endpoint    string  `yaml:"endpoint"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	Timeout     int     `yaml:"timeout"`
}

// VLLMConfig VLLM 配置
type VLLMConfig struct {
	Endpoint    string  `yaml:"endpoint"`
	Model       string  `yaml:"model"`
	Temperature float64 `yaml:"temperature"`
	Timeout     int     `yaml:"timeout"`
}

// DownloadConfig 下载配置
type DownloadConfig struct {
	TempDir       string `yaml:"temp_dir"`        // 临时目录
	Timeout       int    `yaml:"timeout"`         // 下载超时（秒）
	MaxConcurrent int    `yaml:"max_concurrent"`  // 最大并发下载
	RetryCount    int    `yaml:"retry_count"`     // 下载重试次数
	KeepTemp      bool   `yaml:"keep_temp"`       // 是否保留临时文件
}

// BatchConfig 批量提交配置
type BatchConfig struct {
	Size           int `yaml:"size"`            // 批量提交数量
	FlushInterval  int `yaml:"flush_interval"`  // 自动刷新间隔（秒）
	MaxRetry       int `yaml:"max_retry"`       // 提交失败重试次数
	RetryDelay     int `yaml:"retry_delay"`     // 重试延迟（秒）
}

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level      string `yaml:"level"`       // 日志级别
	Console    bool   `yaml:"console"`     // 是否输出到控制台
	File       string `yaml:"file"`        // 日志文件路径
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Endpoint: "http://localhost:8080",
			Timeout:  60,
		},
		Analyzer: AnalyzerConfig{
			Workers:        4,
			FetchLimit:     10,
			RetryCount:     3,
			RetryDelay:     5,
			CheckpointFile: "~/.relive-analyzer/checkpoint.db",
		},
		AI: AIConfig{
			Provider: "ollama",
			Ollama: OllamaConfig{
				Endpoint:    "http://localhost:11434",
				Model:       "llava:13b",
				Temperature: 0.7,
				Timeout:     120,
			},
			VLLM: VLLMConfig{
				Endpoint:    "http://localhost:8000",
				Model:       "llava-v1.6-vicuna-13b",
				Temperature: 0.7,
				Timeout:     120,
			},
		},
		Download: DownloadConfig{
			TempDir:       "~/.relive-analyzer/temp",
			Timeout:       60,
			MaxConcurrent: 5,
			RetryCount:    3,
			KeepTemp:      false,
		},
		Batch: BatchConfig{
			Size:          10,
			FlushInterval: 30,
			MaxRetry:      3,
			RetryDelay:    5,
		},
		Logging: LoggingConfig{
			Level:   "info",
			Console: true,
			File:    "analyzer.log",
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
	c.Analyzer.CheckpointFile = expandEnvVar(c.Analyzer.CheckpointFile)
	c.Download.TempDir = expandEnvVar(c.Download.TempDir)
	c.Logging.File = expandEnvVar(c.Logging.File)

	// AI 配置
	c.AI.Ollama.Endpoint = expandEnvVar(c.AI.Ollama.Endpoint)
	c.AI.Ollama.Model = expandEnvVar(c.AI.Ollama.Model)
	c.AI.VLLM.Endpoint = expandEnvVar(c.AI.VLLM.Endpoint)
	c.AI.VLLM.Model = expandEnvVar(c.AI.VLLM.Model)
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

	if c.Analyzer.Workers <= 0 {
		c.Analyzer.Workers = 4
	}

	if c.Analyzer.FetchLimit <= 0 {
		c.Analyzer.FetchLimit = 10
	}

	if c.AI.Provider != "ollama" && c.AI.Provider != "vllm" {
		return fmt.Errorf("ai.provider must be 'ollama' or 'vllm'")
	}

	return nil
}

// GetServerTimeout 获取服务端超时
func (c *Config) GetServerTimeout() time.Duration {
	return time.Duration(c.Server.Timeout) * time.Second
}

// GetRetryDelay 获取重试延迟
func (c *Config) GetRetryDelay() time.Duration {
	return time.Duration(c.Analyzer.RetryDelay) * time.Second
}

// GetDownloadTimeout 获取下载超时
func (c *Config) GetDownloadTimeout() time.Duration {
	return time.Duration(c.Download.Timeout) * time.Second
}

// GetFlushInterval 获取刷新间隔
func (c *Config) GetFlushInterval() time.Duration {
	return time.Duration(c.Batch.FlushInterval) * time.Second
}

// GetAITimeout 获取 AI 超时
func (c *Config) GetAITimeout() time.Duration {
	switch c.AI.Provider {
	case "ollama":
		return time.Duration(c.AI.Ollama.Timeout) * time.Second
	case "vllm":
		return time.Duration(c.AI.VLLM.Timeout) * time.Second
	default:
		return 120 * time.Second
	}
}

// GenerateSampleConfig 生成示例配置文件
func GenerateSampleConfig() string {
	return `# relive-analyzer API 模式配置示例

server:
  endpoint: "http://nas:8080"           # 服务端地址
  api_key: "${RELIVE_API_KEY}"          # API Key（从环境变量读取）
  timeout: 30                           # API 请求超时（秒）

analyzer:
  workers: 4                            # 并发分析数
  fetch_limit: 10                       # 每批获取任务数
  retry_count: 3                        # AI 分析重试次数
  retry_delay: 5                        # 重试延迟（秒）
  checkpoint_file: "~/.relive-analyzer/checkpoint.db"  # 断点续传文件

ai:
  provider: "ollama"                    # AI Provider: ollama 或 vllm
  ollama:
    endpoint: "http://localhost:11434"
    model: "llava:13b"
    temperature: 0.7
    timeout: 120
  vllm:
    endpoint: "http://localhost:8000"
    model: "llava-v1.6-vicuna-13b"
    temperature: 0.7
    timeout: 120

download:
  temp_dir: "~/.relive-analyzer/temp"   # 临时文件目录
  timeout: 60                           # 下载超时（秒）
  max_concurrent: 5                     # 最大并发下载
  retry_count: 3                        # 下载重试次数
  keep_temp: false                      # 是否保留临时文件（调试用）

batch:
  size: 10                              # 批量提交数量
  flush_interval: 30                    # 自动刷新间隔（秒）
  max_retry: 3                          # 提交失败重试次数
  retry_delay: 5                        # 重试延迟（秒）

logging:
  level: "info"                         # 日志级别: debug, info, warn, error
  console: true                         # 输出到控制台
  file: "analyzer.log"                  # 日志文件路径
`
}
