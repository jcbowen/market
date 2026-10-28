package likeutil

import "testing"

// TestEscape 元字符按字面量转义，普通字符原样保留
//
// 使用示例: go test ./common/likeutil
func TestEscape(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"土豆":      "土豆",
		"%":       `\%`,
		"50%瘦肉":   `50\%瘦肉`,
		"a_b":     `a\_b`,
		`C:\temp`: `C:\\temp`,
		`100%_\`:  `100\%\_\\`,
		// 单次扫描替换：输入里的反斜杠与紧随其后的 % 各自独立转义，不会互相吞并
		`\%`: `\` + `\` + `\` + `%`,
	}
	for input, want := range cases {
		if got := Escape(input); got != want {
			t.Errorf("Escape(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestContains 子串匹配模式串：通配符只允许来自两端拼接，不接受用户输入
func TestContains(t *testing.T) {
	if got, want := Contains("100%"), `%100\%%`; got != want {
		t.Fatalf("Contains 结果不符，got=%q want=%q", got, want)
	}
	// 空关键词也必须是合法模式串（转义后仍带两侧 %），调用方据此可省一次判空
	if got, want := Contains(""), "%%"; got != want {
		t.Fatalf("空输入应退化为全匹配模式，got=%q want=%q", got, want)
	}
}
