package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"sort"
	"strings"
	"testing"

	alipaysdk "github.com/jcbowen/alipay-sdk-go-all"
	wegopayment "github.com/jcbowen/wego/payment"
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
// 多行制下回调按 app_id 从支付配置注册表路由：直接注入 registry 缓存（无 DB 时 getRegistryCached 回落缓存）
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
	s.registry = &payClientRegistry{
		version: "test",
		rows: map[int64]*model.PayConfig{
			1: {ID: 1, Platform: model.PayPlatformAlipay, AlipayAppID: appID, AlipayPublicKey: publicKey},
		},
		wechat: map[int64]*wegopayment.Client{},
		alipay: map[int64]*alipaysdk.GenericExecuteApi{},
	}
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
	got, err := s.parseAlipayCallback(context.Background(), signedNotify(t, pri, successNotifyFields(testAlipayAppID)))
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
	if _, err := s.parseAlipayCallback(context.Background(), signedNotify(t, pri, fields)); err != nil {
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
			if got, err := s.parseAlipayCallback(context.Background(), c.params); err == nil {
				t.Fatalf("非法通知未被拒绝: %+v", got)
			}
		})
	}
}

// TestParseAlipayCallbackUnconfiguredKey 支付宝公钥或 AppID 未配置时拒绝受理通知
func TestParseAlipayCallbackUnconfiguredKey(t *testing.T) {
	// 公钥未配置：app_id 命中配置行但行内支付宝公钥为空，验签前拒绝
	noKey, err := (&PaymentService{cfg: &config.Config{}, registry: &payClientRegistry{
		version: "test",
		rows: map[int64]*model.PayConfig{
			1: {ID: 1, Platform: model.PayPlatformAlipay, AlipayAppID: testAlipayAppID},
		},
		wechat: map[int64]*wegopayment.Client{},
		alipay: map[int64]*alipaysdk.GenericExecuteApi{},
	}}).parseAlipayCallback(context.Background(), map[string]string{"sign": "abc", "app_id": testAlipayAppID})
	if err == nil || noKey != nil {
		t.Fatalf("公钥未配置应拒绝: %+v err=%v", noKey, err)
	}

	// 公钥已配置但 AppID 缺失：验签通过后仍须拒绝（无法校验通知身份）
	s, pri := newAlipayNotifyEnv(t, "", "")
	got, err := s.parseAlipayCallback(context.Background(), signedNotify(t, pri, successNotifyFields(testAlipayAppID)))
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

// newTestAlipayKeys 生成测试用 RSA 密钥对（裸 base64 串，与后台录入格式一致）
// 返回值：
//   - string: PKCS#8 私钥 base64
//   - string: PKIX 公钥 base64
func newTestAlipayKeys(t *testing.T) (string, string) {
	t.Helper()
	pri, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试 RSA 密钥对失败: %v", err)
	}
	priDER, err := x509.MarshalPKCS8PrivateKey(pri)
	if err != nil {
		t.Fatalf("序列化测试私钥失败: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&pri.PublicKey)
	if err != nil {
		t.Fatalf("序列化测试公钥失败: %v", err)
	}
	return base64.StdEncoding.EncodeToString(priDER), base64.StdEncoding.EncodeToString(pubDER)
}

// configuredAlipayConfig 字段齐全的支付宝配置载体（公钥模式）
// 参数：
//   - privateKey: 应用私钥
//   - publicKey: 支付宝公钥
//
// 返回值：
//   - config.AlipayConfig: 配置载体
func configuredAlipayConfig(privateKey, publicKey string) config.AlipayConfig {
	return config.AlipayConfig{
		AppID:           testAlipayAppID,
		PrivateKey:      privateKey,
		AlipayPublicKey: publicKey,
		NotifyURL:       "https://market.example.com/api/v1/payments/callback/alipay",
	}
}

// TestCheckAlipayKeys 密钥自检：合法通过，畸形私钥与错填公钥分别报出对应原因
func TestCheckAlipayKeys(t *testing.T) {
	priKey, pubKey := newTestAlipayKeys(t)

	if msg := checkAlipayKeys(configuredAlipayConfig(priKey, pubKey)); msg != "" {
		t.Fatalf("合法密钥自检应通过，实际: %s", msg)
	}

	cases := []struct {
		name     string
		pri      string
		pub      string
		contains string
	}{
		{"私钥乱码", "not-a-private-key", pubKey, "应用私钥"},
		{"私钥为空", "", pubKey, "应用私钥"},
		{"公钥填成私钥内容", priKey, priKey, "支付宝公钥"},
		{"公钥乱码", priKey, "%%%bad-key%%%", "支付宝公钥"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := checkAlipayKeys(configuredAlipayConfig(c.pri, c.pub))
			if msg == "" {
				t.Fatalf("非法密钥应自检失败")
			}
			if !strings.Contains(msg, c.contains) {
				t.Fatalf("失败原因应包含 %q，实际: %s", c.contains, msg)
			}
		})
	}
}

// TestAlipayConfigStatus 就绪口径：字段齐全且自检通过才算就绪，未就绪须给出原因
func TestAlipayConfigStatus(t *testing.T) {
	priKey, pubKey := newTestAlipayKeys(t)

	cases := []struct {
		name       string
		row        *model.PayConfig
		wantReady  bool
		wantReason string
	}{
		{"配置行为空", nil, false, "不完整"},
		{"必填字段缺失", &model.PayConfig{AlipayAppID: testAlipayAppID}, false, "不完整"},
		{"字段齐全且密钥合法", &model.PayConfig{
			AlipayAppID:      testAlipayAppID,
			AlipayPrivateKey: priKey,
			AlipayPublicKey:  pubKey,
			AlipayNotifyURL:  "https://market.example.com/api/v1/payments/callback/alipay",
		}, true, ""},
		{"字段齐全但公钥错填", &model.PayConfig{
			AlipayAppID:      testAlipayAppID,
			AlipayPrivateKey: priKey,
			AlipayPublicKey:  "%%%bad-key%%%",
			AlipayNotifyURL:  "https://market.example.com/api/v1/payments/callback/alipay",
		}, false, "支付宝公钥"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ready, reason := AlipayConfigStatus(c.row)
			if ready != c.wantReady {
				t.Fatalf("ready = %v, 期望 %v", ready, c.wantReady)
			}
			if c.wantReason == "" && reason != "" {
				t.Fatalf("就绪时不应有原因，实际: %s", reason)
			}
			if c.wantReason != "" && !strings.Contains(reason, c.wantReason) {
				t.Fatalf("原因应包含 %q，实际: %s", c.wantReason, reason)
			}
		})
	}
}

