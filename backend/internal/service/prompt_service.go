package service

import (
	"encoding/json"
	"fmt"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
)

// PromptService 提示词配置服务接口
type PromptService interface {
	// GetPromptConfig 获取提示词配置
	GetPromptConfig() (*model.PromptConfig, error)

	// SetPromptConfig 设置提示词配置
	SetPromptConfig(config *model.PromptConfig) error

	// ResetToDefaults 重置为默认提示词
	ResetToDefaults() error
}

// promptService 提示词配置服务实现
type promptService struct {
	configRepo repository.ConfigRepository
}

// PromptConfigKey 提示词配置在数据库中的键名
const PromptConfigKey = "prompts"

// NewPromptService 创建提示词配置服务
func NewPromptService(configRepo repository.ConfigRepository) PromptService {
	return &promptService{
		configRepo: configRepo,
	}
}

// GetPromptConfig 获取提示词配置
func (s *promptService) GetPromptConfig() (*model.PromptConfig, error) {
	// 尝试从数据库获取
	appConfig, err := s.configRepo.Get(PromptConfigKey)
	if err != nil {
		// 如果数据库中不存在，返回默认配置
		logger.Info("Prompt config not found in database, returning defaults")
		return model.GetDefaultPromptConfig(), nil
	}

	// 解析 JSON
	var config model.PromptConfig
	if err := json.Unmarshal([]byte(appConfig.Value), &config); err != nil {
		logger.Warnf("Failed to parse prompt config: %v, returning defaults", err)
		return model.GetDefaultPromptConfig(), nil
	}

	// 如果某个字段为空，使用默认值填充
	defaults := model.GetDefaultPromptConfig()
	if config.AnalysisPrompt == "" {
		config.AnalysisPrompt = defaults.AnalysisPrompt
	}
	if config.CaptionPrompt == "" {
		config.CaptionPrompt = defaults.CaptionPrompt
	}
	if config.BatchPrompt == "" {
		config.BatchPrompt = defaults.BatchPrompt
	}

	return &config, nil
}

// SetPromptConfig 设置提示词配置
func (s *promptService) SetPromptConfig(config *model.PromptConfig) error {
	// 验证配置
	if config == nil {
		return fmt.Errorf("prompt config cannot be nil")
	}

	// 序列化为 JSON
	value, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshal prompt config: %w", err)
	}

	// 保存到数据库
	if err := s.configRepo.Set(PromptConfigKey, string(value)); err != nil {
		return fmt.Errorf("save prompt config: %w", err)
	}

	logger.Info("Prompt config updated successfully")
	return nil
}

// ResetToDefaults 重置为默认提示词
func (s *promptService) ResetToDefaults() error {
	defaultConfig := model.GetDefaultPromptConfig()
	return s.SetPromptConfig(defaultConfig)
}
