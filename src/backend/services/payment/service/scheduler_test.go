package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 支付对账调度器测试（solution-20261021-支付回调可靠性-v2.md 第九节 T1–T12）
 *
 * 纯函数测试（无外部依赖，CI 可跑）：
 *   - TestWithinReconcileWindow   回查时间窗（微信 120min / 支付宝取 min(窗口, 订单超时)）
 *   - TestParseDBTime             库内时间字符串解析（服务自写格式 + 驱动读回的 RFC3339）
 *   - TestNeedsHandlingMatrix     异常页待处理判定矩阵
 *   - TestNotifyEntryFromSnapshot 流水归属快照（社区/订单号取自支付单）
 *   - TestReconcileConfirmRef     对账凭据格式
 *
 * 集成测试（直连本地 docker 栈的 MySQL/Redis，env 守卫跳过）：
 *   运行方式（渠道调用全部由 fakeChannels 替身接管，不会碰真实商户）：
 *     TEST_MYSQL_PASSWORD=xxx TEST_REDIS_PASSWORD=yyy go test ./services/payment/service/ -run TestScheduler -v
 *   注意：Redis 固定使用 DB 1，与运行中的 payment 容器的调度锁（DB 0）隔离；
 *   若容器已升级为含对账任务的新镜像并在跑，请先 `docker compose stop payment-service`，
 *   否则两个调度器会争抢同一批支付单，扫描类用例（T3/T4）可能出现偶发。
 *
 *   - T1 TestSchedulerReconcileRecoversLostCallback 回调丢失被主动回查捞回
 *   - T2 TestSchedulerReconcileNotPaidKeepsPending   渠道未收款只推进节流
 *   - T3 TestSchedulerReconcilePendingScanFilters    起扫年龄/节流/累计上限三道过滤
 *   - T4 TestSchedulerReconcileGiveUpAfterLimit      用满上限写「放弃对账」待办并停止回查
 *   - T5 TestSchedulerNotExistNeedsTwoRounds         渠道无此交易连续两轮才关单
 *   - T5b TestSchedulerChannelClosedClosesPendingImmediately 渠道已关闭的交易本轮即关单
 *   - T6 TestSchedulerAmountMismatchNeedsHuman       金额不符拒绝确认且进人工待办
 *   - T7 TestSchedulerConcurrentConfirmIsIdempotent  回调与补偿并发只有一笔生效
 *   - T9 TestSchedulerDetectStuckRecordsAnomaly      关闭单挂账检出
 *   - T10 TestSchedulerDetectStuckDeduplicates       挂账待办去重 + 渠道已答未付不再问
 *   - T11 TestClosePayments*                         关单前核实：关渠道交易 / 挂账 / 开关关闭
 *   - T12 TestSchedulerRunOnceDisabledSkipsChannels   总开关关闭后零渠道调用
 *   - TestSchedulerReconcileSkipsExpiredWindow        超出渠道时效不回查
 *   - TestSchedulerQueryFailureKeepsPaymentOpen       渠道查询失败不判死、进失败重试
 *   - TestSchedulerCleanupNotifyLogs                  流水按保留天数清理
 *   - TestSchedulerNilRedisSkipsStart                 Redis 缺失时不启动也不 panic
 */

// schedOrderIDBase 调度器测试订单 ID 起始偏移（避开 payment_test.go 已占用的 +1..+10）
const schedOrderIDBase = testOrderIDBase + 200

// fakeAnswer 假渠道对某个支付单号的应答
type fakeAnswer struct {
	res *channelPayResult
	err error
}

// fakeChannels 渠道查询与关单的测试替身
// 未预置应答的支付单一律返回「查询失败」：这与真实渠道不可达同义，
// 被测代码不得据此改动资金状态，因此测试里若出现「意外被查」也只会被计数暴露出来
type fakeChannels struct {
	mu      sync.Mutex
	answers map[string]fakeAnswer
	queries []string
	closes  []string
}

// newFakeChannels 创建空应答的渠道替身
//
// 返回值：
//   - *fakeChannels: 替身实例
func newFakeChannels() *fakeChannels {
	return &fakeChannels{answers: make(map[string]fakeAnswer)}
}

// set 预置某支付单号的渠道查询应答
// 参数：
//   - no: 支付单号
//   - res: 归一化结果
func (f *fakeChannels) set(no string, res *channelPayResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[no] = fakeAnswer{res: res}
}

// setPaid 预置「渠道已收款」
// 参数：
//   - no: 支付单号
//   - transactionID: 渠道交易号
//   - amount: 渠道侧金额（元）
func (f *fakeChannels) setPaid(no, transactionID string, amount float64) {
	f.set(no, &channelPayResult{State: ChannelStatePaid, TransactionID: transactionID, Amount: amount, Raw: "SUCCESS"})
}

// setPaying 预置「用户仍在支付中」
// 参数：
//   - no: 支付单号
func (f *fakeChannels) setPaying(no string) {
	f.set(no, &channelPayResult{State: ChannelStatePaying, Raw: "NOTPAY"})
}

// setNotExist 预置「渠道无此交易」
// 参数：
//   - no: 支付单号
func (f *fakeChannels) setNotExist(no string) {
	f.set(no, &channelPayResult{State: ChannelStateNotExist, Raw: "ORDER_NOT_EXIST"})
}

// setChannelClosed 预置「渠道交易已关闭」
// 参数：
//   - no: 支付单号
func (f *fakeChannels) setChannelClosed(no string) {
	f.set(no, &channelPayResult{State: ChannelStateClosed, Raw: "CLOSED"})
}

// fail 预置某支付单号的渠道查询失败（网络/配置故障，与「渠道明确未收款」语义不同）
// 参数：
//   - no: 支付单号
//   - err: 失败原因
func (f *fakeChannels) fail(no string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[no] = fakeAnswer{err: err}
}

// queryCount 该支付单被查了几次
// 参数：
//   - no: 支付单号
//
// 返回值：
//   - int: 调用次数
func (f *fakeChannels) queryCount(no string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	for _, got := range f.queries {
		if got == no {
			n++
		}
	}
	return n
}

// closeCount 该支付单的渠道交易被关闭了几次
// 参数：
//   - no: 支付单号
//
// 返回值：
//   - int: 调用次数
func (f *fakeChannels) closeCount(no string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	for _, got := range f.closes {
		if got == no {
			n++
		}
	}
	return n
}

// totalQueries 全部渠道查询调用次数（用于「零渠道调用」断言）
//
// 返回值：
//   - int: 总次数
func (f *fakeChannels) totalQueries() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queries)
}

// query 渠道查询替身实现（签名与 defaultQueryWechatOrder 一致）
func (f *fakeChannels) query(_ context.Context, _ *PaymentService, paymentNo string) (*channelPayResult, error) {
	f.mu.Lock()
	f.queries = append(f.queries, paymentNo)
	ans, ok := f.answers[paymentNo]
	f.mu.Unlock()
	if !ok {
		return &channelPayResult{State: ChannelStateError, Raw: "no fake answer"}, fmt.Errorf("测试未预置 %s 的渠道应答", paymentNo)
	}
	if ans.err != nil {
		return &channelPayResult{State: ChannelStateError, Raw: ans.err.Error()}, ans.err
	}
	return ans.res, nil
}

// close 渠道关单替身实现
func (f *fakeChannels) close(_ context.Context, _ *PaymentService, paymentNo string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes = append(f.closes, paymentNo)
	return nil
}

// installFakeChannels 把四个渠道接缝替换为替身，测试结束自动还原
// 参数：
//   - t: 测试对象
//   - f: 替身
//
// 使用示例：
//   - installFakeChannels(t, newFakeChannels())
func installFakeChannels(t *testing.T, f *fakeChannels) {
	t.Helper()
	queryWechatOrder = f.query
	queryAlipayOrder = f.query
	closeWechatOrder = f.close
	closeAlipayOrder = f.close
	t.Cleanup(func() {
		queryWechatOrder = defaultQueryWechatOrder
		queryAlipayOrder = defaultQueryAlipayOrder
		closeWechatOrder = defaultCloseWechatOrder
		closeAlipayOrder = defaultCloseAlipayOrder
	})
}

// schedEnv 调度器测试环境句柄
type schedEnv struct {
	s     *PaymentService
	sched *Scheduler
	stub  *stubOrderClient
	fc    *fakeChannels
	rdb   *redis.Client
}

