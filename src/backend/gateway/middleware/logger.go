package middleware

import (
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
)

// sensitiveQueryKeys 访问日志里需要脱敏的查询参数名（凭据类，小写比较）
// WS 握手与旧客户端兜底仍支持 ?access_token=，支付/登录回跳也会带 code/state，
// 这些值一旦原样落进访问日志就等于把可用令牌写进磁盘与日志采集链路
var sensitiveQueryKeys = map[string]struct{}{
	"access_token":       {},
	"refresh_token":      {},
	"token":              {},
	"password":           {},
	"captcha":            {},
	"code":               {},
	"auth_code":          {},
	"authorization_code": {},
	"signature":          {},
	"sign":               {},
}

// maskSensitiveQuery 把查询串中凭据类参数的值替换为 ***，其余参数原样保留
// 不借用 url.ParseQuery + Encode：那会重排参数并改写未编码字符，反而降低日志可读性
// 参数:
//   - raw: 原始查询串（不含前导 ?）
//
// 返回值:
//   - string: 脱敏后的查询串
//
// 使用示例:
//
//	maskSensitiveQuery("page=1&access_token=abc") // "page=1&access_token=***"
func maskSensitiveQuery(raw string) string {
	if raw == "" {
		return raw
	}
	pairs := strings.Split(raw, "&")
	for i, pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		if !found || value == "" {
			continue
		}
		if _, ok := sensitiveQueryKeys[strings.ToLower(key)]; ok {
			pairs[i] = key + "=***"
		}
	}
	return strings.Join(pairs, "&")
}

// RequestLoggerMiddleware 请求日志中间件
// 记录请求方法、路径、客户端 IP、状态码、耗时及 trace_id；
// 查询串按参数名脱敏，凭据类参数只保留名字
// 返回：
//   - gin.HandlerFunc: 日志中间件
func RequestLoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := maskSensitiveQuery(c.Request.URL.RawQuery)
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
