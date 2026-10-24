// Package brand 站点设置的网关侧内存缓存
//
// 站点设置（部署域名与平台品牌）持久化在 user 服务 site_configs 单行表，
// 但每次请求都不应为此走一次 gRPC + 数据库查询：网关按版本号 TTL 刷新内存快照，
// 支付回调派生、附件外链基址、前端 HTML 品牌注入三处消费方一律读内存。
//
// 使用示例：
//
//	brand.SetDefault(brand.New(clients.UserClient, os.Getenv("SITE_BASE_URL")))
//	snap := brand.Get(ctx)
//	baseURL := snap.BaseURL
package brand

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/jcbowen/market/src/backend/common/proto/user"
)

// Snapshot 站点设置快照
// BaseURL 已合并兜底优先级：站点设置 base_url 恒优先，数据库未配置时回落 SITE_BASE_URL 环境变量；
// BaseURLSource 标记当前生效值来源（db/env/request：均为空时按请求现推）
type Snapshot struct {
	BaseURL         string
	BaseURLSource   string
	SiteName        string
	LogoURL         string
	FaviconURL      string
	FooterHTML      string
	SEODescription  string
	SEOKeywords     string
	Version         int64
	FromEnvironment bool
}

// Cache 站点设置内存缓存
type Cache struct {
	client     user.UserServiceClient
	envBaseURL string
	ttl        time.Duration

	mu         sync.RWMutex
	snap       Snapshot
	loaded     bool
	fetchedAt  time.Time
	refreshing bool // 刷新进行中标记（single-flight）：过期瞬间的高并发只放行一次刷新
}

// New 创建站点设置内存缓存
// 初始为空快照，首次请求或 TTL 到期时向 user 服务拉取
//
// 参数：
//   - client: user 服务 gRPC 客户端
//   - envBaseURL: 环境变量 SITE_BASE_URL 的值，数据库未配置时兜底
//
// 返回值：
//   - *Cache: 缓存实例
func New(client user.UserServiceClient, envBaseURL string) *Cache {
	return &Cache{client: client, envBaseURL: envBaseURL, ttl: 30 * time.Second}
}

// snapshot 组装当前快照（应用兜底优先级：站点设置 > 环境变量）
//
// 返回值：
//   - Snapshot: 快照副本
func (c *Cache) snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snap := c.snap
	if snap.BaseURL != "" {
		// 站点设置显式配置了域名：恒以数据库为准，保存即接管，环境变量退为兜底
		snap.BaseURLSource = "db"
		snap.FromEnvironment = false
	} else if c.envBaseURL != "" {
		// 数据库未配置时以环境变量兜底，后台页面据此回填默认值
		snap.BaseURL = c.envBaseURL
		snap.BaseURLSource = "env"
		snap.FromEnvironment = true
	} else {
		// 均未配置：调用方按当前请求推导域名，快照标记来源以便后台页面区分展示
		snap.BaseURLSource = "request"
	}
	return snap
}

// get 返回快照，并在缓存过期时触发后台刷新（single-flight）
// 过期瞬间若有 N 个并发请求，仅第一个放行刷新，其余直接拿当前快照（至多旧一个 TTL 周期），
// 避免缓存失效瞬间 N 路请求同时打 user 服务；刷新失败由 refresh 内部 markFetched 延后重试
//
// 参数：
//   - ctx: 请求上下文（保留参数兼容调用方；刷新改用独立超时上下文，
//     不随单个请求结束而中断共享刷新）
//
// 返回值：
//   - Snapshot: 当前快照
func (c *Cache) get(ctx context.Context) Snapshot {
	c.mu.Lock()
	stale := !c.loaded || time.Since(c.fetchedAt) > c.ttl
	if stale && !c.refreshing {
		c.refreshing = true
		c.mu.Unlock()
		go func() {
			defer func() {
				c.mu.Lock()
				c.refreshing = false
				c.mu.Unlock()
			}()
			refreshCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c.refresh(refreshCtx)
		}()
	} else {
		c.mu.Unlock()
	}
	return c.snapshot()
}

// refresh 从 user 服务拉取站点设置并更新快照
//
// 参数：
//   - ctx: 请求上下文
func (c *Cache) refresh(ctx context.Context) {
	if c.client == nil {
		return
	}
	cfg, err := c.client.GetSiteConfig(ctx, &user.GetSiteConfigReq{})
	if err != nil {
		log.Printf("拉取站点设置失败，沿用旧快照: %v", err)
		c.markFetched(5 * time.Second)
		return
	}

	now := time.Now()
	c.mu.Lock()
	c.snap = Snapshot{
		BaseURL:        cfg.BaseUrl,
		SiteName:       cfg.SiteName,
		LogoURL:        cfg.LogoUrl,
		FaviconURL:     cfg.FaviconUrl,
		FooterHTML:     cfg.FooterHtml,
		SEODescription: cfg.SeoDescription,
		SEOKeywords:    cfg.SeoKeywords,
		Version:        cfg.Version,
	}
	c.loaded = true
	c.fetchedAt = now
	c.mu.Unlock()
}

// markFetched 缩短下次刷新间隔（失败退避用）
//
// 参数：
//   - backoff: 距离下次刷新的间隔
func (c *Cache) markFetched(backoff time.Duration) {
	c.mu.Lock()
	c.fetchedAt = time.Now().Add(-c.ttl + backoff)
	c.mu.Unlock()
}

// Warm 启动期同步预取一次站点设置（冷启动首屏兜底）
// get 的刷新是异步的：进程刚起来时快照还是空的，第一个 HTML 请求会拿到空品牌/空 SEO
// （注入的是占位标题，搜索引擎抓到的正是这一份）。启动时按超时同步拉一次，
// 拉不到只记日志、不阻断启动——user 服务稍后恢复时 TTL 刷新会补上。
//
// 参数：
//   - ctx: 预热上下文（调用方给超时，避免 user 服务未就绪时拖慢启动）
//
// 返回值：
//   - error: 预取失败原因（已内部记日志，返回值仅供调用方决定是否告警）
//
// 使用示例：
//
//	warmCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	_ = cache.Warm(warmCtx)
func (c *Cache) Warm(ctx context.Context) error {
	c.refresh(ctx)
	c.mu.RLock()
	loaded := c.loaded
	c.mu.RUnlock()
	if !loaded {
		return errors.New("站点设置预热未取到数据（沿用空快照，等待 TTL 刷新重试）")
	}
	return nil
}

// invalidate 使快照立即过期，保存站点设置后调用，使本实例无需等待 TTL 即生效
func (c *Cache) invalidate() {
	c.mu.Lock()
	c.loaded = false
	c.mu.Unlock()
}

// defaultCache 网关进程级单例
// 路由注册阶段注入，中间件与 handler 零参数取用（与 middleware.SetPermissionResolver 同构）
var defaultCache *Cache

// SetDefault 注入进程级站点设置缓存
//
// 参数：
//   - c: 缓存实例
func SetDefault(c *Cache) { defaultCache = c }

// Get 读取当前站点设置快照
// 缓存未注入时返回空快照，各消费方按「配置为空则回落」的口径处理
//
// 参数：
//   - ctx: 请求上下文
//
// 返回值：
//   - Snapshot: 快照
func Get(ctx context.Context) Snapshot {
	if defaultCache == nil {
		return Snapshot{}
	}
	return defaultCache.get(ctx)
}

// Invalidate 立即失效快照（保存站点设置后调用）
func Invalidate() {
	if defaultCache != nil {
		defaultCache.invalidate()
	}
}
