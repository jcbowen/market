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
	ID int64 `gorm:"column:id;type:bigint;primaryKey" json:"id"`

	// 支付渠道开关与顺序，可选值：wechat / alipay / cash
	EnabledChannels string `gorm:"column:enabled_channels;type:varchar(100)" json:"enabled_channels"`

	// 微信支付 V3
	WechatAppID        string `gorm:"column:wechat_app_id;type:varchar(64)" json:"wechat_app_id"`
	WechatAppSecret    string `gorm:"column:wechat_app_secret;type:varchar(128)" json:"wechat_app_secret"`
	WechatMchID        string `gorm:"column:wechat_mch_id;type:varchar(32)" json:"wechat_mch_id"`
	WechatAPIV3Key     string `gorm:"column:wechat_api_v3_key;type:varchar(64)" json:"wechat_api_v3_key"`
	WechatCertSerialNo string `gorm:"column:wechat_cert_serial_no;type:varchar(64)" json:"wechat_cert_serial_no"`
	WechatPrivateKey   string `gorm:"column:wechat_private_key;type:text" json:"wechat_private_key"`
	WechatNotifyURL    string `gorm:"column:wechat_notify_url;type:varchar(255)" json:"wechat_notify_url"`

	// 支付宝
	AlipayAppID      string `gorm:"column:alipay_app_id;type:varchar(64)" json:"alipay_app_id"`
	AlipayPrivateKey string `gorm:"column:alipay_private_key;type:text" json:"alipay_private_key"`
	AlipayPublicKey  string `gorm:"column:alipay_public_key;type:text" json:"alipay_public_key"`
	AlipayNotifyURL  string `gorm:"column:alipay_notify_url;type:varchar(255)" json:"alipay_notify_url"`
	AlipayReturnURL  string `gorm:"column:alipay_return_url;type:varchar(255)" json:"alipay_return_url"`

	// updated_at 兼作版本号，payment 服务据此检测配置变更并热更新支付客户端
	CreatedAt string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回支付配置表名
// 返回值：
//   - string: 表名
func (PayConfig) TableName() string {
	return "pay_config"
}
