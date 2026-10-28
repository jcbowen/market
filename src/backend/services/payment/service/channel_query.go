package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jcbowen/market/src/backend/services/payment/model"
	wegopayment "github.com/jcbowen/wego/payment"

	alipaysdk "github.com/jcbowen/alipay-sdk-go-all"
	alipaymodel "github.com/jcbowen/alipay-sdk-go-all/model"
)

// 渠道收款结果的归一化状态。
// 两个渠道各自的状态字面量不同，且对账/取消前置/后台同步三条路径都只关心「收没收到钱」，
// 故在此收敛为一层，调用方不再散落 if trade_state == "SUCCESS" 之类的字面量比较。
const (
	// ChannelStatePaid 渠道已收款（微信 SUCCESS；支付宝 TRADE_SUCCESS/TRADE_FINISHED）
	ChannelStatePaid = "PAID"
	// ChannelStatePaying 渠道仍在等待用户支付，后续仍可能成功，需保留并再查
	ChannelStatePaying = "PAYING"
	// ChannelStateClosed 渠道侧交易已关闭/撤销，不会再有收款
	ChannelStateClosed = "CLOSED"
	// ChannelStateNotExist 渠道无此交易（用户压根没发起支付）
	ChannelStateNotExist = "NOT_EXIST"
	// ChannelStateError 查询失败（网络/配置/业务异常），不可据此判定资金状态
	ChannelStateError = "ERROR"
)

// channelPayResult 渠道查询归一化结果
//
// 字段说明：
//   - State: 见 ChannelState* 常量
//   - TransactionID: 渠道交易号（微信 transaction_id / 支付宝 trade_no），State=Paid 时有值
//   - Amount: 渠道侧订单金额，统一为元（微信返回单位为分，此处已换算）
//   - Raw: 渠道原始状态串或错误码，写流水用
type channelPayResult struct {
	State         string
	TransactionID string
	Amount        float64
	Raw           string
}

// 渠道调用以包级变量注入，单测直接替换为假渠道，无需真实商户配置
var (
	queryWechatOrder = defaultQueryWechatOrder
	queryAlipayOrder = defaultQueryAlipayOrder
	closeWechatOrder = defaultCloseWechatOrder
	closeAlipayOrder = defaultCloseAlipayOrder
)

// defaultQueryWechatOrder 查询微信支付单状态（V3 商户订单号查询）
//
// 参数：
//   - ctx: 上下文
//   - s: 支付服务（取配置热更新后的微信客户端）
//   - paymentNo: 商户订单号（本系统支付单号）
//
// 返回值：
//   - *channelPayResult: 归一化结果
//   - error: 渠道调用或配置错误；State=ChannelStateError 时调用方不得改动本地状态
//
// 使用说明：
//   - res, err := defaultQueryWechatOrder(ctx, s, "PAY20261021001")
func defaultQueryWechatOrder(ctx context.Context, s *PaymentService, paymentNo string) (*channelPayResult, error) {
	client := s.getWechatPayClient()
	if client == nil {
		return &channelPayResult{State: ChannelStateError, Raw: "wechat client not configured"}, fmt.Errorf("微信支付未配置或配置不完整")
	}
	order, err := client.QueryOrder(ctx, paymentNo)
	if err != nil {
		// ORDER_NOT_EXIST 是「用户没付过钱」的确定结论，与网络失败必须区分开：
		// 前者可安全关单，后者绝不能据此关单，否则会把在途支付判死
		if isWechatOrderNotExist(err) {
			return &channelPayResult{State: ChannelStateNotExist, Raw: wechatErrCode(err)}, nil
		}
		return &channelPayResult{State: ChannelStateError, Raw: err.Error()}, err
	}
	res := &channelPayResult{TransactionID: order.TransactionID, Raw: order.TradeState}
	if order.Amount != nil {
		// 微信金额单位为分，统一换算为元后交给 handlePaySuccess 做分位比对
		res.Amount = float64(order.Amount.Total) / 100
	}
	switch order.TradeState {
	case wegopayment.TradeStateSuccess, wegopayment.TradeStateRefund:
		// REFUND（转入退款）说明钱确实收到过，同样按已收款处理
		res.State = ChannelStatePaid
	case wegopayment.TradeStateNotPay, wegopayment.TradeStateUserPaying:
		res.State = ChannelStatePaying
	case wegopayment.TradeStateClosed, wegopayment.TradeStateRevoked, wegopayment.TradeStatePayError:
		res.State = ChannelStateClosed
	default:
		// 未知状态一律按「在途」处理：宁可多种一轮，不可错关
		res.State = ChannelStatePaying
	}
	return res, nil
}

