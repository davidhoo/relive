package service

import (
	"fmt"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
)

// ConfigService 配置服务接口
type ConfigService interface {
	// Get 获取配置
	Get(key string) (*model.AppConfig, error)

	// Set 设置配置
	Set(key, value string) error

	// Delete 删除配置（重置为默认值）
	Delete(key string) error

	// List 获取所有配置
	List() ([]*model.AppConfig, error)

	// GetWithDefault 获取配置，如果不存在返回默认值
	GetWithDefault(key, defaultValue string) (string, error)

	// SetBatch 批量设置配置
	SetBatch(configs map[string]string) error
}

// configService 配置服务实现
type configService struct {
	configRepo repository.ConfigRepository
}

// NewConfigService 创建配置服务
func NewConfigService(configRepo repository.ConfigRepository) ConfigService {
	return &configService{
		configRepo: configRepo,
	}
}

// Get 获取配置
func (s *configService) Get(key string) (*model.AppConfig, error) {
	config, err := s.configRepo.Get(key)
	if err != nil {
		return nil, fmt.Errorf("get config: %w", err)
	}
	return config, nil
}

// Set 设置配置
func (s *configService) Set(key, value string) error {
	// 验证配置键（可选）
	if err := s.validateConfigKey(key); err != nil {
		return err
	}

	if err := s.configRepo.Set(key, value); err != nil {
		return fmt.Errorf("set config: %w", err)
	}

	logger.Infof("Config updated: %s = %s", key, value)
	return nil
}

// Delete 删除配置
func (s *configService) Delete(key string) error {
	// 检查是否存在
	exists, err := s.configRepo.Exists(key)
	if err != nil {
		return fmt.Errorf("check config exists: %w", err)
	}

	if !exists {
		return fmt.Errorf("config not found: %s", key)
	}

	if err := s.configRepo.Delete(key); err != nil {
		return fmt.Errorf("delete config: %w", err)
	}

	logger.Infof("Config deleted: %s", key)
	return nil
}

// List 获取所有配置
func (s *configService) List() ([]*model.AppConfig, error) {
	configs, err := s.configRepo.GetAll()
	if err != nil {
		return nil, fmt.Errorf("list configs: %w", err)
	}
	return configs, nil
}

// GetWithDefault 获取配置，如果不存在返回默认值
func (s *configService) GetWithDefault(key, defaultValue string) (string, error) {
	config, err := s.configRepo.Get(key)
	if err != nil {
		// 如果配置不存在，返回默认值
		return defaultValue, nil
	}
	return config.Value, nil
}

// SetBatch 批量设置配置
func (s *configService) SetBatch(configs map[string]string) error {
	// 验证所有配置键
	for key := range configs {
		if err := s.validateConfigKey(key); err != nil {
			return err
		}
	}

	if err := s.configRepo.SetBatch(configs); err != nil {
		return fmt.Errorf("set batch configs: %w", err)
	}

	logger.Infof("Batch config updated: %d items", len(configs))
	return nil
}

// validateConfigKey 验证配置键是否合法
func (s *configService) validateConfigKey(key string) error {
	if key == "" {
		return fmt.Errorf("config key cannot be empty")
	}

	// 定义允许的配置键（可选，提高安全性）
	allowedKeys := map[string]bool{
		"display.algorithm":         true,
		"display.refresh_interval":  true,
		"display.avoid_repeat_days": true,
		"display.fallback_days":     true,
		"display.strategy":          true,
		"ai.provider":               true,
		"ai.temperature":            true,
		"ai.timeout":                true,
		"system.maintenance_mode":   true,
		"system.debug_mode":         true,
		// 添加更多允许的配置键...
	}

	// 如果不在允许列表中，记录警告但不阻止（便于扩展）
	if !allowedKeys[key] {
		logger.Warnf("Setting non-standard config key: %s", key)
	}

	return nil
}
