package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// User 用户模型
// 对应用户表，存储 C 端用户基本信息与登录凭证
type User struct {
	base.MysqlBaseModel
	ID           int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	Openid       string  `gorm:"column:openid;type:varchar(64);unique;comment:微信 openid" json:"openid"`
	AlipayUserID *string `gorm:"column:alipay_user_id;type:varchar(64);unique;comment:支付宝 user_id" json:"alipay_user_id"`
	Unionid      *string `gorm:"column:union_id;type:varchar(64);unique;comment:微信 unionid（多端统一）" json:"union_id"`
	// Phone 手机号：未绑定时存 NULL 而非空串——本列带唯一约束，MySQL 唯一索引允许多行 NULL，
	// 但空串算具体值，第二个无手机号用户插入即撞 1062（纯支付宝/微信授权会员注册走不通）
	Phone       *string `gorm:"column:phone;type:varchar(20);unique;comment:手机号（未绑定为 NULL）" json:"phone"`
	Nickname    string  `gorm:"column:nickname;type:varchar(100);comment:昵称" json:"nickname"`
	Avatar      string  `gorm:"column:avatar;type:varchar(255);comment:头像 URL" json:"avatar"`
	Gender      int32   `gorm:"column:gender;type:tinyint;default:0;comment:0未知 1男 2女，默认0" json:"gender"`
	Birthday    *string `gorm:"column:birthday;type:date;comment:生日（YYYY-MM-DD，可空）" json:"birthday"`
	CommunityID int64   `gorm:"column:community_id;type:bigint;default:0;index;comment:当前选择的社区 ID" json:"community_id"`
	Status      int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	LastLoginAt *string `gorm:"column:last_login_at;type:datetime;comment:最后登录时间" json:"last_login_at"`
	CreatedAt   string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回用户表名
// 返回：
//   - string: 表名
func (User) TableName() string {
	return "users"
}

// NewPhone 构造手机号入库值：空串一律存 NULL
// 唯一索引对 NULL 不冲突、对空串冲突，未绑定手机号必须写成 NULL，
// 否则第二个无手机号会员插入即撞 1062
//
// 参数：
//   - phone: 手机号（空串表示未绑定）
//
// 返回值：
//   - *string: 入库指针（未绑定为 nil）
//
// 使用示例：
//
//	u := &model.User{Phone: model.NewPhone(req.Phone)}
func NewPhone(phone string) *string {
	if phone == "" {
		return nil
	}
	return &phone
}

// PhoneValue 读取手机号（NULL 归一为空串），nil 接收者安全
// 返回值：
//   - string: 手机号，未绑定或用户为空时为空串
func (u *User) PhoneValue() string {
	if u == nil || u.Phone == nil {
		return ""
	}
	return *u.Phone
}
