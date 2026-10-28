package service

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 支付服务测试
 *
 * 纯函数测试（无外部依赖，CI 可跑）：
 *   - yuanToFen           金额元转分（float64 精度防护）
 *   - generatePaymentNo   支付单号唯一性
 *
 * 集成测试（直连本地开发库，env 守卫跳过）：
 *   运行方式（需本地 docker 栈已启动，MySQL 端口映射到宿主机）：
 *     TEST_MYSQL_PASSWORD=xxx go test ./services/payment/service/ -v
 *
 *   - TestCreatePaymentCash        现金收款直接置已支付并同步订单状态
 *   - TestCreatePaymentAlreadyPaid 已支付订单拒绝重复创建支付单（幂等）
 *   - TestFindReusablePendingPayment 微信渠道复用同单同金额待支付单
 *   - TestHandlePaySuccess         回调三重防护：金额比对 / 条件更新幂等 / 终态兜底
 *   - TestProcessRefundCash        现金退款：成功 / refund_no 幂等 / 累计上限校验
 *   - TestProcessRefundNoChannel   渠道未配置：流水落「退款失败」保留审计，重试可清理重发
 *   - TestClosePayments            关闭待支付单后迟到回调被忽略
 *
 * 测试数据统一使用 testOrderIDBase 起始的订单 ID，结束即清理，不与业务数据混淆。
 */

// testOrderIDBase 支付测试订单 ID 起始值（远超真实业务 ID 区间，清理依据）
const testOrderIDBase = int64(990000001)

// stubOrderClient 订单服务桩：记录 UpdateOrderStatus 调用入参，不落真实订单
// 嵌入接口后仅覆写 UpdateOrderStatus/GetOrder，避免实现全部方法
type stubOrderClient struct {
	order.OrderServiceClient
	mu          sync.Mutex
	calls       []*order.UpdateOrderStatusReq
	orderStatus map[int64]int32 // GetOrder 预置的订单状态；缺省视为查询失败
}

// UpdateOrderStatus 记录调用并返回成功响应
func (s *stubOrderClient) UpdateOrderStatus(ctx context.Context, in *order.UpdateOrderStatusReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, in)
	return &common.CommonResponse{}, nil
}

// GetOrder 按预置状态返回订单摘要（未预置的订单返回错误，模拟订单服务查不到）
func (s *stubOrderClient) GetOrder(ctx context.Context, in *order.GetOrderReq, opts ...grpc.CallOption) (*order.OrderDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.orderStatus[in.Id]
	if !ok {
		return nil, fmt.Errorf("桩未预置订单 %d 的状态", in.Id)
	}
	return &order.OrderDetail{Order: &order.OrderInfo{Id: in.Id, Status: st}}, nil
}

// setOrderStatus 预置某订单在桩侧的状态（供支付单/订单一致性检测使用）
// 参数：
//   - id: 订单 ID
//   - status: 订单状态
func (s *stubOrderClient) setOrderStatus(id int64, status int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.orderStatus == nil {
		s.orderStatus = make(map[int64]int32)
	}
	s.orderStatus[id] = status
}

// orderCalls 返回已记录的订单状态同步调用快照
func (s *stubOrderClient) orderCalls() []*order.UpdateOrderStatusReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*order.UpdateOrderStatusReq(nil), s.calls...)
}

