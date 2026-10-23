package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/grpc/peer"

	"github.com/jcbowen/market/src/backend/services/payment/model"
	wegopayment "github.com/jcbowen/wego/payment"
)

// wechatPayParams 微信支付 JSAPI 调起参数结构
// 前端使用此对象调起微信支付 SDK
type wechatPayParams struct {
	AppID     string `json:"appId"`
	TimeStamp string `json:"timeStamp"`
	NonceStr  string `json:"nonceStr"`
	Package   string `json:"package"`
	SignType  string `json:"signType"`
	PaySign   string `json:"paySign"`
}

// buildWechatPayParams 构造微信支付调起参数
// 根据 openid 是否为空自动选择 JSAPI 或 H5 支付方式；
// appID 由网关按客户端渠道解析后传入（小程序=小程序 AppID，H5=商户绑定任一 AppID），
// 为空时回落支付配置默认 AppID（wego 客户端请求级覆盖）
// 参数：
//   - ctx: 上下文
//   - p: 支付记录模型
//   - openid: 用户微信 openid，为空则走 H5 支付
//   - appID: 本次下单使用的 AppID，可为空
//
// 返回值：
//   - string: 调起参数 JSON 字符串（JSAPI）或包含 h5_url 的 JSON（H5）
//   - error: 错误信息
func (s *PaymentService) buildWechatPayParams(ctx context.Context, p *model.Payment, openid, appID string) (string, error) {
	// 支付前检测数据库配置版本，后台修改配置后无需重启即可生效
	s.getPayConfigCached(ctx)
	client := s.getWechatPayClient()
	if client == nil {
		return "", fmt.Errorf("微信支付未配置或配置不完整")
	}
	cfg := s.getWechatPayConfig()
	// 请求级 AppID 优先，为空回落配置默认值；JSAPI 签名必须使用与 openid 同源的 AppID
	if appID == "" {
		appID = cfg.AppID
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
			AppID:       appID,
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

		params := wechatPayParams{
			AppID:     appID,
			TimeStamp: fmt.Sprintf("%d", time.Now().Unix()),
			NonceStr:  randomNonceStr(32),
			Package:   "prepay_id=" + resp.PrepayID,
			SignType:  "RSA",
		}
		paySign, err := signWechatPayParams(params, cfg.PrivateKey)
		if err != nil {
			return "", fmt.Errorf("生成支付签名失败: %w", err)
		}
		params.PaySign = paySign

		data, err := json.Marshal(params)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	resp, err := client.H5Pay(ctx, &wegopayment.H5Request{
		AppID:       appID,
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
var errRefundAccepted = errors.New("退款已受理，等待渠道终态")

// wechatRefund 调用微信退款接口
// P0-2：按响应 Status 分流——仅渠道明确 SUCCESS 才视为退款成功；
// PROCESSING（受理常态）等非终态保持待退款(1)，返回 errRefundAccepted 由调用方分流
// 参数：
//   - ctx: 上下文
//   - p: 原支付记录
//   - refund: 退款记录
//
// 返回值：
//   - error: 渠道调用失败返回错误；受理未到终态返回 errRefundAccepted
func (s *PaymentService) wechatRefund(ctx context.Context, p *model.Payment, refund *model.RefundPayment) error {
	client := s.getWechatPayClient()
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
	if resp.Status != "" && resp.Status != "SUCCESS" {
		log.Printf("[ProcessRefund] 微信退款已受理未到终态 refund_no=%s refund_id=%s status=%s，保持待退款由查单任务收敛",
			refund.RefundNo, resp.RefundID, resp.Status)
		return errRefundAccepted
	}
	return nil
}

// parseWechatCallback 解析微信支付回调报文
// 使用 wego 验证通知签名并解密资源，然后提取支付单号与微信订单号
// 参数：
//   - s: 支付服务实例，用于获取 wego 客户端
//   - body: 回调请求体
//   - headers: 回调请求头
//
// 返回值：
//   - *payCallbackParams: 解析后的通用参数
//   - error: 错误信息
func parseWechatCallback(s *PaymentService, body []byte, headers http.Header) (*payCallbackParams, error) {
	client := s.getWechatPayClient()
	if client == nil {
		return nil, fmt.Errorf("微信支付未配置或配置不完整")
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("回调体为空")
	}

	notification, plainBytes, err := client.VerifyNotification(context.Background(), headers, body)
	if err != nil {
		return nil, fmt.Errorf("微信支付回调验签解密失败: %w", err)
	}

	// 仅处理支付成功通知，避免退款等其他通知被误处理为支付成功
	// notification 可能为 nil（VerifyNotification 对未知资源类型返回空指针），须先判空再取字段
	if notification == nil {
		return nil, fmt.Errorf("%w: 通知内容为空", errNotSuccessNotification)
	}
	if notification.EventType != "TRANSACTION.SUCCESS" {
		return nil, fmt.Errorf("%w: %s", errNotSuccessNotification, notification.EventType)
	}

	var result struct {
		OutTradeNo    string `json:"out_trade_no"`
		TransactionID string `json:"transaction_id"`
		Amount        struct {
			Total int `json:"total"`
		} `json:"amount"`
	}
	if err := json.Unmarshal(plainBytes, &result); err != nil {
		return nil, fmt.Errorf("微信支付回调报文解析失败: %w", err)
	}

	if result.OutTradeNo == "" || result.TransactionID == "" {
		return nil, fmt.Errorf("微信支付回调参数缺失")
	}

	return &payCallbackParams{
		PaymentNo:     result.OutTradeNo,
		TransactionID: result.TransactionID,
		Amount:        float64(result.Amount.Total) / 100,
	}, nil
}

// signWechatPayParams 生成微信 JSAPI 调起支付 RSA 签名
// 签名串格式：appId\ntimeStamp\nnonceStr\npackage\n
// 参数：
//   - params: 前端调起参数
//   - privateKeyPEM: 微信支付商户私钥 PEM
//
// 返回值：
//   - string: Base64 编码的签名
//   - error: 签名失败时返回错误
func signWechatPayParams(params wechatPayParams, privateKeyPEM string) (string, error) {
	message := params.AppID + "\n" + params.TimeStamp + "\n" + params.NonceStr + "\n" + params.Package + "\n"
	hash := sha256.Sum256([]byte(message))

	privateKey, err := parseRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return "", err
	}

	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("RSA 签名失败: %w", err)
	}

	return base64.StdEncoding.EncodeToString(signature), nil
}

// parseRSAPrivateKey 解析 RSA 私钥 PEM
// 支持 PKCS#1 与 PKCS#8 格式
// 参数：
//   - privateKeyPEM: PEM 格式私钥字符串
//
// 返回值：
//   - *rsa.PrivateKey: 解析后的私钥
//   - error: 解析失败时返回错误
func parseRSAPrivateKey(privateKeyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("无法解析私钥 PEM")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("解析私钥失败: %w", err)
		}
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("私钥类型不是 RSA")
	}

	return rsaKey, nil
}

// randomNonceStr 生成指定长度的随机字符串
// 参数：
//   - length: 字符串长度
//
// 返回值：
//   - string: 随机字符串
func randomNonceStr(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		randInt, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[randInt.Int64()]
	}
	return string(b)
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
