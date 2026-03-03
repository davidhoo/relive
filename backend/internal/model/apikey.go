package model

import (
	"time"

	"gorm.io/gorm"
)

// APIKey API Key配置模型
type APIKey struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 基本信息
	Name        string `gorm:"type:varchar(100);not null" json:"name"`        // API Key名称
	Key         string `gorm:"type:varchar(100);not null;uniqueIndex" json:"-"` // 实际的API Key值（不返回）
	Description string `gorm:"type:varchar(500)" json:"description"`           // 描述

	// 状态
	IsActive  bool       `gorm:"default:true" json:"is_active"`  // 是否启用
	ExpiresAt *time.Time `json:"expires_at,omitempty"`           // 过期时间（可选）

	// 使用统计
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`     // 最后使用时间
	UseCount   int64      `gorm:"default:0" json:"use_count"` // 使用次数
}

// TableName 指定表名
func (APIKey) TableName() string {
	return "api_keys"
}

// IsValid 检查API Key是否有效
func (k *APIKey) IsValid() bool {
	if !k.IsActive {
		return false
	}
	if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
		return false
	}
	return true
}

// APIKeyResponse API Key响应（包含Key值，仅创建时返回）
type APIKeyResponse struct {
	ID          uint       `json:"id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Name        string     `json:"name"`
	Key         string     `json:"key,omitempty"` // 仅在创建/重新生成时返回
	Description string     `json:"description"`
	IsActive    bool       `json:"is_active"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	UseCount    int64      `json:"use_count"`
}

// CreateAPIKeyRequest 创建API Key请求
type CreateAPIKeyRequest struct {
	Name        string     `json:"name" binding:"required"`
	Description string     `json:"description"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// UpdateAPIKeyRequest 更新API Key请求
type UpdateAPIKeyRequest struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IsActive    *bool      `json:"is_active,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// RegenerateAPIKeyResponse 重新生成API Key响应
type RegenerateAPIKeyResponse struct {
	ID  uint   `json:"id"`
	Key string `json:"key"`
}
