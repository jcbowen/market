package service

import (
	"context"
	"testing"
	"time"

	"github.com/jcbowen/market/src/backend/common/proto/order"
)

/**
 * C 端订单状态角标统计口径测试（集成测试，需本地 docker 栈）
 *
 * 背景（2026-10-23 口径调整）：退款/售后角标曾按订单状态 7/8 统计，
 * status=8 已退款是终态导致处理完成的订单永远亮角标；调整为
 * 「存在待审核售后单(refunds.status=1)的订单数」——商家同意/拒绝后不再亮。
 *
 * 运行方式（TEST_MYSQL_PASSWORD 未设置时自动 skip）：
 *   TEST_MYSQL_PASSWORD=xxx TEST_REDIS_PASSWORD=yyy go test ./services/order/service/ -run TestGetUserOrderStatusCounts -v
 *
 * 覆盖：
 *   - 退款中订单 + 待审核售后单计入角标，同订单多笔待审核去重计 1；
 *   - 已同意(4)/已拒绝(3)售后单、已退款(8)订单均不再计入；
 *   - 用户隔离：统计不串号；
 *   - 状态 7/8 不混入待发货/待收货等 pending 口径。
 */

// TestGetUserOrderStatusCountsAfterSale 退款/售后角标口径：仅存在待审核售后单的订单计入
func TestGetUserOrderStatusCountsAfterSale(t *testing.T) {
	env := newRefundTestEnv(t)
	svc := NewOrderService(env.db, nil)
	ctx := context.Background()
	// 统计用户与 seedSchedOrder/seedRefund 的固定测试用户(99100001)保持一致，
	// 借基线快照断言增量，天然隔离本地库既有数据
	const uid int64 = 99100001

	// 基线快照：统计按 user_id 聚合，本地库可能存在其它测试残留，断言增量而非绝对值
	base, err := svc.GetUserOrderStatusCounts(ctx, &order.GetUserOrderStatusCountsReq{UserId: uid})
	if err != nil {
		t.Fatalf("查询基线角标失败: %v", err)
	}

	// 场景1：退款中(7)订单 + 两笔待审核售后单 → 去重计 1
	o1 := seedSchedOrder(t, env.db, "counts-a", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	seedRefund(t, env.db, o1, 1, 5)
	seedRefund(t, env.db, o1, 1, 2)

	// 场景2：退款中(7)订单 + 已同意(4)/已拒绝(3)售后单 → 不计入
	o2 := seedSchedOrder(t, env.db, "counts-b", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	seedRefund(t, env.db, o2, 4, 5)
	seedRefund(t, env.db, o2, 3, 1)

	// 场景3：已退款(8)终态订单 + 退款成功(5)售后单 → 不计入
	o3 := seedSchedOrder(t, env.db, "counts-c", 8, schedTimeAgo(2*time.Hour), schedTimeAgo(time.Hour))
	seedRefund(t, env.db, o3, 5, 10)

	// 场景4：已完成(5)订单 + 待审核(1)售后单 → 计入
	// 角标口径只绑「售后单是否待审核」，不硬绑 orders.status=7：
	// 商家对已完成单发起/同意部分退款后再拒、或已完成单申请售后，都可能停在状态 5 + 待审核，
	// 硬绑 7 会让这类订单永远不亮标，用户看不见自己的待处理售后（1.6）
	o4 := seedSchedOrder(t, env.db, "counts-d", 5, schedTimeAgo(2*time.Hour), schedTimeAgo(time.Hour))
	seedRefund(t, env.db, o4, 1, 3)

	// 场景5：用户已软删的订单 + 待审核(1)售后单 → 不计入
	// 软删只影响用户侧列表与角标（后台仍全量可见、可恢复），删掉的单不该继续催用户
	o5 := seedSchedOrder(t, env.db, "counts-e", 5, schedTimeAgo(2*time.Hour), schedTimeAgo(time.Hour))
	seedRefund(t, env.db, o5, 1, 3)
	if err := env.db.GetDb().Exec("UPDATE orders SET user_deleted = 1 WHERE id = ?", o5).Error; err != nil {
		t.Fatalf("置软删标记失败: %v", err)
	}

	got, err := svc.GetUserOrderStatusCounts(ctx, &order.GetUserOrderStatusCountsReq{UserId: uid})
	if err != nil {
		t.Fatalf("查询角标失败: %v", err)
	}
	if want := base.AfterSale + 2; got.AfterSale != want {
		t.Fatalf("AfterSale = %d, want %d（场景1、4 计入；场景1 多笔去重计 1，软删单场景5 不计）", got.AfterSale, want)
	}
	// 状态 7/8 不得混入 pending 口径
	if got.PendingShip != base.PendingShip {
		t.Fatalf("PendingShip = %d, want %d（status=7 不应计入待发货）", got.PendingShip, base.PendingShip)
	}
	if got.PendingReceive != base.PendingReceive {
		t.Fatalf("PendingReceive = %d, want %d（status=8 不应计入待收货）", got.PendingReceive, base.PendingReceive)
	}

	// 用户隔离：另一用户的角标不受上述数据影响（同样以基线快照防本地残留干扰）
	otherBase, err := svc.GetUserOrderStatusCounts(ctx, &order.GetUserOrderStatusCountsReq{UserId: 99100010})
	if err != nil {
		t.Fatalf("查询其他用户基线角标失败: %v", err)
	}
	other, err := svc.GetUserOrderStatusCounts(ctx, &order.GetUserOrderStatusCountsReq{UserId: 99100010})
	if err != nil {
		t.Fatalf("查询其他用户角标失败: %v", err)
	}
	if other.AfterSale != otherBase.AfterSale {
		t.Fatalf("用户隔离失败: 其他用户 AfterSale = %d, want %d", other.AfterSale, otherBase.AfterSale)
	}
}
