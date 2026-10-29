package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/rpcmeta"
)

/**
 * 请求基址中间件的离线单元测试
 *
 * 覆盖：
 *   - RequestBaseURL：scheme 推导（默认 http / X-Forwarded-Proto 透传）
 *   - RequestBaseMiddleware：基址写入请求上下文，供 gRPC 出站拦截器透传
 */

// TestRequestBaseURL 验证按请求推导基址（含反代协议头）
func TestRequestBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/site-info", nil)
	if got := RequestBaseURL(c); got != "http://example.com" {
		t.Fatalf("默认应为 http scheme: %q", got)
	}

	c.Request.Header.Set("X-Forwarded-Proto", "https")
	if got := RequestBaseURL(c); got != "https://example.com" {
		t.Fatalf("应取 X-Forwarded-Proto: %q", got)
	}
}

// TestRequestBaseMiddleware 验证中间件把基址注入请求上下文
func TestRequestBaseMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestBaseMiddleware())
	var got string
	r.GET("/probe", func(c *gin.Context) {
		got = rpcmeta.FromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://10.0.0.2:9000/probe", nil))
	if got != "http://10.0.0.2:9000" {
		t.Fatalf("ctx 基址 = %q, want http://10.0.0.2:9000", got)
	}
}
