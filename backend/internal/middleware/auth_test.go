package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
	testutil.SuppressLogger()
}

// middlewareResult captures the outcome of running a single middleware.
type middlewareResult struct {
	recorder *httptest.ResponseRecorder
	ctx      *gin.Context // non-nil only if the handler after middleware was reached
	passed   bool
}

// runMW executes a middleware via a gin router and captures context values.
func runMW(t *testing.T, req *http.Request, mw gin.HandlerFunc) middlewareResult {
	t.Helper()
	var res middlewareResult
	r := gin.New()
	r.Use(mw)
	r.Any("/*path", func(c *gin.Context) {
		res.passed = true
		res.ctx = c
	})
	res.recorder = httptest.NewRecorder()
	r.ServeHTTP(res.recorder, req)
	return res
}

// runMWChain executes a chain of middlewares.
func runMWChain(t *testing.T, req *http.Request, middlewares ...gin.HandlerFunc) middlewareResult {
	t.Helper()
	var res middlewareResult
	r := gin.New()
	for _, mw := range middlewares {
		r.Use(mw)
	}
	r.Any("/*path", func(c *gin.Context) {
		res.passed = true
		res.ctx = c
	})
	res.recorder = httptest.NewRecorder()
	r.ServeHTTP(res.recorder, req)
	return res
}

func newReq(method, url string) *http.Request {
	return httptest.NewRequest(method, url, nil)
}

func newGetReq(url string) *http.Request {
	return newReq(http.MethodGet, url)
}

// ===== JWTAuth =====

func TestJWTAuth_NoHeader(t *testing.T) {
	auth := &testutil.StubAuthService{}
	res := runMW(t, newGetReq("/test"), JWTAuth(auth))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusUnauthorized, res.recorder.Code)
}

func TestJWTAuth_BadFormat(t *testing.T) {
	auth := &testutil.StubAuthService{}
	req := newGetReq("/test")
	req.Header.Set("Authorization", "Basic abc123")

	res := runMW(t, req, JWTAuth(auth))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusUnauthorized, res.recorder.Code)
}

func TestJWTAuth_ExpiredToken(t *testing.T) {
	auth := &testutil.StubAuthService{
		ValidateTokenFunc: func(token string) (*service.JWTClaims, error) {
			return nil, service.ErrTokenExpired
		},
	}
	req := newGetReq("/test")
	req.Header.Set("Authorization", "Bearer expired-token")

	res := runMW(t, req, JWTAuth(auth))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusUnauthorized, res.recorder.Code)
	assert.Contains(t, res.recorder.Body.String(), "Token expired")
}

func TestJWTAuth_InvalidToken(t *testing.T) {
	auth := &testutil.StubAuthService{
		ValidateTokenFunc: func(token string) (*service.JWTClaims, error) {
			return nil, service.ErrInvalidToken
		},
	}
	req := newGetReq("/test")
	req.Header.Set("Authorization", "Bearer bad-token")

	res := runMW(t, req, JWTAuth(auth))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusUnauthorized, res.recorder.Code)
	assert.Contains(t, res.recorder.Body.String(), "Invalid token")
}

func TestJWTAuth_GenericError(t *testing.T) {
	auth := &testutil.StubAuthService{
		ValidateTokenFunc: func(token string) (*service.JWTClaims, error) {
			return nil, errors.New("something broke")
		},
	}
	req := newGetReq("/test")
	req.Header.Set("Authorization", "Bearer some-token")

	res := runMW(t, req, JWTAuth(auth))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusUnauthorized, res.recorder.Code)
	assert.Contains(t, res.recorder.Body.String(), "Authentication failed")
}

func TestJWTAuth_Success(t *testing.T) {
	auth := &testutil.StubAuthService{
		ValidateTokenFunc: func(token string) (*service.JWTClaims, error) {
			return &service.JWTClaims{UserID: 42, Username: "alice"}, nil
		},
	}
	req := newGetReq("/test")
	req.Header.Set("Authorization", "Bearer valid-token")

	res := runMW(t, req, JWTAuth(auth))

	require.True(t, res.passed)
	assert.Equal(t, http.StatusOK, res.recorder.Code)
	uid, _ := res.ctx.Get(ContextUserIDKey)
	assert.Equal(t, uint(42), uid)
	uname, _ := res.ctx.Get(ContextUsernameKey)
	assert.Equal(t, "alice", uname)
}

