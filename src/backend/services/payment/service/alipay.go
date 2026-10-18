package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/services/payment/model"

	alipaysdk "github.com/jcbowen/alipay-sdk-go-all"
	alipaymodel "github.com/jcbowen/alipay-sdk-go-all/model"
)

// buildAlipayExecutor 根据支付宝配置构建通用执行器（V2 pageExecute 与 V3 API 共享）
// 采用公钥模式：AppID + 应用私钥 + 支付宝公钥；配置不完整或构建失败时返回 nil，
// C 端渠道接口据其就绪状态隐藏支付宝选项
// 参数：
//   - cfg: 支付宝配置载体
//
// 返回值：
//   - *alipaysdk.GenericExecuteApi: 通用执行器，配置不完整时为 nil
func buildAlipayExecutor(cfg config.AlipayConfig) *alipaysdk.GenericExecuteApi {
	if !cfg.IsConfigured() {
		return nil
	}
	configUtil, err := alipaysdk.NewAlipayConfigUtil(&alipaysdk.AlipayConfig{
		AppId:           cfg.AppID,
		PrivateKey:      cfg.PrivateKey,
		AlipayPublicKey: cfg.AlipayPublicKey,
	})
	if err != nil {
		log.Printf("构建支付宝配置工具失败: %v", err)
		return nil
	}
	executor, err := alipaysdk.NewGenericExecuteApi(configUtil)
	if err != nil {
		log.Printf("构建支付宝执行器失败: %v", err)
		return nil
	}
	return executor
}

// buildAlipayParams 构造支付宝手机网站支付（alipay.trade.wap.pay）调起参数
// 通过 V2 网关 pageExecute（GET）生成带 RSA2 签名的收银台跳转 URL，
// H5 端直接跳转该 URL 即可在浏览器内唤起支付宝完成支付
// 参数：
//   - ctx: 上下文
//   - p: 支付记录模型
//
// 返回值：
//   - string: 调起参数 JSON 字符串（{"alipay_pay_url": "https://openapi.alipay.com/..."}）
//   - error: 错误信息
func (s *PaymentService) buildAlipayParams(ctx context.Context, p *model.Payment) (string, error) {
	// 支付前检测数据库配置版本，后台修改配置后无需重启即可生效
	s.getPayConfigCached(ctx)
	executor := s.getAlipayExecutor()
	if executor == nil {
		return "", fmt.Errorf("支付宝支付未配置或配置不完整")
	}
	cfg := s.getAlipayPayConfig()

	biz := map[string]interface{}{
		"out_trade_no":    p.PaymentNo,
		"total_amount":    fmt.Sprintf("%.2f", p.Amount),
		"subject":         "社区商超订单",
		"product_code":    "QUICK_WAP_WAY",
		"timeout_express": s.alipayTimeoutExpress(),
	}
	bizParams := map[string]interface{}{
		"biz_content": biz,
		"notify_url":  cfg.NotifyURL,
	}
	if cfg.ReturnURL != "" {
		bizParams["return_url"] = cfg.ReturnURL
		// quit_url：用户在支付宝收银台中途退出后回跳的商户地址，与支付完成回跳同域
		biz["quit_url"] = cfg.ReturnURL
	}

	payURL, err := executor.PageExecute("alipay.trade.wap.pay", "GET", bizParams, "", "", nil)
	if err != nil {
		return "", fmt.Errorf("支付宝下单失败: %w", err)
	}

	data, err := json.Marshal(map[string]string{"alipay_pay_url": payURL})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// alipayTimeoutExpress 计算支付宝交易的相对超时时长（biz_content.timeout_express）
// 取业务配置的订单超时秒数向上取整换算为分钟，使支付宝侧交易时效与本地订单超时取消时效对齐；
// 不传该参数时支付宝按签约协议给默认时效（约 15 天），本地订单早已取消而收银台仍可付款，
// 成功回调会被支付单终态兜底忽略，形成「钱已收、订单已取消」的资金挂账
//
// 返回值：
//   - string: 形如 "30m"，最小 1m；配置缺失或非正时回落默认 1800 秒
func (s *PaymentService) alipayTimeoutExpress() string {
	seconds := 1800
	if s.cfg != nil && s.cfg.Business.OrderTimeout > 0 {
		seconds = s.cfg.Business.OrderTimeout
	}
	// 向上取整：不足 1 分钟的超时不应被压成 0m（支付宝不接受）
	minutes := (seconds + 59) / 60
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("%dm", minutes)
}

// alipayRefund 调用支付宝退款接口（V3 /v3/alipay/trade/refund）
// out_request_no 传退款单号，支付宝侧保证同退款请求号多次请求只退一次，天然幂等；
// 同步返回即退款受理结果，fund_change=N 表示该请求未实际改变资金（重复请求），同样视为成功
// 走通用 Execute 而非 typed Refund：SDK 的成功响应模型不含 code 字段，且 SDK 对齐 PHP 行为
// 在 HTTP 2xx 时不判业务码；退款是资金写操作，必须自行确认 code=10000，
// 否则网关以 200 返回错误码时会被误记「已退款」并回补库存
// 参数：
//   - ctx: 上下文
//   - p: 原支付记录
//   - refund: 退款记录
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) alipayRefund(ctx context.Context, p *model.Payment, refund *model.RefundPayment) error {
	executor := s.getAlipayExecutor()
	if executor == nil {
		return fmt.Errorf("支付宝支付未配置或配置不完整")
	}
	resp, apiErr := executor.Execute("/v3/alipay/trade/refund", "POST", &alipaysdk.OpenApiGenericRequest{
		BodyParams: &alipaymodel.AlipayTradeRefundModel{
			OutTradeNo:   alipaysdk.Str(p.PaymentNo),
			OutRequestNo: alipaysdk.Str(refund.RefundNo),
			RefundAmount: alipaysdk.Str(fmt.Sprintf("%.2f", refund.Amount)),
			RefundReason: alipaysdk.Str("订单退款"),
		},
	})
	if apiErr != nil {
		return fmt.Errorf("支付宝退款失败: %s", apiErr.Error())
	}
	code := alipayRespString(resp, "code")
	if !alipaysdk.IsBizSuccess(code) {
		return fmt.Errorf("支付宝退款业务失败: code=%s sub_code=%s sub_msg=%s",
			code, alipayRespString(resp, "sub_code"), alipayRespString(resp, "sub_msg"))
	}
	refund.TransactionID = alipayRespString(resp, "trade_no")
	return nil
}