// newSchedEnv 构建调度器测试环境：真实 DB + 真实 Redis（DB 1）+ 假渠道
// Redis 选 DB 1 是为了与正在运行的 payment 容器的调度锁隔离，
// 否则容器持有的 pending/stuck 锁会让本轮测试直接跳过扫描
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *schedEnv: 被测调度器、支付服务、订单桩与渠道替身
func newSchedEnv(t *testing.T) *schedEnv {
	t.Helper()
	s, stub, cleanup := newPaymentTestEnv(t)
	t.Cleanup(cleanup)

	rdb := redis.NewClient(&redis.Options{
		Addr:     envOr("TEST_REDIS_ADDR", "127.0.0.1:6379"),
		Password: os.Getenv("TEST_REDIS_PASSWORD"),
		DB:       1,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("连接测试 Redis 失败: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	fc := newFakeChannels()
	installFakeChannels(t, fc)
	return &schedEnv{s: s, sched: NewScheduler(s, rdb), stub: stub, fc: fc, rdb: rdb}
}

// schedSeed 测试支付单的构造参数（零值走默认：微信渠道、待支付、创建于 10 分钟前）
type schedSeed struct {
	No             string
	OrderID        int64
	Amount         float64
	PayType        int32
	Status         int32
	CreatedAgo     time.Duration
	UpdatedAgo     time.Duration
	ReconcileCount int32
	LastRunAgo     time.Duration // >0 时写 last_reconciled_at（节流指针）
	TransactionID  string
	CommunityID    int64
}

// seedSchedPayment 按参数插入一条测试支付单
// 时间统一相对 now 生成：对账扫描与时间窗都按 created_at/updated_at 过滤。
// 必须走裸 SQL：模型嵌入的 base.CommonBaseModel 有 BeforeCreate 钩子，
// db.Create 会把 created_at/updated_at 无条件改写成当前时间，测试构造的时间窗会被静默抹掉
//
// 参数：
//   - t: 测试对象
//   - s: 被测服务（复用其 DB 连接）
//   - seed: 构造参数
//
// 返回值：
//   - *model.Payment: 已落库的支付单
func seedSchedPayment(t *testing.T, s *PaymentService, seed schedSeed) *model.Payment {
	t.Helper()
	now := time.Now()
	createdAgo := seed.CreatedAgo
	if createdAgo == 0 {
		createdAgo = 10 * time.Minute
	}
	updatedAgo := seed.UpdatedAgo
	if updatedAgo == 0 {
		updatedAgo = createdAgo
	}
	var lastRun interface{}
	if seed.LastRunAgo > 0 {
		lastRun = now.Add(-seed.LastRunAgo).Format(timeLayout)
	}
	res := s.db.GetDb().Exec(
		`INSERT INTO payments
			(payment_no, order_id, order_no, user_id, community_id, pay_type, amount, status,
			 transaction_id, confirm_source, reconcile_count, last_reconciled_at, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		seed.No, seed.OrderID, fmt.Sprintf("NO%d", seed.OrderID), seed.CommunityID,
		int32OrDefault(seed.PayType, 1), seed.Amount, int32OrDefault(seed.Status, 1),
		seed.TransactionID, seed.ReconcileCount, lastRun,
		now.Add(-createdAgo).Format(timeLayout), now.Add(-updatedAgo).Format(timeLayout))
	if res.Error != nil {
		t.Fatalf("插入测试支付单 %s 失败: %v", seed.No, res.Error)
	}
	var p model.Payment
	if err := s.db.GetDb().Where("payment_no = ?", seed.No).First(&p).Error; err != nil {
		t.Fatalf("回查测试支付单 %s 失败: %v", seed.No, err)
	}
	return &p
}

// seedAgedNotifyLog 直插一条指定时刻的流水（同样绕开 BeforeCreate 钩子）
// 参数：
//   - t: 测试对象
//   - s: 被测服务
//   - paymentNo: 支付单号
//   - orderID: 订单 ID（测试数据清理依据）
//   - createdAt: 流水创建时刻
func seedAgedNotifyLog(t *testing.T, s *PaymentService, paymentNo string, orderID int64, createdAt time.Time) {
	t.Helper()
	res := s.db.GetDb().Exec(
		`INSERT INTO payment_notify_logs
			(kind, result, channel, payment_no, order_id, order_no, community_id,
			 handled, error_msg, created_at, updated_at)
		 VALUES (?, ?, 1, ?, ?, ?, 1, ?, '测试流水', ?, ?)`,
		model.NotifyKindCallback, model.NotifyResultVerifyFailed, paymentNo, orderID,
		fmt.Sprintf("NO%d", orderID), model.NotifyPending,
		createdAt.Format(timeLayout), createdAt.Format(timeLayout))
	if res.Error != nil {
		t.Fatalf("插入测试流水 %s 失败: %v", paymentNo, res.Error)
	}
}

// int32OrDefault 零值取默认（测试构造用，避免每个字段都显式写默认值）
// 参数：
//   - value: 给定值
//   - def: 默认值
//
// 返回值：
//   - int32: value 为 0 时取 def
func int32OrDefault(value, def int32) int32 {
	if value == 0 {
		return def
	}
	return value
}

// reloadPayment 重新读出支付单当前状态
// 参数：
//   - t: 测试对象
//   - s: 被测服务
//   - id: 支付单主键
//
// 返回值：
//   - *model.Payment: 最新行
func reloadPayment(t *testing.T, s *PaymentService, id int64) *model.Payment {
	t.Helper()
	var p model.Payment
	if err := s.db.GetDb().Where("id = ?", id).First(&p).Error; err != nil {
		t.Fatalf("读取支付单 %d 失败: %v", id, err)
	}
	return &p
}

// countNotifyLogs 统计指定支付单的流水条数
// 参数：
//   - t: 测试对象
//   - s: 被测服务
//   - paymentNo: 支付单号
//   - kind: NotifyLog.Kind
//   - result: NotifyLog.Result，传 0 表示不限
//
// 返回值：
//   - int64: 条数
func countNotifyLogs(t *testing.T, s *PaymentService, paymentNo string, kind, result int32) int64 {
	t.Helper()
	q := s.db.GetDb().Model(&model.NotifyLog{}).Where("payment_no = ? AND kind = ?", paymentNo, kind)
	if result > 0 {
		q = q.Where("result = ?", result)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		t.Fatalf("统计流水失败: %v", err)
	}
	return count
}

// latestNotifyLog 取指定支付单某类别的最新一条流水
// 参数：
//   - t: 测试对象
//   - s: 被测服务
//   - paymentNo: 支付单号
//   - kind: NotifyLog.Kind
//
// 返回值：
//   - *model.NotifyLog: 流水，不存在时 t.Fatalf
func latestNotifyLog(t *testing.T, s *PaymentService, paymentNo string, kind int32) *model.NotifyLog {
	t.Helper()
	var row model.NotifyLog
	if err := s.db.GetDb().Where("payment_no = ? AND kind = ?", paymentNo, kind).
		Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("支付单 %s 缺少 kind=%d 的流水: %v", paymentNo, kind, err)
	}
	return &row
}

// TestSchedulerReconcileRecoversLostCallback T1：回调丢失的已支付订单被主动回查捞回
// 前置：待支付微信单 50 元，假渠道回答已收款且金额一致
// 断言：支付单置 2、确认来源为对账、凭据 reconcile:单号、订单同步 1 次、写 kind=2 成功流水、节流指针推进
func TestSchedulerReconcileRecoversLostCallback(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-T1-RECOVER"
	p := seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 1, Amount: 50.00, CommunityID: 7})
	env.fc.setPaid(no, "TX-T1-RECOVER", 50.00)

	env.sched.reconcileOne(ctx, p, false)

	got := reloadPayment(t, env.s, p.ID)
	if got.Status != 2 {
		t.Fatalf("渠道已收款时支付单应置为已支付(2), got %d", got.Status)
	}
	if got.ConfirmSource != model.ConfirmSourceReconcile {
		t.Errorf("确认来源应为对账补偿(%d), got %d", model.ConfirmSourceReconcile, got.ConfirmSource)
	}
	if got.ConfirmRef != reconcileConfirmRef(no) {
		t.Errorf("来源凭据应为 %s, got %q", reconcileConfirmRef(no), got.ConfirmRef)
	}
	if got.TransactionID != "TX-T1-RECOVER" {
		t.Errorf("渠道交易号应落库, got %q", got.TransactionID)
	}
	if got.PayTime == nil || *got.PayTime == "" {
		t.Error("补同步应写入支付时间")
	}
	if got.ReconcileCount != 1 {
		t.Errorf("回查计数应为 1, got %d", got.ReconcileCount)
	}
	if got.LastReconciledAt == nil {
		t.Error("节流指针 last_reconciled_at 未推进")
	}

	row := latestNotifyLog(t, env.s, no, model.NotifyKindReconcile)
	if row.Result != model.NotifyResultSuccess {
		t.Errorf("对账流水结果应为确认成功, got %d", row.Result)
	}
	if row.Handled != model.NotifyHandled {
		t.Errorf("确认成功不应占用人工待办, handled=%d", row.Handled)
	}
	// 归属快照随流水落库，异常页与详情才能按社区隔离展示
	if row.CommunityID != 7 || row.OrderNo == "" {
		t.Errorf("流水归属快照缺失: community_id=%d order_no=%q", row.CommunityID, row.OrderNo)
	}

	calls := env.stub.orderCalls()
	if len(calls) != 1 {
		t.Fatalf("订单状态同步应恰好调用 1 次, got %d", len(calls))
	}
	if calls[0].Id != p.OrderID || calls[0].Status != 2 {
		t.Errorf("订单同步入参错误: id=%d status=%d", calls[0].Id, calls[0].Status)
	}
}

// TestSchedulerReconcileNotPaidKeepsPending T2：渠道未收款时只推进节流，绝不动状态
// 前置：待支付单，假渠道回答「用户仍在支付中」
// 断言：状态仍为 1、订单未被同步、计数 +1、流水为「渠道未支付」且不进人工待办
func TestSchedulerReconcileNotPaidKeepsPending(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-T2-PAYING"
	p := seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 2, Amount: 30.00})
	env.fc.setPaying(no)

	env.sched.reconcileOne(ctx, p, false)

	got := reloadPayment(t, env.s, p.ID)
	if got.Status != 1 {
		t.Errorf("渠道未收款时支付单状态应保持待支付(1), got %d", got.Status)
	}
	if got.ReconcileCount != 1 || got.LastReconciledAt == nil {
		t.Errorf("未收款仍应推进节流: count=%d last=%v", got.ReconcileCount, got.LastReconciledAt)
	}
	if calls := env.stub.orderCalls(); len(calls) != 0 {
		t.Errorf("未收款不应同步订单状态, got %d 次", len(calls))
	}
	if countNotifyLogs(t, env.s, no, model.NotifyKindReconcile, model.NotifyResultNotPaid) != 1 {
		t.Error("应写一条「渠道未支付」对账流水")
	}
	if countNotifyLogs(t, env.s, no, model.NotifyKindGiveUp, 0) != 0 {
		t.Error("未用满上限前不得写放弃对账流水")
	}
}

// TestSchedulerReconcilePendingScanFilters T3：回查扫描的三道过滤（起扫年龄 / 节流 / 累计上限）
// 前置：4 笔待支付单——刚创建 10 秒、5 分钟内已回查过、回查次数已达上限、可回查
// 断言：只有「可回查」那笔被渠道调用，其余三笔调用次数为 0
//
// 可回查单刻意造在窗口最老端（119 分钟）：扫描按 created_at 升序取一批，
// 本地库里若存在其他人的存量待支付单也不会把它挤出批次
func TestSchedulerReconcilePendingScanFilters(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()

	tooNew := "ZFTEST-T3-TOONEW"
	throttled := "ZFTEST-T3-THROTTLED"
	exhausted := "ZFTEST-T3-EXHAUSTED"
	eligible := "ZFTEST-T3-ELIGIBLE"
	for _, no := range []string{tooNew, throttled, exhausted, eligible} {
		env.fc.setPaying(no)
	}
	seedSchedPayment(t, env.s, schedSeed{No: tooNew, OrderID: schedOrderIDBase + 3, Amount: 10, CreatedAgo: 10 * time.Second})
	seedSchedPayment(t, env.s, schedSeed{No: throttled, OrderID: schedOrderIDBase + 4, Amount: 10, LastRunAgo: time.Minute})
	seedSchedPayment(t, env.s, schedSeed{No: exhausted, OrderID: schedOrderIDBase + 5, Amount: 10, ReconcileCount: 24})
	seedSchedPayment(t, env.s, schedSeed{No: eligible, OrderID: schedOrderIDBase + 6, Amount: 10, CreatedAgo: 119 * time.Minute})

	env.sched.reconcilePending(ctx)

	if got := env.fc.queryCount(eligible); got != 1 {
		t.Errorf("符合条件支付单应被回查 1 次, got %d", got)
	}
	for no, want := range map[string]int{tooNew: 0, throttled: 0, exhausted: 0} {
		if got := env.fc.queryCount(no); got != want {
			t.Errorf("%s 不应被回查（创建未成熟/节流内/超上限）, got %d 次", no, got)
		}
	}
}

// TestSchedulerReconcileGiveUpAfterLimit T4：用满回查上限后写「放弃对账」待办并停止回查
// 前置：待支付单已回查 23 次，假渠道仍回答未收款
// 断言：本轮后计数 24、写一条 kind=6 且待人工处理的流水；再次扫描不再查渠道；重复登记被去重
func TestSchedulerReconcileGiveUpAfterLimit(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-T4-GIVEUP"
	p := seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 7, Amount: 20, ReconcileCount: 23})
	env.fc.setPaying(no)

	env.sched.reconcileOne(ctx, p, false)

	got := reloadPayment(t, env.s, p.ID)
	if got.ReconcileCount != 24 {
		t.Fatalf("回查计数应到上限 24, got %d", got.ReconcileCount)
	}
	row := latestNotifyLog(t, env.s, no, model.NotifyKindGiveUp)
	if row.Handled != model.NotifyPending {
		t.Errorf("放弃对账必须进人工待办, handled=%d", row.Handled)
	}
	if !strings.Contains(row.ErrorMsg, "24") {
		t.Errorf("放弃对账摘要应写明已回查次数, got %q", row.ErrorMsg)
	}

	// 再跑一轮扫描：累计上限由扫描条件把关，已达 24 次的单不得再次打扰渠道，也不得重复登记待办
	env.sched.reconcilePending(ctx)
	if got := env.fc.queryCount(no); got != 1 {
		t.Errorf("放弃对账后扫描不应再回查该单, got %d 次", got)
	}
	if count := countNotifyLogs(t, env.s, no, model.NotifyKindGiveUp, 0); count != 1 {
		t.Errorf("放弃对账流水应去重, got %d 条", count)
	}
}

// TestSchedulerNotExistNeedsTwoRounds T5：渠道「无此交易」需连续两轮确认才关本地单
// 前置：待支付单，假渠道两轮都回答 NOT_EXIST
// 断言：第一轮状态仍为 1 且留痕「首轮」，第二轮才置 5 并写关单流水
func TestSchedulerNotExistNeedsTwoRounds(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-T5-NOTEXIST"
	p := seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 8, Amount: 15})
	env.fc.setNotExist(no)

	env.sched.reconcileOne(ctx, p, false)
	if got := reloadPayment(t, env.s, p.ID); got.Status != 1 {
		t.Fatalf("首轮 NOT_EXIST 不应关单（防渠道瞬时延迟误杀在途支付）, got %d", got.Status)
	}
	first := latestNotifyLog(t, env.s, no, model.NotifyKindReconcile)
	if first.Result != model.NotifyResultNotExist || !strings.Contains(first.ErrorMsg, "首轮") {
		t.Errorf("首轮应记「等待二次确认」: result=%d msg=%q", first.Result, first.ErrorMsg)
	}

	second := reloadPayment(t, env.s, p.ID)
	env.sched.reconcileOne(ctx, second, false)
	if got := reloadPayment(t, env.s, p.ID); got.Status != 5 {
		t.Fatalf("连续两轮 NOT_EXIST 应关闭本地支付单, got %d", got.Status)
	}
	if countNotifyLogs(t, env.s, no, model.NotifyKindChannelClose, model.NotifyResultClosed) != 1 {
		t.Error("本地关单应写一条渠道关单结果流水")
	}
	if got := reloadPayment(t, env.s, p.ID); got.UpdatedAt == "" {
		t.Error("关单应刷新 updated_at（挂账扫描按关闭时间开窗）")
	}
}

// TestSchedulerChannelClosedClosesPendingImmediately 渠道交易已关闭时无需二轮确认
// 前置：待支付单，假渠道回答 CLOSED（渠道已明确不会再有收款）
// 断言：本轮即关闭本地支付单并写关单流水，且不再调渠道关单接口（已关的交易关一次就够）
func TestSchedulerChannelClosedClosesPendingImmediately(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-CLOSED"
	p := seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 24, Amount: 18})
	env.fc.setChannelClosed(no)

	env.sched.reconcileOne(ctx, p, false)

	if got := reloadPayment(t, env.s, p.ID); got.Status != 5 {
		t.Fatalf("渠道已关闭的待支付单应同步关闭, got %d", got.Status)
	}
	if countNotifyLogs(t, env.s, no, model.NotifyKindChannelClose, model.NotifyResultClosed) != 1 {
		t.Error("应写一条本地关单流水")
	}
	if got := env.fc.closeCount(no); got != 0 {
		t.Errorf("渠道侧已关闭的交易不应再调关单, got %d 次", got)
	}
}

// TestSchedulerAmountMismatchNeedsHuman T6：金额不符拒绝确认并转人工
// 前置一：待支付单 50 元，假渠道回答已收款 49.99 元
// 断言一：支付单仍待支付、订单不动、写 result=4 的对账流水且为待处理、登记对账失败记录
// 前置二：同额单走渠道回调路径
// 断言二：回调路径的金额不符同样进人工待办（异常页可见）
func TestSchedulerAmountMismatchNeedsHuman(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()

	no := "ZFTEST-T6-MISMATCH"
	p := seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 9, Amount: 50.00})
	env.fc.setPaid(no, "TX-T6-MISMATCH", 49.99)

	env.sched.reconcileOne(ctx, p, false)

	if got := reloadPayment(t, env.s, p.ID); got.Status != 1 {
		t.Errorf("金额不符必须拒绝确认, got 状态 %d", got.Status)
	}
	if calls := env.stub.orderCalls(); len(calls) != 0 {
		t.Errorf("金额不符不应同步订单, got %d 次", len(calls))
	}
	row := latestNotifyLog(t, env.s, no, model.NotifyKindReconcile)
	if row.Result != model.NotifyResultAmountMismatch {
		t.Errorf("流水结果应为金额不符, got %d", row.Result)
	}
	if row.Handled != model.NotifyPending {
		t.Errorf("金额不符不分来源都须进人工待办, handled=%d", row.Handled)
	}
	var failures int64
	if err := env.s.db.GetDb().Model(&model.SchedulerFailure{}).
		Where("payment_no = ? AND task_type = ? AND status = ?", no, model.TaskReconcilePending, model.FailureStatusPending).
		Count(&failures).Error; err != nil {
		t.Fatalf("统计对账失败记录失败: %v", err)
	}
	if failures != 1 {
		t.Errorf("补同步失败应登记 1 条待重试记录, got %d", failures)
	}

	// 回调路径同口径：result=4 的流水必须是待处理，否则异常页看不到最需要人工的一类
	callbackNo := "ZFTEST-T6-CALLBACK"
	seedSchedPayment(t, env.s, schedSeed{No: callbackNo, OrderID: schedOrderIDBase + 10, Amount: 50.00})
	if err := env.s.handlePaySuccess(ctx, &payCallbackParams{
		PaymentNo: callbackNo, TransactionID: "TX-T6-CB", Amount: 40.00,
		NotifyKind: model.NotifyKindCallback, Channel: 1, OrderID: schedOrderIDBase + 10,
	}); err == nil {
		t.Error("回调金额不一致应报错")
	}
	cbRow := latestNotifyLog(t, env.s, callbackNo, model.NotifyKindCallback)
	if cbRow.Result != model.NotifyResultAmountMismatch || cbRow.Handled != model.NotifyPending {
		t.Errorf("回调金额不符应进人工待办: result=%d handled=%d", cbRow.Result, cbRow.Handled)
	}
	if cbRow.RawBody != "" {
		t.Error("金额不符流水不得把备注性原文塞进 raw_body（此处无人工备注）")
	}
}

// TestSchedulerConcurrentConfirmIsIdempotent T7：回调与主动补偿并发确认只有一笔生效
// 前置：同一待支付单，5 个并发确认请求同时进入
// 断言：支付单流转为已支付且订单状态同步恰好 1 次（条件更新保证幂等）
func TestSchedulerConcurrentConfirmIsIdempotent(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-T7-CONCURRENT"
	p := seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 11, Amount: 66.00})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 前两个走回调口径、其余走对账口径，模拟回调与调度器同时到达
			_ = env.s.handlePaySuccess(ctx, &payCallbackParams{
				PaymentNo:     no,
				TransactionID: "TX-T7",
				Amount:        66.00,
				ConfirmSource: model.ConfirmSourceReconcile,
				ConfirmRef:    reconcileConfirmRef(no),
				NotifyKind:    model.NotifyKindReconcile,
				Channel:       p.PayType,
				OrderID:       p.OrderID,
			})
		}()
	}
	wg.Wait()

	got := reloadPayment(t, env.s, p.ID)
	if got.Status != 2 {
		t.Fatalf("并发确认后支付单应为已支付, got %d", got.Status)
	}
	if calls := env.stub.orderCalls(); len(calls) != 1 {
		t.Errorf("订单状态同步应恰好 1 次（幂等），got %d", len(calls))
	}
	if count := countNotifyLogs(t, env.s, no, model.NotifyKindReconcile, model.NotifyResultIgnored); count != 4 {
		t.Errorf("其余 4 次应记幂等忽略, got %d 条", count)
	}
}

// TestSchedulerReconcileSkipsExpiredWindow 时间窗外的支付单不打扰渠道
// 前置：微信待支付单创建于 200 分钟前（超出 pending_max_minutes=120）
// 断言：零渠道调用、回查计数不变
func TestSchedulerReconcileSkipsExpiredWindow(t *testing.T) {
	env := newSchedEnv(t)
	no := "ZFTEST-WINDOW-EXPIRED"
	p := seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 12, Amount: 20, CreatedAgo: 200 * time.Minute})
	env.fc.setPaid(no, "TX-NEVER", 20.00)

	env.sched.reconcileOne(context.Background(), p, false)

	if got := env.fc.queryCount(no); got != 0 {
		t.Errorf("超出渠道时效后不应回查, got %d 次", got)
	}
	if got := reloadPayment(t, env.s, p.ID); got.ReconcileCount != 0 {
		t.Errorf("被时间窗跳过的支付单不应推进计数, got %d", got.ReconcileCount)
	}
}

// TestSchedulerDetectStuckRecordsAnomaly T9：取消后挂账检出（钱已收、订单已取消，只能人工退款）
// 前置：支付单已关闭(5) 且从未拿到渠道交易号，假渠道回答已收款
// 断言：状态保持 5（对账绝不改订单事实）、写 kind=3/result=9 且待人工处理的流水、不去关渠道交易
func TestSchedulerDetectStuckRecordsAnomaly(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-T9-STUCK"
	seedSchedPayment(t, env.s, schedSeed{
		No: no, OrderID: schedOrderIDBase + 13, Amount: 88.00, Status: 5,
		CreatedAgo: 40 * time.Minute, UpdatedAgo: 10 * time.Minute, CommunityID: 3,
	})
	env.fc.setPaid(no, "TX-T9-STUCK", 88.00)

	env.sched.detectStuck(ctx)

	got := reloadPaymentByNo(t, env.s, no)
	if got.Status != 5 {
		t.Errorf("挂账检出不得改动已关闭支付单状态, got %d", got.Status)
	}
	if got.TransactionID != "" {
		t.Errorf("挂账检出不应把渠道交易号回填成已支付证据, got %q", got.TransactionID)
	}
	row := latestNotifyLog(t, env.s, no, model.NotifyKindStuck)
	if row.Result != model.NotifyResultStuck {
		t.Errorf("挂账流水结果应为资金挂账, got %d", row.Result)
	}
	if row.Handled != model.NotifyPending {
		t.Errorf("挂账必须进人工待办, handled=%d", row.Handled)
	}
	if !strings.Contains(row.ErrorMsg, "TX-T9-STUCK") || !strings.Contains(row.ErrorMsg, "88.00") {
		t.Errorf("挂账摘要要带渠道交易号与金额便于核对退款, got %q", row.ErrorMsg)
	}
	if row.CommunityID != 3 {
		t.Errorf("挂账流水需带社区快照才能按社区隔离, got %d", row.CommunityID)
	}
	if got := env.fc.closeCount(no); got != 0 {
		t.Errorf("已收款的交易不允许关单，不应调用渠道关单, got %d 次", got)
	}
	if got := env.stub.orderCalls(); len(got) != 0 {
		t.Errorf("挂账检出不应改订单状态, got %d 次", len(got))
	}
}

// TestSchedulerDetectStuckDeduplicates T10：挂账待办去重 + 渠道已答「没收到钱」的关闭单不再复核
// 前置一：T9 场景连跑 3 轮
// 断言一：kind=3 且待处理的流水仍只有 1 条
// 前置二：另一笔关闭单已有「渠道未支付」回查记录
// 断言二：本轮不调渠道
func TestSchedulerDetectStuckDeduplicates(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()

	stuckNo := "ZFTEST-T10-STUCK"
	seedSchedPayment(t, env.s, schedSeed{
		No: stuckNo, OrderID: schedOrderIDBase + 14, Amount: 12.00, Status: 5,
		CreatedAgo: 40 * time.Minute, UpdatedAgo: 5 * time.Minute,
	})
	env.fc.setPaid(stuckNo, "TX-T10", 12.00)
	for i := 0; i < 3; i++ {
		env.sched.detectStuck(ctx)
		// 每轮把节流指针拨回窗口外，模拟「过了 5 分钟再来一轮」
		resetThrottle(t, env.s, stuckNo)
	}
	if count := countNotifyLogs(t, env.s, stuckNo, model.NotifyKindStuck, model.NotifyResultStuck); count != 1 {
		t.Errorf("挂账待办应去重, got %d 条", count)
	}

	answeredNo := "ZFTEST-T10-ANSWERED"
	seedSchedPayment(t, env.s, schedSeed{
		No: answeredNo, OrderID: schedOrderIDBase + 15, Amount: 9.00, Status: 5,
		CreatedAgo: 50 * time.Minute, UpdatedAgo: 5 * time.Minute,
	})
	env.s.recordNotifyLog(ctx, &model.NotifyLog{
		Kind: model.NotifyKindReconcile, Result: model.NotifyResultNotPaid,
		PaymentNo: answeredNo, OrderID: schedOrderIDBase + 15,
	})
	env.fc.setPaying(answeredNo)

	env.sched.detectStuck(ctx)

	if got := env.fc.queryCount(answeredNo); got != 0 {
		t.Errorf("渠道已明确回答未收款的关闭单不应再问, got %d 次", got)
	}
}

// TestSchedulerRunOnceDisabledSkipsChannels T12：总开关关闭后回查与挂账全部停摆
// 前置：enabled=false，同时存在待支付单与关闭单
// 断言：runOnce 零渠道调用，支付单节流字段不变（清理流水不受开关影响）
func TestSchedulerRunOnceDisabledSkipsChannels(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	disabled := false
	env.s.cfg.Business.PaymentReconcile.Enabled = &disabled

	pendingNo := "ZFTEST-T12-PENDING"
	stuckNo := "ZFTEST-T12-STUCK"
	seedSchedPayment(t, env.s, schedSeed{No: pendingNo, OrderID: schedOrderIDBase + 16, Amount: 25, CreatedAgo: 119 * time.Minute})
	seedSchedPayment(t, env.s, schedSeed{No: stuckNo, OrderID: schedOrderIDBase + 17, Amount: 25, Status: 5, CreatedAgo: 60 * time.Minute, UpdatedAgo: 5 * time.Minute})
	env.fc.setPaid(pendingNo, "TX-T12-A", 25.00)
	env.fc.setPaid(stuckNo, "TX-T12-B", 25.00)

	env.sched.runOnce(ctx)

	if got := env.fc.totalQueries(); got != 0 {
		t.Errorf("开关关闭后应零渠道调用, got %d 次", got)
	}
	for _, no := range []string{pendingNo, stuckNo} {
		if got := reloadPaymentByNo(t, env.s, no); got.ReconcileCount != 0 {
			t.Errorf("%s 在开关关闭时不应被回查, count=%d", no, got.ReconcileCount)
		}
	}
	if env.s.cfg.ReconcileEnabled() {
		t.Error("显式 false 必须被识别为关闭（*bool 三态，否则回退开关失效）")
	}
	if env.s.cfg.CancelGuardEnabled() {
		t.Error("总开关关闭时取消前置确认应随之失效")
	}
}

// TestClosePaymentsClosesChannelTrade T11：关单前核实到「未收款」→ 顺带关闭渠道交易
// 前置：待支付微信单，假渠道回答 NOTPAY
// 断言：渠道 CloseOrder 被调用、本地仍置 5、写一条关单结果流水
func TestClosePaymentsClosesChannelTrade(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-T11-CLOSE"
	seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 18, Amount: 33.00})
	env.fc.setPaying(no)

	if err := env.s.ClosePayments(ctx, &payment.ClosePaymentsReq{OrderId: schedOrderIDBase + 18}); err != nil {
		t.Fatalf("关闭支付单失败: %v", err)
	}

	if got := env.fc.closeCount(no); got != 1 {
		t.Errorf("渠道未收款时应关闭渠道交易, got %d 次", got)
	}
	got := reloadPaymentByNo(t, env.s, no)
	if got.Status != 5 {
		t.Errorf("本地支付单仍应置为已关闭(5), got %d", got.Status)
	}
	row := latestNotifyLog(t, env.s, no, model.NotifyKindChannelClose)
	if row.Result != model.NotifyResultClosed {
		t.Errorf("关单流水结果错误, got %d", row.Result)
	}
	if row.Handled != model.NotifyHandled {
		t.Errorf("正常关单不占用人工待办, handled=%d", row.Handled)
	}
}

// TestClosePaymentsSkipsUncreatedTrade 渠道从未创建的交易无需关单
// 前置：待支付单，假渠道回答 NOT_EXIST
// 断言：不调用 CloseOrder，本地照常置 5
func TestClosePaymentsSkipsUncreatedTrade(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-T11-NOTEXIST"
	seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 19, Amount: 12.00})
	env.fc.setNotExist(no)

	if err := env.s.ClosePayments(ctx, &payment.ClosePaymentsReq{OrderId: schedOrderIDBase + 19}); err != nil {
		t.Fatalf("关闭支付单失败: %v", err)
	}
	if got := env.fc.closeCount(no); got != 0 {
		t.Errorf("渠道无此交易时不应调关单接口, got %d 次", got)
	}
	if got := reloadPaymentByNo(t, env.s, no); got.Status != 5 {
		t.Errorf("本地仍应关闭支付单, got %d", got.Status)
	}
}

// TestClosePaymentsRecordsStuck T9 的关单侧入口：取消时渠道其实已收款，登记挂账且绝不关单
// 前置：待支付单，假渠道回答已收款
// 断言：写 kind=3 待人工流水、订单未被同步、本地照常置 5（订单已由取消链路流转到 6）
func TestClosePaymentsRecordsStuck(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-T11-STUCK"
	orderID := schedOrderIDBase + 20
	seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: orderID, Amount: 77.00, CommunityID: 5})
	env.fc.setPaid(no, "TX-T11-STUCK", 77.00)

	if err := env.s.ClosePayments(ctx, &payment.ClosePaymentsReq{OrderId: orderID}); err != nil {
		t.Fatalf("关闭支付单失败: %v", err)
	}

	row := latestNotifyLog(t, env.s, no, model.NotifyKindStuck)
	if row.Handled != model.NotifyPending || row.Result != model.NotifyResultStuck {
		t.Errorf("关单侧检出的挂账须进人工待办: result=%d handled=%d", row.Result, row.Handled)
	}
	if got := env.fc.closeCount(no); got != 0 {
		t.Errorf("已收款交易不得调渠道关单, got %d 次", got)
	}
	if got := reloadPaymentByNo(t, env.s, no); got.Status != 5 {
		t.Errorf("本地支付单照常关闭, got %d", got.Status)
	}
	if calls := env.stub.orderCalls(); len(calls) != 0 {
		t.Errorf("关单路径不得反向把订单同步成已支付, got %d 次", len(calls))
	}
}

// TestClosePaymentsDisabledKeepsLegacyBehaviour T12 的关单侧断言：开关关闭时与改造前逐字一致
// 前置：enabled=false，待支付单
// 断言：零渠道调用，本地批量置 5
func TestClosePaymentsDisabledKeepsLegacyBehaviour(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	disabled := false
	env.s.cfg.Business.PaymentReconcile.Enabled = &disabled

	no := "ZFTEST-T12-CLOSE"
	orderID := schedOrderIDBase + 21
	seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: orderID, Amount: 45.00})
	env.fc.setPaying(no)

	if err := env.s.ClosePayments(ctx, &payment.ClosePaymentsReq{OrderId: orderID}); err != nil {
		t.Fatalf("关闭支付单失败: %v", err)
	}
	if got := env.fc.totalQueries(); got != 0 {
		t.Errorf("开关关闭后关单不应问渠道, got %d 次", got)
	}
	if got := env.fc.closeCount(no); got != 0 {
		t.Errorf("开关关闭后不应关渠道交易, got %d 次", got)
	}
	if got := reloadPaymentByNo(t, env.s, no); got.Status != 5 {
		t.Errorf("本地关单行为不受开关影响, got %d", got.Status)
	}
}

// countFailures 统计某支付单的对账失败记录条数
// 参数：
//   - t: 测试对象
//   - s: 被测服务
//   - paymentNo: 支付单号
//   - pendingOnly: true 只统计仍待重试的行
//
// 返回值：
//   - int64: 条数
func countFailures(t *testing.T, s *PaymentService, paymentNo string, pendingOnly bool) int64 {
	t.Helper()
	q := s.db.GetDb().Model(&model.SchedulerFailure{}).Where("payment_no = ?", paymentNo)
	if pendingOnly {
		q = q.Where("status = ?", model.FailureStatusPending)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		t.Fatalf("统计对账失败记录失败: %v", err)
	}
	return count
}

// seedFailureRow 直插一条对账失败记录（用于构造跨锁竞态遗留的同单多行）
// 参数：
//   - t: 测试对象
//   - s: 被测服务
//   - paymentNo: 支付单号
//   - orderID: 订单 ID（清理依据，须落在 testOrderIDBase 之上）
//   - taskType: 任务类型
func seedFailureRow(t *testing.T, s *PaymentService, paymentNo string, orderID int64, taskType string) {
	t.Helper()
	now := time.Now().Format(timeLayout)
	res := s.db.GetDb().Exec(
		`INSERT INTO payment_scheduler_failures
			(payment_no, order_id, task_type, status, pending_slot, error_msg, retry_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'pending', '测试预置', 0, ?, ?)`,
		paymentNo, orderID, taskType, model.FailureStatusPending, now, now)
	if res.Error != nil {
		t.Fatalf("插入对账失败记录 %s 失败: %v", paymentNo, res.Error)
	}
}

// TestSchedulerQueryFailureKeepsPaymentOpen 渠道查询失败不得据此判定资金状态
// 前置：待支付单，假渠道返回错误（网络/配置故障）
// 断言：状态仍为 1、计数推进、登记 1 条待重试记录；
//
//	节流窗口内 retryFailures 绝不绕开节流直查渠道，把节流指针拨回窗口之外后才重试
func TestSchedulerQueryFailureKeepsPaymentOpen(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-QERR"
	p := seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 22, Amount: 50.00})
	env.fc.fail(no, errors.New("渠道网络异常"))

	env.sched.reconcileOne(ctx, p, false)

	got := reloadPayment(t, env.s, p.ID)
	if got.Status != 1 {
		t.Errorf("渠道查询失败绝不能关单, got %d", got.Status)
	}
	if got.ReconcileCount != 1 {
		t.Errorf("失败仍应计入回查次数以推进节流, got %d", got.ReconcileCount)
	}
	if count := countNotifyLogs(t, env.s, no, model.NotifyKindReconcile, 0); count != 0 {
		t.Errorf("查询失败由失败记录表承接，不应重复写对账流水, got %d 条", count)
	}
	if count := countFailures(t, env.s, no, true); count != 1 {
		t.Fatalf("应登记 1 条待重试记录, got %d", count)
	}

	// 刚查过就重试等于把 throttle_minutes 作废，渠道故障时会退化成每分钟打一次
	env.sched.retryFailures(ctx)
	if got := env.fc.queryCount(no); got != 1 {
		t.Errorf("节流窗口内不得重试该单, got %d 次查询", got)
	}

	resetThrottle(t, env.s, no)
	env.sched.retryFailures(ctx)
	if got := env.fc.queryCount(no); got != 2 {
		t.Errorf("节流窗口过后失败记录应被重试, got %d 次查询", got)
	}
}

// TestSchedulerRetryFailuresDedupesRecords 渠道持续不可达时重试队列不得按轮次自我复制
// 前置：待支付单，假渠道恒返回错误，跑三轮「扫描 + 重试」，每轮把节流指针拨回窗口之外
// 断言：每轮渠道只被调用 1 次（扫描占掉本轮额度，重试须受同一节流约束而非翻倍打扰），
//
//	待重试行恒为 1 条（重复失败只刷新原因），累计回查次数等于轮数
func TestSchedulerRetryFailuresDedupesRecords(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-DEDUP"
	p := seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: schedOrderIDBase + 23, Amount: 30.00})
	env.fc.fail(no, errors.New("渠道持续不可达"))

	for round := 1; round <= 3; round++ {
		// 模拟时间推进：把上次回查时刻拨回节流窗口之外，本轮才允许再打渠道
		resetThrottle(t, env.s, no)
		env.sched.reconcilePending(ctx)
		env.sched.retryFailures(ctx)

		if got := env.fc.queryCount(no); got != round {
			// 扫描与重试共用同一节流指针，同一轮里不能让渠道被打两次
			t.Errorf("第 %d 轮渠道调用应为 %d 次, got %d", round, round, got)
		}
		if count := countFailures(t, env.s, no, true); count != 1 {
			t.Errorf("第 %d 轮待重试行应只有 1 条（重复失败改为刷新原因）, got %d", round, count)
		}
	}

	got := reloadPayment(t, env.s, p.ID)
	if got.ReconcileCount != 3 {
		t.Errorf("每轮只推进一次回查计数，三轮应为 3, got %d", got.ReconcileCount)
	}
	if got.Status != 1 {
		t.Errorf("渠道不可达绝不能改状态, got %d", got.Status)
	}
}

// TestSchedulerRetryFailuresQueriesPaymentOncePerRound 同单多行待重试记录本轮只查渠道一次
// 前置：同一支付单遗留 pending 与 stuck 两条待重试行（跨锁竞态），节流窗口已过
// 断言：渠道只被调用 1 次，被跳过的兄弟行不累加 retry_count（不算一次尝试）
func TestSchedulerRetryFailuresQueriesPaymentOncePerRound(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-ONCEROUND"
	orderID := int64(schedOrderIDBase + 24)
	seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: orderID, Amount: 40.00})
	env.fc.fail(no, errors.New("渠道超时"))
	seedFailureRow(t, env.s, no, orderID, model.TaskReconcilePending)
	seedFailureRow(t, env.s, no, orderID, model.TaskReconcileStuck)

	env.sched.retryFailures(ctx)

	if got := env.fc.queryCount(no); got != 1 {
		t.Errorf("同一支付单本轮应只打扰渠道一次, got %d 次", got)
	}
	var rows []model.SchedulerFailure
	if err := env.s.db.GetDb().Where("payment_no = ?", no).Order("id asc").Find(&rows).Error; err != nil {
		t.Fatalf("读取失败记录失败: %v", err)
	}
	if len(rows) != 2 || rows[0].RetryCount != 1 || rows[1].RetryCount != 0 {
		t.Errorf("只有实际尝试的那行应累加重试次数, got %+v", rows)
	}
}

// TestSchedulerRetryFailuresAbandonsExhausted 回查上限内的失败记录不再打扰渠道即置放弃
// 前置：待支付单已用满 24 次回查且仍留有待重试行
// 断言：零渠道调用、该行转放弃（不能永远滞留待重试白占批次额度）
func TestSchedulerRetryFailuresAbandonsExhausted(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	no := "ZFTEST-EXHAUSTED-RETRY"
	orderID := int64(schedOrderIDBase + 25)
	seedSchedPayment(t, env.s, schedSeed{No: no, OrderID: orderID, Amount: 45.00, ReconcileCount: 24})
	seedFailureRow(t, env.s, no, orderID, model.TaskReconcilePending)
	env.fc.fail(no, errors.New("渠道仍不可达"))

	env.sched.retryFailures(ctx)

	if got := env.fc.queryCount(no); got != 0 {
		t.Errorf("用满上限的单不得再打扰渠道, got %d 次", got)
	}
	if count := countFailures(t, env.s, no, true); count != 0 {
		t.Errorf("用满上限的待重试行应转放弃, got %d 条仍在重试态", count)
	}
	var row model.SchedulerFailure
	if err := env.s.db.GetDb().Where("payment_no = ?", no).First(&row).Error; err != nil {
		t.Fatalf("读取失败记录失败: %v", err)
	}
	if row.Status != model.FailureStatusAbandoned {
		t.Errorf("放弃状态应为 %d, got %d", model.FailureStatusAbandoned, row.Status)
	}
	if row.PendingSlot != nil {
		t.Errorf("转终态必须清空 pending_slot 以便后续新失败重新登记, got %q", *row.PendingSlot)
	}
}

// 说明：唯一索引 uk_failures_pending 的去重能力由 TestSchedulerRetryFailuresDedupesRecords
// 间接钉住（三轮重复失败仍只有 1 条待重试行）；转终态清空占位后允许再次登记，
// 由 TestSchedulerRetryFailuresAbandonsExhausted 的 pending_slot 断言把关。

// TestWithinReconcileWindow 回查时间窗（纯函数）：微信按配置上限，支付宝受订单超时约束
func TestWithinReconcileWindow(t *testing.T) {
	newCfg := func(orderTimeout int) *config.Config {
		cfg := &config.Config{}
		cfg.Business.OrderTimeout = orderTimeout
		return cfg
	}
	cases := []struct {
		name    string
		cfg     *config.Config
		payType int32
		age     time.Duration
		want    bool
	}{
		{"微信窗口内", newCfg(1800), 1, 119 * time.Minute, true},
		{"微信窗口外", newCfg(1800), 1, 121 * time.Minute, false},
		{"支付宝取订单超时为小", newCfg(1800), 2, 31 * time.Minute, false},
		{"支付宝窗口内", newCfg(1800), 2, 29 * time.Minute, true},
		{"支付宝订单超时更长时取配置上限", newCfg(7200), 2, 119 * time.Minute, true},
		{"现金单无渠道可查也在窗口内", newCfg(1800), 4, 10 * time.Minute, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &model.Payment{PayType: c.payType, CreatedAt: time.Now().Add(-c.age).Format(timeLayout)}
			if got := withinReconcileWindow(c.cfg, p); got != c.want {
				t.Errorf("pay_type=%d age=%v 窗口判定 = %v, want %v", c.payType, c.age, got, c.want)
			}
		})
	}
	// 脏数据（时间串解析不了）保守放行，由节流与上限兜底，不能因此永久漏掉对账
	broken := &model.Payment{PayType: 1, CreatedAt: "not-a-time"}
	if !withinReconcileWindow(newCfg(1800), broken) {
		t.Error("created_at 无法解析时应保守放行")
	}
	// 真实读回形态：DSN 带 parseTime=True 时从库里读到的 created_at 是 RFC3339，
	// 若只认「2006-01-02 15:04:05」，时间窗会恒判「解析失败→放行」而彻底失效
	rfc := &model.Payment{PayType: 1, CreatedAt: time.Now().Add(-200 * time.Minute).Format(time.RFC3339)}
	if withinReconcileWindow(newCfg(1800), rfc) {
		t.Error("RFC3339 形态的 created_at 同样必须受时间窗约束")
	}
}

// TestParseDBTime 库内时间字符串解析：本项目读回写法（服务自写 + 驱动读回）都要覆盖
func TestParseDBTime(t *testing.T) {
	cases := []struct {
		name  string
		value string
		ok    bool
	}{
		{"服务写入的 datetime", "2026-10-21 14:19:47", true},
		{"驱动读回的 RFC3339", "2026-10-21T14:19:47+08:00", true},
		{"驱动读回的带纳秒形态", "2026-10-21T14:19:47.123456+08:00", true},
		{"仅日期", "2026-10-21", true},
		{"脏数据", "渠道无时间", false},
		{"空串", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseDBTime(c.value)
			if (err == nil) != c.ok {
				t.Errorf("parseDBTime(%q) 成功 = %v, want %v (err=%v)", c.value, err == nil, c.ok, err)
			}
		})
	}
	// 两种形态解析到同一时刻，时间窗判定才不会因来源不同而漂移
	wall := time.Date(2026, 10, 21, 14, 19, 47, 0, time.Local)
	a, errA := parseDBTime(wall.Format(timeLayout))
	b, errB := parseDBTime(wall.Format(time.RFC3339))
	if errA != nil || errB != nil || !a.Equal(b) {
		t.Errorf("两种写法应解析为同一时刻: %v %v / %v %v", a, errA, b, errB)
	}
}

// TestNeedsHandlingMatrix 异常页待办判定矩阵（纯函数）
// 要点：金额不符与挂账不论来源都必须人工；幂等忽略与「渠道明确未付」属于正常轨迹
func TestNeedsHandlingMatrix(t *testing.T) {
	cases := []struct {
		name   string
		kind   int32
		result int32
		want   bool
	}{
		{"回调验签失败", model.NotifyKindCallback, model.NotifyResultVerifyFailed, true},
		{"回调金额不符", model.NotifyKindCallback, model.NotifyResultAmountMismatch, true},
		{"回调处理异常", model.NotifyKindCallback, model.NotifyResultError, true},
		{"回调成功", model.NotifyKindCallback, model.NotifyResultSuccess, false},
		{"重复通知", model.NotifyKindCallback, model.NotifyResultIgnored, false},
		{"对账金额不符", model.NotifyKindReconcile, model.NotifyResultAmountMismatch, true},
		{"对账未收款", model.NotifyKindReconcile, model.NotifyResultNotPaid, false},
		{"人工同步金额不符", model.NotifyKindManualSync, model.NotifyResultAmountMismatch, true},
		{"挂账检出", model.NotifyKindStuck, model.NotifyResultStuck, true},
		{"放弃对账", model.NotifyKindGiveUp, model.NotifyResultError, true},
		{"渠道关单成功", model.NotifyKindChannelClose, model.NotifyResultClosed, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := model.NeedsHandling(c.kind, c.result); got != c.want {
				t.Errorf("NeedsHandling(kind=%d, result=%d) = %v, want %v", c.kind, c.result, got, c.want)
			}
		})
	}
}

// TestNotifyEntryFromSnapshot 流水归属快照取自支付单（纯函数）
// 社区范围漏填会让这条异常只对超管可见，社区管理员永远看不到自己社区的问题
func TestNotifyEntryFromSnapshot(t *testing.T) {
	p := &model.Payment{
		PaymentNo: "ZF-SNAPSHOT", OrderID: 123, OrderNo: "NO123",
		CommunityID: 9, PayType: 2,
	}
	entry := notifyEntryFrom(p, model.NotifyKindStuck, model.NotifyResultStuck)
	if entry.PaymentNo != p.PaymentNo || entry.OrderID != p.OrderID ||
		entry.OrderNo != p.OrderNo || entry.CommunityID != p.CommunityID || entry.Channel != p.PayType {
		t.Errorf("流水未继承支付单归属快照: %+v", entry)
	}

	// 验签失败等连支付单都解析不出的场景：全部留零值，只进超管全量队列
	empty := notifyEntryFrom(nil, model.NotifyKindCallback, model.NotifyResultVerifyFailed)
	if empty.PaymentNo != "" || empty.CommunityID != 0 || empty.OrderID != 0 {
		t.Errorf("无支付单时流水应为零值: %+v", empty)
	}
}

// TestReconcileConfirmRef 对账凭据格式（异常页据此展示「对账补同步」并回溯批次）
func TestReconcileConfirmRef(t *testing.T) {
	if got := reconcileConfirmRef("ZF123"); got != "reconcile:ZF123" {
		t.Errorf("对账凭据格式错误, got %q", got)
	}
}

// TestSchedulerCleanupNotifyLogs 流水按保留天数清理，测试数据不会被误删
// 前置：先清掉「每日一次」的清理锁（同一进程内多个用例都要跑清理），再写入一条 40 天前的旧流水与一条新流水
// 断言：旧的被删、新的保留
func TestSchedulerCleanupNotifyLogs(t *testing.T) {
	env := newSchedEnv(t)
	ctx := context.Background()
	// 清理锁按日期加 25 小时 TTL 且刻意不释放（集群级每日一次的实现手段），
	// 用例必须显式删除才能再次触发清理；真实运行由当天第一个实例执行一次
	_ = env.rdb.Del(ctx, fmt.Sprintf("scheduler:payment:notify:cleanup:%s", time.Now().Format("2006-01-02"))).Err()

	oldNo := "ZFTEST-CLEAN-OLD"
	newNo := "ZFTEST-CLEAN-NEW"
	orderID := schedOrderIDBase + 23
	seedAgedNotifyLog(t, env.s, oldNo, orderID, time.Now().AddDate(0, 0, -40))
	seedAgedNotifyLog(t, env.s, newNo, orderID, time.Now())

	env.sched.cleanupNotifyLogs(ctx)

	if count := countNotifyLogs(t, env.s, oldNo, model.NotifyKindCallback, 0); count != 0 {
		t.Errorf("超过 30 天保留期的流水应被清理, got %d 条", count)
	}
	if count := countNotifyLogs(t, env.s, newNo, model.NotifyKindCallback, 0); count != 1 {
		t.Errorf("保留期内流水不得被清理, got %d 条", count)
	}
}

// TestSchedulerNilRedisSkipsStart Redis 缺失时调度器不启动而非 panic
// 断言：Start 立即返回（main.go 未配 Redis 时不能让支付服务崩在启动阶段）
func TestSchedulerNilRedisSkipsStart(t *testing.T) {
	sched := NewScheduler(&PaymentService{cfg: &config.Config{}}, nil)
	done := make(chan struct{})
	go func() {
		sched.Start(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Redis 为 nil 时 Start 应立即返回")
	}
}

// resetThrottle 把支付单的节流指针拨回窗口之外，模拟「过了 5 分钟再来一轮」
// 参数：
//   - t: 测试对象
//   - s: 被测服务
//   - paymentNo: 支付单号
func resetThrottle(t *testing.T, s *PaymentService, paymentNo string) {
	t.Helper()
	stale := time.Now().Add(-30 * time.Minute).Format(timeLayout)
	if err := s.db.GetDb().Model(&model.Payment{}).
		Where("payment_no = ?", paymentNo).
		Update("last_reconciled_at", stale).Error; err != nil {
		t.Fatalf("重置节流指针失败: %v", err)
	}
}

// reloadPaymentByNo 按支付单号重新读出支付单
// 参数：
//   - t: 测试对象
//   - s: 被测服务
//   - paymentNo: 支付单号
//
// 返回值：
//   - *model.Payment: 最新行
func reloadPaymentByNo(t *testing.T, s *PaymentService, paymentNo string) *model.Payment {
	t.Helper()
	var p model.Payment
	if err := s.db.GetDb().Where("payment_no = ?", paymentNo).First(&p).Error; err != nil {
		t.Fatalf("读取支付单 %s 失败: %v", paymentNo, err)
	}
	return &p
}

// TestReconcileOutcomeCount 单笔结论计入本轮统计的口径
// 落库判定（shouldRecordReconcile）依赖各计数，计数错位会导致：
//   - 有动作的轮次被误判空转（捞回/挂账不落库，排查断层）；
//   - 空转轮次被误判有动作（每分钟一条记录挤占 debug_logs 全表上限）
func TestReconcileOutcomeCount(t *testing.T) {
	cases := []struct {
		outcome reconcileOutcome
		check   func(s *reconcileStat) bool // 返回 true 表示该计数已 +1
	}{
		{outcomeSkipped, func(s *reconcileStat) bool {
			return s.rescued == 0 && s.stuck == 0 && s.closed == 0 && s.observed == 0 && s.failed == 0
		}},
		{outcomeObserved, func(s *reconcileStat) bool { return s.observed == 1 }},
		{outcomeRescued, func(s *reconcileStat) bool { return s.rescued == 1 }},
		{outcomeStuck, func(s *reconcileStat) bool { return s.stuck == 1 }},
		{outcomeClosed, func(s *reconcileStat) bool { return s.closed == 1 }},
		{outcomeFailed, func(s *reconcileStat) bool { return s.failed == 1 }},
	}
	for _, c := range cases {
		var stat reconcileStat
		c.outcome.count(&stat)
		if !c.check(&stat) {
			t.Errorf("结论 %s 计入错误: %+v", c.outcome, stat)
		}
	}
}

// TestShouldRecordReconcile 空转轮次不落库的判定矩阵
// 组件 StartProcess 创建即落库，判定必须在跑完子任务后做（见 runOnce 注释）
func TestShouldRecordReconcile(t *testing.T) {
	cases := []struct {
		name    string
		stat    reconcileStat
		cleaned int64
		want    bool
	}{
		{"全零空转不记录", reconcileStat{}, 0, false},
		{"仅节流推进不记录", reconcileStat{observed: 50}, 0, false},
		{"捞回一笔即记录", reconcileStat{rescued: 1}, 0, true},
		{"挂账检出即记录", reconcileStat{stuck: 1}, 0, true},
		{"本地关单即记录", reconcileStat{closed: 1}, 0, true},
		{"失败即记录", reconcileStat{failed: 1}, 0, true},
		{"panic 即记录", reconcileStat{panics: 1}, 0, true},
		{"清理到流水即记录", reconcileStat{}, 12, true},
		{"清理出错即记录", reconcileStat{}, -1, true},
	}
	for _, c := range cases {
		if got := shouldRecordReconcile(c.stat, c.cleaned); got != c.want {
			t.Errorf("%s: shouldRecordReconcile(%+v, %d) = %v, 期望 %v", c.name, c.stat, c.cleaned, got, c.want)
		}
	}
}

// TestReconcileStatMerge 子任务统计汇总不丢计数
func TestReconcileStatMerge(t *testing.T) {
	var total reconcileStat
	total.merge(reconcileStat{rescued: 1, observed: 3})
	total.merge(reconcileStat{stuck: 2, failed: 1, panics: 1})
	total.merge(reconcileStat{closed: 1})
	want := reconcileStat{rescued: 1, stuck: 2, closed: 1, observed: 3, failed: 1, panics: 1}
	if total != want {
		t.Errorf("merge 结果 = %+v, 期望 %+v", total, want)
	}
}

// TestRunReconcileTaskPropagatesStat 子任务统计一定回传给调用方
// panic 分支必须覆盖：结果若是匿名返回值，panic 越过本体 return 后结果槽归零，
// panics 与 panic 前已完成的动作计数调用方都读不到，异常轮次会被当成空转不落库
func TestRunReconcileTaskPropagatesStat(t *testing.T) {
	t.Run("正常返回计数透传", func(t *testing.T) {
		stat := runReconcileTask("normalTask", func(stat *reconcileStat) {
			stat.rescued++
			stat.observed += 3
		})
		if stat.rescued != 1 || stat.observed != 3 || stat.panics != 0 {
			t.Errorf("正常统计丢失: %+v", stat)
		}
	})

	t.Run("panic 计数与已写入计数都回传", func(t *testing.T) {
		stat := runReconcileTask("panicTask", func(stat *reconcileStat) {
			stat.closed++ // panic 前已落地的动作
			panic(errors.New("模拟任务内异常"))
		})
		if stat.panics != 1 {
			t.Errorf("panic 应计入 stat.panics，实际 %+v", stat)
		}
		if stat.closed != 1 {
			t.Errorf("panic 前的动作计数不应丢失，实际 %+v", stat)
		}
		// 回传后必须满足落库判定，否则异常轮次在 debugger 里彻底无痕
		if !shouldRecordReconcile(stat, 0) {
			t.Errorf("panic 轮次应判定为需落库，实际统计 %+v", stat)
		}
	})
}
