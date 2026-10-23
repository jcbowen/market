package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	gwMiddleware "github.com/jcbowen/market/src/backend/gateway/middleware"
)

/**
 * 数据作用域社区判定实时口径（P1-10）测试
 *
 * 背景：getAdminCommunityID 原以 JWT 快照（uc.CommunityIDs，登录时绘制）判定请求头
 * 声明的社区是否可采信——授权撤销后快照不刷新，被撤销社区在令牌剩余有效期内
 * 仍被当作数据作用域；反之登录后新授权的社区也要等重登才能使用。
 * 修法：改走 AdminCommunityAccessible（GetAdminContext 的 Bound 实时口径），
 * 与 RequirePermission 共用同一请求内记忆化的实时上下文，每请求最多一次 RPC。
 *
 * 运行方式（全部离线，无需 DB/Redis）：
 *   go test ./gateway/handler/ -run 'TestAdminCommunityScope' -v
 *
 * 覆盖：
 *   - 超管采用请求头社区，但同样走实时口径（快照 super_admin 已降权则不采用）；
 *   - 普通管理员请求头社区实时 Bound=true → 采用（快照不含它也要采用——新授权即时生效）；
 *   - 普通管理员请求头社区实时 Bound=false 但快照仍含它（授权已撤销的陈旧快照）
 *     → 不采用，回退 JWT 默认社区（P1-10 核心回归）；
 *   - RPC IsSuper=true 同样放行；
 *   - 实时上下文解析失败 → 回退默认社区（宁可保守）；
 *   - 无请求头/非法请求头（非数字、0、负数）→ 回退默认社区；
 *   - 记忆化：同一请求内重复判定只发一次 RPC；
 *   - AdminCommunityAccessible 直测：解析失败按不合法处理（fail-closed）。
 */

// stubAdminCtxClient 权限上下文解析桩：仅覆写 GetAdminContext，记录调用次数与入参
type stubAdminCtxClient struct {
	user.UserServiceClient
	calls    int
	lastID   int64 // 最近一次请求的 AdminId
	lastComm int64 // 最近一次请求的 CommunityId
	info     *user.AdminContextInfo
	err      error
}

func (s *stubAdminCtxClient) GetAdminContext(ctx context.Context, in *user.GetAdminContextReq, opts ...grpc.CallOption) (*user.AdminContextInfo, error) {
	s.calls++
	s.lastID = in.AdminId
	s.lastComm = in.CommunityId
	if s.err != nil {
		return nil, s.err
	}
	return s.info, nil
}

// installAdminCtxResolver 注入解析桩并登记恢复：测试结束后置为恒失败桩，
// 与「未注入」一样走 fail-closed，不污染同包其他用例
func installAdminCtxResolver(t *testing.T, stub *stubAdminCtxClient) {
	t.Helper()
	gwMiddleware.SetPermissionResolver(stub)
	t.Cleanup(func() {
		gwMiddleware.SetPermissionResolver(&stubAdminCtxClient{
			err: status.Error(codes.Internal, "resolver reset after test"),
		})
	})
}

// newCommunityScopeContext 构造数据作用域判定用的 gin 语境
//
// 参数：
//   - role: JWT 角色（super_admin / community_admin 等）
//   - snapshot: JWT 快照社区列表（登录时绘制）
//   - defaultComm: JWT 默认社区
//   - header: X-Community-Id 请求头（空串表示不携带）
func newCommunityScopeContext(role string, snapshot []int64, defaultComm int64, header string) *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/orders", nil)
	if header != "" {
		req.Header.Set(gwMiddleware.HeaderCommunityID, header)
	}
	c.Request = req
	c.Set(string(appcontext.KeyUserID), int64(7))
	c.Set(string(appcontext.KeyRole), role)
	c.Set(string(appcontext.KeyCommunityID), defaultComm)
	c.Set(string(appcontext.KeyCommunityIDs), snapshot)
	return c
}

