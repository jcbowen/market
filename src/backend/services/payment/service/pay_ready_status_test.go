package service

import (
	"strings"
	"testing"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 支付配置行就绪原因离线测试（无 DB、无网络，CI 可跑）
 *
 * 覆盖：
 *   - WechatConfigStatus / AlipayConfigStatus  「凭证齐全、仅缺异步通知地址」与
 *     「凭证本身不全」两种未就绪原因的区分
 *
 * 背景：异步通知地址是后台只读的部署域名派生字段（回调路径固定在网关路由上，不接受人工填写），
 * 未配置显式部署域名时必然为空。两种原因的处置动作完全不同——前者要去站点设置补域名，
 * 后者要补凭证，混成一句「必填配置不完整」会让管理员对着不可编辑的字段无从下手。
 */

// TestWechatConfigStatusMissingDeployBase 微信行凭证齐全但缺通知地址时，原因指向部署域名
func TestWechatConfigStatusMissingDeployBase(t *testing.T) {
	full := &model.PayConfig{
		Platform:           model.PayPlatformWechat,
		WechatMchID:        "1600000000",
		WechatAPIV3Key:     "0123456789abcdef0123456789abcdef",
		WechatCertSerialNo: "TEST-SERIAL",
		WechatPrivateKey:   "TEST-PRIVATE-PEM",
	}
	ready, reason := WechatConfigStatus(full)
	if ready || !strings.Contains(reason, "部署域名") {
		t.Fatalf("仅缺通知地址应未就绪且原因指向部署域名，实际 ready=%v reason=%q", ready, reason)
	}

	// 凭证本身不全（缺商户号）时给通用必填清单，不与「只缺通知地址」混为一谈
	credMissing := *full
	credMissing.WechatMchID = ""
	if _, reason := WechatConfigStatus(&credMissing); !strings.Contains(reason, "不完整") || strings.Contains(reason, "部署域名") {
		t.Fatalf("凭证不全应给通用必填清单，实际: %s", reason)
	}

	// 通知地址补齐即就绪，确认新分支没有把可用配置误判为未就绪
	withNotify := *full
	withNotify.WechatNotifyURL = "https://shop.example.com/api/v1/payments/callback/wechat"
	if ready, reason := WechatConfigStatus(&withNotify); !ready || reason != "" {
		t.Fatalf("凭证与通知地址齐全应判就绪，实际 ready=%v reason=%q", ready, reason)
	}
}

// TestAlipayConfigStatusMissingDeployBase 支付宝行凭证齐全但缺通知地址时，原因指向部署域名
// 公钥与证书两种加签方式都要覆盖：必填字段集按模式分流，判定不能只看公钥模式那一组
func TestAlipayConfigStatusMissingDeployBase(t *testing.T) {
	public := &model.PayConfig{
		Platform:         model.PayPlatformAlipay,
		AlipayMode:       config.AlipayModePublic,
		AlipayAppID:      "2021000000000000",
		AlipayPrivateKey: "TEST-PRIVATE-PEM",
		AlipayPublicKey:  "TEST-ALIPAY-PUBLIC-KEY",
	}
	if ready, reason := AlipayConfigStatus(public); ready || !strings.Contains(reason, "部署域名") {
		t.Fatalf("公钥模式仅缺通知地址应未就绪且原因指向部署域名，实际 ready=%v reason=%q", ready, reason)
	}

	certMode := *public
	certMode.AlipayMode = config.AlipayModeCert
	certMode.AlipayPublicKey = ""
	certMode.AlipayAppCert = "TEST-APP-CERT"
	certMode.AlipayPublicCert = "TEST-ALIPAY-PUBLIC-CERT"
	certMode.AlipayRootCert = "TEST-ROOT-CERT"
	if ready, reason := AlipayConfigStatus(&certMode); ready || !strings.Contains(reason, "部署域名") {
		t.Fatalf("证书模式仅缺通知地址应未就绪且原因指向部署域名，实际 ready=%v reason=%q", ready, reason)
	}

	// 证书模式缺应用私钥：仍走「必填不完整」清单，确认新分支不会把缺凭证粉饰成缺域名
	noKey := certMode
	noKey.AlipayPrivateKey = ""
	if _, reason := AlipayConfigStatus(&noKey); !strings.Contains(reason, "不完整") || strings.Contains(reason, "部署域名") {
		t.Fatalf("缺凭证应给按模式的必填清单，实际: %s", reason)
	}
}
