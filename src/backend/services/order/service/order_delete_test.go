package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * C 端订单删除（软删除）集成测试（需本地 docker 栈）
 *
 * 运行方式（TEST_MYSQL_PASSWORD 未设置时自动 skip）：
 *   TEST_MYSQL_PASSWORD=xxx TEST_REDIS_PASSWORD=yyy go test ./services/order/service/ -run TestDeleteOrder -v
 *
 * 覆盖：
 *   - 守卫：归属校验 / 非终态拒绝 / 有进行中售后（1待审核 4退款中）拒绝 / 重复删除幂等成功；
 *   - 守卫的对外错误码（1.10）：未登录 InvalidArgument / 非本人 PermissionDenied /
 *     非终态与门店单 FailedPrecondition / 恢复未删单 NotFound；
 *   - 软删生效：user_deleted=1 落库后，C 端列表排除、后台路径不受影响、
 *     GetOrder 详情保留可达（售后进度/支付详情链路依赖）；
 *   - 后台按会员查单（include_user_deleted=true）仍含已删单，且透传标记供前端显示；
 *   - 角标回归：终态订单本不在 pending 口径，软删前后一致。
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

	// 重复删除幂等成功（1.4 口径：目标已达成不再回错误，前端统一提示删除成功），
	// 且不会把标记改回未删
	if err := svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: done, UserId: uid}); err != nil {
		t.Fatalf("重复删除应幂等成功, got %v", err)
	}
	var again model.Order
	if err := env.db.GetDb().First(&again, done).Error; err != nil || again.UserDeleted != 1 {
		t.Fatalf("重复删除后仍应保持已删标记, got user_deleted=%d err=%v", again.UserDeleted, err)
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

	// 后台路径（ListAdminOrders 传 include_user_deleted=true）仍可查到已删单：
	// 过滤靠显式标志区分调用方，不用 user_id 是否非零推断（1.2）
	adminList, _, err := svc.ListOrders(ctx, &order.ListOrdersReq{
		CommunityId: 1, IncludeUserDeleted: true,
		Pagination: &common.Pagination{Page: 1, PageSize: 100},
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

// assertDeleteErrCode 断言删除/恢复失败对外的 gRPC 码
// 网关按码映射 HTTP 状态与用户文案，只断言「有错」不足以拦住码位回归
// 参数：
//   - t: 测试对象
//   - name: 用例名
//   - err: 被测错误
//   - want: 期望的 gRPC 码
func assertDeleteErrCode(t *testing.T, name string, err error, want codes.Code) {
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

// TestDeleteOrderErrorCodes 删除守卫的对外错误码：未登录 / 非本人 / 非终态 / 门店单 / 恢复未删单
func TestDeleteOrderErrorCodes(t *testing.T) {
	env := newRefundTestEnv(t)
	svc := NewOrderService(env.db, nil)
	ctx := context.Background()
	const uid int64 = 99100001

	// 未登录（归属来自服务端用户上下文，缺失即 UserID=0）：不得放行到收银台散客单
	notLogin := seedSchedOrder(t, env.db, "code-a", 5, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	assertDeleteErrCode(t, "未登录删除", svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: notLogin, UserId: 0}), codes.InvalidArgument)

	// 非本人：PermissionDenied（用终态单，确保命中的是归属分支而非状态分支）
	other := seedSchedOrder(t, env.db, "code-b", 5, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	assertDeleteErrCode(t, "跨用户删除", svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: other, UserId: uid + 777}), codes.PermissionDenied)

	// 非终态：FailedPrecondition
	active := seedSchedOrder(t, env.db, "code-c", 2, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	assertDeleteErrCode(t, "进行中订单删除", svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: active, UserId: uid}), codes.FailedPrecondition)

	// 门店/收银台代客单（source=2）：C 端只读，与 CancelOrder/ApplyRefund/ConfirmReceive 同口径
	posID := seedPosSourceOrder(t, env.db, uid)
	assertDeleteErrCode(t, "门店单删除", svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: posID, UserId: uid}), codes.FailedPrecondition)

	// 后台恢复：未删除的订单统一 NotFound（不透露其他社区是否存在该单）
	assertDeleteErrCode(t, "恢复未删除的订单", svc.RestoreOrder(ctx, &order.RestoreOrderReq{Id: active, CommunityId: 1}), codes.NotFound)
}

// seedPosSourceOrder 直插一条归属指定用户的门店（source=2）已完成订单，返回订单 ID
// seedSchedOrder 固定写 source=1，门店单分支需单独构造
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - uid: 会员 ID
//
// 返回值：
//   - int64: 订单 ID
func seedPosSourceOrder(t *testing.T, db *mysql.Instance, uid int64) int64 {
	t.Helper()
	orderNo := fmt.Sprintf("%sPOS-%d", schedTestOrderNoPrefix, time.Now().UnixNano())
	res := db.GetDb().Exec(
		`INSERT INTO orders
			(order_no, community_id, user_id, status, total_amount, discount_amount, delivery_fee,
			 pay_amount, delivery_type, source, created_at, updated_at)
		 VALUES (?, 1, ?, 5, 10.00, 0, 0, 10.00, 3, 2, NOW(), NOW())`,
		orderNo, uid)
	if res.Error != nil {
		t.Fatalf("插入门店测试订单失败: %v", res.Error)
	}
	var id int64
	if err := db.GetDb().Raw("SELECT id FROM orders WHERE order_no = ?", orderNo).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("回查门店测试订单失败: %v", err)
	}
	return id
}

// TestListOrdersIncludeUserDeleted 后台按会员查单不得连带隐藏其软删订单（1.2 口径）
// 后台「会员订单」弹窗与「常买」在同一面板，若按 user_id 查单顺带过滤 user_deleted，
// 客服会看到「这单不存在」而用户侧角标/售后仍挂着，两侧排查口径互相矛盾
func TestListOrdersIncludeUserDeleted(t *testing.T) {
	env := newRefundTestEnv(t)
	svc := NewOrderService(env.db, nil)
	ctx := context.Background()
	const uid int64 = 99100001

	deleted := seedSchedOrder(t, env.db, "incl-a", 5, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	if err := svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: deleted, UserId: uid}); err != nil {
		t.Fatalf("软删测试订单失败: %v", err)
	}

	contains := func(list []*model.Order, id int64) (bool, int32) {
		for _, o := range list {
			if o.ID == id {
				return true, o.UserDeleted
			}
		}
		return false, -1
	}

	// C 端口径（不传 include）：排除已删单
	cList, _, err := svc.ListOrders(ctx, &order.ListOrdersReq{
		UserId: uid, Pagination: &common.Pagination{Page: 1, PageSize: 100},
	})
	if err != nil {
		t.Fatalf("查询 C 端列表失败: %v", err)
	}
	if got, _ := contains(cList, deleted); got {
		t.Fatal("C 端列表不应包含已删订单")
	}

	// 后台按会员查（include_user_deleted=true）：仍可见，且标记透传给前端
	aList, _, err := svc.ListOrders(ctx, &order.ListOrdersReq{
		UserId: uid, IncludeUserDeleted: true, Pagination: &common.Pagination{Page: 1, PageSize: 100},
	})
	if err != nil {
		t.Fatalf("查询后台会员列表失败: %v", err)
	}
	got, flag := contains(aList, deleted)
	if !got {
		t.Fatal("后台按会员查单应包含已删订单")
	}
	if flag != 1 {
		t.Fatalf("列表应透传 user_deleted=1，供前端显示「用户已删」标记, got %d", flag)
	}
}
