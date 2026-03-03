package model

import (
	"time"

	"gorm.io/gorm"
)

// User 用户模型
type User struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 用户信息
	Username     string `gorm:"type:varchar(50);not null;uniqueIndex:idx_username" json:"username"` // 用户名（唯一）
	PasswordHash string `gorm:"type:varchar(255);not null" json:"-"`                                // 密码哈希（bcrypt）

	// 状态
	IsFirstLogin bool `gorm:"default:true" json:"is_first_login"` // 是否首次登录
}

// TableName 指定表名
func (User) TableName() string {
	return "users"
}
