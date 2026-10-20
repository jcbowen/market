package service

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

/**
 * 管理员按用户名查询的错误口径（GetAdminByUsername）集成回归测试，直连测试库，env 守卫跳过
 *
 * 运行方式（需先起本地 MySQL）：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/user/service/ -run TestGetAdminByUsername -v
 *
 * 覆盖点（对应 D1「登录失败锁定不能把基础设施故障算成凭证失败」的契约）：
 *   - 账号不存在 → bizerr codes.NotFound，经 bizerr.ToStatus 仍是 NotFound（网关据此计入失败次数并回统一文案）
 *   - 数据库不可用 → 普通错误，经 bizerr.ToStatus 归为 Internal（网关据此不计入锁定、回内部错误）
 *   - 两种情况都不得混用，否则 user-service 抖动会把正常管理员锁在门外
 */

// TestGetAdminByUsernameNotFoundIsBizError 验证账号不存在返回 NotFound 业务错误
//
// 参数：
//   - t: 测试对象
func TestGetAdminByUsernameNotFoundIsBizError(t *testing.T) {
	env := newAdminQueryTestEnv(t)
	defer env.close()

	_, err := env.service.GetAdminByUsername(context.Background(), adminQueryMissingUsername)
	var be *bizerr.Error
	if !errors.As(err, &be) {
		t.Fatalf("账号不存在应返回 bizerr 业务错误, got %T: %v", err, err)
	}
	if be.Code() != codes.NotFound {
		t.Errorf("账号不存在的状态码应为 NotFound, got %v", be.Code())
	}
	// server 层统一走 bizerr.ToStatus，这里核对端到端口径
	st, ok := status.FromError(bizerr.ToStatus(err))
	if !ok {
		t.Fatalf("ToStatus 应返回 gRPC 状态错误, got %v", bizerr.ToStatus(err))
	}
	if st.Code() != codes.NotFound {
		t.Errorf("ToStatus 后仍应为 NotFound, got %v", st.Code())
	}
}

// TestGetAdminByUsernameInfraErrorIsNotNotFound 验证数据库故障不被伪装成「账号不存在」
// 连接关闭后的查询错误既不是 ErrRecordNotFound 也不是业务错误，必须原样上抛并归为 Internal
//
// 参数：
//   - t: 测试对象
func TestGetAdminByUsernameInfraErrorIsNotNotFound(t *testing.T) {
	env := newAdminQueryTestEnv(t)
	// 关闭底层连接池制造「服务侧数据库不可用」，与线上 user-service 抖动同源
	sqlDB, err := env.db.GetDb().DB()
	if err != nil {
		t.Fatalf("获取底层连接池失败: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("关闭测试连接池失败: %v", err)
	}

	_, err = env.service.GetAdminByUsername(context.Background(), adminQueryMissingUsername)
	if err == nil {
		t.Fatal("数据库已关闭，查询应返回错误")
	}
	var be *bizerr.Error
	if errors.As(err, &be) && be.Code() == codes.NotFound {
		t.Error("数据库故障不得伪装成「账号不存在」，否则网关会误计失败次数锁死正常账号")
	}
}

// adminQueryMissingUsername 本用例查询的不存在用户名（TEST 前缀，与真实账号不可能冲突）
const adminQueryMissingUsername = "TESTLOGIN-NO-SUCH-ADMIN"

// adminQueryTestEnv 测试环境句柄
type adminQueryTestEnv struct {
	t       *testing.T
	db      *mysql.Instance
	service *AdminService
}

// newAdminQueryTestEnv 建测试库连接并迁移 admins 表
// TEST_MYSQL_PASSWORD 未设置时跳过（与既有 MySQL 集成测试同一守卫口径）
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *adminQueryTestEnv: 被测服务句柄
func newAdminQueryTestEnv(t *testing.T) *adminQueryTestEnv {
	t.Helper()
	if os.Getenv("TEST_MYSQL_PASSWORD") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过管理员查询错误口径测试")
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
	if err := dbInst.GetDb().AutoMigrate(&model.Admin{}); err != nil {
		t.Fatalf("迁移管理员表失败: %v", err)
	}
	// 用例只查询固定不存在用户名、不写入任何行，无需数据清理；
	// 但仍显式确认该用户名不存在，避免存量库里恰好有同名账号把 NotFound 断言变成「查到了」
	var count int64
	if err := dbInst.GetDb().Model(&model.Admin{}).Where("username = ?", adminQueryMissingUsername).Count(&count).Error; err != nil {
		t.Fatalf("核对测试用户名失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("测试库存在同名账号 %s，请人工确认后继续", adminQueryMissingUsername)
	}
	return &adminQueryTestEnv{t: t, db: dbInst, service: NewAdminService(dbInst)}
}

// close 释放测试连接
// 已关闭连接池的用例重复 Close 会报错，此处只记录不断言：清理是否成功不影响结论
func (env *adminQueryTestEnv) close() {
	sqlDB, err := env.db.GetDb().DB()
	if err != nil {
		return
	}
	if err := sqlDB.Close(); err != nil {
		env.t.Logf("关闭测试连接池失败（已关闭或已释放，忽略）: %v", err)
	}
}

// adminQueryEnvOr 读取测试环境变量，缺省时回落本地默认值
//
// 参数：
//   - key: 环境变量名
//   - def: 缺省值
//
// 返回值：
//   - string: 环境变量值或缺省值
func adminQueryEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
