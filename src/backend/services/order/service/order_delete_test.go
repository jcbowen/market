package service

import (
	"context"
	"testing"
	"time"

	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
)

/**
 * C 端订单删除（软删除）集成测试（需本地 docker 栈）
 *
 * 运行方式（TEST_MYSQL_PASSWORD 未设置时自动 skip）：
 *   TEST_MYSQL_PASSWORD=xxx TEST_REDIS_PASSWORD=yyy go test ./services/order/service/ -run TestDeleteOrder -v
 *
 * 覆盖：
 *   - 守卫：归属校验 / 非终态拒绝 / 有进行中售后（1待审核 4退款中）拒绝 / 重复删除拒绝；
 *   - 软删生效：user_deleted=1 落库后，C 端列表（UserId>0 路径）排除、后台路径（UserId=0）不受影响、
 *     GetOrder 详情保留可达（售后进度/支付详情链路依赖）；
 *   - 角标回归：软删前后 GetUserOrderStatusCounts 一致（终态订单本不在 pending 口径，
 *     user_deleted 过滤为防御性冗余，防后续口径调整时回归）。
 */

// TestDeleteOrderGuards 删除守卫：归属 / 终态 / 进行中售后 / 重复删除
func TestDeleteOrderGuards(t *testing.T) {
	env := newRefundTestEnv(t)
	svc := NewOrderService(env.db, nil)
	ctx := context.Background()
	const uid int64 = 99100001
	const otherUID int64 = 99100010

	// 非终态（2 已支付）拒绝
	active := seedSchedOrder(t, env.db, "del-a", 2, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	if err := svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: active, UserId: uid}); err == nil {
		t.Fatal("非终态订单应拒绝删除")
	}

	// 非本人拒绝（用终态订单，确保命中归属分支而非状态分支）
	mine := seedSchedOrder(t, env.db, "del-b", 5, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	if err := svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: mine, UserId: otherUID}); err == nil {
		t.Fatal("非本人订单应拒绝删除")
	}

	// 待审核售后单（status=1）拒绝
	withPending := seedSchedOrder(t, env.db, "del-c", 8, schedTimeAgo(2*time.Hour), schedTimeAgo(time.Hour))
	seedRefund(t, env.db, withPending, 1, 5)
	if err := svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: withPending, UserId: uid}); err == nil {
		t.Fatal("存在待审核售后单应拒绝删除")
	}

	// 退款中售后单（status=4）拒绝
	withRefunding := seedSchedOrder(t, env.db, "del-d", 8, schedTimeAgo(2*time.Hour), schedTimeAgo(time.Hour))
	seedRefund(t, env.db, withRefunding, 4, 5)
	if err := svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: withRefunding, UserId: uid}); err == nil {
		t.Fatal("存在退款中售后单应拒绝删除")
	}
}

// TestDeleteOrderSoftDeleteVisibility 软删生效与可见性：C 端列表排除 / 后台不受影响 / 详情可达 / 角标回归
func TestDeleteOrderSoftDeleteVisibility(t *testing.T) {
	env := newRefundTestEnv(t)
	svc := NewOrderService(env.db, nil)
	ctx := context.Background()
	const uid int64 = 99100001

	done := seedSchedOrder(t, env.db, "del-e", 5, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	canceled := seedSchedOrder(t, env.db, "del-f", 6, schedTimeAgo(2*time.Hour), schedTimeAgo(time.Hour))

	// 基线快照：统计按 user_id 聚合，本地库可能存在其它测试残留，断言增量而非绝对值
	_, baseTotal, err := svc.ListOrders(ctx, &order.ListOrdersReq{
		UserId:     uid,
		Pagination: &common.Pagination{Page: 1, PageSize: 100},
	})
	if err != nil {
		t.Fatalf("查询基线列表失败: %v", err)
	}
	baseCounts, err := svc.GetUserOrderStatusCounts(ctx, &order.GetUserOrderStatusCountsReq{UserId: uid})
	if err != nil {
		t.Fatalf("查询基线角标失败: %v", err)
	}

	// 软删两单
	for _, id := range []int64{done, canceled} {
		if err := svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: id, UserId: uid}); err != nil {
			t.Fatalf("删除订单 %d 失败: %v", id, err)
		}
	}

	// 重复删除拒绝（条件更新 user_deleted=0 防并发路径）
	if err := svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: done, UserId: uid}); err == nil {
		t.Fatal("重复删除应拒绝")
	}

	// C 端列表排除已删单：总数减 2 且列表不含
	list, total, err := svc.ListOrders(ctx, &order.ListOrdersReq{
		UserId:     uid,
		Pagination: &common.Pagination{Page: 1, PageSize: 100},
	})
	if err != nil {
		t.Fatalf("查询列表失败: %v", err)
	}
	if total != baseTotal-2 {
		t.Fatalf("C 端列表 total = %d, want %d（已删单应排除）", total, baseTotal-2)
	}
	deleted := map[int64]bool{done: true, canceled: true}
	for _, o := range list {
		if deleted[o.ID] {
			t.Fatalf("C 端列表不应包含已删订单 %d", o.ID)
		}
	}

	// 后台路径（UserId=0，按社区过滤）不受影响：仍可查到已删单
	adminList, _, err := svc.ListOrders(ctx, &order.ListOrdersReq{
		CommunityId: 1,
		Pagination:  &common.Pagination{Page: 1, PageSize: 100},
	})
	if err != nil {
		t.Fatalf("查询后台列表失败: %v", err)
	}
	found := false
	for _, o := range adminList {
		if o.ID == done {
			found = true
		}
	}
	if !found {
		t.Fatal("后台路径应仍可查到已删订单")
	}

	// 详情保留可达（C 端售后进度/支付详情链路依赖）
	if _, _, _, _, err := svc.GetOrder(ctx, done, uid); err != nil {
		t.Fatalf("已删订单详情应保留可达: %v", err)
	}

	// 角标回归：终态订单本不在 pending 口径，软删前后一致
	counts, err := svc.GetUserOrderStatusCounts(ctx, &order.GetUserOrderStatusCountsReq{UserId: uid})
	if err != nil {
		t.Fatalf("查询角标失败: %v", err)
	}
	if counts.PendingPay != baseCounts.PendingPay || counts.PendingShip != baseCounts.PendingShip ||
		counts.PendingReceive != baseCounts.PendingReceive || counts.AfterSale != baseCounts.AfterSale {
		t.Fatalf("角标不应变化: got %+v, want %+v", counts, baseCounts)
	}
}
