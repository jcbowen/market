package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/model"
	wegopayment "github.com/jcbowen/wego/payment"

	"google.golang.org/grpc/codes"

	alipaysdk "github.com/jcbowen/alipay-sdk-go-all"
)

// payCertMaxSize 证书文件大小上限（64KB，PEM 文件通常不超过 10KB）
const payCertMaxSize = 64 * 1024

// payCertAllowedExts 证书文件允许的扩展名
var payCertAllowedExts = map[string]bool{".pem": true, ".key": true, ".txt": true, ".crt": true}

// payConfigID 支付配置表固定单行主键
const payConfigID = 1

// checkNotifyURL 校验异步通知地址格式：允许空（未配置，就绪判定自然为未就绪），
// 非空时必须是带域名的 http(s) 绝对地址。
// 通知地址写错（漏协议、粘贴成相对路径、带空格换行）时，支付宝/微信会持续重试回调但永远打不到网关，
// 表现为「用户已付款但订单未支付」，属于只能在保存时拦住的配置错误，故在服务端兜底校验而不只依赖前端表单
//
// 参数：
//   - label: 字段中文名，用于错误提示
//   - raw: 通知地址
//
// 返回值：
//   - error: 格式非法时返回业务错误（InvalidArgument），合法返回 nil
func checkNotifyURL(label, raw string) error {
	u := strings.TrimSpace(raw)
	if u == "" {
		return nil
	}
	// 粘贴带来的空格/换行会让支付宝回调打到错误路径，url.Parse 不报错但请求必然 404，须在此拦住
	if strings.ContainsAny(u, " \t\r\n") {
		return bizerr.New(codes.InvalidArgument, fmt.Sprintf("%s不能包含空格或换行", label))
	}
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return bizerr.New(codes.InvalidArgument, fmt.Sprintf("%s必须是 http(s) 开头的完整地址", label))
	}
	return nil
}

