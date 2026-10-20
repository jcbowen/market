package main

import (
	"bytes"
	"context"
	"html"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/gateway/brand"
)

// brandHeadAnchor 前端 HTML 入口中的品牌注入锚点
// 用 meta 标签而非注释承载：vite 构建不会处理 meta 的 name 属性，注释则有被压缩工具剥离的风险
const brandHeadAnchor = `<meta name="brand-head" />`

// titlePattern 匹配 HTML 入口的 <title> 元素
// 用 [^<] 限定标题正文：模板注释里出现过字面量 <title>，若允许跨标签匹配会把注释尾部
// 与紧随其后的注入锚点一并吞掉，导致锚点丢失、品牌标签无处可写
var titlePattern = regexp.MustCompile(`<title>[^<]*</title>`)

// htmlEntry 一套前端（后台或 H5）的 HTML 注入目标
type htmlEntry struct {
	prefix string // 路由前缀：/b 或 /s
	dir    string // 静态资源目录
	// admin 是否为后台入口：后台禁止被搜索引擎收录，标题带「后台管理」后缀
	admin bool
	// social 是否输出社交分享 og 标签（C 端入口）
	social bool
}

// htmlTemplate 已读取并缓存的 HTML 模板
type htmlTemplate struct {
	modTime  time.Time
	size     int64
	raw      []byte
	rendered map[string][]byte
}

// htmlInjector HTML 品牌注入中间件
// 响应前端 HTML 入口时，把站点设置（名称/logo/favicon/SEO）写进服务端返回的文档，
// 使浏览器首屏与搜索引擎爬虫读到同一份品牌信息；配置取自内存快照，无逐请求数据库访问
type htmlInjector struct {
	entries []htmlEntry
	mu      sync.Mutex
	cache   map[string]*htmlTemplate
}

// newHTMLInjector 创建 HTML 品牌注入器
//
// 参数：
//   - entries: 需要注入的前端入口
//
// 返回值：
//   - *htmlInjector: 注入器
func newHTMLInjector(entries []htmlEntry) *htmlInjector {
	return &htmlInjector{entries: entries, cache: make(map[string]*htmlTemplate)}
}

// middleware gin 中间件入口
// 仅命中 HTML 入口路径时介入，其余请求（静态资源与 API）原样放行
//
// 返回值：
//   - gin.HandlerFunc: 中间件处理函数
func (i *htmlInjector) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		for _, e := range i.entries {
			if !isHTMLEntry(p, e.prefix) {
				continue
			}
			if e.admin {
				c.Header("X-Robots-Tag", "noindex, nofollow")
			}
			tpl, err := i.load(targetHTMLFile(p, e))
			if err != nil {
				// 文件不存在或不可读：交回静态资源处理器与 NoRoute 兜底，行为与注入前一致
				c.Next()
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", i.render(tpl, c.Request.Context(), e))
			c.Abort()
			return
		}
		c.Next()
	}
}

// isHTMLEntry 判断请求路径是否属于该前缀下的 HTML 入口
// 带扩展名的路径仅 .html 介入；无扩展名（目录索引与 SPA 深链）由入口页承接
//
// 参数：
//   - p: 请求路径
//   - prefix: 路由前缀
//
// 返回值：
//   - bool: 是否需要注入
func isHTMLEntry(p, prefix string) bool {
	if p == prefix {
		return true
	}
	if !strings.HasPrefix(p, prefix+"/") {
		return false
	}
	base := p[strings.LastIndex(p, "/")+1:]
	if base == "" {
		return true
	}
	dot := strings.LastIndex(base, ".")
	return dot < 0 || base[dot:] == ".html"
}

// targetHTMLFile 解析请求路径对应的 HTML 文件绝对路径
//
// 参数：
//   - p: 请求路径
//   - e: 前端入口配置
//
// 返回值：
//   - string: 文件路径
func targetHTMLFile(p string, e htmlEntry) string {
	rest := strings.TrimPrefix(p, e.prefix+"/")
	if rest == "" || !strings.HasSuffix(rest, ".html") {
		return filepath.Join(e.dir, "index.html")
	}
	return filepath.Join(e.dir, filepath.FromSlash(rest))
}

