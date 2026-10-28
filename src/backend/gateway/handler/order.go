package handler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/debuglog"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/cart"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
)

// orderDedup 下单去重：基于 (user_id + community_id + items) 指纹的短时间窗口防重复提交。
// 单实例内存实现（gateway 当前单副本部署）；多实例扩展需替换为 Redis SETNX。
var orderDedup sync.Map // key: string(sha256 fingerprint), value: time.Time

const orderDedupWindow = 5 * time.Second

// cartCleanupTimeout 下单后异步清理购物车的总预算（整批共用，不按条目叠加）
const cartCleanupTimeout = 30 * time.Second

func init() {
	// 定期清理过期指纹，防止内存泄漏
	go func() {
		for range time.Tick(30 * time.Second) {
			cutoff := time.Now().Add(-orderDedupWindow * 2)
			orderDedup.Range(func(key, value any) bool {
				if t, ok := value.(time.Time); ok && t.Before(cutoff) {
					orderDedup.Delete(key)
				}
				return true
			})
		}
	}()
}

// orderFingerprint 计算下单请求指纹（user + community + 配送方式 + 优惠券 + 商品列表排序哈希）
// 配送方式/优惠券参与指纹：同用户 5s 内换配送方式或优惠券提交相同商品不属于重复提交
func orderFingerprint(userID, communityID, deliveryType, couponID int64, items []*order.OrderItem) string {
	skus := make([]string, 0, len(items))
	for _, it := range items {
		skus = append(skus, fmt.Sprintf("%d:%d", it.SkuId, it.Quantity))
	}
	sort.Strings(skus)
	raw := fmt.Sprintf("%d|%d|%d|%d|%v", userID, communityID, deliveryType, couponID, skus)
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h[:16])
}

// communityNameCache 社区名称本地缓存（TTL 5min）：社区数据变更频率极低，
// 避免订单列表页每次请求都逐条 RPC 查询社区名称
var communityNameCache sync.Map // key: int64(community_id), value: *communityCacheEntry

type communityCacheEntry struct {
	name     string
	cachedAt time.Time
}

const communityCacheTTL = 5 * time.Minute

// getCommunityName 带缓存的社区名称查询：命中缓存直接返回，未命中则 RPC 查询并回填
func (h *OrderHandler) getCommunityName(ctx context.Context, communityID int64) string {
	if v, ok := communityNameCache.Load(communityID); ok {
		entry := v.(*communityCacheEntry)
		if time.Since(entry.cachedAt) < communityCacheTTL {
			return entry.name
		}
	}
	community, err := h.userClient.GetCommunity(ctx, &user.GetCommunityReq{Id: communityID})
	if err != nil || community == nil {
		return ""
	}
	communityNameCache.Store(communityID, &communityCacheEntry{name: community.Name, cachedAt: time.Now()})
	return community.Name
}

// OrderHandler 订单接口处理器
type OrderHandler struct {
	orderClient   order.OrderServiceClient
	userClient    user.UserServiceClient
	paymentClient payment.PaymentServiceClient
	cartClient    cart.CartServiceClient
	// productClient 商品服务客户端：再来一单按商品/SKU 的实时状态筛选可售商品
	productClient product.ProductServiceClient
	// orderTimeoutSec 订单超时未支付时间（秒），与 order-service 读同一份配置，
	// 用于详情响应生成待支付订单的 expire_at
	orderTimeoutSec int
}

// NewOrderHandler 创建订单处理器
// 参数：
//   - orderClient: order-service gRPC 客户端
//   - userClient: user-service gRPC 客户端
//   - paymentClient: payment-service gRPC 客户端，用于订单详情展示支付方式
//   - cartClient: cart-service gRPC 客户端，用于下单后清理购物车
//   - productClient: product-service gRPC 客户端，用于再来一单校验商品/SKU 可售状态
//   - orderTimeoutSec: 订单超时未支付时间（秒）
//
// 返回值：
//   - *OrderHandler: 订单处理器实例
func NewOrderHandler(orderClient order.OrderServiceClient, userClient user.UserServiceClient, paymentClient payment.PaymentServiceClient, cartClient cart.CartServiceClient, productClient product.ProductServiceClient, orderTimeoutSec int) *OrderHandler {
	if orderTimeoutSec <= 0 {
		orderTimeoutSec = 1800
	}
	return &OrderHandler{
		orderClient:     orderClient,
		userClient:      userClient,
		paymentClient:   paymentClient,
		cartClient:      cartClient,
		productClient:   productClient,
		orderTimeoutSec: orderTimeoutSec,
	}
}

// CreateOrderReq 创建订单请求
type CreateOrderReq struct {
	DeliveryType int32              `json:"delivery_type"`
	AddressID    int64              `json:"address_id"`
	DeliveryTime string             `json:"delivery_time"`
	Remark       string             `json:"remark"`
	CouponID     int64              `json:"coupon_id"`
	Items        []*order.OrderItem `json:"items" binding:"required"`
	// 下单社区：购物车跨社区结算场景由前端按结算商品所属社区显式传入；
	// 不传时按用户实时归属社区下单（与原行为一致）
	CommunityID int64 `json:"community_id"`
}

