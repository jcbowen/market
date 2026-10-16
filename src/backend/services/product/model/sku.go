package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Sku 商品 SKU 模型
// 对应 SKU 表，记录规格、价格、库存、安全库存等信息
type Sku struct {
	base.MysqlBaseModel
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	ProductID     int64   `gorm:"column:product_id;type:bigint;not null;index;comment:商品 ID" json:"product_id"`
	CommunityID   int64   `gorm:"column:community_id;type:bigint;not null;uniqueIndex:idx_sku_code_community;comment:社区 ID" json:"community_id"`
	SkuCode       string  `gorm:"column:sku_code;type:varchar(50);not null;uniqueIndex:idx_sku_code_community;comment:SKU 编码" json:"sku_code"`
	SpecValueIDs  string  `gorm:"column:spec_value_ids;type:varchar(128);default:'';comment:逗号分隔的规格值ID(多规格组合键,单规格为空)" json:"spec_value_ids"`
	SpecName      string  `gorm:"column:spec_name;type:varchar(100);comment:规格名称（如 ”红色 / 大号”）" json:"spec_name"`
	SpecJSON      string  `gorm:"column:spec_json;type:json;comment:规格 JSON（如 {”颜色”:”红色”,”尺寸”:”大号”}，展示与下单快照冗余）" json:"spec_json"`
	Price         float64 `gorm:"column:price;type:decimal(10,2);not null;comment:售价" json:"price"`
	OriginalPrice float64 `gorm:"column:original_price;type:decimal(10,2);default:0.00;comment:划线价（原价，0 表示不展示）" json:"original_price"`
	CostPrice     float64 `gorm:"column:cost_price;type:decimal(10,2);default:0.00;comment:成本价" json:"cost_price"`
	Stock         int32   `gorm:"column:stock;type:int;default:0;comment:当前库存，默认0" json:"stock"`
	SafetyStock   int32   `gorm:"column:safety_stock;type:int;default:0;comment:安全库存阈值，默认0" json:"safety_stock"`
	Image         string  `gorm:"column:image;type:varchar(255);comment:规格图片 URL" json:"image"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt     string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt     string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回 SKU 表名
// 返回值：
//   - string: 表名
func (Sku) TableName() string {
	return "product_skus"
}
