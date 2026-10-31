package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/debuglog"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

// 调度器处理中状态（不占用业务状态值域 1-8，避免污染 C 端展示与查询）
// 抢占后若处理失败会立即回滚；进程被 kill 等异常场景由 recoverStuckOrders 兜底恢复
const (
	// processingStatusCancel 超时取消处理中（原状态 1 待支付）
	processingStatusCancel int32 = 100
	// processingStatusComplete 自动完成处理中（原状态 4 配送中）
	processingStatusComplete int32 = 101
	// processingStatusPickup2 自提单自动完成处理中（原状态 2 已支付）
	processingStatusPickup2 int32 = 102
	// processingStatusPickup3 自提单自动完成处理中（原状态 3 备货中）
	processingStatusPickup3 int32 = 103
)

// errOrderAlreadyPaid 超时取消前反查到成功支付单：订单已收款，跳过取消（补同步已另行发起）
var errOrderAlreadyPaid = errors.New("订单已收款，跳过超时取消")

// errOrderStateConflict 抢占态已被并发流转（如支付回调补同步先到、用户/后台已取消），本轮跳过
var errOrderStateConflict = errors.New("订单状态已变更，跳过本轮调度")

// stuckRecoverAfter 处理中状态停留超过该时长视为进程异常中断，兜底恢复原状态
const stuckRecoverAfter = 10 * time.Minute

// 卡单退款自动重试的节流与预算（B1）
// 此前扫描按 id asc 取 20 条，而「渠道受理中」的单本轮直接 continue、什么都不推进，
// 于是队首这批卡单每轮都被重复取出、永久占住批次名额（还会反复打 payment 查单 RPC），
// 后来卡住的单永远排不进批次——与 payment 侧修过的同类饿死问题同源
const (
	// refundRetryInterval 单笔卡单退款的核查间隔：每轮核查后推进 last_checked_at，到点才再次入批
	refundRetryInterval = 10 * time.Minute
	// refundRetryBatch 每轮最多核查的卡单退款数（沿用原 Limit(20)）
	refundRetryBatch = 20
	// refundMaxRetry 累计核查上限，达到即停止自动重试并转人工待办；
	// 按 10min 一次折算约 4 小时，足以覆盖渠道异步退款的正常收敛窗口
	refundMaxRetry = 24
	// releaseMaxRetry 收尾回补重放上限（取消收尾与退款收尾共用），与 retryFailures 的
	// retry_count < 5 同口径；每分钟一轮折算约 5 分钟，覆盖商品服务短时抖动/重启，仍失败则只剩人工补做
	releaseMaxRetry = 5
	// releaseBatch 每轮最多重放的收尾回补记录数（取消与退款各一批，与其余重试任务同用 Limit(100)）
	releaseBatch = 100
)

// orderStat 单轮订单调度统计：debugger 进程记录的落库判定与摘要字段的唯一依据
type orderStat struct {
	recovered      int // 兜底恢复的处理中订单数
	cancelled      int // 超时未支付自动取消
	completed      int // 配送单发货超时自动完成
	pickup         int // 自提单支付超时自动完成
	refundOK       int // 卡单退款重试成功
	approved       int // 超时退款自动审批
	cancelReleased int // 取消收尾回补（库存/优惠券）重放成功
	refundReleased int // 退款收尾回补（库存/优惠券）重放成功
	exportCleaned  int // 过期导出任务清理数（文件 + 记录）
	failed         int // 本轮失败（含扫描/抢占/事务/重试/审批出错）
	panics         int // 本轮被 runOnce 恢复的 panic 次数（正常为 0 或 1）
}

// hasAction 本轮是否发生了实质动作或失败
// 各任务本身只在「有单可处理」时动作，故任一计数非零即非空转；
// 空转轮次不落库，防止 1 分钟一轮的记录挤占 debug_logs 全表上限
// 返回值：
//   - bool: true 表示有实质动作
func (s orderStat) hasAction() bool {
	return s.recovered+s.cancelled+s.completed+s.pickup+s.refundOK+s.approved+s.cancelReleased+s.refundReleased+s.exportCleaned+s.failed+s.panics > 0
}

// merge 把另一份统计并入本轮（子任务结果汇总）
// 参数：
//   - other: 子任务统计
func (s *orderStat) merge(other orderStat) {
	s.recovered += other.recovered
	s.cancelled += other.cancelled
	s.completed += other.completed
	s.pickup += other.pickup
	s.refundOK += other.refundOK
	s.approved += other.approved
	s.cancelReleased += other.cancelReleased
	s.refundReleased += other.refundReleased
	s.exportCleaned += other.exportCleaned
	s.failed += other.failed
	s.panics += other.panics
}

// shouldRecordOrder 判定本轮订单调度是否值得写入 debugger 进程记录
// 组件 StartProcess 创建即落库，故必须在跑完子任务后判定：
// 无任何动作与失败的轮次只打标准日志，不落调试库
// 参数：
//   - stat: 本轮统计
//
// 返回值：
//   - bool: true 表示应记录
func shouldRecordOrder(stat orderStat) bool {
	return stat.hasAction()
}

// Scheduler 订单定时任务调度器
// 负责超时未支付订单自动取消、取消收尾回补重放、发货后超时自动完成、卡单退款自动重试、过期导出任务清理
// 采用 Redis 分布式锁 + 数据库原子抢占 + 失败回滚重试机制
type Scheduler struct {
	db            *mysql.Instance
	redisClient   *redis.Client
	refundService *RefundService
	// exportTaskService 导出任务服务（过期文件与记录清理；可为 nil，测试环境跳过）
	exportTaskService *ExportTaskService
	// cfg 全局配置：取消前置渠道确认开关（business.payment_reconcile）
	cfg *config.Config
}

// NewScheduler 创建订单定时任务调度器
//
// 参数：
//   - db: MySQL 实例
//   - redisClient: Redis 客户端，用于分布式锁
//   - refundService: 退款服务，用于卡单退款自动重试
//   - exportTaskService: 导出任务服务，用于过期导出清理（可为 nil）
//   - cfg: 全局配置（可为 nil，按默认口径处理）
//
// 返回值：
//   - *Scheduler: 调度器实例
func NewScheduler(db *mysql.Instance, redisClient *redis.Client, refundService *RefundService, exportTaskService *ExportTaskService, cfg *config.Config) *Scheduler {
	return &Scheduler{
		db:                db,
		redisClient:       redisClient,
		refundService:     refundService,
		exportTaskService: exportTaskService,
		cfg:               cfg,
	}
}

