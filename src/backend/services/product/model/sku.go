package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Sku 商品 SKU 模型
// 对应 SKU 表，记录规格、价格、库存、安全库存等信息
type Sku struct {
	base.MysqlBaseModel
	ID          int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	ProductID   int64   `gorm:"column:product_id;type:bigint;not null;index" json:"product_id"`
	CommunityID int64   `gorm:"column:community_id;type:bigint;not null;uniqueIndex:idx_sku_code_community" json:"community_id"`
	SkuCode     string  `gorm:"column:sku_code;type:varchar(50);not null;uniqueIndex:idx_sku_code_community" json:"sku_code"`
	SpecName    string  `gorm:"column:spec_name;type:varchar(100)" json:"spec_name"`
	SpecJSON    string  `gorm:"column:spec_json;type:json" json:"spec_json"`
	Price       float64 `gorm:"column:price;type:decimal(10,2);not null" json:"price"`
	CostPrice   float64 `gorm:"column:cost_price;type:decimal(10,2);default:0" json:"cost_price"`
	Stock       int32   `gorm:"column:stock;type:int;default:0" json:"stock"`
	SafetyStock int32   `gorm:"column:safety_stock;type:int;default:0" json:"safety_stock"`
	Image       string  `gorm:"column:image;type:varchar(255)" json:"image"`
	Status      int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt   string  `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string  `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回 SKU 表名
// 返回值：
//   - string: 表名
func (Sku) TableName() string {
	return "skus"
}
