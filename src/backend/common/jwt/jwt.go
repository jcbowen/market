package jwt

import (
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// TokenTypeUser  C 端用户 token 类型
	TokenTypeUser = "user"
	// TokenTypeAdmin 管理员 token 类型
	TokenTypeAdmin = "admin"
)

// Claims 自定义 JWT 声明
type Claims struct {
	UserID      int64    `json:"sub,string"`
	Type        string   `json:"type"`
	Phone       string   `json:"phone,omitempty"`
	Role        string   `json:"role,omitempty"`
	CommunityID int64    `json:"community_id,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	jwt.RegisteredClaims
}

// GetSubjectID 从声明中获取用户/管理员 ID
func (c *Claims) GetSubjectID() int64 {
	return c.UserID
}

// Config JWT 配置
type Config struct {
	Secret          string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

// Manager JWT 管理器
type Manager struct {
	config Config
}

// NewManager 创建 JWT 管理器
// 参数：
//   - cfg: JWT 配置，包含密钥与 token 有效期
// 返回：
//   - *Manager: JWT 管理器实例
func NewManager(cfg Config) *Manager {
	return &Manager{config: cfg}
}

// GenerateAccessToken 生成 access token
// 参数：
//   - userID: 用户/管理员 ID
//   - tokenType: token 类型，user 或 admin
//   - phone: 手机号（可选）
//   - role: 角色编码（管理员必填）
//   - communityID: 社区 ID（可选）
//   - permissions: 权限列表（管理员可选）
// 返回：
//   - token 字符串
//   - 过期时间
//   - 错误
func (m *Manager) GenerateAccessToken(userID int64, tokenType, phone, role string, communityID int64, permissions []string) (string, time.Time, error) {
	return m.generateToken(userID, tokenType, phone, role, communityID, permissions, m.config.AccessTokenTTL)
}

// GenerateRefreshToken 生成 refresh token
// 参数：
//   - userID: 用户/管理员 ID
//   - tokenType: token 类型
// 返回：
//   - token 字符串
//   - 过期时间
//   - 错误
func (m *Manager) GenerateRefreshToken(userID int64, tokenType string) (string, time.Time, error) {
	return m.generateToken(userID, tokenType, "", "", 0, nil, m.config.RefreshTokenTTL)
}

// generateToken 统一生成 token
func (m *Manager) generateToken(userID int64, tokenType, phone, role string, communityID int64, permissions []string, ttl time.Duration) (string, time.Time, error) {
	now := time.Now()
	expireAt := now.Add(ttl)

	claims := Claims{
		UserID:      userID,
		Type:        tokenType,
		Phone:       phone,
		Role:        role,
		CommunityID: communityID,
		Permissions: permissions,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   int64ToString(userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expireAt),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(m.config.Secret))
	return tokenString, expireAt, err
}

// ParseToken 解析并验证 token
// 参数：
//   - tokenString: token 字符串
// 返回：
//   - *Claims: 解析后的声明
//   - error: 解析错误
func (m *Manager) ParseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(m.config.Secret), nil
	})
	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token claims")
}

// int64ToString int64 转 string 的辅助函数
func int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}
