package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jcbowen/jcbaseGo/component/debugger"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/debuglog"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
)

// debuggerDefaultSkipPaths 默认跳过的路径前缀：
//   - 健康检查与 SPA/静态资源流量：量大且无排查价值（组件还会按扩展名自动跳过静态资源请求）
//   - 认证与支付回调：请求体含密码、验证码、token，响应体含签发的令牌与渠道签名参数，
//     整条不入库，作为凭据掩码之外的第二道防线（C 端 /api/v1/auth 与管理端 /api/v1/admin/auth 都要覆盖）
//   - 上传路由：multipart 二进制正文即使只记元数据也没有排查价值，且会挤占记录上限
var debuggerDefaultSkipPaths = []string{
	"/health", "/s/", "/b/", "/uploads",
	"/api/v1/auth",
	"/api/v1/admin/auth",
	"/api/v1/payments/callback",
	"/api/v1/upload/image",
	"/api/v1/admin/upload/image",
}

// debuggerStatus debugger 实际运行状态
// 三处降级（未启用 / MySQL 不可达 / 初始化失败）原先只各打一行日志就永久生效，
// 事后无从判断线上到底有没有在记日志，故记入进程内状态并由 /health 暴露
type debuggerStatus struct {
	State  string `json:"state"`            // disabled / degraded / on
	Reason string `json:"reason,omitempty"` // 降级原因（仅 degraded 时有值）
	// BasePath 调试页面路径，仅在页面确实注册时给出：
	// 空白名单（fail-closed）时页面不可达，此时只给 Note，避免运维照着 base_path 去访问却 404
	BasePath string `json:"base_path,omitempty"`
	Note     string `json:"note,omitempty"` // 启用但页面未注册等需要额外说明的口径
}

// debuggerStatusMu 保护 debuggerStatusValue（/health 与注册流程并发读写）
var (
	debuggerStatusMu    sync.RWMutex
	debuggerStatusValue = debuggerStatus{State: "disabled"}
)

// debugger 侧「未配置」时的取值，与配置模板注释保持一致
const (
	debuggerDefaultRetentionDays  = 7
	debuggerDefaultMaxRecords     = 5000
	debuggerDefaultSampleRate     = 1.0
	debuggerDisabledSampleRate    = -1.0 // 交给组件的「一条都不记」哨兵：0 会被组件默认值归一化回全量
	debuggerDisabledRetentionDays = -1   // 同上，负值表示不做过期清理
)

// setDebuggerStatus 记录 debugger 运行状态
// 参数：
//   - state: disabled / degraded / on
//   - reason: 降级原因，state 为 on 时传空串
//   - basePath: 调试页面路径，仅在页面已注册时传入
//   - note: 需要额外说明的口径（如「仅记录，页面未注册」），无则传空串
func setDebuggerStatus(state, reason, basePath, note string) {
	debuggerStatusMu.Lock()
	defer debuggerStatusMu.Unlock()
	debuggerStatusValue = debuggerStatus{State: state, Reason: reason, BasePath: basePath, Note: note}
}

// debuggerHealthInfo 读取 debugger 运行状态，供 /health 响应附带
// 返回值：
//   - debuggerStatus: 状态快照（值拷贝，避免调用方改到共享状态）
func debuggerHealthInfo() debuggerStatus {
	debuggerStatusMu.RLock()
	defer debuggerStatusMu.RUnlock()
	return debuggerStatusValue
}

// debuggerRetentionPeriod 保留天数换算为组件的保留期
// 参数：
//   - days: 配置值，nil = 未配置（按默认 7 天），<=0 = 显式关闭过期清理
//
// 返回值：
//   - time.Duration: 组件保留期；关闭清理时返回负值而非 0，
//     因为组件 New 会把零值归一化回默认 168h，只有负值能表达「不清理」
//
// 使用示例：
//
//	period := debuggerRetentionPeriod(cfg.Debugger.RetentionDays)
func debuggerRetentionPeriod(days *int) time.Duration {
	if days == nil {
		return debuggerDefaultRetentionDays * 24 * time.Hour
	}
	if *days <= 0 {
		return debuggerDisabledRetentionDays * 24 * time.Hour
	}
	return time.Duration(*days) * 24 * time.Hour
}

// debuggerMaxRecords 记录条数上限换算，nil 走默认值，负数按「不限制」处理
// 参数：
//   - v: 配置值
//
// 返回值：
//   - int: 交给存储的上限，0 表示不裁剪
func debuggerMaxRecords(v *int) int {
	if v == nil {
		return debuggerDefaultMaxRecords
	}
	if *v < 0 {
		return 0
	}
	return *v
}

