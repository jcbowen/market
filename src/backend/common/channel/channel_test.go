package channel

import "testing"

// TestIsValid 渠道 code 合法性校验
func TestIsValid(t *testing.T) {
	cases := []struct {
		name string
		code string
		want bool
	}{
		{"H5", H5, true},
		{"公众号", GZH, true},
		{"小程序", MP, true},
		{"支付宝小程序", ALIMP, true},
		{"安卓App", ANDROID, true},
		{"iOS App", IOS, true},
		{"POS收银台", POS, true},
		{"空串", "", false},
		{"未知渠道", "douyin", false},
		{"已移除的app渠道", "app", false},
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
		{"ALIMP", ALIMP},
		{"h5", H5},
		{" POS ", POS},
		{"", ""},
		{"wx", "wx"},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in); got != tc.want {
			t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAll 内置渠道全量列表：固定顺序（按常用程度），不再支持动态排序
func TestAll(t *testing.T) {
	want := []string{H5, GZH, MP, ALIMP, ANDROID, IOS, POS}
	all := All()
	if len(all) != len(want) {
		t.Fatalf("内置渠道数应为 %d, got %d: %v", len(want), len(all), all)
	}
	for i := range want {
		if all[i] != want[i] {
			t.Fatalf("All() 顺序不符: got %v, want %v", all, want)
		}
	}
	for _, code := range all {
		if !IsValid(code) {
			t.Fatalf("All() 返回了非法渠道: %q", code)
		}
	}
}

// TestIsDisabled 禁用态渠道：仅 android/ios 恒为禁用展示
func TestIsDisabled(t *testing.T) {
	cases := []struct {
		code string
		want bool
	}{
		{ANDROID, true},
		{IOS, true},
		{H5, false},
		{GZH, false},
		{MP, false},
		{ALIMP, false},
		{POS, false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsDisabled(tc.code); got != tc.want {
			t.Fatalf("IsDisabled(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

// TestOrderIndex 固定顺序位次：未知渠道排在最后
func TestOrderIndex(t *testing.T) {
	if OrderIndex(H5) != 0 || OrderIndex(POS) != 6 {
		t.Fatalf("OrderIndex 固定顺序不符: h5=%d pos=%d", OrderIndex(H5), OrderIndex(POS))
	}
	if OrderIndex("douyin") != 7 {
		t.Fatalf("未知渠道应排在最后, got %d", OrderIndex("douyin"))
	}
}
