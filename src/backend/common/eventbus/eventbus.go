// Package eventbus 跨服务事件总线（Redis Pub/Sub）
// order 服务发布订单事件，gateway 订阅后经 WebSocket 推送给后台在线管理员；
// 频道名与载荷结构是两侧共享的契约，修改须同步 docs/需求文档/06-接口设计
package eventbus

import "encoding/json"

// 订单支付成功事件频道：order 服务发布，gateway 订阅后按社区过滤推送
const ChannelOrderPaid = "market:order:paid"

// 订单支付成功事件类型标识（WebSocket 消息体的 type 字段）
const MsgTypeOrderPaid = "order.paid"

// 订单退款事件频道：order 服务发布，gateway 订阅后按社区过滤推送
// 覆盖 C 端申请退款（待审核，需后台处理）与退款成功收尾（钱已退出）两类通知点；
// 货到付款未收款订单的直接取消不产生资金动作，不发此事件
const ChannelOrderRefund = "market:order:refund"

// 订单退款事件类型标识（WebSocket 消息体的 type 字段）
const MsgTypeOrderRefund = "order.refund"

// 退款事件动作：C 端申请（退款单待审核）
const RefundActionApply = "apply"

// 退款事件动作：退款成功（渠道/现金退款完成，钱已退出）
const RefundActionCompleted = "completed"

// OrderPaidEvent 订单支付成功事件载荷
// 所有支付成功路径（渠道回调、线下收款、付款码、后台补同步、调度器补同步）
// 都收口在 order 服务 UpdateOrderStatus 且目标状态为 2，事件在此统一发布
type OrderPaidEvent struct {
	// OrderNo 订单号
	OrderNo string `json:"order_no"`
	// CommunityID 订单归属社区 ID（gateway 按此过滤推送对象）
	CommunityID int64 `json:"community_id"`
	// PayAmount 实付金额（元）
	PayAmount float64 `json:"pay_amount"`
	// DeliveryType 配送方式 1自提 2配送 3到店
	DeliveryType int32 `json:"delivery_type"`
	// Source 订单来源 1C端用户 2收银台代客
	Source int32 `json:"source"`
	// Status 支付完成后的订单状态（到店单收款即完结为 5，其余为 2）
	Status int32 `json:"status"`
	// PayTime 支付时间（服务端格式 yyyy-MM-dd HH:mm:ss）
	PayTime string `json:"pay_time"`
}

// ParseOrderPaidEvent 反序列化订单支付成功事件
//
// 参数：
//   - payload: Redis Pub/Sub 消息体（JSON 字节）
//
// 返回值：
//   - *OrderPaidEvent: 事件载荷，payload 非法 JSON 时返回 nil
//
// 使用示例：
//
//	evt := eventbus.ParseOrderPaidEvent(msg.Payload)
//	if evt != nil { ... }
func ParseOrderPaidEvent(payload []byte) *OrderPaidEvent {
	var evt OrderPaidEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil
	}
	return &evt
}

// OrderRefundEvent 订单退款事件载荷
// 发布点：C 端 ApplyRefund 成功（action=apply，退款单待审核）与
// RefundService.completeRefundSuccess 统一收尾（action=completed，覆盖后台代客、
// 审批同意、重试退款、调度器同步等全部退款成功路径）
type OrderRefundEvent struct {
	// OrderNo 订单号
	OrderNo string `json:"order_no"`
	// CommunityID 订单归属社区 ID（gateway 按此过滤推送对象）
	CommunityID int64 `json:"community_id"`
	// RefundNo 退款单号
	RefundNo string `json:"refund_no"`
	// Amount 本笔退款金额（元）
	Amount float64 `json:"amount"`
	// DeliveryType 配送方式 1自提 2配送 3到店
	DeliveryType int32 `json:"delivery_type"`
	// Source 订单来源 1C端用户 2收银台代客
	Source int32 `json:"source"`
	// Action 事件动作 apply=申请退款(待审核) completed=退款成功
	Action string `json:"action"`
	// OperatorType 操作者类型 user=会员申请 admin=后台操作 system=系统自动（如72h自动同意）
	OperatorType string `json:"operator_type"`
	// Reason 退款原因
	Reason string `json:"reason"`
	// EventTime 事件时间（服务端格式 yyyy-MM-dd HH:mm:ss）
	EventTime string `json:"event_time"`
}

