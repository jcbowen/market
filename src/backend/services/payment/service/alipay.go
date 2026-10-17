package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jcbowen/market/src/backend/services/payment/model"
)

// alipayParams 支付宝支付调起参数结构
type alipayParams struct {
	AppID       string `json:"app_id"`
	Method      string `json:"method"`
	Format      string `json:"format"`
	Charset     string `json:"charset"`
	SignType    string `json:"sign_type"`
	Timestamp   string `json:"timestamp"`
	Version     string `json:"version"`
	NotifyURL   string `json:"notify_url"`
	BizContent  string `json:"biz_content"`
	Sign        string `json:"sign"`
}

// buildAlipayParams 构造支付宝支付调起参数
// 当前为模拟实现，生产环境应接入支付宝官方 SDK 并完善 RSA2 签名逻辑
// 参数：
//   - ctx: 上下文
//   - p: 支付记录模型
//
// 返回值：
//   - string: 调起参数 JSON 字符串
//   - error: 错误信息
func (s *PaymentService) buildAlipayParams(ctx context.Context, p *model.Payment) (string, error) {
	// 支付前检测数据库配置版本，后台修改配置后无需重启即可生效
	s.getPayConfigCached(ctx)
	cfg := s.getAlipayPayConfig()
	if cfg.AppID == "" || cfg.PrivateKey == "" {
		return "", fmt.Errorf("支付宝支付配置缺失")
	}

	biz := map[string]interface{}{
		"out_trade_no": p.PaymentNo,
		"total_amount": fmt.Sprintf("%.2f", p.Amount),
		"subject":      "社区商超订单",
		"product_code": "QUICK_MSECURITY_PAY",
	}
	bizContent, _ := json.Marshal(biz)

	params := alipayParams{
		AppID:     cfg.AppID,
		Method:    "alipay.trade.app.pay",
		Format:    "JSON",
		Charset:   "utf-8",
		SignType:  "RSA2",
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Version:   "1.0",
		NotifyURL: cfg.NotifyURL,
		BizContent: string(bizContent),
	}
	params.Sign = alipaySign(params, cfg.PrivateKey)

	data, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// alipayRefund 调用支付宝退款接口
// 渠道未真实接入前显式报错，禁止静默成功——假成功会让上层记录「已退款」并回补库存，
// 造成资金未实际退出而账面已退的账实不符
// 参数：
//   - ctx: 上下文
//   - p: 原支付记录
//   - refund: 退款记录
//
// 返回值：
//   - error: 恒返回「渠道未接入」错误
func (s *PaymentService) alipayRefund(ctx context.Context, p *model.Payment, refund *model.RefundPayment) error {
	return fmt.Errorf("支付宝渠道退款未接入，请通过其他渠道处理退款")
}

// parseAlipayCallback 解析支付宝支付回调参数
// 当前为模拟实现，生产环境应做 RSA2 验签；交易状态与金额校验对真实渠道同样必要
// 参数：
//   - params: 支付宝回调参数映射
//
// 返回值：
//   - *payCallbackParams: 解析后的通用参数
//   - error: 错误信息
func parseAlipayCallback(params map[string]string) (*payCallbackParams, error) {
	if len(params) == 0 {
		return nil, fmt.Errorf("回调参数为空")
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

// alipaySign 生成支付宝支付签名
// 当前为模拟实现，生产环境应使用 RSA2 私钥签名
// 参数：
//   - params: 支付宝支付参数
//   - privateKey: 支付宝私钥
//
// 返回值：
//   - string: 签名字符串
func alipaySign(params alipayParams, privateKey string) string {
	values := []string{
		"app_id=" + params.AppID,
		"method=" + params.Method,
		"format=" + params.Format,
		"charset=" + params.Charset,
		"sign_type=" + params.SignType,
		"timestamp=" + params.Timestamp,
		"version=" + params.Version,
		"notify_url=" + params.NotifyURL,
		"biz_content=" + params.BizContent,
	}
	sort.Strings(values)
	str := strings.Join(values, "&") + "&key=" + privateKey
	hashed := md5.Sum([]byte(str))
	return hex.EncodeToString(hashed[:])
}
