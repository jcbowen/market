package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// StockRecord 库存流水模型
// 记录 SKU 每次库存变动，包括入库、下单扣减、释放、盘点调整等
type StockRecord struct {
	base.MysqlBaseModel
	ID             int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	SkuID          int64   `gorm:"column:sku_id;type:bigint;not null;index;index:idx_stock_record_sku_created,priority:1;comment:SKU ID" json:"sku_id"`
	CommunityID    int64   `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	Type           int32   `gorm:"column:type;type:tinyint;not null;comment:1入库 2出库 3盘点 4释放" json:"type"`
	Quantity       int32   `gorm:"column:quantity;type:int;not null;comment:变动数量（正入负出）" json:"quantity"`
	BeforeStock    int32   `gorm:"column:before_stock;type:int;not null;comment:变动前库存" json:"before_stock"`
	AfterStock     int32   `gorm:"column:after_stock;type:int;not null;comment:变动后库存" json:"after_stock"`
	BatchID        int64   `gorm:"column:batch_id;type:bigint;not null;default:0;index;comment:批次 ID（0=无批次语境）" json:"batch_id"`
	BatchNo        string  `gorm:"column:batch_no;type:varchar(50);comment:批次号" json:"batch_no"`
	ProductionDate *string `gorm:"column:production_date;type:date;comment:生产日期" json:"production_date"`
	ExpireDate     *string `gorm:"column:expire_date;type:date;comment:过期日期" json:"expire_date"`
	RelatedOrderID int64   `gorm:"column:related_order_id;type:bigint;default:0;comment:关联订单 ID" json:"related_order_id"`
	RefNo          string  `gorm:"column:ref_no;type:varchar(30);default:'';comment:来源单据号（入库单号/出库单号等）" json:"ref_no"`
	OperatorID     int64   `gorm:"column:operator_id;type:bigint;default:0;comment:操作人（管理员 ID）" json:"operator_id"`
	Remark         string  `gorm:"column:remark;type:varchar(255);comment:备注" json:"remark"`
	CreatedAt      string  `gorm:"column:created_at;type:datetime;index;index:idx_stock_record_sku_created,priority:2;comment:创建时间" json:"created_at"`
}

// TableName 返回库存流水表名
// 返回值：
//   - string: 表名
func (StockRecord) TableName() string {
	return "stock_records"
}
