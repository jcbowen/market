package service

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

/**
 * 秒杀池净额闸门回归测试（直连测试库，env 守卫跳过）
 *
 * 运行方式（TEST_MYSQL_PASSWORD 未设置时自动 skip）：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/product/service/ -run TestFlashReleaseNetGate -v
 *
 * 背景（提交 6e5941a）：秒杀活动池的回补原本只按 biz_no 幂等，
 * 「部分退款回补 + 取消全量回补」用了两个不同 biz_no，同一笔扣减会被回补两次，
 * 活动池因此超发（少卖反成多卖）。修法与主库存 assertReleaseWithinDeduct 同口径：
 * 同一订单同一 SKU 的累计秒杀释放（op_type=4）不得超过累计秒杀扣减（op_type=3）。
 *
 * 覆盖：
 *   - 相等边界放行（释放量 == 扣减量）；
 *   - 跨 biz_no 的第二次释放被 FailedPrecondition 拒绝，且活动池与释放日志随事务整体回滚；
 *   - 同 biz_no 重放幂等（不重复回补，也不被闸门误拒）。
 */

// flashGateOrderSeq 秒杀闸门用例的订单号序列（专用号段，避免与业务订单号重叠）
var flashGateOrderSeq int64

// nextFlashGateOrderID 生成用例内唯一的秒杀测试订单号
// 号段 99100099xxxxxxxx 与业务订单号段隔离，幂等号 cancel:{orderID} 亦随之唯一
func nextFlashGateOrderID() int64 {
	return 99100099000000 + atomic.AddInt64(&flashGateOrderSeq, 1)
}

// seedFlashGateFixture 造一条秒杀商品项与其秒杀扣减日志
// 扣减日志是释放路径的前置（无 op_type=3 记录时 releaseOneFlashSku 视为非秒杀直接成功），
// 这里手工落日志，模拟「下单已扣 N 件」的既有状态
//
// 参数：
//   - t: 测试对象
//   - env: 库存测试环境
//   - poolStock/poolSold: 活动池余量与已售数（扣减后的状态）
//   - deducted: 已扣减数量（净额闸门上限）
//
// 返回值：
//   - int64: 订单 ID
//   - *model.FlashSaleItem: 秒杀商品项（含自增 ID）
func seedFlashGateFixture(t *testing.T, env *stockTestEnv, poolStock, poolSold, deducted int32) (int64, *model.FlashSaleItem) {
	t.Helper()
	ctx := context.Background()
	db := env.db.GetDb().WithContext(ctx)

	skuID := seedTestSku(t, env, 0)
	now := time.Now().Format("2006-01-02 15:04:05")
	item := &model.FlashSaleItem{
		FlashSaleID:  99100099,
		SkuID:        skuID,
		FlashPrice:   1,
		Stock:        poolStock,
		SoldCount:    poolSold,
		LimitPerUser: 1,
		Status:       1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("创建秒杀商品项失败: %v", err)
	}

	orderID := nextFlashGateOrderID()
	deductLog := &model.StockOpLog{
		OrderID:     orderID,
		SkuID:       skuID,
		OpType:      stockOpTypeFlashDeduct,
		BizNo:       fmt.Sprintf("STOCKTEST-flash-deduct-%d", orderID),
		FlashSaleID: item.ID,
		Quantity:    deducted,
		CreatedAt:   now,
	}
	if err := db.Create(deductLog).Error; err != nil {
		t.Fatalf("写入秒杀扣减日志失败: %v", err)
	}
	return orderID, item
}

// readFlashPool 回查活动池余量与已售数
func readFlashPool(t *testing.T, env *stockTestEnv, itemID int64) (int32, int32) {
	t.Helper()
	var stock, sold int32
	if err := env.db.GetDb().Raw(
		"SELECT stock, sold_count FROM flash_sale_items WHERE id = ?", itemID).
		Row().Scan(&stock, &sold); err != nil {
		t.Fatalf("回查活动池失败: %v", err)
	}
	return stock, sold
}

// countFlashReleaseLogs 统计某订单某 SKU 的秒杀释放日志条数
func countFlashReleaseLogs(t *testing.T, env *stockTestEnv, orderID, skuID int64) int64 {
	t.Helper()
	var n int64
	if err := env.db.GetDb().Model(&model.StockOpLog{}).
		Where("order_id = ? AND sku_id = ? AND op_type = ?", orderID, skuID, stockOpTypeFlashRelease).
		Count(&n).Error; err != nil {
		t.Fatalf("统计秒杀释放日志失败: %v", err)
	}
	return n
}

// releaseFlash 调一次批量秒杀回补
func releaseFlash(t *testing.T, env *stockTestEnv, orderID, skuID int64, qty int32, bizNo string) error {
	t.Helper()
	return env.stock.BatchReleaseFlashSaleStock(context.Background(),
		&product.BatchReleaseFlashSaleStockReq{
			OrderId: orderID,
			BizNo:   bizNo,
			Items:   []*product.StockItemQty{{SkuId: skuID, Quantity: qty}},
		})
}

