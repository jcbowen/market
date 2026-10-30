package service

// 付款码支付（收银台 B扫C）扣款实现
// 两步化流程的第二步：CreatePayment 对 pay_type=5(微信付款码)/6(支付宝付款码) 只建待支付单，
// 收银员扫顾客付款码后由 PayByAuthCode 向渠道提交扣款。
// 失败重试复用同一支付单（out_trade_no 不变）；USERPAYING 在途由收银台轮询 payment-sync 跟进
// （渠道回调与对账调度器同样兜底），超时未支付由订单超时取消 + 渠道关单 + 挂账机制兜底。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/model"
	wegopayment "github.com/jcbowen/wego/payment"

	alipaysdk "github.com/jcbowen/alipay-sdk-go-all"
	alipaymodel "github.com/jcbowen/alipay-sdk-go-all/model"
)

// 付款码渠道扣款以包级变量注入，单测直接替换，无需真实商户配置；
// 渠道就绪判定已收敛到 resolveActivePayConfig（配置存在/启用/平台匹配/客户端就绪）
var (
	wechatAuthCodePay = defaultWechatAuthCodePay
	alipayAuthCodePay = defaultAlipayAuthCodePay
)

// codePayResult 付款码扣款的归一化结果
//
// 字段说明：
//   - State: ChannelStatePaid（扣款成功）/ ChannelStatePaying（用户支付中）/ ChannelStateClosed（确定性失败，可重新扫码）
//   - TransactionID: 渠道交易号，State=Paid 时有值
//   - Amount: 渠道侧实收金额（元），State=Paid 且渠道应答带金额时有值；
//     为零表示渠道未回传金额，调用方回退到支付单应付额
//   - Message: 失败或在途原因，展示给收银员
type codePayResult struct {
	State         string
	TransactionID string
	Amount        float64
	Message       string
}

// payTypeFromAuthCode 按付款码前缀识别渠道
// 微信付款码 10~15 开头、支付宝付款码 25~30 开头；云闪付（62 开头）等暂不支持返回 0
// 参数：
//   - authCode: 顾客付款码（纯数字）
//
// 返回值：
//   - int32: 5=微信付款码 6=支付宝付款码 0=无法识别或不支持
func payTypeFromAuthCode(authCode string) int32 {
	if len(authCode) < 2 {
		return 0
	}
	switch prefix := authCode[:2]; {
	case prefix >= "10" && prefix <= "15":
		return 5
	case prefix >= "25" && prefix <= "30":
		return 6
	default:
		return 0
	}
}

