package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// FlashSale 秒杀活动模型
// 对应秒杀活动表，按社区隔离
// 状态：0禁用 1启用
//
//nolint:lll
//go:generate go tool gormgen -struct FlashSale
type FlashSale struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index" json:"community_id"`
	Name        string `gorm:"column:name;type:varchar(100);not null" json:"name"`
	StartTime   string `gorm:"column:start_time;type:datetime;not null" json:"start_time"`
	EndTime     string `gorm:"column:end_time;type:datetime;not null" json:"end_time"`
	Status      int32  `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt   string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回秒杀活动表名
// 返回值：
//   - string: 表名
func (FlashSale) TableName() string {
	return "flash_sales"
}

// FlashSaleItem 秒杀活动商品项模型
// 记录秒杀活动包含的 SKU、秒杀价、库存、限购数量
//
//nolint:lll
//go:generate go tool gormgen -struct FlashSaleItem
type FlashSaleItem struct {
	base.MysqlBaseModel
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	FlashSaleID   int64   `gorm:"column:flash_sale_id;type:bigint;not null;index" json:"flash_sale_id"`
	SkuID         int64   `gorm:"column:sku_id;type:bigint;not null;index" json:"sku_id"`
	FlashPrice    float64 `gorm:"column:flash_price;type:decimal(10,2);not null" json:"flash_price"`
	Stock         int32   `gorm:"column:stock;type:int;default:0" json:"stock"`
	SoldCount     int32   `gorm:"column:sold_count;type:int;default:0" json:"sold_count"`
	LimitPerUser  int32   `gorm:"column:limit_per_user;type:int;default:1" json:"limit_per_user"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt     string  `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt     string  `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回秒杀活动商品项表名
// 返回值：
//   - string: 表名
func (FlashSaleItem) TableName() string {
	return "flash_sale_items"
}
