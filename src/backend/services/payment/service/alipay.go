package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jcbowen/jcbaseGo/component/security"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/debuglog"
	"github.com/jcbowen/market/src/backend/services/payment/model"

	alipaysdk "github.com/jcbowen/alipay-sdk-go-all"
	alipaymodel "github.com/jcbowen/alipay-sdk-go-all/model"
)

// normalizeAlipayGateway 规范化支付宝网关地址
// SDK 会自行拼接 V2 的 /gateway.do 与 V3 的接口路径，故网关地址必须只保留到域名（含可选端口），
// 管理端从支付宝开放平台复制的「网关地址」通常带 /gateway.do 后缀，直接入库会导致请求路径重复
// 参数：
//   - raw: 原始网关地址，可为空
//
// 返回值：
//   - string: 仅含协议与域名的网关地址，空串表示使用生产默认网关
func normalizeAlipayGateway(raw string) string {
	gw := strings.TrimSpace(raw)
	if gw == "" {
		return ""
	}
	gw = strings.TrimRight(gw, "/")
	// 去掉 V2 网关路径后缀，保留到域名（大小写不敏感，仅处理末尾一次）
	pathLen := len(alipaysdk.V2GatewayPath)
	if len(gw) >= pathLen && strings.EqualFold(gw[len(gw)-pathLen:], alipaysdk.V2GatewayPath) {
		gw = gw[:len(gw)-pathLen]
	}
	return strings.TrimRight(gw, "/")
}

// checkAlipayKeys 离线自检支付宝密钥可用性（不联网），按加签方式分流：
//   - 应用私钥两种模式一致：走与真实下单一致的签名路径（SDK AloneRsaSign → security.RSASign）；
//   - 公钥模式：支付宝公钥走与回调验签一致的解析路径（security.ParseRSAPublicKey）；
//   - 证书模式：三证书走与执行器构建/通知验签一致的路径（CertSN/CertPublicKey/RootCertSN），
//     并校验根证书链中至少一段可计算序列号。
//
// 用于把「私钥粘错、把应用公钥错填成支付宝公钥、密钥缺字符、证书内容贴错位置」
// 这类配置错误在后台保存时就暴露出来，而不是等真实用户点支付才在签名阶段失败
// 参数：
//   - cfg: 支付宝配置载体
//
// 返回值：
//   - string: 自检失败原因，空串表示通过
func checkAlipayKeys(cfg config.AlipayConfig) string {
	if _, err := alipaysdk.AloneRsaSign("market_alipay_config_check", cfg.PrivateKey, false); err != nil {
		return "应用私钥不可用：" + err.Error()
	}
	if cfg.IsCertMode() {
		if _, err := alipaysdk.CertSN(cfg.AppCert); err != nil {
			return "应用公钥证书不可解析：" + err.Error()
		}
		if _, err := alipaysdk.CertPublicKey(cfg.AlipayPublicCert); err != nil {
			return "支付宝公钥证书不可解析：" + err.Error()
		}
		if _, err := alipaysdk.RootCertSN(cfg.AlipayRootCert); err != nil {
			return "支付宝根证书不可解析：" + err.Error()
		}
		return ""
	}
	if _, err := security.ParseRSAPublicKey([]byte(cfg.AlipayPublicKey)); err != nil {
		return "支付宝公钥不可解析：" + err.Error()
	}
	return ""
}

// AlipayConfigStatus 计算支付配置行的支付宝就绪状态与自检失败原因
// 就绪口径为「当前加签方式的必填字段齐全 且 密钥自检通过」，
// 与 C 端是否展示支付宝、下单是否可用保持一致
// 参数：
//   - cfg: 支付配置行，可为 nil
//
// 返回值：
//   - bool: 是否就绪（可安全对 C 端展示并承接下单）
//   - string: 未就绪原因，就绪时为空串
func AlipayConfigStatus(cfg *model.PayConfig) (bool, string) {
	alipay := alipayConfigOf(cfg)
	if !alipay.IsConfigured() {
		if alipayCredentialsReady(alipay) {
			return false, missingDeployBaseReason
		}
		if alipay.IsCertMode() {
			return false, "支付宝公钥证书模式必填配置不完整（AppID、应用私钥、应用公钥证书、支付宝公钥证书、支付宝根证书、异步通知地址均为必填）"
		}
		return false, "支付宝必填配置不完整（AppID、应用私钥、支付宝公钥、异步通知地址均为必填）"
	}
	if msg := checkAlipayKeys(alipay); msg != "" {
		return false, msg
	}
	return true, ""
}

