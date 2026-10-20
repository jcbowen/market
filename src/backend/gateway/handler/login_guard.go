package handler

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"github.com/jcbowen/market/src/backend/common/config"
)

/**
 * 登录失败锁定（D1）
 *
 * 目标：把「对着一个账号反复试密码」从无限次压到有限次。按 IP 限流挡不住分布式慢速猜测
 * （换 IP 即可续命），故锁定计数必须以账号为键。
 *
 * 存储：Redis（多实例共享计数、gateway 重启不清零），键形如 loginfail:<场景>:<账号标识>。
 * Redis 不可用时放行并记 [WARN]：宁可暂时少一道锁定，也不能因缓存故障关闭登录入口，
 * 此时仍受登录路由的 IP 限流兜底。
 */

// 登录失败锁定参数
const (
	// loginFailLimit 触发锁定的连续失败次数
	loginFailLimit = 5

	// loginFailWindow 失败计数窗口，同时是锁定时长（自第 5 次失败起重新计时）
	loginFailWindow = 15 * time.Minute

	// loginKeyPrefix Redis 键前缀，与订单侧取货码锁定键区分命名空间
	loginKeyPrefix = "loginfail"

	// redisOpTimeout 单次 Redis 命令超时
	// 取 1s：登录本身可容忍的额外延迟很小，缓存抖动时必须快速失败放行而不是把登录请求拖住
	redisOpTimeout = time.Second
)

// loginSceneAdmin 后台登录锁定场景标识
// 键中带场景段，是为了与 C 端将来接入时共用同一 Redis 命名空间而不互相覆盖计数。
// C 端手机号登录当前不校验验证码、任何号码都恒成功，没有「失败」可计，故只有后台场景接入，
// C 端试探由 /auth/* 路由的按 IP 限流兜底（见 gateway/main.go）。
const loginSceneAdmin = "admin"

// loginGuard 登录失败计数与账号锁定
type loginGuard struct {
	cfg *config.Config
	mu  sync.Mutex
	rds *goredis.Client
}

// newLoginGuard 创建登录失败锁定器
// Redis 客户端懒初始化：登录是低频入口，启动期不建连，缓存未就绪也不阻断 gateway 启动
//
// 参数：
//   - cfg: 全局配置（取 Redis 连接参数）
//
// 返回值：
//   - *loginGuard: 锁定器实例
func newLoginGuard(cfg *config.Config) *loginGuard {
	return &loginGuard{cfg: cfg}
}

// client 取 Redis 客户端
// 直连 go-redis 而非 jcbaseGo 的 redis.New：后者在建立实例时 Ping 失败会 PanicIfError 直接 panic，
// 缓存抖动会把登录请求打成 500，而本器的契约是「Redis 不可用时放行」。
// 建客户端本身不发起连接，命令期以短超时快速失败，错误一律按 fail-open 处理
//
// 返回值：
//   - *goredis.Client: Redis 客户端（连接是否可用由命令返回值判定）
func (g *loginGuard) client() *goredis.Client {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rds != nil {
		return g.rds
	}
	db, _ := strconv.Atoi(g.cfg.Redis.Db)
	g.rds = goredis.NewClient(&goredis.Options{
		Addr:         net.JoinHostPort(g.cfg.Redis.Host, g.cfg.Redis.Port),
		Password:     g.cfg.Redis.Password,
		DB:           db,
		DialTimeout:  redisOpTimeout,
		ReadTimeout:  redisOpTimeout,
		WriteTimeout: redisOpTimeout,
	})
	return g.rds
}

