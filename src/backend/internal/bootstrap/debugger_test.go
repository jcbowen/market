package bootstrap

import (
	"testing"

	"github.com/jcbowen/market/src/backend/common/config"
)

/**
 * debugger 进程级日志初始化测试（离线，不连真实 MySQL）
 *
 * 运行方式：
 *   go test ./internal/bootstrap/ -v
 *
 * 覆盖：
 *   - 未启用/无配置/无连接三种降级入参都返回 nil 实例与空释放函数，且绝不触碰 MySQL
 *     （nil 实例登记给 debuglog 后各记录点静默跳过，是「辅助能力不影响主链路」的前提）
 *   - 记录条数上限换算与 gateway registerDebugger 同口径
 */

// TestRegisterProcessDebuggerDegradesToNil 降级入参一律返回 nil，不建连接、不 panic
// 参数：
//   - t: 测试对象
func TestRegisterProcessDebuggerDegradesToNil(t *testing.T) {
	enabledCfg := &config.Config{}
	enabledCfg.Debugger.Enabled = true

	cases := []struct {
		name string
		cfg  *config.Config
	}{
		{"配置为 nil", nil},
		{"debugger 未启用", &config.Config{}},
		{"已启用但无 MySQL 实例", enabledCfg},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dbg, stop := RegisterProcessDebugger(c.cfg, nil)
			if dbg != nil {
				t.Errorf("%s: 应返回 nil 实例，实际 %p", c.name, dbg)
			}
			if stop == nil {
				t.Fatalf("%s: 释放函数不应为 nil", c.name)
			}
			// 空释放函数必须可安全重复调用（main 里 defer，测试里再调一次）
			stop()
			stop()
		})
	}
}

// TestDebuggerMaxRecordsShared 上限换算：nil=默认 5000，<0=不限制（0），其余原值
func TestDebuggerMaxRecordsShared(t *testing.T) {
	zero, negative, custom := 0, -1, 200
	cases := []struct {
		name string
		in   *int
		want int
	}{
		{"未配置取默认", nil, 5000},
		{"显式 0 不裁剪", &zero, 0},
		{"负数视为不限制", &negative, 0},
		{"正常值原样透传", &custom, 200},
	}
	for _, c := range cases {
		if got := debuggerMaxRecordsShared(c.in); got != c.want {
			t.Errorf("%s: 期望 %d，实际 %d", c.name, c.want, got)
		}
	}
}