// defaultQueryAlipayOrder 查询支付宝交易状态（V3 /v3/alipay/trade/query）
// 与 alipayRefund 同样走通用 Execute 并自判业务码：网关可能以 HTTP 200 返回业务失败，
// 而本方法用于判定资金是否到账，不能只看 HTTP 状态
//
// 参数：
//   - ctx: 上下文
//   - s: 支付服务
//   - paymentNo: 商户订单号
//
// 返回值：
//   - *channelPayResult: 归一化结果
//   - error: 渠道调用或配置错误
func defaultQueryAlipayOrder(ctx context.Context, s *PaymentService, paymentNo string) (*channelPayResult, error) {
	executor := s.getAlipayExecutor()
	if executor == nil {
		return &channelPayResult{State: ChannelStateError, Raw: "alipay executor not configured"}, fmt.Errorf("支付宝支付未配置或配置不完整")
	}
	resp, apiErr := executor.Execute("/v3/alipay/trade/query", "POST", &alipaysdk.OpenApiGenericRequest{
		BodyParams: &alipaymodel.AlipayTradeQueryModel{
			OutTradeNo: alipaysdk.Str(paymentNo),
		},
	})
	if apiErr != nil {
		// V3 对不存在的交易返回 4xx，错误码在响应体里：识别 NOT_EXIST 得到
		// 「用户没付过钱」的确定结论（可安全关单/留痕），与网络失败严格区分
		if body := alipayV3ApiErrBody(apiErr); body != nil {
			if sub := alipayRespString(body, "sub_code"); strings.Contains(strings.ToUpper(sub), "TRADE_NOT_EXIST") {
				return &channelPayResult{State: ChannelStateNotExist, Raw: sub}, nil
			}
		}
		return &channelPayResult{State: ChannelStateError, Raw: apiErr.Error()}, fmt.Errorf("支付宝查询失败: %s", apiErr.Error())
	}
	// 经 alipayV3BizError 判定而非 IsBizSuccess(resp["code"])：
	// V3 正常响应体无 code 字段，直接判 code 会把 TRADE_SUCCESS 误判为查询失败（捞回机制失效）
	if failed, code, subCode, subMsg := alipayV3BizError(resp); failed {
		if strings.Contains(strings.ToUpper(subCode), "TRADE_NOT_EXIST") {
			return &channelPayResult{State: ChannelStateNotExist, Raw: subCode}, nil
		}
		raw := fmt.Sprintf("code=%s sub_code=%s sub_msg=%s", code, subCode, subMsg)
		return &channelPayResult{State: ChannelStateError, Raw: raw}, fmt.Errorf("支付宝查询业务失败: %s", raw)
	}
	tradeStatus := alipayRespString(resp, "trade_status")
	res := &channelPayResult{
		State:         normalizeAlipayTradeStatus(tradeStatus),
		TransactionID: alipayRespString(resp, "trade_no"),
		Raw:           tradeStatus,
	}
	res.Amount = alipayRespAmount(resp, "total_amount")
	return res, nil
}

// normalizeAlipayTradeStatus 支付宝交易状态归一化
// 参数：
//   - tradeStatus: 支付宝 trade_status 原值
//
// 返回值：
//   - string: ChannelState* 常量之一，未知状态按在途处理
func normalizeAlipayTradeStatus(tradeStatus string) string {
	switch strings.ToUpper(tradeStatus) {
	case "TRADE_SUCCESS", "TRADE_FINISHED":
		return ChannelStatePaid
	case "WAIT_BUYER_PAY":
		return ChannelStatePaying
	case "TRADE_CLOSED":
		return ChannelStateClosed
	default:
		return ChannelStatePaying
	}
}

// defaultCloseWechatOrder 关闭微信支付单
// 语义边界：微信只允许关闭未支付交易，对已支付交易返回错误（ORDERPAID 类错误码），
// 不会自动退款——退钱必须走 ProcessRefund
//
// 参数：
//   - ctx: 上下文
//   - s: 支付服务
//   - paymentNo: 商户订单号
//
// 返回值：
//   - error: 关闭失败原因，调用方按「不影响本地关单」处理
func defaultCloseWechatOrder(ctx context.Context, s *PaymentService, paymentNo string) error {
	client := s.getWechatPayClient()
	if client == nil {
		return fmt.Errorf("微信支付未配置或配置不完整")
	}
	return client.CloseOrder(ctx, paymentNo)
}

