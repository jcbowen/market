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
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
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
	// 单号与订单绑定校验（P1-2）：网关只校验路径订单的社区归属，管不住 body/参数里的支付单号——
	// 在 A 订单页面提交 B 订单的单号，就能跨订单核实甚至补同步资金状态（含已确认单的订单修复）。
	// 与 codepay.PayByAuthCode:131 同口径；取消前置只传 orderID，不受影响
	if orderID > 0 && paymentNo != "" && p.OrderID != orderID {
		return nil, bizerr.New(codes.FailedPrecondition, "支付单与订单不匹配，请刷新订单信息后重试")
	}
	outcome := &syncOutcome{ChannelState: "", TransactionID: p.TransactionID, Amount: p.Amount, PaymentNo: p.PaymentNo}

	// 已是成功态：重复点击按钮时不再打扰渠道，直接回传既有事实
	if p.Status == 2 {
		outcome.Paid = true
		outcome.ChannelState = ChannelStatePaid
		// P0-3：支付单已确认而订单可能没同步上（回调二阶段曾三次 RPC 失败）。
		// 取消护栏依赖本入口的 Paid 结论拦截取消，只回事实不修复会让订单永远停在待支付，
		// 故按订单实态补一次同步（幂等：订单已流转则条件更新 0 行，无副作用）
		outcome.Message = s.ensureOrderPaidSynced(ctx, p)
		return outcome, nil
	}
	// 已关闭（订单取消/超时后关闭）：走挂账核实语义——渠道已收款则产出人工退款待办，
	// 未收款则记核实流水消除疑虑；与对账调度器 reconcileOne(closed=true) 同口径，
	// 只读渠道 + 写流水，不改任何资金状态（订单已取消，状态机不允许再流转，钱的事走商户平台人工退款）
	if p.Status == 5 {
		return s.verifyClosedPaymentFromChannel(ctx, p)
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
// 指定 payment_no 时按单号取；只给订单 ID 时按「待支付(1) > 已确认未同步(2) > 已关闭(5)」取最近一笔
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
	// 现金/货到付款没有渠道可问，排除后即为「可同步」集合（付款码 5/6 与同渠道在线支付共用查单）。
	// status=2 必须入候选（P0-3）：支付回调二阶段同步订单失败时，支付单已确认而订单仍停留
	// 待支付甚至已被取消——取消护栏靠本入口识别「钱已到账」，漏掉它会直接放行取消造成资损
	for _, st := range []int32{model.PaymentStatusPending, model.PaymentStatusSuccess, model.PaymentStatusClosed} {
		if err := query.Where("order_id = ? AND pay_type IN ? AND status = ?", orderID, []int32{1, 2, 5, 6}, st).
			Order("id desc").First(&p).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			continue
		}
		return &p, nil
	}
	// 无待支付单时回退最近一笔已关闭单：已取消订单的「渠道核实挂账」入口靠它定位
	// （订单已取消、支付单 status=5，此时核实的是「渠道是否已收款」，产出待办而非补同步）
	return nil, nil
}

// verifyClosedPaymentFromChannel 核实已关闭支付单的渠道收款结果（人工挂账检测入口）
// 与对账调度器 reconcileOne(closed=true) 同口径：渠道已收款 → recordStuckPayment 产出
// 人工退款待办（kind=3/result=9，含渠道交易号与金额，同一支付单已有待处理记录时去重）；
// 渠道明确未收款/无此交易/已关闭 → 记核实流水消除疑虑；渠道仍在途 → 留痕建议稍后再核。
// 只读渠道 + 写流水，不改任何资金状态——订单已取消后状态机不允许再流转，
// 钱的事只能走商户平台人工退款
// 参数：
//   - ctx: 上下文
//   - p: 已关闭（status=5）的支付单
//
// 返回值：
//   - *syncOutcome: 核实结论（paid=true 表示渠道已收款、需人工退款）
//   - error: 仅数据库故障
func (s *PaymentService) verifyClosedPaymentFromChannel(ctx context.Context, p *model.Payment) (*syncOutcome, error) {
	outcome := &syncOutcome{ChannelState: "", TransactionID: p.TransactionID, Amount: p.Amount, PaymentNo: p.PaymentNo}
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
		// 挂账事实入库（内置同单去重），异常页产出人工退款待办
		s.recordStuckPayment(ctx, p, res)
		outcome.Message = "渠道已收款而支付单已关闭（资金挂账），请到商户平台按渠道交易号人工退款"
	case ChannelStateNotExist, ChannelStateClosed:
		outcome.Message = fmt.Sprintf("渠道确认未收款（%s），无资金挂账", res.Raw)
		entry := notifyEntryFrom(p, model.NotifyKindManualSync, model.NotifyResultNotPaid)
		entry.ErrorMsg = outcome.Message
		s.recordNotifyLog(ctx, entry)
	default:
		// 渠道仍在途（WAIT_BUYER_PAY 等）：支付单已关闭而渠道未到终态，留痕待观察
		outcome.Message = fmt.Sprintf("渠道交易仍在途（%s），建议稍后再次核实", res.Raw)
		entry := notifyEntryFrom(p, model.NotifyKindManualSync, model.NotifyResultNotPaid)
		entry.ErrorMsg = outcome.Message
		s.recordNotifyLog(ctx, entry)
	}
	return outcome, nil
}

