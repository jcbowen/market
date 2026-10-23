package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/debuglog"
)

/**
 * 网关 debugger 接入口径测试（离线，不连真实 MySQL）
 *
 * 运行方式：
 *   go test ./gateway/ -run TestDebugger -v
 *
 * 覆盖：
 *   - 凭据入口（认证/支付回调）与上传路由在默认跳过列表内，整条不入库
 *   - 未启用时不注册路由，释放函数可重复调用
 *   - MySQL 不可达仅降级：不 panic、不暴露调试页面、/health 可见原因
 *   - 三态掩码开关的日志文案区分「未配置」与「显式 false」
 *   - 显式 0 的保留期/记录上限/采样率不被默认值吞掉（关闭口径为负哨兵）
 */

// newDebuggerTestConfig 构造仅填 debugger 相关字段的配置（不连真实 MySQL）
// 参数：
//   - enabled: debugger.enabled
//   - allowedIPs: 页面白名单
//
// 返回值：
//   - *config.Config: 可直接交给 registerDebugger 的配置
//
// 异常：不触发 panic
func newDebuggerTestConfig(enabled bool, allowedIPs []string) *config.Config {
	cfg := &config.Config{}
	cfg.Db = jcbaseGo.DbStruct{
		// 端口 1 无任何监听，建连即被拒绝，用于验证「MySQL 不可达只降级不 panic」
		Host:     "127.0.0.1",
		Port:     "1",
		Dbname:   "market_test_unreachable",
		Username: "root",
	}
	cfg.Debugger.Enabled = enabled
	cfg.Debugger.ServiceName = "gateway"
	cfg.Debugger.BasePath = "/jcbase/debug"
	cfg.Debugger.AllowedIPs = allowedIPs
	cfg.Debugger.MaxRecords = intPtr(100)
	cfg.Debugger.MaxBodySize = 16
	cfg.Debugger.SampleRate = floatPtr(1.0)
	return cfg
}

// intPtr / floatPtr 取字面量地址，便于在测试里构造「显式 0」这类指针三态配置
func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

// containsPath 判断路径前缀是否在默认跳过列表内
func containsPath(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// TestDebuggerDefaultSkipPaths 凭据入口与上传路由必须整条不入库（掩码之外的第二道防线）
func TestDebuggerDefaultSkipPaths(t *testing.T) {
	for _, prefix := range []string{
		"/api/v1/auth",
		"/api/v1/admin/auth",
		"/api/v1/payments/callback",
		"/api/v1/upload/image",
		"/api/v1/admin/upload/image",
		"/api/v1/admin/settings/payment",
		"/api/v1/admin/settings/attachment",
		"/health",
	} {
		if !containsPath(debuggerDefaultSkipPaths, prefix) {
			t.Errorf("默认跳过路径缺少 %s", prefix)
		}
	}

	// 组件按前缀匹配，这里确认登录/回调的具体路由确实命中
	for _, path := range []string{
		"/api/v1/auth/login/phone",
		"/api/v1/auth/refresh",
		"/api/v1/admin/auth/login",
		"/api/v1/admin/auth/logout",
		"/api/v1/payments/callback/alipay",
		"/api/v1/admin/upload/image",
		"/api/v1/admin/settings/payment",      // 支付配置保存（首次配置携带 V3 密钥）
		"/api/v1/admin/settings/payment/cert", // 证书粘贴上传（携带私钥明文）
		"/api/v1/admin/settings/attachment",   // 附件配置保存（首次配置携带 OSS Secret）
	} {
		matched := false
		for _, prefix := range debuggerDefaultSkipPaths {
			if strings.HasPrefix(path, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s 应被默认跳过", path)
		}
	}
}

// TestDebuggerDisabled 未启用时返回可用的释放函数，状态可被 /health 读出
func TestDebuggerDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	stop := registerDebugger(r, newDebuggerTestConfig(false, nil))
	if stop == nil {
		t.Fatal("未启用也应返回可用的释放函数，避免调用方做 nil 判断")
	}
	stop()
	stop() // 释放函数须可重复调用

	if got := debuggerHealthInfo().State; got != "disabled" {
		t.Errorf("状态应为 disabled，实际 %q", got)
	}
	if len(r.Routes()) != 0 {
		t.Errorf("未启用时不应注册任何路由，实际 %d 条", len(r.Routes()))
	}
	// 未启用不得登记进程级记录实例：回调/调度器等记录点须保持静默
	if debuglog.Instance() != nil {
		t.Error("未启用时不应登记 debuglog 实例")
	}
}

// TestDebuggerDegradedWhenMySQLUnreachable MySQL 不可达属降级：不 panic、不注册页面、状态可见
func TestDebuggerDegradedWhenMySQLUnreachable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	stop := registerDebugger(r, newDebuggerTestConfig(true, []string{"127.0.0.1"}))
	defer stop()

	status := debuggerHealthInfo()
	if status.State != "degraded" {
		t.Fatalf("MySQL 不可达应记为 degraded，实际 %q（原因 %q）", status.State, status.Reason)
	}
	if !strings.Contains(status.Reason, "MySQL") {
		t.Errorf("降级原因应写明是 MySQL 连接问题，实际 %q", status.Reason)
	}
	for _, route := range r.Routes() {
		if strings.Contains(route.Path, "/jcbase/debug") {
			t.Errorf("降级后不应暴露调试页面路由: %s", route.Path)
		}
	}
	// 降级同样不得登记实例：否则进程记录会向不可达的存储写数据
	if debuglog.Instance() != nil {
		t.Error("MySQL 不可达降级时不应登记 debuglog 实例")
	}
}

