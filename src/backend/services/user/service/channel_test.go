package service

import (
	"testing"

	"github.com/jcbowen/market/src/backend/common/channel"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// TestIsPhoneOpenid 手机号占位 openid 识别
func TestIsPhoneOpenid(t *testing.T) {
	cases := []struct {
		name   string
		openid string
		want   bool
	}{
		{"手机号占位", "phone_17898616950", true},
		{"真实 openid", "o6_bmjrPTlm6_2sgVt7hMZOPfL2M", false},
		{"空串", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPhoneOpenid(tc.openid); got != tc.want {
				t.Fatalf("isPhoneOpenid(%q) = %v, want %v", tc.openid, got, tc.want)
			}
		})
	}
}

// TestClientChannelIsConfigured 渠道凭证完整性判定
// 覆盖：h5/pos 无登录凭证，但绑定微信支付时必须有 AppID（AppID 属渠道身份，
// 支付配置行不再持有）；alimp 仅需 AppID（签名私钥复用支付配置）；
// 微信系渠道 AppID/AppSecret 需成对非空
func TestClientChannelIsConfigured(t *testing.T) {
	cases := []struct {
		name string
		ch   model.ClientChannel
		want bool
	}{
		{"h5 未绑微信支付、无凭证视为已配置", model.ClientChannel{Code: channel.H5, PayMethods: `["cash"]`}, true},
		{"h5 绑微信支付缺 AppID", model.ClientChannel{Code: channel.H5, PayMethods: `["wechat:3"]`}, false},
		{"h5 绑微信支付有 AppID", model.ClientChannel{Code: channel.H5, AppID: "wxh5", PayMethods: `["wechat:3"]`}, true},
		{"pos 未绑微信支付、无凭证视为已配置", model.ClientChannel{Code: channel.POS, PayMethods: `["cash"]`}, true},
		{"pos 绑微信支付缺 AppID", model.ClientChannel{Code: channel.POS, PayMethods: `["cash","wechat:1"]`}, false},
		{"pos 仅绑支付宝不需 AppID", model.ClientChannel{Code: channel.POS, PayMethods: `["cash","alipay:2"]`}, true},
		{"支付宝小程序仅 AppID", model.ClientChannel{Code: channel.ALIMP, AppID: "20210001"}, true},
		{"支付宝小程序缺 AppID", model.ClientChannel{Code: channel.ALIMP}, false},
		{"小程序凭证完整", model.ClientChannel{Code: channel.MP, AppID: "wx123", AppSecret: "sec"}, true},
		{"小程序缺 Secret", model.ClientChannel{Code: channel.MP, AppID: "wx123"}, false},
		{"小程序缺 AppID", model.ClientChannel{Code: channel.MP, AppSecret: "sec"}, false},
		{"公众号凭证为空", model.ClientChannel{Code: channel.GZH}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ch.IsConfigured(); got != tc.want {
				t.Fatalf("IsConfigured(code=%s) = %v, want %v", tc.ch.Code, got, tc.want)
			}
		})
	}
}

// TestToClientChannelInfo 渠道模型到 proto 的映射（含支付方式绑定列表）
func TestToClientChannelInfo(t *testing.T) {
	if ToClientChannelInfo(nil) != nil {
		t.Fatalf("nil 输入应返回 nil")
	}
	row := &model.ClientChannel{
		ID: 3, Code: channel.MP, Name: "微信小程序", AppID: "wx123", AppSecret: "sec",
		Enabled: true, PayMethods: `["cash","wechat:5"]`, UpdatedAt: "2026-10-18 00:00:00",
	}
	info := ToClientChannelInfo(row)
	if info.Id != row.ID || info.Code != row.Code || info.AppId != row.AppID ||
		!info.Enabled || !info.Configured || info.UpdatedAt != row.UpdatedAt {
		t.Fatalf("映射字段不符: %+v", info)
	}
	if len(info.PayMethods) != 2 || info.PayMethods[0] != "cash" || info.PayMethods[1] != "wechat:5" {
		t.Fatalf("PayMethods 映射不符: %v", info.PayMethods)
	}
}

// TestPayMethodsToList 绑定 JSON 解析（model.ClientChannel.PayMethodsList）：非法 JSON/空串回退为空列表
func TestPayMethodsToList(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		wantN int
	}{
		{"正常数组", `["cash","wechat:3"]`, 2},
		{"空数组", `[]`, 0},
		{"空串", ``, 0},
		{"非法 JSON", `{bad`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := &model.ClientChannel{PayMethods: tc.raw}
			if got := ch.PayMethodsList(); len(got) != tc.wantN {
				t.Fatalf("PayMethodsList(%q) 长度 = %d, want %d (%v)", tc.raw, len(got), tc.wantN, got)
			}
		})
	}
}
