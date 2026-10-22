package debuglog

import (
	"sync"
	"testing"

	"github.com/jcbowen/jcbaseGo/component/debugger"
)

/**
 * debuglog 接入辅助测试（离线，不连真实 MySQL）
 *
 * 运行方式：
 *   go test ./common/debuglog/ -v
 *
 * 覆盖：
 *   - 未登记实例时 Start/StartQuoted 返回 nil（调用方判空的前提）
 *   - SetInstance/Instance 登记与注销
 *   - StartQuoted 同键每分钟封顶 QuotaPerMinute 条，超限返回 nil，换键恢复
 *   - 完整生命周期：Error + EndProcess 后组件存储中记录状态与严重度正确
 *     （注意组件 Config.Level 默认为 warn，Info 会被丢弃，故实例化必须显式 LevelInfo）
 */

// newTestDebugger 构造内存存储的调试器实例（不连 MySQL）
// Level 必须显式给 LevelInfo：组件默认 warn 会丢弃 Info 级摘要
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *debugger.Debugger: 可直接交给 SetInstance 的实例
func newTestDebugger(t *testing.T) *debugger.Debugger {
	t.Helper()
	storage, err := debugger.NewMemoryStorage(100)
	if err != nil {
		t.Fatalf("创建内存存储失败: %v", err)
	}
	dbg, err := debugger.New(&debugger.Config{
		Enabled:     true,
		ServiceName: "debuglog-test",
		MaxRecords:  100,
		Level:       debugger.LevelInfo,
		Storage:     storage,
	})
	if err != nil {
		t.Fatalf("创建调试器实例失败: %v", err)
	}
	return dbg
}

// TestStartNilSafe 未登记实例时返回 nil
func TestStartNilSafe(t *testing.T) {
	SetInstance(nil)
	t.Cleanup(func() { SetInstance(nil) })

	if rec := Start("任务", "test"); rec != nil {
		t.Error("未登记实例时 Start 应返回 nil")
	}
	if rec := StartQuoted("任务", "test", "1.2.3.4"); rec != nil {
		t.Error("未登记实例时 StartQuoted 应返回 nil")
	}
}

// TestSetInstanceLifecycle 登记后可用，注销后静默
func TestSetInstanceLifecycle(t *testing.T) {
	dbg := newTestDebugger(t)
	SetInstance(dbg)
	if Instance() != dbg {
		t.Fatal("Instance 应返回已登记的实例")
	}
	rec := Start("登记后任务", "test")
	if rec == nil {
		t.Fatal("登记实例后 Start 不应返回 nil")
	}
	rec.Info("记一条")
	_ = rec.EndProcess(StatusCompleted)

	SetInstance(nil)
	if Instance() != nil {
		t.Fatal("注销后 Instance 应为 nil")
	}
	if rec := Start("注销后任务", "test"); rec != nil {
		t.Error("注销后 Start 应返回 nil")
	}
}

// TestQuotaPerMinuteCap 同键封顶、超限返回 nil、换键恢复
// 注意：包内其他测试也会写 quotaHits，故本测试只用独占 key，避免相互干扰
func TestQuotaPerMinuteCap(t *testing.T) {
	dbg := newTestDebugger(t)
	SetInstance(dbg)
	t.Cleanup(func() { SetInstance(nil) })

	const key = "10.9.9.9"
	allowed := 0
	for i := 0; i < QuotaPerMinute+5; i++ {
		if StartQuoted("回调", "payment_callback_alipay", key) != nil {
			allowed++
		}
	}
	if allowed != QuotaPerMinute {
		t.Errorf("同键应放行 %d 条，实际 %d", QuotaPerMinute, allowed)
	}
	// 换键立即恢复（滑动窗口按 key 隔离）
	if StartQuoted("回调", "payment_callback_alipay", "10.9.9.10") == nil {
		t.Error("换键后应恢复放行")
	}
	// 空 quotaKey 不封顶
	for i := 0; i < QuotaPerMinute+5; i++ {
		if StartQuoted("任务", "test", "") == nil {
			t.Fatalf("空 quotaKey 第 %d 次被误封顶", i)
		}
	}
}

// TestRecordLifecycle 完整生命周期：错误记录带 HasError，Info 在 LevelInfo 下落库
func TestRecordLifecycle(t *testing.T) {
	dbg := newTestDebugger(t)
	SetInstance(dbg)
	t.Cleanup(func() { SetInstance(nil) })

	failed := Start("支付宝回调", "payment_callback_alipay")
	failed.Error("受理失败", map[string]interface{}{"out_trade_no": "PAY001", "result": "failure"})
	_ = failed.EndProcess(StatusFailed)

	ok := Start("支付宝回调", "payment_callback_alipay")
	ok.Info("受理完成", map[string]interface{}{"out_trade_no": "PAY002", "result": "success"})
	_ = ok.EndProcess(StatusCompleted)

	for _, tc := range []struct {
		name    string
		rec     debugger.ProcessLoggerInterface
		status  string
		hasErr  bool
		wantMsg string
	}{
		{"失败记录", failed, StatusFailed, true, "受理失败"},
		{"成功记录", ok, StatusCompleted, false, "受理完成"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry, err := dbg.GetStorage().FindByID(tc.rec.GetProcessID())
			if err != nil {
				t.Fatalf("查询进程记录失败: %v", err)
			}
			if entry.RecordType != "process" {
				t.Errorf("记录类型应为 process，实际 %q", entry.RecordType)
			}
			if entry.ProcessName != "支付宝回调" || entry.ProcessType != "payment_callback_alipay" {
				t.Errorf("进程标识错误: name=%q type=%q", entry.ProcessName, entry.ProcessType)
			}
			if entry.Status != tc.status {
				t.Errorf("状态应为 %q，实际 %q", tc.status, entry.Status)
			}
			if entry.HasError != tc.hasErr {
				t.Errorf("HasError 应为 %v，实际 %v", tc.hasErr, entry.HasError)
			}
			if len(entry.LoggerLogs) != 1 || entry.LoggerLogs[0].Message != tc.wantMsg {
				t.Errorf("日志内容错误: %+v", entry.LoggerLogs)
			}
			if entry.ServiceName != "debuglog-test" {
				t.Errorf("服务标识应透传，实际 %q", entry.ServiceName)
			}
		})
	}
}

// TestQuotaConcurrentSafe 并发封顶判定不竞态（-race 下验证）
func TestQuotaConcurrentSafe(t *testing.T) {
	dbg := newTestDebugger(t)
	SetInstance(dbg)
	t.Cleanup(func() { SetInstance(nil) })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			StartQuoted("回调", "payment_callback_alipay", "10.9.9.11")
		}()
	}
	wg.Wait()
}
