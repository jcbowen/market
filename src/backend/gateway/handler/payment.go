package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/response"
)

// PaymentHandler 支付接口处理器
type PaymentHandler struct {
	paymentClient payment.PaymentServiceClient
	orderClient   order.OrderServiceClient
	cfg           *config.Config
}

// NewPaymentHandler 创建支付处理器
// 参数：
//   - paymentClient: payment-service gRPC 客户端
//   - orderClient: order-service gRPC 客户端
//   - cfg: 全局配置
//
// 返回值：
//   - *PaymentHandler: 支付处理器实例
func NewPaymentHandler(paymentClient payment.PaymentServiceClient, orderClient order.OrderServiceClient, cfg *config.Config) *PaymentHandler {
	return &PaymentHandler{paymentClient: paymentClient, orderClient: orderClient, cfg: cfg}
}

// CreatePaymentReq 创建支付请求
type CreatePaymentReq struct {
	OrderID int64  `json:"order_id" binding:"required"`
	Channel string `json:"channel" binding:"required,oneof=wechat alipay cash"`
	Openid  string `json:"openid"`
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
		return 1, true
	case "alipay":
		return 2, true
	case "cash":
		return 3, true
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

	if !isChannelEnabled(req.Channel, h.cfg.Payment.EnabledChannels) {
		response.Error(c, errcode.ParamError, "当前支付方式未开启")
		return
	}

	uc := appcontext.GetUserContext(c)

	// 查询订单详情，获取应付金额并校验订单归属
	orderResp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{
		Id:     req.OrderID,
		UserId: uc.UserID,
	})
	if err != nil {
		response.Error(c, errcode.InternalServerError, err.Error())
		return
	}
	if orderResp.Order == nil || orderResp.Order.Id == 0 {
		response.Error(c, errcode.NotFound, "订单不存在")
		return
	}

	payType, ok := channelToPayType(req.Channel)
	if !ok {
		response.Error(c, errcode.ParamError, "不支持该支付渠道")
		return
	}

	resp, err := h.paymentClient.CreatePayment(c.Request.Context(), &payment.CreatePaymentReq{
		OrderId: req.OrderID,
		UserId:  uc.UserID,
		PayType: payType,
		Amount:  orderResp.Order.PayAmount,
		Openid:  req.Openid,
	})
	if err != nil {
		response.Error(c, errcode.InternalServerError, err.Error())
		return
	}
	response.Success(c, resp)
}

// PaymentChannel 支付渠道信息
// 用于返回给前端展示可用支付方式
type PaymentChannel struct {
	Channel string `json:"channel"`
	Name    string `json:"name"`
}

// PaymentChannels 获取当前启用的支付方式列表
// 根据后台配置 payment.enabled_channels 返回可用渠道
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) PaymentChannels(c *gin.Context) {
	channelNames := map[string]string{
		"wechat": "微信支付",
		"alipay": "支付宝支付",
		"cash":   "货到付款",
	}

	var list []PaymentChannel
	for _, ch := range h.cfg.Payment.EnabledChannels {
		list = append(list, PaymentChannel{
			Channel: ch,
			Name:    channelNames[ch],
		})
	}
	response.Success(c, gin.H{"channels": list})
}

// GetPayment 根据订单 ID 查询支付单
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) GetPayment(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || orderID <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.paymentClient.GetPayment(c.Request.Context(), &payment.GetPaymentReq{
		OrderId: orderID,
	})
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}
	response.Success(c, resp)
}

// WechatCallback 微信支付回调
// 读取 raw body 调用 payment.HandleWechatCallback，返回 XML 成功响应
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) WechatCallback(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.paymentClient.HandleWechatCallback(c.Request.Context(), &payment.WechatCallbackReq{
		Body: body,
	})
	if err != nil {
		c.String(http.StatusOK, "<xml><return_code><![CDATA[FAIL]]></return_code></xml>")
		return
	}
	c.String(http.StatusOK, "<xml><return_code><![CDATA[SUCCESS]]></return_code></xml>")
}

// AlipayCallback 支付宝支付回调
// 读取 form/map 调用 payment.HandleAlipayCallback
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) AlipayCallback(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	params := make(map[string]string, len(c.Request.PostForm))
	for k, v := range c.Request.PostForm {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}

	_, err := h.paymentClient.HandleAlipayCallback(c.Request.Context(), &payment.AlipayCallbackReq{
		Params: params,
	})
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}
	response.Success(c, gin.H{"result": "success"})
}
