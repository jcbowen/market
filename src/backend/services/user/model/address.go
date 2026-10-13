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
	// 省/市/区行政区划代码（来自区划选择器，历史数据允许为空；名称字段保留向后兼容）
	ProvinceCode string `gorm:"column:province_code;type:varchar(12)" json:"province_code"`
	CityCode     string `gorm:"column:city_code;type:varchar(12)" json:"city_code"`
	DistrictCode string `gorm:"column:district_code;type:varchar(12)" json:"district_code"`
	Detail       string `gorm:"column:detail;type:varchar(255);not null" json:"detail"`
	Tag          string `gorm:"column:tag;type:varchar(20)" json:"tag"`
	// 地址所属社区（保存时由服务端按用户当前社区回填，供按社区分组展示与配送校验）
	CommunityID int64  `gorm:"column:community_id;type:bigint;default:0;index" json:"community_id"`
	IsDefault   bool   `gorm:"column:is_default;type:tinyint(1);default:0" json:"is_default"`
	CreatedAt   string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回地址表名
// 返回：
//   - string: 表名
func (Address) TableName() string {
	return "user_addresses"
}
