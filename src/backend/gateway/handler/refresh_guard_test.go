package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/jcbaseGo"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/proto/user"
)

/**
 * refresh 令牌黑名单与刷新加固（P1-5）测试
 *
 * 背景：refresh 令牌原为纯无状态 JWT，7 天有效期内停用管理员/已登出会话仍可续期。
 * 修法三道：黑名单（轮转后旧 jti 即失效）+ 账号实时校验（查库，停用/删除即拒）+ 轮转。
 *
 * 运行方式：
 *   离线用例（无外部依赖）：
 *     go test ./gateway/handler/ -run 'TestRefreshJtiKeyNaming|TestRefreshGuardFailOpen|TestRefreshTokenRejects|TestRefreshTokenRotatesIn' -v
 *   集成用例（需本地 Redis，与登录锁定测试同一套 env）：
 *     TEST_REDIS_PASSWORD=xxx go test ./gateway/handler/ -run 'TestRefreshGuardRevokeThenRevoked|TestRefreshTokenBlacklistReplay' -v
 *
 * 覆盖：
 *   - 键命名带独立前缀，与登录锁定键（loginfail）不撞；
 *   - 吊销后命中、空 jti 不参与、ttl<=0 不落键；
 *   - Redis 不可用放行（fail-open），缓存故障不关闭刷新入口；
 *   - 停用/删除/查库故障分别拒为 AccountDisabled/InvalidToken/Internal；
 *   - access 令牌、空令牌、乱码令牌不得用于刷新；
 *   - 响应体与 cookie 两种模式都能拿到轮转后的新 refresh 令牌；
 *   - 集成：刷新一次后旧令牌即进黑名单，重放被拒且不再查库，新令牌可链式轮转。
 */

// TestRefreshJtiKeyNaming 验证黑名单键命名（离线）：独立前缀，与登录锁定命名空间区分
func TestRefreshJtiKeyNaming(t *testing.T) {
	if got := refreshJtiKey("abc123"); got != "refreshjti:abc123" {
		t.Errorf("黑名单键命名不符: %q", refreshJtiKey("abc123"))
	}
	if refreshJtiKey("jti-a") == refreshJtiKey("jti-b") {
		t.Error("不同 jti 必须映射到不同键")
	}
	if refreshJtiKeyPrefix == "loginfail" {
		t.Error("黑名单键不应与登录锁定键共用命名空间")
	}
}

// TestRefreshGuardRevokeThenRevoked 集成（需 Redis）：吊销即命中、空 jti 不参与、ttl<=0 不落键
func TestRefreshGuardRevokeThenRevoked(t *testing.T) {
	guard, ctx, register := newRefreshGuardTestEnv(t)
	jti := "REFRESHTEST-" + time.Now().Format("150405.000000")
	register(refreshJtiKey(jti))

	if guard.revoked(ctx, jti) {
		t.Fatal("吊销前不应命中黑名单")
	}
	guard.revoke(ctx, jti, time.Minute)
	if !guard.revoked(ctx, jti) {
		t.Error("吊销后应命中黑名单")
	}

	// 空 jti：历史签发（无 jti）令牌不参与黑名单，吊销为 no-op 不 panic
	if guard.revoked(ctx, "") {
		t.Error("空 jti 应按未吊销处理")
	}
	guard.revoke(ctx, "", time.Minute)

	// ttl<=0（令牌已过期）：跳过写键，避免为过期令牌常驻黑名单
	expired := jti + "-expired"
	register(refreshJtiKey(expired))
	guard.revoke(ctx, expired, 0)
	guard.revoke(ctx, expired, -time.Second)
	if guard.revoked(ctx, expired) {
		t.Error("ttl<=0 的吊销应跳过，不得写键")
	}
}

