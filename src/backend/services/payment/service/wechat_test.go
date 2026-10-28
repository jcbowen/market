package service

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/peer"
)

/**
 * 微信支付纯函数离线单测（2026-10-25 全栈审查 E3）
 *
 * 背景：本机无微信商户证书/密钥，wechat.go 走渠道路径的分支（JSAPIPay/H5Pay/Refund/
 * VerifyNotification）无法离线构造，此前整条文件零覆盖。本文件只锁「静态可验证」的
 * 纯函数：签名串格式与可验签性、私钥解析双格式、随机串、客户端 IP 提取、未配置守卫。
 *
 * 覆盖清单（均无外部依赖，CI 可跑）：
 *   - TestParseRSAPrivateKey   PKCS#1 / PKCS#8 / 非 PEM 垃圾输入 / EC 私钥类型错配
 *   - TestSignWechatPayParams  签名串 appId\ntimeStamp\nnonceStr\npackage\n 格式，
 *                              产出可用公钥 VerifyPKCS1v15 验真；坏私钥错误透传
 *   - TestRandomNonceStr       长度、字符集白名单、批量不重复
 *   - TestGetClientIP          无 peer / 有 peer / Addr 为 nil 三态
 *   - TestParseWechatCallbackNotConfigured 商户未配置时短路报错，不 panic
 */

// testRSAKeyPEM 生成一枚测试 RSA 私钥并按指定封装编码为 PEM
// 参数：
//   - t: 测试上下文（失败即 Fatal）
//   - pkcs8: true 输出 PKCS#8（PRIVATE KEY），false 输出 PKCS#1（RSA PRIVATE KEY）
//
// 返回值：
//   - *rsa.PrivateKey: 生成的私钥（供调用方用公钥验签）
//   - string: PEM 文本（供 parseRSAPrivateKey/signWechatPayParams 消费）
//   - error: 生成失败时返回（调用方 Fatal）
func testRSAKeyPEM(t *testing.T, pkcs8 bool) (*rsa.PrivateKey, string, error) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", err
	}
	var block *pem.Block
	if pkcs8 {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, "", err
		}
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	} else {
		block = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	}
	return key, string(pem.EncodeToMemory(block)), nil
}

// TestParseRSAPrivateKey 私钥解析：PKCS#1/PKCS#8 双格式成功，垃圾输入与 EC 私钥报错
func TestParseRSAPrivateKey(t *testing.T) {
	t.Run("PKCS1 成功", func(t *testing.T) {
		_, pemStr, err := testRSAKeyPEM(t, false)
		if err != nil {
			t.Fatalf("生成 PKCS#1 私钥失败: %v", err)
		}
		key, err := parseRSAPrivateKey(pemStr)
		if err != nil {
			t.Fatalf("parseRSAPrivateKey(PKCS#1) 报错: %v", err)
		}
		if key.N.BitLen() != 2048 {
			t.Errorf("私钥长度 = %d, want 2048", key.N.BitLen())
		}
	})

	t.Run("PKCS8 成功", func(t *testing.T) {
		_, pemStr, err := testRSAKeyPEM(t, true)
		if err != nil {
			t.Fatalf("生成 PKCS#8 私钥失败: %v", err)
		}
		if _, err := parseRSAPrivateKey(pemStr); err != nil {
			t.Fatalf("parseRSAPrivateKey(PKCS#8) 报错: %v", err)
		}
	})

	t.Run("非 PEM 输入报错", func(t *testing.T) {
		if _, err := parseRSAPrivateKey("不是私钥"); err == nil {
			t.Fatal("期望垃圾输入报错，实际返回 nil")
		}
	})

	t.Run("EC 私钥类型错配报错", func(t *testing.T) {
		ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("生成 EC 私钥失败: %v", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(ecKey)
		if err != nil {
			t.Fatalf("PKCS#8 封装 EC 私钥失败: %v", err)
		}
		pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		if _, err := parseRSAPrivateKey(pemStr); err == nil ||
			!strings.Contains(err.Error(), "不是 RSA") {
			t.Fatalf("期望「私钥类型不是 RSA」，实际: %v", err)
		}
	})
}

// TestSignWechatPayParams JSAPI 调起签名：验签通过即证明签名串拼接格式正确
func TestSignWechatPayParams(t *testing.T) {
	key, pemStr, err := testRSAKeyPEM(t, false)
	if err != nil {
		t.Fatalf("生成测试私钥失败: %v", err)
	}

	params := wechatPayParams{
		AppID:     "wxAPPID",
		TimeStamp: "1730000000",
		NonceStr:  "abcdefABCDEF123456",
		Package:   "prepay_id=wx20251025XYZ",
		SignType:  "RSA",
	}

	signB64, err := signWechatPayParams(params, pemStr)
	if err != nil {
		t.Fatalf("signWechatPayParams 报错: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(signB64)
	if err != nil {
		t.Fatalf("签名不是合法 Base64: %v", err)
	}
	if len(sig) != key.Size() {
		t.Fatalf("签名长度 = %d, want %d", len(sig), key.Size())
	}

	// 按生产代码约定的签名串逐字段拼接（appId\ntimeStamp\nnonceStr\npackage\n），
	// 公钥验签通过即锁定格式：任何字段漏拼、分隔符改成别的、末尾换行丢失都会验签失败
	message := params.AppID + "\n" + params.TimeStamp + "\n" + params.NonceStr + "\n" + params.Package + "\n"
	hash := sha256.Sum256([]byte(message))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], sig); err != nil {
		t.Fatalf("公钥验签失败（签名串格式与预期不一致）: %v", err)
	}

	t.Run("坏私钥错误透传", func(t *testing.T) {
		if _, err := signWechatPayParams(params, "-----BEGIN RSA PRIVATE KEY-----\nZm9v\n-----END RSA PRIVATE KEY-----\n"); err == nil {
			t.Fatal("期望非法私钥报错，实际返回 nil")
		}
	})
}

// TestRandomNonceStr 随机串：长度精确、只含字母数字、批量不重复
func TestRandomNonceStr(t *testing.T) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for _, n := range []int{1, 16, 32} {
		s := randomNonceStr(n)
		if len(s) != n {
			t.Fatalf("randomNonceStr(%d) 长度 = %d", n, len(s))
		}
		for _, r := range s {
			if !strings.ContainsRune(charset, r) {
				t.Fatalf("随机串含白名单外字符 %q", r)
			}
		}
	}

	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		s := randomNonceStr(32)
		if seen[s] {
			t.Fatalf("随机串重复: %s", s)
		}
		seen[s] = true
	}
}

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
// 背景：验签通过只证明报文由微信发出，不证明交易属于本商户（证书/APIv3 密钥复用或泄露时）
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
	// 未配置微信支付时不比对商户号/AppID，但币种口径与配置无关，外币仍拒
	if err := verifyWechatCallbackIdentity(mk("x", "y", "USD"), "", ""); err == nil {
		t.Fatal("外币单在空配置下仍应拒绝")
	}
}
