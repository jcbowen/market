package handler

import (
	"context"
	"log"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
	"github.com/jcbowen/market/src/backend/gateway/brand"
	gwMiddleware "github.com/jcbowen/market/src/backend/gateway/middleware"
)

// 支付回调路径：固定在网关路由上，回调地址一律由部署域名派生，不再由人工填写
const (
	wechatPayCallbackPath = "/api/v1/payments/callback/wechat"
	alipayPayCallbackPath = "/api/v1/payments/callback/alipay"
	// h5PayResultHashURL 支付宝支付完成回跳落地页（H5 为 hash 路由）
	// 必须是免登录页面：回跳发生在系统浏览器，无登录态；
	// 结果页凭 return_url 上追加的支付单号轮询公开状态接口展示支付结果
	h5PayResultHashURL = "/s/#/pages/pay/result"
)

// SiteSetting 站点设置视图对象
// 同时用于后台 GET 响应与 PUT 请求；出参额外带回生效域名、来源与派生出的回调地址
type SiteSetting struct {
	BaseURL          string `json:"base_url"`
	SiteName         string `json:"site_name"`
	LogoURL          string `json:"logo_url"`
	FaviconURL       string `json:"favicon_url"`
	FooterHTML       string `json:"footer_html"`
	SEODescription   string `json:"seo_description"`
	SEOKeywords      string `json:"seo_keywords"`
	UpdatedAt        string `json:"updated_at"`
	Version          int64  `json:"version"`
	EffectiveBaseURL string `json:"effective_base_url"`
	// BaseURLSource 生效域名来源：env 环境变量 / db 站点设置 / request 按请求现推（未配置）
	BaseURLSource string `json:"base_url_source"`
	// 以下三项为按生效域名派生的回调地址，仅出参，供支付配置页只读展示
	WechatNotifyURL string `json:"wechat_notify_url"`
	AlipayNotifyURL string `json:"alipay_notify_url"`
	AlipayReturnURL string `json:"alipay_return_url"`
}

// platformBaseURL 获取当前生效的部署域名基址
// 优先级：站点设置 base_url > SITE_BASE_URL 环境变量（兜底默认值） > 按请求现推（均未配置时）
//
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - string: 形如 https://host 的基址，末尾不含斜杠
func platformBaseURL(c *gin.Context) string {
	if snap := brand.Get(c.Request.Context()); snap.BaseURL != "" {
		return snap.BaseURL
	}
	return requestBaseURL(c)
}

// requestBaseURL 按当前请求推导部署域名基址
// 优先取反向代理透传的 X-Forwarded-Proto，兼容 HTTPS 部署场景
//
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - string: 形如 https://host 的基址
func requestBaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + c.Request.Host
}

