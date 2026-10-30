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

// TestSavePayConfigRowIdentityUnique 配置行身份键唯一性（P1-1 的模型层防线）
// 微信按商户号唯一、支付宝按 AppID 唯一，两侧刻意不对称：微信 AppID 是渠道身份（一个商户号可关联
// 小程序/H5 等多个 AppID，配置行不再持有该字段），支付宝 AppID 是签名身份（一个 AppID 一套密钥对）。
// 覆盖：同商户号第二条微信配置被拒、同 AppID 第二条支付宝配置被拒、跨平台同串不冲突、
// 身份键为空（凭证未配齐的占位行）不参与校验、更新自身不与自身冲突
//
// 参数：
//   - t: 测试对象
//
// 异常：
//   - 唯一性校验失效或误伤（应拒绝的保存成功、或合法的保存被拒）时使测试失败
func TestSavePayConfigRowIdentityUnique(t *testing.T) {
	svc, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()

	ctx := context.Background()
	// 时间戳后缀避免与库中既有行（含本地开发库的真实配置）撞号
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	mchA := "mch-test-" + suffix
	appA := "app-test-" + suffix

	// 测试自建的配置行必须删干净：测试库同时是本地开发库，
	// 残留行会在后台「支付配置」页显示出来，干扰手工验证
	var created []int64
	defer func() {
		if len(created) > 0 {
			svc.db.GetDb().Where("id IN ?", created).Delete(&model.PayConfig{})
		}
	}()

	wechatFirst, err := svc.SavePayConfigRow(ctx, &payment.PayConfigRow{
		Name: "测试微信", Platform: model.PayPlatformWechat, WechatMchId: mchA,
	})
	if err != nil {
		t.Fatalf("保存首条微信配置失败: %v", err)
	}
	created = append(created, wechatFirst.ID)

	alipayFirst, err := svc.SavePayConfigRow(ctx, &payment.PayConfigRow{
		Name: "测试支付宝", Platform: model.PayPlatformAlipay, AlipayAppId: appA,
	})
	if err != nil {
		t.Fatalf("保存首条支付宝配置失败: %v", err)
	}
	created = append(created, alipayFirst.ID)

	// 乐观锁版本号必须取库中读回的值：Save 返回的是刚写入的内存行（2006-01-02 15:04:05），
	// DSN 带 parseTime=True 时 datetime 列读回是 RFC3339（2026-10-30T15:48:18+08:00），
	// 用内存值回传会被判成「期间已被他人改写」——后台前端的版本号同样来自读取接口，故此处按读取口径取
	wechatReloaded, err := svc.GetPayConfigRow(ctx, wechatFirst.ID)
	if err != nil {
		t.Fatalf("读取微信配置失败: %v", err)
	}

	cases := []struct {
		name       string
		in         *payment.PayConfigRow
		wantSubstr string // 空表示应保存成功
	}{
		{
			name:       "微信同商户号第二条被拒",
			in:         &payment.PayConfigRow{Name: "重复微信", Platform: model.PayPlatformWechat, WechatMchId: mchA},
			wantSubstr: "商户号",
		},
		{
			name:       "支付宝同 AppID 第二条被拒",
			in:         &payment.PayConfigRow{Name: "重复支付宝", Platform: model.PayPlatformAlipay, AlipayAppId: appA},
			wantSubstr: "AppID",
		},
		{
			name: "跨平台同串不冲突",
			in:   &payment.PayConfigRow{Name: "跨平台支付宝", Platform: model.PayPlatformAlipay, AlipayAppId: mchA},
		},
		{
			name: "身份键为空不校验",
			in:   &payment.PayConfigRow{Name: "占位微信", Platform: model.PayPlatformWechat},
		},
		{
			name: "更新自身不与自身冲突",
			in: &payment.PayConfigRow{
				Id: wechatFirst.ID, Name: "改名微信", Platform: model.PayPlatformWechat,
				WechatMchId: mchA, UpdatedAt: wechatReloaded.UpdatedAt,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row, err := svc.SavePayConfigRow(ctx, c.in)
			if c.wantSubstr != "" {
				if err == nil {
					t.Fatalf("应被唯一性校验拒绝，实际保存成功（id=%d）", row.ID)
				}
				if !strings.Contains(err.Error(), c.wantSubstr) {
					t.Fatalf("错误文案未点明冲突字段 %q: %v", c.wantSubstr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("应保存成功，实际失败: %v", err)
			}
			if row.ID != alipayFirst.ID && row.ID != wechatFirst.ID {
				created = append(created, row.ID)
			}
		})
	}
}
