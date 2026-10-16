package model

// StockOpLog 库存操作幂等日志模型
// 以 order_id + sku_id + op_type + biz_no 复合唯一索引约束同一业务操作：
// 同一业务号（退款单号/取消标记/下单补偿）对同一订单同一 SKU 的重复请求
// （如 gRPC 超时重试）只会生效一次；不同业务（多笔部分退款等）各自持有
// 独立 biz_no，互不吞并——修复旧模型按 order_id+sku_id 幂等导致同一订单
// 同一 SKU 第二笔部分退款释放被跳过的问题
type StockOpLog struct {
	ID        int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	OrderID   int64  `gorm:"column:order_id;type:bigint;not null;uniqueIndex:uk_stock_op_order_sku_biz;comment:关联订单 ID" json:"order_id"`
	SkuID     int64  `gorm:"column:sku_id;type:bigint;not null;uniqueIndex:uk_stock_op_order_sku_biz;comment:SKU ID" json:"sku_id"`
	OpType    int8   `gorm:"column:op_type;type:tinyint;not null;uniqueIndex:uk_stock_op_order_sku_biz;comment:操作类型 1扣减 2释放" json:"op_type"`
	BizNo     string `gorm:"column:biz_no;type:varchar(64);not null;default:'';uniqueIndex:uk_stock_op_order_sku_biz;comment:幂等业务号（退款单号/cancel:{orderID}/comp:{orderID}:{skuID}）" json:"biz_no"`
	Quantity  int32  `gorm:"column:quantity;type:int;not null;comment:本次操作数量" json:"quantity"`
	CreatedAt string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回库存操作幂等日志表名
// 返回值：
//   - string: 表名
func (StockOpLog) TableName() string {
	return "stock_op_logs"
}
