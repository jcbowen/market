package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 付款码支付（收银台 B扫C）测试
 *
 * 纯函数测试（无外部依赖，CI 可跑）：
 *   - TestBuildCodePayResp  扣款结果归一化状态映射与默认文案
 *   - TestCodePayConfirmAmount 确认金额取渠道实收、缺省时回退应付额
 *   - TestNotifyChannelOf  流水渠道归一（付款码 5/6 → 微信/支付宝，线下为 0）
 *
 * 集成测试（直连本地开发库，env 守卫跳过；运行方式同 payment_test.go）：
 *   - TestCreatePaymentCodePayNotReady 渠道未就绪：创建待支付单被拦截，不落支付单
 *   - TestPayByAuthCodeSuccess         扣款成功：支付单置已支付+线下来源，订单同步已支付，落 kind=9 流水
 *   - TestPayByAuthCodeUserPaying      USERPAYING 在途：支付单保持待支付
 *   - TestPayByAuthCodeFailedRetry     扣款失败重试：复用同一支付单，重扫后成功
 *   - TestPayByAuthCodeIdempotent      已支付单重复提交：幂等回 PAID，不再调渠道
 *   - TestPayByAuthCodeGuard           守卫：非付款码单/订单绑定不匹配/渠道未配置/参数错误
 *   - TestProcessRefundCodePay         退款渠道映射：5→微信 / 6→支付宝
 *
 * 渠道扣款以包级变量注入桩，无需真实商户配置；测试数据用 testOrderIDBase 起始 ID，结束即清理。
 */

// stubCodePayResult 预设渠道扣款桩的返回值
type stubCodePayResult struct {
	state string
	txnID string
	msg   string
}

// TestBuildCodePayResp 归一化结果映射：PAID 带交易号 / PAYING 在途 / FAILED 兜底文案
func TestBuildCodePayResp(t *testing.T) {
	s := &PaymentService{}
	p := &model.Payment{PaymentNo: "ZF-TEST", OrderID: 123, PayType: 5, Amount: 12.34}

	// PAID：状态与交易号透传
	resp := s.buildCodePayResp(p, &codePayResult{State: ChannelStatePaid, TransactionID: "TX-1"})
	if resp.State != "PAID" || resp.TransactionId != "TX-1" || resp.PaymentNo != "ZF-TEST" || resp.OrderId != 123 {
		t.Errorf("PAID 映射异常: %+v", resp)
	}

	// PAYING：在途
	resp = s.buildCodePayResp(p, &codePayResult{State: ChannelStatePaying, Message: "用户支付中"})
	if resp.State != "PAYING" || resp.TransactionId != "" {
		t.Errorf("PAYING 映射异常: %+v", resp)
	}

	// FAILED：渠道原因为空时兜底通用话术
	resp = s.buildCodePayResp(p, &codePayResult{State: ChannelStateClosed})
	if resp.State != "FAILED" || resp.Message == "" {
		t.Errorf("FAILED 兜底文案缺失: %+v", resp)
	}
	// FAILED：保留渠道原因
	resp = s.buildCodePayResp(p, &codePayResult{State: ChannelStateClosed, Message: "余额不足"})
	if resp.State != "FAILED" || resp.Message != "余额不足" {
		t.Errorf("FAILED 原因应透传: %+v", resp)
	}
}

// TestCodePayConfirmAmount 确认金额取渠道实收，渠道未回传金额时回退支付单应付额
// 传应付额等于自己跟自己对账，handlePaySuccess 的分位比对会失去判定能力，故渠道值优先
func TestCodePayConfirmAmount(t *testing.T) {
	p := &model.Payment{PaymentNo: "ZF-AMT", Amount: 12.34}
	if got := codePayConfirmAmount(&codePayResult{State: ChannelStatePaid, Amount: 12.35}, p); got != 12.35 {
		t.Errorf("渠道实收 12.35 应优先于应付额, got %.2f", got)
	}
	if got := codePayConfirmAmount(&codePayResult{State: ChannelStatePaid}, p); got != 12.34 {
		t.Errorf("渠道未回传金额应回退应付额, got %.2f", got)
	}
	// 结果整体缺失（调用方兜底路径）同样回退，不能传 0 让确认被「金额缺失」拒绝
	if got := codePayConfirmAmount(nil, p); got != 12.34 {
		t.Errorf("结果为 nil 时应回退应付额, got %.2f", got)
	}
}

