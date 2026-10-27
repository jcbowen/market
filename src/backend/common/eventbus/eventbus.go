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

// 管理员权限变更事件频道：user 服务发布（角色增删改 / 任职调整 / 账号停启用），
// gateway 订阅后经 WebSocket 提醒在线管理员刷新本地权限快照（菜单/路由守卫）。
// 后端鉴权本就实时，此事件只消除前端观感滞后，属尽力而为的补充通知
const ChannelAdminPermissionChanged = "market:admin:permission_changed"

// 管理员权限变更事件类型标识（WebSocket 消息体的 type 字段）
const MsgTypeAdminPermissionChanged = "admin.permission_changed"

// AdminPermissionChangedEvent 管理员权限变更事件载荷
// 定向规则（gateway 侧）：AdminIDs 非空时按管理员定向推送（跨社区连接均可达）；
// 否则按 CommunityIDs 逐社区广播（如社区权限上限调整影响该社区全部在线管理员）
type AdminPermissionChangedEvent struct {
	// AdminIDs 直接受影响的管理员 ID 列表（任职调整、账号停启用），非空时优先按此定向
	AdminIDs []int64 `json:"admin_ids,omitempty"`
	// CommunityIDs 受影响的社区 ID 列表（角色增删改、社区权限上限调整）
	CommunityIDs []int64 `json:"community_ids,omitempty"`
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