// TestRefreshGuardFailOpenWithoutRedis 验证 Redis 不可用时放行（fail-open）
// 吊销是加固项：缓存故障时退化为轮转前行为（旧令牌存活到期），不得阻断正常刷新
func TestRefreshGuardFailOpenWithoutRedis(t *testing.T) {
	guard := newRefreshGuard(&config.Config{Redis: jcbaseGo.RedisStruct{
		Host: "127.0.0.1",
		Port: "1",
		Db:   "15",
	}})
	ctx := context.Background()

	guard.revoke(ctx, "any-jti", time.Minute)
	if guard.revoked(ctx, "any-jti") {
		t.Error("Redis 不可用时应放行（等价轮转前行为）")
	}
}

// stubRefreshUserClient refresh 链路 user 服务桩：仅覆写 GetAdminById
// 嵌入接口后其余方法无需实现（本组用例不会触达）
type stubRefreshUserClient struct {
	user.UserServiceClient
	admin  *user.AdminInfo
	err    error
	calls  int
	lastID int64
}

// GetAdminById 记录入参并按预设返回
func (s *stubRefreshUserClient) GetAdminById(ctx context.Context, in *user.GetAdminByIdReq, opts ...grpc.CallOption) (*user.AdminInfo, error) {
	s.calls++
	s.lastID = in.GetId()
	if s.err != nil {
		return nil, s.err
	}
	return s.admin, nil
}

// newRefreshTestHandler 构造刷新处理器（离线）
// Redis 指向无效端口（fail-open），账号状态由桩控制——隔离验证
// 「黑名单校验 + 账号实时校验 + 轮转」逻辑本身
//
// 参数：
//   - stub: user 服务桩
//
// 返回值：
//   - *AdminHandler: 处理器（product/order/payment 客户端为 nil，刷新链路不触达）
//   - *jwt.Manager: 同一 JWT 管理器，供测试签发待刷新的令牌
func newRefreshTestHandler(stub *stubRefreshUserClient) (*AdminHandler, *jwt.Manager) {
	m := jwt.NewManager(jwt.Config{
		Secret:          "p1-5-refresh-test-secret",
		AccessTokenTTL:  2 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	})
	h := &AdminHandler{
		jwtManager: m,
		userClient: stub,
		refreshGuard: newRefreshGuard(&config.Config{Redis: jcbaseGo.RedisStruct{
			Host: "127.0.0.1",
			Port: "1",
			Db:   "15",
		}}),
	}
	return h, m
}

// doRefreshRequest 以表单形式投递一次刷新请求
//
// 参数：
//   - t: 测试对象
//   - h: 处理器
//   - refreshToken: 待刷新令牌，空串表示未携带
//   - optInCookie: 是否带 X-Refresh-Cookie: 1（模拟新版前端的 cookie 承载模式）
//
// 返回值：
//   - *httptest.ResponseRecorder: 响应记录器
func doRefreshRequest(t *testing.T, h *AdminHandler, refreshToken string, optInCookie bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	form := url.Values{}
	if refreshToken != "" {
		form.Set("refresh_token", refreshToken)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/refresh", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if optInCookie {
		req.Header.Set(refreshCookieOptInHeader, "1")
	}
	c.Request = req
	h.RefreshToken(c)
	return recorder
}

// refreshResp 刷新端点响应体（成功时 data 携带新令牌）
type refreshResp struct {
	Code    int                   `json:"code"`
	Message string                `json:"message"`
	Data    map[string]interface{} `json:"data"`
}

// decodeRefreshResp 解析刷新响应体
//
// 参数：
//   - t: 测试对象
//   - recorder: 响应记录器
//
// 返回值：
//   - refreshResp: 解析结果
func decodeRefreshResp(t *testing.T, recorder *httptest.ResponseRecorder) refreshResp {
	t.Helper()
	var resp refreshResp
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应体失败: %v, body=%q", err, recorder.Body.String())
	}
	return resp
}

