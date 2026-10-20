package config

import (
	"github.com/jcbowen/jcbaseGo"
)

// Config 全局配置结构体
// 用于 gateway 及各微服务加载 bin/config.yaml
// 注意：支付配置（微信支付/支付宝/渠道开关）已迁移至数据库 pay_config 表，由后台配置管理，
// 此处仅保留微信小程序登录配置（AppID/AppSecret）
type Config struct {
	WebServer jcbaseGo.WebServer   `json:"web_server" yaml:"web_server"`
	Db        jcbaseGo.DbStruct    `json:"db" yaml:"db"`
	Redis     jcbaseGo.RedisStruct `json:"redis" yaml:"redis"`
	Project   struct {
		Name string `json:"name" yaml:"name" default:"社区商超"`
	} `json:"project" yaml:"project"`
	Grpc struct {
		User    string `json:"user_service" yaml:"user_service" default:"localhost:50051"`
		Product string `json:"product_service" yaml:"product_service" default:"localhost:50052"`
		Order   string `json:"order_service" yaml:"order_service" default:"localhost:50053"`
		Cart    string `json:"cart_service" yaml:"cart_service" default:"localhost:50054"`
		Payment string `json:"payment_service" yaml:"payment_service" default:"localhost:50055"`
	} `json:"grpc" yaml:"grpc"`
	JWT struct {
		Secret          string `json:"secret" yaml:"secret" default:"market-secret-key-change-in-production"`
		AccessTokenTTL  int64  `json:"access_token_ttl" yaml:"access_token_ttl" default:"7200"`
		RefreshTokenTTL int64  `json:"refresh_token_ttl" yaml:"refresh_token_ttl" default:"604800"`
	} `json:"jwt" yaml:"jwt"`
	Log struct {
		Level      string `json:"level" yaml:"level" default:"info"`
		Format     string `json:"format" yaml:"format" default:"json"`
		FilePath   string `json:"file_path" yaml:"file_path" default:"logs/"`
		MaxSize    int    `json:"max_size" yaml:"max_size" default:"100"`
		MaxBackups int    `json:"max_backups" yaml:"max_backups" default:"30"`
		MaxAge     int    `json:"max_age" yaml:"max_age" default:"7"`
		Compress   bool   `json:"compress" yaml:"compress" default:"true"`
	} `json:"log" yaml:"log"`
	Upload struct {
		MaxSize      int      `json:"max_size" yaml:"max_size" default:"10485760"`
		AllowedTypes []string `json:"allowed_types" yaml:"allowed_types"`
		Storage      string   `json:"storage" yaml:"storage" default:"local"`
		LocalPath    string   `json:"local_path" yaml:"local_path" default:"static/uploads/"`
	} `json:"upload" yaml:"upload"`
	GeoIP struct {
		// GeoLite2-City 数据库路径，相对路径以可执行文件所在目录为基准
		// 文件由根目录 download-geoip.mjs 定期下载更新（不入 git 仓库），
		// 缺失时 IP 定位接口返回未命中，不影响其他功能
		DBPath string `json:"db_path" yaml:"db_path" default:"data/geoip/GeoLite2-City.mmdb"`
	} `json:"geoip" yaml:"geoip"`
	Wechat   WechatConfig `json:"wechat" yaml:"wechat"`
	Security struct {
		// TrustedProxies 可信反向代理网段（CIDR 或单个 IP），决定 ClientIP 是否采信代理头
		// 缺省为空＝不信任任何代理头，只认直连地址：伪造 X-Forwarded-For 绕过限流的口子由此封死
		// nginx/caddy 与 gateway 同机或同网段反代时必须显式配置（如 ["127.0.0.1"]、["172.16.0.0/12"]），
		// 否则限流、访问日志与 IP 定位会把所有用户合并成代理 IP（表现为整站共用一个限流桶）
		TrustedProxies []string `json:"trusted_proxies" yaml:"trusted_proxies"`
	} `json:"security" yaml:"security"`
	SMS struct {
		Provider     string `json:"provider" yaml:"provider" default:"aliyun"`
		AccessKey    string `json:"access_key" yaml:"access_key"`
		SecretKey    string `json:"secret_key" yaml:"secret_key"`
		SignName     string `json:"sign_name" yaml:"sign_name" default:"社区商超"`
		TemplateCode string `json:"template_code" yaml:"template_code"`
		CodeExpire   int    `json:"code_expire" yaml:"code_expire" default:"300"`
		SendInterval int    `json:"send_interval" yaml:"send_interval" default:"60"`
	} `json:"sms" yaml:"sms"`
	Business struct {
		ExpireWarningDays int `json:"expire_warning_days" yaml:"expire_warning_days" default:"3"`
		OrderTimeout      int `json:"order_timeout" yaml:"order_timeout" default:"1800"`
		AutoCompleteDays  int `json:"auto_complete_days" yaml:"auto_complete_days" default:"7"`
	} `json:"business" yaml:"business"`

	// 各微服务独立配置
	UserService    ServiceConfig `json:"user_service" yaml:"user_service"`
	ProductService ServiceConfig `json:"product_service" yaml:"product_service"`
	OrderService   ServiceConfig `json:"order_service" yaml:"order_service"`
	CartService    ServiceConfig `json:"cart_service" yaml:"cart_service"`
	PaymentService ServiceConfig `json:"payment_service" yaml:"payment_service"`
}

// ServiceConfig 微服务通用配置
type ServiceConfig struct {
	GrpcPort int `json:"grpc_port" yaml:"grpc_port"`
}

