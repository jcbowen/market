package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// StockBillService 入库单/出库单/批次库存业务服务
type StockBillService struct {
	db *mysql.Instance
}

// NewStockBillService 创建入库/出库单业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *StockBillService: 服务实例
func NewStockBillService(db *mysql.Instance) *StockBillService {
	return &StockBillService{db: db}
}

// CreateStockInOrder 创建入库单（创建即生效）
// 事务内完成：生成批次（SourceType=1）→ 回写 SKU 汇总库存与移动加权平均成本 →
// 落库存流水（type=1）→ 关联采购单时回写 received_quantity 与采购单状态。
//
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.StockInOrder: 入库单模型
//   - []*model.StockInOrderItem: 入库单明细（含生成的批次 ID）
//   - error: 错误信息
func (s *StockBillService) CreateStockInOrder(ctx context.Context, req *product.CreateStockInOrderReq) (*model.StockInOrder, []*model.StockInOrderItem, error) {
	if len(req.Items) == 0 {
		return nil, nil, invalidArg("入库明细不能为空")
	}
	for _, item := range req.Items {
		if item.Quantity <= 0 {
			return nil, nil, invalidArg("入库数量必须大于 0")
		}
	}

	var (
		order *model.StockInOrder
		items []*model.StockInOrderItem
	)
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")

		orderType := model.StockInTypeDirect
		if req.PurchaseOrderId > 0 {
			orderType = model.StockInTypePurchase
			// 校验采购单可入库（已下单/部分入库）
			var po model.PurchaseOrder
			if err := tx.Clauses(lockClause()).First(&po, req.PurchaseOrderId).Error; err != nil {
				return invalidArg("采购单不存在")
			}
			if po.CommunityID != req.CommunityId {
				return invalidArg("采购单不属于当前社区")
			}
			if po.Status != model.PurchaseStatusOrdered && po.Status != model.PurchaseStatusPartial {
				return invalidArg("采购单当前状态不允许入库")
			}
			// 未指定供应商时回落采购单供应商
			if req.SupplierId == 0 {
				req.SupplierId = po.SupplierID
			}
		}

		// 汇总入库金额（先校验 SKU 合法性）
		var totalAmount float64
		skus := make(map[int64]*model.Sku, len(req.Items))
		for _, item := range req.Items {
			sku, ok := skus[item.SkuId]
			if !ok {
				sku = &model.Sku{}
				if err := tx.Clauses(lockClause()).First(sku, item.SkuId).Error; err != nil {
					return invalidArg("SKU %d 不存在", item.SkuId)
				}
				if sku.CommunityID != req.CommunityId {
					return invalidArg("SKU %d 不属于当前社区", item.SkuId)
				}
				skus[item.SkuId] = sku
			}
			unitCost := item.UnitCost
			if unitCost <= 0 {
				unitCost = sku.CostPrice
			}
			totalAmount += float64(item.Quantity) * unitCost
		}

		order = &model.StockInOrder{
			CommunityID:     req.CommunityId,
			InNo:            generateInNo(),
			SupplierID:      req.SupplierId,
			PurchaseOrderID: req.PurchaseOrderId,
			Type:            orderType,
			TotalAmount:     round2(totalAmount),
			OperatorID:      req.OperatorId,
			Remark:          req.Remark,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := tx.Create(order).Error; err != nil {
			return err
		}

		items = make([]*model.StockInOrderItem, 0, len(req.Items))
		for _, item := range req.Items {
			sku := skus[item.SkuId]
			unitCost := item.UnitCost
			if unitCost <= 0 {
				unitCost = sku.CostPrice
			}

			// 生成批次
			batch := &model.StockBatch{
				CommunityID:    req.CommunityId,
				SkuID:          sku.ID,
				BatchNo:        item.BatchNo,
				ProductionDate: strPtr(item.ProductionDate),
				ExpireDate:     strPtr(item.ExpireDate),
				Quantity:       item.Quantity,
				UnitCost:       unitCost,
				Status:         1,
				SourceType:     1,
				RefNo:          order.InNo,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			if err := tx.Create(batch).Error; err != nil {
				return err
			}

			// 回写 SKU 汇总库存 + 移动加权平均成本
			before := sku.Stock
			sku.Stock += item.Quantity
			sku.CostPrice = movingAverageCost(before, sku.CostPrice, item.Quantity, unitCost)
			if err := tx.Model(&model.Sku{}).Where("id = ?", sku.ID).Updates(map[string]interface{}{
				"stock":      sku.Stock,
				"cost_price": sku.CostPrice,
			}).Error; err != nil {
				return err
			}

			// 库存流水
			record := &model.StockRecord{
				SkuID:          sku.ID,
				CommunityID:    req.CommunityId,
				Type:           1,
				Quantity:       item.Quantity,
				BeforeStock:    before,
				AfterStock:     sku.Stock,
				BatchID:        batch.ID,
				BatchNo:        item.BatchNo,
				ProductionDate: batch.ProductionDate,
				ExpireDate:     batch.ExpireDate,
				RefNo:          order.InNo,
				OperatorID:     req.OperatorId,
				Remark:         req.Remark,
				CreatedAt:      now,
			}
			if err := tx.Create(record).Error; err != nil {
				return err
			}

			items = append(items, &model.StockInOrderItem{
				InOrderID:      order.ID,
				SkuID:          sku.ID,
				Quantity:       item.Quantity,
				UnitCost:       unitCost,
				BatchNo:        item.BatchNo,
				ProductionDate: batch.ProductionDate,
				ExpireDate:     batch.ExpireDate,
				BatchID:        batch.ID,
				CreatedAt:      now,
			})
		}
		if err := tx.Create(&items).Error; err != nil {
			return err
		}

		// 关联采购单：回写已入库数量与采购单状态
		if req.PurchaseOrderId > 0 {
			if err := s.receivePurchaseOrder(tx, req.PurchaseOrderId, req.Items); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return order, items, nil
}

// receivePurchaseOrder 回写采购单已入库数量与状态（调用方须已锁采购单头并校验状态）
// 参数：
//   - tx: 事务
//   - purchaseOrderID: 采购单 ID
//   - items: 本次入库明细
//
// 返回值：
//   - error: 超量入库或更新失败
func (s *StockBillService) receivePurchaseOrder(tx *gorm.DB, purchaseOrderID int64, items []*product.StockInOrderItemReq) error {
	var poItems []model.PurchaseOrderItem
	if err := tx.Clauses(lockClause()).Where("purchase_order_id = ?", purchaseOrderID).Find(&poItems).Error; err != nil {
		return err
	}

	// 按 SKU 聚合本次入库量
	inQty := make(map[int64]int32, len(items))
	for _, item := range items {
		inQty[item.SkuId] += item.Quantity
	}

	bySku := make(map[int64]*model.PurchaseOrderItem, len(poItems))
	for i := range poItems {
		bySku[poItems[i].SkuID] = &poItems[i]
	}
	for skuID, qty := range inQty {
		poItem, ok := bySku[skuID]
		if !ok {
			return invalidArg("SKU %d 不在采购单明细中", skuID)
		}
		if poItem.ReceivedQuantity+qty > poItem.Quantity {
			return invalidArg("SKU %d 入库数量超出采购单剩余未收数量", skuID)
		}
		poItem.ReceivedQuantity += qty
		if err := tx.Model(&model.PurchaseOrderItem{}).Where("id = ?", poItem.ID).
			Update("received_quantity", poItem.ReceivedQuantity).Error; err != nil {
			return err
		}
	}

	// 重算采购单状态：全部收满 → 已完结；部分入库 → 部分入库
	allReceived, anyReceived := true, false
	for i := range poItems {
		if poItems[i].ReceivedQuantity > 0 {
			anyReceived = true
		}
		if poItems[i].ReceivedQuantity < poItems[i].Quantity {
			allReceived = false
		}
	}
	newStatus := model.PurchaseStatusPartial
	if allReceived {
		newStatus = model.PurchaseStatusCompleted
	} else if !anyReceived {
		newStatus = model.PurchaseStatusOrdered
	}
	return tx.Model(&model.PurchaseOrder{}).Where("id = ?", purchaseOrderID).
		Updates(map[string]interface{}{
			"status":     newStatus,
			"updated_at": time.Now().Format("2006-01-02 15:04:05"),
		}).Error
}

// ListStockInOrders 查询入库单列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.StockInOrder: 入库单列表
//   - int: 总条数
//   - error: 错误信息
func (s *StockBillService) ListStockInOrders(ctx context.Context, req *product.ListStockInOrdersReq) ([]*model.StockInOrder, int, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.StockInOrder{})
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	if req.PurchaseOrderId > 0 {
		db = db.Where("purchase_order_id = ?", req.PurchaseOrderId)
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
	var list []*model.StockInOrder
	err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// GetStockInOrder 查询入库单详情（含明细，供应商/采购单号/商品信息由本方法回填）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *product.StockInOrderDetail: 入库单详情
//   - error: 错误信息
func (s *StockBillService) GetStockInOrder(ctx context.Context, req *product.GetStockInOrderReq) (*product.StockInOrderDetail, error) {
	var order model.StockInOrder
	if err := s.db.GetDb().WithContext(ctx).First(&order, req.Id).Error; err != nil {
		return nil, err
	}

	var itemRows []model.StockInOrderItem
	if err := s.db.GetDb().WithContext(ctx).Where("in_order_id = ?", order.ID).Order("id asc").Find(&itemRows).Error; err != nil {
		return nil, err
	}

	// 回填供应商名/采购单号
	supplierName := ""
	if order.SupplierID > 0 {
		var supplier model.Supplier
		if err := s.db.GetDb().WithContext(ctx).Select("name").First(&supplier, order.SupplierID).Error; err == nil {
			supplierName = supplier.Name
		}
	}
	purchaseNo := ""
	if order.PurchaseOrderID > 0 {
		var po model.PurchaseOrder
		if err := s.db.GetDb().WithContext(ctx).Select("purchase_no").First(&po, order.PurchaseOrderID).Error; err == nil {
			purchaseNo = po.PurchaseNo
		}
	}

	// 回填 SKU/商品信息
	skuIDs := make([]int64, 0, len(itemRows))
	for _, it := range itemRows {
		skuIDs = append(skuIDs, it.SkuID)
	}
	skuInfo := s.getSkuInfoMap(ctx, skuIDs)

	info := &product.StockInOrderInfo{
		Id:              order.ID,
		InNo:            order.InNo,
		CommunityId:     order.CommunityID,
		SupplierId:      order.SupplierID,
		SupplierName:    supplierName,
		PurchaseOrderId: order.PurchaseOrderID,
		PurchaseNo:      purchaseNo,
		Type:            order.Type,
		TotalAmount:     order.TotalAmount,
		OperatorId:      order.OperatorID,
		Remark:          order.Remark,
		CreatedAt:       order.CreatedAt,
	}
	items := make([]*product.StockInOrderItemInfo, 0, len(itemRows))
	for _, it := range itemRows {
		info := skuInfo[it.SkuID]
		items = append(items, &product.StockInOrderItemInfo{
			Id:             it.ID,
			SkuId:          it.SkuID,
			SkuCode:        info.skuCode,
			ProductName:    info.productName,
			SpecName:       info.specName,
			Quantity:       it.Quantity,
			UnitCost:       it.UnitCost,
			BatchNo:        it.BatchNo,
			ProductionDate: ptrString(it.ProductionDate),
			ExpireDate:     ptrString(it.ExpireDate),
			BatchId:        it.BatchID,
		})
	}
	return &product.StockInOrderDetail{Info: info, Items: items}, nil
}

// CreateStockOutOrder 创建出库单（报损/领用/调整，类型后端枚举校验）
// 事务内完成：FEFO 批次扣减 → 回写 SKU 汇总库存 → 落库存流水（type=2，ref_no=出库单号）。
//
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.StockOutOrder: 出库单模型
//   - []*model.StockOutOrderItem: 出库单明细
//   - error: 错误信息
func (s *StockBillService) CreateStockOutOrder(ctx context.Context, req *product.CreateStockOutOrderReq) (*model.StockOutOrder, []*model.StockOutOrderItem, error) {
	if req.Type != model.StockOutTypeLoss && req.Type != model.StockOutTypeUse && req.Type != model.StockOutTypeAdjust {
		return nil, nil, invalidArg("出库类型不合法（仅支持报损/领用/调整）")
	}
	if len(req.Items) == 0 {
		return nil, nil, invalidArg("出库明细不能为空")
	}
	for _, item := range req.Items {
		if item.Quantity <= 0 {
			return nil, nil, invalidArg("出库数量必须大于 0")
		}
	}

	var (
		order *model.StockOutOrder
		items []*model.StockOutOrderItem
	)
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")
		order = &model.StockOutOrder{
			CommunityID: req.CommunityId,
			OutNo:       generateOutNo(),
			Type:        req.Type,
			OperatorID:  req.OperatorId,
			Remark:      req.Remark,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(order).Error; err != nil {
			return err
		}

		items = make([]*model.StockOutOrderItem, 0, len(req.Items))
		for _, item := range req.Items {
			var sku model.Sku
			if err := tx.Clauses(lockClause()).First(&sku, item.SkuId).Error; err != nil {
				return invalidArg("SKU %d 不存在", item.SkuId)
			}
			if sku.CommunityID != req.CommunityId {
				return invalidArg("SKU %d 不属于当前社区", item.SkuId)
			}
			if sku.Stock < item.Quantity {
				return bizerr.New(codes.ResourceExhausted, fmt.Sprintf("商品 %s 库存不足（当前 %d）", sku.SkuCode, sku.Stock))
			}

			before := sku.Stock
			takes, err := deductBatches(tx, &sku, item.Quantity, now)
			if err != nil {
				return err
			}
			if err := tx.Model(&model.Sku{}).Where("id = ?", sku.ID).
				Update("stock", sku.Stock).Error; err != nil {
				return err
			}

			// 逐批次落流水
			running := before
			for _, t := range takes {
				running -= t.Take
				record := &model.StockRecord{
					SkuID:          sku.ID,
					CommunityID:    req.CommunityId,
					Type:           2,
					Quantity:       -t.Take,
					BeforeStock:    running + t.Take,
					AfterStock:     running,
					BatchID:        t.Batch.ID,
					BatchNo:        t.Batch.BatchNo,
					ProductionDate: t.Batch.ProductionDate,
					ExpireDate:     t.Batch.ExpireDate,
					RefNo:          order.OutNo,
					OperatorID:     req.OperatorId,
					Remark:         req.Remark,
					CreatedAt:      now,
				}
				if err := tx.Create(record).Error; err != nil {
					return err
				}
			}

			items = append(items, &model.StockOutOrderItem{
				OutOrderID: order.ID,
				SkuID:      sku.ID,
				Quantity:   item.Quantity,
				CreatedAt:  now,
			})
		}
		return tx.Create(&items).Error
	})
	if err != nil {
		return nil, nil, err
	}
	return order, items, nil
}

// ListStockOutOrders 查询出库单列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.StockOutOrder: 出库单列表
//   - int: 总条数
//   - error: 错误信息
func (s *StockBillService) ListStockOutOrders(ctx context.Context, req *product.ListStockOutOrdersReq) ([]*model.StockOutOrder, int, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.StockOutOrder{})
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
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
	var list []*model.StockOutOrder
	err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// GetStockOutOrder 查询出库单详情（含明细）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *product.StockOutOrderDetail: 出库单详情
//   - error: 错误信息
func (s *StockBillService) GetStockOutOrder(ctx context.Context, req *product.GetStockOutOrderReq) (*product.StockOutOrderDetail, error) {
	var order model.StockOutOrder
	if err := s.db.GetDb().WithContext(ctx).First(&order, req.Id).Error; err != nil {
		return nil, err
	}

	var itemRows []model.StockOutOrderItem
	if err := s.db.GetDb().WithContext(ctx).Where("out_order_id = ?", order.ID).Order("id asc").Find(&itemRows).Error; err != nil {
		return nil, err
	}

	skuIDs := make([]int64, 0, len(itemRows))
	for _, it := range itemRows {
		skuIDs = append(skuIDs, it.SkuID)
	}
	skuInfo := s.getSkuInfoMap(ctx, skuIDs)

	items := make([]*product.StockOutOrderItemInfo, 0, len(itemRows))
	for _, it := range itemRows {
		info := skuInfo[it.SkuID]
		items = append(items, &product.StockOutOrderItemInfo{
			Id:          it.ID,
			SkuId:       it.SkuID,
			SkuCode:     info.skuCode,
			ProductName: info.productName,
			SpecName:    info.specName,
			Quantity:    it.Quantity,
		})
	}
	return &product.StockOutOrderDetail{
		Info: &product.StockOutOrderInfo{
			Id:          order.ID,
			OutNo:       order.OutNo,
			CommunityId: order.CommunityID,
			Type:        order.Type,
			OperatorId:  order.OperatorID,
			Remark:      order.Remark,
			CreatedAt:   order.CreatedAt,
		},
		Items: items,
	}, nil
}

// ListStockBatches 查询批次库存
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *product.ListStockBatchesResp: 批次列表（含商品信息回填）
//   - error: 错误信息
func (s *StockBillService) ListStockBatches(ctx context.Context, req *product.ListStockBatchesReq) (*product.ListStockBatchesResp, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.StockBatch{})
	if req.CommunityId > 0 {
		db = db.Where("stock_batches.community_id = ?", req.CommunityId)
	}
	if req.SkuId > 0 {
		db = db.Where("stock_batches.sku_id = ?", req.SkuId)
	}
	if req.InStockOnly {
		db = db.Where("stock_batches.quantity > 0")
	}
	if req.Keyword != "" {
		kw := "%" + req.Keyword + "%"
		db = db.Joins("JOIN product_skus s ON s.id = stock_batches.sku_id").
			Joins("JOIN products p ON p.id = s.product_id").
			Where("p.name LIKE ? OR s.sku_code LIKE ? OR stock_batches.batch_no LIKE ?", kw, kw, kw)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	var batches []model.StockBatch
	if err := db.Order("stock_batches.id desc").Offset(offset).Limit(pageSize).Find(&batches).Error; err != nil {
		return nil, err
	}

	skuIDs := make([]int64, 0, len(batches))
	for _, b := range batches {
		skuIDs = append(skuIDs, b.SkuID)
	}
	skuInfo := s.getSkuInfoMap(ctx, skuIDs)

	list := make([]*product.StockBatchInfo, 0, len(batches))
	for _, b := range batches {
		info := skuInfo[b.SkuID]
		list = append(list, &product.StockBatchInfo{
			Id:             b.ID,
			SkuId:          b.SkuID,
			SkuCode:        info.skuCode,
			ProductName:    info.productName,
			SpecName:       info.specName,
			BatchNo:        b.BatchNo,
			ProductionDate: ptrString(b.ProductionDate),
			ExpireDate:     ptrString(b.ExpireDate),
			Quantity:       b.Quantity,
			UnitCost:       b.UnitCost,
			Status:         b.Status,
			SourceType:     b.SourceType,
			RefNo:          b.RefNo,
			CreatedAt:      b.CreatedAt,
		})
	}
	return &product.ListStockBatchesResp{
		List: list,
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}, nil
}

// skuBrief SKU 展示信息（编码/规格名/商品名）
type skuBrief struct {
	skuCode     string
	specName    string
	productName string
}

// getSkuInfoMap 批量获取 SKU 展示信息
// 参数：
//   - ctx: 上下文
//   - skuIDs: SKU ID 列表
//
// 返回值：
//   - map[int64]skuBrief: SKU ID -> 展示信息
func (s *StockBillService) getSkuInfoMap(ctx context.Context, skuIDs []int64) map[int64]skuBrief {
	m := make(map[int64]skuBrief, len(skuIDs))
	if len(skuIDs) == 0 {
		return m
	}
	type row struct {
		SkuID       int64  `gorm:"column:sku_id"`
		SkuCode     string `gorm:"column:sku_code"`
		SpecName    string `gorm:"column:spec_name"`
		ProductName string `gorm:"column:product_name"`
	}
	var rows []row
	if err := s.db.GetDb().WithContext(ctx).Raw(`
		SELECT s.id AS sku_id, s.sku_code, s.spec_name, p.name AS product_name
		FROM product_skus s
		INNER JOIN products p ON p.id = s.product_id
		WHERE s.id IN ?
	`, skuIDs).Scan(&rows).Error; err != nil {
		return m
	}
	for _, r := range rows {
		m[r.SkuID] = skuBrief{skuCode: r.SkuCode, specName: r.SpecName, productName: r.ProductName}
	}
	return m
}

// 单号生成（与盘点单号同风格：前缀 + 时间戳 + 毫秒）
// generatePurchaseNo 定义在 purchase.go

// generateInNo 生成入库单号
// 返回值：
//   - string: 入库单号
func generateInNo() string {
	return "IN" + time.Now().Format("20060102150405") + fmt.Sprintf("%04d", time.Now().Nanosecond()/1e5)
}

// generateOutNo 生成出库单号
// 返回值：
//   - string: 出库单号
func generateOutNo() string {
	return "OUT" + time.Now().Format("20060102150405") + fmt.Sprintf("%04d", time.Now().Nanosecond()/1e5)
}
