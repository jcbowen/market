package service

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/pageparam"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// 幂等日志操作类型（stock_op_logs.op_type）
const (
	stockOpTypeDeduct       int8 = 1 // 主库存扣减（下单）
	stockOpTypeRelease      int8 = 2 // 主库存释放（取消/退款/超时）
	stockOpTypeFlashDeduct  int8 = 3 // 秒杀库存扣减
	stockOpTypeFlashRelease int8 = 4 // 秒杀库存释放
)

// StockService 库存业务服务
type StockService struct {
	db                *mysql.Instance
	expireWarningDays int
}

// NewStockService 创建库存业务服务
// 参数：
//   - db: MySQL 实例
//   - expireWarningDays: 临期预警天数，<=0 时默认 3 天
//
// 返回值：
//   - *StockService: 库存业务服务实例
func NewStockService(db *mysql.Instance, expireWarningDays int) *StockService {
	if expireWarningDays <= 0 {
		expireWarningDays = 3
	}
	return &StockService{db: db, expireWarningDays: expireWarningDays}
}

// batchTake 单批次扣减结果（供流水逐批次落账）
type batchTake struct {
	Batch model.StockBatch
	Take  int32
}

// normalizeStockItems 明细项按 SKU 聚合数量并给出升序 ID 列表
// 同一订单同一 SKU 的多行必须合并为一次扣减：幂等唯一键是 order_id+sku_id+op_type+biz_no，
// 若不合并，第二行会被唯一索引判为重复请求而静默跳过，造成少扣库存（超卖口径）
//
// 参数：
//   - items: 原始明细（sku_id + quantity）
//
// 返回值：
//   - []int64: 去重后按 SKU ID 升序的列表（加锁顺序，避免多请求交叉死锁）
//   - map[int64]int32: SKU ID -> 需扣减的合计数量
//   - error: 数量非法错误
func normalizeStockItems(items []*product.StockItemQty) ([]int64, map[int64]int32, error) {
	qtyBySku := make(map[int64]int32, len(items))
	for _, it := range items {
		if it == nil || it.SkuId <= 0 {
			return nil, nil, invalidArg("库存操作明细缺少 SKU")
		}
		if it.Quantity <= 0 {
			return nil, nil, invalidArg("SKU %d 的扣减数量必须大于 0", it.SkuId)
		}
		qtyBySku[it.SkuId] += it.Quantity
	}
	ids := make([]int64, 0, len(qtyBySku))
	for id := range qtyBySku {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, qtyBySku, nil
}

// insertOpLogsIdempotent 批量插入幂等日志（唯一索引冲突即视为重复请求，不落新行）
//
// 参数：
//   - tx: 事务
//   - logs: 待写入的幂等日志（同一批内 order_id+sku_id+op_type+biz_no 互不相同）
//
// 返回值：
//   - bool: 整批是否都是重复请求（全部冲突，调用方据此短路返回成功）
//   - error: 写入失败或部分冲突错误（部分冲突说明该订单已被其他链路扣过同一 SKU，
//     无法判定应扣集合，宁可失败也不静默少扣）
func insertOpLogsIdempotent(tx *gorm.DB, logs []*model.StockOpLog) (bool, error) {
	if len(logs) == 0 {
		return false, nil
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&logs)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return true, nil
	}
	if res.RowsAffected != int64(len(logs)) {
		return false, invalidArg("库存操作幂等冲突：该订单部分 SKU 已存在同类操作记录")
	}
	return false, nil
}

// syncAggregatesForSkus 回写本事务内改动过的 SKU 所属商品的汇总列
// 库存交易只更新 product_skus.stock，必须同事务把 products 的汇总列（stock / 价格区间）一并带上，
// 否则后台商品列表的「已售罄 / 库存紧张」Tab 与状态统计（直接读 products.stock）长期失真
//
// 参数：
//   - tx: GORM 事务对象
//   - skus: lockSkus 返回的 SKU 映射（key 为 sku_id）
//
// 返回值：
//   - error: 回写失败
func syncAggregatesForSkus(tx *gorm.DB, skus map[int64]*model.Sku) error {
	ids := make([]int64, 0, len(skus))
	seen := make(map[int64]bool, len(skus))
	for _, sku := range skus {
		if sku == nil || seen[sku.ProductID] {
			continue
		}
		seen[sku.ProductID] = true
		ids = append(ids, sku.ProductID)
	}
	return syncProductAggregates(tx, ids)
}