// ===== FirstLoginCheck =====

func TestFirstLoginCheck_SkipsChangePwdPath(t *testing.T) {
	auth := &testutil.StubAuthService{}
	req := newReq(http.MethodPost, "/api/v1/auth/change-Password")

	res := runMW(t, req, FirstLoginCheck(auth))

	assert.True(t, res.passed)
	assert.Equal(t, http.StatusOK, res.recorder.Code)
}

func TestFirstLoginCheck_NoUserID_Passthrough(t *testing.T) {
	auth := &testutil.StubAuthService{}
	req := newGetReq("/api/v1/photos")

	res := runMW(t, req, FirstLoginCheck(auth))

	assert.True(t, res.passed)
	assert.Equal(t, http.StatusOK, res.recorder.Code)
}

func TestFirstLoginCheck_FirstLogin_Forbidden(t *testing.T) {
	auth := &testutil.StubAuthService{
		GetUserInfoFunc: func(userID uint) (*model.UserInfoResponse, error) {
			return &model.UserInfoResponse{ID: 1, Username: "admin", IsFirstLogin: true}, nil
		},
	}
	// Use a chain: first middleware sets the userID, second is FirstLoginCheck
	setUserIDMW := func(c *gin.Context) { c.Set(ContextUserIDKey, uint(1)); c.Next() }
	req := newGetReq("/api/v1/photos")

	res := runMWChain(t, req, setUserIDMW, FirstLoginCheck(auth))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusForbidden, res.recorder.Code)
	assert.Contains(t, res.recorder.Body.String(), "FIRST_LOGIN_REQUIRED")
}

func TestFirstLoginCheck_NotFirstLogin_Passthrough(t *testing.T) {
	auth := &testutil.StubAuthService{
		GetUserInfoFunc: func(userID uint) (*model.UserInfoResponse, error) {
			return &model.UserInfoResponse{ID: 1, Username: "admin", IsFirstLogin: false}, nil
		},
	}
	setUserIDMW := func(c *gin.Context) { c.Set(ContextUserIDKey, uint(1)); c.Next() }
	req := newGetReq("/api/v1/photos")

	res := runMWChain(t, req, setUserIDMW, FirstLoginCheck(auth))

	assert.True(t, res.passed)
	assert.Equal(t, http.StatusOK, res.recorder.Code)
}

func TestFirstLoginCheck_UserNotFound(t *testing.T) {
	auth := &testutil.StubAuthService{
		GetUserInfoFunc: func(userID uint) (*model.UserInfoResponse, error) {
			return nil, service.ErrUserNotFound
		},
	}
	setUserIDMW := func(c *gin.Context) { c.Set(ContextUserIDKey, uint(999)); c.Next() }
	req := newGetReq("/test")

	res := runMWChain(t, req, setUserIDMW, FirstLoginCheck(auth))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusUnauthorized, res.recorder.Code)
}

func TestFirstLoginCheck_InternalError(t *testing.T) {
	auth := &testutil.StubAuthService{
		GetUserInfoFunc: func(userID uint) (*model.UserInfoResponse, error) {
			return nil, errors.New("db error")
		},
	}
	setUserIDMW := func(c *gin.Context) { c.Set(ContextUserIDKey, uint(1)); c.Next() }
	req := newGetReq("/test")

	res := runMWChain(t, req, setUserIDMW, FirstLoginCheck(auth))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusInternalServerError, res.recorder.Code)
}

// ===== PhotoAuth =====

func TestPhotoAuth_JWT_Success(t *testing.T) {
	auth := &testutil.StubAuthService{
		ValidateTokenFunc: func(token string) (*service.JWTClaims, error) {
			return &service.JWTClaims{UserID: 1, Username: "alice"}, nil
		},
	}
	deviceSvc := &testutil.StubDeviceService{}
	req := newGetReq("/photo/1.jpg")
	req.Header.Set("Authorization", "Bearer jwt-token")

	res := runMW(t, req, PhotoAuth(auth, deviceSvc))

	require.True(t, res.passed)
	assert.Equal(t, http.StatusOK, res.recorder.Code)
	uid, _ := res.ctx.Get(ContextUserIDKey)
	assert.Equal(t, uint(1), uid)
}

