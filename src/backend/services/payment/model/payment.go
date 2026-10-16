package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Payment 支付记录模型
// 对应支付记录表
// 支付类型：1微信支付 2支付宝支付
// 状态：1待支付 2支付成功 3支付失败 4已退款 5已关闭（订单取消/超时后关闭，迟到回调由终态兜底忽略）
//
//nolint:lll
//go:generate go tool gormgen -struct Payment
type Payment struct {
	base.MysqlBaseModel
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	PaymentNo     string  `gorm:"column:payment_no;type:varchar(50);not null;unique;comment:支付单号，使用 UUID 生成" json:"payment_no"`
	OrderID       int64   `gorm:"column:order_id;type:bigint;not null;index;comment:订单 ID" json:"order_id"`
	UserID        int64   `gorm:"column:user_id;type:bigint;not null;index;comment:用户 ID" json:"user_id"`
	PayType       int32   `gorm:"column:pay_type;type:tinyint;not null;comment:1微信支付 2支付宝支付 3货到付款 4现金（收银台）" json:"pay_type"`
	Amount        float64 `gorm:"column:amount;type:decimal(10,2);not null;comment:支付金额" json:"amount"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;comment:1待支付 2支付成功 3支付失败 4已退款 5已关闭" json:"status"`
	TransactionID string   `gorm:"column:transaction_id;type:varchar(100);comment:第三方交易号" json:"transaction_id"`
	PayParams     string   `gorm:"column:pay_params;type:text;comment:第三方支付调起参数 JSON" json:"pay_params"`
	PayTime       *string  `gorm:"column:pay_time;type:datetime;comment:支付时间" json:"pay_time"`
	CreatedAt     string   `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt     string   `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
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
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	RefundNo      string  `gorm:"column:refund_no;type:varchar(50);not null;unique;comment:退款单号，唯一索引保证幂等" json:"refund_no"`
	PaymentNo     string  `gorm:"column:payment_no;type:varchar(50);not null;index;comment:关联支付单号" json:"payment_no"`
	OrderID       int64   `gorm:"column:order_id;type:bigint;not null;index;comment:订单 ID" json:"order_id"`
	Amount        float64 `gorm:"column:amount;type:decimal(10,2);not null;comment:退款金额" json:"amount"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;comment:1待退款 2退款成功 3退款失败" json:"status"`
	TransactionID string  `gorm:"column:transaction_id;type:varchar(100);comment:第三方退款交易号" json:"transaction_id"`
	RefundTime    *string `gorm:"column:refund_time;type:datetime;comment:退款成功时间" json:"refund_time"`
	CreatedAt     string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt     string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回退款记录表名
// 返回值：
//   - string: 表名
func (RefundPayment) TableName() string {
	return "refund_payments"
}
