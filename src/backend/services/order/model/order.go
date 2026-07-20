package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Order 订单模型
// 对应订单主表，记录订单基本信息、金额、状态、配送信息等
// 状态：1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款
//
//nolint:lll
//go:generate go tool gormgen -struct Order
type Order struct {
	base.MysqlBaseModel
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	OrderNo       string  `gorm:"column:order_no;type:varchar(50);not null;uniqueIndex" json:"order_no"`
	CommunityID   int64   `gorm:"column:community_id;type:bigint;not null;index" json:"community_id"`
	UserID        int64   `gorm:"column:user_id;type:bigint;not null;index" json:"user_id"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;comment:1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款" json:"status"`
	TotalAmount   float64 `gorm:"column:total_amount;type:decimal(10,2);not null" json:"total_amount"`
	DiscountAmount float64 `gorm:"column:discount_amount;type:decimal(10,2);default:0" json:"discount_amount"`
	DeliveryFee   float64 `gorm:"column:delivery_fee;type:decimal(10,2);default:0" json:"delivery_fee"`
	PayAmount     float64 `gorm:"column:pay_amount;type:decimal(10,2);not null" json:"pay_amount"`
	DeliveryType  int32   `gorm:"column:delivery_type;type:tinyint;default:1;comment:1自提 2配送" json:"delivery_type"`
	DeliveryTime  string  `gorm:"column:delivery_time;type:varchar(50)" json:"delivery_time"`
	AddressID     int64   `gorm:"column:address_id;type:bigint;default:0" json:"address_id"`
	ContactName   string  `gorm:"column:contact_name;type:varchar(50)" json:"contact_name"`
	ContactPhone  string  `gorm:"column:contact_phone;type:varchar(20)" json:"contact_phone"`
	ContactAddress string `gorm:"column:contact_address;type:varchar(255)" json:"contact_address"`
	Remark        string  `gorm:"column:remark;type:varchar(255)" json:"remark"`
	CouponID      int64    `gorm:"column:coupon_id;type:bigint;default:0" json:"coupon_id"`
	PayTime       *string  `gorm:"column:pay_time;type:datetime" json:"pay_time"`
	DeliverTime   *string  `gorm:"column:deliver_time;type:datetime" json:"deliver_time"`
	CompleteTime  *string  `gorm:"column:complete_time;type:datetime" json:"complete_time"`
	CancelTime    *string  `gorm:"column:cancel_time;type:datetime" json:"cancel_time"`
	CancelReason  string   `gorm:"column:cancel_reason;type:varchar(255)" json:"cancel_reason"`
	CreatedAt     string   `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt     string   `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回订单表名
// 返回值：
//   - string: 表名
func (Order) TableName() string {
	return "orders"
}

// OrderItem 订单商品项模型
// 对应订单商品明细表
//
//nolint:lll
//go:generate go tool gormgen -struct OrderItem
type OrderItem struct {
	base.MysqlBaseModel
	ID           int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	OrderID      int64   `gorm:"column:order_id;type:bigint;not null;index" json:"order_id"`
	ProductID    int64   `gorm:"column:product_id;type:bigint;not null" json:"product_id"`
	SkuID        int64   `gorm:"column:sku_id;type:bigint;not null;index" json:"sku_id"`
	ProductName  string  `gorm:"column:product_name;type:varchar(100);not null" json:"product_name"`
	SpecName     string  `gorm:"column:spec_name;type:varchar(100)" json:"spec_name"`
	ProductImage string  `gorm:"column:product_image;type:varchar(255)" json:"product_image"`
	Price        float64 `gorm:"column:price;type:decimal(10,2);not null" json:"price"`
	Quantity     int32   `gorm:"column:quantity;type:int;not null" json:"quantity"`
	TotalPrice   float64 `gorm:"column:total_price;type:decimal(10,2);not null" json:"total_price"`
	CreatedAt    string  `gorm:"column:created_at;type:datetime" json:"created_at"`
}

// TableName 返回订单商品项表名
// 返回值：
//   - string: 表名
func (OrderItem) TableName() string {
	return "order_items"
}

// OrderStatusLog 订单状态流转日志模型
//
//nolint:lll
//go:generate go tool gormgen -struct OrderStatusLog
type OrderStatusLog struct {
	base.MysqlBaseModel
	ID           int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	OrderID      int64  `gorm:"column:order_id;type:bigint;not null;index" json:"order_id"`
	FromStatus   int32  `gorm:"column:from_status;type:tinyint;default:0" json:"from_status"`
	ToStatus     int32  `gorm:"column:to_status;type:tinyint;not null" json:"to_status"`
	OperatorType string `gorm:"column:operator_type;type:varchar(20)" json:"operator_type"`
	OperatorID   int64  `gorm:"column:operator_id;type:bigint;default:0" json:"operator_id"`
	Remark       string `gorm:"column:remark;type:varchar(255)" json:"remark"`
	CreatedAt    string `gorm:"column:created_at;type:datetime" json:"created_at"`
}

// TableName 返回订单状态日志表名
// 返回值：
//   - string: 表名
func (OrderStatusLog) TableName() string {
	return "order_status_logs"
}

// Refund 退款申请模型
// 状态：1待审核 2已同意 3已拒绝 4已退款
//
//nolint:lll
//go:generate go tool gormgen -struct Refund
type Refund struct {
	base.MysqlBaseModel
	ID           int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	RefundNo     string  `gorm:"column:refund_no;type:varchar(50);not null;uniqueIndex" json:"refund_no"`
	OrderID      int64   `gorm:"column:order_id;type:bigint;not null;index" json:"order_id"`
	UserID       int64   `gorm:"column:user_id;type:bigint;not null;index" json:"user_id"`
	Amount       float64 `gorm:"column:amount;type:decimal(10,2);not null" json:"amount"`
	Reason       string  `gorm:"column:reason;type:varchar(255)" json:"reason"`
	Status       int32   `gorm:"column:status;type:tinyint;default:1;comment:1待审核 2已同意 3已拒绝 4已退款" json:"status"`
	RejectReason string  `gorm:"column:reject_reason;type:varchar(255)" json:"reject_reason"`
	OperatorID   int64    `gorm:"column:operator_id;type:bigint;default:0" json:"operator_id"`
	RefundTime   *string  `gorm:"column:refund_time;type:datetime" json:"refund_time"`
	CreatedAt    string   `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt    string  `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回退款申请表名
// 返回值：
//   - string: 表名
func (Refund) TableName() string {
	return "refunds"
}
