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
// 参数：
//   - ctx: 上下文
func (s *Scheduler) runOnce(ctx context.Context) {
	if s.payment.cfg.ReconcileEnabled() {
		s.retryFailures(ctx)
		s.reconcilePending(ctx)
		s.detectStuck(ctx)
	}
	s.cleanupNotifyLogs(ctx)
}

// reconcilePending 回查待支付单（P0-A）
// 扫描条件与单条处理都受「起扫年龄 / 渠道时效 / 节流间隔 / 累计上限」四道约束，
// 保证正常回调有足够时间先行完成，异常单又不会每分钟打扰渠道一次
// 参数：
//   - ctx: 上下文
func (s *Scheduler) reconcilePending(ctx context.Context) {
	defer s.recoverTask("reconcilePending")

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
		return
	}
	for i := range rows {
		s.reconcileOne(ctx, &rows[i], false)
	}
}

// detectStuck 检出资金挂账（P0-C）
// 已关闭（status=5）且从未拿到渠道交易号的支付单，若渠道侧其实收了钱，
// 订单早已取消、库存与优惠券已回补，系统无法自行挽回，只能产出人工退款待办
// 参数：
//   - ctx: 上下文
func (s *Scheduler) detectStuck(ctx context.Context) {
	defer s.recoverTask("detectStuck")

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
		Where("status = 5 AND transaction_id = '' AND pay_type IN ? AND updated_at >= ?", []int32{1, 2, 5, 6}, updatedAfter).
		Where("reconcile_count < ?", config.ReconcileInt(r.MaxReconcileCount, 24)).
		Where("(last_reconciled_at IS NULL OR last_reconciled_at < ?)", throttleBefore).
		Order("updated_at asc").
		Limit(config.ReconcileInt(r.BatchSize, 100)).
		Find(&rows).Error; err != nil {
		log.Printf("[payment-scheduler] 扫描已关闭支付单失败: %v", err)
		return
	}
	for i := range rows {
		// 渠道已明确回答过「没收到钱」的关闭单，事实不会改变，不再重复打扰渠道
		if s.payment.hasNoPaymentConfirmed(ctx, rows[i].PaymentNo) {
			continue
		}
		s.reconcileOne(ctx, &rows[i], true)
	}
}

// reconcileOne 对单笔支付单发起渠道核实并落地结论
// 参数：
//   - ctx: 上下文
//   - p: 支付单
//   - closed: 该单是否已关闭（true 时只会产出挂账流水，不会尝试改状态）
func (s *Scheduler) reconcileOne(ctx context.Context, p *model.Payment, closed bool) {
	if !withinReconcileWindow(s.payment.cfg, p) {
		return
	}
	res, err := s.payment.queryChannelOrder(ctx, p)
	if err != nil || res == nil {
		// 渠道不可达：计入回查次数但不动状态，下轮再来
		s.markReconciled(ctx, p)
		s.payment.recordFailure(ctx, p, reconcileTaskType(closed), fmt.Sprintf("渠道查询失败: %v", err))
		return
	}

	switch res.State {
	case ChannelStatePaid:
		if closed {
			s.markReconciled(ctx, p)
			s.payment.recordStuckPayment(ctx, p, res)
			return
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
			return
		}
		log.Printf("[payment-scheduler] 回调丢失已捞回 payment_no=%s 渠道状态=%s", p.PaymentNo, res.Raw)
		return

	case ChannelStateNotExist:
		s.markReconciled(ctx, p)
		// 两轮确认：首轮只留痕，避免渠道瞬时数据延迟把一笔在途支付判死
		if closed {
			s.payment.recordReconcileLog(ctx, p, model.NotifyResultNotExist, "渠道无此交易")
			return
		}
		if s.payment.hasReconcileNotExist(ctx, p.PaymentNo) {
			s.closePaymentLocally(ctx, p, "渠道连续两轮无此交易，关闭本地支付单")
			return
		}
		s.payment.recordReconcileLog(ctx, p, model.NotifyResultNotExist, "渠道无此交易（首轮，等待二次确认）")
		return

	case ChannelStateClosed:
		s.markReconciled(ctx, p)
		if !closed {
			s.closePaymentLocally(ctx, p, "渠道交易已关闭，本地支付单同步关闭")
		} else {
			s.payment.recordReconcileLog(ctx, p, model.NotifyResultClosed, "渠道交易已关闭")
		}
		return

	default:
		// 在途：只推进节流指针，等待用户完成支付或回调到达
		s.markReconciled(ctx, p)
		s.payment.recordReconcileLog(ctx, p, model.NotifyResultNotPaid, fmt.Sprintf("渠道未收款（%s）", res.Raw))
		if !closed && s.payment.reconcileExhausted(ctx, p) {
			s.payment.recordGiveUp(ctx, p)
		}
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
		Updates(map[string]interface{}{"status": int32(5), "updated_at": now})
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

// retryFailures 重试历史对账失败记录
// 处理前提：失败记录对应的支付单仍处于该任务关注的状态；已被其他路径收敛则标记为已处理
//
// 重试路径与两条扫描共用「分布式锁 + 节流间隔 + 累计上限」三道约束：
// 少了任何一道，渠道故障时它会退化成「每分钟绕过节流直打渠道」，
// 且同单多条待重试行会在同一轮里各查一次渠道、把 5 次重试预算提前吃光
// 参数：
//   - ctx: 上下文
func (s *Scheduler) retryFailures(ctx context.Context) {
	defer s.recoverTask("retryFailures")

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
		s.reconcileOne(ctx, &p, !(p.Status == 1 && f.TaskType == model.TaskReconcilePending))
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
func (s *Scheduler) cleanupNotifyLogs(ctx context.Context) {
	retentionDays := config.ReconcileInt(s.payment.cfg.Business.PaymentReconcile.NotifyLogRetentionDays, 30)
	lockKey := fmt.Sprintf("scheduler:payment:notify:cleanup:%s", time.Now().Format("2006-01-02"))
	if s.acquireLock(ctx, lockKey, 25*time.Hour) == "" {
		return
	}
	deadline := time.Now().AddDate(0, 0, -retentionDays).Format(timeLayout)
	res := s.payment.db.GetDb().WithContext(ctx).
		Where("created_at < ?", deadline).
		Delete(&model.NotifyLog{})
	if res.Error != nil {
		log.Printf("[payment-scheduler] 清理通知流水失败: %v", res.Error)
		return
	}
	if res.RowsAffected > 0 {
		log.Printf("[payment-scheduler] 已清理 %d 条超过 %d 天的通知流水", res.RowsAffected, retentionDays)
	}
}

// recoverTask 任务级 panic 兜底：对账属于旁路能力，任何一笔的异常都不应终止整个调度协程
// 参数：
//   - name: 任务名，用于日志定位
func (s *Scheduler) recoverTask(name string) {
	if r := recover(); r != nil {
		log.Printf("[payment-scheduler] 任务 %s 异常已恢复: %v", name, r)
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
