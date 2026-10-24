package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/jwt"
)

/**
 * WSAuthMiddleware 令牌映射（2026-10-24 握手令牌安全加固）离线用例：不依赖 MySQL/Redis/gRPC
 *
 * 背景：后台 WS 握手原把 access_token 明文放进查询参数，URL 落入浏览器历史与
 * 网关/代理访问日志。现由后台登录/刷新时下发 httpOnly cookie（market_admin_access），
 * 中间件按「cookie 优先、查询参数兜底」映射为标准 Authorization 头后走既有鉴权链。
 *
 * 运行方式：
 *   go test ./gateway/middleware/ -run TestWSAuth -v
 *
 * 覆盖：
 *   - cookie 与查询参数同时存在时 cookie 优先（两枚不同令牌可区分归属）；
 *   - 仅查询参数时兜底可用（旧前端缓存、POS 兼容）；
 *   - cookie 令牌走完整 AuthMiddleware 链可通过（admin 类型校验、UserContext 注入）；
 *   - 两者皆无时按未登录拒绝（11001）。
 */

// newWSTestContext 构造带 cookie 与查询参数的 WS 握手测试上下文
// 参数：
//   - cookieToken: market_admin_access cookie 值（空串表示不带该 cookie）
//   - queryToken: access_token 查询参数值（空串表示不带该参数）
//
// 返回值：
//   - *gin.Context: 待中间件处理的上下文
//   - *httptest.ResponseRecorder: 响应记录器
func newWSTestContext(cookieToken, queryToken string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/ws", nil)
	if cookieToken != "" {
		req.AddCookie(&http.Cookie{Name: CookieAdminAccessToken, Value: cookieToken})
	}
	if queryToken != "" {
		q := req.URL.Query()
		q.Set("access_token", queryToken)
		req.URL.RawQuery = q.Encode()
	}
	c.Request = req
	return c, w
}

// testWSJWTManager 构造离线可用的 JWT 管理器（HS256 本地签发/解析，无外部依赖）
// 返回值：
//   - *jwt.Manager: 测试用管理器
func testWSJWTManager() *jwt.Manager {
	return jwt.NewManager(jwt.Config{
		Secret:          "ws-test-secret",
		AccessTokenTTL:  time.Hour,
		RefreshTokenTTL: 24 * time.Hour,
	})
}

// adminAccessToken 签发一枚测试用 admin access token
// 参数：
//   - t: 测试句柄（签发失败时直接 Fatal）
//   - m: JWT 管理器
//
// 返回值：
//   - string: 可通过 AuthMiddleware(TokenTypeAdmin) 校验的令牌
func adminAccessToken(t *testing.T, m *jwt.Manager) string {
	t.Helper()
	token, _, err := m.GenerateAdminAccessToken(1001, "", "super_admin", 1, nil, nil, "tester", "测试员")
	if err != nil {
		t.Fatalf("签发测试令牌失败: %v", err)
	}
	return token
}

// runWSAuth 执行 WSAuthMiddleware 并在终 handler 记录注入结果
// 参数：
//   - m: JWT 管理器
//   - c: gin 上下文
//
// 返回值：
//   - *appcontext.UserContext: 鉴权链注入的用户上下文（被拒时为 nil）
func runWSAuth(m *jwt.Manager, c *gin.Context) *appcontext.UserContext {
	var uc *appcontext.UserContext
	WSAuthMiddleware(m)(c)
	if !c.IsAborted() {
		uc = appcontext.GetUserContext(c)
	}
	return uc
}

// TestWSAuthPrefersCookieOverQuery cookie 与查询参数同时在场时必须取 cookie
// 若照抄查询参数，攻击者可借日志/历史中的旧 URL 令牌覆盖 httpOnly cookie 里的新令牌
func TestWSAuthPrefersCookieOverQuery(t *testing.T) {
	m := testWSJWTManager()
	cookieTok := adminAccessToken(t, m)
	staleTok := "stale-token-from-url-log"

	c, _ := newWSTestContext(cookieTok, staleTok)
	if got := c.Request.Header.Get("Authorization"); got != "" {
		t.Fatalf("前置断言失败：中间件执行前不应有 Authorization 头")
	}
	runWSAuth(m, c)

	if got := c.Request.Header.Get("Authorization"); got != "Bearer "+cookieTok {
		t.Errorf("应优先取 cookie 令牌映射 Authorization，got=%q", got)
	}
}

// TestWSAuthFallsBackToQuery 仅查询参数时兜底映射（旧前端缓存、POS 兼容路径）
func TestWSAuthFallsBackToQuery(t *testing.T) {
	m := testWSJWTManager()
	tok := adminAccessToken(t, m)

	c, _ := newWSTestContext("", tok)
	runWSAuth(m, c)

	if got := c.Request.Header.Get("Authorization"); got != "Bearer "+tok {
		t.Errorf("无 cookie 时应回退查询参数映射 Authorization，got=%q", got)
	}
}

// TestWSAuthCookieTokenPassesAuthChain cookie 令牌应走通完整鉴权链并注入用户上下文
func TestWSAuthCookieTokenPassesAuthChain(t *testing.T) {
	m := testWSJWTManager()
	tok := adminAccessToken(t, m)

	c, _ := newWSTestContext(tok, "")
	if uc := runWSAuth(m, c); uc == nil || uc.UserID != 1001 || uc.TokenType != jwt.TokenTypeAdmin {
		t.Errorf("cookie 令牌应通过鉴权链并注入 admin 上下文，uc=%+v", uc)
	}
}

// TestWSAuthRejectsWhenNoToken cookie 与查询参数皆无时按未登录拒绝（业务码 11001）
func TestWSAuthRejectsWhenNoToken(t *testing.T) {
	m := testWSJWTManager()

	c, _ := newWSTestContext("", "")
	runWSAuth(m, c)

	if !c.IsAborted() {
		t.Error("无任何令牌应拒绝握手")
	}
}
