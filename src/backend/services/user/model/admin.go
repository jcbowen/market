package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Admin 管理员模型
// 对应管理员表，支持超管、社区管理员、员工等角色
type Admin struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	Username    string `gorm:"column:username;type:varchar(50);not null;uniqueIndex" json:"username"`
	Password    string `gorm:"column:password;type:varchar(255);not null" json:"password"`
	RealName    string `gorm:"column:real_name;type:varchar(50)" json:"real_name"`
	Phone       string `gorm:"column:phone;type:varchar(20)" json:"phone"`
	RoleID      int64  `gorm:"column:role_id;type:bigint;not null;index" json:"role_id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;default:0;index" json:"community_id"`
	Status      int32  `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	LastLoginAt string `gorm:"column:last_login_at;type:datetime" json:"last_login_at"`
	LastLoginIP string `gorm:"column:last_login_ip;type:varchar(50)" json:"last_login_ip"`
	CreatedAt   string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回管理员表名
// 返回：
//   - string: 表名
func (Admin) TableName() string {
	return "admins"
}
