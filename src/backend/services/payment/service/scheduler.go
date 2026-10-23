package service

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/debuglog"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

// Scheduler 支付对账定时任务
// 承担三件事，全部以渠道为事实来源，且都不改动订单状态：
//  1. P0-A 回查待支付单：捞回「用户已付款但回调丢失/验签失败」的支付单；
//  2. P0-C 检出挂账：扫已关闭却无交易号的支付单，问渠道确认是否已收款，产出人工退款待办；
//  3. 流水清理：payment_notify_logs 按保留天数删除，防止表无限增长。
//
// 与 order 调度器的差别在于本任务只读渠道、只在 handlePaySuccess 这一唯一收敛点写状态，
// 因此不需要「处理中」抢占态：payments 的 status 1→2 条件更新本身就是幂等保证。
type Scheduler struct {
	payment     *PaymentService
	redisClient *redis.Client
}

// reconcileOutcome 单笔支付单对账的结论（用于本轮统计与 debugger 摘要）
type reconcileOutcome string

const (
	// outcomeSkipped 不在渠道时效窗口内，未问渠道
	outcomeSkipped reconcileOutcome = "skipped"
	// outcomeObserved 已问渠道、结论无需本地变更（在途/渠道已关/首轮无此交易留痕）
	outcomeObserved reconcileOutcome = "observed"
	// outcomeRescued 回调丢失已捞回（补同步成功）
	outcomeRescued reconcileOutcome = "rescued"
	// outcomeStuck 挂账检出（已关闭但渠道已收款，产出人工退款待办）
	outcomeStuck reconcileOutcome = "stuck"
	// outcomeClosed 渠道确认不会收款，本地支付单关闭
	outcomeClosed reconcileOutcome = "closed"
	// outcomeFailed 渠道查询或补同步失败（下轮重试）
	outcomeFailed reconcileOutcome = "failed"
)

// reconcileStat 单轮对账统计：debugger 进程记录的落库判定与摘要字段的唯一依据
// panics 统计子任务 panic 次数（recoverTask 恢复后计入），使异常轮次不满足空转条件
type reconcileStat struct {
	rescued      int
	stuck        int
	closed       int
	observed     int
	failed       int
	panics       int
	refundOK     int // 退款查单确认成功并完成落库（P0-2）
	refundFailed int // 退款查单确认渠道终态失败，流水转待重试（P0-2）
}

// count 把单笔结论计入本轮统计
// 参数：
//   - stat: 本轮统计（指针写入）
func (o reconcileOutcome) count(stat *reconcileStat) {
	switch o {
	case outcomeRescued:
		stat.rescued++
	case outcomeStuck:
		stat.stuck++
	case outcomeClosed:
		stat.closed++
	case outcomeFailed:
		stat.failed++
	case outcomeSkipped:
		// 窗口外未问渠道，不计入
	default:
		stat.observed++
	}
}

// hasAction 本轮是否发生了「改变本地状态或需要人工关注」的事件
// 纯节流推进（observed）不算动作：60s 一轮若只要有待支付单就落库，
// 会把 debug_logs 全表上限的 HTTP 请求记录迅速挤掉，故空转轮次不落库
// 返回值：
//   - bool: true 表示有实质动作
func (s reconcileStat) hasAction() bool {
	return s.rescued+s.stuck+s.closed+s.failed > 0
}

// merge 把另一份统计并入本轮（子任务结果汇总）
// 参数：
//   - other: 子任务统计
func (s *reconcileStat) merge(other reconcileStat) {
	s.rescued += other.rescued
	s.stuck += other.stuck
	s.closed += other.closed
	s.observed += other.observed
	s.failed += other.failed
	s.panics += other.panics
	s.refundOK += other.refundOK
	s.refundFailed += other.refundFailed
}

// shouldRecordReconcile 判定本轮对账是否值得写入 debugger 进程记录
// 组件 StartProcess 创建即落库，故必须在跑完子任务后判定：
// 纯节流推进、无清理、无 panic 的轮次只打标准日志，不落调试库
// 参数：
//   - stat: 本轮统计
//   - cleaned: 流水清理删除行数；-1 表示清理执行出错
//
// 返回值：
//   - bool: true 表示应记录
func shouldRecordReconcile(stat reconcileStat, cleaned int64) bool {
	return stat.hasAction() || stat.panics > 0 || cleaned != 0
}

// NewScheduler 创建支付对账调度器
// 参数：
//   - payment: 支付业务服务（携带 DB 与配置）
//   - redisClient: Redis 客户端，用于分布式锁；nil 时任务不启动
//
// 返回值：
//   - *Scheduler: 调度器实例
func NewScheduler(payment *PaymentService, redisClient *redis.Client) *Scheduler {
	return &Scheduler{payment: payment, redisClient: redisClient}
}

