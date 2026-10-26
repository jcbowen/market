package service

import (
	"context"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// PurchaseService 采购单业务服务
type PurchaseService struct {
	db *mysql.Instance
}

// NewPurchaseService 创建采购单业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *PurchaseService: 服务实例
func NewPurchaseService(db *mysql.Instance) *PurchaseService {
	return &PurchaseService{db: db}
}

// generatePurchaseNo 生成采购单号
// 返回值：
//   - string: 采购单号
func generatePurchaseNo() string {
	return generateBillNo("PO")
}

// CreatePurchaseOrder 创建采购单
// 默认落草稿态；submit_now=true 时创建后直接下单（跳过草稿）。
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *product.PurchaseOrderDetail: 采购单详情
//   - error: 错误信息
func (s *PurchaseService) CreatePurchaseOrder(ctx context.Context, req *product.CreatePurchaseOrderReq) (*product.PurchaseOrderDetail, error) {
	if err := validatePurchaseItems(req.Items); err != nil {
		return nil, err
	}

	var order *model.PurchaseOrder
	var items []model.PurchaseOrderItem
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")
		status := model.PurchaseStatusDraft
		if req.SubmitNow {
			status = model.PurchaseStatusOrdered
		}

		var totalAmount float64
		for _, item := range req.Items {
			totalAmount += float64(item.Quantity) * item.PurchasePrice
		}

		order = &model.PurchaseOrder{
			CommunityID: req.CommunityId,
			PurchaseNo:  generatePurchaseNo(),
			SupplierID:  req.SupplierId,
			Status:      status,
			TotalAmount: round2(totalAmount),
			OperatorID:  req.OperatorId,
			Remark:      req.Remark,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(order).Error; err != nil {
			return err
		}

		items = make([]model.PurchaseOrderItem, 0, len(req.Items))
		for _, item := range req.Items {
			items = append(items, model.PurchaseOrderItem{
				PurchaseOrderID: order.ID,
				SkuID:           item.SkuId,
				Quantity:        item.Quantity,
				PurchasePrice:   item.PurchasePrice,
				CreatedAt:       now,
			})
		}
		return tx.Create(&items).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetPurchaseOrder(ctx, &product.GetPurchaseOrderReq{Id: order.ID})
}

// UpdatePurchaseOrder 更新采购单（仅草稿态；明细全量替换）
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *product.PurchaseOrderDetail: 采购单详情
//   - error: 错误信息
func (s *PurchaseService) UpdatePurchaseOrder(ctx context.Context, req *product.UpdatePurchaseOrderReq) (*product.PurchaseOrderDetail, error) {
	if err := validatePurchaseItems(req.Items); err != nil {
		return nil, err
	}

	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var po model.PurchaseOrder
		if err := tx.Clauses(lockClause()).First(&po, req.Id).Error; err != nil {
			return invalidArg("采购单不存在")
		}
		if req.CommunityId > 0 && po.CommunityID != req.CommunityId {
			return invalidArg("采购单不属于当前社区")
		}
		if po.Status != model.PurchaseStatusDraft {
			return invalidArg("仅草稿态采购单允许修改")
		}

		var totalAmount float64
		for _, item := range req.Items {
			totalAmount += float64(item.Quantity) * item.PurchasePrice
		}
		now := time.Now().Format("2006-01-02 15:04:05")
		if err := tx.Model(&model.PurchaseOrder{}).Where("id = ?", po.ID).Updates(map[string]interface{}{
			"supplier_id":  req.SupplierId,
			"remark":       req.Remark,
			"total_amount": round2(totalAmount),
			"updated_at":   now,
		}).Error; err != nil {
			return err
		}
		// 明细全量替换
		if err := tx.Where("purchase_order_id = ?", po.ID).Delete(&model.PurchaseOrderItem{}).Error; err != nil {
			return err
		}
		items := make([]model.PurchaseOrderItem, 0, len(req.Items))
		for _, item := range req.Items {
			items = append(items, model.PurchaseOrderItem{
				PurchaseOrderID: po.ID,
				SkuID:           item.SkuId,
				Quantity:        item.Quantity,
				PurchasePrice:   item.PurchasePrice,
				CreatedAt:       now,
			})
		}
		return tx.Create(&items).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetPurchaseOrder(ctx, &product.GetPurchaseOrderReq{Id: req.Id})
}

// SubmitPurchaseOrder 提交采购单（草稿 → 已下单）
// 参数：
//   - ctx: 上下文
//   - req: 提交请求
//
// 返回值：
//   - error: 错误信息
func (s *PurchaseService) SubmitPurchaseOrder(ctx context.Context, req *product.SubmitPurchaseOrderReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var po model.PurchaseOrder
		if err := tx.Clauses(lockClause()).First(&po, req.Id).Error; err != nil {
			return invalidArg("采购单不存在")
		}
		if req.CommunityId > 0 && po.CommunityID != req.CommunityId {
			return invalidArg("采购单不属于当前社区")
		}
		if po.Status != model.PurchaseStatusDraft {
			return invalidArg("仅草稿态采购单允许下单")
		}
		return tx.Model(&model.PurchaseOrder{}).Where("id = ?", po.ID).Updates(map[string]interface{}{
			"status":     model.PurchaseStatusOrdered,
			"updated_at": time.Now().Format("2006-01-02 15:04:05"),
		}).Error
	})
}

