package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// AdminCommunity 管理员与社区关联模型
// 支持一个管理员关联多个社区，实现门店级数据隔离
type AdminCommunity struct {
	base.MysqlBaseModel
	ID          int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	AdminID     int64 `gorm:"column:admin_id;type:bigint;not null;index" json:"admin_id"`
	CommunityID int64 `gorm:"column:community_id;type:bigint;not null;index" json:"community_id"`
}

// TableName 返回管理员社区关联表名
// 返回：
//   - string: 表名
func (AdminCommunity) TableName() string {
	return "admin_communities"
}
