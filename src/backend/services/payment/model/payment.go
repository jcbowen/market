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
	ID        int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	PaymentNo string `gorm:"column:payment_no;type:varchar(50);not null;unique;comment:支付单号，使用 UUID 生成" json:"payment_no"`
	OrderID   int64  `gorm:"column:order_id;type:bigint;not null;index;comment:订单 ID" json:"order_id"`
	// OrderNo 订单号快照（创建支付单时由网关随订单带入）：payment 服务不查 orders 表，
	// 异常流水要能在后台读出可核对的订单号只能在此落库
	OrderNo string `gorm:"column:order_no;type:varchar(50);not null;default:'';comment:订单号快照，供异常流水展示核对" json:"order_no"`
	UserID  int64  `gorm:"column:user_id;type:bigint;not null;index;comment:用户 ID" json:"user_id"`
	// CommunityID 订单所属社区（创建支付单时由网关从订单带入），支付异常队列按社区隔离的唯一依据——
	// payment 服务不查 orders 表，跨服务归属只能在此落库
	CommunityID   int64   `gorm:"column:community_id;type:bigint;not null;default:0;index;comment:订单所属社区 ID，0=未知（仅超管可见）" json:"community_id"`
	PayType       int32   `gorm:"column:pay_type;type:tinyint;not null;comment:1微信支付 2支付宝支付 3货到付款 4现金（收银台）" json:"pay_type"`
	Amount        float64 `gorm:"column:amount;type:decimal(10,2);not null;comment:支付金额" json:"amount"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;index:idx_payments_status_created,priority:1;index:idx_payments_status_reconciled,priority:1;comment:1待支付 2支付成功 3支付失败 4已退款 5已关闭" json:"status"`
	TransactionID string  `gorm:"column:transaction_id;type:varchar(100);comment:第三方交易号" json:"transaction_id"`
	PayParams     string  `gorm:"column:pay_params;type:text;comment:第三方支付调起参数 JSON" json:"pay_params"`
	PayTime       *string `gorm:"column:pay_time;type:datetime;comment:支付时间" json:"pay_time"`
	// ConfirmSource 支付确认的来源渠道：区分「渠道回调正常确认」与「对账补偿/人工同步捞回」，
	// 后者即回调丢失的计数来源；仅作旁证，不参与状态流转校验
	ConfirmSource int32 `gorm:"column:confirm_source;type:tinyint;not null;default:0;comment:确认来源 0未确认 1渠道回调 2对账补偿 3后台手动同步 4线下/收银台" json:"confirm_source"`
	// ConfirmRef 来源凭据：说明这条成功是被谁捞回的，取值为 ConfirmRef* 常量
	// 或对账批次串 reconcile:支付单号（见 service.reconcileConfirmRef）
	ConfirmRef string `gorm:"column:confirm_ref;type:varchar(64);comment:来源凭据 cancel-guard/manual-sync/manual-mark/reconcile:支付单号" json:"confirm_ref"`
	// 渠道回查节流：对账任务与挂账检测共用，避免同一支付单每分钟反复打扰渠道
	ReconcileCount   int32   `gorm:"column:reconcile_count;type:int;not null;default:0;comment:渠道回查次数，超上限后放弃对账" json:"reconcile_count"`
	LastReconciledAt *string `gorm:"column:last_reconciled_at;type:datetime;index:idx_payments_status_reconciled,priority:2;comment:最近一次渠道回查时间" json:"last_reconciled_at"`
	CreatedAt        string  `gorm:"column:created_at;type:datetime;index:idx_payments_status_created,priority:2;comment:创建时间" json:"created_at"`
	UpdatedAt        string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回支付记录表名
// 返回值：
//   - string: 表名
func (Payment) TableName() string {
	return "payments"
}

// 支付单状态（Payment.Status）
const (
	// PaymentStatusPending 待支付
	PaymentStatusPending int32 = 1
	// PaymentStatusSuccess 支付成功
	PaymentStatusSuccess int32 = 2
	// PaymentStatusFailed 支付失败
	PaymentStatusFailed int32 = 3
	// PaymentStatusRefunded 已退款
	PaymentStatusRefunded int32 = 4
	// PaymentStatusClosed 已关闭（订单取消/超时后关闭，迟到回调由终态兜底忽略）
	PaymentStatusClosed int32 = 5
)

// 支付确认来源（Payment.ConfirmSource）
// 仅作旁证，不参与状态流转校验：confirm_source != 1 的成功支付单即「回调丢失后被系统捞回」的计数来源
const (
	// ConfirmSourceNone 尚未确认支付
	ConfirmSourceNone = 0
	// ConfirmSourceCallback 渠道异步通知正常确认
	ConfirmSourceCallback = 1
	// ConfirmSourceReconcile 对账任务主动回查渠道后补同步
	ConfirmSourceReconcile = 2
	// ConfirmSourceManual 主动向渠道核实或人工确认后同步（后台「同步支付状态」、取消前置确认、
	// 人工「标记已支付」），具体场景由 ConfirmRef 前缀区分
	ConfirmSourceManual = 3
	// ConfirmSourceOffline 线下收款自动确认（现金/货到付款，无需第三方）
	ConfirmSourceOffline = 4
)

// ConfirmRef 前缀约定：标识「渠道同步」这一来源具体由谁触发，异常页与详情时间线据此展示
const (
	// ConfirmRefCancelGuard 订单取消前置确认捞回的支付单（钱已到账，订单不可取消）
	ConfirmRefCancelGuard = "cancel-guard"
	// ConfirmRefManualSync 后台人工点击「同步支付状态」捞回
	ConfirmRefManualSync = "manual-sync"
	// ConfirmRefManualMark 后台人工「标记已支付」（未问渠道，凭渠道交易号人工核实）
	ConfirmRefManualMark = "manual-mark"
)

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
