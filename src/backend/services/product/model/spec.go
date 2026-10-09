package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// ProductSpec 商品规格名模型
// 多规格商品的规格名（如"颜色"、"尺寸"），每个商品可有多条
type ProductSpec struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	ProductID   int64  `gorm:"column:product_id;type:bigint;not null;index" json:"product_id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index:idx_community_product,priority:1" json:"community_id"`
	Name        string `gorm:"column:name;type:varchar(64);not null" json:"name"`
	Sort        int32  `gorm:"column:sort;type:int;default:0" json:"sort"`
	CreatedAt   string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
	// default:NULL 使 GORM 在 INSERT 时排除该列，避免空串写入 strict 模式的 datetime 列报错
	DeletedAt string `gorm:"column:deleted_at;type:datetime;default:NULL;index" json:"deleted_at"`
}

// TableName 返回商品规格名表名
// 返回值：
//   - string: 表名
func (ProductSpec) TableName() string {
	return "product_specs"
}

// ProductSpecValue 商品规格值模型
// 挂在规格名下的具体取值（如"红色"、"L"），通过 spec_id 关联规格名
type ProductSpecValue struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	SpecID      int64  `gorm:"column:spec_id;type:bigint;not null;index" json:"spec_id"`
	ProductID   int64  `gorm:"column:product_id;type:bigint;not null;index" json:"product_id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null" json:"community_id"`
	Name        string `gorm:"column:name;type:varchar(64);not null" json:"name"`
	Sort        int32  `gorm:"column:sort;type:int;default:0" json:"sort"`
	CreatedAt   string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
	// default:NULL 使 GORM 在 INSERT 时排除该列，避免空串写入 strict 模式的 datetime 列报错
	DeletedAt string `gorm:"column:deleted_at;type:datetime;default:NULL;index" json:"deleted_at"`
}

// TableName 返回商品规格值表名
// 返回值：
//   - string: 表名
func (ProductSpecValue) TableName() string {
	return "product_spec_values"
}
