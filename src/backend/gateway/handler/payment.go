package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
)

// 支付类型：1=微信，2=支付宝，3=现金/货到付款
const (
	payTypeWechat int32 = 1
	payTypeAlipay int32 = 2
	payTypeCash   int32 = 3
)

// PaymentHandler 支付接口处理器
type PaymentHandler struct {
	paymentClient payment.PaymentServiceClient
	orderClient   order.OrderServiceClient
	userClient    user.UserServiceClient
}

// NewPaymentHandler 创建支付处理器
// 参数：
//   - paymentClient: payment-service gRPC 客户端
//   - orderClient: order-service gRPC 客户端
//   - userClient: user-service gRPC 客户端，用于查询用户微信 openid
//
// 返回值：
//   - *PaymentHandler: 支付处理器实例
func NewPaymentHandler(paymentClient payment.PaymentServiceClient, orderClient order.OrderServiceClient, userClient user.UserServiceClient) *PaymentHandler {
	return &PaymentHandler{paymentClient: paymentClient, orderClient: orderClient, userClient: userClient}
}

// CreatePaymentReq 创建支付请求
// 微信 openid 不接受前端传入，统一由服务端从用户资料取得，避免伪造
type CreatePaymentReq struct {
	OrderID int64  `json:"order_id" binding:"required"`
	Channel string `json:"channel" binding:"required,oneof=wechat alipay cash"`
}

// channelToPayType 将支付渠道字符串转换为内部支付类型
// 参数：
//   - channel: 支付渠道，支持 wechat/alipay/cash
//
// 返回值：
//   - int32: 内部支付类型，1=微信，2=支付宝，3=现金/货到付款
//   - bool: 是否识别成功
func channelToPayType(channel string) (int32, bool) {
	switch channel {
	case "wechat":
		return payTypeWechat, true
	case "alipay":
		return payTypeAlipay, true
	case "cash":
		return payTypeCash, true
	default:
		return 0, false
	}
}

// isChannelEnabled 检查支付渠道是否被后台配置开启
// 参数：
//   - channel: 支付渠道
//   - enabled: 已开启渠道列表
//
// 返回值：
//   - bool: 是否开启
func isChannelEnabled(channel string, enabled []string) bool {
	for _, ch := range enabled {
		if ch == channel {
			return true
		}
	}
	return false
}

// getPayConfigRuntime 获取支付配置运行时信息
// 从 payment 服务读取数据库中的支付渠道开关与渠道就绪状态
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - *payment.PayConfig: 支付配置，读取失败时返回 nil
//   - error: 读取失败错误
func (h *PaymentHandler) getPayConfigRuntime(ctx context.Context) (*payment.PayConfig, error) {
	return h.paymentClient.GetPayConfig(ctx, &payment.GetPayConfigReq{})
}

// isChannelUsable 判断渠道在配置开关中开启且实际配置完整
// 参数：
//   - channel: 支付渠道
//   - cfg: 支付配置
//
// 返回值：
//   - bool: 是否可用
func isChannelUsable(channel string, cfg *payment.PayConfig) bool {
	if cfg == nil || !isChannelEnabled(channel, cfg.EnabledChannels) {
		return false
	}
	switch channel {
	case "wechat":
		return cfg.WechatPaymentReady
	case "alipay":
		return false
	case "cash":
		return true
	default:
		return false
	}
}

