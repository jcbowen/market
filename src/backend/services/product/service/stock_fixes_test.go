package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

/**
 * 2026-10-30 审查修复回归测试（直连测试库，env 守卫跳过）
 *
 * 运行方式（TEST_MYSQL_PASSWORD 未设置时自动 skip）：
 *   TEST_MYSQL_HOST=xxx TEST_MYSQL_USER=xxx TEST_MYSQL_PASSWORD=xxx \
 *     go test ./services/product/service/ -run TestFix -v
 *
 * 覆盖：
 *   - TestFixSalesDeductSkipsExpiredBatch    过期批次不参与销售扣减（P1-1 口径：仅销售拦截）
 *   - TestFixLossOutBillConsumesExpiredBatch 报损出库保留消耗过期批次能力（FEFO 原语义）
 *   - TestFixStockBillIdempotentReplay       出入库单 biz_no 重放幂等（P2-1）
 *   - TestFixMovingAverageWeightedCost       多批不同价入库的移动加权平均成本（P2 测试空白）
 *   - TestFixPartialRefundDistinctBizNo      多笔部分退款按 biz_no 各释放一次且不超扣减量
 *   - TestFixStockCheckZeroAndEmpty          盘点实盘 0 合法、空明细服务层拒绝（P2-3）
 *
 * 测试数据沿用 99100099 社区号段，结束即清理；断言含三方对账（汇总↔批次守恒）。
 */

// fixTestEnv 测试环境句柄（与 stockTestEnv 同构，迁移表清单覆盖单据/盘点）
type fixTestEnv struct {
	db      *mysql.Instance
	stock   *StockService
	bill    *StockBillService
	check   *StockCheckService
	cleanup func()
}

// newFixTestEnv 建测试库连接并迁移本轮测试所需表
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *fixTestEnv: 被测服务与清理句柄；TEST_MYSQL_PASSWORD 未设置时 skip
func newFixTestEnv(t *testing.T) *fixTestEnv {
	t.Helper()
	if getFixEnvOr("TEST_MYSQL_PASSWORD", "") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过审查修复回归测试")
	}
	dbInst, err := mysql.New(jcbaseGo.DbStruct{
		Host:      getFixEnvOr("TEST_MYSQL_HOST", "127.0.0.1"),
		Port:      getFixEnvOr("TEST_MYSQL_PORT", "3306"),
		Dbname:    getFixEnvOr("TEST_MYSQL_DBNAME", "market"),
		Username:  getFixEnvOr("TEST_MYSQL_USER", "root"),
		Password:  getFixEnvOr("TEST_MYSQL_PASSWORD", ""),
		Charset:   "utf8mb4",
		ParseTime: "True",
		Loc:       "Local",
	})
	if err != nil {
		t.Fatalf("连接测试 MySQL 失败: %v", err)
	}
	if err := dbInst.GetDb().AutoMigrate(
		&model.Sku{}, &model.StockBatch{}, &model.StockOpLog{}, &model.StockRecord{},
		&model.Product{}, // syncProductAggregates 回写 products 汇总列需要该表
		&model.StockInOrder{}, &model.StockInOrderItem{},
		&model.StockOutOrder{}, &model.StockOutOrderItem{},
		&model.StockCheck{}, &model.StockCheckItem{},
	); err != nil {
		t.Fatalf("测试库迁移失败: %v", err)
	}
		env := &fixTestEnv{
		db:    dbInst,
		stock: &StockService{db: dbInst, expireWarningDays: 3},
		bill:  &StockBillService{db: dbInst},
		check: &StockCheckService{db: dbInst},
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
		_ = db.Where("community_id = ?", stockTestCommunityID).Delete(&model.Product{})
		_ = db.Where("community_id = ?", stockTestCommunityID).Delete(&model.StockInOrder{})
		_ = db.Where("community_id = ?", stockTestCommunityID).Delete(&model.StockOutOrder{})
		_ = db.Where("community_id = ?", stockTestCommunityID).Delete(&model.StockCheck{})
	}
	return env
}

