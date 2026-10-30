package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo/component/debugger"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/debuglog"
	"github.com/jcbowen/market/src/backend/services/payment/model"

	alipaysdk "github.com/jcbowen/alipay-sdk-go-all"
	wegopayment "github.com/jcbowen/wego/payment"
)

/**
 * 支付宝证书模式离线测试（无 DB、无网络，CI 可跑）
 *
 * 覆盖：
 *   - config.AlipayConfig.IsConfigured  按加签方式分流的必填口径
 *   - checkAlipayKeys / AlipayConfigStatus  证书模式自检与就绪原因
 *   - buildAlipayExecutor  证书模式下执行器可构建、证书缺失时不构建
 *   - parseAlipayCallback  证书模式通知验签走证书公钥，序列号不匹配即拒绝
 *   - recordAlipayVerifyEvidence  验签失败的 debugger 取证记录（序列号判据、待验签串与 sign 指纹）
 *   - checkAlipayUploadContent  上传即校验，传错文件不入库
 *   - checkNotifyURL  通知地址格式服务端兜底
 *
 * 证书由测试内自签生成，只验证「可解析 + 序列号匹配」链路，不涉及真实支付宝证书。
 */

// newTestAlipayCert 生成一张自签 RSA 证书（模拟应用公钥证书/支付宝公钥证书/根证书段）
// 参数：
//   - t: 测试对象
//   - serial: 证书序列号，用于构造序列号不匹配的负向用例
//
// 返回值：
//   - string: PEM 格式证书内容
//   - *rsa.PrivateKey: 与证书配对的私钥，用于伪造支付宝侧签名
func newTestAlipayCert(t *testing.T, serial int64) (string, *rsa.PrivateKey) {
	t.Helper()
	pri, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试证书密钥失败: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject: pkix.Name{
			CommonName:   "Alipay Dev Cert Test",
			Organization: []string{"Alipay"},
			Country:      []string{"CN"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &pri.PublicKey, pri)
	if err != nil {
		t.Fatalf("生成测试证书失败: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), pri
}

// certAlipayConfig 证书模式配置载体（应用私钥与三证书齐备）
// 参数：
//   - t: 测试对象
//   - privateKey: 应用私钥（裸 base64）
//
// 返回值：
//   - config.AlipayConfig: 配置载体，第二返回值为其三证书内容
func certAlipayConfig(t *testing.T, privateKey string) config.AlipayConfig {
	t.Helper()
	appCert, _ := newTestAlipayCert(t, 10001)
	alipayCert, _ := newTestAlipayCert(t, 10002)
	rootCert, _ := newTestAlipayCert(t, 10003)
	return config.AlipayConfig{
		AppID:            testAlipayAppID,
		PrivateKey:       privateKey,
		Mode:             config.AlipayModeCert,
		NotifyURL:        "https://market.example.com/api/v1/payments/callback/alipay",
		AppCert:          appCert,
		AlipayPublicCert: alipayCert,
		AlipayRootCert:   rootCert,
	}
}

// TestAlipayIsConfiguredByMode 必填口径按加签方式分流：公钥模式需支付宝公钥，证书模式需三证书
func TestAlipayIsConfiguredByMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.AlipayConfig
		want bool
	}{
		{"公钥模式字段齐全", config.AlipayConfig{
			AppID: testAlipayAppID, PrivateKey: "k", AlipayPublicKey: "p", NotifyURL: "https://example.com/n",
		}, true},
		{"公钥模式缺支付宝公钥", config.AlipayConfig{
			AppID: testAlipayAppID, PrivateKey: "k", NotifyURL: "https://example.com/n",
		}, false},
		{"证书模式三证书齐全", config.AlipayConfig{
			AppID: testAlipayAppID, PrivateKey: "k", Mode: config.AlipayModeCert, NotifyURL: "https://example.com/n",
			AppCert: "a", AlipayPublicCert: "b", AlipayRootCert: "c",
		}, true},
		{"证书模式缺根证书", config.AlipayConfig{
			AppID: testAlipayAppID, PrivateKey: "k", Mode: config.AlipayModeCert, NotifyURL: "https://example.com/n",
			AppCert: "a", AlipayPublicCert: "b",
		}, false},
		{"证书模式忽略公钥残留但需证书", config.AlipayConfig{
			AppID: testAlipayAppID, PrivateKey: "k", Mode: config.AlipayModeCert, NotifyURL: "https://example.com/n",
			AlipayPublicKey: "残留公钥",
		}, false},
		{"公钥模式忽略证书残留但需公钥", config.AlipayConfig{
			AppID: testAlipayAppID, PrivateKey: "k", NotifyURL: "https://example.com/n",
			AppCert: "a", AlipayPublicCert: "b", AlipayRootCert: "c",
		}, false},
		{"模式空串按公钥模式（存量兼容）", config.AlipayConfig{
			AppID: testAlipayAppID, PrivateKey: "k", AlipayPublicKey: "p", NotifyURL: "https://example.com/n",
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.IsConfigured(); got != c.want {
				t.Fatalf("IsConfigured() = %v, 期望 %v", got, c.want)
			}
		})
	}
}

// TestCheckAlipayKeysCertMode 证书模式自检：三证书可解析即通过，逐张传错位置均能报出对应原因
func TestCheckAlipayKeysCertMode(t *testing.T) {
	priKey, _ := newTestAlipayKeys(t)

	cfg := certAlipayConfig(t, priKey)
	if msg := checkAlipayKeys(cfg); msg != "" {
		t.Fatalf("合法证书模式自检应通过，实际: %s", msg)
	}

	cases := []struct {
		name     string
		mutate   func(*config.AlipayConfig)
		contains string
	}{
		{"应用证书位置放了私钥", func(c *config.AlipayConfig) { c.AppCert = c.PrivateKey }, "应用公钥证书"},
		{"支付宝公钥证书位置放了文本", func(c *config.AlipayConfig) { c.AlipayPublicCert = "not-a-cert" }, "支付宝公钥证书"},
		{"根证书内容乱码", func(c *config.AlipayConfig) { c.AlipayRootCert = "garbage" }, "支付宝根证书"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bad := certAlipayConfig(t, priKey)
			c.mutate(&bad)
			msg := checkAlipayKeys(bad)
			if msg == "" {
				t.Fatalf("非法证书应自检失败")
			}
			if !strings.Contains(msg, c.contains) {
				t.Fatalf("失败原因应包含 %q，实际: %s", c.contains, msg)
			}
		})
	}
}

