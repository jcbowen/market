package service

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"sort"
	"strings"
	"testing"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 支付宝对接离线测试（无 DB、无网络，CI 可跑）
 *
 * 覆盖：
 *   - parseAlipayCallback   异步通知验签 + 身份/状态/金额校验
 *     用测试内生成的 RSA 密钥对模拟「支付宝侧签名」，公钥作为配置中的支付宝公钥，
 *     签名内容由测试自行按官方通知口径拼接，独立于 SDK 实现，能同时回归 SDK 的验签行为
 *   - alipayTimeoutExpress  交易时效与本地订单超时秒数的换算
 *   - 网关回调参数解析口径见 gateway/handler 侧说明（此处覆盖服务端受理逻辑）
 */

// testAlipayAppID 测试用商户 AppID
const testAlipayAppID = "2021000000000000"

// notifySignContent 按支付宝异步通知官方口径拼接待验签串：
// 剔除 sign/sign_type，剩余参数按 key 升序以 k=v 用 & 连接，仅剔除空值（"@" 开头的值照常参与）。
// 在测试内独立实现，用于校验被测链路与真实支付宝口径一致。
// 参数：
//   - params: 通知参数（可含 sign/sign_type，内部剔除）
//
// 返回值：
//   - string: 待签串
func notifySignContent(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return strings.Join(parts, "&")
}

// newAlipayNotifyEnv 构造仅含支付宝配置的被测服务（不落库、不构建 SDK 客户端）
// 参数：
//   - t: 测试对象
//   - appID: 配置中的商户 AppID
//   - publicKey: 配置中的支付宝公钥（裸 base64），传空串模拟公钥未配置
//
// 返回值：
//   - *PaymentService: 被测服务实例
//   - *rsa.PrivateKey: 与 publicKey 配对的私钥，用于伪造支付宝侧签名
func newAlipayNotifyEnv(t *testing.T, appID, publicKey string) (*PaymentService, *rsa.PrivateKey) {
	t.Helper()
	pri, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试 RSA 密钥对失败: %v", err)
	}
	if publicKey == "" {
		der, err := x509.MarshalPKIXPublicKey(&pri.PublicKey)
		if err != nil {
			t.Fatalf("序列化测试公钥失败: %v", err)
		}
		publicKey = base64.StdEncoding.EncodeToString(der)
	}
	s := &PaymentService{cfg: &config.Config{}}
	s.payCfg = &model.PayConfig{AlipayAppID: appID, AlipayPublicKey: publicKey}
	return s, pri
}