// Start 启动对账定时任务
// 总开关（business.payment_reconcile.enabled）关闭时只跑流水清理，零渠道调用
// 参数：
//   - ctx: 用于优雅退出的上下文
func (s *Scheduler) Start(ctx context.Context) {
	if s.redisClient == nil {
		log.Println("[payment-scheduler] Redis 未就绪，对账任务不启动")
		return
	}
	interval := time.Duration(config.ReconcileInt(s.payment.cfg.Business.PaymentReconcile.IntervalSeconds, 60)) * time.Second
	if s.payment.cfg.ReconcileEnabled() {
		log.Printf("[payment-scheduler] 支付对账任务已启动，轮询间隔 %v", interval)
	} else {
		log.Printf("[payment-scheduler] 对账总开关已关闭，仅保留流水清理")
	}

	s.runOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("[payment-scheduler] 支付对账任务已停止")
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

// runOnce 单轮调度：先重试历史失败，再跑两条扫描，最后做每日清理
// 进程级记录口径（debugger）：先跑完子任务再判定是否记录——组件 StartProcess
// 创建即落库，60s 一轮全量记录会把 debug_logs 全表上限的 HTTP 请求记录迅速挤掉，
// 故空转轮次（无本地状态变更、无失败、无清理、无 panic）只打标准日志。
// 有动作的轮次落一条摘要记录（含各结论计数），失败/异常轮次以错误级标记
// 参数：
//   - ctx: 上下文
func (s *Scheduler) runOnce(ctx context.Context) {
	start := time.Now()

	var stat reconcileStat
	if s.payment.cfg.ReconcileEnabled() {
		stat.merge(s.retryFailures(ctx))
		stat.merge(s.reconcilePending(ctx))
		stat.merge(s.detectStuck(ctx))
		stat.merge(s.detectPaidMismatch(ctx))
		stat.merge(s.reconcileRefunds(ctx))
	}
	cleaned := s.cleanupNotifyLogs(ctx)
	if !shouldRecordReconcile(stat, cleaned) {
		return
	}

	rec := debuglog.Start("支付对账调度", "payment_reconcile")
	if rec == nil {
		return
	}
	fields := map[string]interface{}{
		"rescued":      stat.rescued,
		"stuck":        stat.stuck,
		"closed":       stat.closed,
		"observed":     stat.observed,
		"failed":        stat.failed,
		"panics":        stat.panics,
		"refund_ok":     stat.refundOK,
		"refund_failed": stat.refundFailed,
		"cleaned_logs":  cleaned,
		"duration_ms":  time.Since(start).Milliseconds(),
	}
	if stat.failed > 0 || stat.panics > 0 || cleaned < 0 {
		rec.Error("本轮对账存在失败", fields)
		_ = rec.EndProcess(debuglog.StatusFailed)
		return
	}
	rec.Info("本轮对账摘要", fields)
	_ = rec.EndProcess(debuglog.StatusCompleted)
}

// reconcilePending 回查待支付单（P0-A），带任务级 panic 兜底
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - reconcileStat: 本轮统计（落库判定依据）
func (s *Scheduler) reconcilePending(ctx context.Context) reconcileStat {
	return runReconcileTask("reconcilePending", func(stat *reconcileStat) { s.scanPending(ctx, stat) })
}

// scanPending 回查待支付单的任务本体
// 扫描条件与单条处理都受「起扫年龄 / 渠道时效 / 节流间隔 / 累计上限」四道约束，
// 保证正常回调有足够时间先行完成，异常单又不会每分钟打扰渠道一次
// 参数：
//   - ctx: 上下文
//   - stat: 本轮统计（写入指针，由 runReconcileTask 回传）
func (s *Scheduler) scanPending(ctx context.Context, stat *reconcileStat) {
	lockKey := "scheduler:payment:reconcile:pending:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	r := s.payment.cfg.Business.PaymentReconcile
	createdBefore := time.Now().Add(-time.Duration(config.ReconcileInt(r.MinAgeSeconds, 60)) * time.Second).Format(timeLayout)
	// 上界取全局最大时效，逐单再按渠道收敛（支付宝单实际受下单时的 timeout_express 约束）
	createdAfter := time.Now().Add(-time.Duration(config.ReconcileInt(r.PendingMaxMinutes, 120)) * time.Minute).Format(timeLayout)
	throttleBefore := time.Now().Add(-time.Duration(config.ReconcileInt(r.ThrottleMinutes, 5)) * time.Minute).Format(timeLayout)

	var rows []model.Payment
	if err := s.payment.db.GetDb().WithContext(ctx).
		Where("status = 1 AND pay_type IN ? AND created_at < ? AND created_at >= ?", []int32{1, 2, 5, 6}, createdBefore, createdAfter).
		Where("reconcile_count < ?", config.ReconcileInt(r.MaxReconcileCount, 24)).
		Where("(last_reconciled_at IS NULL OR last_reconciled_at < ?)", throttleBefore).
		Order("created_at asc").
		Limit(config.ReconcileInt(r.BatchSize, 100)).
		Find(&rows).Error; err != nil {
		log.Printf("[payment-scheduler] 扫描待支付单失败: %v", err)
		stat.failed++ // 扫描失败计入本轮失败，使异常轮次落入 debugger 记录
		return
	}
	for i := range rows {
		s.reconcileOne(ctx, &rows[i], false).count(stat)
	}
}

// detectStuck 检出资金挂账（P0-C），带任务级 panic 兜底
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - reconcileStat: 本轮统计（落库判定依据）
func (s *Scheduler) detectStuck(ctx context.Context) reconcileStat {
	return runReconcileTask("detectStuck", func(stat *reconcileStat) { s.scanStuck(ctx, stat) })
}

// detectPaidMismatch 检出「支付单已确认而订单未流转」的不一致（P0-3），带任务级 panic 兜底
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - reconcileStat: 本轮统计（落库判定依据）
func (s *Scheduler) detectPaidMismatch(ctx context.Context) reconcileStat {
	return runReconcileTask("detectPaidMismatch", func(stat *reconcileStat) { s.scanPaidMismatch(ctx, stat) })
}

// scanPaidMismatch 支付单/订单不一致检出的任务本体
// 成因：handlePaySuccess 二阶段三次 RPC 都失败（order-service 抖动）时，支付单已是 status=2
// 而订单停留在待支付甚至被取消——此前的两条扫描（status=1 回查、status=5 挂账）都看不见它，
// 取消护栏的 orderID 分支也只查 status=1，资金不一致会一直悬着。
// 本任务逐笔核对订单实态并修复：停留待支付族(1/100) → 补同步；已取消(6) → 挂账待办。
// 扫描窗口与挂账检出一致（updated_at 落在渠道时效内），节流只推进 last_reconciled_at——
// 不走渠道，不能消耗 reconcile_count 的渠道回查预算
// 参数：
//   - ctx: 上下文
//   - stat: 本轮统计（写入指针，由 runReconcileTask 回传）
func (s *Scheduler) scanPaidMismatch(ctx context.Context, stat *reconcileStat) {
	lockKey := "scheduler:payment:reconcile:mismatch:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	r := s.payment.cfg.Business.PaymentReconcile
	updatedAfter := time.Now().Add(-time.Duration(config.ReconcileInt(r.PendingMaxMinutes, 120)) * time.Minute).Format(timeLayout)
	throttleBefore := time.Now().Add(-time.Duration(config.ReconcileInt(r.ThrottleMinutes, 5)) * time.Minute).Format(timeLayout)

	var rows []model.Payment
	if err := s.payment.db.GetDb().WithContext(ctx).
		Where("status = ? AND pay_type IN ? AND updated_at >= ?", model.PaymentStatusSuccess, []int32{1, 2, 5, 6}, updatedAfter).
		Where("(last_reconciled_at IS NULL OR last_reconciled_at < ?)", throttleBefore).
		Order("updated_at asc").
		Limit(config.ReconcileInt(r.BatchSize, 100)).
		Find(&rows).Error; err != nil {
		log.Printf("[payment-scheduler] 扫描已确认支付单失败: %v", err)
		stat.failed++ // 扫描失败计入本轮失败，使异常轮次落入 debugger 记录
		return
	}
	for i := range rows {
		s.repairPaidMismatch(ctx, &rows[i]).count(stat)
	}
}

// repairPaidMismatch 核对并修复单笔「已确认支付单 vs 订单状态」的不一致
// 参数：
//   - ctx: 上下文
//   - p: 已确认支付（status=2）的支付单
//
// 返回值：
//   - reconcileOutcome: rescued=已补同步 / stuck=订单已取消转挂账待办 / failed=查询或补同步失败 / observed=一致无需动作
func (s *Scheduler) repairPaidMismatch(ctx context.Context, p *model.Payment) reconcileOutcome {
	// 只推进节流指针：本任务不走渠道，reconcile_count 是渠道查询语义，混用会提前耗尽回查预算
	s.touchReconciled(ctx, p)
	if grpcclient.OrderClient == nil {
		return outcomeObserved
	}
	detail, err := grpcclient.OrderClient.GetOrder(ctx, &order.GetOrderReq{Id: p.OrderID})
	if err != nil || detail == nil || detail.Order == nil {
		log.Printf("[payment-scheduler] 核对订单状态失败 payment_no=%s order_id=%d: %v", p.PaymentNo, p.OrderID, err)
		return outcomeFailed
	}
	return s.payment.repairPaidOrderSync(ctx, p, detail.Order.Status)
}

// scanStuck 挂账检出的任务本体
// 已关闭（status=5）且从未拿到渠道交易号的支付单，若渠道侧其实收了钱，
// 订单早已取消、库存与优惠券已回补，系统无法自行挽回，只能产出人工退款待办
// 参数：
//   - ctx: 上下文
//   - stat: 本轮统计（写入指针，由 runReconcileTask 回传）
func (s *Scheduler) scanStuck(ctx context.Context, stat *reconcileStat) {
	lockKey := "scheduler:payment:reconcile:stuck:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	r := s.payment.cfg.Business.PaymentReconcile
	updatedAfter := time.Now().Add(-time.Duration(config.ReconcileInt(r.PendingMaxMinutes, 120)) * time.Minute).Format(timeLayout)
	throttleBefore := time.Now().Add(-time.Duration(config.ReconcileInt(r.ThrottleMinutes, 5)) * time.Minute).Format(timeLayout)

	var rows []model.Payment
	if err := s.payment.db.GetDb().WithContext(ctx).
		Where("status = ? AND transaction_id = '' AND pay_type IN ? AND updated_at >= ?", model.PaymentStatusClosed, []int32{1, 2, 5, 6}, updatedAfter).
		Where("reconcile_count < ?", config.ReconcileInt(r.MaxReconcileCount, 24)).
		Where("(last_reconciled_at IS NULL OR last_reconciled_at < ?)", throttleBefore).
		Order("updated_at asc").
		Limit(config.ReconcileInt(r.BatchSize, 100)).
		Find(&rows).Error; err != nil {
		log.Printf("[payment-scheduler] 扫描已关闭支付单失败: %v", err)
		stat.failed++ // 扫描失败计入本轮失败，使异常轮次落入 debugger 记录
		return
	}
	for i := range rows {
		// 渠道已明确回答过「没收到钱」的关闭单，事实不会改变，不再重复打扰渠道
		if s.payment.hasNoPaymentConfirmed(ctx, rows[i].PaymentNo) {
			continue
		}
		s.reconcileOne(ctx, &rows[i], true).count(stat)
	}
}

// reconcileOne 对单笔支付单发起渠道核实并落地结论
// 参数：
//   - ctx: 上下文
//   - p: 支付单
//   - closed: 该单是否已关闭（true 时只会产出挂账流水，不会尝试改状态）
//
// 返回值：
//   - reconcileOutcome: 单笔结论，计入本轮统计（见 reconcileStat）
func (s *Scheduler) reconcileOne(ctx context.Context, p *model.Payment, closed bool) reconcileOutcome {
	if !withinReconcileWindow(s.payment.cfg, p) {
		return outcomeSkipped
	}
	res, err := s.payment.queryChannelOrder(ctx, p)
	if err != nil || res == nil {
		// 渠道不可达：计入回查次数但不动状态，下轮再来
		s.markReconciled(ctx, p)
		s.payment.recordFailure(ctx, p, reconcileTaskType(closed), fmt.Sprintf("渠道查询失败: %v", err))
		return outcomeFailed
	}

	switch res.State {
	case ChannelStatePaid:
		if closed {
			s.markReconciled(ctx, p)
			s.payment.recordStuckPayment(ctx, p, res)
			return outcomeStuck
		}
		err := s.payment.handlePaySuccess(ctx, &payCallbackParams{
			PaymentNo:     p.PaymentNo,
			TransactionID: res.TransactionID,
			Amount:        res.Amount,
			ConfirmSource: model.ConfirmSourceReconcile,
			ConfirmRef:    reconcileConfirmRef(p.PaymentNo),
			NotifyKind:    model.NotifyKindReconcile,
			Channel:       p.PayType,
			OrderID:       p.OrderID,
		})
		s.markReconciled(ctx, p)
		if err != nil {
			log.Printf("[payment-scheduler] 补同步失败 payment_no=%s: %v", p.PaymentNo, err)
			s.payment.recordFailure(ctx, p, reconcileTaskType(closed), err.Error())
			return outcomeFailed
		}
		log.Printf("[payment-scheduler] 回调丢失已捞回 payment_no=%s 渠道状态=%s", p.PaymentNo, res.Raw)
		return outcomeRescued

	case ChannelStateNotExist:
		s.markReconciled(ctx, p)
		// 两轮确认：首轮只留痕，避免渠道瞬时数据延迟把一笔在途支付判死
		if closed {
			s.payment.recordReconcileLog(ctx, p, model.NotifyResultNotExist, "渠道无此交易")
			return outcomeObserved
		}
		if s.payment.hasReconcileNotExist(ctx, p.PaymentNo) {
			s.closePaymentLocally(ctx, p, "渠道连续两轮无此交易，关闭本地支付单")
			return outcomeClosed
		}
		s.payment.recordReconcileLog(ctx, p, model.NotifyResultNotExist, "渠道无此交易（首轮，等待二次确认）")
		return outcomeObserved

	case ChannelStateClosed:
		s.markReconciled(ctx, p)
		if !closed {
			s.closePaymentLocally(ctx, p, "渠道交易已关闭，本地支付单同步关闭")
			return outcomeClosed
		}
		s.payment.recordReconcileLog(ctx, p, model.NotifyResultClosed, "渠道交易已关闭")
		return outcomeObserved

	default:
		// 在途：只推进节流指针，等待用户完成支付或回调到达
		s.markReconciled(ctx, p)
		s.payment.recordReconcileLog(ctx, p, model.NotifyResultNotPaid, fmt.Sprintf("渠道未收款（%s）", res.Raw))
		if !closed && s.payment.reconcileExhausted(ctx, p) {
			s.payment.recordGiveUp(ctx, p)
		}
		return outcomeObserved
	}
}

// reconcileExhausted 判断某笔待支付单是否已用满回查次数，用满则放弃对账并产出待办
// 参数：
//   - ctx: 上下文
//   - p: 支付单
//
// 返回值：
//   - bool: 已达上限
func (s *PaymentService) reconcileExhausted(ctx context.Context, p *model.Payment) bool {
	count := p.ReconcileCount
	var current model.Payment
	if err := s.db.GetDb().WithContext(ctx).Select("reconcile_count").First(&current, p.ID).Error; err == nil {
		count = current.ReconcileCount
	}
	return count >= int32(config.ReconcileInt(s.cfg.Business.PaymentReconcile.MaxReconcileCount, 24))
}

// recordGiveUp 登记「放弃对账」待办
// 超过累计回查上限仍未确认收款的支付单，说明渠道一直在挣扎或回调链路有问题，
// 停止打扰渠道，但必须留一条人工可见的待办，不能静默躺在待支付状态里
// 参数：
//   - ctx: 上下文
//   - p: 支付单
func (s *PaymentService) recordGiveUp(ctx context.Context, p *model.Payment) {
	if s.hasPendingLog(ctx, model.NotifyKindGiveUp, p.PaymentNo) {
		return
	}
	log.Printf("[CRITICAL] 支付单回查次数已达上限，放弃对账: payment_no=%s order_id=%d", p.PaymentNo, p.OrderID)
	entry := notifyEntryFrom(p, model.NotifyKindGiveUp, model.NotifyResultError)
	entry.ErrorMsg = fmt.Sprintf("已回查 %d 次仍未确认收款，停止自动对账，请人工核实渠道状态", p.ReconcileCount)
	s.recordNotifyLog(ctx, entry)
}

// recordReconcileLog 记一条对账回查流水（未收款/无此交易/已关闭等非成功结论）
// 参数：
//   - ctx: 上下文
//   - p: 支付单
//   - result: model.NotifyResult* 结果
//   - msg: 摘要
func (s *PaymentService) recordReconcileLog(ctx context.Context, p *model.Payment, result int32, msg string) {
	entry := notifyEntryFrom(p, model.NotifyKindReconcile, result)
	entry.ErrorMsg = msg
	s.recordNotifyLog(ctx, entry)
}

// hasReconcileNotExist 该支付单此前是否已被回查过一次「渠道无此交易」
// 参数：
//   - ctx: 上下文
//   - paymentNo: 支付单号
//
// 返回值：
//   - bool: 成立表示本轮是第二轮，可以关闭本地支付单
func (s *PaymentService) hasReconcileNotExist(ctx context.Context, paymentNo string) bool {
	return s.hasNotifyResult(ctx, model.NotifyKindReconcile, paymentNo, model.NotifyResultNotExist)
}

// hasNoPaymentConfirmed 渠道是否已明确回答过「这笔没收到钱」
// 用于挂账扫描的去重：渠道对同一笔关闭单不会给出两种答案，问一次即可
// 参数：
//   - ctx: 上下文
//   - paymentNo: 支付单号
//
// 返回值：
//   - bool: 已有未收款或无此交易的回查记录
func (s *PaymentService) hasNoPaymentConfirmed(ctx context.Context, paymentNo string) bool {
	var count int64
	err := s.db.GetDb().WithContext(ctx).Model(&model.NotifyLog{}).
		Where("payment_no = ? AND kind IN ? AND result IN ?", paymentNo,
			[]int32{model.NotifyKindReconcile, model.NotifyKindChannelClose},
			[]int32{model.NotifyResultNotExist, model.NotifyResultNotPaid, model.NotifyResultClosed}).
		Limit(1).Count(&count).Error
	if err != nil {
		log.Printf("[payment-scheduler] 查询回查历史失败 payment_no=%s: %v", paymentNo, err)
		return false
	}
	return count > 0
}

// hasPendingLog 该支付单是否已有指定类别的流水
// 参数：
//   - ctx: 上下文
//   - kind: NotifyLog.Kind
//   - paymentNo: 支付单号
//
// 返回值：
//   - bool: 存在
func (s *PaymentService) hasPendingLog(ctx context.Context, kind int32, paymentNo string) bool {
	var count int64
	err := s.db.GetDb().WithContext(ctx).Model(&model.NotifyLog{}).
		Where("kind = ? AND payment_no = ?", kind, paymentNo).Limit(1).Count(&count).Error
	if err != nil {
		log.Printf("[payment-notify] 查询流水历史失败 kind=%d payment_no=%s: %v", kind, paymentNo, err)
		return false
	}
	return count > 0
}

// hasNotifyResult 该支付单是否已有指定类别与结果的流水
// 参数：
//   - ctx: 上下文
//   - kind: NotifyLog.Kind
//   - paymentNo: 支付单号
//   - result: NotifyLog.Result
//
// 返回值：
//   - bool: 存在
func (s *PaymentService) hasNotifyResult(ctx context.Context, kind int32, paymentNo string, result int32) bool {
	var count int64
	err := s.db.GetDb().WithContext(ctx).Model(&model.NotifyLog{}).
		Where("kind = ? AND payment_no = ? AND result = ?", kind, paymentNo, result).Limit(1).Count(&count).Error
	if err != nil {
		log.Printf("[payment-notify] 查询流水历史失败 kind=%d result=%d payment_no=%s: %v", kind, result, paymentNo, err)
		return false
	}
	return count > 0
}

// closePaymentLocally 渠道确认不会再有收款后，把待支付单置为已关闭
// 条件更新保证与并发回调互不覆盖：回调先到的话这里 0 行生效，事实由 handlePaySuccess 决定
// 参数：
//   - ctx: 上下文
//   - p: 支付单
//   - reason: 关闭原因，写入流水
func (s *Scheduler) closePaymentLocally(ctx context.Context, p *model.Payment, reason string) {
	now := time.Now().Format(timeLayout)
	res := s.payment.db.GetDb().WithContext(ctx).Model(&model.Payment{}).
		Where("payment_no = ? AND status = 1", p.PaymentNo).
		Updates(map[string]interface{}{
			"status":       model.PaymentStatusClosed,
			"pending_flag": nil, // 释放待支付占位键
			"updated_at":   now,
		})
	if res.Error != nil {
		log.Printf("[payment-scheduler] 关闭支付单失败 payment_no=%s: %v", p.PaymentNo, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		return
	}
	log.Printf("[payment-scheduler] 支付单 %s 已关闭：%s", p.PaymentNo, reason)
	entry := notifyEntryFrom(p, model.NotifyKindChannelClose, model.NotifyResultClosed)
	entry.ErrorMsg = reason
	s.payment.recordNotifyLog(ctx, entry)
}

// markReconciled 推进回查计数与节流时间
// 故意不动 updated_at：挂账扫描以「关闭时间 + 渠道时效」为窗口，
// 若每次回查都刷新 updated_at，关闭单会永远留在窗口内被反复扫描
// 参数：
//   - ctx: 上下文
//   - p: 支付单
func (s *Scheduler) markReconciled(ctx context.Context, p *model.Payment) {
	now := time.Now().Format(timeLayout)
	err := s.payment.db.GetDb().WithContext(ctx).Model(&model.Payment{}).
		Where("id = ?", p.ID).
		Updates(map[string]interface{}{
			"reconcile_count":    gorm.Expr("reconcile_count + 1"),
			"last_reconciled_at": now,
		}).Error
	if err != nil {
		log.Printf("[payment-scheduler] 更新回查节流字段失败 payment_no=%s: %v", p.PaymentNo, err)
		return
	}
	p.ReconcileCount++
	p.LastReconciledAt = &now
}

// touchReconciled 只推进节流指针，不动回查计数
// 供不走渠道的任务（支付单/订单不一致检出）使用：reconcile_count 是渠道查询预算，
// 混用会让该单在 DB 校验类任务里被提前判成「回查已达上限」
// 参数：
//   - ctx: 上下文
//   - p: 支付单
func (s *Scheduler) touchReconciled(ctx context.Context, p *model.Payment) {
	now := time.Now().Format(timeLayout)
	if err := s.payment.db.GetDb().WithContext(ctx).Model(&model.Payment{}).
		Where("id = ?", p.ID).
		Update("last_reconciled_at", now).Error; err != nil {
		log.Printf("[payment-scheduler] 更新节流指针失败 payment_no=%s: %v", p.PaymentNo, err)
		return
	}
	p.LastReconciledAt = &now
}

// recordFailure 记录对账处理失败的支付单，交由下一轮 retryFailures 重试
// 只记真正需要额外重试的情形（补同步过程中 DB/RPC 出错），渠道未支付不算失败
//
// 同一支付单同一任务只保留一条待重试行：uk_failures_pending 命中时改为刷新失败原因，
// 渠道持续不可达时不会按轮次堆出几十条等价记录
// 参数：
//   - ctx: 上下文
//   - p: 支付单
//   - taskType: 任务类型（reconcile_pending / reconcile_stuck）
//   - errMsg: 失败原因
func (s *PaymentService) recordFailure(ctx context.Context, p *model.Payment, taskType, errMsg string) {
	now := time.Now().Format(timeLayout)
	slot := model.FailurePendingSlot
	failure := &model.SchedulerFailure{
		PaymentNo:   p.PaymentNo,
		OrderID:     p.OrderID,
		TaskType:    taskType,
		Status:      model.FailureStatusPending,
		PendingSlot: &slot,
		ErrorMsg:    truncateRunes(errMsg, notifyErrorMsgMaxLen),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err := s.db.GetDb().WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "payment_no"}, {Name: "task_type"}, {Name: "pending_slot"}},
		DoUpdates: clause.AssignmentColumns([]string{"error_msg", "updated_at"}),
	}).Create(failure).Error
	if err != nil {
		log.Printf("[payment-scheduler] 记录失败支付单 %s 出错: %v", p.PaymentNo, err)
	}
}

