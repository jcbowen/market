package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/rpcmeta"
)

// RequestBaseMiddleware 请求基址注入中间件（全局）
// 把当前请求的 scheme://host 写入请求上下文，随 gRPC 出站拦截器透传给各服务，
// 供附件 URL 兜底拼装（未配置显式域名时按访问域名拼完整地址，仅响应层，不入库）
//
// 参数：无（全局中间件形态）
//
// 返回值：
//   - gin.HandlerFunc: 中间件处理函数
//
// 使用示例：
//
//	r.Use(RequestBaseMiddleware())
func RequestBaseMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := rpcmeta.WithRequestBase(c.Request.Context(), RequestBaseURL(c))
		if ctx != c.Request.Context() {
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	}
}

// RequestBaseURL 按当前请求推导部署域名基址
// 优先取反向代理透传的 X-Forwarded-Proto，兼容 HTTPS 部署场景；
// handler.site.go 的 requestBaseURL / brand.Cache 的 request 兜底来源共用本口径
//
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - string: 形如 https://host 的基址
func RequestBaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + c.Request.Host
}
