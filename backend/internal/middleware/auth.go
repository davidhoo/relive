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
			statusCode := http.StatusUnauthorized
			message := "用户不存在或登录态已失效，请重新登录"
			if err != service.ErrUserNotFound {
				statusCode = http.StatusInternalServerError
				message = "验证登录状态失败"
			}

			c.JSON(statusCode, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "UNAUTHORIZED",
					Message: message,
				},
			})
			c.Abort()
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

// PhotoAuth 照片图片访问认证中间件
// 同时支持 JWT（Web 端）和 API Key（嵌入式设备），任一通过即放行
func PhotoAuth(authService service.AuthService, deviceService service.DeviceService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 尝试 Authorization: Bearer <token>
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				token := parts[1]
				// 先尝试 JWT
				if claims, err := authService.ValidateToken(token); err == nil {
					c.Set(ContextUserIDKey, claims.UserID)
					c.Set(ContextUsernameKey, claims.Username)
					c.Next()
					return
				}
				// 再尝试 API Key
				if device, err := deviceService.GetByAPIKey(token); err == nil && device.IsEnabled {
					deviceService.UpdateLastSeen(device.ID, c.ClientIP())
					c.Set(ContextDeviceIDKey, device.ID)
					c.Next()
					return
				}
			}
		}

		// 尝试 X-API-Key header
		if apiKey := c.GetHeader("X-API-Key"); apiKey != "" {
			if device, err := deviceService.GetByAPIKey(apiKey); err == nil && device.IsEnabled {
				deviceService.UpdateLastSeen(device.ID, c.ClientIP())
				c.Set(ContextDeviceIDKey, device.ID)
				c.Next()
				return
			}
		}

		// 尝试 ?token= 查询参数（用于 <img> 标签直接加载，无法附加 header）
		if token := c.Query("token"); token != "" {
			if claims, err := authService.ValidateToken(token); err == nil {
				c.Set(ContextUserIDKey, claims.UserID)
				c.Set(ContextUsernameKey, claims.Username)
				c.Next()
				return
			}
		}

		c.JSON(http.StatusUnauthorized, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "UNAUTHORIZED",
				Message: "Authentication required",
			},
		})
		c.Abort()
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
