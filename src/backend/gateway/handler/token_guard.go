package handler

import (
	"context"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	goredis "github.com/go-redis/redis/v8"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/jwt"
)

/**
 * 令牌黑名单（P1-5 + C5）
 *
 * 目标：让「停用管理员」「登出」与「刷新轮转」立即对已签发的令牌生效。
 * JWT 本身无状态，仅靠过期时间的话：refresh 令牌（默认 7 天）被盗或注销后仍可续期
 * access；access 令牌（默认 2 小时）在登出后服务端照旧认它——「登出」只剩前端表演。
 * 修法是轮转 + 黑名单 + 鉴权时查验：
 *   - 签发：access 与 refresh 一律带随机 jti（common/jwt 的 newJti）；
 *   - 刷新：签发新 refresh，同时把旧 jti 以 SETNX 原子消费，旧令牌再用即被拒；
 *   - 登出：吊销本次会话的 access 与 refresh 两枚 jti；
 *   - 鉴权：AuthMiddleware 逐请求查一次黑名单（access jti 命中即 401），
 *     由 main 在装配阶段注入（gwMiddleware.SetTokenRevocationChecker）。
 *
 * 存储：Redis（多 gateway 实例共享），键形如 tokenjti:<jti>，access 与 refresh 共用一个
 * 命名空间——jti 每令牌唯一随机，不会串键；TTL 取令牌剩余寿命，到期自动消失无需清理。
 * Redis 不可用时放行并记 [WARN]：降级为「按过期时间自然失效」，与 loginGuard 的契约一致——
 * 缓存故障不关闭认证入口。代价是鉴权成了热路径，故命令超时压到 500ms：健康 Redis 单键
 * EXISTS 亚毫秒返回，Redis 故障时最坏给每个鉴权请求加 500ms，而不是 1s。
 */

const (
	// tokenJtiKeyPrefix Redis 键前缀，与登录锁定键（loginfail）区分命名空间
	tokenJtiKeyPrefix = "tokenjti"

	// tokenGuardOpTimeout 单次 Redis 命令超时：黑名单查验挂在鉴权热路径上（逐请求一次），
	// 取值须在「健康 Redis 够用」与「故障时不拖慢每个请求」之间权衡，500ms 足够
	tokenGuardOpTimeout = 500 * time.Millisecond

	// tokenGuardMinTTL consume 兜底 TTL：令牌剩余寿命非正时仍须落键，
	// 用 1 分钟足够覆盖并发对手的重放窗口，又不会为废令牌长期占内存
	tokenGuardMinTTL = time.Minute

	// revokedWarnInterval 黑名单查验失败的告警节流间隔（鉴权是热路径，见 revoked）
	revokedWarnInterval = 30 * time.Second
)

// tokenGuard 令牌黑名单（access 与 refresh 共用）
type tokenGuard struct {
	cfg *config.Config
	mu  sync.Mutex
	rds *goredis.Client
	// lastCheckWarn 上次「黑名单查验失败」告警时间（UnixNano，0=从未告警），
	// 热路径只读 Redis 结果、失败时按 revokedWarnInterval 节流打印
	lastCheckWarn atomic.Int64
}

// newTokenGuard 创建令牌黑名单器
// Redis 客户端懒初始化：启动期不建连，缓存未就绪也不阻断 gateway 启动
//
// 参数：
//   - cfg: 全局配置（取 Redis 连接参数）
//
// 返回值：
//   - *tokenGuard: 黑名单器实例
func newTokenGuard(cfg *config.Config) *tokenGuard {
	return &tokenGuard{cfg: cfg}
}

// TokenGuard 令牌黑名单器的对外别名（C5）
// 黑名单状态在 Redis，实例本身只是连接与告警节流，因此网关启动时只构造一份，
// 同时注入两类 handler（刷新/登出写入）与鉴权中间件（逐请求查验），
// 避免同一份状态被多个实例各连一遍 Redis
type TokenGuard = tokenGuard

// NewTokenGuard 构造令牌黑名单器，供 gateway 启动装配
//
// 参数：
//   - cfg: 全局配置（取 Redis 连接参数）
//
// 返回值：
//   - *TokenGuard: 黑名单器实例
func NewTokenGuard(cfg *config.Config) *TokenGuard {
	return newTokenGuard(cfg)
}

// RevocationCheck 导出吊销判定，供鉴权中间件逐请求查验 access 令牌（命中黑名单即拒绝）
// 判定沿用 revoked 的 fail-open 契约：Redis 故障时放行，缓存抖动不把全站打成 401
//
// 返回值：
//   - func(ctx context.Context, jti string) bool: 判定函数
//
// 使用示例：
//
//	gwMiddleware.SetTokenRevocationChecker(guard.RevocationCheck())
func (g *tokenGuard) RevocationCheck() func(ctx context.Context, jti string) bool {
	return g.revoked
}

// client 取 Redis 客户端（懒初始化，直连 go-redis，理由同 loginGuard.client）
//
// 返回值：
//   - *goredis.Client: Redis 客户端（连接是否可用由命令返回值判定）
func (g *tokenGuard) client() *goredis.Client {
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
		DialTimeout:  tokenGuardOpTimeout,
		ReadTimeout:  tokenGuardOpTimeout,
		WriteTimeout: tokenGuardOpTimeout,
	})
	return g.rds
}

