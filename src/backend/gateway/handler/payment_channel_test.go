package handler

import (
	"testing"

	"github.com/jcbowen/market/src/backend/common/channel"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
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

// TestWechatPayReadyOnClient 微信支付按客户端渠道的能力就绪判定
// 覆盖：mp 渠道启用+配置完整、mp 未启用/未配置、h5 恒就绪、gzh/app 链路未开放、未知渠道存量口径放行
func TestWechatPayReadyOnClient(t *testing.T) {
	mpReady := &user.ClientChannelInfo{Code: channel.MP, Enabled: true, Configured: true}
	mpDisabled := &user.ClientChannelInfo{Code: channel.MP, Enabled: false, Configured: true}
	mpNotConfigured := &user.ClientChannelInfo{Code: channel.MP, Enabled: true, Configured: false}

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
		{"h5 恒就绪", channel.H5, nil, true},
		{"公众号链路未开放", channel.GZH, map[string]*user.ClientChannelInfo{channel.GZH: mpReady}, false},
		{"App 链路未开放", channel.APP, nil, false},
		{"未知渠道存量口径放行", "wx", nil, true},
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
// 覆盖：h5 手机网站支付恒就绪、微信生态渠道拒绝、App 链路未接入、未知渠道存量口径放行
func TestAlipayPayReadyOnClient(t *testing.T) {
	mpReady := &user.ClientChannelInfo{Code: channel.MP, Enabled: true, Configured: true}

	cases := []struct {
		name     string
		code     string
		channels map[string]*user.ClientChannelInfo
		want     bool
	}{
		{"h5 恒就绪", channel.H5, nil, true},
		{"微信小程序内无法唤起", channel.MP, map[string]*user.ClientChannelInfo{channel.MP: mpReady}, false},
		{"公众号链路无法唤起", channel.GZH, map[string]*user.ClientChannelInfo{channel.GZH: mpReady}, false},
		{"App 支付未接入", channel.APP, nil, false},
		{"未知渠道存量口径放行", "wx", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := alipayPayReadyOnClient(tc.code, tc.channels); got != tc.want {
				t.Fatalf("alipayPayReadyOnClient(%q) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// TestIsChannelUsableOnClient 支付渠道在客户端渠道上的交集过滤
// 覆盖：未带渠道头不过滤、勾选命中/未命中、空勾选=全渠道兼容、cash 恒可用、非法渠道头
func TestIsChannelUsableOnClient(t *testing.T) {
	cfg := &payment.PayConfig{
		WechatClientChannels: []string{channel.MP},
		AlipayClientChannels: []string{channel.H5},
	}
	channels := map[string]*user.ClientChannelInfo{
		channel.MP: {Code: channel.MP, Enabled: true, Configured: true},
	}

	cases := []struct {
		name       string
		payChannel string
		clientCode string
		want       bool
	}{
		{"未带渠道头保持存量口径", "wechat", "", true},
		{"非法渠道头不过滤", "wechat", "wx", true},
		{"微信勾选命中 mp", "wechat", channel.MP, true},
		{"微信勾选未命中 h5", "wechat", channel.H5, false},
		{"现金渠道全客户端可用", "cash", channel.APP, true},
		{"支付宝勾选命中 h5 且能力就绪", "alipay", channel.H5, true},
		{"支付宝勾选命中但微信生态拒绝", "alipay", channel.MP, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isChannelUsableOnClient(tc.payChannel, tc.clientCode, cfg, channels); got != tc.want {
				t.Fatalf("isChannelUsableOnClient(%q, %q) = %v, want %v", tc.payChannel, tc.clientCode, got, tc.want)
			}
		})
	}

	// 空勾选 = 全部客户端渠道可用（存量数据兼容口径）
	emptyCfg := &payment.PayConfig{}
	if !isChannelUsableOnClient("wechat", channel.H5, emptyCfg, channels) {
		t.Fatalf("空勾选时应全渠道放行: wechat@h5 want true")
	}
	if !isChannelUsableOnClient("alipay", channel.H5, emptyCfg, channels) {
		t.Fatalf("空勾选时应全渠道放行: alipay@h5 want true")
	}
}
