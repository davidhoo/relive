package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

// APIKeyService API Key服务接口
type APIKeyService interface {
	// CRUD
	Create(req *model.CreateAPIKeyRequest) (*model.APIKeyResponse, error)
	Update(id uint, req *model.UpdateAPIKeyRequest) (*model.APIKeyResponse, error)
	Delete(id uint) error
	GetByID(id uint) (*model.APIKeyResponse, error)
	GetAll() ([]*model.APIKeyResponse, error)

	// 验证
	ValidateKey(key string) (*model.APIKey, error)

	// 重新生成
	Regenerate(id uint) (*model.RegenerateAPIKeyResponse, error)
}

// apiKeyService API Key服务实现
type apiKeyService struct {
	repo   repository.APIKeyRepository
	config *config.Config
}

// NewAPIKeyService 创建API Key服务
func NewAPIKeyService(repo repository.APIKeyRepository, cfg *config.Config) APIKeyService {
	return &apiKeyService{
		repo:   repo,
		config: cfg,
	}
}

// Create 创建新的API Key
func (s *apiKeyService) Create(req *model.CreateAPIKeyRequest) (*model.APIKeyResponse, error) {
	// 生成API Key
	key, err := s.generateKey()
	if err != nil {
		return nil, fmt.Errorf("generate api key: %w", err)
	}

	apiKey := &model.APIKey{
		Name:        req.Name,
		Key:         key,
		Description: req.Description,
		IsActive:    true,
		ExpiresAt:   req.ExpiresAt,
	}

	if err := s.repo.Create(apiKey); err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}

	logger.Infof("API Key created: %s (ID: %d)", apiKey.Name, apiKey.ID)

	return &model.APIKeyResponse{
		ID:          apiKey.ID,
		CreatedAt:   apiKey.CreatedAt,
		UpdatedAt:   apiKey.UpdatedAt,
		Name:        apiKey.Name,
		Key:         apiKey.Key, // 仅创建时返回
		Description: apiKey.Description,
		IsActive:    apiKey.IsActive,
		ExpiresAt:   apiKey.ExpiresAt,
	}, nil
}

// Update 更新API Key
func (s *apiKeyService) Update(id uint, req *model.UpdateAPIKeyRequest) (*model.APIKeyResponse, error) {
	apiKey, err := s.repo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("api key not found: %w", err)
	}

	// 更新字段
	if req.Name != "" {
		apiKey.Name = req.Name
	}
	if req.Description != "" {
		apiKey.Description = req.Description
	}
	if req.IsActive != nil {
		apiKey.IsActive = *req.IsActive
	}
	if req.ExpiresAt != nil {
		apiKey.ExpiresAt = req.ExpiresAt
	}

	if err := s.repo.Update(apiKey); err != nil {
		return nil, fmt.Errorf("update api key: %w", err)
	}

	logger.Infof("API Key updated: %s (ID: %d)", apiKey.Name, id)

	return s.toResponse(apiKey), nil
}

// Delete 删除API Key
func (s *apiKeyService) Delete(id uint) error {
	apiKey, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("api key not found: %w", err)
	}

	if err := s.repo.Delete(id); err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}

	logger.Infof("API Key deleted: %s (ID: %d)", apiKey.Name, id)
	return nil
}

// GetByID 根据ID获取API Key
func (s *apiKeyService) GetByID(id uint) (*model.APIKeyResponse, error) {
	apiKey, err := s.repo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("api key not found: %w", err)
	}
	return s.toResponse(apiKey), nil
}

// GetAll 获取所有API Key
func (s *apiKeyService) GetAll() ([]*model.APIKeyResponse, error) {
	apiKeys, err := s.repo.GetAll()
	if err != nil {
		return nil, fmt.Errorf("get all api keys: %w", err)
	}

	responses := make([]*model.APIKeyResponse, len(apiKeys))
	for i, apiKey := range apiKeys {
		responses[i] = s.toResponse(apiKey)
	}

	return responses, nil
}

// ValidateKey 验证API Key
func (s *apiKeyService) ValidateKey(key string) (*model.APIKey, error) {
	apiKey, err := s.repo.ValidateKey(key)
	if err != nil {
		return nil, fmt.Errorf("invalid api key: %w", err)
	}

	// 更新使用统计（异步）
	go func() {
		if err := s.repo.UpdateLastUsed(apiKey.ID); err != nil {
			logger.Warnf("Failed to update api key last used: %v", err)
		}
	}()

	return apiKey, nil
}

// Regenerate 重新生成API Key
func (s *apiKeyService) Regenerate(id uint) (*model.RegenerateAPIKeyResponse, error) {
	apiKey, err := s.repo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("api key not found: %w", err)
	}

	// 生成新的Key
	newKey, err := s.generateKey()
	if err != nil {
		return nil, fmt.Errorf("generate api key: %w", err)
	}

	apiKey.Key = newKey
	if err := s.repo.Update(apiKey); err != nil {
		return nil, fmt.Errorf("update api key: %w", err)
	}

	logger.Infof("API Key regenerated: %s (ID: %d)", apiKey.Name, id)

	return &model.RegenerateAPIKeyResponse{
		ID:  apiKey.ID,
		Key: newKey,
	}, nil
}

// generateKey 生成API Key
func (s *apiKeyService) generateKey() (string, error) {
	// 生成16字节随机数（32位十六进制字符）
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	randomStr := hex.EncodeToString(bytes)
	apiKey := s.config.Security.APIKeyPrefix + randomStr

	// 检查是否已存在（极低概率）
	exists, err := s.repo.ExistsByKey(apiKey)
	if err != nil {
		return "", err
	}
	if exists {
		// 重新生成
		return s.generateKey()
	}

	return apiKey, nil
}

// toResponse 转换为响应格式（不包含Key值）
func (s *apiKeyService) toResponse(apiKey *model.APIKey) *model.APIKeyResponse {
	return &model.APIKeyResponse{
		ID:          apiKey.ID,
		CreatedAt:   apiKey.CreatedAt,
		UpdatedAt:   apiKey.UpdatedAt,
		Name:        apiKey.Name,
		Description: apiKey.Description,
		IsActive:    apiKey.IsActive,
		ExpiresAt:   apiKey.ExpiresAt,
		LastUsedAt:  apiKey.LastUsedAt,
		UseCount:    apiKey.UseCount,
	}
}
