package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// UserCommunity 会员-社区关联模型
// 多社区会员模型：会员可在多个社区出现（添加过收货地址、下过单、
// 后台在该社区添加注册），与 communities 形成多对多关系
type UserCommunity struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	UserID      int64  `gorm:"column:user_id;type:bigint;not null;uniqueIndex:uk_user_community,priority:1;comment:用户 ID" json:"user_id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;uniqueIndex:uk_user_community,priority:2;comment:社区 ID" json:"community_id"`
	Source      string `gorm:"column:source;type:varchar(20);default:'';comment:关联来源：address收货地址/order下单/admin后台添加/backfill存量回填" json:"source"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回会员-社区关联表名
// 返回：
//   - string: 表名
func (UserCommunity) TableName() string {
	return "user_communities"
}
