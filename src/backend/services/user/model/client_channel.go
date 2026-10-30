package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// ClientChannel 客户端渠道模型
// 后台「渠道管理」统一维护各客户端访问凭证（AppID/AppSecret）、启用开关与支付方式绑定；
// 登录（Code2Session/未来的公众号 OAuth）与支付（按渠道 AppID 下单）按渠道取用；
// 渠道启用的支付方式（绑定哪些支付配置与顺序）由 pay_methods 维护，
// 支付配置（pay_config 多行）仅作为凭证载体，不再反向勾选适用渠道
//
//nolint:lll
//go:generate go tool gormgen -struct ClientChannel
type ClientChannel struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	// 唯一性用列级 unique tag 声明（项目约定，见 attachment_migrate_item.go / order main.go）：
	// 单列唯一若用 uniqueIndex tag 会命中 gorm v1.31 的 MigrateColumnUnique 怪癖——
	// 列已存在 UNI 索引但字段无 unique tag 时，每次 AutoMigrate 都尝试 DROP uni_ 前缀约束，
	// MySQL 无此外键报 1091，服务启动失败（存量库必现）
	Code      string `gorm:"column:code;type:varchar(32);not null;default:'';unique;comment:渠道标识：h5 浏览器H5 / gzh 微信公众号 / mp 微信小程序 / alimp 支付宝小程序 / android 安卓App / ios iOS App / pos 门店收银台" json:"code"`
	Name      string `gorm:"column:name;type:varchar(64);comment:显示名称" json:"name"`
	AppID     string `gorm:"column:app_id;type:varchar(64);comment:渠道 AppID（alimp 仅需此项，服务端签名私钥复用支付配置）" json:"app_id"`
	AppSecret string `gorm:"column:app_secret;type:varchar(128);comment:渠道 AppSecret（alimp 无需）" json:"app_secret"`
	Enabled   bool   `gorm:"column:enabled;comment:是否启用（未启用时该端登录/支付能力不可用）" json:"enabled"`
	// PayMethods 渠道绑定的支付方式列表（有序 JSON 数组，顺序即该端收银台展示顺序）：
	// 元素为 "cash" 或 "<platform>:<config_id>"（如 "wechat:3"/"alipay:2"）；
	// 同平台至多一条，cash 至多一条；保存时服务端做语法与平台兼容校验，
	// 配置存在性/启用/凭证就绪在渲染与下单时由网关+payment 服务兜底过滤
	PayMethods string `gorm:"column:pay_methods;type:varchar(255);comment:绑定的支付方式有序 JSON 数组：cash 或 platform:config_id，顺序即收银台展示顺序" json:"pay_methods"`
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

// IsConfigured 渠道凭证是否配置完整
// h5/pos 无需凭证恒视为已配置；alimp 仅需 AppID（签名私钥复用支付配置）；
// 其余渠道要求 AppID 与 AppSecret 均非空
//
// 返回值：
//   - bool: 是否配置完整
func (c *ClientChannel) IsConfigured() bool {
	switch c.Code {
	case "h5", "pos":
		return true
	case "alimp":
		return c.AppID != ""
	}
	return c.AppID != "" && c.AppSecret != ""
}