// lockedRemaining 查询账号是否处于锁定期
// 参数：
//   - ctx: 上下文
//   - scene: 锁定场景（当前仅 loginSceneAdmin）
//   - account: 账号标识（用户名或手机号）
//
// 返回值：
//   - time.Duration: 剩余锁定时长，未锁定时为 0
//   - bool: 是否处于锁定期
func (g *loginGuard) lockedRemaining(ctx context.Context, scene, account string) (time.Duration, bool) {
	rds := g.client()
	locked, err := rds.Exists(ctx, lockKey(scene, account)).Result()
	if err != nil {
		log.Printf("[WARN] 查询登录锁定状态失败（本次放行）key=%s: %v", scene+":"+account, err)
		return 0, false
	}
	if locked == 0 {
		return 0, false
	}
	ttl, err := rds.TTL(ctx, lockKey(scene, account)).Result()
	if err != nil || ttl <= 0 {
		// TTL 异常时按整个窗口兜底，避免出现「已锁定却提示剩余 0 秒」的自相矛盾响应
		return loginFailWindow, true
	}
	return ttl, true
}

// recordFailure 记一次登录失败，达到上限即进入锁定期并清空计数窗口
// 参数：
//   - ctx: 上下文
//   - scene: 锁定场景
//   - account: 账号标识
//
// 返回值：
//   - 无（失败计数与锁定写入异常只记日志，不改变登录响应）
func (g *loginGuard) recordFailure(ctx context.Context, scene, account string) {
	rds := g.client()
	failKey := failKey(scene, account)
	count, err := rds.Incr(ctx, failKey).Result()
	if err != nil {
		log.Printf("[WARN] 登录失败计数写入失败 key=%s: %v", failKey, err)
		return
	}
	// 首次失败起开窗口：后续失败沿用同一窗口，避免持续试探把过期时间无限顺延
	if count == 1 {
		if err := rds.Expire(ctx, failKey, loginFailWindow).Err(); err != nil {
			log.Printf("[WARN] 登录失败计数设置过期失败 key=%s: %v", failKey, err)
		}
	}
	if count < loginFailLimit {
		return
	}
	if err := rds.Set(ctx, lockKey(scene, account), count, loginFailWindow).Err(); err != nil {
		log.Printf("[WARN] 登录锁定写入失败 key=%s: %v", lockKey(scene, account), err)
		return
	}
	_ = rds.Del(ctx, failKey).Err()
	log.Printf("[WARN] 登录连续失败 %d 次已锁定账号 %s:%s %v", count, scene, maskAccount(account), loginFailWindow)
}

// clearFailures 登录成功后清空失败计数与锁定标记
// 参数：
//   - ctx: 上下文
//   - scene: 锁定场景
//   - account: 账号标识
//
// 返回值：
//   - 无
func (g *loginGuard) clearFailures(ctx context.Context, scene, account string) {
	_ = g.client().Del(ctx, failKey(scene, account), lockKey(scene, account)).Err()
}

// failKey 失败计数键名
// 参数：
//   - scene: 锁定场景
//   - account: 账号标识
//
// 返回值：
//   - string: Redis 键名
func failKey(scene, account string) string {
	return loginKeyPrefix + ":" + scene + ":" + account
}

// lockKey 锁定标记键名
// 参数：
//   - scene: 锁定场景
//   - account: 账号标识
//
// 返回值：
//   - string: Redis 键名
func lockKey(scene, account string) string {
	return failKey(scene, account) + ":lock"
}

// loginLockedMessage 生成锁定提示文案（向上取整到分钟，避免「剩余 0 分钟」）
// 参数：
//   - ttl: 剩余锁定时长
//
// 返回值：
//   - string: 面向用户的提示
func loginLockedMessage(ttl time.Duration) string {
	minutes := int(ttl.Minutes())
	if ttl-time.Duration(minutes)*time.Minute > 0 {
		minutes++
	}
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("登录失败次数过多，请 %d 分钟后再试", minutes)
}

// maskAccount 日志中隐去账号明文（手机号留前 3 后 4，用户名留前 2 位）
// 参数：
//   - account: 账号标识
//
// 返回值：
//   - string: 脱敏后的账号
func maskAccount(account string) string {
	r := []rune(account)
	if len(r) == 0 {
		return ""
	}
	if len(r) <= 4 {
		return string(r[:1]) + "***"
	}
	if len(r) == 11 {
		return string(r[:3]) + "****" + string(r[7:])
	}
	return string(r[:2]) + "***"
}
