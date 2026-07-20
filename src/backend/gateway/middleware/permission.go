package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/response"
)

// RoleSuperAdmin 超管角色编码，拥有所有权限
const RoleSuperAdmin = "super_admin"

// RequirePermission 构造基于权限编码的鉴权中间件
// 超管角色直接放行，其他角色需具备指定权限之一
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

		if uc.Role == RoleSuperAdmin {
			c.Next()
			return
		}

		adminPerms := getAdminPermissionsFromContext(c)
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

// getAdminPermissionsFromContext 从 gin 上下文读取管理员权限列表
// 要求上游 AuthMiddleware 已将权限写入上下文 key "admin_permissions"
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - []string: 权限编码列表
func getAdminPermissionsFromContext(c *gin.Context) []string {
	v, ok := c.Get("admin_permissions")
	if !ok {
		return nil
	}
	perms, ok := v.([]string)
	if !ok {
		return nil
	}
	return perms
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
