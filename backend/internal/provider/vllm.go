package provider

import (
	"fmt"
)

// VLLMConfig VLLM 配置
type VLLMConfig struct {
	Endpoint    string  `yaml:"endpoint"`    // VLLM 服务地址
	Model       string  `yaml:"model"`       // 模型名称
	Temperature float64 `yaml:"temperature"` // 温度参数
	Timeout     int     `yaml:"timeout"`     // 超时（秒）
}

// VLLMProvider VLLM 提供者
type VLLMProvider struct {
	config *VLLMConfig
}

// NewVLLMProvider 创建 VLLM provider
func NewVLLMProvider(config *VLLMConfig) (*VLLMProvider, error) {
	if config.Endpoint == "" {
		return nil, fmt.Errorf("vllm endpoint is required")
	}

	return &VLLMProvider{
		config: config,
	}, nil
}

// Name 返回 provider 名称
func (p *VLLMProvider) Name() string {
	return "vllm"
}

// Cost 返回单次调用成本（自部署，免费）
func (p *VLLMProvider) Cost() float64 {
	return 0.0
}

// IsAvailable 检查服务是否可用
func (p *VLLMProvider) IsAvailable() bool {
	// TODO: 实现健康检查
	return false
}

// MaxConcurrency 最大并发数
func (p *VLLMProvider) MaxConcurrency() int {
	return 4
}

// Analyze 分析照片
func (p *VLLMProvider) Analyze(request *AnalyzeRequest) (*AnalyzeResult, error) {
	// TODO: 实现 VLLM 分析逻辑
	return nil, fmt.Errorf("vllm provider not implemented yet")
}