// TestRefreshTokenRejectsDisabledAccount 停用管理员（status≠1）立即失去续期能力
func TestRefreshTokenRejectsDisabledAccount(t *testing.T) {
	stub := &stubRefreshUserClient{admin: &user.AdminInfo{Id: 1001, Status: 0}}
	h, m := newRefreshTestHandler(stub)

	token, _, err := m.GenerateRefreshToken(1001, jwt.TokenTypeAdmin)
	if err != nil {
		t.Fatalf("生成 refresh 令牌失败: %v", err)
	}
	resp := decodeRefreshResp(t, doRefreshRequest(t, h, token, false))
	if resp.Code != errcode.AccountDisabled {
		t.Fatalf("停用账号应拒刷新，code = %d(%s), 期望 %d", resp.Code, resp.Message, errcode.AccountDisabled)
	}
	if stub.calls != 1 || stub.lastID != 1001 {
		t.Errorf("应按 claims.UserID 查库一次: calls=%d lastID=%d", stub.calls, stub.lastID)
	}
	if resp.Data != nil {
		t.Error("拒绝路径不应下发任何令牌字段")
	}
}

// TestRefreshTokenRejectsDeletedAccount 账号已删除（NotFound）按令牌无效处理，不透露账号是否存在
func TestRefreshTokenRejectsDeletedAccount(t *testing.T) {
	stub := &stubRefreshUserClient{err: status.Error(codes.NotFound, "管理员不存在")}
	h, m := newRefreshTestHandler(stub)

	token, _, err := m.GenerateRefreshToken(1002, jwt.TokenTypeAdmin)
	if err != nil {
		t.Fatalf("生成 refresh 令牌失败: %v", err)
	}
	resp := decodeRefreshResp(t, doRefreshRequest(t, h, token, false))
	if resp.Code != errcode.InvalidToken {
		t.Fatalf("账号已删除应拒为 InvalidToken，code = %d(%s), 期望 %d", resp.Code, resp.Message, errcode.InvalidToken)
	}
	if resp.Data != nil {
		t.Error("拒绝路径不应下发任何令牌字段")
	}
}

// TestRefreshTokenRejectsInternalError 查库非 NotFound 故障按内部错误拒绝，不静默放行
func TestRefreshTokenRejectsInternalError(t *testing.T) {
	stub := &stubRefreshUserClient{err: status.Error(codes.Unavailable, "user-service 不可用")}
	h, m := newRefreshTestHandler(stub)

	token, _, err := m.GenerateRefreshToken(1003, jwt.TokenTypeAdmin)
	if err != nil {
		t.Fatalf("生成 refresh 令牌失败: %v", err)
	}
	resp := decodeRefreshResp(t, doRefreshRequest(t, h, token, false))
	if resp.Code != errcode.InternalServerError {
		t.Fatalf("查库故障应拒为 Internal，code = %d(%s), 期望 %d", resp.Code, resp.Message, errcode.InternalServerError)
	}
}

// TestRefreshTokenRejectsNonRefreshToken 用途强校验：access 令牌/空令牌/乱码令牌不得刷新
func TestRefreshTokenRejectsNonRefreshToken(t *testing.T) {
	stub := &stubRefreshUserClient{admin: &user.AdminInfo{Id: 1001, Status: 1}}
	h, m := newRefreshTestHandler(stub)

	// access 令牌冒充刷新：用途不符，且不应触达查库
	access, _, err := m.GenerateAdminAccessToken(1001, "13800138000", "community_admin", 1, nil, nil, "admin", "测试")
	if err != nil {
		t.Fatalf("生成 access 令牌失败: %v", err)
	}
	resp := decodeRefreshResp(t, doRefreshRequest(t, h, access, false))
	if resp.Code != errcode.InvalidToken {
		t.Errorf("access 令牌不得用于刷新，code = %d(%s), 期望 %d", resp.Code, resp.Message, errcode.InvalidToken)
	}
	if stub.calls != 0 {
		t.Error("用途不符时不应查库")
	}

	// 空令牌：按未登录处理
	resp2 := decodeRefreshResp(t, doRefreshRequest(t, h, "", false))
	if resp2.Code != errcode.NotLoginOrTokenExpired {
		t.Errorf("空令牌应拒为 NotLoginOrTokenExpired，code = %d", resp2.Code)
	}

	// 乱码令牌：解析失败按无效令牌处理
	resp3 := decodeRefreshResp(t, doRefreshRequest(t, h, "not-a-jwt", false))
	if resp3.Code != errcode.InvalidToken {
		t.Errorf("乱码令牌应拒为 InvalidToken，code = %d", resp3.Code)
	}
}

