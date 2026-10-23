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

// HasPermission 判断当前管理员在现社区语境下是否具备指定权限（超管与 "*" 通配恒为真）
// 用于同一接口内部对敏感字段再做收窄（如支付报文原文只在 payment:raw:view 下回传），
// 路由级鉴权请继续用 RequirePermission，两者共用同一份实时权限上下文缓存
//
// 参数：
//   - c: gin 上下文
//   - permission: 权限编码
//
// 返回值：
//   - bool: 是否具备；权限上下文解析失败按不具备处理（宁可少给，不可多给）
func HasPermission(c *gin.Context, permission string) bool {
	ctxInfo := resolveAdminContext(c)
	if ctxInfo == nil {
		return false
	}
	if ctxInfo.IsSuper || contains(ctxInfo.Permissions, "*") {
		return true
	}
	return contains(ctxInfo.Permissions, permission)
}

// RequireCommunityAccess 校验请求头声明的当前社区是否对管理员合法
// 一律以 GetAdminContext 的实时口径为准（P1-6）：JWT 快照里的 Role/CommunityIDs 是登录时
// 绘制的，降权或解除任职后照抄快照等于把旧授权续到令牌过期。
// 实时解析不可用（user 服务故障）时拒绝（fail-closed），与 RequirePermission 同口径——
// 同一请求后续若挂了权限码在本场景本就 403，回退快照换不到可用性，只会留下越权面。
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

		ctxInfo := resolveAdminContext(c)
		if ctxInfo == nil {
			response.Error(c, errcode.NoPermission)
			c.Abort()
			return
		}
		if ctxInfo.IsSuper || ctxInfo.Bound {
			c.Next()
			return
		}
		response.Error(c, errcode.NoPermission)
		c.Abort()
	}
}

// IsSuperAdmin 判断当前请求的管理员账号是否为超级管理员（实时口径）
// 取 GetAdminContext 的 IsSuper——它是账号级属性，与社区语境无关，停用与降权即时生效；
// 实时解析不可用时按非超管处理（fail-closed），与 RequirePermission 一致。
// 非管理员令牌一律返回 false：实时上下文以员工 ID 取数，会员令牌不得复用该判定。
//
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - bool: true 表示当前账号是超管
//
// 使用示例：
//   - if !gwMiddleware.IsSuperAdmin(c) {
//     response.Error(c, errcode.NoPermission)
//     return
//     }
func IsSuperAdmin(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if uc := appcontext.GetUserContext(c); uc == nil || uc.TokenType != jwt.TokenTypeAdmin {
		return false
	}
	ctxInfo := resolveAdminContext(c)
	return ctxInfo != nil && ctxInfo.IsSuper
}

// AdminCommunityAccessible 判断当前请求头（X-Community-Id）声明的社区对管理员是否合法（实时口径，P1-10）
// 超管任意社区合法；普通管理员以 GetAdminContext 的 Bound（该社区有任职）为准——
// JWT 快照里的 CommunityIDs 与 Role 都是登录时绘制的，授权变更（追加/撤销任职、降权）后
// 需重登才刷新，不能作为数据归属判定依据。与 RequireCommunityAccess 共用同一请求内记忆化的
// 实时上下文（gin context 记忆化，同一请求最多一次 RPC）
// 未携带请求头时按默认社区解析——调用方（如 getAdminCommunityID）只在
// 「请求头存在且合法」的分支里使用本函数，不会误用该语义
//
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - bool: 合法返回 true；实时上下文解析失败按不合法处理（调用方回退默认社区，
//     最终的访问拒绝由 RequirePermission/RequireCommunityAccess 中间件负责）
func AdminCommunityAccessible(c *gin.Context) bool {
	ctxInfo := resolveAdminContext(c)
	return ctxInfo != nil && (ctxInfo.IsSuper || ctxInfo.Bound)
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
	if uc == nil {
		return nil
	}
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
