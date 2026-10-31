package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	gwMiddleware "github.com/jcbowen/market/src/backend/gateway/middleware"
)

/**
 * 员工改密的社区语境上送测试（审查遗留项：跨社区改他人口令，网关侧）
 *
 * 背景：/admins/:id/password 同时服务「本人改密」与「员工管理改密」，改造前只验旧密码。
 * 员工是多社区任职账号，知道同行口令就能改掉他社区员工乃至超管的密码。
 * 分工：网关负责把操作者社区语境（scopeCommunityID：超管 0，社区管理员取实时采信社区）
 * 随请求上送，user 服务按 admin_communities 任职行裁定归属（见 services/user/service
 * 的 TestChangeAdminPasswordScope）。网关这里锁定「语境不得漏传或传错」，
 * 漏传等于把作用域整条关掉。
 *
 * 运行方式（全部离线，无需 DB/Redis）：
 *   go test ./gateway/handler/ -run TestChangeAdminPasswordCarriesCommunityScope -v
 *
 * 覆盖：
 *   - 实时超管上送 CommunityId=0（不受社区限制）
 *   - 社区管理员采用实时 Bound 社区（新授权即时生效）
 *   - 陈旧快照里的社区不作为语境（回退 JWT 默认社区）
 *   - 服务侧 PermissionDenied 映射为 11005 并透传文案
 *   - 目标 ID / 新旧口令 / force 标记按原样下发
 */

// stubChangePwdClient 改密链路桩：记录 user 服务收到的请求，社区语境是断言重点
type stubChangePwdClient struct {
	user.UserServiceClient
	calls []*user.ChangeAdminPasswordReq
	err   error
}

// ChangeAdminPassword 记录入参并按预设返回
func (s *stubChangePwdClient) ChangeAdminPassword(ctx context.Context, in *user.ChangeAdminPasswordReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.calls = append(s.calls, in)
	if s.err != nil {
		return nil, s.err
	}
	return &common.CommonResponse{}, nil
}

// lastChangePwdReq 取最近一次上送给 user 服务的改密请求
// 参数：
//   - t: 测试对象
//   - stub: 改密桩
//
// 返回值：
//   - *user.ChangeAdminPasswordReq: 最近一次入参
//
// 异常：未发生调用直接 t.Fatalf（漏传语境首先表现为根本没调用）
func lastChangePwdReq(t *testing.T, stub *stubChangePwdClient) *user.ChangeAdminPasswordReq {
	t.Helper()
	if len(stub.calls) == 0 {
		t.Fatal("改密请求未上送 user 服务")
	}
	return stub.calls[len(stub.calls)-1]
}

// newAdminPwdContext 构造改密请求语境（含员工令牌、社区快照与请求头）
// 参数：
//   - role: JWT 角色
//   - snapshot: JWT 快照社区列表（登录时绘制）
//   - defaultComm: JWT 默认社区
//   - header: X-Community-Id 请求头（空串表示不带）
//   - body: JSON 请求体
//
// 返回值：
//   - *gin.Context: 已绑定路径参数 id=12 的上下文
//   - *httptest.ResponseRecorder: 响应记录器
//
// 异常：不触发 panic
func newAdminPwdContext(role string, snapshot []int64, defaultComm int64, header string, body map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	raw, err := json.Marshal(body)
	if err != nil {
		panic(err) // 夹具构造错误，属测试自身缺陷
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/admins/12/password", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if header != "" {
		req.Header.Set(gwMiddleware.HeaderCommunityID, header)
	}
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: "12"}}
	// IsSuperAdmin 走实时口径，但要求令牌是员工令牌，故语境需带 token_type
	c.Set(string(appcontext.KeyUserID), int64(7))
	c.Set(string(appcontext.KeyTokenType), jwt.TokenTypeAdmin)
	c.Set(string(appcontext.KeyRole), role)
	c.Set(string(appcontext.KeyCommunityID), defaultComm)
	c.Set(string(appcontext.KeyCommunityIDs), snapshot)
	return c, recorder
}

// decodeGatewayResponse 解析网关统一响应体
// 参数：
//   - t: 测试对象
//   - rec: 响应记录器
//
// 返回值：
//   - int: 业务码
//   - string: 提示文案
func decodeGatewayResponse(t *testing.T, rec *httptest.ResponseRecorder) (int, string) {
	t.Helper()
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析网关响应失败: %v body=%q", err, rec.Body.String())
	}
	return resp.Code, resp.Message
}