// retryFailures 重试历史对账失败记录，带任务级 panic 兜底
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - reconcileStat: 本轮统计（落库判定依据）
func (s *Scheduler) retryFailures(ctx context.Context) reconcileStat {
	return runReconcileTask("retryFailures", func(stat *reconcileStat) { s.retryFailuresOnce(ctx, stat) })
}

// retryFailuresOnce 历史失败重试的任务本体
// 处理前提：失败记录对应的支付单仍处于该任务关注的状态；已被其他路径收敛则标记为已处理
//
// 重试路径与两条扫描共用「分布式锁 + 节流间隔 + 累计上限」三道约束：
// 少了任何一道，渠道故障时它会退化成「每分钟绕过节流直打渠道」，
// 且同单多条待重试行会在同一轮里各查一次渠道、把 5 次重试预算提前吃光
// 参数：
//   - ctx: 上下文
//   - stat: 本轮统计（写入指针，由 runReconcileTask 回传）
func (s *Scheduler) retryFailuresOnce(ctx context.Context, stat *reconcileStat) {
	lockKey := "scheduler:payment:reconcile:retry:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	r := s.payment.cfg.Business.PaymentReconcile
	maxReconcile := int32(config.ReconcileInt(r.MaxReconcileCount, 24))
	throttleBefore := time.Now().Add(-time.Duration(config.ReconcileInt(r.ThrottleMinutes, 5)) * time.Minute).Format(timeLayout)

	// LEFT JOIN 保留「支付单已被删除」的孤儿记录，交由循环内标记放弃，避免永久滞留待重试态。
	// 累计上限不写在扫描条件里：写进去这些行会永远是「待重试」，白白占住批次额度，
	// 改为读到后在调用渠道之前就置放弃
	var failures []model.SchedulerFailure
	if err := s.payment.db.GetDb().WithContext(ctx).
		Table("payment_scheduler_failures").
		Select("payment_scheduler_failures.*").
		Joins("LEFT JOIN payments ON payments.payment_no = payment_scheduler_failures.payment_no").
		Where("payment_scheduler_failures.status = ? AND payment_scheduler_failures.retry_count < ?",
			model.FailureStatusPending, model.MaxFailureRetry).
		Where("payments.id IS NULL OR payments.last_reconciled_at IS NULL OR payments.last_reconciled_at < ?",
			throttleBefore).
		Order("payment_scheduler_failures.id asc").
		Limit(config.ReconcileInt(r.BatchSize, 100)).
		Find(&failures).Error; err != nil {
		log.Printf("[payment-scheduler] 查询对账失败记录出错: %v", err)
		stat.failed++
		return
	}

	// 同一支付单本轮只打扰渠道一次（跨锁竞态可能留下 pending/stuck 两条待重试行）
	queried := make(map[string]bool, len(failures))
	for i := range failures {
		f := &failures[i]
		var p model.Payment
		if err := s.payment.db.GetDb().WithContext(ctx).Where("payment_no = ?", f.PaymentNo).First(&p).Error; err != nil {
			s.markFailureStatus(ctx, f.ID, model.FailureStatusAbandoned)
			continue
		}
		// 支付单已被回调或其他路径收敛（非待支付/非已关闭），无需再试
		if p.Status != 1 && p.Status != 5 {
			s.markFailureStatus(ctx, f.ID, model.FailureStatusDone)
			continue
		}
		// 回查上限按支付单口径判定：扫描已放弃的单不再重试，也不再打扰渠道
		if p.ReconcileCount >= maxReconcile {
			s.markFailureStatus(ctx, f.ID, model.FailureStatusAbandoned)
			continue
		}
		if queried[p.PaymentNo] {
			continue
		}
		queried[p.PaymentNo] = true
		s.reconcileOne(ctx, &p, p.Status != 1 || f.TaskType != model.TaskReconcilePending).count(stat)
		if p.ReconcileCount >= maxReconcile {
			s.markFailureStatus(ctx, f.ID, model.FailureStatusAbandoned)
			continue
		}
		s.incrementFailureRetry(ctx, f.ID)
	}
}