// deductSkuLocked 在已加锁事务内扣减单个 SKU：FEFO 批次扣减、汇总列回落、逐批次落流水
// 调用前必须已对该 SKU 加行锁（lockClause 或 lockSkus），sku.Stock 会被就地扣减
//
// 参数：
//   - tx: 事务
//   - sku: 已加锁的 SKU
//   - quantity: 扣减数量
//   - orderID: 关联订单 ID（写流水）
//   - now: 统一时间串
//
// 返回值：
//   - error: 库存不足或写入失败
func deductSkuLocked(tx *gorm.DB, sku *model.Sku, quantity int32, orderID int64, now string) error {
	if sku.Stock < quantity {
		return bizerr.New(codes.ResourceExhausted, fmt.Sprintf("商品 %s 库存不足", sku.SkuCode))
	}
	before := sku.Stock
	takes, err := deductBatches(tx, sku, quantity, now)
	if err != nil {
		return err
	}
	// 汇总库存同步落库：deductBatches 仅扣减内存中的 sku.Stock，
	// 必须显式 UPDATE product_skus.stock，否则汇总列永远不降
	//（库存校验形同虚设导致超卖，且低库存预警永不触发）
	if err := tx.Model(&model.Sku{}).Where("id = ?", sku.ID).
		Update("stock", sku.Stock).Error; err != nil {
		return err
	}
	// 逐批次落流水账
	running := before
	for _, t := range takes {
		running -= t.Take
		record := &model.StockRecord{
			SkuID:          sku.ID,
			CommunityID:    sku.CommunityID,
			Type:           2,
			Quantity:       -t.Take,
			BeforeStock:    running + t.Take,
			AfterStock:     running,
			BatchID:        t.Batch.ID,
			BatchNo:        t.Batch.BatchNo,
			ProductionDate: t.Batch.ProductionDate,
			ExpireDate:     t.Batch.ExpireDate,
			RelatedOrderID: orderID,
			CreatedAt:      now,
		}
		if err := tx.Create(record).Error; err != nil {
			return err
		}
	}
	return nil
}

// DeductStock 扣减库存（幂等，FEFO 批次扣减）
// 以 order_id + sku_id + 操作类型 唯一约束实现幂等：同一订单对同一 SKU 的重复
// 扣减请求（如 gRPC 超时重试）直接返回当前库存，不重复扣减。
// 扣减顺序：有效期批次按过期日期升序先出（FEFO），无有效期批次按批次创建顺序后出。
// product_skus.stock 为汇总列，在同一事务内与批次同步维护。
//
// 参数：
//   - ctx: 上下文
//   - req: 扣减请求
//
// 返回值：
//   - bool: 是否成功
//   - int32: 剩余库存
//   - error: 错误信息
//
// 使用示例：
//
//	ok, remain, err := stockService.DeductStock(ctx, &product.DeductStockReq{
//	    SkuId: 1, Quantity: 2, OrderId: 100,
//	})
func (s *StockService) DeductStock(ctx context.Context, req *product.DeductStockReq) (bool, int32, error) {
	// 与批量入口 normalizeStockItems 同口径：负数会绕过 `sku.Stock < quantity` 判定，
	// 反向加库存并污染 stock_records 流水，必须在入口拒绝
	if req.Quantity <= 0 {
		return false, 0, invalidArg("扣减数量必须大于 0")
	}
	var success bool
	var remain int32
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 幂等日志先行插入：重复请求在唯一索引上冲突（RowsAffected=0）时短路返回
		now := time.Now().Format("2006-01-02 15:04:05")
		replayed, err := insertOpLogsIdempotent(tx, []*model.StockOpLog{{
			OrderID:   req.OrderId,
			SkuID:     req.SkuId,
			OpType:    stockOpTypeDeduct,
			Quantity:  req.Quantity,
			CreatedAt: now,
		}})
		if err != nil {
			return err
		}
		if replayed {
			// 已执行过同一扣减：返回当前库存视为成功
			var sku model.Sku
			if err := tx.First(&sku, req.SkuId).Error; err != nil {
				return err
			}
			success = true
			remain = sku.Stock
			return nil
		}

		var sku model.Sku
		if err := tx.Clauses(lockClause()).First(&sku, req.SkuId).Error; err != nil {
			return err
		}
		if err := deductSkuLocked(tx, &sku, req.Quantity, req.OrderId, now); err != nil {
			return err
		}
		if err := syncProductAggregates(tx, []int64{sku.ProductID}); err != nil {
			return err
		}
		success = true
		remain = sku.Stock
		return nil
	})
	return success, remain, err
}

// BatchDeductStock 批量扣减库存（整单明细同一事务、一次往返完成）
// 下单链路原本逐行调用 DeductStock，N 条明细即 N 次 gRPC 往返且每条独立事务，
// 任一条失败要靠订单侧补偿回滚；本方法把整单收进一个事务，全成或全败。
// 幂等口径与 DeductStock 一致（order_id + sku_id + op_type），整批全部命中唯一索引
// 即判定为重复请求直接返回成功。
//
// 参数：
//   - ctx: 上下文
//   - req: 批量扣减请求（订单 ID + 明细）
//
// 返回值：
//   - bool: 是否成功
//   - error: 库存不足/参数非法/数据库错误
//
// 使用示例：
//
//	ok, err := stockService.BatchDeductStock(ctx, &product.BatchDeductStockReq{
//	    OrderId: 100,
//	    Items:   []*product.StockItemQty{{SkuId: 1, Quantity: 2}, {SkuId: 2, Quantity: 1}},
//	})
func (s *StockService) BatchDeductStock(ctx context.Context, req *product.BatchDeductStockReq) (bool, error) {
	ids, qtyBySku, err := normalizeStockItems(req.Items)
	if err != nil {
		return false, err
	}
	var success bool
	err = s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")
		logs := make([]*model.StockOpLog, 0, len(ids))
		for _, id := range ids {
			logs = append(logs, &model.StockOpLog{
				OrderID:   req.OrderId,
				SkuID:     id,
				OpType:    stockOpTypeDeduct,
				Quantity:  qtyBySku[id],
				CreatedAt: now,
			})
		}
		replayed, err := insertOpLogsIdempotent(tx, logs)
		if err != nil {
			return err
		}
		if replayed {
			success = true
			return nil
		}
		// lockSkus 按主键升序加锁，与出入库单据同口径，避免多单交叉扣减死锁
		skus, err := lockSkus(tx, ids, 0)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := deductSkuLocked(tx, skus[id], qtyBySku[id], req.OrderId, now); err != nil {
				return err
			}
		}
		if err := syncAggregatesForSkus(tx, skus); err != nil {
			return err
		}
		success = true
		return nil
	})
	return success, err
}

