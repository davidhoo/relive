package service

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/provider"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

// AIService AI 分析服务接口
type AIService interface {
	// AnalyzePhoto 分析单张照片
	AnalyzePhoto(photoID uint) error

	// AnalyzeBatch 批量分析照片
	AnalyzeBatch(limit int) (*model.AIAnalyzeBatchResponse, error)

	// GetAnalyzeProgress 获取分析进度
	GetAnalyzeProgress() (*model.AIAnalyzeProgressResponse, error)

	// GetProvider 获取当前使用的 provider
	GetProvider() (provider.AIProvider, error)

	// ReloadProvider 重新加载 AI provider（配置变更后调用）
	ReloadProvider() error
}

// aiService AI 分析服务实现
type aiService struct {
	photoRepo    repository.PhotoRepository
	config       *config.Config
	configService ConfigService
	provider     provider.AIProvider
}

// AIConfigFromDB 数据库中存储的 AI 配置结构
type AIConfigFromDB struct {
	Provider    string  `json:"provider"`
	Temperature float64 `json:"temperature"`
	Timeout     int     `json:"timeout"`

	// Ollama
	OllamaEndpoint    string  `json:"ollama_endpoint"`
	OllamaModel       string  `json:"ollama_model"`
	OllamaTemperature float64 `json:"ollama_temperature"`
	OllamaTimeout     int     `json:"ollama_timeout"`

	// Qwen
	QwenAPIKey      string  `json:"qwen_api_key"`
	QwenEndpoint    string  `json:"qwen_endpoint"`
	QwenModel       string  `json:"qwen_model"`
	QwenTemperature float64 `json:"qwen_temperature"`
	QwenTimeout     int     `json:"qwen_timeout"`

	// OpenAI
	OpenAIAPIKey      string  `json:"openai_api_key"`
	OpenAIEndpoint    string  `json:"openai_endpoint"`
	OpenAIModel       string  `json:"openai_model"`
	OpenAITemperature float64 `json:"openai_temperature"`
	OpenAIMaxTokens   int     `json:"openai_max_tokens"`
	OpenAITimeout     int     `json:"openai_timeout"`

	// VLLM
	VLLMEndpoint    string  `json:"vllm_endpoint"`
	VLLMModel       string  `json:"vllm_model"`
	VLLMTemperature float64 `json:"vllm_temperature"`
	VLLMMaxTokens   int     `json:"vllm_max_tokens"`
	VLLMTimeout     int     `json:"vllm_timeout"`

	// Hybrid
	HybridPrimary      string `json:"hybrid_primary"`
	HybridFallback     string `json:"hybrid_fallback"`
	HybridRetryOnError bool   `json:"hybrid_retry_on_error"`
}

// NewAIService 创建 AI 分析服务
func NewAIService(photoRepo repository.PhotoRepository, cfg *config.Config, configService ConfigService) (AIService, error) {
	svc := &aiService{
		photoRepo:    photoRepo,
		config:       cfg,
		configService: configService,
	}

	// 初始化 provider
	if err := svc.initProvider(); err != nil {
		return nil, fmt.Errorf("init provider: %w", err)
	}

	return svc, nil
}

// initProvider 初始化 AI provider
func (s *aiService) initProvider() error {
	// 尝试从数据库加载 AI 配置
	aiConfig := s.loadAIConfig()

	if aiConfig.Provider == "" {
		logger.Warn("AI provider not configured, AI analysis will not be available")
		return nil
	}

	var (
		p   provider.AIProvider
		err error
	)

	switch aiConfig.Provider {
	case "ollama":
		p, err = provider.NewOllamaProvider(&provider.OllamaConfig{
			Endpoint:    aiConfig.OllamaEndpoint,
			Model:       aiConfig.OllamaModel,
			Temperature: aiConfig.OllamaTemperature,
			Timeout:     aiConfig.OllamaTimeout,
		})
	case "qwen":
		p, err = provider.NewQwenProvider(&provider.QwenConfig{
			APIKey:      aiConfig.QwenAPIKey,
			Endpoint:    aiConfig.QwenEndpoint,
			Model:       aiConfig.QwenModel,
			Temperature: aiConfig.QwenTemperature,
			Timeout:     aiConfig.QwenTimeout,
		})
	case "openai":
		p, err = provider.NewOpenAIProvider(&provider.OpenAIConfig{
			APIKey:      aiConfig.OpenAIAPIKey,
			Endpoint:    aiConfig.OpenAIEndpoint,
			Model:       aiConfig.OpenAIModel,
			Temperature: aiConfig.OpenAITemperature,
			MaxTokens:   aiConfig.OpenAIMaxTokens,
			Timeout:     aiConfig.OpenAITimeout,
		})
	case "vllm":
		p, err = provider.NewVLLMProvider(&provider.VLLMConfig{
			Endpoint:    aiConfig.VLLMEndpoint,
			Model:       aiConfig.VLLMModel,
			Temperature: aiConfig.VLLMTemperature,
			MaxTokens:   aiConfig.VLLMMaxTokens,
			Timeout:     aiConfig.VLLMTimeout,
		})
	case "hybrid":
		// 构建 hybrid provider 配置
		primaryConfig, err := s.getProviderConfigFromDB(aiConfig.HybridPrimary, aiConfig)
		if err != nil {
			return fmt.Errorf("get primary provider config: %w", err)
		}

		var fallbackConfig interface{}
		if aiConfig.HybridFallback != "" {
			fallbackConfig, err = s.getProviderConfigFromDB(aiConfig.HybridFallback, aiConfig)
			if err != nil {
				logger.Warnf("Failed to get fallback provider config: %v", err)
				fallbackConfig = nil
			}
		}

		p, err = provider.NewHybridProvider(&provider.HybridConfig{
			Primary:        aiConfig.HybridPrimary,
			Fallback:       aiConfig.HybridFallback,
			PrimaryConfig:  primaryConfig,
			FallbackConfig: fallbackConfig,
		})
	default:
		return fmt.Errorf("unknown AI provider: %s", aiConfig.Provider)
	}

	if err != nil {
		return err
	}

	// 检查 provider 是否可用
	if !p.IsAvailable() {
		return fmt.Errorf("AI provider %s is not available", aiConfig.Provider)
	}

	s.provider = p
	logger.Infof("AI provider initialized: %s (cost=¥%.4f per photo)", p.Name(), p.Cost())

	return nil
}

