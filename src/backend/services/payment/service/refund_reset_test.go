package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 退款流水人工恢复收敛（B2）测试
 *
 * 覆盖（todolist-20261023-审查修复.md B2）：
 *   - TestResetRefundFlowRequiresRemark   核实依据缺失/空白一律拒绝，流水零改动
 *   - TestResetRefundFlowReopensPending   待退款流水预算归零 + 留痕 + 原转办待办销账
 *   - TestResetRefundFlowRejectsTerminal  已成功/已失败流水拒绝（防渠道二次出款与无意义重置）
 *   - TestResetRefundFlowNoFlow           无流水回 recovered=false 而不是报错
 *   - TestResetRefundFlowOrderScope       订单归属不符时不得改动他单流水
 *
 * 运行方式：直连本地 docker 栈的 MySQL（env 守卫跳过），全程不触渠道
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/payment/service/ -run TestResetRefundFlow -v
 */

// refundResetOrderIDBase 本组测试的订单 ID 偏移（避开调度器与核实测试占用的段）
const refundResetOrderIDBase = syncTestOrderIDBase + 60

// seedRefundFlow 按裸 SQL 插入一条退款流水（时间列可控，绕开 base 钩子的当前时间改写）
// 参数：
//   - t: 测试对象
//   - s: 被测服务
//   - no: 退款单号
//   - paymentNo: 关联支付单号
//   - orderID: 订单 ID
//   - status: 流水状态 1待退款 2成功 3失败
//   - retryCount: 已消耗的查单预算
//   - escalated: 已转办标记
func seedRefundFlow(t *testing.T, s *PaymentService, no, paymentNo string, orderID int64, status, retryCount, escalated int32) {
	t.Helper()
	now := time.Now().Format(timeLayout)
	err := s.db.GetDb().Exec(
		`INSERT INTO refund_payments
			(refund_no, payment_no, order_id, amount, status, retry_count, escalated, created_at, updated_at)
		 VALUES (?, ?, ?, 20.00, ?, ?, ?, ?, ?)`,
		no, paymentNo, orderID, status, retryCount, escalated, now, now).Error
	if err != nil {
		t.Fatalf("插入测试退款流水失败: %v", err)
	}
}

// loadRefundFlow 读回退款流水（断言预算字段用）
func loadRefundFlow(t *testing.T, s *PaymentService, no string) *model.RefundPayment {
	t.Helper()
	var flow model.RefundPayment
	if err := s.db.GetDb().Where("refund_no = ?", no).First(&flow).Error; err != nil {
		t.Fatalf("读取退款流水失败: %v", err)
	}
	return &flow
}

// seedRefundEscalationTodo 起一条「退款查单放弃收敛」的人工待办，备注里带退款单号
func seedRefundEscalationTodo(t *testing.T, s *PaymentService, p *model.Payment, refundNo string) {
	t.Helper()
	entry := notifyEntryFrom(p, model.NotifyKindReconcile, model.NotifyResultSyncFailed)
	entry.ErrorMsg = fmt.Sprintf("退款查单连续 10 次未能确认终态，退款单 %s", refundNo)
	s.recordNotifyLog(context.Background(), entry)
}

// assertResetErrCode 断言重置接口返回的业务错误码
// 参数：
//   - t: 测试对象
//   - name: 用例名
//   - err: 被测错误
//   - want: 期望的 gRPC 码
func assertResetErrCode(t *testing.T, name string, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s：应返回错误", name)
	}
	var be *bizerr.Error
	if !errors.As(err, &be) {
		t.Fatalf("%s：应为 bizerr 业务错误, got %T %v", name, err, err)
	}
	if be.Code() != want {
		t.Fatalf("%s：应返回 %v, got %v(%s)", name, want, be.Code(), err.Error())
	}
}

// TestResetRefundFlowRequiresRemark 核实依据缺失或空白：拒绝执行且流水零改动
func TestResetRefundFlowRequiresRemark(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderID := int64(refundResetOrderIDBase + 1)
	no := "ZFTEST-RST-NOREMARK"
	p := seedSchedPayment(t, s, schedSeed{No: "ZFTEST-RST-PAY-1", OrderID: orderID, Amount: 66.00, Status: 2, CommunityID: 3})
	seedRefundFlow(t, s, no, p.PaymentNo, orderID, 1, 10, 1)

	for _, remark := range []string{"", "   "} {
		_, err := s.ResetRefundFlow(ctx, &payment.ResetRefundFlowReq{OrderId: orderID, RefundNo: no, Remark: remark})
		assertResetErrCode(t, "空核实依据 remark="+remark, err, codes.InvalidArgument)
	}
	if flow := loadRefundFlow(t, s, no); flow.RetryCount != 10 || flow.Escalated != 1 {
		t.Errorf("拒绝后预算不得变动, retry_count=%d escalated=%d", flow.RetryCount, flow.Escalated)
	}
	if latestNotifyLogOpt(s, p.PaymentNo, model.NotifyKindRefundReset) != nil {
		t.Errorf("拒绝执行不应留下重置留痕")
	}
}

