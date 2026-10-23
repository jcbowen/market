package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// 采购单状态常量
const (
	PurchaseStatusDraft     int32 = 1 // 草稿
	PurchaseStatusOrdered   int32 = 2 // 已下单（在途）
	PurchaseStatusPartial   int32 = 3 // 部分入库
	PurchaseStatusCompleted int32 = 4 // 已完结（全部入库）
	PurchaseStatusCancelled int32 = 5 // 已取消
)

// PurchaseOrder 采购单模型
// 状态机：草稿 → 已下单 → 部分入库 → 已完结；草稿/已下单可取消（已入库的不可取消）
type PurchaseOrder struct {
	base.MysqlBaseModel
	ID          int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID int64   `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	PurchaseNo  string  `gorm:"column:purchase_no;type:varchar(30);not null;index;comment:采购单号（PO 前缀）" json:"purchase_no"`
	SupplierID  int64   `gorm:"column:supplier_id;type:bigint;not null;default:0;index;comment:供应商 ID（0=未指定）" json:"supplier_id"`
	Status      int32   `gorm:"column:status;type:tinyint;not null;default:1;comment:1草稿 2已下单 3部分入库 4已完结 5已取消" json:"status"`
	TotalAmount float64 `gorm:"column:total_amount;type:decimal(12,2);not null;default:0.00;comment:采购总金额" json:"total_amount"`
	OperatorID  int64   `gorm:"column:operator_id;type:bigint;not null;default:0;comment:创建人（管理员 ID）" json:"operator_id"`
	Remark      string  `gorm:"column:remark;type:varchar(255);default:'';comment:备注" json:"remark"`
	CreatedAt   string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回采购单表名
// 返回值：
//   - string: 表名
func (PurchaseOrder) TableName() string {
	return "purchase_orders"
}

// PurchaseOrderItem 采购单明细模型
// ReceivedQuantity 随入库单回写，全部明细收满时采购单流转为已完结
type PurchaseOrderItem struct {
	base.MysqlBaseModel
	ID               int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	PurchaseOrderID  int64   `gorm:"column:purchase_order_id;type:bigint;not null;index;comment:采购单 ID" json:"purchase_order_id"`
	SkuID            int64   `gorm:"column:sku_id;type:bigint;not null;index;comment:SKU ID" json:"sku_id"`
	Quantity         int32   `gorm:"column:quantity;type:int;not null;comment:采购数量" json:"quantity"`
	ReceivedQuantity int32   `gorm:"column:received_quantity;type:int;not null;default:0;comment:已入库数量" json:"received_quantity"`
	PurchasePrice    float64 `gorm:"column:purchase_price;type:decimal(10,2);not null;default:0.00;comment:采购单价" json:"purchase_price"`
	CreatedAt        string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回采购单明细表名
// 返回值：
//   - string: 表名
func (PurchaseOrderItem) TableName() string {
	return "purchase_order_items"
}
