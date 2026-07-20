package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// ProductService 商品业务服务
type ProductService struct {
	db *mysql.Instance
}

// NewProductService 创建商品业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *ProductService: 商品业务服务实例
func NewProductService(db *mysql.Instance) *ProductService {
	return &ProductService{db: db}
}

// ListProducts 分页查询商品列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.Product: 商品列表
//   - int: 总条数
//   - error: 错误信息
func (s *ProductService) ListProducts(ctx context.Context, req *product.ListProductsReq) ([]*model.Product, int, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.Product{}).Where("status != ?", 2)
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	if req.CategoryId > 0 {
		db = db.Where("category_id = ?", req.CategoryId)
	}
	if req.Keyword != "" {
		db = db.Where("name like ?", "%"+req.Keyword+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var list []*model.Product
	err := db.Order("sort desc, id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// BuildProductExtras 批量查询商品列表的分类名称与默认 SKU 价格/库存
// 默认 SKU 取 status=1 且 id 最小的 SKU
//
// 参数：
//   - ctx: 上下文
//   - products: 商品模型列表
//
// 返回值：
//   - map[int64]string: 分类 ID -> 分类名称
//   - map[int64]float64: 商品 ID -> 默认 SKU 售价
//   - map[int64]int32: 商品 ID -> 默认 SKU 库存
//   - error: 错误信息
func (s *ProductService) BuildProductExtras(ctx context.Context, products []*model.Product) (map[int64]string, map[int64]float64, map[int64]int32, error) {
	categoryNames := make(map[int64]string)
	prices := make(map[int64]float64)
	stocks := make(map[int64]int32)
	if len(products) == 0 {
		return categoryNames, prices, stocks, nil
	}

	categoryIDs := make([]int64, 0, len(products))
	productIDs := make([]int64, 0, len(products))
	seenCategory := make(map[int64]struct{})
	for _, p := range products {
		productIDs = append(productIDs, p.ID)
		if _, ok := seenCategory[p.CategoryID]; !ok && p.CategoryID > 0 {
			seenCategory[p.CategoryID] = struct{}{}
			categoryIDs = append(categoryIDs, p.CategoryID)
		}
	}

	if len(categoryIDs) > 0 {
		var categories []*model.Category
		if err := s.db.GetDb().WithContext(ctx).Where("id IN ?", categoryIDs).Find(&categories).Error; err != nil {
			return nil, nil, nil, err
		}
		for _, c := range categories {
			categoryNames[c.ID] = c.Name
		}
	}

	if len(productIDs) > 0 {
		var skus []*model.Sku
		if err := s.db.GetDb().WithContext(ctx).
			Where("product_id IN ?", productIDs).
			Where("status = ?", 1).
			Order("id asc").
			Find(&skus).Error; err != nil {
			return nil, nil, nil, err
		}
		for _, sku := range skus {
			if _, ok := prices[sku.ProductID]; !ok {
				prices[sku.ProductID] = sku.Price
				stocks[sku.ProductID] = sku.Stock
			}
		}
	}

	return categoryNames, prices, stocks, nil
}

// GetProduct 根据 ID 查询商品
// 参数：
//   - ctx: 上下文
//   - id: 商品 ID
//
// 返回值：
//   - *model.Product: 商品模型
//   - error: 错误信息
func (s *ProductService) GetProduct(ctx context.Context, id int64) (*model.Product, error) {
	var p model.Product
	err := s.db.GetDb().WithContext(ctx).First(&p, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("商品不存在")
	}
	return &p, err
}

// CreateProduct 创建商品
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.Product: 商品模型
//   - error: 错误信息
func (s *ProductService) CreateProduct(ctx context.Context, req *product.CreateProductReq) (*model.Product, error) {
	imagesJSON, _ := json.Marshal(req.Images)
	p := &model.Product{
		CommunityID: req.CommunityId,
		CategoryID:  req.CategoryId,
		Name:        req.Name,
		Description: req.Description,
		CoverImage:  req.CoverImage,
		Images:      string(imagesJSON),
		Unit:        req.Unit,
		IsMultiSpec: req.IsMultiSpec,
		MinBuy:      req.MinBuy,
		Status:      1,
		Sort:        req.Sort,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(p).Error; err != nil {
		return nil, err
	}
	return p, nil
}

// UpdateProduct 更新商品
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *model.Product: 商品模型
//   - error: 错误信息
func (s *ProductService) UpdateProduct(ctx context.Context, req *product.UpdateProductReq) (*model.Product, error) {
	p, err := s.GetProduct(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	if req.CategoryId > 0 {
		p.CategoryID = req.CategoryId
	}
	if req.Name != "" {
		p.Name = req.Name
	}
	if req.Description != "" {
		p.Description = req.Description
	}
	if req.CoverImage != "" {
		p.CoverImage = req.CoverImage
	}
	if len(req.Images) > 0 {
		imagesJSON, _ := json.Marshal(req.Images)
		p.Images = string(imagesJSON)
	}
	if req.Unit != "" {
		p.Unit = req.Unit
	}
	if req.Sort > 0 {
		p.Sort = req.Sort
	}
	if req.Status != 0 {
		p.Status = req.Status
	}
	if req.MinBuy > 0 {
		p.MinBuy = req.MinBuy
	}

	if err := s.db.GetDb().WithContext(ctx).Save(p).Error; err != nil {
		return nil, err
	}
	return p, nil
}

// DeleteProduct 删除商品（软删除：状态置为 2）
// 参数：
//   - ctx: 上下文
//   - id: 商品 ID
//
// 返回值：
//   - error: 错误信息
func (s *ProductService) DeleteProduct(ctx context.Context, id int64) error {
	return s.db.GetDb().WithContext(ctx).
		Model(&model.Product{}).
		Where("id = ?", id).
		Update("status", 2).Error
}

// UpdateProductStatus 更新商品状态
// 参数：
//   - ctx: 上下文
//   - id: 商品 ID
//   - status: 状态
//
// 返回值：
//   - error: 错误信息
func (s *ProductService) UpdateProductStatus(ctx context.Context, id int64, status int32) error {
	return s.db.GetDb().WithContext(ctx).
		Model(&model.Product{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// ListSkusByProductID 查询商品下的 SKU 列表
// 参数：
//   - ctx: 上下文
//   - productID: 商品 ID
//
// 返回值：
//   - []*model.Sku: SKU 列表
//   - error: 错误信息
func (s *ProductService) ListSkusByProductID(ctx context.Context, productID int64) ([]*model.Sku, error) {
	var list []*model.Sku
	err := s.db.GetDb().WithContext(ctx).
		Where("product_id = ?", productID).
		Where("status = ?", 1).
		Order("id asc").
		Find(&list).Error
	return list, err
}

// ToProductInfo 将商品模型转换为 proto 商品信息
// 参数：
//   - p: 商品模型
//
// 返回值：
//   - *product.ProductInfo: proto 商品信息
func ToProductInfo(p *model.Product) *product.ProductInfo {
	return ToProductInfoWithExtra(p, "", 0, 0)
}

// ToProductInfoWithExtra 将商品模型转换为 proto 商品信息，并附加分类名称与默认 SKU 价格/库存
//
// 参数：
//   - p: 商品模型
//   - categoryName: 分类名称
//   - price: 默认 SKU 售价
//   - stock: 默认 SKU 库存
//
// 返回值：
//   - *product.ProductInfo: proto 商品信息
func ToProductInfoWithExtra(p *model.Product, categoryName string, price float64, stock int32) *product.ProductInfo {
	if p == nil {
		return nil
	}
	var images []string
	_ = json.Unmarshal([]byte(p.Images), &images)
	return &product.ProductInfo{
		Id:           p.ID,
		CommunityId:  p.CommunityID,
		CategoryId:   p.CategoryID,
		Name:         p.Name,
		Description:  p.Description,
		CoverImage:   p.CoverImage,
		Images:       images,
		Unit:         p.Unit,
		IsMultiSpec:  p.IsMultiSpec,
		MinBuy:       p.MinBuy,
		Status:       p.Status,
		Sort:         p.Sort,
		SalesCount:   p.SalesCount,
		Price:        price,
		Stock:        stock,
		CategoryName: categoryName,
	}
}
