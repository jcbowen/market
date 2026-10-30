package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// 支付配置平台类型常量
const (
	PayPlatformWechat = "wechat"
	PayPlatformAlipay = "alipay"
)

// PayConfig 支付配置模型（多行存储）
// 每行 = 一条独立支付配置：名称 + 平台类型（wechat/alipay）+ 启用开关 + 凭证字段组；
// 同平台可建多条（如「支付宝-H5」「支付宝-小程序」），各端收银台用哪条由
// client_channels.pay_methods 渠道绑定决定，本表不再存储渠道勾选与全局开关；
// updated_at 兼作配置版本号，payment 服务据此检测变更并按行热重建支付客户端
//
//nolint:lll
//go:generate go tool gormgen -struct PayConfig
type PayConfig struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;comment:主键" json:"id"`

	// 展示名称（如「支付宝-H5」「微信支付-主力」），管理端列表与渠道绑定下拉展示用
	Name string `gorm:"column:name;type:varchar(64);not null;default:'';comment:展示名称" json:"name"`
	// 平台类型：wechat / alipay，决定行内取用哪组凭证字段
	Platform string `gorm:"column:platform;type:varchar(16);not null;default:'';comment:平台类型：wechat/alipay" json:"platform"`
	// 启用开关：停用的配置不参与渲染与下单（即使凭证完整）
	Enabled bool `gorm:"column:enabled;comment:是否启用" json:"enabled"`

	// 微信支付 V3 凭证（platform=wechat 时取用）
	// 注意：本表刻意不存微信 AppID。微信的 AppID 是「渠道身份」（同一商户号可关联小程序、
	// H5 等多个 AppID，各自对应一个客户端渠道），放进配置行就会强迫一个商户号建多份配置；
	// 支付宝相反，其 AppID 是「签名身份」（一个 AppID 一套密钥对），必须留在配置行（AlipayAppID）。
	// 两边字段归属不对称是有意的，不是待统一的不一致。
	// 下单 AppID 由 client_channels.app_id 提供、随建单快照进 payments.app_id，回调归属按后者校验
	WechatAppSecret    string `gorm:"column:wechat_app_secret;type:varchar(128);comment:微信 AppSecret" json:"wechat_app_secret"`
	WechatMchID        string `gorm:"column:wechat_mch_id;type:varchar(32);comment:微信支付商户号" json:"wechat_mch_id"`
	WechatAPIV3Key     string `gorm:"column:wechat_api_v3_key;type:varchar(64);comment:微信支付 APIv3 密钥" json:"wechat_api_v3_key"`
	WechatCertSerialNo string `gorm:"column:wechat_cert_serial_no;type:varchar(64);comment:商户证书序列号" json:"wechat_cert_serial_no"`
	WechatPrivateKey   string `gorm:"column:wechat_private_key;type:text;comment:商户私钥 PEM 内容（后台直接上传 apiclient_key.pem 文件，内容存库，不回传明文）" json:"wechat_private_key"`
	WechatNotifyURL    string `gorm:"column:wechat_notify_url;type:varchar(255);comment:微信支付结果通知 URL" json:"wechat_notify_url"`

	// 支付宝凭证（platform=alipay 时取用）
	AlipayAppID      string `gorm:"column:alipay_app_id;type:varchar(64);comment:支付宝 AppID" json:"alipay_app_id"`
	AlipayPrivateKey string `gorm:"column:alipay_private_key;type:text;comment:支付宝应用私钥内容" json:"alipay_private_key"`
	AlipayPublicKey  string `gorm:"column:alipay_public_key;type:text;comment:支付宝公钥内容（公钥模式）" json:"alipay_public_key"`
	AlipayNotifyURL  string `gorm:"column:alipay_notify_url;type:varchar(255);comment:支付宝异步通知 URL" json:"alipay_notify_url"`
	AlipayReturnURL  string `gorm:"column:alipay_return_url;type:varchar(255);comment:支付宝支付完成回跳 URL" json:"alipay_return_url"`
	// 加签方式：public 公钥模式（空串兼容存量）/ cert 证书模式，决定必填字段口径与回调验签路径
	AlipayMode string `gorm:"column:alipay_mode;type:varchar(16);comment:支付宝加签方式：public 公钥模式（默认，空串兼容存量）/ cert 证书模式" json:"alipay_mode"`
	// 证书模式三证书，内容与私钥同类：仅上传接口写入，GET 不回传明文，保存请求不触碰（空值保留原值）
	AlipayAppCert    string `gorm:"column:alipay_app_cert;type:mediumtext;comment:应用公钥证书内容（证书模式，上传接口维护，不回传明文）" json:"alipay_app_cert"`
	AlipayPublicCert string `gorm:"column:alipay_public_cert;type:mediumtext;comment:支付宝公钥证书内容（证书模式，上传接口维护，不回传明文）" json:"alipay_public_cert"`
	AlipayRootCert   string `gorm:"column:alipay_root_cert;type:mediumtext;comment:支付宝根证书内容（证书模式，上传接口维护，不回传明文）" json:"alipay_root_cert"`
	// 网关地址为空表示生产环境（https://openapi.alipay.com），填沙箱地址即切换沙箱环境
	AlipayGatewayURL string `gorm:"column:alipay_gateway_url;type:varchar(255);comment:支付宝网关地址，空=生产环境 openapi.alipay.com" json:"alipay_gateway_url"`

	CreatedAt string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime;comment:更新时间，兼作配置版本号" json:"updated_at"`
}

// TableName 返回支付配置表名
//
// 返回值：
//   - string: 表名
func (PayConfig) TableName() string {
	return "pay_config"
}
