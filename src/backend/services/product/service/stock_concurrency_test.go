package service

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

/**
 * 库存与券的资金安全并发回归测试（直连测试库，env 守卫跳过）
 *
 * 运行方式（CI 已挂载 MySQL service 容器；本地需先起 docker 栈）：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/product/service/ -run TestConcurrent -v
 *
 * 覆盖（对应三道资金防线中的两道的产品侧）：
 *   - DeductStock        并发扣减不超卖：成功次数恒等于初始库存，汇总列与批次余量归零且不为负
 *   - DeductStock        幂等：同一订单同一 SKU 重复扣减只生效一次
 *   - BatchDeductStock   整单并发不超卖 + 半态回滚、整批重放幂等、同 SKU 多明细聚合
 *   - UseCoupon          并发核销只生效一次，模板券 used_count 不丢失更新
 *
 * 测试数据统一使用 99100099 社区 ID 与 STOCKTEST-/COUPONTEST- 前缀，结束即清理。
 */

// stockTestCommunityID 测试专用社区 ID（与业务社区号段隔离，作为清理依据）
const stockTestCommunityID int64 = 99100099

// stockTestEnv 测试环境句柄
type stockTestEnv struct {
	db      *mysql.Instance
	stock   *StockService
	market  *MarketingService
	cleanup func()
}

// newStockTestEnv 建测试库连接、迁移测试所需表，并返回清理函数
// 表结构统一走 AutoMigrate（与 main.go 同一模型清单），CI 空库与本地存量库均幂等
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *stockTestEnv: 被测服务与清理句柄；TEST_MYSQL_PASSWORD 未设置时 skip
func newStockTestEnv(t *testing.T) *stockTestEnv {
	t.Helper()
	if os.Getenv("TEST_MYSQL_PASSWORD") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过库存/券并发集成测试")
	}
	dbInst, err := mysql.New(jcbaseGo.DbStruct{
		Host:      stockTestEnvOr("TEST_MYSQL_HOST", "127.0.0.1"),
		Port:      stockTestEnvOr("TEST_MYSQL_PORT", "3306"),
		Dbname:    stockTestEnvOr("TEST_MYSQL_DBNAME", "market"),
		Username:  stockTestEnvOr("TEST_MYSQL_USER", "root"),
		Password:  os.Getenv("TEST_MYSQL_PASSWORD"),
		Charset:   "utf8mb4",
		ParseTime: "True",
		Loc:       "Local",
	})
	if err != nil {
		t.Fatalf("连接测试 MySQL 失败: %v", err)
	}
	if err := dbInst.GetDb().AutoMigrate(
		&model.Sku{}, &model.StockBatch{}, &model.StockOpLog{}, &model.StockRecord{},
		&model.Coupon{}, &model.UserCoupon{},
	); err != nil {
		t.Fatalf("测试库迁移失败: %v", err)
	}

	env := &stockTestEnv{
		db:     dbInst,
		stock:  &StockService{db: dbInst, expireWarningDays: 3},
		market: &MarketingService{db: dbInst},
	}
	env.cleanup = func() {
		db := dbInst.GetDb().WithContext(context.Background())
		var skuIDs []int64
		_ = db.Model(&model.Sku{}).Where("community_id = ?", stockTestCommunityID).Pluck("id", &skuIDs)
		if len(skuIDs) > 0 {
			_ = db.Where("sku_id IN ?", skuIDs).Delete(&model.StockBatch{})
			_ = db.Where("sku_id IN ?", skuIDs).Delete(&model.StockRecord{})
			_ = db.Where("sku_id IN ?", skuIDs).Delete(&model.StockOpLog{})
		}
		_ = db.Where("community_id = ?", stockTestCommunityID).Delete(&model.Sku{})
		_ = db.Where("name LIKE ?", "COUPONTEST-%").Delete(&model.UserCoupon{})
		// 测试券名快照留空时上面的 LIKE 匹配不到，按测试专用 user_id 兜底删净，防止跨轮堆积
		_ = db.Where("user_id = ?", stockTestCommunityID).Delete(&model.UserCoupon{})
		_ = db.Where("name LIKE ?", "COUPONTEST-%").Delete(&model.Coupon{})
	}
	return env
}

