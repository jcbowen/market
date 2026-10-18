package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Admin 管理员模型（账号层）
// 多社区模型下仅承载账号身份属性；社区内的角色与权限通过 admin_communities 任职表表达。
// 旧单角色模型的 role_id / community_id 列已废弃（DB 列保留，代码不再读写）。
type Admin struct {
	base.MysqlBaseModel
	ID       int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	Username string `gorm:"column:username;type:varchar(50);not null;unique;comment:用户名" json:"username"`
	Password string `gorm:"column:password;type:varchar(255);not null;comment:bcrypt 加密" json:"password"`
	RealName string `gorm:"column:real_name;type:varchar(50);comment:真实姓名" json:"real_name"`
	Phone    string `gorm:"column:phone;type:varchar(20);comment:手机号" json:"phone"`
	// IsSuper 是否超级管理员：不受社区与角色约束，拥有全部权限
	IsSuper bool `gorm:"column:is_super;type:tinyint;not null;default:0;comment:是否超级管理员" json:"is_super"`
	Status  int32  `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	// UserID 绑定的会员 ID（NULL=未绑定）：绑定后该会员在员工全部任职社区获得履约人员身份
	// （B 履约工作台 + 订单指派），指针类型配合唯一索引允许多个 NULL 并存
	UserID *int64 `gorm:"column:user_id;type:bigint;unique;comment:绑定会员 ID NULL=未绑定 绑定后即履约人员" json:"user_id"`
	LastLoginAt *string `gorm:"column:last_login_at;type:datetime;comment:最后登录时间" json:"last_login_at"`
	LastLoginIP string  `gorm:"column:last_login_ip;type:varchar(50);comment:最后登录 IP" json:"last_login_ip"`
	CreatedAt   string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回管理员表名
// 返回：
//   - string: 表名
func (Admin) TableName() string {
	return "admin_users"
}
