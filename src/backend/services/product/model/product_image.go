package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// ProductImage 商品图片模型
// 对应商品图片表，记录商品的多张详情图及其排序
type ProductImage struct {
	base.MysqlBaseModel
	ID        int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	ProductID int64  `gorm:"column:product_id;type:bigint;not null;index" json:"product_id"`
	URL       string `gorm:"column:url;type:varchar(512);not null" json:"url"`
	Sort      int32  `gorm:"column:sort;type:int;default:0" json:"sort"`
	CreatedAt string `gorm:"column:created_at;type:datetime" json:"created_at"`
}

// TableName 返回商品图片表名
// 返回值：
//   - string: 表名
func (ProductImage) TableName() string {
	return "product_images"
}
