package service

import (
	"errors"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAuthTestService(t *testing.T) (AuthService, repository.UserRepository, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	repo := repository.NewUserRepository(db)
	svc := NewAuthService(repo, &config.Config{
		Security: config.SecurityConfig{JWTSecret: "test-secret"},
	})

	return svc, repo, db
}

func TestValidateTokenRejectsDeletedUser(t *testing.T) {
	svc, repo, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	user := &model.User{Username: "admin", PasswordHash: "hash", IsFirstLogin: false}
	if err := repo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, _, err := svc.GenerateToken(user.ID, user.Username)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	if err := db.Delete(&model.User{}, user.ID).Error; err != nil {
		t.Fatalf("delete user: %v", err)
	}

	_, err = svc.ValidateToken(token)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestValidateTokenRejectsUpdatedUserSession(t *testing.T) {
	svc, repo, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	user := &model.User{Username: "admin", PasswordHash: "hash", IsFirstLogin: false}
	if err := repo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	token, _, err := svc.GenerateToken(user.ID, user.Username)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)
	user.Username = "admin2"
	if err := repo.Update(user); err != nil {
		t.Fatalf("update user: %v", err)
	}

	_, err = svc.ValidateToken(token)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken after user update, got %v", err)
	}
}

func TestAuthService_InitializeDefaultUser(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	require.NoError(t, svc.InitializeDefaultUser())

	resp, err := svc.Login("admin", "admin")
	require.NoError(t, err)
	assert.Equal(t, "admin", resp.User.Username)
	assert.True(t, resp.IsFirstLogin)
	assert.NotEmpty(t, resp.Token)
}

func TestAuthService_InitializeDefaultUser_AlreadyExists(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	require.NoError(t, svc.InitializeDefaultUser())
	require.NoError(t, svc.InitializeDefaultUser()) // should be a no-op
}

func TestAuthService_Login_InvalidUsername(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	require.NoError(t, svc.InitializeDefaultUser())

	_, err := svc.Login("nonexistent", "admin")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthService_Login_WrongPassword(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	require.NoError(t, svc.InitializeDefaultUser())

	_, err := svc.Login("admin", "wrongPassword")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthService_Login_Success(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	require.NoError(t, svc.InitializeDefaultUser())

	resp, err := svc.Login("admin", "admin")
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Token)
	assert.False(t, resp.ExpiresAt.IsZero())
	assert.Equal(t, uint(1), resp.User.ID)
}

func TestAuthService_GenerateAndValidateToken(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	require.NoError(t, svc.InitializeDefaultUser())

	// Small sleep to ensure token IssuedAt is after user.UpdatedAt
	time.Sleep(1100 * time.Millisecond)

	token, expiresAt, err := svc.GenerateToken(1, "admin")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.False(t, expiresAt.IsZero())

	claims, err := svc.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, uint(1), claims.UserID)
	assert.Equal(t, "admin", claims.Username)
}

func TestAuthService_ValidateToken_Invalid(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	_, err := svc.ValidateToken("invalid-token-string")
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestAuthService_ChangePassword_Success(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	require.NoError(t, svc.InitializeDefaultUser())
	require.NoError(t, svc.ChangePassword(1, "admin", "newPassword123", ""))

	// Should login with new Password
	resp, err := svc.Login("admin", "newPassword123")
	require.NoError(t, err)
	assert.Equal(t, "admin", resp.User.Username)
	assert.False(t, resp.IsFirstLogin) // cleared after Password change
}

func TestAuthService_ChangePassword_WrongOld(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	require.NoError(t, svc.InitializeDefaultUser())

	err := svc.ChangePassword(1, "wrongold", "new123", "")
	assert.ErrorIs(t, err, ErrOldPasswordWrong)
}

func TestAuthService_ChangePassword_UserNotFound(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	err := svc.ChangePassword(999, "admin", "new123", "")
	assert.ErrorIs(t, err, ErrUserNotFound)
}

func TestAuthService_ChangePassword_WithNewUsername(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	require.NoError(t, svc.InitializeDefaultUser())
	require.NoError(t, svc.ChangePassword(1, "admin", "newpw", "newadmin"))

	// New username works
	resp, err := svc.Login("newadmin", "newpw")
	require.NoError(t, err)
	assert.Equal(t, "newadmin", resp.User.Username)

	// Old username fails
	_, err = svc.Login("admin", "newpw")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthService_GetUserInfo_Success(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	require.NoError(t, svc.InitializeDefaultUser())

	info, err := svc.GetUserInfo(1)
	require.NoError(t, err)
	assert.Equal(t, "admin", info.Username)
	assert.True(t, info.IsFirstLogin)
}

func TestAuthService_GetUserInfo_NotFound(t *testing.T) {
	svc, _, db := setupAuthTestService(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	_, err := svc.GetUserInfo(999)
	assert.ErrorIs(t, err, ErrUserNotFound)
}
