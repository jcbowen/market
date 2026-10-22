package handler

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	"github.com/jcbowen/jcbaseGo/component/debugger"

	"github.com/jcbowen/market/src/backend/common/debuglog"
	"github.com/jcbowen/market/src/backend/common/proto/cart"
	"github.com/jcbowen/market/src/backend/common/proto/order"
)

/**
 * 下单后购物车兜底清理的 debugger 记录测试（离线，内存存储）
 *
 * 运行方式：
 *   go test ./gateway/handler/ -v -run TestCleanupCartAfterOrder
 *
 * 覆盖记录口径：清理全部成功不落库（每单一条会挤占 debug_logs 全表上限），
 * 仅部分 SKU 删除失败时落一条 failed 记录并带失败 SKU 清单
 */

// stubCartRemoveClient 购物车 gRPC 桩：按 SKU ID 可控返回删除错误
// 嵌入接口后仅覆写 RemoveItem，避免实现全部方法
type stubCartRemoveClient struct {
	cart.CartServiceClient
	removed []int64
	errBy   map[int64]error
}

// RemoveItem 记录被删除的 SKU 并按预设返回错误
func (s *stubCartRemoveClient) RemoveItem(ctx context.Context, in *cart.RemoveItemReq, opts ...grpc.CallOption) (*cart.CartResp, error) {
	s.removed = append(s.removed, in.GetId())
	if err, ok := s.errBy[in.GetId()]; ok {
		return nil, err
	}
	return &cart.CartResp{}, nil
}

// TestCleanupCartAfterOrderRecordPolicy 兜底清理的落库判定
// 参数：
//   - t: 测试对象
func TestCleanupCartAfterOrderRecordPolicy(t *testing.T) {
	newHandler := func(stub *stubCartRemoveClient) *OrderHandler {
		return &OrderHandler{cartClient: stub}
	}
	items := func(skuIDs ...int64) []*order.OrderItem {
		out := make([]*order.OrderItem, 0, len(skuIDs))
		for _, id := range skuIDs {
			out = append(out, &order.OrderItem{SkuId: id})
		}
		return out
	}
	processEntries := func(t *testing.T, dbg *debugger.Debugger) []*debugger.LogEntry {
		t.Helper()
		entries, _, err := dbg.GetStorage().FindAll(1, 50, map[string]interface{}{"record_type": "process"})
		if err != nil {
			t.Fatalf("查询进程记录失败: %v", err)
		}
		return entries
	}

	t.Run("全部成功不落库", func(t *testing.T) {
		dbg := newCallbackTestDebugger(t)
		debuglog.SetInstance(dbg)
		t.Cleanup(func() { debuglog.SetInstance(nil) })

		stub := &stubCartRemoveClient{}
		newHandler(stub).cleanupCartAfterOrder(7, items(101, 102))

		if len(stub.removed) != 2 {
			t.Errorf("两个 SKU 都应发起删除，实际 %v", stub.removed)
		}
		if got := processEntries(t, dbg); len(got) != 0 {
			t.Errorf("清理成功不应落调试记录，实际 %d 条", len(got))
		}
	})

	t.Run("部分失败落一条 failed 并带失败清单", func(t *testing.T) {
		dbg := newCallbackTestDebugger(t)
		debuglog.SetInstance(dbg)
		t.Cleanup(func() { debuglog.SetInstance(nil) })

		stub := &stubCartRemoveClient{errBy: map[int64]error{102: errors.New("cart-service 不可达")}}
		newHandler(stub).cleanupCartAfterOrder(7, items(101, 102))

		entries := processEntries(t, dbg)
		if len(entries) != 1 {
			t.Fatalf("应落 1 条进程记录，实际 %d 条", len(entries))
		}
		entry := entries[0]
		if entry.ProcessType != "cart_cleanup" {
			t.Errorf("进程类型错误: %q", entry.ProcessType)
		}
		if entry.Status != "failed" || !entry.HasError {
			t.Errorf("应为 failed 且带错误徽标，实际 status=%q hasErr=%v", entry.Status, entry.HasError)
		}
		fields := entry.LoggerLogs[0].Fields
		if fields["user_id"] != int64(7) || fields["sku_count"] != 2 {
			t.Errorf("摘要字段错误: %+v", fields)
		}
		skus, ok := fields["failed_skus"].([]int64)
		if !ok || len(skus) != 1 || skus[0] != 102 {
			t.Errorf("失败 SKU 清单错误: %#v", fields["failed_skus"])
		}
	})
}
