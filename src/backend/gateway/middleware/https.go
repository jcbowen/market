package middleware

import (
	"net"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/response"
)

/**
 * 生产环境强制 HTTPS 的兜底中间件（仅用于密码类敏感接口）
 *
 * 防线定位：nginx 层强制 301 跳转是主防线；本中间件防的是「TLS 配置失误/被误关」时
 * 密码以明文过网的兜底——APP_ENV=production 时非 HTTPS 请求直接拒绝，宁可不服务也不泄密。
 *
 * 判定口径：
 *   - 环境变量 REQUIRE_HTTPS=false → 兜底关闭，一律放行（应急豁免，见 EnvRequireHTTPS 的风险说明）
 *   - 原生 TLS（网关自己终结 HTTPS）→ 放行
 *   - X-Forwarded-Proto=https → 仅当直连方（RemoteIP）落在 security.trusted_proxies 网段内才采信。
 *     网关端口公网/内网直达时该头客户端可随意伪造，无条件采信等于兜底失效——
 *     与 ws/hub.go 同源校验「不采信客户端可控头」的口径一致
 *   - 非生产环境（本地开发、内网 HTTP 部署）直接放行，保持零配置可用
 */

// EnvRequireHTTPS 生产 HTTPS 兜底的显式开关名（仅 gateway 进程读取，不经配置文件）
// 设 "false"（忽略大小写）= 关闭兜底；未设置/其余值 = 兜底开启。
// 仅作应急豁免：前置链路（如 CDN/DCDN 动态回源协议）一时无法保证 HTTPS 回源、又需临时恢复登录时使用，
// 关闭后若回源仍是 HTTP，登录密码（兼容口径不带 pwd_alg 也受理）与 token 均明文过网，链路修复后应删除恢复
const EnvRequireHTTPS = "REQUIRE_HTTPS"

// httpsGuard 预解析的可信代理网段匹配器（构造一次，请求热路径只做 CIDR 包含判断）
type httpsGuard struct {
	cidrs []*net.IPNet
}

// newHTTPSGuard 解析可信代理网段（CIDR 或单个 IP，与 security.trusted_proxies 同源同格式）
// 非法网段跳过：配置合法性已由 main.go applyTrustedProxies 的 SetTrustedProxies fail-fast 保证，
// 此处兜底防御不让单个坏值拖垮启动
//
// 参数：
//   - proxies: 可信代理网段列表
//
// 返回值：
//   - *httpsGuard: 匹配器（空列表 = 不采信任何代理头）
func newHTTPSGuard(proxies []string) *httpsGuard {
	g := &httpsGuard{}
	for _, p := range proxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			// 单个 IP 补全为定长网段：v4 用 /32、v6 用 /128
			if ip := net.ParseIP(p); ip != nil {
				if ip.To4() != nil {
					p += "/32"
				} else {
					p += "/128"
				}
			}
		}
		if _, ipnet, err := net.ParseCIDR(p); err == nil {
			g.cidrs = append(g.cidrs, ipnet)
		}
	}
	return g
}

// allows 判断直连地址是否落在可信代理网段内
//
// 参数：
//   - ip: 请求直连地址（RemoteIP），nil 视为不可信
//
// 返回值：
//   - bool: true = 可信代理来源，其代理头可采信
func (g *httpsGuard) allows(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, cidr := range g.cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// RequireHTTPS 生产环境强制 HTTPS 的兜底中间件（仅用于密码类敏感接口）
//
// 兜底可用环境变量 REQUIRE_HTTPS 显式关闭（见 EnvRequireHTTPS）：仅作链路应急豁免，
// 关闭后若回源仍是 HTTP，明文密码请求与 token 均裸奔；未设置/其余值 = 兜底开启。
//
// 参数：
//   - trustedProxies: 可信反向代理网段（security.trusted_proxies，决定 X-Forwarded-Proto 是否采信）
//
// 返回值：
//   - gin.HandlerFunc: 中间件（拒绝时返回 errcode.HTTPSRequired 且不执行后续 handler）
//
// 异常：不 panic；非法网段已在 newHTTPSGuard 内跳过
//
// 使用示例：
//
//	admin.POST("/auth/login", gwMiddleware.RateLimitMiddleware(5, 50), gwMiddleware.RequireHTTPS(cfg.Security.TrustedProxies), adminHandler.Login)
func RequireHTTPS(trustedProxies []string) gin.HandlerFunc {
	guard := newHTTPSGuard(trustedProxies)
	return func(c *gin.Context) {
		// 显式开关优先：false（忽略大小写）= 应急关闭兜底，一律放行
		if strings.EqualFold(os.Getenv(EnvRequireHTTPS), "false") {
			c.Next()
			return
		}
		// 非生产放行：本地开发（http://localhost / 内网 IP）与管理端 HTTP 内网部署不受影响
		if !config.IsProduction() {
			c.Next()
			return
		}
		if c.Request.TLS != nil {
			c.Next()
			return
		}
		// 直连方是可信代理（nginx/CDN 终结 HTTPS 后回源）才采信其声明的前置协议；
		// RemoteIP 解析失败（空串）按不可信处理
		if strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") && guard.allows(net.ParseIP(c.RemoteIP())) {
			c.Next()
			return
		}
		response.Error(c, errcode.HTTPSRequired)
		c.Abort()
	}
}
