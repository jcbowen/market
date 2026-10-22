package config

import (
	"testing"
)

/**
 * debugger 相关环境变量覆盖测试（离线）
 *
 * 运行方式：
 *   go test ./common/config/ -run TestApplyEnvOverrides -v
 *
 * 覆盖：
 *   - DEBUGGER_ALLOWED_IPS 覆盖配置文件白名单，解析风格与 TRUSTED_PROXIES 一致
 *   - DEBUGGER_ENABLED 优先于配置文件值，未设置时不改写
 *   - DEBUGGER_SERVICE_NAME 覆盖 debug_logs 来源标识（去空白），未设置时不改写
 *   - 逗号列表解析：去空白、丢空项
 */

// TestApplyEnvOverridesDebuggerIPs DEBUGGER_ALLOWED_IPS 覆盖配置文件里的白名单（M2）
func TestApplyEnvOverridesDebuggerIPs(t *testing.T) {
	cfg := &Config{}
	cfg.Debugger.AllowedIPs = []string{"10.0.0.1"}

	t.Setenv("DEBUGGER_ALLOWED_IPS", "127.0.0.1, 192.168.1.0/24 ,")
	ApplyEnvOverrides(cfg)

	want := []string{"127.0.0.1", "192.168.1.0/24"}
	if len(cfg.Debugger.AllowedIPs) != len(want) {
		t.Fatalf("白名单应被环境变量覆盖为 %v，实际 %v", want, cfg.Debugger.AllowedIPs)
	}
	for i := range want {
		if cfg.Debugger.AllowedIPs[i] != want[i] {
			t.Errorf("第 %d 项应为 %s，实际 %s", i, want[i], cfg.Debugger.AllowedIPs[i])
		}
	}
}

// TestApplyEnvOverridesDebuggerEnabledWinsOverFile DEBUGGER_ENABLED 优先于配置文件值
func TestApplyEnvOverridesDebuggerEnabledWinsOverFile(t *testing.T) {
	cfg := &Config{}
	cfg.Debugger.Enabled = true

	t.Setenv("DEBUGGER_ENABLED", "false")
	ApplyEnvOverrides(cfg)
	if cfg.Debugger.Enabled {
		t.Error("DEBUGGER_ENABLED=false 应关闭 debugger")
	}

	t.Setenv("DEBUGGER_ENABLED", "1")
	ApplyEnvOverrides(cfg)
	if !cfg.Debugger.Enabled {
		t.Error("DEBUGGER_ENABLED=1 应开启 debugger")
	}

	// 空值等同未设置，保持配置文件值
	cfg.Debugger.Enabled = true
	t.Setenv("DEBUGGER_ENABLED", "")
	ApplyEnvOverrides(cfg)
	if !cfg.Debugger.Enabled {
		t.Error("DEBUGGER_ENABLED 为空时不应改写配置文件值")
	}
}

// TestApplyEnvOverridesDebuggerServiceName DEBUGGER_SERVICE_NAME 覆盖 debug_logs 来源标识
func TestApplyEnvOverridesDebuggerServiceName(t *testing.T) {
	cfg := &Config{}
	cfg.Debugger.ServiceName = "gateway"

	// 覆盖并去空白（compose 按进程名注入 payment-service/order-service）
	t.Setenv("DEBUGGER_SERVICE_NAME", " payment-service ")
	ApplyEnvOverrides(cfg)
	if cfg.Debugger.ServiceName != "payment-service" {
		t.Errorf("DEBUGGER_SERVICE_NAME 应覆盖来源标识（去空白），实际 %q", cfg.Debugger.ServiceName)
	}

	// 空值等同未设置，保持配置文件值
	cfg.Debugger.ServiceName = "gateway"
	t.Setenv("DEBUGGER_SERVICE_NAME", "")
	ApplyEnvOverrides(cfg)
	if cfg.Debugger.ServiceName != "gateway" {
		t.Errorf("DEBUGGER_SERVICE_NAME 为空时不应改写配置文件值，实际 %q", cfg.Debugger.ServiceName)
	}
}

// TestApplyEnvOverridesTrustedProxiesStillWorks 复用逗号解析后可信代理网段行为不变
func TestApplyEnvOverridesTrustedProxiesStillWorks(t *testing.T) {
	cfg := &Config{}
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1/32, ::1")
	ApplyEnvOverrides(cfg)

	want := []string{"127.0.0.1/32", "::1"}
	if len(cfg.Security.TrustedProxies) != len(want) {
		t.Fatalf("可信代理应为 %v，实际 %v", want, cfg.Security.TrustedProxies)
	}
	for i := range want {
		if cfg.Security.TrustedProxies[i] != want[i] {
			t.Errorf("第 %d 项应为 %s，实际 %s", i, want[i], cfg.Security.TrustedProxies[i])
		}
	}
}

// TestSplitCSVList 逗号列表解析：去空白、丢空项，全空白返回空切片
func TestSplitCSVList(t *testing.T) {
	got := splitCSVList(" a , b ")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("splitCSVList(\" a , b \") = %v，期望 [a b]", got)
	}
	if list := splitCSVList(",, ,"); len(list) != 0 {
		t.Errorf("全空白输入应返回空切片，实际 %v", list)
	}
	if list := splitCSVList(""); len(list) != 0 {
		t.Errorf("空输入应返回空切片，实际 %v", list)
	}
}
