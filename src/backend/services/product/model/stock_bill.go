package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// 入库单类型常量
const (
	StockInTypePurchase int32 = 1 // 采购入库
	StockInTypeDirect   int32 = 2 // 直接入库（无采购单）
)

// 出库单类型常量（后端枚举校验，禁止前端随意传值）
const (
	StockOutTypeLoss   int32 = 1 // 报损出库
	StockOutTypeUse    int32 = 2 // 领用出库
	StockOutTypeAdjust int32 = 3 // 调整出库
)

// StockInOrder 入库单模型
// 入库即生效（无草稿态）：创建时同步生成批次、回写 SKU 汇总库存与移动加权平均成本
type StockInOrder struct {
	base.MysqlBaseModel
	ID              int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID     int64   `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	InNo            string  `gorm:"column:in_no;type:varchar(30);not null;index;comment:入库单号（IN 前缀）" json:"in_no"`
	BizNo           string  `gorm:"column:biz_no;type:varchar(64);not null;default:'';index;comment:幂等业务号（客户端 UUID，防网络重试/双击重复入库）" json:"biz_no"`
	SupplierID      int64   `gorm:"column:supplier_id;type:bigint;not null;default:0;comment:供应商 ID（0=未指定）" json:"supplier_id"`
	PurchaseOrderID int64   `gorm:"column:purchase_order_id;type:bigint;not null;default:0;index;comment:关联采购单 ID（0=直接入库）" json:"purchase_order_id"`
	Type            int32   `gorm:"column:type;type:tinyint;not null;default:2;comment:1采购入库 2直接入库" json:"type"`
	TotalAmount     float64 `gorm:"column:total_amount;type:decimal(12,2);not null;default:0.00;comment:入库总金额" json:"total_amount"`
	OperatorID      int64   `gorm:"column:operator_id;type:bigint;not null;default:0;comment:操作人（管理员 ID）" json:"operator_id"`
	Remark          string  `gorm:"column:remark;type:varchar(255);default:'';comment:备注" json:"remark"`
	CreatedAt       string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt       string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回入库单表名
// 返回值：
//   - string: 表名
func (StockInOrder) TableName() string {
	return "stock_in_orders"
}

// StockInOrderItem 入库单明细模型
type StockInOrderItem struct {
	base.MysqlBaseModel
	ID             int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	InOrderID      int64   `gorm:"column:in_order_id;type:bigint;not null;index;comment:入库单 ID" json:"in_order_id"`
	SkuID          int64   `gorm:"column:sku_id;type:bigint;not null;index;comment:SKU ID" json:"sku_id"`
	Quantity       int32   `gorm:"column:quantity;type:int;not null;comment:入库数量" json:"quantity"`
	UnitCost       float64 `gorm:"column:unit_cost;type:decimal(10,2);not null;default:0.00;comment:入库单价（成本）" json:"unit_cost"`
	BatchNo        string  `gorm:"column:batch_no;type:varchar(50);default:'';comment:批次号" json:"batch_no"`
	ProductionDate *string `gorm:"column:production_date;type:date;comment:生产日期" json:"production_date"`
	ExpireDate     *string `gorm:"column:expire_date;type:date;comment:过期日期" json:"expire_date"`
	BatchID        int64   `gorm:"column:batch_id;type:bigint;not null;default:0;comment:生成的批次 ID" json:"batch_id"`
	CreatedAt      string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回入库单明细表名
// 返回值：
//   - string: 表名
func (StockInOrderItem) TableName() string {
	return "stock_in_order_items"
}

// StockOutOrder 出库单模型
// 销售出库由订单扣减链路自动产生流水，不经出库单；出库单仅覆盖报损/领用/调整
type StockOutOrder struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	OutNo       string `gorm:"column:out_no;type:varchar(30);not null;index;comment:出库单号（OUT 前缀）" json:"out_no"`
	BizNo       string `gorm:"column:biz_no;type:varchar(64);not null;default:'';index;comment:幂等业务号（客户端 UUID，防重放出库）" json:"biz_no"`
	Type        int32  `gorm:"column:type;type:tinyint;not null;comment:1报损 2领用 3调整出库" json:"type"`
	OperatorID  int64  `gorm:"column:operator_id;type:bigint;not null;default:0;comment:操作人（管理员 ID）" json:"operator_id"`
	Remark      string `gorm:"column:remark;type:varchar(255);default:'';comment:备注" json:"remark"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回出库单表名
// 返回值：
//   - string: 表名
func (StockOutOrder) TableName() string {
	return "stock_out_orders"
}

// StockOutOrderItem 出库单明细模型
type StockOutOrderItem struct {
	base.MysqlBaseModel
	ID         int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	OutOrderID int64  `gorm:"column:out_order_id;type:bigint;not null;index;comment:出库单 ID" json:"out_order_id"`
	SkuID      int64  `gorm:"column:sku_id;type:bigint;not null;index;comment:SKU ID" json:"sku_id"`
	Quantity   int32  `gorm:"column:quantity;type:int;not null;comment:出库数量" json:"quantity"`
	CreatedAt  string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回出库单明细表名
// 返回值：
//   - string: 表名
func (StockOutOrderItem) TableName() string {
	return "stock_out_order_items"
}