// ParseOrderRefundEvent 反序列化订单退款事件
//
// 参数：
//   - payload: Redis Pub/Sub 消息体（JSON 字节）
//
// 返回值：
//   - *OrderRefundEvent: 事件载荷，payload 非法 JSON 时返回 nil
//
// 使用示例：
//
//	evt := eventbus.ParseOrderRefundEvent(msg.Payload)
//	if evt != nil { ... }
func ParseOrderRefundEvent(payload []byte) *OrderRefundEvent {
	var evt OrderRefundEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil
	}
	return &evt
}

// 导出任务状态变更事件频道：order 服务在任务置「已完成/失败」后发布，
// gateway 订阅后按创建人管理员定向推送（导出是个人的后台动作，不该惊动同社区其他人），
// 用于让下载中心立即取文件，替代面板内的 3s 轮询
const ChannelExportTask = "market:export:task"

// 导出任务状态变更事件类型标识（WebSocket 消息体的 type 字段）
const MsgTypeExportTaskChanged = "export.task_changed"

// 导出任务事件状态：文件已生成，可下载
const ExportTaskStateDone = "done"

// 导出任务事件状态：生成失败，error_msg 为失败原因
const ExportTaskStateFailed = "failed"

// ExportTaskEvent 导出任务状态变更事件载荷
// 刻意不含文件路径：文件位置属服务端内部信息，下载仍走带归属校验的既有接口
type ExportTaskEvent struct {
	// TaskID 任务 ID
	TaskID int64 `json:"task_id"`
	// AdminID 任务创建人管理员 ID（gateway 按此定向推送）
	AdminID int64 `json:"admin_id"`
	// CommunityID 任务归属社区 ID（0=超管跨社区）
	CommunityID int64 `json:"community_id"`
	// ExportType 导出类型，当前仅 orders
	ExportType string `json:"export_type"`
	// State 事件状态（ExportTaskStateDone / ExportTaskStateFailed）
	State string `json:"state"`
	// FileName 下载文件名（完成态）
	FileName string `json:"file_name,omitempty"`
	// RowCount 导出数据行数（完成态）
	RowCount int64 `json:"row_count"`
	// ErrorMsg 失败原因；行数上限截断时复用该字段承载「部分导出」提示语
	ErrorMsg string `json:"error_msg,omitempty"`
	// EventTime 事件时间（服务端格式 yyyy-MM-dd HH:mm:ss）
	EventTime string `json:"event_time"`
}

// ParseExportTaskEvent 反序列化导出任务状态变更事件
//
// 参数：
//   - payload: Redis Pub/Sub 消息体（JSON 字节）
//
// 返回值：
//   - *ExportTaskEvent: 事件载荷，payload 非法 JSON 时返回 nil
//
// 使用示例：
//
//	evt := eventbus.ParseExportTaskEvent(msg.Payload)
//	if evt != nil { ... }
func ParseExportTaskEvent(payload []byte) *ExportTaskEvent {
	var evt ExportTaskEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil
	}
	return &evt
}

// 管理员权限变更事件频道：user 服务发布（角色增删改 / 任职调整 / 账号停启用），
// gateway 订阅后经 WebSocket 提醒在线管理员刷新本地权限快照（菜单/路由守卫）。
// 后端鉴权本就实时，此事件只消除前端观感滞后，属尽力而为的补充通知
const ChannelAdminPermissionChanged = "market:admin:permission_changed"

// 管理员权限变更事件类型标识（WebSocket 消息体的 type 字段）
const MsgTypeAdminPermissionChanged = "admin.permission_changed"

// AdminPermissionChangedEvent 管理员权限变更事件载荷
// 定向规则（gateway 侧，自上而下短路）：
//  1. Global 为真 → 全体在线管理员（全局角色改动，影响所有社区所有管理员）
//  2. AdminIDs 非空 → 按管理员定向推送（跨社区连接均可达）
//  3. 否则按 CommunityIDs 逐社区广播（如社区权限上限调整影响该社区全部在线管理员）
type AdminPermissionChangedEvent struct {
	// AdminIDs 直接受影响的管理员 ID 列表（任职调整、账号停启用），非空时优先按此定向
	AdminIDs []int64 `json:"admin_ids,omitempty"`
	// CommunityIDs 受影响的社区 ID 列表（角色增删改、社区权限上限调整）
	CommunityIDs []int64 `json:"community_ids,omitempty"`
	// Global 受影响范围是否为全部社区（全局角色增删改；此时 CommunityIDs 为空）
	Global bool `json:"global,omitempty"`
	// Reason 变更原因（供日志定位，不下发前端展示）
	Reason string `json:"reason,omitempty"`
}