// markFailureStatus 更新对账失败记录状态
// 转终态时必须清空 pending_slot，否则唯一索引会挡住该单后续的新失败
// 参数：
//   - ctx: 上下文
//   - id: 记录 ID
//   - status: 目标状态
func (s *Scheduler) markFailureStatus(ctx context.Context, id int64, status int32) {
	if err := s.payment.db.GetDb().WithContext(ctx).Model(&model.SchedulerFailure{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       status,
			"pending_slot": nil,
			"updated_at":   time.Now().Format(timeLayout),
		}).Error; err != nil {
		log.Printf("[payment-scheduler] 更新对账失败记录 %d 状态出错: %v", id, err)
	}
}

// incrementFailureRetry 累加失败记录的重试次数
// 参数：
//   - ctx: 上下文
//   - id: 记录 ID
func (s *Scheduler) incrementFailureRetry(ctx context.Context, id int64) {
	if err := s.payment.db.GetDb().WithContext(ctx).Model(&model.SchedulerFailure{}).
		Where("id = ?", id).
		UpdateColumn("retry_count", gorm.Expr("retry_count + 1")).Error; err != nil {
		log.Printf("[payment-scheduler] 累加对账失败记录 %d 重试次数出错: %v", id, err)
	}
}

// cleanupNotifyLogs 删除超过保留天数的通知流水
// 流水的价值集中在「刚出问题的这段时间」，过期后只占空间；
// 用「锁键带日期 + 不释放」实现集群级每日一次，无需额外的游标表
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - int64: 删除行数；0 = 锁未获取或无需删除；-1 = 执行出错（计入失败记录）
func (s *Scheduler) cleanupNotifyLogs(ctx context.Context) int64 {
	retentionDays := config.ReconcileInt(s.payment.cfg.Business.PaymentReconcile.NotifyLogRetentionDays, 30)
	lockKey := fmt.Sprintf("scheduler:payment:notify:cleanup:%s", time.Now().Format("2006-01-02"))
	if s.acquireLock(ctx, lockKey, 25*time.Hour) == "" {
		return 0
	}
	deadline := time.Now().AddDate(0, 0, -retentionDays).Format(timeLayout)
	res := s.payment.db.GetDb().WithContext(ctx).
		Where("created_at < ?", deadline).
		Delete(&model.NotifyLog{})
	if res.Error != nil {
		log.Printf("[payment-scheduler] 清理通知流水失败: %v", res.Error)
		return -1
	}
	if res.RowsAffected > 0 {
		log.Printf("[payment-scheduler] 已清理 %d 条超过 %d 天的通知流水", res.RowsAffected, retentionDays)
	}
	return res.RowsAffected
}