// PayByAuthCode 付款码扣款（收银台 B扫C）
// 对已有待支付支付单提交顾客付款码向渠道收款，渠道按付款码前缀识别（微信 10-15 / 支付宝 25-30，
// 占位单 pay_type 不符时原地纠正），结果归一化为 PAID/PAYING/FAILED：
//   - PAID：扣款成功，事务内置支付单已支付（确认来源=线下，ref 记收银员），事务外同步订单状态
//   - PAYING：用户支付中（输密码等），支付单保持待支付，收银台轮询 payment-sync 跟进最终结果
//   - FAILED：渠道确定性拒绝（码无效/余额不足等），支付单保持待支付，收银员换码重扫（out_trade_no 不变）
//
// 云闪付（62 开头）等暂不支持，直接拒绝提示重扫；渠道网络异常以 Unavailable 错误返回
// （资金状态未知，不可判死）；幂等：已支付单重复提交直接回 PAID
// 参数：
//   - ctx: 上下文
//   - req: 扣款请求（payment_no 定位待支付单，order_id 强校验订单绑定，auth_code 为顾客付款码，operator_id 记操作人）
//
// 返回值：
//   - *payment.PayByAuthCodeResp: 归一化结果
//   - error: 参数错误/支付单不存在/支付单不可收款/渠道请求异常
func (s *PaymentService) PayByAuthCode(ctx context.Context, req *payment.PayByAuthCodeReq) (*payment.PayByAuthCodeResp, error) {
	authCode := strings.TrimSpace(req.AuthCode)
	if req.PaymentNo == "" || authCode == "" || req.OrderId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "参数错误")
	}
	// 付款码为纯数字（微信 10-15 开头、支付宝 25-30 开头，长度 16~32 区间），拦住明显误输
	if len(authCode) < 16 || len(authCode) > 64 {
		return nil, bizerr.New(codes.InvalidArgument, "付款码格式不正确，请重新扫码")
	}

	var p model.Payment
	if err := s.db.GetDb().WithContext(ctx).Where("payment_no = ?", req.PaymentNo).First(&p).Error; err != nil {
		return nil, bizerr.New(codes.NotFound, "支付单不存在")
	}
	// 订单绑定校验：网关传入的订单 ID（已过社区隔离）必须与支付单归属一致，
	// 防止传错/恶意传其它订单的支付单号造成跨订单收款
	if p.OrderID != req.OrderId {
		return nil, bizerr.New(codes.FailedPrecondition, "支付单与订单不匹配，请刷新订单信息后重试")
	}
	if p.PayType != 5 && p.PayType != 6 {
		return nil, bizerr.New(codes.FailedPrecondition, "该支付单不是付款码支付单，无法扫码收款")
	}
	// 幂等：已支付单重复提交（扫码枪回车重放/收银员重复点）不重复扣款，直接回已支付
	if p.Status == 2 {
		return s.buildCodePayResp(&p, &codePayResult{
			State:         ChannelStatePaid,
			TransactionID: p.TransactionID,
			Message:       "该支付单已收款",
		}), nil
	}
	if p.Status != 1 {
		return nil, bizerr.New(codes.FailedPrecondition, "支付单已关闭或已退款，无法收款")
	}

	// 扣款前按前缀识别的平台路由到对应支付配置（网关已按 pos 渠道绑定解析出 req.ConfigId）：
	// 配置不存在/停用/平台不匹配/凭证不齐均在此拦截，与下单校验同口径。
	// 渠道由付款码前缀决定（决策②单一入口）：下单时渠道未知，支付单以 pay_type=5 占位创建，
	// 此处按前缀识别并纠正 pay_type 与 config_id——占位单尚未被任何渠道受理（status=1 待支付），原地切换无损
	detected := payTypeFromAuthCode(authCode)
	if detected == 0 {
		if strings.HasPrefix(authCode, "62") {
			return nil, bizerr.New(codes.FailedPrecondition, "暂不支持云闪付付款码，请让顾客出示微信/支付宝付款码")
		}
		return nil, bizerr.New(codes.FailedPrecondition, "无法识别的付款码，请让顾客出示微信/支付宝付款码")
	}
	platform := model.PayPlatformWechat
	if detected == 6 {
		platform = model.PayPlatformAlipay
	}
	if _, err := s.resolveActivePayConfig(ctx, req.ConfigId, platform); err != nil {
		return nil, err
	}
	if p.PayType != detected {
		if err := s.db.GetDb().WithContext(ctx).Model(&model.Payment{}).
			Where("id = ? AND status = 1", p.ID).Update("pay_type", detected).Error; err != nil {
			return nil, bizerr.New(codes.Internal, "支付单渠道切换失败，请重试")
		}
		p.PayType = detected
	}
	if p.ConfigID != req.ConfigId {
		if err := s.db.GetDb().WithContext(ctx).Model(&model.Payment{}).
			Where("id = ? AND status = 1", p.ID).Update("config_id", req.ConfigId).Error; err != nil {
			return nil, bizerr.New(codes.Internal, "支付单配置切换失败，请重试")
		}
		p.ConfigID = req.ConfigId
	}

	var res *codePayResult
	var err error
	if p.PayType == 5 {
		res, err = wechatAuthCodePay(ctx, s, &p, authCode)
	} else {
		res, err = alipayAuthCodePay(ctx, s, &p, authCode)
	}
	if err != nil {
		// 渠道请求失败（网络/网关异常）：资金状态未知，不可判失败，收银员可重试
		return nil, bizerr.New(codes.Unavailable, "渠道请求失败，请重试")
	}

	// 扣款成功：复用统一的支付成功处理（金额比对/幂等流转/订单同步），确认来源标线下并记录收银员。
	// 金额用渠道实收（渠道未回传时回退应付额）：handlePaySuccess 的分位比对是
	// 「渠道实收 vs 应付」的二次校验，传应付额等于自己跟自己对账、校验形同虚设
	if res.State == ChannelStatePaid {
		if err := s.handlePaySuccess(ctx, &payCallbackParams{
			PaymentNo:     p.PaymentNo,
			TransactionID: res.TransactionID,
			Amount:        codePayConfirmAmount(res, &p),
			ConfirmSource: model.ConfirmSourceOffline,
			ConfirmRef:    fmt.Sprintf("codepay:%d", req.OperatorId),
			NotifyKind:    model.NotifyKindCodePay,
			Channel:       notifyChannelOf(p.PayType),
			OrderID:       p.OrderID,
		}); err != nil {
			return nil, err
		}
	}
	return s.buildCodePayResp(&p, res), nil
}