// signedNotify 生成一条支付宝签名合法的通知参数
// 参数：
//   - t: 测试对象
//   - pri: 伪造的支付宝私钥
//   - fields: 业务通知字段（app_id/out_trade_no/trade_no/trade_status/total_amount 等）
//
// 返回值：
//   - map[string]string: 已附带 sign/sign_type 的完整通知参数
func signedNotify(t *testing.T, pri *rsa.PrivateKey, fields map[string]string) map[string]string {
	t.Helper()
	params := make(map[string]string, len(fields)+2)
	for k, v := range fields {
		params[k] = v
	}
	digest := sha256.Sum256([]byte(notifySignContent(params)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, pri, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("伪造支付宝通知签名失败: %v", err)
	}
	params["sign"] = base64.StdEncoding.EncodeToString(sig)
	params["sign_type"] = "RSA2"
	return params
}

// successNotifyFields 一条支付成功通知的业务字段
// 参数：
//   - appID: 商户 AppID
//
// 返回值：
//   - map[string]string: 字段集合
func successNotifyFields(appID string) map[string]string {
	return map[string]string{
		"app_id":       appID,
		"out_trade_no": "PAY202601010001",
		"trade_no":     "2026010122001000000512345678",
		"trade_status": "TRADE_SUCCESS",
		"total_amount": "12.34",
		"subject":      "社区商超订单",
		"gmt_payment":  "2026-01-01 10:00:00",
		"seller_id":    "2088000000000000",
	}
}

// TestParseAlipayCallbackSuccess 合法成功通知：验签通过并解析出单号、交易号与金额
func TestParseAlipayCallbackSuccess(t *testing.T) {
	s, pri := newAlipayNotifyEnv(t, testAlipayAppID, "")
	got, err := s.parseAlipayCallback(signedNotify(t, pri, successNotifyFields(testAlipayAppID)))
	if err != nil {
		t.Fatalf("合法通知被拒绝: %v", err)
	}
	if got.PaymentNo != "PAY202601010001" || got.TransactionID != "2026010122001000000512345678" {
		t.Fatalf("解析结果不符: %+v", got)
	}
	if got.Amount != 12.34 {
		t.Fatalf("回调金额解析错误: %v", got.Amount)
	}
}

// TestParseAlipayCallbackAtPrefixedValue 值含 "@" 前缀的通知字段须照常参与签名
// （支付宝通知口径不剔除 "@" 值，此前 SDK 误剔除导致合法通知验签失败）
func TestParseAlipayCallbackAtPrefixedValue(t *testing.T) {
	s, pri := newAlipayNotifyEnv(t, testAlipayAppID, "")
	fields := successNotifyFields(testAlipayAppID)
	fields["body"] = "@团购订单"
	if _, err := s.parseAlipayCallback(signedNotify(t, pri, fields)); err != nil {
		t.Fatalf("含 @ 值的通知被拒绝: %v", err)
	}
}

// TestParseAlipayCallbackRejects 各类非法通知必须拒绝，防假回调置已支付与跨应用重放
func TestParseAlipayCallbackRejects(t *testing.T) {
	s, pri := newAlipayNotifyEnv(t, testAlipayAppID, "")

	cases := []struct {
		name   string
		params map[string]string
	}{
		{
			name: "篡改金额",
			params: func() map[string]string {
				p := signedNotify(t, pri, successNotifyFields(testAlipayAppID))
				p["total_amount"] = "0.01"
				return p
			}(),
		},
		{
			name: "签名缺失",
			params: func() map[string]string {
				p := signedNotify(t, pri, successNotifyFields(testAlipayAppID))
				delete(p, "sign")
				return p
			}(),
		},
		{
			name: "跨应用重放（app_id 不匹配）",
			params: func() map[string]string {
				fields := successNotifyFields("2021000000000999")
				return signedNotify(t, pri, fields)
			}(),
		},
		{
			name: "交易状态非成功",
			params: func() map[string]string {
				fields := successNotifyFields(testAlipayAppID)
				fields["trade_status"] = "WAIT_BUYER_PAY"
				return signedNotify(t, pri, fields)
			}(),
		},
		{
			name: "交易已关闭",
			params: func() map[string]string {
				fields := successNotifyFields(testAlipayAppID)
				fields["trade_status"] = "TRADE_CLOSED"
				return signedNotify(t, pri, fields)
			}(),
		},
		{
			name: "缺 out_trade_no",
			params: func() map[string]string {
				fields := successNotifyFields(testAlipayAppID)
				delete(fields, "out_trade_no")
				return signedNotify(t, pri, fields)
			}(),
		},
		{
			name: "缺 trade_no",
			params: func() map[string]string {
				fields := successNotifyFields(testAlipayAppID)
				delete(fields, "trade_no")
				return signedNotify(t, pri, fields)
			}(),
		},
		{
			name: "金额缺失",
			params: func() map[string]string {
				fields := successNotifyFields(testAlipayAppID)
				delete(fields, "total_amount")
				return signedNotify(t, pri, fields)
			}(),
		},
		{
			name: "金额为 0",
			params: func() map[string]string {
				fields := successNotifyFields(testAlipayAppID)
				fields["total_amount"] = "0.00"
				return signedNotify(t, pri, fields)
			}(),
		},
		{
			name:   "空参数",
			params: map[string]string{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := s.parseAlipayCallback(c.params); err == nil {
				t.Fatalf("非法通知未被拒绝: %+v", got)
			}
		})
	}
}

// TestParseAlipayCallbackUnconfiguredKey 支付宝公钥或 AppID 未配置时拒绝受理通知
func TestParseAlipayCallbackUnconfiguredKey(t *testing.T) {
	// 公钥未配置
	noKey, err := (&PaymentService{cfg: &config.Config{}, payCfg: &model.PayConfig{AlipayAppID: testAlipayAppID}}).
		parseAlipayCallback(map[string]string{"sign": "abc"})
	if err == nil || noKey != nil {
		t.Fatalf("公钥未配置应拒绝: %+v err=%v", noKey, err)
	}

	// 公钥已配置但 AppID 缺失：验签通过后仍须拒绝（无法校验通知身份）
	s, pri := newAlipayNotifyEnv(t, "", "")
	got, err := s.parseAlipayCallback(signedNotify(t, pri, successNotifyFields(testAlipayAppID)))
	if err == nil || got != nil {
		t.Fatalf("AppID 未配置应拒绝: %+v err=%v", got, err)
	}
}

// TestAlipayTimeoutExpress 订单超时秒数换算为支付宝相对时效（向上取整为分钟，最小 1m）
func TestAlipayTimeoutExpress(t *testing.T) {
	cases := []struct {
		name     string
		timeout  int
		hasCfg   bool
		expected string
	}{
		{"默认半小时", 1800, true, "30m"},
		{"整分钟", 60, true, "1m"},
		{"不足一分钟向上取整", 90, true, "2m"},
		{"一秒", 1, true, "1m"},
		{"一小时", 3600, true, "60m"},
		{"非正回落默认", 0, true, "30m"},
		{"负数回落默认", -60, true, "30m"},
		{"无配置回落默认", 0, false, "30m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &PaymentService{}
			if c.hasCfg {
				cfg := &config.Config{}
				cfg.Business.OrderTimeout = c.timeout
				s.cfg = cfg
			}
			if got := s.alipayTimeoutExpress(); got != c.expected {
				t.Fatalf("timeout_express = %s, 期望 %s", got, c.expected)
			}
		})
	}
}
