package handler

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
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

// 支付类型：1=微信，2=支付宝，3=现金/货到付款
const (
	payTypeWechat int32 = 1
	payTypeAlipay int32 = 2
	payTypeCash   int32 = 3
)

// 微信支付按客户端渠道解析的业务错误
var (
	errWechatClientChannelNotReady = errors.New("微信小程序渠道未启用或未配置")
	errWechatGzhNotOpen            = errors.New("公众号内微信支付暂未开放")
	errWechatAppNotOpen            = errors.New("微信 App 支付暂未开放")
	// errWechatOpenidUnavailable 渠道身份缺失/查询异常：拒绝支付而非静默降级——
	// 空 openid 会被支付服务误判为 H5 通道下单，小程序内无法调起 h5_url，表现为支付必失败
	errWechatOpenidUnavailable = errors.New("微信支付身份缺失，请重新登录小程序后重试")
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
		return cfg.AlipayReady
	case "cash":
		return true
	default:
		return false
	}
}

// listClientChannelsMap 拉取客户端渠道列表并按 code 建索引
// 拉取失败时返回 nil（渠道级过滤降级跳过，不阻断支付渠道列表）
// 参数：
//   - ctx: 上下文
//   - client: user-service gRPC 客户端
//
// 返回值：
//   - map[string]*user.ClientChannelInfo: code → 渠道信息
func listClientChannelsMap(ctx context.Context, client user.UserServiceClient) map[string]*user.ClientChannelInfo {
	resp, err := client.ListClientChannels(ctx, &user.ListClientChannelsReq{OnlyEnabled: false})
	if err != nil {
		return nil
	}
	m := make(map[string]*user.ClientChannelInfo, len(resp.List))
	for _, ch := range resp.List {
		m[ch.Code] = ch
	}
	return m
}

// wechatPayReadyOnClient 判断微信支付在指定客户端渠道上的能力是否就绪
// mp 需要 mp 渠道已启用且凭证完整（JSAPI）；h5 走微信 H5 支付恒就绪；
// gzh 需公众号 OAuth 授权链路（未开放）、app 需 App 支付 SDK（未接入），均视为未就绪
// 参数：
//   - clientCode: 客户端渠道 code
//   - clientChannels: 客户端渠道索引，nil 时按存量口径放行
//
// 返回值：
//   - bool: 是否就绪
func wechatPayReadyOnClient(clientCode string, clientChannels map[string]*user.ClientChannelInfo) bool {
	switch clientCode {
	case channel.MP:
		ch := clientChannels[channel.MP]
		return ch != nil && ch.Enabled && ch.Configured
	case channel.H5:
		return true
	case channel.GZH, channel.APP:
		return false
	default:
		// 未知渠道保持存量行为（未带渠道头的旧客户端）
		return true
	}
}

// alipayPayReadyOnClient 判断支付宝支付在指定客户端渠道上的能力是否就绪
// h5 走手机网站支付（跳转支付宝收银台）恒就绪；
// mp（微信小程序）/gzh（公众号网页）处于微信生态内无法唤起支付宝；
// app 需 App 支付 SDK（未接入），均视为未就绪
// 参数：
//   - clientCode: 客户端渠道 code
//   - clientChannels: 客户端渠道索引，nil 时按存量口径放行
//
// 返回值：
//   - bool: 是否就绪
func alipayPayReadyOnClient(clientCode string, clientChannels map[string]*user.ClientChannelInfo) bool {
	switch clientCode {
	case channel.H5:
		return true
	case channel.MP, channel.GZH, channel.APP:
		return false
	default:
		// 未知渠道保持存量行为（未带渠道头的旧客户端）
		return true
	}
}

// isChannelUsableOnClient 判断支付渠道在指定客户端渠道上是否可用
// 支付配置的适用渠道勾选（空=全部）与客户端渠道能力就绪取交集
// 参数：
//   - payChannel: 支付渠道（wechat/alipay/cash）
//   - clientCode: 客户端渠道 code，空串或非法值时按存量口径不过滤
//   - cfg: 支付配置
//   - clientChannels: 客户端渠道索引
//
// 返回值：
//   - bool: 是否可用
func isChannelUsableOnClient(payChannel, clientCode string, cfg *payment.PayConfig, clientChannels map[string]*user.ClientChannelInfo) bool {
	if clientCode == "" || !channel.IsValid(clientCode) {
		return true
	}
	switch payChannel {
	case "wechat":
		if len(cfg.WechatClientChannels) > 0 && !containsString(cfg.WechatClientChannels, clientCode) {
			return false
		}
		return wechatPayReadyOnClient(clientCode, clientChannels)
	case "alipay":
		if len(cfg.AlipayClientChannels) > 0 && !containsString(cfg.AlipayClientChannels, clientCode) {
			return false
		}
		return alipayPayReadyOnClient(clientCode, clientChannels)
	default:
		// cash 等无需第三方配置的渠道全客户端可用
		return true
	}
}

