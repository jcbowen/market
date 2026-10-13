package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/response"
)

// AuthMiddleware 构造 JWT 鉴权中间件
// 参数：
//   - manager: JWT 管理器
//   - allowedTypes: 允许的 token 类型，如 user、admin
// 返回：
//   - gin.HandlerFunc: 鉴权中间件
func AuthMiddleware(manager *jwt.Manager, allowedTypes ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedTypes))
	for _, t := range allowedTypes {
		allowed[t] = struct{}{}
	}

	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			response.Error(c, errcode.NotLoginOrTokenExpired)
			c.Abort()
			return
		}

		claims, err := manager.ParseToken(token)
		if err != nil {
			response.Error(c, errcode.InvalidToken)
			c.Abort()
			return
		}

		if len(allowedTypes) > 0 {
			if _, ok := allowed[claims.Type]; !ok {
				response.Error(c, errcode.NoPermission)
				c.Abort()
				return
			}
		}

		appcontext.SetUserContext(c, &appcontext.UserContext{
			UserID:       claims.UserID,
			TokenType:    claims.Type,
			Role:         claims.Role,
			CommunityID:  claims.CommunityID,
			CommunityIDs: claims.CommunityIDs,
			Phone:        claims.Phone,
			TraceID:      c.GetString(string(appcontext.KeyTraceID)),
			RealIP:       c.GetString(string(appcontext.KeyRealIP)),
		})
		c.Set("admin_permissions", claims.Permissions)
		c.Set("admin_username", claims.Username)
		c.Next()
	}
}

// extractToken 从请求头中提取 Bearer token
// 参数：
//   - c: gin 上下文
// 返回：
//   - string: token 字符串，不存在时返回空
func extractToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return ""
	}
	return parts[1]
}
