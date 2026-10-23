package jwt

import (
	"strings"
	"testing"
	"time"
)

/**
 * JWT jti（P1-5）离线单测
 *
 * 背景：refresh 令牌原先只靠过期时间（默认 7 天）自然失效，停用管理员与登出
 * 在令牌有效期内永远在线。修法是「轮转 + 黑名单」：每次刷新签发新 refresh 令牌，
 * 旧令牌凭 RegisteredClaims.ID（jti）写入 Redis 黑名单。本文件锁定 jti 的签发口径：
 * refresh 必有且唯一、access 不携带。
 *
 * 运行方式（无外部依赖）：
 *   go test ./common/jwt/ -v
 */

// newTestManager 测试用 JWT 管理器（密钥与 TTL 均为测试值）
//
// 返回值：
//   - *Manager: 管理器实例
func newTestManager() *Manager {
	return NewManager(Config{
		Secret:          "p1-5-jti-test-secret",
		AccessTokenTTL:  2 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	})
}

// TestRefreshTokenCarriesUniqueJTI 验证 refresh 令牌携带唯一 jti
// 黑名单以 jti 为键：缺失则轮转后旧令牌仍可重放，重复则新旧令牌被同一键误杀/漏杀
func TestRefreshTokenCarriesUniqueJTI(t *testing.T) {
	m := newTestManager()

	token, expireAt, err := m.GenerateRefreshToken(1001, TokenTypeAdmin)
	if err != nil {
		t.Fatalf("生成 refresh 令牌失败: %v", err)
	}
	if expireAt.IsZero() {
		t.Fatal("过期时间不应为零值")
	}

	claims, err := m.ParseToken(token)
	if err != nil {
		t.Fatalf("解析 refresh 令牌失败: %v", err)
	}
	if claims.TokenUse != TokenUseRefresh {
		t.Fatalf("token_use = %q, 期望 %q", claims.TokenUse, TokenUseRefresh)
	}
	if claims.ID == "" {
		t.Fatal("refresh 令牌必须携带 jti，否则黑名单无法定位该令牌")
	}
	if len(claims.ID) != 32 {
		t.Errorf("jti 应为 16 字节随机数的 hex 编码（32 字符），实得 %d 字符: %q", len(claims.ID), claims.ID)
	}
	if claims.GetSubjectID() != 1001 {
		t.Errorf("sub = %d, 期望 1001", claims.GetSubjectID())
	}

	// 同一账号连续签发：jti 必须不同，否则轮转后无法区分新旧令牌
	token2, _, err := m.GenerateRefreshToken(1001, TokenTypeAdmin)
	if err != nil {
		t.Fatalf("第二次生成 refresh 令牌失败: %v", err)
	}
	claims2, err := m.ParseToken(token2)
	if err != nil {
		t.Fatalf("解析第二个 refresh 令牌失败: %v", err)
	}
	if claims2.ID == claims.ID {
		t.Error("两次签发的 refresh 令牌 jti 相同，轮转无法区分新旧令牌")
	}
	if token2 == token {
		t.Error("两次签发的令牌串不应相同")
	}
}

// TestAccessTokenHasNoJTI 验证 access 令牌不携带 jti
// access 分钟级短效、自然过期，不参与黑名单；携带 jti 只会诱导误用
func TestAccessTokenHasNoJTI(t *testing.T) {
	m := newTestManager()

	token, _, err := m.GenerateAccessToken(1001, TokenTypeUser, "13800138000", "", 0, nil)
	if err != nil {
		t.Fatalf("生成 access 令牌失败: %v", err)
	}
	claims, err := m.ParseToken(token)
	if err != nil {
		t.Fatalf("解析 access 令牌失败: %v", err)
	}
	if claims.TokenUse != TokenUseAccess {
		t.Errorf("token_use = %q, 期望 %q", claims.TokenUse, TokenUseAccess)
	}
	if claims.ID != "" {
		t.Errorf("access 令牌不应携带 jti，实得 %q", claims.ID)
	}

	adminToken, _, err := m.GenerateAdminAccessToken(1001, "13800138000", "community_admin", 99, []int64{99}, []string{"*"}, "admin", "测试")
	if err != nil {
		t.Fatalf("生成管理员 access 令牌失败: %v", err)
	}
	adminClaims, err := m.ParseToken(adminToken)
	if err != nil {
		t.Fatalf("解析管理员 access 令牌失败: %v", err)
	}
	if adminClaims.ID != "" {
		t.Errorf("管理员 access 令牌不应携带 jti，实得 %q", adminClaims.ID)
	}
}

// TestNewJTIFormat 验证 jti 生成器输出恒为 32 位小写 hex 且不重复
func TestNewJTIFormat(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		jti := newJTI()
		if len(jti) != 32 {
			t.Fatalf("第 %d 次生成 jti 长度 = %d, 期望 32", i+1, len(jti))
		}
		for _, ch := range jti {
			if !strings.ContainsRune("0123456789abcdef", ch) {
				t.Fatalf("jti 含非小写 hex 字符 %q: %q", ch, jti)
			}
		}
		if seen[jti] {
			t.Fatalf("jti 重复: %q", jti)
		}
		seen[jti] = true
	}
}
