package service

import (
	"errors"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
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
