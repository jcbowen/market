package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/channel"
	"github.com/jcbowen/market/src/backend/common/debuglog"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/paynotify"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
	wegopayment "github.com/jcbowen/wego/payment"
)

// 支付类型：1=微信，2=支付宝，3=现金/货到付款
const (
	payTypeWechat int32 = 1
	payTypeAlipay int32 = 2
	payTypeCash   int32 = 3
)

// 支付平台标识（client_channels.pay_methods 绑定与 pay_config.platform 共用取值）
const (
	payPlatformWechat = "wechat"
	payPlatformAlipay = "alipay"
	payMethodCash     = "cash"
)

// 微信支付按客户端渠道解析的业务错误
var (
	errWechatClientChannelNotReady = errors.New("微信小程序渠道未启用或未配置")
	errWechatGzhNotOpen            = errors.New("公众号内微信支付暂未开放")
	errWechatAppNotOpen            = errors.New("微信 App 支付暂未开放")
	// errWechatOpenidUnavailable 渠道身份缺失/查询异常：拒绝支付而非静默降级——
	// 空 openid 会被支付服务误判为 H5 通道下单，小程序内无法调起 h5_url，表现为支付必失败
	errWechatOpenidUnavailable = errors.New("微信支付身份缺失，请重新登录小程序后重试")
	// errWechatClientAppIDMissing 渠道未填微信 AppID：AppID 属渠道身份（支付配置行不再持有），
	// 带着空值下单只会被微信以「appid 必填」拒绝，把失败前移成一句可执行的提示
	errWechatClientAppIDMissing = errors.New("当前客户端未配置微信支付 AppID，请到后台渠道管理中补全")
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

// parsePayMethodEntry 拆解渠道绑定的支付方式条目
// 语法："cash"（货到付款内置项）或 "<platform>:<config_id>"（如 "wechat:3"）
//
// 参数：
//   - entry: 绑定条目原文
//
// 返回值：
//   - string: 平台标识（wechat/alipay），cash 条目返回空串
//   - int64: 支付配置 ID，cash 条目或非法条目返回 0
func parsePayMethodEntry(entry string) (string, int64) {
	entry = strings.TrimSpace(entry)
	if entry == "" || entry == payMethodCash {
		return "", 0
	}
	idx := strings.Index(entry, ":")
	if idx <= 0 || idx == len(entry)-1 {
		return "", 0
	}
	id, err := strconv.ParseInt(entry[idx+1:], 10, 64)
	if err != nil || id <= 0 {
		return "", 0
	}
	return entry[:idx], id
}

// listActivePayMethodSet 拉取启用且就绪的支付配置集合（键 "platform:config_id"）
// 用于在网关侧提前拦截「渠道绑定了但配置未就绪」的支付方式，给 C 端更友好的错误提示；
// 拉取失败返回 nil：调用方按降级口径处理，不因配置服务抖动放大为整体不可用
//
// 参数：
//   - ctx: 上下文
//   - client: payment-service gRPC 客户端
//
// 返回值：
//   - map[string]struct{}: 就绪支付配置集合，nil 表示拉取失败
func listActivePayMethodSet(ctx context.Context, client payment.PaymentServiceClient) map[string]struct{} {
	resp, err := client.ListActivePayMethods(ctx, &payment.ListActivePayMethodsReq{})
	if err != nil {
		return nil
	}
	set := make(map[string]struct{}, len(resp.Methods))
	for _, m := range resp.Methods {
		set[fmt.Sprintf("%s:%d", m.Platform, m.ConfigId)] = struct{}{}
	}
	return set
}

// resolveChannelPayBinding 解析客户端渠道绑定的支付方式配置
// 渠道启用什么支付方式完全由渠道侧绑定（client_channels.pay_methods）决定；
// 未携带渠道头的旧客户端按 h5 渠道口径处理（h5 为兜底渠道且恒启用）
//
// 参数：
//   - clientCode: 客户端渠道 code，空串按 h5 处理
//   - clientChannels: 客户端渠道索引（code → 渠道信息）
//
// 返回值：
//   - *user.ClientChannelInfo: 渠道信息，渠道不存在时为 nil
func resolveChannelPayBinding(clientCode string, clientChannels map[string]*user.ClientChannelInfo) *user.ClientChannelInfo {
	if clientCode == "" {
		clientCode = channel.H5
	}
	return clientChannels[clientCode]
}

// firstConfigIDForPlatform 从渠道绑定中取指定平台的第一个支付配置 ID
// 绑定顺序即收银台展示顺序，同平台至多一条（user 服务保存时校验），取首个仅为防御
//
// 参数：
//   - methods: 渠道绑定的支付方式列表
//   - platform: 平台标识（wechat/alipay）
//
// 返回值：
//   - int64: 支付配置 ID，未绑定时为 0
func firstConfigIDForPlatform(methods []string, platform string) int64 {
	for _, entry := range methods {
		if p, id := parsePayMethodEntry(entry); p == platform {
			return id
		}
	}
	return 0
}

// bindingHasCash 渠道绑定中是否包含货到付款
//
// 参数：
//   - methods: 渠道绑定的支付方式列表
//
// 返回值：
//   - bool: 是否包含
func bindingHasCash(methods []string) bool {
	for _, entry := range methods {
		if strings.TrimSpace(entry) == payMethodCash {
			return true
		}
	}
	return false
}

// wechatPayReadyOnClient 判断微信支付在指定客户端渠道上的能力是否就绪
// mp 需要 mp 渠道已启用且凭证完整（JSAPI）；h5 走微信 H5 支付，须已配置渠道 AppID
// （AppID 属渠道身份，支付配置行不再持有，缺失时下单必被渠道拒绝）；
// gzh 需公众号 OAuth 授权链路（未接入）、alimp 处于支付宝生态内无法唤起微信、
// android/ios 需 App 支付 SDK（未接入），均视为未就绪
// 参数：
//   - clientCode: 客户端渠道 code
//   - clientChannels: 客户端渠道索引，nil 时 mp 渠道按未就绪处理
//
// 返回值：
//   - bool: 是否就绪
func wechatPayReadyOnClient(clientCode string, clientChannels map[string]*user.ClientChannelInfo) bool {
	switch clientCode {
	case channel.MP:
		ch := clientChannels[channel.MP]
		return ch != nil && ch.Enabled && ch.Configured
	case channel.H5:
		// H5 支付无 openid；统一下单 AppID 取 h5 渠道自身配置：微信 AppID 属渠道身份
		// （支付配置行不再持有）。此前借用 mp 渠道 AppID，mp 未配置时只能带着空值下单，
		// 被渠道以「appid 必填」拒绝——缺失在这里就判未就绪，支付方式列表不会下发微信支付
		ch := clientChannels[channel.H5]
		return ch != nil && strings.TrimSpace(ch.AppId) != ""
	default:
		// gzh/alimp/android/ios 均无可用微信支付链路
		return false
	}
}

// alipayPayReadyOnClient 判断支付宝支付在指定客户端渠道上的能力是否就绪
// h5 走手机网站支付（跳转支付宝收银台）、alimp 走 V3 trade.create 预下单，均恒就绪
// （配置就绪由 listActivePayMethodSet 与 payment 服务下单校验兜底）；
// mp/gzh 处于微信生态内无法唤起支付宝；android/ios 需 App 支付 SDK（未接入），均视为未就绪
// 参数：
//   - clientCode: 客户端渠道 code
//
// 返回值：
//   - bool: 是否就绪
func alipayPayReadyOnClient(clientCode string) bool {
	switch clientCode {
	case channel.H5, channel.ALIMP:
		return true
	default:
		// mp/gzh/android/ios 均无可用支付宝支付链路
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
// 支付方式由当前客户端渠道的绑定（client_channels.pay_methods）决定：
// 渠道未启用/未绑定该支付方式/绑定配置未就绪均拦截在下单前
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) CreatePayment(c *gin.Context) {
	var req CreatePaymentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	payType, ok := channelToPayType(req.Channel)
	if !ok {
		response.Error(c, errcode.ParamError, "不支持该支付渠道")
		return
	}

	// 客户端渠道识别：X-Channel-Code 由 C 端构建期注入（h5/gzh/mp/alimp/android/ios），
	// 未携带时按 h5 渠道口径处理（h5 为兜底渠道）
	clientCode := channel.Normalize(c.GetHeader("X-Channel-Code"))
	if clientCode != "" && !channel.IsValid(clientCode) {
		response.Error(c, errcode.ParamError, "未知客户端渠道")
		return
	}
	clientChannels := listClientChannelsMap(c.Request.Context(), h.userClient)
	cur := resolveChannelPayBinding(clientCode, clientChannels)
	if cur == nil || !cur.Enabled {
		response.Error(c, errcode.OperationNotAllowed, "当前客户端渠道未启用")
		return
	}

	// 渠道绑定校验：该支付方式必须已绑定到当前渠道，且绑定引用的配置启用就绪；
	// 就绪集合拉取失败时跳过该层校验（配置可用性由 payment 服务下单校验兜底）
	var configID int64
	if req.Channel == payMethodCash {
		if !bindingHasCash(cur.PayMethods) {
			response.Error(c, errcode.ParamError, "当前支付方式未开启")
			return
		}
	} else {
		configID = firstConfigIDForPlatform(cur.PayMethods, req.Channel)
		if configID == 0 {
			response.Error(c, errcode.ParamError, "当前支付方式未开启")
			return
		}
		if req.Channel == payPlatformWechat && !wechatPayReadyOnClient(clientCode, clientChannels) {
			response.Error(c, errcode.ParamError, "当前支付方式在当前客户端不可用")
			return
		}
		if req.Channel == payPlatformAlipay && !alipayPayReadyOnClient(clientCode) {
			response.Error(c, errcode.ParamError, "当前支付方式在当前客户端不可用")
			return
		}
		if set := listActivePayMethodSet(c.Request.Context(), h.paymentClient); set != nil {
			if _, ok := set[fmt.Sprintf("%s:%d", req.Channel, configID)]; !ok {
				response.Error(c, errcode.ParamError, "当前支付方式未开启或配置不完整")
				return
			}
		}
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
		// 支付配置由网关按渠道绑定解析后传入；客户端渠道供支付宝分流下单方式
		ConfigId:      configID,
		ClientChannel: clientCode,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// resolveWechatPayIdentity 按客户端渠道解析微信支付所需的 openid 与 AppID
// 微信 AppID 是渠道身份（一个商户号可关联小程序/H5 等多个 AppID），一律取自渠道配置，
// 支付配置行不再持有 AppID；解析不到即拒绝下单，不回落任何默认值
// 渠道语义：
//   - mp：openid 取渠道身份表（mp 渠道回落存量 users.openid），AppID 取 mp 渠道配置（JSAPI）
//   - h5：无 openid 走微信 H5 支付，AppID 取 h5 渠道自身配置
//   - gzh/app：公众号 OAuth 与 App 支付链路未开放，直接拒绝（渠道列表接口已隐藏）
//   - 空/未知：按 h5 兜底渠道口径处理（openid 取存量 users.openid），
//     微信支付在 wechatPayReadyOnClient 处已按渠道 AppID 拦截，此处只保证不回落默认值
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - clientCode: 客户端渠道 code
//
// 返回值：
//   - string: 微信 openid（H5 场景为空串）
//   - string: 下单 AppID（支付服务随建单快照落库，供回调归属校验与撤销取用）
//   - error: 渠道不可用或未配置 AppID 等业务错误
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
		// H5 支付无 openid；统一下单 AppID 取本渠道自身配置（微信 AppID 属渠道身份，
		// 支付配置行不再持有）：缺失即拒绝，不再借用 mp 渠道 AppID——mp 未必配置，
		// 借来的 AppID 也与本端入口无关，回调归属校验按支付单快照比对时会误判
		chResp, err := h.userClient.GetClientChannel(ctx, &user.GetClientChannelReq{Code: channel.H5})
		if err != nil {
			log.Printf("查询 H5 渠道配置失败: %v", err)
			return "", "", errWechatClientAppIDMissing
		}
		if chResp == nil || !chResp.Enabled {
			return "", "", errWechatClientAppIDMissing
		}
		appID := strings.TrimSpace(chResp.AppId)
		if appID == "" {
			return "", "", errWechatClientAppIDMissing
		}
		return "", appID, nil
	case channel.GZH:
		// 公众号 OAuth 支付链路未接入（渠道列表接口已隐藏该组合），直接拒绝
		return "", "", errWechatGzhNotOpen
	case channel.ANDROID, channel.IOS:
		// App 支付 SDK 未接入（渠道列表接口已隐藏该组合），直接拒绝
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

// PaymentChannels 获取当前渠道可用的支付方式列表
// 数据源为当前客户端渠道的绑定（client_channels.pay_methods，有序），
// 逐项过滤渠道能力就绪与配置就绪；未携带渠道头时按 h5 渠道口径处理
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) PaymentChannels(c *gin.Context) {
	channelNames := map[string]string{
		payPlatformWechat: "微信支付",
		payPlatformAlipay: "支付宝支付",
		payMethodCash:     "货到付款",
	}

	// 客户端渠道识别：未携带渠道头按 h5 渠道口径处理；非法渠道头按无可用支付方式返回
	clientCode := channel.Normalize(c.GetHeader("X-Channel-Code"))
	if clientCode != "" && !channel.IsValid(clientCode) {
		response.Success(c, gin.H{"channels": []PaymentChannel{}})
		return
	}
	clientChannels := listClientChannelsMap(c.Request.Context(), h.userClient)
	cur := resolveChannelPayBinding(clientCode, clientChannels)
	if cur == nil || !cur.Enabled {
		response.Success(c, gin.H{"channels": []PaymentChannel{}})
		return
	}

	// 就绪支付配置集合：拉取失败时只回现金，避免把未就绪的第三方支付展示给用户
	// （服务端下单仍有渠道校验兑底，不会放过无法支付的方式）
	active := listActivePayMethodSet(c.Request.Context(), h.paymentClient)

	var list []PaymentChannel
	for _, entry := range cur.PayMethods {
		platform, configID := parsePayMethodEntry(entry)
		if platform == "" {
			if strings.TrimSpace(entry) == payMethodCash {
				list = append(list, PaymentChannel{Channel: payMethodCash, Name: channelNames[payMethodCash]})
			}
			continue
		}
		if active != nil {
			if _, ok := active[fmt.Sprintf("%s:%d", platform, configID)]; !ok {
				continue
			}
		}
		switch platform {
		case payPlatformWechat:
			if !wechatPayReadyOnClient(clientCode, clientChannels) {
				continue
			}
		case payPlatformAlipay:
			if !alipayPayReadyOnClient(clientCode) {
				continue
			}
		default:
			continue
		}
		list = append(list, PaymentChannel{Channel: platform, Name: channelNames[platform]})
	}
	if list == nil {
		list = []PaymentChannel{}
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
// 读取 raw body 与微信签名头，调用 payment.HandleWechatCallback，
// 按 V3 规范应答：成功 HTTP 204 空体（wegopayment.NotificationSuccess），
// 失败 5xx + {"code":"FAIL"}（wegopayment.NotificationFail，微信按间隔重试）
// 受理情况经 recordWechatCallback 记入 debugger 进程级日志（请求体为渠道密文，只记元数据）
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) WechatCallback(c *gin.Context) {
	start := time.Now()
	clientIP := c.ClientIP()

	body, err := c.GetRawData()
	if err != nil {
		recordWechatCallback(clientIP, start, len(body), "failure", fmt.Sprintf("读取请求体失败: %v", err))
		// V3 规范失败应答：非 2xx + JSON。原 response.Error 返回 HTTP 200，
		// 微信会视为受理成功而不再重试，导致通知丢失
		wegopayment.NotificationFail(c.Writer, http.StatusInternalServerError, "读取请求体失败")
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
		recordWechatCallback(clientIP, start, len(body), "failure", err.Error())
		// V3 规范失败应答：5xx + {"code":"FAIL","message":"..."}，微信会按间隔重试
		wegopayment.NotificationFail(c.Writer, http.StatusInternalServerError, "回调处理失败")
		return
	}
	recordWechatCallback(clientIP, start, len(body), "success", "")
	// V3 规范成功应答：HTTP 204 空体（wego v0.2.0 NotificationSuccess）
	wegopayment.NotificationSuccess(c.Writer)
}

// recordWechatCallback 把一次微信回调的受理情况记入 debugger 进程级日志
// 微信 V3 通知体为渠道密文（resource.ciphertext），解析无排查价值，故只记
// 体量/结果/错误/IP/耗时；验签与解密结论以 payment 服务的 payment_notify_logs 为权威流水。
// 按客户端 IP 频率封顶（debuglog.StartQuoted），防伪造回调刷量挤占 debug_logs
// 参数：
//   - clientIP: 客户端 IP（兼作封顶键）
//   - start: 处理开始时间（算耗时）
//   - bodySize: 请求体字节数
//   - result: success / failure
//   - errMsg: 失败原因，成功时传空串
func recordWechatCallback(clientIP string, start time.Time, bodySize int, result, errMsg string) {
	rec := debuglog.StartQuoted("微信回调", "payment_callback_wechat", clientIP)
	if rec == nil {
		return
	}
	fields := map[string]interface{}{
		"body_size":   bodySize,
		"result":      result,
		"duration_ms": time.Since(start).Milliseconds(),
		"client_ip":   clientIP,
	}
	if result == "success" {
		rec.Info("微信回调受理完成", fields)
		_ = rec.EndProcess(debuglog.StatusCompleted)
		return
	}
	fields["error"] = errMsg
	rec.Error("微信回调受理失败", fields)
	_ = rec.EndProcess(debuglog.StatusFailed)
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
// 其余（验签失败/参数缺失/DB 异常）返回 failure 触发支付宝按通知策略重发。
// 受理情况经 recordAlipayCallback 记入 debugger 进程级日志：成功只落摘要，
// 失败落全量通知参数（含 sign），详见 alipayNotifyEvidence
// 参数：
//   - c: gin 上下文
func (h *PaymentHandler) AlipayCallback(c *gin.Context) {
	start := time.Now()
	clientIP := c.ClientIP()
	contentType := c.GetHeader("Content-Type")

	// 多读 1 字节用于判定是否超限，避免整包读入超大报文
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, alipayNotifyBodyLimit+1))
	if err != nil || len(body) > alipayNotifyBodyLimit {
		reason := fmt.Sprintf("请求体读取失败或超限: %v len=%d", err, len(body))
		recordAlipayCallback(clientIP, start, alipayNotifyEvidence(nil, contentType, len(body), reason), "failure", reason)
		log.Printf("支付宝回调读取请求体失败或超限: %v len=%d", err, len(body))
		c.String(http.StatusOK, "failure")
		return
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		reason := fmt.Sprintf("通知参数解析失败: %v", err)
		recordAlipayCallback(clientIP, start, alipayNotifyEvidence(nil, contentType, len(body), reason), "failure", reason)
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
		recordAlipayCallback(clientIP, start, alipayNotifyEvidence(params, contentType, len(body), ""), "failure", err.Error())
		log.Printf("支付宝回调处理失败: %v", err)
		c.String(http.StatusOK, "failure")
		return
	}
	recordAlipayCallback(clientIP, start, alipayNotifyFields(params), "success", "")
	c.String(http.StatusOK, "success")
}

// alipayNotifyFields 从通知参数中提取成功回调的排查摘要字段（白名单）
// 成功回调无需取证（同一事实已落 payments 表），买家账号等隐私字段不落调试库；
// 验签结论与原始报文以 payment 服务的 payment_notify_logs 为权威流水
// 参数：
//   - params: url 解析后的通知参数
//
// 返回值：
//   - map[string]interface{}: 摘要字段，缺失的键不放入
func alipayNotifyFields(params map[string]string) map[string]interface{} {
	fields := make(map[string]interface{}, 4)
	for _, key := range []string{"out_trade_no", "trade_status", "total_amount", "notify_id"} {
		if v := strings.TrimSpace(params[key]); v != "" {
			fields[key] = v
		}
	}
	return fields
}

// alipayNotifyEvidence 组装失败回调的取证字段：通知参数（sign 原文 + 买家标识脱敏）+ 报文元信息
// 与成功摘要刻意不同：验签类故障只有拿到原文和签名值才可能复盘——
// 待验签串由「除 sign/sign_type 外的全部参数按 key 升序拼接」生成，少一个字段就判不出
// 到底是支付宝没发（如证书序列号缺失）还是我们解析丢了。
// 落 sign 原文不构成新的伪造面：签名把 out_trade_no、total_amount 一并签死，
// 改动任一字段即验不过，重放真实通知也只会命中同一笔已支付单的幂等分支。
// buyer_logon_id/buyer_user_id/buyer_open_id 属个人标识且与验签无关，统一走
// paynotify.MaskAlipayParams 脱敏，与 payment_notify_logs.raw_body 口径一致。
// 参数：
//   - params: url 解析后的通知参数，解析失败时传 nil（此时只有报文元信息可记）
//   - contentType: 请求 Content-Type（支付宝部分通知不带标准 form 头，是排查线索）
//   - bodyLen: 请求体字节数
//   - parseErr: 解析/读取失败原因，空串表示参数已正常解析
//
// 返回值：
//   - map[string]interface{}: 取证字段，通知参数平铺为顶层键
func alipayNotifyEvidence(params map[string]string, contentType string, bodyLen int, parseErr string) map[string]interface{} {
	masked := paynotify.MaskAlipayParams(params)
	fields := make(map[string]interface{}, len(masked)+4)
	for key, value := range masked {
		fields[key] = value
	}
	fields["content_type"] = contentType
	fields["body_bytes"] = bodyLen
	fields["param_count"] = len(params)
	if parseErr != "" {
		fields["parse_error"] = parseErr
	}
	return fields
}

// recordAlipayCallback 把一次支付宝回调的受理情况记入 debugger 进程级日志
// 回调路径整条跳过 HTTP 请求日志（SkipPaths 第二道防线），排查盲区由本函数补齐：
// 记录到达网关后的解析、受理结果与耗时，失败以错误级标记便于界面筛选。
// 按客户端 IP 频率封顶（debuglog.StartQuoted）：支付宝重发策略下单笔最多 25 次，
// 远低于封顶；伪造回调则被限制在每分钟 QuotaPerMinute 条，保护 debug_logs 全表上限
// 参数：
//   - clientIP: 客户端 IP（兼作封顶键）
//   - start: 处理开始时间（算耗时）
//   - fields: 记录字段，成功为 alipayNotifyFields 摘要、失败为 alipayNotifyEvidence 取证原文；
//     nil 仅用于极端兜底（尚未拿到任何参数）
//   - result: success / failure
//   - errMsg: 失败原因，成功时传空串
func recordAlipayCallback(clientIP string, start time.Time, fields map[string]interface{}, result, errMsg string) {
	rec := debuglog.StartQuoted("支付宝回调", "payment_callback_alipay", clientIP)
	if rec == nil {
		return
	}
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields["result"] = result
	fields["duration_ms"] = time.Since(start).Milliseconds()
	fields["client_ip"] = clientIP
	if result == "success" {
		rec.Info("支付宝回调受理完成", fields)
		_ = rec.EndProcess(debuglog.StatusCompleted)
		return
	}
	fields["error"] = errMsg
	rec.Error("支付宝回调受理失败", fields)
	_ = rec.EndProcess(debuglog.StatusFailed)
}
