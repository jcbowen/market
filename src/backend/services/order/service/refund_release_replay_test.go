package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 退款收尾回补的调度器重放测试（审查遗留项：退款最终失败无重放）
 *
 * 背景：completeRefundSuccess 把退款单置已退款(5) 后向商品服务回补库存/回补优惠券，
 * 三次重试仍失败时原先只打 [CRITICAL] 日志，钱已退、库存不回补，且重放队列里没有这条记录
 * （取消链路有 cancel_release，退款链路缺失）。修法：失败即入队 task_type=refund_release
 * 且带 biz_no=退款单号，由调度器逐轮重放到收敛。
 *
 * 运行方式（需本地 docker 栈，env 守卫跳过）：
 *   TEST_MYSQL_PASSWORD=xxx TEST_REDIS_PASSWORD=yyy go test ./services/order/service/ -run TestRefundReleaseReplay -v
 *
 * 覆盖：
 *   - 按退款单号重建回补清单并成功：记录标记已处理、BatchReleaseStock 带同一 biz_no
 *   - 订单已置已退款(8) 且有用券：一并回补优惠券（判据是订单状态，不是退款单 type）
 *   - 缺 biz_no：标记放弃(3) 并留 [CRITICAL]，不做整单重放（会吞掉同订单其他退款的槽位）
 *   - 退款单已转出成功态(5)：标记已处理，不永久滞留队列
 *   - product 侧持续失败：推进 retry_count、状态保持待重试；恢复后下一轮自动收敛
 *   - 退款单查不到：按失败推进 retry_count，记录不丢
 *   - 达 releaseMaxRetry：本轮不再取该记录（转人工）
 *   - 退款收尾入口侧：回补三次仍失败即入队一条 refund_release，恢复后一轮重放收敛
 *   - recordSchedulerFailure：biz_no 落库、error_msg 按字符截断不切中文
 *   - truncateRunes 纯函数口径
 */

// releaseRefundNoPrefix 退款收尾重放测试的退款单号前缀（清理与断言依据）
const releaseRefundNoPrefix = "SCHEDTEST-RFREL-"

