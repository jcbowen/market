package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
)

// RealIPMiddleware 把按可信代理规则解析出的客户端 IP 固化进上下文
// gin 的 ClientIP() 已按 SetTrustedProxies 过滤代理头，本中间件只做一件事：写入 appcontext.KeyRealIP，
// 供鉴权中间件构建 UserContext 与下游审计取用（此前该键从未被写入，UserContext.RealIP 恒为空串）
//
// 返回值：
//   - gin.HandlerFunc: 真实 IP 中间件，须置于限流与鉴权之前
func RealIPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(string(appcontext.KeyRealIP), c.ClientIP())
		c.Next()
	}
}