// EnsureDefaultPayConfig 确保支付配置表存在默认配置行
// 表为空时写入一份默认记录（渠道仅现金，其余字段为空待后台配置）
//
// 返回值：
//   - error: 初始化失败时返回错误
func (s *PaymentService) EnsureDefaultPayConfig() error {
	var count int64
	if err := s.db.GetDb().Model(&model.PayConfig{}).Count(&count).Error; err != nil {
		return fmt.Errorf("查询支付配置表失败: %w", err)
	}
	if count > 0 {
		return nil
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	row := &model.PayConfig{
		ID:              payConfigID,
		EnabledChannels: `["cash"]`,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.db.GetDb().Create(row).Error; err != nil {
		return fmt.Errorf("初始化默认支付配置失败: %w", err)
	}
	log.Printf("已初始化默认支付配置（pay_config id=%d）", payConfigID)
	return nil
}

// GetPayConfig 查询支付配置
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - *model.PayConfig: 支付配置行
//   - error: 查询失败时返回错误
func (s *PaymentService) GetPayConfig(ctx context.Context) (*model.PayConfig, error) {
	var row model.PayConfig
	if err := s.db.GetDb().WithContext(ctx).First(&row, payConfigID).Error; err != nil {
		return nil, fmt.Errorf("读取支付配置失败: %w", err)
	}
	return &row, nil
}

// SavePayConfig 保存支付配置
// 保存后更新 updated_at 作为版本号，各 payment 服务实例在下一次支付链路调用时自动检测并热更新
// 参数：
//   - ctx: 上下文
//   - cfg: proto 支付配置
//
// 返回值：
//   - error: 保存失败或配置版本冲突时返回错误
func (s *PaymentService) SavePayConfig(ctx context.Context, cfg *payment.PayConfig) error {
	row, err := s.GetPayConfig(ctx)
	if err != nil {
		return err
	}

	// 乐观锁：调用方携带读取时的版本号（updated_at），与库中不一致说明期间已被他人改写，
	// 拒绝整行覆写（支付配置为单行全量覆盖式保存，后提交者会静默丢掉先提交者的改动）；
	// 版本号为空表示调用方未参与并发写入（如旧版客户端），不校验。
	// 用 Aborted：网关据此归入业务错误码 10007（数据冲突）并原样透传提示到管理端界面
	if cfg.UpdatedAt != "" && cfg.UpdatedAt != row.UpdatedAt {
		log.Printf("支付配置保存冲突：调用方版本 %s，当前版本 %s", cfg.UpdatedAt, row.UpdatedAt)
		return bizerr.New(codes.Aborted, "支付配置已被其他操作更新，请刷新页面获取最新配置后重新提交")
	}

	// 通知地址格式服务端兜底（详见 checkNotifyURL），拒绝后不写入、不触发热更新
	if err := checkNotifyURL("微信异步通知地址", cfg.WechatNotifyUrl); err != nil {
		return err
	}
	if err := checkNotifyURL("支付宝异步通知地址", cfg.AlipayNotifyUrl); err != nil {
		return err
	}

	channels, err := json.Marshal(cfg.EnabledChannels)
	if err != nil {
		return fmt.Errorf("解析支付渠道列表失败: %w", err)
	}
	wechatChannels, err := json.Marshal(cfg.WechatClientChannels)
	if err != nil {
		return fmt.Errorf("解析微信支付适用客户端渠道失败: %w", err)
	}
	alipayChannels, err := json.Marshal(cfg.AlipayClientChannels)
	if err != nil {
		return fmt.Errorf("解析支付宝适用客户端渠道失败: %w", err)
	}

	row.EnabledChannels = string(channels)
	row.WechatClientChannels = string(wechatChannels)
	row.AlipayClientChannels = string(alipayChannels)
	row.WechatAppID = cfg.WechatAppId
	// AppSecret 为支付链路无关的历史字段，且网关 GET 已脱敏不回传明文：
	// 空值保留原值，避免前端回传空串时误清空
	if cfg.WechatAppSecret != "" {
		row.WechatAppSecret = cfg.WechatAppSecret
	}
	row.WechatMchID = cfg.WechatMchId
	// V3 密钥网关 GET 已脱敏不回传明文：空值保留原值，避免前端回传空串时误清空
	// （与 AppSecret/私钥同口径；需要更换密钥时提交新值即可覆盖）
	if cfg.WechatApiV3Key != "" {
		row.WechatAPIV3Key = cfg.WechatApiV3Key
	}
	row.WechatCertSerialNo = cfg.WechatCertSerialNo
	// 私钥为空时保留原值，避免前端未重新上传证书时清空已上传内容
	if cfg.WechatPrivateKey != "" {
		row.WechatPrivateKey = cfg.WechatPrivateKey
	}
	row.WechatNotifyURL = cfg.WechatNotifyUrl
	row.AlipayAppID = cfg.AlipayAppId
	if cfg.AlipayPrivateKey != "" {
		row.AlipayPrivateKey = cfg.AlipayPrivateKey
	}
	row.AlipayPublicKey = cfg.AlipayPublicKey
	row.AlipayNotifyURL = cfg.AlipayNotifyUrl
	row.AlipayReturnURL = cfg.AlipayReturnUrl
	row.AlipayGatewayURL = normalizeAlipayGateway(cfg.AlipayGatewayUrl)
	// 加签方式：空串按公钥模式落库（存量口径），cert 模式下三证书仅由上传接口维护、此处不触碰；
	// 支付宝公钥属公钥模式载体，证书模式下残留值不参与必填判定（见 AlipayConfig.IsConfigured），无需清空
	if cfg.AlipayMode == config.AlipayModeCert {
		row.AlipayMode = config.AlipayModeCert
	} else {
		row.AlipayMode = config.AlipayModePublic
	}
	row.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")

	if err := s.db.GetDb().WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("保存支付配置失败: %w", err)
	}

	// 本实例立即刷新，其余实例在下次支付调用时按版本号自动刷新
	s.payMu.Lock()
	defer s.payMu.Unlock()
	s.applyPayConfig(row)
	return nil
}

// checkAlipayUploadContent 校验上传到支付宝证书字段的文件内容是否可解析
// 按 cert_type 使用与真实链路一致的解析路径（私钥签名自检 / 证书序列号计算 / 证书公钥提取），
// 上传即拦截「把根证书传到应用证书位置」「私钥缺字符」这类只有等到下单或回调才暴露的错误，
// 且校验失败时不写库，原有配置保持不变
//
// 参数：
//   - certType: 证书类型（private_key、app_cert、alipay_public_cert、root_cert）
//   - content: 文件内容
//
// 返回值：
//   - string: 校验失败原因，通过时为空串
func checkAlipayUploadContent(certType, content string) string {
	switch certType {
	case "private_key":
		if _, err := alipaysdk.AloneRsaSign("market_alipay_config_check", content, false); err != nil {
			return "应用私钥不可用：" + err.Error()
		}
	case "app_cert":
		if _, err := alipaysdk.CertSN(content); err != nil {
			return "应用公钥证书不可解析：" + err.Error()
		}
	case "alipay_public_cert":
		if _, err := alipaysdk.CertPublicKey(content); err != nil {
			return "支付宝公钥证书不可解析：" + err.Error()
		}
	case "root_cert":
		if _, err := alipaysdk.RootCertSN(content); err != nil {
			return "支付宝根证书不可解析：" + err.Error()
		}
	default:
		// 未知类型必须拒绝而非放行：返回空串会被调用方当作校验通过，
		// 与 UploadPayCert 写库 switch 的 default 拒绝口径保持一致
		return "不支持的证书类型: " + certType
	}
	return ""
}

// UploadPayCert 上传支付证书文件
// 校验扩展名与大小后，将文件内容写入对应渠道的证书字段
// 参数：
//   - ctx: 上下文
//   - req: 上传请求
//
// 返回值：
//   - error: 校验或保存失败时返回错误
func (s *PaymentService) UploadPayCert(ctx context.Context, req *payment.UploadPayCertReq) error {
	if req.Channel != "wechat" && req.Channel != "alipay" {
		return fmt.Errorf("不支持的支付渠道: %s", req.Channel)
	}
	if len(req.Content) == 0 {
		return fmt.Errorf("证书文件内容为空")
	}
	if len(req.Content) > payCertMaxSize {
		return fmt.Errorf("证书文件超过大小限制（最大 %dKB）", payCertMaxSize/1024)
	}
	ext := strings.ToLower(req.Filename)
	if idx := strings.LastIndex(ext, "."); idx >= 0 {
		ext = ext[idx:]
	} else {
		ext = ""
	}
	if !payCertAllowedExts[ext] {
		return fmt.Errorf("仅支持上传 %v 格式的证书文件", allowedExtList())
	}
	// 支付宝证书内容可解析性校验：FailedPrecondition 使网关原样透传具体原因，便于定位传错的文件
	if req.Channel == "alipay" {
		if msg := checkAlipayUploadContent(req.CertType, string(req.Content)); msg != "" {
			return bizerr.New(codes.FailedPrecondition, msg)
		}
	}

	row, err := s.GetPayConfig(ctx)
	if err != nil {
		return err
	}

	content := string(req.Content)
	switch {
	case req.Channel == "wechat" && req.CertType == "private_key":
		row.WechatPrivateKey = content
	case req.Channel == "alipay" && req.CertType == "private_key":
		row.AlipayPrivateKey = content
	// 证书模式三证书：内容与私钥同类处理，仅此处写入，保存请求不触碰
	case req.Channel == "alipay" && req.CertType == "app_cert":
		row.AlipayAppCert = content
	case req.Channel == "alipay" && req.CertType == "alipay_public_cert":
		row.AlipayPublicCert = content
	case req.Channel == "alipay" && req.CertType == "root_cert":
		row.AlipayRootCert = content
	default:
		return fmt.Errorf("不支持的证书类型: %s", req.CertType)
	}
	row.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")

	if err := s.db.GetDb().WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("保存证书失败: %w", err)
	}

	s.payMu.Lock()
	defer s.payMu.Unlock()
	s.applyPayConfig(row)
	return nil
}

