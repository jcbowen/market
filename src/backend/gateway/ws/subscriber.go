package ws

import (
	"context"
	"log"
	"net"
	"strconv"
	"time"

	goredis "github.com/go-redis/redis/v8"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/eventbus"
)

// 订阅重连间隔：Redis 断连/订阅异常后的重试等待
const resubscribeDelay = 3 * time.Second

// RunOrderEventSubscriber 订阅后台实时通知的事件频道（订单支付成功、退款、导出任务终态、管理员权限变更），
// 按事件语义分别以社区广播 / 管理员定向 / 全员提醒转发给在线管理员连接
// 阻塞运行，应在独立 goroutine 中启动；ctx 取消后优雅退出。
// Redis 不可用时不阻断 gateway 启动：订阅失败按间隔重试，期间仅丢失实时通知（不补偿），
// 后台订单列表数据不受影响
//
// 参数：
//   - ctx: 生命周期上下文（gateway 退出时取消）
//   - cfg: 全局配置（取 Redis 连接参数）
//   - hub: WebSocket 集线器
//
// 异常：
//   - 订阅/接收错误仅记日志并重试，不向上返回
//
// 使用示例：
//
//	go ws.RunOrderEventSubscriber(ctx, cfg, hub)
func RunOrderEventSubscriber(ctx context.Context, cfg *config.Config, hub *Hub) {
	rds := newSubscriberClient(cfg)
	defer func() { _ = rds.Close() }()

	for {
		if ctx.Err() != nil {
			return
		}
		if err := subscribeOnce(ctx, rds, hub); err != nil && ctx.Err() == nil {
			log.Printf("[ws] 订单事件订阅中断，%s 后重试: %v", resubscribeDelay, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(resubscribeDelay):
			}
		}
	}
}

// subscribeOnce 发起一次订阅并阻塞消费，直到消息出错或 ctx 取消
//
// 参数：
//   - ctx: 生命周期上下文
//   - rds: Redis 客户端
//   - hub: WebSocket 集线器
//
// 返回值：
//   - error: 订阅或消费过程中的错误（ctx 取消时返回 nil）
func subscribeOnce(ctx context.Context, rds *goredis.Client, hub *Hub) error {
	sub := rds.Subscribe(ctx,
		eventbus.ChannelOrderPaid,
		eventbus.ChannelOrderRefund,
		eventbus.ChannelExportTask,
		eventbus.ChannelPaymentAnomaly,
		eventbus.ChannelAdminPermissionChanged,
	)
	defer func() { _ = sub.Close() }()

	// 确认订阅建立后再进入消费循环，避免「以为在收、实际订阅未就绪」的静默丢失
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	log.Printf("[ws] 已订阅事件频道: %s, %s, %s, %s, %s",
		eventbus.ChannelOrderPaid, eventbus.ChannelOrderRefund,
		eventbus.ChannelExportTask, eventbus.ChannelPaymentAnomaly,
		eventbus.ChannelAdminPermissionChanged)

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return errChannelClosed
			}
			dispatchEvent(hub, msg.Channel, []byte(msg.Payload))
		}
	}
}