// ReleaseStock 释放库存（幂等，回补生成调整批次）
// 以 order_id + sku_id + 操作类型 + biz_no 唯一约束实现幂等：取消/退款/超时关单等
// 多条链路对同一订单同一 SKU 的重复释放请求只会生效一次，防止超量回补。
// 回补的库存无法对应原批次（销售时未指定），统一生成"释放回补"批次，
// 单位成本取 SKU 当前移动加权平均成本。
//
// 参数：
//   - ctx: 上下文
//   - req: 释放请求
//
// 返回值：
//   - error: 错误信息
func (s *StockService) ReleaseStock(ctx context.Context, req *product.ReleaseStockReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 幂等日志先行插入：重复请求（同 order+sku+op+biz_no）在唯一索引上冲突（RowsAffected=0）时直接跳过；
		// 不同业务号（多笔部分退款/取消/超时关单）各占一行，互不吞并
		now := time.Now().Format("2006-01-02 15:04:05")
		replayed, err := insertOpLogsIdempotent(tx, []*model.StockOpLog{{
			OrderID:   req.OrderId,
			SkuID:     req.SkuId,
			OpType:    stockOpTypeRelease,
			BizNo:     req.BizNo,
			Quantity:  req.Quantity,
			CreatedAt: now,
		}})
		if err != nil {
			return err
		}
		if replayed {
			return nil
		}

		var sku model.Sku
		if err := tx.Clauses(lockClause()).First(&sku, req.SkuId).Error; err != nil {
			return err
		}
		if err := releaseSkuLocked(tx, &sku, req.Quantity, req.OrderId, req.BizNo, now); err != nil {
			return err
		}
		return syncProductAggregates(tx, []int64{sku.ProductID})
	})
}

// releaseSkuLocked 在已加锁事务内回补单个 SKU：汇总列回涨 + 生成「释放回补」批次 + 落流水
// 回补库存无法对应原批次（销售时未指定），统一按 SKU 当前移动加权平均成本建批次
//
// 参数：
//   - tx: 事务
//   - sku: 已加锁的 SKU，Stock 就地回涨
//   - quantity: 回补数量
//   - orderID: 关联订单 ID
//   - bizNo: 幂等业务号（写入批次与流水的 ref_no）
//   - now: 统一时间串
//
// 返回值：
//   - error: 写入失败
func releaseSkuLocked(tx *gorm.DB, sku *model.Sku, quantity int32, orderID int64, bizNo, now string) error {
	if quantity <= 0 {
		return invalidArg("SKU %s 的回补数量必须大于 0", sku.SkuCode)
	}
	// 跨 biz_no 重复回补拦截：取消/退款/补偿各占独立幂等槽位，互不吞并，
	// 少了这道闸，货到付款单「先部分退款、再整单取消」会把同一 SKU 回补两次
	if err := assertReleaseWithinDeduct(tx, orderID, sku.ID); err != nil {
		return err
	}
	before := sku.Stock
	sku.Stock += quantity
	if err := tx.Model(&model.Sku{}).Where("id = ?", sku.ID).Updates(map[string]interface{}{
		"stock": sku.Stock,
	}).Error; err != nil {
		return err
	}
	batch := &model.StockBatch{
		CommunityID: sku.CommunityID,
		SkuID:       sku.ID,
		Quantity:    quantity,
		UnitCost:    sku.CostPrice,
		Status:      1,
		SourceType:  2,
		RefNo:       bizNo,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := tx.Create(batch).Error; err != nil {
		return err
	}
	record := &model.StockRecord{
		SkuID:          sku.ID,
		CommunityID:    sku.CommunityID,
		Type:           4,
		Quantity:       quantity,
		BeforeStock:    before,
		AfterStock:     sku.Stock,
		BatchID:        batch.ID,
		RelatedOrderID: orderID,
		RefNo:          bizNo,
		CreatedAt:      now,
	}
	return tx.Create(record).Error
}

// assertReleaseWithinDeduct 校验该订单该 SKU 的累计回补不超过累计扣减
// 调用时本次回补的幂等日志已落库，故统计口径已含本次；由于取消/退款/补偿各占独立
// biz_no 槽位（唯一索引只防同号重放），这道按订单维度的净上限才能拦住跨业务号的重复回补
//
// 参数：
//   - tx: 事务
//   - orderID: 订单 ID（0 表示无订单归属，不做拦截）
//   - skuID: SKU ID
//
// 返回值：
//   - error: 超出净上限或查询失败
func assertReleaseWithinDeduct(tx *gorm.DB, orderID, skuID int64) error {
	if orderID == 0 {
		return nil
	}
	var deducted, released int64
	if err := tx.Model(&model.StockOpLog{}).
		Where("order_id = ? AND sku_id = ? AND op_type = ?", orderID, skuID, stockOpTypeDeduct).
		Select("COALESCE(SUM(quantity), 0)").Scan(&deducted).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.StockOpLog{}).
		Where("order_id = ? AND sku_id = ? AND op_type = ?", orderID, skuID, stockOpTypeRelease).
		Select("COALESCE(SUM(quantity), 0)").Scan(&released).Error; err != nil {
		return err
	}
	if released > deducted {
		return bizerr.New(codes.FailedPrecondition,
			fmt.Sprintf("订单 %d 的商品（SKU %d）回补数量 %d 超过扣减数量 %d，已拒绝", orderID, skuID, released, deducted))
	}
	return nil
}

