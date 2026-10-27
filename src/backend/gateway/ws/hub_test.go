package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/jcbowen/market/src/backend/common/eventbus"
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

/**
 * 服务端权限过滤与定向踢线（2026-10-27 越权收口）离线用例：不依赖 MySQL/Redis/gRPC
 *
 * 背景：WS 通道承载订单情报（order.paid / order.refund）与权限变更提醒两类事件，
 * 原先服务端把数据类事件无差别下发给同社区的全部连接，「能否看到订单」只由前端
 * hasPermission 判定——原生 WebSocket 直连读帧不受前端约束，等于把边界放在攻击者
 * 可控的一侧。现改为握手时定格权限快照、推送时刻按快照过滤；快照过期的出路是
 * 提醒送达后定向踢线（浏览器 onclose 自动重连即刷新）。
 * 安全边界必须有回归覆盖，本组用例锁死这两条口径。
 *
 * 运行方式：
 *   go test ./gateway/ws/ -run 'TestBroadcast|TestKick|TestNewClientPermissions|TestZeroID' -v
 *
 * 覆盖：
 *   - 数据类事件只送达持 order:view（或超管/通配）的连接；
 *   - 新增数据类事件已在 msgRequiredPerm 登记（漏登记即向无权限连接下发业务数据）；
 *   - 导出任务事件按创建人定向，定向不是绕过权限门槛的后门；
 *   - 未登记权限要求的类型（权限变更提醒）恒送达——权限被收窄者仍需收到提醒；
 *   - 快照缺失（perms 为 nil）按无权限处理，收不到数据类事件；
 *   - ID<=0 的连接不参与社区/管理员定向匹配（遗留全局角色 community_id=0 不得命中）；
 *   - Kick 系列注销后在线数下降、send 关闭、后续广播不 panic（send on closed channel）。
 */

// registerTestClient 登记一个不带真实 WebSocket 连接的客户端
// conn 仅在 readPump/writePump 中使用，本组用例只操作 hub 与 send 缓冲，传 nil 安全
// 参数：
//   - h: 集线器
//   - communityID: 该连接声明的当前社区
//   - adminID: 管理员 ID
//   - username: 管理员账号（日志定位）
//   - perms: 权限快照
//
// 返回值：
//   - *Client: 已登记的客户端
func registerTestClient(h *Hub, communityID, adminID int64, username string, perms *ClientPermissions) *Client {
	c := NewClient(h, nil, communityID, adminID, username, perms)
	h.Register(c)
	return c
}

// receivedTypes 非阻塞取走连接缓冲里已下发消息的 type
// 连接被注销（send 已关闭）时读到 !ok 直接返回，不会因关闭通道恒可读而死循环
//
// 参数：
//   - t: 测试句柄
//   - c: 客户端连接
//
// 返回值：
//   - []string: 收到的消息类型（按下发顺序）
func receivedTypes(t *testing.T, c *Client) []string {
	t.Helper()
	got := make([]string, 0, 4)
	for {
		select {
		case payload, ok := <-c.send:
			if !ok {
				return got
			}
			var msg Message
			if err := json.Unmarshal(payload, &msg); err != nil {
				t.Fatalf("消息反序列化失败: %v", err)
			}
			got = append(got, msg.Type)
		default:
			return got
		}
	}
}

// TestNewClientPermissions 权限快照构造口径
// 参数：
//   - t: 测试句柄
func TestNewClientPermissions(t *testing.T) {
	cases := []struct {
		name    string
		isSuper bool
		codes   []string
		need    string
		want    bool
	}{
		{"超管可收全部", true, nil, PermOrderView, true},
		{"通配可收全部", false, []string{"*"}, PermOrderView, true},
		{"显式持有可收", false, []string{"product:view", PermOrderView}, PermOrderView, true},
		{"未持有则拒", false, []string{"product:view"}, PermOrderView, false},
		{"空权限则拒", false, nil, PermOrderView, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{perms: NewClientPermissions(tc.isSuper, tc.codes)}
			if got := c.canRecv(eventbus.MsgTypeOrderPaid); got != tc.want {
				t.Errorf("canRecv(order.paid) got=%v want=%v", got, tc.want)
			}
		})
	}
	// 未登记的类型不受门槛约束：权限变更提醒必须送达所有连接
	gated := (&Client{perms: NewClientPermissions(false, nil)}).canRecv(eventbus.MsgTypeAdminPermissionChanged)
	if !gated {
		t.Error("权限变更提醒被门槛过滤，被收窄权限的管理员将收不到通知")
	}
}