// Start 启动定时任务调度器
//
// 参数：
//   - ctx: 用于优雅退出的上下文
//   - orderTimeout: 订单超时未支付时间（秒），<=0 时默认 1800
//   - autoCompleteDays: 发货后自动完成天数，<=0 时默认 7
func (s *Scheduler) Start(ctx context.Context, orderTimeout int, autoCompleteDays int) {
	if orderTimeout <= 0 {
		orderTimeout = 1800
	}
	if autoCompleteDays <= 0 {
		autoCompleteDays = 7
	}

	log.Println("[order-scheduler] 定时任务调度器已启动")

	// 首次立即执行一次，随后每 1 分钟执行一次
	s.runOnce(ctx, orderTimeout, autoCompleteDays)

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("[order-scheduler] 定时任务调度器已停止")
			return
		case <-ticker.C:
			s.runOnce(ctx, orderTimeout, autoCompleteDays)
		}
	}
}

// runOnce 单次执行两类的定时任务
// 进程级记录口径（debugger）：先跑完子任务再判定是否记录——组件 StartProcess
// 创建即落库，1 分钟一轮全量记录会把 debug_logs 全表上限的 HTTP 请求记录
// 迅速挤掉，故空转轮次（无任何动作与失败）只打标准日志。
// 有动作的轮次落一条摘要记录（含各任务计数），含失败的轮次以错误级标记
//
// 落库统一放在 defer 里：正常跑完与 panic 提前展开都只记一次，且异常轮次一定留痕。
// 订单调度此前没有任何 panic 兜底，一笔异常会终止整个调度协程并让后续轮次静默消失；
// 现由本层恢复——本轮剩余子任务跳过（下一轮重来），协程继续存活
// 参数：
//   - ctx: 上下文
//   - orderTimeout: 订单超时未支付时间（秒）
//   - autoCompleteDays: 发货后自动完成天数
func (s *Scheduler) runOnce(ctx context.Context, orderTimeout int, autoCompleteDays int) {
	start := time.Now()

	var stat orderStat
	defer func() {
		if r := recover(); r != nil {
			stat.panics++
			log.Printf("[order-scheduler] 本轮调度异常已恢复: %v", r)
		}
		s.recordRound(start, stat)
	}()

	// 兜底恢复：进程异常中断导致回滚未执行的订单，先恢复原状态再进入本轮调度
	stat.merge(s.recoverStuckOrders(ctx))
	stat.merge(s.cancelTimeoutOrders(ctx, orderTimeout))
	stat.merge(s.retryCancelReleases(ctx))
	stat.merge(s.retryRefundReleases(ctx))
	stat.merge(s.autoCompleteOrders(ctx, autoCompleteDays))
	stat.merge(s.autoCompletePickupOrders(ctx, autoCompleteDays))
	stat.merge(s.retryStuckRefunds(ctx))
	stat.merge(s.autoApproveStaleRefunds(ctx))
	stat.merge(s.cleanupExportTasks(ctx))
}

// recordRound 把本轮统计落一条 debugger 进程记录；空转轮次只打标准日志不落库
// 参数：
//   - start: 本轮开始时间（算耗时）
//   - stat: 本轮统计
func (s *Scheduler) recordRound(start time.Time, stat orderStat) {
	if !shouldRecordOrder(stat) {
		return
	}
	rec := debuglog.Start("订单调度", "order_scheduler")
	if rec == nil {
		return
	}
	fields := map[string]interface{}{
		"recovered":      stat.recovered,
		"cancelled":      stat.cancelled,
		"completed":      stat.completed,
		"pickup":         stat.pickup,
		"refund_ok":      stat.refundOK,
		"approved":       stat.approved,
		"cancel_release": stat.cancelReleased,
		"refund_release": stat.refundReleased,
		"export_cleaned": stat.exportCleaned,
		"failed":         stat.failed,
		"panics":         stat.panics,
		"duration_ms":    time.Since(start).Milliseconds(),
	}
	if stat.failed > 0 || stat.panics > 0 {
		rec.Error("本轮订单调度存在失败", fields)
		_ = rec.EndProcess(debuglog.StatusFailed)
		return
	}
	rec.Info("本轮订单调度摘要", fields)
	_ = rec.EndProcess(debuglog.StatusCompleted)
}

// retryStuckRefunds 自动重试卡在"退款中"(status=4) 超过阈值的退款单
// 场景：同意退款后 ProcessRefund 失败/进程中断，退款单永久停留在 4 且库存未回补。
// 重试以退款单自身的 payment_no 重新发起渠道退款，成功后走统一收尾逻辑。
// 批次选取按 last_checked_at asc 做游标轮转（B1）：每轮取出后**无条件推进指针**，
// 渠道受理中而跳过的单也一样推进，因此没有「队首反复取出、新单永久排不进」的饿死；
// 累计核查达 refundMaxRetry 仍未收敛的单停止自动重试，落一条人工待办（处置入口在后台
// 「恢复自动收敛」（ResetRefundRetry），把 retry_count 归零并重新起扫）。
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - orderStat: 本轮统计（落库判定依据）
func (s *Scheduler) retryStuckRefunds(ctx context.Context) orderStat {
	var stat orderStat
	if s.refundService == nil {
		return stat
	}
	lockKey := "scheduler:order:refund:retry:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return stat
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	// 仅重试停留超过 stuckRecoverAfter 的退款单，避免与刚发起的退款竞态
	deadline := time.Now().Add(-stuckRecoverAfter).Format("2006-01-02 15:04:05")
	throttleBefore := time.Now().Add(-refundRetryInterval).Format("2006-01-02 15:04:05")
	var refunds []*model.Refund
	if err := s.db.GetDb().WithContext(ctx).
		Where("status = ? AND updated_at < ?", 4, deadline).
		Where("retry_count < ?", refundMaxRetry).
		Where("(last_checked_at IS NULL OR last_checked_at < ?)", throttleBefore).
		Order("last_checked_at asc").Limit(refundRetryBatch).Find(&refunds).Error; err != nil {
		log.Printf("[order-scheduler] 查询卡单退款失败: %v", err)
		stat.failed++
		return stat
	}
	for _, r := range refunds {
		// 指针必须先于动作推进：本体的三条 continue 分支（查单失败/渠道受理中/重试失败）
		// 都不改状态，指针若不推进就等于把这批单钉在队首
		if s.markRefundChecked(ctx, r) >= int32(refundMaxRetry) {
			s.giveUpRefundRetry(ctx, r)
			stat.failed++ // 转人工计为异常轮次，使本轮落入 debugger 的 failed 记录
			continue
		}
		// P0-2：先问 payment 侧流水状态再决定动作——渠道受理中（PROCESSING）的单
		// 不能盲目重发（ProcessRefund 幂等会拒绝「处理中」重复提交），查单确认成功
		// 的直接收尾，失败/无流水的才重新发起
		flowStatus, err := s.refundService.queryRefundFlowStatus(ctx, r)
		if err != nil {
			log.Printf("[order-scheduler] 查询退款流水状态失败 refund_id=%d: %v", r.ID, err)
			stat.failed++
			continue
		}
		switch flowStatus {
		case 1:
			// 渠道受理中：等待 payment 侧查单任务收敛，本轮跳过
			continue
		case 2:
			// 渠道已确认退款成功：直接走统一收尾（置已退款、回补库存、更新订单去向）
			var items []*model.RefundItem
			if err := s.db.GetDb().WithContext(ctx).Where("refund_id = ?", r.ID).Find(&items).Error; err != nil {
				log.Printf("[order-scheduler] 读取退款明细失败 refund_id=%d: %v", r.ID, err)
				stat.failed++
				continue
			}
			if err := s.refundService.completeRefundSuccess(ctx, r, items, "system", 0); err != nil {
				log.Printf("[order-scheduler] 退款查单收敛收尾失败 refund_id=%d: %v", r.ID, err)
				stat.failed++
				continue
			}
			_ = s.refundService.createRefundLog(ctx, r.ID, "retry", "system", 0, "退款查单确认成功，自动完成收尾")
			log.Printf("[order-scheduler] 退款单 %d 渠道确认成功，已置为已退款", r.ID)
			stat.refundOK++
		default:
			// 0=流水不存在 / 3=渠道失败：重新发起渠道退款（失败流水由 payment 幂等清理后重发）
			err := s.refundService.RetryRefund(ctx, &order.RetryRefundReq{Id: r.ID})
			if err != nil {
				log.Printf("[order-scheduler] 自动重试退款单 %d 失败: %v", r.ID, err)
				stat.failed++
				continue
			}
			log.Printf("[order-scheduler] 退款单 %d 卡单重试已发起", r.ID)
			stat.refundOK++
		}
	}
	return stat
}

