package service

import (
	"context"
	"log"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
)

// confirmRefCancelGuard 与 payment 服务约定的「取消前置确认」来源标记，
// 落到 payments.confirm_ref，便于事后区分这笔收款是被后台按钮捞回还是被取消流程拦下
const confirmRefCancelGuard = "cancel-guard"

// confirmPaidWithChannel 取消订单前向渠道确认是否已收款（P0-B′）
//
// 为什么需要：本地支付单没有成功记录，不等于用户没付钱——回调可能丢失或验签失败，
// 而订单一旦进入 6（已取消），状态机 `2: {1, 100}` 就不允许再回到已支付，
// 库存与优惠券已回补，只能人工退款。所以必须在取消事务之前问渠道，而不是事后补救。
//
// 失败口径：渠道查询失败按「未收款」放行取消，理由有二——
// 一是这与改造前的行为一致（此前根本没有渠道确认这一步），不会因渠道抖动导致超时单永不取消；
// 二是漏网的挂账会被 payment 对账任务检出并进入后台异常页，有兜底。
//
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - cfg: 全局配置，取消前置开关（business.payment_reconcile.cancel_guard）由此读取
//
// 返回值：
//   - bool: 渠道已收款；true 时支付服务已把订单补同步为已支付，调用方必须放弃取消
//
// 使用示例：
//   - if confirmPaidWithChannel(ctx, o.ID, s.cfg) { return errOrderAlreadyPaid }
func confirmPaidWithChannel(ctx context.Context, orderID int64, cfg *config.Config) bool {
	if !cfg.CancelGuardEnabled() {
		return false
	}
	if grpcclient.PaymentClient == nil {
		return false
	}
	resp, err := grpcclient.PaymentClient.SyncPaymentFromChannel(ctx, &payment.SyncPaymentReq{
		OrderId: orderID,
		Reason:  confirmRefCancelGuard,
	})
	if err != nil {
		log.Printf("[order] 取消前渠道确认失败 order_id=%d，按未收款继续取消: %v", orderID, err)
		return false
	}
	if resp == nil || !resp.Paid {
		return false
	}
	log.Printf("[order] 取消前渠道确认到已收款 order_id=%d payment_no=%s，已补同步为已支付",
		orderID, resp.PaymentNo)
	return true
}