// getPayConfigCached 获取支付配置（进程内缓存 + 每次调用按版本核对）
// 缓存版本与数据库 updated_at 一致时复用缓存并保留已构建的渠道客户端，不一致时刷新；
// 数据库查询失败时降级返回当前缓存，避免 DB 抖动阻断支付链路
// 这里刻意不做 TTL 免查库：调用点全在下单/回调/退款链路（非列表逐行，成本是单行主键查询），
// 而支付密钥、回调地址变更后必须立即生效——若缓存期内仍用旧配置，
// 多实例部署下会出现新密钥签名的单被旧客户端验签失败、或下单用已废弃渠道等资金链路故障
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - *model.PayConfig: 支付配置行（缓存或最新），均不可用时返回 nil
func (s *PaymentService) getPayConfigCached(ctx context.Context) *model.PayConfig {
	var row model.PayConfig
	if err := s.db.GetDb().WithContext(ctx).First(&row, payConfigID).Error; err != nil {
		log.Printf("读取支付配置失败（使用缓存兜底）: %v", err)
		s.payMu.RLock()
		defer s.payMu.RUnlock()
		return s.payCfg
	}

	s.payMu.RLock()
	unchanged := s.payCfg != nil && s.payCfg.UpdatedAt == row.UpdatedAt
	cached := s.payCfg
	s.payMu.RUnlock()
	if unchanged {
		return cached
	}

	s.payMu.Lock()
	defer s.payMu.Unlock()
	// 双重检查：等待锁期间其他协程可能已完成刷新
	if s.payCfg == nil || s.payCfg.UpdatedAt != row.UpdatedAt {
		s.applyPayConfig(&row)
	}
	return s.payCfg
}

// getWechatPayConfig 获取微信支付配置（数据库驱动，热更新）
// 读取时持有读锁，返回值为字段副本，可安全在锁外使用
//
// 返回值：
//   - config.WechatConfig: 微信支付配置
func (s *PaymentService) getWechatPayConfig() config.WechatConfig {
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	return wechatConfigOf(s.payCfg)
}

// getAlipayPayConfig 获取支付宝支付配置（数据库驱动，热更新）
// 读取时持有读锁，返回值为字段副本，可安全在锁外使用
//
// 返回值：
//   - config.AlipayConfig: 支付宝支付配置
func (s *PaymentService) getAlipayPayConfig() config.AlipayConfig {
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	return alipayConfigOf(s.payCfg)
}

