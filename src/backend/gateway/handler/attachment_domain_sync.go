package handler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/gateway/brand"
)

// attachmentDomainSyncInterval 附件访问域名补写的进程内节流间隔
// 补写本身是幂等的只补空操作（visit_domain 有值时 RPC 内直接返回），节流只为控制空转成本：
// 未配置域名的全新部署在首次请求后即触发补写；已配置的部署每 10 分钟多一次轻量配置读取
const attachmentDomainSyncInterval = 10 * time.Minute

// attachmentDomainSyncTimeout 单次补写 RPC 的超时预算
// 请求 ctx 可能随客户端断连取消，补写脱离取消信号另行限时；失败等下一轮节流窗口重试
const attachmentDomainSyncTimeout = 3 * time.Second

// AttachmentDomainSync 附件访问域名（visit_domain）惰性补写中间件
// 背景：附件 URL 由 product 服务统一拼为「配置的附件域名 + 附件路径」的完整地址，
// local 模式依赖 visit_domain 恒有值。站点设置保存时已有一次联动补写（site.go），
// 本中间件补足其余触发路径：全新部署从未保存站点设置时，按首个进入网关的请求兜底。
// 域名来源优先级与 platformBaseURL 一致：站点设置 base_url > SITE_BASE_URL > 当前请求访问域名
type AttachmentDomainSync struct {
	productClient product.ProductServiceClient

	mu   sync.Mutex
	last time.Time
}

// NewAttachmentDomainSync 创建附件访问域名补写中间件
//
// 参数:
//   - productClient: product 服务 gRPC 客户端
//
// 返回值:
//   - *AttachmentDomainSync: 中间件实例
func NewAttachmentDomainSync(productClient product.ProductServiceClient) *AttachmentDomainSync {
	return &AttachmentDomainSync{productClient: productClient}
}

// Middleware 返回 gin 中间件；节流窗口外的请求触发一次补写尝试后放行
//
// 返回值:
//   - gin.HandlerFunc: gin 中间件
func (s *AttachmentDomainSync) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.trySync(c)
		c.Next()
	}
}

// trySync 节流触发附件访问域名补写
// 互斥锁只圈住节流判定与时间戳推进，RPC 在锁外执行：本中间件挂在网关全局路由上，
// 持锁跨网络往返会让全站请求排队等这一次补写；并发请求最多只有一个进入补写，其余直接放行
//
// 参数:
//   - c: gin 上下文
func (s *AttachmentDomainSync) trySync(c *gin.Context) {
	s.mu.Lock()
	if !s.last.IsZero() && time.Since(s.last) < attachmentDomainSyncInterval {
		s.mu.Unlock()
		return
	}
	s.last = time.Now()
	s.mu.Unlock()

	// 域名兜底链与用户口径一致：站点域名（brand 缓存，含站点设置/SITE_BASE_URL）为空时
	// 用当前访问域名兜底，保证 visit_domain 总能补上值
	base := brand.Get(c.Request.Context()).BaseURL
	if base == "" {
		base = requestBaseURL(c)
	}
	if base == "" {
		return
	}
	// 补写脱离请求的取消信号（客户端断连不该让补写必败）并限时，失败等下一轮节流窗口
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), attachmentDomainSyncTimeout)
	defer cancel()
	syncAttachmentVisitDomainBy(s.productClient, ctx, base)
}

// syncAttachmentVisitDomainBy 附件域名补写核心：local 存储且 visit_domain 为空时补写为 baseURL
// 单向只补空：visit_domain 一旦有值（手填或历史补写）不再回写；OSS 模式域名由 OSS 配置派生，不处理
//
// 参数:
//   - productClient: product 服务 gRPC 客户端
//   - ctx: 上下文
//   - baseURL: 生效部署域名基址（形如 https://host）
//
// 返回值:
//   - bool: 是否发生了补写
func syncAttachmentVisitDomainBy(productClient product.ProductServiceClient, ctx context.Context, baseURL string) bool {
	cfg, err := productClient.GetAttachmentConfig(ctx, &common.Empty{})
	if err != nil {
		log.Printf("联动附件访问域名前读取附件配置失败: %v", err)
		return false
	}
	if cfg.StorageType != "local" || cfg.VisitDomain != "" {
		return false
	}

	cfg.VisitDomain = baseURL
	if _, err := productClient.SaveAttachmentConfig(ctx, cfg); err != nil {
		log.Printf("按部署域名补写附件访问域名失败: %v", err)
		return false
	}
	log.Printf("附件访问域名未配置，已按生效部署域名自动补写: %s", baseURL)
	return true
}