// stockTestEnvOr 读取环境变量，未设置时返回默认值
func stockTestEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// stockTestSkuSeq SKU 编码序列号：同一纳秒内连续造数也不撞 sku_code+community 唯一键
var stockTestSkuSeq int64

// seedTestSku 建一个测试 SKU 及其唯一批次（汇总列与批次余量一致）
//
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - stock: 初始库存
//
// 返回值：
//   - int64: SKU ID
func seedTestSku(t *testing.T, env *stockTestEnv, stock int32) int64 {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	sku := &model.Sku{
		ProductID:   99100099,
		CommunityID: stockTestCommunityID,
		SkuCode:     fmt.Sprintf("STOCKTEST-%d-%d", time.Now().UnixNano(), atomic.AddInt64(&stockTestSkuSeq, 1)),
		SpecJSON:    "{}", // JSON 列不接受空串，与 CreateSku 的归一化口径一致
		Price:       1,
		Stock:       stock,
		Status:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.db.GetDb().Create(sku).Error; err != nil {
		t.Fatalf("创建测试 SKU 失败: %v", err)
	}
	batch := &model.StockBatch{
		CommunityID: stockTestCommunityID,
		SkuID:       sku.ID,
		Quantity:    stock,
		Status:      1,
		SourceType:  1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.db.GetDb().Create(batch).Error; err != nil {
		t.Fatalf("创建测试批次失败: %v", err)
	}
	return sku.ID
}

// stockOf 回查 SKU 汇总库存
// 独立函数而非复用同一个 Sku 变量：GORM 的 First(&sku, id) 会把结构体已加载的主键
// 也拼进 WHERE，第二次回查同一变量必然查成 `id = 上一条 AND id = 本条`，报 record not found。
//
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - skuID: SKU ID
//
// 返回值：
//   - int32: 当前库存
func stockOf(t *testing.T, env *stockTestEnv, skuID int64) int32 {
	t.Helper()
	var sku model.Sku
	if err := env.db.GetDb().First(&sku, skuID).Error; err != nil {
		t.Fatalf("回查 SKU %d 失败: %v", skuID, err)
	}
	return sku.Stock
}

// TestConcurrentDeductStockNoOversell 并发扣减不得超卖
// 初始库存 N，发起 3N 个各扣 1 的并发请求：成功数恒为 N，汇总列与批次余量归零不为负
func TestConcurrentDeductStockNoOversell(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()

	const initial = 10
	skuID := seedTestSku(t, env, initial)

	var success int32
	var wg sync.WaitGroup
	for i := 0; i < initial*3; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			ok, _, err := env.stock.DeductStock(context.Background(), &product.DeductStockReq{
				OrderId:  900000 + int64(seq),
				SkuId:    skuID,
				Quantity: 1,
			})
			if err == nil && ok {
				atomic.AddInt32(&success, 1)
			}
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&success); got != initial {
		t.Errorf("并发扣减成功数 = %d，期望 %d（超卖或漏卖）", got, initial)
	}

	var sku model.Sku
	if err := env.db.GetDb().First(&sku, skuID).Error; err != nil {
		t.Fatalf("回查 SKU 失败: %v", err)
	}
	if sku.Stock != 0 {
		t.Errorf("汇总库存 = %d，期望 0", sku.Stock)
	}

	var batchRemain int32
	if err := env.db.GetDb().Model(&model.StockBatch{}).
		Where("sku_id = ?", skuID).Select("IFNULL(SUM(quantity),0)").Scan(&batchRemain).Error; err != nil {
		t.Fatalf("汇总批次余量失败: %v", err)
	}
	if batchRemain != 0 {
		t.Errorf("批次余量合计 = %d，期望 0（汇总列与批次不一致）", batchRemain)
	}

	var negative int64
	if err := env.db.GetDb().Model(&model.StockBatch{}).
		Where("sku_id = ? AND quantity < 0", skuID).Count(&negative).Error; err != nil {
		t.Fatalf("统计负余量批次失败: %v", err)
	}
	if negative > 0 {
		t.Errorf("存在 %d 条负余量批次", negative)
	}
}

// TestConcurrentDeductStockIdempotent 同一订单同一 SKU 并发重复扣减只生效一次
// 下单重试与补偿链路都以 order_id+sku_id+op_type 为幂等键，重复生效即重复扣库存
func TestConcurrentDeductStockIdempotent(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()

	skuID := seedTestSku(t, env, 10)
	const orderID = 910001

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = env.stock.DeductStock(context.Background(), &product.DeductStockReq{
				OrderId:  orderID,
				SkuId:    skuID,
				Quantity: 1,
			})
		}()
	}
	wg.Wait()

	var sku model.Sku
	if err := env.db.GetDb().First(&sku, skuID).Error; err != nil {
		t.Fatalf("回查 SKU 失败: %v", err)
	}
	if sku.Stock != 9 {
		t.Errorf("重复扣减后库存 = %d，期望 9（幂等键未生效）", sku.Stock)
	}
	var logs int64
	if err := env.db.GetDb().Model(&model.StockOpLog{}).
		Where("order_id = ? AND sku_id = ?", orderID, skuID).Count(&logs).Error; err != nil {
		t.Fatalf("统计幂等日志失败: %v", err)
	}
	if logs != 1 {
		t.Errorf("幂等日志 = %d 条，期望 1 条", logs)
	}
}

