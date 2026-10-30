package service

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/peer"
)

/**
 * 微信支付纯函数离线单测（2026-10-25 全栈审查 E3；2026-10-30 随签名收敛回 wego 调整）
 *
 * 背景：本机无微信商户证书/密钥，wechat.go 走渠道路径的分支（JSAPIPay/H5Pay/Refund/
 * VerifyNotification）无法离线构造。调起参数签名此前由本服务自行实现，配套有私钥解析、
 * 签名串格式、随机串三组离线用例；2026-10-30 起改由 wego 的 BuildJSAPIPayParams(appID,
 * prepayID) 生成（wego 原先只能取客户端级 AppID、无法按请求传入与 openid 同源的 AppID，
 * 属参数面缺陷，已在库内修复），本地实现与这三组用例一并删除，随机串覆盖移至 wego 的
 * TestGenerateNonce。
 *
 * 覆盖清单（均无外部依赖，CI 可跑）：
 *   - TestGetClientIP          无 peer / 有 peer / Addr 为 nil 三态
 *   - TestParseWechatCallbackNotConfigured 商户未配置时短路报错，不 panic
 *   - TestVerifyWechatCallbackIdentity     归属校验：商户号/AppID/币种口径
 *
 * 「一条微信配置同时绑 mp + h5，两笔单分别按各自渠道 AppID 通过归属校验」需要真实配置行
 * 与支付单，属集成用例（见 todolist 第六节），本机无商户证书无法离线构造。
 */

// TestGetClientIP 从 gRPC 上下文取对端地址：无 peer、有 peer、Addr 为 nil 三态
func TestGetClientIP(t *testing.T) {
	t.Run("无 peer 返回空串", func(t *testing.T) {
		if got := getClientIP(context.Background()); got != "" {
			t.Errorf("getClientIP(无 peer) = %q, want \"\"", got)
		}
	})

	t.Run("有 peer 返回地址", func(t *testing.T) {
		addr := &net.TCPAddr{IP: net.ParseIP("10.0.0.8"), Port: 18080}
		ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: addr})
		if got := getClientIP(ctx); got != "10.0.0.8:18080" {
			t.Errorf("getClientIP = %q, want 10.0.0.8:18080", got)
		}
	})

	t.Run("peer 存在但 Addr 为 nil 返回空串", func(t *testing.T) {
		ctx := peer.NewContext(context.Background(), &peer.Peer{})
		if got := getClientIP(ctx); got != "" {
			t.Errorf("getClientIP(Addr=nil) = %q, want \"\"", got)
		}
	})
}

// TestParseWechatCallbackNotConfigured 商户未配置时守卫短路：报错而非 panic
// 后续验签解密分支依赖真实商户证书，本机无法离线构造 wego 客户端，不在覆盖范围
func TestParseWechatCallbackNotConfigured(t *testing.T) {
	s := &PaymentService{}
	_, err := parseWechatCallback(s, context.Background(), []byte(`{"id":"fake"}`), http.Header{})
	if err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("期望「微信支付未配置或配置不完整」，实际: %v", err)
	}
}

// TestVerifyWechatCallbackIdentity 回调归属校验：商户号/AppID/币种不匹配必须拒绝
// 背景：验签通过只证明报文由微信发出，不证明交易属于本商户（证书/APIv3 密钥复用或泄露时）。
// wantAppID 取自支付单的 app_id 快照——微信 AppID 属渠道身份，支付配置行不再持有
func TestVerifyWechatCallbackIdentity(t *testing.T) {
	mk := func(mch, app, currency string) *wechatTransactionNotification {
		n := &wechatTransactionNotification{MchID: mch, AppID: app}
		n.Amount.Currency = currency
		return n
	}
	cases := []struct {
		name    string
		n       *wechatTransactionNotification
		wantErr bool
	}{
		{"全部匹配", mk("1600000001", "wxapp", "CNY"), false},
		{"商户号不匹配", mk("1600000002", "wxapp", "CNY"), true},
		{"AppID 不匹配", mk("1600000001", "otherapp", "CNY"), true},
		{"外币单拒绝", mk("1600000001", "wxapp", "USD"), true},
		{"币种缺省放行（旧通知不带该字段）", mk("1600000001", "wxapp", ""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyWechatCallbackIdentity(tc.n, "1600000001", "wxapp")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
	// 单上无 AppID 快照（现金/线下单）时跳过 AppID 比对，商户号仍须一致
	if err := verifyWechatCallbackIdentity(mk("1600000001", "anyapp", "CNY"), "1600000001", ""); err != nil {
		t.Fatalf("空 AppID 应跳过比对: %v", err)
	}
	// 未配置微信支付时不比对商户号/AppID，但币种口径与配置无关，外币仍拒
	if err := verifyWechatCallbackIdentity(mk("x", "y", "USD"), "", ""); err == nil {
		t.Fatal("外币单在空配置下仍应拒绝")
	}
}