// ParseAdminPermissionChangedEvent 反序列化管理员权限变更事件
//
// 参数：
//   - payload: Redis Pub/Sub 消息体（JSON 字节）
//
// 返回值：
//   - *AdminPermissionChangedEvent: 事件载荷，payload 非法 JSON 时返回 nil
func ParseAdminPermissionChangedEvent(payload []byte) *AdminPermissionChangedEvent {
	var evt AdminPermissionChangedEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil
	}
	return &evt
}

// 支付异常/挂账人工介入告警事件频道：payment 服务在异常流水落库后发布，
// gateway 订阅后按社区广播给在线管理员，让异常队列的待处理项即刻叫醒人（语音 + 列表刷新），
// 而不是等人自己打开异常页才发现。WS 不持久化，人不在后台时告警会永久错过，
// 权威仍是异常队列的 pending 计数本身，本事件只是提前叫醒
const ChannelPaymentAnomaly = "market:payment:anomaly"

// 支付异常/挂账人工介入告警事件类型标识（WebSocket 消息体的 type 字段）
const MsgTypePaymentAnomalyDetected = "payment.anomaly_detected"

// PaymentAnomalyEvent 支付异常/挂账人工介入告警事件载荷
// 只带定位凭据与金额，不带原始报文与对账轨迹：排查仍走带权限的异常队列接口
type PaymentAnomalyEvent struct {
	// Kind 流水类别（payment 服务 model.NotifyKind*：3 系统检出挂账 8 人工核实挂账 6 放弃对账）
	Kind int32 `json:"kind"`
	// OrderNo 订单号快照（流水解析不出订单时可能为空）
	OrderNo string `json:"order_no"`
	// PaymentNo 支付单号
	PaymentNo string `json:"payment_no"`
	// CommunityID 归属社区 ID（0=未知，仅超管全量队列可见）
	CommunityID int64 `json:"community_id"`
	// Amount 支付单金额（元）；支付单查不到时为 0
	Amount float64 `json:"amount"`
	// EventTime 事件时间（服务端格式 yyyy-MM-dd HH:mm:ss）
	EventTime string `json:"event_time"`
}

// ParsePaymentAnomalyEvent 反序列化支付异常告警事件
//
// 参数：
//   - payload: Redis Pub/Sub 消息体（JSON 字节）
//
// 返回值：
//   - *PaymentAnomalyEvent: 事件载荷，payload 非法 JSON 时返回 nil
func ParsePaymentAnomalyEvent(payload []byte) *PaymentAnomalyEvent {
	var evt PaymentAnomalyEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil
	}
	return &evt
}

// PermOrderView 数据类事件统一要求的权限点（与 middleware 权限目录同一字符串）。
// 定义在事件契约包里供 gateway/ws 生成权限映射：两侧共用一份口径，避免推送门槛与接口权限漂移
const PermOrderView = "order:view"

// DataClassMsgTypes 数据类消息类型清单：承载业务数据的事件全在此列
// （订单支付、订单退款、导出任务终态、支付异常告警）。
// gateway/ws 的 msgRequiredPerm 由本清单生成——新增数据类事件只需在此追加一行；
// 漏加会被 hub_test 的 TestMsgRequiredPermCoversAllDataClassTypes 判红，
// 不会出现「未在映射表登记即恒可接收」把业务数据下发给无权限连接的情况。
// 提醒类消息（admin.permission_changed、hello、ping）刻意不列入：
// 权限被收窄的管理员必须收到权限变更通知才能刷新本地快照，加门槛会让提醒对它自己失效
var DataClassMsgTypes = []string{
	MsgTypeOrderPaid,
	MsgTypeOrderRefund,
	MsgTypeExportTaskChanged,
	MsgTypePaymentAnomalyDetected,
}
