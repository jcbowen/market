package handler

import (
	"log"
	"strconv"

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
}

// NewOrderHandler 创建订单处理器
// 参数：
//   - orderClient: order-service gRPC 客户端
//   - userClient: user-service gRPC 客户端
//   - paymentClient: payment-service gRPC 客户端，用于订单详情展示支付方式
//
// 返回值：
//   - *OrderHandler: 订单处理器实例
func NewOrderHandler(orderClient order.OrderServiceClient, userClient user.UserServiceClient, paymentClient payment.PaymentServiceClient) *OrderHandler {
	return &OrderHandler{orderClient: orderClient, userClient: userClient, paymentClient: paymentClient}
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
	resp, err := h.orderClient.CreateOrder(c.Request.Context(), &order.CreateOrderReq{
		UserId:       uc.UserID,
		CommunityId:  uc.CommunityID,
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
// 支持 status、community_id 与分页
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) ListOrders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))

	uc := appcontext.GetUserContext(c)
	communityID := uc.CommunityID
	if id, err := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64); err == nil && id > 0 {
		communityID = id
	}

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
				items = append(items, gin.H{
					"product_id":  item.ProductId,
					"product_name": item.ProductName,
					"sku_id":      item.SkuId,
					"sku_name":    item.SpecName,
					"price":       item.Price,
					"quantity":    item.Quantity,
					"image":       item.ProductImage,
				})
			}
		}
		list = append(list, gin.H{
			"id":           o.Id,
			"order_no":     o.OrderNo,
			"status":       o.Status,
			"status_text":  clientOrderStatusText(o.Status),
			"total_amount": o.TotalAmount,
			"pay_amount":   o.PayAmount,
			"items":        items,
			"created_at":   o.CreatedAt,
		})
	}
	response.SuccessWithPage(c, list, int(resp.Pagination.Total), page, pageSize)
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
		"id":           0,
		"contact_name": "",
		"contact_phone": "",
		"province":     "",
		"city":         "",
		"district":     "",
		"detail":       "",
		"tag":          "",
		"is_default":   false,
	}
	if o != nil && o.ContactName != "" {
		address["contact_name"] = o.ContactName
		address["contact_phone"] = o.ContactPhone
		address["detail"] = o.ContactAddress
	}

	items := make([]gin.H, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, gin.H{
			"product_id":  item.ProductId,
			"product_name": item.ProductName,
			"sku_id":      item.SkuId,
			"sku_name":    item.SpecName,
			"price":       item.Price,
			"quantity":    item.Quantity,
			"image":       item.ProductImage,
		})
	}

	data := gin.H{
		"id":            o.Id,
		"order_no":      o.OrderNo,
		"status":        o.Status,
		"status_text":   clientOrderStatusText(o.Status),
		"total_amount":  o.TotalAmount,
		"pay_amount":    o.PayAmount,
		"pay_type_text": h.orderPayTypeText(c, o.Id),
		"items":         items,
		"address":       address,
		"remark":        o.Remark,
		"created_at":    o.CreatedAt,
		"paid_at":       o.PayTime,
		"delivered_at":  o.DeliverTime,
		"received_at":   o.CompleteTime,
	}
	response.Success(c, data)
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
// 仅配送中（status=4）或自提已支付（status=2 且 delivery_type=1）订单可确认收货
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
	canReceive := o.Status == 4 || (o.Status == 2 && o.DeliveryType == 1)
	if !canReceive {
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

// ApplyRefundReq 申请退款请求
type ApplyRefundReq struct {
	Amount float64 `json:"amount" binding:"required"`
	Reason string  `json:"reason" binding:"required"`
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

	uc := appcontext.GetUserContext(c)
	resp, err := h.orderClient.ApplyRefund(c.Request.Context(), &order.ApplyRefundReq{
		OrderId: id,
		UserId:  uc.UserID,
		Amount:  req.Amount,
		Reason:  req.Reason,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// clientOrderStatusText 将订单状态码转换为 C 端展示文案
// 参数：
//   - status: 状态码
//
// 返回值：
//   - string: 状态中文描述
func clientOrderStatusText(status int32) string {
	switch status {
	case 1:
		return "待支付"
	case 2:
		return "待发货"
	case 3:
		return "备货中"
	case 4:
		return "待收货"
	case 5:
		return "已完成"
	case 6:
		return "已取消"
	case 7:
		return "退款中"
	case 8:
		return "已退款"
	case 9:
		return "退款/售后"
	default:
		return "未知"
	}
}
