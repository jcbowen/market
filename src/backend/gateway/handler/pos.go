package handler

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/response"
)

/**
 * 收银台代客下单（G，决策点⑥⑨）
 * - 代客下单：复用 order.CreateOrder（source=2 收银台），收银员选择支付方式当场结清
 *   （本次仅现金 pay_type=4，后续接入微信/支付宝付款码时扩展 5/6）；散客（user_id=0）免会员下单
 * - 零元单（全额优惠）统一走支付单（amount=0 创建即成功），保持「订单必有支付记录」口径
 * - 挂单：pos_held_orders 后端暂存草稿，不生成正式订单、不占库存
 * - 订单归属当前后台社区语境（X-Community-Id），会员可选任意社区（到店消费语义）
 */

// posPayTypeCash 收银台支付方式：现金
// 后续接入微信/支付宝付款码收款时在此扩展常量（如 posPayTypeWechatQR=5），支付单与订单落库随之透传
const posPayTypeCash int32 = 4

// PosOrderItemReq 代客下单商品项
type PosOrderItemReq struct {
	SkuID    int64 `json:"sku_id" binding:"required"`
	Quantity int32 `json:"quantity" binding:"required,min=1"`
	// 单品改价：>0 时以该价成交（仅对当前订单生效，不改商品档案价）；0/缺省=按服务端实时价
	Price float64 `json:"price"`
}

// CreatePosOrderReq 代客下单请求（当场结清）
type CreatePosOrderReq struct {
	// 会员 ID（0=散客）
	UserID int64 `json:"user_id"`
	// 支付方式（4=现金；0 时默认现金。后续扩展付款码渠道）
	PayType int32 `json:"pay_type"`
	// 配送类型：1自提 2配送 3到店（到店仅收银台可用，即买即走，不生成取货码、无需核销）
	DeliveryType   int32  `json:"delivery_type" binding:"required,oneof=1 2 3"`
	ContactName    string `json:"contact_name"`
	ContactPhone   string `json:"contact_phone"`
	ContactAddress string `json:"contact_address"`
	Remark         string `json:"remark"`
	CouponID       int64  `json:"coupon_id"`
	// 手动优惠总额（收银台抹零/整单改价折算）：服务端校验不可超应付
	ManualDiscount float64           `json:"manual_discount"`
	Items          []PosOrderItemReq `json:"items" binding:"required,min=1"`
}

