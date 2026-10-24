package service

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 退款单人工恢复自动收敛（B2）测试
 *
 * 盯的死角：卡单重试累计核查达 refundMaxRetry 后自动批次永久放弃这笔退款，
 * 而渠道（微信）退款允许 1-3 个工作日到账，后来给出终态也没人来收尾，
 * 后台唯一能做的是「重试退款」（再次触渠道出款，且支付侧在途流水会拒绝受理）。
 * 现口径：新增「恢复自动收敛」出口，先归零 payment 侧流水预算、再复位本侧
 * retry_count/last_checked_at（UpdateColumns 绕开钩子，updated_at 不动，下一轮立即起扫），
 * 全程不触渠道出款；支付侧拒绝时本侧预算保持原样。
 *
 * 运行：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/order/service/ -run TestResetRefundRetry -v
 *
 * 测试数据沿用 SCHEDTEST- 前缀（退款三表由 newRefundTestEnv 的 cleanup 删除）。
 */

// resetReq 构造恢复自动收敛请求（社区固定 1，与 seedSchedOrder 落库的社区一致）
// 参数：
//   - id: 退款单 ID
//   - remark: 渠道核实依据
//
// 返回值：
//   - *order.ResetRefundRetryReq: 请求体
func resetReq(id int64, remark string) *order.ResetRefundRetryReq {
	return &order.ResetRefundRetryReq{
		Id:           id,
		CommunityId:  1,
		OperatorId:   7,
		OperatorName: "测试管理员",
		Remark:       remark,
	}
}