// CancelPurchaseOrder 取消采购单（草稿/已下单可取消；已产生入库的不允许）
// 参数：
//   - ctx: 上下文
//   - req: 取消请求
//
// 返回值：
//   - error: 错误信息
func (s *PurchaseService) CancelPurchaseOrder(ctx context.Context, req *product.CancelPurchaseOrderReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var po model.PurchaseOrder
		if err := tx.Clauses(lockClause()).First(&po, req.Id).Error; err != nil {
			return invalidArg("采购单不存在")
		}
		if req.CommunityId > 0 && po.CommunityID != req.CommunityId {
			return invalidArg("采购单不属于当前社区")
		}
		if po.Status != model.PurchaseStatusDraft && po.Status != model.PurchaseStatusOrdered {
			return invalidArg("采购单已产生入库或已完结，不能取消")
		}
		return tx.Model(&model.PurchaseOrder{}).Where("id = ?", po.ID).Updates(map[string]interface{}{
			"status":     model.PurchaseStatusCancelled,
			"updated_at": time.Now().Format("2006-01-02 15:04:05"),
		}).Error
	})
}

// ListPurchaseOrders 查询采购单列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *product.ListPurchaseOrdersResp: 分页列表
//   - error: 错误信息
func (s *PurchaseService) ListPurchaseOrders(ctx context.Context, req *product.ListPurchaseOrdersReq) (*product.ListPurchaseOrdersResp, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.PurchaseOrder{})
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	if req.Status > 0 {
		db = db.Where("status = ?", req.Status)
	}
	if req.SupplierId > 0 {
		db = db.Where("supplier_id = ?", req.SupplierId)
	}
	if req.Keyword != "" {
		db = db.Where("purchase_no LIKE ?", "%"+req.Keyword+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	offset := (page - 1) * pageSize
	var list []model.PurchaseOrder
	if err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, err
	}

	// 回填供应商名
	supplierIDs := make([]int64, 0, len(list))
	for _, po := range list {
		if po.SupplierID > 0 {
			supplierIDs = append(supplierIDs, po.SupplierID)
		}
	}
	supplierNames := s.getSupplierNames(ctx, supplierIDs)

	respList := make([]*product.PurchaseOrderInfo, 0, len(list))
	for i := range list {
		po := &list[i]
		respList = append(respList, &product.PurchaseOrderInfo{
			Id:           po.ID,
			PurchaseNo:   po.PurchaseNo,
			CommunityId:  po.CommunityID,
			SupplierId:   po.SupplierID,
			SupplierName: supplierNames[po.SupplierID],
			Status:       po.Status,
			TotalAmount:  po.TotalAmount,
			Remark:       po.Remark,
			OperatorId:   po.OperatorID,
			CreatedAt:    po.CreatedAt,
			UpdatedAt:    po.UpdatedAt,
		})
	}
	return &product.ListPurchaseOrdersResp{
		List: respList,
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}, nil
}