// loadAIConfig 加载 AI 配置（优先从数据库，其次从 YAML）
func (s *aiService) loadAIConfig() *AIConfigFromDB {
	aiConfig := &AIConfigFromDB{
		// 默认值从 YAML 配置读取
		Provider:    s.config.AI.Provider,
		Temperature: s.config.AI.Temperature,
		Timeout:     s.config.AI.Timeout,

		OllamaEndpoint:    s.config.AI.Ollama.Endpoint,
		OllamaModel:       s.config.AI.Ollama.Model,
		OllamaTemperature: s.config.AI.Ollama.Temperature,
		OllamaTimeout:     s.config.AI.Ollama.Timeout,

		QwenAPIKey:      s.config.AI.Qwen.APIKey,
		QwenEndpoint:    s.config.AI.Qwen.Endpoint,
		QwenModel:       s.config.AI.Qwen.Model,
		QwenTemperature: s.config.AI.Qwen.Temperature,
		QwenTimeout:     s.config.AI.Qwen.Timeout,

		OpenAIAPIKey:      s.config.AI.OpenAI.APIKey,
		OpenAIEndpoint:    s.config.AI.OpenAI.Endpoint,
		OpenAIModel:       s.config.AI.OpenAI.Model,
		OpenAITemperature: s.config.AI.OpenAI.Temperature,
		OpenAIMaxTokens:   s.config.AI.OpenAI.MaxTokens,
		OpenAITimeout:     s.config.AI.OpenAI.Timeout,

		VLLMEndpoint:    s.config.AI.VLLM.Endpoint,
		VLLMModel:       s.config.AI.VLLM.Model,
		VLLMTemperature: s.config.AI.VLLM.Temperature,
		VLLMMaxTokens:   s.config.AI.VLLM.MaxTokens,
		VLLMTimeout:     s.config.AI.VLLM.Timeout,

		HybridPrimary:      s.config.AI.Hybrid.Primary,
		HybridFallback:     s.config.AI.Hybrid.Fallback,
		HybridRetryOnError: s.config.AI.Hybrid.RetryOnError,
	}

	// 尝试从数据库读取配置
	if s.configService != nil {
		dbConfig, err := s.configService.Get("ai")
		if err == nil && dbConfig != nil && dbConfig.Value != "" {
			var dbAIConfig AIConfigFromDB
			if err := json.Unmarshal([]byte(dbConfig.Value), &dbAIConfig); err == nil {
				// 数据库配置覆盖 YAML 配置
				logger.Info("Loading AI config from database")
				aiConfig = &dbAIConfig
			}
		}
	}

	// 设置默认值
	if aiConfig.Temperature == 0 {
		aiConfig.Temperature = 0.7
	}
	if aiConfig.Timeout == 0 {
		aiConfig.Timeout = 60
	}

	return aiConfig
}

