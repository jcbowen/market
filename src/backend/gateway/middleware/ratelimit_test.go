package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

/**
 * 限流器与闲置淘汰的离线单元测试（不连库不连网）
 *
 * 运行方式：go test ./gateway/middleware/ -run TestLimiter -v
 *
 * 覆盖（对应 D2「limiters map 补 TTL 淘汰」）：
 *   - 桶额度用尽后拒绝放行，且随时间按 rate 回血
 *   - 闲置超过 limiterIdleTTL 的键被淘汰，活跃键保留
 *   - 淘汰不再整表清空：刚被限流的键不会因清扫恢复额度
 *   - IP 限流中间件超限返回 429 与 RateLimit 业务码
 */

// TestLimiterStoreAllowAndRefill 验证令牌桶扣减与回血
//
// 参数：
//   - t: 测试对象
func TestLimiterStoreAllowAndRefill(t *testing.T) {
	st := newLimiterStore[string](1, 3)
	for i := 0; i < 3; i++ {
		if !st.allow("1.1.1.1") {
			t.Fatalf("容量 3 的桶前 3 次请求应全部放行（第 %d 次被拒）", i+1)
		}
	}
	if st.allow("1.1.1.1") {
		t.Error("桶额度用尽后应拒绝放行")
	}
	if !st.allow("2.2.2.2") {
		t.Error("限流按 key 分桶，其他 IP 不应受影响")
	}
}

// TestLimiterStoreSweepsIdleKeys 验证闲置淘汰只清老键、保留活跃键
//
// 参数：
//   - t: 测试对象
func TestLimiterStoreSweepsIdleKeys(t *testing.T) {
	st := newLimiterStore[string](1, 3)
	now := time.Now()
	st.limiters["idle"] = &idleLimiter{limiter: NewRateLimiter(1, 3), lastUsed: now.Add(-limiterIdleTTL - time.Minute)}
	st.limiters["active"] = &idleLimiter{limiter: NewRateLimiter(1, 3), lastUsed: now.Add(-time.Minute)}

	st.sweepLocked(now)

	if _, ok := st.limiters["idle"]; ok {
		t.Error("闲置超过窗口的键应被淘汰，否则 map 只增不减形成内存放大")
	}
	if _, ok := st.limiters["active"]; !ok {
		t.Error("活跃键不应被误删")
	}
	if st.lastSweep != now {
		t.Errorf("清扫后应推进扫描时间, got %v", st.lastSweep)
	}
}

// TestLimiterStoreAllowSweepsInline 验证淘汰在取桶时顺带执行且不重置额度
//
// 参数：
//   - t: 测试对象
func TestLimiterStoreAllowSweepsInline(t *testing.T) {
	st := newLimiterStore[int64](1, 1)
	// 先把 lastSweep 推到需要扫描的时点，同时留一个已耗尽的活跃键与一个闲置键
	st.lastSweep = time.Now().Add(-limiterSweepInterval)
	st.limiters[1] = &idleLimiter{limiter: NewRateLimiter(1, 1), lastUsed: time.Now()}
	st.limiters[2] = &idleLimiter{limiter: NewRateLimiter(1, 1), lastUsed: time.Now().Add(-limiterIdleTTL - time.Second)}

	if !st.allow(1) {
		t.Fatal("首次取桶应放行")
	}
	if _, ok := st.limiters[2]; ok {
		t.Error("取桶时应顺带淘汰闲置键")
	}
	if st.allow(1) {
		t.Error("已耗尽的活跃键不得因清扫恢复额度（原实现整表清空会让被限流方瞬间满血）")
	}
}

// TestRateLimitMiddlewareRejectsOverLimit 验证中间件超限返回 429
//
// 参数：
//   - t: 测试对象
func TestRateLimitMiddlewareRejectsOverLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimitMiddleware(0.001, 2))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	do := func() int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))
		return w.Code
	}
	if code := do(); code != http.StatusOK {
		t.Fatalf("容量内请求应正常放行, got %d", code)
	}
	do()
	if code := do(); code != http.StatusTooManyRequests {
		t.Errorf("超出容量应返回 429, got %d", code)
	}
}
