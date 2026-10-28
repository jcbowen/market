package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// NotifyLog 支付通知与对账流水
// 记录「渠道通知/主动回查/人工同步」三类动作的完整轨迹，承担两个职责：
//  1. 取证：验签或解密失败的原始报文必须留痕，否则事后无法判断「渠道发过但被拒」还是「渠道根本没发」；
//  2. 出口：把散落在 stderr 的 [CRITICAL]（挂账、金额不符、放弃对账）变成可查询、可销账的队列。
//
// 注意：成功回调不存 raw_body（同一事实已落 payments 表），失败必存；
// headers 中的签名与证书序列号入库前需掩码。
//
//nolint:lll
//go:generate go tool gormgen -struct NotifyLog
type NotifyLog struct {
	base.MysqlBaseModel
	ID        int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	Kind      int32  `gorm:"column:kind;type:tinyint;not null;default:1;index:idx_notify_kind_result_created,priority:1;index:idx_notify_handled_created,priority:1;comment:1渠道回调 2对账补同步 3挂账检出(系统:回调/对账) 4渠道关单 5后台手动同步 6放弃对账 7人工恢复退款收敛 8挂账检出(后台人工核实) 9付款码扣款" json:"kind"`
	Result    int32  `gorm:"column:result;type:tinyint;not null;index:idx_notify_kind_result_created,priority:2;comment:1确认成功 2幂等忽略 3验签或解密失败 4金额不符 5处理异常 6渠道未支付 7渠道无此交易 8关单结果 9资金挂账 10订单同步失败 11人工恢复退款收敛" json:"result"`
	Channel   int32  `gorm:"column:channel;type:tinyint;not null;comment:1微信 2支付宝 0线下或未知（付款码 5/6 按渠道归一为 1/2）" json:"channel"`
	PaymentNo string `gorm:"column:payment_no;type:varchar(50);not null;default:'';index;comment:支付单号，验签失败时可能解析不出而为空" json:"payment_no"`
	OrderID   int64  `gorm:"column:order_id;type:bigint;not null;default:0;index;comment:订单 ID，同上可为 0" json:"order_id"`
	OrderNo   string `gorm:"column:order_no;type:varchar(50);not null;default:'';comment:订单号快照，order_id 解析不出时后台仍可定位" json:"order_no"`
	// CommunityID 归属社区，异常队列按社区隔离的唯一依据：取自支付单（创建时由订单带入）。
	// 验签失败等连支付单都解析不出的流水只能留 0，这类对普通管理员不可见、仅超管全量队列可见
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;default:0;index:idx_notify_community_handled,priority:1;comment:归属社区，0=未知（仅超管可见）" json:"community_id"`
	Handled     int32  `gorm:"column:handled;type:tinyint;not null;default:0;index:idx_notify_handled_created,priority:2;index:idx_notify_community_handled,priority:2;comment:异常销账 0待处理 1已处理" json:"handled"`
	ErrorMsg    string `gorm:"column:error_msg;type:varchar(500);comment:结果摘要或失败原因" json:"error_msg"`
	// DetectRemark 这条流水「是谁、在什么本地状态下」被登记（B3 审计留痕）：
	// 与 Remark 严格分工——后者只属于人工销账，销账 SQL 会整列覆写；
	// 若把检出时的操作人与原状态快照写进 Remark，销账一次就被抹掉，责任链断在销账那一刻
	DetectRemark string `gorm:"column:detect_remark;type:varchar(255);comment:检出留痕：触发人与原状态快照（系统触发标注为系统）" json:"detect_remark"`
	Remark       string `gorm:"column:remark;type:varchar(255);comment:人工销账备注（核实依据）" json:"remark"`
	RawBody      string `gorm:"column:raw_body;type:text;comment:原始通知报文，仅失败与挂账场景存储" json:"raw_body"`
	Headers      string `gorm:"column:headers;type:text;comment:关键请求头 JSON（签名已掩码）" json:"headers"`
	CreatedAt    string `gorm:"column:created_at;type:datetime;index:idx_notify_kind_result_created,priority:3;index:idx_notify_handled_created,priority:3;index:idx_notify_community_handled,priority:3;comment:创建时间" json:"created_at"`
	UpdatedAt    string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回支付通知流水表名
//
// 返回值：
//   - string: 表名
func (NotifyLog) TableName() string {
	return "payment_notify_logs"
}

// 流水类别（NotifyLog.Kind）
const (
	// NotifyKindCallback 渠道支付通知（回调进入支付服务即记录，含验签失败）
	NotifyKindCallback = 1
	// NotifyKindReconcile 对账任务主动回查后补同步成功
	NotifyKindReconcile = 2
	// NotifyKindStuck 挂账检出（支付单已关闭但渠道已收款，需人工退款）——系统自动检出（回调、对账调度器、关单前核实）
	NotifyKindStuck = 3
	// NotifyKindChannelClose 关闭渠道侧交易的结果
	NotifyKindChannelClose = 4
	// NotifyKindManualSync 后台人工同步/标记已支付
	NotifyKindManualSync = 5
	// NotifyKindGiveUp 回查次数超限，放弃对账
	NotifyKindGiveUp = 6
	// NotifyKindRefundReset 后台人工重置退款流水的收敛预算（留痕，不是待办）
	NotifyKindRefundReset = 7
	// NotifyKindManualStuckCheck 后台人工核实检出的挂账（B3）
	// 与 NotifyKindStuck 是同一事实的两个检出来源：3 由系统自动扫出（回调、对账调度器、关单前核实），8 由管理员点「同步支付状态」核实出。
	// 刻意拆开：待办里「谁先发现的」直接决定后续追查方向——系统检出说明回调/关单链路有洞，
	// 人工检出说明那笔单早已超窗调度器不再覆盖，两者的处置与统计口径不能混在一张表里
	NotifyKindManualStuckCheck = 8
	// NotifyKindCodePay 收银台付款码（B扫C）渠道扣款确认
	// 付款码支付没有异步通知（微信 V3 明确不走 notify_url），结果来自同步应答或回查，
	// 记成 kind=1 渠道回调会污染「回调丢失率」口径，故单列一类
	NotifyKindCodePay = 9
)

// 流水结果（NotifyLog.Result）
const (
	// NotifyResultSuccess 确认成功（支付单由未确认流转为已支付）
	NotifyResultSuccess = 1
	// NotifyResultIgnored 幂等忽略（已是终态或已支付，重复通知）
	NotifyResultIgnored = 2
	// NotifyResultVerifyFailed 验签或解密失败
	NotifyResultVerifyFailed = 3
	// NotifyResultAmountMismatch 金额与应付不一致，拒绝确认
	NotifyResultAmountMismatch = 4
	// NotifyResultError 处理异常（DB/RPC/渠道调用失败）
	NotifyResultError = 5
	// NotifyResultNotPaid 渠道确认未支付
	NotifyResultNotPaid = 6
	// NotifyResultNotExist 渠道无此交易（用户未发起支付）
	NotifyResultNotExist = 7
	// NotifyResultClosed 渠道关单结果（成功或业务不允许）
	NotifyResultClosed = 8
	// NotifyResultStuck 资金挂账：渠道已收款而本地支付单已关闭，必须人工退款
	NotifyResultStuck = 9
	// NotifyResultSyncFailed 支付已确认但订单状态同步失败（重试后仍失败），需人工触发同步
	NotifyResultSyncFailed = 10
	// NotifyResultRefundReset 人工重置退款流水收敛预算（操作留痕，不进待办队列）
	NotifyResultRefundReset = 11
)

// 销账状态（NotifyLog.Handled）
const (
	// NotifyPending 待人工处理（异常页队列）
	NotifyPending = 0
	// NotifyHandled 已处理/无需处理（正常流水默认置此位，不占用异常页）
	NotifyHandled = 1
)

// NeedsHandling 判断一条流水是否需要进入后台异常队列
// 只有「渠道发过但被拒」「挂账」「放弃对账」三类才是待办；
// 成功、幂等忽略、渠道明确未支付属于正常轨迹，落库备查但不打扰人工
//
// 参数：
//   - kind: NotifyLog.Kind
//   - result: NotifyLog.Result
//
// 返回值：
//   - bool: true 表示 handled 需置 0（待处理）
//
// 使用示例：
//   - entry.Handled = boolToInt(model.NeedsHandling(entry.Kind, entry.Result))
func NeedsHandling(kind, result int32) bool {
	// 金额不符与挂账不分来源都要人工：钱可能少收/多收，或已收而订单已取消，
	// 由对账或后台同步检出时同样不能留在队列里等人自己发现；
	// 订单同步失败同理：支付单已确认而订单未流转，不人工介入会一直悬着
	if result == NotifyResultAmountMismatch || result == NotifyResultStuck || result == NotifyResultSyncFailed {
		return true
	}
	switch kind {
	case NotifyKindStuck, NotifyKindManualStuckCheck, NotifyKindGiveUp:
		return true
	case NotifyKindCallback:
		return result == NotifyResultVerifyFailed || result == NotifyResultError
	default:
		return false
	}
}
