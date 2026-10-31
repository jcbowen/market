package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/response"
)

/**
 * 限流器与闲置淘汰的离线单元测试（不连库不连网）
 *
 * 运行方式：go test ./gateway/middleware/ -run 'TestLimiter|TestRateLimit|TestUserRateLimit' -v
 *
 * 覆盖（对应 D2「limiters map 补 TTL 淘汰」）：
 *   - 桶额度用尽后拒绝放行，且随时间按 rate 回血
 *   - 闲置超过 limiterIdleTTL 的键被淘汰，活跃键保留
 *   - 淘汰不再整表清空：刚被限流的键不会因清扫恢复额度
 *   - IP 限流中间件超限返回 429 与 RateLimit 业务码
 *   - 用户限流中间件按用户 ID 分桶、未登录请求直接放行（C 端上传/下单路由的按人限流）
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

// doUserUpload 以指定用户身份打一次受用户限流保护的请求
// 参数：
//   - r: 已挂载身份注入 + UserRateLimitMiddleware 的路由
//   - uid: 登录用户 ID；<=0 表示未登录语境（不带身份头）
//
// 返回值：
//   - *httptest.ResponseRecorder: 响应记录器
func doUserUpload(r *gin.Engine, uid int64) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/upload/image", nil)
	if uid > 0 {
		req.Header.Set(testUIDHeader, strconv.FormatInt(uid, 10))
	}
	r.ServeHTTP(w, req)
	return w
}

// testUIDHeader 测试用身份头，由下方注入中间件写入 gin 上下文，模拟 AuthMiddleware 之后的语境
const testUIDHeader = "X-Test-Uid"

// TestUserRateLimitMiddlewareScopesByUser 用户限流按用户 ID 分桶且未登录不拦
// 背景：C 端 /upload/image 对所有登录用户开放（换头像必需），图片落盘是真实存储成本，
// 全局 IP 限流挡不住单账号低速刷盘，路由上必须挂按人限流
func TestUserRateLimitMiddlewareScopesByUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 身份注入必须先于限流：与生产「UserRateLimitMiddleware 置于 AuthMiddleware 之后」同一顺序
	r.Use(func(c *gin.Context) {
		if v := c.GetHeader(testUIDHeader); v != "" {
			uid, _ := strconv.ParseInt(v, 10, 64)
			c.Set(string(appcontext.KeyUserID), uid)
			c.Set(string(appcontext.KeyTokenType), "user")
		}
		c.Next()
	})
	// rate 取极小值：用例只验容量与分桶，不让时间回血干扰断言
	r.Use(UserRateLimitMiddleware(0.0001, 2))
	r.POST("/api/v1/user/upload/image", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	for i := 0; i < 2; i++ {
		if w := doUserUpload(r, 1001); w.Code != http.StatusOK {
			t.Fatalf("桶容量 2 内第 %d 次上传应放行, got %d body=%q", i+1, w.Code, w.Body.String())
		}
	}
	// 第 3 次：同一用户超限
	over := doUserUpload(r, 1001)
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("同一用户超出容量应 429, got %d body=%q", over.Code, over.Body.String())
	}
	if got := over.Header().Get("Retry-After"); got == "" {
		t.Error("429 必须带 Retry-After，否则前端只会原样重试")
	}
	var resp response.Response
	if err := json.Unmarshal(over.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析限流响应失败: %v body=%q", err, over.Body.String())
	}
	if resp.Code != errcode.RateLimit {
		t.Errorf("限流业务码应为 RateLimit: got=%d msg=%q", resp.Code, resp.Message)
	}
	// 换用户不受影响：限流按人分桶，攻击者无法用他人身份被拒来推断配额
	if w := doUserUpload(r, 2002); w.Code != http.StatusOK {
		t.Errorf("其他用户不应被该用户的桶拖累, got %d", w.Code)
	}
	// 未登录语境（缺 UserID）直接放行：本中间件只作用于 Auth 之后的按人限流，
	// 身份缺失时不能拒绝，否则会把鉴权顺序问题伪装成限流故障
	if w := doUserUpload(r, 0); w.Code != http.StatusOK {
		t.Errorf("缺用户身份的请求应交由后续鉴权处理而非限流拒绝, got %d", w.Code)
	}
}
