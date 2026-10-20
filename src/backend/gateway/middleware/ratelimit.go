package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/response"
)

// 限流器空闲淘汰参数
// 键空间无上限的 map 只增不减会造成内存放大（IP 键尤甚：爬虫与移动网络换 IP 极快），
// 故按最后使用时间淘汰，不再采用「定时整表清空」——那会让刚被限流的攻击流量瞬间恢复额度
const (
	// limiterIdleTTL 限流器闲置多久后可被淘汰
	limiterIdleTTL = 30 * time.Minute

	// limiterSweepInterval 两次淘汰扫描的最小间隔
	limiterSweepInterval = 5 * time.Minute
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

// limiterStore 按 key 分配令牌桶并淘汰闲置桶
// 淘汰在取桶时顺带执行（无后台协程），同一把锁内完成，避免与读写并发竞争
type limiterStore[K comparable] struct {
	mu        sync.Mutex
	limiters  map[K]*idleLimiter
	rate      float64
	capacity  float64
	lastSweep time.Time
}

// idleLimiter 带最后使用时间的令牌桶
type idleLimiter struct {
	limiter  *RateLimiter
	lastUsed time.Time
}

// newLimiterStore 构造限流桶容器
// 参数：
//   - rate: 每秒请求数
//   - capacity: 桶容量
//
// 返回值：
//   - *limiterStore[K]: 以 K 为键的限流容器
func newLimiterStore[K comparable](rate, capacity float64) *limiterStore[K] {
	return &limiterStore[K]{
		limiters:  make(map[K]*idleLimiter),
		rate:      rate,
		capacity:  capacity,
		lastSweep: time.Now(),
	}
}

// allow 取（或建）该 key 的令牌桶并尝试扣一个令牌
// 参数：
//   - key: 限流键（客户端 IP 或用户 ID）
//
// 返回值：
//   - bool: 是否放行
func (st *limiterStore[K]) allow(key K) bool {
	now := time.Now()

	st.mu.Lock()
	if now.Sub(st.lastSweep) >= limiterSweepInterval {
		st.sweepLocked(now)
	}
	entry, ok := st.limiters[key]
	if !ok {
		entry = &idleLimiter{limiter: NewRateLimiter(st.rate, st.capacity)}
		st.limiters[key] = entry
	}
	entry.lastUsed = now
	st.mu.Unlock()

	return entry.limiter.Allow()
}

// sweepLocked 淘汰闲置超过 limiterIdleTTL 的桶（调用方须持有 st.mu）
// 参数：
//   - now: 扫描基准时间
func (st *limiterStore[K]) sweepLocked(now time.Time) {
	for key, entry := range st.limiters {
		if now.Sub(entry.lastUsed) >= limiterIdleTTL {
			delete(st.limiters, key)
		}
	}
	st.lastSweep = now
}

// RateLimitMiddleware 构造基于客户端 IP 的限流中间件
// ClientIP 是否采信代理头由 `r.SetTrustedProxies` 决定（见 main.go 的 applyTrustedProxies），
// 未配置可信代理时只认直连地址，伪造 X-Forwarded-For 换 IP 绕限流的路径即被封死
// 参数：
//   - rate: 每秒请求数
//   - capacity: 桶容量
//
// 返回值：
//   - gin.HandlerFunc: 限流中间件
func RateLimitMiddleware(rate, capacity float64) gin.HandlerFunc {
	st := newLimiterStore[string](rate, capacity)

	return func(c *gin.Context) {
		if !st.allow(c.ClientIP()) {
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

// UserRateLimitMiddleware 构造基于用户 ID 的限流中间件（需置于 AuthMiddleware 之后）
// 防止同一用户高频调用敏感接口（如下单、退款申请）
// 参数：
//   - rate: 每秒请求数
//   - capacity: 桶容量
//
// 返回值：
//   - gin.HandlerFunc: 限流中间件
func UserRateLimitMiddleware(rate, capacity float64) gin.HandlerFunc {
	st := newLimiterStore[int64](rate, capacity)

	return func(c *gin.Context) {
		uc := appcontext.GetUserContext(c)
		if uc == nil || uc.UserID <= 0 {
			c.Next()
			return
		}

		if !st.allow(uc.UserID) {
			c.Header("Retry-After", "12")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, response.Response{
				Code:    errcode.RateLimit,
				Message: "操作过于频繁，请稍后再试",
			})
			return
		}
		c.Next()
	}
}