// TestNormalizeAlipayGateway 网关规范化：只保留到域名，去掉开放平台复制来的 /gateway.do 后缀
func TestNormalizeAlipayGateway(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		expected string
	}{
		{"空串保持生产", "", ""},
		{"两侧空白", "  https://openapi.alipay.com  ", "https://openapi.alipay.com"},
		{"带 V2 路径后缀", "https://openapi-sandbox.dl.alipaydev.com/gateway.do", "https://openapi-sandbox.dl.alipaydev.com"},
		{"大小写混用的路径后缀", "https://openapi-sandbox.dl.alipaydev.com/Gateway.DO", "https://openapi-sandbox.dl.alipaydev.com"},
		{"末尾多余斜杠", "https://openapi-sandbox.dl.alipaydev.com/", "https://openapi-sandbox.dl.alipaydev.com"},
		{"自定义端口", "https://proxy.example.com:8443/gateway.do", "https://proxy.example.com:8443"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeAlipayGateway(c.raw); got != c.expected {
				t.Fatalf("normalizeAlipayGateway(%q) = %q, 期望 %q", c.raw, got, c.expected)
			}
		})
	}
}

// TestBuildAlipayExecutorGatewayRouting 网关地址贯通：沙箱网关生成的收银台跳转串指向沙箱且路径不重复
func TestBuildAlipayExecutorGatewayRouting(t *testing.T) {
	priKey, pubKey := newTestAlipayKeys(t)
	const sandboxGateway = "https://openapi-sandbox.dl.alipaydev.com"

	cases := []struct {
		name     string
		gateway  string
		wantBase string
	}{
		{"留空走生产网关", "", "https://openapi.alipay.com/gateway.do"},
		{"沙箱网关", sandboxGateway, sandboxGateway + "/gateway.do"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := configuredAlipayConfig(priKey, pubKey)
			cfg.GatewayURL = c.gateway
			executor := buildAlipayExecutor(cfg)
			if executor == nil {
				t.Fatalf("配置齐全时应构建执行器")
			}
			payURL, err := executor.PageExecute("alipay.trade.wap.pay", "GET", map[string]interface{}{
				"biz_content": map[string]interface{}{"out_trade_no": "PAY202601010001", "total_amount": "12.34"},
				"notify_url":  cfg.NotifyURL,
			}, "", "", nil)
			if err != nil {
				t.Fatalf("生成收银台跳转串失败: %v", err)
			}
			if !strings.HasPrefix(payURL, c.wantBase+"?") {
				t.Fatalf("跳转串前缀 = %s，期望 %s?", payURL[:min(len(payURL), 80)], c.wantBase)
			}
			// 网关带 /gateway.do 时 SDK 会再拼一次，出现双路径即为规范化失效
			if strings.Contains(payURL, "/gateway.do/gateway.do") {
				t.Fatalf("跳转串出现重复网关路径: %s", payURL)
			}
		})
	}
}

