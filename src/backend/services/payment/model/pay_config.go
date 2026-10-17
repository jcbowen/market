package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// PayConfig 支付配置模型（单行存储，id 固定为 1）
// 存储微信支付 V3、支付宝支付全部配置及证书内容，替代原 config.yaml 中的支付节点
//
//nolint:lll
//go:generate go tool gormgen -struct PayConfig
type PayConfig struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;comment:主键，固定为 1" json:"id"`

	// 支付渠道开关与顺序，可选值：wechat / alipay / cash
	EnabledChannels string `gorm:"column:enabled_channels;type:varchar(100);comment:启用的支付渠道 JSON 数组，可选值 wechat/alipay/cash，决定 C 端展示与顺序" json:"enabled_channels"`

	// 适用客户端渠道（勾选的客户端渠道 code JSON 数组：mp/gzh/app/h5）
	// 空数组/空串=全部客户端渠道可用（存量数据兼容口径）；渲染时与后台「渠道管理」已启用渠道取交集
	WechatClientChannels string `gorm:"column:wechat_channels;type:varchar(255);comment:微信支付适用客户端渠道 JSON 数组（mp/gzh/app/h5），空=全部可用" json:"wechat_channels"`
	AlipayClientChannels string `gorm:"column:alipay_channels;type:varchar(255);comment:支付宝适用客户端渠道 JSON 数组（mp/gzh/app/h5），空=全部可用" json:"alipay_channels"`

	// 微信支付 V3
	WechatAppID        string `gorm:"column:wechat_app_id;type:varchar(64);comment:微信支付 AppID" json:"wechat_app_id"`
	WechatAppSecret    string `gorm:"column:wechat_app_secret;type:varchar(128);comment:微信支付 AppSecret" json:"wechat_app_secret"`
	WechatMchID        string `gorm:"column:wechat_mch_id;type:varchar(32);comment:微信支付商户号" json:"wechat_mch_id"`
	WechatAPIV3Key     string `gorm:"column:wechat_api_v3_key;type:varchar(64);comment:微信支付 APIv3 密钥" json:"wechat_api_v3_key"`
	WechatCertSerialNo string `gorm:"column:wechat_cert_serial_no;type:varchar(64);comment:商户证书序列号" json:"wechat_cert_serial_no"`
	WechatPrivateKey   string `gorm:"column:wechat_private_key;type:text;comment:商户私钥 PEM 内容（后台直接上传 apiclient_key.pem 文件，内容存库，不回传明文）" json:"wechat_private_key"`
	WechatNotifyURL    string `gorm:"column:wechat_notify_url;type:varchar(255);comment:微信支付结果通知 URL" json:"wechat_notify_url"`

	// 支付宝
	AlipayAppID      string `gorm:"column:alipay_app_id;type:varchar(64);comment:支付宝 AppID" json:"alipay_app_id"`
	AlipayPrivateKey string `gorm:"column:alipay_private_key;type:text;comment:支付宝应用私钥内容" json:"alipay_private_key"`
	AlipayPublicKey  string `gorm:"column:alipay_public_key;type:text;comment:支付宝公钥内容" json:"alipay_public_key"`
	AlipayNotifyURL  string `gorm:"column:alipay_notify_url;type:varchar(255);comment:支付宝异步通知 URL" json:"alipay_notify_url"`
	AlipayReturnURL  string `gorm:"column:alipay_return_url;type:varchar(255);comment:支付宝支付完成回跳 URL" json:"alipay_return_url"`

	// updated_at 兼作版本号，payment 服务据此检测配置变更并热更新支付客户端
	CreatedAt string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime;comment:更新时间，兼作配置版本号" json:"updated_at"`
}

// TableName 返回支付配置表名
// 返回值：
//   - string: 表名
func (PayConfig) TableName() string {
	return "pay_config"
}
