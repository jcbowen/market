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

// SkuWithProduct SKU + 冗余商品名（SearchSkus join 查询结果载体）
type SkuWithProduct struct {
	model.Sku
	ProductName string `gorm:"column:product_name" json:"product_name"`
}

// SearchSkus 社区内 SKU 级搜索（join products）
// keyword 匹配商品名 / SKU 编码 / 条形码（LIKE 模糊）；keyword 为空时返回全量（分页）。
// 仅含启用 SKU 与未删除商品（products.status=2 为软删）。
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//   - keyword: 关键字（可为空）
//   - page: 页码（从 1 开始）
//   - pageSize: 每页条数
//
// 返回值：
//   - []*SkuWithProduct: SKU 列表（含商品名）
//   - int64: 总数
//   - error: 错误信息
func (s *SkuService) SearchSkus(ctx context.Context, communityID int64, keyword string, page, pageSize int) ([]*SkuWithProduct, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}

	query := s.db.GetDb().WithContext(ctx).
		Table("product_skus AS s").
		Joins("JOIN products AS p ON p.id = s.product_id AND p.status <> 2").
		Where("s.community_id = ?", communityID).
		Where("s.status = ?", 1)
	if kw := strings.TrimSpace(keyword); kw != "" {
		like := "%" + kw + "%"
		query = query.Where("(p.name LIKE ? OR s.sku_code LIKE ? OR s.barcode LIKE ?)", like, like, like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []*SkuWithProduct
	err := query.
		Select("s.*, p.name AS product_name").
		Order("p.id DESC, s.id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
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