// TestFlashReleaseNetGateAllowsEqualBoundary 释放量等于扣减量时放行（相等边界）
// 扣减 2 件后按退款单号回补 2 件：累计释放 2 == 累计扣减 2，应成功回补原活动池
func TestFlashReleaseNetGateAllowsEqualBoundary(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()
	orderID, item := seedFlashGateFixture(t, env, 8, 2, 2)

	if err := releaseFlash(t, env, orderID, item.SkuID, 2, "REFUNDTEST-flash-eq"); err != nil {
		t.Fatalf("等于扣减量的回补应放行: %v", err)
	}
	stock, sold := readFlashPool(t, env, item.ID)
	if stock != 10 || sold != 0 {
		t.Fatalf("活动池 = stock %d/sold %d, want 10/0", stock, sold)
	}
	if n := countFlashReleaseLogs(t, env, orderID, item.SkuID); n != 1 {
		t.Fatalf("秒杀释放日志 = %d 条, want 1", n)
	}
}

// TestFlashReleaseNetGateRejectsCrossBizNo 跨 biz_no 的重复回补被净额闸门拒绝且整体回滚
// 场景：部分退款已回补 2 件（等于扣减量），取消链路再用 cancel:{orderID} 回补 2 件
func TestFlashReleaseNetGateRejectsCrossBizNo(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()
	orderID, item := seedFlashGateFixture(t, env, 8, 2, 2)

	if err := releaseFlash(t, env, orderID, item.SkuID, 2, "REFUNDTEST-flash-x"); err != nil {
		t.Fatalf("首次回补（等于扣减量）应放行: %v", err)
	}
	stockAfterFirst, soldAfterFirst := readFlashPool(t, env, item.ID)

	err := releaseFlash(t, env, orderID, item.SkuID, 2, fmt.Sprintf("cancel:%d", orderID))
	var be *bizerr.Error
	if !errors.As(err, &be) || be.Code() != codes.FailedPrecondition {
		t.Fatalf("第二次回补应被闸门以 FailedPrecondition 拒绝, got %v", err)
	}

	// 闸门报错须带动事务整体回滚：活动池停在第一次后的值，超发的释放日志不留痕
	// （否则拒绝后仍累加了池，或留下日志把后续合法回补永久堵死）
	stock, sold := readFlashPool(t, env, item.ID)
	if stock != stockAfterFirst || sold != soldAfterFirst {
		t.Fatalf("活动池被越界回补改写: %d/%d, want %d/%d",
			stock, sold, stockAfterFirst, soldAfterFirst)
	}
	if n := countFlashReleaseLogs(t, env, orderID, item.SkuID); n != 1 {
		t.Fatalf("越界回补的释放日志未回滚: %d 条, want 1", n)
	}
}

// TestFlashReleaseReplayIsIdempotent 同 biz_no 重放幂等：不重复回补也不被闸门误拒
// 调度器兜底重放与前端重试都走同一幂等号，重放必须静默成功且池余量不变
func TestFlashReleaseReplayIsIdempotent(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()
	orderID, item := seedFlashGateFixture(t, env, 8, 2, 2)

	bizNo := "REFUNDTEST-flash-replay"
	if err := releaseFlash(t, env, orderID, item.SkuID, 2, bizNo); err != nil {
		t.Fatalf("首次回补失败: %v", err)
	}
	stockFirst, soldFirst := readFlashPool(t, env, item.ID)

	if err := releaseFlash(t, env, orderID, item.SkuID, 2, bizNo); err != nil {
		t.Fatalf("同幂等号重放应静默成功: %v", err)
	}
	stock, sold := readFlashPool(t, env, item.ID)
	if stock != stockFirst || sold != soldFirst {
		t.Fatalf("重放重复回补活动池: %d/%d, want %d/%d", stock, sold, stockFirst, soldFirst)
	}
	if n := countFlashReleaseLogs(t, env, orderID, item.SkuID); n != 1 {
		t.Fatalf("重放不应新增释放日志: %d 条, want 1", n)
	}
}

// TestFlashReleaseSkipsNonFlashSku 无秒杀扣减记录的 SKU 回补直接成功
// 反向护栏：闸门不得把普通商品订单的取消回补一起拦下
func TestFlashReleaseSkipsNonFlashSku(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()
	skuID := seedTestSku(t, env, 5)
	orderID := nextFlashGateOrderID()

	if err := releaseFlash(t, env, orderID, skuID, 5, fmt.Sprintf("cancel:%d", orderID)); err != nil {
		t.Fatalf("非秒杀 SKU 回补应直接成功: %v", err)
	}
	if n := countFlashReleaseLogs(t, env, orderID, skuID); n != 0 {
		t.Fatalf("非秒杀 SKU 不应落释放日志: %d 条, want 0", n)
	}
}
