package middleware

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/response"
)

// tokenRevocationChecker 令牌吊销检查器，网关启动时注入（与 SetPermissionResolver 同一注入模式）
// 签名 jti 非空表示该令牌已被登出/停用写入黑名单
var tokenRevocationChecker func(ctx context.Context, jti string) bool

// SetTokenRevocationChecker 注入令牌吊销检查器（C5）
// access 令牌有效期 2 小时，登出若不查黑名单，服务端在剩余有效期内照旧认这枚令牌，
// 「登出」就只剩前端删本地存储这一半动作；refresh 令牌同理。检查器由 handler 侧的
// Redis 黑名单器提供，中间件只消费判定结果，不关心存储
//
// 参数：
//   - check: 判定函数，返回 true 表示该 jti 已吊销
//
// 使用示例：
//
//	gwMiddleware.SetTokenRevocationChecker(func(ctx context.Context, jti string) bool {
//		return tokenGuard.revoked(ctx, jti)
//	})
func SetTokenRevocationChecker(check func(ctx context.Context, jti string) bool) {
	tokenRevocationChecker = check
}

// AuthMiddleware 构造 JWT 鉴权中间件
// 参数：
//   - manager: JWT 管理器
//   - allowedTypes: 允许的 token 类型，如 user、admin
//
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

		// 强校验 token 用途：refresh token 不得用于接口访问（防长效令牌提权）
		if claims.TokenUse != jwt.TokenUseAccess {
			response.Error(c, errcode.InvalidToken)
			c.Abort()
			return
		}

		// 吊销检查（C5）：登出与停用把 jti 写入黑名单后，这枚 access 立即失效。
		// 代价是每次鉴权多一次 Redis EXISTS（亚毫秒，远小于同请求后续的 gRPC 权限解析）；
		// 黑名单器在 Redis 故障时 fail-open，与登录锁定/刷新轮转同一契约——缓存抖动不关闭认证入口。
		// 未注入检查器时跳过（保持单测与未启用黑名单的部署原样）
		if tokenRevocationChecker != nil && tokenRevocationChecker(c.Request.Context(), claims.ID) {
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
//
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