// TestAlipayConfigStatusCertMode 证书模式就绪口径：模式切换后未补齐证书即判未就绪
func TestAlipayConfigStatusCertMode(t *testing.T) {
	priKey, _ := newTestAlipayKeys(t)
	carrier := certAlipayConfig(t, priKey)

	full := &model.PayConfig{
		AlipayMode:       config.AlipayModeCert,
		AlipayAppID:      testAlipayAppID,
		AlipayPrivateKey: priKey,
		AlipayNotifyURL:  carrier.NotifyURL,
		AlipayAppCert:    carrier.AppCert,
		AlipayPublicCert: carrier.AlipayPublicCert,
		AlipayRootCert:   carrier.AlipayRootCert,
		AlipayPublicKey:  "公钥模式残留值不参与证书模式判定",
	}
	if ready, reason := AlipayConfigStatus(full); !ready || reason != "" {
		t.Fatalf("证书模式齐全应就绪，实际 ready=%v reason=%q", ready, reason)
	}

	// 同一份数据切回公钥模式：残留公钥不合法，须判未就绪（fail-closed）
	publicMode := *full
	publicMode.AlipayMode = config.AlipayModePublic
	if ready, reason := AlipayConfigStatus(&publicMode); ready || reason == "" {
		t.Fatalf("切回公钥模式未补齐有效公钥应未就绪，实际 ready=%v reason=%q", ready, reason)
	}

	missing := *full
	missing.AlipayRootCert = ""
	ready, reason := AlipayConfigStatus(&missing)
	if ready {
		t.Fatalf("缺根证书不得判就绪")
	}
	if !strings.Contains(reason, "不完整") {
		t.Fatalf("原因应提示必填不完整，实际: %s", reason)
	}
}