// alipayHTTPTimeout 支付宝 API 请求（V3 下单/查询/退款等）的整体时限
// SDK 的 GenericExecuteApi 默认使用 http.DefaultClient（无超时），对端不响应时会永久占用
// gRPC 处理协程，最终拖垮支付服务实例；V3 接口正常在数百毫秒内返回，15 秒足以覆盖长尾
const alipayHTTPTimeout = 15 * time.Second

// buildAlipayExecutor 根据支付宝配置构建通用执行器（V2 pageExecute 与 V3 API 共享）
// 按加签方式分流：
//   - 公钥模式：AppID + 应用私钥 + 支付宝公钥；
//   - 证书模式：AppID + 应用私钥 + 应用公钥证书/支付宝公钥证书/根证书内容，
//     SDK 会据此计算 appCertSN 与 rootCertSN 并在请求中携带证书序列号，响应验签按 alipay-sn 头取缓存公钥。
//
// 配置不完整或构建失败时返回 nil，C 端渠道接口据其就绪状态隐藏支付宝选项
// 参数：
//   - cfg: 支付宝配置载体
//
// 返回值：
//   - *alipaysdk.GenericExecuteApi: 通用执行器，配置不完整时为 nil
func buildAlipayExecutor(cfg config.AlipayConfig) *alipaysdk.GenericExecuteApi {
	if !cfg.IsConfigured() {
		return nil
	}
	sdkConfig := &alipaysdk.AlipayConfig{
		// GatewayURL 入库前已由 normalizeAlipayGateway 规范化为「协议+域名」，空串即 SDK 默认生产网关
		ServerUrl:  cfg.GatewayURL,
		AppId:      cfg.AppID,
		PrivateKey: cfg.PrivateKey,
	}
	if cfg.IsCertMode() {
		sdkConfig.AppCertContent = cfg.AppCert
		sdkConfig.AlipayPublicCertContent = cfg.AlipayPublicCert
		sdkConfig.RootCertContent = cfg.AlipayRootCert
	} else {
		sdkConfig.AlipayPublicKey = cfg.AlipayPublicKey
	}
	configUtil, err := alipaysdk.NewAlipayConfigUtil(sdkConfig)
	if err != nil {
		log.Printf("构建支付宝配置工具失败: %v", err)
		return nil
	}
	executor, err := alipaysdk.NewGenericExecuteApi(configUtil)
	if err != nil {
		log.Printf("构建支付宝执行器失败: %v", err)
		return nil
	}
	// SDK 默认复用 http.DefaultClient（全局无超时），这里注入独立客户端，
	// 保证单次调用有明确上限且不影响进程内其他 HTTP 调用
	executor.SetHttpClient(&http.Client{Timeout: alipayHTTPTimeout})
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
	// 客户端与配置均按支付单绑定的配置行获取（内部含版本热刷新）
	executor := s.getAlipayExecutorFor(ctx, p.ConfigID)
	if executor == nil {
		return "", fmt.Errorf("支付宝支付未配置或配置不完整")
	}
	cfg := s.getAlipayConfigFor(ctx, p.ConfigID)

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
		// 回跳地址带支付单号：落地页是免登录结果页，凭支付单号轮询公开状态接口展示结果。
		// 单号拼在 hash 路由的 query 上（uni-app H5 为 hash 路由），
		// 支付宝回跳时会在整串末尾追加 &out_trade_no=... 等参数，同样落在 hash query 内，两者取其一即可
		returnURL := appendQueryParam(cfg.ReturnURL, "no", p.PaymentNo)
		bizParams["return_url"] = returnURL
		// quit_url：用户在支付宝收银台中途退出后回跳的商户地址，与支付完成回跳同域
		biz["quit_url"] = returnURL
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

// buildAlipayMiniParams 构造支付宝小程序支付（alipay.trade.create）调起参数
// 小程序内无法唤起网页收银台，改用 V3 trade.create 预下单：
// 服务端创建渠道交易拿到 trade_no，C 端凭 my.tradePay({ tradeNO }) 唤起支付宝收银台；
// 异步通知地址经 V3 请求头 alipay-notify-url 传入，与手机网站支付共用同一通知链路
// 参数：
//   - ctx: 上下文
//   - p: 支付记录模型
//
// 返回值：
//   - string: 调起参数 JSON 字符串（{"trade_no": "..."}）
//   - error: 错误信息
func (s *PaymentService) buildAlipayMiniParams(ctx context.Context, p *model.Payment) (string, error) {
	executor := s.getAlipayExecutorFor(ctx, p.ConfigID)
	if executor == nil {
		return "", fmt.Errorf("支付宝支付未配置或配置不完整")
	}
	cfg := s.getAlipayConfigFor(ctx, p.ConfigID)

	biz := &alipaymodel.AlipayTradeCreateModel{
		OutTradeNo:     alipaysdk.Str(p.PaymentNo),
		TotalAmount:    alipaysdk.Str(fmt.Sprintf("%.2f", p.Amount)),
		Subject:        alipaysdk.Str("社区商超订单"),
		TimeoutExpress: alipaysdk.Str(s.alipayTimeoutExpress()),
	}
	req := &alipaysdk.OpenApiGenericRequest{BodyParams: biz}
	if cfg.NotifyURL != "" {
		// V3 异步通知地址走请求头：与手机网站支付共用同一通知链路（验签、幂等、终态兑底均同口径）
		req.HeaderParams = map[string]string{"alipay-notify-url": cfg.NotifyURL}
	}
	resp, apiErr := executor.Execute("/v3/alipay/trade/create", "POST", req)
	if apiErr != nil {
		return "", fmt.Errorf("支付宝预下单失败: %s", apiErr.Error())
	}
	if failed, code, subCode, subMsg := alipayV3BizError(resp); failed {
		return "", fmt.Errorf("支付宝预下单业务失败: code=%s sub_code=%s sub_msg=%s", code, subCode, subMsg)
	}
	tradeNo := alipayRespString(resp, "trade_no")
	if tradeNo == "" {
		return "", fmt.Errorf("支付宝预下单未返回 trade_no")
	}

	data, err := json.Marshal(map[string]string{"trade_no": tradeNo})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// appendQueryParam 在 URL 上追加一个 query 参数（已存在同名参数时不重复追加）
// 参数：
//   - raw: 原始 URL，可为空
//   - key: 参数名
//   - value: 参数值（内部做 URL 编码）
//
// 返回值：
//   - string: 追加后的 URL，raw 为空时返回空串
func appendQueryParam(raw, key, value string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + url.QueryEscape(key) + "=" + url.QueryEscape(value)
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
// 走通用 Execute 而非 typed Refund：SDK 对齐 PHP 行为在 HTTP 2xx 时不判业务码；
// 退款是资金写操作，必须自行确认业务结果——经 alipayV3BizError 判定
// （V3 正常响应体无 code 字段，直接视为成功；网关异常以 200 返回错误体时取 code 拒绝），
// 否则正常退款会被误记「退款失败」并反复重试
// 参数：
//   - ctx: 上下文
//   - p: 原支付记录
//   - refund: 退款记录
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) alipayRefund(ctx context.Context, p *model.Payment, refund *model.RefundPayment) error {
	executor := s.getAlipayExecutorFor(ctx, p.ConfigID)
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
	if failed, code, subCode, subMsg := alipayV3BizError(resp); failed {
		return fmt.Errorf("支付宝退款业务失败: code=%s sub_code=%s sub_msg=%s", code, subCode, subMsg)
	}
	refund.TransactionID = alipayRespString(resp, "trade_no")
	return nil
}

// alipayRespAmount 从通用接口的响应体中取金额字段（元）
// V3 金额按字符串下发（"0.01"），字段缺失或解析失败返回 0，由调用方回退到本地应付额
// 参数：
//   - resp: 响应体反序列化结果
//   - key: 金额字段名（如 total_amount）
//
// 返回值：
//   - float64: 金额（元），取不到时为 0
func alipayRespAmount(resp map[string]interface{}, key string) float64 {
	amount, _ := strconv.ParseFloat(alipayRespString(resp, key), 64)
	return amount
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

// alipayV3BizError 判定支付宝 V3 2xx 响应体是否为业务失败
// V3 正常业务响应体不含 code 字段（业务结果直接平铺，如 trade_status），
// 仅网关异常场景可能以 HTTP 200 返回带 code 的错误体；SDK 对齐 PHP 行为
// 在 2xx 时不判 code，由调用方自行判定。此前各调用点用 IsBizSuccess(resp["code"])
// 判定，正常响应因 code 缺失恒被判为业务失败——主动查单查到 TRADE_SUCCESS 仍按
// 「渠道查询失败」静默跳过（对账捞回机制失效）、退款成功被记失败，
// 2026-10-22 线上故障即由此导致
// 参数：
//   - resp: Execute 返回的响应体
//
// 返回值：
//   - bool: 业务失败返回 true；正常业务响应（无 code 字段）或 code=10000 返回 false
//   - string: code，响应体携带时返回
//   - string: sub_code
//   - string: sub_msg
func alipayV3BizError(resp map[string]interface{}) (bool, string, string, string) {
	code := alipayRespString(resp, "code")
	if code == "" {
		return false, "", "", ""
	}
	if alipaysdk.IsBizSuccess(code) {
		return false, code, "", ""
	}
	return true, code, alipayRespString(resp, "sub_code"), alipayRespString(resp, "sub_msg")
}

// alipayV3ApiErrBody 解析 V3 非 2xx 异常的响应体（错误码与业务结果都在 body 中，
// 如 ACQ.TRADE_NOT_EXIST），供调用方区分「渠道明确回答」与「网络/服务异常」
// 参数：
//   - apiErr: Execute 返回的异常
//
// 返回值：
//   - map[string]interface{}: 响应体反序列化结果，body 为空或非法 JSON 时返回 nil
func alipayV3ApiErrBody(apiErr *alipaysdk.ApiException) map[string]interface{} {
	if apiErr == nil || apiErr.ResponseBody == "" {
		return nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(apiErr.ResponseBody), &obj); err != nil {
		return nil
	}
	return obj
}

// parseAlipayCallback 解析支付宝支付回调参数并完成 RSA2 验签
// 多配置下按通知明文 app_id 路由到对应配置行（app_id 是通知必带字段），
// 再用该配置的支付宝公钥/证书验签与归属校验
// 验签按加签方式分流（RsaCheckV1 规则：剔除 sign/sign_type 后按 key 升序拼接验签），
//   - 公钥模式：使用配置的支付宝公钥；
//   - 证书模式：通知带 alipay_cert_sn 时按序列号在「支付宝公钥证书 + 根证书链」严格定位公钥，
//     定位不到（证书过期/未同步更新）即拒绝；通知未带该字段时（官方从未承诺通知侧携带，
//     SDK v0.0.6 起）直接用本商户支付宝公钥证书里的公钥验签，信任锚仍是本地那份证书，
//     签名不出自该证书私钥照样拒绝，不存在放行路径。
//
// 验签不通过直接拒绝；验签后再核对 app_id、交易状态与金额，防跨应用通知重放与非成功通知误置
// 参数：
//   - ctx: 上下文（按 app_id 定位配置行用）
//   - params: 支付宝回调参数映射（POST form）
//
// 返回值：
//   - *payCallbackParams: 解析后的通用参数
//   - error: 错误信息
func (s *PaymentService) parseAlipayCallback(ctx context.Context, params map[string]string) (*payCallbackParams, error) {
	if len(params) == 0 {
		return nil, fmt.Errorf("回调参数为空")
	}

	// 按 app_id 路由配置行：通知必带 app_id，多配置下以它定位验签公钥与归属校验基准
	cfg, matched := s.alipayConfigByAppID(ctx, params["app_id"])
	if !matched {
		return nil, fmt.Errorf("支付宝通知 app_id %q 未匹配任何支付配置", params["app_id"])
	}
	var ok bool
	var err error
	if cfg.IsCertMode() {
		ok, err = alipaysdk.RsaCheckV1WithCert(params, cfg.AlipayPublicCert, cfg.AlipayRootCert)
	} else {
		if cfg.AlipayPublicKey == "" {
			return nil, fmt.Errorf("支付宝公钥未配置，无法验签")
		}
		ok, err = alipaysdk.RsaCheckV1(params, cfg.AlipayPublicKey, false)
	}
	if err != nil || !ok {
		// 只记参数 key 列表不记值（sign/金额等值不入标准日志），
		// 用于区分「签名与本证书不同源」（err 为 nil、ok 为 false）与 SDK 明确报错（缺 sign、
		// 序列号不匹配、证书不可解析）；缺 alipay_cert_sn 本身不再是失败原因
		keys := make([]string, 0, len(params))
		for k := range params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		log.Printf("支付宝回调验签失败: %v 通知参数字段=%v", err, keys)
		s.recordAlipayVerifyEvidence(params, cfg, err)
		return nil, fmt.Errorf("支付宝回调验签失败")
	}

	// 通知身份校验：支付宝公钥为全商户共用，验签只能证明「来自支付宝」，
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
	// 用哨兵错误包装：这类是渠道的正常状态推送，流水记「幂等忽略」而非「验签失败」
	if ts := params["trade_status"]; ts != "TRADE_SUCCESS" && ts != "TRADE_FINISHED" {
		return nil, fmt.Errorf("%w: 支付宝交易状态 %s", errNotSuccessNotification, ts)
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

// alipayVerifyQuotaKey 验签取证进程记录的封顶键
// 取全局固定值而非支付单号/IP：伪造回调会带随机单号与分散 IP，
// 按单号或 IP 封顶等于不封顶，固定键把该类型记录总量钉在每分钟 QuotaPerMinute 条
const alipayVerifyQuotaKey = "payment_verify_alipay"

// fp12 取字符串 sha256 十六进制的前 12 位，用于比对「两侧看到的是否同一份内容」
// 只留前 12 位：取证只需要「相同/不同」的判据，完整摘要会让调试页与流水原文一样可被拿去做离线碰撞比对
// 参数：
//   - value: 待取指纹的字符串，空串同样有确定值
//
// 返回值：
//   - string: 12 位十六进制指纹
func fp12(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

// recordAlipayVerifyEvidence 把一次验签失败的取证要素记入 debugger 进程级日志
// 与网关「支付宝回调」记录（失败时含全量通知原文与 sign）按 out_trade_no 对齐，两条一比即可定性：
//   - notify_cert_sn 与 local_cert_sn 不等 / 通知侧为空 → 支付宝不是用我们这张证书签的通知（加签载体不一致）；
//   - content_fp 与用网关原文复算的串不等 → 字段在解析或拼接环节丢失（商户侧问题）；
//   - 两者都对得上、sign_fp 却验不过 → 验签公钥与签名私钥不同源（证书该更新了）。
//
// 只落指纹不落原文：原文已由网关记录覆盖，重复落一份只是放大隐私面。
// debugger 未启用（实例为 nil）或命中封顶时静默跳过，标准日志仍照常输出 key 列表。
// 参数：
//   - params: 通知参数（未经改写的原始 form 参数）
//   - cfg: 当前支付宝配置（决定取证书公钥还是配置公钥）
//   - verifyErr: SDK 返回的验签错误，nil 表示「无异常但签名与原文不匹配」
func (s *PaymentService) recordAlipayVerifyEvidence(params map[string]string, cfg config.AlipayConfig, verifyErr error) {
	rec := debuglog.StartQuoted("支付宝回调验签", "payment_verify_alipay", alipayVerifyQuotaKey)
	if rec == nil {
		return
	}

	// 待验签串按 SDK 同规则复算（剔除 sign/sign_type 后按 key 升序拼接非空值），
	// 长度与指纹即可判断商户侧拼出的串与支付宝签名时用的串是否同一份
	contentParams := make(map[string]string, len(params))
	for k, v := range params {
		contentParams[k] = v
	}
	delete(contentParams, "sign")
	delete(contentParams, "sign_type")
	content := alipaysdk.GetSignContentForNotify(contentParams)

	signature := params["sign"]
	reason := "签名与待验签串不匹配"
	if verifyErr != nil {
		reason = verifyErr.Error()
	}
	fields := map[string]interface{}{
		"out_trade_no":   params["out_trade_no"],
		"notify_id":      params["notify_id"],
		"verify_error":   reason,
		"param_count":    len(params),
		"content_length": len(content),
		"content_fp":     fp12(content),
		"sign_length":    len(signature),
		"sign_fp":        fp12(signature),
	}

	if cfg.IsCertMode() {
		// 证书模式：验签公钥取自支付宝公钥证书，序列号命中关系直接给出，
		// 不必让排查人再去两处抄 SN 手工比对
		fields["alipay_mode"] = config.AlipayModeCert
		fields["notify_cert_sn"] = strings.TrimSpace(params["alipay_cert_sn"])
		localSN, err := alipaysdk.CertSN(cfg.AlipayPublicCert)
		if err != nil {
			fields["local_cert_sn"] = "不可解析: " + err.Error()
		} else {
			fields["local_cert_sn"] = localSN
			fields["cert_sn_matched"] = localSN == strings.TrimSpace(params["alipay_cert_sn"])
		}
		if pubKey, err := alipaysdk.CertPublicKey(cfg.AlipayPublicCert); err != nil {
			fields["verify_key_fp"] = "不可解析: " + err.Error()
		} else {
			fields["verify_key_fp"] = fp12(pubKey)
		}
	} else {
		fields["alipay_mode"] = config.AlipayModePublic
		fields["verify_key_fp"] = fp12(cfg.AlipayPublicKey)
	}

	rec.Error("支付宝回调验签失败", fields)
	_ = rec.EndProcess(debuglog.StatusFailed)
}
