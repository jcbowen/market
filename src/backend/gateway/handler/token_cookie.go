package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// refresh 令牌的浏览器端承载方式（F2）：
// refresh_token 有效期 7 天，比 access（2 小时）泄漏价值高得多，
// 原先由前端存在 JS 可读载体里（后台 js-cookie、C 端 uni.setStorageSync），
// 一处 XSS 即可长期冒用。现改为由后端下发 httpOnly cookie，JS 读不到，
// 登出时由后端下发过期 cookie 清除。

// refreshTokenCookieName C 端 refresh_token 的 httpOnly cookie 名
// 名称保持与历史一致，已登录的 H5 会话不因改名失效
const refreshTokenCookieName = "market_refresh"

// adminRefreshTokenCookieName 后台 refresh_token 的 httpOnly cookie 名
// 与 C 端分名（P1-11）：两端同源同域（同一网关主机），共用一枚 cookie 时
// 「后台登录会覆盖会员登录的令牌」「后台登出把会员会话一起踢下线」，
// 且会员端刷新会拿到后台令牌（跨端 session fixation）。
const adminRefreshTokenCookieName = "market_admin_refresh"

// refreshCookiePath cookie 作用路径：只覆盖 /api/v1 下的认证端点，
// 不给全站请求带上这枚令牌
const refreshCookiePath = "/api/v1"

// adminCookiePath 后台 cookie 作用路径：收窄到 /api/v1/admin，
// 后台令牌不必随会员接口请求一起上行
const adminCookiePath = "/api/v1/admin"

// adminRoutePrefix 后台路由前缀，用于按请求路径判定 cookie 归属端
const adminRoutePrefix = "/api/v1/admin"

// refreshCookieScope 按请求路径取本端应读写的 cookie 名与作用路径
// 登录/刷新/登出三处都走同一判定，避免「下发用 A 名、清除用 B 名」的错配
//
// 参数：
//   - c: gin 上下文
//
// 返回：
//   - name: cookie 名（后台 market_admin_refresh / C 端 market_refresh）
//   - path: cookie 作用路径（分别为 /api/v1/admin 与 /api/v1）
//
// 使用示例：
//
//	name, path := refreshCookieScope(c)
func refreshCookieScope(c *gin.Context) (string, string) {
	if c != nil && c.Request != nil && strings.HasPrefix(c.Request.URL.Path, adminRoutePrefix) {
		return adminRefreshTokenCookieName, adminCookiePath
	}
	return refreshTokenCookieName, refreshCookiePath
}

// refreshCookieOptInHeader 前端声明「我要 cookie 承载」的请求头
// C 端三端共用同一套登录端点：H5 走 cookie，小程序/App 拿不到 httpOnly cookie
// （小程序不自管浏览器 cookie、App 端无同源概念），故由前端显式选择承载方式，
// 未带该头时后端照旧把 refresh_token 放响应体，保持旧行为不破坏兼容
const refreshCookieOptInHeader = "X-Refresh-Cookie"

// wantsRefreshCookie 判断本次登录/刷新请求是否要求以 httpOnly cookie 承载 refresh_token
//
// 参数：
//   - c: gin 上下文
//
// 返回：
//   - bool: true 表示下发 cookie 且响应体不再返回 refresh_token
func wantsRefreshCookie(c *gin.Context) bool {
	return strings.TrimSpace(c.GetHeader(refreshCookieOptInHeader)) == "1"
}

// requestIsHTTPS 判断本次请求最终是否走 HTTPS
// 口径与 baseURLOf 一致：先看反向代理透传的 X-Forwarded-Proto，再回看 TLS。
// 不能写死 Secure：HTTP 部署下带 Secure 的 cookie 会被浏览器丢弃，登录直接不可用
//
// 参数：
//   - c: gin 上下文
//
// 返回：
//   - bool: true 表示应下发 Secure 属性
func requestIsHTTPS(c *gin.Context) bool {
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		return strings.EqualFold(proto, "https")
	}
	return c.Request.TLS != nil
}

// setRefreshTokenCookie 下发 refresh_token 的 httpOnly cookie
// Secure 取决于本次请求是否为 HTTPS（见 requestIsHTTPS）；
// 非 HTTPS 时不下发 Secure，否则本地与 HTTP 部署直接失效
//
// 参数：
//   - c: gin 上下文
//   - token: 已签发的 refresh_token
//   - maxAgeSeconds: 存活秒数，取 refresh_token 的有效期
func setRefreshTokenCookie(c *gin.Context, token string, maxAgeSeconds int64) {
	if token == "" {
		return
	}
	name, path := refreshCookieScope(c)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, token, int(maxAgeSeconds), path, "", requestIsHTTPS(c), true)
}

// clearRefreshTokenCookie 清除本端 refresh_token cookie（登出用）
// 属性需与下发时一致（名、路径、Secure），否则浏览器不认这枚待删除的 cookie
//
// 参数：
//   - c: gin 上下文
func clearRefreshTokenCookie(c *gin.Context) {
	name, path := refreshCookieScope(c)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, "", -1, path, "", requestIsHTTPS(c), true)
}

// readRefreshTokenCookie 读取请求携带的 refresh_token cookie
// 按请求路径取本端 cookie：后台只认后台那枚，C 端只认 C 端那枚
//
// 参数：
//   - c: gin 上下文
//
// 返回：
//   - string: cookie 值，不存在返回空字符串
func readRefreshTokenCookie(c *gin.Context) string {
	name, _ := refreshCookieScope(c)
	v, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return v
}

// firstNonEmptyRefreshToken 按「cookie 优先、请求体兜底」取出 refresh_token
// cookie 优先：H5/后台场景下前端不再持有令牌，只能靠 cookie；
// 请求体兜底：小程序与 App 无 httpOnly cookie 语义，仍按旧方式提交
//
// 参数：
//   - c: gin 上下文
//   - bodyToken: 已从请求体解析出的 refresh_token（可为空）
//
// 返回：
//   - string: 最终使用的 refresh_token，两者皆无时返回空字符串
func firstNonEmptyRefreshToken(c *gin.Context, bodyToken string) string {
	if v := readRefreshTokenCookie(c); v != "" {
		return v
	}
	return bodyToken
}
