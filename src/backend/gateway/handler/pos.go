package handler

import (
	"context"
	"fmt"
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
 * - 代客下单：复用 order.CreateOrder（source=2 收银台），收银员选择支付方式当场结清：
 *   现金(4)创建支付单即收款；付款码(5/6)为两步化——先建待支付支付单，扫码后 pay-code 扣款；
 *   散客（user_id=0）免会员下单
 * - 零元单（全额优惠）统一走支付单（amount=0 创建即成功），保持「订单必有支付记录」口径；
 *   付款码渠道不受理 0 元交易，全额优惠单只能收现金
 * - 挂单：pos_held_orders 后端暂存草稿，不生成正式订单、不占库存
 * - 订单归属当前后台社区语境（X-Community-Id），会员可选任意社区（到店消费语义）
 */

// 收银台支付方式：现金(4) / 微信付款码(5) / 支付宝付款码(6)
// 支付单与订单落库随 PayType 透传，渠道就绪校验见 ensurePosCodePayReady
const (
	posPayTypeCash       int32 = 4
	posPayTypeWechatCode int32 = 5
	posPayTypeAlipayCode int32 = 6
)

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
	// 收银台支付方式：缺省现金；可选现金(4)/微信付款码(5)/支付宝付款码(6)
	if req.PayType == 0 {
		req.PayType = posPayTypeCash
	}
	switch req.PayType {
	case posPayTypeCash, posPayTypeWechatCode, posPayTypeAlipayCode:
	default:
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
	// 付款码支付(5/6)为两步化的第一步：只创建待支付支付单（不扣款），扫码扣款走 PayPosOrderByCode
	codePay := req.PayType == posPayTypeWechatCode || req.PayType == posPayTypeAlipayCode
	if codePay {
		// 渠道就绪前置校验：未配置直接提示改用现金，避免产生无法收款的待支付单
		if err := h.ensurePosCodePayReady(c.Request.Context(), req.PayType); err != nil {
			response.Error(c, errcode.OperationNotAllowed, err.Error())
			return
		}
		// 渠道不受理 0 元扣款，全额优惠单只能收现金；此时订单已创建（结算结果只能下单后确定），
		// best-effort 自动取消刚建的订单，避免留下无支付单的孤儿待支付订单
		if resp.PayAmount <= 0 {
			h.cancelOrphanPosOrder(c, resp.Id, communityID, "收银台 0 元订单误选付款码，自动取消")
			response.Error(c, errcode.OperationNotAllowed, "付款码支付不支持 0 元订单，请使用现金收款")
			return
		}
	}
	payResp, err := h.paymentClient.CreatePayment(c.Request.Context(), &payment.CreatePaymentReq{
		OrderId:     resp.Id,
		CommunityId: communityID,
		OrderNo:     resp.OrderNo,
		UserId:      req.UserID,
		PayType:     req.PayType,
		Amount:      resp.PayAmount,
	})
	if err != nil {
		log.Printf("收银台创建支付单失败 order_id=%d pay_type=%d: %v", resp.Id, req.PayType, err)
		response.Error(c, errcode.InternalServerError, "订单已创建（"+resp.OrderNo+"）但创建支付单失败，请在订单列表处理")
		return
	}

	response.Success(c, gin.H{
		"id":           resp.Id,
		"order_no":     resp.OrderNo,
		"pay_amount":   resp.PayAmount,
		"delivery_fee": resp.DeliveryFee,
		"pickup_code":  resp.PickupCode,
		// 现金创建即收款完成；付款码为两步化此处仅建待支付单，paid=false，待扫码扣款后确认
		"paid":       !codePay,
		"payment_no": payResp.PaymentNo,
	})
}

// cancelOrphanPosOrder best-effort 取消收银台下单后无法收款的孤儿订单
// 场景：付款码渠道不受理 0 元交易，但结算金额（券/改价）只能在下单后确定，
// 命中时订单已落库——复用后台取消语义（UpdateOrderStatus status=6，服务端条件更新并回补库存）
// 把孤儿单收掉，保持「订单必有支付记录」口径。取消失败仅记日志（订单仍可人工取消/超时自动取消），
// 不影响本次请求的错误返回
// 参数：
//   - c: gin 上下文（取操作人）
//   - orderID: 刚创建的订单 ID
//   - communityID: 当前后台社区语境
//   - reason: 取消原因（写入订单备注）
func (h *AdminHandler) cancelOrphanPosOrder(c *gin.Context, orderID int64, communityID int64, reason string) {
	uc := appcontext.GetUserContext(c)
	operatorID := int64(0)
	if uc != nil {
		operatorID = uc.UserID
	}
	if _, err := h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:           orderID,
		Status:       6,
		OperatorType: "admin",
		OperatorId:   operatorID,
		Remark:       reason,
		CommunityId:  communityID,
	}); err != nil {
		log.Printf("收银台孤儿订单自动取消失败 order_id=%d: %v", orderID, err)
	}
}

// ensurePosCodePayReady 校验收银台付款码支付对应渠道是否可用
// 占位 pay_type 由 ListPosPayMethods 按实际可用渠道动态下发（两渠道齐备以 5 占位、
// 仅支付宝可用下发 6），故此处按 payType 精确校验对应渠道，与 payment 服务下单校验同口径，
// 可用 = pay_config 中该渠道开关开启且配置就绪
// 参数：
//   - ctx: 上下文
//   - payType: 支付方式（5=微信付款码 6=支付宝付款码）
//
// 返回值：
//   - error: 对应渠道不可用或 payType 非法时返回带提示的错误
func (h *AdminHandler) ensurePosCodePayReady(ctx context.Context, payType int32) error {
	cfg, err := h.paymentClient.GetPayConfig(ctx, &payment.GetPayConfigReq{})
	if err != nil {
		return fmt.Errorf("支付配置读取失败，请改用现金收款")
	}
	switch payType {
	case posPayTypeWechatCode:
		if isChannelUsable("wechat", cfg) {
			return nil
		}
		return fmt.Errorf("微信付款码收款未开启，请使用现金收款")
	case posPayTypeAlipayCode:
		if isChannelUsable("alipay", cfg) {
			return nil
		}
		return fmt.Errorf("支付宝付款码收款未开启，请使用现金收款")
	}
	return fmt.Errorf("该支付方式暂未开通，请选择现金收款")
}