// TestBuildAlipayExecutorCertMode 证书模式执行器：证书齐全可构建，缺证书不构建（C 端隐藏支付宝）
func TestBuildAlipayExecutorCertMode(t *testing.T) {
	priKey, _ := newTestAlipayKeys(t)

	if buildAlipayExecutor(certAlipayConfig(t, priKey)) == nil {
		t.Fatalf("证书模式应能构建执行器")
	}

	incomplete := certAlipayConfig(t, priKey)
	incomplete.AlipayPublicCert = ""
	if buildAlipayExecutor(incomplete) != nil {
		t.Fatalf("证书缺失时不应构建执行器")
	}
}

// TestParseAlipayCallbackCertMode 证书模式回调验签：证书序列号命中即通过，不匹配一律拒绝；
// 缺 alipay_cert_sn 用本证书公钥直验（通过与否取决于签名是否出自本证书私钥）
func TestParseAlipayCallbackCertMode(t *testing.T) {
	priKey, _ := newTestAlipayKeys(t)
	carrier := certAlipayConfig(t, priKey)
	alipayCert, alipayPri := newTestAlipayCert(t, 20260)

	certSN, err := alipaysdk.CertSN(alipayCert)
	if err != nil {
		t.Fatalf("计算测试证书序列号失败: %v", err)
	}

	s := &PaymentService{cfg: &config.Config{}}
	// 证书模式回调按 app_id 从支付配置注册表路由（多行制）：
	// 测试无 DB，GetPayConfigs 失败时 getRegistryCached 回落 s.registry 缓存，直接注入配置行
	s.registry = &payClientRegistry{
		version: "test",
		rows: map[int64]*model.PayConfig{
			1: {
				ID:               1,
				Platform:         model.PayPlatformAlipay,
				AlipayMode:       config.AlipayModeCert,
				AlipayAppID:      testAlipayAppID,
				AlipayPrivateKey: priKey,
				// 验签取支付宝公钥证书与根证书，公钥模式残留公钥不参与
				AlipayPublicKey:  "%%%bad-key%%%",
				AlipayPublicCert: alipayCert,
				AlipayRootCert:   carrier.AlipayRootCert,
			},
		},
		wechat: map[int64]*wegopayment.Client{},
		alipay: map[int64]*alipaysdk.GenericExecuteApi{},
	}

	fields := successNotifyFields(testAlipayAppID)
	fields["alipay_cert_sn"] = certSN
	got, err := s.parseAlipayCallback(context.Background(), signedNotify(t, alipayPri, fields))
	if err != nil {
		t.Fatalf("证书模式合法通知应验签通过: %v", err)
	}
	if got.PaymentNo != fields["out_trade_no"] || got.TransactionID != fields["trade_no"] {
		t.Fatalf("通知解析结果不符: %+v", got)
	}

	wrongSN := successNotifyFields(testAlipayAppID)
	wrongSN["alipay_cert_sn"] = "0123456789abcdef0123456789abcdef"
	if _, err := s.parseAlipayCallback(context.Background(), signedNotify(t, alipayPri, wrongSN)); err == nil ||
		!strings.Contains(err.Error(), "验签失败") {
		t.Fatalf("序列号不匹配应拒绝通知，实际: %v", err)
	}

	// 漏传 alipay_cert_sn：官方从不承诺通知带该字段（SDK v0.0.6 起按本商户支付宝公钥证书公钥直验）
	noSN := successNotifyFields(testAlipayAppID)
	if _, err := s.parseAlipayCallback(context.Background(), signedNotify(t, alipayPri, noSN)); err != nil {
		t.Fatalf("缺 alipay_cert_sn 的本证书合法通知应验签通过: %v", err)
	}

	// 缺字段不等于放宽验签钥匙：非本商户证书私钥签的通知仍须拒绝
	_, foreignPri := newTestAlipayCert(t, 20262)
	if _, err := s.parseAlipayCallback(context.Background(), signedNotify(t, foreignPri, successNotifyFields(testAlipayAppID))); err == nil {
		t.Fatalf("非本商户支付宝公钥证书签发的通知应拒绝")
	}
}