// debuggerSampleRate 采样率换算：nil = 全量，<=0 = 一条都不记，>1 收敛为全量
// 参数：
//   - v: 配置值
//
// 返回值：
//   - float64: 交给组件的采样率（负值为「关闭记录」哨兵）
func debuggerSampleRate(v *float64) float64 {
	if v == nil {
		return debuggerDefaultSampleRate
	}
	if *v <= 0 {
		return debuggerDisabledSampleRate
	}
	if *v >= 1 {
		return debuggerDefaultSampleRate
	}
	return *v
}

// sampleRateText 采样率的可读文本，避免日志里把「显式关闭」打印成 -1.00 让人误解
// 参数：
//   - v: 配置值（nil 表示未配置）
func sampleRateText(v *float64) string {
	if v == nil {
		return "全量(默认)"
	}
	if *v <= 0 {
		return "已显式关闭(不记录)"
	}
	return fmt.Sprintf("%.2f", *v)
}

// retentionText 保留期可读文本：显式 0 要说清是「不清理」而不是「不限」
// 参数：
//   - v: 配置值（nil 表示未配置）
func retentionText(v *int) string {
	if v == nil {
		return fmt.Sprintf("%d天(默认)", debuggerDefaultRetentionDays)
	}
	if *v <= 0 {
		return "不清理"
	}
	return fmt.Sprintf("%d天", *v)
}

