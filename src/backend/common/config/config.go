package config

import (
	"github.com/jcbowen/jcbaseGo"
)

// Config 全局配置结构体
// 用于 gateway 及各微服务加载 bin/config.yaml
// 注意：支付配置（微信支付/支付宝/渠道开关）已迁移至数据库 pay_config 表，由后台配置管理，
// 此处仅保留微信小程序登录配置（AppID/AppSecret）
type Config struct {
	WebServer jcbaseGo.WebServer `json:"web_server" yaml:"web_server"`
	Db        jcbaseGo.DbStruct  `json:"db" yaml:"db"`
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
	Wechat WechatConfig `json:"wechat" yaml:"wechat"`
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

// AlipayConfig 支付宝支付配置数据载体
// 支付配置实际存储于数据库 pay_config 表，此结构体仅作为服务间传递的数据载体
type AlipayConfig struct {
	AppID           string `json:"app_id" yaml:"app_id"`                     // 支付宝 AppID
	PrivateKey      string `json:"private_key" yaml:"private_key"`           // 支付宝应用私钥
	AlipayPublicKey string `json:"alipay_public_key" yaml:"alipay_public_key"` // 支付宝公钥
	NotifyURL       string `json:"notify_url" yaml:"notify_url"`             // 支付宝支付结果通知 URL
	ReturnURL       string `json:"return_url" yaml:"return_url"`             // 支付宝支付完成回跳 URL
}

// IsConfigured 判断支付宝支付配置是否完整
//
// 返回：
//   - bool: 配置完整返回 true
func (a *AlipayConfig) IsConfigured() bool {
	return a.AppID != "" &&
		a.PrivateKey != "" &&
		a.AlipayPublicKey != "" &&
		a.NotifyURL != ""
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