// CreateOrder 创建订单
// 从上下文取 user_id、community_id
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	var req CreateOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	// 纵深防御：C 端一律服务端取价，清空客户端传入的价格字段，
	// 防止注入 price 以任意低价成交（服务端另有 Source==2 改价来源校验）
	for _, item := range req.Items {
		item.Price = 0
	}
	// 下单社区：显式传入（购物车跨社区结算）时直接使用，
	// 订单归属、自提点/配送履约、社区满减与秒杀价均按该社区计算；
	// 未传时按用户实时归属社区：token 中的 community_id 是登录快照，
	// 切换社区不重签 token，用快照会把新社区的订单挂到旧社区
	communityID := req.CommunityID
	if communityID <= 0 {
		userResp, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: uc.UserID})
		if err != nil {
			log.Printf("查询用户归属社区失败: %v", err)
			response.GRPCError(c, err)
			return
		}
		if userResp == nil {
			response.Error(c, errcode.NotFound, "用户不存在")
			return
		}
		communityID = userResp.CommunityId
	}

	// 防重复下单：相同用户+社区+配送方式+优惠券+商品列表在 5s 窗口内仅允许一次提交
	fp := orderFingerprint(uc.UserID, communityID, int64(req.DeliveryType), req.CouponID, req.Items)
	if prev, loaded := orderDedup.LoadOrStore(fp, time.Now()); loaded {
		if t, ok := prev.(time.Time); ok && time.Since(t) < orderDedupWindow {
			response.Error(c, errcode.ParamError, "请勿重复提交订单")
			return
		}
		orderDedup.Store(fp, time.Now())
	}

	resp, err := h.orderClient.CreateOrder(c.Request.Context(), &order.CreateOrderReq{
		UserId:       uc.UserID,
		CommunityId:  communityID,
		DeliveryType: req.DeliveryType,
		AddressId:    req.AddressID,
		DeliveryTime: req.DeliveryTime,
		Remark:       req.Remark,
		CouponId:     req.CouponID,
		Items:        req.Items,
	})
	if err != nil {
		// 下单失败时释放指纹：库存不足等原因被拒后，用户应可立即重试而非被去重窗口拦截
		orderDedup.Delete(fp)
		log.Printf("创建订单失败: %v", err)
		response.GRPCError(c, err)
		return
	}

	// 服务端兜底清理购物车：下单成功后异步删除已购 SKU，防止客户端崩溃/网络中断导致残留
	if h.cartClient != nil {
		go h.cleanupCartAfterOrder(uc.UserID, req.Items)
	}

	response.Success(c, resp)
}

// cleanupCartAfterOrder 下单成功后异步删除已购 SKU 的服务端兜底清理
// debugger 记录口径：全部成功不落库——组件 StartProcess 创建即落库，每单一条会
// 迅速挤占 debug_logs 全表上限、把 HTTP 请求记录冲掉（与调度器「空转不落库」同口径）；
// 仅部分失败时记一条 error 级摘要（异步任务没有请求日志，这条记录是排查残留的主路径）
// 参数：
//   - userID: 下单用户 ID
//   - items: 订单条目，逐项按 SKU ID 删除购物车行
//
// 返回值：无（失败只记录，不影响已成功的下单响应）
//
// 使用示例：
//
//	go h.cleanupCartAfterOrder(uc.UserID, req.Items)
func (h *OrderHandler) cleanupCartAfterOrder(userID int64, items []*order.OrderItem) {
	// 异步任务没有请求上下文可继承，必须自带总超时：否则 gRPC 卡住会让这条协程
	// 长期持有连接与 items 切片，且清理永不结束也不会有任何流水可查
	bgCtx, cancel := context.WithTimeout(context.Background(), cartCleanupTimeout)
	defer cancel()
	failedSkus := make([]int64, 0, len(items))
	for _, item := range items {
		if _, err := h.cartClient.RemoveItem(bgCtx, &cart.RemoveItemReq{
			Id:     item.SkuId,
			UserId: userID,
		}); err != nil {
			failedSkus = append(failedSkus, item.SkuId)
			log.Printf("下单后清理购物车失败（user=%d sku=%d）: %v", userID, item.SkuId, err)
		}
	}
	if len(failedSkus) == 0 {
		return
	}
	rec := debuglog.Start("下单后清理购物车", "cart_cleanup")
	if rec == nil {
		return
	}
	rec.Error("部分 SKU 清理失败", map[string]interface{}{
		"user_id":     userID,
		"sku_count":   len(items),
		"failed_skus": failedSkus,
	})
	_ = rec.EndProcess(debuglog.StatusFailed)
}