// newPaymentTestEnv 构建支付测试环境：真实 DB + 桩化的订单客户端
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *PaymentService: 被测服务实例
//   - *stubOrderClient: 订单状态同步桩
//   - func(): 清理函数（恢复全局客户端、删除测试数据）
func newPaymentTestEnv(t *testing.T) (*PaymentService, *stubOrderClient, func()) {
	t.Helper()
	if os.Getenv("TEST_MYSQL_PASSWORD") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过支付集成测试（需本地 docker 栈）")
	}
	dbCfg := jcbaseGo.DbStruct{
		Host:      envOr("TEST_MYSQL_HOST", "127.0.0.1"),
		Port:      envOr("TEST_MYSQL_PORT", "3306"),
		Dbname:    envOr("TEST_MYSQL_DBNAME", "market"),
		Username:  envOr("TEST_MYSQL_USER", "root"),
		Password:  os.Getenv("TEST_MYSQL_PASSWORD"),
		Charset:   "utf8mb4",
		ParseTime: "True",
		Loc:       "Local",
	}
	dbInst, err := mysql.New(dbCfg)
	if err != nil {
		t.Fatalf("连接测试数据库失败: %v", err)
	}
	if err := dbInst.GetDb().AutoMigrate(&model.Payment{}, &model.RefundPayment{}, &model.NotifyLog{}, &model.SchedulerFailure{}, &model.PayConfig{}); err != nil {
		t.Fatalf("迁移支付测试表失败: %v", err)
	}

	// 现金/货到付款建单走 cashCreateLockTx 锁 pay_config 固定单行（id=1），测试库必须存在该行；
	// 渠道字段全部留空，微信/支付宝仍走「未配置」失败路径，与原测试语义一致
	if err := dbInst.GetDb().Where(model.PayConfig{ID: payConfigID}).
		FirstOrCreate(&model.PayConfig{ID: payConfigID}).Error; err != nil {
		t.Fatalf("初始化支付配置行失败: %v", err)
	}

	// 直接构造服务实例，微信/支付宝渠道保持未配置状态，便于测渠道失败路径
	s := &PaymentService{db: dbInst, cfg: &config.Config{}}

	stub := &stubOrderClient{orderStatus: make(map[int64]int32)}
	origOrder := grpcclient.OrderClient
	grpcclient.OrderClient = stub
	cleanup := func() {
		grpcclient.OrderClient = origOrder
		dbInst.GetDb().Where("order_id >= ?", testOrderIDBase).Delete(&model.Payment{})
		dbInst.GetDb().Where("order_id >= ?", testOrderIDBase).Delete(&model.RefundPayment{})
		dbInst.GetDb().Where("order_id >= ?", testOrderIDBase).Delete(&model.NotifyLog{})
		dbInst.GetDb().Where("order_id >= ?", testOrderIDBase).Delete(&model.SchedulerFailure{})
	}
	return s, stub, cleanup
}

// envOr 读取环境变量，未设置时返回默认值
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// insertPayment 插入一条测试支付单
// 参数：
//   - t: 测试对象
//   - s: 被测服务（复用其 DB 连接）
//   - p: 支付单模型（order_id/pay_type/amount/status 必填）
func insertPayment(t *testing.T, s *PaymentService, p *model.Payment) {
	t.Helper()
	if err := s.db.GetDb().Create(p).Error; err != nil {
		t.Fatalf("插入测试支付单失败: %v", err)
	}
}

// TestYuanToFen 金额元转分：验证 float64 精度防护的换算正确性
func TestYuanToFen(t *testing.T) {
	cases := []struct {
		name string
		yuan float64
		want int
	}{
		{"零元", 0, 0},
		{"普通两位小数", 12.34, 1234},
		{"整数元", 99, 9900},
		{"经典浮点误差 0.1+0.2", 0.1 + 0.2, 30},
		{"两位小数 99.99", 99.99, 9999},
		{"四舍五入进位", 0.005, 1},
		{"极小值舍去", 1e-9, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := yuanToFen(c.yuan); got != c.want {
				t.Errorf("yuanToFen(%v) = %d, want %d", c.yuan, got, c.want)
			}
		})
	}
}

// TestGeneratePaymentNo 支付单号：前缀 ZF 且批量生成不重复
func TestGeneratePaymentNo(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		no := generatePaymentNo()
		if len(no) < 3 || no[:2] != "ZF" {
			t.Fatalf("支付单号前缀错误: %s", no)
		}
		if seen[no] {
			t.Fatalf("支付单号重复: %s", no)
		}
		seen[no] = true
	}
}

// TestCreatePaymentCash 现金收款：支付单直接置已支付，并同步订单状态为已支付
func TestCreatePaymentCash(t *testing.T) {
	s, stub, cleanup := newPaymentTestEnv(t)
	defer cleanup()

	orderID := testOrderIDBase
	p, params, err := s.CreatePayment(context.Background(), &payment.CreatePaymentReq{
		OrderId: orderID,
		UserId:  0, // 收银台散客单允许无会员
		Amount:  58.50,
		PayType: 4,
	})
	if err != nil {
		t.Fatalf("现金收款创建支付单失败: %v", err)
	}
	if p.Status != 2 {
		t.Errorf("现金收款支付单状态 = %d, want 2（直接已支付）", p.Status)
	}
	if params != "" {
		t.Errorf("现金收款不应产生第三方调起参数, got %q", params)
	}
	calls := stub.orderCalls()
	if len(calls) != 1 || calls[0].Id != orderID || calls[0].Status != 2 {
		t.Errorf("订单状态同步调用异常: %+v", calls)
	}
}