// buildCodePayResp 组装付款码扣款响应（归一化状态映射 proto 枚举）
// 参数：
//   - p: 支付单
//   - res: 渠道归一化结果
//
// 返回值：
//   - *payment.PayByAuthCodeResp: 响应
func (s *PaymentService) buildCodePayResp(p *model.Payment, res *codePayResult) *payment.PayByAuthCodeResp {
	resp := &payment.PayByAuthCodeResp{
		PaymentNo: p.PaymentNo,
		OrderId:   p.OrderID,
		PayType:   p.PayType,
		Amount:    p.Amount,
		Message:   res.Message,
	}
	switch res.State {
	case ChannelStatePaid:
		resp.State = "PAID"
		resp.TransactionId = res.TransactionID
	case ChannelStatePaying:
		resp.State = "PAYING"
	default:
		resp.State = "FAILED"
		// FAILED 的默认提示：渠道拒绝原因为空时给通用话术
		if resp.Message == "" {
			resp.Message = "扣款失败，请重新扫码"
		}
	}
	return resp
}

// defaultWechatAuthCodePay 微信付款码扣款（V3 /v3/pay/transactions/codepay）
// 同步响应即扣款结果：SUCCESS=成功；USERPAYING=用户支付中（轮询查单跟进）；其余状态=确定性失败
// 参数：
//   - ctx: 上下文
//   - s: 支付服务
//   - p: 待支付支付单
//   - authCode: 顾客微信付款码
//
// 返回值：
//   - *codePayResult: 归一化结果
//   - error: 渠道请求异常（非业务拒绝），调用方按「状态未知可重试」处理
func defaultWechatAuthCodePay(ctx context.Context, s *PaymentService, p *model.Payment, authCode string) (*codePayResult, error) {
	client := s.getWechatPayClientFor(ctx, p.ConfigID)
	if client == nil {
		return nil, fmt.Errorf("微信支付未配置或配置不完整")
	}
	order, err := client.CodePay(ctx, &wegopayment.CodePayRequest{
		Description: "社区商超订单",
		OutTradeNo:  p.PaymentNo,
		Amount:      wegopayment.Amount{Total: yuanToFen(p.Amount), Currency: "CNY"},
		Payer:       wegopayment.CodePayer{AuthCode: authCode},
	})
	if err != nil {
		var apiErr *wegopayment.APIError
		if !errors.As(err, &apiErr) {
			return nil, err
		}
		// 重试撞上前一笔已支付（USERPAYING 后用户完成付款又重扫）：渠道报 ORDER_PAID，
		// 不能按失败处理，回查渠道确认真实资金状态
		if strings.Contains(strings.ToUpper(apiErr.Code), "ORDER_PAID") || strings.Contains(strings.ToUpper(apiErr.Code), "ORDERPAID") {
			return s.resolveCodePayByQuery(ctx, p)
		}
		// 业务拒绝（付款码无效/余额不足/风控等）：确定性失败，提示收银员重新扫码
		return &codePayResult{State: ChannelStateClosed, Message: apiErr.Message}, nil
	}
	switch order.TradeState {
	case wegopayment.TradeStateSuccess, wegopayment.TradeStateRefund:
		// REFUND（转入退款）说明钱确实收到过，同样按已收款处理
		var paid float64
		if order.Amount != nil {
			// 微信金额单位为分，统一换算为元
			paid = float64(order.Amount.Total) / 100
		}
		return &codePayResult{State: ChannelStatePaid, TransactionID: order.TransactionID, Amount: paid}, nil
	case wegopayment.TradeStateUserPaying, wegopayment.TradeStateNotPay:
		// USERPAYING 用户输密码中；NOTPAY 是「未支付」的在途口径，均交由轮询跟进，不可判死
		return &codePayResult{State: ChannelStatePaying, Message: order.TradeStateDesc}, nil
	default:
		// CLOSED/REVOKED/PAYERROR 等为确定性失败
		return &codePayResult{State: ChannelStateClosed, Message: order.TradeStateDesc}, nil
	}
}

