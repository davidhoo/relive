package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	ID             string     `json:"id"`
	Mode           string     `json:"mode,omitempty"`
	Status         string     `json:"status"` // pending, running, sleeping, stopping, completed, failed
	TotalCount     int        `json:"total_count"`
	SuccessCount   int        `json:"success_count"`
	FailedCount    int        `json:"failed_count"`
	CurrentIndex   int        `json:"current_index"`
	CurrentPhotoID *uint      `json:"current_photo_id,omitempty"`
	CurrentMessage string     `json:"current_message,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	ErrorMessage   string     `json:"error_message,omitempty"`
}

// IsRunning 检查任务是否运行中
func (t *AnalyzeTask) IsRunning() bool {
	return t.Status == "running" || t.Status == "sleeping" || t.Status == "stopping"
}

// AIService AI 分析服务接口
type AIService interface {
	// AnalyzePhoto 分析单张照片
	AnalyzePhoto(photoID uint) error

	// ReAnalyzePhoto 重新分析照片（强制重新分析已分析的照片）
	ReAnalyzePhoto(photoID uint) error

	// AnalyzeBatch 批量分析照片（异步启动）
	AnalyzeBatch(limit int) (*AnalyzeTask, error)

	// StartBackgroundAnalyze 启动后台持续分析
	StartBackgroundAnalyze() (*AnalyzeTask, error)

	// StopBackgroundAnalyze 停止后台持续分析
	StopBackgroundAnalyze() error

	// GetBackgroundLogs 获取后台分析日志
	GetBackgroundLogs() []string

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
	photoRepo        repository.PhotoRepository
	config           *config.Config
	configService    ConfigService
	runtimeService   AnalysisRuntimeService
	provider         provider.AIProvider
	currentTask      *AnalyzeTask
	taskMutex        sync.RWMutex
	backgroundStopCh chan struct{}
	backgroundLogMu  sync.RWMutex
	backgroundLogs   []string
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
	VLLMConcurrency int     `json:"vllm_concurrency"`

	// Hybrid
	HybridPrimary      string `json:"hybrid_primary"`
	HybridFallback     string `json:"hybrid_fallback"`
	HybridRetryOnError bool   `json:"hybrid_retry_on_error"`
}

// NewAIService 创建 AI 分析服务
func NewAIService(photoRepo repository.PhotoRepository, cfg *config.Config, configService ConfigService, runtimeService AnalysisRuntimeService) (AIService, error) {
	svc := &aiService{
		photoRepo:      photoRepo,
		config:         cfg,
		configService:  configService,
		runtimeService: runtimeService,
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

	// 加载提示词配置
	promptConfig := s.loadPromptConfig()

	var (
		p   provider.AIProvider
		err error
	)

	switch aiConfig.Provider {
	case "ollama":
		p, err = provider.NewOllamaProvider(&provider.OllamaConfig{
			Endpoint:       aiConfig.OllamaEndpoint,
			Model:          aiConfig.OllamaModel,
			Temperature:    aiConfig.OllamaTemperature,
			Timeout:        aiConfig.OllamaTimeout,
			AnalysisPrompt: promptConfig.AnalysisPrompt,
			CaptionPrompt:  promptConfig.CaptionPrompt,
		})
	case "qwen":
		p, err = provider.NewQwenProvider(&provider.QwenConfig{
			APIKey:         aiConfig.QwenAPIKey,
			Endpoint:       aiConfig.QwenEndpoint,
			Model:          aiConfig.QwenModel,
			Temperature:    aiConfig.QwenTemperature,
			Timeout:        aiConfig.QwenTimeout,
			AnalysisPrompt: promptConfig.AnalysisPrompt,
			CaptionPrompt:  promptConfig.CaptionPrompt,
			BatchPrompt:    promptConfig.BatchPrompt,
		})
	case "openai":
		p, err = provider.NewOpenAIProvider(&provider.OpenAIConfig{
			APIKey:         aiConfig.OpenAIAPIKey,
			Endpoint:       aiConfig.OpenAIEndpoint,
			Model:          aiConfig.OpenAIModel,
			Temperature:    aiConfig.OpenAITemperature,
			MaxTokens:      aiConfig.OpenAIMaxTokens,
			Timeout:        aiConfig.OpenAITimeout,
			AnalysisPrompt: promptConfig.AnalysisPrompt,
			CaptionPrompt:  promptConfig.CaptionPrompt,
		})
	case "vllm":
		p, err = provider.NewVLLMProvider(&provider.VLLMConfig{
			Endpoint:       aiConfig.VLLMEndpoint,
			Model:          aiConfig.VLLMModel,
			Temperature:    aiConfig.VLLMTemperature,
			MaxTokens:      aiConfig.VLLMMaxTokens,
			Timeout:        aiConfig.VLLMTimeout,
			Concurrency:    aiConfig.VLLMConcurrency,
			AnalysisPrompt: promptConfig.AnalysisPrompt,
			CaptionPrompt:  promptConfig.CaptionPrompt,
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
		aiConfig.Timeout = 120 // 默认 120 秒，支持更复杂的模型如 qwen3.5-plus
	}

	return aiConfig
}

// loadPromptConfig 加载提示词配置
func (s *aiService) loadPromptConfig() *provider.PromptConfig {
	// 默认使用 provider 包中的默认值
	config := &provider.PromptConfig{}

	// 尝试从数据库读取配置
	if s.configService != nil {
		dbConfig, err := s.configService.Get("prompts")
		if err == nil && dbConfig != nil && dbConfig.Value != "" {
			var dbPromptConfig provider.PromptConfig
			if err := json.Unmarshal([]byte(dbConfig.Value), &dbPromptConfig); err == nil {
				// 只使用非空的配置项
				if dbPromptConfig.AnalysisPrompt != "" {
					config.AnalysisPrompt = dbPromptConfig.AnalysisPrompt
				}
				if dbPromptConfig.CaptionPrompt != "" {
					config.CaptionPrompt = dbPromptConfig.CaptionPrompt
				}
				if dbPromptConfig.BatchPrompt != "" {
					config.BatchPrompt = dbPromptConfig.BatchPrompt
				}
				logger.Info("Loading prompt config from database")
			}
		}
	}

	return config
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

// getImageDataForAI 获取用于 AI 分析的图片数据
// 优先使用已生成的缩略图，如果不存在则使用原图
func (s *aiService) getImageDataForAI(photo *model.Photo) ([]byte, string, error) {
	// 优先使用缩略图（1024px，质量更好且已预处理）
	if photo.ThumbnailPath != "" {
		thumbnailPath := filepath.Join(s.config.Photos.ThumbnailPath, photo.ThumbnailPath)
		if data, err := os.ReadFile(thumbnailPath); err == nil {
			logger.Debugf("Using thumbnail for photo %d: %s", photo.ID, thumbnailPath)
			return data, thumbnailPath, nil
		} else {
			logger.Warnf("Thumbnail not found for photo %d, falling back to original: %v", photo.ID, err)
		}
	}

	// 回退到原图
	data, err := os.ReadFile(photo.FilePath)
	if err != nil {
		return nil, "", err
	}
	return data, photo.FilePath, nil
}

func isPhotoReadyForAI(photo *model.Photo) bool {
	if photo == nil {
		return false
	}
	if photo.ThumbnailStatus != "ready" {
		return false
	}
	// 有有效 GPS 坐标但还没 geocode 完成，等待 geocode
	if photo.GPSLatitude != nil && photo.GPSLongitude != nil &&
		(*photo.GPSLatitude != 0 || *photo.GPSLongitude != 0) &&
		photo.GeocodeStatus != "ready" {
		return false
	}
	return true
}

func photoNotReadyReason(photo *model.Photo) string {
	if photo == nil {
		return "photo not found"
	}
	if photo.ThumbnailStatus != "ready" {
		return "thumbnail not ready"
	}
	// 有有效 GPS 坐标但还没 geocode 完成
	if photo.GPSLatitude != nil && photo.GPSLongitude != nil &&
		(*photo.GPSLatitude != 0 || *photo.GPSLongitude != 0) &&
		photo.GeocodeStatus != "ready" {
		return "geocode not ready"
	}
	return "photo not ready for ai analysis"
}

// AnalyzePhoto 分析单张照片
func (s *aiService) AnalyzePhoto(photoID uint) error {
	return s.analyzePhotoInternal(photoID, false)
}

// ReAnalyzePhoto 重新分析照片（强制重新分析已分析的照片）
func (s *aiService) ReAnalyzePhoto(photoID uint) error {
	logger.Infof("Re-analyzing photo %d (force mode)", photoID)
	return s.analyzePhotoInternal(photoID, true)
}

// analyzePhotoInternal 内部分析方法
// force: 是否强制重新分析已分析的照片
func (s *aiService) analyzePhotoInternal(photoID uint, force bool) error {
	if s.provider == nil {
		return fmt.Errorf("AI provider not configured")
	}

	// 获取照片信息
	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		return fmt.Errorf("get photo: %w", err)
	}

	// 检查是否已分析（非强制模式下跳过已分析的照片）
	if photo.AIAnalyzed && !force {
		logger.Warnf("Photo %d already analyzed, skipping", photoID)
		return nil
	}

	if !isPhotoReadyForAI(photo) {
		return fmt.Errorf("photo %d is not ready for ai analysis: %s", photoID, photoNotReadyReason(photo))
	}

	// 获取图片数据（优先使用缩略图）
	imageData, imagePath, err := s.getImageDataForAI(photo)
	if err != nil {
		return fmt.Errorf("read image file: %w", err)
	}

	// 如果使用的是原图（缩略图不存在），需要预处理
	if imagePath == photo.FilePath {
		processor := &util.ImageProcessor{
			MaxLongSide: 768,
			JPEGQuality: 80,
		}
		processedData, err := processor.ProcessForAI(photo.FilePath)
		if err != nil {
			logger.Warnf("Image preprocessing failed, using original: %v", err)
			processedData = imageData
		}
		imageData = processedData
	}
	// 如果使用的是缩略图（1024px，质量90），直接使用即可

	// 构建分析请求
	req := &provider.AnalyzeRequest{
		ImageData: imageData,
		ImagePath: imagePath,
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

	// ========== 第一次会话：分析照片 ==========
	logger.Infof("Analyzing photo %d with provider %s (session 1: analysis)...", photoID, s.provider.Name())
	result, err := s.provider.Analyze(req)
	if err != nil {
		return fmt.Errorf("analyze photo: %w", err)
	}

	// ========== 第二次会话：生成创意文案 ==========
	logger.Infof("Generating caption for photo %d (session 2: creative caption)...", photoID)
	caption, err := s.provider.GenerateCaption(req)
	if err != nil {
		// 如果文案生成失败，使用描述的一部分作为fallback
		logger.Warnf("Caption generation failed for photo %d, using fallback: %v", photoID, err)
		if len(result.Description) > 30 {
			caption = result.Description[:30]
		} else {
			caption = result.Description
		}
	}

	// 更新照片记录
	now := time.Now()
	photo.AIAnalyzed = true
	photo.AIProvider = s.provider.Name() // 保存 AI 提供商名称
	photo.Description = result.Description
	photo.Caption = caption
	photo.MainCategory = result.MainCategory
	photo.Tags = result.Tags
	photo.MemoryScore = int(result.MemoryScore)
	photo.BeautyScore = int(result.BeautyScore)
	photo.ScoreReason = result.Reason
	photo.AnalyzedAt = &now

	// 计算综合评分
	photo.OverallScore = int(float64(photo.MemoryScore)*0.7 + float64(photo.BeautyScore)*0.3)

	if err := s.photoRepo.Update(photo); err != nil {
		return fmt.Errorf("update photo: %w", err)
	}

	logger.Infof("Photo %d analyzed successfully (2 sessions): memory=%d, beauty=%d, overall=%d, caption=%s",
		photoID, photo.MemoryScore, photo.BeautyScore, photo.OverallScore, caption)

	return nil
}

// GetTaskStatus 获取当前任务状态
func (s *aiService) GetTaskStatus() *AnalyzeTask {
	s.taskMutex.RLock()
	defer s.taskMutex.RUnlock()
	return s.currentTask
}

// GetBackgroundLogs 获取后台分析日志
func (s *aiService) GetBackgroundLogs() []string {
	s.backgroundLogMu.RLock()
	defer s.backgroundLogMu.RUnlock()

	logs := make([]string, len(s.backgroundLogs))
	copy(logs, s.backgroundLogs)
	return logs
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
		ID:             fmt.Sprintf("task_%d", time.Now().Unix()),
		Mode:           model.AnalysisOwnerTypeBatch,
		Status:         "running",
		TotalCount:     len(photos),
		SuccessCount:   0,
		FailedCount:    0,
		CurrentIndex:   0,
		StartedAt:      time.Now(),
		CurrentMessage: "正在批量分析未分析照片",
	}

	if s.runtimeService != nil {
		if _, err := s.runtimeService.AcquireGlobal(model.AnalysisOwnerTypeBatch, task.ID, "在线批量分析运行中"); err != nil {
			s.taskMutex.Unlock()
			return nil, fmt.Errorf("acquire analysis runtime: %w", err)
		}
	}
	s.currentTask = task
	s.taskMutex.Unlock()

	logger.Infof("Starting async batch analysis: %d photos, task_id=%s, provider supports batch: %v, batch size: %d",
		len(photos), task.ID, s.provider.SupportsBatch(), s.provider.MaxBatchSize())

	// 异步执行分析
	go s.runBatchAnalysis(task, photos)

	return task, nil
}

// StartBackgroundAnalyze 启动后台持续分析
func (s *aiService) StartBackgroundAnalyze() (*AnalyzeTask, error) {
	if s.provider == nil {
		return nil, fmt.Errorf("AI provider not configured")
	}

	s.taskMutex.Lock()
	if s.currentTask != nil && s.currentTask.IsRunning() {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("analysis task already running")
	}

	task := &AnalyzeTask{
		ID:             fmt.Sprintf("background_%d", time.Now().Unix()),
		Mode:           model.AnalysisOwnerTypeBackground,
		Status:         "running",
		StartedAt:      time.Now(),
		CurrentMessage: "后台分析已启动",
	}

	if s.runtimeService != nil {
		if _, err := s.runtimeService.AcquireGlobal(model.AnalysisOwnerTypeBackground, task.ID, "在线后台分析运行中"); err != nil {
			s.taskMutex.Unlock()
			return nil, fmt.Errorf("acquire analysis runtime: %w", err)
		}
	}

	s.currentTask = task
	s.backgroundStopCh = make(chan struct{})
	s.resetBackgroundLogs()
	s.appendBackgroundLog("后台分析已启动")
	s.taskMutex.Unlock()

	go s.runBackgroundAnalysis(task)
	return task, nil
}

// StopBackgroundAnalyze 停止后台持续分析
func (s *aiService) StopBackgroundAnalyze() error {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()

	if s.currentTask == nil || s.currentTask.Mode != model.AnalysisOwnerTypeBackground || !s.currentTask.IsRunning() {
		return fmt.Errorf("background analysis is not running")
	}

	if s.currentTask.Status != "stopping" {
		s.currentTask.Status = "stopping"
		s.currentTask.CurrentMessage = "正在停止后台分析，等待当前批次完成"
		s.appendBackgroundLog("收到停止请求，等待当前批次完成")
	}

	if s.backgroundStopCh != nil {
		close(s.backgroundStopCh)
		s.backgroundStopCh = nil
	}

	return nil
}

// runBatchAnalysis 后台执行批量分析
func (s *aiService) runBatchAnalysis(task *AnalyzeTask, photos []*model.Photo) {
	var leaseCancel context.CancelFunc
	if s.runtimeService != nil {
		var ctx context.Context
		ctx, leaseCancel = context.WithCancel(context.Background())
		go s.keepRuntimeLeaseAlive(ctx, model.AnalysisOwnerTypeBatch, task.ID)
	}
	defer func() {
		if leaseCancel != nil {
			leaseCancel()
		}
		if s.runtimeService != nil {
			if err := s.runtimeService.ReleaseGlobal(model.AnalysisOwnerTypeBatch, task.ID); err != nil && !errors.Is(err, ErrAnalysisRuntimeOwnedByOther) {
				logger.Warnf("Failed to release analysis runtime lease for task %s: %v", task.ID, err)
			}
		}
	}()

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

func (s *aiService) runBackgroundAnalysis(task *AnalyzeTask) {
	var leaseCancel context.CancelFunc
	if s.runtimeService != nil {
		var ctx context.Context
		ctx, leaseCancel = context.WithCancel(context.Background())
		go s.keepRuntimeLeaseAlive(ctx, model.AnalysisOwnerTypeBackground, task.ID)
	}
	defer func() {
		if leaseCancel != nil {
			leaseCancel()
		}
		if s.runtimeService != nil {
			if err := s.runtimeService.ReleaseGlobal(model.AnalysisOwnerTypeBackground, task.ID); err != nil && !errors.Is(err, ErrAnalysisRuntimeOwnedByOther) {
				logger.Warnf("Failed to release analysis runtime lease for background task %s: %v", task.ID, err)
			}
		}

		s.taskMutex.Lock()
		now := time.Now()
		task.CompletedAt = &now
		if task.Status == "stopping" {
			task.Status = "completed"
			task.CurrentMessage = "后台分析已停止"
		} else if task.Status != "failed" {
			task.Status = "completed"
			task.CurrentMessage = "后台分析已结束"
		}
		s.taskMutex.Unlock()
		s.appendBackgroundLog(task.CurrentMessage)
	}()

	for {
		if s.isBackgroundStopRequested() {
			return
		}

		limit := s.backgroundBatchSize()
		photos, err := s.photoRepo.GetUnanalyzed(limit)
		if err != nil {
			s.setTaskState(task, "running", 0, nil, fmt.Sprintf("获取待分析照片失败：%v", err))
			s.appendBackgroundLog(fmt.Sprintf("获取待分析照片失败：%v", err))
			if s.waitForBackgroundNextCycle(5 * time.Second) {
				return
			}
			continue
		}

		if len(photos) == 0 {
			s.setTaskState(task, "sleeping", 0, nil, "没有新的未分析照片，后台分析等待中")
			s.appendBackgroundLog("没有新的未分析照片，5 秒后重试")
			if s.waitForBackgroundNextCycle(5 * time.Second) {
				return
			}
			continue
		}

		s.setTaskState(task, "running", len(photos), nil, fmt.Sprintf("本轮准备分析 %d 张照片", len(photos)))
		s.appendBackgroundLog(fmt.Sprintf("开始新一轮后台分析：%d 张照片", len(photos)))

		prevSuccess, prevFailed := 0, 0
		s.taskMutex.RLock()
		prevSuccess = task.SuccessCount
		prevFailed = task.FailedCount
		s.taskMutex.RUnlock()

		cycleSuccess, cycleFailed := 0, 0
		if s.provider.SupportsBatch() && s.provider.MaxBatchSize() > 1 {
			cycleSuccess, cycleFailed, _ = s.analyzeInBatchesAsync(task, photos)
		} else {
			cycleSuccess, cycleFailed, _ = s.analyzeOneByOneAsync(task, photos)
		}

		s.taskMutex.Lock()
		task.SuccessCount = prevSuccess + cycleSuccess
		task.FailedCount = prevFailed + cycleFailed
		task.CurrentIndex = 0
		task.CurrentPhotoID = nil
		task.CurrentMessage = fmt.Sprintf("本轮完成：成功 %d，失败 %d", cycleSuccess, cycleFailed)
		s.taskMutex.Unlock()
		s.appendBackgroundLog(task.CurrentMessage)

		if s.waitForBackgroundNextCycle(2 * time.Second) {
			return
		}
	}
}

func (s *aiService) keepRuntimeLeaseAlive(ctx context.Context, ownerType, ownerID string) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if _, err := s.runtimeService.HeartbeatGlobal(ownerType, ownerID); err != nil {
				logger.Warnf("Failed to heartbeat analysis runtime lease for %s/%s: %v", ownerType, ownerID, err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *aiService) backgroundBatchSize() int {
	if s.provider == nil {
		return 1
	}

	if s.provider.SupportsBatch() && s.provider.MaxBatchSize() > 1 {
		return s.provider.MaxBatchSize()
	}

	if concurrency := s.provider.MaxConcurrency(); concurrency > 0 {
		return concurrency
	}

	return 1
}

func (s *aiService) waitForBackgroundNextCycle(delay time.Duration) bool {
	s.taskMutex.RLock()
	stopCh := s.backgroundStopCh
	s.taskMutex.RUnlock()

	if stopCh == nil {
		return true
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-stopCh:
		return true
	case <-timer.C:
		return false
	}
}

func (s *aiService) isBackgroundStopRequested() bool {
	s.taskMutex.RLock()
	defer s.taskMutex.RUnlock()
	return s.currentTask != nil && s.currentTask.Mode == model.AnalysisOwnerTypeBackground && s.currentTask.Status == "stopping"
}

func (s *aiService) setTaskState(task *AnalyzeTask, status string, totalCount int, currentPhotoID *uint, message string) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()

	task.Status = status
	if totalCount > 0 {
		task.TotalCount = totalCount
	}
	task.CurrentIndex = 0
	task.CurrentPhotoID = currentPhotoID
	task.CurrentMessage = message
}

func (s *aiService) appendBackgroundLog(message string) {
	if message == "" {
		return
	}

	entry := fmt.Sprintf("[%s] %s", time.Now().Format("2006-01-02 15:04:05"), message)
	s.backgroundLogMu.Lock()
	defer s.backgroundLogMu.Unlock()

	s.backgroundLogs = append(s.backgroundLogs, entry)
	if len(s.backgroundLogs) > 100 {
		s.backgroundLogs = s.backgroundLogs[len(s.backgroundLogs)-100:]
	}
}

func (s *aiService) resetBackgroundLogs() {
	s.backgroundLogMu.Lock()
	defer s.backgroundLogMu.Unlock()
	s.backgroundLogs = make([]string, 0, 100)
}

// analyzeOneByOneAsync 逐个分析照片（支持并发）
func (s *aiService) analyzeOneByOneAsync(task *AnalyzeTask, photos []*model.Photo) (successCount, failedCount int, totalCost float64) {
	// 获取并发数限制，但限制最大为1以避免SQLite锁竞争
	// 多服务(扫描/缩略图/分析/GPS)共享同一个SQLite数据库
	concurrency := s.provider.MaxConcurrency()
	if concurrency <= 0 {
		concurrency = 1 // 默认单并发，避免数据库锁竞争
	}
	// 强制限制为1，因为SQLite在多进程访问时容易锁竞争
	if concurrency > 1 {
		concurrency = 1
	}

	totalCount := len(photos)
	logger.Infof("[Task %s] Starting concurrent analysis: %d photos, concurrency=%d", task.ID, totalCount, concurrency)

	// 使用 semaphore 控制并发
	semaphore := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, photo := range photos {
		wg.Add(1)
		go func(idx int, p *model.Photo) {
			defer wg.Done()

			// 获取 semaphore 许可
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			s.taskMutex.Lock()
			photoID := p.ID
			task.CurrentPhotoID = &photoID
			task.CurrentMessage = fmt.Sprintf("正在分析照片 #%d", p.ID)
			s.taskMutex.Unlock()
			if task.Mode == model.AnalysisOwnerTypeBackground {
				s.appendBackgroundLog(fmt.Sprintf("开始分析照片 #%d (%s)", p.ID, p.FileName))
			}

			logger.Infof("[Task %s] Analyzing photo %d/%d: id=%d, path=%s", task.ID, idx+1, totalCount, p.ID, p.FileName)

			err := s.AnalyzePhoto(p.ID)

			// 更新进度（加锁保护）
			mu.Lock()
			if err != nil {
				logger.Errorf("[Task %s] Failed to analyze photo %d: %v", task.ID, p.ID, err)
				failedCount++
				if task.Mode == model.AnalysisOwnerTypeBackground {
					s.appendBackgroundLog(fmt.Sprintf("分析照片 #%d 失败：%v", p.ID, err))
				}
			} else {
				successCount++
				totalCost += s.provider.Cost()
				if task.Mode == model.AnalysisOwnerTypeBackground {
					s.appendBackgroundLog(fmt.Sprintf("分析照片 #%d 成功", p.ID))
				}
			}
			task.CurrentIndex = idx + 1
			task.SuccessCount = successCount
			task.FailedCount = failedCount
			mu.Unlock()
		}(i, photo)
	}

	// 等待所有分析完成
	wg.Wait()

	logger.Infof("[Task %s] Concurrent analysis completed: total=%d, success=%d, failed=%d",
		task.ID, totalCount, successCount, failedCount)

	return successCount, failedCount, totalCost
}

// getImageDataForBatch 获取批量分析用的图片数据
// 优先使用缩略图，如果不存在则使用原图并压缩
func (s *aiService) getImageDataForBatch(photo *model.Photo) ([]byte, error) {
	// 优先使用已生成的缩略图（1024px，质量90）
	if photo.ThumbnailPath != "" {
		thumbnailPath := filepath.Join(s.config.Photos.ThumbnailPath, photo.ThumbnailPath)
		if data, err := os.ReadFile(thumbnailPath); err == nil {
			return data, nil
		}
	}

	// 回退到原图并压缩
	processor := &util.ImageProcessor{
		MaxLongSide: 768,
		JPEGQuality: 80,
	}
	return processor.ProcessForAI(photo.FilePath)
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
		if len(batch) > 0 {
			photoID := batch[0].ID
			task.CurrentPhotoID = &photoID
		}
		task.CurrentMessage = fmt.Sprintf("正在处理批次 %d/%d", i/batchSize+1, (len(photos)+batchSize-1)/batchSize)
		s.taskMutex.Unlock()

		logger.Infof("[Task %s] Processing batch %d/%d: photos %d-%d", task.ID, i/batchSize+1, (len(photos)+batchSize-1)/batchSize, i+1, end)
		if task.Mode == model.AnalysisOwnerTypeBackground {
			s.appendBackgroundLog(fmt.Sprintf("开始处理批次 %d/%d（照片 %d-%d）", i/batchSize+1, (len(photos)+batchSize-1)/batchSize, i+1, end))
		}

		// 准备批量请求
		requests := make([]*provider.AnalyzeRequest, 0, len(batch))
		photoMap := make(map[int]*model.Photo)

		for j, photo := range batch {
			if photo.AIAnalyzed {
				continue
			}

			// 获取图片数据（优先使用缩略图）
			imageData, err := s.getImageDataForBatch(photo)
			if err != nil {
				logger.Errorf("[Task %s] Failed to get image data for photo %d: %v", task.ID, photo.ID, err)
				failedCount++
				continue
			}

			req := &provider.AnalyzeRequest{
				ImageData: imageData,
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
			if task.Mode == model.AnalysisOwnerTypeBackground {
				s.appendBackgroundLog(fmt.Sprintf("批处理失败，回退逐张分析：%v", err))
			}
			// 批量失败，回退到逐个分析
			for idx := range photoMap {
				photo := photoMap[idx]
				s.taskMutex.Lock()
				photoID := photo.ID
				task.CurrentPhotoID = &photoID
				task.CurrentMessage = fmt.Sprintf("回退逐张分析照片 #%d", photo.ID)
				s.taskMutex.Unlock()
				if err := s.AnalyzePhoto(photo.ID); err != nil {
					logger.Errorf("[Task %s] Failed to analyze photo %d: %v", task.ID, photo.ID, err)
					failedCount++
					if task.Mode == model.AnalysisOwnerTypeBackground {
						s.appendBackgroundLog(fmt.Sprintf("回退分析照片 #%d 失败：%v", photo.ID, err))
					}
				} else {
					successCount++
					totalCost += s.provider.Cost()
					if task.Mode == model.AnalysisOwnerTypeBackground {
						s.appendBackgroundLog(fmt.Sprintf("回退分析照片 #%d 成功", photo.ID))
					}
				}
			}
		} else {
			// ========== 第一次会话：批量分析完成，保存中间结果 ==========
			logger.Infof("[Task %s] Batch analysis completed, starting caption generation...", task.ID)

			for idx, result := range results {
				photo, ok := photoMap[idx]
				if !ok {
					continue
				}

				s.taskMutex.Lock()
				photoID := photo.ID
				task.CurrentPhotoID = &photoID
				task.CurrentMessage = fmt.Sprintf("正在写入照片 #%d 的分析结果", photo.ID)
				s.taskMutex.Unlock()

				// ========== 第二次会话：生成创意文案 ==========
				req := requests[idx]
				caption, captionErr := s.provider.GenerateCaption(req)
				if captionErr != nil {
					// 如果文案生成失败，使用描述的一部分作为fallback
					logger.Warnf("[Task %s] Caption generation failed for photo %d, using fallback: %v", task.ID, photo.ID, captionErr)
					if len(result.Description) > 30 {
						caption = result.Description[:30]
					} else {
						caption = result.Description
					}
				}

				now := time.Now()
				photo.AIAnalyzed = true
				photo.AIProvider = result.Provider
				photo.Description = result.Description
				photo.Caption = caption
				photo.MainCategory = result.MainCategory
				photo.Tags = result.Tags
				photo.MemoryScore = int(result.MemoryScore)
				photo.BeautyScore = int(result.BeautyScore)
				photo.ScoreReason = result.Reason
				photo.AnalyzedAt = &now
				photo.OverallScore = int(float64(photo.MemoryScore)*0.7 + float64(photo.BeautyScore)*0.3)

				if err := s.photoRepo.Update(photo); err != nil {
					logger.Errorf("[Task %s] Failed to update photo %d: %v", task.ID, photo.ID, err)
					failedCount++
					if task.Mode == model.AnalysisOwnerTypeBackground {
						s.appendBackgroundLog(fmt.Sprintf("写入照片 #%d 结果失败：%v", photo.ID, err))
					}
				} else {
					successCount++
					totalCost += s.provider.BatchCost()
					if task.Mode == model.AnalysisOwnerTypeBackground {
						s.appendBackgroundLog(fmt.Sprintf("写入照片 #%d 结果成功", photo.ID))
					}
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

	return &model.AIAnalyzeProgressResponse{
		Total:      total,
		Analyzed:   analyzed,
		Unanalyzed: unanalyzed,
		Progress:   progress,
		Provider:   s.config.AI.Provider,
	}, nil
}

// formatDateTime 格式化日期时间
func formatDateTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