// getProviderConfigFromDB 从数据库配置获取指定 provider 的配置
func (s *aiService) getProviderConfigFromDB(providerName string, aiConfig *AIConfigFromDB) (interface{}, error) {
	switch providerName {
	case "ollama":
		return &provider.OllamaConfig{
			Endpoint:    aiConfig.OllamaEndpoint,
			Model:       aiConfig.OllamaModel,
			Temperature: aiConfig.OllamaTemperature,
			Timeout:     aiConfig.OllamaTimeout,
		}, nil
	case "qwen":
		return &provider.QwenConfig{
			APIKey:      aiConfig.QwenAPIKey,
			Endpoint:    aiConfig.QwenEndpoint,
			Model:       aiConfig.QwenModel,
			Temperature: aiConfig.QwenTemperature,
			Timeout:     aiConfig.QwenTimeout,
		}, nil
	case "openai":
		return &provider.OpenAIConfig{
			APIKey:      aiConfig.OpenAIAPIKey,
			Endpoint:    aiConfig.OpenAIEndpoint,
			Model:       aiConfig.OpenAIModel,
			Temperature: aiConfig.OpenAITemperature,
			MaxTokens:   aiConfig.OpenAIMaxTokens,
			Timeout:     aiConfig.OpenAITimeout,
		}, nil
	case "vllm":
		return &provider.VLLMConfig{
			Endpoint:    aiConfig.VLLMEndpoint,
			Model:       aiConfig.VLLMModel,
			Temperature: aiConfig.VLLMTemperature,
			MaxTokens:   aiConfig.VLLMMaxTokens,
			Timeout:     aiConfig.VLLMTimeout,
		}, nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}
}

// getProviderConfig 获取指定 provider 的配置
func (s *aiService) getProviderConfig(providerName string) (interface{}, error) {
	switch providerName {
	case "ollama":
		return &provider.OllamaConfig{
			Endpoint:    s.config.AI.Ollama.Endpoint,
			Model:       s.config.AI.Ollama.Model,
			Temperature: s.config.AI.Ollama.Temperature,
			Timeout:     s.config.AI.Ollama.Timeout,
		}, nil
	case "qwen":
		return &provider.QwenConfig{
			APIKey:      s.config.AI.Qwen.APIKey,
			Endpoint:    s.config.AI.Qwen.Endpoint,
			Model:       s.config.AI.Qwen.Model,
			Temperature: s.config.AI.Qwen.Temperature,
			Timeout:     s.config.AI.Qwen.Timeout,
		}, nil
	case "openai":
		return &provider.OpenAIConfig{
			APIKey:      s.config.AI.OpenAI.APIKey,
			Endpoint:    s.config.AI.OpenAI.Endpoint,
			Model:       s.config.AI.OpenAI.Model,
			Temperature: s.config.AI.OpenAI.Temperature,
			MaxTokens:   s.config.AI.OpenAI.MaxTokens,
			Timeout:     s.config.AI.OpenAI.Timeout,
		}, nil
	case "vllm":
		return &provider.VLLMConfig{
			Endpoint:    s.config.AI.VLLM.Endpoint,
			Model:       s.config.AI.VLLM.Model,
			Temperature: s.config.AI.VLLM.Temperature,
			MaxTokens:   s.config.AI.VLLM.MaxTokens,
			Timeout:     s.config.AI.VLLM.Timeout,
		}, nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}
}

// GetProvider 获取当前使用的 provider
func (s *aiService) GetProvider() (provider.AIProvider, error) {
	if s.provider == nil {
		return nil, fmt.Errorf("AI provider not configured")
	}
	return s.provider, nil
}

// ReloadProvider 重新加载 AI provider（配置变更后调用）
func (s *aiService) ReloadProvider() error {
	logger.Info("Reloading AI provider due to configuration change...")

	// 重置当前 provider
	s.provider = nil

	// 重新初始化 provider
	if err := s.initProvider(); err != nil {
		return fmt.Errorf("failed to reload AI provider: %w", err)
	}

	if s.provider != nil {
		logger.Infof("AI provider reloaded successfully: %s", s.provider.Name())
	} else {
		logger.Info("AI provider cleared (no provider configured)")
	}

	return nil
}