// runReconcileTask 执行一个对账子任务：任务级 panic 兜底，并保证计数一定回传给调用方
// 对账属于旁路能力，任何一笔的异常都不应终止整个调度协程；
// panic 计入 stat.panics，使异常轮次不满足空转条件（shouldRecordReconcile），
// 从而在 debugger 进程记录中可见——否则 panic 被静默恢复后无任何痕迹
//
// 结果必须是命名返回值：panic 越过子任务本体的 return 之后，匿名结果槽是零值，
// defer 里对局部变量的写入调用方读不到（panic 计数与 panic 前已完成的动作计数会一起归零）；
// 命名返回值就是结果槽本身，经 fn 的指针写入对调用方可见
// 参数：
//   - name: 任务名，用于日志定位
//   - fn: 子任务本体，把本轮统计写入收到的指针
//
// 返回值：
//   - reconcileStat: 本轮统计；panic 时保留 panic 前已写入的计数并追加 panics
//
// 使用示例：
//
//	stat := runReconcileTask("detectStuck", func(stat *reconcileStat) { s.scanStuck(ctx, stat) })
func runReconcileTask(name string, fn func(stat *reconcileStat)) (stat reconcileStat) {
	defer func() {
		if r := recover(); r != nil {
			stat.panics++
			log.Printf("[payment-scheduler] 任务 %s 异常已恢复: %v", name, r)
		}
	}()
	fn(&stat)
	return stat
}

