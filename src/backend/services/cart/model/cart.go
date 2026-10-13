package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Cart 购物车模型
// 对应购物车表，记录用户加入购物车的商品及数量
type Cart struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	UserID      int64  `gorm:"column:user_id;type:bigint;not null;uniqueIndex:uk_user_sku;index:idx_user_community;comment:用户 ID" json:"user_id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index:idx_user_community;comment:社区 ID" json:"community_id"`
	ProductID   int64  `gorm:"column:product_id;type:bigint;not null;comment:商品 ID" json:"product_id"`
	SkuID       int64  `gorm:"column:sku_id;type:bigint;not null;uniqueIndex:uk_user_sku;comment:SKU ID" json:"sku_id"`
	Quantity    int32  `gorm:"column:quantity;type:int;default:1;comment:数量" json:"quantity"`
	Selected    bool   `gorm:"column:selected;type:tinyint(1);default:1;comment:是否选中，默认1" json:"selected"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`

	// 商品快照（加购时写入）：SKU/商品被删除后仍可展示失效商品信息
	ProductName  string `gorm:"column:product_name;type:varchar(255);default:'';comment:商品名称快照" json:"product_name"`
	SpecName     string `gorm:"column:spec_name;type:varchar(255);default:'';comment:规格名快照" json:"spec_name"`
	ProductImage string `gorm:"column:product_image;type:varchar(500);default:'';comment:商品图片快照" json:"product_image"`
}

// TableName 返回购物车表名
// 返回值：
//   - string: 表名
func (Cart) TableName() string {
	return "carts"
}
