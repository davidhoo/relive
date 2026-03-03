package repository

import (
	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// APIKeyRepository API Key仓库接口
type APIKeyRepository interface {
	// 基础CRUD
	Create(apiKey *model.APIKey) error
	Update(apiKey *model.APIKey) error
	Delete(id uint) error
	GetByID(id uint) (*model.APIKey, error)
	GetByKey(key string) (*model.APIKey, error)
	GetAll() ([]*model.APIKey, error)

	// 查询
	ExistsByKey(key string) (bool, error)
	Count() (int64, error)

	// 验证
	ValidateKey(key string) (*model.APIKey, error)

	// 使用统计
	UpdateLastUsed(id uint) error
}

// apiKeyRepository API Key仓库实现
type apiKeyRepository struct {
	db *gorm.DB
}

// NewAPIKeyRepository 创建API Key仓库
func NewAPIKeyRepository(db *gorm.DB) APIKeyRepository {
	return &apiKeyRepository{db: db}
}

// Create 创建API Key
func (r *apiKeyRepository) Create(apiKey *model.APIKey) error {
	return r.db.Create(apiKey).Error
}

// Update 更新API Key
func (r *apiKeyRepository) Update(apiKey *model.APIKey) error {
	return r.db.Save(apiKey).Error
}

// Delete 删除API Key（软删除）
func (r *apiKeyRepository) Delete(id uint) error {
	return r.db.Delete(&model.APIKey{}, id).Error
}

// GetByID 根据ID获取API Key
func (r *apiKeyRepository) GetByID(id uint) (*model.APIKey, error) {
	var apiKey model.APIKey
	err := r.db.First(&apiKey, id).Error
	if err != nil {
		return nil, err
	}
	return &apiKey, nil
}

// GetByKey 根据Key值获取API Key
func (r *apiKeyRepository) GetByKey(key string) (*model.APIKey, error) {
	var apiKey model.APIKey
	err := r.db.Where("key = ?", key).First(&apiKey).Error
	if err != nil {
		return nil, err
	}
	return &apiKey, nil
}

// GetAll 获取所有API Key
func (r *apiKeyRepository) GetAll() ([]*model.APIKey, error) {
	var apiKeys []*model.APIKey
	err := r.db.Order("created_at DESC").Find(&apiKeys).Error
	return apiKeys, err
}

// ExistsByKey 检查Key是否存在
func (r *apiKeyRepository) ExistsByKey(key string) (bool, error) {
	var count int64
	err := r.db.Model(&model.APIKey{}).Where("key = ?", key).Count(&count).Error
	return count > 0, err
}

// Count 统计API Key总数
func (r *apiKeyRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.APIKey{}).Count(&count).Error
	return count, err
}

// ValidateKey 验证API Key是否有效
func (r *apiKeyRepository) ValidateKey(key string) (*model.APIKey, error) {
	apiKey, err := r.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if !apiKey.IsValid() {
		return nil, gorm.ErrRecordNotFound
	}
	return apiKey, nil
}

// UpdateLastUsed 更新最后使用时间
func (r *apiKeyRepository) UpdateLastUsed(id uint) error {
	return r.db.Model(&model.APIKey{}).Where("id = ?", id).Updates(map[string]interface{}{
		"last_used_at": r.db.NowFunc(),
		"use_count":    gorm.Expr("use_count + 1"),
	}).Error
}
