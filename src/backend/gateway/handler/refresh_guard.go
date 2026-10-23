package handler

import (
	"context"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"github.com/jcbowen/market/src/backend/common/config"
)

/**
 * refresh 令牌黑名单（P1-5）
 *
 * 目标：让「停用管理员」与「登出」立即对已签发的 refresh 令牌生效。
 * JWT 本身无状态，仅靠过期时间的话，被盗或已注销的 refresh 令牌在有效期内
 * （默认 7 天）仍可续期 access token。修法是轮转 + 黑名单：
 * 每次刷新签发新 refresh 令牌，同时把旧令牌的 jti 写入 Redis（TTL=旧令牌剩余寿命），
 * 旧令牌再用即被拒；登出时同样吊销当前令牌。
 *
 * 存储：Redis（多 gateway 实例共享），键形如 refreshjti:<jti>。
 * Redis 不可用时放行并记 [WARN]：降级为轮转前行为（旧令牌存活到期），
 * 与 loginGuard 的契约一致——缓存故障不关闭认证入口。
 */

const (
	// refreshJtiKeyPrefix Redis 键前缀，与登录锁定键（loginfail）区分命名空间
	refreshJtiKeyPrefix = "refreshjti"

	// refreshGuardOpTimeout 单次 Redis 命令超时（刷新是低频入口，1s 足够）
	refreshGuardOpTimeout = time.Second

	// refreshGuardMinTTL consume 兜底 TTL：令牌剩余寿命非正时仍须落键，
	// 用 1 分钟足够覆盖并发对手的重放窗口，又不会为废令牌长期占内存
	refreshGuardMinTTL = time.Minute
)

// refreshGuard refresh 令牌黑名单
type refreshGuard struct {
	cfg *config.Config
	mu  sync.Mutex
	rds *goredis.Client
}

// newRefreshGuard 创建 refresh 黑名单器
// Redis 客户端懒初始化：启动期不建连，缓存未就绪也不阻断 gateway 启动
//
// 参数：
//   - cfg: 全局配置（取 Redis 连接参数）
//
// 返回值：
//   - *refreshGuard: 黑名单器实例
func newRefreshGuard(cfg *config.Config) *refreshGuard {
	return &refreshGuard{cfg: cfg}
}

// client 取 Redis 客户端（懒初始化，直连 go-redis，理由同 loginGuard.client）
//
// 返回值：
//   - *goredis.Client: Redis 客户端（连接是否可用由命令返回值判定）
func (g *refreshGuard) client() *goredis.Client {
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
		DialTimeout:  refreshGuardOpTimeout,
		ReadTimeout:  refreshGuardOpTimeout,
		WriteTimeout: refreshGuardOpTimeout,
	})
	return g.rds
}

// revoked 查询 jti 是否已被吊销
// Redis 故障时放行（返回 false）并记 [WARN]：与 loginGuard 同契约，
// 缓存抖动不得把正常刷新请求打成失败
//
// 参数：
//   - ctx: 上下文
//   - jti: 令牌唯一标识（空串按未吊销处理：历史签发无 jti 的令牌不参与黑名单）
//
// 返回值：
//   - bool: 已吊销返回 true
func (g *refreshGuard) revoked(ctx context.Context, jti string) bool {
	if jti == "" {
		return false
	}
	n, err := g.client().Exists(ctx, refreshJtiKey(jti)).Result()
	if err != nil {
		log.Printf("[WARN] 查询 refresh 黑名单失败（本次放行）jti=%s: %v", jti, err)
		return false
	}
	return n > 0
}

// consume 原子「校验并消费」一个 jti，返回是否应判为已吊销（P1-7 并发重放）
// 刷新链路不能用「先 revoked 查、成功后再 revoke 写」两步：同一枚旧 refresh 令牌被并发
// 提交两次时，两个请求都在写键前查到「未吊销」，于是都换取到新令牌——轮转防线失效。
// SETNX 把两步合一：抢到键即「此前未吊销且本次已消费」，抢不到即已被并发请求或上次轮转消费。
// Redis 故障时无法判定，沿用 fail-open 契约放行（与 revoked 一致），缓存抖动不关闭刷新入口。
//
// 参数：
//   - ctx: 上下文
//   - jti: 令牌唯一标识（空串按不参与黑名单处理，返回 false）
//   - ttl: 消费记录存活时长，取被消费令牌的剩余寿命
//
// 返回值：
//   - bool: true 表示该令牌已被消费/吊销，调用方须拒绝
//
// 使用示例：
//
//	if h.refreshGuard.consume(ctx, claims.ID, time.Until(claims.ExpiresAt.Time)) {
//		response.Error(c, errcode.InvalidToken)
//		return
//	}
func (g *refreshGuard) consume(ctx context.Context, jti string, ttl time.Duration) bool {
	if jti == "" {
		return false
	}
	if ttl <= 0 {
		// 令牌已自然过期（正常情况下 ParseToken 已先拒掉），仍落一个短 TTL 键，
		// 避免「已过期」被当成「未消费」放行
		ttl = refreshGuardMinTTL
	}
	ok, err := g.client().SetNX(ctx, refreshJtiKey(jti), 1, ttl).Result()
	if err != nil {
		log.Printf("[WARN] 消费 refresh 令牌失败（本次放行）jti=%s: %v", jti, err)
		return false
	}
	return !ok
}

// revoke 吊销 jti（TTL 取令牌剩余寿命，过期后条目自动消失，无需清理）
// 写入失败只记日志不阻断刷新：吊销是加固项，Redis 故障时退化为「不轮转」
//
// 参数：
//   - ctx: 上下文
//   - jti: 令牌唯一标识（空串跳过）
//   - ttl: 黑名单存活时长，<=0 时跳过（令牌已过期，无需再吊销）
//
// 返回值：
//   - 无
func (g *refreshGuard) revoke(ctx context.Context, jti string, ttl time.Duration) {
	if jti == "" || ttl <= 0 {
		return
	}
	if err := g.client().Set(ctx, refreshJtiKey(jti), 1, ttl).Err(); err != nil {
		log.Printf("[WARN] 写入 refresh 黑名单失败 jti=%s: %v", jti, err)
	}
}

// refreshJtiKey 黑名单键名
//
// 参数：
//   - jti: 令牌唯一标识
//
// 返回值：
//   - string: Redis 键名
func refreshJtiKey(jti string) string {
	return refreshJtiKeyPrefix + ":" + jti
}
