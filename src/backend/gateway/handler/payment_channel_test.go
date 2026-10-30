package handler

import (
	"testing"

	"github.com/jcbowen/market/src/backend/common/channel"
	"github.com/jcbowen/market/src/backend/common/proto/user"
)

// TestContainsString 字符串列表包含判断
func TestContainsString(t *testing.T) {
	cases := []struct {
		name   string
		list   []string
		target string
		want   bool
	}{
		{"命中", []string{"mp", "h5"}, "mp", true},
		{"未命中", []string{"mp"}, "h5", false},
		{"空列表", nil, "mp", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsString(tc.list, tc.target); got != tc.want {
				t.Fatalf("containsString(%v, %q) = %v, want %v", tc.list, tc.target, got, tc.want)
			}
		})
	}
}

// TestParsePayMethodEntry 渠道绑定条目拆解
// 覆盖：cash 内置项、platform:config_id、非法语法一律回退零值
func TestParsePayMethodEntry(t *testing.T) {
	cases := []struct {
		name         string
		entry        string
		wantPlatform string
		wantID       int64
	}{
		{"cash 内置项", "cash", "", 0},
		{"cash 带空白", " cash ", "", 0},
		{"微信配置", "wechat:3", "wechat", 3},
		{"支付宝配置", "alipay:12", "alipay", 12},
		{"非法：缺冒号", "wechat", "", 0},
		{"非法：ID 非数字", "wechat:abc", "", 0},
		{"非法：ID 为 0", "wechat:0", "", 0},
		{"非法：ID 为负", "wechat:-1", "", 0},
		{"非法：空串", "", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			platform, id := parsePayMethodEntry(tc.entry)
			if platform != tc.wantPlatform || id != tc.wantID {
				t.Fatalf("parsePayMethodEntry(%q) = (%q, %d), want (%q, %d)", tc.entry, platform, id, tc.wantPlatform, tc.wantID)
			}
		})
	}
}

// TestFirstConfigIDForPlatform 与 TestBindingHasCash 渠道绑定解析
func TestBindingResolution(t *testing.T) {
	methods := []string{"cash", "alipay:2", "wechat:5"}
	if id := firstConfigIDForPlatform(methods, "wechat"); id != 5 {
		t.Fatalf("wechat 配置 ID = %d, want 5", id)
	}
	if id := firstConfigIDForPlatform(methods, "alipay"); id != 2 {
		t.Fatalf("alipay 配置 ID = %d, want 2", id)
	}
	if id := firstConfigIDForPlatform(methods, "unknown"); id != 0 {
		t.Fatalf("未绑定平台应返回 0, got %d", id)
	}
	if !bindingHasCash(methods) {
		t.Fatal("绑定含 cash 应返回 true")
	}
	if bindingHasCash([]string{"wechat:1"}) {
		t.Fatal("绑定不含 cash 应返回 false")
	}
}

