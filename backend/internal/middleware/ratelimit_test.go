package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func setupRateLimitRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/login", LoginRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, model.Response{Success: true})
	})
	return r
}

func TestLoginRateLimit_NormalRequest(t *testing.T) {
	r := setupRateLimitRouter()

	req := httptest.NewRequest("POST", "/login", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp model.Response
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.True(t, resp.Success)
}

func TestLoginRateLimit_ExceedBurst(t *testing.T) {
	r := setupRateLimitRouter()

	// burst=5，前 5 次应该通过
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/login", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
	}

	// 第 6 次应该被限流
	req := httptest.NewRequest("POST", "/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	var resp model.Response
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.False(t, resp.Success)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, "RATE_LIMITED", resp.Error.Code)
}

func TestLoginRateLimit_DifferentIPsIndependent(t *testing.T) {
	r := setupRateLimitRouter()

	// 用光 IP1 的 burst
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/login", nil)
		req.RemoteAddr = "192.168.1.1:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	// IP1 被限流
	req := httptest.NewRequest("POST", "/login", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// IP2 仍然可以正常访问
	req = httptest.NewRequest("POST", "/login", nil)
	req.RemoteAddr = "192.168.1.2:5678"
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestLoginRateLimit_ResponseFormat(t *testing.T) {
	r := setupRateLimitRouter()

	// 用光 burst
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest("POST", "/login", nil)
		req.RemoteAddr = "172.16.0.1:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	// 验证 429 响应格式
	req := httptest.NewRequest("POST", "/login", nil)
	req.RemoteAddr = "172.16.0.1:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var resp model.Response
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Equal(t, "RATE_LIMITED", resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "Too many login attempts")
}
