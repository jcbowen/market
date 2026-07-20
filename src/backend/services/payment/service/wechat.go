package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jcbowen/market/src/backend/services/payment/model"
)

// wechatPayParams 微信支付调起参数结构
type wechatPayParams struct {
	AppID     string `json:"appId"`
	TimeStamp string `json:"timeStamp"`
	NonceStr  string `json:"nonceStr"`
	Package   string `json:"package"`
	SignType  string `json:"signType"`
	PaySign   string `json:"paySign"`
}

// buildWechatPayParams 构造微信支付调起参数
// 当前为模拟实现，生产环境应接入微信官方 SDK 并完善签名逻辑
// 参数：
//   - ctx: 上下文
//   - p: 支付记录模型
//   - openid: 用户微信 openid
//
// 返回值：
//   - string: 调起参数 JSON 字符串
//   - error: 错误信息
func (s *PaymentService) buildWechatPayParams(ctx context.Context, p *model.Payment, openid string) (string, error) {
	cfg := s.cfg.Wechat
	if cfg.AppID == "" || cfg.MchID == "" {
		return "", fmt.Errorf("微信支付配置缺失")
	}

	params := wechatPayParams{
		AppID:     cfg.AppID,
		TimeStamp: fmt.Sprintf("%d", time.Now().Unix()),
		NonceStr:  randomNonceStr(16),
		Package:   "prepay_id=mock_" + p.PaymentNo,
		SignType:  "MD5",
		PaySign:   "",
	}

	params.PaySign = wechatSign(params, cfg.APIKey)

	data, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// wechatRefund 调用微信退款接口
// 当前为模拟实现，生产环境应使用微信官方证书及 API 发起退款请求
// 参数：
//   - ctx: 上下文
//   - p: 原支付记录
//   - refund: 退款记录
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) wechatRefund(ctx context.Context, p *model.Payment, refund *model.RefundPayment) error {
	cfg := s.cfg.Wechat
	if cfg.AppID == "" || cfg.MchID == "" {
		return fmt.Errorf("微信支付配置缺失")
	}
	// 模拟调用成功，实际应发起 HTTPS 请求并校验返回
	refund.TransactionID = "WX_REFUND_" + refund.RefundNo
	return nil
}

// parseWechatCallback 解析微信支付回调报文
// 当前为模拟实现，按 JSON 格式解析并提取支付单号与第三方交易号
// 参数：
//   - body: 回调请求体
//
// 返回值：
//   - *payCallbackParams: 解析后的通用参数
//   - error: 错误信息
func parseWechatCallback(body []byte) (*payCallbackParams, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("回调体为空")
	}

	var result struct {
		PaymentNo     string  `json:"payment_no"`
		TransactionID string  `json:"transaction_id"`
		Amount        float64 `json:"amount"`
	}

	// 生产环境应解析微信加密的 JSON 报文，并做验签
	if err := json.Unmarshal(body, &result); err != nil {
		// 若解析失败，尝试从 body 中按固定格式提取
		result.PaymentNo = extractValue(string(body), "payment_no")
		result.TransactionID = extractValue(string(body), "transaction_id")
	}

	if result.PaymentNo == "" || result.TransactionID == "" {
		return nil, fmt.Errorf("微信支付回调参数缺失")
	}
	return &payCallbackParams{
		PaymentNo:     result.PaymentNo,
		TransactionID: result.TransactionID,
		Amount:        result.Amount,
	}, nil
}

// wechatSign 生成微信支付 MD5 签名
// 参数：
//   - params: 支付调起参数
//   - apiKey: 微信 API 密钥
//
// 返回值：
//   - string: 签名字符串
func wechatSign(params wechatPayParams, apiKey string) string {
	values := []string{
		"appId=" + params.AppID,
		"timeStamp=" + params.TimeStamp,
		"nonceStr=" + params.NonceStr,
		"package=" + params.Package,
		"signType=" + params.SignType,
	}
	str := strings.Join(values, "&") + "&key=" + apiKey
	hashed := md5.Sum([]byte(str))
	return strings.ToUpper(hex.EncodeToString(hashed[:]))
}

// randomNonceStr 生成指定长度的随机字符串
// 参数：
//   - length: 字符串长度
//
// 返回值：
//   - string: 随机字符串
func randomNonceStr(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}
