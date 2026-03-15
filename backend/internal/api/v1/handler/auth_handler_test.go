package handler

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthHandler_Login_Success(t *testing.T) {
	authSvc := &testutil.StubAuthService{
		LoginFunc: func(username, pw string) (*model.LoginResponse, error) {
			return &model.LoginResponse{
				Token:     "jwt-token",
				ExpiresAt: time.Now().Add(24 * time.Hour),
				User:      model.UserInfo{ID: 1, Username: "admin"},
			}, nil
		},
	}
	h := NewAuthHandler(authSvc, false)

	body := []byte(`{"username":"admin","Password":"password123"}`)
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/auth/login", body, nil, h.Login)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)

	// 验证 Set-Cookie
	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "relive_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "Login should set relive_session cookie")
	assert.Equal(t, "jwt-token", sessionCookie.Value)
	assert.True(t, sessionCookie.HttpOnly)
	assert.Equal(t, "/api/v1/", sessionCookie.Path)
}

func TestAuthHandler_Login_InvalidCredentials(t *testing.T) {
	authSvc := &testutil.StubAuthService{
		LoginFunc: func(username, pw string) (*model.LoginResponse, error) {
			return nil, service.ErrInvalidCredentials
		},
	}
	h := NewAuthHandler(authSvc, false)

	body := []byte(`{"username":"admin","Password":"wrong"}`)
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/auth/login", body, nil, h.Login)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthHandler_Login_BadJSON(t *testing.T) {
	authSvc := &testutil.StubAuthService{}
	h := NewAuthHandler(authSvc, false)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/auth/login", []byte(`{bad`), nil, h.Login)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthHandler_Login_InternalError(t *testing.T) {
	authSvc := &testutil.StubAuthService{
		LoginFunc: func(username, pw string) (*model.LoginResponse, error) {
			return nil, errors.New("db error")
		},
	}
	h := NewAuthHandler(authSvc, false)

	body := []byte(`{"username":"admin","Password":"password123"}`)
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/auth/login", body, nil, h.Login)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAuthHandler_Logout(t *testing.T) {
	h := NewAuthHandler(&testutil.StubAuthService{}, false)

	rec := performJSONRequest(t, http.MethodPost, "/api/v1/auth/logout", nil, nil, h.Logout)
	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)

	// 验证 cookie 被清除
	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "relive_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "Logout should clear relive_session cookie")
	assert.Equal(t, "", sessionCookie.Value)
	assert.True(t, sessionCookie.MaxAge < 0)
}

func TestAuthHandler_GetUserInfo_Success(t *testing.T) {
	authSvc := &testutil.StubAuthService{
		GetUserInfoFunc: func(userID uint) (*model.UserInfoResponse, error) {
			return &model.UserInfoResponse{ID: 1, Username: "admin", IsFirstLogin: false}, nil
		},
	}
	h := NewAuthHandler(authSvc, false)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/auth/user", nil, nil, func(c *gin.Context) {
		c.Set("userID", uint(1))
		h.GetUserInfo(c)
	})

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	require.True(t, resp.Success)
}

func TestAuthHandler_GetUserInfo_NotAuthenticated(t *testing.T) {
	h := NewAuthHandler(&testutil.StubAuthService{}, false)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/auth/user", nil, nil, h.GetUserInfo)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthHandler_GetUserInfo_NotFound(t *testing.T) {
	authSvc := &testutil.StubAuthService{
		GetUserInfoFunc: func(userID uint) (*model.UserInfoResponse, error) {
			return nil, service.ErrUserNotFound
		},
	}
	h := NewAuthHandler(authSvc, false)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/auth/user", nil, nil, func(c *gin.Context) {
		c.Set("userID", uint(999))
		h.GetUserInfo(c)
	})

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAuthHandler_ChangePassword_Success(t *testing.T) {
	authSvc := &testutil.StubAuthService{
		ChangePasswordFunc: func(userID uint, oldPw, newPw, newUsername string) error {
			return nil
		},
	}
	h := NewAuthHandler(authSvc, false)

	body := []byte(`{"old_Password":"old123","new_Password":"new123456"}`)
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/auth/change-password", body, nil, func(c *gin.Context) {
		c.Set("userID", uint(1))
		h.ChangePassword(c)
	})

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthHandler_ChangePassword_OldPwWrong(t *testing.T) {
	authSvc := &testutil.StubAuthService{
		ChangePasswordFunc: func(userID uint, oldPw, newPw, newUsername string) error {
			return service.ErrOldPasswordWrong
		},
	}
	h := NewAuthHandler(authSvc, false)

	body := []byte(`{"old_Password":"wrong","new_Password":"new123456"}`)
	rec := performJSONRequest(t, http.MethodPost, "/api/v1/auth/change-password", body, nil, func(c *gin.Context) {
		c.Set("userID", uint(1))
		h.ChangePassword(c)
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