// TestCheckAlipayUploadContent 上传即校验：四类内容合法通过，传错文件给出对应原因
func TestCheckAlipayUploadContent(t *testing.T) {
	priKey, _ := newTestAlipayKeys(t)
	certPEM, _ := newTestAlipayCert(t, 30001)
	// 模拟真实支付宝根证书里的国密（SM2）段：PEM 结构合法但 Go 标准库解不出证书体
	badBlockPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not-a-der-cert")}))

	cases := []struct {
		name     string
		certType string
		content  string
		wantMsg  string
	}{
		{"私钥合法", "private_key", priKey, ""},
		{"私钥乱码", "private_key", "not-a-private-key", "应用私钥"},
		{"应用公钥证书合法", "app_cert", certPEM, ""},
		{"应用公钥证书放了私钥", "app_cert", priKey, "应用公钥证书"},
		{"支付宝公钥证书合法", "alipay_public_cert", certPEM, ""},
		{"支付宝公钥证书非证书", "alipay_public_cert", "not-a-cert", "支付宝公钥证书"},
		{"根证书合法", "root_cert", certPEM, ""},
		{"根证书含不可解析段仍可用", "root_cert", badBlockPEM + certPEM, ""},
		{"根证书全为不可解析段", "root_cert", badBlockPEM, "支付宝根证书"},
		{"根证书乱码", "root_cert", "garbage", "支付宝根证书"},
		{"未知类型拒绝", "unknown_type", "whatever", "不支持的证书类型"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := checkAlipayUploadContent(c.certType, c.content)
			if c.wantMsg == "" {
				if msg != "" {
					t.Fatalf("应校验通过，实际: %s", msg)
				}
				return
			}
			if !strings.Contains(msg, c.wantMsg) {
				t.Fatalf("原因应包含 %q，实际: %s", c.wantMsg, msg)
			}
		})
	}
}

// TestCheckNotifyURL 通知地址格式兜底：允许留空，拒绝相对地址与缺协议地址
func TestCheckNotifyURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"留空表示未配置", "", false},
		{"https 完整地址", "https://market.example.com/api/v1/payments/callback/alipay", false},
		{"http 完整地址", "http://127.0.0.1:8080/api/v1/payments/callback/alipay", false},
		{"缺协议", "market.example.com/api/v1/payments/callback/alipay", true},
		{"相对路径", "/api/v1/payments/callback/alipay", true},
		{"非 http 协议", "ftp://market.example.com/callback", true},
		{"含空格", "https://market.example.com/a b", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkNotifyURL("支付宝异步通知地址", c.raw)
			if c.wantErr && err == nil {
				t.Fatalf("非法地址应返回错误")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("合法地址不应报错，实际: %v", err)
			}
		})
	}
}

/**
 * TestParseAlipayCallbackRecordsVerifyEvidence 验签失败的 debugger 取证记录
 *
 * 覆盖（故障形态取「证书不同源」——通知不带 alipay_cert_sn 且签名不出自本证书私钥；
 * 缺该字段本身自 SDK v0.0.6 起不再判失败，不再作为取证入口）：
 *   - 记录内容与判据：alipay_mode、通知侧 alipay_cert_sn、本地证书序列号与是否命中、
 *     待验签串长度与指纹、sign 长度与指纹、参数条数与失败原因；
 *   - 待验签串指纹与测试内独立实现的拼接规则一致，证明复算未丢字段；
 *   - debugger 未登记（实例为 nil）时验签失败只走标准日志，不 panic；
 *   - 验签通过的通知不落取证记录；序列号不匹配与证书不同源两种失败各自留痕。
 *
 * 与网关的「支付宝回调」记录（失败时含全量通知原文）用 out_trade_no 对齐即完成取证闭环。
 */