// TestConcurrentBatchDeductNoOversell 整单批量扣减并发不超卖且不出现半态
// 每个请求同时扣 SKU-A 2 件、SKU-B 1 件：A 库存 6、B 库存 3，理论上最多 3 单成功。
// 断言两点：成功数恰为 3；A/B 的实际扣减量必须严格等于 2×成功数与 1×成功数，
// 以此验证整单走同一事务——任一 SKU 库存不足时另一 SKU 的扣减必须回滚。
func TestConcurrentBatchDeductNoOversell(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()

	skuA := seedTestSku(t, env, 6)
	skuB := seedTestSku(t, env, 3)

	var success int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			ok, err := env.stock.BatchDeductStock(context.Background(), &product.BatchDeductStockReq{
				OrderId: 930000 + int64(seq),
				Items: []*product.StockItemQty{
					{SkuId: skuA, Quantity: 2},
					{SkuId: skuB, Quantity: 1},
				},
			})
			if err == nil && ok {
				atomic.AddInt32(&success, 1)
			}
		}(i)
	}
	wg.Wait()

	const wantSuccess = 3
	if got := atomic.LoadInt32(&success); got != wantSuccess {
		t.Errorf("批量扣减成功数 = %d，期望 %d（超卖或漏卖）", got, wantSuccess)
	}

	for _, tc := range []struct {
		name     string
		skuID    int64
		remain   int32
		deducted int32
	}{
		{"SKU-A", skuA, 0, wantSuccess * 2},
		{"SKU-B", skuB, 0, wantSuccess * 1},
	} {
		if got := stockOf(t, env, tc.skuID); got != tc.remain {
			t.Errorf("%s 汇总库存 = %d，期望 %d", tc.name, got, tc.remain)
		}
		var batchRemain int32
		if err := env.db.GetDb().Model(&model.StockBatch{}).
			Where("sku_id = ?", tc.skuID).Select("IFNULL(SUM(quantity),0)").Scan(&batchRemain).Error; err != nil {
			t.Fatalf("汇总 %s 批次余量失败: %v", tc.name, err)
		}
		if batchRemain != tc.remain {
			t.Errorf("%s 批次余量合计 = %d，期望 %d（汇总列与批次不一致）", tc.name, batchRemain, tc.remain)
		}
		var deducted int32
		if err := env.db.GetDb().Model(&model.StockOpLog{}).
			Where("sku_id = ? AND op_type = ?", tc.skuID, 1).
			Select("IFNULL(SUM(quantity),0)").Scan(&deducted).Error; err != nil {
			t.Fatalf("汇总 %s 扣减流水失败: %v", tc.name, err)
		}
		if deducted != tc.deducted {
			t.Errorf("%s 扣减流水合计 = %d，期望 %d（存在半态扣减）", tc.name, deducted, tc.deducted)
		}
	}

	var negative int64
	if err := env.db.GetDb().Model(&model.StockBatch{}).
		Where("sku_id IN ? AND quantity < 0", []int64{skuA, skuB}).Count(&negative).Error; err != nil {
		t.Fatalf("统计负余量批次失败: %v", err)
	}
	if negative > 0 {
		t.Errorf("存在 %d 条负余量批次", negative)
	}
}