// alipayRespString 从通用接口的响应体中取字符串字段
// 参数：
//   - resp: 响应体反序列化结果
//   - key: 字段名
//
// 返回值：
//   - string: 字段值，缺失或类型不符时返回空串
func alipayRespString(resp map[string]interface{}, key string) string {
	v, ok := resp[key]
	if !ok {
		return ""
	}
	str, ok := v.(string)
	if !ok {
		return ""
	}
	return str
}

// parseAlipayCallback 解析支付宝支付回调参数并完成 RSA2 验签
// 验签使用数据库配置的支付宝公钥（RsaCheckV1 规则：剔除 sign/sign_type 后按 key 升序拼接验签），
// 验签不通过直接拒绝；验签后再核对 app_id、交易状态与金额，防跨应用通知重放与非成功通知误置
// 参数：
//   - params: 支付宝回调参数映射（POST form）
//
// 返回值：
//   - *payCallbackParams: 解析后的通用参数
//   - error: 错误信息
func (s *PaymentService) parseAlipayCallback(params map[string]string) (*payCallbackParams, error) {
	if len(params) == 0 {
		return nil, fmt.Errorf("回调参数为空")
	}

	cfg := s.getAlipayPayConfig()
	if cfg.AlipayPublicKey == "" {
		return nil, fmt.Errorf("支付宝公钥未配置，无法验签")
	}
	ok, err := alipaysdk.RsaCheckV1(params, cfg.AlipayPublicKey, false)
	if err != nil || !ok {
		return nil, fmt.Errorf("支付宝回调验签失败")
	}

	// 通知身份校验：公钥模式下支付宝公钥为全商户共用，验签只能证明「来自支付宝」，
	// 不能证明这条通知是发给本应用的，须核对 app_id（官方异步通知校验要求）
	if cfg.AppID == "" {
		return nil, fmt.Errorf("支付宝 AppID 未配置，无法校验通知身份")
	}
	if appID := params["app_id"]; appID != cfg.AppID {
		return nil, fmt.Errorf("支付宝通知 app_id 不匹配: %s", appID)
	}

	paymentNo := params["out_trade_no"]
	transactionID := params["trade_no"]
	if paymentNo == "" || transactionID == "" {
		return nil, fmt.Errorf("支付宝回调参数缺失")
	}

	// 交易状态校验：仅 TRADE_SUCCESS / TRADE_FINISHED 视为支付成功，
	// 其余（WAIT_BUYER_PAY / TRADE_CLOSED 等）一律拒绝，防非成功通知误置已支付
	if ts := params["trade_status"]; ts != "TRADE_SUCCESS" && ts != "TRADE_FINISHED" {
		return nil, fmt.Errorf("支付宝交易状态非成功: %s", ts)
	}

	// 回调金额（元），供 handlePaySuccess 与应付金额强制比对，缺失或非法直接拒绝
	amount, err := strconv.ParseFloat(params["total_amount"], 64)
	if err != nil || amount <= 0 {
		return nil, fmt.Errorf("支付宝回调金额缺失或非法")
	}

	return &payCallbackParams{
		PaymentNo:     paymentNo,
		TransactionID: transactionID,
		Amount:        amount,
	}, nil
}
