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
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	Name        string `gorm:"column:name;type:varchar(100);not null;comment:活动名称" json:"name"`
	StartTime   string `gorm:"column:start_time;type:datetime;not null;comment:开始时间" json:"start_time"`
	EndTime     string `gorm:"column:end_time;type:datetime;not null;comment:结束时间" json:"end_time"`
	Status      int32  `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
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
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	FlashSaleID   int64   `gorm:"column:flash_sale_id;type:bigint;not null;index;comment:秒杀活动 ID" json:"flash_sale_id"`
	SkuID         int64   `gorm:"column:sku_id;type:bigint;not null;index;comment:SKU ID" json:"sku_id"`
	FlashPrice    float64 `gorm:"column:flash_price;type:decimal(10,2);not null;comment:秒杀价" json:"flash_price"`
	Stock         int32   `gorm:"column:stock;type:int;default:0;comment:秒杀库存" json:"stock"`
	SoldCount     int32   `gorm:"column:sold_count;type:int;default:0;comment:已售数量" json:"sold_count"`
	LimitPerUser  int32   `gorm:"column:limit_per_user;type:int;default:1;comment:每人限购，默认1" json:"limit_per_user"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt     string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt     string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回秒杀活动商品项表名
// 返回值：
//   - string: 表名
func (FlashSaleItem) TableName() string {
	return "flash_sale_items"
}