func TestParseAlipayCallbackRecordsVerifyEvidence(t *testing.T) {
	priKey, _ := newTestAlipayKeys(t)
	carrier := certAlipayConfig(t, priKey)
	alipayCert, alipayPri := newTestAlipayCert(t, 20261)
	localSN, err := alipaysdk.CertSN(alipayCert)
	if err != nil {
		t.Fatalf("计算测试证书序列号失败: %v", err)
	}

	s := &PaymentService{cfg: &config.Config{}}
	s.registry = &payClientRegistry{
		version: "test",
		rows: map[int64]*model.PayConfig{
			1: {
				ID:               1,
				Platform:         model.PayPlatformAlipay,
				AlipayMode:       config.AlipayModeCert,
				AlipayAppID:      testAlipayAppID,
				AlipayPrivateKey: priKey,
				AlipayPublicCert: alipayCert,
				AlipayRootCert:   carrier.AlipayRootCert,
			},
		},
		wechat: map[int64]*wegopayment.Client{},
		alipay: map[int64]*alipaysdk.GenericExecuteApi{},
	}

	// 取证要盯的故障形态是「证书不同源」：通知不带 alipay_cert_sn，签名又不出自本证书私钥。
	// SDK v0.0.6 起缺该字段仍会用本证书公钥直验一次，签名对不上才失败（err 为 nil、ok 为 false）。
	// 此处 debugger 尚未登记，须只走标准日志不 panic。
	debuglog.SetInstance(nil)
	t.Cleanup(func() { debuglog.SetInstance(nil) })
	_, foreignPri := newTestAlipayCert(t, 20262)
	foreign := successNotifyFields(testAlipayAppID)
	if _, err := s.parseAlipayCallback(context.Background(), signedNotify(t, foreignPri, foreign)); err == nil {
		t.Fatalf("非本商户支付宝公钥证书签发的通知应拒绝")
	}

	dbg := newPaymentTestDebugger(t)
	debuglog.SetInstance(dbg)

	got := signedNotify(t, foreignPri, foreign)
	if _, err := s.parseAlipayCallback(context.Background(), got); err == nil {
		t.Fatalf("非本商户支付宝公钥证书签发的通知应拒绝")
	}

	// 同一批字段改由本证书私钥签发：缺 alipay_cert_sn 也验签通过，且不落取证记录
	if _, err := s.parseAlipayCallback(context.Background(), signedNotify(t, alipayPri, successNotifyFields(testAlipayAppID))); err != nil {
		t.Fatalf("缺 alipay_cert_sn 的本证书合法通知应验签通过: %v", err)
	}
	if _, passed, err := dbg.GetStorage().FindAll(1, 50, map[string]interface{}{"record_type": "process"}); err != nil || passed != 1 {
		t.Fatalf("验签通过不应新增取证记录，实际 total=%d err=%v", passed, err)
	}

	entries, total, err := dbg.GetStorage().FindAll(1, 50, map[string]interface{}{"record_type": "process"})
	if err != nil || total != 1 {
		t.Fatalf("应落 1 条验签取证记录，实际 total=%d err=%v", total, err)
	}
	entry := entries[0]
	if entry.ProcessName != "支付宝回调验签" || entry.ProcessType != "payment_verify_alipay" {
		t.Errorf("进程标识错误: %+v", entry)
	}
	if entry.Status != "failed" || !entry.HasError {
		t.Errorf("验签失败应为 failed 且带错误徽标，实际 status=%q hasErr=%v", entry.Status, entry.HasError)
	}

	fields := entry.LoggerLogs[0].Fields
	if fields["out_trade_no"] != got["out_trade_no"] || fields["alipay_mode"] != config.AlipayModeCert {
		t.Errorf("取证记录缺少单号或加签模式: %+v", fields)
	}
	// 判据主体：通知侧序列号为空、本地证书序列号与是否命中直接给出
	if fields["notify_cert_sn"] != "" || fields["local_cert_sn"] != localSN || fields["cert_sn_matched"] != false {
		t.Errorf("序列号判据错误: 期望 notify_cert_sn 空 / local_cert_sn=%s / matched=false，实际 %+v", localSN, fields)
	}
	if fields["content_fp"] != fp12(notifySignContent(got)) {
		t.Errorf("待验签串指纹与独立实现的拼接结果不符: %+v", fields)
	}
	if fields["sign_fp"] != fp12(got["sign"]) || fields["sign_length"] != len(got["sign"]) {
		t.Errorf("sign 指纹/长度错误: %+v", fields)
	}
	if fields["param_count"] != len(got) {
		t.Errorf("参数条数应为 %d，实际 %+v", len(got), fields["param_count"])
	}
	if reason, _ := fields["verify_error"].(string); reason == "" {
		t.Errorf("取证记录应带失败原因: %+v", fields)
	}

	// 序列号不匹配的故障形态要与「缺字段」区分开：通知侧带 SN 但与本地不等
	const wrongNotifySN = "0123456789abcdef0123456789abcdef"
	wrongSN := successNotifyFields(testAlipayAppID)
	wrongSN["alipay_cert_sn"] = wrongNotifySN
	if _, err := s.parseAlipayCallback(context.Background(), signedNotify(t, alipayPri, wrongSN)); err == nil {
		t.Fatalf("序列号不匹配的通知应拒绝")
	}

	entries, total, _ = dbg.GetStorage().FindAll(1, 50, map[string]interface{}{"record_type": "process"})
	if total != 2 {
		t.Fatalf("应落 2 条取证记录，实际 total=%d", total)
	}
	matched := false
	for _, e := range entries {
		for _, l := range e.LoggerLogs {
			if l.Fields["notify_cert_sn"] != wrongNotifySN {
				continue
			}
			matched = true
			if l.Fields["cert_sn_matched"] != false || l.Fields["local_cert_sn"] != localSN {
				t.Errorf("序列号不等应记为 matched=false 并保留本地序列号: %+v", l.Fields)
			}
		}
	}
	if !matched {
		t.Fatalf("未找到序列号不匹配的取证记录: %+v", entries)
	}
}