// WechatConfig 微信小程序登录配置
// 注意：微信支付 V3 的 AppID/商户号/密钥/证书等支付配置已迁移至数据库 pay_config 表，
// 此结构体仅保留小程序登录所需字段；支付侧使用 WechatConfig 作为数据载体传递
type WechatConfig struct {
	AppID        string `json:"app_id" yaml:"app_id"`                 // 小程序/公众号 AppID
	AppSecret    string `json:"app_secret" yaml:"app_secret"`         // 小程序/公众号 AppSecret
	MchID        string `json:"mch_id" yaml:"mch_id"`                 // 微信支付商户号（仅支付载体使用）
	APIV3Key     string `json:"api_v3_key" yaml:"api_v3_key"`         // 微信支付 V3 API 密钥（仅支付载体使用）
	CertSerialNo string `json:"cert_serial_no" yaml:"cert_serial_no"` // 微信支付商户证书序列号（仅支付载体使用）
	PrivateKey   string `json:"private_key" yaml:"private_key"`       // 微信支付商户私钥 PEM（仅支付载体使用）
	NotifyURL    string `json:"notify_url" yaml:"notify_url"`         // 微信支付结果通知 URL（仅支付载体使用）
}

// IsMiniprogramConfigured 判断微信小程序登录配置是否完整
//
// 返回：
//   - bool: 配置完整返回 true
func (w *WechatConfig) IsMiniprogramConfigured() bool {
	return w.AppID != "" && w.AppSecret != ""
}

// IsPaymentConfigured 判断微信支付 V3 配置是否完整
// 缺少任一必要字段则 C 端不展示微信支付选项
//
// 返回：
//   - bool: 配置完整返回 true
func (w *WechatConfig) IsPaymentConfigured() bool {
	return w.AppID != "" &&
		w.MchID != "" &&
		w.APIV3Key != "" &&
		w.CertSerialNo != "" &&
		w.PrivateKey != "" &&
		w.NotifyURL != ""
}

// 支付宝加签方式常量（pay_config.alipay_mode）
// 空串按公钥模式处理，兼容存量数据
const (
	AlipayModePublic = "public" // 公钥模式：AppID + 应用私钥 + 支付宝公钥
	AlipayModeCert   = "cert"   // 证书模式：AppID + 应用私钥 + 应用公钥证书 + 支付宝公钥证书 + 支付宝根证书
)

// AlipayConfig 支付宝支付配置数据载体
// 支付配置实际存储于数据库 pay_config 表，此结构体仅作为服务间传递的数据载体
type AlipayConfig struct {
	AppID      string `json:"app_id" yaml:"app_id"`           // 支付宝 AppID
	PrivateKey string `json:"private_key" yaml:"private_key"` // 支付宝应用私钥
	// Mode 加签方式：public（默认，空串兼容存量）/ cert，见 AlipayMode* 常量
	Mode            string `json:"mode" yaml:"mode"`
	AlipayPublicKey string `json:"alipay_public_key" yaml:"alipay_public_key"` // 支付宝公钥（公钥模式）
	NotifyURL       string `json:"notify_url" yaml:"notify_url"`               // 支付宝支付结果通知 URL
	ReturnURL       string `json:"return_url" yaml:"return_url"`               // 支付宝支付完成回跳 URL
	// GatewayURL V2/V3 网关地址，空=生产环境 https://openapi.alipay.com；
	// 填支付宝沙箱网关（https://openapi-sandbox.dl.alipaydev.com）即切到沙箱环境联调
	GatewayURL string `json:"gateway_url" yaml:"gateway_url"`
	// 证书模式三证书内容（PEM 文本，仅证书模式参与必填判定与执行器构建）
	AppCert          string `json:"app_cert" yaml:"app_cert"`                     // 应用公钥证书
	AlipayPublicCert string `json:"alipay_public_cert" yaml:"alipay_public_cert"` // 支付宝公钥证书
	AlipayRootCert   string `json:"alipay_root_cert" yaml:"alipay_root_cert"`     // 支付宝根证书
}

// IsCertMode 判断当前配置是否为证书模式（空串按公钥模式，兼容存量数据）
//
// 返回：
//   - bool: 证书模式返回 true
func (a *AlipayConfig) IsCertMode() bool {
	return a.Mode == AlipayModeCert
}

// IsConfigured 判断支付宝支付配置是否完整
// 按加签方式分流必填口径：公钥模式需支付宝公钥，证书模式需三证书齐全；
// 另一模式的残留字段不参与判定，模式切换后未补齐即判未就绪（fail-closed）
//
// 返回：
//   - bool: 配置完整返回 true
func (a *AlipayConfig) IsConfigured() bool {
	if a.AppID == "" || a.PrivateKey == "" || a.NotifyURL == "" {
		return false
	}
	if a.IsCertMode() {
		return a.AppCert != "" && a.AlipayPublicCert != "" && a.AlipayRootCert != ""
	}
	return a.AlipayPublicKey != ""
}

// currentConfigPath 当前加载的配置文件路径
// 由 gateway 启动时通过 SetConfigPath 设置
var currentConfigPath string

// SetConfigPath 设置配置文件保存路径
// 参数：
//   - path: 配置文件绝对路径
func SetConfigPath(path string) {
	currentConfigPath = path
}

// GetConfigPath 获取配置文件保存路径
//
// 返回：
//   - string: 配置文件路径
func GetConfigPath() string {
	return currentConfigPath
}
