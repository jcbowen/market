package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Role 角色模型
// 对应角色表，存储角色编码、权限列表等
type Role struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	Name        string `gorm:"column:name;type:varchar(50);not null" json:"name"`
	Code        string `gorm:"column:code;type:varchar(50);not null;unique" json:"code"`
	Permissions string `gorm:"column:permissions;type:json" json:"permissions"`
	Description string `gorm:"column:description;type:varchar(255)" json:"description"`
	CreatedAt   string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回角色表名
// 返回：
//   - string: 表名
func (Role) TableName() string {
	return "admin_roles"
}
