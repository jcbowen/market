package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// AdminCommunity 管理员社区任职模型（原多对多绑定表升级）
// 一行 = 某管理员在某社区担任某角色；同一管理员在同一社区可担任多个角色（多行），
// 同一管理员可在多个社区任职（多社区员工）
type AdminCommunity struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	// AdminID 管理员 ID
	AdminID int64 `gorm:"column:admin_id;type:bigint;not null;index;uniqueIndex:uk_admin_community_role,priority:1;comment:管理员 ID" json:"admin_id"`
	// CommunityID 任职社区 ID
	CommunityID int64 `gorm:"column:community_id;type:bigint;not null;index;uniqueIndex:uk_admin_community_role,priority:2;comment:任职社区 ID" json:"community_id"`
	// RoleID 在该社区担任的角色 ID（指向 admin_roles，须为该社区的角色）
	RoleID int64 `gorm:"column:role_id;type:bigint;not null;default:0;index;uniqueIndex:uk_admin_community_role,priority:3;comment:角色 ID" json:"role_id"`
}

// TableName 返回管理员社区任职表名
// 返回：
//   - string: 表名
func (AdminCommunity) TableName() string {
	return "admin_communities"
}
