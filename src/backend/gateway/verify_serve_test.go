package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/gateway/verifyfs"
)

/**
 * 平台验证文件伪静态下发测试（离线，不连数据库）
 *
 * 运行方式：
 *   go test ./gateway/ -run TestServeVerifyFile -v
 *
 * 覆盖：
 *   - 根目录单段 .txt 命中：200、text/plain、nosniff、no-cache、内容原样
 *   - HEAD 请求命中且无 body
 *   - 未命中 / 子路径 / 非 GET/HEAD 落回 NoRoute 404
 */

// newVerifyTestRouter 用临时 verify 目录构造只挂 NoRoute 的 gin 引擎
//
// 参数：
//   - t: 测试上下文
//
// 返回值：
//   - *gin.Engine: 已注册 NoRoute 的路由
func newVerifyTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	t.Cleanup(func() { verifyFileDir = verifyfs.DefaultDir() })
	verifyFileDir = dir

	if err := verifyfs.Save(dir, "MP_verify_AAA", []byte("iWBHBXYQsZqiTenJ")); err != nil {
		t.Fatalf("夹具保存失败: %v", err)
	}
	if err := verifyfs.Save(dir, "pI0foeRR2F", []byte(`{"file_name":"pI0foeRR2F.txt","file_content":"x"}`)); err != nil {
		t.Fatalf("夹具保存失败: %v", err)
	}

	r := gin.New()
	r.NoRoute(func(c *gin.Context) {
		if serveVerifyFile(c) {
			return
		}
		c.String(http.StatusNotFound, "not-found")
	})
	return r
}

func TestServeVerifyFile(t *testing.T) {
	r := newVerifyTestRouter(t)

	t.Run("公众号验证文件命中", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/MP_verify_AAA.txt", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("期望 200，实际 %d", w.Code)
		}
		if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Fatalf("期望 text/plain; charset=utf-8，实际 %q", got)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("期望 nosniff，实际 %q", got)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("期望 no-cache，实际 %q", got)
		}
		if w.Body.String() != "iWBHBXYQsZqiTenJ" {
			t.Fatalf("内容原样下发失败: %q", w.Body.String())
		}
	})

	t.Run("小程序JSON校验文件命中", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/pI0foeRR2F.txt", nil))
		if w.Code != http.StatusOK || w.Body.String() != `{"file_name":"pI0foeRR2F.txt","file_content":"x"}` {
			t.Fatalf("JSON 校验文件下发失败: %d %q", w.Code, w.Body.String())
		}
	})

	t.Run("HEAD命中无body", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/MP_verify_AAA.txt", nil))
		if w.Code != http.StatusOK || w.Body.Len() != 0 {
			t.Fatalf("HEAD 期望 200 无 body，实际 %d %q", w.Code, w.Body.String())
		}
	})

	t.Run("未命中走404", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/not_exist.txt", nil))
		if w.Code != http.StatusNotFound || w.Body.String() != "not-found" {
			t.Fatalf("未命中期望 404，实际 %d %q", w.Code, w.Body.String())
		}
	})

	t.Run("子路径不命中", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/a/MP_verify_AAA.txt", nil))
		if w.Code != http.StatusNotFound || w.Body.String() != "not-found" {
			t.Fatalf("子路径期望 404，实际 %d %q", w.Code, w.Body.String())
		}
	})

	t.Run("POST不命中", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/MP_verify_AAA.txt", nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("POST 期望落回 404，实际 %d", w.Code)
		}
	})
}