// AnalyzePhoto 分析单张照片
func (s *aiService) AnalyzePhoto(photoID uint) error {
	if s.provider == nil {
		return fmt.Errorf("AI provider not configured")
	}

	// 获取照片信息
	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		return fmt.Errorf("get photo: %w", err)
	}

	// 检查是否已分析
	if photo.AIAnalyzed {
		logger.Warnf("Photo %d already analyzed, skipping", photoID)
		return nil
	}

	// 读取照片文件
	imageData, err := os.ReadFile(photo.FilePath)
	if err != nil {
		return fmt.Errorf("read image file: %w", err)
	}

	// 预处理图片（压缩）
	processor := &util.ImageProcessor{
		MaxLongSide: 1024,
		JPEGQuality: 85,
	}
	processedData, err := processor.ProcessForAI(photo.FilePath)
	if err != nil {
		logger.Warnf("Image preprocessing failed, using original: %v", err)
		processedData = imageData
	}

	// 构建分析请求
	req := &provider.AnalyzeRequest{
		ImageData: processedData,
		ImagePath: photo.FilePath,
		ExifInfo: &provider.ExifInfo{
			DateTime: formatDateTime(photo.TakenAt),
			City:     photo.Location,
			Model:    photo.CameraModel,
		},
		Options: &provider.AnalyzeOptions{
			Temperature: s.config.AI.Temperature,
			Timeout:     time.Duration(s.config.AI.Timeout) * time.Second,
		},
	}

	// 调用 AI 分析
	logger.Infof("Analyzing photo %d with provider %s...", photoID, s.provider.Name())
	result, err := s.provider.Analyze(req)
	if err != nil {
		return fmt.Errorf("analyze photo: %w", err)
	}

	// 更新照片记录
	now := time.Now()
	photo.AIAnalyzed = true
	photo.AIProvider = s.provider.Name() // 保存 AI 提供商名称
	photo.Description = result.Description
	photo.Caption = result.Caption
	photo.MainCategory = result.MainCategory
	photo.Tags = result.Tags
	photo.MemoryScore = int(result.MemoryScore)
	photo.BeautyScore = int(result.BeautyScore)
	photo.AnalyzedAt = &now

	// 计算综合评分
	photo.OverallScore = int(float64(photo.MemoryScore)*0.7 + float64(photo.BeautyScore)*0.3)

	if err := s.photoRepo.Update(photo); err != nil {
		return fmt.Errorf("update photo: %w", err)
	}

	logger.Infof("Photo %d analyzed successfully: memory=%d, beauty=%d, overall=%d, duration=%v",
		photoID, photo.MemoryScore, photo.BeautyScore, photo.OverallScore, result.Duration)

	return nil
}

// AnalyzeBatch 批量分析照片
func (s *aiService) AnalyzeBatch(limit int) (*model.AIAnalyzeBatchResponse, error) {
	if s.provider == nil {
		return nil, fmt.Errorf("AI provider not configured")
	}

	// 获取未分析的照片
	photos, err := s.photoRepo.GetUnanalyzed(limit)
	if err != nil {
		return nil, fmt.Errorf("get unanalyzed photos: %w", err)
	}

	if len(photos) == 0 {
		return &model.AIAnalyzeBatchResponse{
			TotalCount:   0,
			SuccessCount: 0,
			FailedCount:  0,
			TotalCost:    0,
		}, nil
	}

	logger.Infof("Starting batch analysis: %d photos", len(photos))

	successCount := 0
	failedCount := 0
	totalCost := 0.0
	startTime := time.Now()

	// 逐个分析（后续可优化为并发）
	for i, photo := range photos {
		logger.Infof("Analyzing photo %d/%d: id=%d, path=%s", i+1, len(photos), photo.ID, photo.FileName)

		err := s.AnalyzePhoto(photo.ID)
		if err != nil {
			logger.Errorf("Failed to analyze photo %d: %v", photo.ID, err)
			failedCount++
			continue
		}

		successCount++
		totalCost += s.provider.Cost()
	}

	duration := time.Since(startTime)

	logger.Infof("Batch analysis completed: total=%d, success=%d, failed=%d, cost=¥%.2f, duration=%v",
		len(photos), successCount, failedCount, totalCost, duration)

	return &model.AIAnalyzeBatchResponse{
		TotalCount:   len(photos),
		SuccessCount: successCount,
		FailedCount:  failedCount,
		TotalCost:    totalCost,
		Duration:     duration.Seconds(),
	}, nil
}

// GetAnalyzeProgress 获取分析进度
func (s *aiService) GetAnalyzeProgress() (*model.AIAnalyzeProgressResponse, error) {
	// 统计总数
	total, err := s.photoRepo.Count()
	if err != nil {
		return nil, fmt.Errorf("count total: %w", err)
	}

	// 统计已分析数
	analyzed, err := s.photoRepo.CountAnalyzed()
	if err != nil {
		return nil, fmt.Errorf("count analyzed: %w", err)
	}

	// 统计未分析数
	unanalyzed, err := s.photoRepo.CountUnanalyzed()
	if err != nil {
		return nil, fmt.Errorf("count unanalyzed: %w", err)
	}

	// 计算进度百分比
	progress := 0.0
	if total > 0 {
		progress = float64(analyzed) / float64(total) * 100
	}

	// 估算剩余成本（如果 provider 可用）
	estimatedCost := 0.0
	if s.provider != nil {
		estimatedCost = float64(unanalyzed) * s.provider.Cost()
	}

	return &model.AIAnalyzeProgressResponse{
		Total:         total,
		Analyzed:      analyzed,
		Unanalyzed:    unanalyzed,
		Progress:      progress,
		EstimatedCost: estimatedCost,
		Provider:      s.config.AI.Provider,
	}, nil
}

// formatDateTime 格式化日期时间
func formatDateTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
