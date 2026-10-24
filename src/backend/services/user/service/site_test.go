package service

import "testing"

/**
 * site.go 页脚富文本消毒纯函数测试
 * 覆盖 sanitizeFooterHTML 白名单重建与 safeFooterHref 链接协议边界，无外部依赖，CI 可直接跑
 */

// TestSanitizeFooterHTML 校验页脚富文本白名单清洗行为
//
// 参数：
//   - t: 测试句柄
func TestSanitizeFooterHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空输入原样返回空", "", ""},
		{"白名单标签与排版保留", `<p><b>加粗</b><i>斜体</i><u>下划线</u><br></p>`, `<p><b>加粗</b><i>斜体</i><u>下划线</u><br></p>`},
		{"b/i/u 与 strong/em 归一保留", `<strong>s</strong><em>e</em><span>行内</span>`, `<strong>s</strong><em>e</em><span>行内</span>`},
		{"事件属性剥离", `<p onclick="x()" onmouseover="y()">文本</p>`, `<p>文本</p>`},
		{"危险标签连子树丢弃", `<p>a</p><script>alert(1)</script><p>b</p>`, `<p>a</p><p>b</p>`},
		{"iframe 整体丢弃", `<p>a</p><iframe src="https://evil.example.com"></iframe><p>b</p>`, `<p>a</p><p>b</p>`},
		{"非白名单标签解包保留子内容", `<div><span>内容</span></div>`, `<span>内容</span>`},
		{"javascript 链接剔 href 保留文字并加固", `<a href="javascript:alert(1)">链接</a>`, `<a target="_blank" rel="noopener noreferrer" style="color:inherit;text-decoration:none">链接</a>`},
		{"合法外链保留并加固", `<a href="https://beian.miit.gov.cn/">备案</a>`, `<a href="https://beian.miit.gov.cn/" target="_blank" rel="noopener noreferrer" style="color:inherit;text-decoration:none">备案</a>`},
		{"用户提供的 style 被剥除后重注入固定样式", `<a href="https://beian.miit.gov.cn/" style="color:red;font-weight:bold">备案</a>`, `<a href="https://beian.miit.gov.cn/" target="_blank" rel="noopener noreferrer" style="color:inherit;text-decoration:none">备案</a>`},
		{"文本特殊字符转义", `<p>1 < 2 & "引用"</p>`, `<p>1 &lt; 2 &amp; &#34;引用&#34;</p>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeFooterHTML(c.in); got != c.want {
				t.Fatalf("sanitizeFooterHTML(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSafeFooterHref 校验链接目标协议边界
//
// 参数：
//   - t: 测试句柄
func TestSafeFooterHref(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空值放行为空", "", ""},
		{"https 外链放行", "https://beian.miit.gov.cn/", "https://beian.miit.gov.cn/"},
		{"http 外链放行", "http://example.com/a", "http://example.com/a"},
		{"站内绝对路径放行", "/about", "/about"},
		{"协议相对地址拒绝", "//evil.com", ""},
		{"ftp 协议拒绝", "ftp://files.example.com", ""},
		{"javascript 协议拒绝", "javascript:alert(1)", ""},
		{"无协议裸域名拒绝", "beian.miit.gov.cn", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := safeFooterHref(c.in); got != c.want {
				t.Fatalf("safeFooterHref(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