// defaultCloseAlipayOrder 关闭支付宝交易（V3 /v3/alipay/trade/close）
// 对已付款交易返回业务失败（ACQ.TRADE_STATUS_ERROR），同样不触发退款
//
// 参数：
//   - ctx: 上下文
//   - s: 支付服务
//   - paymentNo: 商户订单号
//
// 返回值：
//   - error: 关闭失败原因
func defaultCloseAlipayOrder(ctx context.Context, s *PaymentService, paymentNo string) error {
	executor := s.getAlipayExecutor()
	if executor == nil {
		return fmt.Errorf("支付宝支付未配置或配置不完整")
	}
	resp, apiErr := executor.Execute("/v3/alipay/trade/close", "POST", &alipaysdk.OpenApiGenericRequest{
		BodyParams: &alipaymodel.AlipayTradeCloseModel{
			OutTradeNo: alipaysdk.Str(paymentNo),
		},
	})
	if apiErr != nil {
		return fmt.Errorf("支付宝关单失败: %s", apiErr.Error())
	}
	// 经 alipayV3BizError 判定：V3 正常响应体无 code 字段，判 code 会把关单成功误判为失败
	if failed, code, subCode, subMsg := alipayV3BizError(resp); failed {
		return fmt.Errorf("支付宝关单业务失败: code=%s sub_code=%s sub_msg=%s", code, subCode, subMsg)
	}
	return nil
}

// queryChannelOrder 按支付单归属渠道查询渠道侧收款结果
// 查询前按版本号刷新支付配置：未承接过下单的实例若沿用启动时的空配置，会把可用渠道判为不可用
//
// 参数：
//   - ctx: 上下文
//   - p: 支付记录（pay_type 为 1微信 / 2支付宝 / 5微信付款码 / 6支付宝付款码，
//     付款码单与同渠道在线支付走同一套查单接口）
//
// 返回值：
//   - *channelPayResult: 归一化结果
//   - error: 渠道无查询能力（现金/货到付款）或调用失败
//
// 使用示例：
//   - res, err := s.queryChannelOrder(ctx, p)
func (s *PaymentService) queryChannelOrder(ctx context.Context, p *model.Payment) (*channelPayResult, error) {
	if p == nil {
		return nil, fmt.Errorf("支付单为空")
	}
	s.getPayConfigCached(ctx)
	switch p.PayType {
	case 1, 5:
		return queryWechatOrder(ctx, s, p.PaymentNo)
	case 2, 6:
		return queryAlipayOrder(ctx, s, p.PaymentNo)
	default:
		return nil, fmt.Errorf("支付方式 %d 无渠道查询能力", p.PayType)
	}
}

// closeChannelOrder 关闭渠道侧未支付交易
// 微信付款码单（pay_type=5）走撤销而非关单：付款码交易在 USERPAYING 状态下
// 关单接口会被拒绝，只有撤销才能终止在途扣款（撤销对已支付交易失败且不退款，退款走 ProcessRefund）
// 参数：
//   - ctx: 上下文
//   - p: 支付记录
//
// 返回值：
//   - error: 关闭失败原因（不影响调用方继续本地关单）
func (s *PaymentService) closeChannelOrder(ctx context.Context, p *model.Payment) error {
	if p == nil {
		return fmt.Errorf("支付单为空")
	}
	s.getPayConfigCached(ctx)
	switch p.PayType {
	case 1:
		return closeWechatOrder(ctx, s, p.PaymentNo)
	case 2, 6:
		return closeAlipayOrder(ctx, s, p.PaymentNo)
	case 5:
		return closeWechatCodeOrder(ctx, s, p.PaymentNo)
	default:
		return fmt.Errorf("支付方式 %d 无渠道关单能力", p.PayType)
	}
}

// closeWechatCodeOrder 撤销微信付款码交易（V3 reverse，包级变量便于单测替换）
// 参数：
//   - ctx: 上下文
//   - s: 支付服务
//   - paymentNo: 商户订单号（本系统支付单号）
//
// 返回值：
//   - error: 渠道调用或配置错误
var closeWechatCodeOrder = defaultCloseWechatCodeOrder

// defaultCloseWechatCodeOrder 撤销微信付款码交易（V3 /v3/pay/transactions/out-trade-no/{}/reverse）
// 参数：
//   - ctx: 上下文
//   - s: 支付服务
//   - paymentNo: 商户订单号（本系统支付单号）
//
// 返回值：
//   - error: 渠道调用或配置错误
func defaultCloseWechatCodeOrder(ctx context.Context, s *PaymentService, paymentNo string) error {
	client := s.getWechatPayClient()
	if client == nil {
		return fmt.Errorf("微信支付未配置或配置不完整")
	}
	return client.ReverseOrder(ctx, paymentNo)
}

// isWechatOrderNotExist 判断微信查询错误是否为「订单不存在」
// 参数：
//   - err: 微信接口错误
//
// 返回值：
//   - bool: 成立表示该笔交易在渠道侧从未创建，可安全结束对账
func isWechatOrderNotExist(err error) bool {
	var apiErr *wegopayment.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return strings.Contains(strings.ToUpper(apiErr.Code), "NOT_EXIST")
}

// wechatErrCode 提取微信接口错误码
// 参数：
//   - err: 微信接口错误
//
// 返回值：
//   - string: 错误码，非 APIError 时返回错误文本
func wechatErrCode(err error) string {
	var apiErr *wegopayment.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	if err == nil {
		return ""
	}
	return err.Error()
}