// BatchReleaseStock 批量释放库存（整单明细同一事务、一次往返完成）
// 取消/超时关单原本逐行调用 ReleaseStock，N 条明细 N 次往返且各自独立事务，
// 中途失败会留下「部分回补」的半态；本方法整单原子回补。
// 幂等口径与 ReleaseStock 一致（order_id + sku_id + op_type + biz_no），
// 同一 biz_no 重复请求整批短路成功，不同业务（退款/取消）各自传不同 biz_no。
//
// 参数：
//   - ctx: 上下文
//   - req: 批量释放请求
//
// 返回值：
//   - error: 参数非法/数据库错误
//
// 使用示例：
//
//	err := stockService.BatchReleaseStock(ctx, &product.BatchReleaseStockReq{
//	    OrderId: 100, BizNo: fmt.Sprintf("cancel:%d", 100), Items: items,
//	})
func (s *StockService) BatchReleaseStock(ctx context.Context, req *product.BatchReleaseStockReq) error {
	ids, qtyBySku, err := normalizeStockItems(req.Items)
	if err != nil {
		return err
	}
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")
		logs := make([]*model.StockOpLog, 0, len(ids))
		for _, id := range ids {
			logs = append(logs, &model.StockOpLog{
				OrderID:   req.OrderId,
				SkuID:     id,
				OpType:    stockOpTypeRelease,
				BizNo:     req.BizNo,
				Quantity:  qtyBySku[id],
				CreatedAt: now,
			})
		}
		replayed, err := insertOpLogsIdempotent(tx, logs)
		if err != nil {
			return err
		}
		if replayed {
			return nil
		}
		skus, err := lockSkus(tx, ids, 0)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := releaseSkuLocked(tx, skus[id], qtyBySku[id], req.OrderId, req.BizNo, now); err != nil {
				return err
			}
		}
		return syncAggregatesForSkus(tx, skus)
	})
}

// DeductFlashSaleStock 扣减秒杀活动库存（幂等）
// flash_sale_items.stock 独立于主库存，仅作秒杀活动的限量池；
// 幂等键 op_type=3（与主库存扣减区分），重复请求直接成功。
// 无生效秒杀活动时直接 no-op（订单侧对全部明细统一调用，非秒杀商品不应报错）。
// 参数：
//   - ctx: 上下文
//   - req: 扣减请求
//
// 返回值：
//   - error: 错误信息（秒杀库存不足时返回错误，由订单侧补偿回滚）
func (s *StockService) DeductFlashSaleStock(ctx context.Context, req *product.DeductFlashSaleStockReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deductOneFlashSku(tx, req.OrderId, req.SkuId, req.Quantity, time.Now())
	})
}

// deductOneFlashSku 在事务内扣减单个 SKU 的秒杀库存（定位活动 → 幂等日志 → 条件更新）
// 无生效秒杀活动时直接 no-op（订单侧对全部明细统一调用，非秒杀商品不应报错），
// 且不落幂等日志，释放侧据此判断是否需要回补
//
// 参数：
//   - tx: 事务
//   - orderID: 订单 ID
//   - skuID: SKU ID
//   - quantity: 扣减数量
//   - now: 统一时间（活动生效区间判定与日志时间同源）
//
// 返回值：
//   - error: 秒杀库存不足/数据库错误
func deductOneFlashSku(tx *gorm.DB, orderID, skuID int64, quantity int32, now time.Time) error {
	item, err := lockActiveFlashSaleItem(tx, skuID, now)
	if err != nil {
		return err
	}
	if item == nil {
		return nil
	}

	opLog := &model.StockOpLog{
		OrderID:     orderID,
		SkuID:       skuID,
		OpType:      stockOpTypeFlashDeduct,
		FlashSaleID: item.ID,
		Quantity:    quantity,
		CreatedAt:   now.Format("2006-01-02 15:04:05"),
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(opLog)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}

	if item.Stock < quantity {
		return bizerr.New(codes.ResourceExhausted, "秒杀库存不足")
	}
	// 条件更新防并发超卖（行锁已加，双保险）
	r := tx.Model(&model.FlashSaleItem{}).
		Where("id = ? AND stock >= ?", item.ID, quantity).
		Updates(map[string]interface{}{
			"stock":      item.Stock - quantity,
			"sold_count": item.SoldCount + quantity,
		})
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return bizerr.New(codes.ResourceExhausted, "秒杀库存不足")
	}
	return nil
}