// ListOrders 获取当前用户订单列表
// 支持显式传入 status、community_id 与分页；不按 token 中的社区快照兜底过滤——
// 切换社区不重签 token，快照是登录时的旧值，会导致新社区/未选社区时期的订单查不到（显示不全）
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) ListOrders(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))

	// 仅显式传 community_id > 0 时按社区过滤，默认展示该用户全部订单
	communityID := int64(0)
	if id, err := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64); err == nil && id > 0 {
		communityID = id
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.orderClient.ListOrders(c.Request.Context(), &order.ListOrdersReq{
		UserId:      uc.UserID,
		CommunityId: communityID,
		Status:      int32(status),
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 批量补社区名称（本地缓存 TTL 5min，消除逐条 RPC）
	communityNames := make(map[int64]string)
	for _, o := range resp.List {
		if o.CommunityId <= 0 {
			continue
		}
		if _, ok := communityNames[o.CommunityId]; ok {
			continue
		}
		communityNames[o.CommunityId] = h.getCommunityName(c.Request.Context(), o.CommunityId)
	}

	// 并行获取订单商品明细（消除 N+1 串行 RPC，延迟从 N×RTT 降为 ~1×RTT）
	// gin.Context 非并发安全：在循环外取一次派生 ctx 供所有 goroutine 只读使用
	detailCtx := c.Request.Context()
	type orderDetail struct {
		items   []gin.H
		payType int32
	}
	details := make([]orderDetail, len(resp.List))
	var wg sync.WaitGroup
	for i, o := range resp.List {
		wg.Add(1)
		go func(idx int64, orderID int64) {
			defer wg.Done()
			var d orderDetail
			if detail, err := h.orderClient.GetOrder(detailCtx, &order.GetOrderReq{
				Id:     orderID,
				UserId: uc.UserID,
			}); err != nil {
				log.Printf("并行拉取订单明细失败（order=%d）: %v", orderID, err)
			} else if detail != nil {
				for _, item := range detail.Items {
					d.items = append(d.items, gin.H{
						"id":            item.Id,
						"product_id":    item.ProductId,
						"product_name":  item.ProductName,
						"sku_id":        item.SkuId,
						"spec_name":     item.SpecName,
						"price":         item.Price,
						"quantity":      item.Quantity,
						"total_price":   item.TotalPrice,
						"product_image": item.ProductImage,
					})
				}
			}
			d.payType = h.orderPayType(detailCtx, orderID)
			details[idx] = d
		}(int64(i), o.Id)
	}
	wg.Wait()

	list := make([]gin.H, 0, len(resp.List))
	for i, o := range resp.List {
		items := details[i].items
		if items == nil {
			items = make([]gin.H, 0)
		}
		list = append(list, gin.H{
			"id":            o.Id,
			"order_no":      o.OrderNo,
			"status":        o.Status,
			"status_text":   clientOrderStatusText(o.Status, o.DeliveryType),
			"delivery_type": o.DeliveryType,
			"total_amount":  o.TotalAmount,
			"pay_amount":    o.PayAmount,
			// 订单所属社区（列表页展示社区标签）
			"community_id":   o.CommunityId,
			"community_name": communityNames[o.CommunityId],
			// 支付类型（1微信 2支付宝 3货到付款 4现金；0=未支付/未知），
			// 供前端区分货到付款单的"取消订单/申请退款"按钮显示
			"pay_type": details[i].payType,
			// 订单来源（1=C端用户 2=收银台代客）：门店单在 C 端展示但只读（隐藏操作按钮）
			"source":     o.Source,
			"items":      items,
			"created_at": o.CreatedAt,
			// 待支付订单的超时截止时间，供前端展示倒计时（与 order-service 超时取消同一配置）
			"expire_at": orderExpireAt(o.Status, o.CreatedAt, h.orderTimeoutSec),
		})
	}
	response.SuccessWithPage(c, list, int(resp.Pagination.Total), page, pageSize)
}

// ListFrequentProducts 获取当前用户常买商品（购物车"常买商品"列表）
// 统计口径：仅计有效订单（已支付及之后），按购买次数降序，默认返回 10 条
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) ListFrequentProducts(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	uc := appcontext.GetUserContext(c)
	resp, err := h.orderClient.GetUserFrequentProducts(c.Request.Context(), &order.GetUserFrequentProductsReq{
		UserId: uc.UserID,
		Limit:  int32(limit),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	list := make([]gin.H, 0, len(resp.List))
	for _, p := range resp.List {
		list = append(list, gin.H{
			"product_id":     p.ProductId,
			"product_name":   p.ProductName,
			"product_image":  p.ProductImage,
			"sku_id":         p.SkuId,
			"purchase_count": p.PurchaseCount,
			"total_quantity": p.TotalQuantity,
		})
	}
	response.Success(c, gin.H{"list": list})
}

// GetUserOrderStatusCounts 用户订单状态角标统计（C 端「我的-我的订单」红标）
// 接口: GET /api/v1/orders/status-counts
// 口径与订单列表 Tab 一致：待支付=1；待发货=2/3；待收货=4；
// 退款/售后只统计存在待审核售后单（refunds.status=1）的订单，且排除用户已软删除的订单
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) GetUserOrderStatusCounts(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	resp, err := h.orderClient.GetUserOrderStatusCounts(c.Request.Context(), &order.GetUserOrderStatusCountsReq{
		UserId: uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"pending_pay":     resp.PendingPay,
		"pending_ship":    resp.PendingShip,
		"pending_receive": resp.PendingReceive,
		"after_sale":      resp.AfterSale,
	})
}

// PreviewFreeShippingReq 免配送费试算请求
type PreviewFreeShippingReq struct {
	Items []struct {
		SkuID    int64 `json:"sku_id" binding:"required"`
		Quantity int32 `json:"quantity" binding:"required,min=1"`
	} `json:"items" binding:"required,min=1"`
	// 试算社区：购物车跨社区结算时前端按结算社区传入；
	// 不传时按用户实时归属社区试算（与原行为一致）
	CommunityID int64 `json:"community_id"`
}

// PreviewFreeShipping 免配送费试算（C 端确认页展示：
// 已免配送费 / 还差 X 元 / 不参与商品清单）
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) PreviewFreeShipping(c *gin.Context) {
	var req PreviewFreeShippingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	communityID := req.CommunityID
	if communityID <= 0 {
		// 实时查用户当前归属社区（与 CreateOrder 同口径）：
		// token 中的 community_id 是登录快照，切换社区不重签 token，用快照会按旧社区试算；
		// 也不信任客户端可伪造的 X-Community-Id，防止登录用户试算任意社区的活动与配送费配置
		uc := appcontext.GetUserContext(c)
		userResp, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: uc.UserID})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		if userResp == nil || userResp.CommunityId <= 0 {
			response.Error(c, errcode.ParamError, "缺少社区信息")
			return
		}
		communityID = userResp.CommunityId
	}

	items := make([]*order.PreviewFreeShippingItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, &order.PreviewFreeShippingItem{
			SkuId:    item.SkuID,
			Quantity: item.Quantity,
		})
	}
	resp, err := h.orderClient.PreviewFreeShipping(c.Request.Context(), &order.PreviewFreeShippingReq{
		CommunityId: communityID,
		Items:       items,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"free_shipping":   resp.FreeShipping,
		"activity_name":   resp.ActivityName,
		"threshold":       resp.Threshold,
		"eligible_amount": resp.EligibleAmount,
		"remain_amount":   resp.RemainAmount,
		"excluded_names":  resp.ExcludedNames,
		"delivery_fee":    resp.DeliveryFee,
	})
}