// TestBroadcastFiltersByPermissionSnapshot 数据类事件按快照过滤、提醒类事件恒送达
// 参数：
//   - t: 测试句柄
func TestBroadcastFiltersByPermissionSnapshot(t *testing.T) {
	h := NewHub()
	// 同社区三种身份：无订单权限的店员、有订单权限的店员、超管；外加异社区持权限者
	noPerm := registerTestClient(h, 1, 11, "no-perm", NewClientPermissions(false, []string{"product:view"}))
	withPerm := registerTestClient(h, 1, 12, "with-perm", NewClientPermissions(false, []string{"product:view", PermOrderView}))
	super := registerTestClient(h, 1, 13, "super", NewClientPermissions(true, nil))
	elsewhere := registerTestClient(h, 2, 14, "elsewhere", NewClientPermissions(false, []string{PermOrderView}))

	h.BroadcastToCommunity(1, &Message{Type: eventbus.MsgTypeOrderPaid, Data: map[string]any{"order_no": "X"}})
	h.BroadcastToCommunity(1, &Message{Type: eventbus.MsgTypeOrderRefund})

	if got := receivedTypes(t, noPerm); len(got) != 0 {
		t.Errorf("无 order:view 的连接收到了数据类事件: %v", got)
	}
	if got := receivedTypes(t, withPerm); len(got) != 2 {
		t.Errorf("持权限连接收到的事件数 got=%d want=2 (%v)", len(got), got)
	}
	if got := receivedTypes(t, super); len(got) != 2 {
		t.Errorf("超管连接收到的事件数 got=%d want=2 (%v)", len(got), got)
	}
	if got := receivedTypes(t, elsewhere); len(got) != 0 {
		t.Errorf("异社区连接收到了他社区订单事件: %v", got)
	}

	// 同一批连接再收提醒类事件：无权限的那个也必须收到（否则权限被收窄者收不到通知）
	h.BroadcastToCommunities([]int64{1}, &Message{Type: eventbus.MsgTypeAdminPermissionChanged})
	if got := receivedTypes(t, noPerm); len(got) != 1 {
		t.Errorf("无订单权限的连接收不到权限变更提醒: %v", got)
	}
	if got := receivedTypes(t, withPerm); len(got) != 1 {
		t.Errorf("持权限连接收不到权限变更提醒: %v", got)
	}
}

// TestBroadcastToAdminsRespectsSnapshot 定向推送同样受权限快照约束
// BroadcastToAdmins 用于「只影响某人」的变更，不能因为定向就绕过数据类事件门槛
// 参数：
//   - t: 测试句柄
func TestBroadcastToAdminsRespectsSnapshot(t *testing.T) {
	h := NewHub()
	target := registerTestClient(h, 1, 21, "target", NewClientPermissions(false, []string{"product:view"}))
	other := registerTestClient(h, 1, 22, "other", NewClientPermissions(false, []string{PermOrderView}))

	h.BroadcastToAdmins([]int64{21}, &Message{Type: eventbus.MsgTypeOrderPaid})
	if got := receivedTypes(t, target); len(got) != 0 {
		t.Errorf("定向推送绕过了权限门槛: %v", got)
	}
	if got := receivedTypes(t, other); len(got) != 0 {
		t.Errorf("非目标管理员收到了定向消息: %v", got)
	}

	h.BroadcastToAdmins([]int64{21}, &Message{Type: eventbus.MsgTypeAdminPermissionChanged})
	if got := receivedTypes(t, target); len(got) != 1 {
		t.Errorf("目标管理员收不到定向提醒: %v", got)
	}
}

// TestBroadcastIgnoresNonPositiveIDs ID<=0 是「全局」占位，不得命中任何连接
// 遗留全局角色的 community_id 就是 0，历史上它被当成普通社区 ID 传进广播后
// 匹配不到连接、事件静默丢失；这里固化「0 不参与匹配」的判定
// 参数：
//   - t: 测试句柄
func TestBroadcastIgnoresNonPositiveIDs(t *testing.T) {
	h := NewHub()
	c := registerTestClient(h, 1, 31, "c", NewClientPermissions(false, []string{PermOrderView}))

	h.BroadcastToCommunities([]int64{0, -1}, &Message{Type: eventbus.MsgTypeAdminPermissionChanged})
	h.BroadcastToAdmins([]int64{0}, &Message{Type: eventbus.MsgTypeAdminPermissionChanged})
	if got := receivedTypes(t, c); len(got) != 0 {
		t.Errorf("占位 ID 命中了连接: %v", got)
	}

	// 全局事件走 BroadcastAll：不针对任何 ID，所有连接均可达
	h.BroadcastAll(&Message{Type: eventbus.MsgTypeAdminPermissionChanged})
	if got := receivedTypes(t, c); len(got) != 1 {
		t.Errorf("BroadcastAll 未送达: %v", got)
	}
}