// BatchDeductFlashSaleStock 批量扣减秒杀活动库存（整单明细同一事务、一次往返完成）
// 逐条口径与 DeductFlashSaleStock 完全一致（非秒杀 SKU 自动跳过），只是把 N 次往返收进一个事务
//
// 参数：
//   - ctx: 上下文
//   - req: 批量扣减请求
//
// 返回值：
//   - error: 参数非法/秒杀库存不足/数据库错误
func (s *StockService) BatchDeductFlashSaleStock(ctx context.Context, req *product.BatchDeductFlashSaleStockReq) error {
	ids, qtyBySku, err := normalizeStockItems(req.Items)
	if err != nil {
		return err
	}
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		for _, id := range ids {
			if err := deductOneFlashSku(tx, req.OrderId, id, qtyBySku[id], now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReleaseFlashSaleStock 释放秒杀活动库存（幂等）
// 仅当该订单该 SKU 存在秒杀扣减日志时才释放（订单侧对全部明细统一调用，
// 非秒杀商品自动 no-op，无需订单表记录秒杀标记）。
// 参数：
//   - ctx: 上下文
//   - req: 释放请求
//
// 返回值：
//   - error: 错误信息
func (s *StockService) ReleaseFlashSaleStock(ctx context.Context, req *product.ReleaseFlashSaleStockReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return releaseOneFlashSku(tx, req.OrderId, req.SkuId, req.Quantity, req.BizNo, time.Now())
	})
}

// releaseOneFlashSku 在事务内回补单个 SKU 的秒杀库存（查扣减日志 → 幂等日志 → 定向回补原活动池）
//
// 参数：
//   - tx: 事务
//   - orderID: 订单 ID
//   - skuID: SKU ID
//   - quantity: 回补数量
//   - bizNo: 幂等业务号
//   - now: 统一时间
//
// 返回值：
//   - error: 数据库错误
func releaseOneFlashSku(tx *gorm.DB, orderID, skuID int64, quantity int32, bizNo string, now time.Time) error {
	// 无秒杀扣减记录：非秒杀商品，直接成功
	var deductLog model.StockOpLog
	err := tx.Where("order_id = ? AND sku_id = ? AND op_type = ?", orderID, skuID, stockOpTypeFlashDeduct).
		First(&deductLog).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}

	opLog := &model.StockOpLog{
		OrderID:   orderID,
		SkuID:     skuID,
		OpType:    stockOpTypeFlashRelease,
		BizNo:     bizNo,
		Quantity:  quantity,
		CreatedAt: now.Format("2006-01-02 15:04:05"),
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(opLog)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}

	// 优先按扣减日志记录的活动商品 ID 定向回补：同 SKU 多活动先后/并发时，
	// 活动结束后的退款也能回补到当初扣减的活动池，避免 A 扣 B 补的池错位；
	// 日志缺失（历史数据/活动项被删）时回落到现有定位逻辑
	item, err := lockFlashSaleItemByID(tx, deductLog.FlashSaleID, skuID)
	if err != nil {
		return err
	}
	if item == nil {
		item, err = lockFlashSaleItemForRelease(tx, skuID, now)
		if err != nil {
			return err
		}
	}
	return tx.Model(&model.FlashSaleItem{}).Where("id = ?", item.ID).
		Updates(map[string]interface{}{
			"stock":      item.Stock + quantity,
			"sold_count": item.SoldCount - quantity,
		}).Error
}