func TestPhotoAuth_BearerAPIKey_Success(t *testing.T) {
	auth := &testutil.StubAuthService{
		ValidateTokenFunc: func(token string) (*service.JWTClaims, error) {
			return nil, service.ErrInvalidToken
		},
	}
	var seenDeviceID uint
	deviceSvc := &testutil.StubDeviceService{
		GetByAPIKeyFunc: func(apiKey string) (*model.Device, error) {
			return &model.Device{ID: 10, IsEnabled: true}, nil
		},
		UpdateLastSeenFunc: func(deviceID uint, ip string) {
			seenDeviceID = deviceID
		},
	}
	req := newGetReq("/photo/1.jpg")
	req.Header.Set("Authorization", "Bearer my-api-key")

	res := runMW(t, req, PhotoAuth(auth, deviceSvc))

	require.True(t, res.passed)
	assert.Equal(t, http.StatusOK, res.recorder.Code)
	assert.Equal(t, uint(10), seenDeviceID)
	did, _ := res.ctx.Get(ContextDeviceIDKey)
	assert.Equal(t, uint(10), did)
}

func TestPhotoAuth_XAPIKey_Success(t *testing.T) {
	auth := &testutil.StubAuthService{
		ValidateTokenFunc: func(token string) (*service.JWTClaims, error) {
			return nil, service.ErrInvalidToken
		},
	}
	deviceSvc := &testutil.StubDeviceService{
		GetByAPIKeyFunc: func(apiKey string) (*model.Device, error) {
			if apiKey == "x-key" {
				return &model.Device{ID: 20, IsEnabled: true}, nil
			}
			return nil, testutil.ErrStubNotFound
		},
		UpdateLastSeenFunc: func(deviceID uint, ip string) {},
	}
	req := newGetReq("/photo/1.jpg")
	req.Header.Set("X-API-Key", "x-key")

	res := runMW(t, req, PhotoAuth(auth, deviceSvc))

	require.True(t, res.passed)
	assert.Equal(t, http.StatusOK, res.recorder.Code)
}

func TestPhotoAuth_QueryToken_Success(t *testing.T) {
	auth := &testutil.StubAuthService{
		ValidateTokenFunc: func(token string) (*service.JWTClaims, error) {
			if token == "query-jwt" {
				return &service.JWTClaims{UserID: 5, Username: "bob"}, nil
			}
			return nil, service.ErrInvalidToken
		},
	}
	deviceSvc := &testutil.StubDeviceService{}
	req := newGetReq("/photo/1.jpg?token=query-jwt")

	res := runMW(t, req, PhotoAuth(auth, deviceSvc))

	require.True(t, res.passed)
	assert.Equal(t, http.StatusOK, res.recorder.Code)
	uid, _ := res.ctx.Get(ContextUserIDKey)
	assert.Equal(t, uint(5), uid)
}

func TestPhotoAuth_AllFail_Unauthorized(t *testing.T) {
	auth := &testutil.StubAuthService{
		ValidateTokenFunc: func(token string) (*service.JWTClaims, error) {
			return nil, service.ErrInvalidToken
		},
	}
	deviceSvc := &testutil.StubDeviceService{
		GetByAPIKeyFunc: func(apiKey string) (*model.Device, error) {
			return nil, testutil.ErrStubNotFound
		},
	}
	req := newGetReq("/photo/1.jpg")

	res := runMW(t, req, PhotoAuth(auth, deviceSvc))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusUnauthorized, res.recorder.Code)
}