// getWechatPayClient 获取当前微信支付客户端快照
// 读取时持有读锁；客户端指针一经发布即不再原地修改，返回后可安全在锁外调用
//
// 返回值：
//   - *wegopayment.Client: 微信支付客户端，未配置或配置不完整时为 nil
func (s *PaymentService) getWechatPayClient() *wegopayment.Client {
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	return s.wechatPayClient
}

// getAlipayExecutor 获取当前支付宝通用执行器快照
// 读取时持有读锁；执行器指针一经发布即不再原地修改，返回后可安全在锁外调用
//
// 返回值：
//   - *alipaysdk.GenericExecuteApi: 支付宝执行器，未配置或配置不完整时为 nil
func (s *PaymentService) getAlipayExecutor() *alipaysdk.GenericExecuteApi {
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	return s.alipayExecutor
}

// wechatConfigOf 将支付配置行转换为微信支付配置载体
// 参数：
//   - cfg: 支付配置行，可为 nil
//
// 返回值：
//   - config.WechatConfig: 微信支付配置，cfg 为 nil 时返回零值
func wechatConfigOf(cfg *model.PayConfig) config.WechatConfig {
	if cfg == nil {
		return config.WechatConfig{}
	}
	return config.WechatConfig{
		AppID:        cfg.WechatAppID,
		AppSecret:    cfg.WechatAppSecret,
		MchID:        cfg.WechatMchID,
		APIV3Key:     cfg.WechatAPIV3Key,
		CertSerialNo: cfg.WechatCertSerialNo,
		PrivateKey:   cfg.WechatPrivateKey,
		NotifyURL:    cfg.WechatNotifyURL,
	}
}

// alipayConfigOf 将支付配置行转换为支付宝配置载体
// 参数：
//   - cfg: 支付配置行，可为 nil
//
// 返回值：
//   - config.AlipayConfig: 支付宝配置，cfg 为 nil 时返回零值
func alipayConfigOf(cfg *model.PayConfig) config.AlipayConfig {
	if cfg == nil {
		return config.AlipayConfig{}
	}
	return config.AlipayConfig{
		AppID:            cfg.AlipayAppID,
		PrivateKey:       cfg.AlipayPrivateKey,
		Mode:             cfg.AlipayMode,
		AlipayPublicKey:  cfg.AlipayPublicKey,
		NotifyURL:        cfg.AlipayNotifyURL,
		ReturnURL:        cfg.AlipayReturnURL,
		GatewayURL:       cfg.AlipayGatewayURL,
		AppCert:          cfg.AlipayAppCert,
		AlipayPublicCert: cfg.AlipayPublicCert,
		AlipayRootCert:   cfg.AlipayRootCert,
	}
}

// applyPayConfig 应用配置到内存：更新缓存副本并按需重建微信支付客户端与支付宝执行器
// 微信与支付宝各自独立构建互不影响（一方配置缺失不阻断另一方）
// 调用方必须已持有 s.payMu 写锁
// 参数：
//   - row: 最新支付配置行
func (s *PaymentService) applyPayConfig(row *model.PayConfig) {
	cfgCopy := *row
	s.payCfg = &cfgCopy

	// 微信支付客户端：配置不完整时置空，C 端渠道接口会相应隐藏微信支付
	wechat := wechatConfigOf(&cfgCopy)
	if !wechat.IsPaymentConfigured() {
		s.wechatPayClient = nil
	} else {
		// NewClientE 在配置异常时返回错误，失败时置空客户端，C 端渠道接口会相应隐藏微信支付
		client, err := wegopayment.NewClientE(&wegopayment.Config{
			AppID:        wechat.AppID,
			MchID:        wechat.MchID,
			APIV3Key:     wechat.APIV3Key,
			CertSerialNo: wechat.CertSerialNo,
			PrivateKey:   wechat.PrivateKey,
			NotifyURL:    wechat.NotifyURL,
		})
		if err != nil {
			log.Printf("重建微信支付客户端失败: %v", err)
			s.wechatPayClient = nil
		} else {
			s.wechatPayClient = client
		}
	}

	// 支付宝执行器：公钥模式（AppID + 应用私钥 + 支付宝公钥），
	// 配置不完整或构建失败时置 nil，C 端渠道接口会相应隐藏支付宝支付
	s.alipayExecutor = buildAlipayExecutor(alipayConfigOf(&cfgCopy))
}

// allowedExtList 生成允许的证书扩展名提示列表
//
// 返回值：
//   - []string: 扩展名列表
func allowedExtList() []string {
	exts := make([]string, 0, len(payCertAllowedExts))
	for ext := range payCertAllowedExts {
		exts = append(exts, ext)
	}
	return exts
}
