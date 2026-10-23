package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 退款收尾/拒绝的订单条件更新（P1-6）集成测试
 *
 * 背景：completeRefundSuccess 与 RejectRefund 里订单行是事务内无锁 First 读取 +
 * 整行 Save 收尾，并发场景（同一订单另一笔退款先完成、后台改状态）会把对方写入的
 * 状态整体盖掉——已退款(8)的订单被回写成退款前状态、或反之。修法改为
 * `WHERE id AND status=7` 条件 Updates，未命中即返回 FailedPrecondition 中止事务。
 *
 * 运行方式（需本地 docker 栈，env 守卫跳过）：
 *   TEST_MYSQL_PASSWORD=xxx TEST_REDIS_PASSWORD=yyy go test ./services/order/service/ -run TestRefund -v
 *
 * 覆盖：
 *   - 订单不在退款中(7)：completeRefundSuccess/RejectRefund 均拒绝且事务整体回滚，
 *     不出现「退款单已终结但订单状态被覆盖」的半完成态，也不留假状态日志；
 *   - 正常路径不回归：整单退完订单 7→8（退款单升级整单退、销量回退），
 *     拒绝回到来源状态（退款单置已拒绝、审批日志与状态日志落账）。
 */

// refundTestEnv 退款状态机测试环境（复用调度器测试环境：真实 DB + 桩化全局客户端）
type refundTestEnv struct {
	svc     *RefundService
	db      *mysql.Instance
	product *stubProductClient
	// sched 底层调度器测试环境句柄：卡单重试用例需要 Redis 锁与调度器实例本身
	sched *schedTestEnv
}

// newRefundTestEnv 构建退款测试环境
// 调度器测试环境已按 SCHEDTEST- 前缀清理 orders/items/logs，退款三表在此补齐
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *refundTestEnv: 退款服务与测试库句柄；TEST_MYSQL_PASSWORD 未设置时 skip
func newRefundTestEnv(t *testing.T) *refundTestEnv {
	t.Helper()
	sched := newSchedulerTestEnv(t)
	env := &refundTestEnv{
		svc:     NewRefundService(sched.db, sched.payment),
		db:      sched.db,
		product: sched.product,
		sched:   sched,
	}
	// LIFO：本清理先于调度器环境的订单清理执行，此时 orders 行还在，可按前缀定位退款数据
	t.Cleanup(func() {
		ctx := context.Background()
		like := schedTestOrderNoPrefix + "%"
		_ = env.db.GetDb().WithContext(ctx).Exec(
			"DELETE FROM refund_logs WHERE refund_id IN (SELECT id FROM refunds WHERE order_id IN (SELECT id FROM orders WHERE order_no LIKE ?))",
			like).Error
		_ = env.db.GetDb().WithContext(ctx).Exec(
			"DELETE FROM refund_items WHERE refund_id IN (SELECT id FROM refunds WHERE order_id IN (SELECT id FROM orders WHERE order_no LIKE ?))",
			like).Error
		_ = env.db.GetDb().WithContext(ctx).Exec(
			"DELETE FROM refunds WHERE order_id IN (SELECT id FROM orders WHERE order_no LIKE ?)",
			like).Error
	})
	return env
}