// TestCreatePaymentCashConcurrent 现金双击防护：同订单并发建单只允许一笔成功
// 覆盖 2026-10-25 收银台审查 P1：现金单「创建即已支付」且不走 pending_flag 唯一索引，
// 「查重→创建」由订单级分段锁串行化，锁外并发请求在锁内查重被拒（AlreadyExists）
func TestCreatePaymentCashConcurrent(t *testing.T) {
	s, stub, cleanup := newPaymentTestEnv(t)
	defer cleanup()

	orderID := testOrderIDBase + 3
	const n = 4
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = s.CreatePayment(context.Background(), &payment.CreatePaymentReq{
				OrderId: orderID,
				UserId:  0,
				Amount:  66.60,
				PayType: 4,
			})
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("并发现金建单应仅一笔成功，实际 %d 笔，errs=%v", succeeded, errs)
	}

	var cnt int64
	if err := s.db.GetDb().Model(&model.Payment{}).Where("order_id = ? AND status = 2", orderID).Count(&cnt).Error; err != nil {
		t.Fatalf("统计支付流水失败: %v", err)
	}
	if cnt != 1 {
		t.Errorf("订单已支付现金流水应恰为 1 笔，实际 %d", cnt)
	}
	if calls := stub.orderCalls(); len(calls) != 1 {
		t.Errorf("订单状态同步应仅发生一次，实际 %d 次: %+v", len(calls), calls)
	}
}

// TestCreatePaymentAlreadyPaid 幂等：订单已有支付成功记录时拒绝再次创建支付单
func TestCreatePaymentAlreadyPaid(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()

	orderID := testOrderIDBase + 1
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-ALREADY-PAID",
		OrderID:   orderID,
		UserID:    1,
		PayType:   1,
		Amount:    99.90,
		Status:    2,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})

	_, _, err := s.CreatePayment(context.Background(), &payment.CreatePaymentReq{
		OrderId: orderID,
		UserId:  1,
		Amount:  99.90,
		PayType: 1,
	})
	if err == nil {
		t.Fatal("已支付订单重复创建支付单应被拒绝，实际未报错")
	}
}

// TestFindReusablePendingPayment 微信渠道复用：同订单同渠道同金额的待支付单直接复用
func TestFindReusablePendingPayment(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()

	orderID := testOrderIDBase + 2
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-REUSE-PENDING",
		OrderID:   orderID,
		UserID:    1,
		PayType:   1,
		Amount:    99.90,
		Status:    1,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})
	ctx := context.Background()

	// 同单同渠道同金额：复用
	got, err := s.findReusablePendingPayment(ctx, &payment.CreatePaymentReq{OrderId: orderID, PayType: 1, Amount: 99.90})
	if err != nil {
		t.Fatalf("复用待支付单查询失败: %v", err)
	}
	if got == nil || got.PaymentNo != "PAYTEST-REUSE-PENDING" {
		t.Errorf("应复用已存在待支付单, got %+v", got)
	}

	// 金额不同：不复用
	got, err = s.findReusablePendingPayment(ctx, &payment.CreatePaymentReq{OrderId: orderID, PayType: 1, Amount: 88.00})
	if err != nil || got != nil {
		t.Errorf("金额不同不应复用, got %+v err %v", got, err)
	}

	// 现金渠道：永不复用（创建时即支付成功，复用会跳过订单状态同步副作用）
	got, err = s.findReusablePendingPayment(ctx, &payment.CreatePaymentReq{OrderId: orderID, PayType: 4, Amount: 99.90})
	if err != nil || got != nil {
		t.Errorf("现金渠道不应复用, got %+v err %v", got, err)
	}
}

