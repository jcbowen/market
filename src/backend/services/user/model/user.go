package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// User 用户模型
// 对应用户表，存储 C 端用户基本信息与登录凭证
type User struct {
	base.MysqlBaseModel
	ID           int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	Openid       string  `gorm:"column:openid;type:varchar(64);index:idx_user_openid,unique" json:"openid"`
	AlipayUserID *string `gorm:"column:alipay_user_id;type:varchar(64);index:idx_user_alipay,unique" json:"alipay_user_id"`
	Unionid      *string `gorm:"column:union_id;type:varchar(64);index:idx_user_union,unique" json:"union_id"`
	Phone        string  `gorm:"column:phone;type:varchar(20);index:idx_user_phone,unique" json:"phone"`
	Nickname     string `gorm:"column:nickname;type:varchar(100)" json:"nickname"`
	Avatar       string `gorm:"column:avatar;type:varchar(255)" json:"avatar"`
	Gender       int32  `gorm:"column:gender;type:tinyint;default:0" json:"gender"`
	CommunityID  int64  `gorm:"column:community_id;type:bigint;default:0;index" json:"community_id"`
	Status       int32  `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	LastLoginAt  string `gorm:"column:last_login_at;type:datetime" json:"last_login_at"`
	CreatedAt    string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt    string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回用户表名
// 返回：
//   - string: 表名
func (User) TableName() string {
	return "users"
}
