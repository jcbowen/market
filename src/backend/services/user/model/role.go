package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Role 角色模型
// 对应角色表，存储角色编码、权限列表等
type Role struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	Name        string `gorm:"column:name;type:varchar(50);not null;comment:角色名称" json:"name"`
	Code        string `gorm:"column:code;type:varchar(50);not null;unique;comment:角色编码（super_admin / community_admin / staff）" json:"code"`
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
