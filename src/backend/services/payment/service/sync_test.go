package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 已关闭支付单的渠道核实（人工挂账检测入口）测试
 *
 * 覆盖（solution-20261021-支付回调可靠性-v2.md 的人工兜底延伸）：
 *   - TestSyncClosedPaymentDetectsStuck   渠道已收款 → 挂账待办 + 结论 paid，不改任何资金状态
 *   - TestSyncClosedPaymentNotPaid        渠道明确未收款 → 记核实流水消除疑虑
 *   - TestSyncClosedPaymentStuckDedup     重复核实不重复产出挂账待办
 *   - TestSyncClosedPaymentLocateFallback 订单下无待支付单时回退定位最近的关闭单
 *
 * 运行方式：直连本地 docker 栈的 MySQL（env 守卫跳过），渠道调用由 fakeChannels 替身接管
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/payment/service/ -run TestSyncClosedPayment -v
 */

// syncTestOrderIDBase 关闭单核实测试的订单 ID 偏移（避开调度器测试占用的段）
const syncTestOrderIDBase = schedOrderIDBase + 60

// latestNotifyLogOpt 宽松版流水读取：缺行返回 nil 而非 Fatal（用于「不应存在」类断言）
// 参数：
//   - s: 被测服务
//   - paymentNo: 支付单号
//   - kind: 流水类别
//
// 返回值：
//   - *model.NotifyLog: 最新一条流水，不存在返回 nil
func latestNotifyLogOpt(s *PaymentService, paymentNo string, kind int32) *model.NotifyLog {
	var row model.NotifyLog
	if err := s.db.GetDb().Where("payment_no = ? AND kind = ?", paymentNo, kind).
		Order("id desc").First(&row).Error; err != nil {
		return nil
	}
	return &row
}

// TestSyncClosedPaymentDetectsStuck 渠道已收款：挂账待办 + paid 结论，资金状态零改动
// 前置：订单只有一笔已关闭(5)且无渠道交易号的支付单，假渠道回答已收款
// 断言：结论 paid=true/changed=false、支付单保持关闭且不回填交易号、写 kind=3/result=9 待办、
// 不触发订单 RPC、不调用渠道关单
func TestSyncClosedPaymentDetectsStuck(t *testing.T) {
	s, stub, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	fc := newFakeChannels()
	installFakeChannels(t, fc)
	ctx := context.Background()

	no := "ZFTEST-SYNC-STUCK"
	orderID := syncTestOrderIDBase + 1
	seedSchedPayment(t, s, schedSeed{
		No: no, OrderID: orderID, Amount: 66.00, Status: 5,
		CreatedAgo: 3 * time.Hour, UpdatedAgo: 3 * time.Hour, CommunityID: 3,
	})
	fc.setPaid(no, "TX-SYNC-STUCK", 66.00)

	outcome, err := s.SyncPaymentFromChannel(ctx, orderID, "", model.ConfirmRefManualSync)
	if err != nil {
		t.Fatalf("核实失败: %v", err)
	}
	if !outcome.Paid || outcome.Changed {
		t.Errorf("渠道已收款应返回 paid=true/changed=false, got paid=%v changed=%v", outcome.Paid, outcome.Changed)
	}
	if outcome.TransactionID != "TX-SYNC-STUCK" {
		t.Errorf("结论应带渠道交易号便于退款核对, got %q", outcome.TransactionID)
	}
	if !strings.Contains(outcome.Message, "人工退款") {
		t.Errorf("结论应指明人工退款路径, got %q", outcome.Message)
	}

	got := reloadPaymentByNo(t, s, no)
	if got.Status != 5 {
		t.Errorf("挂账核实不得改动已关闭支付单状态, got %d", got.Status)
	}
	if got.TransactionID != "" {
		t.Errorf("挂账核实不应把渠道交易号回填成已支付证据, got %q", got.TransactionID)
	}
	row := latestNotifyLog(t, s, no, model.NotifyKindStuck)
	if row.Result != model.NotifyResultStuck || row.Handled != model.NotifyPending {
		t.Errorf("应产出待人工处理的挂账待办, result=%d handled=%d", row.Result, row.Handled)
	}
	if got := fc.closeCount(no); got != 0 {
		t.Errorf("已收款的交易不允许关单, got %d 次", got)
	}
	if got := stub.orderCalls(); len(got) != 0 {
		t.Errorf("挂账核实不应改订单状态, got %d 次", len(got))
	}
}

