package middleware

import (
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
	"github.com/jcbowen/market/src/backend/common/proto/user"
)

/**
 * 实时鉴权口径（P1-6）离线用例：不依赖 MySQL/Redis/gRPC
 *
 * 背景：社区访问校验与权限校验原先先读 JWT 登录快照，快照命中即放行。
 * 快照是登录那一刻绘制的——超管被降权、管理员被解除社区任职、账号被停用，
 * 都要等 access_token 自然过期或重登才生效，中间期内旧授权仍然可用。
 * 修法：实时上下文（GetAdminContext）优先，快照只在实时解析不可用时作为降级兜底；
 * 账号停用由 user 服务在 GetAdminContext 内直接判为「非超管、无权限、无任职」。
 *
 * 运行方式：
 *   go test ./gateway/middleware/ -run TestRequire -v
 *
 * 覆盖：
 *   - 停用/解除任职的实时结果（空权限、Bound=false）→ 权限校验与社区校验均 403；
 *   - 降权超管（快照 super_admin，实时 IsSuper=false）→ 社区校验 403，不再照抄快照；
 *   - 登录后追加授权（快照不含该社区，实时 Bound=true）→ 放行；
 *   - 实时解析故障（RPC 报错）→ 按快照降级放行，服务抖动不把管理员挡在门外。
 *
 * 使用示例：
 *   info := &user.AdminContextInfo{Permissions: []string{"order:update"}}
 *   c, w := newPermissionTestContext(uc, "/b", "1", info, nil)
 *   RequirePermission("order:update")(c)
 */

// stubAdminCtxClient 权限上下文解析桩：仅覆写 GetAdminContext，其余方法本组用例不触达
type stubAdminCtxClient struct {
	user.UserServiceClient
	info *user.AdminContextInfo
	err  error
}

// GetAdminContext 按预设返回实时权限上下文
func (s *stubAdminCtxClient) GetAdminContext(ctx context.Context, in *user.GetAdminContextReq, opts ...grpc.CallOption) (*user.AdminContextInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.info, nil
}

// newPermissionTestContext 构造带登录快照与当前社区请求头的 gin 上下文
// 参数：
//   - uc: 登录快照（JWT 解析结果）
//   - path: 请求路径（决定 X-Community-Id 解析语境）
//   - header: X-Community-Id 请求头值
//   - info: 实时权限上下文（由桩返回）
//   - err: 实时解析故障（非 nil 时桩返回该错误）
//
// 返回值：
//   - *gin.Context: 待中间件处理的上下文
//   - *httptest.ResponseRecorder: 响应记录器
func newPermissionTestContext(uc *appcontext.UserContext, path, header string, info *user.AdminContextInfo, err error) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	SetPermissionResolver(&stubAdminCtxClient{info: info, err: err})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if header != "" {
		req.Header.Set(HeaderCommunityID, header)
	}
	c.Request = req
	appcontext.SetUserContext(c, uc)
	return c, w
}

// respCode 解析响应体里的业务码
func respCode(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应体失败: %v, body=%q", err, w.Body.String())
	}
	return body.Code
}

// adminSnapshot 登录快照（JWT 承载的角色与社区列表）
// 参数：
//   - role: 角色编码
//   - ids: 快照里的绑定社区列表
//
// 返回值：
//   - *appcontext.UserContext: 可直接注入 gin 上下文的快照
func adminSnapshot(role string, ids []int64) *appcontext.UserContext {
	return &appcontext.UserContext{UserID: 1001, TokenType: jwt.TokenTypeAdmin, Role: role, CommunityID: 1, CommunityIDs: ids}
}

// TestRequirePermissionDeniesDisabledAdmin 停用账号的实时上下文（空权限、非超管）必须拒
// 快照里带着 super_admin 也无济于事——账号状态由 user 服务在 GetAdminContext 内收口
func TestRequirePermissionDeniesDisabledAdmin(t *testing.T) {
	c, w := newPermissionTestContext(adminSnapshot(RoleSuperAdmin, nil), "/api/v1/admin/orders", "",
		&user.AdminContextInfo{IsSuper: false, Permissions: []string{}, Bound: false}, nil)
	RequirePermission("order:update")(c)
	if !c.IsAborted() || respCode(t, w) != errcode.NoPermission {
		t.Errorf("停用账号应被权限中间件拒绝，aborted=%v code=%d", c.IsAborted(), respCode(t, w))
	}
}