// TestKickClosesMatchedClients 踢线后在线数下降、通道关闭、后续广播不 panic
// 参数：
//   - t: 测试句柄
func TestKickClosesMatchedClients(t *testing.T) {
	h := NewHub()
	kicked := registerTestClient(h, 1, 41, "kicked", NewClientPermissions(false, []string{PermOrderView}))
	kept := registerTestClient(h, 2, 42, "kept", NewClientPermissions(false, []string{PermOrderView}))

	if n := h.KickAdmins([]int64{41}); n != 1 {
		t.Errorf("KickAdmins 断开数 got=%d want=1", n)
	}
	if h.Count() != 1 {
		t.Fatalf("踢线后在线数 got=%d want=1", h.Count())
	}
	// Unregister 关闭 send：此处读到通道关闭并返回空，同时验证不会 panic
	if got := receivedTypes(t, kicked); len(got) != 0 {
		t.Errorf("被踢连接仍有积压消息: %v", got)
	}

	// 已注销连接不在集合内，后续广播只命中存活连接（曾经的 send on closed channel 竞态点）
	h.BroadcastAll(&Message{Type: eventbus.MsgTypeAdminPermissionChanged})
	if got := receivedTypes(t, kept); len(got) != 1 {
		t.Errorf("存活连接未收到踢线后的广播: %v", got)
	}

	// 幂等：重复踢同一目标不报错、计数为 0
	if n := h.KickCommunities([]int64{1}); n != 0 {
		t.Errorf("重复踢线断开数 got=%d want=0", n)
	}
	if n := h.KickAll(); n != 1 {
		t.Errorf("KickAll 断开数 got=%d want=1", n)
	}
	if h.Count() != 0 {
		t.Errorf("KickAll 后在线数 got=%d want=0", h.Count())
	}
	// 空集广播仍不应 panic
	h.BroadcastToCommunity(2, &Message{Type: eventbus.MsgTypeOrderPaid})
}

// TestAllDataClassTypesAreGated 数据类事件权限门槛全量回归
// 门槛清单的唯一来源是 eventbus.DataClassMsgTypes，msgRequiredPerm 由它生成；
// 新增数据类事件只需往清单追加一项即被本用例自动覆盖，不需要再改这里的硬编码列表
// （本用例同时盯住反向漂移：提醒类被误登记、或映射表被绕过私自加项）
//
// 参数：
//   - t: 测试句柄
func TestAllDataClassTypesAreGated(t *testing.T) {
	// 清单完整性：现存的 4 类数据事件一项都不能少
	for _, want := range []string{
		eventbus.MsgTypeOrderPaid,
		eventbus.MsgTypeOrderRefund,
		eventbus.MsgTypeExportTaskChanged,
		eventbus.MsgTypePaymentAnomalyDetected,
	} {
		if !slices.Contains(eventbus.DataClassMsgTypes, want) {
			t.Errorf("%s 未列入 DataClassMsgTypes，会向无权限连接下发业务数据", want)
		}
	}

	none := NewClientPermissions(false, nil)
	for _, typ := range eventbus.DataClassMsgTypes {
		perm, ok := msgRequiredPerm[typ]
		if !ok {
			t.Errorf("%s 未登记 msgRequiredPerm，会向无权限连接下发业务数据", typ)
			continue
		}
		if perm != PermOrderView {
			t.Errorf("%s 权限门槛口径漂移 got=%q want=%q", typ, perm, PermOrderView)
		}
		if (&Client{perms: none}).canRecv(typ) {
			t.Errorf("%s 无权限连接仍可接收", typ)
		}
	}

	// 提醒类：刻意不设门槛，登记进来就是回归
	reminderTypes := []string{
		eventbus.MsgTypeAdminPermissionChanged,
		MsgTypeHello,
		MsgTypePing,
	}
	for _, typ := range reminderTypes {
		if _, ok := msgRequiredPerm[typ]; ok {
			t.Errorf("%s 属提醒类，不应登记权限门槛（被收窄权限者将收不到通知）", typ)
		}
		if !(&Client{perms: none}).canRecv(typ) {
			t.Errorf("%s 被门槛过滤", typ)
		}
	}

	// 反向漂移：映射表若被绕过清单私自加项，说明两处口径开始各说各话
	for typ := range msgRequiredPerm {
		if !slices.Contains(eventbus.DataClassMsgTypes, typ) {
			t.Errorf("msgRequiredPerm 存在清单外的登记项 %s，应统一走 DataClassMsgTypes", typ)
		}
	}
}

