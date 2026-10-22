package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

// SyncChannelStateNoPayment 无可同步的渠道支付单（订单从未发起支付，或只有现金/货到付款单）
const SyncChannelStateNoPayment = "NO_PAYMENT"

// syncOutcome 一次「向渠道核实」的结果
// 字段与 proto SyncPaymentResp 一一对应，供 gRPC 层直接映射；
// 内部（取消前置确认）也复用同一结构，避免两套判定口径
type syncOutcome struct {
	Paid          bool
	Changed       bool
	ChannelState  string
	TransactionID string
	Amount        float64
	PaymentNo     string
	Message       string
}

// SyncPaymentFromChannel 主动向渠道核实一笔支付单的收款结果，已收款则补同步
// 三个调用方共用：后台「同步支付状态」按钮、订单取消前置确认（order 服务 RPC）、
// 以及排查问题时的手工验证；三条路径都以渠道为事实来源，本地状态只在渠道确认后改动
//
// 幂等与安全边界：
//   - 支付单已是成功/退款/失败态 → 不改任何状态，只回传渠道事实；
//   - 渠道未支付/无此交易 → 不改本地状态（待支付单继续等待回调或对账）；
//   - 渠道查询失败 → 明确返回 ERROR，调用方不得据此判定资金状态
//
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID（payment_no 为空时按订单取最近一笔待支付渠道单）
//   - paymentNo: 指定支付单号，可为空
//   - ref: 来源凭据（model.ConfirmRef* 常量），写入 payments.confirm_ref 并落流水
//
// 返回值：
//   - *syncOutcome: 核实结果
//   - error: 仅参数错误与数据库故障；渠道失败通过 ChannelState=ERROR 表达
//
// 使用示例：
//   - res, err := s.SyncPaymentFromChannel(ctx, 123, "", model.ConfirmRefManualSync)
func (s *PaymentService) SyncPaymentFromChannel(ctx context.Context, orderID int64, paymentNo, ref string) (*syncOutcome, error) {
	if orderID <= 0 && paymentNo == "" {
		return nil, bizerr.New(codes.InvalidArgument, "订单 ID 与支付单号至少填一个")
	}
	p, err := s.locateSyncTarget(ctx, orderID, paymentNo)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return &syncOutcome{ChannelState: SyncChannelStateNoPayment, Message: "订单没有待同步的渠道支付单"}, nil
	}
	outcome := &syncOutcome{ChannelState: "", TransactionID: p.TransactionID, Amount: p.Amount, PaymentNo: p.PaymentNo}

	// 已是成功态：重复点击按钮时不再打扰渠道，直接回传既有事实
	if p.Status == 2 {
		outcome.Paid = true
		outcome.ChannelState = ChannelStatePaid
		outcome.Message = "支付单已确认支付，无需同步"
		return outcome, nil
	}
	if p.Status != 1 {
		outcome.ChannelState = ChannelStateClosed
		outcome.Message = fmt.Sprintf("支付单状态 %d（非待支付），本次不改动", p.Status)
		return outcome, nil
	}

	res, queryErr := s.queryChannelOrder(ctx, p)
	if queryErr != nil || res == nil {
		outcome.ChannelState = ChannelStateError
		outcome.Message = "渠道查询失败，未能确认收款状态"
		if queryErr != nil {
			outcome.Message = fmt.Sprintf("渠道查询失败：%s", queryErr.Error())
		}
		entry := notifyEntryFrom(p, model.NotifyKindManualSync, model.NotifyResultError)
		entry.ErrorMsg = outcome.Message
		s.recordNotifyLog(ctx, entry)
		return outcome, nil
	}

	outcome.ChannelState = res.State
	outcome.Amount = res.Amount
	switch res.State {
	case ChannelStatePaid:
		outcome.Paid = true
		outcome.TransactionID = res.TransactionID
		if err := s.handlePaySuccess(ctx, &payCallbackParams{
			PaymentNo:     p.PaymentNo,
			TransactionID: res.TransactionID,
			Amount:        res.Amount,
			ConfirmSource: model.ConfirmSourceManual,
			ConfirmRef:    ref,
			NotifyKind:    model.NotifyKindManualSync,
			Channel:       p.PayType,
			OrderID:       p.OrderID,
		}); err != nil {
			outcome.Message = fmt.Sprintf("渠道已收款但补同步未成功：%s", err.Error())
			return outcome, nil
		}
		outcome.Changed = true
		outcome.Message = "渠道已收款，已补同步为支付成功"
	case ChannelStateNotExist:
		outcome.Message = "渠道无此交易，用户未发起支付"
		entry := notifyEntryFrom(p, model.NotifyKindManualSync, model.NotifyResultNotExist)
		entry.ErrorMsg = outcome.Message
		s.recordNotifyLog(ctx, entry)
	default:
		outcome.Message = fmt.Sprintf("渠道未收款（%s）", res.Raw)
		entry := notifyEntryFrom(p, model.NotifyKindManualSync, model.NotifyResultNotPaid)
		entry.ErrorMsg = outcome.Message
		s.recordNotifyLog(ctx, entry)
	}
	return outcome, nil
}

