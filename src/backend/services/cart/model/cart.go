package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Cart 购物车模型
// 对应购物车表，记录用户加入购物车的商品及数量
type Cart struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	UserID      int64  `gorm:"column:user_id;type:bigint;not null;uniqueIndex:uk_user_sku;index:idx_user_community" json:"user_id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index:idx_user_community" json:"community_id"`
	ProductID   int64  `gorm:"column:product_id;type:bigint;not null" json:"product_id"`
	SkuID       int64  `gorm:"column:sku_id;type:bigint;not null;uniqueIndex:uk_user_sku" json:"sku_id"`
	Quantity    int32  `gorm:"column:quantity;type:int;default:1" json:"quantity"`
	Selected    bool   `gorm:"column:selected;type:tinyint(1);default:1" json:"selected"`
	CreatedAt   string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回购物车表名
// 返回值：
//   - string: 表名
func (Cart) TableName() string {
	return "carts"
}