// TestAdminCommunityScopeResolution 覆盖 getAdminCommunityID 的社区采信矩阵
func TestAdminCommunityScopeResolution(t *testing.T) {
	stub := &stubAdminCtxClient{}
	installAdminCtxResolver(t, stub)
	h := &AdminHandler{}

	t.Run("超管采用请求头社区但须实时确认", func(t *testing.T) {
		stub.calls = 0
		stub.err = nil
		stub.info = &user.AdminContextInfo{IsSuper: true}
		c := newCommunityScopeContext("super_admin", []int64{1}, 1, "999")
		if got := h.getAdminCommunityID(c); got != 999 {
			t.Errorf("实时超管应采用请求头社区 999，得到 %d", got)
		}
		if stub.calls != 1 {
			t.Errorf("超管判定不再走快照直通，应触达一次 RPC，实际 %d 次", stub.calls)
		}
	})

	t.Run("降权超管不得再任意切换社区", func(t *testing.T) {
		// 快照仍是登录时绘制的 super_admin，实时口径 IsSuper=false 且该社区无任职 → 回退默认社区
		stub.calls = 0
		stub.err = nil
		stub.info = &user.AdminContextInfo{IsSuper: false, Bound: false}
		c := newCommunityScopeContext("super_admin", []int64{1}, 1, "999")
		if got := h.getAdminCommunityID(c); got != 1 {
			t.Errorf("降权超管应回退 JWT 默认社区 1，得到 %d", got)
		}
	})

	t.Run("普通管理员Bound社区采用即使快照不含", func(t *testing.T) {
		// 登录后才被授权社区 999：快照只有 1，但实时 Bound=true
		stub.calls = 0
		stub.info = &user.AdminContextInfo{Bound: true, Permissions: []string{"order:list"}}
		c := newCommunityScopeContext("community_admin", []int64{1}, 1, "999")
		if got := h.getAdminCommunityID(c); got != 999 {
			t.Errorf("实时 Bound=true 应采用请求头社区 999（新授权即时生效），得到 %d", got)
		}
		if stub.lastID != 7 || stub.lastComm != 999 {
			t.Errorf("RPC 入参不符: adminID=%d communityID=%d", stub.lastID, stub.lastComm)
		}
	})

	t.Run("撤销授权后陈旧快照不得作为数据作用域", func(t *testing.T) {
		// P1-10 核心回归：快照仍含 999（登录时绘制），但实时 Bound=false → 回退默认社区
		stub.calls = 0
		stub.info = &user.AdminContextInfo{Bound: false}
		c := newCommunityScopeContext("community_admin", []int64{1, 999}, 1, "999")
		if got := h.getAdminCommunityID(c); got != 1 {
			t.Errorf("实时 Bound=false 应回退 JWT 默认社区 1（陈旧快照不可采信），得到 %d", got)
		}
	})

	t.Run("RPC报IsSuper同样放行", func(t *testing.T) {
		stub.calls = 0
		stub.info = &user.AdminContextInfo{IsSuper: true}
		c := newCommunityScopeContext("community_admin", []int64{1}, 1, "555")
		if got := h.getAdminCommunityID(c); got != 555 {
			t.Errorf("实时 IsSuper 应采用请求头社区 555，得到 %d", got)
		}
	})

	t.Run("上下文解析失败回退默认社区", func(t *testing.T) {
		stub.calls = 0
		stub.err = status.Error(codes.Unavailable, "user service down")
		c := newCommunityScopeContext("community_admin", []int64{1}, 1, "999")
		if got := h.getAdminCommunityID(c); got != 1 {
			t.Errorf("解析失败应保守回退默认社区 1，得到 %d", got)
		}
	})

	t.Run("无请求头回退默认社区", func(t *testing.T) {
		stub.calls = 0
		stub.info = &user.AdminContextInfo{Bound: true}
		c := newCommunityScopeContext("community_admin", []int64{1, 2}, 2, "")
		if got := h.getAdminCommunityID(c); got != 2 {
			t.Errorf("无请求头应回退 JWT 默认社区 2，得到 %d", got)
		}
		if stub.calls != 0 {
			t.Errorf("无请求头不应触达 RPC，实际调用 %d 次", stub.calls)
		}
	})

	t.Run("非法请求头回退默认社区", func(t *testing.T) {
		stub.calls = 0
		stub.info = &user.AdminContextInfo{Bound: true}
		for _, header := range []string{"abc", "0", "-3"} {
			c := newCommunityScopeContext("community_admin", []int64{1}, 1, header)
			if got := h.getAdminCommunityID(c); got != 1 {
				t.Errorf("非法请求头 %q 应回退默认社区 1，得到 %d", header, got)
			}
		}
		if stub.calls != 0 {
			t.Errorf("非法请求头不应触达 RPC，实际调用 %d 次", stub.calls)
		}
	})

	t.Run("同请求内重复判定只发一次RPC", func(t *testing.T) {
		stub.calls = 0
		stub.err = nil // 复位前序用例注入的错误
		stub.info = &user.AdminContextInfo{Bound: true}
		c := newCommunityScopeContext("community_admin", []int64{1}, 1, "999")
		if got := h.getAdminCommunityID(c); got != 999 {
			t.Fatalf("首次判定应采用请求头社区 999，得到 %d", got)
		}
		if got := h.getAdminCommunityID(c); got != 999 {
			t.Errorf("二次判定应得到相同结果 999，得到 %d", got)
		}
		if stub.calls != 1 {
			t.Errorf("请求内记忆化应只发一次 RPC，实际 %d 次", stub.calls)
		}
	})
}

// TestAdminCommunityAccessibleDirect 直测中间件包的判定函数（fail-closed 语义）
func TestAdminCommunityAccessibleDirect(t *testing.T) {
	stub := &stubAdminCtxClient{}
	installAdminCtxResolver(t, stub)

	// 超管：同样以实时口径为准，快照直通已移除
	stub.calls = 0
	stub.info = &user.AdminContextInfo{IsSuper: true}
	c := newCommunityScopeContext("super_admin", []int64{1}, 1, "42")
	if !gwMiddleware.AdminCommunityAccessible(c) {
		t.Error("实时超管应判为合法")
	}
	if stub.calls != 1 {
		t.Errorf("超管判定应触达一次 RPC，实际 %d 次", stub.calls)
	}

	// 快照超管但实时已降权且无任职：不合法（P1-6 降权即时生效）
	stub.calls = 0
	stub.info = &user.AdminContextInfo{IsSuper: false, Bound: false}
	c = newCommunityScopeContext("super_admin", []int64{1}, 1, "42")
	if gwMiddleware.AdminCommunityAccessible(c) {
		t.Error("降权超管不得凭登录快照判为合法")
	}

	// 普通管理员：Bound=false 不合法
	stub.calls = 0
	stub.info = &user.AdminContextInfo{Bound: false}
	c = newCommunityScopeContext("community_admin", []int64{1}, 1, "42")
	if gwMiddleware.AdminCommunityAccessible(c) {
		t.Error("Bound=false 应判为不合法")
	}

	// 普通管理员：解析失败不合法（宁可少给，不可多给）
	stub.calls = 0
	stub.err = status.Error(codes.Unavailable, "down")
	c = newCommunityScopeContext("community_admin", []int64{1}, 1, "42")
	if gwMiddleware.AdminCommunityAccessible(c) {
		t.Error("解析失败应判为不合法（fail-closed）")
	}
}
