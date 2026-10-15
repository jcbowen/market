package handler

import (
	"log"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
)

// OrderHandler 订单接口处理器
type OrderHandler struct {
	orderClient   order.OrderServiceClient
	userClient    user.UserServiceClient
	paymentClient payment.PaymentServiceClient
	// orderTimeoutSec 订单超时未支付时间（秒），与 order-service 读同一份配置，
	// 用于详情响应生成待支付订单的 expire_at
	orderTimeoutSec int
}

// NewOrderHandler 创建订单处理器
// 参数：
//   - orderClient: order-service gRPC 客户端
//   - userClient: user-service gRPC 客户端
//   - paymentClient: payment-service gRPC 客户端，用于订单详情展示支付方式
//   - orderTimeoutSec: 订单超时未支付时间（秒）
//
// 返回值：
//   - *OrderHandler: 订单处理器实例
func NewOrderHandler(orderClient order.OrderServiceClient, userClient user.UserServiceClient, paymentClient payment.PaymentServiceClient, orderTimeoutSec int) *OrderHandler {
	if orderTimeoutSec <= 0 {
		orderTimeoutSec = 1800
	}
	return &OrderHandler{
		orderClient:     orderClient,
		userClient:      userClient,
		paymentClient:   paymentClient,
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
	// 订单归属社区实时查用户当前归属：token 中的 community_id 是登录快照，
	// 切换社区不重签 token，用快照会把新社区的订单挂到旧社区
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
	resp, err := h.orderClient.CreateOrder(c.Request.Context(), &order.CreateOrderReq{
		UserId:       uc.UserID,
		CommunityId:  userResp.CommunityId,
		DeliveryType: req.DeliveryType,
		AddressId:    req.AddressID,
		DeliveryTime: req.DeliveryTime,
		Remark:       req.Remark,
		CouponId:     req.CouponID,
		Items:        req.Items,
	})
	if err != nil {
		log.Printf("创建订单失败: %v", err)
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListOrders 获取当前用户订单列表
// 支持显式传入 status、community_id 与分页；不按 token 中的社区快照兜底过滤——
// 切换社区不重签 token，快照是登录时的旧值，会导致新社区/未选社区时期的订单查不到（显示不全）
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) ListOrders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
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

	list := make([]gin.H, 0, len(resp.List))
	for _, o := range resp.List {
		detail, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{
			Id:     o.Id,
			UserId: uc.UserID,
		})
		items := make([]gin.H, 0)
		if err == nil && detail != nil {
			for _, item := range detail.Items {
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
		}
		list = append(list, gin.H{
			"id":            o.Id,
			"order_no":      o.OrderNo,
			"status":        o.Status,
			"status_text":   clientOrderStatusText(o.Status, o.DeliveryType),
			"delivery_type": o.DeliveryType,
			"total_amount":  o.TotalAmount,
			"pay_amount":    o.PayAmount,
			// 支付类型（1微信 2支付宝 3货到付款；0=未支付/未知），
			// 供前端区分货到付款单的"取消订单/申请退款"按钮显示
			"pay_type":   h.orderPayType(c, o.Id),
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

// PreviewFreeShippingReq 免配送费试算请求
type PreviewFreeShippingReq struct {
	Items []struct {
		SkuID    int64 `json:"sku_id" binding:"required"`
		Quantity int32 `json:"quantity" binding:"required,min=1"`
	} `json:"items" binding:"required,min=1"`
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
	uc := appcontext.GetUserContext(c)
	// 实时查用户当前归属社区（与 CreateOrder 同口径）：
	// token 中的 community_id 是登录快照，切换社区不重签 token，用快照会按旧社区试算；
	// 也不信任客户端可伪造的 X-Community-Id，防止登录用户试算任意社区的活动与配送费配置
	userResp, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: uc.UserID})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if userResp == nil || userResp.CommunityId <= 0 {
		response.Error(c, errcode.ParamError, "缺少社区信息")
		return
	}
	communityID := userResp.CommunityId

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
		"pay_type":        h.orderPayType(c, o.Id),
		"pay_type_text":   h.orderPayTypeText(c, o.Id),
		"items":           items,
		"address":         address,
		"remark":          o.Remark,
		"created_at":      o.CreatedAt,
		// 自提核销码（仅自提单返回，C 端订单详情展示取货码/二维码）
		"pickup_code": o.PickupCode,
		"paid_at":         o.PayTime,
		"delivered_at":    o.DeliverTime,
		"received_at":     o.CompleteTime,
		// 待支付订单的超时截止时间，供前端展示倒计时（与 order-service 超时取消同一配置）
		"expire_at": orderExpireAt(o.Status, o.CreatedAt, h.orderTimeoutSec),
	}
	response.Success(c, data)
}

// orderExpireAt 计算待支付订单的超时截止时间
// 仅 status=1（待支付）时返回 created_at + order_timeout，其余状态返回空串
// 参数：
//   - status: 订单状态
//   - createdAt: 订单创建时间（格式 2006-01-02 15:04:05）
//   - orderTimeoutSec: 超时秒数
//
// 返回值：
//   - string: 截止时间字符串，非待支付订单返回空串
func orderExpireAt(status int32, createdAt string, orderTimeoutSec int) string {
	if status != 1 || createdAt == "" {
		return ""
	}
	created, err := time.ParseInLocation("2006-01-02 15:04:05", createdAt, time.Local)
	if err != nil {
		return ""
	}
	return created.Add(time.Duration(orderTimeoutSec) * time.Second).Format("2006-01-02 15:04:05")
}

// orderPayType 查询订单支付类型数值
// 未支付或查询不到支付记录时返回 0
// 参数：
//   - c: gin 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - int32: 支付类型（1微信 2支付宝 3货到付款；0=未支付/未知）
func (h *OrderHandler) orderPayType(c *gin.Context, orderID int64) int32 {
	resp, err := h.paymentClient.GetPayment(c.Request.Context(), &payment.GetPaymentReq{OrderId: orderID})
	if err != nil || resp == nil {
		return 0
	}
	return resp.PayType
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
	switch resp.PayType {
	case 1:
		return "微信支付"
	case 2:
		return "支付宝支付"
	case 3:
		return "货到付款"
	default:
		return ""
	}
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

// GetRefundConfig 获取退款配置（C 端发起退款时展示运费策略用）
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
		"refund_freight_policy": resp.RefundFreightPolicy,
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
		if deliveryType == 1 {
			return "待自提"
		}
		return "待发货"
	case 3:
		return "备货中"
	case 4:
		if deliveryType == 1 {
			return "待自提"
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