func TestPhotoAuth_DisabledDevice_Fails(t *testing.T) {
	auth := &testutil.StubAuthService{
		ValidateTokenFunc: func(token string) (*service.JWTClaims, error) {
			return nil, service.ErrInvalidToken
		},
	}
	deviceSvc := &testutil.StubDeviceService{
		GetByAPIKeyFunc: func(apiKey string) (*model.Device, error) {
			return &model.Device{ID: 30, IsEnabled: false}, nil
		},
	}
	req := newGetReq("/photo/1.jpg")
	req.Header.Set("Authorization", "Bearer disabled-key")

	res := runMW(t, req, PhotoAuth(auth, deviceSvc))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusUnauthorized, res.recorder.Code)
}

// ===== APIKeyAuth =====

func TestAPIKeyAuth_NoKey(t *testing.T) {
	deviceSvc := &testutil.StubDeviceService{}
	req := newGetReq("/api/v1/analyzer/tasks")

	res := runMW(t, req, APIKeyAuth(deviceSvc))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusUnauthorized, res.recorder.Code)
	assert.Contains(t, res.recorder.Body.String(), "API Key required")
}

func TestAPIKeyAuth_InvalidKey(t *testing.T) {
	deviceSvc := &testutil.StubDeviceService{
		GetByAPIKeyFunc: func(apiKey string) (*model.Device, error) {
			return nil, testutil.ErrStubNotFound
		},
	}
	req := newGetReq("/api/v1/analyzer/tasks")
	req.Header.Set("Authorization", "Bearer bad-key")

	res := runMW(t, req, APIKeyAuth(deviceSvc))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusUnauthorized, res.recorder.Code)
	assert.Contains(t, res.recorder.Body.String(), "Invalid API Key")
}

func TestAPIKeyAuth_DeviceDisabled(t *testing.T) {
	deviceSvc := &testutil.StubDeviceService{
		GetByAPIKeyFunc: func(apiKey string) (*model.Device, error) {
			return &model.Device{ID: 1, DeviceID: "D1", Name: "Frame", IsEnabled: false}, nil
		},
	}
	req := newGetReq("/api/v1/analyzer/tasks")
	req.Header.Set("Authorization", "Bearer valid-key")

	res := runMW(t, req, APIKeyAuth(deviceSvc))

	assert.False(t, res.passed)
	assert.Equal(t, http.StatusForbidden, res.recorder.Code)
	assert.Contains(t, res.recorder.Body.String(), "DEVICE_DISABLED")
}

func TestAPIKeyAuth_BearerSuccess(t *testing.T) {
	var seenDeviceID uint
	deviceSvc := &testutil.StubDeviceService{
		GetByAPIKeyFunc: func(apiKey string) (*model.Device, error) {
			return &model.Device{ID: 5, DeviceID: "D5", Name: "Analyzer", IsEnabled: true}, nil
		},
		UpdateLastSeenFunc: func(deviceID uint, ip string) {
			seenDeviceID = deviceID
		},
	}
	req := newGetReq("/api/v1/analyzer/tasks")
	req.Header.Set("Authorization", "Bearer good-key")

	res := runMW(t, req, APIKeyAuth(deviceSvc))

	require.True(t, res.passed)
	assert.Equal(t, http.StatusOK, res.recorder.Code)
	did, _ := res.ctx.Get("device_id")
	assert.Equal(t, uint(5), did)
	dname, _ := res.ctx.Get("device_name")
	assert.Equal(t, "Analyzer", dname)
	assert.Equal(t, uint(5), seenDeviceID)
}

func TestAPIKeyAuth_XAPIKey_Success(t *testing.T) {
	deviceSvc := &testutil.StubDeviceService{
		GetByAPIKeyFunc: func(apiKey string) (*model.Device, error) {
			return &model.Device{ID: 6, DeviceID: "D6", Name: "ESP32", IsEnabled: true}, nil
		},
		UpdateLastSeenFunc: func(deviceID uint, ip string) {},
	}
	req := newGetReq("/api/v1/analyzer/tasks")
	req.Header.Set("X-API-Key", "x-key-value")

	res := runMW(t, req, APIKeyAuth(deviceSvc))

	require.True(t, res.passed)
	assert.Equal(t, http.StatusOK, res.recorder.Code)
	did, _ := res.ctx.Get("device_id_str")
	assert.Equal(t, "D6", did)
}
