package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// CommunityPricing 社区定价模型
// 记录 SKU 在特定社区的独立售价，用于差异化定价
type CommunityPricing struct {
	base.MysqlBaseModel
	ID          int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID int64   `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	SkuID       int64   `gorm:"column:sku_id;type:bigint;not null;index;comment:SKU ID" json:"sku_id"`
	Price       float64 `gorm:"column:price;type:decimal(10,2);not null;comment:社区专属价格" json:"price"`
	CreatedAt   string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回社区定价表名
// 返回值：
//   - string: 表名
func (CommunityPricing) TableName() string {
	return "community_pricing"
}
