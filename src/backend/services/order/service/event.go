package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jcbowen/market/src/backend/common/eventbus"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

// publishOrderPaidEvent 订单支付成功后向事件总线发布事件（尽力而为）
// 触发点唯一：UpdateOrderStatus 目标状态为 2（全部支付成功路径的收口，含到店单收款即完结）。
// 事件只做后台 WS 语音通知用，任何失败（订单回读、序列化、Redis 不可用）仅记日志，
// 不影响支付与状态流转主流程；Redis 客户端未注入时静默跳过
//
// 参数：
//   - ctx: 上下文（主流程事务已提交后调用，勿传入事务句柄）
//   - orderID: 订单 ID
//
// 异常：
//   - 订单回读失败 / Publish 失败仅记 [WARN] 日志，不向上返回错误
//
// 使用示例：
//
//	s.publishOrderPaidEvent(ctx, req.Id)
func (s *OrderService) publishOrderPaidEvent(ctx context.Context, orderID int64) {
	if s.redisClient == nil {
		return
	}
	var o model.Order
	if err := s.db.GetDb().WithContext(ctx).First(&o, orderID).Error; err != nil {
		log.Printf("[order] 支付成功事件发布失败（订单回读）: order_id=%d err=%v", orderID, err)
		return
	}
	payload, err := json.Marshal(eventbus.OrderPaidEvent{
		OrderNo:      o.OrderNo,
		CommunityID:  o.CommunityID,
		PayAmount:    o.PayAmount,
		DeliveryType: o.DeliveryType,
		Source:       o.Source,
		Status:       o.Status,
		PayTime:      derefString(o.PayTime),
	})
	if err != nil {
		log.Printf("[order] 支付成功事件序列化失败: order_id=%d err=%v", orderID, err)
		return
	}
	if err := s.redisClient.Publish(ctx, eventbus.ChannelOrderPaid, payload).Err(); err != nil {
		log.Printf("[order] 支付成功事件发布失败: order_no=%s err=%v", o.OrderNo, err)
	}
}

// publishRefundEvent 订单退款事件发布（尽力而为）
// 发布点仅两处：C 端 ApplyRefund 成功（action=apply，退款单待审核需后台处理）与
// completeRefundSuccess 统一收尾（action=completed，覆盖后台代客/审批同意/重试/调度器同步）。
// 货到付款未收款订单的直接取消无资金动作，不发布。
// 任何失败（订单回读、序列化、Redis 不可用）仅记日志，不影响退款主流程；
// Redis 客户端未注入时静默跳过
//
// 参数：
//   - ctx: 上下文（主流程事务已提交后调用，勿传入事务句柄）
//   - orderID: 订单 ID
//   - refundNo: 退款单号
//   - amount: 本笔退款金额（元）
//   - action: 事件动作（eventbus.RefundActionApply / RefundActionCompleted）
//   - operatorType: 操作者类型 user/admin/system
//   - reason: 退款原因
//
// 异常：
//   - 订单回读失败 / Publish 失败仅记 [WARN] 日志，不向上返回错误
//
// 使用示例：
//
//	s.publishRefundEvent(ctx, orderID, refund.RefundNo, refund.Amount,
//	    eventbus.RefundActionApply, "user", req.Reason)
func (s *RefundService) publishRefundEvent(ctx context.Context, orderID int64, refundNo string, amount float64, action, operatorType, reason string) {
	if s.redisClient == nil {
		return
	}
	var o model.Order
	if err := s.db.GetDb().WithContext(ctx).First(&o, orderID).Error; err != nil {
		log.Printf("[order] 退款事件发布失败（订单回读）: order_id=%d refund_no=%s err=%v", orderID, refundNo, err)
		return
	}
	payload, err := json.Marshal(eventbus.OrderRefundEvent{
		OrderNo:      o.OrderNo,
		CommunityID:  o.CommunityID,
		RefundNo:     refundNo,
		Amount:       amount,
		DeliveryType: o.DeliveryType,
		Source:       o.Source,
		Action:       action,
		OperatorType: operatorType,
		Reason:       reason,
		EventTime:    time.Now().Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		log.Printf("[order] 退款事件序列化失败: order_no=%s refund_no=%s err=%v", o.OrderNo, refundNo, err)
		return
	}
	if err := s.redisClient.Publish(ctx, eventbus.ChannelOrderRefund, payload).Err(); err != nil {
		log.Printf("[order] 退款事件发布失败: order_no=%s refund_no=%s err=%v", o.OrderNo, refundNo, err)
	}
}

// derefString 取字符串指针的值，nil 时返回空串
//
// 参数：
//   - p: 字符串指针
//
// 返回值：
//   - string: 指针指向的值或空串
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