// seedRefund 直插一条退款单（GORM Create；时间戳显式带值，防零值 datetime）
//
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - orderID: 归属订单 ID
//   - status: 退款单状态（1 待审核/4 退款中）
//   - amount: 退款金额
//
// 返回值：
//   - *model.Refund: 插入后的退款单（含自增 ID）
func seedRefund(t *testing.T, db *mysql.Instance, orderID int64, status int32, amount float64) *model.Refund {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	r := &model.Refund{
		RefundNo:      generateRefundNo(),
		OrderID:       orderID,
		UserID:        99100001,
		Amount:        amount,
		FreightAmount: 0,
		Type:          1,
		Reason:        "P1-6 测试退款",
		Status:        status,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.GetDb().WithContext(context.Background()).Create(r).Error; err != nil {
		t.Fatalf("插入测试退款单失败: %v", err)
	}
	return r
}

// seedOrderStatusLog 直插一条订单状态日志（供 lastStatusBeforeRefundTx 定位来源状态）
func seedOrderStatusLog(t *testing.T, db *mysql.Instance, orderID int64, from, to int32) {
	t.Helper()
	if err := db.GetDb().WithContext(context.Background()).Create(&model.OrderStatusLog{
		OrderID:      orderID,
		FromStatus:   from,
		ToStatus:     to,
		OperatorType: "user",
		OperatorID:   99100001,
		Remark:       "P1-6 测试",
		CreatedAt:    time.Now().Format("2006-01-02 15:04:05"),
	}).Error; err != nil {
		t.Fatalf("插入状态日志失败: %v", err)
	}
}

// refundRowStatus 回查退款单状态/退款时间/拒绝原因/类型
func refundRowStatus(t *testing.T, db *mysql.Instance, refundID int64) (status int32, refundTime, rejectReason string, refundType int32) {
	t.Helper()
	if err := db.GetDb().WithContext(context.Background()).Raw(
		"SELECT status, IFNULL(refund_time,''), IFNULL(reject_reason,''), type FROM refunds WHERE id = ?",
		refundID).Row().Scan(&status, &refundTime, &rejectReason, &refundType); err != nil {
		t.Fatalf("回查退款单失败: %v", err)
	}
	return
}

// countOrderStatusLogs 统计订单状态日志条数
func countOrderStatusLogs(t *testing.T, db *mysql.Instance, orderID int64) int64 {
	t.Helper()
	var n int64
	if err := db.GetDb().WithContext(context.Background()).Model(&model.OrderStatusLog{}).
		Where("order_id = ?", orderID).Count(&n).Error; err != nil {
		t.Fatalf("统计状态日志失败: %v", err)
	}
	return n
}

// countRefundLogs 统计退款审批日志条数
func countRefundLogs(t *testing.T, db *mysql.Instance, refundID int64) int64 {
	t.Helper()
	var n int64
	if err := db.GetDb().WithContext(context.Background()).Model(&model.RefundLog{}).
		Where("refund_id = ?", refundID).Count(&n).Error; err != nil {
		t.Fatalf("统计退款审批日志失败: %v", err)
	}
	return n
}

// assertBizCode 断言业务错误码（bizerr 自定义错误，非 gRPC status，须 errors.As 取出）
func assertBizCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	var be *bizerr.Error
	if !errors.As(err, &be) {
		t.Fatalf("错误应为 *bizerr.Error, got %T: %v", err, err)
	}
	if be.Code() != want {
		t.Fatalf("错误码 = %v, 期望 %v", be.Code(), want)
	}
}