// markRefundChecked 无条件推进卡单退款的节流指针并累加核查次数
// 刻意走 UpdateColumns：普通 Updates 会触发 base 模型的更新钩子把 updated_at 刷成当前时间，
// 而 updated_at 正是「退款单停留退款中超过 stuckRecoverAfter」的起扫判据，
// 被自己的扫描刷新一轮就把卡单时长清零，预算窗口也会跟着漂移
// 参数：
//   - ctx: 上下文
//   - r: 本轮取出的退款单（成功后同步其内存字段）
//
// 返回值：
//   - int32: 推进后的累计核查次数；写库失败返回推进前的值（该单下轮仍会入批，
//     不会因指针没写进而被永久漏掉），失败只记日志不中断本轮处置
//
// 使用示例：
//   - if s.markRefundChecked(ctx, r) >= int32(refundMaxRetry) { s.giveUpRefundRetry(ctx, r) }
func (s *Scheduler) markRefundChecked(ctx context.Context, r *model.Refund) int32 {
	now := time.Now().Format("2006-01-02 15:04:05")
	res := s.db.GetDb().WithContext(ctx).Model(&model.Refund{}).
		Where("id = ?", r.ID).
		UpdateColumns(map[string]interface{}{
			"last_checked_at": now,
			"retry_count":     gorm.Expr("retry_count + 1"),
		})
	if res.Error != nil {
		log.Printf("[order-scheduler] 推进卡单退款节流指针失败 refund_id=%d: %v", r.ID, res.Error)
		return r.RetryCount
	}
	r.LastCheckedAt = &now
	r.RetryCount++
	return r.RetryCount
}

// giveUpRefundRetry 卡单退款自动重试用满预算：停止自动处理并留下人工可见的待办
// 静默放弃会被读成「资金问题不存在」，所以三处都要留痕：stderr 的 [CRITICAL]（供日志告警）、
// refund_logs 的 give_up 轨迹（后台退款详情时间线直接可见，与审批轨迹同一条）、
// 本轮调度统计计为失败（debugger 进程记录留档）。
// 扫描条件带 retry_count < 上限，用满的单不再入批，因此本函数对同一退款单只执行一次。
// 参数：
//   - ctx: 上下文
//   - r: 已达上限的退款单
func (s *Scheduler) giveUpRefundRetry(ctx context.Context, r *model.Refund) {
	log.Printf("[CRITICAL] 退款单自动重试已达上限(%d次)仍未收敛，停止自动处理 refund_id=%d refund_no=%s order_id=%d",
		refundMaxRetry, r.ID, r.RefundNo, r.OrderID)
	if err := s.refundService.createRefundLog(ctx, r.ID, "give_up", "system", 0,
		fmt.Sprintf("自动重试已核查 %d 次仍未收敛，已停止自动处理；请人工核实渠道退款状态，确认后在后台「恢复自动收敛」重新起扫", r.RetryCount)); err != nil {
		log.Printf("[order-scheduler] 写入卡单退款放弃轨迹失败 refund_id=%d: %v", r.ID, err)
	}
}

// recoverStuckOrders 兜底扫描：将停留在处理中状态超过阈值的订单批量恢复为原状态
// 覆盖进程被 kill 导致 claimOrders 后回滚逻辑未执行的场景
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - orderStat: 本轮统计（落库判定依据）
func (s *Scheduler) recoverStuckOrders(ctx context.Context) orderStat {
	var stat orderStat
	deadline := time.Now().Add(-stuckRecoverAfter).Format("2006-01-02 15:04:05")
	pairs := []struct{ processing, from int32 }{
		{processingStatusCancel, 1},
		{processingStatusComplete, 4},
		{processingStatusPickup2, 2},
		{processingStatusPickup3, 3},
	}
	for _, p := range pairs {
		res := s.db.GetDb().WithContext(ctx).Exec(
			"UPDATE orders SET status = ?, updated_at = NOW() WHERE status = ? AND updated_at < ?",
			p.from, p.processing, deadline,
		)
		if res.Error != nil {
			log.Printf("[order-scheduler] 兜底恢复处理中订单 (status %d -> %d) 失败: %v", p.processing, p.from, res.Error)
			stat.failed++
			continue
		}
		stat.recovered += int(res.RowsAffected)
		if res.RowsAffected > 0 {
			log.Printf("[order-scheduler] 兜底恢复 %d 个停留超时的处理中订单 (status %d -> %d)", res.RowsAffected, p.processing, p.from)
		}
	}
	return stat
}