// TestRefreshTokenRotatesInBody 响应体模式：刷新成功回传新 refresh 令牌且新旧不同
// 旧前端/POS 从响应体取值，轮转后必须回传新值，否则客户端继续持有即将被吊销的旧令牌
func TestRefreshTokenRotatesInBody(t *testing.T) {
	stub := &stubRefreshUserClient{admin: &user.AdminInfo{Id: 1001, Status: 1}}
	h, m := newRefreshTestHandler(stub)

	oldToken, _, err := m.GenerateRefreshToken(1001, jwt.TokenTypeAdmin)
	if err != nil {
		t.Fatalf("生成 refresh 令牌失败: %v", err)
	}
	resp := decodeRefreshResp(t, doRefreshRequest(t, h, oldToken, false))
	if resp.Code != errcode.Success {
		t.Fatalf("正常账号刷新应成功，code = %d(%s)", resp.Code, resp.Message)
	}

	newToken, _ := resp.Data["refresh_token"].(string)
	if newToken == "" {
		t.Fatal("响应体模式必须回传新 refresh 令牌")
	}
	if newToken == oldToken {
		t.Error("轮转后必须签发新 refresh 令牌")
	}
	if resp.Data["access_token"] == "" {
		t.Error("刷新应同时签发新 access_token")
	}
}

// TestRefreshTokenRotatesInCookie cookie 模式：新令牌随 httpOnly cookie 下发，响应体不透出
func TestRefreshTokenRotatesInCookie(t *testing.T) {
	stub := &stubRefreshUserClient{admin: &user.AdminInfo{Id: 1001, Status: 1}}
	h, m := newRefreshTestHandler(stub)

	oldToken, _, err := m.GenerateRefreshToken(1001, jwt.TokenTypeAdmin)
	if err != nil {
		t.Fatalf("生成 refresh 令牌失败: %v", err)
	}
	recorder := doRefreshRequest(t, h, oldToken, true)
	resp := decodeRefreshResp(t, recorder)
	if resp.Code != errcode.Success {
		t.Fatalf("cookie 模式刷新应成功，code = %d(%s)", resp.Code, resp.Message)
	}
	if resp.Data["refresh_token"] != nil {
		t.Error("cookie 模式不应在响应体回传 refresh 令牌")
	}

	var newToken string
	for _, ck := range recorder.Result().Cookies() {
		if ck.Name == refreshTokenCookieName {
			newToken = ck.Value
			if !ck.HttpOnly {
				t.Error("刷新下发的 cookie 必须 HttpOnly")
			}
		}
	}
	if newToken == "" {
		t.Fatal("cookie 模式未下发 refresh 令牌 cookie")
	}
	if newToken == oldToken {
		t.Error("cookie 模式同样必须轮转（新旧令牌相同则吊销形同虚设）")
	}
}

