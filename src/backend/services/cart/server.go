package main

import (
	stdctx "context"
	"errors"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/cart"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/cart/model"
)

// server 实现 cart.CartServiceServer 接口
type server struct {
	cart.UnimplementedCartServiceServer
	db            *mysql.Instance
	productClient product.ProductServiceClient
}

// newServer 创建购物车服务 gRPC 实现
// 参数：
//   - db: MySQL 实例
//   - productClient: 商品服务 gRPC 客户端
//
// 返回值：
//   - *server: gRPC 服务实现
func newServer(db *mysql.Instance, productClient product.ProductServiceClient) *server {
	return &server{db: db, productClient: productClient}
}

// GetCart 获取购物车列表
// 如果请求中携带 community_id，则只返回该社区下的购物车项
//
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *cart.CartResp: 购物车响应
//   - error: 错误信息
func (s *server) GetCart(ctx stdctx.Context, req *cart.GetCartReq) (*cart.CartResp, error) {
	items, err := s.getCartItems(ctx, req.UserId, req.CommunityId)
	if err != nil {
		return nil, err
	}
	return &cart.CartResp{Items: items}, nil
}

// AddItem 添加商品到购物车
// 若同一用户同一 SKU 已存在记录，则累加数量并置为选中；否则新建记录。
// 保持与 Redis 实现一致，仅校验 SKU/商品是否存在及数量大于 0，不校验 min_buy。
//
// 参数：
//   - ctx: 上下文
//   - req: 添加请求
//
// 返回值：
//   - *cart.CartResp: 购物车响应
//   - error: 错误信息
func (s *server) AddItem(ctx stdctx.Context, req *cart.AddItemReq) (*cart.CartResp, error) {
	if req.Quantity <= 0 {
		return nil, status.Errorf(codes.InvalidArgument, "数量必须大于0")
	}

	skuInfo, err := s.productClient.GetSkuById(ctx, &product.GetSkuByIdReq{Id: req.SkuId})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "查询 SKU 失败: %v", err)
	}
	if skuInfo.Id == 0 {
		return nil, status.Errorf(codes.NotFound, "SKU 不存在")
	}

	productInfo, err := s.productClient.GetProduct(ctx, &product.GetProductReq{Id: skuInfo.ProductId, CommunityId: skuInfo.CommunityId})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "查询商品失败: %v", err)
	}
	if productInfo.Product == nil || productInfo.Product.Id == 0 {
		return nil, status.Errorf(codes.NotFound, "商品不存在")
	}

	if err := s.upsertCartItem(ctx, req.UserId, req.CommunityId, skuInfo.ProductId, req.SkuId, req.Quantity); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	items, err := s.getCartItems(ctx, req.UserId, 0)
	if err != nil {
		return nil, err
	}
	return &cart.CartResp{Items: items}, nil
}

// UpdateItem 更新购物车项
// 按 user_id + sku_id 定位记录，sku_id 由 gateway 通过 req.Id 传入。
// 仅当 quantity > 0 时更新数量，始终更新 selected 状态。
//
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *cart.CartResp: 购物车响应
//   - error: 错误信息
func (s *server) UpdateItem(ctx stdctx.Context, req *cart.UpdateItemReq) (*cart.CartResp, error) {
	if err := s.updateCartItem(ctx, req.UserId, req.Id, req.Quantity, req.Selected); err != nil {
		return nil, err
	}

	items, err := s.getCartItems(ctx, req.UserId, 0)
	if err != nil {
		return nil, err
	}
	return &cart.CartResp{Items: items}, nil
}

// RemoveItem 移除购物车项
// 按 user_id + sku_id 删除记录，sku_id 由 gateway 通过 req.Id 传入。
//
// 参数：
//   - ctx: 上下文
//   - req: 删除请求
//
// 返回值：
//   - *cart.CartResp: 购物车响应
//   - error: 错误信息
func (s *server) RemoveItem(ctx stdctx.Context, req *cart.RemoveItemReq) (*cart.CartResp, error) {
	if err := s.db.GetDb().WithContext(ctx).
		Where("user_id = ? AND sku_id = ?", req.UserId, req.Id).
		Delete(&model.Cart{}).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	items, err := s.getCartItems(ctx, req.UserId, 0)
	if err != nil {
		return nil, err
	}
	return &cart.CartResp{Items: items}, nil
}

