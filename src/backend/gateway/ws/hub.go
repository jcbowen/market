// Package ws 管理后台 WebSocket 推送
// hub 维护在线管理员连接（按社区分组），Redis 订阅器接收 order 服务的支付成功事件，
// 过滤社区后逐连接推送。握手鉴权复用 gateway 既有 admin JWT + 权限链（见 middleware.WSAuthMiddleware）
package ws

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket 连接参数
const (
	// writeWait 单次写超时（控制帧与普通消息共用）
	writeWait = 10 * time.Second
	// pongWait 读超时上限：超过该时长未收到任何对端数据（含 pong）即断开
	pongWait = 60 * time.Second
	// pingPeriod 服务端 ping 间隔，须小于 pongWait（浏览器协议层自动回 pong）
	pingPeriod = (pongWait * 9) / 10
	// sendBufferSize 单连接发送缓冲条数：推高峰值时丢弃最慢的连接而非阻塞广播
	sendBufferSize = 16
	// maxMessageSize 客户端上行消息大小上限（本协议客户端无需上行业务数据，仅容忍 pong）
	maxMessageSize = 512
)

// 服务端下发消息 type 标识
const (
	// MsgTypeHello 握手成功后的欢迎消息
	MsgTypeHello = "hello"
	// MsgTypePing 服务端应用层心跳（HTTP 轮询兼容用；WS 通道以协议层 ping 为准）
	MsgTypePing = "ping"
)

// Message 服务端下发的统一消息结构
type Message struct {
	// Type 消息类型：hello / order.paid
	Type string `json:"type"`
	// Data 业务载荷，随 Type 变化
	Data any `json:"data,omitempty"`
}

// Upgrader 升级器（导出供握手 handler 使用）
// CheckOrigin 只放行与请求同主机的握手：管理端与网关同源部署，浏览器带来的 Origin 主机名
// 必然等于本次请求的主机名；跨站页面带着受害管理员的 httpOnly 登录 cookie 发起握手时
// Origin 是外站域名，直接拒绝——这是 WS 通道对 CSWSH（跨站 WebSocket 劫持）的收口，
// cookie 承载鉴权后 JWT 不再出现在 URL 里，光靠「令牌校验兜底」挡不住自动携带 cookie 的跨源连接
var Upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     CheckSameHostOrigin,
}

// CheckSameHostOrigin 判定 WebSocket 握手是否与页面同主机
//
// 参数：
//   - r: WebSocket 握手请求
//
// 返回值：
//   - bool: 放行则为 true——Origin 缺失（非浏览器客户端不发送该头，如原生 POS/探测脚本），
//     或 Origin 的主机名（忽略端口与大小写）等于本次请求的主机名
//
// 异常：
//   - 无错误返回；Origin 无法解析按拒绝处理（fail-closed）
//
// 使用示例：
//
//	var Upgrader = websocket.Upgrader{CheckOrigin: ws.CheckSameHostOrigin}
//
// 说明：主机名比对刻意忽略端口——反向代理到源站时端口口径天然不一致
// （vite 的 changeOrigin 把 Host 改成代理目标端口，nginx 的 $host 又会剥掉端口），
// 只比端口反而会误杀合法同源部署；跨主机名才是本校验要拦的攻击面
//
// 说明2：不从 X-Forwarded-Host 取"真实主机名"——该头客户端可控，网关直接暴露时
// 攻击者将其与 Origin 设为同值即可通过校验，等于把同源判定交给可伪造头；r.Host 的
// 可信度由部署规范收口：nginx 统一 proxy_set_header Host $host 把浏览器访问的
// 域名还原进 Host（见 deploy/nginx.conf.example 与 10-部署方案/03-生产环境部署.md），
// 因此直接比 r.Host 即为比对真实访问主机名（安全评审结论 2026-10-24，维持现状）
func CheckSameHostOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		log.Printf("[ws] 拒绝握手：Origin 无法解析 origin=%q remote=%s", origin, r.RemoteAddr)
		return false
	}
	if hostName(u.Host) != hostName(r.Host) {
		log.Printf("[ws] 拒绝跨源握手 origin=%q host=%s remote=%s", origin, r.Host, r.RemoteAddr)
		return false
	}
	return true
}

// hostName 取 host:port 里的主机名部分并转小写
//
// 参数：
//   - host: 形如 "example.com:8080" 或 "[::1]:18080" 的主机串
//
// 返回值：
//   - string: 主机名（无端口时原样返回，仅大小写归一）
func hostName(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(h)
	}
	// 无端口：IPv6 字面量带方括号，去括号后即为主机名
	return strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
}

