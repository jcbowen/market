package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/model"
	wegopayment "github.com/jcbowen/wego/payment"
)

// payCertMaxSize 证书文件大小上限（64KB，PEM 文件通常不超过 10KB）
const payCertMaxSize = 64 * 1024

// payCertAllowedExts 证书文件允许的扩展名
var payCertAllowedExts = map[string]bool{".pem": true, ".key": true, ".txt": true, ".crt": true}

// payConfigID 支付配置表固定单行主键
const payConfigID = 1

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
//   - error: 保存失败时返回错误
func (s *PaymentService) SavePayConfig(ctx context.Context, cfg *payment.PayConfig) error {
	row, err := s.GetPayConfig(ctx)
	if err != nil {
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
	row.WechatAPIV3Key = cfg.WechatApiV3Key
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

// getPayConfigCached 获取支付配置（带进程内缓存与版本检测）
// 缓存版本与数据库 updated_at 一致时直接复用，不一致时刷新缓存并重建支付客户端；
// 数据库查询失败时降级返回当前缓存，避免 DB 抖动阻断支付链路
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
		AppID:           cfg.AlipayAppID,
		PrivateKey:      cfg.AlipayPrivateKey,
		AlipayPublicKey: cfg.AlipayPublicKey,
		NotifyURL:       cfg.AlipayNotifyURL,
		ReturnURL:       cfg.AlipayReturnURL,
	}
}

// applyPayConfig 应用配置到内存：更新缓存副本并按需重建微信支付客户端
// 调用方必须已持有 s.payMu 写锁
// 参数：
//   - row: 最新支付配置行
func (s *PaymentService) applyPayConfig(row *model.PayConfig) {
	cfgCopy := *row
	s.payCfg = &cfgCopy

	// 配置不完整时置空客户端，C 端渠道接口会相应隐藏微信支付
	wechat := wechatConfigOf(&cfgCopy)
	if !wechat.IsPaymentConfigured() {
		s.wechatPayClient = nil
		return
	}

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
		return
	}
	s.wechatPayClient = client
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