// TestHandlePaySuccess 回调三重防护：金额比对 / 条件更新幂等 / 终态兜底
func TestHandlePaySuccess(t *testing.T) {
	s, stub, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	// 场景一：金额不一致拒绝确认，支付单状态保持待支付
	orderID := testOrderIDBase + 10
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-CB-AMOUNT",
		OrderID:   orderID,
		UserID:    1,
		PayType:   1,
		Amount:    50.00,
		Status:    1,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})
	if err := s.handlePaySuccess(ctx, &payCallbackParams{PaymentNo: "PAYTEST-CB-AMOUNT", TransactionID: "TX-001", Amount: 49.99}); err == nil {
		t.Fatal("回调金额与应付不一致应报错")
	}
	var p model.Payment
	if err := s.db.GetDb().Where("payment_no = ?", "PAYTEST-CB-AMOUNT").First(&p).Error; err != nil || p.Status != 1 {
		t.Errorf("金额不一致时支付单状态应保持待支付(1), got %d err %v", p.Status, err)
	}

	// 场景二：金额一致流转成功，且同步订单状态
	if err := s.handlePaySuccess(ctx, &payCallbackParams{PaymentNo: "PAYTEST-CB-AMOUNT", TransactionID: "TX-001", Amount: 50.00}); err != nil {
		t.Fatalf("正常回调处理失败: %v", err)
	}
	if err := s.db.GetDb().Where("payment_no = ?", "PAYTEST-CB-AMOUNT").First(&p).Error; err != nil {
		t.Fatalf("查询支付单失败: %v", err)
	}
	if p.Status != 2 || p.TransactionID != "TX-001" {
		t.Errorf("支付单应流转为已支付(2)并记录交易号, got status=%d tx=%s", p.Status, p.TransactionID)
	}
	calls := stub.orderCalls()
	if len(calls) != 1 || calls[0].Id != orderID || calls[0].Status != 2 {
		t.Errorf("订单状态同步调用异常: %+v", calls)
	}

	// 场景三：重复回调幂等——支付单已是终态，直接忽略且不再同步订单
	if err := s.handlePaySuccess(ctx, &payCallbackParams{PaymentNo: "PAYTEST-CB-AMOUNT", TransactionID: "TX-001", Amount: 50.00}); err != nil {
		t.Fatalf("重复回调应幂等返回 nil, got %v", err)
	}
	if n := len(stub.orderCalls()); n != 1 {
		t.Errorf("重复回调不应再次同步订单状态, 同步次数 = %d, want 1", n)
	}

	// 场景四：已关闭支付单的迟到回调被终态兜底忽略（防已取消订单被误置已支付）
	closedOrderID := testOrderIDBase + 11
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-CB-CLOSED",
		OrderID:   closedOrderID,
		UserID:    1,
		PayType:   1,
		Amount:    20.00,
		Status:    5,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})
	if err := s.handlePaySuccess(ctx, &payCallbackParams{PaymentNo: "PAYTEST-CB-CLOSED", TransactionID: "TX-002", Amount: 20.00}); err != nil {
		t.Fatalf("已关闭单迟到回调应忽略, got %v", err)
	}
	p = model.Payment{} // 重置残留主键，避免 GORM 将旧 ID 拼入查询条件
	if err := s.db.GetDb().Where("payment_no = ?", "PAYTEST-CB-CLOSED").First(&p).Error; err != nil || p.Status != 5 {
		t.Errorf("已关闭单状态不应被改变, got %d err %v", p.Status, err)
	}

	// 场景五：参数缺失 / 金额非法直接拒绝
	if err := s.handlePaySuccess(ctx, &payCallbackParams{PaymentNo: "", TransactionID: "TX", Amount: 1}); err == nil {
		t.Error("支付单号缺失应报错")
	}
	if err := s.handlePaySuccess(ctx, &payCallbackParams{PaymentNo: "PAYTEST-CB-CLOSED", TransactionID: "TX", Amount: 0}); err == nil {
		t.Error("回调金额非法应报错")
	}
}

