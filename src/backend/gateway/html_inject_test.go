package main

import (
	"path/filepath"
	"testing"
)

/**
 * HTML 注入目标文件解析（P2 路径穿越）测试
 *
 * 背景：targetHTMLFile 原直接 filepath.Join(e.dir, rest)，请求路径末段以 .html 结尾
 * 即可借 ../ 或反斜杠逃出静态目录读取任意文件。修法：Join（内含 Clean）后校验
 * 前缀仍落在入口目录内，越界一律回退入口页 index.html。
 *
 * 运行方式：go test ./gateway/ -run TestTargetHTMLFile -v
 */

// TestTargetHTMLFileTraversal 遍历与越界路径必须回退入口页，不得逃出静态目录
func TestTargetHTMLFileTraversal(t *testing.T) {
	dir := t.TempDir()
	entry := htmlEntry{prefix: "/b", dir: dir, admin: true}
	entryIndex := filepath.Join(dir, "index.html")

	t.Run("正常路径", func(t *testing.T) {
		cases := map[string]string{
			"/b":                entryIndex, // 目录索引
			"/b/":               entryIndex,
			"/b/index.html":     entryIndex,
			"/b/deep/page.html": filepath.Join(dir, "deep", "page.html"),
			"/b/assets/app.js":  entryIndex, // 非 .html 不介入，交回入口页承接（SPA 深链口径）
		}
		for p, want := range cases {
			if got := targetHTMLFile(p, entry); got != want {
				t.Errorf("targetHTMLFile(%q) = %q, want %q", p, got, want)
			}
		}
	})

	t.Run("越界路径回退入口页", func(t *testing.T) {
		cases := []string{
			"/b/../../secret.html",          // 相对目录穿越（含 %2F 编码形态：net/url 在进 gin 前已解码为 ../）
			"/b/sub/../../../../etc/x.html", // 深层穿越
			"/b/..\\..\\secret.html",        // Windows 反斜杠（URL.Path 解码后可携带）
			"/b/..\\.git\\config.html",      // 借 .html 后缀读仓库内敏感文件
		}
		for _, p := range cases {
			got := targetHTMLFile(p, entry)
			if got != entryIndex {
				t.Errorf("targetHTMLFile(%q) = %q, 应回退入口页 %q", p, got, entryIndex)
			}
		}
	})
}
