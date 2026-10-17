package channel

import "testing"

// TestIsValid 渠道 code 合法性校验
func TestIsValid(t *testing.T) {
	cases := []struct {
		name string
		code string
		want bool
	}{
		{"小程序", MP, true},
		{"公众号", GZH, true},
		{"App", APP, true},
		{"H5", H5, true},
		{"空串", "", false},
		{"未知渠道", "douyin", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValid(tc.code); got != tc.want {
				t.Fatalf("IsValid(%q) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// TestNormalize 渠道 code 规整：大小写归一，未知值原样返回（交由调用方校验）
func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"MP", MP},
		{"GZH", GZH},
		{"App", APP},
		{"h5", H5},
		{"", ""},
		{"wx", "wx"},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in); got != tc.want {
			t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAll 内置渠道全量列表
func TestAll(t *testing.T) {
	all := All()
	if len(all) != 4 {
		t.Fatalf("内置渠道数应为 4, got %d: %v", len(all), all)
	}
	for _, code := range all {
		if !IsValid(code) {
			t.Fatalf("All() 返回了非法渠道: %q", code)
		}
	}
}
