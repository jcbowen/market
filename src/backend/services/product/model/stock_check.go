package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// StockCheck 库存盘点模型
// 记录盘点单基本信息及状态
type StockCheck struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CheckNo     string `gorm:"column:check_no;type:varchar(50);not null;unique;comment:盘点单号" json:"check_no"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	OperatorID  int64  `gorm:"column:operator_id;type:bigint;default:0;comment:操作人 ID" json:"operator_id"`
	Status      int32  `gorm:"column:status;type:tinyint;default:1;comment:1待提交 2已提交" json:"status"`
	TotalItems  int32  `gorm:"column:total_items;type:int;default:0;comment:盘点商品种数" json:"total_items"`
	DiffItems   int32  `gorm:"column:diff_items;type:int;default:0;comment:差异商品种数" json:"diff_items"`
	Remark      string `gorm:"column:remark;type:varchar(255);comment:备注" json:"remark"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
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
	ID           int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CheckID      int64  `gorm:"column:check_id;type:bigint;not null;index;comment:盘点单 ID" json:"check_id"`
	SkuID        int64  `gorm:"column:sku_id;type:bigint;not null;index;comment:SKU ID" json:"sku_id"`
	SystemStock  int32  `gorm:"column:system_stock;type:int;not null;comment:系统库存" json:"system_stock"`
	ActualStock  int32  `gorm:"column:actual_stock;type:int;not null;comment:实盘库存" json:"actual_stock"`
	DiffQuantity int32  `gorm:"column:diff_quantity;type:int;not null;comment:差异数量（实盘 - 系统）" json:"diff_quantity"`
	CreatedAt    string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回盘点单明细表名
// 返回值：
//   - string: 表名
func (StockCheckItem) TableName() string {
	return "stock_check_items"
}
