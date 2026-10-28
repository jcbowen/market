package service

import "testing"

/**
 * 订单状态机流转表锁定测试（纯函数，无需数据库）
 *
 * 重点锁定 2026-10-18 修复：100（调度器超时取消处理中）→ 2 必须放行，
 * 否则支付回调二阶段/调度器补同步会被状态机拒绝，已收款订单在 1↔100 间死循环；
 * 同时锁定各非法回退（取消/完成后不可再支付等）不被误放行。
 * 2026-10-28 追加：已完成(5)→退款中(7) 放行（已完成单售后窗口），退款发起不再走整行 Save 旁路。
 */

// TestIsTransitionAllowed 状态机合法流转判定
func TestIsTransitionAllowed(t *testing.T) {
	cases := []struct {
		name     string
		from, to int32
		want     bool
	}{
		{"待支付→已支付", 1, 2, true},
		{"取消处理中→已支付（调度器补同步）", 100, 2, true},
		{"已支付→备货中", 2, 3, true},
		{"备货中→配送中（发货）", 3, 4, true},
		{"已支付→已完成（自提确认）", 2, 5, true},
		{"配送中→已完成（确认收货）", 4, 5, true},
		{"已支付→退款中", 2, 7, true},
		{"配送中→退款中", 4, 7, true},
		{"已完成→退款中（售后期内）", 5, 7, true},
		{"待支付→退款中（未收款）", 1, 7, false},
		{"已取消→退款中", 6, 7, false},
		{"退款中→已退款", 7, 8, true},
		{"已取消→已支付（非法回退）", 6, 2, false},
		{"已完成→已支付（非法回退）", 5, 2, false},
		{"已退款→已支付（非法回退）", 8, 2, false},
		{"待支付→已完成（跳态）", 1, 5, false},
		{"已退款→退款中（非法回退）", 8, 7, false},
		{"未知目标状态", 1, 99, false},
		{"未知来源状态", 42, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransitionAllowed(tc.from, tc.to); got != tc.want {
				t.Fatalf("isTransitionAllowed(%d, %d) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}