// SelectAll 全选/取消全选
// 更新当前用户全部购物车项的选中状态。
//
// 参数：
//   - ctx: 上下文
//   - req: 全选请求
//
// 返回值：
//   - *cart.CartResp: 购物车响应
//   - error: 错误信息
func (s *server) SelectAll(ctx stdctx.Context, req *cart.SelectAllReq) (*cart.CartResp, error) {
	now := time.Now().Format("2006-01-02 15:04:05")
	if err := s.db.GetDb().WithContext(ctx).Model(&model.Cart{}).
		Where("user_id = ?", req.UserId).
		Updates(map[string]interface{}{
			"selected":   req.Selected,
			"updated_at": now,
		}).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	items, err := s.getCartItems(ctx, req.UserId, 0)
	if err != nil {
		return nil, err
	}
	return &cart.CartResp{Items: items}, nil
}

// ClearCart 清空购物车
// 删除当前用户全部购物车记录。
//
// 参数：
//   - ctx: 上下文
//   - req: 清空请求
//
// 返回值：
//   - *common.CommonResponse: 通用响应
//   - error: 错误信息
func (s *server) ClearCart(ctx stdctx.Context, req *cart.ClearCartReq) (*common.CommonResponse, error) {
	if err := s.db.GetDb().WithContext(ctx).Where("user_id = ?", req.UserId).Delete(&model.Cart{}).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// GetSelectedItems 获取已选中的购物车项
//
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *cart.CartResp: 购物车响应
//   - error: 错误信息
func (s *server) GetSelectedItems(ctx stdctx.Context, req *cart.GetSelectedItemsReq) (*cart.CartResp, error) {
	var list []*model.Cart
	if err := s.db.GetDb().WithContext(ctx).
		Where("user_id = ? AND selected = ?", req.UserId, true).
		Order("id desc").
		Find(&list).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	items, err := s.buildCartItems(ctx, list)
	if err != nil {
		return nil, err
	}
	return &cart.CartResp{Items: items}, nil
}

// getCartItems 从 MySQL 读取购物车全部项并补齐商品展示信息
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - communityID: 社区 ID，0 表示不过滤
//
// 返回值：
//   - []*cart.CartItem: 购物车项列表
//   - error: 错误信息
func (s *server) getCartItems(ctx stdctx.Context, userID, communityID int64) ([]*cart.CartItem, error) {
	db := s.db.GetDb().WithContext(ctx).Where("user_id = ?", userID)
	if communityID > 0 {
		db = db.Where("community_id = ?", communityID)
	}

	var list []*model.Cart
	if err := db.Order("id desc").Find(&list).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return s.buildCartItems(ctx, list)
}

// buildCartItems 将购物车模型列表转换为响应项，并从商品服务补齐展示字段
//
// 参数：
//   - ctx: 上下文
//   - list: 购物车模型列表
//
// 返回值：
//   - []*cart.CartItem: 购物车响应项列表
//   - error: 错误信息
func (s *server) buildCartItems(ctx stdctx.Context, list []*model.Cart) ([]*cart.CartItem, error) {
	items := make([]*cart.CartItem, 0, len(list))
	for _, m := range list {
		item, err := s.buildCartItem(ctx, m)
		if err != nil {
			return nil, err
		}
		if item != nil {
			items = append(items, item)
		}
	}
	return items, nil
}

// buildCartItem 将单个购物车模型转换为响应项
// 若关联 SKU 或商品已不存在，则返回 nil 并跳过该项。
//
// 参数：
//   - ctx: 上下文
//   - m: 购物车模型
//
// 返回值：
//   - *cart.CartItem: 购物车响应项，SKU/商品不存在时返回 nil
//   - error: 错误信息
func (s *server) buildCartItem(ctx stdctx.Context, m *model.Cart) (*cart.CartItem, error) {
	skuInfo, err := s.productClient.GetSkuById(ctx, &product.GetSkuByIdReq{Id: m.SkuID})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "查询 SKU 失败: %v", err)
	}
	if skuInfo.Id == 0 {
		return nil, nil
	}

	productInfo, err := s.productClient.GetProduct(ctx, &product.GetProductReq{Id: skuInfo.ProductId, CommunityId: skuInfo.CommunityId})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "查询商品失败: %v", err)
	}
	if productInfo.Product == nil || productInfo.Product.Id == 0 {
		return nil, nil
	}

	productImage := skuInfo.Image
	if productImage == "" {
		productImage = productInfo.Product.CoverImage
	}

	return &cart.CartItem{
		Id:           m.SkuID,
		ProductId:    m.ProductID,
		SkuId:        m.SkuID,
		ProductName:  productInfo.Product.Name,
		SpecName:     skuInfo.SpecName,
		ProductImage: productImage,
		Price:        skuInfo.Price,
		Stock:        skuInfo.Stock,
		Quantity:     m.Quantity,
		Selected:     m.Selected,
		CommunityId:  m.CommunityID,
		MinBuy:       productInfo.Product.MinBuy,
	}, nil
}

