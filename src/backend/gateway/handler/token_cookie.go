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

// refreshTokenCookieName refresh_token 的 httpOnly cookie 名
const refreshTokenCookieName = "market_refresh"

// refreshCookiePath cookie 作用路径：只覆盖 /api/v1 下的认证端点，
// 不给全站请求带上这枚令牌
const refreshCookiePath = "/api/v1"

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
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(refreshTokenCookieName, token, int(maxAgeSeconds), refreshCookiePath, "", requestIsHTTPS(c), true)
}

// clearRefreshTokenCookie 清除 refresh_token cookie（登出用）
// 属性需与下发时一致（路径、Secure），否则浏览器不认这枚待删除的 cookie
//
// 参数：
//   - c: gin 上下文
func clearRefreshTokenCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(refreshTokenCookieName, "", -1, refreshCookiePath, "", requestIsHTTPS(c), true)
}

// readRefreshTokenCookie 读取请求携带的 refresh_token cookie
//
// 参数：
//   - c: gin 上下文
//
// 返回：
//   - string: cookie 值，不存在返回空字符串
func readRefreshTokenCookie(c *gin.Context) string {
	v, err := c.Cookie(refreshTokenCookieName)
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
