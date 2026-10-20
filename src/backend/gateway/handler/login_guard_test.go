package handler

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo"

	"github.com/jcbowen/market/src/backend/common/config"
)

/**
 * 登录失败锁定（D1）测试：文案与脱敏为离线用例，计数与锁定为 Redis 集成用例（env 守卫跳过）
 *
 * 运行方式（需本地 Redis，与既有 MySQL 集成测试同一套 env）：
 *   TEST_REDIS_PASSWORD=xxx go test ./gateway/handler/ -run TestLoginGuard -v
 *
 * 覆盖：
 *   - 连续失败达 loginFailLimit 次进入锁定期，锁定期内 remaining 递减且不超过窗口
 *   - 登录成功清空计数与锁定标记；锁定按账号隔离
 *   - 第 5 次之前的失败不误锁
 *   - Redis 不可用时放行（fail-open），不因缓存故障关闭登录入口
 *   - 键名含场景段，与 C 端将来接入共用同一前缀时不会互相覆盖
 *   - 锁定文案向上取整到分钟，不出现「剩余 0 分钟」
 *   - 日志脱敏不回显完整手机号或短账号
 */

// TestLoginGuardLockedWindow 验证失败计数到阈值即锁定、成功即清除、锁定按账号隔离
//
// 参数：
//   - t: 测试对象
func TestLoginGuardLockedWindow(t *testing.T) {
	guard, ctx, account := newLoginGuardTestEnv(t)

	for i := 0; i < loginFailLimit-1; i++ {
		guard.recordFailure(ctx, loginSceneAdmin, account)
		if _, locked := guard.lockedRemaining(ctx, loginSceneAdmin, account); locked {
			t.Fatalf("第 %d 次失败不应触发锁定", i+1)
		}
	}
	guard.recordFailure(ctx, loginSceneAdmin, account)

	ttl, locked := guard.lockedRemaining(ctx, loginSceneAdmin, account)
	if !locked {
		t.Fatalf("连续失败 %d 次后应锁定", loginFailLimit)
	}
	if ttl <= 0 || ttl > loginFailWindow {
		t.Errorf("锁定剩余时间应在 (0, %v] 区间, got %v", loginFailWindow, ttl)
	}

	// 账号隔离：其他账号不受影响
	if _, locked := guard.lockedRemaining(ctx, loginSceneAdmin, account+"-other"); locked {
		t.Error("锁定按账号生效，不应波及其他账号")
	}

	guard.clearFailures(ctx, loginSceneAdmin, account)
	if _, locked := guard.lockedRemaining(ctx, loginSceneAdmin, account); locked {
		t.Error("登录成功清除失败标记后应解除锁定")
	}
}

// TestLoginGuardFailOpenWithoutRedis 验证 Redis 不可用时放行（fail-open）
// 用无效端口触发连接失败：锁定是纵深防御的一层，缓存故障不能关掉登录入口
//
// 参数：
//   - t: 测试对象
func TestLoginGuardFailOpenWithoutRedis(t *testing.T) {
	guard := newLoginGuard(&config.Config{Redis: jcbaseGo.RedisStruct{
		Host: "127.0.0.1",
		Port: "1",
		Db:   "15",
	}})
	ctx := context.Background()

	guard.recordFailure(ctx, loginSceneAdmin, "any-account")
	if _, locked := guard.lockedRemaining(ctx, loginSceneAdmin, "any-account"); locked {
		t.Error("Redis 不可用时应放行（仍受 IP 限流兜底）")
	}
}

// TestLoginGuardMessages 验证 Redis 键命名、锁定文案与日志脱敏（离线用例，无需 Redis）
//
// 参数：
//   - t: 测试对象
func TestLoginGuardMessages(t *testing.T) {
	// 键名带场景段：C 端将来接入同一锁定器时，计数与标记不会和后台互相覆盖
	if failKey("admin", "13800138000") == failKey("phone", "13800138000") {
		t.Error("不同场景的失败计数键必须分开")
	}
	if lockKey("admin", "13800138000") != "loginfail:admin:13800138000:lock" {
		t.Errorf("锁定键命名不符预期: %q", lockKey("admin", "13800138000"))
	}

	if got := loginLockedMessage(30 * time.Second); got != "登录失败次数过多，请 1 分钟后再试" {
		t.Errorf("不足 1 分钟应进位为 1 分钟, got %q", got)
	}
	if got := loginLockedMessage(4*time.Minute + 1*time.Second); got != "登录失败次数过多，请 5 分钟后再试" {
		t.Errorf("锁定文案应向上取整到分钟, got %q", got)
	}

	if got := maskAccount("13800138000"); got != "138****8000" {
		t.Errorf("手机号脱敏不符: %q", got)
	}
	if got := maskAccount("admin"); got != "ad***" {
		t.Errorf("用户名脱敏不符: %q", got)
	}
	if got := maskAccount("ab"); got != "a***" {
		t.Errorf("短账号脱敏不符: %q", got)
	}
}

// newLoginGuardTestEnv 构造连到测试 Redis 的锁定器，并注册按键清理钩子
// TEST_REDIS_PASSWORD 未设置时跳过（与 MySQL 集成测试同一守卫口径）
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *loginGuard: 锁定器
//   - context.Context: 请求上下文
//   - string: 本轮唯一账号标识（避免跨轮残留相互干扰）
func newLoginGuardTestEnv(t *testing.T) (*loginGuard, context.Context, string) {
	t.Helper()
	if os.Getenv("TEST_REDIS_PASSWORD") == "" {
		t.Skip("TEST_REDIS_PASSWORD 未设置，跳过登录锁定集成测试")
	}
	cfg := &config.Config{Redis: jcbaseGo.RedisStruct{
		Host:     loginTestEnvOr("TEST_REDIS_HOST", "127.0.0.1"),
		Port:     loginTestEnvOr("TEST_REDIS_PORT", "6379"),
		Password: os.Getenv("TEST_REDIS_PASSWORD"),
		Db:       loginTestEnvOr("TEST_REDIS_DB", "15"),
	}}
	guard := newLoginGuard(cfg)
	ctx := context.Background()
	// 客户端构造本身不建连（生产代码要求 Redis 不可用时不 panic），
	// 测试需显式 Ping 探测可用性，否则连不上只会在断言里表现为「永远未锁定」的假通过
	rds := guard.client()
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := rds.Ping(pingCtx).Err(); err != nil {
		t.Fatalf("测试 Redis 不可用，无法验证登录锁定: %v", err)
	}

	account := "LOGINTEST-" + time.Now().Format("150405.000000")
	keys := []string{
		failKey(loginSceneAdmin, account), lockKey(loginSceneAdmin, account),
		failKey(loginSceneAdmin, account+"-other"), lockKey(loginSceneAdmin, account+"-other"),
	}
	// 键名含时间戳，异常中断不会与下一轮相撞；仍显式删净，避免测试库堆积
	t.Cleanup(func() {
		if err := rds.Del(context.Background(), keys...).Err(); err != nil {
			t.Logf("清理登录锁定键失败（需手工核对 LOGINTEST- 键）: %v", err)
		}
	})
	return guard, ctx, account
}

// loginTestEnvOr 读取环境变量，未设置时返回默认值
//
// 参数：
//   - key: 环境变量名
//   - def: 缺省值
//
// 返回值：
//   - string: 环境变量值或缺省值
func loginTestEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
