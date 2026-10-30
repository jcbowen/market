package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Payment 支付记录模型
// 对应支付记录表
// 支付类型：1微信支付 2支付宝支付 3货到付款 4现金（收银台） 5微信付款码 6支付宝付款码
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
	// PendingFlag 待支付占位键：status=1 时=order_id，离开待支付（2/3/4/5）即置 NULL。
	// 配合该列的唯一索引把「同一订单至多一笔可扣款待支付单」下沉为库级约束
	// （MySQL 无部分索引，用可空列表达；NULL 不参与唯一冲突）。写入约定见
	// service.CreatePayment（置值）与 handlePaySuccess/ClosePayments/closePaymentLocally（清值）。
	// 索引名按本仓库约定由列级 unique tag 生成为 uni_payments_pending_flag；
	// 历史上用 uniqueIndex:uk_payments_order_pending 命名的旧索引由启动迁移
	// service.EnsurePaymentPendingFlagIndex 一次性删除（AutoMigrate 不会重命名既有索引）
	PendingFlag *int64 `gorm:"column:pending_flag;type:bigint;unique;comment:待支付占位键，status=1 时=order_id 否则 NULL" json:"pending_flag"`
	UserID      int64  `gorm:"column:user_id;type:bigint;not null;index;comment:用户 ID" json:"user_id"`
	// CommunityID 订单所属社区（创建支付单时由网关从订单带入），支付异常队列按社区隔离的唯一依据——
	// payment 服务不查 orders 表，跨服务归属只能在此落库
	CommunityID int64   `gorm:"column:community_id;type:bigint;not null;default:0;index;comment:订单所属社区 ID，0=未知（仅超管可见）" json:"community_id"`
	PayType     int32   `gorm:"column:pay_type;type:tinyint;not null;comment:1微信支付 2支付宝支付 3货到付款 4现金（收银台） 5微信付款码 6支付宝付款码；可回查/可对账的渠道口径为 {1,2,5,6}" json:"pay_type"`
	// ConfigID 使用的支付配置行 ID（pay_config 多行）：创建支付单时由网关按渠道绑定解析后带入，
	// 回调路由（多配置验签/验签公钥）、退款、查单、关单均按本列取对应配置的渠道客户端；
	// 现金/货到付款（3/4）无渠道交互恒为 0
	ConfigID  int64   `gorm:"column:config_id;type:bigint;not null;default:0;index;comment:支付配置 ID，0=线下/未关联" json:"config_id"`
	Amount    float64 `gorm:"column:amount;type:decimal(10,2);not null;comment:支付金额" json:"amount"`
	Status      int32   `gorm:"column:status;type:tinyint;default:1;index:idx_payments_status_created,priority:1;index:idx_payments_status_reconciled,priority:1;comment:1待支付 2支付成功 3支付失败 4已退款 5已关闭" json:"status"`
	// TransactionID 渠道交易号（支付宝 trade_no / 微信 transaction_id），渠道无关同列存放。
	// 后台「支付单号/渠道交易号」单框搜索按本列精确匹配，加普通索引（允许空串重复，不能 unique）
	TransactionID string  `gorm:"column:transaction_id;type:varchar(100);index;comment:第三方交易号" json:"transaction_id"`
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
// 渠道退款分两阶段落库：受理成功先写待退款(1)，渠道终态确认后置退款成功(2)/退款失败(3)；
// 微信为异步受理语义（PROCESSING 是常态），终态由退款查单任务 reconcileRefunds 收敛，
// 收敛过程用 RetryCount/Escalated 推进节流与转人工待办（见 service.scanPendingRefunds）
//
//nolint:lll
//go:generate go tool gormgen -struct RefundPayment
type RefundPayment struct {
	base.MysqlBaseModel
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	RefundNo      string  `gorm:"column:refund_no;type:varchar(50);not null;unique;comment:退款单号，唯一索引保证幂等" json:"refund_no"`
	PaymentNo     string  `gorm:"column:payment_no;type:varchar(50);not null;index;comment:关联支付单号" json:"payment_no"`
	OrderID       int64   `gorm:"column:order_id;type:bigint;not null;index;comment:订单 ID（以支付单归属为准，不取请求传入值）" json:"order_id"`
	Amount        float64 `gorm:"column:amount;type:decimal(10,2);not null;comment:退款金额" json:"amount"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;index:idx_refunds_status_updated,priority:1;comment:1待退款 2退款成功 3退款失败" json:"status"`
	TransactionID string  `gorm:"column:transaction_id;type:varchar(100);comment:第三方退款交易号" json:"transaction_id"`
	// RetryCount 退款查单收敛的连续失败次数：渠道查单失败或本地落库失败时 +1 并刷新 updated_at（节流推进），
	// 达到 service.refundQueryMaxRetry 即退出自动批次并转人工待办；渠道明确回答 PROCESSING 只刷新
	// updated_at 不计数（见 service.scanPendingRefunds / retryRefundQuery）。
	// 达上限后唯一的出路是后台人工核实渠道结果后调 ResetRefundFlow 归零（B2），流水重新被捞起继续收敛
	RetryCount int32 `gorm:"column:retry_count;type:int;not null;default:0;comment:退款查单收敛失败次数，超上限放弃收敛并转人工待办" json:"retry_count"`
	// Escalated 已转人工待办标记：0 未转办 1 已转办，仅作待办去重（一个流水只登记一条待办）。
	// 收敛任务在「查单失败超 refundQueryMaxRetry」或「待退款停留超 24 小时」时置 1；
	// 真正让流水退出自动批次的是 RetryCount 达上限（与支付侧「放弃对账」同口径），
	// 超期转办的在途微信退款仍会继续被渠道终态收敛，见 service.scanPendingRefunds
	Escalated  int32   `gorm:"column:escalated;type:tinyint;not null;default:0;comment:已转人工待办标记 0未转办 1已转办（待办去重）" json:"escalated"`
	RefundTime *string `gorm:"column:refund_time;type:datetime;comment:退款成功时间" json:"refund_time"`
	CreatedAt  string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt  string  `gorm:"column:updated_at;type:datetime;index:idx_refunds_status_updated,priority:2;comment:更新时间（退款查单节流指针）" json:"updated_at"`
}

// TableName 返回退款记录表名
// 返回值：
//   - string: 表名
func (RefundPayment) TableName() string {
	return "refund_payments"
}
