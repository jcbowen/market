package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Payment 支付记录模型
// 对应支付记录表
// 支付类型：1微信支付 2支付宝支付
// 状态：1待支付 2支付成功 3支付失败 4已退款
//
//nolint:lll
//go:generate go tool gormgen -struct Payment
type Payment struct {
	base.MysqlBaseModel
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	PaymentNo     string  `gorm:"column:payment_no;type:varchar(50);not null;uniqueIndex" json:"payment_no"`
	OrderID       int64   `gorm:"column:order_id;type:bigint;not null;index" json:"order_id"`
	UserID        int64   `gorm:"column:user_id;type:bigint;not null;index" json:"user_id"`
	PayType       int32   `gorm:"column:pay_type;type:tinyint;not null;comment:1微信支付 2支付宝支付" json:"pay_type"`
	Amount        float64 `gorm:"column:amount;type:decimal(10,2);not null" json:"amount"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;comment:1待支付 2支付成功 3支付失败 4已退款" json:"status"`
	TransactionID string   `gorm:"column:transaction_id;type:varchar(100)" json:"transaction_id"`
	PayParams     string   `gorm:"column:pay_params;type:text" json:"pay_params"`
	PayTime       *string  `gorm:"column:pay_time;type:datetime" json:"pay_time"`
	CreatedAt     string   `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt     string   `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回支付记录表名
// 返回值：
//   - string: 表名
func (Payment) TableName() string {
	return "payments"
}

// RefundPayment 退款记录模型
//
//nolint:lll
//go:generate go tool gormgen -struct RefundPayment
type RefundPayment struct {
	base.MysqlBaseModel
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	RefundNo      string  `gorm:"column:refund_no;type:varchar(50);not null;index" json:"refund_no"`
	PaymentNo     string  `gorm:"column:payment_no;type:varchar(50);not null;index" json:"payment_no"`
	OrderID       int64   `gorm:"column:order_id;type:bigint;not null;index" json:"order_id"`
	Amount        float64 `gorm:"column:amount;type:decimal(10,2);not null" json:"amount"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;comment:1待退款 2退款成功 3退款失败" json:"status"`
	TransactionID string  `gorm:"column:transaction_id;type:varchar(100)" json:"transaction_id"`
	RefundTime    string  `gorm:"column:refund_time;type:datetime" json:"refund_time"`
	CreatedAt     string  `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt     string  `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回退款记录表名
// 返回值：
//   - string: 表名
func (RefundPayment) TableName() string {
	return "refund_payments"
}
