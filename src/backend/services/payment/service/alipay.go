package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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
	cfg := s.cfg.Alipay
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
// 当前为模拟实现，生产环境应使用支付宝官方 SDK 发起退款请求
// 参数：
//   - ctx: 上下文
//   - p: 原支付记录
//   - refund: 退款记录
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) alipayRefund(ctx context.Context, p *model.Payment, refund *model.RefundPayment) error {
	cfg := s.cfg.Alipay
	if cfg.AppID == "" || cfg.PrivateKey == "" {
		return fmt.Errorf("支付宝支付配置缺失")
	}
	// 模拟调用成功，实际应发起 SDK 调用并校验返回
	refund.TransactionID = "ALI_REFUND_" + refund.RefundNo
	return nil
}

// parseAlipayCallback 解析支付宝支付回调参数
// 当前为模拟实现，生产环境应做 RSA2 验签
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

	return &payCallbackParams{
		PaymentNo:     paymentNo,
		TransactionID: transactionID,
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

// extractValue 从键值对字符串中提取指定 key 的值
// 参数：
//   - source: 源字符串
//   - key: 目标键
//
// 返回值：
//   - string: 提取到的值
func extractValue(source, key string) string {
	prefix := key + "="
	idx := strings.Index(source, prefix)
	if idx < 0 {
		return ""
	}
	end := strings.Index(source[idx+len(prefix):], "&")
	if end < 0 {
		return source[idx+len(prefix):]
	}
	return source[idx+len(prefix) : idx+len(prefix)+end]
}
