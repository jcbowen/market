package middleware

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
)

// RequestLoggerMiddleware 请求日志中间件
// 记录请求方法、路径、客户端 IP、状态码、耗时及 trace_id
// 返回：
//   - gin.HandlerFunc: 日志中间件
func RequestLoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery
		traceID := c.GetString(string(appcontext.KeyTraceID))

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method

		if raw != "" {
			path = path + "?" + raw
		}

		log.Printf("[%s] %s %s %d %s trace_id=%s", method, path, clientIP, status, latency, traceID)
	}
}