// seedRefundWithNo 直插一条指定单号与状态的退款单（单号带测试前缀，便于按 biz_no 清理与断言）
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - orderID: 归属订单 ID
//   - no: 退款单号（须带 releaseRefundNoPrefix）
//   - status: 退款单状态（5 已退款 / 3 已拒绝等）
//
// 返回值：
//   - *model.Refund: 插入后的退款单（含自增 ID）
//
// 异常：插入失败直接 t.Fatalf
func seedRefundWithNo(t *testing.T, db *mysql.Instance, orderID int64, no string, status int32) *model.Refund {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	r := &model.Refund{
		RefundNo:  no,
		OrderID:   orderID,
		UserID:    99100001,
		Amount:    10.00,
		Type:      2,
		Reason:    "退款收尾重放测试",
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.GetDb().WithContext(context.Background()).Create(r).Error; err != nil {
		t.Fatalf("插入测试退款单失败: %v", err)
	}
	return r
}

// seedRefundItemWithSku 直插一条退款商品明细（回补清单按此重建）
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - refundID: 退款单 ID
//   - skuID: SKU ID
//   - qty: 退货数量
//
// 异常：插入失败直接 t.Fatalf
func seedRefundItemWithSku(t *testing.T, db *mysql.Instance, refundID, skuID int64, qty int32) {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	if err := db.GetDb().WithContext(context.Background()).Create(&model.RefundItem{
		RefundID:     refundID,
		OrderItemID:  1,
		SkuID:        skuID,
		ProductName:  "退款收尾重放测试商品",
		Quantity:     qty,
		RefundAmount: 10.00,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("插入退款商品明细失败: %v", err)
	}
}

// seedReleaseFailure 直插一条退款收尾回补待重试记录
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - orderID: 订单 ID
//   - bizNo: 幂等业务号（退款单号；传空串构造缺号场景）
//   - retryCount: 已重试次数（用于验证达上限后不再取）
//
// 返回值：
//   - int64: 记录 ID
//
// 异常：插入失败直接 t.Fatalf
func seedReleaseFailure(t *testing.T, db *mysql.Instance, orderID int64, bizNo string, retryCount int32) int64 {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	f := &model.SchedulerFailure{
		OrderID:    orderID,
		TaskType:   taskTypeRefundRelease,
		BizNo:      bizNo,
		Status:     1,
		ErrorMsg:   "回补库存失败: rpc error: code = Unavailable desc = 桩：批量回补库存失败",
		RetryCount: retryCount,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := db.GetDb().WithContext(context.Background()).Create(f).Error; err != nil {
		t.Fatalf("插入回补失败记录失败: %v", err)
	}
	return f.ID
}

// loadReleaseFailure 回查失败记录当前状态
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - id: 记录 ID
//
// 返回值：
//   - int32: status（1 待重试 / 2 已处理 / 3 放弃）
//   - int32: retry_count
//   - string: error_msg
//
// 异常：查询失败直接 t.Fatalf
func loadReleaseFailure(t *testing.T, db *mysql.Instance, id int64) (int32, int32, string) {
	t.Helper()
	var status, retry int32
	var errMsg string
	if err := db.GetDb().WithContext(context.Background()).Raw(
		"SELECT status, retry_count, IFNULL(error_msg,'') FROM order_scheduler_failures WHERE id = ?", id).
		Row().Scan(&status, &retry, &errMsg); err != nil {
		t.Fatalf("回查回补失败记录失败: %v", err)
	}
	return status, retry, errMsg
}

// tailRunes 取字符串末尾 n 个字符（仅用于失败信息，避免按字节切片把中文切坏）
// 参数：
//   - value: 原始字符串
//   - n: 需要的字符数
//
// 返回值：
//   - string: 尾部字符（不足 n 时返回全串）
func tailRunes(value string, n int) string {
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return string(runes[len(runes)-n:])
}

// batchReleaseCallsFor 取指定幂等号的批量回补调用（含失败尝试，用于计数重试节奏）
// 参数：
//   - stub: 商品服务桩
//   - bizNo: 幂等号
//
// 返回值：
//   - []*product.BatchReleaseStockReq: 命中的调用快照
func batchReleaseCallsFor(stub *stubProductClient, bizNo string) []*product.BatchReleaseStockReq {
	var out []*product.BatchReleaseStockReq
	for _, c := range stub.batchReleaseCalls() {
		if c.GetBizNo() == bizNo {
			out = append(out, c)
		}
	}
	return out
}

// runRefundReleaseOnce 按调度器口径跑一轮退款收尾重放（先清锁键，与运行中的 docker 调度器共用 Redis）
// 参数：
//   - t: 测试对象
//   - env: 调度器测试环境
//   - ctx: 上下文
//
// 返回值：
//   - orderStat: 本轮统计
func runRefundReleaseOnce(t *testing.T, env *schedTestEnv, ctx context.Context) orderStat {
	t.Helper()
	if err := env.redis.Del(ctx, "scheduler:order:refund:release:lock").Err(); err != nil {
		t.Fatalf("清理退款收尾重放锁键失败: %v", err)
	}
	return env.sched.retryRefundReleases(ctx)
}

// TestRefundReleaseReplayHappyPaths 退款收尾重放的成功分支
// 覆盖：按退款单号重建清单并回补库存、全额退完时一并回补优惠券、
// 缺 biz_no 与退款单转出成功态时分别落放弃/已处理
func TestRefundReleaseReplayHappyPaths(t *testing.T) {
	env := newSchedulerTestEnv(t)
	ctx := context.Background()
	db := env.db
	t.Cleanup(func() { cleanupRefundReleaseTestData(t, db) })

	t.Run("按退款单号重放库存回补", func(t *testing.T) {
		orderID := seedSchedOrder(t, db, "rr-stock", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Minute))
		refundNo := releaseRefundNoPrefix + "STOCK"
		r := seedRefundWithNo(t, db, orderID, refundNo, 5)
		seedRefundItemWithSku(t, db, r.ID, 44101, 2)
		fID := seedReleaseFailure(t, db, orderID, refundNo, 0)

		stat := runRefundReleaseOnce(t, env, ctx)
		if stat.refundReleased != 1 || stat.failed != 0 {
			t.Fatalf("重放统计 = %+v, 期望 refundReleased=1 failed=0", stat)
		}
		if status, retry, _ := loadReleaseFailure(t, db, fID); status != 2 || retry != 0 {
			t.Fatalf("重放成功后记录应为已处理(2) 且不动 retry_count，实际 status=%d retry=%d", status, retry)
		}
		calls := batchReleaseCallsFor(env.product, refundNo)
		if len(calls) != 1 {
			t.Fatalf("成功重放应恰一次批量回补，实际 %d 次", len(calls))
		}
		if calls[0].GetOrderId() != orderID || len(calls[0].Items) != 1 ||
			calls[0].Items[0].GetSkuId() != 44101 || calls[0].Items[0].GetQuantity() != 2 {
			t.Fatalf("回补入参不符（幂等槽位须为退款单号）: %+v", calls[0])
		}
		// 部分退款（订单未置 8）不回补优惠券：优惠券只在整单退完时释放
		if got := env.product.couponCalls(); len(got) != 0 {
			t.Fatalf("部分退款收尾不应回补优惠券，实际 %+v", got)
		}
	})

	t.Run("全额退完一并回补优惠券", func(t *testing.T) {
		orderID := seedSchedOrder(t, db, "rr-coupon", 8, schedTimeAgo(time.Hour), schedTimeAgo(time.Minute))
		if err := db.GetDb().WithContext(ctx).Exec(
			"UPDATE orders SET coupon_id = 99100077 WHERE id = ?", orderID).Error; err != nil {
			t.Fatalf("预置用券订单失败: %v", err)
		}
		refundNo := releaseRefundNoPrefix + "COUPON"
		r := seedRefundWithNo(t, db, orderID, refundNo, 5)
		seedRefundItemWithSku(t, db, r.ID, 44102, 1)
		fID := seedReleaseFailure(t, db, orderID, refundNo, 1)

		stat := runRefundReleaseOnce(t, env, ctx)
		if stat.refundReleased != 1 {
			t.Fatalf("重放统计 = %+v, 期望 refundReleased=1", stat)
		}
		if status, _, _ := loadReleaseFailure(t, db, fID); status != 2 {
			t.Fatalf("重放成功后记录应为已处理(2)，实际 %d", status)
		}
		coupons := env.product.couponCalls()
		if len(coupons) != 1 || coupons[0].GetOrderId() != orderID {
			t.Fatalf("整单退完应回补一次优惠券（订单 %d），实际 %+v", orderID, coupons)
		}
		if len(batchReleaseCallsFor(env.product, refundNo)) != 1 {
			t.Fatalf("库存回补应恰一次，实际 %d 次", len(batchReleaseCallsFor(env.product, refundNo)))
		}
	})

	t.Run("缺幂等号标记放弃不整单重放", func(t *testing.T) {
		orderID := seedSchedOrder(t, db, "rr-nobiz", 8, schedTimeAgo(time.Hour), schedTimeAgo(time.Minute))
		fID := seedReleaseFailure(t, db, orderID, "", 0)

		before := len(env.product.releaseCalls())
		stat := runRefundReleaseOnce(t, env, ctx)
		if status, retry, _ := loadReleaseFailure(t, db, fID); status != 3 || retry != 0 {
			t.Fatalf("缺 biz_no 的记录应放弃(3) 且不推进 retry_count，实际 status=%d retry=%d", status, retry)
		}
		if stat.refundReleased != 0 || stat.failed != 1 {
			t.Fatalf("缺号场景统计 = %+v, 期望 refundReleased=0 failed=1", stat)
		}
		if len(env.product.releaseCalls()) != before {
			t.Fatal("缺号记录不得触发整单回补（同订单其他退款的槽位会被吞）")
		}
	})

	t.Run("退款单转出成功态标记已处理", func(t *testing.T) {
		orderID := seedSchedOrder(t, db, "rr-not5", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Minute))
		refundNo := releaseRefundNoPrefix + "REJECTED"
		seedRefundWithNo(t, db, orderID, refundNo, 3) // 已拒绝：没有回补语义
		fID := seedReleaseFailure(t, db, orderID, refundNo, 0)

		stat := runRefundReleaseOnce(t, env, ctx)
		if status, retry, _ := loadReleaseFailure(t, db, fID); status != 2 || retry != 0 {
			t.Fatalf("无回补语义的记录应标记已处理(2)，实际 status=%d retry=%d", status, retry)
		}
		if stat.refundReleased != 0 || stat.failed != 0 {
			t.Fatalf("无回补语义不应计成功或失败，实际 %+v", stat)
		}
		if len(batchReleaseCallsFor(env.product, refundNo)) != 0 {
			t.Fatal("被拒退款单不得回补库存")
		}
	})

	t.Run("达重试上限不再取", func(t *testing.T) {
		orderID := seedSchedOrder(t, db, "rr-giveup", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Minute))
		refundNo := releaseRefundNoPrefix + "MAXRETRY"
		r := seedRefundWithNo(t, db, orderID, refundNo, 5)
		seedRefundItemWithSku(t, db, r.ID, 44103, 1)
		fID := seedReleaseFailure(t, db, orderID, refundNo, int32(releaseMaxRetry))

		stat := runRefundReleaseOnce(t, env, ctx)
		if status, retry, _ := loadReleaseFailure(t, db, fID); status != 1 || retry != int32(releaseMaxRetry) {
			t.Fatalf("达上限记录应保持待重试(1) 等人工处置，实际 status=%d retry=%d", status, retry)
		}
		if stat.refundReleased != 0 || stat.failed != 0 {
			t.Fatalf("达上限记录不应被处理，实际 %+v", stat)
		}
		if len(batchReleaseCallsFor(env.product, refundNo)) != 0 {
			t.Fatal("达上限后不应再打扰渠道")
		}
	})

	t.Run("退款单不存在按失败推进重试", func(t *testing.T) {
		orderID := seedSchedOrder(t, db, "rr-missing", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Minute))
		fID := seedReleaseFailure(t, db, orderID, releaseRefundNoPrefix+"NOT-EXIST", 0)

		stat := runRefundReleaseOnce(t, env, ctx)
		status, retry, _ := loadReleaseFailure(t, db, fID)
		if status != 1 || retry != 1 {
			t.Fatalf("读不到退款单应保持待重试(1) 并推进 retry_count，实际 status=%d retry=%d", status, retry)
		}
		if stat.failed != 1 {
			t.Fatalf("读单失败应计入 failed，实际 %+v", stat)
		}
	})
}

