package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 支付证书上传写入口径回归测试（直连测试库，env 守卫跳过）
 *
 * 运行方式（TEST_MYSQL_PASSWORD 未设置时自动 skip）：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/payment/service/ -run TestUploadPayCert -v
 *
 * 背景（审查 P2-3）：UploadPayCert 原为「读取整行 → 改证书字段 → 整行 Save」，
 * 与并发保存互相覆写对方的列；改为「只写证书列 + WHERE id AND updated_at=读取时版本」的
 * 条件更新后，本组用例锁定三条口径：
 *   - 只落证书列与乐观锁版本，其余列原样保留；
 *   - 版本号确实推进（否则注册表按「版本未变」复用缓存，新证书不会生效）；
 *   - 校验分支（类型不支持/扩展名/空内容）不落库、不推进版本。
 */

// newCertTestConfig 建一条测试用微信支付配置行并返回读取时快照
// 走 SavePayConfigRow 而非直插：与真实建行走同一条写路径，列默认值与版本号口径一致
//
// 参数：
//   - t: 测试对象
//   - svc: 被测支付服务
//   - created: 收集自建行 ID，供用例结束后删净
//
// 返回值：
//   - *model.PayConfig: 建好后的配置行（按读取口径，updated_at 即乐观锁版本）
func newCertTestConfig(t *testing.T, svc *PaymentService, created *[]int64) *model.PayConfig {
	t.Helper()
	ctx := context.Background()
	row, err := svc.SavePayConfigRow(ctx, &payment.PayConfigRow{
		Name:            "证书上传测试微信",
		Platform:        model.PayPlatformWechat,
		WechatMchId:     fmt.Sprintf("mch-cert-%d", time.Now().UnixNano()),
		WechatNotifyUrl: "https://cert-test.example.com/notify",
	})
	if err != nil {
		t.Fatalf("创建证书测试配置失败: %v", err)
	}
	*created = append(*created, row.ID)
	// 版本号按读取口径取（写回内存值是「Y-m-d H:i:s」，parseTime 下读回是 RFC3339）
	reloaded, err := svc.GetPayConfigRow(ctx, row.ID)
	if err != nil {
		t.Fatalf("读取证书测试配置失败: %v", err)
	}
	return reloaded
}

// TestUploadPayCertWritesOnlyCertColumn 上传只写证书列并推进乐观锁版本，其余列不受影响
func TestUploadPayCertWritesOnlyCertColumn(t *testing.T) {
	svc, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	var created []int64
	defer func() {
		if len(created) > 0 {
			svc.db.GetDb().Where("id IN ?", created).Delete(&model.PayConfig{})
		}
	}()

	before := newCertTestConfig(t, svc, &created)
	pem := "-----BEGIN PRIVATE KEY-----\ncert-test-material\n-----END PRIVATE KEY-----"

	after, err := svc.UploadPayCert(ctx, &payment.UploadPayCertReq{
		ConfigId: before.ID,
		CertType: "private_key",
		Filename: "apiclient_key.pem",
		Content:  []byte(pem),
	})
	if err != nil {
		t.Fatalf("上传商户私钥失败: %v", err)
	}
	if after.WechatPrivateKey != pem {
		t.Fatalf("私钥未写入：回传值 = %q", after.WechatPrivateKey)
	}
	if after.UpdatedAt == before.UpdatedAt {
		t.Fatalf("乐观锁版本未推进（注册表会复用缓存不重建渠道）: %s", after.UpdatedAt)
	}
	// 其余列原样：整行覆写会把回调地址/商户号写成读取时快照（并发场景下即丢失更新）
	if after.WechatNotifyURL != "https://cert-test.example.com/notify" {
		t.Fatalf("回调地址被改写: %q", after.WechatNotifyURL)
	}
	if after.WechatMchID != before.WechatMchID || after.Name != before.Name {
		t.Fatalf("非证书列被覆写: mch=%q name=%q", after.WechatMchID, after.Name)
	}

	// 同类型再上传一次：版本继续推进，内容替换为新值（重传同一证书是后台常态）
	second, err := svc.UploadPayCert(ctx, &payment.UploadPayCertReq{
		ConfigId: before.ID, CertType: "private_key", Filename: "apiclient_key.pem", Content: []byte("re-uploaded"),
	})
	if err != nil {
		t.Fatalf("重复上传同一证书失败: %v", err)
	}
	if second.WechatPrivateKey != "re-uploaded" || second.UpdatedAt == after.UpdatedAt {
		t.Fatalf("重传后内容/版本异常: %q %s vs %s", second.WechatPrivateKey, second.UpdatedAt, after.UpdatedAt)
	}
}

// TestUploadPayCertValidationRejects 校验分支拒绝上传且不动库中行
// 覆盖：config_id 缺失、空内容、扩展名白名单、平台与证书类型不匹配
func TestUploadPayCertValidationRejects(t *testing.T) {
	svc, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	var created []int64
	defer func() {
		if len(created) > 0 {
			svc.db.GetDb().Where("id IN ?", created).Delete(&model.PayConfig{})
		}
	}()

	before := newCertTestConfig(t, svc, &created)

	cases := []struct {
		name       string
		req        *payment.UploadPayCertReq
		wantSubstr string
	}{
		{"缺少配置 ID", &payment.UploadPayCertReq{CertType: "private_key", Filename: "a.pem", Content: []byte("x")}, "缺少支付配置 ID"},
		{"内容为空", &payment.UploadPayCertReq{ConfigId: before.ID, CertType: "private_key", Filename: "a.pem"}, "证书文件内容为空"},
		{"扩展名不允许", &payment.UploadPayCertReq{ConfigId: before.ID, CertType: "private_key", Filename: "a.zip", Content: []byte("x")}, "仅支持上传"},
		{"微信不支持应用证书", &payment.UploadPayCertReq{ConfigId: before.ID, CertType: "app_cert", Filename: "a.pem", Content: []byte("x")}, "不支持上传"},
	}
	for _, tc := range cases {
		if _, err := svc.UploadPayCert(ctx, tc.req); err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
			t.Fatalf("%s: 期望报错含 %q, got %v", tc.name, tc.wantSubstr, err)
		}
	}

	// 全部拒绝后行未变：无证书内容、版本未推进
	after, err := svc.GetPayConfigRow(ctx, before.ID)
	if err != nil {
		t.Fatalf("回读配置失败: %v", err)
	}
	if after.WechatPrivateKey != "" {
		t.Fatalf("被拒上传仍写入了证书内容: %q", after.WechatPrivateKey)
	}
	if after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("被拒上传推进了版本号: %s -> %s", before.UpdatedAt, after.UpdatedAt)
	}
}
