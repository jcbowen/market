package service

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

/**
 * 员工改密的社区作用域测试（审查遗留项：跨社区改他人口令）
 *
 * 背景：/user/admins/:id/password 同时服务「本人改密」与「员工管理改密」，改造前只验旧密码。
 * 员工是多社区任职的账号，任一员工只要知道同行口令（离职同事、共享弱口令都可能被知道），
 * 就能改掉他社区员工乃至超管账号的密码——旧密码校验不构成租户边界。
 * 修法：网关把操作者社区语境（scopeCommunityID）随请求带上，user 服务按 admin_communities
 * 任职行判定；超管语境传 0 放行，force（超管重置）分支由网关 IsSuperAdmin 把关、不受作用域限制。
 *
 * 运行方式（需本地 docker 栈，env 守卫跳过）：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/user/service/ -run TestChangeAdminPasswordScope -v
 *
 * 覆盖：
 *   - 本社区员工改密成功（新口令可校验、旧口令失效）
 *   - 跨社区员工一律 PermissionDenied，且目标口令未被改写
 *   - 越权请求不暴露目标旧密码是否正确（作用域校验先于旧密码校验）
 *   - 超管语境 communityID=0 放行（社区管理员不可能有超管任职行，天然只能走此路径）
 *   - force=true 不受作用域限制（超管重置密码）
 *   - 旧密码错误仍拒绝，且不改动口令
 *   - 目标员工不存在返回 NotFound
 *
 * 测试账号统一使用 pwdScopePrefix 前缀用户名与 991000xx 社区号段，用例结束即删除。
 */

// pwdScopePrefix 改密作用域测试账号用户名前缀（清理依据）
const pwdScopePrefix = "PWSCOPE-"

// 测试社区号段（不与真实社区混用）
const (
	pwdScopeCommunityA = int64(99100099) // 员工 A 的任职社区
	pwdScopeCommunityB = int64(99100098) // 员工 B 的任职社区（A 的管理员看不到）
)

// pwdScopeEnv 改密作用域测试环境句柄
type pwdScopeEnv struct {
	db      *mysql.Instance
	service *AdminService
}

// newPwdScopeTestEnv 建测试库连接、迁移员工与任职表，并预置两个分属不同社区的测试员工
// TEST_MYSQL_PASSWORD 未设置时跳过；预置前若同名账号已存在直接失败，避免把存量账号当测试数据删掉
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *pwdScopeEnv: 被测服务句柄与测试库
func newPwdScopeTestEnv(t *testing.T) *pwdScopeEnv {
	t.Helper()
	if os.Getenv("TEST_MYSQL_PASSWORD") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过改密社区作用域集成测试")
	}
	dbInst, err := mysql.New(jcbaseGo.DbStruct{
		Host:      adminQueryEnvOr("TEST_MYSQL_HOST", "127.0.0.1"),
		Port:      adminQueryEnvOr("TEST_MYSQL_PORT", "3306"),
		Dbname:    adminQueryEnvOr("TEST_MYSQL_DBNAME", "market"),
		Username:  adminQueryEnvOr("TEST_MYSQL_USER", "root"),
		Password:  os.Getenv("TEST_MYSQL_PASSWORD"),
		Charset:   "utf8mb4",
		ParseTime: "True",
		Loc:       "Local",
	})
	if err != nil {
		t.Fatalf("连接测试 MySQL 失败: %v", err)
	}
	if err := dbInst.GetDb().AutoMigrate(&model.Admin{}, &model.AdminCommunity{}); err != nil {
		t.Fatalf("迁移员工与任职表失败: %v", err)
	}
	for _, username := range []string{pwdScopePrefix + "A", pwdScopePrefix + "B", pwdScopePrefix + "SUPER"} {
		var cnt int64
		if err := dbInst.GetDb().Model(&model.Admin{}).Where("username = ?", username).Count(&cnt).Error; err != nil {
			t.Fatalf("核对测试账号失败: %v", err)
		}
		if cnt != 0 {
			t.Fatalf("测试库存在同名账号 %s，请人工确认后继续", username)
		}
	}
	t.Cleanup(func() { cleanupPwdScopeData(t, dbInst) })
	return &pwdScopeEnv{db: dbInst, service: NewAdminService(dbInst)}
}

// cleanupPwdScopeData 删除测试员工与其任职行（按用户名前缀与社区号段定位，不动存量数据）
// 参数：
//   - t: 测试对象
//   - dbInst: 测试库实例
func cleanupPwdScopeData(t *testing.T, dbInst *mysql.Instance) {
	t.Helper()
	ctx := context.Background()
	db := dbInst.GetDb().WithContext(ctx)
	if err := db.Exec(
		"DELETE FROM admin_communities WHERE community_id IN (?, ?) AND admin_id IN (SELECT id FROM admin_users WHERE username LIKE ?)",
		pwdScopeCommunityA, pwdScopeCommunityB, pwdScopePrefix+"%").Error; err != nil {
		t.Logf("清理测试任职行失败: %v", err)
	}
	if err := db.Where("username LIKE ?", pwdScopePrefix+"%").Delete(&model.Admin{}).Error; err != nil {
		t.Logf("清理测试员工失败: %v", err)
	}
}