// TestRefundReleaseReplayKeepsFailingUntilHealed 回补持续失败时的重放节奏与最终收敛
// 一轮重放内部按 runPostCommitEffect 做 postEffectRetry 次尝试（幂等槽位相同，product 侧只生效一次），
// 仍失败则推进 retry_count 并把记录留在队列；渠道恢复后下一轮自动收敛为已处理
func TestRefundReleaseReplayKeepsFailingUntilHealed(t *testing.T) {
	env := newSchedulerTestEnv(t)
	ctx := context.Background()
	db := env.db
	t.Cleanup(func() { cleanupRefundReleaseTestData(t, db) })

	orderID := seedSchedOrder(t, db, "rr-flaky", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Minute))
	refundNo := releaseRefundNoPrefix + "FLAKY"
	r := seedRefundWithNo(t, db, orderID, refundNo, 5)
	seedRefundItemWithSku(t, db, r.ID, 44104, 3)
	fID := seedReleaseFailure(t, db, orderID, refundNo, 0)
	env.product.failBatchRelease(refundNo)

	stat := runRefundReleaseOnce(t, env, ctx)
	if stat.failed != 1 || stat.refundReleased != 0 {
		t.Fatalf("回补失败轮次统计 = %+v, 期望 failed=1", stat)
	}
	if status, retry, errMsg := loadReleaseFailure(t, db, fID); status != 1 || retry != 1 || errMsg == "" {
		t.Fatalf("失败应保持待重试(1) 并推进 retry_count 与原因，实际 status=%d retry=%d msg=%q", status, retry, errMsg)
	}
	// 单轮内按重试节奏打到 postEffectRetry 次（同一幂等号，product 侧唯一键保证只生效一次）
	if n := len(batchReleaseCallsFor(env.product, refundNo)); n != postEffectRetry {
		t.Fatalf("失败轮次应尝试 %d 次，实际 %d 次", postEffectRetry, n)
	}

	// 渠道恢复：下一轮重放收敛
	env.product.healBatchRelease(refundNo)
	before := len(batchReleaseCallsFor(env.product, refundNo))
	stat = runRefundReleaseOnce(t, env, ctx)
	if stat.refundReleased != 1 {
		t.Fatalf("恢复后重放应成功，实际 %+v", stat)
	}
	if status, _, _ := loadReleaseFailure(t, db, fID); status != 2 {
		t.Fatalf("收敛后记录应为已处理(2)，实际 %d", status)
	}
	calls := batchReleaseCallsFor(env.product, refundNo)
	if len(calls) != before+1 {
		t.Fatalf("成功轮次应只补做一次，实际 %d 次（此前 %d 次）", len(calls), before)
	}
	// 重放与首失败的幂等槽位必须一致，否则 product 侧去重失效导致重复回补
	for _, c := range calls {
		if c.GetBizNo() != refundNo || c.GetOrderId() != orderID {
			t.Fatalf("重放槽位漂移: order_id=%d biz_no=%q", c.GetOrderId(), c.GetBizNo())
		}
	}
}