/**
 * newPaymentTestDebugger 内存存储的调试器实例（不连 MySQL），供进程级记录断言使用
 *
 * Level 必须显式置 Info：组件 Config.Level 默认 warn，会把 Info 级记录全部丢弃；
 * 与 gateway handler 包的同名助手保持一致（跨包测试无法复用，故各自持有）
 *
 * 参数：
 *   - t: 测试对象
 *
 * 返回值：
 *   - *debugger.Debugger: 可直接登记给 debuglog 的实例
 *
 * 异常：
 *   - 存储或实例创建失败时 t.Fatalf
 *
 * 使用示例：
 *   - dbg := newPaymentTestDebugger(t); debuglog.SetInstance(dbg)
 */
func newPaymentTestDebugger(t *testing.T) *debugger.Debugger {
	t.Helper()
	storage, err := debugger.NewMemoryStorage(200)
	if err != nil {
		t.Fatalf("创建内存存储失败: %v", err)
	}
	dbg, err := debugger.New(&debugger.Config{
		Enabled:     true,
		ServiceName: "payment-test",
		MaxRecords:  200,
		Level:       debugger.LevelInfo,
		Storage:     storage,
	})
	if err != nil {
		t.Fatalf("创建调试器实例失败: %v", err)
	}
	return dbg
}