// upsertCartItem 新增或更新购物车记录
// 同一用户同一 SKU 已存在时累加数量并置为选中，否则新建记录。
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - communityID: 社区 ID
//   - productID: 商品 ID
//   - skuID: SKU ID
//   - quantity: 新增数量
//
// 返回值：
//   - error: 错误信息
func (s *server) upsertCartItem(ctx stdctx.Context, userID, communityID, productID, skuID int64, quantity int32) error {
	now := time.Now().Format("2006-01-02 15:04:05")

	var cart model.Cart
	err := s.db.GetDb().WithContext(ctx).
		Where("user_id = ? AND sku_id = ?", userID, skuID).
		First(&cart).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			cart = model.Cart{
				UserID:      userID,
				CommunityID: communityID,
				ProductID:   productID,
				SkuID:       skuID,
				Quantity:    quantity,
				Selected:    true,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			return s.db.GetDb().WithContext(ctx).Create(&cart).Error
		}
		return err
	}

	cart.Quantity += quantity
	cart.Selected = true
	cart.UpdatedAt = now
	return s.db.GetDb().WithContext(ctx).Model(&model.Cart{}).
		Where("id = ?", cart.ID).
		Updates(map[string]interface{}{
			"quantity":   cart.Quantity,
			"selected":   true,
			"updated_at": now,
		}).Error
}

// updateCartItem 按 user_id + sku_id 更新购物车记录
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - skuID: SKU ID
//   - quantity: 数量，小于等于 0 时不更新
//   - selected: 选中状态
//
// 返回值：
//   - error: 错误信息
func (s *server) updateCartItem(ctx stdctx.Context, userID, skuID int64, quantity int32, selected bool) error {
	now := time.Now().Format("2006-01-02 15:04:05")

	var cart model.Cart
	if err := s.db.GetDb().WithContext(ctx).
		Where("user_id = ? AND sku_id = ?", userID, skuID).
		First(&cart).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return status.Errorf(codes.NotFound, "购物车项不存在")
		}
		return status.Errorf(codes.Internal, "%v", err)
	}

	updates := map[string]interface{}{
		"selected":   selected,
		"updated_at": now,
	}
	if quantity > 0 {
		updates["quantity"] = quantity
	}

	if err := s.db.GetDb().WithContext(ctx).Model(&model.Cart{}).
		Where("id = ?", cart.ID).
		Updates(updates).Error; err != nil {
		return status.Errorf(codes.Internal, "%v", err)
	}
	return nil
}
