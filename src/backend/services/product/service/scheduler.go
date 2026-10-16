package service

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
)

// couponExpireInterval 优惠券过期流转调度间隔
const couponExpireInterval = time.Minute

// couponExpireLockKey 优惠券过期流转分布式锁键
// 命名规范 scheduler:{服务名}:{业务}:{动作}:lock，服务名前缀避免多环境共用 Redis 实例时跨服务键名冲突
const couponExpireLockKey = "scheduler:product:coupon:expire:lock"

// CouponScheduler 优惠券定时任务调度器
// 负责 user_coupons 过期自动流转（status 1未使用 → 3已过期）
//
// 与 order 服务 Scheduler 的订单状态机不同：过期流转是单条原子条件 UPDATE
// （仅 status=1 且已过期的行会被更新），天然幂等且无中间态，无需抢占/回滚机制，
// 只需 Redis 分布式锁防止多实例重复扫描。
//
// 被动兜底已存在：UseCoupon 使用时校验 expire_at（"优惠券已过期"），
// ListUserCoupons 在 status=1 筛选时排除已过期行；本调度器负责把状态落库，
// 使 C 端「已过期」分组、后台发放记录的状态筛选与真实状态一致。
type CouponScheduler struct {
	db          *mysql.Instance
	redisClient *redis.Client
}

// NewCouponScheduler 创建优惠券过期流转调度器
//
// 参数：
//   - db: MySQL 实例
//   - redisClient: Redis 客户端，用于分布式锁
//
// 返回值：
//   - *CouponScheduler: 调度器实例
func NewCouponScheduler(db *mysql.Instance, redisClient *redis.Client) *CouponScheduler {
	return &CouponScheduler{db: db, redisClient: redisClient}
}

// Start 启动调度器：首次立即执行一次，随后每分钟一次
//
// 参数：
//   - ctx: 用于优雅退出的上下文
func (s *CouponScheduler) Start(ctx context.Context) {
	log.Println("[coupon-scheduler] 优惠券过期流转调度器已启动")
	s.runOnce(ctx)

	ticker := time.NewTicker(couponExpireInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("[coupon-scheduler] 优惠券过期流转调度器已停止")
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

// runOnce 单次执行：过期未使用的用户券批量置为已过期
func (s *CouponScheduler) runOnce(ctx context.Context) {
	lockValue := s.acquireLock(ctx)
	if lockValue == "" {
		return
	}
	defer s.releaseLock(ctx, lockValue)

	res := s.db.GetDb().WithContext(ctx).Exec(
		"UPDATE user_coupons SET status = ?, updated_at = NOW() WHERE status = ? AND expire_at IS NOT NULL AND expire_at < NOW()",
		3, 1,
	)
	if res.Error != nil {
		log.Printf("[coupon-scheduler] 过期流转失败: %v", res.Error)
		return
	}
	if res.RowsAffected > 0 {
		log.Printf("[coupon-scheduler] 已将 %d 张过期用户券流转为已过期", res.RowsAffected)
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

// acquireLock 获取分布式锁（TTL 5 分钟，长于单轮执行耗时即可）
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - string: 获取成功返回锁持有者标识（写入的随机值），失败返回空串
func (s *CouponScheduler) acquireLock(ctx context.Context) string {
	value := strconv.FormatInt(time.Now().UnixNano(), 10)
	ok, err := s.redisClient.SetNX(ctx, couponExpireLockKey, value, 5*time.Minute).Result()
	if err != nil {
		log.Printf("[coupon-scheduler] 获取锁失败: %v", err)
		return ""
	}
	if !ok {
		return ""
	}
	return value
}

// releaseLock 释放分布式锁（Lua 原子比较锁值后删除，见 releaseLockScript）
//
// 参数：
//   - ctx: 上下文
//   - value: acquireLock 返回的持有者标识
func (s *CouponScheduler) releaseLock(ctx context.Context, value string) {
	if err := releaseLockScript.Run(ctx, s.redisClient, []string{couponExpireLockKey}, value).Err(); err != nil {
		log.Printf("[coupon-scheduler] 释放锁失败: %v", err)
	}
}
