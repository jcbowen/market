package middleware

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
)

// userStatusTTL 会员状态进程内缓存时长
// 拉黑后最迟该时长内全网生效，避免每个请求都打一次 user 服务
const userStatusTTL = 30 * time.Second

// userStatusEntry 会员状态缓存条目
type userStatusEntry struct {
	status  int32
	expires time.Time
}

// userStatusCache 会员状态进程内缓存
type userStatusCache struct {
	mu      sync.Mutex
	entries map[int64]userStatusEntry
}

var statusCache = &userStatusCache{entries: make(map[int64]userStatusEntry)}

// getUserStatus 读缓存（过期后由调用方回源）
func (c *userStatusCache) get(id int64) (int32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok || time.Now().After(e.expires) {
		return 0, false
	}
	return e.status, true
}

// set 写缓存
func (c *userStatusCache) set(id int64, st int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 简单防膨胀：条目过多时整体重建（C 端规模下极少触发）
	if len(c.entries) > 10000 {
		c.entries = make(map[int64]userStatusEntry)
	}
	c.entries[id] = userStatusEntry{status: st, expires: time.Now().Add(userStatusTTL)}
}

// UserStatusMiddleware 拦截被拉黑会员的请求
// 挂载于 C 端登录态路由组（AuthMiddleware 之后）：
//   - 会员状态非正常（status != 1）时统一返回「系统错误，请联系工作人员」
//   - 状态走 30 秒进程内缓存；会员不存在按拉黑处理；user 服务临时故障时放行（不因依赖抖动扩大故障面）
//
// 参数：
//   - userClient: user-service gRPC 客户端
// 返回：
//   - gin.HandlerFunc: 会员状态拦截中间件
func UserStatusMiddleware(userClient user.UserServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		uc := appcontext.GetUserContext(c)
		if uc == nil || uc.UserID <= 0 {
			c.Next()
			return
		}

		// 被拉黑后 30 秒内强制回源一次，保证恢复/拉黑操作及时生效感知
		st, ok := statusCache.get(uc.UserID)
		if !ok {
			info, err := userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: uc.UserID})
			switch {
			case err == nil:
				st = info.Status
				statusCache.set(uc.UserID, st)
			case status.Code(err) == codes.NotFound:
				st = 0
			default:
				// 依赖故障放行，避免 user 服务抖动放大为全站不可用
				c.Next()
				return
			}
		}

		if st != 1 {
			response.Error(c, errcode.UserBlocked)
			c.Abort()
			return
		}
		c.Next()
	}
}

// InvalidateUserStatusCache 清除指定会员的状态缓存（拉黑/恢复后调用，立即生效）
// 参数：
//   - id: 会员 ID
func InvalidateUserStatusCache(id int64) {
	statusCache.mu.Lock()
	defer statusCache.mu.Unlock()
	delete(statusCache.entries, id)
}