// TestRequirePermissionAllowsByLiveRole 实时上下文有权限即放行（与快照角色无关）
func TestRequirePermissionAllowsByLiveRole(t *testing.T) {
	c, w := newPermissionTestContext(adminSnapshot("community_admin", []int64{1}), "/api/v1/admin/orders", "1",
		&user.AdminContextInfo{Permissions: []string{"order:update"}, Bound: true}, nil)
	RequirePermission("order:update")(c)
	if c.IsAborted() || w.Body.String() != "" {
		t.Errorf("实时具备权限应放行，aborted=%v body=%q", c.IsAborted(), w.Body.String())
	}
}

// TestRequireCommunityAccessDeniesDowngradedSuper 降权后的超管不得再凭快照通行
func TestRequireCommunityAccessDeniesDowngradedSuper(t *testing.T) {
	c, w := newPermissionTestContext(adminSnapshot(RoleSuperAdmin, nil), "/api/v1/admin/orders", "9",
		&user.AdminContextInfo{IsSuper: false, Permissions: []string{}, Bound: false}, nil)
	RequireCommunityAccess()(c)
	if !c.IsAborted() || respCode(t, w) != errcode.NoPermission {
		t.Errorf("降权超管访问未任职社区应 403，aborted=%v", c.IsAborted())
	}
}

// TestRequireCommunityAccessDeniesRevokedAssignment 解除社区任职后立即失去该社区访问权
// 快照里仍带着登录时的社区列表，这正是「快照优先」的漏洞形态
func TestRequireCommunityAccessDeniesRevokedAssignment(t *testing.T) {
	c, w := newPermissionTestContext(adminSnapshot("community_admin", []int64{1, 2}), "/api/v1/admin/orders", "2",
		&user.AdminContextInfo{IsSuper: false, Permissions: []string{}, Bound: false}, nil)
	RequireCommunityAccess()(c)
	if !c.IsAborted() || respCode(t, w) != errcode.NoPermission {
		t.Errorf("已解除任职的社区应 403，aborted=%v", c.IsAborted())
	}
}

// TestRequireCommunityAccessAllowsNewAssignment 登录后追加授权的社区应实时放行（不误伤）
func TestRequireCommunityAccessAllowsNewAssignment(t *testing.T) {
	c, w := newPermissionTestContext(adminSnapshot("community_admin", []int64{1}), "/api/v1/admin/orders", "7",
		&user.AdminContextInfo{IsSuper: false, Permissions: []string{"order:update"}, Bound: true}, nil)
	RequireCommunityAccess()(c)
	if c.IsAborted() {
		t.Errorf("新增任职社区应放行，code=%d", respCode(t, w))
	}
}

// TestRequireCommunityAccessFallsBackToSnapshotOnRPCError 实时解析故障时按快照降级放行
// user 服务抖动不能让全站管理员瞬间 403；降级口径与 refresh 黑名单的 fail-open 一致
func TestRequireCommunityAccessFallsBackToSnapshotOnRPCError(t *testing.T) {
	c, w := newPermissionTestContext(adminSnapshot("community_admin", []int64{1, 2}), "/api/v1/admin/orders", "2",
		nil, status.Error(codes.Unavailable, "user-service 不可用"))
	RequireCommunityAccess()(c)
	if c.IsAborted() {
		t.Errorf("实时解析故障应按快照降级放行，code=%d", respCode(t, w))
	}

	// 快照也不含该社区时仍然拒绝：降级只是放宽缓存口径，不是放开越权
	c2, _ := newPermissionTestContext(adminSnapshot("community_admin", []int64{1}), "/api/v1/admin/orders", "2",
		nil, status.Error(codes.Unavailable, "user-service 不可用"))
	RequireCommunityAccess()(c2)
	if !c2.IsAborted() {
		t.Error("故障降级不得放行快照外的社区")
	}
}
