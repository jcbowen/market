package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// StockBatch 批次库存模型
// 完整批次管理：每个入库/回补/盘盈动作生成批次行，扣减按 FEFO
// （有效期批次按过期日期升序先出，无有效期批次按入库顺序后出）。
// product_skus.stock 保留为汇总列（= Σ 在库批次 quantity），在同一事务内维护。
type StockBatch struct {
	base.MysqlBaseModel
	ID             int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID    int64   `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	SkuID          int64   `gorm:"column:sku_id;type:bigint;not null;index:idx_batch_sku_fefo,priority:1;comment:SKU ID" json:"sku_id"`
	BatchNo        string  `gorm:"column:batch_no;type:varchar(50);default:'';comment:批次号（可空，表示无批次信息）" json:"batch_no"`
	ProductionDate *string `gorm:"column:production_date;type:date;comment:生产日期" json:"production_date"`
	ExpireDate     *string `gorm:"column:expire_date;type:date;index:idx_batch_sku_fefo,priority:2;comment:过期日期（FEFO 排序依据，空排最后）" json:"expire_date"`
	Quantity       int32   `gorm:"column:quantity;type:int;not null;default:0;comment:剩余数量" json:"quantity"`
	UnitCost       float64 `gorm:"column:unit_cost;type:decimal(10,2);not null;default:0.00;comment:批次单位成本" json:"unit_cost"`
	Status         int32   `gorm:"column:status;type:tinyint;not null;default:1;comment:0已清零 1在库" json:"status"`
	SourceType     int32   `gorm:"column:source_type;type:tinyint;not null;default:1;comment:来源：1入库 2释放回补 3盘点盘盈" json:"source_type"`
	RefNo          string  `gorm:"column:ref_no;type:varchar(30);default:'';comment:来源单据号（入库单号/退款单号等）" json:"ref_no"`
	CreatedAt      string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt      string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回批次库存表名
// 返回值：
//   - string: 表名
func (StockBatch) TableName() string {
	return "stock_batches"
}
