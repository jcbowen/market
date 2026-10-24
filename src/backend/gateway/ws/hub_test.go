package ws

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

/**
 * CheckSameHostOrigin 同源判定（2026-10-24 握手安全加固）离线用例：不依赖 MySQL/Redis/gRPC
 *
 * 背景：后台 WS 的鉴权凭据已改为 httpOnly cookie，浏览器会对跨站页面自动携带该 cookie，
 * 原先「放行任意 Origin」的口径等于给 CSWSH 开门——恶意页面可在受害者登录态下
 * 连上推送通道、持续窃取本店订单情报。现只放行 Origin 主机名与请求主机名一致的握手。
 *
 * 运行方式：
 *   go test ./gateway/ws/ -run TestCheckSameHostOrigin -v
 *
 * 覆盖：
 *   - 同源放行（含大小写、代理改写端口、IPv6 字面量）；
 *   - Origin 缺失放行（非浏览器客户端不发送该头）；
 *   - 跨站主机名拒绝；Origin 无法解析按拒绝处理（fail-closed）。
 */

// TestCheckSameHostOrigin 逐场景校验放行/拒绝口径
// 参数：
//   - t: 测试句柄
func TestCheckSameHostOrigin(t *testing.T) {
	cases := []struct {
		name   string
		target string // 请求 URL（httptest 据此填 r.Host，即浏览器实际访问的主机）
		origin string // 浏览器 Origin 头，空串表示不携带该头
		want   bool
	}{
		{"同源 https", "https://market.example.com/api/v1/admin/ws", "https://market.example.com", true},
		{"同源大小写不敏感", "https://Market.Example.com/api/v1/admin/ws", "https://market.example.com", true},
		{"同源带端口", "http://localhost:18080/api/v1/admin/ws", "http://localhost:18080", true},
		{
			// 反向代理改端口是常态：vite 的 changeOrigin 把 Host 换成代理目标端口，
			// nginx 的 $host 又会剥掉端口，因此主机名相同即放行
			"同源但代理改写端口",
			"http://localhost:18080/api/v1/admin/ws",
			"http://localhost:3001",
			true,
		},
		{"同源 IPv6 字面量", "http://[::1]:18080/api/v1/admin/ws", "http://[::1]:3001", true},
		{"无 Origin（非浏览器客户端）", "https://market.example.com/api/v1/admin/ws", "", true},
		{"跨站 https", "https://market.example.com/api/v1/admin/ws", "https://evil.com", false},
		{"跨站带端口", "https://market.example.com/api/v1/admin/ws", "https://evil.com:8443", false},
		{
			// 前缀拼接式伪装主机名必须拒绝：market.example.com.evil.com 不是我们的域
			"跨站后缀伪装",
			"https://market.example.com/api/v1/admin/ws",
			"https://market.example.com.evil.com",
			false,
		},
		{"Origin 无法解析", "https://market.example.com/api/v1/admin/ws", "%not-a-url%", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := CheckSameHostOrigin(req); got != tc.want {
				t.Errorf("放行判定 got=%v want=%v (origin=%q host=%q)", got, tc.want, tc.origin, req.Host)
			}
		})
	}
}

// TestUpgraderUsesSameHostOriginCheck Upgrader 必须挂上同源校验
// 回退成「放行任意来源」是本文件要防的回归，用构造器行为直接守卫
// 参数：
//   - t: 测试句柄
func TestUpgraderUsesSameHostOriginCheck(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://market.example.com/api/v1/admin/ws", nil)
	req.Header.Set("Origin", "https://evil.com")
	if Upgrader.CheckOrigin(req) {
		t.Error("Upgrader 仍放行跨站 Origin，CheckOrigin 未挂同源校验")
	}
}
