package bootstrap

import (
	"log"

	"github.com/jcbowen/jcbaseGo/component/debugger"

	"github.com/jcbowen/market/src/backend/common/config"
)

// RegisterProcessDebugger 为 gRPC 业务服务接入 debugger 进程级日志
// 与 gateway 共享同一张 debug_logs 表（ServiceName 取自 cfg.Debugger.ServiceName；
// 一体化 entrypoint 分发配置副本时已按进程名覆写，天然区分来源），HTTP 请求日志
// 与调试页面仍归 gateway，本函数只建实例供进程级记录（调度器摘要等）使用，
// 不注册任何路由。
// 属辅助能力：未启用或 MySQL 不可达时返回 nil 实例，调用方登记到 debuglog 后
// 各记录点静默跳过，不影响服务主链路。
// 过期清理不在此启动：与 gateway 共享同一张表，由 gateway 的清理协程统一负责，
// 三个实例各自清理只会重复劳动（gateway 未启用时的表增长由 retention 配置约束，
// 属已知取舍，见需求文档 07-基础设施/03）。
// 参数：
//   - cfg: 全局配置
//
// 返回值：
//   - *debugger.Debugger: 调试器实例；未启用/不可用/初始化失败时为 nil
//   - func(): 释放函数（关闭本次自建的 MySQL 连接），可安全重复调用
//
// 使用示例：
//
//	dbg, stopDebugger := bootstrap.RegisterProcessDebugger(cfg)
//	defer stopDebugger()
//	debuglog.SetInstance(dbg)
func RegisterProcessDebugger(cfg *config.Config) (*debugger.Debugger, func()) {
	noop := func() {}
	if cfg == nil || !cfg.Debugger.Enabled {
		return nil, noop
	}

	inst, err := InitMySQL(cfg)
	if err != nil {
		log.Printf("debugger 进程日志未启用: 连接 MySQL 失败: %v", err)
		return nil, noop
	}
	sqlDB, err := inst.GetDb().DB()
	if err != nil {
		log.Printf("debugger 进程日志未启用: 获取数据库连接失败: %v", err)
		return nil, noop
	}

	// 记录上限换算与 gateway registerDebugger 保持同口径（同一张表的裁剪行为必须一致）；
	// 该换算在 gateway 侧独立实现（main 包不可跨包复用），改动需同步两边
	maxRecords := debuggerMaxRecordsShared(cfg.Debugger.MaxRecords)
	storage, err := debugger.NewDatabaseStorage(inst.GetDb(), maxRecords, "debug_logs")
	if err != nil {
		_ = sqlDB.Close()
		log.Printf("debugger 进程日志未启用: 初始化存储失败: %v", err)
		return nil, noop
	}

	dbg, err := debugger.New(&debugger.Config{
		Enabled:     true,
		ServiceName: cfg.Debugger.ServiceName,
		MaxRecords:  maxRecords,
		Storage:     storage,
		// 进程级记录（调度器摘要）用 Info 级，组件 Config.Level 默认为 warn
		// （default:"3"），不显式开到 LevelInfo 的话 Info 摘要会被 shouldLog 全部丢弃
		Level: debugger.LevelInfo,
	})
	if err != nil {
		_ = storage.Close()
		_ = sqlDB.Close()
		log.Printf("debugger 进程日志未启用: 创建实例失败: %v", err)
		return nil, noop
	}

	stop := func() {
		_ = storage.Close()
		_ = sqlDB.Close()
	}
	log.Printf("debugger 进程日志已启用: service_name=%s（请求日志与调试页面见 gateway）", cfg.Debugger.ServiceName)
	return dbg, stop
}

// debuggerMaxRecordsShared 记录条数上限换算，与 gateway registerDebugger 的 debuggerMaxRecords 同口径
// 参数：
//   - v: 配置值，nil = 未配置（默认 5000），<0 = 不限制
//
// 返回值：
//   - int: 交给存储的上限，0 表示不裁剪
func debuggerMaxRecordsShared(v *int) int {
	const defaultMaxRecords = 5000
	if v == nil {
		return defaultMaxRecords
	}
	if *v < 0 {
		return 0
	}
	return *v
}
