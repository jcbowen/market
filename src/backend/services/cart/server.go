package main

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jcbowen/jcbaseGo/component/redis"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/proto/cart"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
)

// server 实现 cart.CartServiceServer 接口
type server struct {
	cart.UnimplementedCartServiceServer
	rdb           *redis.Instance
	productClient product.ProductServiceClient
}

// cartItemKey 购物车 Redis 键前缀
const cartItemKey = "market:cart"

// cartItem 购物车项内部结构
type cartItem struct {
	ProductID    int64   `json:"product_id"`
	SkuID        int64   `json:"sku_id"`
	ProductName  string  `json:"product_name"`
	SpecName     string  `json:"spec_name"`
	ProductImage string  `json:"product_image"`
	Price        float64 `json:"price"`
	Stock        int32   `json:"stock"`
	Quantity     int32   `json:"quantity"`
	Selected     bool    `json:"selected"`
	CommunityID  int64   `json:"community_id"`
	MinBuy       int32   `json:"min_buy"`
	UpdatedAt    string  `json:"updated_at"`
}

// newServer 创建购物车服务 gRPC 实现
// 参数：
//   - rdb: Redis 实例
//   - productClient: 商品服务 gRPC 客户端
//
// 返回值：
//   - *server: gRPC 服务实现
func newServer(rdb *redis.Instance, productClient product.ProductServiceClient) *server {
	return &server{rdb: rdb, productClient: productClient}
}

// GetCart 获取购物车列表
// 如果请求中携带 community_id，则只返回该社区下的购物车项
func (s *server) GetCart(ctx stdctx.Context, req *cart.GetCartReq) (*cart.CartResp, error) {
	items, err := s.getCartItems(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	if req.CommunityId > 0 {
		filtered := make([]*cart.CartItem, 0, len(items))
		for _, item := range items {
			if item.CommunityId == req.CommunityId {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	return &cart.CartResp{Items: items}, nil
}

// AddItem 添加商品到购物车
// 如果 SKU/商品信息可查询，则同步补齐 product_name、spec_name、product_image、price、stock 等展示字段
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

	key := s.cartKey(req.UserId)
	field := strconv.FormatInt(req.SkuId, 10)

	existing, err := s.rdb.HGet(key, field)
	var item cartItem
	if err == nil && existing != "" {
		_ = json.Unmarshal([]byte(existing), &item)
	}

	item.ProductID = skuInfo.ProductId
	item.SkuID = req.SkuId
	item.ProductName = productInfo.Product.Name
	item.SpecName = skuInfo.SpecName
	item.ProductImage = skuInfo.Image
	if item.ProductImage == "" {
		item.ProductImage = productInfo.Product.CoverImage
	}
	item.Price = skuInfo.Price
	item.Stock = skuInfo.Stock
	item.Quantity += req.Quantity
	item.Selected = true
	item.CommunityID = skuInfo.CommunityId
	item.MinBuy = productInfo.Product.MinBuy
	item.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")

	data, _ := json.Marshal(item)
	if err := s.rdb.HSet(key, field, data); err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	items, err := s.getCartItems(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return &cart.CartResp{Items: items}, nil
}

// UpdateItem 更新购物车项
func (s *server) UpdateItem(ctx stdctx.Context, req *cart.UpdateItemReq) (*cart.CartResp, error) {
	key := s.cartKey(req.UserId)
	field := strconv.FormatInt(req.Id, 10)

	existing, err := s.rdb.HGet(key, field)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "购物车项不存在")
	}

	var item cartItem
	if err := json.Unmarshal([]byte(existing), &item); err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	if req.Quantity > 0 {
		item.Quantity = req.Quantity
	}
	item.Selected = req.Selected
	item.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")

	data, _ := json.Marshal(item)
	if err := s.rdb.HSet(key, field, data); err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	items, err := s.getCartItems(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return &cart.CartResp{Items: items}, nil
}

// RemoveItem 移除购物车项
func (s *server) RemoveItem(ctx stdctx.Context, req *cart.RemoveItemReq) (*cart.CartResp, error) {
	key := s.cartKey(req.UserId)
	field := strconv.FormatInt(req.Id, 10)

	if err := s.rdb.GetClient().HDel(ctx, key, field).Err(); err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	items, err := s.getCartItems(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return &cart.CartResp{Items: items}, nil
}

// SelectAll 全选/取消全选
func (s *server) SelectAll(ctx stdctx.Context, req *cart.SelectAllReq) (*cart.CartResp, error) {
	key := s.cartKey(req.UserId)
	all, err := s.rdb.HGetAll(key)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	for field, val := range all {
		var item cartItem
		if err := json.Unmarshal([]byte(val), &item); err != nil {
			continue
		}
		item.Selected = req.Selected
		item.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
		data, _ := json.Marshal(item)
		_ = s.rdb.HSet(key, field, data)
	}

	items, err := s.getCartItems(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return &cart.CartResp{Items: items}, nil
}

// ClearCart 清空购物车
func (s *server) ClearCart(ctx stdctx.Context, req *cart.ClearCartReq) (*common.CommonResponse, error) {
	if err := s.rdb.Del(s.cartKey(req.UserId)); err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// GetSelectedItems 获取已选中的购物车项
func (s *server) GetSelectedItems(ctx stdctx.Context, req *cart.GetSelectedItemsReq) (*cart.CartResp, error) {
	all, err := s.getCartItems(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	var selected []*cart.CartItem
	for _, item := range all {
		if item.Selected {
			selected = append(selected, item)
		}
	}
	return &cart.CartResp{Items: selected}, nil
}

// getCartItems 从 Redis 读取购物车全部项
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//
// 返回值：
//   - []*cart.CartItem: 购物车项列表
//   - error: 错误信息
func (s *server) getCartItems(ctx stdctx.Context, userID int64) ([]*cart.CartItem, error) {
	key := s.cartKey(userID)
	all, err := s.rdb.HGetAll(key)
	if err != nil {
		return nil, err
	}

	items := make([]*cart.CartItem, 0, len(all))
	for _, val := range all {
		var item cartItem
		if err := json.Unmarshal([]byte(val), &item); err != nil {
			continue
		}
		items = append(items, &cart.CartItem{
			Id:           item.SkuID,
			ProductId:    item.ProductID,
			SkuId:        item.SkuID,
			ProductName:  item.ProductName,
			SpecName:     item.SpecName,
			ProductImage: item.ProductImage,
			Price:        item.Price,
			Stock:        item.Stock,
			Quantity:     item.Quantity,
			Selected:     item.Selected,
			CommunityId:  item.CommunityID,
			MinBuy:       item.MinBuy,
		})
	}
	return items, nil
}

// cartKey 生成用户购物车 Redis 键
// 参数：
//   - userID: 用户 ID
//
// 返回值：
//   - string: Redis 键
func (s *server) cartKey(userID int64) string {
	return fmt.Sprintf("%s:%d", cartItemKey, userID)
}
