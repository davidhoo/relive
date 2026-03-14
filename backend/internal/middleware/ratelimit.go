package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type ipLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// LoginRateLimit 返回登录专用限流中间件
// 双层限流：按 IP 每分钟 10 次（burst 5）+ 全局每分钟 60 次
func LoginRateLimit() gin.HandlerFunc {
	var (
		ipLimiters sync.Map
		// 全局限流：每秒 1 次，突发上限 60
		globalLimiter = rate.NewLimiter(rate.Every(time.Second), 60)
	)

	// 后台清理不活跃 IP 条目，防止内存泄漏
	go func() {
		for {
			time.Sleep(10 * time.Minute)
			ipLimiters.Range(func(key, value any) bool {
				entry := value.(*ipLimiterEntry)
				if time.Since(entry.lastSeen) > 10*time.Minute {
					ipLimiters.Delete(key)
				}
				return true
			})
		}
	}()

	return func(c *gin.Context) {
		// 全局限流检查
		if !globalLimiter.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "RATE_LIMITED",
					Message: "Too many login attempts, please try again later",
				},
			})
			return
		}

		// 按 IP 限流检查
		ip := c.ClientIP()
		val, _ := ipLimiters.LoadOrStore(ip, &ipLimiterEntry{
			// 每 6 秒 1 次 ≈ 每分钟 10 次，突发上限 5
			limiter:  rate.NewLimiter(rate.Every(6*time.Second), 5),
			lastSeen: time.Now(),
		})
		entry := val.(*ipLimiterEntry)
		entry.lastSeen = time.Now()

		if !entry.limiter.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, model.Response{
				Success: false,
				Error: &model.ErrorInfo{
					Code:    "RATE_LIMITED",
					Message: "Too many login attempts from this IP, please try again later",
				},
			})
			return
		}

		c.Next()
	}
}
