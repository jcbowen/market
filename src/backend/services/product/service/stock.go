package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// stockOpTypeDeduct/stockOpTypeRelease 幂等日志操作类型
const (
	stockOpTypeDeduct int8 = 1 // 扣减
	stockOpTypeRelease int8 = 2 // 释放
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

// DeductStock 扣减库存（幂等）
// 以 order_id + sku_id + 操作类型 唯一约束实现幂等：同一订单对同一 SKU 的重复
// 扣减请求（如 gRPC 超时重试）直接返回当前库存，不重复扣减。
// 幂等日志与扣减在同一事务内写入，扣减失败时日志一并回滚。
// 参数：
//   - ctx: 上下文
//   - req: 扣减请求
//
// 返回值：
//   - bool: 是否成功
//   - int32: 剩余库存
//   - error: 错误信息
func (s *StockService) DeductStock(ctx context.Context, req *product.DeductStockReq) (bool, int32, error) {
	var success bool
	var remain int32
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 幂等日志先行插入：重复请求在唯一索引上冲突（RowsAffected=0）时短路返回
		now := time.Now().Format("2006-01-02 15:04:05")
		opLog := &model.StockOpLog{
			OrderID:   req.OrderId,
			SkuID:     req.SkuId,
			OpType:    stockOpTypeDeduct,
			Quantity:  req.Quantity,
			CreatedAt: now,
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(opLog)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
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
		if sku.Stock < req.Quantity {
			return bizerr.New(codes.ResourceExhausted, fmt.Sprintf("商品 %s 库存不足", sku.SkuCode))
		}
		before := sku.Stock
		sku.Stock -= req.Quantity
		if err := tx.Save(&sku).Error; err != nil {
			return err
		}
		record := &model.StockRecord{
			SkuID:          sku.ID,
			CommunityID:    sku.CommunityID,
			Type:           2,
			Quantity:       -req.Quantity,
			BeforeStock:    before,
			AfterStock:     sku.Stock,
			RelatedOrderID: req.OrderId,
			CreatedAt:      now,
		}
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		success = true
		remain = sku.Stock
		return nil
	})
	return success, remain, err
}

// ReleaseStock 释放库存（幂等）
// 以 order_id + sku_id + 操作类型 唯一约束实现幂等：取消/退款/超时关单等
// 多条链路对同一订单同一 SKU 的重复释放请求只会生效一次，防止超量回补。
// 参数：
//   - ctx: 上下文
//   - req: 释放请求
//
// 返回值：
//   - error: 错误信息
func (s *StockService) ReleaseStock(ctx context.Context, req *product.ReleaseStockReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 幂等日志先行插入：重复请求在唯一索引上冲突（RowsAffected=0）时直接跳过
		now := time.Now().Format("2006-01-02 15:04:05")
		opLog := &model.StockOpLog{
			OrderID:   req.OrderId,
			SkuID:     req.SkuId,
			OpType:    stockOpTypeRelease,
			Quantity:  req.Quantity,
			CreatedAt: now,
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(opLog)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}

		var sku model.Sku
		if err := tx.Clauses(lockClause()).First(&sku, req.SkuId).Error; err != nil {
			return err
		}
		before := sku.Stock
		sku.Stock += req.Quantity
		if err := tx.Save(&sku).Error; err != nil {
			return err
		}
		record := &model.StockRecord{
			SkuID:          sku.ID,
			CommunityID:    sku.CommunityID,
			Type:           4,
			Quantity:       req.Quantity,
			BeforeStock:    before,
			AfterStock:     sku.Stock,
			RelatedOrderID: req.OrderId,
			CreatedAt:      now,
		}
		return tx.Create(record).Error
	})
}

