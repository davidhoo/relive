package provider

import (
	"fmt"
	"time"
)

// ProviderType provider 类型
type ProviderType string

const (
	ProviderTypeQwen   ProviderType = "qwen"
	ProviderTypeOllama ProviderType = "ollama"
	ProviderTypeOpenAI ProviderType = "openai"
	ProviderTypeVLLM   ProviderType = "vllm"
	ProviderTypeHybrid ProviderType = "hybrid"
)

// AIProvider 统一的 AI 提供商接口
type AIProvider interface {
	// Analyze 分析照片
	Analyze(request *AnalyzeRequest) (*AnalyzeResult, error)

	// Name 返回 provider 名称
	Name() string

	// Cost 返回单次调用成本（人民币）
	Cost() float64

	// IsAvailable 检查服务是否可用
	IsAvailable() bool

	// MaxConcurrency 最大并发数（防止过载）
	MaxConcurrency() int
}

// AnalyzeRequest 分析请求
type AnalyzeRequest struct {
	// 图片数据（已预处理）
	ImageData []byte
	ImagePath string

	// EXIF 信息（辅助分析）
	ExifInfo *ExifInfo

	// 配置选项
	Options *AnalyzeOptions
}

// ExifInfo EXIF 信息
type ExifInfo struct {
	DateTime string // 拍摄时间
	City     string // 拍摄城市
	Make     string // 相机品牌
	Model    string // 相机型号
}

// AnalyzeOptions 分析选项
type AnalyzeOptions struct {
	Temperature float64       // 温度参数（0.0-1.0）
	MaxTokens   int           // 最大 token 数
	Timeout     time.Duration // 超时时间
}

// AnalyzeResult 分析结果
type AnalyzeResult struct {
	// AI 生成内容
	Description  string // 详细描述（80-200字）
	Caption      string // 短文案（8-30字）
	MainCategory string // 主分类
	Tags         string // 辅助标签（逗号分隔）

	// 评分
	MemoryScore float64 // 回忆价值（0-100）
	BeautyScore float64 // 美观度（0-100）
	Reason      string  // 评分理由（≤40字）

	// 元数据
	Provider   string        // 使用的 provider
	ModelName  string        // 使用的模型
	Timestamp  time.Time     // 分析时间
	Duration   time.Duration // 耗时
	TokensUsed int           // 消耗的 tokens
	Cost       float64       // 实际成本
}

// NewProvider 创建 provider
func NewProvider(providerType ProviderType, config interface{}) (AIProvider, error) {
	switch providerType {
	case ProviderTypeQwen:
		if cfg, ok := config.(*QwenConfig); ok {
			return NewQwenProvider(cfg)
		}
		return nil, fmt.Errorf("invalid qwen config type")
	case ProviderTypeOllama:
		if cfg, ok := config.(*OllamaConfig); ok {
			return NewOllamaProvider(cfg)
		}
		return nil, fmt.Errorf("invalid ollama config type")
	case ProviderTypeOpenAI:
		if cfg, ok := config.(*OpenAIConfig); ok {
			return NewOpenAIProvider(cfg)
		}
		return nil, fmt.Errorf("invalid openai config type")
	case ProviderTypeVLLM:
		if cfg, ok := config.(*VLLMConfig); ok {
			return NewVLLMProvider(cfg)
		}
		return nil, fmt.Errorf("invalid vllm config type")
	case ProviderTypeHybrid:
		if cfg, ok := config.(*HybridConfig); ok {
			return NewHybridProvider(cfg)
		}
		return nil, fmt.Errorf("invalid hybrid config type")
	default:
		return nil, fmt.Errorf("unknown provider type: %s", providerType)
	}
}