// TestProcessRefundCash 现金退款：成功落流水 / refund_no 幂等 / 累计上限校验
func TestProcessRefundCash(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderID := testOrderIDBase + 20
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-RF-CASH",
		OrderID:   orderID,
		UserID:    1,
		PayType:   4,
		Amount:    100.00,
		Status:    2,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})

	// 首次部分退款 30 元
	resp, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: orderID, RefundNo: "RF-PAYTEST-001", Amount: 30})
	if err != nil {
		t.Fatalf("现金退款失败: %v", err)
	}
	if !resp.Completed {
		t.Fatal("现金退款无渠道异步，应即时完成 (completed=true)")
	}
	var refunds []model.RefundPayment
	if err := s.db.GetDb().Where("order_id = ?", orderID).Find(&refunds).Error; err != nil {
		t.Fatalf("查询退款流水失败: %v", err)
	}
	if len(refunds) != 1 || refunds[0].Status != 2 || refunds[0].Amount != 30 {
		t.Fatalf("退款流水异常: %+v", refunds)
	}

	// 同 refund_no 重复提交：幂等成功，不新增流水
	if _, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: orderID, RefundNo: "RF-PAYTEST-001", Amount: 30}); err != nil {
		t.Fatalf("同退款单号重复提交应幂等成功, got %v", err)
	}
	if err := s.db.GetDb().Where("order_id = ?", orderID).Find(&refunds).Error; err != nil || len(refunds) != 1 {
		t.Fatalf("幂等重提不应新增流水, count=%d err %v", len(refunds), err)
	}

	// 累计超限：30 + 71 > 100 拒绝
	if _, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: orderID, RefundNo: "RF-PAYTEST-002", Amount: 71}); err == nil {
		t.Fatal("退款金额超过剩余可退金额应报错")
	}

	// 退满剩余 70 元：成功且支付单置已退款(4)
	if _, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: orderID, RefundNo: "RF-PAYTEST-003", Amount: 70}); err != nil {
		t.Fatalf("退满剩余金额失败: %v", err)
	}
	var p model.Payment
	if err := s.db.GetDb().Where("payment_no = ?", "PAYTEST-RF-CASH").First(&p).Error; err != nil || p.Status != 4 {
		t.Errorf("全额退款后支付单应置已退款(4), got %d err %v", p.Status, err)
	}

	// 参数校验：非法入参拒绝
	if _, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: orderID, RefundNo: "RF-PAYTEST-004", Amount: 0}); err == nil {
		t.Error("退款金额为 0 应报错")
	}
	if _, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: orderID, RefundNo: "", Amount: 10}); err == nil {
		t.Error("退款单号为空应报错")
	}
}