// TestSyncClosedPaymentNotPaid 渠道明确未收款：记核实流水消除疑虑，不产挂账待办
// 前置：已关闭支付单，假渠道回答交易已关闭（超时取消后的正常渠道终态）
// 断言：paid=false、写 kind=5/result=6 核实流水、无 kind=3 挂账待办、资金状态零改动
func TestSyncClosedPaymentNotPaid(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	fc := newFakeChannels()
	installFakeChannels(t, fc)
	ctx := context.Background()

	no := "ZFTEST-SYNC-NOTPAID"
	orderID := syncTestOrderIDBase + 2
	seedSchedPayment(t, s, schedSeed{
		No: no, OrderID: orderID, Amount: 12.00, Status: 5,
		CreatedAgo: 3 * time.Hour, UpdatedAgo: 3 * time.Hour,
	})
	fc.setChannelClosed(no)

	outcome, err := s.SyncPaymentFromChannel(ctx, orderID, "", model.ConfirmRefManualSync)
	if err != nil {
		t.Fatalf("核实失败: %v", err)
	}
	if outcome.Paid {
		t.Errorf("渠道未收款不应返回 paid=true")
	}
	if !strings.Contains(outcome.Message, "未收款") {
		t.Errorf("结论应明确未收款消除疑虑, got %q", outcome.Message)
	}
	row := latestNotifyLog(t, s, no, model.NotifyKindManualSync)
	if row.Result != model.NotifyResultNotPaid {
		t.Errorf("核实流水应为渠道未收款, got %d", row.Result)
	}
	if got := latestNotifyLogOpt(s, no, model.NotifyKindStuck); got != nil {
		t.Errorf("渠道未收款不应产出挂账待办, got %+v", got)
	}
}

// TestSyncClosedPaymentStuckDedup 重复核实不重复产出挂账待办
// 前置：T9 场景连点两次「渠道核实挂账」
// 断言：kind=3 且待处理的流水仍只有 1 条
func TestSyncClosedPaymentStuckDedup(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	fc := newFakeChannels()
	installFakeChannels(t, fc)
	ctx := context.Background()

	no := "ZFTEST-SYNC-DEDUP"
	orderID := syncTestOrderIDBase + 3
	seedSchedPayment(t, s, schedSeed{
		No: no, OrderID: orderID, Amount: 9.90, Status: 5,
		CreatedAgo: 3 * time.Hour, UpdatedAgo: 3 * time.Hour,
	})
	fc.setPaid(no, "TX-SYNC-DEDUP", 9.90)

	for i := 0; i < 2; i++ {
		outcome, err := s.SyncPaymentFromChannel(ctx, orderID, "", model.ConfirmRefManualSync)
		if err != nil || !outcome.Paid {
			t.Fatalf("第 %d 次核实应返回 paid=true, err=%v outcome=%+v", i+1, err, outcome)
		}
	}
	var count int64
	if err := s.db.GetDb().Model(&model.NotifyLog{}).
		Where("payment_no = ? AND kind = ? AND result = ? AND handled = ?",
			no, model.NotifyKindStuck, model.NotifyResultStuck, model.NotifyPending).
		Count(&count).Error; err != nil {
		t.Fatalf("查询挂账待办失败: %v", err)
	}
	if count != 1 {
		t.Errorf("重复核实应去重, 挂账待办 = %d 条, want 1", count)
	}
}

