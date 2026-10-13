package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Address 用户收货地址模型
// 对应地址表，一个用户可拥有多个地址，仅允许一个默认地址
type Address struct {
	base.MysqlBaseModel
	ID           int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	UserID       int64  `gorm:"column:user_id;type:bigint;not null;index;comment:用户 ID" json:"user_id"`
	ContactName  string `gorm:"column:contact_name;type:varchar(50);not null;comment:联系人" json:"contact_name"`
	ContactPhone string `gorm:"column:contact_phone;type:varchar(20);not null;comment:联系电话" json:"contact_phone"`
	Province     string `gorm:"column:province;type:varchar(50);not null;comment:省" json:"province"`
	City         string `gorm:"column:city;type:varchar(50);not null;comment:市" json:"city"`
	District     string `gorm:"column:district;type:varchar(50);not null;comment:区" json:"district"`
	// 省/市/区行政区划代码（来自区划选择器，历史数据允许为空；名称字段保留向后兼容）
	ProvinceCode string `gorm:"column:province_code;type:varchar(12);comment:省级行政区划代码" json:"province_code"`
	CityCode     string `gorm:"column:city_code;type:varchar(12);comment:市级行政区划代码" json:"city_code"`
	DistrictCode string `gorm:"column:district_code;type:varchar(12);comment:区县行政区划代码" json:"district_code"`
	Detail       string `gorm:"column:detail;type:varchar(255);not null;comment:详细地址" json:"detail"`
	Tag          string `gorm:"column:tag;type:varchar(20);comment:标签（家/公司/学校）" json:"tag"`
	// 地址所属社区（保存时由服务端按用户当前社区回填，供按社区分组展示与配送校验）
	CommunityID int64  `gorm:"column:community_id;type:bigint;default:0;index;comment:关联社区 ID" json:"community_id"`
	IsDefault   bool   `gorm:"column:is_default;type:tinyint(1);default:0;comment:是否默认地址，默认0" json:"is_default"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回地址表名
// 返回：
//   - string: 表名
func (Address) TableName() string {
	return "user_addresses"
}