// TestNotifyChannelOf 流水渠道归一：付款码 5/6 分别落微信/支付宝，线下收款无渠道落 0
// 直接写 pay_type 会让异常页渠道列出现「-」之外的未知值（5/6/3/4 不在 channel 取值域内）
func TestNotifyChannelOf(t *testing.T) {
	cases := map[int32]int32{1: 1, 5: 1, 2: 2, 6: 2, 3: 0, 4: 0}
	for payType, want := range cases {
		if got := notifyChannelOf(payType); got != want {
			t.Errorf("pay_type=%d 渠道 = %d, want %d", payType, got, want)
		}
	}
}

// TestCreatePaymentCodePayPlaceholder 付款码占位单创建：建单发生在扫码前、平台未知，
// 不携带 config_id 也不校验渠道就绪（就绪校验移至扫码后的 PayByAuthCode 与网关前置校验）
func TestCreatePaymentCodePayPlaceholder(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	for _, pt := range []int32{5, 6} {
		resp, _, err := s.CreatePayment(ctx, &payment.CreatePaymentReq{
			OrderId: testOrderIDBase + 40,
			UserId:  0,
			Amount:  10.00,
			PayType: pt,
		})
		if err != nil {
			t.Fatalf("pay_type=%d 占位单创建应成功（未绑配置不拦截建单）: %v", pt, err)
		}
		var p model.Payment
		if err := s.db.GetDb().Where("payment_no = ?", resp.PaymentNo).First(&p).Error; err != nil {
			t.Fatalf("查询占位支付单失败: %v", err)
		}
		if p.Status != 1 || p.ConfigID != 0 {
			t.Errorf("占位单应待支付且 config_id=0, got status=%d config_id=%d", p.Status, p.ConfigID)
		}
		s.db.GetDb().Where("payment_no = ?", resp.PaymentNo).Delete(&model.Payment{})
	}

	// 0 元单拒收：渠道不受理 0 元交易
	_, _, err := s.CreatePayment(ctx, &payment.CreatePaymentReq{
		OrderId: testOrderIDBase + 41,
		UserId:  0,
		Amount:  0,
		PayType: 5,
	})
	if err == nil {
		t.Fatal("付款码 0 元单应被拒绝")
	}
}

// seedActiveWechatPayConfig 在测试库播种一条启用且凭证齐全的微信支付配置行，返回配置 ID
// 私钥用测试生成的真实 RSA PEM：wegopayment.NewClientE 构建时解析，假串会导致客户端构建失败、
// resolveActivePayConfig 判未就绪
func seedActiveWechatPayConfig(t *testing.T, s *PaymentService) int64 {
	t.Helper()
	pri, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试 RSA 私钥失败: %v", err)
	}
	priDER, err := x509.MarshalPKCS8PrivateKey(pri)
	if err != nil {
		t.Fatalf("序列化测试私钥失败: %v", err)
	}
	priPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priDER}))
	row := &model.PayConfig{
		ID: payConfigID + 1, Name: "test-wechat", Platform: model.PayPlatformWechat, Enabled: true,
		WechatAppID: "wx-test-app", WechatAppSecret: "test-secret", WechatMchID: "1900000001",
		WechatAPIV3Key: "01234567890123456789012345678901", WechatCertSerialNo: "TESTSERIAL0001",
		WechatPrivateKey: priPEM, WechatNotifyURL: "https://market.example.com/api/v1/payments/callback/wechat",
	}
	if err := s.db.GetDb().Where(model.PayConfig{ID: row.ID}).Assign(*row).FirstOrCreate(row).Error; err != nil {
		t.Fatalf("播种微信支付配置行失败: %v", err)
	}
	t.Cleanup(func() { s.db.GetDb().Where("id = ?", row.ID).Delete(&model.PayConfig{}) })
	return row.ID
}

// seedActiveAlipayPayConfig 在测试库播种一条启用且凭证齐全的支付宝支付配置行（公钥模式），返回配置 ID
func seedActiveAlipayPayConfig(t *testing.T, s *PaymentService) int64 {
	t.Helper()
	priKey, pubKey := newTestAlipayKeys(t)
	row := &model.PayConfig{
		ID: payConfigID + 2, Name: "test-alipay", Platform: model.PayPlatformAlipay, Enabled: true,
		AlipayAppID: testAlipayAppID, AlipayPrivateKey: priKey, AlipayPublicKey: pubKey,
		AlipayMode: config.AlipayModePublic, AlipayNotifyURL: "https://market.example.com/api/v1/payments/callback/alipay",
	}
	if err := s.db.GetDb().Where(model.PayConfig{ID: row.ID}).Assign(*row).FirstOrCreate(row).Error; err != nil {
		t.Fatalf("播种支付宝支付配置行失败: %v", err)
	}
	t.Cleanup(func() { s.db.GetDb().Where("id = ?", row.ID).Delete(&model.PayConfig{}) })
	return row.ID
}

