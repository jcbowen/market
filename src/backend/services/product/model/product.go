package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Product 商品模型
// 对应商品主表，包含基础信息、多规格标识、销售状态等
type Product struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index" json:"community_id"`
	CategoryID  int64  `gorm:"column:category_id;type:bigint;not null;index" json:"category_id"`
	Name        string `gorm:"column:name;type:varchar(100);not null" json:"name"`
	Description string `gorm:"column:description;type:text" json:"description"`
	CoverImage  string `gorm:"column:cover_image;type:varchar(255)" json:"cover_image"`
	Images      string `gorm:"column:images;type:json" json:"images"`
	Unit        string `gorm:"column:unit;type:varchar(20)" json:"unit"`
	IsMultiSpec bool   `gorm:"column:is_multi_spec;type:tinyint(1);default:0" json:"is_multi_spec"`
	MinBuy      int32  `gorm:"column:min_buy;type:int;default:1;comment:最低购买份数" json:"min_buy"`
	Status      int32  `gorm:"column:status;type:tinyint;default:1;comment:0下架 1上架 2删除" json:"status"`
	Sort        int32  `gorm:"column:sort;type:int;default:0" json:"sort"`
	SalesCount  int32  `gorm:"column:sales_count;type:int;default:0" json:"sales_count"`
	CreatedAt   string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回商品表名
// 返回值：
//   - string: 表名
func (Product) TableName() string {
	return "products"
}
