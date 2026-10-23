package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 挂账待办的检出来源与审计留痕测试（B3）
 *
 * 盯的故障形态：挂账待办只记「渠道已收款」这一句结论——
 *   1. 分不清是系统自动扫出的（回调/关单链路有洞）还是管理员点按钮核实出的
 *      （通常说明该单早已超出调度窗口），两种成因的后续追查方向完全相反；
 *   2. 查不到是谁核实的、当时本地是什么状态，事后复盘只能靠猜。
 * 现口径：待办按来源拆 kind（3 系统 / 8 人工），检出留痕写 detect_remark 列并带上原状态快照；
 * detect_remark 刻意与 remark 分列——销账 SQL 整列覆写 remark，同列留痕会在销账那一刻被抹掉。
 *
 * 运行：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/payment/service/ -run TestStuckAudit -v
 *
 * 测试数据沿用 ZFTEST- 前缀（支付与流水由 newPaymentTestEnv 的 cleanup 删除）。
 */

// stuckAuditOrderIDBase 本组用例的订单 ID 偏移（避开同步与调度器用例占用的段）
const stuckAuditOrderIDBase = syncTestOrderIDBase + 30

// seedClosedStuckPayment 造一笔「已关闭而渠道已收款」的支付单，返回可直接登记挂账的渠道应答
//
// 参数：
//   - t: 测试对象
//   - s: 被测服务
//   - no: 支付单号（唯一，重复调用会撞单号）
//   - orderID: 订单 ID
//   - payType: 支付方式（1 微信 2 支付宝 3 货到付款 4 现金）
//
// 返回值：
//   - *channelPayResult: 与「渠道已收款」对应的应答（交易号由单号派生，便于断言）
func seedClosedStuckPayment(t *testing.T, s *PaymentService, no string, orderID int64, payType int32) *channelPayResult {
	t.Helper()
	seedSchedPayment(t, s, schedSeed{
		No: no, OrderID: orderID, Amount: 18.80, PayType: payType, Status: 5,
		CreatedAgo: 3 * time.Hour, UpdatedAgo: 3 * time.Hour, CommunityID: 3,
	})
	p := reloadPaymentByNo(t, s, no)
	return &channelPayResult{
		State:         ChannelStatePaid,
		TransactionID: "TX-AUDIT-" + no,
		Amount:        p.Amount,
		Raw:           "SUCCESS",
	}
}

// TestStuckAuditSplitsDetectSource 同一种挂账事实按检出来源分 kind 落待办
// 系统路径（对账调度器、关单前核实）记 kind=3，后台人工核实记 kind=8，
// 两边的 detect_remark 分别标注系统与操作人，异常页据此才能分开统计
func TestStuckAuditSplitsDetectSource(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	systemNo, manualNo := "ZFTEST-AUDIT-SYSTEM", "ZFTEST-AUDIT-MANUAL"
	resSys := seedClosedStuckPayment(t, s, systemNo, stuckAuditOrderIDBase+1, 1)
	resManual := seedClosedStuckPayment(t, s, manualNo, stuckAuditOrderIDBase+2, 2)

	s.recordStuckPayment(ctx, reloadPaymentByNo(t, s, systemNo), resSys, systemActor)
	s.recordStuckPayment(ctx, reloadPaymentByNo(t, s, manualNo), resManual, manualActor())

	sysRow := latestNotifyLog(t, s, systemNo, model.NotifyKindStuck)
	assertDetectTrace(t, sysRow, "系统自动触发", "支付单状态 5")

	manualRow := latestNotifyLog(t, s, manualNo, model.NotifyKindManualStuckCheck)
	assertDetectTrace(t, manualRow, manualTestOperatorName, "支付单状态 5", "支付方式 2")
	if manualRow.Handled != model.NotifyPending {
		t.Errorf("人工检出的挂账同样要是待办, got handled=%d", manualRow.Handled)
	}
}