// TestDebuggerMaskStateText 三态配置在日志里必须区分「未配置」与「显式 false」
func TestDebuggerMaskStateText(t *testing.T) {
	on, off := true, false
	cases := []struct {
		in   *bool
		want string
	}{
		{nil, "默认开启"},
		{&on, "开启"},
		{&off, "已显式关闭"},
	}
	for _, c := range cases {
		if got := maskStateText(c.in); got != c.want {
			t.Errorf("maskStateText(%v) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestDebuggerZeroValueSemantics 显式 0 必须与「未配置」可区分
// 框架会在读取配置后把零值回填成 default 标签值，故这些字段用指针；
// 换算给组件的「关闭」口径必须是负哨兵，否则 0 会被组件再归一化回默认值
func TestDebuggerZeroValueSemantics(t *testing.T) {
	if got := debuggerRetentionPeriod(nil); got != 7*24*time.Hour {
		t.Errorf("未配置应取默认 7 天，实际 %v", got)
	}
	if got := debuggerRetentionPeriod(intPtr(0)); got >= 0 {
		t.Errorf("显式 0 应换算成非正的保留期（不启动清理），实际 %v", got)
	}
	if got := debuggerRetentionPeriod(intPtr(3)); got != 3*24*time.Hour {
		t.Errorf("显式 3 天应原样生效，实际 %v", got)
	}

	if got := debuggerMaxRecords(nil); got != debuggerDefaultMaxRecords {
		t.Errorf("未配置应取默认 %d 条，实际 %d", debuggerDefaultMaxRecords, got)
	}
	if got := debuggerMaxRecords(intPtr(0)); got != 0 {
		t.Errorf("显式 0 应原样传给存储表示不限制，实际 %d", got)
	}

	if got := debuggerSampleRate(nil); got != 1.0 {
		t.Errorf("未配置应全量采样，实际 %v", got)
	}
	if got := debuggerSampleRate(floatPtr(0)); got >= 0 {
		t.Errorf("显式 0 应换算成负哨兵表示不记录（0 会被组件归一化回全量），实际 %v", got)
	}
	if got := debuggerSampleRate(floatPtr(0.1)); got != 0.1 {
		t.Errorf("显式 0.1 应原样生效，实际 %v", got)
	}
	if got := debuggerSampleRate(floatPtr(2)); got != 1.0 {
		t.Errorf("大于 1 应收敛为全量，实际 %v", got)
	}
}

// TestDebuggerTextRendering 日志文案要把「不清理 / 不记录」与默认值区分开，
// 避免运维看到数字就按数字理解
func TestDebuggerTextRendering(t *testing.T) {
	if got := retentionText(nil); got != "7天(默认)" {
		t.Errorf("未配置保留期文案不符: %q", got)
	}
	if got := retentionText(intPtr(0)); got != "不清理" {
		t.Errorf("显式 0 保留期文案不符: %q", got)
	}
	if got := sampleRateText(floatPtr(0)); got != "已显式关闭(不记录)" {
		t.Errorf("显式 0 采样文案不符: %q", got)
	}
	if got := sampleRateText(nil); got != "全量(默认)" {
		t.Errorf("未配置采样文案不符: %q", got)
	}
	if got := pageText("/jcbase/debug", false); got != "未注册" {
		t.Errorf("页面未注册时文案不符: %q", got)
	}
}
