package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jcbowen/jcbaseGo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/user"
)

/**
 * 管理员凭据出口回归（P2 C1）
 *
 * 背景：登录曾由网关取回 AdminInfo.password 自行做 bcrypt 比对，于是员工列表把
 * bcrypt 摘要一起下发（response 直接透传 proto AdminInfo）。摘要一旦外泄即可离线爆破，
 * 与撞库合并就是后台账号批量沦陷。
 *
 * 修法：口令比对下沉到 user 服务（VerifyAdminCredential，只回布尔结论），
 * ToAdminInfo 不再赋值 password，网关列表出口改走 toAdminVO 白名单。
 *
 * 运行方式（全部离线，无需 DB/Redis；Redis 指向无效端口触发锁定 fail-open）：
 *   go test ./gateway/handler/ -run 'TestAdminCredential' -v
 *
 * 覆盖：
 *   - 登录走 VerifyAdminCredential，响应体任何位置都不出现 bcrypt 摘要；
 *   - 旧的账号全量查询出口在登录链路上被调用即失败（防止回退到泄漏路径）；
 *   - 口令错误与账号不存在回同一错误码（消除账号枚举），且都不下发令牌；
 *   - 员工列表逐行走白名单：即便服务层误填 password 也不外泄，同时前端依赖的
 *     roles / user_phone / user_nickname 不得被白名单漏掉。
 */

// stubCredClient 凭据链路桩：只覆写登录与员工列表实际触达的方法
// GetAdminByUsername 恒回 Unimplemented——登录改道后仍调用它即视为回归
type stubCredClient struct {
	user.UserServiceClient
	admin         *user.AdminInfo
	passwordValid bool
	verifyCalls   int
	listItems     []*user.AdminInfo
}

// VerifyAdminCredential 口令校验桩：ghost 账号按「不存在」回 NotFound，与真实服务口径一致
func (s *stubCredClient) VerifyAdminCredential(ctx context.Context, in *user.VerifyAdminCredentialReq, opts ...grpc.CallOption) (*user.VerifyAdminCredentialResp, error) {
	s.verifyCalls++
	if in.Username == "ghost" {
		return nil, status.Error(codes.NotFound, "管理员不存在")
	}
	return &user.VerifyAdminCredentialResp{PasswordValid: s.passwordValid, Admin: s.admin}, nil
}

// GetAdminByUsername 登录链路不得再触达此出口（它曾把口令摘要带进网关）
func (s *stubCredClient) GetAdminByUsername(ctx context.Context, in *user.GetAdminByUsernameReq, opts ...grpc.CallOption) (*user.AdminInfo, error) {
	return nil, status.Error(codes.Unimplemented, "登录不应再按用户名拉取全量账号资料")
}

// GetAdminContext 权限实时解析桩：固定返回一条权限，够登录响应组装使用
func (s *stubCredClient) GetAdminContext(ctx context.Context, in *user.GetAdminContextReq, opts ...grpc.CallOption) (*user.AdminContextInfo, error) {
	return &user.AdminContextInfo{Permissions: []string{"order:list"}, Bound: true}, nil
}

// ListAdmins 员工列表桩：按预设行返回，分页固定为首页
func (s *stubCredClient) ListAdmins(ctx context.Context, in *user.ListAdminsReq, opts ...grpc.CallOption) (*user.ListAdminsResp, error) {
	return &user.ListAdminsResp{
		List: s.listItems,
		Pagination: &common.PaginationResponse{
			Total:    int32(len(s.listItems)),
			Page:     1,
			PageSize: 10,
		},
	}, nil
}

