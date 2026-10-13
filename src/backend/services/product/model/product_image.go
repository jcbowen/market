package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// ProductImage 商品图片模型
// 对应商品图片表，记录商品的多张详情图及其排序
type ProductImage struct {
	base.MysqlBaseModel
	ID        int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	ProductID int64  `gorm:"column:product_id;type:bigint;not null;index;comment:商品 ID" json:"product_id"`
	URL       string `gorm:"column:url;type:varchar(512);not null;comment:图片 URL" json:"url"`
	Sort      int32  `gorm:"column:sort;type:int;default:0;comment:排序，默认0" json:"sort"`
	CreatedAt string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回商品图片表名
// 返回值：
//   - string: 表名
func (ProductImage) TableName() string {
	return "product_images"
}