// seedPwdScopeAdmin 预置一个测试员工（口令固定，可选是否超管），并返回其 ID
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - suffix: 用户名后缀（拼在 pwdScopePrefix 之后）
//   - isSuper: 是否超管账号
//
// 返回值：
//   - int64: 员工 ID
func seedPwdScopeAdmin(t *testing.T, env *pwdScopeEnv, suffix string, isSuper bool) int64 {
	t.Helper()
	hashed, err := hashPassword(pwdScopeOldPassword)
	if err != nil {
		t.Fatalf("预置口令摘要失败: %v", err)
	}
	a := &model.Admin{Username: pwdScopePrefix + suffix, Password: hashed, RealName: "改密作用域测试", IsSuper: isSuper, Status: 1}
	if err := env.db.GetDb().WithContext(context.Background()).Create(a).Error; err != nil {
		t.Fatalf("插入测试员工失败: %v", err)
	}
	return a.ID
}

// seedPwdScopeAssignment 预置一条员工在某社区的任职行
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - adminID: 员工 ID
//   - communityID: 社区 ID
func seedPwdScopeAssignment(t *testing.T, env *pwdScopeEnv, adminID, communityID int64) {
	t.Helper()
	if err := env.db.GetDb().WithContext(context.Background()).Create(&model.AdminCommunity{
		AdminID: adminID, CommunityID: communityID, RoleID: 1,
	}).Error; err != nil {
		t.Fatalf("插入测试任职行失败: %v", err)
	}
}

// 测试口令常量（弱口令仅为本用例内部夹具，与业务账号无关）
const (
	pwdScopeOldPassword = "OldPass-123456"
	pwdScopeNewPassword = "NewPass-654321"
)

// currentPasswordHash 回查员工当前口令摘要
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - adminID: 员工 ID
//
// 返回值：
//   - string: bcrypt 摘要
func currentPasswordHash(t *testing.T, env *pwdScopeEnv, adminID int64) string {
	t.Helper()
	var hashed string
	if err := env.db.GetDb().WithContext(context.Background()).Raw(
		"SELECT password FROM admin_users WHERE id = ?", adminID).Row().Scan(&hashed); err != nil {
		t.Fatalf("回查员工口令失败: %v", err)
	}
	return hashed
}

// bizCodeOf 取业务错误码（非 bizerr 时返回 Unknown，便于断言错误语义而不是只看报没报错）
// 参数：
//   - err: 被测函数返回的错误
//
// 返回值：
//   - codes.Code: gRPC 状态码
func bizCodeOf(err error) codes.Code {
	var be *bizerr.Error
	if errors.As(err, &be) {
		return be.Code()
	}
	return codes.Unknown
}