// TestResetRefundFlowReopensPending 待退款流水：预算归零 + 留痕 + 原待办销账
func TestResetRefundFlowReopensPending(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderID := int64(refundResetOrderIDBase + 2)
	no := "ZFTEST-RST-PENDING"
	p := seedSchedPayment(t, s, schedSeed{No: "ZFTEST-RST-PAY-2", OrderID: orderID, Amount: 66.00, Status: 2, CommunityID: 3})
	seedRefundFlow(t, s, no, p.PaymentNo, orderID, 1, 10, 1)
	seedRefundEscalationTodo(t, s, p, no)

	resp, err := s.ResetRefundFlow(ctx, &payment.ResetRefundFlowReq{
		OrderId: orderID, RefundNo: no, Remark: "微信商户平台显示退款处理中", OperatorId: 7, OperatorName: "测试管理员",
	})
	if err != nil {
		t.Fatalf("重置失败: %v", err)
	}
	if !resp.GetRecovered() {
		t.Errorf("在途流水应被重置, recovered=false message=%q", resp.GetMessage())
	}

	flow := loadRefundFlow(t, s, no)
	if flow.Status != 1 {
		t.Errorf("重置不得改写流水状态, got %d", flow.Status)
	}
	if flow.RetryCount != 0 || flow.Escalated != 0 {
		t.Errorf("预算与转办标记应归零, retry_count=%d escalated=%d", flow.RetryCount, flow.Escalated)
	}

	// 留痕：kind=7/result=11，handled=1（人工动作是轨迹不是待办）
	trace := latestNotifyLog(t, s, p.PaymentNo, model.NotifyKindRefundReset)
	if trace.Result != model.NotifyResultRefundReset || trace.Handled != model.NotifyHandled {
		t.Errorf("留痕结果/销账位不符, result=%d handled=%d", trace.Result, trace.Handled)
	}
	if !strings.Contains(trace.ErrorMsg, "微信商户平台显示退款处理中") || !strings.Contains(trace.ErrorMsg, "测试管理员") {
		t.Errorf("留痕应带核实依据与操作人, got %q", trace.ErrorMsg)
	}
	if trace.CommunityID != p.CommunityID || trace.OrderID != orderID {
		t.Errorf("留痕归属快照缺失，社区管理员将查不到, community=%d order=%d", trace.CommunityID, trace.OrderID)
	}

	// 原转办待办必须被销账，否则队列里那条还亮着
	var pending int64
	if err := s.db.GetDb().Model(&model.NotifyLog{}).
		Where("payment_no = ? AND result = ? AND handled = ?", p.PaymentNo, model.NotifyResultSyncFailed, model.NotifyPending).
		Count(&pending).Error; err != nil {
		t.Fatalf("统计待办失败: %v", err)
	}
	if pending != 0 {
		t.Errorf("重置应销掉该退款单的转办待办, 仍待处理 %d 条", pending)
	}
}

// TestResetRefundFlowRejectsTerminal 已终态流水一律拒绝：成功态防渠道二次出款，失败态本就能重发
func TestResetRefundFlowRejectsTerminal(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	cases := []struct {
		name   string
		order  int64
		flowNo string
		status int32
	}{
		{"已成功", refundResetOrderIDBase + 3, "ZFTEST-RST-DONE", 2},
		{"已失败", refundResetOrderIDBase + 4, "ZFTEST-RST-FAILED", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := seedSchedPayment(t, s, schedSeed{No: "ZFTEST-RST-PAY-" + c.flowNo, OrderID: c.order, Amount: 66.00, Status: 2, CommunityID: 3})
			seedRefundFlow(t, s, c.flowNo, p.PaymentNo, c.order, c.status, 10, 1)

			_, err := s.ResetRefundFlow(ctx, &payment.ResetRefundFlowReq{OrderId: c.order, RefundNo: c.flowNo, Remark: "已核实"})
			assertResetErrCode(t, "终态流水拒绝 status="+fmt.Sprint(c.status), err, codes.FailedPrecondition)
			if flow := loadRefundFlow(t, s, c.flowNo); flow.RetryCount != 10 || flow.Status != c.status {
				t.Errorf("拒绝后流水不得变化, status=%d retry_count=%d", flow.Status, flow.RetryCount)
			}
			if latestNotifyLogOpt(s, p.PaymentNo, model.NotifyKindRefundReset) != nil {
				t.Errorf("拒绝执行不应留下重置留痕")
			}
		})
	}
}

// TestResetRefundFlowNoFlow 无对应流水：回 recovered=false（重发前失败流水会被清理，属正常窗口）
func TestResetRefundFlowNoFlow(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	orderID := int64(refundResetOrderIDBase + 5)
	resp, err := s.ResetRefundFlow(ctx, &payment.ResetRefundFlowReq{OrderId: orderID, RefundNo: "TKNOTEXIST", Remark: "已核实"})
	if err != nil {
		t.Fatalf("无流水不应报错: %v", err)
	}
	if resp.GetRecovered() || resp.GetMessage() == "" {
		t.Errorf("应回 recovered=false 并说明原因, recovered=%v message=%q", resp.GetRecovered(), resp.GetMessage())
	}
}

// TestResetRefundFlowOrderScope 订单归属不符：按 (refund_no, order_id) 定位，不得改动他单流水
func TestResetRefundFlowOrderScope(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	owner := int64(refundResetOrderIDBase + 6)
	other := int64(refundResetOrderIDBase + 7)
	no := "ZFTEST-RST-SCOPE"
	p := seedSchedPayment(t, s, schedSeed{No: "ZFTEST-RST-PAY-6", OrderID: owner, Amount: 66.00, Status: 2, CommunityID: 3})
	seedRefundFlow(t, s, no, p.PaymentNo, owner, 1, 10, 1)

	resp, err := s.ResetRefundFlow(ctx, &payment.ResetRefundFlowReq{OrderId: other, RefundNo: no, Remark: "已核实"})
	if err != nil {
		t.Fatalf("跨订单定位不应报错: %v", err)
	}
	if resp.GetRecovered() {
		t.Errorf("跨订单请求不得重置别人的流水")
	}
	if flow := loadRefundFlow(t, s, no); flow.RetryCount != 10 {
		t.Errorf("归属不符时流水预算应保持, retry_count=%d", flow.RetryCount)
	}
}
