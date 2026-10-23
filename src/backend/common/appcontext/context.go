package appcontext

import (
	stdctx "context"

	"github.com/gin-gonic/gin"
)

// Key 上下文键类型，避免 key 冲突
type Key string

const (
	// KeyTraceID 链路追踪 ID
	KeyTraceID Key = "trace_id"
	// KeyUserID 用户/管理员 ID
	KeyUserID Key = "user_id"
	// KeyTokenType token 类型 user/admin
	KeyTokenType Key = "token_type"
	// KeyRole 管理员角色编码
	KeyRole Key = "role"
	// KeyCommunityID 社区 ID
	KeyCommunityID Key = "community_id"
	// KeyCommunityIDs 管理员绑定的全部社区 ID 列表
	KeyCommunityIDs Key = "community_ids"
	// KeyPhone 手机号
	KeyPhone Key = "phone"
	// KeyRealIP 真实 IP
	KeyRealIP Key = "real_ip"
)

// UserContext 从 gin 上下文提取的用户信息
type UserContext struct {
	UserID      int64
	TokenType   string
	Role        string
	CommunityID int64
	// CommunityIDs 管理员绑定的全部社区 ID 列表（超管为空，表示不限制社区）
	CommunityIDs []int64
	Phone        string
	TraceID      string
	RealIP       string
}

// SetUserContext 将用户信息写入 gin 上下文
// 参数：
//   - c: gin 上下文
//   - uc: 用户信息
func SetUserContext(c *gin.Context, uc *UserContext) {
	if uc == nil || c == nil {
		return
	}
	c.Set(string(KeyUserID), uc.UserID)
	c.Set(string(KeyTokenType), uc.TokenType)
	c.Set(string(KeyRole), uc.Role)
	c.Set(string(KeyCommunityID), uc.CommunityID)
	c.Set(string(KeyCommunityIDs), uc.CommunityIDs)
	c.Set(string(KeyPhone), uc.Phone)
	c.Set(string(KeyTraceID), uc.TraceID)
	c.Set(string(KeyRealIP), uc.RealIP)
}

// GetUserContext 从 gin 上下文读取用户信息
// 参数：
//   - c: gin 上下文
//
// 返回：
//   - *UserContext: 用户信息，不存在返回零值
func GetUserContext(c *gin.Context) *UserContext {
	if c == nil {
		return &UserContext{}
	}
	uc := &UserContext{}
	if v, ok := c.Get(string(KeyUserID)); ok {
		uc.UserID, _ = v.(int64)
	}
	if v, ok := c.Get(string(KeyTokenType)); ok {
		uc.TokenType, _ = v.(string)
	}
	if v, ok := c.Get(string(KeyRole)); ok {
		uc.Role, _ = v.(string)
	}
	if v, ok := c.Get(string(KeyCommunityID)); ok {
		uc.CommunityID, _ = v.(int64)
	}
	if v, ok := c.Get(string(KeyCommunityIDs)); ok {
		uc.CommunityIDs, _ = v.([]int64)
	}
	if v, ok := c.Get(string(KeyPhone)); ok {
		uc.Phone, _ = v.(string)
	}
	if v, ok := c.Get(string(KeyTraceID)); ok {
		uc.TraceID, _ = v.(string)
	}
	if v, ok := c.Get(string(KeyRealIP)); ok {
		uc.RealIP, _ = v.(string)
	}
	return uc
}

// WithTraceID 将 trace_id 写入标准 context
// 参数：
//   - ctx: 标准上下文
//   - traceID: 链路追踪 ID
//
// 返回：
//   - context.Context: 带有 trace_id 的上下文
func WithTraceID(ctx stdctx.Context, traceID string) stdctx.Context {
	return stdctx.WithValue(ctx, KeyTraceID, traceID)
}

// GetTraceID 从标准 context 读取 trace_id
// 参数：
//   - ctx: 标准上下文
//
// 返回：
//   - string: trace_id
func GetTraceID(ctx stdctx.Context) string {
	if v := ctx.Value(KeyTraceID); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