// defaultAlipayAuthCodePay 支付宝付款码扣款（V3 /v3/alipay/trade/pay，scene=bar_code）
// 与 alipayRefund 同样走通用 Execute 并自判业务码：网关可能以 HTTP 200 返回业务失败，
// 而本方法用于判定资金是否到账，不能只看 HTTP 状态
// 参数：
//   - ctx: 上下文
//   - s: 支付服务
//   - p: 待支付支付单
//   - authCode: 顾客支付宝付款码
//
// 返回值：
//   - *codePayResult: 归一化结果
//   - error: 渠道请求异常（非业务拒绝），调用方按「状态未知可重试」处理
func defaultAlipayAuthCodePay(ctx context.Context, s *PaymentService, p *model.Payment, authCode string) (*codePayResult, error) {
	executor := s.getAlipayExecutorFor(ctx, p.ConfigID)
	if executor == nil {
		return nil, fmt.Errorf("支付宝支付未配置或配置不完整")
	}
	resp, apiErr := executor.Execute("/v3/alipay/trade/pay", "POST", &alipaysdk.OpenApiGenericRequest{
		BodyParams: &alipaymodel.AlipayTradePayModel{
			OutTradeNo:  alipaysdk.Str(p.PaymentNo),
			Scene:       alipaysdk.Str("bar_code"),
			AuthCode:    alipaysdk.Str(authCode),
			Subject:     alipaysdk.Str("社区商超订单"),
			TotalAmount: alipaysdk.Str(fmt.Sprintf("%.2f", p.Amount)),
		},
	})
	if apiErr != nil {
		// V3 业务拒绝（如撞上前一笔已完成支付）以 4xx 返回，错误码在响应体里：
		// ACQ.TRADE_HAS_SUCCESS 是「渠道已收款」的确定结论，回查确认真实资金状态；
		// 其余异常维持原语义（状态未知，可重试）
		if body := alipayV3ApiErrBody(apiErr); body != nil {
			if strings.Contains(strings.ToUpper(alipayRespString(body, "sub_code")), "ACQ.TRADE_HAS_SUCCESS") {
				return s.resolveCodePayByQuery(ctx, p)
			}
		}
		return nil, apiErr
	}
	// 经 alipayV3BizError 判定：V3 正常响应体无 code 字段，判 code 会把支付成功误判为失败
	if failed, _, subCode, subMsg := alipayV3BizError(resp); failed {
		// 重试撞上前一笔已完成支付（ACQ.TRADE_HAS_SUCCESS）：回查确认真实资金状态
		if strings.Contains(strings.ToUpper(subCode), "ACQ.TRADE_HAS_SUCCESS") {
			return s.resolveCodePayByQuery(ctx, p)
		}
		return &codePayResult{State: ChannelStateClosed, Message: subMsg}, nil
	}
	switch normalizeAlipayTradeStatus(alipayRespString(resp, "trade_status")) {
	case ChannelStatePaid:
		return &codePayResult{
			State:         ChannelStatePaid,
			TransactionID: alipayRespString(resp, "trade_no"),
			Amount:        alipayRespAmount(resp, "total_amount"),
		}, nil
	case ChannelStatePaying:
		return &codePayResult{State: ChannelStatePaying, Message: alipayRespString(resp, "trade_status")}, nil
	default:
		return &codePayResult{State: ChannelStateClosed, Message: alipayRespString(resp, "trade_status")}, nil
	}
}

// codePayConfirmAmount 本次支付确认使用的金额（元）
// 渠道实收优先；渠道应答未带金额（或解析失败）时回退支付单应付额，
// 否则 handlePaySuccess 会因「金额缺失」直接拒绝确认，把已扣到钱的单卡在待支付
// 参数：
//   - res: 渠道归一化结果
//   - p: 支付单
//
// 返回值：
//   - float64: 用于与应付额做分位比对的金额
func codePayConfirmAmount(res *codePayResult, p *model.Payment) float64 {
	if res != nil && res.Amount > 0 {
		return res.Amount
	}
	return p.Amount
}

// notifyChannelOf 支付单支付方式归一化为流水渠道（1 微信 / 2 支付宝）
// 直接写 pay_type 会让付款码单（5/6）与线下单（3/4）在异常页的渠道列显示成未知值：
// 5 归微信、6 归支付宝，线下收款无渠道记 0（前端展示为「-」）
// 参数：
//   - payType: 支付方式（1微信 2支付宝 3货到付款 4现金 5微信付款码 6支付宝付款码）
//
// 返回值：
//   - int32: 流水 channel 取值
func notifyChannelOf(payType int32) int32 {
	switch payType {
	case 1, 5:
		return 1
	case 2, 6:
		return 2
	default:
		return 0
	}
}

// resolveCodePayByQuery 重试撞上「渠道已支付」时的回查确认
// 回查失败时不能断言失败（钱可能已扣），按在途处理交由轮询/对账跟进
// 参数：
//   - ctx: 上下文
//   - p: 待确认的支付单
//
// 返回值：
//   - *codePayResult: 归一化结果
//   - error: 无（回查异常也归一化为在途/失败结果而非错误）
func (s *PaymentService) resolveCodePayByQuery(ctx context.Context, p *model.Payment) (*codePayResult, error) {
	res, err := s.queryChannelOrder(ctx, p)
	if err != nil || res == nil {
		return &codePayResult{State: ChannelStatePaying, Message: "渠道状态确认中，请稍后查看收款结果"}, nil
	}
	switch res.State {
	case ChannelStatePaid:
		return &codePayResult{State: ChannelStatePaid, TransactionID: res.TransactionID, Amount: res.Amount, Message: "该单已支付"}, nil
	case ChannelStateNotExist, ChannelStateClosed:
		return &codePayResult{State: ChannelStateClosed, Message: "渠道交易已关闭，请重新扫码"}, nil
	default:
		return &codePayResult{State: ChannelStatePaying, Message: "用户支付中，请等待收款结果"}, nil
	}
}
