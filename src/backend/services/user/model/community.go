package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Community 社区模型
// 对应社区表，管理可配送/自提的社区站点信息
type Community struct {
	base.MysqlBaseModel
	ID              int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	Name            string  `gorm:"column:name;type:varchar(100);not null;comment:社区名称" json:"name"`
	Address         string  `gorm:"column:address;type:varchar(255);comment:地址" json:"address"`
	Longitude       float64 `gorm:"column:longitude;type:decimal(10,7);default:0;comment:经度" json:"longitude"`
	Latitude        float64 `gorm:"column:latitude;type:decimal(10,7);default:0;comment:纬度" json:"latitude"`
	ContactPhone    string  `gorm:"column:contact_phone;type:varchar(20);comment:联系电话" json:"contact_phone"`
	ManagerName     string  `gorm:"column:manager_name;type:varchar(50);comment:负责人姓名" json:"manager_name"`
	ManagerPhone    string  `gorm:"column:manager_phone;type:varchar(20);comment:负责人电话" json:"manager_phone"`
	PickupAddress   string  `gorm:"column:pickup_address;type:varchar(255);comment:自提点地址" json:"pickup_address"`
	DeliveryFee     float64 `gorm:"column:delivery_fee;type:decimal(10,2);default:0;comment:配送费，默认0" json:"delivery_fee"`
	MinOrderAmount  float64 `gorm:"column:min_order_amount;type:decimal(10,2);default:0;comment:起送金额，默认0" json:"min_order_amount"`
	DeliveryRangeKm float64 `gorm:"column:delivery_range_km;type:decimal(6,2);default:0;comment:配送范围（公里）" json:"delivery_range_km"`
	// 营业时间：管理端支持一天内多个运营时间段，序列化为 JSON 存储（如
	// [{"start":"08:00","end":"12:00"},{"start":"14:00","end":"22:00"}]）；
	// 兼容存量 "HH:mm-HH:mm" 单段文本，由展示端解析
	BusinessHours string `gorm:"column:business_hours;type:varchar(255);comment:营业时间描述" json:"business_hours"`
	// 社区所在省/市/区（C 端锁定收货地址前缀用）
	Province string `gorm:"column:province;type:varchar(50);comment:省份" json:"province"`
	City     string `gorm:"column:city;type:varchar(50);comment:城市" json:"city"`
	District string `gorm:"column:district;type:varchar(50);comment:区县" json:"district"`
	// 省/市/区行政区划代码（来自区划选择器，历史数据允许为空；名称字段保留向后兼容）
	ProvinceCode string `gorm:"column:province_code;type:varchar(12);comment:省级行政区划代码" json:"province_code"`
	CityCode     string `gorm:"column:city_code;type:varchar(12);comment:市级行政区划代码" json:"city_code"`
	DistrictCode string `gorm:"column:district_code;type:varchar(12);comment:区县行政区划代码" json:"district_code"`
	// 是否允许社区外配送（false 时 C 端收货地址前缀锁定为本社区省市区）
	AllowOutsideDelivery bool   `gorm:"column:allow_outside_delivery;type:tinyint(1);default:0;comment:是否允许社区外配送 0否 1是" json:"allow_outside_delivery"`
	Sort                 int32  `gorm:"column:sort;type:int;default:0;comment:排序，默认0" json:"sort"`
	Status               int32  `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt            string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt            string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回社区表名
// 返回：
//   - string: 表名
func (Community) TableName() string {
	return "communities"
}