// locateSyncTarget 定位待核实的支付单
// 指定 payment_no 时按单号取；只给订单 ID 时取该订单最近一笔渠道待支付单
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - paymentNo: 支付单号，可为空
//
// 返回值：
//   - *model.Payment: 目标支付单，不存在返回 nil
//   - error: 数据库错误
func (s *PaymentService) locateSyncTarget(ctx context.Context, orderID int64, paymentNo string) (*model.Payment, error) {
	var p model.Payment
	query := s.db.GetDb().WithContext(ctx)
	if paymentNo != "" {
		if err := query.Where("payment_no = ?", paymentNo).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil
			}
			return nil, err
		}
		return &p, nil
	}
	// 现金/货到付款没有渠道可问，排除后即为「可同步」集合（付款码 5/6 与同渠道在线支付共用查单）
	if err := query.Where("order_id = ? AND pay_type IN ? AND status = 1", orderID, []int32{1, 2, 5, 6}).
		Order("id desc").First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// MarkPaymentPaidManually 人工标记指定支付单为已支付
// 「渠道同步不上但钱确实到账」的最后一条出路：不查渠道，凭人工在商户平台核实到的渠道交易号确认。
// 只对 status=1 的待支付单生效；订单侧流转依赖 handlePaySuccess 的 RPC，
// 订单已取消（6→2 非法）时支付单会成功而订单不同步，属于需要退款的挂账，
// 因此这里限定订单必须仍是待支付——该约束由调用方（网关读订单状态）与服务层双重保证。
//
// 参数：
//   - ctx: 上下文
//   - paymentNo: 被确认的支付单号
//   - transactionID: 渠道交易号（人工抄自商户平台，必填）
//   - amount: 渠道实际收款金额（必填，与应付金额做分位比对）
//   - remark: 人工核实依据（谁凭什么判定到账），写入流水原文列备查
//
// 返回值：
//   - error: 参数缺失、支付单不可确认或金额不符
//
// 使用示例：
//   - err := s.MarkPaymentPaidManually(ctx, "ZF...", "4200001234", 20.00, "商户平台单号核对 by admin:1")
func (s *PaymentService) MarkPaymentPaidManually(ctx context.Context, paymentNo, transactionID string, amount float64, remark string) error {
	if paymentNo == "" || transactionID == "" || amount <= 0 {
		return bizerr.New(codes.InvalidArgument, "请填写渠道交易号与实际收款金额")
	}
	if strings.TrimSpace(remark) == "" {
		return bizerr.New(codes.InvalidArgument, "请填写人工核实依据")
	}
	var p model.Payment
	if err := s.db.GetDb().WithContext(ctx).Where("payment_no = ?", paymentNo).First(&p).Error; err != nil {
		// 单号查不到与数据库故障要分开：前者是运营抄错单号（可在页面上立刻改正），
		// 后者是服务故障，都归为「不存在」会把故障误导成数据问题
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return bizerr.New(codes.NotFound, fmt.Sprintf("支付单 %s 不存在", paymentNo))
		}
		return bizerr.New(codes.Internal, "支付单查询失败，请稍后重试")
	}
	switch p.PayType {
	case 1, 2, 5, 6:
	default:
		return bizerr.New(codes.FailedPrecondition, "现金/货到付款支付单无需人工标记")
	}
	if p.Status != 1 {
		return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("支付单状态为 %d，仅待支付单可人工标记", p.Status))
	}
	if yuanToFen(amount) != yuanToFen(p.Amount) {
		return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("人工填写金额 %.2f 与应付 %.2f 不一致，请改用退款流程", amount, p.Amount))
	}
	return s.handlePaySuccess(ctx, &payCallbackParams{
		PaymentNo:     paymentNo,
		TransactionID: transactionID,
		Amount:        amount,
		ConfirmSource: model.ConfirmSourceManual,
		ConfirmRef:    model.ConfirmRefManualMark,
		NotifyKind:    model.NotifyKindManualSync,
		Channel:       p.PayType,
		OrderID:       p.OrderID,
		Remark:        remark,
	})
}