// TestPayByAuthCodeSuccess 扣款成功：支付单置已支付+线下来源+交易号，订单同步已支付
func TestPayByAuthCodeSuccess(t *testing.T) {
	s, stub, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderID := testOrderIDBase + 42
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-CODE-PAID",
		OrderID:   orderID,
		UserID:    0,
		PayType:   5,
		Amount:    33.33,
		Status:    1,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})

	origPay := wechatAuthCodePay
	wechatAuthCodePay = func(ctx context.Context, s *PaymentService, p *model.Payment, authCode string) (*codePayResult, error) {
		return &codePayResult{State: ChannelStatePaid, TransactionID: "WX-TX-CODE-001", Amount: p.Amount}, nil
	}
	configID := seedActiveWechatPayConfig(t, s)
	defer func() { wechatAuthCodePay = origPay }()

	resp, err := s.PayByAuthCode(ctx, &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-CODE-PAID", OrderId: orderID, AuthCode: "1345235315201154982642655744", OperatorId: 9, ConfigId: configID})
	if err != nil {
		t.Fatalf("付款码扣款失败: %v", err)
	}
	if resp.State != "PAID" || resp.TransactionId != "WX-TX-CODE-001" {
		t.Fatalf("扣款结果异常: %+v", resp)
	}

	var p model.Payment
	if err := s.db.GetDb().Where("payment_no = ?", "PAYTEST-CODE-PAID").First(&p).Error; err != nil {
		t.Fatalf("查询支付单失败: %v", err)
	}
	if p.Status != 2 || p.TransactionID != "WX-TX-CODE-001" {
		t.Errorf("支付单应置已支付并记录交易号, got status=%d tx=%s", p.Status, p.TransactionID)
	}
	if p.ConfirmSource != model.ConfirmSourceOffline || p.ConfirmRef != "codepay:9" {
		t.Errorf("确认来源应为线下并记录收银员, got source=%d ref=%s", p.ConfirmSource, p.ConfirmRef)
	}
	// 付款码扣款是同步确认（无异步通知），流水单列 kind=9；记成 kind=1 会污染「回调丢失率」口径
	if count := countNotifyLogs(t, s, "PAYTEST-CODE-PAID", model.NotifyKindCodePay, model.NotifyResultSuccess); count != 1 {
		t.Errorf("扣款成功应落一条 kind=9/result=1 流水, got %d", count)
	}
	calls := stub.orderCalls()
	if len(calls) != 1 || calls[0].Id != orderID || calls[0].Status != 2 {
		t.Errorf("订单状态同步调用异常: %+v", calls)
	}
}

// TestPayByAuthCodeUserPaying USERPAYING 在途：支付单保持待支付，订单不同步
func TestPayByAuthCodeUserPaying(t *testing.T) {
	s, stub, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderID := testOrderIDBase + 43
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-CODE-PAYING",
		OrderID:   orderID,
		UserID:    0,
		PayType:   6,
		Amount:    25.00,
		Status:    1,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})

	origPay := alipayAuthCodePay
	alipayAuthCodePay = func(ctx context.Context, s *PaymentService, p *model.Payment, authCode string) (*codePayResult, error) {
		return &codePayResult{State: ChannelStatePaying, Message: "用户支付中"}, nil
	}
	configID := seedActiveAlipayPayConfig(t, s)
	defer func() { alipayAuthCodePay = origPay }()

	resp, err := s.PayByAuthCode(ctx, &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-CODE-PAYING", OrderId: orderID, AuthCode: "2801234567890123456789012345", OperatorId: 9, ConfigId: configID})
	if err != nil {
		t.Fatalf("付款码扣款失败: %v", err)
	}
	if resp.State != "PAYING" {
		t.Fatalf("应返回 PAYING, got %+v", resp)
	}

	var p model.Payment
	if err := s.db.GetDb().Where("payment_no = ?", "PAYTEST-CODE-PAYING").First(&p).Error; err != nil || p.Status != 1 {
		t.Fatalf("在途时支付单应保持待支付(1), got %d err %v", p.Status, err)
	}
	if n := len(stub.orderCalls()); n != 0 {
		t.Errorf("在途时不应同步订单状态, 同步次数 = %d", n)
	}
}

