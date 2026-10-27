package service

import (
	"bytes"
	"golang.org/x/net/html"
	"slices"
	"strings"
)

// 商品富文本详情白名单消毒器
// 参考 user 服务页脚消毒器（site.go）的「白名单标签重建 + 文本转义」模式，
// 面向商品详情场景扩展：支持图文混排（img）与列表/标题，防存储型 XSS。
// 与页脚版的差异：允许 img（src 限站内相对路径与 http(s) 外链）、a（新窗口打开）、
// 列表与标题标签；style/class 等展示属性一律剔除，防 CSS 注入

// contentTagAttrs 允许保留的标签及其白名单属性
var contentTagAttrs = map[string][]string{
	"p":      {},
	"br":     {},
	"b":      {},
	"strong": {},
	"i":      {},
	"em":     {},
	"u":      {},
	"span":   {},
	"div":    {},
	"h1":     {}, "h2": {}, "h3": {}, "h4": {}, "h5": {}, "h6": {},
	"ul": {}, "ol": {}, "li": {},
	"img": {"src", "alt", "width", "height"},
	"a":   {"href", "title"},
}

// contentVoidTags 无需闭合标签的自闭合元素
var contentVoidTags = map[string]bool{"br": true, "img": true}

// contentDropTags 连同子内容整体丢弃的危险标签（其文本内容也是攻击载荷，不可解包保留）
var contentDropTags = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true,
	"embed": true, "noscript": true, "template": true, "svg": true, "math": true,
	"form": true, "input": true, "button": true, "textarea": true, "select": true,
}

// SanitizeProductContentHTML 按白名单清洗商品富文本详情
// 白名单标签重建并仅保留安全属性；其余标签解包（保留子内容）；
// 危险标签连子树整体丢弃；文本一律 HTML 转义，防存储型 XSS
//
// 参数：
//   - raw: 管理端富文本编辑器产出的原始 HTML
//
// 返回值：
//   - string: 消毒后的 HTML，输入为空时返回空串
func SanitizeProductContentHTML(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	writeContentNodes(&buf, doc)
	return strings.TrimSpace(buf.String())
}

// writeContentNodes 递归输出经白名单过滤的节点树
func writeContentNodes(buf *bytes.Buffer, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			buf.WriteString(html.EscapeString(c.Data))
		case html.ElementNode:
			if contentDropTags[c.Data] {
				continue
			}
			attrs, ok := contentSafeAttrs(c)
			if !ok {
				// 非白名单标签：解包，仅保留子内容
				writeContentNodes(buf, c)
				continue
			}
			buf.WriteString("<" + c.Data)
			for _, a := range attrs {
				buf.WriteString(" " + a.Key + `="` + html.EscapeString(a.Val) + `"`)
			}
			buf.WriteString(">")
			writeContentNodes(buf, c)
			if !contentVoidTags[c.Data] {
				buf.WriteString("</" + c.Data + ">")
			}
		default:
			writeContentNodes(buf, c)
		}
	}
}

// contentSafeAttrs 提取节点白名单内的安全属性
// img src 仅放行站内相对路径（/uploads/...，排除 // 开头的协议相对地址）与 http(s) 外链，
// javascript: 等危险协议静默剔除该属性（剔除后 img 无 src，浏览器不渲染，内容仍在）；
// a 链接统一追加 target="_blank"、rel="noopener noreferrer"，href 不合规时剔除该属性
//
// 参数：
//   - n: 元素节点
//
// 返回值：
//   - []html.Attribute: 保留的属性列表
//   - bool: 标签是否在白名单（false 表示解包处理）
func contentSafeAttrs(n *html.Node) ([]html.Attribute, bool) {
	allowed, ok := contentTagAttrs[n.Data]
	if !ok {
		return nil, false
	}
	var out []html.Attribute
	for _, a := range n.Attr {
		if !slices.Contains(allowed, a.Key) {
			continue
		}
		switch a.Key {
		case "src", "href":
			a.Val = safeContentURL(a.Val)
			if a.Val == "" {
				continue
			}
		}
		out = append(out, a)
	}
	if n.Data == "a" {
		out = append(out,
			html.Attribute{Key: "target", Val: "_blank"},
			html.Attribute{Key: "rel", Val: "noopener noreferrer"},
		)
	}
	// img 注入自适应内联样式：rich-text 的子节点无法被页面 CSS 选择器命中，
	// 超宽原图会横向撑破详情页；width 属性仍生效但受 max-width 钳制
	if n.Data == "img" {
		out = append(out, html.Attribute{
			Key: "style",
			Val: "max-width:100%;height:auto;display:block;margin:8px 0;border-radius:4px",
		})
	}
	return out, true
}

// safeContentURL 校验图片/链接目标 URL
// 仅放行站内绝对路径（排除 // 开头的协议相对地址）与 http(s) 外链
//
// 参数：
//   - raw: 原始 URL
//
// 返回值：
//   - string: 合法时原样返回，否则返回空串
func safeContentURL(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	lower := strings.ToLower(v)
	if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "vbscript:") {
		return ""
	}
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return v
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return v
	}
	return ""
}