// cancelTimeoutOrders 取消超时未支付订单
//
// 参数：
//   - ctx: 上下文
//   - orderTimeout: 超时时间（秒）
//
// 返回值：
//   - orderStat: 本轮统计（落库判定依据）
func (s *Scheduler) cancelTimeoutOrders(ctx context.Context, orderTimeout int) orderStat {
	var stat orderStat
	lockKey := "scheduler:order:timeout:cancel:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return stat
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	// 1. 优先处理历史失败记录
	stat.merge(s.retryFailures(ctx, "timeout_cancel", 0))

	// 2. 原子抢占一批待取消订单（status=1 → 100 处理中）
	orders := s.claimOrders(ctx, 1, processingStatusCancel, "created_at < ?", time.Now().Add(-time.Duration(orderTimeout)*time.Second).Format("2006-01-02 15:04:05"))
	if len(orders) == 0 {
		return stat
	}

	// 3. 处理抢占到的订单；失败时回滚原状态，避免订单永久卡在处理中
	for _, o := range orders {
		eff, err := s.cancelOrderTx(ctx, o, 1)
		if err != nil {
			if errors.Is(err, errOrderAlreadyPaid) {
				// 已收款订单已流转为已支付（或补同步失败等待下轮重试），不可回滚覆盖
				log.Printf("[order-scheduler] 订单 %d 取消前反查到已收款，跳过取消", o.ID)
				continue
			}
			if errors.Is(err, errOrderStateConflict) {
				// 抢占态已被并发流转（如补同步先到置为已支付），不回滚不记失败
				log.Printf("[order-scheduler] 订单 %d 取消时状态已变更，跳过", o.ID)
				continue
			}
			log.Printf("[order-scheduler] 自动取消订单 %d 失败: %v", o.ID, err)
			s.rollbackStatus(ctx, o.ID, processingStatusCancel, 1)
			s.recordFailure(ctx, o.ID, "timeout_cancel", err.Error())
			stat.failed++
			continue
		}
		// 取消成功后回补库存与优惠券（失败入重试队列，不回滚已提交的取消）
		releaseCancelledOrder(ctx, s.db, eff)
		// 取消成功后关闭待支付支付单，防迟到回调误置已支付
		closePendingPayments(ctx, o.ID)
		// 兜底复核：取消事务提交与支付回调二阶段之间存在极窄竞态窗口，
		// 此刻若支付已成功则订单属于"已收款被取消"，需人工对账并发起退款
		s.auditPaidAfterCancel(ctx, o.ID)
		log.Printf("[order-scheduler] 订单 %d 超时未支付，已自动取消", o.ID)
		stat.cancelled++
	}
	return stat
}

// auditPaidAfterCancel 取消成功后的兜底复核（极窄竞态窗口的检测哨）
// 取消前反查（isOrderPaid）与取消事务的条件更新已挡住绝大多数竞态，
// 此处覆盖「反查通过后、事务提交前支付刚好成功且回调二阶段晚于取消提交」的残余窗口；
// 命中时仅能告警（库存/券已释放，自动恢复链路风险高），交由人工核对后主动退款
func (s *Scheduler) auditPaidAfterCancel(ctx context.Context, orderID int64) {
	if grpcclient.PaymentClient == nil {
		return
	}
	paid, err := isOrderPaid(ctx, orderID)
	if err != nil {
		// 支付服务不可达时无从复核，已有取消前反查与条件更新两道防线
		return
	}
	if paid {
		log.Printf("[CRITICAL] 订单 %d 取消后复核发现已收款（取消与支付回调竞态窗口），需人工对账并发起退款", orderID)
	}
}

// retryCancelReleases 重放「取消已提交、远端回补未成功」的收尾动作
// 取消事务提交后回补库存/优惠券重试仍失败时，落 task_type=cancel_release 的失败记录（订单已是终态 6，
// 不能像旧实现那样靠「取消整体失败回滚」来重试，否则会造成一物两卖）。
// 本任务按幂等号 cancel:{orderID} 重放：product 侧唯一键保证重复回补只生效一次，重放安全；
// 达 releaseMaxRetry 次仍未收敛的记录停止自动重放（每轮已打 [CRITICAL]，转人工按幂等槽位补做）。
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - orderStat: 本轮统计（落库判定依据）
func (s *Scheduler) retryCancelReleases(ctx context.Context) orderStat {
	var stat orderStat
	lockKey := "scheduler:order:cancel:release:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return stat
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	var failures []*model.SchedulerFailure
	if err := s.db.GetDb().WithContext(ctx).
		Where("task_type = ? AND status = ? AND retry_count < ?", taskTypeCancelRelease, 1, releaseMaxRetry).
		Order("id asc").
		Limit(releaseBatch).
		Find(&failures).Error; err != nil {
		log.Printf("[order-scheduler] 查询取消收尾回补失败记录出错: %v", err)
		stat.failed++
		return stat
	}

	for _, f := range failures {
		eff, err := loadCancelEffect(ctx, s.db, f.OrderID)
		if err != nil {
			log.Printf("[order-scheduler] 重放订单 %d 取消收尾时读取订单失败: %v", f.OrderID, err)
			s.incrementFailureRetry(ctx, f.ID, err.Error())
			stat.failed++
			continue
		}
		if err := runCancelEffects(ctx, eff); err != nil {
			s.incrementFailureRetry(ctx, f.ID, err.Error())
			log.Printf("[order-scheduler] 重放订单 %d 取消收尾回补仍失败: %v", f.OrderID, err)
			stat.failed++
			continue
		}
		s.markFailureStatus(ctx, f.ID, 2)
		stat.cancelReleased++
		log.Printf("[order-scheduler] 订单 %d 取消收尾回补重放成功", f.OrderID)
	}
	return stat
}