// TestExportTaskEventTargetedAndGated 导出任务事件：只达创建人，且定向不绕过权限门槛
// 参数：
//   - t: 测试句柄
func TestExportTaskEventTargetedAndGated(t *testing.T) {
	h := NewHub()
	creator := registerTestClient(h, 1, 41, "creator", NewClientPermissions(false, []string{PermOrderView}))
	// 创建人本人若无订单权限，同样不该被「定向」放行
	creatorNoPerm := registerTestClient(h, 1, 42, "creator-no-perm", NewClientPermissions(false, []string{"product:view"}))
	colleague := registerTestClient(h, 1, 43, "colleague", NewClientPermissions(false, []string{PermOrderView}))

	msg := &Message{Type: eventbus.MsgTypeExportTaskChanged, Data: map[string]any{"task_id": 9, "state": eventbus.ExportTaskStateDone}}
	h.BroadcastToAdmins([]int64{41}, msg)
	h.BroadcastToAdmins([]int64{42}, msg)

	if got := receivedTypes(t, creator); len(got) != 1 || got[0] != eventbus.MsgTypeExportTaskChanged {
		t.Errorf("创建人未收到本人为自己任务的下发: %v", got)
	}
	if got := receivedTypes(t, creatorNoPerm); len(got) != 0 {
		t.Errorf("定向推送绕过了权限门槛: %v", got)
	}
	if got := receivedTypes(t, colleague); len(got) != 0 {
		t.Errorf("非创建人收到了他人的导出事件: %v", got)
	}
}

// TestPaymentAnomalyEventCommunityGated 支付异常事件：按社区广播且受权限门槛约束
// 事件带挂账单号与金额属业务数据；community_id=0（未知归属）不得命中任何社区连接，
// 超管的全量可见性走异常队列 HTTP 接口，WS 不例外放行
// 参数：
//   - t: 测试句柄
func TestPaymentAnomalyEventCommunityGated(t *testing.T) {
	h := NewHub()
	withPerm := registerTestClient(h, 1, 51, "with-perm", NewClientPermissions(false, []string{"product:view", PermOrderView}))
	noPerm := registerTestClient(h, 1, 52, "no-perm", NewClientPermissions(false, []string{"product:view"}))
	elsewhere := registerTestClient(h, 2, 53, "elsewhere", NewClientPermissions(false, []string{PermOrderView}))
	unknownCommunity := registerTestClient(h, 3, 54, "unknown-community", NewClientPermissions(false, []string{PermOrderView}))

	// 正常社区归属：同社区持权限者可达，无权限与异社区不可达
	h.BroadcastToCommunity(1, &Message{Type: eventbus.MsgTypePaymentAnomalyDetected, Data: map[string]any{"payment_no": "P1"}})
	if got := receivedTypes(t, withPerm); len(got) != 1 || got[0] != eventbus.MsgTypePaymentAnomalyDetected {
		t.Errorf("同社区持权限连接未收到支付异常告警: %v", got)
	}
	if got := receivedTypes(t, noPerm); len(got) != 0 {
		t.Errorf("无 order:view 的连接收到了支付异常告警: %v", got)
	}
	if got := receivedTypes(t, elsewhere); len(got) != 0 {
		t.Errorf("异社区连接收到了他社区支付异常告警: %v", got)
	}

	// 未知归属（community_id=0）：按「不推送」处理，不等于全员广播
	h.BroadcastToCommunity(0, &Message{Type: eventbus.MsgTypePaymentAnomalyDetected, Data: map[string]any{"payment_no": "P2"}})
	if got := receivedTypes(t, unknownCommunity); len(got) != 0 {
		t.Errorf("community_id=0 的告警命中了社区连接: %v", got)
	}
}
