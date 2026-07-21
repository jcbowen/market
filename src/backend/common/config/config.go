package config

import (
	"fmt"
	"os"
	"sync"

	"github.com/jcbowen/jcbaseGo"
	"gopkg.in/yaml.v3"
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
	Wechat WechatConfig `json:"wechat" yaml:"wechat"`
	Alipay AlipayConfig `json:"alipay" yaml:"alipay"`
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
// enabled_channels 控制当前可用的支付渠道，同时决定 C 端展示顺序
// 可选值：wechat（微信支付）、alipay（支付宝支付）、cash（货到付款/现金）
type PaymentConfig struct {
	EnabledChannels []string `json:"enabled_channels" yaml:"enabled_channels"`
}

// WechatConfig 微信相关配置
// 同时用于微信小程序登录与微信支付 V3
type WechatConfig struct {
	AppID        string `json:"app_id" yaml:"app_id"`                 // 小程序/公众号 AppID
	AppSecret    string `json:"app_secret" yaml:"app_secret"`         // 小程序/公众号 AppSecret
	MchID        string `json:"mch_id" yaml:"mch_id"`                 // 微信支付商户号
	APIV3Key     string `json:"api_v3_key" yaml:"api_v3_key"`         // 微信支付 V3 API 密钥
	CertSerialNo string `json:"cert_serial_no" yaml:"cert_serial_no"` // 微信支付商户证书序列号
	PrivateKey   string `json:"private_key" yaml:"private_key"`       // 微信支付商户私钥 PEM
	NotifyURL    string `json:"notify_url" yaml:"notify_url"`         // 微信支付结果通知 URL
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

// AlipayConfig 支付宝支付配置
// 当前项目仅做配置保存与开关控制，实际支付仍为模拟实现
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

// CfgMu 配置读写锁
// 用于保护运行时的配置并发读写（如 Admin 修改支付配置时与 PaymentChannels 读取）
var CfgMu sync.RWMutex

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

// Save 将支付相关配置增量写回配置文件
// 只更新 wechat、alipay、payment 节点，保留其他配置不变
// 写回后 Gateway 内存中的配置对象会同步更新，底层微信支付相关密钥修改后需要重启服务生效
// 参数：
//   - cfg: 要保存的配置
//
// 返回值：
//   - error: 保存失败时返回错误
func Save(cfg *Config) error {
	if currentConfigPath == "" {
		return fmt.Errorf("配置文件路径未设置")
	}

	data, err := os.ReadFile(currentConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	var root map[string]interface{}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("解析配置文件失败: %w", err)
		}
	}
	if root == nil {
		root = make(map[string]interface{})
	}

	newData, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	var partial map[string]interface{}
	if err := yaml.Unmarshal(newData, &partial); err != nil {
		return fmt.Errorf("解析新配置失败: %w", err)
	}

	for _, key := range []string{"wechat", "alipay", "payment"} {
		if v, ok := partial[key]; ok {
			root[key] = v
		}
	}

	updated, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("序列化合并后配置失败: %w", err)
	}

	if err := os.WriteFile(currentConfigPath, updated, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return nil
}