// retryRefundReleases 重放「退款已提交成功、远端回补未成功」的收尾动作
// 退款事务提交后回补库存/优惠券重试仍失败时，落 task_type=refund_release 的失败记录并带上
// biz_no=退款单号：同一订单可以有多笔部分退款，各自占独立幂等槽位，只按订单 ID 无法定位补哪一笔
// （取消收尾按订单整体重放，不需要该列）。
// 重放按退款单号取明细后走同一套幂等口径：product 侧回补唯一键含 biz_no，重复调用只生效一次；
// 退款单已转出成功态(5) 的记录（被拒/撤销）没有回补语义，直接标记已处理，不永久滞留队列。
// 达 releaseMaxRetry 次仍未收敛的记录停止自动重放，每轮已打 [CRITICAL] 供人工按幂等槽位补做。
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - orderStat: 本轮统计（落库判定依据）
func (s *Scheduler) retryRefundReleases(ctx context.Context) orderStat {
	var stat orderStat
	lockKey := "scheduler:order:refund:release:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return stat
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	var failures []*model.SchedulerFailure
	if err := s.db.GetDb().WithContext(ctx).
		Where("task_type = ? AND status = ? AND retry_count < ?", taskTypeRefundRelease, 1, releaseMaxRetry).
		Order("id asc").
		Limit(releaseBatch).
		Find(&failures).Error; err != nil {
		log.Printf("[order-scheduler] 查询退款收尾回补失败记录出错: %v", err)
		stat.failed++
		return stat
	}

	for _, f := range failures {
		if f.BizNo == "" {
			// 无幂等号无法定位回补哪一笔：标记放弃并留线索，不能整单重放（可能吞掉同订单其他退款的槽位）
			log.Printf("[CRITICAL] 退款收尾回补记录 %d（order_id=%d）缺退款单号，停止自动重放，需人工按退款单核对", f.ID, f.OrderID)
			s.markFailureStatus(ctx, f.ID, 3)
			stat.failed++
			continue
		}
		eff, err := loadRefundReleaseEffect(ctx, s.db, f.BizNo)
		if err != nil {
			log.Printf("[order-scheduler] 重放退款单 %s 收尾回补时读取失败: %v", f.BizNo, err)
			s.incrementFailureRetry(ctx, f.ID, err.Error())
			stat.failed++
			continue
		}
		if eff == nil {
			s.markFailureStatus(ctx, f.ID, 2)
			log.Printf("[order-scheduler] 退款单 %s 已不处于退款成功态，收尾回补记录标记为已处理", f.BizNo)
			continue
		}
		if err := runRefundEffects(ctx, eff); err != nil {
			s.incrementFailureRetry(ctx, f.ID, err.Error())
			log.Printf("[order-scheduler] 重放退款单 %s 收尾回补仍失败: %v", f.BizNo, err)
			stat.failed++
			continue
		}
		s.markFailureStatus(ctx, f.ID, 2)
		stat.refundReleased++
		log.Printf("[order-scheduler] 退款单 %s 收尾回补重放成功", f.BizNo)
	}
	return stat
}

// autoCompleteOrders 自动完成已发货超时的订单
//
// 参数：
//   - ctx: 上下文
//   - autoCompleteDays: 自动完成天数
//
// 返回值：
//   - orderStat: 本轮统计（落库判定依据）
func (s *Scheduler) autoCompleteOrders(ctx context.Context, autoCompleteDays int) orderStat {
	var stat orderStat
	lockKey := "scheduler:order:auto:complete:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return stat
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	// 1. 优先处理历史失败记录
	stat.merge(s.retryFailures(ctx, "auto_complete", autoCompleteDays))

	// 2. 原子抢占一批待自动完成订单（status=4 → 101 处理中）
	deadline := time.Now().AddDate(0, 0, -autoCompleteDays).Format("2006-01-02 15:04:05")
	orders := s.claimOrders(ctx, 4, processingStatusComplete, "deliver_time IS NOT NULL AND deliver_time < ?", deadline)
	if len(orders) == 0 {
		return stat
	}

	// 3. 处理抢占到的订单；失败时回滚原状态，避免订单永久卡在处理中
	remark := fmt.Sprintf("发货后%d天未确认收货，系统自动完成", autoCompleteDays)
	for _, o := range orders {
		if err := s.completeOrderTx(ctx, o, 4, processingStatusComplete, remark); err != nil {
			log.Printf("[order-scheduler] 自动完成订单 %d 失败: %v", o.ID, err)
			s.rollbackStatus(ctx, o.ID, processingStatusComplete, 4)
			s.recordFailure(ctx, o.ID, "auto_complete", err.Error())
			stat.failed++
			continue
		}
		log.Printf("[order-scheduler] 订单 %d 发货超过 %d 天未确认收货，已自动完成", o.ID, autoCompleteDays)
		stat.completed++
	}
	return stat
}

// claimOrders 原子抢占订单
// 通过 UPDATE ... LIMIT 将符合条件的订单更新为中间状态，然后查询这些订单进行处理
//
// 参数：
//   - ctx: 上下文
//   - fromStatus: 原状态
//   - processingStatus: 处理中状态（临时状态，避免重复处理）
//   - condition: 额外条件（带占位符）
//   - args: 条件参数
//
// 返回值：
//   - []*model.Order: 抢占到的订单列表
func (s *Scheduler) claimOrders(ctx context.Context, fromStatus, processingStatus int32, condition string, args ...interface{}) []*model.Order {
	db := s.db.GetDb().WithContext(ctx)

	// 先原子抢占一批订单
	totalArgs := append([]interface{}{fromStatus}, args...)
	sql := "UPDATE orders SET status = ?, updated_at = NOW() WHERE status = ?"
	if condition != "" {
		sql += " AND " + condition
	}
	sql += " LIMIT 100"
	fullArgs := append([]interface{}{processingStatus}, totalArgs...)
	if err := db.Exec(sql, fullArgs...).Error; err != nil {
		log.Printf("[order-scheduler] 抢占订单失败 (status %d -> %d): %v", fromStatus, processingStatus, err)
		return nil
	}

	// 查询本次抢占到的订单（仍处于处理中状态且满足原条件）
	var orders []*model.Order
	query := db.Where("status = ?", processingStatus)
	if condition != "" {
		query = query.Where(condition, args...)
	}
	if err := query.Order("id asc").Limit(100).Find(&orders).Error; err != nil {
		log.Printf("[order-scheduler] 查询已抢占订单失败 (status %d): %v", processingStatus, err)
		return nil
	}
	return orders
}

