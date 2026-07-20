package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// StockCheck 库存盘点模型
// 记录盘点单基本信息及状态
type StockCheck struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	CheckNo     string `gorm:"column:check_no;type:varchar(50);not null;uniqueIndex" json:"check_no"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index" json:"community_id"`
	OperatorID  int64  `gorm:"column:operator_id;type:bigint;default:0" json:"operator_id"`
	Status      int32  `gorm:"column:status;type:tinyint;default:1;comment:1待提交 2已提交" json:"status"`
	TotalItems  int32  `gorm:"column:total_items;type:int;default:0" json:"total_items"`
	DiffItems   int32  `gorm:"column:diff_items;type:int;default:0" json:"diff_items"`
	Remark      string `gorm:"column:remark;type:varchar(255)" json:"remark"`
	CreatedAt   string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回盘点单表名
// 返回值：
//   - string: 表名
func (StockCheck) TableName() string {
	return "stock_checks"
}

// StockCheckItem 盘点单明细模型
// 记录每个 SKU 的盘点实际库存
type StockCheckItem struct {
	base.MysqlBaseModel
	ID           int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	CheckID      int64 `gorm:"column:check_id;type:bigint;not null;index" json:"check_id"`
	SkuID        int64 `gorm:"column:sku_id;type:bigint;not null;index" json:"sku_id"`
	SystemStock  int32 `gorm:"column:system_stock;type:int;not null" json:"system_stock"`
	ActualStock  int32 `gorm:"column:actual_stock;type:int;not null" json:"actual_stock"`
	DiffQuantity int32 `gorm:"column:diff_quantity;type:int;not null" json:"diff_quantity"`
	CreatedAt    string `gorm:"column:created_at;type:datetime" json:"created_at"`
}

// TableName 返回盘点单明细表名
// 返回值：
//   - string: 表名
func (StockCheckItem) TableName() string {
	return "stock_check_items"
}
