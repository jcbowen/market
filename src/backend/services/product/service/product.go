package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

	db := s.db.GetDb().WithContext(ctx).Model(&model.Product{})
	if req.Status > 0 {
		db = db.Where("status = ?", req.Status)
	} else {
		db = db.Where("status != ?", 2)
	}
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

// BuildCategoryNames 批量查询商品列表的分类名称
//
// 参数：
//   - ctx: 上下文
//   - products: 商品模型列表
//
// 返回值：
//   - map[int64]string: 分类 ID -> 分类名称
//   - error: 错误信息
func (s *ProductService) BuildCategoryNames(ctx context.Context, products []*model.Product) (map[int64]string, error) {
	categoryNames := make(map[int64]string)
	if len(products) == 0 {
		return categoryNames, nil
	}

	categoryIDs := make([]int64, 0, len(products))
	seenCategory := make(map[int64]struct{})
	for _, p := range products {
		if _, ok := seenCategory[p.CategoryID]; !ok && p.CategoryID > 0 {
			seenCategory[p.CategoryID] = struct{}{}
			categoryIDs = append(categoryIDs, p.CategoryID)
		}
	}
	if len(categoryIDs) == 0 {
		return categoryNames, nil
	}

	var categories []*model.Category
	if err := s.db.GetDb().WithContext(ctx).Where("id IN ?", categoryIDs).Find(&categories).Error; err != nil {
		return nil, err
	}
	for _, c := range categories {
		categoryNames[c.ID] = c.Name
	}
	return categoryNames, nil
}

// BuildProductImages 批量查询商品对应的图片 URL 列表
// 返回结果中的图片按 `sort` 升序、`id` 升序排列。
//
// 参数：
//   - ctx: 上下文
//   - productIDs: 商品 ID 列表
//
// 返回值：
//   - map[int64][]string: 商品 ID -> 图片 URL 列表
//   - error: 错误信息
func (s *ProductService) BuildProductImages(ctx context.Context, productIDs []int64) (map[int64][]string, error) {
	imageMap := make(map[int64][]string)
	if len(productIDs) == 0 {
		return imageMap, nil
	}

	var images []model.ProductImage
	if err := s.db.GetDb().WithContext(ctx).
		Where("product_id IN ?", productIDs).
		Order("sort asc, id asc").
		Find(&images).Error; err != nil {
		return nil, err
	}

	for _, img := range images {
		imageMap[img.ProductID] = append(imageMap[img.ProductID], img.URL)
	}
	return imageMap, nil
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
// 创建商品主记录的同时，将请求中的图片 URL 按顺序写入 `product_images` 表，
// 并继续保留 `products.images` JSON 字段作为过渡兼容。
// 规格树与 SKU 组合由 saveSpecsAndSkus 在同一事务内保存：
// 单规格维护唯一默认 SKU，多规格按规格树全量落库并回写汇总字段。
//
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.Product: 商品模型
//   - error: 错误信息
func (s *ProductService) CreateProduct(ctx context.Context, req *product.CreateProductReq) (*model.Product, error) {
	if len(req.SkuItems) == 0 {
		return nil, fmt.Errorf("请填写商品价格与库存")
	}

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

	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(p).Error; err != nil {
			return err
		}
		if len(req.Images) > 0 {
			if err := s.saveProductImages(tx, p.ID, req.Images); err != nil {
				return err
			}
		}
		return s.saveSpecsAndSkus(tx, p, req.Specs, req.SkuItems)
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// UpdateProduct 更新商品
// 请求 images 非空时，删除该商品在 `product_images` 表中的全部旧记录并按新顺序重新插入，
// 同时同步更新 `products.images` JSON 字段；images 为空数组时保留现有图片不变更。
// 规格树与 SKU 组合由 saveSpecsAndSkus 在同一事务内全量保存（含 diff 软删除）。
//
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
	p.IsMultiSpec = req.IsMultiSpec

	// images 为空数组时视为"不变更"，保留商品现有图片，避免编辑接口意外清空图片
	if len(req.Images) > 0 {
		imagesJSON, _ := json.Marshal(req.Images)
		p.Images = string(imagesJSON)
	}

	err = s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(p).Error; err != nil {
			return err
		}
		if len(req.Images) > 0 {
			if err := tx.Where("product_id = ?", req.Id).Delete(&model.ProductImage{}).Error; err != nil {
				return err
			}
			if err := s.saveProductImages(tx, req.Id, req.Images); err != nil {
				return err
			}
		}
		return s.saveSpecsAndSkus(tx, p, req.Specs, req.SkuItems)
	})
	if err != nil {
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

// ListProductImages 查询指定商品的图片记录
// 返回结果按 `sort` 升序、`id` 升序排列。
//
// 参数：
//   - ctx: 上下文
//   - productID: 商品 ID
//
// 返回值：
//   - []model.ProductImage: 商品图片记录列表
//   - error: 错误信息
func (s *ProductService) ListProductImages(ctx context.Context, productID int64) ([]model.ProductImage, error) {
	var list []model.ProductImage
	err := s.db.GetDb().WithContext(ctx).
		Where("product_id = ?", productID).
		Order("sort asc, id asc").
		Find(&list).Error
	return list, err
}

// saveProductImages 批量保存商品图片记录
// 图片按传入顺序设置 `sort`，从 0 开始递增。
//
// 参数：
//   - tx: GORM 事务对象
//   - productID: 商品 ID
//   - images: 图片 URL 列表
//
// 返回值：
//   - error: 错误信息
func (s *ProductService) saveProductImages(tx *gorm.DB, productID int64, images []string) error {
	// 批量插入时基类 BeforeCreate 钩子（SetFieldIfExist）不作用于切片，需显式填充时间，
	// 否则 created_at 以空串写入 strict 模式的 datetime 列会报 Error 1292
	now := time.Now().Format("2006-01-02 15:04:05")
	records := make([]model.ProductImage, 0, len(images))
	for i, url := range images {
		records = append(records, model.ProductImage{
			ProductID: productID,
			URL:       url,
			Sort:      int32(i),
			CreatedAt: now,
		})
	}
	return tx.Create(&records).Error
}

// ToProductInfo 将商品模型转换为 proto 商品信息
// 价格/库存取自商品表汇总字段（由规格保存流程回写维护）
//
// 参数：
//   - p: 商品模型
//   - categoryName: 分类名称
//   - images: 商品图片 URL 列表
//
// 返回值：
//   - *product.ProductInfo: proto 商品信息
func ToProductInfo(p *model.Product, categoryName string, images []string) *product.ProductInfo {
	if p == nil {
		return nil
	}
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
		Price:        p.MinPrice,
		Stock:        p.Stock,
		MinPrice:     p.MinPrice,
		MaxPrice:     p.MaxPrice,
		CategoryName: categoryName,
	}
}