// containsString 判断列表中是否包含目标字符串
//
// 参数：
//   - list: 字符串列表
//   - target: 目标值
//
// 返回值：
//   - bool: 是否包含
func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
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

	// 客户端渠道识别：X-Channel-Code 由 C 端构建期注入（mp/gzh/app/h5），未携带时按存量口径处理
	clientCode := channel.Normalize(c.GetHeader("X-Channel-Code"))
	if clientCode != "" && !channel.IsValid(clientCode) {
		response.Error(c, errcode.ParamError, "未知客户端渠道")
		return
	}
	clientChannels := listClientChannelsMap(c.Request.Context(), h.userClient)
	if !isChannelUsableOnClient(req.Channel, clientCode, payCfg, clientChannels) {
		response.Error(c, errcode.ParamError, "当前支付方式在当前客户端不可用")
		return
	}

	// 微信支付按客户端渠道解析 openid 与 AppID（JSAPI 签名二者须同源），openid 不接受前端传入
	var openid, appID string
	if payType == payTypeWechat {
		var rerr error
		openid, appID, rerr = h.resolveWechatPayIdentity(c.Request.Context(), uc.UserID, clientCode)
		if rerr != nil {
			response.Error(c, errcode.ParamError, rerr.Error())
			return
		}
	}

	resp, err := h.paymentClient.CreatePayment(c.Request.Context(), &payment.CreatePaymentReq{
		OrderId: req.OrderID,
		UserId:  uc.UserID,
		PayType: payType,
		Amount:  orderResp.Order.PayAmount,
		Openid:  openid,
		AppId:   appID,
		// 社区与订单号取自刚查到的订单（权威来源，不接受前端传入）：
		// 支付异常队列按社区隔离、后台按订单号核对都依赖这两项快照
		CommunityId: orderResp.Order.CommunityId,
		OrderNo:     orderResp.Order.OrderNo,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// resolveWechatPayIdentity 按客户端渠道解析微信支付所需的 openid 与 AppID
// 渠道语义：
//   - mp：openid 取渠道身份表（mp 渠道回落存量 users.openid），AppID 取 mp 渠道配置（JSAPI）
//   - h5：无 openid 走 H5 支付；AppID 优先取 mp 渠道 AppID（须与商户号绑定），缺省回落支付配置默认值
//   - gzh/app：公众号 OAuth 与 App 支付链路未开放，直接拒绝（渠道列表接口已隐藏）
//   - 空/未知：存量口径（users.openid + 支付配置默认 AppID），兼容未升级客户端
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - clientCode: 客户端渠道 code
//
// 返回值：
//   - string: 微信 openid（H5 场景为空串）
//   - string: 下单 AppID（空串时 payment 服务回落配置默认值）
//   - error: 渠道不可用等业务错误
func (h *PaymentHandler) resolveWechatPayIdentity(ctx context.Context, userID int64, clientCode string) (string, string, error) {
	switch clientCode {
	case channel.MP:
		chResp, err := h.userClient.GetClientChannel(ctx, &user.GetClientChannelReq{Code: channel.MP})
		if err != nil {
			return "", "", err
		}
		if chResp == nil || !chResp.Enabled || !chResp.Configured {
			return "", "", errWechatClientChannelNotReady
		}
		openidResp, err := h.userClient.GetUserChannelOpenid(ctx, &user.GetUserChannelOpenidReq{
			UserId:      userID,
			ChannelCode: channel.MP,
		})
		// 查询异常/身份缺失时拒绝支付而非静默降级：
		// 空 openid 会被支付服务按 H5 通道下单，小程序内无法调起，表现为支付必失败
		if err != nil {
			log.Printf("查询用户微信支付身份失败 user_id=%d: %v", userID, err)
			return "", "", errWechatOpenidUnavailable
		}
		if openidResp == nil || openidResp.Openid == "" {
			return "", "", errWechatOpenidUnavailable
		}
		return openidResp.Openid, chResp.AppId, nil
	case channel.H5:
		// H5 支付无 openid；统一下单 AppID 取 mp 渠道 AppID（任一与商户绑定的 AppID 均可）
		if chResp, err := h.userClient.GetClientChannel(ctx, &user.GetClientChannelReq{Code: channel.MP}); err == nil &&
			chResp != nil && chResp.Enabled && chResp.Configured {
			return "", chResp.AppId, nil
		}
		return "", "", nil
	case channel.GZH:
		return "", "", errWechatGzhNotOpen
	case channel.APP:
		return "", "", errWechatAppNotOpen
	default:
		// 存量口径：users.openid（排除 phone_ 占位）+ 支付配置默认 AppID
		return h.resolveWechatOpenid(ctx, userID), "", nil
	}
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
// 根据数据库支付配置中的渠道开关与渠道实际配置完整性返回可用渠道，
// 并按 X-Channel-Code 声明的客户端渠道取交集（未携带渠道头时保持存量口径不过滤）
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

	// 客户端渠道识别：拉取失败时降级为不过滤，避免渠道服务抖动阻断支付方式列表
	clientCode := channel.Normalize(c.GetHeader("X-Channel-Code"))
	if clientCode != "" && !channel.IsValid(clientCode) {
		clientCode = ""
	}
	var clientChannels map[string]*user.ClientChannelInfo
	if clientCode != "" {
		clientChannels = listClientChannelsMap(c.Request.Context(), h.userClient)
	}

	var list []PaymentChannel
	for _, ch := range payCfg.EnabledChannels {
		if !isChannelUsable(ch, payCfg) {
			continue
		}
		if !isChannelUsableOnClient(ch, clientCode, payCfg, clientChannels) {
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

// PaymentStatus 查询支付状态（免登录）
// C 端支付宝回跳结果页轮询用：payment_no 是 ZF+UUID 形态的不可枚举凭据，
// 仅回传状态级最小字段集（状态/金额/订单号），不含用户与商品隐私信息
// 接口: GET /api/v1/payments/status?no=<payment_no>
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) PaymentStatus(c *gin.Context) {
	paymentNo := strings.TrimSpace(c.Query("no"))
	if paymentNo == "" {
		response.Error(c, errcode.ParamError, "缺少支付单号")
		return
	}
	resp, err := h.paymentClient.GetPaymentStatus(c.Request.Context(), &payment.GetPaymentStatusReq{
		PaymentNo: paymentNo,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"payment_no": resp.PaymentNo,
		"status":     resp.Status,
		"amount":     resp.Amount,
		"order_id":   resp.OrderId,
		"order_no":   resp.OrderNo,
	})
}

// AlipayPayUrl 凭支付单号换取支付宝收银台链接（免登录）
// H5 支付引导页用：引导页 URL 只携带不可枚举的支付单号，打开后凭单号实时换取
// 新签名的收银台链接，规避支付链接（含 # 等保留字符）经 uni-app H5 路由
// query 传递时被框架编解码破坏的问题（实测 ? 会被改写为 =，与编码层数无关）；
// 仅待支付的支付宝单返回链接，其它状态只回支付单状态，引导页据此跳结果页
// 接口: GET /api/v1/payments/alipay/url?no=<payment_no>
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) AlipayPayUrl(c *gin.Context) {
	paymentNo := strings.TrimSpace(c.Query("no"))
	if paymentNo == "" {
		response.Error(c, errcode.ParamError, "缺少支付单号")
		return
	}
	resp, err := h.paymentClient.GetAlipayPayUrl(c.Request.Context(), &payment.GetAlipayPayUrlReq{
		PaymentNo: paymentNo,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"payment_no":     resp.PaymentNo,
		"status":         resp.Status,
		"order_id":       resp.OrderId,
		"alipay_pay_url": resp.AlipayPayUrl,
	})
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

// alipayNotifyBodyLimit 支付宝异步通知请求体体积上限。
// 通知参数为扁平 form，正常体量在 2KB 以内；设置上限防止异常大报文耗尽内存。
const alipayNotifyBodyLimit = 64 << 10

// AlipayCallback 支付宝支付回调
// 直接读取 raw body 并以 url 编码解析通知参数（不依赖 Content-Type，
// 支付宝部分场景投递的通知并不带 application/x-www-form-urlencoded，
// 交给 c.Request.ParseForm 会得到空参数、导致永远返回 failure 引发无限重发），
// 再交由 payment 服务做 RSA2 验签与状态流转，
// 按支付宝规范返回纯文本 success/failure：仅业务受理成功返回 success，
// 其余（验签失败/参数缺失/DB 异常）返回 failure 触发支付宝按通知策略重发
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) AlipayCallback(c *gin.Context) {
	// 多读 1 字节用于判定是否超限，避免整包读入超大报文
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, alipayNotifyBodyLimit+1))
	if err != nil || len(body) > alipayNotifyBodyLimit {
		log.Printf("支付宝回调读取请求体失败或超限: %v len=%d", err, len(body))
		c.String(http.StatusOK, "failure")
		return
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		log.Printf("支付宝回调解析通知参数失败: %v", err)
		c.String(http.StatusOK, "failure")
		return
	}
	params := make(map[string]string, len(values))
	for k, v := range values {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}

	_, err = h.paymentClient.HandleAlipayCallback(c.Request.Context(), &payment.AlipayCallbackReq{Params: params})
	if err != nil {
		log.Printf("支付宝回调处理失败: %v", err)
		c.String(http.StatusOK, "failure")
		return
	}
	c.String(http.StatusOK, "success")
}