// newCredTestHandler 构造凭据链路处理器（离线）
// 参数：
//   - stub: user 服务桩
//
// 返回值：
//   - *AdminHandler: 处理器（product/order/payment 客户端为 nil，凭据链路不触达）
func newCredTestHandler(stub *stubCredClient) *AdminHandler {
	return &AdminHandler{
		jwtManager: jwt.NewManager(jwt.Config{
			Secret:          "c1-credential-test-secret",
			AccessTokenTTL:  2 * time.Hour,
			RefreshTokenTTL: 7 * 24 * time.Hour,
		}),
		userClient: stub,
		cfg:        &config.Config{},
		// Redis 指向无效端口：锁定计数 fail-open，用例只验凭据出口不验锁定行为
		guard: newLoginGuard(&config.Config{Redis: jcbaseGo.RedisStruct{Host: "127.0.0.1", Port: "1", Db: "15"}}),
	}
}

// doAdminLoginRequest 投递一次后台登录请求
// 参数：
//   - t: 测试对象
//   - h: 处理器
//   - username: 用户名
//   - password: 口令
//
// 返回值：
//   - *httptest.ResponseRecorder: 响应记录器
func doAdminLoginRequest(t *testing.T, h *AdminHandler, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body, err := json.Marshal(AdminLoginReq{Username: username, Password: password})
	if err != nil {
		t.Fatalf("构造登录请求失败: %v", err)
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Login(c)
	return recorder
}

// decodeRespBody 解析网关响应为通用结构
// 参数：
//   - t: 测试对象
//   - recorder: 响应记录器
//
// 返回值：
//   - map[string]interface{}: data 字段（业务负载）
//   - int: 业务错误码
func decodeRespBody(t *testing.T, recorder *httptest.ResponseRecorder) (map[string]interface{}, int) {
	t.Helper()
	var resp struct {
		Code    int                    `json:"code"`
		Message string                 `json:"message"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v, body=%s", err, recorder.Body.String())
	}
	return resp.Data, resp.Code
}

// TestAdminCredentialLoginKeepsHashInUserService 验证登录成功后口令摘要不出现在响应任何位置
// 桩故意在 AdminInfo.password 里塞入摘要：白名单与「不下发摘要」两道防线都必须拦住它
//
// 参数：
//   - t: 测试对象
func TestAdminCredentialLoginKeepsHashInUserService(t *testing.T) {
	stub := &stubCredClient{
		passwordValid: true,
		admin: &user.AdminInfo{
			Id:           7,
			Username:     "boss",
			Password:     "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy",
			RealName:     "店长",
			Phone:        "13800138000",
			Status:       1,
			CommunityIds: []int64{3},
		},
	}
	h := newCredTestHandler(stub)

	recorder := doAdminLoginRequest(t, h, "boss", "correct-password")
	if stub.verifyCalls != 1 {
		t.Fatalf("登录应走 VerifyAdminCredential, 调用次数 = %d", stub.verifyCalls)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	data, code := decodeRespBody(t, recorder)
	if code != 0 {
		t.Fatalf("登录应成功, code=%d body=%s", code, recorder.Body.String())
	}
	if tok, _ := data["access_token"].(string); tok == "" {
		t.Fatal("登录成功应下发 access_token")
	}
	if strings.Contains(recorder.Body.String(), "$2a$") {
		t.Errorf("响应体不得出现 bcrypt 摘要: %s", recorder.Body.String())
	}
	admin, _ := data["admin"].(map[string]interface{})
	if admin == nil {
		t.Fatalf("登录响应应携带 admin, body=%s", recorder.Body.String())
	}
	if _, ok := admin["password"]; ok {
		t.Error("admin 视图对象不得包含 password 字段")
	}
	if admin["id"] != float64(7) || admin["community_id"] != float64(3) {
		t.Errorf("admin 关键字段异常: %+v", admin)
	}
	if perms, ok := admin["permissions"].([]interface{}); !ok || len(perms) != 1 {
		t.Errorf("admin.permissions 应为解析后的权限数组: %+v", admin["permissions"])
	}
}

// TestAdminCredentialLoginFailuresShareErrorCode 验证口令错误与账号不存在回同一错误码且都不下发令牌
// 区分两者会让攻击者用登录接口枚举在职管理员账号，配合锁定计数还能验证「猜中了账号、口令错了」
//
// 参数：
//   - t: 测试对象
func TestAdminCredentialLoginFailuresShareErrorCode(t *testing.T) {
	// 口令错误：账号存在
	stub := &stubCredClient{
		passwordValid: false,
		admin:         &user.AdminInfo{Id: 7, Username: "boss", Password: "$2a$10$fakehash", Status: 1, CommunityIds: []int64{3}},
	}
	h := newCredTestHandler(stub)
	_, wrongCode := decodeRespBody(t, doAdminLoginRequest(t, h, "boss", "wrong-password"))

	// 账号不存在：NotFound
	stub2 := &stubCredClient{}
	h2 := newCredTestHandler(stub2)
	_, missingCode := decodeRespBody(t, doAdminLoginRequest(t, h2, "ghost", "whatever"))

	if wrongCode != errcode.IncorrectUsernameOrPassword {
		t.Errorf("口令错误码 = %d, want %d", wrongCode, errcode.IncorrectUsernameOrPassword)
	}
	if missingCode != errcode.IncorrectUsernameOrPassword {
		t.Errorf("账号不存在码 = %d, want %d（不得泄露账号是否存在）", missingCode, errcode.IncorrectUsernameOrPassword)
	}
	if stub.verifyCalls != 1 || stub2.verifyCalls != 1 {
		t.Errorf("两条路径都应走一次口令校验: %d / %d", stub.verifyCalls, stub2.verifyCalls)
	}
}

// TestAdminCredentialListRedactsSensitiveFields 验证员工列表逐行走白名单：摘要不外泄、前端所需字段不丢
// 参数：
//   - t: 测试对象
func TestAdminCredentialListRedactsSensitiveFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &stubCredClient{listItems: []*user.AdminInfo{
		{
			Id:           7,
			Username:     "boss",
			Password:     "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy",
			RealName:     "店长",
			Phone:        "13800138000",
			Status:       1,
			CommunityIds: []int64{3},
			UserId:       21,
			UserNickname: "张三",
			UserPhone:    "13900139000",
			Roles:        []*user.RoleInfo{{Id: 5, Name: "收银员", Code: "cashier", CommunityId: 3, Permissions: []string{"order:list"}}},
		},
	}}
	h := newCredTestHandler(stub)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/admins", nil)
	h.ListAdmins(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "$2a$") {
		t.Errorf("列表响应体不得出现 bcrypt 摘要: %s", recorder.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			List  []map[string]interface{} `json:"list"`
			Total int                      `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v, body=%s", err, recorder.Body.String())
	}
	if len(resp.Data.List) != 1 {
		t.Fatalf("列表行数 = %d, want 1, body=%s", len(resp.Data.List), recorder.Body.String())
	}
	row := resp.Data.List[0]
	if _, ok := row["password"]; ok {
		t.Error("列表行不得包含 password 字段")
	}
	// 前端「本社区角色」列与编辑回显 role_ids 依赖这些字段，白名单不得漏
	for _, key := range []string{"id", "username", "real_name", "phone", "status", "community_ids", "user_id", "user_nickname", "user_phone", "roles"} {
		if _, ok := row[key]; !ok {
			t.Errorf("列表行缺少前端依赖字段 %q", key)
		}
	}
	roles, _ := row["roles"].([]interface{})
	if len(roles) != 1 {
		t.Fatalf("roles 应下发 1 项, got %+v", row["roles"])
	}
	role, _ := roles[0].(map[string]interface{})
	if role["id"] != float64(5) || role["code"] != "cashier" {
		t.Errorf("角色行字段异常: %+v", role)
	}
	if _, ok := role["permissions"]; ok {
		t.Error("角色行不得携带权限明细（权限按社区实时解析，随行下发会误导为权威口径）")
	}
}
