package testutil

import (
	"errors"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
)

// ErrStubNotFound is a sentinel error for stubs when no function is configured.
var ErrStubNotFound = errors.New("stub: not found")

// StubAuthService implements service.AuthService with configurable function fields.
// Any unconfigured method returns zero values or nil error.
type StubAuthService struct {
	LoginFunc           func(username, password string) (*model.LoginResponse, error)
	ChangePasswordFunc  func(userID uint, oldPw, newPw, newUsername string) error
	GetUserInfoFunc     func(userID uint) (*model.UserInfoResponse, error)
	GenerateTokenFunc   func(userID uint, username string) (string, time.Time, error)
	ValidateTokenFunc   func(tokenString string) (*service.JWTClaims, error)
	InitDefaultUserFunc func() error
}

func (s *StubAuthService) Login(username, password string) (*model.LoginResponse, error) {
	if s.LoginFunc != nil {
		return s.LoginFunc(username, password)
	}
	return nil, nil
}

func (s *StubAuthService) ChangePassword(userID uint, oldPw, newPw, newUsername string) error {
	if s.ChangePasswordFunc != nil {
		return s.ChangePasswordFunc(userID, oldPw, newPw, newUsername)
	}
	return nil
}

func (s *StubAuthService) GetUserInfo(userID uint) (*model.UserInfoResponse, error) {
	if s.GetUserInfoFunc != nil {
		return s.GetUserInfoFunc(userID)
	}
	return nil, nil
}

func (s *StubAuthService) GenerateToken(userID uint, username string) (string, time.Time, error) {
	if s.GenerateTokenFunc != nil {
		return s.GenerateTokenFunc(userID, username)
	}
	return "", time.Time{}, nil
}

func (s *StubAuthService) ValidateToken(tokenString string) (*service.JWTClaims, error) {
	if s.ValidateTokenFunc != nil {
		return s.ValidateTokenFunc(tokenString)
	}
	return nil, nil
}

func (s *StubAuthService) InitializeDefaultUser() error {
	if s.InitDefaultUserFunc != nil {
		return s.InitDefaultUserFunc()
	}
	return nil
}

// StubDeviceService implements service.DeviceService with configurable function fields.
// Only methods needed for middleware testing are wired up; add more as needed.
type StubDeviceService struct {
	GetByAPIKeyFunc   func(apiKey string) (*model.Device, error)
	UpdateLastSeenFunc func(deviceID uint, ip string)
}

func (s *StubDeviceService) GetByAPIKey(apiKey string) (*model.Device, error) {
	if s.GetByAPIKeyFunc != nil {
		return s.GetByAPIKeyFunc(apiKey)
	}
	return nil, ErrStubNotFound
}

func (s *StubDeviceService) UpdateLastSeen(deviceID uint, ip string) {
	if s.UpdateLastSeenFunc != nil {
		s.UpdateLastSeenFunc(deviceID, ip)
	}
}

// The remaining DeviceService methods are no-ops to satisfy the interface.

func (s *StubDeviceService) Create(_ *model.CreateDeviceRequest) (*model.CreateDeviceResponse, error) {
	return nil, nil
}
func (s *StubDeviceService) Delete(_ uint) error        { return nil }
func (s *StubDeviceService) Update(_ *model.Device) error { return nil }
func (s *StubDeviceService) UpdateEnabled(_ uint, _ bool) error { return nil }
func (s *StubDeviceService) UpdateRenderProfile(_ uint, _ string) error { return nil }
func (s *StubDeviceService) GetByID(_ uint) (*model.Device, error) { return nil, nil }
func (s *StubDeviceService) GetByDeviceID(_ string) (*model.Device, error) { return nil, nil }
func (s *StubDeviceService) List(_ int, _ int) ([]*model.Device, int64, error) { return nil, 0, nil }
func (s *StubDeviceService) ListByDeviceType(_ string) ([]*model.Device, error) { return nil, nil }
func (s *StubDeviceService) ListByPlatform(_ string) ([]*model.Device, error) { return nil, nil }
func (s *StubDeviceService) CountAll() (int64, error) { return 0, nil }
func (s *StubDeviceService) CountOnline() (int64, error) { return 0, nil }
func (s *StubDeviceService) CountByDeviceType(_ string) (int64, error) { return 0, nil }
func (s *StubDeviceService) CountByPlatform(_ string) (int64, error) { return 0, nil }
