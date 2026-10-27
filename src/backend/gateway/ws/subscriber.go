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

// RunOrderEventSubscriber 订阅订单事件频道（支付成功 + 退款），按社区转发给在线管理员连接
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
	sub := rds.Subscribe(ctx, eventbus.ChannelOrderPaid, eventbus.ChannelOrderRefund, eventbus.ChannelAdminPermissionChanged)
	defer func() { _ = sub.Close() }()

	// 确认订阅建立后再进入消费循环，避免「以为在收、实际订阅未就绪」的静默丢失
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	log.Printf("[ws] 已订阅事件频道: %s, %s, %s",
		eventbus.ChannelOrderPaid, eventbus.ChannelOrderRefund, eventbus.ChannelAdminPermissionChanged)

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

// dispatchEvent 按频道解析事件并按社区广播
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
	case eventbus.ChannelAdminPermissionChanged:
		evt := eventbus.ParseAdminPermissionChangedEvent(payload)
		if evt == nil {
			log.Printf("[ws] 权限变更事件解析失败，已忽略: %s", string(payload))
			return
		}
		// 定向规则：AdminIDs 非空按管理员定向（跨社区连接均可达），
		// 否则按社区广播（社区权限上限调整影响该社区全部在线管理员）
		if len(evt.AdminIDs) > 0 {
			hub.BroadcastToAdmins(evt.AdminIDs, &Message{
				Type: eventbus.MsgTypeAdminPermissionChanged,
				Data: evt,
			})
			return
		}
		hub.BroadcastToCommunities(evt.CommunityIDs, &Message{
			Type: eventbus.MsgTypeAdminPermissionChanged,
			Data: evt,
		})
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