// TestProcessRefundConcurrentSamePayment 同一支付单的并发退款回归（FOR UPDATE 累计防线）
// 三个并发场景各起一组 goroutine，全部走现金路径（pay_type=4，不触渠道，只考 DB 串行化）：
//  1. 并发不同 refund_no、单笔即超剩余额度：只允许一笔成功，其余必须被累计上限拒绝；
//  2. 并发同一 refund_no 双提交：只落一条流水，两次调用都按幂等成功返回（不得二次出款）；
//  3. 并发同 refund_no 但金额不同：一笔成功、另一笔被拒（拒绝「换金额重试静默按旧金额成功」）
//
// 参数：
//   - t: 测试对象
//
// 使用示例：
//
//	TEST_MYSQL_PASSWORD=xxx go test ./services/payment/service/ -run TestProcessRefundConcurrentSamePayment -v
func TestProcessRefundConcurrentSamePayment(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	// newPaidPayment 建一笔金额 amount 的已支付现金单，返回订单 ID 与支付单号
	newPaidPayment := func(orderID int64, paymentNo string, amount float64) {
		t.Helper()
		insertPayment(t, s, &model.Payment{
			PaymentNo: paymentNo,
			OrderID:   orderID,
			UserID:    1,
			PayType:   4,
			Amount:    amount,
			Status:    2,
			CreatedAt: "2026-01-01 00:00:00",
			UpdatedAt: "2026-01-01 00:00:00",
		})
	}

	// runConcurrent 并发发起 n 笔退款（refundNo 由下标生成），返回成功笔数
	runConcurrent := func(orderID int64, n int, amount float64, refundNo func(i int) string) int {
		t.Helper()
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{
					OrderId:  orderID,
					RefundNo: refundNo(i),
					Amount:   amount,
				})
				if err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()
		return ok
	}

	// countFlows 统计某支付单的成功(2)流水条数与金额合计
	countFlows := func(paymentNo string) (int, float64) {
		t.Helper()
		var rows []model.RefundPayment
		if err := s.db.GetDb().Where("payment_no = ? AND status = 2", paymentNo).Find(&rows).Error; err != nil {
			t.Fatalf("查询退款流水失败: %v", err)
		}
		var sum float64
		for _, r := range rows {
			sum += r.Amount
		}
		return len(rows), sum
	}

	t.Run("并发不同退款单号超额度只放行一笔", func(t *testing.T) {
		orderID := testOrderIDBase + 30
		newPaidPayment(orderID, "PAYTEST-CC-1", 100.00)
		if ok := runConcurrent(orderID, 4, 60, func(i int) string { return fmt.Sprintf("RF-PAYTEST-CC1-%d", i) }); ok != 1 {
			t.Fatalf("100 元支付单并发退 60 元 × 4，应恰好 1 笔成功，实际 %d 笔", ok)
		}
		if n, sum := countFlows("PAYTEST-CC-1"); n != 1 || sum != 60 {
			t.Fatalf("成功流水应为 1 条共 60 元，实际 %d 条共 %.2f 元", n, sum)
		}
	})

	t.Run("并发同退款单号双提交只落一条流水", func(t *testing.T) {
		orderID := testOrderIDBase + 31
		newPaidPayment(orderID, "PAYTEST-CC-2", 100.00)
		if ok := runConcurrent(orderID, 2, 40, func(int) string { return "RF-PAYTEST-CC2-SAME" }); ok != 2 {
			t.Fatalf("同退款单号双提交应两次都返回成功（首次落库 + 幂等命中），实际成功 %d 次", ok)
		}
		if n, sum := countFlows("PAYTEST-CC-2"); n != 1 || sum != 40 {
			t.Fatalf("幂等双提交只应出款一次（1 条 40 元），实际 %d 条共 %.2f 元", n, sum)
		}
	})

	t.Run("并发同退款单号不同金额拒绝一笔", func(t *testing.T) {
		orderID := testOrderIDBase + 32
		newPaidPayment(orderID, "PAYTEST-CC-3", 100.00)
		// 先落一笔 50 元成功流水，再用同单号请求 20 元：必须被拒而不是按旧金额静默成功
		if _, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: orderID, RefundNo: "RF-PAYTEST-CC3", Amount: 50}); err != nil {
			t.Fatalf("首次退款失败: %v", err)
		}
		if _, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: orderID, RefundNo: "RF-PAYTEST-CC3", Amount: 20}); err == nil {
			t.Fatal("同退款单号换金额重试应被拒绝（否则调用方会以为新金额已退成功）")
		}
		if n, sum := countFlows("PAYTEST-CC-3"); n != 1 || sum != 50 {
			t.Fatalf("流水应保持原样（1 条 50 元），实际 %d 条共 %.2f 元", n, sum)
		}
	})
}

// TestProcessRefundNoChannel 渠道未配置：流水落「退款失败」保留审计，同 refund_no 重试时清理后重新发起
func TestProcessRefundNoChannel(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderID := testOrderIDBase + 21
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-RF-WX-NOCH",
		OrderID:   orderID,
		UserID:    1,
		PayType:   1,
		Amount:    88.00,
		Status:    2,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})

	// 微信渠道未配置：第二阶段渠道调用失败，流水置「退款失败」保留审计
	if _, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: orderID, RefundNo: "RF-PAYTEST-NOCH-001", Amount: 10}); err == nil {
		t.Fatal("渠道未配置退款应报错")
	}
	var refunds []model.RefundPayment
	if err := s.db.GetDb().Where("refund_no = ?", "RF-PAYTEST-NOCH-001").Find(&refunds).Error; err != nil || len(refunds) != 1 {
		t.Fatalf("渠道失败应保留审计流水, count=%d err %v", len(refunds), err)
	}
	if refunds[0].Status != 3 {
		t.Errorf("渠道失败流水状态 = %d, want 3（退款失败）", refunds[0].Status)
	}

	// 同 refund_no 重试：上次失败流水被清理后重新发起，仍因渠道未配置失败（仍 1 条流水）
	if _, err := s.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: orderID, RefundNo: "RF-PAYTEST-NOCH-001", Amount: 10}); err == nil {
		t.Fatal("重试应再次因渠道未配置失败")
	}
	if err := s.db.GetDb().Where("refund_no = ?", "RF-PAYTEST-NOCH-001").Find(&refunds).Error; err != nil || len(refunds) != 1 {
		t.Fatalf("重试后应保持单条失败流水, count=%d err %v", len(refunds), err)
	}
}

