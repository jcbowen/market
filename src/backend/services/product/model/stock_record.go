package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// StockRecord 库存流水模型
// 记录 SKU 每次库存变动，包括入库、下单扣减、释放、盘点调整等
type StockRecord struct {
	base.MysqlBaseModel
	ID             int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	SkuID          int64  `gorm:"column:sku_id;type:bigint;not null;index" json:"sku_id"`
	CommunityID    int64  `gorm:"column:community_id;type:bigint;not null;index" json:"community_id"`
	Type           int32  `gorm:"column:type;type:tinyint;not null;comment:1入库 2出库 3盘点 4释放" json:"type"`
	Quantity       int32  `gorm:"column:quantity;type:int;not null" json:"quantity"`
	BeforeStock    int32  `gorm:"column:before_stock;type:int;not null" json:"before_stock"`
	AfterStock     int32  `gorm:"column:after_stock;type:int;not null" json:"after_stock"`
	BatchNo        string  `gorm:"column:batch_no;type:varchar(50)" json:"batch_no"`
	ProductionDate *string `gorm:"column:production_date;type:date" json:"production_date"`
	ExpireDate     *string `gorm:"column:expire_date;type:date" json:"expire_date"`
	RelatedOrderID int64   `gorm:"column:related_order_id;type:bigint;default:0" json:"related_order_id"`
	OperatorID     int64  `gorm:"column:operator_id;type:bigint;default:0" json:"operator_id"`
	Remark         string `gorm:"column:remark;type:varchar(255)" json:"remark"`
	CreatedAt      string `gorm:"column:created_at;type:datetime" json:"created_at"`
}

// TableName 返回库存流水表名
// 返回值：
//   - string: 表名
func (StockRecord) TableName() string {
	return "stock_records"
}
