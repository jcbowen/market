// Package debuglog jcbaseGo debugger 组件（component/debugger）进程级记录的接入辅助。
//
// 组件能力全部直接使用，本包不做任何复刻：
//   - 进程记录创建/结束/日志收集：debugger.StartProcess / ProcessLogger.Info/Warn/Error / EndProcess
//   - 存储与查询：组件的 DatabaseStorage（共享 debug_logs 表）与调试页面
//
// 本包只解决三个接入问题：
//  1. 实例传递：组件实例由各服务 main（gateway registerDebugger / bootstrap.RegisterProcessDebugger）
//     创建，handler 与 service 子包无法直接持有，故经 SetInstance/Instance 共享；
//  2. nil 安全：未启用或 MySQL 不可达时实例为 nil，直接调用组件方法会 panic，
//     Start/StartQuoted 此时返回 nil，调用方判空即可（Go 惯例，无需包装类型）；
//  3. 频率封顶：回调入口按客户端 IP 限流，防伪造请求刷满 debug_logs 全表上限
//     （组件采样率只作用于 HTTP 中间件，进程记录无内置限流，故在调用侧补）。
package debuglog

import (
	"sync"
	"time"

	"github.com/jcbowen/jcbaseGo/component/debugger"
)

// QuotaPerMinute 进程记录频率封顶：同键每分钟最多记录的条数。
// 回调入口（含伪造请求）按客户端 IP 封顶；调度器等低频入口不启用封顶
const QuotaPerMinute = 30

// 进程结束状态取值（组件无常量导出，与组件文档示例一致的字面量）
const (
	// StatusCompleted 进程正常结束
	StatusCompleted = "completed"
	// StatusFailed 进程结束但存在失败（验签失败/受理失败/任务异常等）
	StatusFailed = "failed"
)

// defaultInstance 进程级记录默认调试器实例
// 由各服务 main 在启动时经 SetInstance 登记（nil = 不记录），运行期只读
var (
	defaultMu       sync.RWMutex
	defaultInstance *debugger.Debugger
)

// SetInstance 登记/注销进程级记录默认实例
// 启动成功时传调试器实例；释放（进程退出）或降级时传 nil
// 参数：
//   - dbg: 调试器实例；nil 表示不记录
func SetInstance(dbg *debugger.Debugger) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultInstance = dbg
}

// Instance 读取默认调试器实例
// 返回值：
//   - *debugger.Debugger: 已登记的实例；未登记时为 nil
func Instance() *debugger.Debugger {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultInstance
}

// Start 启动一次进程级调试记录（组件的 StartProcess 直出）
// 参数：
//   - processName: 进程名称，界面过滤用（如「支付宝回调」「支付对账调度」）
//   - processType: 进程类型（如 payment_callback_alipay / payment_reconcile / order_scheduler）
//
// 返回值：
//   - debugger.ProcessLoggerInterface: 组件进程记录器；nil 表示未启用或未登记实例，
//     调用方判空后 return 即可（组件方法在 nil 接口上调用会 panic）
//
// 使用示例：
//
//	rec := debuglog.Start("支付宝回调", "payment_callback_alipay")
//	if rec == nil {
//		return
//	}
//	rec.Info("受理完成", map[string]interface{}{"out_trade_no": no})
//	_ = rec.EndProcess(debuglog.StatusCompleted)
func Start(processName, processType string) debugger.ProcessLoggerInterface {
	dbg := Instance()
	if dbg == nil {
		return nil
	}
	return dbg.StartProcess(processName, processType)
}

// StartQuoted 带频率封顶的 Start：同 quotaKey 每分钟最多 QuotaPerMinute 条
// 用于回调类高频入口（含伪造请求）：防异常流量刷满 debug_logs 全表上限，
// 挤占 HTTP 请求记录。封顶命中返回 nil（调用方仍应打标准日志）
// 参数：
//   - processName: 进程名称
//   - processType: 进程类型
//   - quotaKey: 封顶键（如客户端 IP）；空串不封顶（等同 Start）
//
// 返回值：
//   - debugger.ProcessLoggerInterface: 组件进程记录器；nil 表示未启用或已被封顶
func StartQuoted(processName, processType, quotaKey string) debugger.ProcessLoggerInterface {
	if Instance() == nil {
		return nil
	}
	if quotaKey != "" && !allowQuota(quotaKey) {
		return nil
	}
	return Start(processName, processType)
}

// 频率封顶滑动窗口：key → 最近一分钟内的记录时间
var (
	quotaMu   sync.Mutex
	quotaHits = make(map[string][]time.Time)
)

// allowQuota 滑动窗口判定：key 在最近一分钟内的记录数是否未达上限
// 参数：
//   - key: 封顶键（如客户端 IP）
//
// 返回值：
//   - bool: true 放行（本条记录计入窗口）
//
// 防御：key 数量异常增长（扫描型流量）时整体重置，防止 map 无限膨胀——
// 重置会短暂放过少量记录，属可用性与内存安全的权衡，可接受
func allowQuota(key string) bool {
	now := time.Now()
	cutoff := now.Add(-time.Minute)

	quotaMu.Lock()
	defer quotaMu.Unlock()

	if len(quotaHits) > 1024 {
		quotaHits = make(map[string][]time.Time)
	}
	// 新建而非原地截断：原地复用底层数组的过滤写法依赖「写入位永不超过读取位」
	// 的隐式约束，可读性差；此处量级（每 IP 每分钟 ≤ 上限条）分配成本可忽略
	hits := make([]time.Time, 0, len(quotaHits[key])+1)
	for _, t := range quotaHits[key] {
		if t.After(cutoff) {
			hits = append(hits, t)
		}
	}
	if len(hits) >= QuotaPerMinute {
		quotaHits[key] = hits
		return false
	}
	quotaHits[key] = append(hits, now)
	return true
}
