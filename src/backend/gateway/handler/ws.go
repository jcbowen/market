package handler

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/gateway/ws"
)

// WSHandler 管理后台 WebSocket 握手处理器
type WSHandler struct {
	hub *ws.Hub
}

// NewWSHandler 创建 WebSocket 握手处理器
//
// 参数：
//   - hub: WebSocket 集线器（gateway 启动时创建，与事件订阅器共享）
//
// 返回值：
//   - *WSHandler: 处理器实例
//
// 使用示例：
//
//	h := handler.NewWSHandler(hub)
func NewWSHandler(hub *ws.Hub) *WSHandler {
	return &WSHandler{hub: hub}
}

// Handle 升级 WebSocket 连接并挂入集线器
// 路由链已保证：admin JWT 鉴权（WSAuthMiddleware）→ 社区访问校验 → order:view 权限校验。
// 握手声明的当前社区（X-Community-Id / 查询参数映射）决定后续事件推送范围，
// 前端切换社区须断开重连以刷新过滤维度
//
// 参数：
//   - c: gin 上下文
//
// 异常：
//   - 升级失败时 upgrader 已写入错误响应，直接返回（不重复写）
func (h *WSHandler) Handle(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	if uc == nil {
		// 防御分支：鉴权链完整时不会出现
		c.AbortWithStatus(401)
		return
	}
	// 社区取请求头（可能由查询参数映射而来），缺省回退 JWT 登录快照，与 resolveAdminContext 同口径
	communityID := uc.CommunityID
	if id, err := strconv.ParseInt(c.GetHeader("X-Community-Id"), 10, 64); err == nil && id > 0 {
		communityID = id
	}

	conn, err := ws.Upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("[ws] 连接升级失败 admin=%s(%d): %v", c.GetString("admin_username"), uc.UserID, err)
		return
	}
	client := ws.NewClient(h.hub, conn, communityID, uc.UserID, c.GetString("admin_username"))
	h.hub.Attach(client)
	log.Printf("[ws] 管理员接入 admin=%s(%d) community=%d 在线数=%d",
		c.GetString("admin_username"), uc.UserID, communityID, h.hub.Count())
}
