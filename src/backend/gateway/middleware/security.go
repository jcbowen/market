package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SecurityHeadersMiddleware 设置安全响应头中间件
// 返回：
//   - gin.HandlerFunc: 安全头中间件
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data: https:; connect-src 'self' https:;")
		c.Next()
	}
}

// BlockTraceMiddleware 拦截 TRACE 请求
// 返回：
//   - gin.HandlerFunc: TRACE 拦截中间件
func BlockTraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodTrace {
			c.AbortWithStatus(http.StatusMethodNotAllowed)
			return
		}
		c.Next()
	}
}
