package service

import (
	"slices"
	"testing"

	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 支付异常 WS 告警的类别口径测试（纯函数，无外部依赖）
 *
 * 盯的故障形态：
 *   1. 白名单放开幂等忽略/验签失败这类大流量类别，语音告警被噪音淹没；
 *   2. 去重按单一 kind 划分，同一笔挂账「系统检出(3)」后管理员点同步又核实出「人工(8)」，
 *      同一条待办响两遍；反过来把放弃对账(6)也并进挂账组，会把真正的升级事实吞掉。
 */

// TestAnomalyAlertKinds 白名单只含叫醒类别
func TestAnomalyAlertKinds(t *testing.T) {
	for _, kind := range []int32{model.NotifyKindStuck, model.NotifyKindManualStuckCheck, model.NotifyKindGiveUp} {
		if _, ok := anomalyAlertKinds[kind]; !ok {
			t.Errorf("kind=%d 应在告警白名单内", kind)
		}
	}
	// 噪音与量大类刻意不推：进来就是回归
	for _, kind := range []int32{
		model.NotifyKindCallback,
		model.NotifyKindReconcile,
		model.NotifyKindChannelClose,
		model.NotifyKindManualSync,
		model.NotifyKindRefundReset,
	} {
		if _, ok := anomalyAlertKinds[kind]; ok {
			t.Errorf("kind=%d 不属叫醒类别，不应进入告警白名单", kind)
		}
	}
}

// TestAnomalyAlertGroup 去重组口径：3 与 8 同组互斥，6 独立成组，白名单外不参与去重
func TestAnomalyAlertGroup(t *testing.T) {
	stuck := anomalyAlertGroup(model.NotifyKindStuck)
	if !slices.Contains(stuck, model.NotifyKindStuck) || !slices.Contains(stuck, model.NotifyKindManualStuckCheck) {
		t.Errorf("挂账组应同时覆盖系统检出与人工核实两个来源, got=%v", stuck)
	}
	manual := anomalyAlertGroup(model.NotifyKindManualStuckCheck)
	if !slices.Equal(stuck, manual) {
		t.Errorf("3 与 8 必须同组（同一事实的两个来源）, got3=%v got8=%v", stuck, manual)
	}

	giveUp := anomalyAlertGroup(model.NotifyKindGiveUp)
	if len(giveUp) != 1 || giveUp[0] != model.NotifyKindGiveUp {
		t.Errorf("放弃对账应独立成组，不被挂账告警吞掉, got=%v", giveUp)
	}
	if slices.Contains(giveUp, model.NotifyKindStuck) {
		t.Error("放弃对账与挂账同组会漏掉升级告警")
	}

	for _, kind := range []int32{model.NotifyKindCallback, model.NotifyKindManualSync} {
		if got := anomalyAlertGroup(kind); got != nil {
			t.Errorf("非告警类别 kind=%d 不应有去重组, got=%v", kind, got)
		}
	}
}
