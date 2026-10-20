package main

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jcbowen/jcbaseGo/component/debugger"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
)

// debuggerDefaultSkipPaths 默认跳过的路径前缀：健康检查与 SPA/静态资源流量，避免日志噪音
// （debugger 自身还会按扩展名自动跳过静态资源请求）
var debuggerDefaultSkipPaths = []string{"/health", "/s/", "/b/", "/uploads"}

// registerDebugger 按配置接入 jcbaseGo debugger：请求日志写共享 debug_logs 表（MySQL），
// 前端页面挂在 cfg.Debugger.BasePath（自带 AllowedIPs 白名单防护）。
// 属辅助能力：未启用或 MySQL 不可达时仅降级跳过，不影响网关主链路启动
// 参数：
//   - r: gin 引擎（须在 Recovery 之后调用，保证记录完整请求链）
//   - cfg: 全局配置
//
// 异常：不触发 panic；初始化失败仅记录日志并返回
func registerDebugger(r *gin.Engine, cfg *config.Config) {
	if !cfg.Debugger.Enabled {
		return
	}

	inst, err := bootstrap.InitMySQL(cfg)
	if err != nil {
		log.Printf("debugger 未启用: 连接 MySQL 失败: %v", err)
		return
	}

	storage, err := debugger.NewDatabaseStorage(inst.GetDb(), cfg.Debugger.MaxRecords, "debug_logs")
	if err != nil {
		log.Printf("debugger 未启用: 初始化存储失败: %v", err)
		return
	}

	dbg, err := debugger.New(&debugger.Config{
		Enabled:         true,
		ServiceName:     cfg.Debugger.ServiceName,
		AllowedIPs:      cfg.Debugger.AllowedIPs,
		UseCDN:          cfg.Debugger.UseCDN,
		MaxBodySize:     cfg.Debugger.MaxBodySize,
		MaxRecords:      cfg.Debugger.MaxRecords,
		SampleRate:      cfg.Debugger.SampleRate,
		RetentionPeriod: time.Duration(cfg.Debugger.RetentionDays) * 24 * time.Hour,
		SkipPaths:       append(debuggerDefaultSkipPaths, cfg.Debugger.SkipPaths...),
		Storage:         storage,
	})
	if err != nil {
		log.Printf("debugger 未启用: 创建实例失败: %v", err)
		return
	}

	r.Use(dbg.Middleware())
	dbg.WithController(r, &debugger.ControllerConfig{
		BasePath: cfg.Debugger.BasePath,
		UseCDN:   cfg.Debugger.UseCDN,
		Title:    "Gateway 请求调试器",
	})

	startDebuggerCleanup(storage, cfg.Debugger.RetentionDays)
	log.Printf("debugger 已启用: service_name=%s 页面=%s 保留=%d天 上限=%d条",
		cfg.Debugger.ServiceName, cfg.Debugger.BasePath, cfg.Debugger.RetentionDays, cfg.Debugger.MaxRecords)
}

// startDebuggerCleanup 启动日志过期清理：启动时先清一次（覆盖长期停机积压），此后每日执行
// 参数：
//   - storage: debugger 存储器
//   - retentionDays: 日志保留天数
func startDebuggerCleanup(storage debugger.Storage, retentionDays int) {
	retention := time.Duration(retentionDays) * 24 * time.Hour
	go func() {
		clean := func() {
			if err := storage.Cleanup(time.Now().Add(-retention)); err != nil {
				log.Printf("debugger 日志清理失败: %v", err)
			}
		}
		clean()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			clean()
		}
	}()
}
