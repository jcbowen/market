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

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

// Scheduler 订单定时任务调度器
// 负责超时未支付订单自动取消、发货后超时自动完成
// 采用 Redis 分布式锁 + 数据库原子抢占 + 失败重试机制
type Scheduler struct {
	db  *mysql.Instance
	redisClient *redis.Client
}

// NewScheduler 创建订单定时任务调度器
//
// 参数：
//   - db: MySQL 实例
//   - redisClient: Redis 客户端，用于分布式锁
//
// 返回值：
//   - *Scheduler: 调度器实例
func NewScheduler(db *mysql.Instance, redisClient *redis.Client) *Scheduler {
	return &Scheduler{
		db:          db,
		redisClient: redisClient,
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
	s.cancelTimeoutOrders(ctx, orderTimeout)
	s.autoCompleteOrders(ctx, autoCompleteDays)
}

// cancelTimeoutOrders 取消超时未支付订单
//
// 参数：
//   - ctx: 上下文
//   - orderTimeout: 超时时间（秒）
func (s *Scheduler) cancelTimeoutOrders(ctx context.Context, orderTimeout int) {
	lockKey := "scheduler:timeout:cancel:lock"
	if !s.acquireLock(ctx, lockKey, 5*time.Minute) {
		return
	}
	defer s.releaseLock(ctx, lockKey)

	// 1. 优先处理历史失败记录
	s.retryFailures(ctx, "timeout_cancel", 0)

	// 2. 原子抢占一批待取消订单（status=1 → 9 处理中）
	orders := s.claimOrders(ctx, 1, 9, "created_at < ?", time.Now().Add(-time.Duration(orderTimeout)*time.Second).Format("2006-01-02 15:04:05"))
	if len(orders) == 0 {
		return
	}

	// 3. 处理抢占到的订单
	for _, o := range orders {
		if err := s.cancelOrderTx(ctx, o); err != nil {
			log.Printf("[order-scheduler] 自动取消订单 %d 失败: %v", o.ID, err)
			s.recordFailure(ctx, o.ID, "timeout_cancel", err.Error())
			continue
		}
		log.Printf("[order-scheduler] 订单 %d 超时未支付，已自动取消", o.ID)
	}
}

// autoCompleteOrders 自动完成已发货超时的订单
//
// 参数：
//   - ctx: 上下文
//   - autoCompleteDays: 自动完成天数
func (s *Scheduler) autoCompleteOrders(ctx context.Context, autoCompleteDays int) {
	lockKey := "scheduler:auto:complete:lock"
	if !s.acquireLock(ctx, lockKey, 5*time.Minute) {
		return
	}
	defer s.releaseLock(ctx, lockKey)

	// 1. 优先处理历史失败记录
	s.retryFailures(ctx, "auto_complete", autoCompleteDays)

	// 2. 原子抢占一批待自动完成订单（status=4 → 10 处理中）
	deadline := time.Now().AddDate(0, 0, -autoCompleteDays).Format("2006-01-02 15:04:05")
	orders := s.claimOrders(ctx, 4, 10, "deliver_time IS NOT NULL AND deliver_time < ?", deadline)
	if len(orders) == 0 {
		return
	}

	// 3. 处理抢占到的订单
	for _, o := range orders {
		if err := s.completeOrderTx(ctx, o, autoCompleteDays); err != nil {
			log.Printf("[order-scheduler] 自动完成订单 %d 失败: %v", o.ID, err)
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
//
// 返回值：
//   - error: 错误信息
func (s *Scheduler) cancelOrderTx(ctx context.Context, o *model.Order) error {
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
			}); err != nil {
				return fmt.Errorf("释放库存失败: %w", err)
			}
		}

		log := &model.OrderStatusLog{
			OrderID:      o.ID,
			FromStatus:   1,
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
//   - autoCompleteDays: 自动完成天数，用于备注
//
// 返回值：
//   - error: 错误信息
func (s *Scheduler) completeOrderTx(ctx context.Context, o *model.Order, autoCompleteDays int) error {
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
			FromStatus:   4,
			ToStatus:     5,
			OperatorType: "system",
			OperatorID:   0,
			Remark:       fmt.Sprintf("发货后%d天未确认收货，系统自动完成", autoCompleteDays),
			CreatedAt:    now,
		}
		return tx.Create(statusLog).Error
	})
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

		var err error
		switch taskType {
		case "timeout_cancel":
			if o.Status != 1 {
				s.markFailureStatus(ctx, f.ID, 2)
				continue
			}
			err = s.cancelOrderTx(ctx, &o)
		case "auto_complete":
			if o.Status != 4 {
				s.markFailureStatus(ctx, f.ID, 2)
				continue
			}
			err = s.completeOrderTx(ctx, &o, autoCompleteDays)
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

// acquireLock 获取 Redis 分布式锁
//
// 参数：
//   - ctx: 上下文
//   - key: 锁键
//   - ttl: 锁过期时间
//
// 返回值：
//   - bool: 获取成功返回 true
func (s *Scheduler) acquireLock(ctx context.Context, key string, ttl time.Duration) bool {
	value := strconv.FormatInt(time.Now().UnixNano(), 10)
	ok, err := s.redisClient.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		log.Printf("[order-scheduler] 获取锁 %s 失败: %v", key, err)
		return false
	}
	return ok
}

// releaseLock 释放 Redis 分布式锁
//
// 参数：
//   - ctx: 上下文
//   - key: 锁键
func (s *Scheduler) releaseLock(ctx context.Context, key string) {
	if err := s.redisClient.Del(ctx, key).Err(); err != nil {
		log.Printf("[order-scheduler] 释放锁 %s 失败: %v", key, err)
	}
}