// GetSiteSetting 获取站点设置
// 接口: GET /api/v1/admin/settings/site
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetSiteSetting(c *gin.Context) {
	cfg, err := h.userClient.GetSiteConfig(c.Request.Context(), &user.GetSiteConfigReq{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	snap := brand.Get(c.Request.Context())
	base := snap.BaseURL
	if base == "" {
		base = requestBaseURL(c)
	}
	setting := SiteSetting{
		BaseURL:          cfg.BaseUrl,
		SiteName:         cfg.SiteName,
		LogoURL:          cfg.LogoUrl,
		FaviconURL:       cfg.FaviconUrl,
		FooterHTML:       cfg.FooterHtml,
		SEODescription:   cfg.SeoDescription,
		SEOKeywords:      cfg.SeoKeywords,
		UpdatedAt:        cfg.UpdatedAt,
		Version:          cfg.Version,
		EffectiveBaseURL: base,
		BaseURLSource:    snap.BaseURLSource,
	}
	setting.WechatNotifyURL = base + wechatPayCallbackPath
	setting.AlipayNotifyURL = base + alipayPayCallbackPath
	setting.AlipayReturnURL = base + h5PayResultHashURL

	response.Success(c, setting)
}

// UpdateSiteSetting 保存站点设置
// 仅超管可写；保存成功后立即失效网关内存快照，并把派生出的支付回调地址回写 pay_config
// 接口: PUT /api/v1/admin/settings/site
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateSiteSetting(c *gin.Context) {
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}

	var req SiteSetting
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 乐观并发：站点设置是整份 PUT，表单缓存/多人同时编辑时后保存者会覆盖前一次改动。
	// 前端把载入时的 version 原样带回，与库中当前值不一致即拒绝提交，提示重新载入。
	// 不传 version（0，历史调用）保持原行为。校验在网关、写库仍在 user 服务，
	// 读—比对—写之间仍有毫秒级窗口（彻底串行化要给 SaveSiteConfigReq 加 expected_version，
	// 改动横跨 proto/服务/网关/前端四层），此处取「覆盖事故基本消除」而非「绝对不可能」。
	if req.Version > 0 {
		current, err := h.userClient.GetSiteConfig(c.Request.Context(), &user.GetSiteConfigReq{})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		if current.Version != req.Version {
			response.Error(c, errcode.DataConflict, "配置已被他人修改，请重新载入后再保存")
			return
		}
	}

	cfg, err := h.userClient.SaveSiteConfig(c.Request.Context(), &user.SaveSiteConfigReq{
		BaseUrl:        req.BaseURL,
		SiteName:       req.SiteName,
		LogoUrl:        req.LogoURL,
		FaviconUrl:     req.FaviconURL,
		FooterHtml:     req.FooterHTML,
		SeoDescription: req.SEODescription,
		SeoKeywords:    req.SEOKeywords,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 数据库已变更，立即刷新网关快照，使域名与品牌无需等待 TTL
	brand.Invalidate()
	snap := brand.Get(c.Request.Context())
	if snap.BaseURL != "" {
		h.syncPayCallbackURLs(c.Request.Context(), snap.BaseURL)
	}
	// 附件访问域名联动：本地存储且未配置访问域名时补写为生效部署域名（详见 syncAttachmentVisitDomain）。
	// 仅认显式配置来源（db 站点设置 / env SITE_BASE_URL）；base 为请求现推（request）时
	// 首访者是谁域名就是谁，写库会把临时兜底固化成错误配置（如局域网被 localhost 抢占），
	// 此时保持 visit_domain 为空，附件 URL 长期按站内绝对路径降级、随访问域名解析
	attachmentSynced := false
	if snap.BaseURL != "" && snap.BaseURLSource != "request" {
		attachmentSynced = h.syncAttachmentVisitDomain(c.Request.Context(), snap.BaseURL)
	}

	response.Success(c, gin.H{"success": true, "version": cfg.Version, "attachment_domain_synced": attachmentSynced})
}

// GetPublicSiteInfo 获取公开站点信息
// 免登录接口：登录页与 C 端首屏需要展示品牌，只回非敏感字段
// 接口: GET /api/v1/site-info
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetPublicSiteInfo(c *gin.Context) {
	snap := brand.Get(c.Request.Context())
	response.Success(c, gin.H{
		"site_name":       snap.SiteName,
		"logo_url":        snap.LogoURL,
		"favicon_url":     snap.FaviconURL,
		"footer_html":     snap.FooterHTML,
		"seo_description": snap.SEODescription,
		"seo_keywords":    snap.SEOKeywords,
		"base_url":        snap.BaseURL,
		"version":         snap.Version,
	})
}

// syncPayCallbackURLs 把站点域名派生的回调地址回写 pay_config
// 与渠道 AppID 回写同构：已是派生值时不写库，避免每次读取配置都产生一次无意义更新
//
// 参数：
//   - ctx: 上下文
//   - baseURL: 生效的部署域名基址
func (h *AdminHandler) syncPayCallbackURLs(ctx context.Context, baseURL string) {
	resp, err := h.paymentClient.GetPayConfig(ctx, &payment.GetPayConfigReq{})
	if err != nil {
		log.Printf("派生支付回调地址前读取支付配置失败: %v", err)
		return
	}

	wechatNotify, alipayNotify, alipayReturn := payCallbackURLsFor(baseURL)
	if resp.WechatNotifyUrl == wechatNotify && resp.AlipayNotifyUrl == alipayNotify && resp.AlipayReturnUrl == alipayReturn {
		return
	}
	h.writeBackPayConfig(ctx, resp, func(p *payment.PayConfig) {
		p.WechatNotifyUrl = wechatNotify
		p.AlipayNotifyUrl = alipayNotify
		p.AlipayReturnUrl = alipayReturn
	})
}

// syncAttachmentVisitDomain 站点域名联动补空附件访问域名（站点设置保存路径的触发点）
// 核心逻辑在 syncAttachmentVisitDomainBy；调用方须保证 baseURL 来自显式配置
// （db 站点设置 / SITE_BASE_URL 环境变量），请求现推的域名不入库
//
// 参数：
//   - ctx: 上下文
//   - baseURL: 生效的部署域名基址（形如 https://host）
//
// 返回值：
//   - bool: 是否发生了补写
func (h *AdminHandler) syncAttachmentVisitDomain(ctx context.Context, baseURL string) bool {
	return syncAttachmentVisitDomainBy(h.productClient, ctx, baseURL)
}

// syncAttachmentVisitDomainBy 附件域名补写核心：local 存储且 visit_domain 为空时补写为 baseURL
// 单向只补空：visit_domain 一旦有值（手填或历史补写）不再回写；OSS 模式域名由 OSS 配置派生，不处理。
// 为空且无显式配置来源时不写库：附件 URL 按站内绝对路径 /uploads/<path> 降级，随访问域名解析
//
// 参数：
//   - productClient: product 服务 gRPC 客户端
//   - ctx: 上下文
//   - baseURL: 显式配置的部署域名基址（形如 https://host）
//
// 返回值：
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
	log.Printf("附件访问域名未配置，已按显式配置的部署域名补写: %s", baseURL)
	return true
}
