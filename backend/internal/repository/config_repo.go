package repository

import (
	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// ConfigRepository 配置仓库接口
type ConfigRepository interface {
	// 基础 CRUD
	Get(key string) (*model.AppConfig, error)
	Set(key string, value string) error
	Delete(key string) error
	Exists(key string) (bool, error)

	// 批量操作
	GetAll() ([]*model.AppConfig, error)
	GetByKeys(keys []string) (map[string]string, error)
	SetBatch(configs map[string]string) error

	// 查询
	List(page, pageSize int) ([]*model.AppConfig, int64, error)
	Count() (int64, error)
}

// configRepository 配置仓库实现
type configRepository struct {
	db *gorm.DB
}

// NewConfigRepository 创建配置仓库
func NewConfigRepository(db *gorm.DB) ConfigRepository {
	return &configRepository{db: db}
}

// Get 获取配置
func (r *configRepository) Get(key string) (*model.AppConfig, error) {
	var config model.AppConfig
	err := r.db.Where("key = ?", key).First(&config).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// Set 设置配置
func (r *configRepository) Set(key string, value string) error {
	// 先查找是否存在
	var config model.AppConfig
	err := r.db.Where("key = ?", key).First(&config).Error

	if err == gorm.ErrRecordNotFound {
		// 不存在，创建新记录
		config = model.AppConfig{
			Key:   key,
			Value: value,
		}
		return r.db.Create(&config).Error
	} else if err != nil {
		return err
	}

	// 存在，更新值
	config.Value = value
	return r.db.Save(&config).Error
}

// Delete 删除配置
func (r *configRepository) Delete(key string) error {
	return r.db.Where("key = ?", key).Delete(&model.AppConfig{}).Error
}

// Exists 检查配置是否存在
func (r *configRepository) Exists(key string) (bool, error) {
	var count int64
	err := r.db.Model(&model.AppConfig{}).Where("key = ?", key).Count(&count).Error
	return count > 0, err
}

// GetAll 获取所有配置
func (r *configRepository) GetAll() ([]*model.AppConfig, error) {
	var configs []*model.AppConfig
	err := r.db.Find(&configs).Error
	return configs, err
}

// GetByKeys 根据键列表获取配置
func (r *configRepository) GetByKeys(keys []string) (map[string]string, error) {
	var configs []*model.AppConfig
	err := r.db.Where("key IN ?", keys).Find(&configs).Error
	if err != nil {
		return nil, err
	}

	// 转换为 map
	configMap := make(map[string]string)
	for _, config := range configs {
		configMap[config.Key] = config.Value
	}

	return configMap, nil
}

// SetBatch 批量设置配置
func (r *configRepository) SetBatch(configs map[string]string) error {
	// 使用事务
	return r.db.Transaction(func(tx *gorm.DB) error {
		for key, value := range configs {
			// 先查找是否存在
			var config model.AppConfig
			err := tx.Where("key = ?", key).First(&config).Error

			if err == gorm.ErrRecordNotFound {
				// 不存在，创建新记录
				config = model.AppConfig{
					Key:   key,
					Value: value,
				}
				if err := tx.Create(&config).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				// 存在，更新值
				config.Value = value
				if err := tx.Save(&config).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// List 分页列表查询
func (r *configRepository) List(page, pageSize int) ([]*model.AppConfig, int64, error) {
	var configs []*model.AppConfig
	var total int64

	// 统计总数
	if err := r.db.Model(&model.AppConfig{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 分页
	offset := (page - 1) * pageSize
	if err := r.db.Order("key ASC").Offset(offset).Limit(pageSize).Find(&configs).Error; err != nil {
		return nil, 0, err
	}

	return configs, total, nil
}

// Count 统计配置总数
func (r *configRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.AppConfig{}).Count(&count).Error
	return count, err
}
