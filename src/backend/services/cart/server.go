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
	"gorm.io/gorm/clause"

	"github.com/jcbowen/market/src/backend/common/attachmenturl"
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
	// attachmentLoader 附件配置读取器：商品图快照入库归一与出库重拼共用
	attachmentLoader *attachmenturl.Loader
}

// newServer 创建购物车服务 gRPC 实现
// 参数：
//   - db: MySQL 实例
//   - productClient: 商品服务 gRPC 客户端
//
// 返回值：
//   - *server: gRPC 服务实现
func newServer(db *mysql.Instance, productClient product.ProductServiceClient) *server {
	return &server{db: db, productClient: productClient, attachmentLoader: attachmenturl.NewLoader(db.GetDb())}
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

// skuStatusEnabled 与商品服务 SkuService.SkuStatusEnabled 同值：SKU 启用态是加购与展示的
// 共同口径（cart 不引商品服务包，proto SkuInfo.status 原样透出，此处按值判定）
const skuStatusEnabled = 1

// maxCartItemQty 单条购物车项数量上限，与下单侧「单品 1~999」口径一致
const maxCartItemQty = 999

// AddItem 添加商品到购物车
// 若同一用户同一 SKU 已存在记录，则累加数量并置为选中；否则新建记录。
// 交易入口校验三件事：加购社区=SKU 归属社区（P0-4）、SKU 启用（规格未失效）、商品可售（P1-4）；
// 不校验 min_buy：起订份数是下单门槛，由订单服务 CreateOrder 统一拦截
// （购物车允许暂存低于起订量的数量，用户可继续加购到门槛）
//
// 参数：
//   - ctx: 上下文
//   - req: 添加请求
//
// 返回值：
//   - *cart.CartResp: 购物车响应
//   - error: 错误信息
func (s *server) AddItem(ctx stdctx.Context, req *cart.AddItemReq) (*cart.CartResp, error) {
	// 数量上限对齐下单口径（order 侧 1~999）：不设限会让超大数量进购物车试算与展示，
	// 到下单才被拒；累加后总量仍由下单侧兜底
	if req.Quantity <= 0 || req.Quantity > maxCartItemQty {
		return nil, status.Errorf(codes.InvalidArgument, "数量须在 1~%d 之间", maxCartItemQty)
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

	// 规格失效收口：SKU 被后台禁用/移除规格/随商品软删连带禁用（status≠1）后不允许加购。
	// 与结算侧商品服务的拦截（BatchGetSkus 只放启用 SKU）以及展示侧 cartItemInvalidReason
	// 的「规格已失效」同口径，避免加进去结算不了
	if skuInfo.Status != skuStatusEnabled {
		return nil, status.Errorf(codes.FailedPrecondition, "规格已失效，暂不可购买")
	}

	// 可售收口（P1-4）：加购即交易入口，非上架态（下架/定时上架未到点）不允许加购。
	// 判定由商品服务统一执行（require_sellable），此处只声明意图；
	// 展示侧 cartItemInvalidReason 的「商品已下架」判定与此同口径
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
		ProductName: productInfo.Product.Name,
		SpecName:    skuInfo.SpecName,
		// 商品图快照入库归一为附件相对路径（common/attachmenturl 口径），出库按当前配置重拼
		ProductImage: s.attachmentLoader.SourcePath(ctx, productImageOf(skuInfo, productInfo)),
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
// quantity > 0 时更新数量；selected 仅在纯选中操作（quantity <= 0）或勾选（selected=true）时更新，
// 纯改数量（quantity > 0 且 selected 未传，bool 零值 false）不影响选中态，
// 避免"改一次数量勾选被静默清掉"（再来一单后改数量即复现）
//
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *cart.CartResp: 购物车响应
//   - error: 错误信息
func (s *server) UpdateItem(ctx stdctx.Context, req *cart.UpdateItemReq) (*cart.CartResp, error) {
	// 改数量路径（quantity>0 才是改数量，<=0 为纯选中操作）补上限，与 AddItem 同口径
	if req.Quantity > maxCartItemQty {
		return nil, status.Errorf(codes.InvalidArgument, "数量须在 1~%d 之间", maxCartItemQty)
	}
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
// 更新当前用户购物车项的选中状态；
// community_id > 0 时仅更新该社区的购物车项（购物车页分组全选），为 0 时更新全部（整页全选）
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
	db := s.db.GetDb().WithContext(ctx).Model(&model.Cart{}).
		Where("user_id = ?", req.UserId)
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	if err := db.Updates(map[string]interface{}{
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
// 商品档案整批预取（见 loadCartCatalog），逐行只做纯内存装配
//
// 参数：
//   - ctx: 上下文
//   - list: 购物车模型列表
//
// 返回值：
//   - []*cart.CartItem: 购物车响应项列表
//   - error: 错误信息
func (s *server) buildCartItems(ctx stdctx.Context, list []*model.Cart) ([]*cart.CartItem, error) {
	skuIDs := make([]int64, 0, len(list))
	for _, m := range list {
		skuIDs = append(skuIDs, m.SkuID)
	}
	catalog, err := s.loadCartCatalog(ctx, skuIDs)
	if err != nil {
		return nil, err
	}

	items := make([]*cart.CartItem, 0, len(list))
	for _, m := range list {
		items = append(items, s.buildCartItem(ctx, m, catalog))
	}
	return items, nil
}

// cartCatalog 购物车展示所需的商品目录（整批一次预取的结果）
// key 缺失即代表该 SKU/商品在商品服务已取不到（被删除，或商品与规格的社区归属漂移），
// 由装配侧按「商品已失效」处理并回退购物车快照
type cartCatalog struct {
	skus     map[int64]*product.SkuInfo
	products map[int64]*product.ProductInfo
}

// loadCartCatalog 批量预取购物车行涉及的 SKU 与商品档案
// 一次 BatchGetSkus 加「按规格归属社区分组的 BatchGetProducts」替代逐行 GetSkuById + GetProduct：
// 原先 N 行购物车要 2N 次 gRPC 往返，且每次 GetProduct 在服务侧还要跑 5 条 SQL
// （主表/SKU/规格/图集/分类名），行数一多就放大成明显的列表延迟。C 端购物车固定单社区语境，
// 分组后通常就是 1 次商品批量调用。
// tolerate_missing=true 取「逐项判定」口径：SKU/商品被删除或社区归属漂移只从结果中剔除、
// 不整体报错，交由展示侧按失效处理（原单行版对同样场景是 NotFound→失效，
// 而归属漂移会返回 Internal 让整个购物车打不开）。
//
// 参数：
//   - ctx: 上下文
//   - skuIDs: 购物车行涉及的 SKU ID（重复值由商品服务去重）
//
// 返回值：
//   - *cartCatalog: 商品目录（空列表时为空目录，不发起 RPC）
//   - error: 批量查询的系统错误
func (s *server) loadCartCatalog(ctx stdctx.Context, skuIDs []int64) (*cartCatalog, error) {
	catalog := &cartCatalog{skus: map[int64]*product.SkuInfo{}, products: map[int64]*product.ProductInfo{}}
	if len(skuIDs) == 0 {
		return catalog, nil
	}

	skuResp, err := s.productClient.BatchGetSkus(ctx, &product.BatchGetSkusReq{Ids: skuIDs, TolerateMissing: true})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "查询 SKU 失败: %v", err)
	}
	// 商品档案按规格归属社区分组查询（与单行版 GetProduct 传 skuInfo.CommunityId 同口径）
	productIDsByCommunity := map[int64][]int64{}
	for _, sku := range skuResp.List {
		catalog.skus[sku.Id] = sku
		productIDsByCommunity[sku.CommunityId] = append(productIDsByCommunity[sku.CommunityId], sku.ProductId)
	}

	for communityID, productIDs := range productIDsByCommunity {
		resp, err := s.productClient.BatchGetProducts(ctx, &product.BatchGetProductsReq{
			Ids:             productIDs,
			CommunityId:     communityID,
			TolerateMissing: true,
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "查询商品失败: %v", err)
		}
		for _, info := range resp.List {
			catalog.products[info.Id] = info
		}
	}
	return catalog, nil
}

// buildCartItem 用预取的商品目录装配单个购物车响应项（纯内存，不发 RPC）
// 失效判定：SKU 不存在 →"商品已失效"；商品不存在 →"商品已失效"；SKU 已禁用/移除 →"规格已失效"；
// 商品下架 →"商品已下架"；库存为 0 →"商品已售罄"（判定实现见 cartItemInvalidReason）。
// 失效项不再跳过而是返回并携带失效原因，展示信息优先使用实时数据，SKU/商品已删除时回退快照；
// 失效项 selected 强制置为 false，结算（GetSelectedItems）与合计自动排除。
//
// 参数：
//   - ctx: 上下文（附件 URL 重拼需要读取附件配置）
//   - m: 购物车模型
//   - catalog: 预取的商品目录
//
// 返回值：
//   - *cart.CartItem: 购物车响应项
func (s *server) buildCartItem(ctx stdctx.Context, m *model.Cart, catalog *cartCatalog) *cart.CartItem {
	skuInfo := catalog.skus[m.SkuID]
	productInfo := catalog.products[skuInfo.GetProductId()]
	invalidReason := cartItemInvalidReason(skuInfo, productInfo)

	// 展示字段：实时数据优先，快照兜底（SKU/商品已删除时实时数据不可得）
	productName := m.ProductName
	specName := m.SpecName
	productImage := m.ProductImage
	price := 0.0
	minBuy := int32(0)
	var skuTotal int32
	stock := int32(0)
	if skuInfo != nil {
		price = skuInfo.Price
		specName = skuInfo.SpecName
		productImage = skuInfo.Image
		stock = skuInfo.Stock
	}
	if productInfo != nil {
		productName = productInfo.Name
		if productImage == "" {
			productImage = productInfo.CoverImage
		}
		minBuy = productInfo.MinBuy
		skuTotal = productInfo.SkuTotal
	}

	// 库存紧张提示：低于安全库存或低于购物车数量时展示"仅剩 X 件"（失效项不提示）
	stockHint := ""
	if invalidReason == "" && (stock < skuInfo.GetSafetyStock() || stock < m.Quantity) {
		stockHint = fmt.Sprintf("仅剩 %d 件", stock)
	}

	return &cart.CartItem{
		Id:          m.SkuID,
		ProductId:   m.ProductID,
		SkuId:       m.SkuID,
		ProductName: productName,
		SpecName:    specName,
		// 出库统一按当前附件配置重拼完整 URL：实时数据已是完整 URL（RestoreURL 幂等归一重拼），
		// 快照存量完整 URL 自动跟随新域名/存储模式，外链原样
		ProductImage:  s.attachmentLoader.RestoreURL(ctx, productImage),
		Price:         price,
		Stock:         stock,
		Quantity:      m.Quantity,
		Selected:      m.Selected && invalidReason == "",
		CommunityId:   m.CommunityID,
		MinBuy:        minBuy,
		SkuTotal:      skuTotal,
		Invalid:       invalidReason != "",
		InvalidReason: invalidReason,
		StockHint:     stockHint,
	}
}

// cartItemInvalidReason 判定购物车项的失效原因（纯内存，输入为预取到的 SKU 与商品）
// 判定顺序与逐行 RPC 版一致：SKU 取不到 →"商品已失效"；商品取不到 →"商品已失效"
// （商品先于规格状态判定，保证「商品已删除」不会被误报成「规格已失效」）；
// SKU 已禁用/被移除（status≠1）→"规格已失效"；商品下架 →"商品已下架"；库存为 0 →"商品已售罄"。
// 「规格已失效」与后端拦截同口径（商品服务 BatchGetSkus 只放启用 SKU），
// 后台禁用某 SKU 后购物车存量行不再显示有效，也不会被选进结算。
//
// 参数：
//   - sku: 预取的 SKU 信息，nil 表示商品服务已取不到
//   - productInfo: 预取的商品信息，nil 表示商品服务已取不到
//
// 返回值：
//   - string: 失效原因，空字符串表示未失效
func cartItemInvalidReason(sku *product.SkuInfo, productInfo *product.ProductInfo) string {
	switch {
	case sku == nil || productInfo == nil:
		return "商品已失效"
	case sku.Status != skuStatusEnabled:
		return "规格已失效"
	case productInfo.Status != 1:
		return "商品已下架"
	case sku.Stock <= 0:
		return "商品已售罄"
	}
	return ""
}

// upsertCartItem 新增或更新购物车记录（单条 upsert）
// 同一用户同一 SKU 已存在时累加数量并置为选中，否则新建记录；
// 无论新建还是更新均刷新商品快照，保证快照与最新商品信息一致。
// 依赖 carts 的 uk_user_sku 唯一索引走 INSERT ... ON DUPLICATE KEY UPDATE：
// 先查后写的旧实现在并发加购同一 SKU 时两条请求都读到「不存在」，
// 第二条 Create 直接抛原始 1062 给前端，且 quantity 用「读-改-写」会丢更新；
// 改由数据库侧累加后两个问题同时消除
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
//   - error: 写入失败原因（唯一索引冲突不再作为错误返回）
//
// 使用示例：
//
//	err := s.upsertCartItem(ctx, userID, communityID, productID, skuID, 2, snap)
func (s *server) upsertCartItem(ctx stdctx.Context, userID, communityID, productID, skuID int64, quantity int32, snap *cartSnapshot) error {
	now := time.Now().Format("2006-01-02 15:04:05")

	row := model.Cart{
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
	return s.db.GetDb().WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}, {Name: "sku_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				// 累加在 SQL 里完成：引用的是冲突行的既有值，不是本次待插入值
				"quantity":      gorm.Expr("quantity + ?", quantity),
				"selected":      true,
				"updated_at":    now,
				"product_name":  snap.ProductName,
				"spec_name":     snap.SpecName,
				"product_image": snap.ProductImage,
			}),
		}).
		Create(&row).Error
}

// updateCartItem 按 user_id + sku_id 更新购物车记录
// selected 更新条件：quantity <= 0（纯选中/取消操作）或 selected == true。
// proto UpdateItemReq.selected 为 bool 无法区分"未传"与"false"，
// 前端改数量只传 quantity（selected 落为 false），无条件写入会把已勾选项静默置为未选中
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
		"updated_at": now,
	}
	if quantity > 0 {
		updates["quantity"] = quantity
	}
	if quantity <= 0 || selected {
		updates["selected"] = selected
	}

	if err := s.db.GetDb().WithContext(ctx).Model(&model.Cart{}).
		Where("id = ?", cart.ID).
		Updates(updates).Error; err != nil {
		return status.Errorf(codes.Internal, "%v", err)
	}
	return nil
}

// ClearInvalidItems 清空当前用户全部失效购物车项
// 失效判定与 GetCart 一致（同走 getCartItems/buildCartItem），删除后返回剩余购物车项
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