// MarkPaymentPaidManually 人工标记指定支付单为已支付
// 「渠道同步不上但钱确实到账」的最后一条出路：不查渠道，凭人工在商户平台核实到的渠道交易号确认。
// 只对 status=1 的待支付单生效；订单侧流转依赖 handlePaySuccess 的 RPC，
// 订单已取消（6→2 非法）时支付单会成功而订单不同步，属于需要退款的挂账，
// 因此这里限定订单必须仍是待支付——该约束由调用方（网关读订单状态）与服务层双重保证。
//
// 参数：
//   - ctx: 上下文
//   - orderID: 支付单应归属的订单 ID（>0 时强校验，与 codepay 同口径）
//   - paymentNo: 被确认的支付单号
//   - transactionID: 渠道交易号（人工抄自商户平台，必填）
//   - amount: 渠道实际收款金额（必填，与应付金额做分位比对）
//   - remark: 人工核实依据（谁凭什么判定到账），写入流水原文列备查
//
// 返回值：
//   - error: 参数缺失、支付单不可确认、单号与订单不绑定或金额不符
//
// 使用示例：
//   - err := s.MarkPaymentPaidManually(ctx, 123, "ZF...", "4200001234", 20.00, "商户平台单号核对 by admin:1")
func (s *PaymentService) MarkPaymentPaidManually(ctx context.Context, orderID int64, paymentNo, transactionID string, amount float64, remark string) error {
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
	// 订单绑定校验（P1-2）：网关只校验路径订单的社区归属，管不住 body 里的支付单号——
	// 在 A 订单页面提交 B 订单的单号就能跨订单（乃至跨社区）确认资金状态，必须服务层兜底
	if orderID > 0 && p.OrderID != orderID {
		return bizerr.New(codes.FailedPrecondition, "支付单与订单不匹配，请刷新订单信息后重试")
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
	if s.hasStuckLog(ctx, p.PaymentNo) {
		return
	}
	msg := fmt.Sprintf("渠道已收款（交易号 %s，金额 %.2f）而支付单已关闭，需人工核对退款",
		res.TransactionID, res.Amount)
	log.Printf("[CRITICAL] 资金挂账 %s payment_no=%s order_id=%d", msg, p.PaymentNo, p.OrderID)
	entry := notifyEntryFrom(p, model.NotifyKindStuck, model.NotifyResultStuck)
	entry.ErrorMsg = msg
	s.recordNotifyLog(ctx, entry)
}

// hasStuckLog 判断该支付单是否已有挂账记录（含已销账）
// 不按 handled=0 过滤：人工销账只代表「已知晓/已介入」，渠道侧资金未退回前
// 对账任务每分钟仍会扫到它——只去重待处理记录的话，销账后下一轮立即重新生成
// 同条待办并循环到回查上限放弃对账，销账动作被架空。按「存在即不再登记」口径，
// 真实归还以人工渠道退款为准，异常页历史记录保留可查
//
// 参数：
//   - ctx: 上下文
//   - paymentNo: 支付单号
//
// 返回值：
//   - bool: 已存在挂账记录则不再重复登记
func (s *PaymentService) hasStuckLog(ctx context.Context, paymentNo string) bool {
	var count int64
	err := s.db.GetDb().WithContext(ctx).Model(&model.NotifyLog{}).
		Where("kind = ? AND payment_no = ?", model.NotifyKindStuck, paymentNo).
		Count(&count).Error
	if err != nil {
		log.Printf("[payment-notify] 查询挂账去重记录失败 payment_no=%s: %v", paymentNo, err)
		return false
	}
	return count > 0
}

// orderStatusCancelled 订单已取消（与 order 服务 model.Order 的状态口径一致）
const orderStatusCancelled = 6

// paidFamilyStatuses 订单收款后状态族：已支付/备货中/配送中/已完成/退款中/已退款
// 支付单已确认（status=2）时订单应处于该族；停留 1/100 说明同步失败，6 说明订单已取消（资金挂账）
var paidFamilyStatuses = []int32{2, 3, 4, 5, 7, 8}

// inPaidFamily 订单状态是否属于收款后状态族
//
// 参数：
//   - status: 订单状态
//
// 返回值：
//   - bool: 是否已流转到收款后状态
func inPaidFamily(status int32) bool {
	for _, s := range paidFamilyStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// ensureOrderPaidSynced 支付单已确认（status=2）时，按订单实态补齐「订单已支付」（P0-3 修复入口）
// 取消前置护栏、后台「同步支付状态」按钮、对账扫描三方共用：护栏的 Paid 结论要拦住取消，
// 就必须保证订单真的同步上，否则订单会永远停在待支付，用户侧显示未付、后台显示已收
//
// 参数：
//   - ctx: 上下文
//   - p: 已确认支付（status=2）的支付单
//
// 返回值：
//   - string: 处理结论摘要，供调用方写入 outcome.Message
func (s *PaymentService) ensureOrderPaidSynced(ctx context.Context, p *model.Payment) string {
	if grpcclient.OrderClient == nil {
		return "支付单已确认支付，订单同步未校验（订单服务不可用）"
	}
	detail, err := grpcclient.OrderClient.GetOrder(ctx, &order.GetOrderReq{Id: p.OrderID})
	if err != nil || detail == nil || detail.Order == nil {
		return "支付单已确认支付，订单状态查询失败，请留意异常队列"
	}
	switch s.repairPaidOrderSync(ctx, p, detail.Order.Status) {
	case outcomeObserved:
		return "支付单已确认支付，订单已同步"
	case outcomeStuck:
		return "支付单已确认支付但订单已取消（资金挂账），请人工退款"
	case outcomeRescued:
		return "支付单已确认支付，订单状态已补齐"
	default:
		return "支付单已确认支付，订单补同步失败，请留意异常队列"
	}
}

// repairPaidOrderSync 按调用方已查到的订单状态执行补同步/挂账登记（ensureOrderPaidSynced 与对账扫描共用）
// 订单已在收款后状态族 → 无需动作；已取消(6) → 登记资金挂账待办；
// 仍停留待支付族(1/100) → 补同步为已支付（状态机 1/100→2 合法，条件更新幂等）；
// 状态未知(<0，查询失败) → 不妄动
//
// 参数：
//   - ctx: 上下文
//   - p: 已确认支付（status=2）的支付单
//   - orderStatus: 订单当前状态；<0 表示查询失败
//
// 返回值：
//   - reconcileOutcome: rescued=已补同步 / stuck=订单已取消转挂账待办 / failed=补同步失败 / observed=无需动作
func (s *PaymentService) repairPaidOrderSync(ctx context.Context, p *model.Payment, orderStatus int32) reconcileOutcome {
	switch {
	case orderStatus < 0:
		return outcomeFailed
	case inPaidFamily(orderStatus):
		return outcomeObserved
	case orderStatus == orderStatusCancelled:
		s.recordPaidButOrderCancelled(ctx, p)
		return outcomeStuck
	}
	if _, err := grpcclient.OrderClient.UpdateOrderStatus(ctx, &order.UpdateOrderStatusReq{
		Id:           p.OrderID,
		Status:       2,
		OperatorType: "system",
		Remark:       "支付成功（同步入口补同步）",
	}); err != nil {
		log.Printf("[CRITICAL] 订单 %d 已收款但补同步失败 payment_no=%s: %v", p.OrderID, p.PaymentNo, err)
		return outcomeFailed
	}
	log.Printf("[payment-sync] 订单 %d 已收款但状态未同步，已补为已支付 payment_no=%s", p.OrderID, p.PaymentNo)
	return outcomeRescued
}

// recordPaidButOrderCancelled 登记「支付单已确认而订单已取消」的资金挂账（P0-3）
// 与 recordStuckPayment 同族同去重（kind=3/result=9），但事实不同：那边是渠道已收款而支付单已关闭，
// 这边是本地已确认收款而订单走了取消——通常是取消护栏放行前的历史数据或同步竞态，同样只能人工退款
//
// 参数：
//   - ctx: 上下文
//   - p: 已确认支付（status=2）的支付单
func (s *PaymentService) recordPaidButOrderCancelled(ctx context.Context, p *model.Payment) {
	if s.hasStuckLog(ctx, p.PaymentNo) {
		return
	}
	msg := fmt.Sprintf("支付单已确认收款（交易号 %s，金额 %.2f）而订单已取消，需人工核对退款",
		p.TransactionID, p.Amount)
	log.Printf("[CRITICAL] 资金挂账 %s payment_no=%s order_id=%d", msg, p.PaymentNo, p.OrderID)
	entry := notifyEntryFrom(p, model.NotifyKindStuck, model.NotifyResultStuck)
	entry.ErrorMsg = msg
	s.recordNotifyLog(ctx, entry)
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
