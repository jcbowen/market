package middleware

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
)

// RoleSuperAdmin 超管角色编码，拥有所有权限
const RoleSuperAdmin = "super_admin"

// HeaderCommunityID 前端声明的当前社区请求头
const HeaderCommunityID = "X-Community-Id"

// adminContextResolver 实时权限上下文解析器，网关启动时注入 user 服务客户端
var adminContextResolver func(ctx context.Context, adminID, communityID int64) (*user.AdminContextInfo, error)

// SetPermissionResolver 注入权限上下文解析器（user 服务客户端）
// 多社区模型下权限随当前社区变化，无法再依赖登录时的 JWT 快照，
// 必须逐请求按（管理员, 当前社区）实时解析任职角色权限并集
//
// 参数：
//   - client: user 服务 gRPC 客户端
func SetPermissionResolver(client user.UserServiceClient) {
	adminContextResolver = func(ctx context.Context, adminID, communityID int64) (*user.AdminContextInfo, error) {
		return client.GetAdminContext(ctx, &user.GetAdminContextReq{
			AdminId:     adminID,
			CommunityId: communityID,
		})
	}
}

// RequirePermission 构造基于权限编码的鉴权中间件
// 超管直接放行；其他管理员按当前社区语境实时解析权限，具备指定权限之一（或 "*" 通配）即可通过
// 参数：
//   - permissions: 需要的权限编码列表，具备其一即可通过
//
// 返回值：
//   - gin.HandlerFunc: 权限校验中间件
func RequirePermission(permissions ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		uc := appcontext.GetUserContext(c)
		if uc.TokenType != jwt.TokenTypeAdmin {
			response.Error(c, errcode.NoPermission)
			c.Abort()
			return
		}

		ctxInfo := resolveAdminContext(c)
		if ctxInfo == nil {
			response.Error(c, errcode.NoPermission)
			c.Abort()
			return
		}
		if ctxInfo.IsSuper {
			c.Next()
			return
		}

		adminPerms := ctxInfo.Permissions
		if contains(adminPerms, "*") {
			c.Next()
			return
		}
		for _, need := range permissions {
			if contains(adminPerms, need) {
				c.Next()
				return
			}
		}

		response.Error(c, errcode.NoPermission)
		c.Abort()
	}
}

// RequireCommunityAccess 校验请求头声明的当前社区是否对管理员合法
// 超管任意社区均合法；普通管理员的社区须在其绑定范围内
// （先查 JWT 快照，未命中时实时查任职——覆盖登录后新授权的社区），越权直接 403
//
// 返回值：
//   - gin.HandlerFunc: 社区访问校验中间件
func RequireCommunityAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader(HeaderCommunityID)
		id, err := strconv.ParseInt(header, 10, 64)
		if err != nil || id <= 0 {
			// 未携带声明，走默认社区回退逻辑
			c.Next()
			return
		}

		uc := appcontext.GetUserContext(c)
		if uc.Role == RoleSuperAdmin {
			c.Next()
			return
		}
		if containsInt64(uc.CommunityIDs, id) {
			c.Next()
			return
		}

		// 快照未命中（如登录后被追加授权的社区）实时查任职
		ctxInfo := resolveAdminContext(c)
		if ctxInfo != nil && (ctxInfo.IsSuper || ctxInfo.Bound) {
			c.Next()
			return
		}

		response.Error(c, errcode.NoPermission)
		c.Abort()
	}
}

// resolveAdminContext 解析并缓存当前请求的管理员权限上下文
// 同一请求内只解析一次（gin context 记忆化），社区取 X-Community-Id 请求头，缺省回退 JWT 默认社区
//
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - *user.AdminContextInfo: 权限上下文，解析失败返回 nil
func resolveAdminContext(c *gin.Context) *user.AdminContextInfo {
	if v, ok := c.Get("admin_context"); ok {
		if info, ok := v.(*user.AdminContextInfo); ok {
			return info
		}
	}
	if adminContextResolver == nil {
		return nil
	}
	uc := appcontext.GetUserContext(c)
	communityID := uc.CommunityID
	if id, err := strconv.ParseInt(c.GetHeader(HeaderCommunityID), 10, 64); err == nil && id > 0 {
		communityID = id
	}
	info, err := adminContextResolver(c.Request.Context(), uc.UserID, communityID)
	if err != nil || info == nil {
		return nil
	}
	c.Set("admin_context", info)
	return info
}

// getAdminPermissionsFromContext 从 gin 上下文读取管理员权限列表
// 兼容保留：返回当前请求语境下实时解析的权限（原为 JWT 快照）
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - []string: 权限编码列表
func getAdminPermissionsFromContext(c *gin.Context) []string {
	if info := resolveAdminContext(c); info != nil {
		return info.Permissions
	}
	return nil
}

// containsInt64 判断 int64 切片是否包含指定元素
// 参数：
//   - list: int64 切片
//   - item: 目标元素
//
// 返回值：
//   - bool: 是否包含
func containsInt64(list []int64, item int64) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

// contains 判断字符串切片是否包含指定元素
// 参数：
//   - list: 字符串切片
//   - item: 目标元素
//
// 返回值：
//   - bool: 是否包含
func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}
