package main

import (
	stdctx "context"
	"errors"
	"fmt"
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

	// 社区隔离强校验（P0-4）：加购社区必须与 SKU 归属社区一致。
	// 此前的取用链路（GetSkuById 不带社区 + GetProduct 传 skuInfo.CommunityId 自证）
	// 对任意 community_id 恒通过，导致可跨社区取用价格/库存/优惠。
	// 未带社区时回落 SKU 自身归属，避免落下 community_id=0 的脏数据
	if req.CommunityId <= 0 {
		req.CommunityId = skuInfo.CommunityId
	} else if skuInfo.CommunityId != req.CommunityId {
		return nil, status.Errorf(codes.PermissionDenied, "该商品不属于当前社区")
	}

	// 可售收口（P1-4）：加购即交易入口，非上架态（下架/定时上架未到点）不允许加购。
	// 判定由商品服务统一执行（require_sellable），此处只声明意图；
	// 展示侧 invalidReasonOf 的「商品已下架」判定与此同口径
	productInfo, err := s.productClient.GetProduct(ctx, &product.GetProductReq{
		Id:              skuInfo.ProductId,
		CommunityId:     skuInfo.CommunityId,
		RequireSellable: true,
	})
	if err != nil {
		// 商品服务的业务拒绝（可售校拦截 FailedPrecondition、商品不存在 NotFound）
		// 原样透出：包成 Internal 会把「已下架」显示成系统错误，误导用户
		if code := status.Code(err); code == codes.FailedPrecondition || code == codes.NotFound {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "查询商品失败: %v", err)
	}
	if productInfo.Product == nil || productInfo.Product.Id == 0 {
		return nil, status.Errorf(codes.NotFound, "商品不存在")
	}

	if err := s.upsertCartItem(ctx, req.UserId, req.CommunityId, skuInfo.ProductId, req.SkuId, req.Quantity, &cartSnapshot{
		ProductName:  productInfo.Product.Name,
		SpecName:     skuInfo.SpecName,
		ProductImage: productImageOf(skuInfo, productInfo),
	}); err != nil {
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
// 失效判定：SKU 不存在/商品不存在 →"商品已失效"；商品下架 →"商品已下架"；库存为 0 →"商品已售罄"。
// 失效项不再跳过而是返回并携带失效原因，展示信息优先使用实时数据，SKU/商品已删除时回退快照；
// 失效项 selected 强制置为 false，结算（GetSelectedItems）与合计自动排除。
//
// 参数：
//   - ctx: 上下文
//   - m: 购物车模型
//
// 返回值：
//   - *cart.CartItem: 购物车响应项
//   - error: 错误信息
func (s *server) buildCartItem(ctx stdctx.Context, m *model.Cart) (*cart.CartItem, error) {
	skuInfo, productInfo, invalidReason, err := s.invalidReasonOf(ctx, m.SkuID)
	if err != nil {
		return nil, err
	}

	// 展示字段：实时数据优先，快照兜底（SKU/商品已删除时实时数据不可得）
	productName := m.ProductName
	specName := m.SpecName
	productImage := m.ProductImage
	price := 0.0
	minBuy := int32(0)
	var skuTotal int32
	if skuInfo.Id != 0 {
		price = skuInfo.Price
		specName = skuInfo.SpecName
		productImage = skuInfo.Image
	}
	if productInfo != nil && productInfo.Product != nil && productInfo.Product.Id != 0 {
		productName = productInfo.Product.Name
		if productImage == "" {
			productImage = productInfo.Product.CoverImage
		}
		minBuy = productInfo.Product.MinBuy
		skuTotal = int32(len(productInfo.Skus))
	}

	// 库存紧张提示：低于安全库存或低于购物车数量时展示"仅剩 X 件"（失效项不提示）
	stockHint := ""
	if invalidReason == "" && (skuInfo.Stock < skuInfo.SafetyStock || skuInfo.Stock < m.Quantity) {
		stockHint = fmt.Sprintf("仅剩 %d 件", skuInfo.Stock)
	}

	return &cart.CartItem{
		Id:            m.SkuID,
		ProductId:     m.ProductID,
		SkuId:         m.SkuID,
		ProductName:   productName,
		SpecName:      specName,
		ProductImage:  productImage,
		Price:         price,
		Stock:         skuInfo.Stock,
		Quantity:      m.Quantity,
		Selected:      m.Selected && invalidReason == "",
		CommunityId:   m.CommunityID,
		MinBuy:        minBuy,
		SkuTotal:      skuTotal,
		Invalid:       invalidReason != "",
		InvalidReason: invalidReason,
		StockHint:     stockHint,
	}, nil
}

// invalidReasonOf 判定购物车项的失效原因
// 判定顺序：SKU 不存在 →"商品已失效"；商品不存在 →"商品已失效"；商品下架 →"商品已下架"；库存为 0 →"商品已售罄"。
// SKU/商品的"不存在"类错误（NotFound）同样归入失效而非系统错误；其他查询错误按系统错误返回。
//
// 参数：
//   - ctx: 上下文
//   - skuID: SKU ID
//
// 返回值：
//   - *product.SkuInfo: SKU 信息（SKU 已删除时为空对象）
//   - *product.ProductDetail: 商品详情（商品已删除时为 nil）
//   - string: 失效原因，空字符串表示未失效
//   - error: 系统错误信息
func (s *server) invalidReasonOf(ctx stdctx.Context, skuID int64) (*product.SkuInfo, *product.ProductDetail, string, error) {
	skuInfo, err := s.productClient.GetSkuById(ctx, &product.GetSkuByIdReq{Id: skuID})
	if err != nil {
		if status.Code(err) != codes.NotFound {
			return nil, nil, "", status.Errorf(codes.Internal, "查询 SKU 失败: %v", err)
		}
		// SKU 已删除：按失效处理，展示信息回退快照
		return &product.SkuInfo{}, nil, "商品已失效", nil
	}

	productInfo, err := s.productClient.GetProduct(ctx, &product.GetProductReq{Id: skuInfo.ProductId, CommunityId: skuInfo.CommunityId})
	if err != nil {
		if status.Code(err) != codes.NotFound {
			return nil, nil, "", status.Errorf(codes.Internal, "查询商品失败: %v", err)
		}
		return skuInfo, nil, "商品已失效", nil
	}

	switch {
	case productInfo.Product == nil || productInfo.Product.Id == 0:
		return skuInfo, productInfo, "商品已失效", nil
	case productInfo.Product.Status != 1:
		return skuInfo, productInfo, "商品已下架", nil
	case skuInfo.Stock <= 0:
		return skuInfo, productInfo, "商品已售罄", nil
	}
	return skuInfo, productInfo, "", nil
}

// upsertCartItem 新增或更新购物车记录
// 同一用户同一 SKU 已存在时累加数量并置为选中，否则新建记录；
// 无论新建还是更新均刷新商品快照，保证快照与最新商品信息一致。
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - communityID: 社区 ID
//   - productID: 商品 ID
//   - skuID: SKU ID
//   - quantity: 新增数量
//   - snap: 商品快照（名称/规格/图片）
//
// 返回值：
//   - error: 错误信息
func (s *server) upsertCartItem(ctx stdctx.Context, userID, communityID, productID, skuID int64, quantity int32, snap *cartSnapshot) error {
	now := time.Now().Format("2006-01-02 15:04:05")

	var cart model.Cart
	err := s.db.GetDb().WithContext(ctx).
		Where("user_id = ? AND sku_id = ?", userID, skuID).
		First(&cart).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			cart = model.Cart{
				UserID:       userID,
				CommunityID:  communityID,
				ProductID:    productID,
				SkuID:        skuID,
				Quantity:     quantity,
				Selected:     true,
				CreatedAt:    now,
				UpdatedAt:    now,
				ProductName:  snap.ProductName,
				SpecName:     snap.SpecName,
				ProductImage: snap.ProductImage,
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
			"quantity":      cart.Quantity,
			"selected":      true,
			"updated_at":    now,
			"product_name":  snap.ProductName,
			"spec_name":     snap.SpecName,
			"product_image": snap.ProductImage,
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

// ClearInvalidItems 清空当前用户全部失效购物车项
// 失效判定与 GetCart 一致（复用 buildCartItem），删除后返回剩余购物车项
//
// 参数：
//   - ctx: 上下文
//   - req: 清空请求
//
// 返回值：
//   - *cart.CartResp: 剩余购物车响应
//   - error: 错误信息
func (s *server) ClearInvalidItems(ctx stdctx.Context, req *cart.ClearInvalidItemsReq) (*cart.CartResp, error) {
	items, err := s.getCartItems(ctx, req.UserId, 0)
	if err != nil {
		return nil, err
	}

	invalidSkuIds := make([]int64, 0, len(items))
	for _, it := range items {
		if it.Invalid {
			invalidSkuIds = append(invalidSkuIds, it.SkuId)
		}
	}

	if len(invalidSkuIds) > 0 {
		if err := s.db.GetDb().WithContext(ctx).
			Where("user_id = ? AND sku_id IN ?", req.UserId, invalidSkuIds).
			Delete(&model.Cart{}).Error; err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
	}

	remaining, err := s.getCartItems(ctx, req.UserId, 0)
	if err != nil {
		return nil, err
	}
	return &cart.CartResp{Items: remaining}, nil
}

// cartSnapshot 购物车商品快照（加购/更新时写入 carts 表）
type cartSnapshot struct {
	ProductName  string
	SpecName     string
	ProductImage string
}

// productImageOf 计算商品图片：优先 SKU 规格图，其次商品封面
//
// 参数：
//   - skuInfo: SKU 信息
//   - productInfo: 商品详情
//
// 返回值：
//   - string: 图片地址
func productImageOf(skuInfo *product.SkuInfo, productInfo *product.ProductDetail) string {
	if skuInfo.Image != "" {
		return skuInfo.Image
	}
	if productInfo.Product != nil {
		return productInfo.Product.CoverImage
	}
	return ""
}
