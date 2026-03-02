package service

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/provider"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

// AnalyzeTask 分析任务状态
type AnalyzeTask struct {
	ID            string    `json:"id"`
	Status        string    `json:"status"` // pending, running, completed, failed
	TotalCount    int       `json:"total_count"`
	SuccessCount  int       `json:"success_count"`
	FailedCount   int       `json:"failed_count"`
	CurrentIndex  int       `json:"current_index"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
}

// IsRunning 检查任务是否运行中
func (t *AnalyzeTask) IsRunning() bool {
	return t.Status == "running"
}

// AIService AI 分析服务接口
type AIService interface {
	// AnalyzePhoto 分析单张照片
	AnalyzePhoto(photoID uint) error

	// AnalyzeBatch 批量分析照片（异步启动）
	AnalyzeBatch(limit int) (*AnalyzeTask, error)

	// GetAnalyzeProgress 获取分析进度
	GetAnalyzeProgress() (*model.AIAnalyzeProgressResponse, error)

	// GetTaskStatus 获取任务状态
	GetTaskStatus() *AnalyzeTask

	// GetProvider 获取当前使用的 provider
	GetProvider() (provider.AIProvider, error)

	// ReloadProvider 重新加载 AI provider（配置变更后调用）
	ReloadProvider() error
}

// aiService AI 分析服务实现
type aiService struct {
	photoRepo     repository.PhotoRepository
	config        *config.Config
	configService ConfigService
	provider      provider.AIProvider
	currentTask   *AnalyzeTask
	taskMutex     sync.RWMutex
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
		aiConfig.Timeout = 120  // 默认 120 秒，支持更复杂的模型如 qwen3.5-plus
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
	// 对于较大的模型（如 qwen3.5-plus），减小图片大小可以加快处理速度
	processor := &util.ImageProcessor{
		MaxLongSide: 768,  // 减小到 768px 以加快上传和处理速度
		JPEGQuality: 80,   // 稍微降低质量以减小文件大小
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

// GetTaskStatus 获取当前任务状态
func (s *aiService) GetTaskStatus() *AnalyzeTask {
	s.taskMutex.RLock()
	defer s.taskMutex.RUnlock()
	return s.currentTask
}

// AnalyzeBatch 批量分析照片（异步启动）
func (s *aiService) AnalyzeBatch(limit int) (*AnalyzeTask, error) {
	if s.provider == nil {
		return nil, fmt.Errorf("AI provider not configured")
	}

	// 检查是否已有运行中的任务
	s.taskMutex.Lock()
	if s.currentTask != nil && s.currentTask.IsRunning() {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("analysis task already running")
	}

	// 获取未分析的照片
	photos, err := s.photoRepo.GetUnanalyzed(limit)
	if err != nil {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("get unanalyzed photos: %w", err)
	}

	if len(photos) == 0 {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("no unanalyzed photos found")
	}

	// 创建新任务
	task := &AnalyzeTask{
		ID:           fmt.Sprintf("task_%d", time.Now().Unix()),
		Status:       "running",
		TotalCount:   len(photos),
		SuccessCount: 0,
		FailedCount:  0,
		CurrentIndex: 0,
		StartedAt:    time.Now(),
	}
	s.currentTask = task
	s.taskMutex.Unlock()

	logger.Infof("Starting async batch analysis: %d photos, task_id=%s, provider supports batch: %v, batch size: %d",
		len(photos), task.ID, s.provider.SupportsBatch(), s.provider.MaxBatchSize())

	// 异步执行分析
	go s.runBatchAnalysis(task, photos)

	return task, nil
}

// runBatchAnalysis 后台执行批量分析
func (s *aiService) runBatchAnalysis(task *AnalyzeTask, photos []*model.Photo) {
	successCount, failedCount, _ := 0, 0, 0.0

	// 如果 provider 支持批量分析，使用批量模式
	if s.provider.SupportsBatch() && s.provider.MaxBatchSize() > 1 {
		successCount, failedCount, _ = s.analyzeInBatchesAsync(task, photos)
	} else {
		// 否则逐个分析
		successCount, failedCount, _ = s.analyzeOneByOneAsync(task, photos)
	}

	// 更新任务完成状态
	s.taskMutex.Lock()
	task.Status = "completed"
	task.SuccessCount = successCount
	task.FailedCount = failedCount
	now := time.Now()
	task.CompletedAt = &now
	s.taskMutex.Unlock()

	logger.Infof("Batch analysis task %s completed: total=%d, success=%d, failed=%d",
		task.ID, task.TotalCount, successCount, failedCount)
}

// analyzeOneByOneAsync 逐个分析照片（异步更新进度）
func (s *aiService) analyzeOneByOneAsync(task *AnalyzeTask, photos []*model.Photo) (successCount, failedCount int, totalCost float64) {
	for i, photo := range photos {
		// 更新当前索引
		s.taskMutex.Lock()
		task.CurrentIndex = i + 1
		s.taskMutex.Unlock()

		logger.Infof("[Task %s] Analyzing photo %d/%d: id=%d, path=%s", task.ID, i+1, len(photos), photo.ID, photo.FileName)

		err := s.AnalyzePhoto(photo.ID)
		if err != nil {
			logger.Errorf("[Task %s] Failed to analyze photo %d: %v", task.ID, photo.ID, err)
			failedCount++
		} else {
			successCount++
			totalCost += s.provider.Cost()
		}

		// 更新任务进度
		s.taskMutex.Lock()
		task.SuccessCount = successCount
		task.FailedCount = failedCount
		s.taskMutex.Unlock()
	}
	return successCount, failedCount, totalCost
}

// analyzeInBatchesAsync 分批批量分析照片（异步更新进度）
func (s *aiService) analyzeInBatchesAsync(task *AnalyzeTask, photos []*model.Photo) (successCount, failedCount int, totalCost float64) {
	batchSize := s.provider.MaxBatchSize()

	// 将照片分批
	for i := 0; i < len(photos); i += batchSize {
		end := i + batchSize
		if end > len(photos) {
			end = len(photos)
		}
		batch := photos[i:end]

		// 更新当前索引
		s.taskMutex.Lock()
		task.CurrentIndex = end
		s.taskMutex.Unlock()

		logger.Infof("[Task %s] Processing batch %d/%d: photos %d-%d", task.ID, i/batchSize+1, (len(photos)+batchSize-1)/batchSize, i+1, end)

		// 准备批量请求
		requests := make([]*provider.AnalyzeRequest, 0, len(batch))
		photoMap := make(map[int]*model.Photo)

		for j, photo := range batch {
			if photo.AIAnalyzed {
				continue
			}

			imageData, err := os.ReadFile(photo.FilePath)
			if err != nil {
				logger.Errorf("[Task %s] Failed to read photo %d: %v", task.ID, photo.ID, err)
				failedCount++
				continue
			}

			processor := &util.ImageProcessor{
				MaxLongSide: 768,
				JPEGQuality: 80,
			}
			processedData, err := processor.ProcessForAI(photo.FilePath)
			if err != nil {
				processedData = imageData
			}

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

			requests = append(requests, req)
			photoMap[len(requests)-1] = batch[j]
		}

		if len(requests) == 0 {
			continue
		}

		// 调用批量分析
		results, err := s.provider.AnalyzeBatch(requests)
		if err != nil {
			logger.Errorf("[Task %s] Batch analysis failed: %v", task.ID, err)
			// 批量失败，回退到逐个分析
			for idx := range photoMap {
				photo := photoMap[idx]
				if err := s.AnalyzePhoto(photo.ID); err != nil {
					logger.Errorf("[Task %s] Failed to analyze photo %d: %v", task.ID, photo.ID, err)
					failedCount++
				} else {
					successCount++
					totalCost += s.provider.Cost()
				}
			}
		} else {
			// 保存结果
			for idx, result := range results {
				photo, ok := photoMap[idx]
				if !ok {
					continue
				}

				now := time.Now()
				photo.AIAnalyzed = true
				photo.AIProvider = result.Provider
				photo.Description = result.Description
				photo.Caption = result.Caption
				photo.MainCategory = result.MainCategory
				photo.Tags = result.Tags
				photo.MemoryScore = int(result.MemoryScore)
				photo.BeautyScore = int(result.BeautyScore)
				photo.AnalyzedAt = &now
				photo.OverallScore = int(float64(photo.MemoryScore)*0.7 + float64(photo.BeautyScore)*0.3)

				if err := s.photoRepo.Update(photo); err != nil {
					logger.Errorf("[Task %s] Failed to update photo %d: %v", task.ID, photo.ID, err)
					failedCount++
				} else {
					successCount++
					totalCost += s.provider.BatchCost()
				}
			}
		}

		// 更新任务进度
		s.taskMutex.Lock()
		task.SuccessCount = successCount
		task.FailedCount = failedCount
		s.taskMutex.Unlock()
	}

	return successCount, failedCount, totalCost
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
		// 使用批量成本估算
		estimatedCost = float64(unanalyzed) * s.provider.BatchCost()
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