// ListPosPayMethods 收银台可用支付方式（支付方式动态渲染的数据源）
// 现金恒可用；付款码选项按实际可用渠道动态下发占位 pay_type：两渠道齐备以 5 占位
// （label 用通用「付款码支付」，渠道由扫码时的付款码前缀自动识别）、仅微信可用下发
// 5「微信付款码」、仅支付宝可用下发 6「支付宝付款码」，使下单校验具备真实渠道语义。
// 拉取支付配置失败时降级只回现金（服务端下单仍有渠道校验兜底，不会放过无法收款的方式）
//
// 接口: GET /api/v1/admin/pos/pay-methods
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListPosPayMethods(c *gin.Context) {
	methods := []gin.H{{"pay_type": posPayTypeCash, "label": "现金"}}
	if cfg, err := h.paymentClient.GetPayConfig(c.Request.Context(), &payment.GetPayConfigReq{}); err == nil {
		wechatReady := isChannelUsable("wechat", cfg)
		alipayReady := isChannelUsable("alipay", cfg)
		switch {
		case wechatReady && alipayReady:
			methods = append(methods, gin.H{"pay_type": posPayTypeWechatCode, "label": "付款码支付"})
		case wechatReady:
			methods = append(methods, gin.H{"pay_type": posPayTypeWechatCode, "label": "微信付款码"})
		case alipayReady:
			methods = append(methods, gin.H{"pay_type": posPayTypeAlipayCode, "label": "支付宝付款码"})
		}
	}
	response.Success(c, gin.H{"list": methods})
}

// PayPosOrderCodeReq 收银台付款码扣款请求（两步化第二步）
type PayPosOrderCodeReq struct {
	// 待支付支付单号（CreatePosOrder 响应返回）
	PaymentNo string `json:"payment_no" binding:"required"`
	// 顾客付款码（扫码枪读取：微信 10-15 开头 / 支付宝 25-30 开头，纯数字 16~64 位）
	AuthCode string `json:"auth_code" binding:"required"`
}

// PayPosOrderByCode 收银台付款码扣款：对下单创建的待支付支付单提交顾客付款码向渠道收款
// 结果归一化：PAID=收款成功（订单已同步已支付，直接出小票）；PAYING=用户支付中（输密码等），
// 前端轮询 GET 本接口确认最终结果；FAILED=渠道确定性拒绝（码无效/余额不足等），
// 提示重新扫码——失败重试复用同一支付单（out_trade_no 不变），不会产生多笔渠道交易
//
// 接口: POST /api/v1/admin/pos/orders/:id/pay-code
//
// 参数：
//   - c: gin 上下文，路径 :id 为订单 ID
func (h *AdminHandler) PayPosOrderByCode(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	var req PayPosOrderCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 社区隔离：只能对本社区语境内的订单付款码收款
	if _, err := h.orderInCommunity(c, id); err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.paymentClient.PayByAuthCode(c.Request.Context(), &payment.PayByAuthCodeReq{
		PaymentNo:  req.PaymentNo,
		AuthCode:   req.AuthCode,
		OperatorId: uc.UserID,
		// 订单 ID（已过社区隔离）：服务端强校验与支付单的绑定关系，防跨订单收款
		OrderId: id,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"state":          resp.State,
		"message":        resp.Message,
		"payment_no":     resp.PaymentNo,
		"order_id":       resp.OrderId,
		"transaction_id": resp.TransactionId,
		"pay_type":       resp.PayType,
		"amount":         resp.Amount,
	})
}

// GetPosOrderPayStatus 收银台付款码收款结果查询（PAYING 在途轮询用）
// 向渠道核实该订单是否已收款，已收款则补同步订单与支付单；幂等可反复轮询：
// 渠道未收款时只回传渠道事实（channel_state），不改动本地状态
//
// 接口: GET /api/v1/admin/pos/orders/:id/pay-code?payment_no=ZFxxx
//
// 参数：
//   - c: gin 上下文，路径 :id 为订单 ID，payment_no 可选定位支付单
func (h *AdminHandler) GetPosOrderPayStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	// 社区隔离 + 读出订单（同步要求订单仍处待支付族，取消单只能走退款）
	o, err := h.orderInCommunity(c, id)
	if err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}
	if o.Status != 1 && o.Status != 100 {
		// 订单已不在待支付族：直接把事实告诉收银台（付款成功单已被同步为已支付）
		response.Success(c, gin.H{"paid": o.Status == 2, "changed": false, "order_status": o.Status})
		return
	}

	// reason 留空即由支付服务按 manual-sync 记入 payments.confirm_ref
	resp, err := h.paymentClient.SyncPaymentFromChannel(c.Request.Context(), &payment.SyncPaymentReq{
		OrderId:   id,
		PaymentNo: c.Query("payment_no"),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"paid":           resp.Paid,
		"changed":        resp.Changed,
		"channel_state":  resp.ChannelState,
		"transaction_id": resp.TransactionId,
		"amount":         resp.Amount,
		"payment_no":     resp.PaymentNo,
		"message":        resp.Message,
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
