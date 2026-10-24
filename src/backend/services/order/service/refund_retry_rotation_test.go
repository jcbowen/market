package service

import (
	"context"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 卡单退款自动重试的轮转与预算测试（B1）
 *
 * 盯的故障形态：扫描按 `id asc + Limit(20)` 取卡单，而「渠道受理中(PROCESSING)」这类
 * 本轮什么都不改的 continue 分支不推进任何指针——队首这批单每轮都被重复取出并占满批次
 * 名额，后来卡住的退款单永远排不进去（与 payment 侧修过的同类饿死同源），
 * 同时还每轮反复打 payment 的 QueryRefundResult RPC。
 * 现口径：按节流指针 last_checked_at asc 做游标轮转，取出后**无条件推进**指针并累加
 * retry_count（UpdateColumns 专避 updated_at，否则起扫判据被自己的扫描清零），
 * 累计达 refundMaxRetry 即停止自动重试、落一条 give_up 人工待办。
 *
 * 运行：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/order/service/ -run TestSchedulerRefundRetryRotation -v
 *
 * 测试数据沿用 SCHEDTEST- 前缀（退款三表由 newRefundTestEnv 的 cleanup 删除）。
 */

// refundSeed 卡单退款测试数据构造参数（零值走默认：状态 4、停留 30 分钟、从未核查、零重试）
type refundSeed struct {
	No         string
	OrderID    int64
	Status     int32
	RetryCount int32
	// CheckedAgo >0 时写 last_checked_at（相对当前时间），0 表示从未核查（NULL）
	CheckedAgo time.Duration
	// UpdatedAgo 退款单停留当前状态的时长基准，默认 30 分钟（超过 stuckRecoverAfter 起扫阈值）
	UpdatedAgo time.Duration
}

// seedStuckRefund 裸 SQL 插入一条退款单
// 必须走裸 SQL：base 模型的 BeforeCreate 钩子会把 created_at/updated_at 覆写为当前时间，
// 而本组用例的核心正是「停留时长」与「节流指针」构造出的时间窗
//
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - seed: 构造参数
//
// 返回值：
//   - *model.Refund: 已落库的退款单（含自增 ID）
func seedStuckRefund(t *testing.T, db *mysql.Instance, seed refundSeed) *model.Refund {
	t.Helper()
	now := time.Now()
	updatedAgo := seed.UpdatedAgo
	if updatedAgo == 0 {
		updatedAgo = 30 * time.Minute
	}
	status := seed.Status
	if status == 0 {
		status = 4
	}
	var lastChecked interface{}
	if seed.CheckedAgo > 0 {
		lastChecked = now.Add(-seed.CheckedAgo).Format("2006-01-02 15:04:05")
	}
	res := db.GetDb().Exec(
		`INSERT INTO refunds
			(refund_no, order_id, user_id, payment_no, amount, freight_amount, type, reason, status,
			 retry_count, last_checked_at, created_at, updated_at)
		 VALUES (?, ?, 99100001, ?, 12.00, 0.00, 1, 'B1 卡单重试测试', ?, ?, ?, ?, ?)`,
		seed.No, seed.OrderID, "SCHEDTEST-PAY-"+seed.No, status, seed.RetryCount, lastChecked,
		now.Add(-updatedAgo).Format("2006-01-02 15:04:05"), now.Add(-updatedAgo).Format("2006-01-02 15:04:05"))
	if res.Error != nil {
		t.Fatalf("插入测试退款单 %s 失败: %v", seed.No, res.Error)
	}
	var r model.Refund
	if err := db.GetDb().Where("refund_no = ?", seed.No).First(&r).Error; err != nil {
		t.Fatalf("读回测试退款单 %s 失败: %v", seed.No, err)
	}
	return &r
}

// refundCheckRow 回查退款单的节流指针、重试计数、更新时间与状态
// updated_at 用 time.Time 承接：parseTime=True 下读回 string 是 RFC3339 形态，
// 与写入字面量做字符串比较必然失真（本用例要断言的是「指针推进不刷 updated_at」）
//
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - refundID: 退款单 ID
//
// 返回值：
//   - *time.Time: last_checked_at（NULL 表示从未核查）
//   - int32: retry_count
//   - time.Time: updated_at
//   - int32: status
func refundCheckRow(t *testing.T, db *mysql.Instance, refundID int64) (*time.Time, int32, time.Time, int32) {
	t.Helper()
	var (
		checked   *time.Time
		retry     int32
		updatedAt time.Time
		status    int32
	)
	err := db.GetDb().Raw(
		"SELECT last_checked_at, retry_count, updated_at, status FROM refunds WHERE id = ?", refundID,
	).Row().Scan(&checked, &retry, &updatedAt, &status)
	if err != nil {
		t.Fatalf("回查退款单 %d 失败: %v", refundID, err)
	}
	return checked, retry, updatedAt, status
}

// refundLogActions 回查退款单的轨迹动作列表（give_up 待办是否落账、是否只落一次）
func refundLogActions(t *testing.T, db *mysql.Instance, refundID int64) []string {
	t.Helper()
	var actions []string
	if err := db.GetDb().Model(&model.RefundLog{}).Where("refund_id = ?", refundID).
		Order("id asc").Pluck("action", &actions).Error; err != nil {
		t.Fatalf("回查退款单 %d 轨迹失败: %v", refundID, err)
	}
	return actions
}

// countAction 统计动作出现次数
func countAction(actions []string, want string) int {
	n := 0
	for _, a := range actions {
		if a == want {
			n++
		}
	}
	return n
}

// TestSchedulerRefundRetryRotation 卡单重试的游标轮转、无条件推进与预算封顶
func TestSchedulerRefundRetryRotation(t *testing.T) {
	ctx := context.Background()

	t.Run("节流窗口内的旧单不挡新单", func(t *testing.T) {
		env := newRefundTestEnv(t)
		orderID := seedSchedOrder(t, env.db, "B1-A", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
		// A 先插入（ID 更小）：旧实现按 id asc 取，A 每轮都被取出又每轮什么都不改
		a := seedStuckRefund(t, env.db, refundSeed{No: "SCHEDTEST-RF-B1-A", OrderID: orderID, CheckedAgo: time.Minute})
		b := seedStuckRefund(t, env.db, refundSeed{No: "SCHEDTEST-RF-B1-B", OrderID: orderID})

		sched := NewScheduler(env.db, env.sched.redis, env.svc, nil, nil)
		sched.retryStuckRefunds(ctx)

		calls := env.sched.payment.flowQueryCalls()
		if len(calls) != 1 || calls[0] != b.RefundNo {
			t.Fatalf("应只核查从未核查的 B，实际核查了 %v（A 的指针推进时间=%d）", calls, a.RetryCount)
		}
		if checked, _, _, _ := refundCheckRow(t, env.db, b.ID); checked == nil {
			t.Error("B 核查后应写入 last_checked_at 节流指针")
		}
		if checked, retry, _, _ := refundCheckRow(t, env.db, a.ID); checked == nil || retry != 0 {
			t.Errorf("A 在节流窗口内不应入批, last_checked_at=%v retry=%d", checked, retry)
		}
	})

	t.Run("渠道受理中同样推进指针", func(t *testing.T) {
		env := newRefundTestEnv(t)
		orderID := seedSchedOrder(t, env.db, "B1-C", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
		c := seedStuckRefund(t, env.db, refundSeed{No: "SCHEDTEST-RF-B1-C", OrderID: orderID})
		// 桩默认即「渠道受理中」，此处显式置位让用例意图不依赖桩默认值
		env.sched.payment.setRefundFlow(c.RefundNo, 1)
		_, _, updatedAtBefore, _ := refundCheckRow(t, env.db, c.ID)

		sched := NewScheduler(env.db, env.sched.redis, env.svc, nil, nil)
		stat := sched.retryStuckRefunds(ctx)

		checked, retry, updatedAt, status := refundCheckRow(t, env.db, c.ID)
		if checked == nil || retry != 1 {
			t.Fatalf("受理中跳过也必须推进指针与计数, last_checked_at=%v retry=%d", checked, retry)
		}
		if !updatedAt.Equal(updatedAtBefore) {
			t.Errorf("推进指针不得刷新 updated_at（它是停留时长起扫判据）, before=%v after=%v", updatedAtBefore, updatedAt)
		}
		if status != 4 {
			t.Errorf("渠道受理中不应改动退款单状态, status=%d", status)
		}
		if stat.refundOK != 0 || stat.failed != 0 {
			t.Errorf("本轮只是核查，无成功也无失败, refundOK=%d failed=%d", stat.refundOK, stat.failed)
		}

		// 节流窗口内再跑一轮：同一单不得再次打扰 payment 查单
		before := len(env.sched.payment.flowQueryCalls())
		sched.retryStuckRefunds(ctx)
		if after := len(env.sched.payment.flowQueryCalls()); after != before {
			t.Errorf("节流窗口内不应重复核查同一退款单, 前=%d 后=%d", before, after)
		}
	})

	t.Run("用满预算转人工并停止自动重试", func(t *testing.T) {
		env := newRefundTestEnv(t)
		orderID := seedSchedOrder(t, env.db, "B1-D", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
		d := seedStuckRefund(t, env.db, refundSeed{
			No: "SCHEDTEST-RF-B1-D", OrderID: orderID, RetryCount: int32(refundMaxRetry - 1),
		})
		// 若本轮没被判为用满，这里会走重发分支；预置受理中确保只可能命中 give_up
		env.sched.payment.setRefundFlow(d.RefundNo, 1)

		sched := NewScheduler(env.db, env.sched.redis, env.svc, nil, nil)
		stat := sched.retryStuckRefunds(ctx)

		if _, retry, _, _ := refundCheckRow(t, env.db, d.ID); retry != int32(refundMaxRetry) {
			t.Fatalf("本轮应把计数推满, retry=%d want=%d", retry, refundMaxRetry)
		}
		actions := refundLogActions(t, env.db, d.ID)
		if got := countAction(actions, "give_up"); got != 1 {
			t.Errorf("应且仅应落一条 give_up 人工待办, got=%d actions=%v", got, actions)
		}
		if stat.failed != 1 {
			t.Errorf("转人工应计为异常轮次以便 debugger 留档, failed=%d", stat.failed)
		}
		// 注：不断言测试侧 d 对象的内存字段——调度器内部 Find 加载的是独立副本，
		// markRefundChecked 推进的是该副本的计数，与测试侧引用无关；落库语义已由上方 DB 回查覆盖

		// 再跑两轮：用满的单不再入批（既不打扰渠道，也不会重复写待办）
		sched.retryStuckRefunds(ctx)
		sched.retryStuckRefunds(ctx)
		if got := countAction(refundLogActions(t, env.db, d.ID), "give_up"); got != 1 {
			t.Errorf("用满后不应重复产生待办, got=%d", got)
		}
		for _, no := range env.sched.payment.flowQueryCalls() {
			if no == d.RefundNo {
				t.Errorf("用满上限的单不该再发起流水核查, calls=%v", env.sched.payment.flowQueryCalls())
			}
		}
	})

	t.Run("流水查询失败计为失败但仍推进指针", func(t *testing.T) {
		env := newRefundTestEnv(t)
		orderID := seedSchedOrder(t, env.db, "B1-E", 7, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
		e := seedStuckRefund(t, env.db, refundSeed{No: "SCHEDTEST-RF-B1-E", OrderID: orderID})
		env.sched.payment.failRefundFlowQuery(e.RefundNo)

		sched := NewScheduler(env.db, env.sched.redis, env.svc, nil, nil)
		stat := sched.retryStuckRefunds(ctx)

		if stat.failed != 1 {
			t.Errorf("流水查询失败应计入本轮失败, failed=%d", stat.failed)
		}
		if checked, retry, _, _ := refundCheckRow(t, env.db, e.ID); checked == nil || retry != 1 {
			t.Errorf("查询失败同样要推进指针，否则该单每轮重复打扰渠道, last_checked_at=%v retry=%d", checked, retry)
		}
		if _, _, _, status := refundCheckRow(t, env.db, e.ID); status != 4 {
			t.Errorf("查询失败不得改动资金相关状态, status=%d", status)
		}
	})
}