// TestStuckAuditDedupsAcrossSources 已存在系统检出的待办时，人工核实不再开单
// 去重必须跨 kind：按单 kind 去重会让同一笔挂账在异常页出现两条等价待办
func TestStuckAuditDedupsAcrossSources(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	no := "ZFTEST-AUDIT-CROSSDEDUP"
	res := seedClosedStuckPayment(t, s, no, stuckAuditOrderIDBase+3, 1)
	p := reloadPaymentByNo(t, s, no)
	s.recordStuckPayment(ctx, p, res, systemActor)

	// 人工再核实同一笔：待办不翻倍，但要留下「谁又核实过」的 kind=5 留痕
	s.recordStuckPayment(ctx, p, res, manualActor())

	var count int64
	if err := s.db.GetDb().Model(&model.NotifyLog{}).
		Where("payment_no = ? AND kind IN (?, ?) AND result = ?",
			no, model.NotifyKindStuck, model.NotifyKindManualStuckCheck, model.NotifyResultStuck).
		Count(&count).Error; err != nil {
		t.Fatalf("统计挂账待办失败: %v", err)
	}
	if count != 1 {
		t.Errorf("跨检出来源应共用一条挂账待办, got %d 条, want 1", count)
	}
	trace := latestNotifyLog(t, s, no, model.NotifyKindManualSync)
	if trace.Result != model.NotifyResultIgnored {
		t.Errorf("重复核实应落幂等留痕, got result=%d", trace.Result)
	}
	if trace.Handled != model.NotifyHandled {
		t.Errorf("幂等留痕不得占用异常页, got handled=%d", trace.Handled)
	}
	assertDetectTrace(t, trace, manualTestOperatorName)
}

// TestStuckAuditTraceSurvivesHandling 销账只写 remark，不得抹掉检出留痕
// 这是 detect_remark 单独成列的全部理由：若留痕写进 remark，
// 运营销账填一句「已渠道退款」就会把「谁检出的、当时什么状态」整列覆盖
func TestStuckAuditTraceSurvivesHandling(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	no := "ZFTEST-AUDIT-HANDLE"
	res := seedClosedStuckPayment(t, s, no, stuckAuditOrderIDBase+4, 2)
	s.recordStuckPayment(ctx, reloadPaymentByNo(t, s, no), res, manualActor())
	before := latestNotifyLog(t, s, no, model.NotifyKindManualStuckCheck)
	trace := before.DetectRemark

	if err := s.MarkPaymentAnomalyHandled(ctx, before.ID, "商户平台已按交易号人工退款", 3); err != nil {
		t.Fatalf("销账失败: %v", err)
	}
	after := latestNotifyLog(t, s, no, model.NotifyKindManualStuckCheck)
	if after.Handled != model.NotifyHandled {
		t.Errorf("销账应置已处理, got handled=%d", after.Handled)
	}
	if after.DetectRemark != trace {
		t.Errorf("销账不得覆写检出留痕, before=%q after=%q", trace, after.DetectRemark)
	}
	if !strings.Contains(after.Remark, "商户平台") {
		t.Errorf("销账依据应写入 remark, got %q", after.Remark)
	}
}

// TestStuckAuditClosedCallbackStaysSystemKind 已关闭支付单收到成功回调仍按系统检出登记
// 回调没有操作人，写 kind=8 等于把渠道链路的故障算到某个管理员头上，追溯方向直接错
func TestStuckAuditClosedCallbackStaysSystemKind(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	no := "ZFTEST-AUDIT-CALLBACK"
	seedClosedStuckPayment(t, s, no, stuckAuditOrderIDBase+5, 1)
	// 已关闭单的回调走 writeCallbackLog 的挂账分支：不改资金状态，只按 kind=3 登记待办
	if err := s.handlePaySuccess(ctx, &payCallbackParams{
		PaymentNo:     no,
		TransactionID: "TX-AUDIT-CALLBACK",
		Amount:        18.80,
		NotifyKind:    model.NotifyKindCallback,
		Channel:       1,
	}); err != nil {
		t.Fatalf("已关闭单的回调应幂等忽略而非报错: %v", err)
	}
	row := latestNotifyLog(t, s, no, model.NotifyKindStuck)
	if row.Result != model.NotifyResultStuck {
		t.Errorf("回调挂账应记资金挂账, got %d", row.Result)
	}
	if got := latestNotifyLogOpt(s, no, model.NotifyKindManualStuckCheck); got != nil {
		t.Errorf("回调路径没有操作人，不得记成人工检出, got %+v", got)
	}
	if got := reloadPaymentByNo(t, s, no); got.Status != 5 {
		t.Errorf("回调挂账不得改动支付单状态, got %d", got.Status)
	}
}