// TestRefundReleaseReplayCouponFailureCountsAsFailed 优惠券回补失败同样入队重放
// 库存与优惠券互不阻断：库存成功、券失败时整条记录仍留在队列，重放时库存按同槽位被 product 去重
func TestRefundReleaseReplayCouponFailureCountsAsFailed(t *testing.T) {
	env := newSchedulerTestEnv(t)
	ctx := context.Background()
	db := env.db
	t.Cleanup(func() { cleanupRefundReleaseTestData(t, db) })

	orderID := seedSchedOrder(t, db, "rr-coupfail", 8, schedTimeAgo(time.Hour), schedTimeAgo(time.Minute))
	if err := db.GetDb().WithContext(ctx).Exec(
		"UPDATE orders SET coupon_id = 99100078 WHERE id = ?", orderID).Error; err != nil {
		t.Fatalf("预置用券订单失败: %v", err)
	}
	refundNo := releaseRefundNoPrefix + "COUPON-FAIL"
	r := seedRefundWithNo(t, db, orderID, refundNo, 5)
	seedRefundItemWithSku(t, db, r.ID, 44105, 1)
	fID := seedReleaseFailure(t, db, orderID, refundNo, 0)
	env.product.failReleaseCoupon(orderID)

	if stat := runRefundReleaseOnce(t, env, ctx); stat.failed != 1 {
		t.Fatalf("券回补失败应计入 failed，实际 %+v", stat)
	}
	if status, retry, _ := loadReleaseFailure(t, db, fID); status != 1 || retry != 1 {
		t.Fatalf("券失败应留在队列，实际 status=%d retry=%d", status, retry)
	}
	if len(env.product.couponCalls()) != postEffectRetry {
		t.Fatalf("券回补应按重试节奏尝试 %d 次，实际 %d 次", postEffectRetry, len(env.product.couponCalls()))
	}

	env.product.healReleaseCoupon(orderID)
	stockBefore := len(batchReleaseCallsFor(env.product, refundNo))
	if stat := runRefundReleaseOnce(t, env, ctx); stat.refundReleased != 1 {
		t.Fatalf("券恢复后重放应成功，实际 %+v", stat)
	}
	if status, _, _ := loadReleaseFailure(t, db, fID); status != 2 {
		t.Fatalf("收敛后记录应为已处理(2)，实际 %d", status)
	}
	// 重放会再次调用库存回补（同槽位），去重由 product 侧唯一键负责，订单侧不重复计数
	if len(batchReleaseCallsFor(env.product, refundNo)) <= stockBefore {
		t.Fatal("重放轮次应重新提交库存回补（由 product 侧幂等去重）")
	}
}

