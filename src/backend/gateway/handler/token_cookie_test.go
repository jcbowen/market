package handler

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

/**
 * refresh_token httpOnly cookie（F2）离线用例：只验证签发/读取/清除的属性与优先级，
 * 不依赖 MySQL/Redis/gRPC
 *
 * 运行方式：
 *   go test ./gateway/handler/ -run TestRefreshTokenCookie -v
 *
 * 覆盖：
 *   - 未带 X-Refresh-Cookie 请求头时不开启 cookie 承载（小程序/App 保持响应体下发）
 *   - cookie 属性：HttpOnly、SameSite=Lax、Path=/api/v1、Max-Age 为传入秒数
 *   - Secure 随请求协议：X-Forwarded-Proto=https 或 TLS 时置位，HTTP 部署不置位
 *   - 读取优先级：cookie 命中即用 cookie，cookie 缺失才回退请求体
 *   - 清除用负 Max-Age 且路径一致，保证浏览器认这枚待删 cookie
 *
 * 使用示例：
 *   c, w := newCookieTestContext("GET", "/", nil)
 *   setRefreshTokenCookie(c, "tok", 604800)
 *   if !strings.Contains(w.Header().Get("Set-Cookie"), "HttpOnly") { t.Fail() }
 */

// newCookieTestContext 构造一个用于 cookie 断言的 gin 上下文与记录器
// 参数：
//   - method: 请求方法
//   - path: 请求路径
//   - headers: 附加请求头（键值对，可为 nil）
//
// 返回：
//   - *gin.Context: 处理器内可用的上下文
//   - *httptest.ResponseRecorder: 用于读取响应头
func newCookieTestContext(method, path string, headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c, w
}