// TestPayByAuthCodeFailedRetry 扣款失败重试：渠道拒绝不落终态，复用同一支付单重扫后成功
func TestPayByAuthCodeFailedRetry(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderID := testOrderIDBase + 44
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-CODE-RETRY",
		OrderID:   orderID,
		UserID:    0,
		PayType:   5,
		Amount:    18.00,
		Status:    1,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})

	origPay := wechatAuthCodePay
	results := []stubCodePayResult{
		{state: ChannelStateClosed, msg: " insufficient credit"},
		{state: ChannelStatePaid, txnID: "WX-TX-RETRY-001"},
	}
	call := 0
	wechatAuthCodePay = func(ctx context.Context, s *PaymentService, p *model.Payment, authCode string) (*codePayResult, error) {
		r := results[call]
		call++
		return &codePayResult{State: r.state, TransactionID: r.txnID, Message: r.msg}, nil
	}
	configID := seedActiveWechatPayConfig(t, s)
	defer func() { wechatAuthCodePay = origPay }()

	req := &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-CODE-RETRY", OrderId: orderID, AuthCode: "1345235315201154982642655744", OperatorId: 9, ConfigId: configID}

	// 第一次：渠道拒绝（余额不足），支付单保持待支付
	resp, err := s.PayByAuthCode(ctx, req)
	if err != nil {
		t.Fatalf("渠道拒绝应归一化返回而非报错: %v", err)
	}
	if resp.State != "FAILED" {
		t.Fatalf("首次扣款应 FAILED, got %+v", resp)
	}
	var p model.Payment
	if err := s.db.GetDb().Where("payment_no = ?", "PAYTEST-CODE-RETRY").First(&p).Error; err != nil || p.Status != 1 {
		t.Fatalf("失败后支付单应保持待支付(1), got %d err %v", p.Status, err)
	}

	// 第二次（重新扫码）：同一支付单扣款成功
	resp, err = s.PayByAuthCode(ctx, req)
	if err != nil {
		t.Fatalf("重试扣款失败: %v", err)
	}
	if resp.State != "PAID" || resp.TransactionId != "WX-TX-RETRY-001" {
		t.Fatalf("重试应 PAID, got %+v", resp)
	}
	if call != 2 {
		t.Fatalf("两次提交都应真实触达渠道, 调用次数 = %d", call)
	}
	if err := s.db.GetDb().Where("payment_no = ?", "PAYTEST-CODE-RETRY").First(&p).Error; err != nil || p.Status != 2 {
		t.Errorf("重试成功后支付单应已支付(2), got %d err %v", p.Status, err)
	}
}