// TestChangeAdminPasswordCarriesCommunityScope 网关必须把操作者社区语境随改密请求上送
func TestChangeAdminPasswordCarriesCommunityScope(t *testing.T) {
	resolver := &stubAdminCtxClient{}
	installAdminCtxResolver(t, resolver)
	pwdStub := &stubChangePwdClient{}
	h := &AdminHandler{userClient: pwdStub}

	// 口令长度满足 pwdpolicy 下限，走明文兼容通道（不声明 pwd_alg）
	body := map[string]string{"old_password": "OldPass-123456", "new_password": "NewPass-654321"}

	t.Run("实时超管上送零语境", func(t *testing.T) {
		pwdStub.calls = nil
		resolver.info = &user.AdminContextInfo{IsSuper: true}
		c, rec := newAdminPwdContext("super_admin", []int64{1}, 1, "999", body)
		h.ChangeAdminPassword(c)

		got := lastChangePwdReq(t, pwdStub)
		if got.CommunityId != 0 {
			t.Errorf("超管语境应上送 0（不受社区限制），实际 %d", got.CommunityId)
		}
		if got.Id != 12 || got.OldPassword != body["old_password"] || got.NewPassword != body["new_password"] || got.Force {
			t.Errorf("改密入参不符: %+v", got)
		}
		if code, _ := decodeGatewayResponse(t, rec); code != 0 {
			t.Errorf("改密成功应回 code=0，实际 %d", code)
		}
	})

	t.Run("社区管理员采用实时采信社区", func(t *testing.T) {
		pwdStub.calls = nil
		resolver.info = &user.AdminContextInfo{Bound: true}
		// 快照不含 999（登录后才授权），实时 Bound=true 仍应采用
		c, _ := newAdminPwdContext("community_admin", []int64{1}, 1, "999", body)
		h.ChangeAdminPassword(c)

		if got := lastChangePwdReq(t, pwdStub); got.CommunityId != 999 {
			t.Errorf("应采用实时 Bound 社区 999 作为语境，实际 %d", got.CommunityId)
		}
	})

	t.Run("陈旧快照社区不作为语境", func(t *testing.T) {
		pwdStub.calls = nil
		resolver.info = &user.AdminContextInfo{Bound: false}
		// 快照仍含 999 但实时已撤销任职：回退默认社区 1，与数据作用域同口径
		c, _ := newAdminPwdContext("community_admin", []int64{1, 999}, 1, "999", body)
		h.ChangeAdminPassword(c)

		if got := lastChangePwdReq(t, pwdStub); got.CommunityId != 1 {
			t.Errorf("陈旧快照社区 999 不得作为语境，应回退默认社区 1，实际 %d", got.CommunityId)
		}
	})

	t.Run("无请求头按默认社区上送", func(t *testing.T) {
		pwdStub.calls = nil
		resolver.info = &user.AdminContextInfo{Bound: true}
		c, _ := newAdminPwdContext("community_admin", []int64{1, 2}, 2, "", body)
		h.ChangeAdminPassword(c)

		if got := lastChangePwdReq(t, pwdStub); got.CommunityId != 2 {
			t.Errorf("无请求头应按 JWT 默认社区 2 上送，实际 %d", got.CommunityId)
		}
	})

	t.Run("服务越权拒绝映射为无权限", func(t *testing.T) {
		pwdStub.calls = nil
		pwdStub.err = status.Error(codes.PermissionDenied, "无权操作其他社区的员工账号")
		defer func() { pwdStub.err = nil }()
		resolver.info = &user.AdminContextInfo{Bound: true}
		c, rec := newAdminPwdContext("community_admin", []int64{1}, 1, "1", body)
		h.ChangeAdminPassword(c)

		code, msg := decodeGatewayResponse(t, rec)
		if code != errcode.NoPermission {
			t.Errorf("PermissionDenied 应映射为 %d，实际 %d", errcode.NoPermission, code)
		}
		if msg != "无权操作其他社区的员工账号" {
			t.Errorf("应透传服务侧文案，实际 %q", msg)
		}
	})

	t.Run("旧密码错误仍按参数错误返回", func(t *testing.T) {
		pwdStub.calls = nil
		pwdStub.err = status.Error(codes.InvalidArgument, "旧密码错误")
		defer func() { pwdStub.err = nil }()
		resolver.info = &user.AdminContextInfo{Bound: true}
		c, rec := newAdminPwdContext("community_admin", []int64{1}, 1, "1", body)
		h.ChangeAdminPassword(c)

		if code, msg := decodeGatewayResponse(t, rec); code != errcode.ParamError || msg != "旧密码错误" {
			t.Errorf("旧密码错误应回 ParamError 并带文案，实际 code=%d msg=%q", code, msg)
		}
	})
}
