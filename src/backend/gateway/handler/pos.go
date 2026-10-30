package handler

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/channel"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/user"
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
	ManualDiscount float64 `json:"manual_discount"`
	// 直接收款金额（「直接收款」无商品单专用）：>0 时 items 可为空，
	// 订单不含商品明细，金额即成交金额；仅支持到店（delivery_type=3），不参与券/满减/手动优惠
	ManualAmount float64           `json:"manual_amount"`
	Items        []PosOrderItemReq `json:"items"`
}

// CreatePosOrder 代客下单（收银台）
// 流程：校验进行中班次（强制开工，无班次拒绝收银）→ 付款码渠道就绪前置校验（不可用提示改用现金，
// 先于建单，避免留下无法收款的孤儿待支付订单）→ 创建订单（source=2，经手人=当前管理员，
// 归属当前班次）→ 按所选支付方式创建支付单（现金创建即支付成功，含 0 元全额优惠单；
// 付款码仅建待支付支付单，扫码扣款走 PayPosOrderByCode）→ 返回订单信息。
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

	// 直接收款单（无商品，manual_amount>0）：仅到店，金额即成交金额，
	// 不允许叠加商品明细/券/手动优惠（order 服务同口径兜底，防绕过网关直调 RPC 扣库存不建明细）；
	// 常规单必须带商品明细
	isManualOrder := req.ManualAmount > 0
	if isManualOrder {
		if req.DeliveryType != 3 {
			response.Error(c, errcode.ParamError, "直接收款仅支持到店模式")
			return
		}
		if req.ManualAmount < 0.01 {
			response.Error(c, errcode.ParamError, "收款金额不能低于 0.01 元")
			return
		}
		if req.ManualAmount >= 1000000 {
			response.Error(c, errcode.ParamError, "收款金额超出单笔上限")
			return
		}
		if len(req.Items) > 0 {
			response.Error(c, errcode.ParamError, "直接收款单不支持商品明细，请走商品结账或使用订单查询继续收款")
			return
		}
		req.CouponID = 0
		req.ManualDiscount = 0
	} else if len(req.Items) == 0 {
		response.Error(c, errcode.ParamError, "订单商品不能为空")
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

	// 付款码支付(5/6)为两步化：先建待支付支付单，扫码后 pay-code 扣款
	codePay := req.PayType == posPayTypeWechatCode || req.PayType == posPayTypeAlipayCode
	// 渠道就绪前置校验（P1-8）：必须置于建单之前——建单后才发现渠道不可用，
	// 会留下一笔无法收款的孤儿待支付订单（收银员已出小票、顾客已离场的场景尤其实时）
	if codePay {
		if err := h.ensurePosCodePayReady(c.Request.Context(), req.PayType); err != nil {
			response.Error(c, errcode.OperationNotAllowed, err.Error())
			return
		}
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
		ManualAmount:   req.ManualAmount,
	})
	if err != nil {
		log.Printf("收银台创建订单失败: %v", err)
		response.GRPCError(c, err)
		return
	}

	// 当场结清：统一创建支付单（含 0 元全额优惠单，amount=0 创建即成功），保持「订单必有支付记录」口径
	// 社区与订单号随订单一并带入，支付异常队列据此按社区隔离、后台据此展示可核对的订单号
	// 付款码渠道不受理 0 元扣款，全额优惠单只能收现金；结算金额（券/改价）只能下单后确定，
	// 命中时订单已落库——best-effort 自动取消刚建的订单，避免留下无支付单的孤儿待支付订单
	if codePay && resp.PayAmount <= 0 {
		h.cancelOrphanPosOrder(c, resp.Id, communityID, "收银台 0 元订单误选付款码，自动取消")
		response.Error(c, errcode.OperationNotAllowed, "付款码支付不支持 0 元订单，请使用现金收款")
		return
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

// isPosPaidStatus 判定订单是否处于已收款状态族（P1-7）
// 2已支付 3备货中 4已发货 5已完成 7退款中 8已退款——后四态均由已收款订单流转而来，
// 退款态也以收款为前提。收银台轮询「是否已收款」必须整族判定：付款成功后订单会被
// 后续流程推离 2，只认 2 会把已收款单误报未收款，诱导重复扣款
// 参数：
//   - status: 订单状态
//
// 返回值：
//   - bool: 已收款族返回 true
func isPosPaidStatus(status int32) bool {
	switch status {
	case 2, 3, 4, 5, 7, 8:
		return true
	default:
		return false
	}
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
// 可用 = POS 渠道绑定了该平台的支付配置 且 该配置启用就绪
// 参数：
//   - ctx: 上下文
//   - payType: 支付方式（5=微信付款码 6=支付宝付款码）
//
// 返回值：
//   - error: 对应渠道不可用或 payType 非法时返回带提示的错误
func (h *AdminHandler) ensurePosCodePayReady(ctx context.Context, payType int32) error {
	bound, err := h.posBoundPayMethods(ctx)
	if err != nil {
		return fmt.Errorf("支付配置读取失败，请改用现金收款")
	}
	var plat string
	switch payType {
	case posPayTypeWechatCode:
		plat = payPlatformWechat
	case posPayTypeAlipayCode:
		plat = payPlatformAlipay
	default:
		return fmt.Errorf("该支付方式暂未开通，请选择现金收款")
	}
	if _, ok := h.posReadyConfig(ctx, bound, plat); !ok {
		return fmt.Errorf("%s付款码收款未开启，请使用现金收款", posPlatformLabel(plat))
	}
	return nil
}

// posPlatformLabel 平台标识的中文名（错误提示用）
func posPlatformLabel(platform string) string {
	if platform == payPlatformAlipay {
		return "支付宝"
	}
	return "微信"
}

// posBoundPayMethods 读取 POS 收银台渠道绑定的支付方式列表
// POS 为特殊渠道：无登录凭证、恒启用，仅参与支付方式绑定
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - []string: 渠道绑定的支付方式列表（"cash" 或 "<platform>:<config_id>"）
//   - error: 渠道查询失败错误
func (h *AdminHandler) posBoundPayMethods(ctx context.Context) ([]string, error) {
	resp, err := h.userClient.GetClientChannel(ctx, &user.GetClientChannelReq{Code: channel.POS})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	return resp.PayMethods, nil
}

// posReadyConfig 从 POS 绑定与就绪集合中解析指定平台的可用支付配置
// POS 付款码按前缀自动识别平台，故每平台至多绑定一条配置（保存时校验）
//
// 参数：
//   - ctx: 上下文
//   - methods: POS 渠道绑定的支付方式列表
//   - platform: 平台标识（wechat/alipay）
//
// 返回值：
//   - int64: 可用支付配置 ID，不可用时为 0
//   - bool: 是否存在启用且就绪的绑定配置
func (h *AdminHandler) posReadyConfig(ctx context.Context, methods []string, platform string) (int64, bool) {
	configID := firstConfigIDForPlatform(methods, platform)
	if configID == 0 {
		return 0, false
	}
	set := listActivePayMethodSet(ctx, h.paymentClient)
	if set == nil {
		// 就绪集合拉取失败：降级为绑定即视为可用，最终由 payment 服务扣款校验兜底
		return configID, true
	}
	if _, ok := set[fmt.Sprintf("%s:%d", platform, configID)]; !ok {
		return 0, false
	}
	return configID, true
}

// authCodePlatform 按付款码前缀识别支付平台
// 微信付款码 10-15 开头 / 支付宝付款码 25-30 开头（均为纯数字）
//
// 参数：
//   - authCode: 顾客付款码
//
// 返回值：
//   - string: 平台标识（wechat/alipay），无法识别时为空串
func authCodePlatform(authCode string) string {
	authCode = strings.TrimSpace(authCode)
	if len(authCode) < 2 {
		return ""
	}
	switch authCode[:2] {
	case "10", "11", "12", "13", "14", "15":
		return payPlatformWechat
	case "25", "26", "27", "28", "29", "30":
		return payPlatformAlipay
	}
	return ""
}

// ListPosPayMethods 收银台可用支付方式（支付方式动态渲染的数据源）
// 现金恒可用；付款码选项按 POS 渠道绑定与配置就绪情况动态下发占位 pay_type：
// 两渠道齐备以 5 占位（label 用通用「付款码支付」，渠道由扫码时的付款码前缀自动识别）、
// 仅微信可用下发 5「微信付款码」、仅支付宝可用下发 6「支付宝付款码」，使下单校验具备真实渠道语义。
// 拉取失败时降级只回现金（服务端下单仍有渠道校验兜底，不会放过无法收款的方式）
//
// 接口: GET /api/v1/admin/pos/pay-methods
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListPosPayMethods(c *gin.Context) {
	methods := []gin.H{{"pay_type": posPayTypeCash, "label": "现金"}}
	if bound, err := h.posBoundPayMethods(c.Request.Context()); err == nil {
		_, wechatReady := h.posReadyConfig(c.Request.Context(), bound, payPlatformWechat)
		_, alipayReady := h.posReadyConfig(c.Request.Context(), bound, payPlatformAlipay)
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

	// 按付款码前缀识别平台，取 POS 渠道绑定的对应支付配置
	// （PayByAuthCode 服务端会再校验配置存在、启用、平台匹配且凭证就绪）
	plat := authCodePlatform(req.AuthCode)
	if plat == "" {
		response.Error(c, errcode.ParamError, "无法识别付款码类型，请确认扫描的是微信/支付宝付款码")
		return
	}
	bound, err := h.posBoundPayMethods(c.Request.Context())
	if err != nil {
		response.Error(c, errcode.InternalServerError, "支付配置读取失败，请改用现金收款")
		return
	}
	configID, ok := h.posReadyConfig(c.Request.Context(), bound, plat)
	if !ok {
		response.Error(c, errcode.OperationNotAllowed, fmt.Sprintf("%s付款码收款未开启，请使用现金收款", posPlatformLabel(plat)))
		return
	}

	resp, err := h.paymentClient.PayByAuthCode(c.Request.Context(), &payment.PayByAuthCodeReq{
		PaymentNo:  req.PaymentNo,
		AuthCode:   req.AuthCode,
		OperatorId: uc.UserID,
		// 订单 ID（已过社区隔离）：服务端强校验与支付单的绑定关系，防跨订单收款
		OrderId: id,
		// 支付配置：按 POS 渠道绑定解析（付款码前缀识别平台后取对应配置）
		ConfigId: configID,
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
		// 订单已不在待支付族：直接把事实告诉收银台。
		// paid 判定必须覆盖已收款状态族（P1-7）：付款成功后订单可能已被发货/核销/完成
		// 推往 3/4/5，退款中(7)/已退款(8)同样以收款为前提——只认 2 会把「已收款」
		// 误报成未收款，诱导收银台重复扣款
		response.Success(c, gin.H{"paid": isPosPaidStatus(o.Status), "changed": false, "order_status": o.Status})
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

// PosPayableOrder 收银台待支付订单校验：读取订单并确认可继续收款
// 口径：本社区 + 收银台来源(source=2) + 待支付(status=1)。
// C 端待支付单不在收银台代收范围（用户侧已有在线支付入口，双通道并发收款风险大于收益）
// 参数：
//   - c: gin 上下文（社区语境与操作人）
//   - id: 订单 ID
//
// 返回值：
//   - *order.OrderInfo: 校验通过的订单
//   - bool: 校验是否通过（未通过时已直接写响应，调用方须立即 return）
func (h *AdminHandler) PosPayableOrder(c *gin.Context, id int64) (*order.OrderInfo, bool) {
	o, err := h.orderInCommunity(c, id)
	if err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return nil, false
	}
	if o.Source != 2 {
		response.Error(c, errcode.OperationNotAllowed, "仅收银台订单支持继续收款，C 端订单请联系顾客线上支付")
		return nil, false
	}
	if o.Status != 1 {
		response.Error(c, errcode.OperationNotAllowed, "订单已不在待支付状态，请刷新后查看")
		return nil, false
	}
	return o, true
}

// PayPosOrderByCash 收银台待支付订单改用现金收款（扣款失败换现金/订单查询继续收现金）
// 编排：关闭订单遗留的待支付支付单（含付款码单，防止旧码事后仍可扣款）→ 创建现金支付单
// （现金创建即成功，payment 服务事务提交后自动同步订单为已支付，到店单由 order 侧自动完结）。
// 不校验进行中班次：订单已归属创建时班次，跨班次补收属既有交班统计口径的自然延伸
//
// 接口: POST /api/v1/admin/pos/orders/:id/pay-cash
//
// 参数：
//   - c: gin 上下文，路径 :id 为订单 ID
func (h *AdminHandler) PayPosOrderByCash(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	o, ok := h.PosPayableOrder(c, id)
	if !ok {
		return
	}

	// 先关遗留待支付支付单：付款码单关掉后旧 payment_no 不可再扣款，杜绝双通道重复收款。
	// 关单失败不建现金单（避免旧码+现金并存），报错让收银员重试
	if _, err := h.paymentClient.ClosePayments(c.Request.Context(), &payment.ClosePaymentsReq{OrderId: id}); err != nil {
		log.Printf("收银台现金收款关闭遗留支付单失败 order_id=%d: %v", id, err)
		response.Error(c, errcode.InternalServerError, "关闭原待支付单失败，请重试")
		return
	}

	// 创建现金支付单：payment 侧创建即置为已支付，事务提交后自动同步订单状态（3 次重试 + 异常队列兜底）
	if _, err := h.paymentClient.CreatePayment(c.Request.Context(), &payment.CreatePaymentReq{
		OrderId:     id,
		CommunityId: o.CommunityId,
		OrderNo:     o.OrderNo,
		UserId:      o.UserId,
		PayType:     posPayTypeCash,
		Amount:      o.PayAmount,
	}); err != nil {
		log.Printf("收银台现金收款创建支付单失败 order_id=%d: %v", id, err)
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"paid":       true,
		"order_id":   id,
		"order_no":   o.OrderNo,
		"pay_amount": o.PayAmount,
		"pay_type":   posPayTypeCash,
	})
}

// EnsurePosCodePaySession 收银台待支付订单重建付款码收款会话（订单查询「继续收款」入口）
// 复用 CreatePayment 的幂等语义：同渠道同金额的待支付单直接复用（out_trade_no 不变），
// 遗留待支付单金额不符/渠道变化时服务端自动关旧建新。
// 现金收款走 PayPosOrderByCash，不经过本接口
//
// 接口: POST /api/v1/admin/pos/orders/:id/pay-code-session
//
// 参数：
//   - c: gin 上下文，路径 :id 为订单 ID
func (h *AdminHandler) EnsurePosCodePaySession(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	o, ok := h.PosPayableOrder(c, id)
	if !ok {
		return
	}

	// 占位 pay_type 与 ListPosPayMethods 下发口径一致：两渠道齐备以 5 占位（渠道按码前缀识别）、
	// 仅支付宝可用下发 6。此处按 POS 渠道绑定与配置就绪情况选占位值，渠道未绑定时报错提示改现金
	codePayType := posPayTypeWechatCode
	bound, err := h.posBoundPayMethods(c.Request.Context())
	if err != nil {
		response.Error(c, errcode.InternalServerError, "支付配置读取失败，请改用现金收款")
		return
	}
	_, wechatReady := h.posReadyConfig(c.Request.Context(), bound, payPlatformWechat)
	_, alipayReady := h.posReadyConfig(c.Request.Context(), bound, payPlatformAlipay)
	if !wechatReady {
		if !alipayReady {
			response.Error(c, errcode.OperationNotAllowed, "付款码收款未开启，请使用现金收款")
			return
		}
		codePayType = posPayTypeAlipayCode
	}

	resp, err := h.paymentClient.CreatePayment(c.Request.Context(), &payment.CreatePaymentReq{
		OrderId:     id,
		CommunityId: o.CommunityId,
		OrderNo:     o.OrderNo,
		UserId:      o.UserId,
		PayType:     codePayType,
		Amount:      o.PayAmount,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"payment_no": resp.PaymentNo,
		"pay_type":   codePayType,
		"order_id":   id,
		"order_no":   o.OrderNo,
		"pay_amount": o.PayAmount,
	})
}
