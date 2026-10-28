package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/proto/user"
)

/**
 * C 端 refresh 令牌轮转与黑名单（P1-8）测试
 *
 * 背景：C 端刷新只做了「解析旧令牌→发新令牌」，旧令牌从不登记为已用，
 * 登出也只清了浏览器 cookie。等于被盗或已注销的 refresh 令牌在 7 天有效期内
 * 可以无限续期，后台侧早已修的三道（黑名单/实时账号校验/轮转）在会员侧同样缺失。
 *
 * 运行方式：
 *   集成用例（需本地 Redis，与后台 refresh 用例同一套 env）：
 *     TEST_REDIS_PASSWORD=xxx go test ./gateway/handler/ -run 'TestUserRefresh' -v
 *
 * 覆盖：
 *   - 刷新一次后旧令牌即被消费，重放被拒且不再查库；
 *   - 轮转后的新令牌可继续链式刷新（不误伤正常会话）；
 *   - 登出吊销当前令牌后，同一令牌再也换不出新 access。
 *
 * 使用示例：
 *   h, m := newAuthRefreshTestHandler(t, stub)
 *   code := doUserRefreshRequest(t, h, oldToken)
 */

// stubUserRefreshClient C 端刷新链路 user 服务桩：仅覆写 GetUserById
type stubUserRefreshClient struct {
	user.UserServiceClient
	info  *user.UserInfo
	calls int
}

// GetUserById 记录调用次数并按预设返回
func (s *stubUserRefreshClient) GetUserById(ctx context.Context, in *user.GetUserByIdReq, opts ...grpc.CallOption) (*user.UserInfo, error) {
	s.calls++
	return s.info, nil
}

// newAuthRefreshTestHandler 构造连到测试 Redis 的 C 端认证处理器
// TEST_REDIS_PASSWORD 未设置时由内层环境助手跳过（与后台 refresh 用例同一守卫口径）
//
// 参数：
//   - t: 测试对象
//   - stub: user 服务桩
//
// 返回值：
//   - *AuthHandler: 认证处理器（已注入测试 Redis 黑名单器）
//   - *jwt.Manager: 同一 JWT 管理器，供签发待刷新的令牌
//   - context.Context: 黑名单断言用上下文
//   - func(keys ...string): 测试键清理登记
func newAuthRefreshTestHandler(t *testing.T, stub *stubUserRefreshClient) (*AuthHandler, *jwt.Manager, context.Context, func(...string)) {
	t.Helper()
	guard, ctx, register := newTokenGuardTestEnv(t)
	m := jwt.NewManager(jwt.Config{
		Secret:          "p1-8-user-refresh-test-secret",
		AccessTokenTTL:  2 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	})
	return &AuthHandler{jwtManager: m, userClient: stub, tokenGuard: guard}, m, ctx, register
}

// decodeUserRefreshResp 投递一次 C 端刷新请求并解析响应码与新令牌
// 处理器用 ShouldBindJSON 取请求体令牌（与真实前端一致），故 body 模式发 JSON 而非表单
//
// 参数：
//   - t: 测试对象
//   - h: 认证处理器
//   - refreshToken: 待刷新的令牌
//   - body: true=令牌放请求体（小程序/App 模式），false=令牌放 httpOnly cookie（H5 模式）
//
// 返回值：
//   - int: 业务码
//   - string: 新 refresh 令牌（失败或不轮转时为空）
func decodeUserRefreshResp(t *testing.T, h *AuthHandler, refreshToken string, body bool) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	var reqBody io.Reader = strings.NewReader("")
	contentType := ""
	if body && refreshToken != "" {
		payload, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
		if err != nil {
			t.Fatalf("构造请求体失败: %v", err)
		}
		reqBody, contentType = bytes.NewReader(payload), "application/json"
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", reqBody)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if !body && refreshToken != "" {
		req.AddCookie(&http.Cookie{Name: refreshTokenCookieName, Value: refreshToken, Path: refreshCookiePath})
	}
	c.Request = req
	h.RefreshToken(c)

	var resp refreshResp
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应体失败: %v, body=%q", err, recorder.Body.String())
	}
	newToken, _ := resp.Data["refresh_token"].(string)
	return resp.Code, newToken
}

