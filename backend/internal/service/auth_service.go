package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or Password")
	ErrUserNotFound       = errors.New("user not found")
	ErrInvalidToken       = errors.New("invalid token")
	ErrTokenExpired       = errors.New("token expired")
	ErrFirstLoginRequired = errors.New("first login, Password change required")
	ErrOldPasswordWrong   = errors.New("old Password is incorrect")
)

// AuthService 认证服务接口
type AuthService interface {
	Login(username, Password string) (*model.LoginResponse, error)
	ChangePassword(userID uint, oldPassword, newPassword, newUsername string) error
	GetUserInfo(userID uint) (*model.UserInfoResponse, error)
	GenerateToken(userID uint, username string) (string, time.Time, error)
	ValidateToken(tokenString string) (*JWTClaims, error)
	InitializeDefaultUser() error
}

// JWTClaims JWT 声明
type JWTClaims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// authService 认证服务实现
type authService struct {
	userRepo   repository.UserRepository
	cfg        *config.Config
	jwtSecret  []byte
	tokenExpiry time.Duration
}

// NewAuthService 创建认证服务
func NewAuthService(userRepo repository.UserRepository, cfg *config.Config) AuthService {
	return &authService{
		userRepo:    userRepo,
		cfg:         cfg,
		jwtSecret:   []byte(cfg.Security.JWTSecret),
		tokenExpiry: 24 * time.Hour, // Token 24小时过期
	}
}

// Login 用户登录
func (s *authService) Login(username, Password string) (*model.LoginResponse, error) {
	// 查找用户
	user, err := s.userRepo.GetByUsername(username)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	// 生成 Token
	tokenString, expiresAt, err := s.GenerateToken(user.ID, user.Username)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	return &model.LoginResponse{
		Token:        tokenString,
		ExpiresAt:    expiresAt,
		User:         model.UserInfo{ID: user.ID, Username: user.Username},
		IsFirstLogin: user.IsFirstLogin,
	}, nil
}

// ChangePassword 修改密码（可选同时修改用户名）
func (s *authService) ChangePassword(userID uint, oldPassword, newPassword, newUsername string) error {
	// 获取用户
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserNotFound
		}
		return fmt.Errorf("failed to get user: %w", err)
	}

	// 验证旧密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)); err != nil {
		return ErrOldPasswordWrong
	}

	// 如果提供了新用户名，检查是否与其他用户冲突
	if newUsername != "" && newUsername != user.Username {
		exists, err := s.userRepo.Exists(newUsername)
		if err != nil {
			return fmt.Errorf("failed to check username: %w", err)
		}
		if exists {
			return errors.New("username already exists")
		}
		user.Username = newUsername
	}

	// 生成新密码哈希
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash Password: %w", err)
	}
	user.PasswordHash = string(newHash)

	// 更新用户信息（用户名和密码）
	if err := s.userRepo.Update(user); err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	// 如果是首次登录，更新状态
	if user.IsFirstLogin {
		if err := s.userRepo.UpdateFirstLoginStatus(userID, false); err != nil {
			logger.Warnf("Failed to update first login status: %v", err)
		}
	}

	return nil
}

// GetUserInfo 获取用户信息
func (s *authService) GetUserInfo(userID uint) (*model.UserInfoResponse, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &model.UserInfoResponse{
		ID:           user.ID,
		Username:     user.Username,
		IsFirstLogin: user.IsFirstLogin,
	}, nil
}

// GenerateToken 生成 JWT Token
func (s *authService) GenerateToken(userID uint, username string) (string, time.Time, error) {
	expiresAt := time.Now().Add(s.tokenExpiry)

	claims := JWTClaims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "relive",
			Subject:   fmt.Sprintf("%d", userID),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", time.Time{}, err
	}

	return tokenString, expiresAt, nil
}

// ValidateToken 验证 JWT Token
func (s *authService) ValidateToken(tokenString string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidToken
}

// InitializeDefaultUser 初始化默认 admin 用户
func (s *authService) InitializeDefaultUser() error {
	// 检查是否已存在用户
	count, err := s.userRepo.Count()
	if err != nil {
		return fmt.Errorf("failed to count users: %w", err)
	}

	// 如果已有用户，不创建默认用户
	if count > 0 {
		return nil
	}

	// 生成默认密码哈希（admin）
	PasswordHash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash default Password: %w", err)
	}

	// 创建默认用户
	defaultUser := &model.User{
		Username:     "admin",
		PasswordHash: string(PasswordHash),
		IsFirstLogin: true,
	}

	if err := s.userRepo.Create(defaultUser); err != nil {
		return fmt.Errorf("failed to create default user: %w", err)
	}

	logger.Info("Default admin user created (username: admin, Password: admin)")
	return nil
}
