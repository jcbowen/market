package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// cspShared 两侧的公共 CSP 指令：资源默认同源，图片/字体允许 data: 与 https:
// （附件外链与 base64 占位图都需要），style-src 保留 'unsafe-inline'：
// naive-ui 与 uni-app 都在运行时注入 <style>，去掉会把后台与 C 端样式整片打回无样式
const cspShared = "default-src 'self'; img-src 'self' data: https:; font-src 'self' data: https:; connect-src 'self' https:; style-src 'self' 'unsafe-inline'"

// cspClient H5 入口页的 CSP
// 入口页（/s/app.html、/s/index.html 以及 SPA 深链兜底返回的 index.html）带构建期内联脚本
// （uni-app 视口探测、Frame 模式宽屏跳转），脚本内容随每次构建变化、无法预先固定 hash，
// 故保留 script-src 'unsafe-inline'；'unsafe-eval' 已实测可去：两端产物无 eval( 与 new Function( 调用
const cspClient = "script-src 'self' 'unsafe-inline'; frame-ancestors 'self'; " + cspShared

// cspConsole 后台与其余路径（含 API）的 CSP
// 后台入口只有外链 module 脚本、无内联脚本，内联与 eval 全部关闭
const cspConsole = "script-src 'self'; " + cspShared

// isClientEntry 判断是否为 C 端 H5 的静态入口路径
// 精确到 /s 与其子路径：/static 之类的同源前缀不能误判为 H5 入口
//
// 参数：
//   - p: 请求路径
//
// 返回值：
//   - bool: 是否属于 H5 入口
func isClientEntry(p string) bool {
	return p == "/s" || strings.HasPrefix(p, "/s/")
}

// SecurityHeadersMiddleware 设置安全响应头中间件
// 返回：
//   - gin.HandlerFunc: 安全头中间件
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		c.Header("Content-Security-Policy", cspConsole)
		if isClientEntry(c.Request.URL.Path) {
			c.Header("Content-Security-Policy", cspClient)
		}
		// app.html 需被同域 iframe 嵌入（PC 端模拟手机边框），允许 SAMEORIGIN；
		// index.html 是外框本身、不参与被嵌，沿用 DENY
		if strings.HasPrefix(c.Request.URL.Path, "/s/app.html") {
			c.Header("X-Frame-Options", "SAMEORIGIN")
		} else {
			c.Header("X-Frame-Options", "DENY")
		}
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