// TestUserRefreshRejectsReplay 刷新一次后旧令牌即失效，重放被拒且不再查库
func TestUserRefreshRejectsReplay(t *testing.T) {
	stub := &stubUserRefreshClient{info: &user.UserInfo{Id: 3001, Status: 1}}
	h, m, ctx, register := newAuthRefreshTestHandler(t, stub)

	oldToken, _, err := m.GenerateRefreshToken(3001, jwt.TokenTypeUser)
	if err != nil {
		t.Fatalf("生成 refresh 令牌失败: %v", err)
	}
	oldClaims, err := m.ParseToken(oldToken)
	if err != nil {
		t.Fatalf("解析旧令牌失败: %v", err)
	}
	register(tokenJtiKey(oldClaims.ID))

	code, newToken := decodeUserRefreshResp(t, h, oldToken, true)
	if code != errcode.Success {
		t.Fatalf("首次刷新应成功，code = %d", code)
	}
	if newToken == "" || newToken == oldToken {
		t.Fatal("轮转失败：未拿到新 refresh 令牌")
	}
	if !h.tokenGuard.revoked(ctx, oldClaims.ID) {
		t.Fatal("刷新后旧 jti 应已写入黑名单")
	}

	callsBefore := stub.calls
	replay, _ := decodeUserRefreshResp(t, h, oldToken, true)
	if replay != errcode.InvalidToken {
		t.Errorf("已消费的旧令牌应拒为 InvalidToken，实得 code = %d", replay)
	}
	if stub.calls != callsBefore {
		t.Error("黑名单命中时不应再查库")
	}

	// 新令牌可继续链式刷新
	newClaims, err := m.ParseToken(newToken)
	if err != nil {
		t.Fatalf("解析新令牌失败: %v", err)
	}
	register(tokenJtiKey(newClaims.ID))
	next, _ := decodeUserRefreshResp(t, h, newToken, true)
	if next != errcode.Success {
		t.Errorf("轮转后的新令牌应可继续刷新，实得 code = %d", next)
	}
}

// TestUserLogoutRevokesRefreshToken 登出吊销当前令牌：同一枚 refresh 再也换不出新 access
// 只清 cookie 不吊销的话，被盗令牌在剩余有效期内依旧可用（登出等于没登出）
func TestUserLogoutRevokesRefreshToken(t *testing.T) {
	stub := &stubUserRefreshClient{info: &user.UserInfo{Id: 3002, Status: 1}}
	h, m, ctx, register := newAuthRefreshTestHandler(t, stub)

	token, _, err := m.GenerateRefreshToken(3002, jwt.TokenTypeUser)
	if err != nil {
		t.Fatalf("生成 refresh 令牌失败: %v", err)
	}
	claims, err := m.ParseToken(token)
	if err != nil {
		t.Fatalf("解析令牌失败: %v", err)
	}
	register(tokenJtiKey(claims.ID))

	// 以 cookie 承载发起登出
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: refreshTokenCookieName, Value: token, Path: refreshCookiePath})
	c.Request = req
	h.Logout(c)
	if recorder.Result().StatusCode != http.StatusOK {
		t.Fatalf("登出应返回 200，实得 %d", recorder.Result().StatusCode)
	}
	if !h.tokenGuard.revoked(ctx, claims.ID) {
		t.Fatal("登出后令牌应进黑名单")
	}

	code, _ := decodeUserRefreshResp(t, h, token, true)
	if code != errcode.InvalidToken {
		t.Errorf("登出后的令牌应拒为 InvalidToken，实得 code = %d", code)
	}
}