// TestRefreshTokenBlacklistReplay 集成（需 Redis）：刷新一次后旧令牌即进黑名单
// 重放被拒且不再查库；轮转后的新令牌可继续链式刷新（不误伤）
func TestRefreshTokenBlacklistReplay(t *testing.T) {
	guard, ctx, register := newRefreshGuardTestEnv(t)
	m := jwt.NewManager(jwt.Config{
		Secret:          "p1-5-refresh-test-secret",
		AccessTokenTTL:  2 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	})
	stub := &stubRefreshUserClient{admin: &user.AdminInfo{Id: 2001, Status: 1}}
	h := &AdminHandler{jwtManager: m, userClient: stub, refreshGuard: guard}

	oldToken, _, err := m.GenerateRefreshToken(2001, jwt.TokenTypeAdmin)
	if err != nil {
		t.Fatalf("生成 refresh 令牌失败: %v", err)
	}
	oldClaims, err := m.ParseToken(oldToken)
	if err != nil {
		t.Fatalf("解析旧令牌失败: %v", err)
	}
	register(refreshJtiKey(oldClaims.ID))

	// 首次刷新成功并轮转
	resp := decodeRefreshResp(t, doRefreshRequest(t, h, oldToken, false))
	if resp.Code != errcode.Success {
		t.Fatalf("首次刷新应成功，code = %d(%s)", resp.Code, resp.Message)
	}
	newToken, _ := resp.Data["refresh_token"].(string)
	if newToken == "" || newToken == oldToken {
		t.Fatal("轮转失败：未拿到新 refresh 令牌")
	}

	// 旧令牌的 jti 已写入黑名单
	if !guard.revoked(ctx, oldClaims.ID) {
		t.Fatal("刷新后旧 jti 应写入黑名单")
	}

	// 重放旧令牌：黑名单前置拦截，不再查库
	callsBefore := stub.calls
	replay := decodeRefreshResp(t, doRefreshRequest(t, h, oldToken, false))
	if replay.Code != errcode.InvalidToken {
		t.Errorf("已轮转的旧令牌应拒为 InvalidToken，code = %d(%s)", replay.Code, replay.Message)
	}
	if stub.calls != callsBefore {
		t.Error("黑名单命中时不应再查库")
	}

	// 新令牌可继续刷新（链式轮转不误伤）
	newClaims, err := m.ParseToken(newToken)
	if err != nil {
		t.Fatalf("解析新令牌失败: %v", err)
	}
	register(refreshJtiKey(newClaims.ID))
	next := decodeRefreshResp(t, doRefreshRequest(t, h, newToken, false))
	if next.Code != errcode.Success {
		t.Fatalf("轮转后的新令牌应可继续刷新，code = %d(%s)", next.Code, next.Message)
	}
}

// newRefreshGuardTestEnv 构造连到测试 Redis 的黑名单器，并返回键清理登记函数
// TEST_REDIS_PASSWORD 未设置时跳过（与 MySQL 集成测试、登录锁定测试同一守卫口径）
// 集成用例在签发令牌后才拿得到 jti，故清理键统一在收尾时删除
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *refreshGuard: 黑名单器
//   - context.Context: 请求上下文
//   - func(keys ...string): 键清理登记函数
func newRefreshGuardTestEnv(t *testing.T) (*refreshGuard, context.Context, func(keys ...string)) {
	t.Helper()
	if os.Getenv("TEST_REDIS_PASSWORD") == "" {
		t.Skip("TEST_REDIS_PASSWORD 未设置，跳过 refresh 黑名单集成测试")
	}
	cfg := &config.Config{Redis: jcbaseGo.RedisStruct{
		Host:     loginTestEnvOr("TEST_REDIS_HOST", "127.0.0.1"),
		Port:     loginTestEnvOr("TEST_REDIS_PORT", "6379"),
		Password: os.Getenv("TEST_REDIS_PASSWORD"),
		Db:       loginTestEnvOr("TEST_REDIS_DB", "15"),
	}}
	guard := newRefreshGuard(cfg)
	ctx := context.Background()
	// 客户端构造本身不建连，测试须显式 Ping 探测可用性，
	// 否则连不上只会在断言里表现为「永远未吊销」的假通过
	rds := guard.client()
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := rds.Ping(pingCtx).Err(); err != nil {
		t.Fatalf("测试 Redis 不可用，无法验证 refresh 黑名单: %v", err)
	}

	var registered []string
	t.Cleanup(func() {
		if len(registered) == 0 {
			return
		}
		if err := rds.Del(context.Background(), registered...).Err(); err != nil {
			t.Logf("清理 refresh 黑名单键失败（需手工核对 %s: 前缀键）: %v", refreshJtiKeyPrefix, err)
		}
	})
	return guard, ctx, func(keys ...string) {
		registered = append(registered, keys...)
	}
}
