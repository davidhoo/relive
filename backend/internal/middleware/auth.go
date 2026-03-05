package middleware

import (
	"net/http"
	"strings"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/gin-gonic/gin"
)

// Context keys
const (
	ContextUserIDKey   = "userID"
	ContextUsernameKey = "username"
	ContextDeviceIDKey = "deviceID"
)

// JWTAuth JWT 认证中间件
func JWTAuth(authService service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从 Header 获取 Token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "UNAUTHORIZED",
					Message: "Authorization header required",
				},
			})
			c.Abort()
			return
		}

		// 提取 Bearer Token
		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			c.JSON(http.StatusUnauthorized, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "UNAUTHORIZED",
					Message: "Invalid authorization header format",
				},
			})
			c.Abort()
			return
		}

		tokenString := parts[1]

		// 验证 Token
		claims, err := authService.ValidateToken(tokenString)
		if err != nil {
			var message string
			switch err {
			case service.ErrTokenExpired:
				message = "Token expired"
			case service.ErrInvalidToken:
				message = "Invalid token"
			default:
				message = "Authentication failed"
			}

			c.JSON(http.StatusUnauthorized, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "UNAUTHORIZED",
					Message: message,
				},
			})
			c.Abort()
			return
		}

		// 将用户信息存入上下文
		c.Set(ContextUserIDKey, claims.UserID)
		c.Set(ContextUsernameKey, claims.Username)

		c.Next()
	}
}

// FirstLoginCheck 首次登录检查中间件
// 如果用户是首次登录，只允许访问修改密码接口
func FirstLoginCheck(authService service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 跳过修改密码接口本身的检查
		if c.Request.URL.Path == "/api/v1/auth/change-Password" {
			c.Next()
			return
		}

		userID, exists := c.Get(ContextUserIDKey)
		if !exists {
			c.Next()
			return
		}

		// 获取用户信息
		userInfo, err := authService.GetUserInfo(userID.(uint))
		if err != nil {
			c.Next()
			return
		}

		// 如果是首次登录，返回错误
		if userInfo.IsFirstLogin {
			c.JSON(http.StatusForbidden, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "FIRST_LOGIN_REQUIRED",
					Message: "首次登录，请先修改密码",
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// APIKeyAuth API Key 认证中间件（用于设备和 Analyzer）
// 统一从 devices 表验证 API Key
func APIKeyAuth(deviceService service.DeviceService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var apiKey string

		// 1. 尝试从 Authorization Header 获取 Bearer Token
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				apiKey = parts[1]
			}
		}

		// 2. 尝试从 X-API-Key Header 获取
		if apiKey == "" {
			apiKey = c.GetHeader("X-API-Key")
		}

		// 3. 尝试从查询参数获取（某些设备可能更方便）
		if apiKey == "" {
			apiKey = c.Query("api_key")
		}

		if apiKey == "" {
			c.JSON(http.StatusUnauthorized, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "UNAUTHORIZED",
					Message: "API Key required",
				},
			})
			c.Abort()
			return
		}

		// 从设备表验证 API Key
		device, err := deviceService.GetByAPIKey(apiKey)
		if err != nil {
			c.JSON(http.StatusUnauthorized, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "UNAUTHORIZED",
					Message: "Invalid API Key",
				},
			})
			c.Abort()
			return
		}

		// 检查设备是否可用
		if !device.IsEnabled {
			c.JSON(http.StatusForbidden, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "DEVICE_DISABLED",
					Message: "Device is disabled",
				},
			})
			c.Abort()
			return
		}

		// 更新设备最后请求时间和 IP（异步，不阻塞请求）
		deviceService.UpdateLastSeen(device.ID, c.ClientIP())

		// 将设备信息存入上下文
		c.Set("device_id", device.ID)
		c.Set("device_id_str", device.DeviceID)
		c.Set("device_name", device.Name)

		c.Next()
	}
}