// revoked 查询 jti 是否已被吊销（鉴权热路径，由 AuthMiddleware 逐请求调用）
// Redis 故障时放行（返回 false）并记 [WARN]：与 loginGuard 同契约，
// 缓存抖动不得把正常请求打成 401；代价是这期间登出/吊销暂不生效，令牌退回「按过期时间失效」
//
// 参数：
//   - ctx: 上下文
//   - jti: 令牌唯一标识（空串按未吊销处理：本次改动之前签发、不带 jti 的历史令牌不参与黑名单）
//
// 返回值：
//   - bool: 已吊销返回 true
func (g *tokenGuard) revoked(ctx context.Context, jti string) bool {
	if jti == "" {
		return false
	}
	n, err := g.client().Exists(ctx, tokenJtiKey(jti)).Result()
	if err != nil {
		// 逐请求打印会在 Redis 故障时把日志刷爆、掩盖真实故障线索，按间隔节流：
		// 只有「距上次告警已超过间隔」且 CAS 抢到打印权的请求才落一条日志——
		// 抢到同时刷新时间戳，抢不到的继续静默（若每次都写时间戳，窗口会被后来的请求顶走，永远不再打印）
		now := time.Now().UnixNano()
		last := g.lastCheckWarn.Load()
		if last == 0 || time.Duration(now-last) >= revokedWarnInterval {
			if g.lastCheckWarn.CompareAndSwap(last, now) {
				log.Printf("[WARN] 查询令牌黑名单失败（本次放行）jti=%s: %v", jti, err)
			}
		}
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
//	if h.tokenGuard.consume(ctx, claims.ID, time.Until(claims.ExpiresAt.Time)) {
//		response.Error(c, errcode.InvalidToken)
//		return
//	}
func (g *tokenGuard) consume(ctx context.Context, jti string, ttl time.Duration) bool {
	if jti == "" {
		return false
	}
	if ttl <= 0 {
		// 令牌已自然过期（正常情况下 ParseToken 已先拒掉），仍落一个短 TTL 键，
		// 避免「已过期」被当成「未消费」放行
		ttl = tokenGuardMinTTL
	}
	ok, err := g.client().SetNX(ctx, tokenJtiKey(jti), 1, ttl).Result()
	if err != nil {
		log.Printf("[WARN] 消费 refresh 令牌失败（本次放行）jti=%s: %v", jti, err)
		return false
	}
	return !ok
}

// revoke 吊销 jti（TTL 取令牌剩余寿命，过期后条目自动消失，无需清理）
// 写入失败只记日志不阻断登出：吊销是加固项，Redis 故障时退化为「按过期时间自然失效」
//
// 参数：
//   - ctx: 上下文
//   - jti: 令牌唯一标识（空串跳过）
//   - ttl: 黑名单存活时长，<=0 时跳过（令牌已过期，无需再吊销）
//
// 返回值：
//   - 无
func (g *tokenGuard) revoke(ctx context.Context, jti string, ttl time.Duration) {
	if jti == "" || ttl <= 0 {
		return
	}
	if err := g.client().Set(ctx, tokenJtiKey(jti), 1, ttl).Err(); err != nil {
		log.Printf("[WARN] 写入令牌黑名单失败 jti=%s: %v", jti, err)
	}
}

// revokePresentedTokens 登出时吊销本次会话携带的 access 与 refresh 两枚令牌（C5）
// 两条令牌到达方式不同：access 走 Authorization 头（小程序/App/POS）或后台 H5 的 httpOnly
// cookie；refresh 走 cookie 或请求体。登出路由是公开路由（不带鉴权中间件），
// 因此在此自行解析；解析失败或非本端类型/用途一律跳过——那种令牌本就用不了，
// 类型校验还能防止拿 A 端的登出请求吊销 B 端令牌
//
// 参数：
//   - c: gin 上下文
//   - manager: JWT 管理器
//   - guard: 令牌黑名单器
//   - tokenType: 本端令牌类型（jwt.TokenTypeUser / jwt.TokenTypeAdmin）
//   - bodyRefreshToken: 请求体里的 refresh_token（无则传空串，仍会尝试 cookie）
//
// 使用示例：
//
//	revokePresentedTokens(c, h.jwtManager, h.tokenGuard, jwt.TokenTypeAdmin, "")
func revokePresentedTokens(c *gin.Context, manager *jwt.Manager, guard *tokenGuard, tokenType, bodyRefreshToken string) {
	ctx := c.Request.Context()
	revokeOne := func(token, wantUse string) {
		if token == "" {
			return
		}
		claims, err := manager.ParseToken(token)
		if err != nil || claims.Type != tokenType || claims.TokenUse != wantUse {
			return
		}
		guard.revoke(ctx, claims.ID, time.Until(claims.ExpiresAt.Time))
	}
	revokeOne(presentedAccessToken(c), jwt.TokenUseAccess)
	revokeOne(firstNonEmptyRefreshToken(c, bodyRefreshToken), jwt.TokenUseRefresh)
}

// tokenJtiKey 黑名单键名
//
// 参数：
//   - jti: 令牌唯一标识
//
// 返回值：
//   - string: Redis 键名
func tokenJtiKey(jti string) string {
	return tokenJtiKeyPrefix + ":" + jti
}
