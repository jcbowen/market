package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Address 用户收货地址模型
// 对应地址表，一个用户可拥有多个地址，仅允许一个默认地址
type Address struct {
	base.MysqlBaseModel
	ID           int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	UserID       int64  `gorm:"column:user_id;type:bigint;not null;index" json:"user_id"`
	ContactName  string `gorm:"column:contact_name;type:varchar(50);not null" json:"contact_name"`
	ContactPhone string `gorm:"column:contact_phone;type:varchar(20);not null" json:"contact_phone"`
	Province     string `gorm:"column:province;type:varchar(50);not null" json:"province"`
	City         string `gorm:"column:city;type:varchar(50);not null" json:"city"`
	District     string `gorm:"column:district;type:varchar(50);not null" json:"district"`
	Detail       string `gorm:"column:detail;type:varchar(255);not null" json:"detail"`
	Tag          string `gorm:"column:tag;type:varchar(20)" json:"tag"`
	IsDefault    bool   `gorm:"column:is_default;type:tinyint(1);default:0" json:"is_default"`
	CreatedAt    string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt    string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回地址表名
// 返回：
//   - string: 表名
func (Address) TableName() string {
	return "addresses"
}
