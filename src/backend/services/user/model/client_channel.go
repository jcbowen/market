package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// ClientChannel 客户端渠道模型
// 后台「渠道管理」统一维护各客户端访问凭证（AppID/AppSecret）、启用开关与排序；
// 登录（Code2Session/未来的公众号 OAuth）与支付（按渠道 AppID 下单）按渠道取用，
// 支付配置侧按渠道勾选各支付方式的适用范围
//
//nolint:lll
//go:generate go tool gormgen -struct ClientChannel
type ClientChannel struct {
	base.MysqlBaseModel
	ID        int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	Code      string `gorm:"column:code;type:varchar(32);uniqueIndex:uk_channel_code;comment:渠道标识：mp 微信小程序 / gzh 微信公众号 / app 开放平台App / h5 浏览器H5" json:"code"`
	Name      string `gorm:"column:name;type:varchar(64);comment:显示名称" json:"name"`
	AppID     string `gorm:"column:app_id;type:varchar(64);comment:渠道 AppID" json:"app_id"`
	AppSecret string `gorm:"column:app_secret;type:varchar(128);comment:渠道 AppSecret" json:"app_secret"`
	Enabled   bool   `gorm:"column:enabled;comment:是否启用（未启用时该端登录/支付能力不可用）" json:"enabled"`
	Sort      int32  `gorm:"column:sort;type:int;comment:排序值，越小越靠前" json:"sort"`
	CreatedAt string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回客户端渠道表名
//
// 返回值：
//   - string: 表名
func (ClientChannel) TableName() string {
	return "client_channels"
}

// IsConfigured 渠道凭证是否配置完整（AppID 与 AppSecret 均非空）
// h5 渠道无需凭证，恒视为已配置
//
// 返回值：
//   - bool: 是否配置完整
func (c *ClientChannel) IsConfigured() bool {
	if c.Code == "h5" {
		return true
	}
	return c.AppID != "" && c.AppSecret != ""
}