// reconcileRefunds 退款查单收敛（P0-2），带任务级 panic 兜底
// 微信退款受理（PROCESSING）是常态而非成功：扫描停留待退款超窗的微信渠道退款流水，
// 按 RefundQuery 核实渠道终态——SUCCESS 完成落库收尾，ABNORMAL/CLOSED 置失败交由
// order 侧卡单重试按「失败可重新发起」口径处理，PROCESSING 保持待退款下一轮再查
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - reconcileStat: 本轮统计（落库判定依据）
func (s *Scheduler) reconcileRefunds(ctx context.Context) reconcileStat {
	return runReconcileTask("reconcileRefunds", func(stat *reconcileStat) { s.scanPendingRefunds(ctx, stat) })
}

// scanPendingRefunds 退款查单收敛的任务本体
// 仅处理微信渠道（pay_type 1/5，支付宝退款为同步语义不在此列）；
// 受理瞬间给渠道异步留 2 分钟窗口，避免刚受理就被查单打扰
//
// 参数：
//   - ctx: 上下文
//   - stat: 本轮统计（写入指针，由 runReconcileTask 回传）
func (s *Scheduler) scanPendingRefunds(ctx context.Context, stat *reconcileStat) {
	lockKey := "scheduler:payment:refund:reconcile:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	deadline := time.Now().Add(-2 * time.Minute).Format(timeLayout)
	var flows []model.RefundPayment
	// refund_payments 无 pay_type 冗余列：JOIN payments 过滤微信渠道（pay_type 1/5）
	if err := s.payment.db.GetDb().WithContext(ctx).
		Table("refund_payments").
		Select("refund_payments.*").
		Joins("JOIN payments ON payments.payment_no = refund_payments.payment_no AND payments.pay_type IN ?", []int32{1, 5}).
		Where("refund_payments.status = ? AND refund_payments.updated_at < ?", 1, deadline).
		Order("refund_payments.id asc").Limit(20).
		Scan(&flows).Error; err != nil {
		log.Printf("[payment-scheduler] 查询待收敛退款流水失败: %v", err)
		stat.failed++
		return
	}
	if len(flows) == 0 {
		return
	}

	client := s.payment.getWechatPayClient()
	for i := range flows {
		flow := &flows[i]
		if client == nil {
			log.Printf("[payment-scheduler] 微信客户端未就绪，退款查单本轮跳过 refund_no=%s", flow.RefundNo)
			return
		}
		resp, err := client.RefundQuery(ctx, flow.RefundNo)
		if err != nil {
			// 查单失败不算流水失败：渠道单刚创建时可能暂不可查，下一轮继续
			log.Printf("[payment-scheduler] 退款查单失败 refund_no=%s: %v", flow.RefundNo, err)
			continue
		}
		switch resp.Status {
		case "SUCCESS":
			flow.TransactionID = resp.RefundID
			if err := s.payment.completeChannelRefundSuccess(ctx, &model.Payment{PaymentNo: flow.PaymentNo}, flow); err != nil {
				log.Printf("[payment-scheduler] 退款成功落库失败 refund_no=%s: %v", flow.RefundNo, err)
				stat.failed++
				continue
			}
			stat.refundOK++
			log.Printf("[payment-scheduler] 退款查单确认成功 refund_no=%s，流水与支付单已收敛", flow.RefundNo)
		case "ABNORMAL", "CLOSED":
			// 渠道终态失败：流水置退款失败(3)。order 侧 QueryRefundResult 感知后走重试，
			// ProcessRefund 的幂等逻辑允许清理失败流水后重新发起
			now := time.Now().Format(timeLayout)
			res := s.payment.db.GetDb().WithContext(ctx).Model(&model.RefundPayment{}).
				Where("id = ? AND status = 1", flow.ID).
				Updates(map[string]interface{}{
					"status":         int32(3),
					"transaction_id": resp.RefundID,
					"updated_at":     now,
				})
			if res.Error != nil {
				log.Printf("[payment-scheduler] 退款流水置失败 refund_no=%s: %v", flow.RefundNo, res.Error)
				stat.failed++
				continue
			}
			if res.RowsAffected > 0 {
				stat.refundFailed++
				log.Printf("[payment-scheduler] 退款查单确认渠道失败 refund_no=%s status=%s，流水已转待重试", flow.RefundNo, resp.Status)
			}
		default:
			// PROCESSING 等：保持待退款，下一轮再查
		}
	}
}