// PreviewCouponEligibilityReq 券可用性试算请求
type PreviewCouponEligibilityReq struct {
	Items []struct {
		SkuID    int64 `json:"sku_id" binding:"required"`
		Quantity int32 `json:"quantity" binding:"required,min=1"`
	} `json:"items" binding:"required,min=1"`
	// 待试算的券 ID（用户券对应的 coupons.id）；空表示没有可用券，直接返回空表
	CouponIDs []int64 `json:"coupon_ids"`
	// 试算社区：跨社区结算时前端按结算社区传入；不传时按用户实时归属社区（与免配送费试算同口径）
	CommunityID int64 `json:"community_id"`
}

// PreviewCouponEligibility 券可用性试算（C 端确认页：券是否可用、范围内金额与预计抵扣）
// 门槛按券适用范围计算（与下单校验同一口径），前端据此置灰不可用券并展示差额
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) PreviewCouponEligibility(c *gin.Context) {
	var req PreviewCouponEligibilityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	uc := appcontext.GetUserContext(c)

	communityID := req.CommunityID
	if communityID <= 0 {
		// 与免配送费试算同一口径：不信任 token 里的社区快照与客户端可伪造的 X-Community-Id
		userResp, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: uc.UserID})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		if userResp == nil || userResp.CommunityId <= 0 {
			response.Error(c, errcode.ParamError, "缺少社区信息")
			return
		}
		communityID = userResp.CommunityId
	}

	items := make([]*order.PreviewFreeShippingItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, &order.PreviewFreeShippingItem{
			SkuId:    item.SkuID,
			Quantity: item.Quantity,
		})
	}
	resp, err := h.orderClient.PreviewCouponEligibility(c.Request.Context(), &order.PreviewCouponEligibilityReq{
		CommunityId: communityID,
		UserId:      uc.UserID,
		Items:       items,
		CouponIds:   req.CouponIDs,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	list := make([]gin.H, 0, len(resp.List))
	for _, item := range resp.List {
		list = append(list, gin.H{
			"coupon_id":       item.CouponId,
			"available":       item.Available,
			"reason":          item.Reason,
			"eligible_amount": item.EligibleAmount,
			"threshold":       item.Threshold,
			"discount_amount": item.DiscountAmount,
		})
	}
	response.SuccessWithList(c, list)
}

// GetOrder 获取订单详情
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) GetOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{
		Id:     id,
		UserId: uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 服务端异常兜底：订单为空时直接返回未找到，避免下方解引用空指针
	if resp.Order == nil {
		response.Error(c, errcode.NotFound)
		return
	}

	o := resp.Order
	address := gin.H{
		"id":            0,
		"contact_name":  "",
		"contact_phone": "",
		"province":      "",
		"city":          "",
		"district":      "",
		"detail":        "",
		"tag":           "",
		"is_default":    false,
	}
	if o != nil && o.DeliveryType == 1 {
		// 自提单：地址卡展示自提点信息（社区名称 + 自提点地址，未配置时回退社区地址）
		if community, err := h.userClient.GetCommunity(c.Request.Context(), &user.GetCommunityReq{Id: o.CommunityId}); err == nil && community != nil {
			address["contact_name"] = community.Name
			address["detail"] = community.PickupAddress
			if address["detail"] == "" {
				address["detail"] = community.Address
			}
		}
	} else if o != nil && o.ContactName != "" {
		address["contact_name"] = o.ContactName
		address["contact_phone"] = o.ContactPhone
		address["detail"] = o.ContactAddress
	}

	items := make([]gin.H, 0, len(resp.Items))
	for _, item := range resp.Items {
		// 键名与购物车/商品接口及前端 types.ts 的 OrderItem 保持一致
		items = append(items, gin.H{
			"id":            item.Id,
			"product_id":    item.ProductId,
			"product_name":  item.ProductName,
			"sku_id":        item.SkuId,
			"spec_name":     item.SpecName,
			"price":         item.Price,
			"quantity":      item.Quantity,
			"total_price":   item.TotalPrice,
			"product_image": item.ProductImage,
		})
	}

	data := gin.H{
		"id":              o.Id,
		"order_no":        o.OrderNo,
		"status":          o.Status,
		"status_text":     clientOrderStatusText(o.Status, o.DeliveryType),
		"total_amount":    o.TotalAmount,
		"discount_amount": o.DiscountAmount,
		"delivery_fee":    o.DeliveryFee,
		"pay_amount":      o.PayAmount,
		"delivery_type":   o.DeliveryType,
		"delivery_time":   o.DeliveryTime,
		"cancel_reason":   o.CancelReason,
		"cancel_time":     o.CancelTime,
		"pay_type":        h.orderPayType(c.Request.Context(), o.Id),
		"pay_type_text":   h.orderPayTypeText(c, o.Id),
		// 订单来源（1=C端用户 2=收银台代客）：门店单在 C 端展示但只读（隐藏操作按钮）
		"source":     o.Source,
		"items":      items,
		"address":    address,
		"remark":     o.Remark,
		"created_at": o.CreatedAt,
		// 自提核销码（仅自提单返回，C 端订单详情展示取货码/二维码）
		"pickup_code":  o.PickupCode,
		"paid_at":      o.PayTime,
		"delivered_at": o.DeliverTime,
		"received_at":  o.CompleteTime,
		// 履约人员姓名快照与送达签收凭证（决策点②拍照留证，C 端订单详情可查看）
		"delivery_user_name":    o.DeliveryUserName,
		"delivery_proof_images": o.DeliveryProofImages,
		// 待支付订单的超时截止时间，供前端展示倒计时（与 order-service 超时取消同一配置）
		"expire_at": orderExpireAt(o.Status, o.CreatedAt, h.orderTimeoutSec),
	}
	response.Success(c, data)
}

