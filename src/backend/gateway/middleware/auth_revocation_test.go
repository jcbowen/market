package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
)

/**
 * access 令牌吊销查验（C5）离线用例：不依赖 Redis/gRPC
 *
 * 背景：access 令牌有效期 2 小时且原先不带 jti，登出/停用只能等它自然过期，
 * 「登出」在服务端等于没发生。现 access 也带 jti，鉴权时逐请求查一次黑名单。
 * 本文件锁定中间件这一段的口径：命中即 11002、判定收到的正是令牌里的 jti、
 * 未注入检查器时跳过（单测与未启用黑名单的部署保持原行为）。
 *
 * 运行方式：
 *   go test ./gateway/middleware/ -run TestAuthRevocation -v
 *
 * 覆盖：
 *   - 检查器返回 true → 中止、不注入用户上下文、错误码 11002；
 *   - 检查器返回 false → 正常放行；
 *   - 检查器收到的 jti 等于令牌里的 jti（键错位等于黑名单永远查不到）；
 *   - 未注入检查器 → 跳过查验（不影响既有鉴权）；
 *   - 吊销查验排在用途校验之后：refresh 令牌仍先按「不得用于接口访问」被拒，不进黑名单查询。
 */

// revocationTestManager 构造离线可用的 JWT 管理器
//
// 返回值：
//   - *jwt.Manager: 测试用管理器
func revocationTestManager() *jwt.Manager {
	return jwt.NewManager(jwt.Config{
		Secret:          "c5-revocation-test-secret",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
	})
}

// runAuthWithToken 用给定令牌执行一次鉴权
//
// 参数：
//   - m: JWT 管理器
//   - token: Authorization 头携带的令牌
//
// 返回值：
//   - *gin.Context: 处理后的上下文
//   - *httptest.ResponseRecorder: 响应记录器
func runAuthWithToken(m *jwt.Manager, token string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/orders", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)
	AuthMiddleware(m, jwt.TokenTypeAdmin)(c)
	return c, w
}

// setCheckerForTest 注入检查器并登记恢复（避免污染同包其他用例）
//
// 参数：
//   - t: 测试对象
//   - check: 判定函数（nil 表示恢复未注入状态）
func setCheckerForTest(t *testing.T, check func(ctx context.Context, jti string) bool) {
	t.Helper()
	SetTokenRevocationChecker(check)
	t.Cleanup(func() { SetTokenRevocationChecker(nil) })
}

// TestAuthRevocationRejectsRevokedToken 命中黑名单的 access 令牌必须被拒
// 断言点包含「判定收到的 jti」：键错位时黑名单查询永远落空，登出等于没吊销
//
// 参数：
//   - t: 测试对象
func TestAuthRevocationRejectsRevokedToken(t *testing.T) {
	m := revocationTestManager()
	token, _, err := m.GenerateAdminAccessToken(1001, "", "super_admin", 1, nil, nil, "tester", "测试员")
	if err != nil {
		t.Fatalf("签发测试令牌失败: %v", err)
	}
	claims, err := m.ParseToken(token)
	if err != nil {
		t.Fatalf("解析测试令牌失败: %v", err)
	}
	if claims.ID == "" {
		t.Fatal("前置断言失败：access 令牌应携带 jti，否则黑名单无从定位")
	}

	var gotJTI string
	setCheckerForTest(t, func(_ context.Context, jti string) bool {
		gotJTI = jti
		return true
	})

	c, w := runAuthWithToken(m, token)
	if !c.IsAborted() {
		t.Error("已吊销的 access 令牌应被中止")
	}
	if uc := appcontext.GetUserContext(c); uc.UserID != 0 {
		t.Errorf("被拒请求不得注入用户上下文, uc=%+v", uc)
	}
	if got := respCode(t, w); got != errcode.InvalidToken {
		t.Errorf("错误码 = %d, 期望 %d（已吊销按无效令牌处理）", got, errcode.InvalidToken)
	}
	if gotJTI != claims.ID {
		t.Errorf("检查器收到的 jti = %q, 期望 %q", gotJTI, claims.ID)
	}
}

// TestAuthRevocationAllowsLiveToken 未命中黑名单时正常放行
//
// 参数：
//   - t: 测试对象
func TestAuthRevocationAllowsLiveToken(t *testing.T) {
	m := revocationTestManager()
	token, _, err := m.GenerateAdminAccessToken(1001, "", "super_admin", 1, nil, nil, "tester", "测试员")
	if err != nil {
		t.Fatalf("签发测试令牌失败: %v", err)
	}

	calls := 0
	setCheckerForTest(t, func(context.Context, string) bool {
		calls++
		return false
	})

	c, w := runAuthWithToken(m, token)
	if c.IsAborted() {
		t.Fatalf("未吊销的令牌不应被拒, body=%s", w.Body.String())
	}
	if uc := appcontext.GetUserContext(c); uc == nil || uc.UserID != 1001 {
		t.Errorf("应正常注入用户上下文, uc=%+v", uc)
	}
	if calls != 1 {
		t.Errorf("鉴权应查验黑名单一次, calls=%d", calls)
	}
}

// TestAuthRevocationSkippedWithoutChecker 未注入检查器时跳过查验（保持既有部署行为）
// 单测、以及未配置 Redis 黑名单的部署不因缺注入而全量 401
//
// 参数：
//   - t: 测试对象
func TestAuthRevocationSkippedWithoutChecker(t *testing.T) {
	m := revocationTestManager()
	token, _, err := m.GenerateAdminAccessToken(1001, "", "super_admin", 1, nil, nil, "tester", "测试员")
	if err != nil {
		t.Fatalf("签发测试令牌失败: %v", err)
	}
	setCheckerForTest(t, nil)

	c, w := runAuthWithToken(m, token)
	if c.IsAborted() {
		t.Errorf("未注入检查器时应跳过吊销查验, body=%s", w.Body.String())
	}
}

// TestAuthRevocationRunsAfterUseCheck refresh 令牌仍先按用途被拒，不进黑名单查询
// 顺序颠倒会让「拿 refresh 冒充 access」多走一次 Redis，且错误归因混乱
//
// 参数：
//   - t: 测试对象
func TestAuthRevocationRunsAfterUseCheck(t *testing.T) {
	m := revocationTestManager()
	refresh, _, err := m.GenerateRefreshToken(1001, jwt.TokenTypeAdmin)
	if err != nil {
		t.Fatalf("签发 refresh 令牌失败: %v", err)
	}

	calls := 0
	setCheckerForTest(t, func(context.Context, string) bool {
		calls++
		return false
	})

	c, w := runAuthWithToken(m, refresh)
	if !c.IsAborted() {
		t.Fatal("refresh 令牌不得用于接口访问")
	}
	if got := respCode(t, w); got != errcode.InvalidToken {
		t.Errorf("错误码 = %d, 期望 %d", got, errcode.InvalidToken)
	}
	if calls != 0 {
		t.Errorf("用途校验未通过时不应查黑名单, calls=%d", calls)
	}
}
