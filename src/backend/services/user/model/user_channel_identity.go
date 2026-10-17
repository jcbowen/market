package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// UserChannelIdentity 用户渠道身份模型
// 按客户端渠道存储用户的微信 openid/unionid（同一微信用户在不同 appid 下 openid 不同）；
// 存量 users.openid 单列视为 mp 渠道口径，查询时回落读取，不做数据迁移
//
//nolint:lll
//go:generate go tool gormgen -struct UserChannelIdentity
type UserChannelIdentity struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	UserID      int64  `gorm:"column:user_id;type:bigint;uniqueIndex:uk_user_channel,priority:1;comment:用户 ID" json:"user_id"`
	ChannelCode string `gorm:"column:channel_code;type:varchar(32);uniqueIndex:uk_user_channel,priority:2;comment:客户端渠道 code：mp/gzh/app/h5" json:"channel_code"`
	Openid      string `gorm:"column:openid;type:varchar(64);comment:该渠道下的微信 openid" json:"openid"`
	Unionid     string `gorm:"column:unionid;type:varchar(64);comment:微信开放平台 unionid" json:"unionid"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回用户渠道身份表名
//
// 返回值：
//   - string: 表名
func (UserChannelIdentity) TableName() string {
	return "user_channel_identities"
}