// Client 单个在线管理员的 WebSocket 连接
type Client struct {
	hub *Hub
	// conn 已升级的连接（升级时 gorilla 已清除 HTTP 服务器设置的超时，长连接不受 WriteTimeout 影响）
	conn *websocket.Conn
	// send 待下发消息缓冲，写入失败即断开该连接
	send chan []byte
	// communityID 握手时声明的当前社区（X-Community-Id 请求头 / 查询参数映射），事件按此过滤
	communityID int64
	// adminID 管理员 ID（日志定位用）
	adminID int64
	// username 管理员账号（日志定位用）
	username string
}

// Hub 在线连接集合与广播
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
}

// NewHub 创建连接集线器
//
// 返回值：
//   - *Hub: 集线器实例
//
// 使用示例：
//
//	hub := ws.NewHub()
func NewHub() *Hub {
	return &Hub{clients: make(map[*Client]struct{})}
}

// Register 登记新连接（握手成功后调用）
//
// 参数：
//   - c: 客户端连接
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

// Unregister 注销连接（读循环退出时调用，幂等）
//
// 参数：
//   - c: 客户端连接
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		return
	}
	delete(h.clients, c)
	close(c.send)
}

// Count 当前在线连接数（健康信息用）
//
// 返回值：
//   - int: 连接数
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// BroadcastToCommunity 向指定社区的全部在线连接推送消息
// 发送缓冲已满的连接直接丢弃消息（不断开）：通知类消息允许错过，不能因慢连接阻塞整轮广播
//
// 参数：
//   - communityID: 目标社区 ID（<=0 表示未声明社区，不推送）
//   - msg: 待下发消息
func (h *Hub) BroadcastToCommunity(communityID int64, msg *Message) {
	if communityID <= 0 {
		return
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[ws] 消息序列化失败 type=%s err=%v", msg.Type, err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.communityID != communityID {
			continue
		}
		select {
		case c.send <- payload:
		default:
			log.Printf("[ws] 连接发送缓冲已满，丢弃消息 admin=%s(%d) community=%d",
				c.username, c.adminID, c.communityID)
		}
	}
}

// Attach 将已升级的连接挂入集线器：登记后先下发 hello，再启动读写循环
// 调用前置条件：路由链已完成 admin JWT 鉴权、社区访问校验与 order:view 权限校验，
// UserContext 已注入 gin 上下文
//
// 参数：
//   - c: 客户端连接
func (h *Hub) Attach(c *Client) {
	h.Register(c)
	go c.writePump()
	// hello 必须先于 readPump 启动写入（send 有缓冲，writePump 随后取走）：
	// readPump 退出即触发 Unregister 关闭 send，若 hello 排在其后，
	// 连接瞬断时会对已关闭 channel 发送而 panic
	c.SendJSON(&Message{Type: MsgTypeHello})
	go c.readPump()
}

// NewClient 由已升级连接构造客户端
//
// 参数：
//   - hub: 集线器
//   - conn: 已升级连接
//   - communityID: 当前社区 ID
//   - adminID: 管理员 ID
//   - username: 管理员账号
//
// 返回值：
//   - *Client: 客户端实例
//
// 使用示例：
//
//	conn, err := upgrader.Upgrade(w, r, nil)
//	client := ws.NewClient(hub, conn, communityID, adminID, username)
//	hub.ServeHTTP(client)
func NewClient(hub *Hub, conn *websocket.Conn, communityID, adminID int64, username string) *Client {
	return &Client{
		hub:         hub,
		conn:        conn,
		send:        make(chan []byte, sendBufferSize),
		communityID: communityID,
		adminID:     adminID,
		username:    username,
	}
}

// SendJSON 向连接下发一条 JSON 消息（缓冲满时丢弃并记日志）
//
// 参数：
//   - msg: 待下发消息
func (c *Client) SendJSON(msg *Message) {
	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[ws] 消息序列化失败 type=%s err=%v", msg.Type, err)
		return
	}
	select {
	case c.send <- payload:
	default:
		log.Printf("[ws] 连接发送缓冲已满，丢弃消息 admin=%s(%d)", c.username, c.adminID)
	}
}

// readPump 读循环：仅维护存活（响应 pong 刷新读超时），客户端无上行业务数据
// 退出时注销连接并关闭底层连接
func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// writePump 写循环：转发 send 缓冲消息并周期性发协议层 ping（浏览器自动回 pong）
// send 通道由 Unregister 关闭，读到关闭即退出
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
