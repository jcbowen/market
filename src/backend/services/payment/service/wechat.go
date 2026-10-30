package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"math"
	"net/http"
	"net/url"
	"slices"
	"time"

	"google.golang.org/grpc/peer"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/services/payment/model"
	wegopayment "github.com/jcbowen/wego/payment"
)

// buildWechatPayParams 构造微信支付调起参数
// 根据 openid 是否为空自动选择 JSAPI 或 H5 支付方式；
// AppID 是渠道身份（一个商户号可关联小程序/H5 等多个 AppID），由网关按客户端渠道解析后
// 随建单落库到 p.AppID，支付配置行不再持有 AppID；为空即拒绝下单而非回落配置默认值——
// 空/错配 AppID 会被微信以「appid 与 openid 不同源」拒绝，静默降级只会把失败推迟到用户按下支付之后
// 参数：
//   - ctx: 上下文
//   - p: 支付记录模型（AppID 快照取 p.AppID）
//   - openid: 用户微信 openid，为空则走 H5 支付
//
// 返回值：
//   - string: 调起参数 JSON 字符串（JSAPI）或包含 h5_url 的 JSON（H5）
//   - error: 错误信息
func (s *PaymentService) buildWechatPayParams(ctx context.Context, p *model.Payment, openid string) (string, error) {
	// 客户端与配置均按支付单绑定的配置行获取（内部含版本热刷新）
	client := s.getWechatPayClientFor(ctx, p.ConfigID)
	if client == nil {
		return "", fmt.Errorf("微信支付未配置或配置不完整")
	}
	cfg := s.getWechatConfigFor(ctx, p.ConfigID)
	if p.AppID == "" {
		return "", fmt.Errorf("当前客户端渠道未配置微信 AppID，无法发起支付，请在渠道管理中补全")
	}
	description := "社区商超订单"
	amountTotal := yuanToFen(p.Amount)

	// 有 openid 走 JSAPI（小程序/公众号），否则走 H5
	// H5 支付需要应用主页 URL，从 notify_url 推导，避免硬编码
	h5AppURL := "http://localhost:18080/s"
	if cfg.NotifyURL != "" {
		if u, err := url.Parse(cfg.NotifyURL); err == nil {
			h5AppURL = u.Scheme + "://" + u.Host + "/s"
		}
	}

	if openid != "" {
		resp, err := client.JSAPIPay(ctx, &wegopayment.JSAPIRequest{
			AppID:       p.AppID,
			Description: description,
			OutTradeNo:  p.PaymentNo,
			Amount: wegopayment.Amount{
				Total:    amountTotal,
				Currency: "CNY",
			},
			Payer: wegopayment.Payer{
				OpenID: openid,
			},
		})
		if err != nil {
			return "", fmt.Errorf("微信统一下单失败: %w", err)
		}

		// 调起参数由 SDK 生成：签名串 appId\ntimeStamp\nnonceStr\npackage\n 与商户私钥都在库里，
		// 业务层不再自行签名（AppID 走请求级入参，与 openid 同源）
		params, err := client.BuildJSAPIPayParams(p.AppID, resp.PrepayID)
		if err != nil {
			return "", fmt.Errorf("生成支付签名失败: %w", err)
		}

		data, err := json.Marshal(params)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	resp, err := client.H5Pay(ctx, &wegopayment.H5Request{
		AppID:       p.AppID,
		Description: description,
		OutTradeNo:  p.PaymentNo,
		Amount: wegopayment.Amount{
			Total:    amountTotal,
			Currency: "CNY",
		},
		SceneInfo: &wegopayment.SceneInfo{
			PayerClientIP: getClientIP(ctx),
			H5Info: &wegopayment.H5Info{
				Type:    "Wap",
				AppName: "社区商超",
				AppURL:  h5AppURL,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("微信 H5 下单失败: %w", err)
	}

	data, err := json.Marshal(map[string]string{"h5_url": resp.H5URL})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// errRefundAccepted 渠道退款「已受理未到终态」哨兵（P0-2）
// 微信申请退款受理成功返回 status=PROCESSING（常态），并非退款成功；
// 调用方收到本哨兵必须保持退款流水待退款(1)，由退款查单任务按 RefundQuery 收敛
// 响应缺 status（空串）同样按已受理处理：微信 V3 字段可空，不能把「没告诉我们结果」
// 误读成「退款成功」，否则会在渠道未出款时把支付单置成已退款
var errRefundAccepted = errors.New("退款已受理，等待渠道终态")

// 微信退款查单以包级变量注入，单测直接替换为假渠道，无需真实商户配置
// 参数与返回值语义见 defaultWechatRefundQuery
var wechatRefundQuery = defaultWechatRefundQuery

// defaultWechatRefundQuery 查询微信退款单终态（按商户退款单号 out_refund_no）
// 参数：
//   - ctx: 上下文
//   - s: 支付服务（按配置行取热更新后的微信客户端）
//   - configID: 支付配置行 ID（退款调用点从支付单携带，多配置下路由到对应商户客户端）
//   - refundNo: 商户退款单号（本系统退款流水号）
//
// 返回值：
//   - status: 渠道退款状态原文（SUCCESS / PROCESSING / ABNORMAL / CLOSED，空串表示渠道未给出）
//   - refundID: 渠道退款单号，写流水用；查询失败时为空
//   - error: 客户端未配置或渠道调用失败；调用方不得据此改动本地流水状态
//
// 使用示例：
//   - status, refundID, err := defaultWechatRefundQuery(ctx, s, 3, "RF20261021001")
func defaultWechatRefundQuery(ctx context.Context, s *PaymentService, configID int64, refundNo string) (status, refundID string, err error) {
	client := s.getWechatPayClientFor(ctx, configID)
	if client == nil {
		return "", "", fmt.Errorf("微信支付未配置或配置不完整")
	}
	resp, err := client.RefundQuery(ctx, refundNo)
	if err != nil {
		return "", "", err
	}
	if resp == nil {
		return "", "", fmt.Errorf("微信退款查单返回空响应 refund_no=%s", refundNo)
	}
	return resp.Status, resp.RefundID, nil
}

// wechatRefund 调用微信退款接口
// P0-2：按响应 Status 分流——仅渠道明确 SUCCESS 才视为退款成功；
// PROCESSING（受理常态）与空 status 等非确定终态保持待退款(1)，
// 返回 errRefundAccepted 由调用方分流
// 参数：
//   - ctx: 上下文
//   - p: 原支付记录
//   - refund: 退款记录
//
// 返回值：
//   - error: 渠道调用失败返回错误；受理未到终态返回 errRefundAccepted
func (s *PaymentService) wechatRefund(ctx context.Context, p *model.Payment, refund *model.RefundPayment) error {
	client := s.getWechatPayClientFor(ctx, p.ConfigID)
	if client == nil {
		return fmt.Errorf("微信支付未配置或配置不完整")
	}

	resp, err := client.Refund(ctx, &wegopayment.RefundRequest{
		OutTradeNo:  p.PaymentNo,
		OutRefundNo: refund.RefundNo,
		Amount: wegopayment.RefundAmount{
			Refund: yuanToFen(refund.Amount),
			Total:  yuanToFen(p.Amount),
		},
	})
	if err != nil {
		return fmt.Errorf("微信退款失败: %w", err)
	}

	refund.TransactionID = resp.RefundID
	if resp.Status != "SUCCESS" {
		// 空 status 一并走本分支：渠道未给出结果不等于退款成功
		log.Printf("[ProcessRefund] 微信退款已受理未到终态 refund_no=%s refund_id=%s status=%q，保持待退款由查单任务收敛",
			refund.RefundNo, resp.RefundID, resp.Status)
		return errRefundAccepted
	}
	return nil
}

// wechatVerifyTimeout 回调验签/解密的整体时限
// 验签在平台证书首次下载（或缓存失效）时会发起 HTTP 请求，不能用无截止的 Background，
// 否则证书地址不可达时回调处理协程会被无限期挂住
const wechatVerifyTimeout = 10 * time.Second

// wechatTransactionNotification 支付成功通知资源中与本系统资金判定相关的字段
// 其余字段（payer、goods_detail 等）不参与入账，不落结构体
type wechatTransactionNotification struct {
	OutTradeNo    string `json:"out_trade_no"`
	TransactionID string `json:"transaction_id"`
	AppID         string `json:"appid"`
	MchID         string `json:"mchid"`
	Amount        struct {
		Total    int    `json:"total"`
		Currency string `json:"currency"`
	} `json:"amount"`
}

// verifyWechatCallbackIdentity 校验通知归属：商户号、AppID 必须与这笔支付的实际身份一致，币种必须为 CNY
// 验签通过只证明「报文由微信发出」，不证明「这笔交易属于本商户」：平台证书/APIv3 密钥
// 曾在多商户间复用、或密钥泄露被代入他人通知时，不核对就会按别人的交易入账。
// 依据取自支付单快照：商户号是 config_id 对应配置行的值，AppID 是下单时落库的渠道身份
// （微信的 AppID 属渠道而非配置行，一条配置可服务多个渠道，只有单上的快照才能唯一确定这笔交易用了哪个）。
// 空值表示该项无可比依据（未配置微信支付、或该单是线下/存量单），跳过比对而非拒绝。
//
// 参数：
//   - n: 解密后的通知资源
//   - wantMchID: 本商户号（支付单 config_id 对应配置行的值）
//   - wantAppID: 本笔下单使用的 AppID（支付单快照）
//
// 返回值：
//   - error: 归属或币种不符时返回错误
func verifyWechatCallbackIdentity(n *wechatTransactionNotification, wantMchID, wantAppID string) error {
	if wantMchID != "" && n.MchID != wantMchID {
		return fmt.Errorf("微信支付回调商户号不匹配: 回调=%s 配置=%s", n.MchID, wantMchID)
	}
	if wantAppID != "" && n.AppID != wantAppID {
		return fmt.Errorf("微信支付回调 AppID 不匹配: 回调=%s 配置=%s", n.AppID, wantAppID)
	}
	if n.Amount.Currency != "" && n.Amount.Currency != "CNY" {
		return fmt.Errorf("微信支付回调币种不支持: %s", n.Amount.Currency)
	}
	return nil
}

// parseWechatCallback 解析微信支付回调报文
// 归属判定以「支付单快照」为准，而不是由验签命中的配置行决定：V3 通知全文加密，
// 解密之前没有任何可用的路由信息，而同一商户号下的多条配置共用 APIv3 密钥与平台证书，
// 任意一行都能解密成功——「首个验签成功即认定归属」会把归属判给随机命中的错配行，
// 随后的归属校验必然拒绝，已付款的订单因此永不入账（多配置改造引入的核心风险）。
// 现在的口径：遍历（按配置 ID 升序，结果确定）只为拿到明文与商户订单号，
// 再反查支付单，用 payments.config_id 对应行的商户号 + payments.app_id 的 AppID 快照做归属校验；
// AppID 属渠道身份、下单时已随单落库，回调时不反查渠道当前配置，渠道后续改动也不影响在途单。
// 参数：
//   - s: 支付服务实例，用于获取 wego 客户端与支付单
//   - ctx: 调用方上下文（回调处理链路），内部再套验签时限
//   - body: 回调请求体
//   - headers: 回调请求头
//
// 返回值：
//   - *payCallbackParams: 解析后的通用参数
//   - error: 错误信息
func parseWechatCallback(s *PaymentService, ctx context.Context, body []byte, headers http.Header) (*payCallbackParams, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("回调体为空")
	}
	clients := s.allWechatClients(ctx)
	if len(clients) == 0 {
		return nil, fmt.Errorf("微信支付未配置或配置不完整")
	}

	verifyCtx, cancel := context.WithTimeout(ctx, wechatVerifyTimeout)
	defer cancel()

	// 首次解密：候选按配置 ID 升序尝试验签解密，成功者提供明文
	// （同商户号多行共用密钥时明文一致，取哪一行都不影响下面的归属判定）
	var (
		plainBytes []byte
		verifiedID int64
		lastErr    error
	)
	for _, id := range slices.Sorted(maps.Keys(clients)) {
		notification, plain, err := clients[id].VerifyNotification(verifyCtx, headers, body)
		if err != nil {
			lastErr = err
			continue
		}
		// 仅处理支付成功通知，避免退款等其他通知被误处理为支付成功
		// notification 可能为 nil（VerifyNotification 对未知资源类型返回空指针），须先判空再取字段
		if notification == nil {
			return nil, fmt.Errorf("%w: 通知内容为空", errNotSuccessNotification)
		}
		if notification.EventType != "TRANSACTION.SUCCESS" {
			return nil, fmt.Errorf("%w: %s", errNotSuccessNotification, notification.EventType)
		}
		plainBytes, verifiedID = plain, id
		break
	}
	if verifiedID == 0 {
		return nil, fmt.Errorf("微信支付回调验签解密失败: %w", lastErr)
	}

	var result wechatTransactionNotification
	if err := json.Unmarshal(plainBytes, &result); err != nil {
		return nil, fmt.Errorf("微信支付回调报文解析失败: %w", err)
	}

	if result.OutTradeNo == "" || result.TransactionID == "" {
		return nil, fmt.Errorf("微信支付回调参数缺失")
	}

	// 归属依据切换：查到支付单即按单上快照校验；查不到是本系统无此单的孤儿通知，
	// 只能沿用解密命中的那一行做商户号校验，入账仍会被 handlePaySuccess 拒绝并落异常流水
	attributionID := verifiedID
	var wantAppID string
	var p model.Payment
	switch err := s.db.GetDb().WithContext(ctx).
		Where("payment_no = ?", result.OutTradeNo).First(&p).Error; {
	case err == nil:
		attributionID, wantAppID = p.ConfigID, p.AppID
	case errors.Is(err, gorm.ErrRecordNotFound):
		log.Printf("[WechatCallback] 通知单号 %s 无对应支付单，按解密命中配置行 %d 校验归属", result.OutTradeNo, verifiedID)
	default:
		// 查询异常时拒绝：归属无法确认就不能放行资金入账，回 FAIL 由微信按策略重推
		return nil, fmt.Errorf("微信支付回调归属查询失败: %w", err)
	}

	wechatCfg := s.getWechatConfigFor(ctx, attributionID)
	if err := verifyWechatCallbackIdentity(&result, wechatCfg.MchID, wantAppID); err != nil {
		return nil, err
	}

	return &payCallbackParams{
		PaymentNo:     result.OutTradeNo,
		TransactionID: result.TransactionID,
		Amount:        float64(result.Amount.Total) / 100,
	}, nil
}

// yuanToFen 将元转换为分
// 使用 math.Round 避免浮点精度导致少一分（如 0.29 元 * 100 = 28.9999...）
// 参数：
//   - yuan: 金额（元）
//
// 返回值：
//   - int: 金额（分）
func yuanToFen(yuan float64) int {
	return int(math.Round(yuan * 100))
}

// getClientIP 从 gRPC 上下文中获取对端地址
// 当前返回的是调用方（gateway）地址；如需真实客户端 IP，应由 gateway 通过 gRPC metadata 透传
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - string: 客户端 IP 字符串
func getClientIP(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return ""
}