// TestSyncClosedPaymentLocateFallback 订单下无待支付单时回退定位最近的已关闭单
// 前置：订单只有一笔已关闭支付单（无任何待支付单）
// 断言：凭订单 ID 能定位到该关闭单并完成核实
func TestSyncClosedPaymentLocateFallback(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	fc := newFakeChannels()
	installFakeChannels(t, fc)
	ctx := context.Background()

	no := "ZFTEST-SYNC-FALLBACK"
	orderID := syncTestOrderIDBase + 4
	seedSchedPayment(t, s, schedSeed{
		No: no, OrderID: orderID, Amount: 5.00, Status: 5,
		CreatedAgo: 2 * time.Hour, UpdatedAgo: 2 * time.Hour,
	})
	fc.setNotExist(no)

	outcome, err := s.SyncPaymentFromChannel(ctx, orderID, "", model.ConfirmRefManualSync)
	if err != nil {
		t.Fatalf("核实失败: %v", err)
	}
	if outcome.PaymentNo != no {
		t.Errorf("应回退定位到关闭单 %s, got %q", no, outcome.PaymentNo)
	}
	if outcome.ChannelState != ChannelStateNotExist {
		t.Errorf("渠道状态应为无此交易, got %q", outcome.ChannelState)
	}
}

// TestSyncPaidPaymentRepairsUnpaidOrder 已确认支付单 + 订单停留待支付 → 补同步（P0-3）
// 前置：status=2 支付单（订单因二阶段 RPC 失败没同步上），桩侧订单仍为待支付(1)
// 断言：结论 paid=true、订单被补同步为已支付、消息说明已补齐、无挂账待办
func TestSyncPaidPaymentRepairsUnpaidOrder(t *testing.T) {
	s, stub, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	no := "ZFTEST-SYNC-PAID-REPAIR"
	orderID := syncTestOrderIDBase + 5
	seedSchedPayment(t, s, schedSeed{
		No: no, OrderID: orderID, Amount: 66.00, Status: 2,
		TransactionID: "TX-SYNC-PAID", CreatedAgo: 30 * time.Minute, UpdatedAgo: 30 * time.Minute, CommunityID: 3,
	})
	stub.setOrderStatus(orderID, 1)

	outcome, err := s.SyncPaymentFromChannel(ctx, orderID, "", model.ConfirmRefManualSync)
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if !outcome.Paid {
		t.Error("支付单已确认应返回 paid=true")
	}
	if !strings.Contains(outcome.Message, "补齐") {
		t.Errorf("消息应说明订单状态已补齐, got %q", outcome.Message)
	}
	calls := stub.orderCalls()
	if len(calls) != 1 || calls[0].Status != 2 || calls[0].Id != orderID {
		t.Errorf("应补同步订单为已支付 1 次, got %+v", calls)
	}
	if row := latestNotifyLogOpt(s, no, model.NotifyKindStuck); row != nil {
		t.Errorf("补同步成功不应产出挂账待办, got %+v", row)
	}
}

// assertBizCode 断言错误为业务错误且状态码相符（bizerr.New 产出的 *Error 需
// 经 gRPC 边界 ToStatus 才转 status，服务层测试直接断言类型与 Code()）
// 参数：
//   - t: 测试上下文
//   - err: 被测错误
//   - want: 期望的 gRPC 状态码
func assertBizCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatal("应返回业务错误，实际为 nil")
	}
	var be *bizerr.Error
	if !errors.As(err, &be) {
		t.Fatalf("错误应为 *bizerr.Error, got %T (%v)", err, err)
	}
	if be.Code() != want {
		t.Errorf("错误码应为 %v, got %v (%v)", want, be.Code(), err)
	}
}

