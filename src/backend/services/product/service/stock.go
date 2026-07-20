package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// StockService 库存业务服务
type StockService struct {
	db *mysql.Instance
}

// NewStockService 创建库存业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *StockService: 库存业务服务实例
func NewStockService(db *mysql.Instance) *StockService {
	return &StockService{db: db}
}

// DeductStock 扣减库存
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
		var sku model.Sku
		if err := tx.Clauses(lockClause()).First(&sku, req.SkuId).Error; err != nil {
			return err
		}
		if sku.Stock < req.Quantity {
			return fmt.Errorf("库存不足")
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
			CreatedAt:      time.Now().Format("2006-01-02 15:04:05"),
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

// ReleaseStock 释放库存
// 参数：
//   - ctx: 上下文
//   - req: 释放请求
//
// 返回值：
//   - error: 错误信息
func (s *StockService) ReleaseStock(ctx context.Context, req *product.ReleaseStockReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			CreatedAt:      time.Now().Format("2006-01-02 15:04:05"),
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

	var warnings []*product.StockWarningInfo
	for _, sku := range skus {
		if sku.Stock <= sku.SafetyStock {
			var productName string
			var p model.Product
			_ = s.db.GetDb().WithContext(ctx).Select("name").First(&p, sku.ProductID).Error
			productName = p.Name
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
	}
	return warnings, nil
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
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
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
		suggest := w.SafetyStock*2 - w.Stock
		if suggest < 1 {
			suggest = 1
		}
		items = append(items, &product.PurchaseOrderItem{
			SkuId:         w.SkuId,
			SkuCode:       w.SkuCode,
			SpecName:      w.SpecName,
			ProductName:   w.ProductName,
			CurrentStock:  w.Stock,
			SafetyStock:   w.SafetyStock,
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
