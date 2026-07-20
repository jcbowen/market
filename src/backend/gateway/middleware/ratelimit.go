package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/response"
)

// RateLimiter 基于内存的令牌桶限流器
type RateLimiter struct {
	rate       float64
	capacity   float64
	tokens     float64
	lastUpdate time.Time
	mu         sync.Mutex
}

// NewRateLimiter 创建令牌桶限流器
// 参数：
//   - rate: 每秒放入令牌数
//   - capacity: 桶容量
//
// 返回值：
//   - *RateLimiter: 限流器实例
func NewRateLimiter(rate, capacity float64) *RateLimiter {
	return &RateLimiter{
		rate:       rate,
		capacity:   capacity,
		tokens:     capacity,
		lastUpdate: time.Now(),
	}
}

// Allow 尝试获取一个令牌
// 返回值：
//   - bool: 是否允许通过
func (l *RateLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(l.lastUpdate).Seconds()
	l.tokens += elapsed * l.rate
	if l.tokens > l.capacity {
		l.tokens = l.capacity
	}
	l.lastUpdate = now

	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}

// RateLimitMiddleware 构造基于客户端 IP 的限流中间件
// 参数：
//   - rate: 每秒请求数
//   - capacity: 桶容量
//
// 返回值：
//   - gin.HandlerFunc: 限流中间件
func RateLimitMiddleware(rate, capacity float64) gin.HandlerFunc {
	limiters := make(map[string]*RateLimiter)
	var mu sync.Mutex

	return func(c *gin.Context) {
		ip := c.ClientIP()
		mu.Lock()
		l, ok := limiters[ip]
		if !ok {
			l = NewRateLimiter(rate, capacity)
			limiters[ip] = l
		}
		mu.Unlock()

		if !l.Allow() {
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, response.Response{
				Code:    errcode.RateLimit,
				Message: errcode.GetMessage(errcode.RateLimit),
			})
			return
		}
		c.Next()
	}
}
