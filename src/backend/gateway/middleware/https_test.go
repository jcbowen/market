package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/errcode"
)

/**
 * RequireHTTPS 中间件离线单元测试（不连库不连网）
 *
 * 运行方式：go test ./gateway/middleware/ -run TestRequireHTTPS -v
 *
 * 覆盖：
 *   - 生产环境（APP_ENV=production）非 HTTPS 请求被拒绝，返回 11011
 *   - 生产环境 X-Forwarded-Proto=https：可信代理网段内直连放行（原生 TLS 路径 httptest 不可模拟，靠前者同口径）
 *   - 生产环境 X-Forwarded-Proto=https：非可信直连（网关端口可达的外部客户端伪造头）拒绝——兜底不被伪造绕过
 *   - 非生产环境一律放行（本地开发/内网 HTTP 部署不受影响）
 */

// newHTTPSProbe 构建挂载 RequireHTTPS 的最小 gin 引擎，next 标记是否走到业务 handler
//
// 参数：
//   - hit: 业务 handler 是否被执行的标记指针
//   - trustedProxies: 传给 RequireHTTPS 的可信代理网段
//
// 返回值：
//   - *gin.Engine: 测试用引擎
func newHTTPSProbe(hit *bool, trustedProxies []string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/probe", RequireHTTPS(trustedProxies), func(c *gin.Context) {
		*hit = true
		c.Status(http.StatusOK)
	})
	return r
}

// TestRequireHTTPSProductionBlocksPlainHTTP 生产环境拒绝非 HTTPS 明文请求
//
// 参数：
//   - t: 测试对象
func TestRequireHTTPSProductionBlocksPlainHTTP(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	var hit bool
	r := newHTTPSProbe(&hit, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/probe", nil))

	if hit {
		t.Error("生产环境明文 HTTP 请求不应放行到业务 handler")
	}
	// response.Error 统一经 HTTP 200 + JSON body 业务码返回
	if !strings.Contains(w.Body.String(), `"code":`+strconv.Itoa(errcode.HTTPSRequired)) {
		t.Errorf("拒绝响应应含业务码 %d，实际: %s", errcode.HTTPSRequired, w.Body.String())
	}
}

// TestRequireHTTPSProductionAllowsHTTPSFromTrustedProxy 生产环境放行可信代理网段内声明的 HTTPS
//
// 参数：
//   - t: 测试对象
func TestRequireHTTPSProductionAllowsHTTPSFromTrustedProxy(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	var hit bool
	// httptest.NewRequest 默认 RemoteAddr 为 192.0.2.1:1234，落在本用例配置的可信网段内
	r := newHTTPSProbe(&hit, []string{"192.0.2.0/24"})

	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !hit {
		t.Errorf("可信代理来源的 X-Forwarded-Proto=https 应放行，实际 HTTP %d: %s", w.Code, w.Body.String())
	}
}

// TestRequireHTTPSProductionRejectsForgedProtoFromUntrustedIP 生产环境拒绝非可信直连伪造的 HTTPS 声明
// 网关端口公网/内网直达时，客户端自带 X-Forwarded-Proto: https 不得绕过兜底
//
// 参数：
//   - t: 测试对象
func TestRequireHTTPSProductionRejectsForgedProtoFromUntrustedIP(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	var hit bool
	// 可信网段不含请求直连地址 198.51.100.7（模拟外部客户端直达网关）
	r := newHTTPSProbe(&hit, []string{"10.0.0.0/8"})

	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	req.RemoteAddr = "198.51.100.7:44321"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if hit {
		t.Error("非可信直连伪造 X-Forwarded-Proto=https 不应放行")
	}
	if !strings.Contains(w.Body.String(), `"code":`+strconv.Itoa(errcode.HTTPSRequired)) {
		t.Errorf("拒绝响应应含业务码 %d，实际: %s", errcode.HTTPSRequired, w.Body.String())
	}
}

// TestRequireHTTPSNonProductionAllowsPlainHTTP 非生产环境放行明文请求（本地开发零配置可用）
//
// 参数：
//   - t: 测试对象
func TestRequireHTTPSNonProductionAllowsPlainHTTP(t *testing.T) {
	t.Setenv("APP_ENV", "")
	var hit bool
	r := newHTTPSProbe(&hit, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/probe", nil))

	if !hit {
		t.Errorf("非生产环境明文请求应放行，实际 HTTP %d: %s", w.Code, w.Body.String())
	}
}