// load 读取并缓存 HTML 模板原文
// 按 mtime 与文件大小失效，前端重新构建后无需重启网关
//
// 参数：
//   - file: 模板文件路径
//
// 返回值：
//   - *htmlTemplate: 缓存条目
//   - error: 读取失败时返回错误
func (i *htmlInjector) load(file string) (*htmlTemplate, error) {
	stat, err := os.Stat(file)
	if err != nil || stat.IsDir() {
		return nil, os.ErrNotExist
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	if tpl, ok := i.cache[file]; ok && tpl.modTime.Equal(stat.ModTime()) && tpl.size == stat.Size() {
		return tpl, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	tpl := &htmlTemplate{modTime: stat.ModTime(), size: stat.Size(), raw: raw, rendered: make(map[string][]byte)}
	i.cache[file] = tpl
	return tpl, nil
}

// render 用当前品牌快照渲染模板
// 渲染结果按「模板 mtime + 配置版本」缓存，两者未变时逐请求不做字符串替换
//
// 参数：
//   - tpl: 模板缓存条目
//   - ctx: 请求上下文
//   - e: 前端入口配置
//
// 返回值：
//   - []byte: 渲染后的 HTML
func (i *htmlInjector) render(tpl *htmlTemplate, ctx context.Context, e htmlEntry) []byte {
	snap := brand.Get(ctx)
	key := strconv.FormatInt(tpl.modTime.UnixNano(), 10) + "|" + strconv.FormatInt(snap.Version, 10)

	i.mu.Lock()
	if out, ok := tpl.rendered[key]; ok {
		i.mu.Unlock()
		return out
	}
	i.mu.Unlock()

	out := renderHTML(tpl.raw, snap, e)
	i.mu.Lock()
	tpl.rendered[key] = out
	i.mu.Unlock()
	return out
}

// renderHTML 执行品牌注入
// 未配置站点名称时保留模板原有标题，避免首屏标题变空
//
// 参数：
//   - raw: 模板原文
//   - snap: 站点设置快照
//   - e: 前端入口配置
//
// 返回值：
//   - []byte: 注入后的 HTML
func renderHTML(raw []byte, snap brand.Snapshot, e htmlEntry) []byte {
	out := raw
	if snap.SiteName != "" {
		title := "<title>" + html.EscapeString(pageTitle(snap.SiteName, e)) + "</title>"
		// ReplaceAll 的替换串会展开 $1 这类组引用，站点名含 $ 时必须转义为 $$
		out = titlePattern.ReplaceAll(out, []byte(strings.ReplaceAll(title, "$", "$$")))
	}
	return bytes.Replace(out, []byte(brandHeadAnchor), []byte(brandHeadTags(snap, e)), 1)
}

// pageTitle 组装页面标题，后台带「后台管理」与历史口径一致
//
// 参数：
//   - siteName: 站点名称
//   - e: 前端入口配置
//
// 返回值：
//   - string: 页面标题
func pageTitle(siteName string, e htmlEntry) string {
	if e.admin {
		return siteName + " - 后台管理"
	}
	return siteName
}

// brandHeadTags 生成品牌相关 link 与 meta 标签，仅输出已配置项
//
// 参数：
//   - snap: 站点设置快照
//   - e: 前端入口配置
//
// 返回值：
//   - string: 标签串，可为空
func brandHeadTags(snap brand.Snapshot, e htmlEntry) string {
	var b strings.Builder
	if favicon := absoluteURL(snap.FaviconURL, snap.BaseURL); favicon != "" {
		// 版本号参与查询串，换图标后浏览器立即取新，不受长缓存影响
		writeTag(&b, `<link rel="icon" href="`, withVersion(favicon, snap.Version), `" />`)
	}
	if logo := absoluteURL(snap.LogoURL, snap.BaseURL); logo != "" {
		writeTag(&b, `<link rel="apple-touch-icon" href="`, logo, `" />`)
	}
	if snap.SEODescription != "" {
		writeTag(&b, `<meta name="description" content="`, snap.SEODescription, `" />`)
	}
	if snap.SEOKeywords != "" {
		writeTag(&b, `<meta name="keywords" content="`, snap.SEOKeywords, `" />`)
	}
	if e.social {
		writeTag(&b, `<meta property="og:site_name" content="`, snap.SiteName, `" />`)
		writeTag(&b, `<meta property="og:description" content="`, snap.SEODescription, `" />`)
		writeTag(&b, `<meta property="og:image" content="`, absoluteURL(snap.LogoURL, snap.BaseURL), `" />`)
		writeTag(&b, `<meta property="og:url" content="`, snap.BaseURL, `" />`)
	}
	return b.String()
}

// writeTag 输出一个属性值已转义的标签
// 值为空时整体跳过，避免产出空 content 的噪音标签
//
// 参数：
//   - b: 输出缓冲
//   - head: 标签前半段（含属性名与引号）
//   - value: 属性值
//   - tail: 标签收尾
func writeTag(b *strings.Builder, head, value, tail string) {
	if value == "" {
		return
	}
	b.WriteString(head)
	b.WriteString(html.EscapeString(value))
	b.WriteString(tail)
}

// absoluteURL 把站内相对路径补全为绝对地址
// 已是绝对地址或站点域名缺失时原样返回
//
// 参数：
//   - raw: 配置中的资源地址
//   - base: 部署域名基址
//
// 返回值：
//   - string: 绝对地址，raw 为空时返回空串
func absoluteURL(raw, base string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || base == "" {
		return raw
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return base + raw
}

// withVersion 为 URL 追加版本查询串
//
// 参数：
//   - raw: 原始 URL
//   - version: 配置版本号
//
// 返回值：
//   - string: 带 v=version 的 URL
func withVersion(raw string, version int64) string {
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + "v=" + strconv.FormatInt(version, 10)
}

// registerHTMLInjection 注册 HTML 品牌注入中间件
// 必须在 r.Static 之前调用：中间件在路由前命中 HTML 入口，绕开 gin 通配路由与精确路由的冲突
//
// 参数：
//   - r: gin 引擎
//   - adminPath: 后台静态目录
//   - h5Path: H5 静态目录
func registerHTMLInjection(r *gin.Engine, adminPath, h5Path string) {
	r.Use(newHTMLInjector([]htmlEntry{
		{prefix: "/b", dir: adminPath, admin: true},
		{prefix: "/s", dir: h5Path, social: true},
	}).middleware())
	log.Printf("HTML 品牌注入已启用（/b、/s）")
}