// dispatchEvent 按频道解析事件并选择下发范围（社区广播 / 管理员定向 / 全员），
// 数据类事件的权限门槛统一在 hub 的 msgRequiredPerm 判定
//
// 参数：
//   - hub: WebSocket 集线器
//   - channel: Redis 频道名（决定事件类型）
//   - payload: 频道消息体
func dispatchEvent(hub *Hub, channel string, payload []byte) {
	switch channel {
	case eventbus.ChannelOrderPaid:
		evt := eventbus.ParseOrderPaidEvent(payload)
		if evt == nil {
			log.Printf("[ws] 订单支付事件解析失败，已忽略: %s", string(payload))
			return
		}
		hub.BroadcastToCommunity(evt.CommunityID, &Message{
			Type: eventbus.MsgTypeOrderPaid,
			Data: evt,
		})
	case eventbus.ChannelOrderRefund:
		evt := eventbus.ParseOrderRefundEvent(payload)
		if evt == nil {
			log.Printf("[ws] 订单退款事件解析失败，已忽略: %s", string(payload))
			return
		}
		hub.BroadcastToCommunity(evt.CommunityID, &Message{
			Type: eventbus.MsgTypeOrderRefund,
			Data: evt,
		})
	case eventbus.ChannelExportTask:
		evt := eventbus.ParseExportTaskEvent(payload)
		if evt == nil {
			log.Printf("[ws] 导出任务事件解析失败，已忽略: %s", string(payload))
			return
		}
		// 导出是创建人个人的后台动作，按管理员定向推送，不惊动同社区其他管理员；
		// 事件带任务 ID/行数/失败原因属业务数据，权限门槛由 msgRequiredPerm 统一判定
		hub.BroadcastToAdmins([]int64{evt.AdminID}, &Message{
			Type: eventbus.MsgTypeExportTaskChanged,
			Data: evt,
		})
	case eventbus.ChannelPaymentAnomaly:
		evt := eventbus.ParsePaymentAnomalyEvent(payload)
		if evt == nil {
			log.Printf("[ws] 支付异常事件解析失败，已忽略: %s", string(payload))
			return
		}
		// 异常队列按社区隔离（含 community_id=0 仅超管可见的口径），告警按社区广播；
		// 挂账金额/单号属业务数据，权限门槛由 msgRequiredPerm 统一判定
		hub.BroadcastToCommunity(evt.CommunityID, &Message{
			Type: eventbus.MsgTypePaymentAnomalyDetected,
			Data: evt,
		})
	case eventbus.ChannelAdminPermissionChanged:
		evt := eventbus.ParseAdminPermissionChangedEvent(payload)
		if evt == nil {
			log.Printf("[ws] 权限变更事件解析失败，已忽略: %s", string(payload))
			return
		}
		notify := &Message{Type: eventbus.MsgTypeAdminPermissionChanged, Data: evt}
		// 定向规则见 eventbus.AdminPermissionChangedEvent 注释：全局 → 全员；
		// 否则按管理员定向；再否则按社区广播
		// 提醒送达后一律断开受影响连接：连接里的权限快照是握手时定格的，
		// 不重连就仍按旧权限收数据类事件，而客户端 onclose 会自动重连并刷新快照
		switch {
		case evt.Global:
			hub.BroadcastAll(notify)
			log.Printf("[ws] 全局权限变更 reason=%q 已断开全部连接 %d 条", evt.Reason, hub.KickAll())
		case len(evt.AdminIDs) > 0:
			hub.BroadcastToAdmins(evt.AdminIDs, notify)
			log.Printf("[ws] 权限变更定向踢线 reason=%q 目标管理员=%v 断开连接=%d",
				evt.Reason, evt.AdminIDs, hub.KickAdmins(evt.AdminIDs))
		default:
			hub.BroadcastToCommunities(evt.CommunityIDs, notify)
			// 社区语境下的连接一律重连刷新：网关侧快照只含角色权限并集，
			// 社区权限上限变化不改并集，但保持「变更后即重连取最新上下文」的单一口径
			// 比按 adminID 逐个定向更完整（社区内管理员集合在服务端不可枚举）
			log.Printf("[ws] 权限变更社区广播 reason=%q 目标社区=%v 断开连接=%d",
				evt.Reason, evt.CommunityIDs, hub.KickCommunities(evt.CommunityIDs))
		}
	default:
		log.Printf("[ws] 未知的订单事件频道，已忽略: %s", channel)
	}
}

// newSubscriberClient 创建订阅用 Redis 客户端
// 与 login_guard 同口径直连 go-redis（jcbaseGo redis.New 建连失败会 panic），短超时快速失败
//
// 参数：
//   - cfg: 全局配置
//
// 返回值：
//   - *goredis.Client: Redis 客户端
func newSubscriberClient(cfg *config.Config) *goredis.Client {
	db, _ := strconv.Atoi(cfg.Redis.Db)
	return goredis.NewClient(&goredis.Options{
		Addr:        net.JoinHostPort(cfg.Redis.Host, cfg.Redis.Port),
		Password:    cfg.Redis.Password,
		DB:          db,
		DialTimeout: 3 * time.Second,
		ReadTimeout: 3 * time.Second,
	})
}

// errChannelClosed 频道通道被关闭的哨兵错误（触发外层重订阅）
var errChannelClosed = errChannel("redis channel closed")

// errChannel 简易静态错误类型
type errChannel string

// Error 实现 error 接口
//
// 返回值：
//   - string: 错误描述
func (e errChannel) Error() string { return string(e) }