// BatchReleaseFlashSaleStock 批量回补秒杀活动库存（整单明细同一事务、一次往返完成）
// 逐条口径与 ReleaseFlashSaleStock 一致：无秒杀扣减记录的 SKU 自动跳过。
// biz_no 为空时回落 cancel:{order_id}，与订单取消链路的幂等号保持同源。
//
// 参数：
//   - ctx: 上下文
//   - req: 批量回补请求
//
// 返回值：
//   - error: 参数非法/数据库错误
func (s *StockService) BatchReleaseFlashSaleStock(ctx context.Context, req *product.BatchReleaseFlashSaleStockReq) error {
	ids, qtyBySku, err := normalizeStockItems(req.Items)
	if err != nil {
		return err
	}
	bizNo := req.BizNo
	if bizNo == "" {
		bizNo = fmt.Sprintf("cancel:%d", req.OrderId)
	}
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		for _, id := range ids {
			if err := releaseOneFlashSku(tx, req.OrderId, id, qtyBySku[id], bizNo, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// lockActiveFlashSaleItem 锁定 SKU 当前生效秒杀活动的商品项
// 无生效秒杀活动时返回 (nil, nil)，调用方据此走 no-op 路径。
// 参数：
//   - tx: 事务
//   - skuID: SKU ID
//   - now: 当前时间（判定活动时效）
//
// 返回值：
//   - *model.FlashSaleItem: 秒杀商品项（已加行锁）；无生效活动时 nil
//   - error: 错误信息
func lockActiveFlashSaleItem(tx *gorm.DB, skuID int64, now time.Time) (*model.FlashSaleItem, error) {
	nowStr := now.Format("2006-01-02 15:04:05")
	var item model.FlashSaleItem
	err := tx.Clauses(lockClause()).
		Joins("JOIN flash_sales fs ON fs.id = flash_sale_items.flash_sale_id").
		Where("flash_sale_items.sku_id = ? AND flash_sale_items.status = 1", skuID).
		Where("fs.status = 1 AND fs.start_time <= ? AND fs.end_time >= ?", nowStr, nowStr).
		Order("flash_sale_items.id ASC").
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

// lockFlashSaleItemByID 按扣减日志记录的活动商品 ID 定位秒杀项（带行锁）
// 校验 SKU 归属一致，防止脏日志回补到其他 SKU 的活动池；活动项已被删时返回 (nil, nil)，
// 调用方据此走回落定位逻辑。
// 参数：
//   - tx: 事务
//   - itemID: 秒杀商品项 ID（取自幂等日志 flash_sale_id）
//   - skuID: SKU ID（归属校验）
//
// 返回值：
//   - *model.FlashSaleItem: 秒杀商品项（已加行锁）；未找到时 nil
//   - error: 错误信息
func lockFlashSaleItemByID(tx *gorm.DB, itemID, skuID int64) (*model.FlashSaleItem, error) {
	if itemID <= 0 {
		return nil, nil
	}
	var item model.FlashSaleItem
	err := tx.Clauses(lockClause()).
		Where("id = ? AND sku_id = ?", itemID, skuID).
		First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

// lockFlashSaleItemForRelease 释放时定位秒杀商品项
// 优先取当前生效活动；活动已结束时回落到该 SKU 最近的一条秒杀商品项
// （退款可能发生在活动结束后，仍需回补）。
// 参数：
//   - tx: 事务
//   - skuID: SKU ID
//   - now: 当前时间
//
// 返回值：
//   - *model.FlashSaleItem: 秒杀商品项（已加行锁）
//   - error: 错误信息
func lockFlashSaleItemForRelease(tx *gorm.DB, skuID int64, now time.Time) (*model.FlashSaleItem, error) {
	nowStr := now.Format("2006-01-02 15:04:05")
	var item model.FlashSaleItem
	err := tx.Clauses(lockClause()).
		Joins("JOIN flash_sales fs ON fs.id = flash_sale_items.flash_sale_id").
		Where("flash_sale_items.sku_id = ?", skuID).
		Order(fmt.Sprintf("CASE WHEN fs.status = 1 AND fs.start_time <= '%s' AND fs.end_time >= '%s' THEN 0 ELSE 1 END ASC, flash_sale_items.id DESC", nowStr, nowStr)).
		First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// deductBatches 按 FEFO 从批次扣减数量并同步 SKU 汇总库存（调用方须已锁 SKU 行）
// 参数：
//   - tx: 事务
//   - sku: SKU 模型（行锁持有中，函数内更新其 Stock 汇总）
//   - quantity: 扣减数量（正数）
//   - now: 当前时间字符串（updated_at 用）
//
// 返回值：
//   - []batchTake: 各批次扣减明细
//   - error: 批次合计与汇总不一致或更新失败
func deductBatches(tx *gorm.DB, sku *model.Sku, quantity int32, now string) ([]batchTake, error) {
	var batches []model.StockBatch
	// FEFO：有有效期批次按过期日期升序先出，无有效期批次排最后按创建顺序
	if err := tx.Clauses(lockClause()).
		Where("sku_id = ? AND quantity > 0", sku.ID).
		Order("expire_date IS NULL ASC, expire_date ASC, id ASC").
		Find(&batches).Error; err != nil {
		return nil, err
	}

	remaining := quantity
	takes := make([]batchTake, 0, len(batches))
	for _, b := range batches {
		if remaining <= 0 {
			break
		}
		take := b.Quantity
		if take > remaining {
			take = remaining
		}
		takes = append(takes, batchTake{Batch: b, Take: take})
		remaining -= take
	}
	if remaining > 0 {
		return nil, fmt.Errorf("SKU %s 批次库存不一致：汇总 %d 大于批次合计", sku.SkuCode, sku.Stock)
	}

	for i := range takes {
		t := &takes[i]
		t.Batch.Quantity -= t.Take
		status := int32(1)
		if t.Batch.Quantity <= 0 {
			t.Batch.Quantity = 0
			status = 0
		}
		t.Batch.Status = status
		if err := tx.Model(&model.StockBatch{}).Where("id = ?", t.Batch.ID).
			Updates(map[string]interface{}{
				"quantity":   t.Batch.Quantity,
				"status":     status,
				"updated_at": now,
			}).Error; err != nil {
			return nil, err
		}
	}
	sku.Stock -= quantity
	return takes, nil
}

// ListStockRecords 查询库存流水
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.StockRecord: 流水列表
//   - int: 总条数
//   - error: 错误信息
func (s *StockService) ListStockRecords(ctx context.Context, req *product.ListStockRecordsReq) ([]*model.StockRecord, int, error) {
	page, pageSize := pageparam.Clamp(int(req.Pagination.GetPage()), int(req.Pagination.GetPageSize()))

	db := s.db.GetDb().WithContext(ctx).Model(&model.StockRecord{})
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	if req.SkuId > 0 {
		db = db.Where("sku_id = ?", req.SkuId)
	}
	if req.Type > 0 {
		db = db.Where("type = ?", req.Type)
	}
	if req.StartDate != "" {
		db = db.Where("created_at >= ?", req.StartDate)
	}
	if req.EndDate != "" {
		db = db.Where("created_at <= ?", req.EndDate+" 23:59:59")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var list []*model.StockRecord
	err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// GetStockWarnings 获取库存预警
// 同时检查低库存（warning_type=1）与临期（warning_type=2）两种情况，同一 SKU 可能产生两条预警记录。
// 临期预警按批次库存（在库批次的最小过期日期）精确计算。
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//
// 返回值：
//   - []*product.StockWarningInfo: 预警列表
//   - error: 错误信息
func (s *StockService) GetStockWarnings(ctx context.Context, communityID int64) ([]*product.StockWarningInfo, error) {
	var skus []*model.Sku
	db := s.db.GetDb().WithContext(ctx).Where("status = ?", 1)
	if communityID > 0 {
		db = db.Where("community_id = ?", communityID)
	}
	if err := db.Find(&skus).Error; err != nil {
		return nil, err
	}

	// 按 SKU 聚合最早未过期在库批次日期
	earliestExpireDate, err := s.getEarliestExpireDateBySku(ctx, communityID)
	if err != nil {
		return nil, err
	}

	// 批量查询商品名称
	productIDs := make([]int64, 0, len(skus))
	for _, sku := range skus {
		productIDs = append(productIDs, sku.ProductID)
	}
	productNames := s.getProductNames(ctx, productIDs)

	var warnings []*product.StockWarningInfo
	now := time.Now()
	deadline := now.AddDate(0, 0, s.expireWarningDays).Format("2006-01-02")
	for _, sku := range skus {
		productName := productNames[sku.ProductID]

		// 低库存预警
		if sku.Stock <= sku.SafetyStock {
			warnings = append(warnings, &product.StockWarningInfo{
				SkuId:       sku.ID,
				SkuCode:     sku.SkuCode,
				SpecName:    sku.SpecName,
				ProductName: productName,
				Stock:       sku.Stock,
				SafetyStock: sku.SafetyStock,
				WarningType: 1,
			})
		}

		// 临期预警：当前库存大于 0 且存在即将过期的在库批次
		if sku.Stock > 0 {
			if expireDate, ok := earliestExpireDate[sku.ID]; ok && expireDate != "" {
				if expireDate <= deadline {
					warnings = append(warnings, &product.StockWarningInfo{
						SkuId:       sku.ID,
						SkuCode:     sku.SkuCode,
						SpecName:    sku.SpecName,
						ProductName: productName,
						Stock:       sku.Stock,
						SafetyStock: sku.SafetyStock,
						WarningType: 2,
						ExpireDate:  expireDate,
					})
				}
			}
		}
	}
	return warnings, nil
}

// getEarliestExpireDateBySku 查询各 SKU 最早未过期的在库批次过期日期
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID，<=0 时查询全部社区
//
// 返回值：
//   - map[int64]string: SKU ID -> 最早过期日期（YYYY-MM-DD）
//   - error: 错误信息
func (s *StockService) getEarliestExpireDateBySku(ctx context.Context, communityID int64) (map[int64]string, error) {
	type result struct {
		SkuID      int64  `gorm:"column:sku_id"`
		ExpireDate string `gorm:"column:expire_date"`
	}
	db := s.db.GetDb().WithContext(ctx).Model(&model.StockBatch{}).
		Select("sku_id", "MIN(expire_date) as expire_date").
		Where("quantity > 0 AND expire_date IS NOT NULL").
		Where("expire_date >= ?", time.Now().Format("2006-01-02"))
	if communityID > 0 {
		db = db.Where("community_id = ?", communityID)
	}
	var rows []result
	if err := db.Group("sku_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]string, len(rows))
	for _, r := range rows {
		m[r.SkuID] = r.ExpireDate
	}
	return m, nil
}

// getProductNames 批量查询商品名称
// 参数：
//   - ctx: 上下文
//   - productIDs: 商品 ID 列表
//
// 返回值：
//   - map[int64]string: 商品 ID -> 商品名称
func (s *StockService) getProductNames(ctx context.Context, productIDs []int64) map[int64]string {
	names := make(map[int64]string)
	if len(productIDs) == 0 {
		return names
	}
	var products []model.Product
	if err := s.db.GetDb().WithContext(ctx).Select("id", "name").Where("id IN ?", productIDs).Find(&products).Error; err != nil {
		log.Printf("[WARN] 批量取商品名称失败，列表将缺失商品名: %v", err)
		return names
	}
	for _, p := range products {
		names[p.ID] = p.Name
	}
	return names
}

// GeneratePurchaseOrder 生成补货建议单
// 仅针对低库存预警（warning_type=1）的 SKU 生成补货建议；
// 临期预警 SKU 不参与补货，应由促销/清仓流程处理。
//
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID，<=0 时查询全部
//   - skuIDs: 指定仅生成这些 SKU 的建议（工作台勾选补货）；为空时生成全部低库存建议
//
// 返回值：
//   - []*product.PurchaseOrderItem: 补货建议列表
//   - error: 错误信息
func (s *StockService) GeneratePurchaseOrder(ctx context.Context, communityID int64, skuIDs []int64) ([]*product.PurchaseOrderItem, error) {
	warnings, err := s.GetStockWarnings(ctx, communityID)
	if err != nil {
		return nil, err
	}
	var selected map[int64]struct{}
	if len(skuIDs) > 0 {
		selected = make(map[int64]struct{}, len(skuIDs))
		for _, id := range skuIDs {
			selected[id] = struct{}{}
		}
	}
	items := make([]*product.PurchaseOrderItem, 0, len(warnings))
	for _, w := range warnings {
		// 跳过临期预警，仅处理低库存
		if w.WarningType != 1 {
			continue
		}
		// 指定了 SKU 集合时仅保留命中的项（不在集合内的低库存项跳过）
		if selected != nil {
			if _, ok := selected[w.SkuId]; !ok {
				continue
			}
		}
		suggest := w.SafetyStock*2 - w.Stock
		if suggest < 1 {
			suggest = 1
		}
		items = append(items, &product.PurchaseOrderItem{
			SkuId:           w.SkuId,
			SkuCode:         w.SkuCode,
			SpecName:        w.SpecName,
			ProductName:     w.ProductName,
			CurrentStock:    w.Stock,
			SafetyStock:     w.SafetyStock,
			SuggestQuantity: suggest,
		})
	}
	return items, nil
}

// ToStockRecordInfo 将库存流水模型转换为 proto 信息
// 参数：
//   - r: 库存流水模型
//
// 返回值：
//   - *product.StockRecordInfo: proto 库存流水信息
func ToStockRecordInfo(r *model.StockRecord) *product.StockRecordInfo {
	if r == nil {
		return nil
	}
	return &product.StockRecordInfo{
		Id:             r.ID,
		SkuId:          r.SkuID,
		Type:           r.Type,
		Quantity:       r.Quantity,
		BeforeStock:    r.BeforeStock,
		AfterStock:     r.AfterStock,
		BatchId:        r.BatchID,
		BatchNo:        r.BatchNo,
		ProductionDate: ptrString(r.ProductionDate),
		ExpireDate:     ptrString(r.ExpireDate),
		RelatedOrderId: r.RelatedOrderID,
		RefNo:          r.RefNo,
		OperatorId:     r.OperatorID,
		Remark:         r.Remark,
		CreatedAt:      r.CreatedAt,
	}
}

// movingAverageCost 计算移动加权平均成本（保留两位小数）
// 参数：
//   - oldQty: 变动前库存数量
//   - oldCost: 变动前单位成本
//   - inQty: 入库数量
//   - inCost: 入库单价
//
// 返回值：
//   - float64: 新的移动加权平均成本
func movingAverageCost(oldQty int32, oldCost float64, inQty int32, inCost float64) float64 {
	total := int64(oldQty) + int64(inQty)
	if total <= 0 || inQty <= 0 {
		return round2(oldCost)
	}
	return round2((float64(oldQty)*oldCost + float64(inQty)*inCost) / float64(total))
}

// round2 四舍五入保留两位小数
// 参数：
//   - v: 原值
//
// 返回值：
//   - float64: 保留两位小数后的值
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// ptrString 将字符串指针安全转换为字符串
//
// 参数：
//   - s: 字符串指针
//
// 返回值：
//   - string: 空字符串或指针指向的值
func ptrString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// strPtr 将非空字符串转换为指针，空字符串返回 nil
//
// 参数：
//   - s: 字符串
//
// 返回值：
//   - *string: 非空指针或 nil
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// invalidArg 构造用户输入/业务规则类错误（gRPC InvalidArgument）
// 参数：
//   - format: 格式串
//   - args: 格式参数
//
// 返回值：
//   - error: bizerr 错误
func invalidArg(format string, args ...interface{}) error {
	return bizerr.New(codes.InvalidArgument, fmt.Sprintf(format, args...))
}

// lockClause 返回悲观锁子句
// 返回值：
//   - clause.Locking: 加锁子句
func lockClause() clause.Locking {
	return clause.Locking{Strength: "UPDATE"}
}

// lockSkus 在事务内按 sku_id 升序批量加行锁，返回 ID 到 SKU 的映射
// 两个要点都是刻意的：
//   - 升序加锁：并发事务若各自按入参顺序对多行 FOR UPDATE，会互相持有对方等待的行锁形成死锁
//   - 一次 IN 查询替代逐行 First：多明细入库/盘点时把 N 次往返压成 1 次
//
// 参数：
//   - tx: 事务句柄
//   - skuIDs: 待加锁的 SKU ID（允许重复，内部去重）
//   - communityID: 归属社区，>0 时校验每个 SKU 必须属于该社区
//
// 返回值：
//   - map[int64]*model.Sku: SKU 快照，键为 sku_id
//   - error: ID 不存在或跨社区时返回错误
func lockSkus(tx *gorm.DB, skuIDs []int64, communityID int64) (map[int64]*model.Sku, error) {
	uniq := make(map[int64]struct{}, len(skuIDs))
	ids := make([]int64, 0, len(skuIDs))
	for _, id := range skuIDs {
		if _, ok := uniq[id]; !ok {
			uniq[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var rows []*model.Sku
	if err := tx.Clauses(lockClause()).Order("id").Find(&rows, ids).Error; err != nil {
		return nil, err
	}
	skus := make(map[int64]*model.Sku, len(rows))
	for _, r := range rows {
		if communityID > 0 && r.CommunityID != communityID {
			return nil, invalidArg("SKU %d 不属于当前社区", r.ID)
		}
		skus[r.ID] = r
	}
	for _, id := range ids {
		if _, ok := skus[id]; !ok {
			return nil, invalidArg("SKU %d 不存在", id)
		}
	}
	return skus, nil
}
