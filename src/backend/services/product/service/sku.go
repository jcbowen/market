package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// SkuService SKU 业务服务
type SkuService struct {
	db *mysql.Instance
}

// NewSkuService 创建 SKU 业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *SkuService: SKU 业务服务实例
func NewSkuService(db *mysql.Instance) *SkuService {
	return &SkuService{db: db}
}

// ListSkus 根据商品 ID 查询 SKU 列表
// 参数：
//   - ctx: 上下文
//   - productID: 商品 ID
//
// 返回值：
//   - []*model.Sku: SKU 列表
//   - error: 错误信息
func (s *SkuService) ListSkus(ctx context.Context, productID int64) ([]*model.Sku, error) {
	var list []*model.Sku
	err := s.db.GetDb().WithContext(ctx).
		Where("product_id = ?", productID).
		Where("status = ?", 1).
		Order("id asc").
		Find(&list).Error
	return list, err
}

// GetSkuByID 根据 ID 查询 SKU
// 参数：
//   - ctx: 上下文
//   - id: SKU ID
//
// 返回值：
//   - *model.Sku: SKU 模型
//   - error: 错误信息
func (s *SkuService) GetSkuByID(ctx context.Context, id int64) (*model.Sku, error) {
	var sku model.Sku
	err := s.db.GetDb().WithContext(ctx).First(&sku, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("SKU 不存在")
	}
	return &sku, err
}

// CreateSku 创建 SKU
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.Sku: SKU 模型
//   - error: 错误信息
func (s *SkuService) CreateSku(ctx context.Context, req *product.CreateSkuReq) (*model.Sku, error) {
	// spec_json 为 JSON 列，空字符串非法，归一化为空对象
	specJSON := req.SpecJson
	if specJSON == "" {
		specJSON = "{}"
	}
	sku := &model.Sku{
		ProductID:   req.ProductId,
		CommunityID: req.CommunityId,
		SkuCode:     req.SkuCode,
		SpecName:    req.SpecName,
		SpecJSON:    specJSON,
		Price:       req.Price,
		CostPrice:   req.CostPrice,
		Stock:       req.Stock,
		SafetyStock: req.SafetyStock,
		Image:       req.Image,
		Barcode:     strings.TrimSpace(req.Barcode),
		Status:      1,
	}
	// 建品与初始批次同事务：批次库存是扣减的唯一来源，汇总库存必须有对应批次，
	// 批次落库失败时连同 SKU 一并回滚，避免产生"有汇总库存无批次"的不可售 SKU
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(sku).Error; err != nil {
			return err
		}
		if req.Stock > 0 {
			now := time.Now().Format("2006-01-02 15:04:05")
			if _, err := createInitBatch(tx, req.CommunityId, sku.ID, req.Stock, req.CostPrice, "INIT", now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return sku, nil
}

// UpdateSku 更新 SKU
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *model.Sku: SKU 模型
//   - error: 错误信息
func (s *SkuService) UpdateSku(ctx context.Context, req *product.UpdateSkuReq) (*model.Sku, error) {
	sku, err := s.GetSkuByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	if req.SpecName != "" {
		sku.SpecName = req.SpecName
	}
	if req.SpecJson != "" {
		sku.SpecJSON = req.SpecJson
	}
	if req.Price > 0 {
		sku.Price = req.Price
	}
	if req.CostPrice > 0 {
		sku.CostPrice = req.CostPrice
	}
	if req.SafetyStock > 0 {
		sku.SafetyStock = req.SafetyStock
	}
	if req.Image != "" {
		sku.Image = req.Image
	}
	if req.Status != 0 {
		sku.Status = req.Status
	}
	if req.Barcode != "" {
		sku.Barcode = strings.TrimSpace(req.Barcode)
	}

	if err := s.db.GetDb().WithContext(ctx).Save(sku).Error; err != nil {
		return nil, err
	}
	return sku, nil
}

// ToSkuInfo 将 SKU 模型转换为 proto SKU 信息
// 参数：
//   - sku: SKU 模型
//
// 返回值：
//   - *product.SkuInfo: proto SKU 信息
func ToSkuInfo(sku *model.Sku) *product.SkuInfo {
	if sku == nil {
		return nil
	}
	return &product.SkuInfo{
		Id:            sku.ID,
		ProductId:     sku.ProductID,
		CommunityId:   sku.CommunityID,
		SkuCode:       sku.SkuCode,
		SpecName:      sku.SpecName,
		SpecJson:      sku.SpecJSON,
		Price:         sku.Price,
		OriginalPrice: sku.OriginalPrice,
		CostPrice:     sku.CostPrice,
		Stock:         sku.Stock,
		SafetyStock:   sku.SafetyStock,
		Image:         sku.Image,
		Status:        sku.Status,
		SpecValueIds:  sku.SpecValueIDs,
		Barcode:       sku.Barcode,
	}
}