// CreatePayment 创建支付单
// 根据 order_id 查询订单金额，channel 转换为 pay_type，再调用 payment-service 创建支付单
// 会校验当前渠道是否在后台支付开关配置中开启
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) CreatePayment(c *gin.Context) {
	var req CreatePaymentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	payCfg, err := h.getPayConfigRuntime(c.Request.Context())
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if !isChannelUsable(req.Channel, payCfg) {
		response.Error(c, errcode.ParamError, "当前支付方式未开启或配置不完整")
		return
	}

	uc := appcontext.GetUserContext(c)

	// 查询订单详情，获取应付金额并校验订单归属
	orderResp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{
		Id:     req.OrderID,
		UserId: uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if orderResp.Order == nil || orderResp.Order.Id == 0 {
		response.Error(c, errcode.NotFound, "订单不存在")
		return
	}
	// 仅待支付订单可发起支付，防止对已支付/已完成订单重复创建支付单
	if orderResp.Order.Status != 1 {
		response.Error(c, errcode.OperationNotAllowed, "订单当前状态不可支付")
		return
	}

	payType, ok := channelToPayType(req.Channel)
	if !ok {
		response.Error(c, errcode.ParamError, "不支持该支付渠道")
		return
	}

	// 微信支付需按 openid 有无区分 JSAPI / H5，openid 由服务端解析
	var openid string
	if payType == payTypeWechat {
		openid = h.resolveWechatOpenid(c.Request.Context(), uc.UserID)
	}

	resp, err := h.paymentClient.CreatePayment(c.Request.Context(), &payment.CreatePaymentReq{
		OrderId: req.OrderID,
		UserId:  uc.UserID,
		PayType: payType,
		Amount:  orderResp.Order.PayAmount,
		Openid:  openid,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// resolveWechatOpenid 解析用户可用于微信 JSAPI 支付的 openid
// 手机号登录用户的 openid 是 phone_<手机号> 占位值，并非微信真实 openid，返回空串使支付回落 H5 通道；
// 查询失败同样返回空串，保证支付流程不被 openid 缺失阻断
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//
// 返回值：
//   - string: 微信 openid，不可用时为空串
func (h *PaymentHandler) resolveWechatOpenid(ctx context.Context, userID int64) string {
	resp, err := h.userClient.GetUserById(ctx, &user.GetUserByIdReq{Id: userID})
	if err != nil || resp == nil {
		return ""
	}
	if resp.Openid == "" || strings.HasPrefix(resp.Openid, phoneOpenidPrefix) {
		return ""
	}
	return resp.Openid
}

// PaymentChannel 支付渠道信息
// 用于返回给前端展示可用支付方式
type PaymentChannel struct {
	Channel string `json:"channel"`
	Name    string `json:"name"`
}

// PaymentChannels 获取当前启用的支付方式列表
// 根据数据库支付配置中的渠道开关与渠道实际配置完整性返回可用渠道
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) PaymentChannels(c *gin.Context) {
	channelNames := map[string]string{
		"wechat": "微信支付",
		"alipay": "支付宝支付",
		"cash":   "货到付款",
	}

	payCfg, err := h.getPayConfigRuntime(c.Request.Context())
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	var list []PaymentChannel
	for _, ch := range payCfg.EnabledChannels {
		if !isChannelUsable(ch, payCfg) {
			continue
		}
		list = append(list, PaymentChannel{
			Channel: ch,
			Name:    channelNames[ch],
		})
	}
	response.Success(c, gin.H{"channels": list})
}

// GetPayment 根据订单 ID 查询支付单
// 先校验订单归属当前用户，防止越权查询他人支付单（含金额、状态等敏感信息）
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) GetPayment(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || orderID <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	// 订单归属校验：GetOrderReq 携带 UserId 由服务端过滤，非本人订单视为不存在
	uc := appcontext.GetUserContext(c)
	orderResp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{
		Id:     orderID,
		UserId: uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if orderResp.Order == nil || orderResp.Order.Id == 0 {
		response.Error(c, errcode.NotFound, "订单不存在")
		return
	}

	resp, err := h.paymentClient.GetPayment(c.Request.Context(), &payment.GetPaymentReq{
		OrderId: orderID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// WechatCallback 微信支付回调
// 读取 raw body 与微信签名头，调用 payment.HandleWechatCallback，返回 XML 成功响应
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) WechatCallback(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	headers := make(map[string]string)
	for k, v := range c.Request.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	_, err = h.paymentClient.HandleWechatCallback(c.Request.Context(), &payment.WechatCallbackReq{
		Body:    body,
		Headers: headers,
	})
	if err != nil {
		c.String(http.StatusOK, "<xml><return_code><![CDATA[FAIL]]></return_code></xml>")
		return
	}
	c.String(http.StatusOK, "<xml><return_code><![CDATA[SUCCESS]]></return_code></xml>")
}