// TestCompleteRefundSuccessRejectsOrderNotInRefunding 订单已被并发流转时拒绝收尾并整体回滚
// 修复前：无锁读 + 整行 Save 会把订单的当前状态（如 8 已退款）覆盖成 8/退款前状态，
// 且照样写下一条 from=7 的状态日志，状态轨迹被污染
func TestCompleteRefundSuccessRejectsOrderNotInRefunding(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()

	// 订单不在退款中(7)：模拟另一笔退款先完成/后台改状态，已把订单推往其他状态
	orderID := seedSchedOrder(t, env.db, "refund-conflict", 2, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	seedSchedItem(t, env.db, orderID, 44010, 1)
	refund := seedRefund(t, env.db, orderID, 4, 10.00)
	logsBefore := countOrderStatusLogs(t, env.db, orderID)

	items := []*model.RefundItem{{SkuID: 44010, Quantity: 1}}
	err := env.svc.completeRefundSuccess(ctx, refund, items, "admin", 1)
	assertBizCode(t, err, codes.FailedPrecondition)

	// 订单状态未被改写
	if st, _, _ := orderSchedStatus(t, env.db, orderID); st != 2 {
		t.Fatalf("订单状态应保持 2, got %d", st)
	}
	// 退款单保持退款中(4)，交由重试/调度兜底（事务回滚了 4→5 的推进）
	if st, _, _, _ := refundRowStatus(t, env.db, refund.ID); st != 4 {
		t.Fatalf("退款单应保持退款中(4), got %d", st)
	}
	// 不新增任何状态日志（不存在 from=7 的假流转）
	if after := countOrderStatusLogs(t, env.db, orderID); after != logsBefore {
		t.Fatalf("不应新增状态日志: before=%d after=%d", logsBefore, after)
	}
	// 销量回退只在事务提交后执行，失败路径不得触发
	if n := len(env.product.salesAdjustCalls()); n != 0 {
		t.Fatalf("失败路径不应回退销量, AdjustSalesCount 调用 %d 次", n)
	}
}

// TestCompleteRefundSuccessNormalPath 正常收尾不回归：整单退完订单 7→8
func TestCompleteRefundSuccessNormalPath(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()

	// 前置态：订单退款中(7)，来源 2→7，退款单退款中(4) 且金额等于实付（整单退）
	orderID := seedSchedOrder(t, env.db, "refund-ok", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	seedSchedItem(t, env.db, orderID, 44011, 1)
	seedOrderStatusLog(t, env.db, orderID, 2, 7)
	refund := seedRefund(t, env.db, orderID, 4, 10.00)
	items := []*model.RefundItem{{SkuID: 44011, Quantity: 1}}

	if err := env.svc.completeRefundSuccess(ctx, refund, items, "admin", 1); err != nil {
		t.Fatalf("正常收尾应成功: %v", err)
	}

	// 订单整单退完 → 8 已退款
	if st, _, _ := orderSchedStatus(t, env.db, orderID); st != 8 {
		t.Fatalf("订单应流转为已退款(8), got %d", st)
	}
	// 退款单 → 5 已退款，退款成功时间落库
	st, refundTime, _, _ := refundRowStatus(t, env.db, refund.ID)
	if st != 5 {
		t.Fatalf("退款单应流转为已退款(5), got %d", st)
	}
	if refundTime == "" {
		t.Fatal("退款成功时间应落库")
	}
	// 状态日志 7→8，备注为整单退完成
	var from int32
	var remark string
	if err := env.db.GetDb().WithContext(ctx).Raw(
		"SELECT from_status, remark FROM order_status_logs WHERE order_id = ? AND to_status = 8 ORDER BY id DESC LIMIT 1",
		orderID).Row().Scan(&from, &remark); err != nil {
		t.Fatalf("查询状态日志失败: %v", err)
	}
	if from != 7 || remark != "退款完成" {
		t.Fatalf("状态日志不符: from=%d remark=%q", from, remark)
	}
	// 销量按明细回退
	found := false
	for _, a := range env.product.salesAdjustCalls() {
		for _, it := range a.GetItems() {
			if it.GetSkuId() == 44011 && it.GetDelta() == -1 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("应按退款明细回退销量（净成交口径）")
	}
}

// TestRejectRefundRejectsOrderNotInRefunding 订单已被并发流转时拒绝退款并整体回滚
// 修复前：退款单的 1→3 已提交视野内、订单被整行 Save 覆盖，留下半完成态
func TestRejectRefundRejectsOrderNotInRefunding(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()

	// 订单已离开退款中(7)（另一笔退款先完成推往 8）
	orderID := seedSchedOrder(t, env.db, "reject-conflict", 8, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	refund := seedRefund(t, env.db, orderID, 1, 10.00)
	logsBefore := countOrderStatusLogs(t, env.db, orderID)

	err := env.svc.RejectRefund(ctx, &order.RejectRefundReq{
		Id:           refund.ID,
		OperatorId:   1,
		RejectReason: "测试拒绝",
		CommunityId:  1,
	})
	assertBizCode(t, err, codes.FailedPrecondition)

	// 事务整体回滚：退款单仍待审核(1)
	if st, _, _, _ := refundRowStatus(t, env.db, refund.ID); st != 1 {
		t.Fatalf("退款单应回滚保持待审核(1), got %d", st)
	}
	// 订单状态保持 8，不被回写成退款前状态
	if st, _, _ := orderSchedStatus(t, env.db, orderID); st != 8 {
		t.Fatalf("订单状态应保持 8, got %d", st)
	}
	// 未留下拒绝审批日志与状态日志
	if n := countRefundLogs(t, env.db, refund.ID); n != 0 {
		t.Fatalf("不应留下拒绝审批日志, got %d 条", n)
	}
	if after := countOrderStatusLogs(t, env.db, orderID); after != logsBefore {
		t.Fatalf("不应新增状态日志: before=%d after=%d", logsBefore, after)
	}
}

// TestRejectRefundNormalPath 正常拒绝不回归：订单回到来源状态，审计日志齐全
func TestRejectRefundNormalPath(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()

	// 来源状态 3（备货中）：拒绝后应回到 3 而不是兜底成 2
	orderID := seedSchedOrder(t, env.db, "reject-ok", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	seedOrderStatusLog(t, env.db, orderID, 3, 7)
	refund := seedRefund(t, env.db, orderID, 1, 10.00)

	if err := env.svc.RejectRefund(ctx, &order.RejectRefundReq{
		Id:           refund.ID,
		OperatorId:   1,
		RejectReason: "商品未拆封，拒绝退款",
		CommunityId:  1,
	}); err != nil {
		t.Fatalf("正常拒绝应成功: %v", err)
	}

	// 订单回到来源状态 3
	if st, _, _ := orderSchedStatus(t, env.db, orderID); st != 3 {
		t.Fatalf("订单应回到备货中(3), got %d", st)
	}
	// 退款单 → 3 已拒绝且记录原因
	st, _, reason, _ := refundRowStatus(t, env.db, refund.ID)
	if st != 3 || reason != "商品未拆封，拒绝退款" {
		t.Fatalf("退款单应已拒绝且带原因: status=%d reason=%q", st, reason)
	}
	// 审批日志 1 条，状态日志 7→3
	if n := countRefundLogs(t, env.db, refund.ID); n != 1 {
		t.Fatalf("应留 1 条拒绝审批日志, got %d", n)
	}
	var from int32
	if err := env.db.GetDb().WithContext(ctx).Raw(
		"SELECT from_status FROM order_status_logs WHERE order_id = ? AND to_status = 3 ORDER BY id DESC LIMIT 1",
		orderID).Row().Scan(&from); err != nil {
		t.Fatalf("查询状态日志失败: %v", err)
	}
	if from != 7 {
		t.Fatalf("状态日志 from = %d, 期望 7", from)
	}
}