// TestWechatPayReadyOnClient 微信支付按客户端渠道的能力就绪判定
// 覆盖：mp 渠道启用+配置完整、mp 未启用/未配置、
// h5 以渠道 AppID 为准（微信 AppID 属渠道身份，支付配置行不再持有）、
// gzh/alimp/android/ios 链路未开放或生态不兼容
func TestWechatPayReadyOnClient(t *testing.T) {
	mpReady := &user.ClientChannelInfo{Code: channel.MP, Enabled: true, Configured: true}
	mpDisabled := &user.ClientChannelInfo{Code: channel.MP, Enabled: false, Configured: true}
	mpNotConfigured := &user.ClientChannelInfo{Code: channel.MP, Enabled: true, Configured: false}
	h5WithAppID := &user.ClientChannelInfo{Code: channel.H5, AppId: "wx-h5-app"}
	h5BlankAppID := &user.ClientChannelInfo{Code: channel.H5, AppId: "   "}
	h5NoAppID := &user.ClientChannelInfo{Code: channel.H5}

	cases := []struct {
		name     string
		code     string
		channels map[string]*user.ClientChannelInfo
		want     bool
	}{
		{"mp 渠道就绪", channel.MP, map[string]*user.ClientChannelInfo{channel.MP: mpReady}, true},
		{"mp 渠道停用", channel.MP, map[string]*user.ClientChannelInfo{channel.MP: mpDisabled}, false},
		{"mp 渠道未配置凭证", channel.MP, map[string]*user.ClientChannelInfo{channel.MP: mpNotConfigured}, false},
		{"mp 渠道索引缺失（渠道服务异常降级）", channel.MP, nil, false},
		{"h5 已配 AppID 就绪", channel.H5, map[string]*user.ClientChannelInfo{channel.H5: h5WithAppID}, true},
		{"h5 未配 AppID 未就绪（下单会被渠道以 appid 必填拒绝）", channel.H5, map[string]*user.ClientChannelInfo{channel.H5: h5NoAppID}, false},
		{"h5 AppID 全空白视为未配", channel.H5, map[string]*user.ClientChannelInfo{channel.H5: h5BlankAppID}, false},
		{"h5 渠道索引缺失", channel.H5, nil, false},
		{"公众号 OAuth 链路未接入", channel.GZH, nil, false},
		{"支付宝小程序内无法唤起微信", channel.ALIMP, nil, false},
		{"安卓 App 支付未接入", channel.ANDROID, nil, false},
		{"iOS App 支付未接入", channel.IOS, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wechatPayReadyOnClient(tc.code, tc.channels); got != tc.want {
				t.Fatalf("wechatPayReadyOnClient(%q) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// TestAlipayPayReadyOnClient 支付宝支付按客户端渠道的能力就绪判定
// 覆盖：h5 手机网站支付与 alimp 小程序支付恒就绪、微信生态渠道与 App 渠道拒绝
func TestAlipayPayReadyOnClient(t *testing.T) {
	cases := []struct {
		name string
		code string
		want bool
	}{
		{"h5 恒就绪", channel.H5, true},
		{"支付宝小程序恒就绪", channel.ALIMP, true},
		{"微信小程序内无法唤起", channel.MP, false},
		{"公众号内无法唤起", channel.GZH, false},
		{"安卓 App 支付未接入", channel.ANDROID, false},
		{"iOS App 支付未接入", channel.IOS, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := alipayPayReadyOnClient(tc.code); got != tc.want {
				t.Fatalf("alipayPayReadyOnClient(%q) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// TestResolveChannelPayBinding 渠道绑定解析：未带渠道头按 h5 兜底
func TestResolveChannelPayBinding(t *testing.T) {
	h5 := &user.ClientChannelInfo{Code: channel.H5, Enabled: true}
	channels := map[string]*user.ClientChannelInfo{
		channel.H5: h5,
	}
	if got := resolveChannelPayBinding("", channels); got != h5 {
		t.Fatal("未带渠道头应按 h5 渠道解析")
	}
	if got := resolveChannelPayBinding(channel.H5, channels); got != h5 {
		t.Fatal("显式 h5 应命中渠道表")
	}
	if got := resolveChannelPayBinding(channel.MP, channels); got != nil {
		t.Fatal("渠道表中不存在的渠道应返回 nil")
	}
}

// TestAuthCodePlatform 付款码前缀识别平台
func TestAuthCodePlatform(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"10", payPlatformWechat},
		{"15", payPlatformWechat},
		{"1345678901234567", payPlatformWechat},
		{"25", payPlatformAlipay},
		{"30", payPlatformAlipay},
		{"2834567890123456", payPlatformAlipay},
		{"16", ""},
		{"35", ""},
		{"", ""},
		{"9", ""},
	}
	for _, tc := range cases {
		if got := authCodePlatform(tc.code); got != tc.want {
			t.Fatalf("authCodePlatform(%q) = %q, want %q", tc.code, got, tc.want)
		}
	}
}
