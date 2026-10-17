package service

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/product"
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
)

// stuckRecoverAfter 处理中状态停留超过该时长视为进程异常中断，兜底恢复原状态
const stuckRecoverAfter = 10 * time.Minute

// Scheduler 订单定时任务调度器
// 负责超时未支付订单自动取消、发货后超时自动完成、卡单退款自动重试
// 采用 Redis 分布式锁 + 数据库原子抢占 + 失败回滚重试机制
type Scheduler struct {
	db            *mysql.Instance
	redisClient   *redis.Client
	refundService *RefundService
}

// NewScheduler 创建订单定时任务调度器
//
// 参数：
//   - db: MySQL 实例
//   - redisClient: Redis 客户端，用于分布式锁
//   - refundService: 退款服务，用于卡单退款自动重试
//
// 返回值：
//   - *Scheduler: 调度器实例
func NewScheduler(db *mysql.Instance, redisClient *redis.Client, refundService *RefundService) *Scheduler {
	return &Scheduler{
		db:            db,
		redisClient:   redisClient,
		refundService: refundService,
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
func (s *Scheduler) runOnce(ctx context.Context, orderTimeout int, autoCompleteDays int) {
	// 兜底恢复：进程异常中断导致回滚未执行的订单，先恢复原状态再进入本轮调度
	s.recoverStuckOrders(ctx)
	s.cancelTimeoutOrders(ctx, orderTimeout)
	s.autoCompleteOrders(ctx, autoCompleteDays)
	s.autoCompletePickupOrders(ctx, autoCompleteDays)
	s.retryStuckRefunds(ctx)
	s.autoApproveStaleRefunds(ctx)
}

// retryStuckRefunds 自动重试卡在"退款中"(status=4) 超过阈值的退款单
// 场景：同意退款后 ProcessRefund 失败/进程中断，退款单永久停留在 4 且库存未回补。
// 重试以退款单自身的 payment_no 重新发起渠道退款，成功后走统一收尾逻辑。
// 参数：
//   - ctx: 上下文
func (s *Scheduler) retryStuckRefunds(ctx context.Context) {
	if s.refundService == nil {
		return
	}
	lockKey := "scheduler:order:refund:retry:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	// 仅重试停留超过 stuckRecoverAfter 的退款单，避免与刚发起的退款竞态
	deadline := time.Now().Add(-stuckRecoverAfter).Format("2006-01-02 15:04:05")
	var refunds []*model.Refund
	if err := s.db.GetDb().WithContext(ctx).
		Where("status = ? AND updated_at < ?", 4, deadline).
		Order("id asc").Limit(20).Find(&refunds).Error; err != nil {
		log.Printf("[order-scheduler] 查询卡单退款失败: %v", err)
		return
	}
	for _, r := range refunds {
		err := s.refundService.RetryRefund(ctx, &order.RetryRefundReq{Id: r.ID})
		if err != nil {
			log.Printf("[order-scheduler] 自动重试退款单 %d 失败: %v", r.ID, err)
			continue
		}
		log.Printf("[order-scheduler] 退款单 %d 卡单重试成功，已置为已退款", r.ID)
	}
}

// recoverStuckOrders 兜底扫描：将停留在处理中状态超过阈值的订单批量恢复为原状态
// 覆盖进程被 kill 导致 claimOrders 后回滚逻辑未执行的场景
// 参数：
//   - ctx: 上下文
func (s *Scheduler) recoverStuckOrders(ctx context.Context) {
	deadline := time.Now().Add(-stuckRecoverAfter).Format("2006-01-02 15:04:05")
	pairs := []struct{ processing, from int32 }{
		{processingStatusCancel, 1},
		{processingStatusComplete, 4},
	}
	for _, p := range pairs {
		res := s.db.GetDb().WithContext(ctx).Exec(
			"UPDATE orders SET status = ?, updated_at = NOW() WHERE status = ? AND updated_at < ?",
			p.from, p.processing, deadline,
		)
		if res.Error != nil {
			log.Printf("[order-scheduler] 兜底恢复处理中订单 (status %d -> %d) 失败: %v", p.processing, p.from, res.Error)
			continue
		}
		if res.RowsAffected > 0 {
			log.Printf("[order-scheduler] 兜底恢复 %d 个停留超时的处理中订单 (status %d -> %d)", res.RowsAffected, p.processing, p.from)
		}
	}
}

// cancelTimeoutOrders 取消超时未支付订单
//
// 参数：
//   - ctx: 上下文
//   - orderTimeout: 超时时间（秒）
func (s *Scheduler) cancelTimeoutOrders(ctx context.Context, orderTimeout int) {
	lockKey := "scheduler:order:timeout:cancel:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	// 1. 优先处理历史失败记录
	s.retryFailures(ctx, "timeout_cancel", 0)

	// 2. 原子抢占一批待取消订单（status=1 → 100 处理中）
	orders := s.claimOrders(ctx, 1, processingStatusCancel, "created_at < ?", time.Now().Add(-time.Duration(orderTimeout)*time.Second).Format("2006-01-02 15:04:05"))
	if len(orders) == 0 {
		return
	}

	// 3. 处理抢占到的订单；失败时回滚原状态，避免订单永久卡在处理中
	for _, o := range orders {
		if err := s.cancelOrderTx(ctx, o, 1); err != nil {
			log.Printf("[order-scheduler] 自动取消订单 %d 失败: %v", o.ID, err)
			s.rollbackStatus(ctx, o.ID, processingStatusCancel, 1)
			s.recordFailure(ctx, o.ID, "timeout_cancel", err.Error())
			continue
		}
		// 取消成功后关闭待支付支付单，防迟到回调误置已支付
		closePendingPayments(ctx, o.ID)
		log.Printf("[order-scheduler] 订单 %d 超时未支付，已自动取消", o.ID)
	}
}

// autoCompleteOrders 自动完成已发货超时的订单
//
// 参数：
//   - ctx: 上下文
//   - autoCompleteDays: 自动完成天数
func (s *Scheduler) autoCompleteOrders(ctx context.Context, autoCompleteDays int) {
	lockKey := "scheduler:order:auto:complete:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	// 1. 优先处理历史失败记录
	s.retryFailures(ctx, "auto_complete", autoCompleteDays)

	// 2. 原子抢占一批待自动完成订单（status=4 → 101 处理中）
	deadline := time.Now().AddDate(0, 0, -autoCompleteDays).Format("2006-01-02 15:04:05")
	orders := s.claimOrders(ctx, 4, processingStatusComplete, "deliver_time IS NOT NULL AND deliver_time < ?", deadline)
	if len(orders) == 0 {
		return
	}

	// 3. 处理抢占到的订单；失败时回滚原状态，避免订单永久卡在处理中
	for _, o := range orders {
		if err := s.completeOrderTx(ctx, o, 4, autoCompleteDays); err != nil {
			log.Printf("[order-scheduler] 自动完成订单 %d 失败: %v", o.ID, err)
			s.rollbackStatus(ctx, o.ID, processingStatusComplete, 4)
			s.recordFailure(ctx, o.ID, "auto_complete", err.Error())
			continue
		}
		log.Printf("[order-scheduler] 订单 %d 发货超过 %d 天未确认收货，已自动完成", o.ID, autoCompleteDays)
	}
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

// cancelOrderTx 在事务中取消订单并释放库存
//
// 参数：
//   - ctx: 上下文
//   - o: 待取消订单
//   - fromStatus: 流转起点状态（真实原状态，用于状态日志）
//
// 返回值：
//   - error: 错误信息
func (s *Scheduler) cancelOrderTx(ctx context.Context, o *model.Order, fromStatus int32) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")
		o.Status = 6
		o.CancelReason = "超时未支付自动取消"
		o.CancelTime = &now
		o.UpdatedAt = now
		if err := tx.Save(o).Error; err != nil {
			return err
		}

		var items []*model.OrderItem
		if err := tx.Where("order_id = ?", o.ID).Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			if _, err := grpcclient.ProductClient.ReleaseStock(ctx, &product.ReleaseStockReq{
				SkuId:    item.SkuID,
				Quantity: item.Quantity,
				OrderId:  o.ID,
				BizNo:    fmt.Sprintf("cancel:%d", o.ID),
			}); err != nil {
				return fmt.Errorf("释放库存失败: %w", err)
			}
		}

		// 回补优惠券：与 CancelOrder/cancelOrderInTx 同口径，防止超时取消吞券
		if o.CouponID > 0 {
			if _, err := grpcclient.ProductClient.ReleaseCoupon(ctx, &product.ReleaseCouponReq{OrderId: o.ID}); err != nil {
				return fmt.Errorf("回补优惠券失败: %w", err)
			}
		}

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
}

// completeOrderTx 在事务中完成订单
//
// 参数：
//   - ctx: 上下文
//   - o: 待完成订单
//   - fromStatus: 流转起点状态（真实原状态，用于状态日志）
//   - autoCompleteDays: 自动完成天数，用于备注
//
// 返回值：
//   - error: 错误信息
func (s *Scheduler) completeOrderTx(ctx context.Context, o *model.Order, fromStatus int32, autoCompleteDays int) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")
		o.Status = 5
		o.CompleteTime = &now
		o.UpdatedAt = now
		if err := tx.Save(o).Error; err != nil {
			return err
		}

		statusLog := &model.OrderStatusLog{
			OrderID:      o.ID,
			FromStatus:   fromStatus,
			ToStatus:     5,
			OperatorType: "system",
			OperatorID:   0,
			Remark:       fmt.Sprintf("发货后%d天未确认收货，系统自动完成", autoCompleteDays),
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
func (s *Scheduler) retryFailures(ctx context.Context, taskType string, autoCompleteDays int) {
	var failures []*model.SchedulerFailure
	if err := s.db.GetDb().WithContext(ctx).
		Where("task_type = ? AND status = ? AND retry_count < ?", taskType, 1, 5).
		Order("id asc").
		Limit(100).
		Find(&failures).Error; err != nil {
		log.Printf("[order-scheduler] 查询 %s 失败记录出错: %v", taskType, err)
		return
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
		}

		var err error
		switch taskType {
		case "timeout_cancel":
			if o.Status != 1 {
				s.markFailureStatus(ctx, f.ID, 2)
				continue
			}
			err = s.cancelOrderTx(ctx, &o, 1)
		case "auto_complete":
			if o.Status != 4 {
				s.markFailureStatus(ctx, f.ID, 2)
				continue
			}
			err = s.completeOrderTx(ctx, &o, 4, autoCompleteDays)
		}

		if err != nil {
			s.incrementFailureRetry(ctx, f.ID, err.Error())
			log.Printf("[order-scheduler] 重试 %s 订单 %d 仍失败: %v", taskType, f.OrderID, err)
			continue
		}
		s.markFailureStatus(ctx, f.ID, 2)
		log.Printf("[order-scheduler] 重试 %s 订单 %d 成功", taskType, f.OrderID)
	}
}

// autoCompletePickupOrders 自提单超时自动完成
// 场景：用户支付后长期未到店取货、社区忘记核销 → 订单永久停留在待取货状态，库存永久占用。
// 支付后超过 autoCompleteDays 天未核销的自提单（delivery_type=1, status∈{2,3}）自动置为已完成。
// 参数：
//   - ctx: 上下文
//   - autoCompleteDays: 超时天数（与配送单共用配置）
func (s *Scheduler) autoCompletePickupOrders(ctx context.Context, autoCompleteDays int) {
	lockKey := "scheduler:order:pickup:complete:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	deadline := time.Now().AddDate(0, 0, -autoCompleteDays).Format("2006-01-02 15:04:05")
	var orders []*model.Order
	if err := s.db.GetDb().WithContext(ctx).
		Where("delivery_type = 1 AND status IN ? AND pay_time IS NOT NULL AND pay_time < ?", []int32{2, 3}, deadline).
		Order("id asc").Limit(100).Find(&orders).Error; err != nil {
		log.Printf("[order-scheduler] 查询超时自提单失败: %v", err)
		return
	}

	for _, o := range orders {
		now := time.Now().Format("2006-01-02 15:04:05")
		fromStatus := o.Status
		res := s.db.GetDb().WithContext(ctx).Model(&model.Order{}).
			Where("id = ? AND status IN ?", o.ID, []int32{2, 3}).
			Updates(map[string]interface{}{
				"status":        int32(5),
				"complete_time": &now,
				"updated_at":    now,
			})
		if res.Error != nil {
			log.Printf("[order-scheduler] 自提单 %d 自动完成失败: %v", o.ID, res.Error)
			continue
		}
		if res.RowsAffected == 0 {
			continue
		}
		s.db.GetDb().WithContext(ctx).Create(&model.OrderStatusLog{
			OrderID:      o.ID,
			FromStatus:   fromStatus,
			ToStatus:     5,
			OperatorType: "system",
			OperatorID:   0,
			Remark:       fmt.Sprintf("支付后%d天未取货，系统自动完成", autoCompleteDays),
			CreatedAt:    now,
		})
		log.Printf("[order-scheduler] 自提单 %d 支付超过 %d 天未核销，已自动完成", o.ID, autoCompleteDays)
	}
}

// autoApproveStaleRefunds 退款超时自动审批
// 场景：用户申请退款后管理员长期不处理 → 退款单永久停留在待审核(status=1)。
// 超过 72h 未处理的退款申请自动同意并发起退款，保障消费者权益。
// 参数：
//   - ctx: 上下文
func (s *Scheduler) autoApproveStaleRefunds(ctx context.Context) {
	if s.refundService == nil {
		return
	}
	lockKey := "scheduler:order:refund:auto:approve:lock"
	lockValue := s.acquireLock(ctx, lockKey, 5*time.Minute)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockKey, lockValue)

	const staleHours = 72
	deadline := time.Now().Add(-staleHours * time.Hour).Format("2006-01-02 15:04:05")
	var refunds []*model.Refund
	if err := s.db.GetDb().WithContext(ctx).
		Where("status = 1 AND created_at < ?", deadline).
		Order("id asc").Limit(20).Find(&refunds).Error; err != nil {
		log.Printf("[order-scheduler] 查询超时退款申请失败: %v", err)
		return
	}

	for _, r := range refunds {
		err := s.refundService.ApproveRefund(ctx, &order.ApproveRefundReq{
			Id:         r.ID,
			OperatorId: 0,
		})
		if err != nil {
			log.Printf("[order-scheduler] 自动审批退款单 %d 失败: %v", r.ID, err)
			continue
		}
		log.Printf("[order-scheduler] 退款单 %d 超过 %dh 未处理，已自动同意", r.ID, staleHours)
	}
}

// recordFailure 记录处理失败的订单
//
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - taskType: 任务类型
//   - errMsg: 错误信息
func (s *Scheduler) recordFailure(ctx context.Context, orderID int64, taskType string, errMsg string) {
	now := time.Now().Format("2006-01-02 15:04:05")
	failure := &model.SchedulerFailure{
		OrderID:    orderID,
		TaskType:   taskType,
		Status:     1,
		ErrorMsg:   errMsg,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(failure).Error; err != nil {
		log.Printf("[order-scheduler] 记录失败订单 %d 出错: %v", orderID, err)
	}
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