// acquireLock 获取 Redis 分布式锁
// 参数：
//   - ctx: 上下文
//   - key: 锁键
//   - ttl: 锁过期时间
//
// 返回值：
//   - string: 成功返回持有者标识，失败返回空串
func (s *Scheduler) acquireLock(ctx context.Context, key string, ttl time.Duration) string {
	value := strconv.FormatInt(time.Now().UnixNano(), 10)
	ok, err := s.redisClient.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		log.Printf("[payment-scheduler] 获取锁 %s 失败: %v", key, err)
		return ""
	}
	if !ok {
		return ""
	}
	return value
}

// releaseLock 释放 Redis 分布式锁（Lua 原子比较锁值后删除，避免误删他人锁）
// 参数：
//   - ctx: 上下文
//   - key: 锁键
//   - value: acquireLock 返回的持有者标识
func (s *Scheduler) releaseLock(ctx context.Context, key, value string) {
	if err := releaseLockScript.Run(ctx, s.redisClient, []string{key}, value).Err(); err != nil {
		log.Printf("[payment-scheduler] 释放锁 %s 失败: %v", key, err)
	}
}

// releaseLockScript 与 order 调度器同语义的原子释放脚本（本服务独立持有一份，避免跨服务耦合）
var releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0
`)

// timeLayout 库内 datetime 列统一的字符串格式
const timeLayout = "2006-01-02 15:04:05"

// reconcileTaskType 按支付单当前状态给出失败记录的任务类型
// 参数：
//   - closed: 是否来自挂账扫描
//
// 返回值：
//   - string: model.FailureTask* 之一
func reconcileTaskType(closed bool) string {
	if closed {
		return model.TaskReconcileStuck
	}
	return model.TaskReconcilePending
}

// reconcileConfirmRef 对账补同步的来源凭据，带批次日期便于回溯具体流水
// 参数：
//   - paymentNo: 支付单号
//
// 返回值：
//   - string: 形如 reconcile:ZFxxx
func reconcileConfirmRef(paymentNo string) string {
	return "reconcile:" + paymentNo
}

// withinReconcileWindow 判断支付单是否仍在渠道时效内，超出后回查没有意义
// 微信在线支付下单默认 2 小时失效；支付宝（含付款码）受下单时的 timeout_express（对齐订单超时）约束；
// 微信付款码单同样随订单超时取消而终止（超时关单走撤销），因此 2/5/6 都按
// 「配置上限与订单超时取小」判定，避免对已失效交易反复发问
//
// 参数：
//   - cfg: 全局配置
//   - p: 支付单
//
// 返回值：
//   - bool: 仍在时效内
func withinReconcileWindow(cfg *config.Config, p *model.Payment) bool {
	r := cfg.Business.PaymentReconcile
	window := time.Duration(config.ReconcileInt(r.PendingMaxMinutes, 120)) * time.Minute
	if p.PayType == 2 || p.PayType == 5 || p.PayType == 6 {
		if orderTimeout := config.ReconcileInt(cfg.Business.OrderTimeout, 1800); time.Duration(orderTimeout)*time.Second < window {
			window = time.Duration(orderTimeout) * time.Second
		}
	}
	created, err := parseDBTime(p.CreatedAt)
	if err != nil {
		// 时间解析不了（历史脏数据）时保守放行，交给节流与上限兜底
		return true
	}
	return time.Since(created) <= window
}

// dbTimeLayouts 库内 datetime 列可能的文本形态
// DSN 带 parseTime=True 时，驱动把 datetime 读成 time.Time，再回填到模型的 string 字段会带上
// 「2026-10-21T14:19:47+08:00」这种 RFC3339 形态；而本服务自己写入的是「2006-01-02 15:04:05」。
// 两种都必须能解析，否则时间窗判定会恒落入「解析失败保守放行」分支而彻底失效
var dbTimeLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	timeLayout,
	"2006-01-02",
}

// parseDBTime 解析从库中读回的时间字符串
// 参数：
//   - value: 时间字符串（RFC3339 或 datetime 文本均可）
//
// 返回值：
//   - time.Time: 解析结果，无偏移量的形态按本地时区解释
//   - error: 全部候选格式都解析失败
//
// 使用示例：
//   - t, err := parseDBTime("2026-10-21T14:19:47+08:00")
func parseDBTime(value string) (time.Time, error) {
	for _, layout := range dbTimeLayouts {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间字符串 %q", value)
}
