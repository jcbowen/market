package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/services/payment/model"

	alipaysdk "github.com/jcbowen/alipay-sdk-go-all"
)

/**
 * 支付宝证书模式离线测试（无 DB、无网络，CI 可跑）
 *
 * 覆盖：
 *   - config.AlipayConfig.IsConfigured  按加签方式分流的必填口径
 *   - checkAlipayKeys / AlipayConfigStatus  证书模式自检与就绪原因
 *   - buildAlipayExecutor  证书模式下执行器可构建、证书缺失时不构建
 *   - parseAlipayCallback  证书模式通知验签走证书公钥，序列号不匹配即拒绝
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

// TestParseAlipayCallbackCertMode 证书模式回调验签：证书序列号命中即通过，不匹配一律拒绝
func TestParseAlipayCallbackCertMode(t *testing.T) {
	priKey, _ := newTestAlipayKeys(t)
	carrier := certAlipayConfig(t, priKey)
	alipayCert, alipayPri := newTestAlipayCert(t, 20260)

	certSN, err := alipaysdk.CertSN(alipayCert)
	if err != nil {
		t.Fatalf("计算测试证书序列号失败: %v", err)
	}

	s := &PaymentService{cfg: &config.Config{}}
	s.payCfg = &model.PayConfig{
		AlipayMode:       config.AlipayModeCert,
		AlipayAppID:      testAlipayAppID,
		AlipayPrivateKey: priKey,
		// 验签取支付宝公钥证书与根证书，公钥模式残留公钥不参与
		AlipayPublicKey:  "%%%bad-key%%%",
		AlipayPublicCert: alipayCert,
		AlipayRootCert:   carrier.AlipayRootCert,
	}

	fields := successNotifyFields(testAlipayAppID)
	fields["alipay_cert_sn"] = certSN
	got, err := s.parseAlipayCallback(signedNotify(t, alipayPri, fields))
	if err != nil {
		t.Fatalf("证书模式合法通知应验签通过: %v", err)
	}
	if got.PaymentNo != fields["out_trade_no"] || got.TransactionID != fields["trade_no"] {
		t.Fatalf("通知解析结果不符: %+v", got)
	}

	wrongSN := successNotifyFields(testAlipayAppID)
	wrongSN["alipay_cert_sn"] = "0123456789abcdef0123456789abcdef"
	if _, err := s.parseAlipayCallback(signedNotify(t, alipayPri, wrongSN)); err == nil ||
		!strings.Contains(err.Error(), "验签失败") {
		t.Fatalf("序列号不匹配应拒绝通知，实际: %v", err)
	}

	// 证书模式漏传 alipay_cert_sn：无法定位公钥，须拒绝而非放行
	noSN := successNotifyFields(testAlipayAppID)
	if _, err := s.parseAlipayCallback(signedNotify(t, alipayPri, noSN)); err == nil {
		t.Fatalf("缺少 alipay_cert_sn 的通知应拒绝")
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