// TestConcurrentBatchDeductIdempotent 同一订单整批重复扣减只生效一次
// 下单重试、补偿回滚后重发都会带同一 order_id 再次调用，幂等键为 order_id+sku_id+op_type；
// 重复请求应短路返回成功而非二次扣减，否则库存被吞。
func TestConcurrentBatchDeductIdempotent(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()

	skuA := seedTestSku(t, env, 10)
	skuB := seedTestSku(t, env, 10)
	const orderID = 940001

	items := []*product.StockItemQty{
		{SkuId: skuA, Quantity: 2},
		{SkuId: skuB, Quantity: 3},
	}

	var wg sync.WaitGroup
	var success int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := env.stock.BatchDeductStock(context.Background(), &product.BatchDeductStockReq{
				OrderId: orderID,
				Items:   items,
			}); err == nil && ok {
				atomic.AddInt32(&success, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&success); got < 1 {
		t.Errorf("并发重复扣减全部失败，成功数 = %d", got)
	}

	if got := stockOf(t, env, skuA); got != 8 {
		t.Errorf("SKU-A 库存 = %d，期望 8（幂等键未生效）", got)
	}
	if got := stockOf(t, env, skuB); got != 7 {
		t.Errorf("SKU-B 库存 = %d，期望 7（幂等键未生效）", got)
	}

	var logs int64
	if err := env.db.GetDb().Model(&model.StockOpLog{}).
		Where("order_id = ?", orderID).Count(&logs).Error; err != nil {
		t.Fatalf("统计幂等日志失败: %v", err)
	}
	if logs != 2 {
		t.Errorf("幂等日志 = %d 条，期望 2 条（每 SKU 一条）", logs)
	}

	// 并发后再串行重放一次：应短路返回成功且不改变库存
	ok, err := env.stock.BatchDeductStock(context.Background(), &product.BatchDeductStockReq{
		OrderId: orderID,
		Items:   items,
	})
	if err != nil {
		t.Fatalf("重放批量扣减失败: %v", err)
	}
	if !ok {
		t.Errorf("重放批量扣减返回 success = false，期望 true（幂等应视为成功）")
	}
	if got := stockOf(t, env, skuA); got != 8 {
		t.Errorf("重放后 SKU-A 库存 = %d，期望 8（重放被重复扣减）", got)
	}
}

// TestBatchDeductAggregatesSameSku 同一 SKU 多明细必须聚合后扣减
// 购物车同类商品可能拆成多行；若逐行各写一条幂等日志，唯一键会让后一行被 DoNothing 吞掉，
// 造成「扣一次记两行订单量」的少扣超卖，normalizeStockItems 的聚合即为此存在。
func TestBatchDeductAggregatesSameSku(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()

	skuID := seedTestSku(t, env, 10)
	const orderID = 950001

	ok, err := env.stock.BatchDeductStock(context.Background(), &product.BatchDeductStockReq{
		OrderId: orderID,
		Items: []*product.StockItemQty{
			{SkuId: skuID, Quantity: 2},
			{SkuId: skuID, Quantity: 3},
		},
	})
	if err != nil {
		t.Fatalf("聚合扣减失败: %v", err)
	}
	if !ok {
		t.Errorf("聚合扣减返回 success = false，期望 true")
	}

	if got := stockOf(t, env, skuID); got != 5 {
		t.Errorf("库存 = %d，期望 5（同 SKU 多行未聚合导致少扣）", got)
	}

	var logs []*model.StockOpLog
	if err := env.db.GetDb().Where("order_id = ? AND sku_id = ?", orderID, skuID).Find(&logs).Error; err != nil {
		t.Fatalf("查询幂等日志失败: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("幂等日志 = %d 条，期望 1 条", len(logs))
	}
	if logs[0].Quantity != 5 {
		t.Errorf("日志扣减数量 = %d，期望 5（聚合后总量写错）", logs[0].Quantity)
	}

	var batchRemain int32
	if err := env.db.GetDb().Model(&model.StockBatch{}).
		Where("sku_id = ?", skuID).Select("IFNULL(SUM(quantity),0)").Scan(&batchRemain).Error; err != nil {
		t.Fatalf("汇总批次余量失败: %v", err)
	}
	if batchRemain != 5 {
		t.Errorf("批次余量合计 = %d，期望 5（汇总列与批次不一致）", batchRemain)
	}
}

// TestBatchDeductPartialConflictAborts 同一订单部分 SKU 已扣过时整批报错且不静默少扣
// 补偿/重试链路可能先用单条 DeductStock 扣了 A，再用 BatchDeductStock 提交 A+B：
// 批量日志的 RowsAffected 只到 1（B）而非 2，若当成成功会漏扣 A、当成重放又会漏扣 B，
// 因此 insertOpLogsIdempotent 对「部分冲突」直接报错，让上层重试整单。
func TestBatchDeductPartialConflictAborts(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()

	skuA := seedTestSku(t, env, 10)
	skuB := seedTestSku(t, env, 10)
	const orderID = 960001

	if _, _, err := env.stock.DeductStock(context.Background(), &product.DeductStockReq{
		OrderId: orderID, SkuId: skuA, Quantity: 2,
	}); err != nil {
		t.Fatalf("单条扣减 SKU-A 失败: %v", err)
	}

	ok, err := env.stock.BatchDeductStock(context.Background(), &product.BatchDeductStockReq{
		OrderId: orderID,
		Items: []*product.StockItemQty{
			{SkuId: skuA, Quantity: 2},
			{SkuId: skuB, Quantity: 3},
		},
	})
	if err == nil {
		t.Errorf("部分冲突批量扣减返回成功，期望报错（ok=%v）", ok)
	}
	if ok {
		t.Errorf("部分冲突批量扣减 success = true，期望 false")
	}

	if got := stockOf(t, env, skuA); got != 8 {
		t.Errorf("SKU-A 库存 = %d，期望 8（只应被首次单条扣减 2 件）", got)
	}
	if got := stockOf(t, env, skuB); got != 10 {
		t.Errorf("SKU-B 库存 = %d，期望 10（整批必须回滚，不得只扣一半）", got)
	}
}

// TestConcurrentUseCouponSingleRedeem 同一张用户券并发核销只生效一次
// 条件更新（status=1 才置 2）保证后到者失败，模板券 used_count 用 SQL 表达式原子自增
func TestConcurrentUseCouponSingleRedeem(t *testing.T) {
	env := newStockTestEnv(t)
	defer env.cleanup()

	now := time.Now().Format("2006-01-02 15:04:05")
	coupon := &model.Coupon{
		CommunityID: stockTestCommunityID,
		Name:        fmt.Sprintf("COUPONTEST-%d", time.Now().UnixNano()),
		Type:        1,
		TotalCount:  100,
		Status:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.db.GetDb().Create(coupon).Error; err != nil {
		t.Fatalf("创建测试券失败: %v", err)
	}
	uc := &model.UserCoupon{
		UserID:    99100099,
		CouponID:  coupon.ID,
		Status:    1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := env.db.GetDb().Create(uc).Error; err != nil {
		t.Fatalf("创建测试用户券失败: %v", err)
	}

	var wg sync.WaitGroup
	var success int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			if err := env.market.UseCoupon(context.Background(), &product.UseCouponReq{
				UserCouponId: uc.ID,
				OrderId:      920000 + int64(seq),
			}); err == nil {
				atomic.AddInt32(&success, 1)
			}
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&success); got != 1 {
		t.Errorf("并发核销成功数 = %d，期望 1", got)
	}

	var c model.Coupon
	if err := env.db.GetDb().First(&c, coupon.ID).Error; err != nil {
		t.Fatalf("回查模板券失败: %v", err)
	}
	if c.UsedCount != 1 {
		t.Errorf("used_count = %d，期望 1（并发丢失更新）", c.UsedCount)
	}
}
