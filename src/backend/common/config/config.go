package config

import (
	"github.com/jcbowen/jcbaseGo"
)

// Config 全局配置结构体
// 用于 gateway 及各微服务加载 bin/config.yaml
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
	Wechat struct {
		AppID     string `json:"app_id" yaml:"app_id"`
		AppSecret string `json:"app_secret" yaml:"app_secret"`
		MchID     string `json:"mch_id" yaml:"mch_id"`
		APIKey    string `json:"api_key" yaml:"api_key"`
		NotifyURL string `json:"notify_url" yaml:"notify_url"`
		CertPath  string `json:"cert_path" yaml:"cert_path"`
	} `json:"wechat" yaml:"wechat"`
	Alipay struct {
		AppID           string `json:"app_id" yaml:"app_id"`
		PrivateKey      string `json:"private_key" yaml:"private_key"`
		AlipayPublicKey string `json:"alipay_public_key" yaml:"alipay_public_key"`
		NotifyURL       string `json:"notify_url" yaml:"notify_url"`
		ReturnURL       string `json:"return_url" yaml:"return_url"`
	} `json:"alipay" yaml:"alipay"`
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

	// 支付开关配置
	Payment PaymentConfig `json:"payment" yaml:"payment"`
}

// ServiceConfig 微服务通用配置
type ServiceConfig struct {
	GrpcPort int `json:"grpc_port" yaml:"grpc_port"`
}

// PaymentConfig 支付方式开关配置
// enabled_channels 控制当前可用的支付渠道
// 可选值：wechat（微信支付）、alipay（支付宝支付）、cash（货到付款/现金）
type PaymentConfig struct {
	EnabledChannels []string `json:"enabled_channels" yaml:"enabled_channels"`
}