// cancelOrderTx 在事务中取消订单，返回提交后需回补的远端副作用清单
// 取消前反查支付单守卫：支付回调两阶段同步失败时订单仍停留待支付，
// 若直接取消会造成"已收款但被取消"的资损；反查到成功支付单则改走补同步并跳过取消；
// 支付服务不可达等不确定情况放弃本轮取消，下轮重试（宁可延迟取消，不可误取消已收款单）
// 库存/优惠券回补不在事务内发起：远端已提交的回补撤不回，本地若回滚就成了
// 「订单仍待支付而库存已放走」的一物两卖，故由调用方在提交成功后执行。
//
// 参数：
//   - ctx: 上下文
//   - o: 待取消订单
//   - fromStatus: 流转起点状态（真实原状态，用于状态日志）
//
// 返回值：
//   - *cancelEffect: 提交后需回补的清单（失败时为 nil）
//   - error: errOrderAlreadyPaid 表示订单已收款跳过取消（订单状态已被补同步或待下轮重试）；
//     errOrderStateConflict 表示抢占态已被并发流转（如补同步先到置为已支付），跳过且不记失败；其余为取消失败
func (s *Scheduler) cancelOrderTx(ctx context.Context, o *model.Order, fromStatus int32) (*cancelEffect, error) {
	if grpcclient.PaymentClient != nil {
		paid, pErr := isOrderPaid(ctx, o.ID)
		if pErr != nil {
			return nil, fmt.Errorf("取消前反查支付单失败: %w", pErr)
		}
		if !paid {
			// 本地没有成功支付单不等于用户没付：回调可能丢失或验签失败。
			// 渠道确认到已收款时，payment 服务已把订单补同步为已支付（100→2 合法），此处放弃取消
			paid = confirmPaidWithChannel(ctx, o.ID, s.cfg)
		}
		if paid {
			// 已收款：补同步订单为已支付（状态机已允许 100→2，与支付回调二阶段同接口，幂等）
			if _, uErr := grpcclient.OrderClient.UpdateOrderStatus(ctx, &order.UpdateOrderStatusReq{
				Id:           o.ID,
				Status:       2,
				OperatorType: "system",
				Remark:       "支付成功（超时取消前反查支付单补同步）",
			}); uErr != nil {
				// 补同步与支付回调二阶段可能同时命中同一订单：先到者已把状态置为已支付族，
				// 后到者必然报「状态已变更」。此处复核实际状态，只有仍停留未支付态才是真卡单，
				// 否则只是竞态下的重复补同步，误报 CRITICAL 会淹没真实的资损告警
				if synced, sErr := s.isPaidSynced(ctx, o.ID); sErr == nil && synced {
					log.Printf("[order-scheduler] 订单 %d 已由支付回调置为已支付，补同步无需重复执行", o.ID)
				} else {
					log.Printf("[CRITICAL] 订单 %d 已收款但补同步失败（下轮调度重试）: %v", o.ID, uErr)
				}
			} else {
				log.Printf("[order-scheduler] 订单 %d 已收款但状态未同步，已补为已支付并跳过取消", o.ID)
			}
			return nil, errOrderAlreadyPaid
		}
	}

	var eff *cancelEffect
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")
		// 条件更新防并发覆盖：仅当订单仍处于本实例抢占态(100)或回滚后的原状态(1)时生效。
		// 若并发场景下订单已被置为其他状态（如补同步先到置为已支付、用户已自行取消），
		// 直接整行 Save 会覆盖该状态造成资损（已收款被取消），此处必须中止
		res := tx.Model(&model.Order{}).
			Where("id = ? AND status IN ?", o.ID, []int32{processingStatusCancel, fromStatus}).
			Updates(map[string]interface{}{
				"status":        int32(6),
				"cancel_reason": "超时未支付自动取消",
				"cancel_time":   &now,
				"updated_at":    now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			o.Status = 6 // 仅同步内存副本供日志使用
			return errOrderStateConflict
		}
		o.Status = 6
		o.CancelReason = "超时未支付自动取消"
		o.CancelTime = &now
		o.UpdatedAt = now

		var items []*model.OrderItem
		if err := tx.Where("order_id = ?", o.ID).Find(&items).Error; err != nil {
			return err
		}
		// 与 C 端取消/后台取消共用整单回补清单：此前本路径只回补主库存，
		// 命中秒杀的超时单会把活动池扣减永久悬空（下单时扣了、取消时不补）
		eff = &cancelEffect{OrderID: o.ID, Items: items, CouponID: o.CouponID}

		log := &model.OrderStatusLog{
			OrderID:      o.ID,
			FromStatus:   fromStatus,
			ToStatus:     6,
			OperatorType: "system",
			OperatorID:   0,
			Remark:       "超时未支付自动取消",
			CreatedAt:    now,
		}
		return tx.Create(log).Error
	})
	if err != nil {
		return nil, err
	}
	return eff, nil
}

// isOrderPaid 反查支付服务判断订单是否已收款成功（仅 status=2 支付成功视为已支付）
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - bool: 是否已收款
//   - error: 支付服务不可达等不确定错误；codes.NotFound 表示无成功支付单，返回 nil,false
func isOrderPaid(ctx context.Context, orderID int64) (bool, error) {
	resp, err := grpcclient.PaymentClient.GetPayment(ctx, &payment.GetPaymentReq{OrderId: orderID})
	if err == nil {
		return resp != nil, nil
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
		return false, nil
	}
	return false, err
}

// isPaidSynced 复核订单是否已处于收款后的状态（2已支付/3备货中/4配送中/5已完成/7退款中/8已退款）
// 用于区分「补同步被并发抢先」与「订单确实仍停留在未支付态」：前者无需告警，后者才是需要人工介入的卡单
//
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - bool: 订单是否已同步为收款后状态
//   - error: 查询失败错误（调用方按未同步处理，保留 CRITICAL 告警）
func (s *Scheduler) isPaidSynced(ctx context.Context, orderID int64) (bool, error) {
	var count int64
	err := s.db.GetDb().WithContext(ctx).Model(&model.Order{}).
		Where("id = ? AND status IN ?", orderID, []int32{2, 3, 4, 5, 7, 8}).
		Count(&count).Error
	return count > 0, err
}

// completeOrderTx 在事务中完成订单
// 条件更新防并发覆盖：仅当订单仍处于抢占态或回滚后的原状态时生效，
// 防止把并发流转后的状态（如退款中 7）整行覆盖为已完成
//
// 参数：
//   - ctx: 上下文
//   - o: 待完成订单
//   - fromStatus: 流转起点状态（真实原状态，用于状态日志）
//   - processingStatus: 本任务对应的抢占处理中状态（配送 101 / 自提 102/103）
//   - remark: 状态日志备注（配送单与自提单文案不同，由调用方构造）
//
// 返回值：
//   - error: 错误信息
func (s *Scheduler) completeOrderTx(ctx context.Context, o *model.Order, fromStatus, processingStatus int32, remark string) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")
		res := tx.Model(&model.Order{}).
			Where("id = ? AND status IN ?", o.ID, []int32{processingStatus, fromStatus}).
			Updates(map[string]interface{}{
				"status":        int32(5),
				"complete_time": &now,
				"updated_at":    now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("订单 %d 状态已变更，自动完成中止", o.ID)
		}
		o.Status = 5
		o.CompleteTime = &now
		o.UpdatedAt = now

		statusLog := &model.OrderStatusLog{
			OrderID:      o.ID,
			FromStatus:   fromStatus,
			ToStatus:     5,
			OperatorType: "system",
			OperatorID:   0,
			Remark:       remark,
			CreatedAt:    now,
		}
		return tx.Create(statusLog).Error
	})
}

