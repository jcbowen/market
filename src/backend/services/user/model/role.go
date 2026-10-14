package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// 内置角色编码：每个社区的「社区管理员」内置角色
const (
	// RoleCodeSuperAdmin 全局超管角色编码（仅存量数据兼容用，新模型超管标记在 admin_users.is_super）
	RoleCodeSuperAdmin = "super_admin"
	// RoleCodeCommunityAdmin 社区管理员内置角色编码（每个社区一条，community_id = 所属社区）
	RoleCodeCommunityAdmin = "community_admin"
)

// Role 角色模型
// 多社区模型：community_id = 0 为全局角色（仅超管相关），> 0 为社区私有角色
// 同一 code 可在不同社区各有一条（如每社区一条内置 community_admin）
type Role struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	Name        string `gorm:"column:name;type:varchar(50);not null;comment:角色名称" json:"name"`
	Code        string `gorm:"column:code;type:varchar(50);not null;uniqueIndex:uk_role_community_code,priority:2;comment:角色编码（community_admin 为内置社区管理员角色）" json:"code"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;default:0;index;uniqueIndex:uk_role_community_code,priority:1;comment:所属社区 ID，0=全局角色" json:"community_id"`
	Permissions string `gorm:"column:permissions;type:json;comment:权限 JSON 数组" json:"permissions"`
	Description string `gorm:"column:description;type:varchar(255);comment:描述" json:"description"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回角色表名
// 返回：
//   - string: 表名
func (Role) TableName() string {
	return "admin_roles"
}

// IsBuiltinCommunityAdmin 是否为社区管理员内置角色（每社区一条，不可删除、权限仅超管可改）
// 返回：
//   - bool: 是否内置社区管理员角色
func (r *Role) IsBuiltinCommunityAdmin() bool {
	return r != nil && r.Code == RoleCodeCommunityAdmin && r.CommunityID > 0
}
