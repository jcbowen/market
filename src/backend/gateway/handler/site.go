package handler

import (
	"context"
	"log"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
	"github.com/jcbowen/market/src/backend/gateway/brand"
)

// 支付回调路径：固定在网关路由上，回调地址一律由部署域名派生，不再由人工填写
const (
	wechatPayCallbackPath = "/api/v1/payments/callback/wechat"
	alipayPayCallbackPath = "/api/v1/payments/callback/alipay"
	// h5OrderListHashURL 支付宝支付完成回跳落地页（H5 为 hash 路由）
	h5OrderListHashURL = "/s/#/pages/order/list"
)

// SiteSetting 站点设置视图对象
// 同时用于后台 GET 响应与 PUT 请求；出参额外带回生效域名、来源与派生出的回调地址
type SiteSetting struct {
	BaseURL          string `json:"base_url"`
	SiteName         string `json:"site_name"`
	LogoURL          string `json:"logo_url"`
	FaviconURL       string `json:"favicon_url"`
	CopyrightText    string `json:"copyright_text"`
	ICPNumber        string `json:"icp_number"`
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
		CopyrightText:    cfg.CopyrightText,
		ICPNumber:        cfg.IcpNumber,
		SEODescription:   cfg.SeoDescription,
		SEOKeywords:      cfg.SeoKeywords,
		UpdatedAt:        cfg.UpdatedAt,
		Version:          cfg.Version,
		EffectiveBaseURL: base,
		BaseURLSource:    snap.BaseURLSource,
	}
	setting.WechatNotifyURL = base + wechatPayCallbackPath
	setting.AlipayNotifyURL = base + alipayPayCallbackPath
	setting.AlipayReturnURL = base + h5OrderListHashURL

	response.Success(c, setting)
}

// UpdateSiteSetting 保存站点设置
// 仅超管可写；保存成功后立即失效网关内存快照，并把派生出的支付回调地址回写 pay_config
// 接口: PUT /api/v1/admin/settings/site
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateSiteSetting(c *gin.Context) {
	if ctx := appcontext.GetUserContext(c); ctx.Role != "super_admin" {
		response.Error(c, errcode.NoPermission)
		return
	}

	var req SiteSetting
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	cfg, err := h.userClient.SaveSiteConfig(c.Request.Context(), &user.SaveSiteConfigReq{
		BaseUrl:        req.BaseURL,
		SiteName:       req.SiteName,
		LogoUrl:        req.LogoURL,
		FaviconUrl:     req.FaviconURL,
		CopyrightText:  req.CopyrightText,
		IcpNumber:      req.ICPNumber,
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

	response.Success(c, gin.H{"success": true, "version": cfg.Version})
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
		"copyright_text":  snap.CopyrightText,
		"icp_number":      snap.ICPNumber,
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