// getFixEnvOr 读取环境变量，未设置时返回默认值
func getFixEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// seedFixSku 建一个测试 SKU（无批次，批次由用例自行造）
//
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - stock: 初始汇总库存
//   - cost: 成本价
//
// 返回值：
//   - int64: SKU ID
func seedFixSku(t *testing.T, env *fixTestEnv, stock int32, cost float64) int64 {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	sku := &model.Sku{
		ProductID:   99100099,
		CommunityID: stockTestCommunityID,
		SkuCode:     fmt.Sprintf("STOCKTEST-%d-fix", time.Now().UnixNano()),
		SpecJSON:    "{}",
		Price:       1,
		CostPrice:   cost,
		Stock:       stock,
		Status:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.db.GetDb().Create(sku).Error; err != nil {
		t.Fatalf("创建测试 SKU 失败: %v", err)
	}
	return sku.ID
}

// seedFixBatch 给测试 SKU 补一个批次
//
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - skuID: SKU ID
//   - quantity: 批次余量
//   - unitCost: 批次单位成本
//   - expire: 过期日期（nil=无有效期）
//
// 返回值：
//   - int64: 批次 ID
func seedFixBatch(t *testing.T, env *fixTestEnv, skuID int64, quantity int32, unitCost float64, expire *string) int64 {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	batch := &model.StockBatch{
		CommunityID: stockTestCommunityID,
		SkuID:       skuID,
		BatchNo:     fmt.Sprintf("B-%d", time.Now().UnixNano()),
		ExpireDate:  expire,
		Quantity:    quantity,
		UnitCost:    unitCost,
		Status:      1,
		SourceType:  1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.db.GetDb().Create(batch).Error; err != nil {
		t.Fatalf("创建测试批次失败: %v", err)
	}
	return batch.ID
}

// fixSkuOf 回查 SKU 模型（独立变量，避免 GORM 主键残留 WHERE 的问题，见 stockOf 注释）
//
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - skuID: SKU ID
//
// 返回值：
//   - model.Sku: SKU 模型
func fixSkuOf(t *testing.T, env *fixTestEnv, skuID int64) model.Sku {
	t.Helper()
	var sku model.Sku
	if err := env.db.GetDb().First(&sku, skuID).Error; err != nil {
		t.Fatalf("回查 SKU %d 失败: %v", skuID, err)
	}
	return sku
}

// fixBatchQtyOf 回查指定批次余量
//
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - batchID: 批次 ID
//
// 返回值：
//   - int32: 批次余量
func fixBatchQtyOf(t *testing.T, env *fixTestEnv, batchID int64) int32 {
	t.Helper()
	var batch model.StockBatch
	if err := env.db.GetDb().First(&batch, batchID).Error; err != nil {
		t.Fatalf("回查批次 %d 失败: %v", batchID, err)
	}
	return batch.Quantity
}

// assertFixConsistency 三方对账：SKU 汇总库存必须等于在库批次合计（双向守恒）
//
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - skuID: SKU ID
func assertFixConsistency(t *testing.T, env *fixTestEnv, skuID int64) {
	t.Helper()
	sku := fixSkuOf(t, env, skuID)
	var batchTotal int32
	if err := env.db.GetDb().Model(&model.StockBatch{}).
		Where("sku_id = ? AND quantity > 0", skuID).
		Select("IFNULL(SUM(quantity),0)").Scan(&batchTotal).Error; err != nil {
		t.Fatalf("汇总批次余量失败: %v", err)
	}
	if batchTotal != sku.Stock {
		t.Fatalf("三方对账失败：product_skus.stock=%d，Σ批次=%d", sku.Stock, batchTotal)
	}
}

// dateStr 以当天为基准偏移 n 天的 ISO 日期
//
// 参数：
//   - n: 偏移天数（负数=过去）
//
// 返回值：
//   - string: "2006-01-02" 格式日期
func dateStr(n int) string {
	return time.Now().AddDate(0, 0, n).Format("2006-01-02")
}

// TestFixSalesDeductSkipsExpiredBatch 销售扣减必须跳过过期批次：
// 过期批次 + 有效批次并存时，扣减只消耗有效批次，过期批次余量原封不动
func TestFixSalesDeductSkipsExpiredBatch(t *testing.T) {
	env := newFixTestEnv(t)
	defer env.cleanup()

	skuID := seedFixSku(t, env, 10, 0)
	expiredID := seedFixBatch(t, env, skuID, 5, 0, strPtr(dateStr(-1))) // 已过期
	validID := seedFixBatch(t, env, skuID, 5, 0, strPtr(dateStr(30)))   // 有效

	ok, _, err := env.stock.DeductStock(context.Background(), &product.DeductStockReq{
		OrderId: 930001, SkuId: skuID, Quantity: 3,
	})
	if err != nil || !ok {
		t.Fatalf("销售扣减失败: ok=%v err=%v", ok, err)
	}

	if got := fixBatchQtyOf(t, env, expiredID); got != 5 {
		t.Errorf("过期批次余量 = %d，期望 5（过期批次不得参与销售扣减）", got)
	}
	if got := fixBatchQtyOf(t, env, validID); got != 2 {
		t.Errorf("有效批次余量 = %d，期望 2（扣减应全部落在有效批次）", got)
	}
	if got := fixSkuOf(t, env, skuID).Stock; got != 7 {
		t.Errorf("汇总库存 = %d，期望 7", got)
	}
	assertFixConsistency(t, env, skuID)

	// 消费完有效批次后，过期批次不可被销售路径兜底：报「无可用（未过期）批次」而非继续扣过期
	_, _, err = env.stock.DeductStock(context.Background(), &product.DeductStockReq{
		OrderId: 930002, SkuId: skuID, Quantity: 3,
	})
	if err == nil {
		t.Fatal("有效批次耗尽后销售扣减竟然成功（过期批次被销售）")
	}
	if got := fixBatchQtyOf(t, env, expiredID); got != 5 {
		t.Errorf("失败扣减后过期批次余量 = %d，期望 5", got)
	}
}

// TestFixLossOutBillConsumesExpiredBatch 报损出库保留消耗过期批次能力：
// FEFO 原语义下过期批次最先被报损消耗（这正是报损路径的收益）
func TestFixLossOutBillConsumesExpiredBatch(t *testing.T) {
	env := newFixTestEnv(t)
	defer env.cleanup()

	skuID := seedFixSku(t, env, 10, 0)
	expiredID := seedFixBatch(t, env, skuID, 5, 0, strPtr(dateStr(-1)))
	validID := seedFixBatch(t, env, skuID, 5, 0, strPtr(dateStr(30)))

	_, _, err := env.bill.CreateStockOutOrder(context.Background(), &product.CreateStockOutOrderReq{
		CommunityId: stockTestCommunityID,
		Type:        model.StockOutTypeLoss,
		OperatorId:  0,
		Items:       []*product.StockOutOrderItemReq{{SkuId: skuID, Quantity: 3}},
		BizNo:       "fix-loss-1",
	})
	if err != nil {
		t.Fatalf("报损出库失败: %v", err)
	}

	if got := fixBatchQtyOf(t, env, expiredID); got != 2 {
		t.Errorf("过期批次余量 = %d，期望 2（报损应优先消耗过期批次）", got)
	}
	if got := fixBatchQtyOf(t, env, validID); got != 5 {
		t.Errorf("有效批次余量 = %d，期望 5", got)
	}
	assertFixConsistency(t, env, skuID)
}

// TestFixStockBillIdempotentReplay 出入库单 biz_no 重放幂等：
// 同 biz_no 二次提交返回原单，库存只变动一次；采购单回写也只生效一次
func TestFixStockBillIdempotentReplay(t *testing.T) {
	env := newFixTestEnv(t)
	defer env.cleanup()

	skuID := seedFixSku(t, env, 0, 0)

	order1, _, err := env.bill.CreateStockInOrder(context.Background(), &product.CreateStockInOrderReq{
		CommunityId: stockTestCommunityID,
		Items:       []*product.StockInOrderItemReq{{SkuId: skuID, Quantity: 5}},
		BizNo:       "fix-in-uuid-1",
	})
	if err != nil {
		t.Fatalf("首次入库失败: %v", err)
	}
	if got := fixSkuOf(t, env, skuID).Stock; got != 5 {
		t.Fatalf("首次入库后库存 = %d，期望 5", got)
	}

	// 同 biz_no 重放：必须返回原单且不重复加库存
	order2, _, err := env.bill.CreateStockInOrder(context.Background(), &product.CreateStockInOrderReq{
		CommunityId: stockTestCommunityID,
		Items:       []*product.StockInOrderItemReq{{SkuId: skuID, Quantity: 5}},
		BizNo:       "fix-in-uuid-1",
	})
	if err != nil {
		t.Fatalf("重放入库失败: %v", err)
	}
	if order2.ID != order1.ID {
		t.Errorf("重放返回单据 ID = %d，期望 %d（应返回原单）", order2.ID, order1.ID)
	}
	if got := fixSkuOf(t, env, skuID).Stock; got != 5 {
		t.Errorf("重放入库后库存 = %d，期望 5（重复入库未被幂等拦截）", got)
	}

	// 出库单同样幂等
	out1, _, err := env.bill.CreateStockOutOrder(context.Background(), &product.CreateStockOutOrderReq{
		CommunityId: stockTestCommunityID,
		Type:        model.StockOutTypeAdjust,
		Items:       []*product.StockOutOrderItemReq{{SkuId: skuID, Quantity: 2}},
		BizNo:       "fix-out-uuid-1",
	})
	if err != nil {
		t.Fatalf("首次出库失败: %v", err)
	}
	out2, _, err := env.bill.CreateStockOutOrder(context.Background(), &product.CreateStockOutOrderReq{
		CommunityId: stockTestCommunityID,
		Type:        model.StockOutTypeAdjust,
		Items:       []*product.StockOutOrderItemReq{{SkuId: skuID, Quantity: 2}},
		BizNo:       "fix-out-uuid-1",
	})
	if err != nil {
		t.Fatalf("重放出库失败: %v", err)
	}
	if out2.ID != out1.ID {
		t.Errorf("重放出库返回单据 ID = %d，期望 %d", out2.ID, out1.ID)
	}
	if got := fixSkuOf(t, env, skuID).Stock; got != 3 {
		t.Errorf("重放出库后库存 = %d，期望 3（重复出库未被幂等拦截）", got)
	}
	assertFixConsistency(t, env, skuID)
}

// TestFixMovingAverageWeightedCost 多批不同价入库的移动加权平均成本：
// 10 件 @2.00 与 10 件 @4.00 入库后成本应为 (10*2+10*4)/20 = 3.00（加权分支，历史上只测过单批）
func TestFixMovingAverageWeightedCost(t *testing.T) {
	env := newFixTestEnv(t)
	defer env.cleanup()

	skuID := seedFixSku(t, env, 0, 0)
	if _, _, err := env.bill.CreateStockInOrder(context.Background(), &product.CreateStockInOrderReq{
		CommunityId: stockTestCommunityID,
		Items:       []*product.StockInOrderItemReq{{SkuId: skuID, Quantity: 10, UnitCost: 2}},
		BizNo:       "fix-cost-1",
	}); err != nil {
		t.Fatalf("第一批入库失败: %v", err)
	}
	if got := fixSkuOf(t, env, skuID).CostPrice; got != 2 {
		t.Fatalf("第一批入库后成本 = %v，期望 2", got)
	}
	if _, _, err := env.bill.CreateStockInOrder(context.Background(), &product.CreateStockInOrderReq{
		CommunityId: stockTestCommunityID,
		Items:       []*product.StockInOrderItemReq{{SkuId: skuID, Quantity: 10, UnitCost: 4}},
		BizNo:       "fix-cost-2",
	}); err != nil {
		t.Fatalf("第二批入库失败: %v", err)
	}
	if got := fixSkuOf(t, env, skuID).CostPrice; got != 3 {
		t.Errorf("加权后成本 = %v，期望 3（(10*2+10*4)/20）", got)
	}
	assertFixConsistency(t, env, skuID)
}

// TestFixPartialRefundDistinctBizNo 同一订单同 SKU 两笔不同 biz_no 的部分退款
// 各释放一次且互不吞并；累计释放超过扣减量时被净上限校验拒绝
func TestFixPartialRefundDistinctBizNo(t *testing.T) {
	env := newFixTestEnv(t)
	defer env.cleanup()

	skuID := seedFixSku(t, env, 10, 0)
	seedFixBatch(t, env, skuID, 10, 0, nil)
	const orderID = 940001
	if _, _, err := env.stock.DeductStock(context.Background(), &product.DeductStockReq{
		OrderId: orderID, SkuId: skuID, Quantity: 10,
	}); err != nil {
		t.Fatalf("下单扣减失败: %v", err)
	}

	// 第一笔部分退款 3 件
	if err := env.stock.ReleaseStock(context.Background(), &product.ReleaseStockReq{
		OrderId: orderID, SkuId: skuID, Quantity: 3, BizNo: "refund:1",
	}); err != nil {
		t.Fatalf("第一笔退款释放失败: %v", err)
	}
	if got := fixSkuOf(t, env, skuID).Stock; got != 3 {
		t.Fatalf("第一笔退款后库存 = %d，期望 3", got)
	}

	// 第二笔部分退款 3 件（不同 biz_no，不得被幂等吞掉）
	if err := env.stock.ReleaseStock(context.Background(), &product.ReleaseStockReq{
		OrderId: orderID, SkuId: skuID, Quantity: 3, BizNo: "refund:2",
	}); err != nil {
		t.Fatalf("第二笔退款释放失败（被幂等误吞）: %v", err)
	}
	if got := fixSkuOf(t, env, skuID).Stock; got != 6 {
		t.Fatalf("第二笔退款后库存 = %d，期望 6", got)
	}

	// 同 biz_no 重放：只生效一次
	if err := env.stock.ReleaseStock(context.Background(), &product.ReleaseStockReq{
		OrderId: orderID, SkuId: skuID, Quantity: 3, BizNo: "refund:2",
	}); err != nil {
		t.Fatalf("同 biz_no 重放不应报错: %v", err)
	}
	if got := fixSkuOf(t, env, skuID).Stock; got != 6 {
		t.Errorf("重放后库存 = %d，期望 6（重复释放未拦截）", got)
	}

	// 累计释放 11 > 扣减 10：净上限拒绝
	if err := env.stock.ReleaseStock(context.Background(), &product.ReleaseStockReq{
		OrderId: orderID, SkuId: skuID, Quantity: 5, BizNo: "refund:3",
	}); err == nil {
		t.Fatal("累计释放超过扣减量未被拒绝（超量回补）")
	}
	if got := fixSkuOf(t, env, skuID).Stock; got != 6 {
		t.Errorf("被拒绝的释放后库存 = %d，期望 6", got)
	}
	assertFixConsistency(t, env, skuID)
}

// TestFixStockCheckZeroAndEmpty 盘点实盘 0 是合法值（整批盘亏）；空明细服务层直接拒绝
func TestFixStockCheckZeroAndEmpty(t *testing.T) {
	env := newFixTestEnv(t)
	defer env.cleanup()

	// 实盘 0：全部盘亏，批次清零、汇总归零
	skuID := seedFixSku(t, env, 5, 0)
	seedFixBatch(t, env, skuID, 5, 0, strPtr(dateStr(30)))

	check, err := env.check.CreateStockCheck(context.Background(), &product.CreateStockCheckReq{
		CommunityId: stockTestCommunityID,
	})
	if err != nil {
		t.Fatalf("创建盘点单失败: %v", err)
	}
	if err := env.check.SubmitStockCheck(context.Background(), &product.SubmitStockCheckReq{
		Id:    check.ID,
		Items: []*product.StockCheckItem{{SkuId: skuID, ActualStock: 0}},
	}); err != nil {
		t.Fatalf("实盘 0 提交失败（0 是合法值，不得当成未填）: %v", err)
	}
	if got := fixSkuOf(t, env, skuID).Stock; got != 0 {
		t.Errorf("实盘 0 提交后库存 = %d，期望 0", got)
	}
	assertFixConsistency(t, env, skuID)

	// 空明细：服务层拒绝（网关有校验，内部 gRPC 直连此前可造出已提交空单）
	check2, err := env.check.CreateStockCheck(context.Background(), &product.CreateStockCheckReq{
		CommunityId: stockTestCommunityID,
	})
	if err != nil {
		t.Fatalf("创建盘点单失败: %v", err)
	}
	if err := env.check.SubmitStockCheck(context.Background(), &product.SubmitStockCheckReq{
		Id: check2.ID,
	}); err == nil {
		t.Fatal("空明细盘点提交未被服务层拒绝")
	}
}