// TestPayByAuthCodeIdempotent 已支付单重复提交（扫码枪回车重放）：幂等回 PAID，不再调渠道
func TestPayByAuthCodeIdempotent(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderID := testOrderIDBase + 45
	insertPayment(t, s, &model.Payment{
		PaymentNo:     "PAYTEST-CODE-IDEM",
		OrderID:       orderID,
		UserID:        0,
		PayType:       5,
		Amount:        12.00,
		Status:        2,
		TransactionID: "WX-TX-IDEM-001",
		CreatedAt:     "2026-01-01 00:00:00",
		UpdatedAt:     "2026-01-01 00:00:00",
	})

	origPay := wechatAuthCodePay
	wechatAuthCodePay = func(ctx context.Context, s *PaymentService, p *model.Payment, authCode string) (*codePayResult, error) {
		t.Error("已支付单重复提交不应再触达渠道")
		return &codePayResult{State: ChannelStatePaid, TransactionID: "WX-TX-IDEM-001"}, nil
	}
	defer func() { wechatAuthCodePay = origPay }()

	resp, err := s.PayByAuthCode(ctx, &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-CODE-IDEM", OrderId: orderID, AuthCode: "1345235315201154982642655744", OperatorId: 9})
	if err != nil {
		t.Fatalf("已支付单重复提交应幂等成功: %v", err)
	}
	if resp.State != "PAID" || resp.TransactionId != "WX-TX-IDEM-001" {
		t.Fatalf("幂等结果异常: %+v", resp)
	}
}

// TestPayByAuthCodeGuard 守卫路径：非付款码单 / 渠道未配置 / 参数错误
func TestPayByAuthCodeGuard(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	// 非付款码支付单：现金单不能走付款码收款
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-CODE-CASH",
		OrderID:   testOrderIDBase + 46,
		UserID:    1,
		PayType:   4,
		Amount:    8.00,
		Status:    1,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})
	if _, err := s.PayByAuthCode(ctx, &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-CODE-CASH", OrderId: testOrderIDBase + 46, AuthCode: "1345235315201154982642655744", OperatorId: 9}); err == nil {
		t.Error("现金单走付款码收款应被拒绝")
	}

	// 订单绑定不匹配：payment_no 与 order_id 不一致时拒绝（防跨订单收款）
	if _, err := s.PayByAuthCode(ctx, &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-CODE-CASH", OrderId: testOrderIDBase + 999, AuthCode: "1345235315201154982642655744", OperatorId: 9}); err == nil {
		t.Error("支付单与订单不匹配应被拒绝")
	}

	// 支付配置缺失（req.ConfigId=0）：扣款前被 resolveActivePayConfig 拦截
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-CODE-NOCH",
		OrderID:   testOrderIDBase + 47,
		UserID:    0,
		PayType:   5,
		Amount:    8.00,
		Status:    1,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})
	if _, err := s.PayByAuthCode(ctx, &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-CODE-NOCH", OrderId: testOrderIDBase + 47, AuthCode: "1345235315201154982642655744", OperatorId: 9}); err == nil {
		t.Error("缺少支付配置应拒绝扣款")
	}

	// 参数错误：付款码缺失 / 长度不合法 / 支付单号缺失 / 订单 ID 缺失
	noch := &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-CODE-NOCH", OperatorId: 9}
	if _, err := s.PayByAuthCode(ctx, noch); err == nil {
		t.Error("付款码缺失应报错")
	}
	if _, err := s.PayByAuthCode(ctx, &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-CODE-NOCH", OrderId: testOrderIDBase + 47, AuthCode: "123", OperatorId: 9}); err == nil {
		t.Error("付款码过短应报错")
	}
	if _, err := s.PayByAuthCode(ctx, &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-CODE-NOCH", AuthCode: "1345235315201154982642655744", OperatorId: 9}); err == nil {
		t.Error("订单 ID 缺失应报错")
	}
	if _, err := s.PayByAuthCode(ctx, &payment.PayByAuthCodeReq{AuthCode: "1345235315201154982642655744", OperatorId: 9}); err == nil {
		t.Error("支付单号缺失应报错")
	}
	// 不存在的支付单
	if _, err := s.PayByAuthCode(ctx, &payment.PayByAuthCodeReq{PaymentNo: "PAYTEST-NOT-EXIST", OrderId: testOrderIDBase + 47, AuthCode: "1345235315201154982642655744", OperatorId: 9}); err == nil {
		t.Error("支付单不存在应报错")
	}
}

// TestProcessRefundCodePay 退款渠道映射：微信付款码(5)走微信退款、支付宝付款码(6)走支付宝退款
// 测试环境渠道均未配置，通过失败流水可验证「真实触达了哪个渠道的退款路径」
func TestProcessRefundCodePay(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	cases := []struct {
		name      string
		payType   int32
		paymentNo string
		refundNo  string
		orderID   int64
	}{
		{"微信付款码退款走微信路径", 5, "PAYTEST-RF-CODE-5", "RF-PAYTEST-CODE-5", testOrderIDBase + 50},
		{"支付宝付款码退款走支付宝路径", 6, "PAYTEST-RF-CODE-6", "RF-PAYTEST-CODE-6", testOrderIDBase + 51},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			insertPayment(t, s, &model.Payment{
				PaymentNo: tc.paymentNo,
				OrderID:   tc.orderID,
				UserID:    1,
				PayType:   tc.payType,
				Amount:    20.00,
				Status:    2,
				CreatedAt: "2026-01-01 00:00:00",
				UpdatedAt: "2026-01-01 00:00:00",
			})

			// 渠道未配置：退款第二阶段失败，流水置「退款失败」——证明已分流至渠道退款路径
			if _, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: tc.orderID, RefundNo: tc.refundNo, Amount: 5}); err == nil {
				t.Fatal("渠道未配置退款应报错")
			}
			var refunds []model.RefundPayment
			if err := s.db.GetDb().Where("refund_no = ?", tc.refundNo).Find(&refunds).Error; err != nil || len(refunds) != 1 {
				t.Fatalf("渠道失败应保留审计流水, count=%d err %v", len(refunds), err)
			}
			if refunds[0].Status != 3 {
				t.Errorf("流水状态 = %d, want 3（退款失败）", refunds[0].Status)
			}
		})
	}
}