// TestResetRefundRetryRequiresRemark 核实依据缺失：拒绝执行，预算与轨迹零改动
func TestResetRefundRetryRequiresRemark(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()
	orderID := seedSchedOrder(t, env.db, "B2-REMARK", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	r := seedStuckRefund(t, env.db, refundSeed{No: "B2-REMARK", OrderID: orderID, RetryCount: 24, CheckedAgo: time.Minute})

	err := env.svc.ResetRefundRetry(ctx, resetReq(r.ID, "   "))
	assertBizCode(t, err, codes.InvalidArgument)

	checked, retry, _, _ := refundCheckRow(t, env.db, r.ID)
	if retry != 24 || checked == nil {
		t.Errorf("拒绝后预算与指针不得变动, retry_count=%d last_checked_at=%v", retry, checked)
	}
	if got := countAction(refundLogActions(t, env.db, r.ID), "reset"); got != 0 {
		t.Errorf("拒绝执行不得留下 reset 轨迹, got %d 条", got)
	}
	if got := len(env.sched.payment.refundResetCalls()); got != 0 {
		t.Errorf("拒绝执行不得调用支付侧重置, got %d 次", got)
	}
}

// TestResetRefundRetryReopensBudget 用满预算的退款单：两侧预算归零、updated_at 不动、轨迹落账
func TestResetRefundRetryReopensBudget(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()
	orderID := seedSchedOrder(t, env.db, "B2-OK", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	r := seedStuckRefund(t, env.db, refundSeed{No: "B2-OK", OrderID: orderID, RetryCount: 24, CheckedAgo: 5 * time.Minute})

	_, _, beforeUpdated, _ := refundCheckRow(t, env.db, r.ID)
	if err := env.svc.ResetRefundRetry(ctx, resetReq(r.ID, "微信商户平台显示退款处理中")); err != nil {
		t.Fatalf("恢复自动收敛失败: %v", err)
	}

	checked, retry, updatedAt, status := refundCheckRow(t, env.db, r.ID)
	if retry != 0 {
		t.Errorf("重试预算应归零, got %d", retry)
	}
	if checked != nil {
		t.Errorf("节流指针应置 NULL 使其排回队首, got %v", *checked)
	}
	// updated_at 是「停留超过 stuckRecoverAfter 才算卡单」的起算列，被刷新会让刚重置的单
	// 再等一整个停留阈值才被调度器看见
	if !updatedAt.Equal(beforeUpdated) {
		t.Errorf("重置不得刷新 updated_at, before=%v after=%v", beforeUpdated, updatedAt)
	}
	if status != 4 {
		t.Errorf("重置只恢复收敛不改退款单状态, got %d", status)
	}

	calls := env.sched.payment.refundResetCalls()
	if len(calls) != 1 {
		t.Fatalf("应恰好调用一次支付侧重置, got %d", len(calls))
	}
	if calls[0].RefundNo != r.RefundNo || calls[0].OrderId != orderID {
		t.Errorf("重置入参应以退款单记录为准, got refund_no=%s order_id=%d", calls[0].RefundNo, calls[0].OrderId)
	}
	if calls[0].Remark != "微信商户平台显示退款处理中" || calls[0].OperatorName != "测试管理员" {
		t.Errorf("核实依据与操作人应透传到支付侧, got remark=%s operator=%s", calls[0].Remark, calls[0].OperatorName)
	}

	var log model.RefundLog
	if err := env.db.GetDb().Model(&model.RefundLog{}).
		Where("refund_id = ? AND action = ?", r.ID, "reset").First(&log).Error; err != nil {
		t.Fatalf("应落一条 reset 轨迹: %v", err)
	}
	if log.OperatorType != "admin" || log.OperatorID != 7 || log.Remark != "微信商户平台显示退款处理中" {
		t.Errorf("轨迹操作人与依据不符, type=%s operator=%d remark=%q", log.OperatorType, log.OperatorID, log.Remark)
	}

	// 重置后调度器当轮即可重新核查：last_checked_at 为 NULL 在 asc 轮转里排最前
	stat := NewScheduler(env.db, env.sched.redis, env.svc, nil, nil).retryStuckRefunds(ctx)
	if stat.failed != 0 {
		t.Errorf("重置后重试轮次不应异常, failed=%d", stat.failed)
	}
}

// TestResetRefundRetryPaymentSideRefuses 支付侧拒绝（流水已终态）：本侧预算保持原样
func TestResetRefundRetryPaymentSideRefuses(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()
	orderID := seedSchedOrder(t, env.db, "B2-REFUSE", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	r := seedStuckRefund(t, env.db, refundSeed{No: "B2-REFUSE", OrderID: orderID, RetryCount: 24, CheckedAgo: time.Minute})
	env.sched.payment.mu.Lock()
	env.sched.payment.refundResetErrs[r.RefundNo] = true
	env.sched.payment.mu.Unlock()

	if err := env.svc.ResetRefundRetry(ctx, resetReq(r.ID, "已核实")); err == nil {
		t.Fatalf("支付侧拒绝时本侧应返回错误")
	}
	_, retry, _, _ := refundCheckRow(t, env.db, r.ID)
	if retry != 24 {
		t.Errorf("支付侧拒绝后本侧预算不得归零, retry_count=%d", retry)
	}
	if got := countAction(refundLogActions(t, env.db, r.ID), "reset"); got != 0 {
		t.Errorf("支付侧拒绝时不得落 reset 轨迹, got %d 条", got)
	}
}

// TestResetRefundRetryFreshStuck 新卡单（预算本就是目标值）：重置应成功而非误报状态已变更
// 回归背景：MySQL 默认 changed-rows 语义（DSN 未开 clientFoundRows）下，retry_count=0 且
// last_checked_at=NULL 的退款单执行重置时 UPDATE 无值变化、affected=0，旧实现直接报
// 「退款单状态已变更，请刷新后重试」且反复重试永远失败；修复后回查确认仍是退款中且预算
// 已是目标值即视为等效已恢复
func TestResetRefundRetryFreshStuck(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()
	orderID := seedSchedOrder(t, env.db, "B2-FRESH", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	// CheckedAgo 省略（0）= 从未被核查（last_checked_at NULL），RetryCount 零值 = 预算未动过
	r := seedStuckRefund(t, env.db, refundSeed{No: "B2-FRESH", OrderID: orderID, UpdatedAgo: time.Hour})

	if err := env.svc.ResetRefundRetry(ctx, resetReq(r.ID, "微信商户平台显示退款处理中")); err != nil {
		t.Fatalf("新卡单重置应成功（旧实现误报状态已变更）: %v", err)
	}

	checked, retry, _, status := refundCheckRow(t, env.db, r.ID)
	if retry != 0 || checked != nil || status != 4 {
		t.Errorf("新卡单预算与指针应保持目标值, retry=%d checked=%v status=%d", retry, checked, status)
	}
	if got := len(env.sched.payment.refundResetCalls()); got != 1 {
		t.Errorf("应恰好调用一次支付侧重置, got %d 次", got)
	}
	if got := countAction(refundLogActions(t, env.db, r.ID), "reset"); got != 1 {
		t.Errorf("应落一条 reset 轨迹, got %d 条", got)
	}
}

// TestResetRefundRetryGuards 状态与社区门禁：非退款中拒绝，跨社区拒绝
func TestResetRefundRetryGuards(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()

	t.Run("非退款中状态拒绝", func(t *testing.T) {
		orderID := seedSchedOrder(t, env.db, "B2-PENDING", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
		r := seedStuckRefund(t, env.db, refundSeed{No: "B2-PENDING", OrderID: orderID, Status: 1, RetryCount: 3})
		err := env.svc.ResetRefundRetry(ctx, resetReq(r.ID, "已核实"))
		assertBizCode(t, err, codes.InvalidArgument)
	})

	t.Run("跨社区拒绝", func(t *testing.T) {
		orderID := seedSchedOrder(t, env.db, "B2-CROSS", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
		r := seedStuckRefund(t, env.db, refundSeed{No: "B2-CROSS", OrderID: orderID, RetryCount: 24})
		req := resetReq(r.ID, "已核实")
		req.CommunityId = 2
		err := env.svc.ResetRefundRetry(ctx, req)
		assertBizCode(t, err, codes.PermissionDenied)
		_, retry, _, _ := refundCheckRow(t, env.db, r.ID)
		if retry != 24 {
			t.Errorf("越权请求不得改动预算, retry_count=%d", retry)
		}
	})
}