// TestCompleteRefundSuccessEnqueuesReleaseFailure 退款收尾回补失败即入队，且不影响退款成功
// 覆盖的是本次修复的入口侧（前面各例只喂现成的失败记录）：
// 退款单已置成功(5)、订单已置已退款(8) 之后远端回补仍失败，必须留下一条
// task_type=refund_release、biz_no=退款单号 的待重试记录，随后一轮重放即收敛。
// 关键不变式：钱已退的既成事实不回滚，货/券靠重放对齐
func TestCompleteRefundSuccessEnqueuesReleaseFailure(t *testing.T) {
	env := newRefundTestEnv(t)
	ctx := context.Background()
	db := env.sched.db
	t.Cleanup(func() { cleanupRefundReleaseTestData(t, db) })

	// 前置态与整单退正常路径一致：订单退款中(7)、来源 2→7、退款单退款中(4) 且金额等于实付
	orderID := seedSchedOrder(t, db, "rr-enqueue", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	seedSchedItem(t, db, orderID, 44110, 1)
	seedOrderStatusLog(t, env.db, orderID, 2, 7)
	refundNo := releaseRefundNoPrefix + "ENQUEUE"
	refund := seedRefundWithNo(t, db, orderID, refundNo, 4)
	// 收尾回补按预设失败，重试节奏与 runPostCommitEffect 一致
	env.product.failBatchRelease(refundNo)

	if err := env.svc.completeRefundSuccess(ctx, refund, []*model.RefundItem{{SkuID: 44110, Quantity: 1}}, "admin", 1); err != nil {
		t.Fatalf("远端回补失败不得让退款收尾返回业务失败: %v", err)
	}
	// 退款既成事实：退款单 5、订单 8
	if st, _, _, _ := refundRowStatus(t, env.db, refund.ID); st != 5 {
		t.Fatalf("退款单应已置已退款(5), got %d", st)
	}
	if st, _, _ := orderSchedStatus(t, db, orderID); st != 8 {
		t.Fatalf("订单应已流转为已退款(8), got %d", st)
	}
	// 回补已按三次重试尝试过（不是失败一次就入队）
	if n := len(batchReleaseCallsFor(env.product, refundNo)); n != postEffectRetry {
		t.Fatalf("入队前应尝试回补 %d 次，实际 %d 次", postEffectRetry, n)
	}
	// 队列里必须恰好一条，且槽位是退款单号（同一订单的其他退款各有槽位）
	var fID int64
	var taskType, bizNo string
	var status, retry int32
	var count int64
	if err := db.GetDb().WithContext(ctx).Model(&model.SchedulerFailure{}).
		Where("order_id = ? AND task_type = ?", orderID, taskTypeRefundRelease).Count(&count).Error; err != nil {
		t.Fatalf("统计退款收尾失败记录失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("退款收尾失败应恰好入队一条，实际 %d", count)
	}
	if err := db.GetDb().WithContext(ctx).Raw(
		"SELECT id, task_type, biz_no, status, retry_count FROM order_scheduler_failures WHERE order_id = ? AND task_type = ?",
		orderID, taskTypeRefundRelease).Row().Scan(&fID, &taskType, &bizNo, &status, &retry); err != nil {
		t.Fatalf("回查入队记录失败: %v", err)
	}
	if bizNo != refundNo {
		t.Errorf("入队 biz_no 应为退款单号（幂等槽位）: got=%q want=%q", bizNo, refundNo)
	}
	if status != 1 || retry != 0 {
		t.Errorf("新入队记录应为待重试(1) 且未消耗重试次数: status=%d retry=%d", status, retry)
	}

	// 闭环：恢复商品服务后跑一轮调度，同一条记录转已处理，不再新增记录
	env.product.healBatchRelease(refundNo)
	if stat := runRefundReleaseOnce(t, env.sched, ctx); stat.refundReleased != 1 {
		t.Fatalf("恢复后重放应成功，实际 %+v", stat)
	}
	if st, _, _ := loadReleaseFailure(t, db, fID); st != 2 {
		t.Fatalf("重放收敛后记录应为已处理(2)，实际 %d", st)
	}
	if err := db.GetDb().WithContext(ctx).Model(&model.SchedulerFailure{}).
		Where("order_id = ? AND task_type = ?", orderID, taskTypeRefundRelease).Count(&count).Error; err != nil {
		t.Fatalf("复核重放后记录数失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("重放应复用同一条记录，不得重复入队: count=%d", count)
	}
}

// TestRecordSchedulerFailureWritesBizNoAndTruncates 入队写库口径：biz_no 落库、错误原因按字符截断
// 入队脱离调用方取消信号（退款收尾常在客户端断连后触发），且中文超长原因不得把字切坏
func TestRecordSchedulerFailureWritesBizNoAndTruncates(t *testing.T) {
	env := newSchedulerTestEnv(t)
	ctx := context.Background()
	db := env.db
	t.Cleanup(func() { cleanupRefundReleaseTestData(t, db) })

	orderID := seedSchedOrder(t, db, "rr-enqueue", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Minute))
	refundNo := releaseRefundNoPrefix + "ENQUEUE"
	// 超长中文原因：按字节截断会切成半个字，MySQL 直接报 1406 丢掉整条记录
	longMsg := "回补库存失败: " + strings.Repeat("渠道抖动未收敛", 80)
	recordSchedulerFailure(ctx, db, orderID, taskTypeRefundRelease, refundNo, longMsg)

	var rows []model.SchedulerFailure
	if err := db.GetDb().WithContext(ctx).
		Where("task_type = ? AND order_id = ?", taskTypeRefundRelease, orderID).Find(&rows).Error; err != nil {
		t.Fatalf("查询入队记录失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("收尾最终失败应恰入队 1 条，实际 %d 条", len(rows))
	}
	got := rows[0]
	if got.BizNo != refundNo || got.Status != 1 || got.RetryCount != 0 {
		t.Fatalf("入队记录字段不符: %+v", got)
	}
	if runes := []rune(got.ErrorMsg); len(runes) > schedulerFailureMsgMax {
		t.Fatalf("error_msg 应按 %d 字符截断，实际 %d 字符", schedulerFailureMsgMax, len(runes))
	}
	if !strings.HasSuffix(got.ErrorMsg, "…") {
		t.Fatalf("截断应带 … 标记，实际 %q", tailRunes(got.ErrorMsg, 5))
	}
	if strings.ContainsRune(got.ErrorMsg, '\uFFFD') {
		t.Fatalf("截断后出现替换字符（半个字）: %q", got.ErrorMsg)
	}

	// 调用方 ctx 已取消时仍要入队：收尾失败记录是资金口径的最后一道保险
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	recordSchedulerFailure(canceled, db, orderID, taskTypeRefundRelease, refundNo+"-CANCELED", "调用方已断连")
	var afterCancel int64
	if err := db.GetDb().WithContext(context.Background()).Model(&model.SchedulerFailure{}).
		Where("task_type = ? AND biz_no = ?", taskTypeRefundRelease, refundNo+"-CANCELED").Count(&afterCancel).Error; err != nil {
		t.Fatalf("统计断连后入队记录失败: %v", err)
	}
	if afterCancel != 1 {
		t.Fatalf("调用方 ctx 取消不应影响入队，实际记录 %d 条", afterCancel)
	}
}

// TestTruncateRunes 失败原因截断的纯函数口径
// MySQL varchar(N) 按字符计数：长度未超字节上限但超字符上限的中文串同样要截断
func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name  string
		value string
		max   int
		want  string
	}{
		{"不截断", "rpc error: code = Unavailable", 500, "rpc error: code = Unavailable"},
		{"max 非正数原样返回", "回补库存失败", 0, "回补库存失败"},
		{"ASCII 截断", "abcdef", 4, "abc…"},
		{"中文按字符截断不切字", "回补库存失败", 4, "回补库…"},
		{"字节超上限但字符未超不截断", "中文中文中文", 8, "中文中文中文"},
	}
	for _, c := range cases {
		if got := truncateRunes(c.value, c.max); got != c.want {
			t.Errorf("%s: truncateRunes(%q, %d) = %q, 期望 %q", c.name, c.value, c.max, got, c.want)
		}
	}
	// 恰好等于上限：不追加省略号，也不丢字符
	exact := strings.Repeat("退", schedulerFailureMsgMax)
	if got := truncateRunes(exact, schedulerFailureMsgMax); got != exact {
		t.Errorf("恰好 %d 字符不应被截断，实际长度 %d", schedulerFailureMsgMax, len([]rune(got)))
	}
}

// cleanupRefundReleaseTestData 删除退款收尾重放用例产生的失败记录与退款单
// 订单/明细/状态日志由 newSchedulerTestEnv 的 cleanup 按 SCHEDTEST- 前缀删除；
// 失败记录只删本用例测试订单名下（或测试前缀幂等号），不动开发库中真实待重试记录
// 参数：
//   - t: 测试对象
//   - db: 测试库
func cleanupRefundReleaseTestData(t *testing.T, db *mysql.Instance) {
	t.Helper()
	ctx := context.Background()
	like := schedTestOrderNoPrefix + "%"
	_ = db.GetDb().WithContext(ctx).Exec(
		"DELETE FROM order_scheduler_failures WHERE biz_no LIKE ? OR order_id IN (SELECT id FROM orders WHERE order_no LIKE ?)",
		releaseRefundNoPrefix+"%", like).Error
	_ = db.GetDb().WithContext(ctx).Exec(
		"DELETE FROM refund_items WHERE refund_id IN (SELECT id FROM refunds WHERE refund_no LIKE ?)",
		releaseRefundNoPrefix+"%").Error
	_ = db.GetDb().WithContext(ctx).Exec(
		"DELETE FROM refunds WHERE refund_no LIKE ?", releaseRefundNoPrefix+"%").Error
	_ = db.GetDb().WithContext(ctx).Exec(
		"DELETE FROM order_status_logs WHERE order_id IN (SELECT id FROM orders WHERE order_no LIKE ?)", like).Error
}