// CreatePosOrder 代客下单（收银台）
// 流程：校验进行中班次（强制开工，无班次拒绝收银）→ 创建订单（source=2，经手人=当前管理员，
// 归属当前班次）→ 按所选支付方式创建支付单（创建即支付成功，含 0 元全额优惠单）→ 返回订单信息。
//
// 接口: POST /api/v1/admin/pos/orders
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreatePosOrder(c *gin.Context) {
	var req CreatePosOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	// 收银台支付方式：缺省现金；本次仅现金可选，后续接入付款码渠道时在此放行
	if req.PayType == 0 {
		req.PayType = posPayTypeCash
	}
	if req.PayType != posPayTypeCash {
		response.Error(c, errcode.ParamError, "该支付方式暂未开通，请选择现金收款")
		return
	}

	// 配送单必须有收货联系信息（收银台无地址簿，收银员直填）
	if req.DeliveryType == 2 && (req.ContactName == "" || req.ContactPhone == "" || req.ContactAddress == "") {
		response.Error(c, errcode.ParamError, "配送订单请填写完整的联系人、电话与地址")
		return
	}

	uc := appcontext.GetUserContext(c)
	communityID := h.getAdminCommunityID(c)

	// 强制开工：无进行中班次不能收银（交班体系，后端强制校验）
	shiftResp, err := h.orderClient.GetCurrentShift(c.Request.Context(), &order.GetCurrentShiftReq{
		CommunityId: communityID,
		OperatorId:  uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if shiftResp == nil || shiftResp.Id <= 0 {
		response.Error(c, errcode.ParamError, "当前没有进行中的班次，请先开工后再收银")
		return
	}

	items := make([]*order.OrderItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, &order.OrderItem{SkuId: item.SkuID, Quantity: item.Quantity, Price: item.Price})
	}

	// 散客单不允许用券（券挂在会员名下）
	couponID := req.CouponID
	if req.UserID <= 0 {
		couponID = 0
	}

	resp, err := h.orderClient.CreateOrder(c.Request.Context(), &order.CreateOrderReq{
		UserId:         req.UserID,
		CommunityId:    communityID,
		DeliveryType:   req.DeliveryType,
		Remark:         req.Remark,
		CouponId:       couponID,
		Items:          items,
		Source:         2,
		OperatorId:     uc.UserID,
		OperatorName:   c.GetString("admin_username"),
		ContactName:    req.ContactName,
		ContactPhone:   req.ContactPhone,
		ContactAddress: req.ContactAddress,
		ManualDiscount: req.ManualDiscount,
		ShiftId:        shiftResp.Id,
	})
	if err != nil {
		log.Printf("收银台创建订单失败: %v", err)
		response.GRPCError(c, err)
		return
	}

	// 当场结清：统一创建支付单（含 0 元全额优惠单，amount=0 创建即成功），保持「订单必有支付记录」口径
	// 社区与订单号随订单一并带入，支付异常队列据此按社区隔离、后台据此展示可核对的订单号
	if _, err := h.paymentClient.CreatePayment(c.Request.Context(), &payment.CreatePaymentReq{
		OrderId:     resp.Id,
		CommunityId: communityID,
		OrderNo:     resp.OrderNo,
		UserId:      req.UserID,
		PayType:     req.PayType,
		Amount:      resp.PayAmount,
	}); err != nil {
		log.Printf("收银台收款失败 order_id=%d pay_type=%d: %v", resp.Id, req.PayType, err)
		response.Error(c, errcode.InternalServerError, "订单已创建（"+resp.OrderNo+"）但收款失败，请在订单列表处理")
		return
	}

	response.Success(c, gin.H{
		"id":           resp.Id,
		"order_no":     resp.OrderNo,
		"pay_amount":   resp.PayAmount,
		"delivery_fee": resp.DeliveryFee,
		"pickup_code":  resp.PickupCode,
		"paid":         true,
	})
}

// CreatePosHeldOrderReq 挂单请求（工作区快照）
type CreatePosHeldOrderReq struct {
	// 会员 ID（0=散客）
	UserID   int64              `json:"user_id"`
	UserName string             `json:"user_name"`
	Remark   string             `json:"remark"`
	Items    []PosHeldItemInput `json:"items" binding:"required,min=1"`
}

// PosHeldItemInput 挂单明细项（含商品快照，恢复时回填收银台展示）
type PosHeldItemInput struct {
	SkuID        int64   `json:"sku_id" binding:"required"`
	Quantity     int32   `json:"quantity" binding:"required,min=1"`
	ProductName  string  `json:"product_name"`
	SpecName     string  `json:"spec_name"`
	ProductImage string  `json:"product_image"`
	Price        float64 `json:"price"`
}

// CreatePosHeldOrder 挂单：暂存当前收银台工作区
//
// 接口: POST /api/v1/admin/pos/held-orders
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreatePosHeldOrder(c *gin.Context) {
	var req CreatePosHeldOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	items := make([]*order.PosHeldOrderItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, &order.PosHeldOrderItem{
			SkuId:        item.SkuID,
			Quantity:     item.Quantity,
			ProductName:  item.ProductName,
			SpecName:     item.SpecName,
			ProductImage: item.ProductImage,
			Price:        item.Price,
		})
	}

	resp, err := h.orderClient.CreatePosHeldOrder(c.Request.Context(), &order.CreatePosHeldOrderReq{
		CommunityId:  h.getAdminCommunityID(c),
		OperatorId:   uc.UserID,
		OperatorName: c.GetString("admin_username"),
		UserId:       req.UserID,
		UserName:     req.UserName,
		Items:        items,
		Remark:       req.Remark,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListPosHeldOrders 挂单列表（当前社区语境；可选 operator_id=1 仅看自己的挂单）
//
// 接口: GET /api/v1/admin/pos/held-orders
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListPosHeldOrders(c *gin.Context) {
	operatorID, _ := strconv.ParseInt(c.DefaultQuery("operator_id", "0"), 10, 64)
	resp, err := h.orderClient.ListPosHeldOrders(c.Request.Context(), &order.ListPosHeldOrdersReq{
		CommunityId: h.getAdminCommunityID(c),
		OperatorId:  operatorID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"list": resp.List})
}

// DeletePosHeldOrder 删除挂单（恢复结账或顾客放弃后调用）
//
// 接口: DELETE /api/v1/admin/pos/held-orders/:id
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeletePosHeldOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	_, err = h.orderClient.DeletePosHeldOrder(c.Request.Context(), &order.DeletePosHeldOrderReq{
		Id:          id,
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}