// TestAppendQueryParam 回跳地址追加支付单号：hash 路由结果页凭单号轮询公开状态接口
func TestAppendQueryParam(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			"hash 路由无 query 时用 ? 追加",
			"https://demo.example.com/s/#/pages/pay/result",
			"https://demo.example.com/s/#/pages/pay/result?no=ZF123",
		},
		{
			"已有 query 时用 & 追加",
			"https://demo.example.com/s/#/pages/pay/result?from=alipay",
			"https://demo.example.com/s/#/pages/pay/result?from=alipay&no=ZF123",
		},
		{"空串原样返回", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := appendQueryParam(c.raw, "no", "ZF123"); got != c.want {
				t.Fatalf("appendQueryParam = %q, want %q", got, c.want)
			}
		})
	}
}

// TestAlipayV3BizError V3 2xx 响应体业务结果判定
// 核心回归点：V3 正常响应体不含 code 字段，必须视为业务成功；
// 仅当响应体携带 code 且非 10000 时才判失败（网关异常以 200 返回错误体的场景）。
// 此前用 IsBizSuccess(resp["code"]) 判定，正常响应恒判失败，
// 导致对账查单查到 TRADE_SUCCESS 仍被静默跳过、退款成功被记失败
func TestAlipayV3BizError(t *testing.T) {
	cases := []struct {
		name       string
		resp       map[string]interface{}
		wantFailed bool
		wantCode   string
		wantSub    string
	}{
		{"V3 正常响应无 code 字段按成功", map[string]interface{}{"trade_status": "TRADE_SUCCESS", "total_amount": "0.01"}, false, "", ""},
		{"网关异常以 200 返回错误体", map[string]interface{}{"code": "40004", "sub_code": "ACQ.TRADE_NOT_EXIST", "sub_msg": "交易不存在"}, true, "40004", "ACQ.TRADE_NOT_EXIST"},
		{"code 为 10000 视为成功", map[string]interface{}{"code": "10000"}, false, "10000", ""},
		{"空响应体按成功", map[string]interface{}{}, false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			failed, code, subCode, _ := alipayV3BizError(c.resp)
			if failed != c.wantFailed {
				t.Fatalf("failed = %v, want %v", failed, c.wantFailed)
			}
			if code != c.wantCode {
				t.Fatalf("code = %q, want %q", code, c.wantCode)
			}
			if subCode != c.wantSub {
				t.Fatalf("sub_code = %q, want %q", subCode, c.wantSub)
			}
		})
	}
}

// TestAlipayV3ApiErrBody V3 非 2xx 异常响应体解析
// 覆盖：错误码在 body 中的标准形态、空 body、非法 JSON 均安全返回
func TestAlipayV3ApiErrBody(t *testing.T) {
	t.Run("4xx 错误体可解析出 sub_code", func(t *testing.T) {
		apiErr := alipaysdk.NewApiException(400, "bad request", nil,
			`{"code":"ACQ.TRADE_NOT_EXIST","sub_code":"ACQ.TRADE_NOT_EXIST","sub_msg":"交易不存在"}`)
		body := alipayV3ApiErrBody(apiErr)
		if body == nil {
			t.Fatal("body = nil, want 解析结果")
		}
		if got := alipayRespString(body, "sub_code"); got != "ACQ.TRADE_NOT_EXIST" {
			t.Fatalf("sub_code = %q, want ACQ.TRADE_NOT_EXIST", got)
		}
	})
	t.Run("空 body 返回 nil", func(t *testing.T) {
		if body := alipayV3ApiErrBody(alipaysdk.NewApiException(500, "oops", nil, "")); body != nil {
			t.Fatalf("body = %v, want nil", body)
		}
	})
	t.Run("非法 JSON 返回 nil", func(t *testing.T) {
		if body := alipayV3ApiErrBody(alipaysdk.NewApiException(500, "oops", nil, "not-json")); body != nil {
			t.Fatalf("body = %v, want nil", body)
		}
	})
	t.Run("nil 异常返回 nil", func(t *testing.T) {
		if body := alipayV3ApiErrBody(nil); body != nil {
			t.Fatalf("body = %v, want nil", body)
		}
	})
}
