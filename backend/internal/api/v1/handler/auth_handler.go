package handler

import (
	"net/http"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// AuthHandler 认证处理器
type AuthHandler struct {
	authService  service.AuthService
	isProduction bool
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(authService service.AuthService, isProduction bool) *AuthHandler {
	return &AuthHandler{
		authService:  authService,
		isProduction: isProduction,
	}
}

// Login 用户登录
// @Summary 用户登录
// @Description 用户登录获取 JWT Token
// @Tags auth
// @Accept json
// @Produce json
// @Param request body model.LoginRequest true "登录请求"
// @Success 200 {object} model.Response{data=model.LoginResponse}
// @Failure 400 {object} model.Response
// @Failure 401 {object} model.Response
// @Router /api/v1/auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req model.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "Invalid request: " + err.Error(),
			},
		})
		return
	}

	resp, err := h.authService.Login(req.Username, req.Password)
	if err != nil {
		if err == service.ErrInvalidCredentials {
			c.JSON(http.StatusUnauthorized, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "INVALID_CREDENTIALS",
					Message: "Invalid username or Password",
				},
			})
			return
		}
		logger.Errorf("Login failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INTERNAL_ERROR",
				Message: "Login failed",
			},
		})
		return
	}

	c.SetCookie("relive_session", resp.Token, 86400, "/api/v1/", "", h.isProduction, true)

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    resp,
		Message: "Login successful",
	})
}

// Logout 用户登出
// @Summary 用户登出
// @Description 用户登出（客户端清除 Token）
// @Tags auth
// @Produce json
// @Success 200 {object} model.Response
// @Router /api/v1/auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	// JWT 是无状态的，登出由客户端清除 Token
	// 可选：将 Token 加入黑名单（如果需要实现服务端登出）
	c.SetCookie("relive_session", "", -1, "/api/v1/", "", h.isProduction, true)

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Logout successful",
	})
}

// ChangePassword 修改密码
// @Summary 修改密码
// @Description 修改当前用户密码（首次登录必须修改）
// @Tags auth
// @Accept json
// @Produce json
// @Param request body model.ChangePasswordRequest true "修改密码请求"
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 401 {object} model.Response
// @Router /api/v1/auth/change-Password [post]
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	// 从上下文获取当前用户ID
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "UNAUTHORIZED",
				Message: "User not authenticated",
			},
		})
		return
	}

	var req model.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: "Invalid request: " + err.Error(),
			},
		})
		return
	}

	err := h.authService.ChangePassword(userID.(uint), req.OldPassword, req.NewPassword, req.NewUsername)
	if err != nil {
		switch err {
		case service.ErrUserNotFound:
			c.JSON(http.StatusNotFound, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "USER_NOT_FOUND",
					Message: "User not found",
				},
			})
		case service.ErrOldPasswordWrong:
			c.JSON(http.StatusBadRequest, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "OLD_Password_WRONG",
					Message: "Old Password is incorrect",
				},
			})
		default:
			if err.Error() == "username already exists" {
				c.JSON(http.StatusConflict, model.Response{
					Success: false,
					Error: &model.ErrorInfo{
						Code:    "USERNAME_EXISTS",
						Message: "Username already exists",
					},
				})
				return
			}
			logger.Errorf("Change Password failed: %v", err)
			c.JSON(http.StatusInternalServerError, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "INTERNAL_ERROR",
					Message: "Failed to change Password",
				},
			})
		}
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Password changed successfully",
	})
}

// GetUserInfo 获取当前用户信息
// @Summary 获取当前用户信息
// @Description 获取当前登录用户的信息
// @Tags auth
// @Produce json
// @Success 200 {object} model.Response{data=model.UserInfoResponse}
// @Failure 401 {object} model.Response
// @Router /api/v1/auth/user [get]
func (h *AuthHandler) GetUserInfo(c *gin.Context) {
	// 从上下文获取当前用户ID
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "UNAUTHORIZED",
				Message: "User not authenticated",
			},
		})
		return
	}

	userInfo, err := h.authService.GetUserInfo(userID.(uint))
	if err != nil {
		if err == service.ErrUserNotFound {
			c.JSON(http.StatusNotFound, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "USER_NOT_FOUND",
					Message: "User not found",
				},
			})
			return
		}
		logger.Errorf("Get user info failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INTERNAL_ERROR",
				Message: "Failed to get user info",
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    userInfo,
		Message: "User info retrieved successfully",
	})
}
