package provider

import (
	"fmt"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

// HybridConfig 混合模式配置
type HybridConfig struct {
	Primary   string      `yaml:"primary"`   // 主要 provider
	Fallback  string      `yaml:"fallback"`  // 备用 provider
	PrimaryConfig  interface{} `yaml:"primary_config"`
	FallbackConfig interface{} `yaml:"fallback_config"`
}

// HybridProvider 混合模式提供者
type HybridProvider struct {
	config   *HybridConfig
	primary  AIProvider
	fallback AIProvider
}

// NewHybridProvider 创建 Hybrid provider
func NewHybridProvider(config *HybridConfig) (*HybridProvider, error) {
	if config.Primary == "" {
		return nil, fmt.Errorf("primary provider is required")
	}

	// 创建主 provider
	primary, err := NewProvider(ProviderType(config.Primary), config.PrimaryConfig)
	if err != nil {
		return nil, fmt.Errorf("create primary provider: %w", err)
	}

	// 创建备用 provider（可选）
	var fallback AIProvider
	if config.Fallback != "" {
		fallback, err = NewProvider(ProviderType(config.Fallback), config.FallbackConfig)
		if err != nil {
			return nil, fmt.Errorf("create fallback provider: %w", err)
		}
	}

	return &HybridProvider{
		config:   config,
		primary:  primary,
		fallback: fallback,
	}, nil
}

// Name 返回 provider 名称
func (p *HybridProvider) Name() string {
	return "hybrid"
}

// Cost 返回单次调用成本（取主 provider 成本）
func (p *HybridProvider) Cost() float64 {
	return p.primary.Cost()
}

// IsAvailable 检查服务是否可用
func (p *HybridProvider) IsAvailable() bool {
	if p.primary.IsAvailable() {
		return true
	}
	if p.fallback != nil {
		return p.fallback.IsAvailable()
	}
	return false
}

// MaxConcurrency 最大并发数
func (p *HybridProvider) MaxConcurrency() int {
	return p.primary.MaxConcurrency()
}

// SupportsBatch 是否支持批量分析
func (p *HybridProvider) SupportsBatch() bool {
	return p.primary.SupportsBatch()
}

// MaxBatchSize 最大批量大小
func (p *HybridProvider) MaxBatchSize() int {
	return p.primary.MaxBatchSize()
}

// BatchCost 批量处理成本
func (p *HybridProvider) BatchCost() float64 {
	return p.primary.BatchCost()
}

// Analyze 分析照片
func (p *HybridProvider) Analyze(request *AnalyzeRequest) (*AnalyzeResult, error) {
	startTime := time.Now()

	// 尝试使用主 provider
	if p.primary.IsAvailable() {
		result, err := p.primary.Analyze(request)
		if err == nil {
			return result, nil
		}
		// 主 provider 失败，记录错误
		logger.Warnf("Primary provider %s failed: %v, trying fallback", p.primary.Name(), err)
	}

	// 主 provider 不可用或失败，使用备用 provider
	if p.fallback != nil && p.fallback.IsAvailable() {
		result, err := p.fallback.Analyze(request)
		if err == nil {
			// 标记使用了 fallback
			result.Provider = fmt.Sprintf("hybrid(%s->%s)", p.primary.Name(), p.fallback.Name())
			result.Duration = time.Since(startTime)
			return result, nil
		}
		return nil, fmt.Errorf("fallback provider %s also failed: %w", p.fallback.Name(), err)
	}

	return nil, fmt.Errorf("no available provider")
}

// AnalyzeBatch 批量分析照片
func (p *HybridProvider) AnalyzeBatch(requests []*AnalyzeRequest) ([]*AnalyzeResult, error) {
	startTime := time.Now()

	// 尝试使用主 provider
	if p.primary.IsAvailable() && p.primary.SupportsBatch() {
		results, err := p.primary.AnalyzeBatch(requests)
		if err == nil {
			return results, nil
		}
		// 主 provider 失败，记录错误
		logger.Warnf("Primary provider %s batch failed: %v, trying fallback", p.primary.Name(), err)
	}

	// 主 provider 不可用或不支持批量，使用备用 provider
	if p.fallback != nil && p.fallback.IsAvailable() {
		results, err := p.fallback.AnalyzeBatch(requests)
		if err == nil {
			// 标记使用了 fallback
			for _, result := range results {
				result.Provider = fmt.Sprintf("hybrid(%s->%s)", p.primary.Name(), p.fallback.Name())
				result.Duration = time.Since(startTime) / time.Duration(len(requests))
			}
			return results, nil
		}
		return nil, fmt.Errorf("fallback provider %s also failed: %w", p.fallback.Name(), err)
	}

	return nil, fmt.Errorf("no available provider for batch analysis")
}

// GenerateCaption 生成照片文案（第二次会话）
// 只看照片，直接生成创意文案
func (p *HybridProvider) GenerateCaption(request *AnalyzeRequest) (string, error) {
	// 尝试使用主 provider
	if p.primary.IsAvailable() {
		caption, err := p.primary.GenerateCaption(request)
		if err == nil {
			return caption, nil
		}
		logger.Warnf("Primary provider %s caption generation failed: %v, trying fallback", p.primary.Name(), err)
	}

	// 主 provider 不可用或失败，使用备用 provider
	if p.fallback != nil && p.fallback.IsAvailable() {
		caption, err := p.fallback.GenerateCaption(request)
		if err == nil {
			return caption, nil
		}
		return "", fmt.Errorf("fallback provider %s also failed: %w", p.fallback.Name(), err)
	}

	return "", fmt.Errorf("no available provider for caption generation")
}