// TestRefreshTokenCookieOptIn 验证只有显式带 X-Refresh-Cookie: 1 才启用 cookie 承载
func TestRefreshTokenCookieOptIn(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"未带头", "", false},
		{"值为 1", "1", true},
		{"带空格的 1", " 1", true},
		{"值为 0 不算开启", "0", false},
		{"脏值不算开启", "yes", false},
	}
	for _, tc := range cases {
		c, _ := newCookieTestContext(http.MethodPost, "/api/v1/auth/logout",
			map[string]string{refreshCookieOptInHeader: tc.header})
		if got := wantsRefreshCookie(c); got != tc.want {
			t.Errorf("%s: wantsRefreshCookie=%v, 期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestSetRefreshTokenCookieAttributes 验证 cookie 的安全属性与存活时长
// 两端分名分路径（P1-11）：同源部署下后台与会员各用一枚 cookie，互不覆盖、互不清除
func TestSetRefreshTokenCookieAttributes(t *testing.T) {
	cases := []struct {
		scope    string
		path     string
		wantName string
		wantPath string
	}{
		{"后台", "/api/v1/admin/auth/login", adminRefreshTokenCookieName, adminCookiePath},
		{"C 端", "/api/v1/auth/login/phone", refreshTokenCookieName, refreshCookiePath},
	}
	for _, tc := range cases {
		c, w := newCookieTestContext(http.MethodPost, tc.path, nil)
		setRefreshTokenCookie(c, "refresh-token-value", int64(7*24*time.Hour/time.Second))

		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("%s: 期望下发 1 枚 cookie，实得 %d 枚（Set-Cookie=%q）", tc.scope, len(cookies), w.Header().Values("Set-Cookie"))
		}
		got := cookies[0]
		if got.Name != tc.wantName {
			t.Errorf("%s: cookie 名 = %q, 期望 %q", tc.scope, got.Name, tc.wantName)
		}
		if !got.HttpOnly {
			t.Error("cookie 缺少 HttpOnly，JS 仍能读到 7 天有效期的长期凭证")
		}
		if got.Secure {
			t.Error("HTTP 请求下不应置 Secure：浏览器会丢弃该 cookie，登录直接不可用")
		}
		if got.Path != tc.wantPath {
			t.Errorf("%s: cookie Path = %q, 期望 %q（不应全站请求携带令牌）", tc.scope, got.Path, tc.wantPath)
		}
		if got.MaxAge <= 0 {
			t.Errorf("cookie Max-Age = %d, 期望正数", got.MaxAge)
		}
		if got.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie SameSite = %v, 期望 Lax", got.SameSite)
		}
		if got.Value != "refresh-token-value" {
			t.Errorf("cookie 值 = %q, 期望原样透传", got.Value)
		}
	}
}

// TestRefreshCookieScopeIsolation 验证两端 cookie 互不串用（P1-11）
// 后台令牌只随 /api/v1/admin 上行；C 端请求读不到后台那枚，反之亦然，
// 否则后台登出会清掉会员会话、或会员端刷新时拿到后台令牌
func TestRefreshCookieScopeIsolation(t *testing.T) {
	// 后台登录下发的令牌
	adminCtx, adminW := newCookieTestContext(http.MethodPost, "/api/v1/admin/auth/login", nil)
	setRefreshTokenCookie(adminCtx, "admin-token", 60)
	adminCookies := adminW.Result().Cookies()
	if len(adminCookies) != 1 {
		t.Fatalf("后台登录应下发 1 枚 cookie，实得 %d 枚", len(adminCookies))
	}

	// 该枚后台令牌拿到 C 端刷新请求里，不应被读到
	crossReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	crossReq.AddCookie(adminCookies[0])
	crossCtx, _ := newCookieTestContext(http.MethodPost, "/api/v1/auth/refresh", nil)
	crossCtx.Request = crossReq
	if got := readRefreshTokenCookie(crossCtx); got != "" {
		t.Errorf("C 端不应读到后台 cookie，实得 %q", got)
	}

	// C 端登录下发的令牌
	userCtx, userW := newCookieTestContext(http.MethodPost, "/api/v1/auth/login/phone", nil)
	setRefreshTokenCookie(userCtx, "user-token", 60)
	userCookies := userW.Result().Cookies()
	if len(userCookies) != 1 {
		t.Fatalf("C 端登录应下发 1 枚 cookie，实得 %d 枚", len(userCookies))
	}
	// C 端 cookie 的 Path=/api/v1 是前缀匹配，浏览器请求后台接口时确实会一并带上，
	// 后台必须只认自己那枚
	bothReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/refresh", nil)
	bothReq.AddCookie(adminCookies[0])
	bothReq.AddCookie(userCookies[0])
	bothCtx, _ := newCookieTestContext(http.MethodPost, "/api/v1/admin/auth/refresh", nil)
	bothCtx.Request = bothReq
	if got := readRefreshTokenCookie(bothCtx); got != "admin-token" {
		t.Errorf("后台请求应只认后台令牌，实得 %q", got)
	}
}

// TestSetRefreshTokenCookieEmptyToken 验证空令牌不下发，避免写入一枚无意义的空 cookie
func TestSetRefreshTokenCookieEmptyToken(t *testing.T) {
	c, w := newCookieTestContext(http.MethodPost, "/api/v1/auth/refresh", nil)
	setRefreshTokenCookie(c, "", 60)
	if len(w.Result().Cookies()) != 0 {
		t.Error("空令牌不应下发 cookie")
	}
}

// TestRefreshTokenCookieSecureFollowsScheme 验证 Secure 随实际协议变化
func TestRefreshTokenCookieSecureFollowsScheme(t *testing.T) {
	// 反代透传 https
	c, w := newCookieTestContext(http.MethodPost, "/api/v1/auth/login/phone",
		map[string]string{"X-Forwarded-Proto": "https"})
	setRefreshTokenCookie(c, "tok", 60)
	if !w.Result().Cookies()[0].Secure {
		t.Error("X-Forwarded-Proto=https 时应置 Secure")
	}

	// 反代透传 http（大小写不敏感）
	c2, w2 := newCookieTestContext(http.MethodPost, "/api/v1/auth/login/phone",
		map[string]string{"X-Forwarded-Proto": "HTTP"})
	setRefreshTokenCookie(c2, "tok", 60)
	if requestIsHTTPS(c2) {
		t.Error("X-Forwarded-Proto=HTTP 时不应判定为 HTTPS")
	}
	if w2.Result().Cookies()[0].Secure {
		t.Error("HTTP 部署不应置 Secure")
	}

	// 无代理头时看 TLS
	c3, _ := newCookieTestContext(http.MethodPost, "/api/v1/auth/login/phone", nil)
	if requestIsHTTPS(c3) {
		t.Error("无 TLS 且无代理头时不应判定为 HTTPS")
	}
	c3.Request.TLS = &tls.ConnectionState{}
	if !requestIsHTTPS(c3) {
		t.Error("TLS 直连应判定为 HTTPS")
	}
}

// TestReadRefreshTokenCookiePrecedence 验证「cookie 优先、请求体兜底」的取值口径
func TestReadRefreshTokenCookiePrecedence(t *testing.T) {
	c, _ := newCookieTestContext(http.MethodPost, "/api/v1/auth/refresh", nil)
	if got := firstNonEmptyRefreshToken(c, "from-body"); got != "from-body" {
		t.Errorf("无 cookie 时应回退请求体，实得 %q", got)
	}

	c2, w := newCookieTestContext(http.MethodPost, "/api/v1/auth/refresh", nil)
	setRefreshTokenCookie(c2, "from-cookie", 60)
	// 用刚下发的 cookie 值构造下一次带 cookie 的请求
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	for _, ck := range w.Result().Cookies() {
		req.AddCookie(ck)
	}
	c2.Request = req
	if got := readRefreshTokenCookie(c2); got != "from-cookie" {
		t.Fatalf("cookie 读取失败，实得 %q", got)
	}
	if got := firstNonEmptyRefreshToken(c2, "from-body"); got != "from-cookie" {
		t.Errorf("cookie 命中时应优先于请求体，实得 %q", got)
	}

	c3, _ := newCookieTestContext(http.MethodPost, "/api/v1/auth/refresh", nil)
	if got := firstNonEmptyRefreshToken(c3, ""); got != "" {
		t.Errorf("两者皆空应返回空串，实得 %q", got)
	}
}

// TestClearRefreshTokenCookie 验证登出清除用的属性与下发一致
func TestClearRefreshTokenCookie(t *testing.T) {
	c, w := newCookieTestContext(http.MethodPost, "/api/v1/auth/logout", nil)
	clearRefreshTokenCookie(c)
	cs := w.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("登出应下发 1 枚清除 cookie，实得 %d 枚", len(cs))
	}
	if cs[0].MaxAge >= 0 {
		t.Errorf("清除 cookie 的 Max-Age = %d, 期望负数", cs[0].MaxAge)
	}
	if cs[0].Path != refreshCookiePath {
		t.Errorf("清除 cookie 的 Path = %q, 期望与下发一致（%q）", cs[0].Path, refreshCookiePath)
	}
	if !cs[0].HttpOnly {
		t.Error("清除 cookie 同样需要 HttpOnly，否则属性不匹配删不掉")
	}
}