// registerDebugger 按配置接入 jcbaseGo debugger：请求日志写共享 debug_logs 表（MySQL），
// 前端页面挂在 cfg.Debugger.BasePath（按 AllowedIPs 白名单防护，鉴权走 gin 的 ClientIP）。
// 属辅助能力：未启用或 MySQL 不可达时仅降级跳过，不影响网关主链路启动。
// 空白名单时组件不注册页面路由（fail-closed），请求日志照常记录。
// 同时把实例登记到 debuglog 默认实例：支付回调摘要、下单后异步任务等进程级记录
// 与请求日志共用该实例（共享同一张表，ServiceName 均为 gateway）。
// 参数：
//   - r: gin 引擎（须在 Recovery 之后调用，保证记录完整请求链）
//   - cfg: 全局配置
//
// 返回值：
//   - func(): 释放函数，停止清理协程、注销 debuglog 默认实例并关闭本次自建的 MySQL 连接；可安全重复调用
//
// 异常：不触发 panic；初始化失败仅记录日志并返回空的释放函数
// 使用示例：
//
//	stopDebugger := registerDebugger(r, cfg)
//	defer stopDebugger()
func registerDebugger(r *gin.Engine, cfg *config.Config) (stop func()) {
	// 入口先注销，防止重复注册（测试或多引擎场景）时旧实例残留
	debuglog.SetInstance(nil)
	if !cfg.Debugger.Enabled {
		setDebuggerStatus("disabled", "", "", "")
		return func() {}
	}

	release := func() {}
	stop = func() { release() }

	inst, err := bootstrap.InitMySQL(cfg)
	if err != nil {
		reason := "连接 MySQL 失败: " + err.Error()
		setDebuggerStatus("degraded", reason, "", "")
		log.Printf("debugger 未启用: %s，请求日志不记录", reason)
		return stop
	}

	// 本实例只服务于请求日志，随释放函数关闭，避免退出时留连接悬空
	sqlDB, err := inst.GetDb().DB()
	if err != nil {
		reason := "获取数据库连接失败: " + err.Error()
		setDebuggerStatus("degraded", reason, "", "")
		log.Printf("debugger 未启用: %s，请求日志不记录", reason)
		return stop
	}

	maxRecords := debuggerMaxRecords(cfg.Debugger.MaxRecords)
	storage, err := debugger.NewDatabaseStorage(inst.GetDb(), maxRecords, "debug_logs")
	if err != nil {
		_ = sqlDB.Close()
		reason := "初始化存储失败: " + err.Error()
		setDebuggerStatus("degraded", reason, "", "")
		log.Printf("debugger 未启用: %s，请求日志不记录", reason)
		return stop
	}

	retention := debuggerRetentionPeriod(cfg.Debugger.RetentionDays)
	dbg, err := debugger.New(&debugger.Config{
		Enabled:                true,
		ServiceName:            cfg.Debugger.ServiceName,
		AllowedIPs:             cfg.Debugger.AllowedIPs,
		AllowEmptyWhitelist:    cfg.Debugger.AllowEmptyWhitelist,
		UseCDN:                 cfg.Debugger.UseCDN,
		MaxBodySize:            cfg.Debugger.MaxBodySize,
		MaxRecords:             maxRecords,
		SampleRate:             debuggerSampleRate(cfg.Debugger.SampleRate),
		RetentionPeriod:        retention,
		CleanupInterval:        time.Duration(cfg.Debugger.CleanupIntervalHours) * time.Hour,
		SkipPaths:              append(debuggerDefaultSkipPaths, cfg.Debugger.SkipPaths...),
		Storage:                storage,
		BodyMaskEnabled:        cfg.Debugger.MaskBody,
		HeaderMaskKeys:         cfg.Debugger.HeaderMaskKeys,
		BodyMaskKeys:           cfg.Debugger.BodyMaskKeys,
		MultipartSkipFiles:     cfg.Debugger.MultipartSkipFiles,
		MultipartPreserveState: cfg.Debugger.MultipartPreserveState,
		// 进程级记录（回调摘要/异步任务）用 Info 级，组件 Config.Level 默认为 warn
		// （default:"3"），不显式开到 LevelInfo 的话 Info 摘要会被 shouldLog 全部丢弃
		Level: debugger.LevelInfo,
	})
	if err != nil {
		_ = storage.Close()
		_ = sqlDB.Close()
		reason := "创建实例失败: " + err.Error()
		setDebuggerStatus("degraded", reason, "", "")
		log.Printf("debugger 未启用: %s，请求日志不记录", reason)
		return stop
	}

	r.Use(dbg.Middleware())
	dbg.WithController(r, &debugger.ControllerConfig{
		BasePath: cfg.Debugger.BasePath,
		UseCDN:   cfg.Debugger.UseCDN,
		Title:    "Gateway 请求调试器",
	})
	// 进程级记录（回调摘要/异步任务）与请求日志共用本实例，此后 debuglog.Start 才可能返回非 nil
	debuglog.SetInstance(dbg)

	// 过期清理由组件协程承担：启动先清一次，之后按 cleanup_interval_hours 周期执行。
	// retention_days 显式 0（保留期换算为负）时不调用本方法，绝不等价于清空全表
	stopCleanup := func() {}
	if retention > 0 {
		stopCleanup = dbg.StartPeriodicCleanup()
	}
	release = func() {
		stopCleanup()
		debuglog.SetInstance(nil)
		_ = storage.Close()
		_ = sqlDB.Close()
	}

	// 白名单为空且未显式放开时组件不注册页面路由（fail-closed），此时 /health 不报 base_path，
	// 免得运维照着路径去访问只得到「资源不存在」
	pageRegistered := len(cfg.Debugger.AllowedIPs) > 0 || cfg.Debugger.AllowEmptyWhitelist
	basePath := ""
	note := ""
	if pageRegistered {
		basePath = cfg.Debugger.BasePath
	} else {
		note = "仅记录，调试页面未注册（allowed_ips 为空且 allow_empty_whitelist=false）"
		log.Printf("debugger 已启用（仅记录）: 白名单为空，调试页面 %s 未注册，需要看页面请配 allowed_ips 或 DEBUGGER_ALLOWED_IPS",
			cfg.Debugger.BasePath)
	}
	setDebuggerStatus("on", "", basePath, note)

	maxRecordsText := fmt.Sprintf("%d条", maxRecords)
	if maxRecords == 0 {
		maxRecordsText = "不限制"
	}
	log.Printf("debugger 已启用: service_name=%s 页面=%s 保留=%s 清理间隔=%dh 上限=%s 采样=%s 掩码=%s",
		cfg.Debugger.ServiceName, pageText(cfg.Debugger.BasePath, pageRegistered), retentionText(cfg.Debugger.RetentionDays),
		cfg.Debugger.CleanupIntervalHours, maxRecordsText, sampleRateText(cfg.Debugger.SampleRate),
		maskStateText(cfg.Debugger.MaskBody))
	return stop
}

// pageText 渲染日志里的页面口径，未注册时明确写「未注册」而不是留空
// 参数：
//   - basePath: 配置的页面路径
//   - registered: 页面路由是否已注册
func pageText(basePath string, registered bool) string {
	if !registered {
		return "未注册"
	}
	return basePath
}

// maskStateText 把 *bool 三态渲染成日志可读文本，避免 nil 被误读成 false
// 参数：
//   - v: 配置值，nil 表示未配置（走组件默认开启）
func maskStateText(v *bool) string {
	if v == nil {
		return "默认开启"
	}
	if *v {
		return "开启"
	}
	return "已显式关闭"
}
