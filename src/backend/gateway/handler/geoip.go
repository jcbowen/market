package handler

import (
	"net"
	"net/netip"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/geoip"
	"github.com/jcbowen/market/src/backend/common/response"
)

// GeoIPHandler IP 归属地定位接口
// 基于本地 GeoLite2-City mmdb 数据库（来源 https://geo.wp-statistics.com/#databases，
// 由 download-geoip.mjs 定期更新，git 不跟踪数据库文件）离线解析出口 IP 的大致经纬度
type GeoIPHandler struct {
	dbPath string
}

// NewGeoIPHandler 创建 IP 定位 handler
// 参数：
//   - dbPath: GeoLite2-City mmdb 数据库路径（相对路径以可执行文件所在目录为基准）
// 返回：
//   - *GeoIPHandler: handler 实例
func NewGeoIPHandler(dbPath string) *GeoIPHandler {
	return &GeoIPHandler{dbPath: dbPath}
}

// Locate 按客户端出口 IP 返回大致经纬度（城市级精度）
// 数据库未就绪、内网 IP 或查询未命中时 data 返回 null，调用方自行降级
// 使用示例：GET /api/v1/geoip/locate
// 参数：
//   - c: gin 上下文
func (h *GeoIPHandler) Locate(c *gin.Context) {
	ipStr := c.ClientIP()
	if ipStr == "" {
		// ClientIP 解析失败时兜底取直连地址
		if host, _, err := net.SplitHostPort(c.Request.RemoteAddr); err == nil {
			ipStr = host
		}
	}

	// IPv6 客户端可能携带 zone（如 fe80::1%eth0），去掉后再解析
	addr, err := netip.ParseAddr(strings.Trim(ipStr, "[]"))
	if err != nil {
		response.Success(c, nil)
		return
	}

	loc := geoip.Lookup(h.dbPath, addr)
	if loc == nil {
		response.Success(c, nil)
		return
	}

	response.Success(c, gin.H{
		"longitude":    loc.Longitude,
		"latitude":     loc.Latitude,
		"country":      loc.Country,
		"country_name": loc.CountryName,
		"city":         loc.City,
	})
}