// rollbackStatus 将仍处于处理中状态的订单回滚为原状态
// 仅当订单仍处于 processing 状态时生效，避免覆盖并发场景下的其他流转
//
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - processingStatus: 处理中状态
//   - fromStatus: 原状态
func (s *Scheduler) rollbackStatus(ctx context.Context, orderID int64, processingStatus, fromStatus int32) {
	if err := s.db.GetDb().WithContext(ctx).Exec(
		"UPDATE orders SET status = ?, updated_at = NOW() WHERE id = ? AND status = ?",
		fromStatus, orderID, processingStatus,
	).Error; err != nil {
		log.Printf("[order-scheduler] 回滚订单 %d 状态 (%d -> %d) 失败: %v", orderID, processingStatus, fromStatus, err)
	}
}

// retryFailures 重试历史失败记录
//
// 参数：
//   - ctx: 上下文
//   - taskType: 任务类型
//   - autoCompleteDays: 发货后自动完成天数，仅 auto_complete 类型使用
//
// 返回值：
//   - orderStat: 本轮统计（按 taskType 归集到对应动作计数，落库判定依据）
func (s *Scheduler) retryFailures(ctx context.Context, taskType string, autoCompleteDays int) orderStat {
	var stat orderStat
	var failures []*model.SchedulerFailure
	if err := s.db.GetDb().WithContext(ctx).
		Where("task_type = ? AND status = ? AND retry_count < ?", taskType, 1, 5).
		Order("id asc").
		Limit(100).
		Find(&failures).Error; err != nil {
		log.Printf("[order-scheduler] 查询 %s 失败记录出错: %v", taskType, err)
		stat.failed++
		return stat
	}

	for _, f := range failures {
		var o model.Order
		if err := s.db.GetDb().WithContext(ctx).First(&o, f.OrderID).Error; err != nil {
			log.Printf("[order-scheduler] 重试时查询订单 %d 失败: %v", f.OrderID, err)
			s.markFailureStatus(ctx, f.ID, 3)
			continue
		}

		// 历史遗留的处理中状态（回滚未执行的异常场景）：先恢复原状态再走正常重试判断
		switch {
		case taskType == "timeout_cancel" && o.Status == processingStatusCancel:
			s.rollbackStatus(ctx, o.ID, processingStatusCancel, 1)
			if err := s.db.GetDb().WithContext(ctx).First(&o, f.OrderID).Error; err != nil {
				log.Printf("[order-scheduler] 重试时回查订单 %d 失败: %v", f.OrderID, err)
				continue
			}
		case taskType == "auto_complete" && o.Status == processingStatusComplete:
			s.rollbackStatus(ctx, o.ID, processingStatusComplete, 4)
			if err := s.db.GetDb().WithContext(ctx).First(&o, f.OrderID).Error; err != nil {
				log.Printf("[order-scheduler] 重试时回查订单 %d 失败: %v", f.OrderID, err)
				continue
			}
		case taskType == "pickup_complete" && (o.Status == processingStatusPickup2 || o.Status == processingStatusPickup3):
			from := int32(2)
			if o.Status == processingStatusPickup3 {
				from = 3
			}
			s.rollbackStatus(ctx, o.ID, o.Status, from)
			if err := s.db.GetDb().WithContext(ctx).First(&o, f.OrderID).Error; err != nil {
				log.Printf("[order-scheduler] 重试时回查订单 %d 失败: %v", f.OrderID, err)
				continue
			}
		}

		var err error
		var eff *cancelEffect
		switch taskType {
		case "timeout_cancel":
			if o.Status != 1 {
				s.markFailureStatus(ctx, f.ID, 2)
				continue
			}
			eff, err = s.cancelOrderTx(ctx, &o, 1)
		case "auto_complete":
			if o.Status != 4 {
				s.markFailureStatus(ctx, f.ID, 2)
				continue
			}
			err = s.completeOrderTx(ctx, &o, 4, processingStatusComplete, fmt.Sprintf("发货后%d天未确认收货，系统自动完成", autoCompleteDays))
		case "pickup_complete":
			if o.Status != 2 && o.Status != 3 {
				s.markFailureStatus(ctx, f.ID, 2)
				continue
			}
			processing := processingStatusPickup2
			if o.Status == 3 {
				processing = processingStatusPickup3
			}
			err = s.completeOrderTx(ctx, &o, o.Status, processing, fmt.Sprintf("支付后%d天未取货，系统自动完成", autoCompleteDays))
		}

		if err != nil {
			if errors.Is(err, errOrderAlreadyPaid) {
				// 已收款订单跳过取消，失败记录视为已解决（补同步已另行发起）
				s.markFailureStatus(ctx, f.ID, 2)
				log.Printf("[order-scheduler] 重试 %s 订单 %d 时反查到已收款，已跳过", taskType, f.OrderID)
				continue
			}
			if errors.Is(err, errOrderStateConflict) {
				// 抢占态已被并发流转（如补同步先到置为已支付），失败记录视为已解决
				s.markFailureStatus(ctx, f.ID, 2)
				log.Printf("[order-scheduler] 重试 %s 订单 %d 时状态已变更，已跳过", taskType, f.OrderID)
				continue
			}
			s.incrementFailureRetry(ctx, f.ID, err.Error())
			log.Printf("[order-scheduler] 重试 %s 订单 %d 仍失败: %v", taskType, f.OrderID, err)
			stat.failed++
			continue
		}
		s.markFailureStatus(ctx, f.ID, 2)
		// 取消重试成功后回补库存与优惠券（非 timeout_cancel 的 eff 为 nil，内部直接跳过）
		releaseCancelledOrder(ctx, s.db, eff)
		// 成功计数按任务类型归集，与上面的 switch 同一套取值
		switch taskType {
		case "timeout_cancel":
			s.auditPaidAfterCancel(ctx, f.OrderID)
			stat.cancelled++
		case "auto_complete":
			stat.completed++
		default: // pickup_complete
			stat.pickup++
		}
		log.Printf("[order-scheduler] 重试 %s 订单 %d 成功", taskType, f.OrderID)
	}
	return stat
}