// TestClosePayments 关闭待支付单：订单取消后关闭支付单，迟到回调被忽略
func TestClosePayments(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderID := testOrderIDBase + 30
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-CLOSE-01",
		OrderID:   orderID,
		UserID:    1,
		PayType:   1,
		Amount:    66.00,
		Status:    1,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})

	if err := s.ClosePayments(ctx, &payment.ClosePaymentsReq{OrderId: orderID}); err != nil {
		t.Fatalf("关闭待支付单失败: %v", err)
	}
	var p model.Payment
	if err := s.db.GetDb().Where("payment_no = ?", "PAYTEST-CLOSE-01").First(&p).Error; err != nil || p.Status != 5 {
		t.Fatalf("待支付单应被关闭(5), got %d err %v", p.Status, err)
	}

	// 迟到回调：被终态兜底忽略，状态不变
	if err := s.handlePaySuccess(ctx, &payCallbackParams{PaymentNo: "PAYTEST-CLOSE-01", TransactionID: "TX-LATE", Amount: 66.00}); err != nil {
		t.Fatalf("已关闭单迟到回调应忽略, got %v", err)
	}
	if err := s.db.GetDb().Where("payment_no = ?", "PAYTEST-CLOSE-01").First(&p).Error; err != nil || p.Status != 5 {
		t.Errorf("迟到回调不应改变已关闭单状态, got %d err %v", p.Status, err)
	}
}

// TestGetPaymentRefundedVisible GetPayment/BatchGetPayments 有效口径：
// 支付成功(2)与已退款(4)均可见（已退款单需向用户展示原支付方式），待支付/已关闭不可见
func TestGetPaymentRefundedVisible(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	const orderID = testOrderIDBase + 40
	paidNo := "PAYTEST-RF-VISIBLE-01"
	insertPayment(t, s, &model.Payment{
		PaymentNo: paidNo,
		OrderID:   orderID,
		UserID:    1,
		PayType:   1,
		Amount:    88.00,
		Status:    model.PaymentStatusSuccess,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})

	// 支付成功(2)：可见
	p, err := s.GetPayment(ctx, orderID)
	if err != nil || p == nil || p.Status != model.PaymentStatusSuccess {
		t.Fatalf("支付成功单应可见, got %v err %v", p, err)
	}

	// 全额退款后置已退款(4)：仍可见（口径修正点）
	if err := s.db.GetDb().Model(&model.Payment{}).
		Where("payment_no = ?", paidNo).
		Update("status", model.PaymentStatusRefunded).Error; err != nil {
		t.Fatalf("置已退款失败: %v", err)
	}
	p, err = s.GetPayment(ctx, orderID)
	if err != nil || p == nil || p.Status != model.PaymentStatusRefunded {
		t.Fatalf("已退款单应仍可见（返回原支付方式）, got %v err %v", p, err)
	}

	// BatchGetPayments 同口径：已退款单返回，待支付单不返回
	pendingOrderID := orderID + 1
	insertPayment(t, s, &model.Payment{
		PaymentNo: "PAYTEST-RF-VISIBLE-02",
		OrderID:   pendingOrderID,
		UserID:    1,
		PayType:   1,
		Amount:    10.00,
		Status:    model.PaymentStatusPending,
		CreatedAt: "2026-01-01 00:00:00",
		UpdatedAt: "2026-01-01 00:00:00",
	})
	batch, err := s.BatchGetPayments(ctx, []int64{orderID, pendingOrderID})
	if err != nil {
		t.Fatalf("BatchGetPayments 失败: %v", err)
	}
	if len(batch) != 1 || batch[0].OrderID != orderID || batch[0].Status != model.PaymentStatusRefunded {
		t.Fatalf("BatchGetPayments 应仅返回已退款单 1 条, got %+v err %v", batch, err)
	}

	// 待支付订单：GetPayment 查不到
	if _, err := s.GetPayment(ctx, pendingOrderID); err == nil {
		t.Fatal("待支付单不应被 GetPayment 命中")
	}
}