// closeChannelTrade 关闭渠道侧未支付交易并留痕
// 语义边界（微信/支付宝一致）：渠道只允许关闭未支付交易，对已付款交易返回业务失败且不会自动退款，
// 所以本方法只在「渠道明确未收款」后调用；关单失败不影响本地关单，钱的事由对账调度器继续跟
//
// 参数：
//   - ctx: 上下文
//   - p: 支付单
//   - res: 刚查到的渠道状态（写进流水摘要，省去二次查询）
func (s *PaymentService) closeChannelTrade(ctx context.Context, p *model.Payment, res *channelPayResult) {
	msg := fmt.Sprintf("渠道状态 %s", res.Raw)
	if err := s.closeChannelOrder(ctx, p); err != nil {
		entry := notifyEntryFrom(p, model.NotifyKindChannelClose, model.NotifyResultError)
		entry.ErrorMsg = fmt.Sprintf("%s，关闭渠道交易失败: %s", msg, err.Error())
		s.recordNotifyLog(ctx, entry)
		return
	}
	entry := notifyEntryFrom(p, model.NotifyKindChannelClose, model.NotifyResultClosed)
	entry.ErrorMsg = fmt.Sprintf("%s，已关闭渠道交易", msg)
	s.recordNotifyLog(ctx, entry)
}

// recordStuckPayment 登记一条资金挂账异常（渠道已收款而本地支付单已关闭/订单已取消）
// 同一支付单只保留一条待处理记录：对账任务每分钟都会扫到它，不去重会把异常页刷成噪声
//
// 参数：
//   - ctx: 上下文
//   - p: 支付单
//   - res: 渠道查询结果（提供渠道交易号与金额）
func (s *PaymentService) recordStuckPayment(ctx context.Context, p *model.Payment, res *channelPayResult) {
	if s.hasPendingStuckLog(ctx, p.PaymentNo) {
		return
	}
	msg := fmt.Sprintf("渠道已收款（交易号 %s，金额 %.2f）而支付单已关闭，需人工核对退款",
		res.TransactionID, res.Amount)
	log.Printf("[CRITICAL] 资金挂账 %s payment_no=%s order_id=%d", msg, p.PaymentNo, p.OrderID)
	entry := notifyEntryFrom(p, model.NotifyKindStuck, model.NotifyResultStuck)
	entry.ErrorMsg = msg
	s.recordNotifyLog(ctx, entry)
}

// hasPendingStuckLog 判断该支付单是否已有未销账的挂账记录
// 参数：
//   - ctx: 上下文
//   - paymentNo: 支付单号
//
// 返回值：
//   - bool: 存在则无需重复登记
func (s *PaymentService) hasPendingStuckLog(ctx context.Context, paymentNo string) bool {
	var count int64
	err := s.db.GetDb().WithContext(ctx).Model(&model.NotifyLog{}).
		Where("kind = ? AND payment_no = ? AND handled = ?", model.NotifyKindStuck, paymentNo, model.NotifyPending).
		Count(&count).Error
	if err != nil {
		log.Printf("[payment-notify] 查询挂账去重记录失败 payment_no=%s: %v", paymentNo, err)
		return false
	}
	return count > 0
}

// ToSyncPaymentResp 将核实结果映射为 proto 响应
// 参数：
//   - o: 核实结果
//
// 返回值：
//   - *payment.SyncPaymentResp: proto 响应
func ToSyncPaymentResp(o *syncOutcome) *payment.SyncPaymentResp {
	if o == nil {
		return nil
	}
	return &payment.SyncPaymentResp{
		Paid:          o.Paid,
		Changed:       o.Changed,
		ChannelState:  o.ChannelState,
		TransactionId: o.TransactionID,
		Amount:        o.Amount,
		PaymentNo:     o.PaymentNo,
		Message:       o.Message,
	}
}