// orderExpireAt 计算待支付订单的超时截止时间
// 仅 status=1（待支付）时返回 created_at + order_timeout，其余状态返回空串
// 参数：
//   - status: 订单状态
//   - createdAt: 订单创建时间（兼容 2006-01-02 15:04:05 与 RFC3339 两种格式）
//   - orderTimeoutSec: 超时秒数
//
// 返回值：
//   - string: 截止时间字符串（2006-01-02 15:04:05），非待支付订单或解析失败返回空串
func orderExpireAt(status int32, createdAt string, orderTimeoutSec int) string {
	if status != 1 || createdAt == "" {
		return ""
	}
	created, err := time.ParseInLocation("2006-01-02 15:04:05", createdAt, time.Local)
	if err != nil {
		// 兼容订单服务返回的 RFC3339 格式（如 2026-10-17T00:55:01+08:00）
		created, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return ""
		}
	}
	return created.Add(time.Duration(orderTimeoutSec) * time.Second).Format("2006-01-02 15:04:05")
}

// orderPayType 查询订单支付类型数值
// 未支付或查询不到支付记录时返回 0
// 参数：
//   - ctx: 上下文（可在并行拉取场景复用派生 ctx，不依赖 gin.Context）
//   - orderID: 订单 ID
//
// 返回值：
//   - int32: 支付类型（1微信 2支付宝 3货到付款 4现金；0=未支付/未知）
func (h *OrderHandler) orderPayType(ctx context.Context, orderID int64) int32 {
	resp, err := h.paymentClient.GetPayment(ctx, &payment.GetPaymentReq{OrderId: orderID})
	if err != nil {
		log.Printf("查询订单支付方式失败（order=%d）: %v", orderID, err)
		return 0
	}
	if resp == nil {
		return 0
	}
	return resp.PayType
}

