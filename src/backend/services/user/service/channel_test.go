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
// 覆盖：h5 无需凭证恒为已配置；微信系渠道 AppID/AppSecret 需成对非空
func TestClientChannelIsConfigured(t *testing.T) {
	cases := []struct {
		name string
		ch   model.ClientChannel
		want bool
	}{
		{"h5 无凭证视为已配置", model.ClientChannel{Code: channel.H5}, true},
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

// TestToClientChannelInfo 渠道模型到 proto 的映射
func TestToClientChannelInfo(t *testing.T) {
	if ToClientChannelInfo(nil) != nil {
		t.Fatalf("nil 输入应返回 nil")
	}
	row := &model.ClientChannel{
		ID: 3, Code: channel.MP, Name: "微信小程序", AppID: "wx123", AppSecret: "sec",
		Enabled: true, Sort: 1, UpdatedAt: "2026-10-18 00:00:00",
	}
	info := ToClientChannelInfo(row)
	if info.Id != row.ID || info.Code != row.Code || info.AppId != row.AppID ||
		!info.Enabled || !info.Configured || info.UpdatedAt != row.UpdatedAt {
		t.Fatalf("映射字段不符: %+v", info)
	}
}
