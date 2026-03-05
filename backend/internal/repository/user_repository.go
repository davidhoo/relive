package repository

import (
	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// UserRepository 用户仓库接口
type UserRepository interface {
	// 基础 CRUD
	Create(user *model.User) error
	Update(user *model.User) error
	GetByID(id uint) (*model.User, error)
	GetByUsername(username string) (*model.User, error)

	// 查询
	Exists(username string) (bool, error)
	Count() (int64, error)

	// 密码相关
	UpdatePassword(userID uint, PasswordHash string) error
	UpdateFirstLoginStatus(userID uint, isFirstLogin bool) error
}

// userRepository 用户仓库实现
type userRepository struct {
	db *gorm.DB
}

// NewUserRepository 创建用户仓库
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

// Create 创建用户
func (r *userRepository) Create(user *model.User) error {
	return r.db.Create(user).Error
}

// Update 更新用户
func (r *userRepository) Update(user *model.User) error {
	return r.db.Save(user).Error
}

// GetByID 根据 ID 获取用户
func (r *userRepository) GetByID(id uint) (*model.User, error) {
	var user model.User
	err := r.db.First(&user, id).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetByUsername 根据用户名获取用户
func (r *userRepository) GetByUsername(username string) (*model.User, error) {
	var user model.User
	err := r.db.Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// Exists 检查用户是否存在
func (r *userRepository) Exists(username string) (bool, error) {
	var count int64
	err := r.db.Model(&model.User{}).Where("username = ?", username).Count(&count).Error
	return count > 0, err
}

// Count 统计用户总数
func (r *userRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.User{}).Count(&count).Error
	return count, err
}

// UpdatePassword 更新密码
func (r *userRepository) UpdatePassword(userID uint, PasswordHash string) error {
	return r.db.Model(&model.User{}).
		Where("id = ?", userID).
		Update("password_hash", PasswordHash).Error
}

// UpdateFirstLoginStatus 更新首次登录状态
func (r *userRepository) UpdateFirstLoginStatus(userID uint, isFirstLogin bool) error {
	return r.db.Model(&model.User{}).
		Where("id = ?", userID).
		Update("is_first_login", isFirstLogin).Error
}