// payTypeTextOf 支付类型码转文案（管理端各订单列表共用）
// 参数：
//   - payType: 支付类型（1微信 2支付宝 3货到付款 4现金；0=未支付/未知）
//
// 返回值：
//   - string: 支付方式文案，未知类型返回空串
func payTypeTextOf(payType int32) string {
	switch payType {
	case 1:
		return "微信支付"
	case 2:
		return "支付宝支付"
	case 3:
		return "货到付款"
	case 4:
		return "现金"
	case 5:
		return "微信付款码"
	case 6:
		return "支付宝付款码"
	default:
		return ""
	}
}

// orderPayTypeText 查询订单支付方式文案
// 未支付或查询不到支付记录时返回空字符串
// 参数：
//   - c: gin 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - string: 支付方式文案
func (h *OrderHandler) orderPayTypeText(c *gin.Context, orderID int64) string {
	resp, err := h.paymentClient.GetPayment(c.Request.Context(), &payment.GetPaymentReq{OrderId: orderID})
	if err != nil || resp == nil {
		return ""
	}
	return payTypeTextOf(resp.PayType)
}

// CancelOrderReq 取消订单请求
type CancelOrderReq struct {
	Reason string `json:"reason"`
}

// CancelOrder 取消订单
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) CancelOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req CancelOrderReq
	_ = c.ShouldBindJSON(&req)

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.CancelOrder(c.Request.Context(), &order.CancelOrderReq{
		Id:     id,
		UserId: uc.UserID,
		Reason: req.Reason,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// DeleteOrder C 端用户删除订单（软删除）
// 仅终态订单（5已完成 6已取消 8已退款）可删；用户归属由服务端以登录态 UserID 强校验，
// 软删后 C 端列表与角标统计不可见，详情链路保留可达
// 参数：
//   - c: gin 上下文（路径参数 id）
//
// 异常：
//   - ParamError: id 非法
//   - GRPCError: 订单不存在 / 非本人订单 / 非终态 / 有进行中售后
func (h *OrderHandler) DeleteOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.DeleteOrder(c.Request.Context(), &order.DeleteOrderReq{
		Id:     id,
		UserId: uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// Reorder 再来一单
// 把订单内商品按原数量重新加入该订单所属社区的购物车（C 端订单详情页入口）。
// 仅用户本人的终态订单（已完成 5 / 已取消 6 / 已退款 8）可复购；
// 门店单（收银台代客 source=2）在 C 端只读，不提供复购入口；
// 商品下架/规格失效/SKU 删除等逐项跳过，不阻断其余商品加购。
// 参数：
//   - c: gin 上下文（路径参数 id 为订单 ID）
//
// 异常：
//   - ParamError: id 非法 / 门店单 / 订单状态不支持
//   - NotFound: 订单不存在或非本人订单
//   - OperationNotAllowed: 全部商品均无法加入购物车（消息为失败原因汇总）
//   - GRPCError: 订单查询失败
func (h *OrderHandler) Reorder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	orderResp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: id, UserId: uc.UserID})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	// GetOrder 对非本人订单按不存在处理，此处兜底空响应
	if orderResp.Order == nil {
		response.Error(c, errcode.NotFound, "订单不存在")
		return
	}
	o := orderResp.Order
	if o.Source == 2 {
		response.Error(c, errcode.ParamError, "门店订单请在门店购买")
		return
	}
	// 仅终态订单可复购：进行中订单重复加购易造成误下单
	if o.Status != 5 && o.Status != 6 && o.Status != 8 {
		response.Error(c, errcode.OrderStatusNotAllowed, "订单当前状态不支持再来一单")
		return
	}
	if len(orderResp.Items) == 0 {
		response.Error(c, errcode.NotFound, "订单商品不存在")
		return
	}

	// 加购社区取订单所属社区：AddItem 会校验 SKU 归属社区，
	// 用户切换到其他社区后仍可复购原社区商品（购物车支持跨社区展示）
	// AddItem 只校验商品/SKU 的存在与归属，不校验上架/停售/库存，
	// 因此复购前批量拉取实时状态，失效项逐项跳过并给出原因。
	// 必须走 tolerate_missing 口径：BatchGetProducts/BatchGetSkus 默认是下单交易口径
	// （任一项缺失/停用即整体拒绝），会让一个失效规格阻断整单其余商品复购
	productIDs := make([]int64, 0, len(orderResp.Items))
	skuIDs := make([]int64, 0, len(orderResp.Items))
	for _, item := range orderResp.Items {
		productIDs = append(productIDs, item.ProductId)
		skuIDs = append(skuIDs, item.SkuId)
	}
	productsResp, err := h.productClient.BatchGetProducts(c.Request.Context(), &product.BatchGetProductsReq{
		Ids:             productIDs,
		CommunityId:     o.CommunityId,
		TolerateMissing: true,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	skusResp, err := h.productClient.BatchGetSkus(c.Request.Context(), &product.BatchGetSkusReq{
		Ids:             skuIDs,
		CommunityId:     o.CommunityId,
		TolerateMissing: true,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	productByID := make(map[int64]*product.ProductInfo, len(productsResp.List))
	for _, p := range productsResp.List {
		productByID[p.Id] = p
	}
	skuByID := make(map[int64]*product.SkuInfo, len(skusResp.List))
	for _, s := range skusResp.List {
		skuByID[s.Id] = s
	}

	added := 0
	skipped := make([]gin.H, 0, len(orderResp.Items))
	for _, item := range orderResp.Items {
		// 先按实时可售状态过滤：下架/停售/库存不足的商品不再加入购物车
		if reason := reorderSkipReason(item, productByID[item.ProductId], skuByID[item.SkuId]); reason != "" {
			skipped = append(skipped, gin.H{
				"product_name": item.ProductName,
				"spec_name":    item.SpecName,
				"reason":       reason,
			})
			continue
		}
		_, addErr := h.cartClient.AddItem(c.Request.Context(), &cart.AddItemReq{
			UserId:      uc.UserID,
			CommunityId: o.CommunityId,
			ProductId:   item.ProductId,
			SkuId:       item.SkuId,
			Quantity:    item.Quantity,
		})
		if addErr == nil {
			added++
			continue
		}
		// 复用统一翻译：剥离 rpc error 前缀，拿到服务端的业务文案（已下架/规格失效等）
		_, reason := response.TranslateGRPCError(addErr)
		skipped = append(skipped, gin.H{
			"product_name": item.ProductName,
			"spec_name":    item.SpecName,
			"reason":       reason,
		})
	}

	// 全部失败：直接返回业务错误并汇总原因，前端不跳转购物车
	if added == 0 {
		reasons := make([]string, 0, len(skipped))
		for _, s := range skipped {
			reasons = append(reasons, fmt.Sprintf("%v（%v）", s["product_name"], s["reason"]))
		}
		response.Error(c, errcode.OperationNotAllowed, "商品均已无法购买："+strings.Join(reasons, "、"))
		return
	}

	response.Success(c, gin.H{"added": added, "skipped": skipped})
}

// reorderSkipReason 判断订单明细商品当前是否仍可购买
// 可售口径与 C 端下单一致：商品上架(status=1)、SKU 启用(status=1)、库存不小于购买数量
// 参数：
//   - item: 订单明细（取购买数量）
//   - p: 商品实时信息，nil 表示商品已删除
//   - s: SKU 实时信息，nil 表示规格已删除
//
// 返回值：
//   - string: 跳过原因；空串表示可购买
func reorderSkipReason(item *order.OrderItemInfo, p *product.ProductInfo, s *product.SkuInfo) string {
	if p == nil || p.Status != 1 {
		return "商品已下架"
	}
	if s == nil {
		return "规格已失效"
	}
	if s.Status != 1 {
		return "规格已停售"
	}
	if s.Stock < item.Quantity {
		return fmt.Sprintf("库存不足，仅剩 %d 件", s.Stock)
	}
	return ""
}

// ConfirmReceive 确认收货
// 仅配送中（status=4）的配送单可确认收货；
// 自提单已收紧为必须凭取货码核销（决策点⑤），C 端不可自行确认收货
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) ConfirmReceive(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	orderResp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: id, UserId: uc.UserID})
	if err != nil || orderResp.Order == nil {
		response.Error(c, errcode.NotFound, "订单不存在")
		return
	}
	o := orderResp.Order
	// 门店单（收银台代客）C 端只读：确认收货由门店/履约人员完成
	if o.Source == 2 {
		response.Error(c, errcode.ParamError, "门店订单请联系门店处理")
		return
	}
	if o.Status != 4 {
		if o.DeliveryType == 1 {
			response.Error(c, errcode.ParamError, "自提订单请到店出示取货码，由工作人员核销")
			return
		}
		response.Error(c, errcode.ParamError, "订单状态不允许确认收货")
		return
	}

	_, err = h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:           id,
		Status:       5,
		OperatorType: "user",
		OperatorId:   uc.UserID,
		Remark:       "用户确认收货",
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": 5})
}

// ApplyRefundItemReq 申请退款商品明细项
type ApplyRefundItemReq struct {
	OrderItemID int64 `json:"order_item_id"`
	Quantity    int32 `json:"quantity"`
}

// ListOrderRefunds 查询订单退款进度（C端订单详情「退款进度」弹窗用）
// 先校验订单归属当前用户，防止越权查看他人退款单；
// 仅回传进度展示所需字段（金额/原因/状态/拒绝原因/到账时间/商品明细），不含后台审批日志
//
// 接口: GET /api/v1/orders/:id/refunds
//
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) ListOrderRefunds(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	// 订单归属校验：GetOrderReq 携带 UserId 由服务端过滤，非本人订单视为不存在
	uc := appcontext.GetUserContext(c)
	orderResp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: id, UserId: uc.UserID})
	if err != nil || orderResp.Order == nil {
		response.Error(c, errcode.NotFound, "订单不存在")
		return
	}

	resp, err := h.orderClient.ListRefunds(c.Request.Context(), &order.ListRefundsReq{
		UserId:  uc.UserID,
		OrderId: id,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	refunds := make([]gin.H, 0, len(resp.List))
	for _, r := range resp.List {
		items := make([]gin.H, 0, len(r.Items))
		for _, item := range r.Items {
			items = append(items, gin.H{
				"product_name":  item.ProductName,
				"spec_name":     item.SpecName,
				"quantity":      item.Quantity,
				"refund_amount": item.RefundAmount,
			})
		}
		refunds = append(refunds, gin.H{
			"id":             r.Id,
			"refund_no":      r.RefundNo,
			"amount":         r.Amount,
			"freight_amount": r.FreightAmount,
			// 退款类型：1整单退 2部分退 3仅金额退
			"type":   r.Type,
			"reason": r.Reason,
			// 退款状态：1待审核 2已同意 3已拒绝 4退款中 5已退款
			"status":        r.Status,
			"reject_reason": r.RejectReason,
			"refund_time":   r.RefundTime,
			"created_at":    r.CreatedAt,
			"items":         items,
		})
	}
	response.Success(c, gin.H{"refunds": refunds})
}

// ApplyRefundReq 申请退款请求
// items 为空表示整单退；amount 为客户端手动调低后的金额（仅允许低于服务端汇算值）
type ApplyRefundReq struct {
	Amount        float64              `json:"amount"`
	Reason        string               `json:"reason" binding:"required"`
	Items         []ApplyRefundItemReq `json:"items"`
	RefundFreight bool                 `json:"refund_freight"`
}

// ApplyRefund 申请订单退款
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) ApplyRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req ApplyRefundReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	pbReq := &order.ApplyRefundReq{
		OrderId:       id,
		UserId:        ucUserID(c),
		Amount:        req.Amount,
		Reason:        req.Reason,
		RefundFreight: req.RefundFreight,
	}
	for _, item := range req.Items {
		pbReq.Items = append(pbReq.Items, &order.ApplyRefundItem{
			OrderItemId: item.OrderItemID,
			Quantity:    item.Quantity,
		})
	}

	resp, err := h.orderClient.ApplyRefund(c.Request.Context(), pbReq)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ucUserID 从请求上下文安全取出当前登录用户 ID
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - int64: 用户 ID
func ucUserID(c *gin.Context) int64 {
	uc := appcontext.GetUserContext(c)
	if uc == nil {
		return 0
	}
	return uc.UserID
}