// StockEntry 库存调整（入库/出库）
// 参数：
//   - ctx: 上下文
//   - req: 入库/出库请求
//
// 返回值：
//   - error: 错误信息
func (s *StockService) StockEntry(ctx context.Context, req *product.StockEntryReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range req.Items {
			var sku model.Sku
			if err := tx.Clauses(lockClause()).First(&sku, item.SkuId).Error; err != nil {
				return err
			}
			before := sku.Stock

			recordType := req.Type
			if recordType == 0 {
				recordType = 1
			}
			quantity := item.Quantity
			if recordType == 2 {
				if sku.Stock < quantity {
					return fmt.Errorf("SKU %d 库存不足，当前库存 %d", sku.ID, sku.Stock)
				}
				quantity = -quantity
			}

			sku.Stock += quantity
			if err := tx.Save(&sku).Error; err != nil {
				return err
			}
			record := &model.StockRecord{
				SkuID:          sku.ID,
				CommunityID:    req.CommunityId,
				Type:           recordType,
				Quantity:       quantity,
				BeforeStock:    before,
				AfterStock:     sku.Stock,
				BatchNo:        item.BatchNo,
				ProductionDate: strPtr(item.ProductionDate),
				ExpireDate:     strPtr(item.ExpireDate),
				OperatorID:     req.OperatorId,
				Remark:         req.Remark,
				CreatedAt:      time.Now().Format("2006-01-02 15:04:05"),
			}
			if err := tx.Create(record).Error; err != nil {
				return err
			}
		}
		return nil
	})
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
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}

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

	// 按 SKU 聚合最早未过期批次日期
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

		// 临期预警：当前库存大于 0 且存在即将过期的批次
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

// getEarliestExpireDateBySku 查询各 SKU 最早未过期的批次过期日期
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
	db := s.db.GetDb().WithContext(ctx).Model(&model.StockRecord{}).
		Select("sku_id", "MIN(expire_date) as expire_date").
		Where("expire_date IS NOT NULL").
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
		return names
	}
	for _, p := range products {
		names[p.ID] = p.Name
	}
	return names
}

// StockOut 库存出库（销售出库/报损出库）
// 参数：
//   - ctx: 上下文
//   - req: 出库请求
//
// 返回值：
//   - error: 错误信息
func (s *StockService) StockOut(ctx context.Context, req *product.StockOutReq) error {
	items := make([]*product.StockEntryItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, &product.StockEntryItem{
			SkuId:    item.SkuId,
			Quantity: item.Quantity,
		})
	}
	return s.StockEntry(ctx, &product.StockEntryReq{
		CommunityId: req.CommunityId,
		Type:        req.Type,
		Items:       items,
		OperatorId:  req.OperatorId,
		Remark:      req.Remark,
	})
}

// GeneratePurchaseOrder 生成补货建议单
// 仅针对低库存预警（warning_type=1）的 SKU 生成补货建议；
// 临期预警 SKU 不参与补货，应由促销/清仓流程处理。
//
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID，<=0 时查询全部
//
// 返回值：
//   - []*product.PurchaseOrderItem: 补货建议列表
//   - error: 错误信息
func (s *StockService) GeneratePurchaseOrder(ctx context.Context, communityID int64) ([]*product.PurchaseOrderItem, error) {
	warnings, err := s.GetStockWarnings(ctx, communityID)
	if err != nil {
		return nil, err
	}
	items := make([]*product.PurchaseOrderItem, 0, len(warnings))
	for _, w := range warnings {
		// 跳过临期预警，仅处理低库存
		if w.WarningType != 1 {
			continue
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
		BatchNo:        r.BatchNo,
		ProductionDate: ptrString(r.ProductionDate),
		ExpireDate:     ptrString(r.ExpireDate),
		RelatedOrderId: r.RelatedOrderID,
		OperatorId:     r.OperatorID,
		Remark:         r.Remark,
		CreatedAt:      r.CreatedAt,
	}
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

// lockClause 返回悲观锁子句
// 返回值：
//   - clause.Locking: 加锁子句
func lockClause() clause.Locking {
	return clause.Locking{Strength: "UPDATE"}
}
