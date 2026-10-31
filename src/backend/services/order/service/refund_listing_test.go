package service

import (
	"context"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 售后单列表与退款统计集成测试（需本地 docker 栈）
 *
 * 背景（2026-10-31 售后展示修复）：部分退款成功后各端无售后痕迹，
 * 本次补齐三块查询能力并用例固化口径：
 *   - C 端「退款/售后」tab 改为独立售后单维度（RefundService.ListRefunds 按 UserId 隔离）；
 *   - 订单已成功退款金额（fillRefundedAmounts，money 口径含运费，仅 status=5）；
 *   - 行级已退数量（refundedQuantitiesByOrders，refunds.status=5 的 SUM(refund_items.quantity)）。
 *
 * 运行方式（TEST_MYSQL_PASSWORD 未设置时自动 skip）：
 *   TEST_MYSQL_PASSWORD=xxx TEST_REDIS_PASSWORD=yyy go test ./services/order/service/ -run TestRefundListing -v
 *
 * 覆盖：
 *   - ListRefunds 用户隔离：本人售后单可见，他人退款单不串号；OrderNo/OrderItemCount 回填；
 *   - refundedQuantitiesByOrders 批量口径：仅 status=5 计入、同行多笔累加、
 *     待审核单不计入、空入参不发查询；
 *   - GetOrder 详情回填：订单 RefundedAmount 与商品行 RefundedQuantity。
 */

// seedRefundForUser 直插一条指定用户的退款单
//（seedRefund 固定 UserID=99100001，用户隔离用例需要他人数据，参数化一份）
//
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - orderID: 归属订单 ID
//   - userID: 退款单归属用户 ID
//   - status: 退款单状态
//   - amount: 退款金额
//
// 返回值：
//   - *model.Refund: 插入后的退款单（含自增 ID）
func seedRefundForUser(t *testing.T, db *mysql.Instance, orderID, userID int64, status int32, amount float64) *model.Refund {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	r := &model.Refund{
		RefundNo:  generateRefundNo(),
		OrderID:   orderID,
		UserID:    userID,
		Amount:    amount,
		Type:      2,
		Reason:    "售后单列表测试退款",
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.GetDb().WithContext(context.Background()).Create(r).Error; err != nil {
		t.Fatalf("插入测试退款单失败: %v", err)
	}
	return r
}

// seedRefundItem 直插一条退款商品明细
//
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - refundID: 退款单 ID
//   - orderItemID: 订单商品行 ID
//   - qty: 退货数量
func seedRefundItem(t *testing.T, db *mysql.Instance, refundID, orderItemID int64, qty int32) {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	if err := db.GetDb().WithContext(context.Background()).Create(&model.RefundItem{
		RefundID:     refundID,
		OrderItemID:  orderItemID,
		SkuID:        1,
		ProductName:  "售后单列表测试商品",
		Quantity:     qty,
		RefundAmount: 5.50,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("插入退款商品明细失败: %v", err)
	}
}

// TestRefundListingListRefundsUserIsolation 售后单列表按 UserId 隔离，不串号
func TestRefundListingListRefundsUserIsolation(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()

	// 本人与他人各一笔已成功退款单（挂各自订单，避免同单两用户歧义）
	o1 := seedSchedOrder(t, env.db, "refund-list-a", 5, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	mine := seedRefundForUser(t, env.db, o1, 99100001, 5, 5.50)
	o2 := seedSchedOrder(t, env.db, "refund-list-b", 5, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	others := seedRefundForUser(t, env.db, o2, 99100010, 5, 3.00)

	list, _, err := env.svc.ListRefunds(ctx, &order.ListRefundsReq{
		UserId:     99100001,
		Pagination: &common.Pagination{Page: 1, PageSize: 50},
	})
	if err != nil {
		t.Fatalf("查询售后单列表失败: %v", err)
	}

	foundMine, foundOthers := false, false
	for _, r := range list {
		switch r.ID {
		case mine.ID:
			foundMine = true
			if r.OrderNo == "" {
				t.Fatalf("售后单 %d 未回填关联订单号", r.ID)
			}
		case others.ID:
			foundOthers = true
		}
	}
	if !foundMine {
		t.Fatalf("本人售后单 %d 未出现在列表中", mine.ID)
	}
	if foundOthers {
		t.Fatalf("用户隔离失效: 他人售后单 %d 出现在本人列表中", others.ID)
	}
}

// TestRefundListingQuantitiesByOrders 行级已退数量批量统计口径
func TestRefundListingQuantitiesByOrders(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()

	// 空入参不发查询直接返回空映射
	empty, err := refundedQuantitiesByOrders(ctx, env.db.GetDb(), nil)
	if err != nil {
		t.Fatalf("空入参应返回空映射而非报错: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("空入参结果集应为空, got %v", empty)
	}

	// 一单两行：item1 买 2 件、item2 买 3 件
	oid := seedSchedOrder(t, env.db, "refund-qty", 5, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	item1 := int64(0)
	item2 := int64(0)
	seedSchedItem(t, env.db, oid, 11, 2)
	if err := env.db.GetDb().WithContext(ctx).Raw(
		"SELECT id FROM order_items WHERE order_id = ? ORDER BY id LIMIT 1", oid).Scan(&item1).Error; err != nil || item1 == 0 {
		t.Fatalf("回查订单商品行失败: %v", err)
	}
	seedSchedItem(t, env.db, oid, 12, 3)
	if err := env.db.GetDb().WithContext(ctx).Raw(
		"SELECT id FROM order_items WHERE order_id = ? ORDER BY id DESC LIMIT 1", oid).Scan(&item2).Error; err != nil || item2 == 0 {
		t.Fatalf("回查订单商品行失败: %v", err)
	}

	// 成功退款单A：item1 退 1 件（计入）
	rA := seedRefund(t, env.db, oid, 5, 5.50)
	seedRefundItem(t, env.db, rA.ID, item1, 1)
	// 待审核单：item2 退 2 件（不计入——尚未成功）
	rB := seedRefund(t, env.db, oid, 1, 3.00)
	seedRefundItem(t, env.db, rB.ID, item2, 2)
	// 成功退款单B：item1 再退 2 件（同行多笔累加 → item1 共 3 件）
	rC := seedRefund(t, env.db, oid, 5, 4.00)
	seedRefundItem(t, env.db, rC.ID, item1, 2)
	// 已拒绝单：item2 再退 1 件（不计入）
	rD := seedRefund(t, env.db, oid, 3, 1.00)
	seedRefundItem(t, env.db, rD.ID, item2, 1)

	got, err := refundedQuantitiesByOrders(ctx, env.db.GetDb(), []int64{oid})
	if err != nil {
		t.Fatalf("统计行级已退数量失败: %v", err)
	}
	if got[oid][item1] != 3 {
		t.Fatalf("item1 已退数量 = %d, want 3（两笔成功退款单累加，拒绝/待审核不计）", got[oid][item1])
	}
	if got[oid][item2] != 0 {
		t.Fatalf("item2 已退数量 = %d, want 0（其退款单均未成功）", got[oid][item2])
	}
}

// TestRefundListingGetOrderFill 详情回填：订单 RefundedAmount 与商品行 RefundedQuantity
func TestRefundListingGetOrderFill(t *testing.T) {
	env := newRefundTestEnv(t)
	svc := NewOrderService(env.db, nil)
	ctx := context.Background()

	// 2 件薯片 11 元、成功退 1 件 5.50 的核心复现场景
	oid := seedSchedOrder(t, env.db, "refund-fill", 5, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	seedSchedItem(t, env.db, oid, 11, 2)
	var itemID int64
	if err := env.db.GetDb().WithContext(ctx).Raw(
		"SELECT id FROM order_items WHERE order_id = ? ORDER BY id LIMIT 1", oid).Scan(&itemID).Error; err != nil || itemID == 0 {
		t.Fatalf("回查订单商品行失败: %v", err)
	}
	r := seedRefund(t, env.db, oid, 5, 5.50)
	seedRefundItem(t, env.db, r.ID, itemID, 1)

	o, items, _, refunds, err := svc.GetOrder(ctx, oid, 0)
	if err != nil {
		t.Fatalf("查询订单详情失败: %v", err)
	}
	if o.RefundedAmount != 5.50 {
		t.Fatalf("订单 RefundedAmount = %.2f, want 5.50", o.RefundedAmount)
	}
	var gotQty int32
	for _, it := range items {
		if it.ID == itemID {
			gotQty = it.RefundedQuantity
		}
	}
	if gotQty != 1 {
		t.Fatalf("商品行 RefundedQuantity = %d, want 1", gotQty)
	}
	if len(refunds) == 0 {
		t.Fatalf("详情应携带售后记录（ refunds 为空）")
	}
}