// autoCompletePickupOrders 自提单超时自动完成
// 场景：用户支付后长期未到店取货、社区忘记核销 → 订单永久停留在待取货状态，库存永久占用。
// 支付后超过 autoCompleteDays 天未核销的自提单（delivery_type=1, status∈{2,3}）自动置为已完成。
// 与 autoCompleteOrders 同机制：失败记录重试 + 原子抢占（中间状态可回溯原状态）+ 失败回滚
//
// 参数：
//   - ctx: 上下文
//   - autoCompleteDays: 超时天数（与配送单共用配置）
//
// 返回值：
//   - orderStat: 本轮统计（落库判定依据）
func (s *Scheduler) autoCompletePickupOrders(ctx context.Context, autoCompleteDays int) orderStat {
	var stat orderStat
	lockKey := "scheduler:order:pickup:complete:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return stat
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	// 1. 优先处理历史失败记录
	stat.merge(s.retryFailures(ctx, "pickup_complete", autoCompleteDays))

	// 2. 按原状态分别原子抢占（自提单原状态可能为 2 已支付 / 3 备货中，
	//    拆分处理中状态使 recoverStuckOrders 兜底恢复时可还原真实原状态）
	deadline := time.Now().AddDate(0, 0, -autoCompleteDays).Format("2006-01-02 15:04:05")
	cond := "delivery_type = 1 AND pay_time IS NOT NULL AND pay_time < ?"
	remark := fmt.Sprintf("支付后%d天未取货，系统自动完成", autoCompleteDays)
	for _, p := range []struct{ from, processing int32 }{
		{2, processingStatusPickup2},
		{3, processingStatusPickup3},
	} {
		orders := s.claimOrders(ctx, p.from, p.processing, cond, deadline)
		for _, o := range orders {
			if err := s.completeOrderTx(ctx, o, p.from, p.processing, remark); err != nil {
				log.Printf("[order-scheduler] 自提单 %d 自动完成失败: %v", o.ID, err)
				s.rollbackStatus(ctx, o.ID, p.processing, p.from)
				s.recordFailure(ctx, o.ID, "pickup_complete", err.Error())
				stat.failed++
				continue
			}
			log.Printf("[order-scheduler] 自提单 %d 支付超过 %d 天未核销，已自动完成", o.ID, autoCompleteDays)
			stat.pickup++
		}
	}
	return stat
}

// cleanupExportTasks 清理过期导出任务（文件保留 3 天，到期删文件 + 删记录）
// 每轮最多清 200 条（CleanupExpired 内部限额），扫描走 expires_at 索引，量级极小；
// 服务未注入（测试环境）时跳过
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - orderStat: 本轮统计（落库判定依据）
func (s *Scheduler) cleanupExportTasks(ctx context.Context) orderStat {
	var stat orderStat
	if s.exportTaskService == nil {
		return stat
	}
	if n := s.exportTaskService.CleanupExpired(ctx); n > 0 {
		log.Printf("[order-scheduler] 已清理 %d 个过期导出任务", n)
		stat.exportCleaned = n
	}
	return stat
}

// autoApproveStaleRefunds 退款超时自动审批
// 场景：用户申请退款后管理员长期不处理 → 退款单永久停留在待审核(status=1)。
// 超过 72h 未处理的退款申请自动同意并发起退款，保障消费者权益。
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - orderStat: 本轮统计（落库判定依据）
func (s *Scheduler) autoApproveStaleRefunds(ctx context.Context) orderStat {
	var stat orderStat
	if s.refundService == nil {
		return stat
	}
	lockKey := "scheduler:order:refund:auto:approve:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return stat
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	const staleHours = 72
	deadline := time.Now().Add(-staleHours * time.Hour).Format("2006-01-02 15:04:05")
	var refunds []*model.Refund
	if err := s.db.GetDb().WithContext(ctx).
		Where("status = 1 AND created_at < ?", deadline).
		Order("id asc").Limit(20).Find(&refunds).Error; err != nil {
		log.Printf("[order-scheduler] 查询超时退款申请失败: %v", err)
		stat.failed++
		return stat
	}

	for _, r := range refunds {
		err := s.refundService.ApproveRefund(ctx, &order.ApproveRefundReq{
			Id:         r.ID,
			OperatorId: 0,
		})
		if err != nil {
			log.Printf("[order-scheduler] 自动审批退款单 %d 失败: %v", r.ID, err)
			stat.failed++
			continue
		}
		log.Printf("[order-scheduler] 退款单 %d 超过 %dh 未处理，已自动同意", r.ID, staleHours)
		stat.approved++
	}
	return stat
}

// recordFailure 记录处理失败的订单
// 按订单整体重试的任务（超时取消/自动完成等）不带幂等号，biz_no 留空
//
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - taskType: 任务类型
//   - errMsg: 错误信息
func (s *Scheduler) recordFailure(ctx context.Context, orderID int64, taskType string, errMsg string) {
	recordSchedulerFailure(ctx, s.db, orderID, taskType, "", errMsg)
}

// markFailureStatus 更新失败记录状态
//
// 参数：
//   - ctx: 上下文
//   - id: 失败记录 ID
//   - status: 目标状态
func (s *Scheduler) markFailureStatus(ctx context.Context, id int64, status int32) {
	now := time.Now().Format("2006-01-02 15:04:05")
	if err := s.db.GetDb().WithContext(ctx).
		Model(&model.SchedulerFailure{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     status,
			"updated_at": now,
		}).Error; err != nil {
		log.Printf("[order-scheduler] 更新失败记录 %d 状态出错: %v", id, err)
	}
}

// incrementFailureRetry 增加失败记录重试次数并更新错误信息
//
// 参数：
//   - ctx: 上下文
//   - id: 失败记录 ID
//   - errMsg: 最新错误信息
func (s *Scheduler) incrementFailureRetry(ctx context.Context, id int64, errMsg string) {
	now := time.Now().Format("2006-01-02 15:04:05")
	if err := s.db.GetDb().WithContext(ctx).
		Model(&model.SchedulerFailure{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"retry_count": gorm.Expr("retry_count + 1"),
			"error_msg":   errMsg,
			"updated_at":  now,
		}).Error; err != nil {
		log.Printf("[order-scheduler] 增加失败记录 %d 重试次数出错: %v", id, err)
	}
}

// releaseLockScript 分布式锁原子释放脚本：仅当锁值与持有者写入的值一致时才删除，
// 防止任务执行超过 TTL、锁已被其他实例重新获取时误删他人锁
var releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0
`)

// acquireLock 获取 Redis 分布式锁
//
// 参数：
//   - ctx: 上下文
//   - key: 锁键
//   - ttl: 锁过期时间
//
// 返回值：
//   - string: 获取成功返回锁持有者标识（写入的随机值），失败返回空串
func (s *Scheduler) acquireLock(ctx context.Context, key string, ttl time.Duration) string {
	value := strconv.FormatInt(time.Now().UnixNano(), 10)
	ok, err := s.redisClient.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		log.Printf("[order-scheduler] 获取锁 %s 失败: %v", key, err)
		return ""
	}
	if !ok {
		return ""
	}
	return value
}

// releaseLock 释放 Redis 分布式锁（Lua 原子比较锁值后删除，见 releaseLockScript）
//
// 参数：
//   - ctx: 上下文
//   - key: 锁键
//   - value: acquireLock 返回的持有者标识
func (s *Scheduler) releaseLock(ctx context.Context, key, value string) {
	if err := releaseLockScript.Run(ctx, s.redisClient, []string{key}, value).Err(); err != nil {
		log.Printf("[order-scheduler] 释放锁 %s 失败: %v", key, err)
	}
}
