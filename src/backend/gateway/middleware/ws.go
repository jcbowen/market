package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/jwt"
)

// CookieAdminAccessToken 后台 access_token 的 httpOnly cookie 名
// 浏览器 WebSocket 握手无法自定义请求头，token 原经查询参数传递会落入
// 浏览器历史、网关与代理访问日志；现由后台登录/刷新时下发 httpOnly cookie 承载
// （与 refresh cookie 同域同路径 /api/v1/admin），常量定义于本包供
// handler（下发/清除）与本中间件（读取）两侧引用，避免口径漂移
const CookieAdminAccessToken = "market_admin_access"

// WSAuthMiddleware WebSocket 握手鉴权中间件
// 浏览器 WebSocket 握手无法自定义请求头（带不了 Authorization / X-Community-Id），
// 取 token 与社区声明映射到标准请求头后，复用既有 AuthMiddleware 全套校验
// （JWT 解析、TokenUse 强校验、admin 类型限定、UserContext 注入），
// 后续可继续挂 RequireCommunityAccess / RequirePermission，与普通 admin 接口同一套权限口径
//
// token 来源：httpOnly cookie（新版前端，不落 URL 与日志）优先，
// 查询参数兜底（旧前端缓存、POS 等未携带 cookie 声明的客户端）
//
// 参数：
//   - manager: JWT 管理器
//
// 返回值：
//   - gin.HandlerFunc: 鉴权中间件
//
// 使用示例：
//
//	admin.GET("/ws", gwMiddleware.WSAuthMiddleware(jwtManager),
//	    gwMiddleware.RequireCommunityAccess(), gwMiddleware.RequirePermission("order:view"), h.Handle)
func WSAuthMiddleware(manager *jwt.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			if t := firstNonEmptyAccessToken(c); t != "" {
				c.Request.Header.Set("Authorization", "Bearer "+t)
			}
		}
		if c.GetHeader(HeaderCommunityID) == "" {
			if id := c.Query("community_id"); id != "" {
				c.Request.Header.Set(HeaderCommunityID, id)
			}
		}
		AuthMiddleware(manager, jwt.TokenTypeAdmin)(c)
	}
}

// firstNonEmptyAccessToken 按「cookie 优先、查询参数兜底」取出 WS 握手 access_token
// cookie 优先：新版后台登录后由 httpOnly cookie 承载，token 不再出现在握手 URL
// （URL 会进浏览器历史与网关/代理访问日志）；查询参数兜底：旧前端缓存、POS
// 等未携带 cookie 的客户端保持原握手方式可用
//
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - string: access_token，两者皆无时返回空字符串
func firstNonEmptyAccessToken(c *gin.Context) string {
	if v, err := c.Cookie(CookieAdminAccessToken); err == nil && v != "" {
		return v
	}
	return c.Query("access_token")
}