// TestSyncPaymentNoOrderBinding 单号不属于指定订单 → 拒绝对账（P1-2）
// 前置：订单 A 名下有一笔待支付单，订单 B 下另有一笔待支付单
// 断言：以订单 A 的 ID 配订单 B 的单号同步返回 FailedPrecondition，且零改动
func TestSyncPaymentNoOrderBinding(t *testing.T) {
	s, stub, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	fc := newFakeChannels()
	installFakeChannels(t, fc)
	ctx := context.Background()

	orderA := syncTestOrderIDBase + 7
	orderB := syncTestOrderIDBase + 8
	seedSchedPayment(t, s, schedSeed{
		No: "ZFTEST-SYNC-BIND-A", OrderID: orderA, Amount: 10.00, Status: 1,
		CreatedAgo: time.Hour, UpdatedAgo: time.Hour, CommunityID: 3,
	})
	seedSchedPayment(t, s, schedSeed{
		No: "ZFTEST-SYNC-BIND-B", OrderID: orderB, Amount: 20.00, Status: 1,
		CreatedAgo: time.Hour, UpdatedAgo: time.Hour, CommunityID: 3,
	})

	_, err := s.SyncPaymentFromChannel(ctx, orderA, "ZFTEST-SYNC-BIND-B", model.ConfirmRefManualSync)
	assertBizCode(t, err, codes.FailedPrecondition)
	// 订单侧与支付单侧都不得被触碰
	if got := stub.orderCalls(); len(got) != 0 {
		t.Errorf("校验失败不应改订单状态, got %+v", got)
	}
	if got := reloadPaymentByNo(t, s, "ZFTEST-SYNC-BIND-B"); got.Status != 1 {
		t.Errorf("校验失败不得改动支付单, got status=%d", got.Status)
	}
}

// TestMarkPaymentPaidManuallyOrderBinding 人工标记时单号与订单不绑定 → 拒绝（P1-2）
// 前置：订单 A 名下有一笔待支付单
// 断言：以订单 B 的 ID 标记订单 A 的单号返回 FailedPrecondition，支付单保持待支付
func TestMarkPaymentPaidManuallyOrderBinding(t *testing.T) {
	s, stub, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderA := syncTestOrderIDBase + 9
	orderB := syncTestOrderIDBase + 10
	seedSchedPayment(t, s, schedSeed{
		No: "ZFTEST-SYNC-MARK-BIND", OrderID: orderA, Amount: 30.00, Status: 1,
		CreatedAgo: time.Hour, UpdatedAgo: time.Hour, CommunityID: 3,
	})

	err := s.MarkPaymentPaidManually(ctx, orderB, "ZFTEST-SYNC-MARK-BIND", "TX-MARK-BIND", 30.00, "抄错单号场景 by admin:1")
	assertBizCode(t, err, codes.FailedPrecondition)
	if got := reloadPaymentByNo(t, s, "ZFTEST-SYNC-MARK-BIND"); got.Status != 1 {
		t.Errorf("校验失败不得改动支付单, got status=%d", got.Status)
	}
	if got := stub.orderCalls(); len(got) != 0 {
		t.Errorf("校验失败不应改订单状态, got %+v", got)
	}
}

// TestSyncPaidPaymentOrderCancelledStuck 已确认支付单 + 订单已取消 → 挂账待办（P0-3）
// 前置：status=2 支付单，桩侧订单已取消(6)
// 断言：结论 paid=true、写 kind=3/result=9 待办、不改订单状态
func TestSyncPaidPaymentOrderCancelledStuck(t *testing.T) {
	s, stub, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	no := "ZFTEST-SYNC-PAID-CANCELLED"
	orderID := syncTestOrderIDBase + 6
	seedSchedPayment(t, s, schedSeed{
		No: no, OrderID: orderID, Amount: 66.00, Status: 2,
		TransactionID: "TX-SYNC-PAID-CXL", CreatedAgo: 30 * time.Minute, UpdatedAgo: 30 * time.Minute, CommunityID: 3,
	})
	stub.setOrderStatus(orderID, 6)

	outcome, err := s.SyncPaymentFromChannel(ctx, orderID, "", model.ConfirmRefManualSync)
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if !outcome.Paid {
		t.Error("支付单已确认应返回 paid=true")
	}
	if !strings.Contains(outcome.Message, "人工退款") {
		t.Errorf("消息应指明人工退款路径, got %q", outcome.Message)
	}
	row := latestNotifyLog(t, s, no, model.NotifyKindStuck)
	if row.Result != model.NotifyResultStuck || row.Handled != model.NotifyPending {
		t.Errorf("应产出待人工处理的挂账待办, result=%d handled=%d", row.Result, row.Handled)
	}
	if got := stub.orderCalls(); len(got) != 0 {
		t.Errorf("订单已取消不允许再改状态, got %d 次", len(got))
	}
}