// GetPurchaseOrder 查询采购单详情（含明细）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *product.PurchaseOrderDetail: 采购单详情
//   - error: 错误信息
func (s *PurchaseService) GetPurchaseOrder(ctx context.Context, req *product.GetPurchaseOrderReq) (*product.PurchaseOrderDetail, error) {
	var po model.PurchaseOrder
	if err := s.db.GetDb().WithContext(ctx).First(&po, req.Id).Error; err != nil {
		return nil, invalidArg("采购单不存在")
	}

	var itemRows []model.PurchaseOrderItem
	if err := s.db.GetDb().WithContext(ctx).Where("purchase_order_id = ?", po.ID).Order("id asc").Find(&itemRows).Error; err != nil {
		return nil, err
	}

	// 回填 SKU/商品信息
	skuIDs := make([]int64, 0, len(itemRows))
	for _, it := range itemRows {
		skuIDs = append(skuIDs, it.SkuID)
	}
	skuInfo := s.getSkuInfoMap(ctx, skuIDs)

	supplierName := ""
	if po.SupplierID > 0 {
		var sp model.Supplier
		if err := s.db.GetDb().WithContext(ctx).Select("name").First(&sp, po.SupplierID).Error; err == nil {
			supplierName = sp.Name
		}
	}

	items := make([]*product.PurchaseOrderItemInfo, 0, len(itemRows))
	for _, it := range itemRows {
		info := skuInfo[it.SkuID]
		items = append(items, &product.PurchaseOrderItemInfo{
			Id:               it.ID,
			SkuId:            it.SkuID,
			SkuCode:          info.skuCode,
			SpecName:         info.specName,
			ProductName:      info.productName,
			Quantity:         it.Quantity,
			ReceivedQuantity: it.ReceivedQuantity,
			PurchasePrice:    it.PurchasePrice,
		})
	}
	return &product.PurchaseOrderDetail{
		Info: &product.PurchaseOrderInfo{
			Id:           po.ID,
			PurchaseNo:   po.PurchaseNo,
			CommunityId:  po.CommunityID,
			SupplierId:   po.SupplierID,
			SupplierName: supplierName,
			Status:       po.Status,
			TotalAmount:  po.TotalAmount,
			Remark:       po.Remark,
			OperatorId:   po.OperatorID,
			CreatedAt:    po.CreatedAt,
			UpdatedAt:    po.UpdatedAt,
		},
		Items: items,
	}, nil
}

// validatePurchaseItems 校验采购明细
// 参数：
//   - items: 明细列表
//
// 返回值：
//   - error: 校验失败原因
func validatePurchaseItems(items []*product.PurchaseOrderItemReq) error {
	if len(items) == 0 {
		return invalidArg("采购明细不能为空")
	}
	for _, item := range items {
		if item.SkuId <= 0 {
			return invalidArg("采购明细缺少 SKU")
		}
		if item.Quantity <= 0 {
			return invalidArg("采购数量必须大于 0")
		}
		if item.PurchasePrice < 0 {
			return invalidArg("采购单价不能为负数")
		}
	}
	return nil
}

// getSupplierNames 批量获取供应商名称
// 参数：
//   - ctx: 上下文
//   - supplierIDs: 供应商 ID 列表
//
// 返回值：
//   - map[int64]string: 供应商 ID -> 名称
func (s *PurchaseService) getSupplierNames(ctx context.Context, supplierIDs []int64) map[int64]string {
	m := make(map[int64]string, len(supplierIDs))
	if len(supplierIDs) == 0 {
		return m
	}
	var suppliers []model.Supplier
	if err := s.db.GetDb().WithContext(ctx).Select("id", "name").Where("id IN ?", supplierIDs).Find(&suppliers).Error; err != nil {
		return m
	}
	for _, sp := range suppliers {
		m[sp.ID] = sp.Name
	}
	return m
}

// getSkuInfoMap 复用 StockBillService 的 SKU 信息批量查询（同包内方法提升）
// 参数：
//   - ctx: 上下文
//   - skuIDs: SKU ID 列表
//
// 返回值：
//   - map[int64]skuBrief: SKU ID -> 展示信息
func (s *PurchaseService) getSkuInfoMap(ctx context.Context, skuIDs []int64) map[int64]skuBrief {
	billSvc := &StockBillService{db: s.db}
	return billSvc.getSkuInfoMap(ctx, skuIDs)
}