// TestChangeAdminPasswordScope 改密的社区作用域断言
// 用例逐个建员工，避免相互污染；每个断言后复核目标口令是否被改写
func TestChangeAdminPasswordScope(t *testing.T) {
	env := newPwdScopeTestEnv(t)
	ctx := context.Background()

	t.Run("本社区员工改密成功", func(t *testing.T) {
		adminID := seedPwdScopeAdmin(t, env, "A", false)
		seedPwdScopeAssignment(t, env, adminID, pwdScopeCommunityA)

		err := env.service.ChangeAdminPassword(ctx, adminID, pwdScopeOldPassword, pwdScopeNewPassword, false, pwdScopeCommunityA)
		if err != nil {
			t.Fatalf("本社区改密应放行, got %v", err)
		}
		hashed := currentPasswordHash(t, env, adminID)
		if CheckPassword(pwdScopeNewPassword, hashed) != nil {
			t.Error("新口令应可通过校验")
		}
		if CheckPassword(pwdScopeOldPassword, hashed) == nil {
			t.Error("旧口令应已失效")
		}
	})

	t.Run("跨社区员工一律拒绝", func(t *testing.T) {
		adminID := seedPwdScopeAdmin(t, env, "B", false)
		seedPwdScopeAssignment(t, env, adminID, pwdScopeCommunityB)

		before := currentPasswordHash(t, env, adminID)
		err := env.service.ChangeAdminPassword(ctx, adminID, pwdScopeOldPassword, pwdScopeNewPassword, false, pwdScopeCommunityA)
		if code := bizCodeOf(err); code != codes.PermissionDenied {
			t.Fatalf("跨社区改密应返回 PermissionDenied, got code=%v err=%v", code, err)
		}
		if currentPasswordHash(t, env, adminID) != before {
			t.Fatal("越权请求不得改写目标口令")
		}
	})

	t.Run("越权请求不暴露旧密码是否正确", func(t *testing.T) {
		adminID := seedPwdScopeAdmin(t, env, "B2", false)
		seedPwdScopeAssignment(t, env, adminID, pwdScopeCommunityB)

		// 旧密码故意写错：作用域校验在前，返回的必须是「无权操作」而不是「旧密码错误」
		err := env.service.ChangeAdminPassword(ctx, adminID, "WrongPass-000000", pwdScopeNewPassword, false, pwdScopeCommunityA)
		if code := bizCodeOf(err); code != codes.PermissionDenied {
			t.Fatalf("先判作用域，应返回 PermissionDenied, got code=%v err=%v", code, err)
		}
	})

	t.Run("超管语境传 0 不受社区限制", func(t *testing.T) {
		// 超管账号无任何社区任职行：社区管理员语境（communityID>0）必然被拒，只能走 0 放行路径
		adminID := seedPwdScopeAdmin(t, env, "SUPER", true)
		before := currentPasswordHash(t, env, adminID)
		if err := env.service.ChangeAdminPassword(ctx, adminID, pwdScopeOldPassword, pwdScopeNewPassword, false, pwdScopeCommunityA); bizCodeOf(err) != codes.PermissionDenied {
			t.Fatalf("社区语境改超管口令应被拒, got code=%v err=%v", bizCodeOf(err), err)
		}
		if currentPasswordHash(t, env, adminID) != before {
			t.Fatal("被拒的改密不得改写超管口令")
		}
		if err := env.service.ChangeAdminPassword(ctx, adminID, pwdScopeOldPassword, pwdScopeNewPassword, false, 0); err != nil {
			t.Fatalf("超管语境（communityID=0）应放行, got %v", err)
		}
		if CheckPassword(pwdScopeNewPassword, currentPasswordHash(t, env, adminID)) != nil {
			t.Error("超管语境改密后新口令应生效")
		}
	})

	t.Run("force 重置不受作用域限制", func(t *testing.T) {
		adminID := seedPwdScopeAdmin(t, env, "B3", false)
		seedPwdScopeAssignment(t, env, adminID, pwdScopeCommunityB)

		// force=超管重置密码：跳过旧密码与作用域校验，越权判定由网关 IsSuperAdmin 负责
		if err := env.service.ChangeAdminPassword(ctx, adminID, "", pwdScopeNewPassword, true, pwdScopeCommunityA); err != nil {
			t.Fatalf("force 重置应放行, got %v", err)
		}
		if CheckPassword(pwdScopeNewPassword, currentPasswordHash(t, env, adminID)) != nil {
			t.Error("force 重置后新口令应生效")
		}
	})

	t.Run("旧密码错误仍拒绝", func(t *testing.T) {
		adminID := seedPwdScopeAdmin(t, env, "A2", false)
		seedPwdScopeAssignment(t, env, adminID, pwdScopeCommunityA)
		before := currentPasswordHash(t, env, adminID)

		err := env.service.ChangeAdminPassword(ctx, adminID, "WrongPass-000000", pwdScopeNewPassword, false, pwdScopeCommunityA)
		if code := bizCodeOf(err); code != codes.InvalidArgument {
			t.Fatalf("旧密码错误应返回 InvalidArgument, got code=%v err=%v", code, err)
		}
		if currentPasswordHash(t, env, adminID) != before {
			t.Fatal("旧密码错误不得改写口令")
		}
	})

	t.Run("无任职行员工在社区语境下拒绝", func(t *testing.T) {
		// 未在任何社区任职的员工：社区管理员语境同样改不了（避免「无任职=无边界」的漏判）
		adminID := seedPwdScopeAdmin(t, env, "A3", false)
		err := env.service.ChangeAdminPassword(ctx, adminID, pwdScopeOldPassword, pwdScopeNewPassword, false, pwdScopeCommunityA)
		if code := bizCodeOf(err); code != codes.PermissionDenied {
			t.Fatalf("无任职行应返回 PermissionDenied, got code=%v err=%v", code, err)
		}
	})

	t.Run("目标员工不存在返回 NotFound", func(t *testing.T) {
		err := env.service.ChangeAdminPassword(ctx, 991000999999, pwdScopeOldPassword, pwdScopeNewPassword, false, pwdScopeCommunityA)
		if code := bizCodeOf(err); code != codes.NotFound {
			t.Fatalf("不存在的员工应返回 NotFound, got code=%v err=%v", code, err)
		}
	})
}