// GetRefundConfig 获取退款配置（C 端发起退款时展示运费策略与已完成单售后窗口用）
//
// 接口: GET /api/v1/refund/config
//
// 参数:
//   - c: gin 上下文
func (h *OrderHandler) GetRefundConfig(c *gin.Context) {
	resp, err := h.orderClient.GetRefundConfig(c.Request.Context(), &order.GetRefundConfigReq{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"refund_freight_policy":  resp.RefundFreightPolicy,
		"completed_refund_hours": resp.CompletedRefundHours,
	})
}

// clientOrderStatusText 将订单状态码转换为 C 端展示文案
// 注意：status 入参只会是业务值域 1-8（9/90 是查询聚合虚拟状态，100/101 是调度器
// 处理中状态且会被回滚，均不会作为订单实际状态返回给 C 端）
// 参数：
//   - status: 状态码
//   - deliveryType: 配送方式（1 自提 2 配送），用于区分自提单的"待自提"文案
//
// 返回值：
//   - string: 状态中文描述
func clientOrderStatusText(status, deliveryType int32) string {
	switch status {
	case 1:
		return "待支付"
	case 2:
		switch deliveryType {
		case 1:
			return "待自提"
		case 3:
			// 到店单（收银台即买即走）：已支付即完成消费，无待自提/待发货语义
			return "已支付"
		}
		return "待发货"
	case 3:
		return "备货中"
	case 4:
		if deliveryType == 1 {
			return "待自提"
		}
		if deliveryType == 3 {
			return "已支付"
		}
		return "待收货"
	case 5:
		return "已完成"
	case 6:
		return "已取消"
	case 7:
		return "退款中"
	case 8:
		return "已退款"
	default:
		return "未知"
	}
}
