package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/eventbus"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

// refundConfigID 退款配置表固定单行主键
const refundConfigID = 1

// defaultCompletedRefundHours 已完成单售后窗口默认值（168 小时 = 7 天）；
// 配置行缺失或读取失败时兜底默认值，读到 0 视为显式关闭 C 端已完成单售后入口
const defaultCompletedRefundHours = 168

// invalidArg 构造用户输入/业务规则类退款错误（gRPC InvalidArgument），
// 网关按业务错误码透传原始文案，避免被归类为服务器内部错误吞掉提示
// 参数：
//   - format: 格式串
//   - args: 参数
//
// 返回值：
//   - error: 业务错误
func invalidArg(format string, args ...interface{}) error {
	return bizerr.New(codes.InvalidArgument, fmt.Sprintf(format, args...))
}

// refundFreightPolicies 运费退还策略合法值集合
var refundFreightPolicies = map[string]bool{"always": true, "never": true, "user_decide": true}

// ensureOrderCommunity 订单社区归属校验（多社区隔离强校验）
// 社区隔离防线：管理端退款链路（创建/审批/重试/拒绝）在服务端校验订单归属社区，
// 防止 A 社区管理员跨社区操作 B 社区订单；communityId<=0 视为内部系统调用（调度器等），跳过校验
// 参数：
//   - tx: 数据库连接（可为事务）
//   - orderID: 订单 ID
//   - communityId: 操作者所属社区 ID（>0 时启用校验）
//
// 返回值：
//   - error: 订单不存在或跨社区时返回错误
func ensureOrderCommunity(tx *gorm.DB, orderID, communityId int64) error {
	if communityId <= 0 {
		return nil
	}
	var o model.Order
	if err := tx.Select("id, community_id").First(&o, orderID).Error; err != nil {
		return err
	}
	if o.CommunityID != communityId {
		return bizerr.New(codes.PermissionDenied, "无权操作其他社区的订单")
	}
	return nil
}

// adminRefundable 判定订单当前是否允许发起后台代客退款
// 可退状态为 已支付(2)/备货中(3)/配送中(4)/已完成(5)：
//   - 到店单（delivery_type=3，收银台即买即走）在收款事务内被直接终态化为已完成(5)
//     （见 order.go UpdateOrderStatus 的 case 2 分支），这个 5 是自动终态而非用户确认收货；
//   - 自提/配送单的已完成(5) 为用户确认收货（或自动完成）后的终态，2026-10-25 起同样放行：
//     已完成单售后窗口仅约束 C 端自助申请，后台代客退款是商家主动行为，不受窗口限制，
//     由商家自行判断是否给超窗顾客退款（避免「顾客愿意协商、系统却拦住」的新客诉）。
//
// 参数：
//   - o: 订单模型（需含 status 与 delivery_type）
//
// 返回值：
//   - bool: true 表示允许发起代客退款
//
// 异常：
//   - 无（纯内存判定，不访问数据库）
//
// 使用示例：
//
//	if !adminRefundable(&o) {
//	    return invalidArg("订单状态不允许退款")
//	}
func adminRefundable(o *model.Order) bool {
	return o.Status >= 2 && o.Status <= 5
}

// RefundService 退款业务服务
type RefundService struct {
	db            *mysql.Instance
	paymentClient payment.PaymentServiceClient
	// redisClient 可选：退款事件经 Redis 发布到事件总线（WS 语音通知），未注入时静默跳过
	redisClient *redis.Client
}

// NewRefundService 创建退款业务服务
// 参数：
//   - db: MySQL 实例
//   - paymentClient: 支付服务 gRPC 客户端
//
// 返回值：
//   - *RefundService: 退款业务服务实例
func NewRefundService(db *mysql.Instance, paymentClient payment.PaymentServiceClient) *RefundService {
	return &RefundService{db: db, paymentClient: paymentClient}
}

// WithRedis 注入 Redis 客户端（可选，用于退款事件发布）
//
// 参数：
//   - client: Redis 客户端
//
// 返回值：
//   - *RefundService: 链式返回自身
func (s *RefundService) WithRedis(client *redis.Client) *RefundService {
	s.redisClient = client
	return s
}

// refundLine 汇算后的退款行
type refundLine struct {
	item   *model.OrderItem
	qty    int32
	amount float64
}

// ApplyRefund 申请退款
// 支持整单退（req.Items 为空）与部分退（req.Items 指定商品行与数量）；
// 金额以服务端汇算为准（行小计按比例分摊订单优惠，整单退尾差减法兜底），
// 客户端传入的 req.Amount 仅允许调低，调低差额从最后一行明细扣减；
// 运费仅整单退时按 refund_freight_policy 策略退还（user_decide 时按 req.RefundFreight 勾选）；
// 同一订单存在未完结退款申请（待审核/退款中）时禁止重复申请：
// 事务内 FOR UPDATE 锁订单行后再查 pending，串行化同一订单的并发申请，杜绝双申请同时通过校验
// 参数：
//   - ctx: 上下文
//   - req: 申请请求
//
// 返回值：
//   - *model.Refund: 退款模型（含明细）
//   - error: 错误信息
func (s *RefundService) ApplyRefund(ctx context.Context, req *order.ApplyRefundReq) (*model.Refund, error) {
	if req.Reason == "" {
		return nil, invalidArg("请填写退款原因")
	}

	// 支付单号在事务外获取（纯读查询，不参与竞态）
	paymentNo := ""
	if s.paymentClient != nil {
		if p, err := s.paymentClient.GetPayment(ctx, &payment.GetPaymentReq{OrderId: req.OrderId}); err == nil && p != nil {
			paymentNo = p.PaymentNo
		}
	}

	var refund *model.Refund
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// FOR UPDATE 锁订单行：并发申请在此排队，避免同时通过状态与 pending 校验
		var o model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&o, req.OrderId).Error; err != nil {
			return err
		}
		if o.UserID != req.UserId {
			return invalidArg("无权限申请退款")
		}
		// 门店订单（收银台代客 source=2）C 端只读：退款走后台入口（AdminCreateRefund）处理
		if o.Source == 2 {
			return bizerr.New(codes.PermissionDenied, "门店订单请联系门店处理")
		}
		// 常规可退状态：已支付(2)/备货中(3)/配送中(4)
		if o.Status != 2 && o.Status != 3 && o.Status != 4 {
			// 已完成单售后窗口（2026-10-25）：已完成(5)订单在完成后 N 小时内允许 C 端自助申请
			// （生鲜/包装品的差异化窗口待分类标记落地后启用，本期统一窗口，默认 7 天）；
			// 窗口为 0 表示关闭该入口，超窗引导联系门店协商
			if o.Status != 5 {
				return invalidArg("订单状态不允许申请退款")
			}
			hours := s.getCompletedRefundHours(ctx)
			if hours <= 0 {
				return invalidArg("已完成订单不支持在线申请售后，请联系门店协商处理")
			}
			if !completedAfterSaleOpen(&o, hours) {
				return invalidArg("已超出售后申请窗口（完成后%s内），请联系门店协商处理", completedSaleWindowText(hours))
			}
		}

		// 同一订单存在未完结退款申请时禁止重复申请（持行锁后检查，串行化生效）
		var pending int64
		if err := tx.Model(&model.Refund{}).
			Where("order_id = ? AND status IN ?", o.ID, []int32{1, 4}).
			Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return invalidArg("该订单存在未完结的退款申请，请等待处理完成")
		}

		calc, err := s.calcRefundOrder(ctx, &o, req.Items, req.Amount, req.RefundFreight, false)
		if err != nil {
			return err
		}
		lines, isFull, freight, amount := calc.lines, calc.isFull, calc.freight, calc.amount

		now := time.Now().Format("2006-01-02 15:04:05")
		refundType := int32(2)
		if isFull {
			refundType = 1
		}
		refund = &model.Refund{
			RefundNo:      generateRefundNo(),
			OrderID:       o.ID,
			UserID:        req.UserId,
			PaymentNo:     paymentNo,
			Amount:        amount,
			FreightAmount: freight,
			Type:          refundType,
			Reason:        req.Reason,
			Status:        1,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		for _, line := range lines {
			refund.Items = append(refund.Items, &model.RefundItem{
				OrderItemID:  line.item.ID,
				SkuID:        line.item.SkuID,
				ProductName:  line.item.ProductName,
				SpecName:     line.item.SpecName,
				Quantity:     line.qty,
				RefundAmount: line.amount,
				CreatedAt:    now,
			})
		}

		fromStatus := o.Status
		if err := tx.Create(refund).Error; err != nil {
			return err
		}
		// 明细一次批量插入：逐条 Create 在同一事务里发 N 次往返，多商品退款尤其明显
		for _, item := range refund.Items {
			item.RefundID = refund.ID
		}
		if len(refund.Items) > 0 {
			if err := tx.Create(&refund.Items).Error; err != nil {
				return err
			}
		}
		o.Status = 7
		o.UpdatedAt = now
		if err := tx.Save(&o).Error; err != nil {
			return err
		}
		refund.OrderNo = o.OrderNo
		return createStatusLogTx(tx, o.ID, fromStatus, 7, "user", req.UserId, "申请退款: "+req.Reason)
	}); err != nil {
		return nil, err
	}
	// 事务提交后尽力而为发布退款申请事件（WS 通知后台处理，失败不影响申请）
	s.publishRefundEvent(ctx, req.OrderId, refund.RefundNo, refund.Amount, eventbus.RefundActionApply, "user", req.Reason)
	return refund, nil
}

// AdminCreateRefund 后台代客退款（发起即执行，免审核）
// 整单退/部分退金额口径与 C 端申请一致（calcRefundOrder 服务端汇算）；
// 创建即置为退款中(4)并直接调用渠道退款，成功后走统一收尾（置已退款、回补库存、更新订单状态）；
// 渠道调用失败时退款单保留在退款中，由后台「重试退款」或调度器兜底；
// 事务内 FOR UPDATE 锁订单行后再查 pending，串行化同一订单的并发发起
// 参数：
//   - ctx: 上下文
//   - req: 后台代客退款请求
//
// 返回值：
//   - *model.Refund: 退款模型（含明细）
//   - error: 错误信息
func (s *RefundService) AdminCreateRefund(ctx context.Context, req *order.AdminCreateRefundReq) (*model.Refund, error) {
	if req.Reason == "" {
		return nil, invalidArg("请填写退款原因")
	}

	// 支付单号在事务外获取（纯读查询，不参与竞态）
	paymentNo := ""
	if s.paymentClient != nil {
		if p, err := s.paymentClient.GetPayment(ctx, &payment.GetPaymentReq{OrderId: req.OrderId}); err == nil && p != nil {
			paymentNo = p.PaymentNo
		}
	}

	var refund *model.Refund
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// FOR UPDATE 锁订单行：并发发起在此排队，避免同时通过状态与 pending 校验
		var o model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&o, req.OrderId).Error; err != nil {
			return err
		}
		// 社区隔离强校验：管理端代客退款发起即执行出款，服务端必须校验订单归属社区
		if err := ensureOrderCommunity(tx, o.ID, req.CommunityId); err != nil {
			return err
		}
		if !adminRefundable(&o) {
			return invalidArg("订单状态不允许退款")
		}

		// 存在未完结退款申请（待审核/退款中）时禁止重复发起（持行锁后检查，串行化生效）
		var pending int64
		if err := tx.Model(&model.Refund{}).
			Where("order_id = ? AND status IN ?", o.ID, []int32{1, 4}).
			Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return invalidArg("该订单存在未完结的退款申请，请等待处理完成")
		}

		// mode=2 仅金额退（不退商品、不回补库存）；mode 0/1 按商品退（空 items 整单退）
		amountOnly := req.Mode == 2
		calc, err := s.calcRefundOrder(ctx, &o, req.Items, req.Amount, req.RefundFreight, amountOnly)
		if err != nil {
			return err
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		refundType := int32(2)
		if calc.amountOnly {
			refundType = 3
		} else if calc.isFull {
			refundType = 1
		}
		refund = &model.Refund{
			RefundNo:      generateRefundNo(),
			OrderID:       o.ID,
			UserID:        o.UserID,
			PaymentNo:     paymentNo,
			Amount:        calc.amount,
			FreightAmount: calc.freight,
			Type:          refundType,
			Reason:        req.Reason,
			// 后台发起即执行：直接进入退款中，无需审核
			Status:    4,
			CreatedAt: now,
			UpdatedAt: now,
		}
		for _, line := range calc.lines {
			refund.Items = append(refund.Items, &model.RefundItem{
				OrderItemID:  line.item.ID,
				SkuID:        line.item.SkuID,
				ProductName:  line.item.ProductName,
				SpecName:     line.item.SpecName,
				Quantity:     line.qty,
				RefundAmount: line.amount,
				CreatedAt:    now,
			})
		}

		fromStatus := o.Status
		if err := tx.Create(refund).Error; err != nil {
			return err
		}
		// 明细一次批量插入：逐条 Create 在同一事务里发 N 次往返，多商品退款尤其明显
		for _, item := range refund.Items {
			item.RefundID = refund.ID
		}
		if len(refund.Items) > 0 {
			if err := tx.Create(&refund.Items).Error; err != nil {
				return err
			}
		}
		o.Status = 7
		o.UpdatedAt = now
		if err := tx.Save(&o).Error; err != nil {
			return err
		}
		refund.OrderNo = o.OrderNo
		return createStatusLogTx(tx, o.ID, fromStatus, 7, "admin", req.OperatorId, "后台代客退款: "+req.Reason)
	}); err != nil {
		return nil, err
	}

	if err := s.createRefundLog(ctx, refund.ID, "create", "admin", req.OperatorId, "后台代客退款: "+req.Reason); err != nil {
		return refund, err
	}

	// 发起即执行：直接发起渠道退款
	completed, err := s.processChannelRefund(ctx, refund)
	if err != nil {
		// 渠道失败：退款单保留在退款中，审批日志留痕，由「重试退款」兜底；
		// 对外返回业务语义错误（FailedPrecondition 透传文案），不吞成服务器内部错误
		_ = s.createRefundLog(ctx, refund.ID, "create", "admin", req.OperatorId, "渠道退款调用失败: "+err.Error())
		return refund, bizerr.New(codes.FailedPrecondition, "渠道退款调用失败，售后单已保留在退款中，可在售后管理页重试")
	}
	if !completed {
		// P0-2：渠道已受理未到终态（受理常态 PROCESSING）——退款单保持退款中(4)，
		// 不回补库存不改订单状态；终态由 payment 侧查单任务收敛、order 调度器同步收尾
		_ = s.createRefundLog(ctx, refund.ID, "create", "admin", req.OperatorId, "渠道已受理退款，等待退款结果同步")
		return refund, nil
	}

	if err := s.completeRefundSuccess(ctx, refund, refund.Items, "admin", req.OperatorId); err != nil {
		return refund, err
	}
	// 同步内存状态：completeRefundSuccess 只更新 DB，返回给网关的模型需反映最终状态
	refund.Status = 5
	_ = s.createRefundLog(ctx, refund.ID, "create", "admin", req.OperatorId, "后台代客退款成功")
	return refund, nil
}

// refundCalc 退款汇算结果（C 端申请与后台代客退款共用）
type refundCalc struct {
	lines   []*refundLine
	isFull  bool
	freight float64
	amount  float64
	// amountOnly 仅金额退（不退商品、不回补库存）
	amountOnly bool
}

// calcRefundOrder 汇算一笔退款的退款行、运费与最终金额
// 金额以实付口径（pay_amount）服务端汇算为准：剩余可退 = pay_amount - 已成功退款合计（含运费），
// 商品行金额按比例分摊订单优惠（含收银台手动优惠），整单退尾差减法兜底；
// reqAmount 仅允许调低，调低差额从最后一行明细扣减；
// mode=2 仅金额退：不退商品，amount 为必填的自定义金额（不超过剩余可退）；
// 运费仅整单退按 refund_freight_policy 策略退还（user_decide 时按 refundFreight 勾选），
// 且不超过剩余可退运费（配送费已含在 pay_amount 内）
// 参数：
//   - ctx: 上下文
//   - o: 订单
//   - reqItems: 退款商品明细（mode=1 时为空表示整单退）
//   - reqAmount: 客户端传入金额（按商品退时仅允许调低；仅金额退时为必填退款金额）
//   - refundFreight: user_decide 策略下是否退运费
//   - amountOnly: 是否仅金额退（mode=2）
//
// 返回值：
//   - *refundCalc: 汇算结果
//   - error: 校验失败时返回错误
func (s *RefundService) calcRefundOrder(ctx context.Context, o *model.Order, reqItems []*order.ApplyRefundItem, reqAmount float64, refundFreight bool, amountOnly bool) (*refundCalc, error) {
	// 剩余可退总额（money 口径）：实付 - 已成功退款合计（含运费），任何模式都不允许突破
	refundedMoney, refundedFreight, err := s.refundedMoneyStat(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	remaining := round2(o.PayAmount - refundedMoney)
	if remaining <= 0 {
		return nil, invalidArg("订单钱款已全部退还")
	}
	// 运费与商品部分分开封顶：运费仅在策略允许时退还，避免「不退运费」策略被 money 口径绕过
	freightRemaining := round2(o.DeliveryFee - refundedFreight)
	if freightRemaining < 0 {
		freightRemaining = 0
	}
	goodsRemaining := round2(remaining - freightRemaining)
	if goodsRemaining < 0 {
		goodsRemaining = 0
	}

	// 仅金额退：不涉及商品行与运费，金额为自定义值，上限=剩余可退
	if amountOnly {
		if len(reqItems) > 0 {
			return nil, invalidArg("仅金额退不支持指定退款商品")
		}
		if reqAmount <= 0 {
			return nil, invalidArg("请填写退款金额")
		}
		if round2(reqAmount) > remaining+1e-6 {
			return nil, invalidArg("退款金额不能超过剩余可退金额 %.2f 元", remaining)
		}
		return &refundCalc{lines: nil, isFull: false, freight: 0, amount: round2(reqAmount), amountOnly: true}, nil
	}

	var orderItems []*model.OrderItem
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ?", o.ID).Find(&orderItems).Error; err != nil {
		return nil, err
	}
	itemMap := make(map[int64]*model.OrderItem, len(orderItems))
	for _, item := range orderItems {
		itemMap[item.ID] = item
	}

	// 无商品明细订单（收银台「直接收款」）：items 为空即整单退，
	// 按剩余可退金额执行，不产生退款行、不回补库存；金额仍允许调低。
	// 直接收款单不收运费，remaining 即全部可退金额。
	// 非空 reqItems 仍走下方校验（商品不属于该订单会报错）
	if len(orderItems) == 0 && len(reqItems) == 0 {
		amount, isFull := itemlessRefundPlan(remaining, reqAmount)
		return &refundCalc{isFull: isFull, amount: amount}, nil
	}

	refundedQty, _, err := s.refundedStat(ctx, o.ID)
	if err != nil {
		return nil, err
	}

	lines, isFull, err := buildRefundLines(o, orderItems, itemMap, reqItems, refundedQty)
	if err != nil {
		return nil, err
	}

	// 运费：仅整单退按策略退还，且不超过剩余可退运费（配送费已含在 pay_amount 内）
	var freight float64
	if isFull && freightRemaining > 0 {
		freight = s.resolveFreight(ctx, o, isFull, refundFreight, freightRemaining)
	}

	// 商品部分应退金额：整单退以"剩余可退商品金额"为准（尾差在最后一行兜底）；
	// 部分退为各行比例分摊之和，超过剩余可退商品金额时按比例收敛（防多笔部分退累计突破实付）
	var goodsRefund float64
	if isFull {
		goodsRefund = goodsRemaining
		fixLastLineTailDiff(lines, goodsRefund)
	} else {
		goodsRefund = sumRefundLines(lines)
		if goodsRefund > goodsRemaining {
			scaleLinesToTarget(lines, goodsRemaining)
			goodsRefund = goodsRemaining
		}
	}

	// 客户端金额仅允许调低（按商品退时），调低差额从最后一行明细扣减
	refundable := round2(goodsRefund + freight)
	amount := refundable
	if reqAmount > 0 && reqAmount < refundable {
		amount = round2(reqAmount)
	}
	cut := round2(refundable - amount)
	if cut > 0 {
		if cut > goodsRefund+1e-6 {
			return nil, invalidArg("退款金额不能低于运费金额")
		}
		last := lines[len(lines)-1]
		if last.amount < cut {
			return nil, invalidArg("调整金额过低，最后一行商品应退不足抵扣")
		}
		last.amount = round2(last.amount - cut)
	}
	return &refundCalc{lines: lines, isFull: isFull, freight: freight, amount: amount}, nil
}

// itemlessRefundPlan 无商品明细订单（收银台直接收款）整单退的金额与类型
// 默认退还剩余可退全部金额，客户端传值仅允许调低（超上限按剩余可退兜底）；
// 类型按「本次是否把剩余可退退完」判定——调低未退完时属部分退（type=2），
// 恒按整单退展示会与「订单仍有剩余可退」自相矛盾
// 参数：
//   - remaining: 剩余可退金额（调用方已四舍五入到分）
//   - reqAmount: 客户端传入金额（0 表示不指定，按剩余可退全退）
//
// 返回值：
//   - float64: 本次退款金额（2位小数）
//   - bool: 是否整单退完（决定退款单类型 1/2）
func itemlessRefundPlan(remaining, reqAmount float64) (float64, bool) {
	remaining = round2(remaining)
	if reqAmount > 0 {
		if amount := round2(reqAmount); amount < remaining {
			return amount, false
		}
	}
	return remaining, true
}

// scaleLinesToTarget 将退款行金额按比例收敛到目标合计（最后一行兜底尾差）
// 用于部分退累计金额超出剩余可退时，防止突破实付口径
// 参数：
//   - lines: 退款行
//   - target: 目标合计
func scaleLinesToTarget(lines []*refundLine, target float64) {
	if len(lines) == 0 {
		return
	}
	sum := sumRefundLines(lines)
	if sum <= 0 {
		return
	}
	factor := target / sum
	var front float64
	for _, line := range lines[:len(lines)-1] {
		line.amount = round2(line.amount * factor)
		front += line.amount
	}
	last := lines[len(lines)-1]
	last.amount = round2(target - front)
	if last.amount < 0 {
		last.amount = 0
	}
}

// buildRefundLines 根据申请参数构造退款行并完成逐行汇算
// 整单退时取每行剩余可退数量；部分退时校验行归属与数量上限
// 参数：
//   - o: 订单
//   - orderItems: 订单商品行
//   - itemMap: 订单商品行索引（order_item_id -> 行）
//   - reqItems: 退款商品明细（为空表示整单退）
//   - refundedQty: 各商品行已退数量
//
// 返回值：
//   - []*refundLine: 退款行（金额为按比例分摊优惠后的应退金额）
//   - bool: 是否整单退
//   - error: 校验失败时返回错误
func buildRefundLines(o *model.Order, orderItems []*model.OrderItem, itemMap map[int64]*model.OrderItem, reqItems []*order.ApplyRefundItem, refundedQty map[int64]int32) ([]*refundLine, bool, error) {
	isFull := len(reqItems) == 0
	var lines []*refundLine

	if isFull {
		for _, item := range orderItems {
			rem := item.Quantity - refundedQty[item.ID]
			if rem <= 0 {
				continue
			}
			lines = append(lines, &refundLine{
				item:   item,
				qty:    rem,
				amount: itemRefundAmount(item, o, rem),
			})
		}
		if len(lines) == 0 {
			return nil, false, invalidArg("订单商品已全部退款")
		}
		return lines, true, nil
	}

	seen := make(map[int64]bool, len(reqItems))
	for _, ri := range reqItems {
		item, ok := itemMap[ri.OrderItemId]
		if !ok {
			return nil, false, invalidArg("退款商品不属于该订单")
		}
		if seen[ri.OrderItemId] {
			return nil, false, invalidArg("商品 %s 重复提交", item.ProductName)
		}
		seen[ri.OrderItemId] = true
		rem := item.Quantity - refundedQty[item.ID]
		if ri.Quantity <= 0 || ri.Quantity > rem {
			return nil, false, invalidArg("商品 %s 可退数量不足（剩余 %d）", item.ProductName, rem)
		}
		lines = append(lines, &refundLine{
			item:   item,
			qty:    ri.Quantity,
			amount: itemRefundAmount(item, o, ri.Quantity),
		})
	}
	return lines, false, nil
}

// itemRefundAmount 计算商品行指定退货数量的应退金额
// 行全额应退 = 行小计 - 行小计/订单商品总额 × 订单优惠总额（2位小数），
// 部分数量按 行全额应退 × (退货数量 / 行数量) 比例计算
// 参数：
//   - item: 订单商品行
//   - o: 所属订单
//   - qty: 退货数量
//
// 返回值：
//   - float64: 应退金额（2位小数）
func itemRefundAmount(item *model.OrderItem, o *model.Order, qty int32) float64 {
	full := itemFullRefundAmount(item, o)
	return round2(full * float64(qty) / float64(item.Quantity))
}

// itemFullRefundAmount 计算订单商品行全额应退金额（行小计减去按比例分摊的订单优惠）
// 优惠分摊口径 = 系统优惠(discount_amount) + 收银台手动优惠(manual_discount 抹零/整单改价)，
// 保证行金额合计与实付口径一致（收银台改价/抹零订单退款不超出原支付流水）
// 参数：
//   - item: 订单商品行
//   - o: 所属订单
//
// 返回值：
//   - float64: 行全额应退金额（2位小数）
func itemFullRefundAmount(item *model.OrderItem, o *model.Order) float64 {
	if o.TotalAmount <= 0 {
		return round2(item.TotalPrice)
	}
	totalDiscount := o.DiscountAmount + o.ManualDiscount
	share := round2(totalDiscount * item.TotalPrice / o.TotalAmount)
	if share > item.TotalPrice {
		share = item.TotalPrice
	}
	return round2(item.TotalPrice - share)
}

// sumRefundLines 汇总退款行金额
// 参数：
//   - lines: 退款行
//
// 返回值：
//   - float64: 合计金额（2位小数）
func sumRefundLines(lines []*refundLine) float64 {
	var sum float64
	for _, line := range lines {
		sum += line.amount
	}
	return round2(sum)
}

// fixLastLineTailDiff 整单退尾差兜底：最后一行金额 = 目标合计 - 前面各行之和
// 参数：
//   - lines: 退款行
//   - target: 商品部分应退目标合计
func fixLastLineTailDiff(lines []*refundLine, target float64) {
	if len(lines) == 0 {
		return
	}
	var front float64
	for _, line := range lines[:len(lines)-1] {
		front += line.amount
	}
	last := lines[len(lines)-1]
	last.amount = round2(target - front)
	if last.amount < 0 {
		last.amount = 0
	}
}

// refundedStat 统计订单已完成退款的各商品行已退数量与已退商品金额
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - map[int64]int32: order_item_id -> 已退数量
//   - float64: 已退商品金额合计（不含运费）
//   - error: 查询失败时返回错误
func (s *RefundService) refundedStat(ctx context.Context, orderID int64) (map[int64]int32, float64, error) {
	var items []*model.RefundItem
	err := s.db.GetDb().WithContext(ctx).
		Table("refund_items").
		Joins("JOIN refunds ON refunds.id = refund_items.refund_id").
		Where("refunds.order_id = ? AND refunds.status = 5", orderID).
		Select("refund_items.*").
		Find(&items).Error
	if err != nil {
		return nil, 0, err
	}

	qty := make(map[int64]int32)
	var goods float64
	for _, item := range items {
		qty[item.OrderItemID] += item.Quantity
		goods += item.RefundAmount
	}
	return qty, round2(goods), nil
}

// refundedMoneyStat 统计订单已完成退款的总金额与运费金额（money 口径，剩余可退计算用）
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - float64: 已成功退款总金额（含运费）
//   - float64: 已成功退还的运费合计
//   - error: 查询失败时返回错误
func (s *RefundService) refundedMoneyStat(ctx context.Context, orderID int64) (float64, float64, error) {
	var result struct {
		Total   float64
		Freight float64
	}
	err := s.db.GetDb().WithContext(ctx).Model(&model.Refund{}).
		Select("COALESCE(SUM(amount), 0) AS total, COALESCE(SUM(freight_amount), 0) AS freight").
		Where("order_id = ? AND status = ?", orderID, 5).
		Scan(&result).Error
	if err != nil {
		return 0, 0, err
	}
	return round2(result.Total), round2(result.Freight), nil
}

// resolveFreight 按运费退还策略解析本单应退运费
// 仅整单退涉及运费；user_decide 策略下按用户勾选结果退还；
// freightAvail 为剩余可退运费上限（配送费已含在 pay_amount，多笔退款不重复退运费）
// 参数：
//   - ctx: 上下文
//   - o: 订单
//   - isFull: 是否整单退
//   - userChoosesFreight: user_decide 策略下用户是否选择退运费
//   - freightAvail: 剩余可退运费上限
//
// 返回值：
//   - float64: 应退运费金额（2位小数）
func (s *RefundService) resolveFreight(ctx context.Context, o *model.Order, isFull bool, userChoosesFreight bool, freightAvail float64) float64 {
	if !isFull || o.DeliveryFee <= 0 || freightAvail <= 0 {
		return 0
	}
	switch s.getFreightPolicy(ctx) {
	case "always":
		return freightAvail
	case "user_decide":
		if userChoosesFreight {
			return freightAvail
		}
	}
	return 0
}

// getFreightPolicy 读取运费退还策略，读取失败或值非法时兜底 always（始终退运费）
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - string: 运费退还策略
func (s *RefundService) getFreightPolicy(ctx context.Context) string {
	var row model.RefundConfig
	if err := s.db.GetDb().WithContext(ctx).First(&row, refundConfigID).Error; err != nil {
		return "always"
	}
	if !refundFreightPolicies[row.RefundFreightPolicy] {
		return "always"
	}
	return row.RefundFreightPolicy
}

// getCompletedRefundHours 读取已完成单售后窗口（小时）
// 配置行不存在/读取失败时兜底 defaultCompletedRefundHours；
// 返回 0 为合法值，表示显式关闭已完成单的 C 端售后入口
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - int32: 窗口小时数（>=0）
func (s *RefundService) getCompletedRefundHours(ctx context.Context) int32 {
	var row model.RefundConfig
	if err := s.db.GetDb().WithContext(ctx).First(&row, refundConfigID).Error; err != nil {
		return defaultCompletedRefundHours
	}
	if row.CompletedRefundHours < 0 {
		return defaultCompletedRefundHours
	}
	return row.CompletedRefundHours
}

// parseOrderTime 解析订单时间字符串，兼容两种存储形态：
// "2006-01-02 15:04:05"（本服务写入格式）与 RFC3339/RFC3339Nano
// （GORM string 字段接 datetime 列读库时经 database/sql convertAssign 会输出该格式，
// 见 order 服务 ToPosShiftInfo.normalizeTime 同类处理）
// 参数：
//   - s: 时间字符串
//
// 返回值：
//   - time.Time: 解析结果
//   - error: 两种格式均解析失败时返回错误
func parseOrderTime(s string) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// completedAfterSaleOpen 判定已完成订单当前是否仍在售后窗口内
// 起算时间取 complete_time（确认收货/核销/自动完成时写入），
// 历史数据缺失时兜底 updated_at（宁可少算不放大窗口）；窗口不随部分退款重置
// 参数：
//   - o: 订单模型（需含 complete_time 与 updated_at）
//   - hours: 窗口小时数（>0，由调用方保证）
//
// 返回值：
//   - bool: true 表示仍在窗口内
func completedAfterSaleOpen(o *model.Order, hours int32) bool {
	base := o.UpdatedAt
	if o.CompleteTime != nil && *o.CompleteTime != "" {
		base = *o.CompleteTime
	}
	completedAt, err := parseOrderTime(base)
	if err != nil {
		// 起算时间不可解析时保守放行（completed 5 本身即合法售后对象），由后台审批兜底把关
		return true
	}
	return time.Since(completedAt) <= time.Duration(hours)*time.Hour
}

// completedSaleWindowText 窗口时长转展示文案：整 24 小时按「天」表述，其余按小时
// 参数：
//   - hours: 窗口小时数（>0）
//
// 返回值：
//   - string: 如「7 天」/「24 小时」
func completedSaleWindowText(hours int32) string {
	if hours > 0 && hours%24 == 0 {
		return fmt.Sprintf("%d 天", hours/24)
	}
	return fmt.Sprintf("%d 小时", hours)
}

// GetRefundConfig 查询退款配置
// 配置行缺失时自愈：插入默认配置行（always + 默认窗口）后返回，
// 与 getFreightPolicy/getCompletedRefundHours 的缺行兜底语义一致，
// 避免「读接口报错、写接口把默认值当 0 落库」的口径分叉
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - *model.RefundConfig: 退款配置
//   - error: 读取或初始化失败时返回错误
func (s *RefundService) GetRefundConfig(ctx context.Context) (*model.RefundConfig, error) {
	var row model.RefundConfig
	err := s.db.GetDb().WithContext(ctx).First(&row, refundConfigID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		now := time.Now().Format("2006-01-02 15:04:05")
		defaultRow := &model.RefundConfig{
			ID:                   refundConfigID,
			RefundFreightPolicy:  "always",
			CompletedRefundHours: defaultCompletedRefundHours,
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		if cerr := s.db.GetDb().WithContext(ctx).Create(defaultRow).Error; cerr != nil {
			return nil, fmt.Errorf("初始化默认退款配置失败: %w", cerr)
		}
		return defaultRow, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取退款配置失败: %w", err)
	}
	return &row, nil
}

// SaveRefundConfig 保存退款配置
// 参数：
//   - ctx: 上下文
//   - policy: 运费退还策略（always/never/user_decide）
//   - completedRefundHours: 已完成单售后窗口（小时），>=0，0=不允许已完成单申请售后
//
// 返回值：
//   - error: 策略非法或保存失败时返回错误
func (s *RefundService) SaveRefundConfig(ctx context.Context, policy string, completedRefundHours int32) error {
	if !refundFreightPolicies[policy] {
		return fmt.Errorf("运费退还策略不合法: %s", policy)
	}
	if completedRefundHours < 0 {
		return fmt.Errorf("已完成单售后窗口不合法: %d", completedRefundHours)
	}
	row, err := s.GetRefundConfig(ctx)
	if err != nil {
		return err
	}
	row.RefundFreightPolicy = policy
	row.CompletedRefundHours = completedRefundHours
	row.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	if err := s.db.GetDb().WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("保存退款配置失败: %w", err)
	}
	return nil
}

// EnsureDefaultRefundConfig 确保退款配置表存在默认配置行（始终退运费）
//
// 返回值：
//   - error: 初始化失败时返回错误
func (s *RefundService) EnsureDefaultRefundConfig() error {
	var count int64
	if err := s.db.GetDb().Model(&model.RefundConfig{}).Count(&count).Error; err != nil {
		return fmt.Errorf("查询退款配置表失败: %w", err)
	}
	if count > 0 {
		return nil
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	row := &model.RefundConfig{
		ID:                   refundConfigID,
		RefundFreightPolicy:  "always",
		CompletedRefundHours: defaultCompletedRefundHours,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := s.db.GetDb().Create(row).Error; err != nil {
		return fmt.Errorf("初始化默认退款配置失败: %w", err)
	}
	return nil
}

// ListRefunds 查询退款列表（含明细与订单号）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.Refund: 退款列表（已填充 OrderNo 与 Items）
//   - int: 总条数
//   - error: 错误信息
func (s *RefundService) ListRefunds(ctx context.Context, req *order.ListRefundsReq) ([]*model.Refund, int, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.Refund{})
	if req.CommunityId > 0 {
		db = db.Where("order_id IN (SELECT id FROM orders WHERE community_id = ?)", req.CommunityId)
	}
	if req.Status > 0 {
		db = db.Where("status = ?", req.Status)
	}
	if req.UserId > 0 {
		db = db.Where("user_id = ?", req.UserId)
	}
	if req.OrderId > 0 {
		db = db.Where("order_id = ?", req.OrderId)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var list []*model.Refund
	if err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	if err := s.fillRefundExtras(ctx, list); err != nil {
		return nil, 0, err
	}
	return list, int(total), nil
}

// fillRefundExtras 批量填充退款列表的关联订单号与商品明细
// 参数：
//   - ctx: 上下文
//   - list: 退款列表
//
// 返回值：
//   - error: 查询失败时返回错误
func (s *RefundService) fillRefundExtras(ctx context.Context, list []*model.Refund) error {
	if len(list) == 0 {
		return nil
	}
	orderIDs := make([]int64, 0, len(list))
	refundIDs := make([]int64, 0, len(list))
	for _, r := range list {
		orderIDs = append(orderIDs, r.OrderID)
		refundIDs = append(refundIDs, r.ID)
	}

	var orders []*model.Order
	if err := s.db.GetDb().WithContext(ctx).Where("id IN ?", orderIDs).Find(&orders).Error; err != nil {
		return err
	}
	orderNoMap := make(map[int64]string, len(orders))
	for _, o := range orders {
		orderNoMap[o.ID] = o.OrderNo
	}
	// 所属订单的商品明细行数：前端据此区分「无明细」来自直接收款单还是退完升格的整单退
	orderItemCount, err := orderItemCountsByOrders(ctx, s.db.GetDb(), orderIDs)
	if err != nil {
		return err
	}

	var items []*model.RefundItem
	if err := s.db.GetDb().WithContext(ctx).Where("refund_id IN ?", refundIDs).Find(&items).Error; err != nil {
		return err
	}
	itemMap := make(map[int64][]*model.RefundItem)
	for _, item := range items {
		itemMap[item.RefundID] = append(itemMap[item.RefundID], item)
	}

	// 批量加载审批日志（同意/拒绝/重试轨迹）
	var logs []*model.RefundLog
	if err := s.db.GetDb().WithContext(ctx).Where("refund_id IN ?", refundIDs).Order("id asc").Find(&logs).Error; err != nil {
		return err
	}
	logMap := make(map[int64][]*model.RefundLog)
	for _, l := range logs {
		logMap[l.RefundID] = append(logMap[l.RefundID], l)
	}

	for _, r := range list {
		r.OrderNo = orderNoMap[r.OrderID]
		r.OrderItemCount = orderItemCount[r.OrderID]
		r.Items = itemMap[r.ID]
		r.Logs = logMap[r.ID]
	}
	return nil
}

// ApproveRefund 同意退款
// 审核通过后将退款单置为"退款中"，调用 payment-service 发起实际退款；
// 退款成功后置为"已退款"、按退款明细释放库存并更新订单状态：
// 整单退或全部商品退完时订单置为 8 已退款，部分退款完成时回到退款前状态
// 参数：
//   - ctx: 上下文
//   - req: 审核请求
//
// 返回值：
//   - error: 错误信息
func (s *RefundService) ApproveRefund(ctx context.Context, req *order.ApproveRefundReq) error {
	var refund model.Refund
	if err := s.db.GetDb().WithContext(ctx).First(&refund, req.Id).Error; err != nil {
		return err
	}
	// 社区隔离强校验：审批通过即触发渠道出款，服务端必须校验退款单归属订单的社区
	if err := ensureOrderCommunity(s.db.GetDb().WithContext(ctx), refund.OrderID, req.CommunityId); err != nil {
		return err
	}
	if refund.Status != 1 {
		return invalidArg("退款状态不允许审核")
	}

	var refundItems []*model.RefundItem
	if err := s.db.GetDb().WithContext(ctx).Where("refund_id = ?", refund.ID).Find(&refundItems).Error; err != nil {
		return err
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	// 条件更新防并发：仅当退款单仍处于待审核(1)时生效，避免双审导致重复渠道退款
	res := s.db.GetDb().WithContext(ctx).Model(&model.Refund{}).
		Where("id = ? AND status = ?", req.Id, int32(1)).
		Updates(map[string]interface{}{
			"status":      int32(4),
			"operator_id": req.OperatorId,
			"updated_at":  now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return invalidArg("退款状态不允许审核")
	}
	refund.Status = 4

	// 操作者类型按 OperatorId 区分：0 为系统自动审批（调度器超时自动同意），
	// 其余为后台管理员人工审批；审批与完成日志均需如实区分，便于审计追溯
	operatorType := "admin"
	remark := ""
	if req.OperatorId == 0 {
		operatorType = "system"
		remark = "超过72小时未处理，系统自动同意"
	}

	// 审批日志：同意（进入退款中）
	if err := s.createRefundLog(ctx, refund.ID, "approve", operatorType, req.OperatorId, remark); err != nil {
		return err
	}

	// 安全收紧：支付单号一律以退款单服务端记录为准，忽略请求传入值，防止把退款打到非本单支付流水
	completed, err := s.processChannelRefund(ctx, &refund)
	if err != nil {
		return err
	}
	if !completed {
		// P0-2：渠道已受理未到终态——退款单保持退款中(4)，待 payment 侧查单收敛后由调度器同步收尾
		_ = s.createRefundLog(ctx, refund.ID, "approve", operatorType, req.OperatorId, "渠道已受理退款，等待退款结果同步")
		return nil
	}

	return s.completeRefundSuccess(ctx, &refund, refundItems, operatorType, req.OperatorId)
}

// RetryRefund 重试退款
// 针对卡在"退款中"(status=4) 的退款单：以退款单自身的 payment_no 重新发起渠道退款，
// 成功后走统一收尾（置已退款、回补库存、更新订单状态）。供后台手动重试与调度器自动重试复用。
// 参数：
//   - ctx: 上下文
//   - req: 重试请求（operator_id 为 0 时视为系统自动重试）
//
// 返回值：
//   - error: 错误信息
func (s *RefundService) RetryRefund(ctx context.Context, req *order.RetryRefundReq) error {
	var refund model.Refund
	if err := s.db.GetDb().WithContext(ctx).First(&refund, req.Id).Error; err != nil {
		return err
	}
	// 社区隔离强校验：重试即再次触发渠道出款，服务端必须校验退款单归属订单的社区
	if err := ensureOrderCommunity(s.db.GetDb().WithContext(ctx), refund.OrderID, req.CommunityId); err != nil {
		return err
	}
	if refund.Status != 4 {
		return invalidArg("仅退款中状态的退款单可重试")
	}

	var refundItems []*model.RefundItem
	if err := s.db.GetDb().WithContext(ctx).Where("refund_id = ?", refund.ID).Find(&refundItems).Error; err != nil {
		return err
	}

	operatorType := "admin"
	if req.OperatorId == 0 {
		operatorType = "system"
	}

	completed, err := s.processChannelRefund(ctx, &refund)
	if err != nil {
		// 记录失败日志，保留审计线索
		_ = s.createRefundLog(ctx, refund.ID, "retry", operatorType, req.OperatorId, "重试失败: "+err.Error())
		return err
	}
	if !completed {
		// P0-2：渠道已受理未到终态——保持退款中(4)，由调度器在查单收敛后同步收尾
		_ = s.createRefundLog(ctx, refund.ID, "retry", operatorType, req.OperatorId, "渠道已受理退款，等待退款结果同步")
		return nil
	}

	if err := s.completeRefundSuccess(ctx, &refund, refundItems, operatorType, req.OperatorId); err != nil {
		return err
	}
	return s.createRefundLog(ctx, refund.ID, "retry", operatorType, req.OperatorId, "重试退款成功")
}

// ResetRefundRetry 恢复卡在「退款中」(status=4) 退款单的自动收敛（B2）
// 与 RetryRefund 的分工：RetryRefund 再次向渠道发起出款，本方法只把两侧的「问结果」预算归零
// ——payment 侧退款流水的 retry_count/escalated 与 order 侧 refunds 的 retry_count/last_checked_at，
// 让调度器与退款查单任务重新把它当作在途单去核实渠道终态，全程不触渠道、不动钱。
//
// 顺序有讲究：先恢复 payment 侧再恢复本侧。支付侧拒绝（流水已终态成功/已失败）时直接返回错误、
// 本侧预算保持原样；支付侧成功后本侧才复位，复位要么生效、要么命中「预算本就是目标值」的
// 新卡单（等效已恢复）——两种结局里两侧最终状态一致，不会出现「订单在起劲重试而流水已放弃
// 收敛」的半截状态（并发下状态已变的拦截见下方 RowsAffected==0 分支）。
//
// 核实依据（remark）必填并落 refund_logs：本动作等于人工确认渠道还在处理这笔退款，
// 没有依据就重置，事后无从判断是谁批准重新收敛的。
//
// 参数：
//   - ctx: 上下文
//   - req: 重置请求（id 退款单 ID + community_id 强校验 + remark 核实依据 + 操作人）
//
// 返回值：
//   - error: 参数缺失、归属不符、状态不允许或支付侧拒绝时返回错误
//
// 使用示例：
//   - err := svc.ResetRefundRetry(ctx, &order.ResetRefundRetryReq{Id: 8, CommunityId: 3, OperatorId: 1, Remark: "微信商户平台显示退款处理中"})
func (s *RefundService) ResetRefundRetry(ctx context.Context, req *order.ResetRefundRetryReq) error {
	if strings.TrimSpace(req.Remark) == "" {
		return invalidArg("请填写渠道核实依据")
	}
	var refund model.Refund
	if err := s.db.GetDb().WithContext(ctx).First(&refund, req.Id).Error; err != nil {
		return err
	}
	// 社区隔离强校验：与 RetryRefund 同口径，跨社区退款单一律拒绝
	if err := ensureOrderCommunity(s.db.GetDb().WithContext(ctx), refund.OrderID, req.CommunityId); err != nil {
		return err
	}
	if refund.Status != 4 {
		return invalidArg("仅退款中状态的退款单可恢复自动收敛")
	}

	if s.paymentClient != nil && refund.PaymentNo != "" {
		if _, err := s.paymentClient.ResetRefundFlow(ctx, &payment.ResetRefundFlowReq{
			OrderId:      refund.OrderID,
			RefundNo:     refund.RefundNo,
			Remark:       req.Remark,
			OperatorId:   req.OperatorId,
			OperatorName: req.OperatorName,
		}); err != nil {
			return fmt.Errorf("重置退款流水失败: %w", err)
		}
	}

	// 复位本侧收敛预算。用 UpdateColumns 绕开 base 钩子：updated_at 同时是
	// 「停留超过 stuckRecoverAfter 才算卡单」的起算列，被刷成当前时间会让刚重置的单
	// 再等一整个停留阈值才重新被调度器看见；last_checked_at 置 NULL 在 asc 轮转里排最前，
	// 下一轮立即优先核查
	res := s.db.GetDb().WithContext(ctx).Model(&model.Refund{}).
		Where("id = ? AND status = 4", refund.ID).
		UpdateColumns(map[string]interface{}{"retry_count": int32(0), "last_checked_at": nil})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// MySQL 默认 changed-rows 语义（DSN 未开 clientFoundRows）：新卡单的预算本就是目标值
		// （retry_count=0 且 last_checked_at=NULL），UPDATE 无值变化时 affected=0，不代表并发冲突；
		// 回查确认仍是退款中且预算已是目标值则视为等效已恢复，真正已变更（收敛/重试过）才拦截报错
		var cur model.Refund
		if err := s.db.GetDb().WithContext(ctx).
			Select("status", "retry_count", "last_checked_at").
			First(&cur, refund.ID).Error; err != nil {
			return err
		}
		if cur.Status != 4 || cur.RetryCount != 0 || cur.LastCheckedAt != nil {
			return invalidArg("退款单状态已变更，请刷新后重试")
		}
	}

	operatorType := "admin"
	if req.OperatorId == 0 {
		operatorType = "system"
	}
	return s.createRefundLog(ctx, refund.ID, "reset", operatorType, req.OperatorId, req.Remark)
}

// processChannelRefund 调用 payment-service 以退款单记录的支付单号发起渠道退款
// 参数：
//   - ctx: 上下文
//   - refund: 退款单
//
// 返回值：
//   - error: 错误信息
func (s *RefundService) processChannelRefund(ctx context.Context, refund *model.Refund) (bool, error) {
	if s.paymentClient == nil {
		// 无支付客户端（桩测试环境）：维持原「直接放行、调用方按成功收尾」语义
		return true, nil
	}
	resp, err := s.paymentClient.ProcessRefund(ctx, &payment.ProcessRefundReq{
		OrderId:   refund.OrderID,
		RefundNo:  refund.RefundNo,
		Amount:    refund.Amount,
		PaymentNo: refund.PaymentNo,
	})
	if err != nil {
		return false, fmt.Errorf("调用支付退款失败: %w", err)
	}
	// P0-2：completed=false 表示渠道已受理未到终态（微信 PROCESSING 常态），
	// 调用方不得走成功收尾，终态由 payment 侧查单任务收敛后经调度器同步
	return resp.GetCompleted(), nil
}

// queryRefundFlowStatus 查询 payment 侧退款流水状态（读库不触渠道，P0-2）
// 供调度器对卡单退款做收敛决策：2=成功（直接收尾）3/0=失败或无流水（重新发起）1=渠道受理中（继续等待）
//
// 参数：
//   - ctx: 上下文
//   - refund: 退款单（须携带 PaymentNo/OrderID/RefundNo）
//
// 返回值：
//   - int32: payment 侧流水状态（语义同 QueryRefundResultResp.Status）
//   - error: RPC 失败时返回错误（调用方跳过本轮）
func (s *RefundService) queryRefundFlowStatus(ctx context.Context, refund *model.Refund) (int32, error) {
	if s.paymentClient == nil {
		// 桩测试环境无支付客户端：按「可重新发起」口径放行
		return 3, nil
	}
	if refund.PaymentNo == "" {
		// 无关联支付流水（历史数据/现金兜底口径）：按可重新发起处理
		return 3, nil
	}
	resp, err := s.paymentClient.QueryRefundResult(ctx, &payment.QueryRefundResultReq{
		OrderId:  refund.OrderID,
		RefundNo: refund.RefundNo,
	})
	if err != nil {
		return 0, fmt.Errorf("查询退款流水状态失败: %w", err)
	}
	return resp.GetStatus(), nil
}

// completeRefundSuccess 退款成功的统一收尾：置已退款、按明细回补库存、更新订单去向并写状态日志。
// 整单退/全部退完 → 订单 8 已退款；部分退款完成 → 回退款前状态。
//
// 跨服务副作用（库存回补、优惠券回补）一律在事务提交后触发：
// 事务内调 RPC 会让退款单/订单的行锁横跨网络往返（并发退款互相阻塞、连接池被拖垮），
// 更糟的是「RPC 已成功、事务随后回滚」会在 product 侧留下无对应的回补流水。
// 事务内只组装事实（订单 ID、明细、幂等槽位、是否整单退），提交后再统一调用，
// 失败由 runRefundEffect 有限重试并打 [CRITICAL] 告警
// 参数：
//   - ctx: 上下文
//   - refund: 退款单（须处于退款中状态）
//   - items: 退款商品明细
//   - operatorType: 操作者类型（admin/system）
//   - operatorID: 操作者 ID
//
// 返回值：
//   - error: 本地事务失败时返回错误；提交后的跨服务副作用失败只告警，不影响返回值
func (s *RefundService) completeRefundSuccess(ctx context.Context, refund *model.Refund, items []*model.RefundItem, operatorType string, operatorID int64) error {
	// 事务内向事务外传递的收尾事实
	var (
		stockOrderID  int64
		stockBizNo    string
		stockItems    []*product.StockItemQty
		couponOrderID int64
	)
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refundCompleted model.Refund
		if err := tx.First(&refundCompleted, refund.ID).Error; err != nil {
			return err
		}
		if refundCompleted.Status != 4 {
			return bizerr.New(codes.FailedPrecondition, "退款单状态异常")
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		// 条件更新防并发：仅当退款单仍处于退款中(4)时生效
		res := tx.Model(&model.Refund{}).Where("id = ? AND status = ?", refund.ID, int32(4)).Updates(map[string]interface{}{
			"status":      int32(5),
			"refund_time": &now,
			"updated_at":  now,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return bizerr.New(codes.FailedPrecondition, "退款单状态已变更，请刷新后重试")
		}

		var o model.Order
		if err := tx.First(&o, refundCompleted.OrderID).Error; err != nil {
			return err
		}

		// 按退款明细整批回补库存（仅金额退无商品明细，不回补库存）；
		// biz_no=退款单号：每笔退款独立幂等槽位，同一订单同一 SKU 多笔部分退款互不吞并。
		// 原先逐行 ReleaseStock + ReleaseFlashSaleStock（2N 次往返），收口为 2 次批量 RPC。
		// 此处只组装入参，真正调用放到事务提交之后
		stockItems = make([]*product.StockItemQty, 0, len(items))
		for _, item := range items {
			stockItems = append(stockItems, &product.StockItemQty{SkuId: item.SkuID, Quantity: item.Quantity})
		}
		stockOrderID = o.ID
		stockBizNo = refundCompleted.RefundNo

		// 订单去向与类型升级（money 口径）：累计成功退款（含运费）达到实付 → 订单 8 已退款，
		// 且本笔退款单类型升级为整单退（部分退/仅金额退补齐全款后同样按整单退展示）；
		// 未退完 → 回退款前状态
		fully, err := orderMoneyFullyRefundedTx(tx, o.ID)
		if err != nil {
			return err
		}
		nextStatus := lastStatusBeforeRefundTx(tx, o.ID)
		remark := "部分退款完成"
		if fully {
			nextStatus = 8
			remark = "退款完成"
			if err := tx.Model(&model.Refund{}).Where("id = ? AND type <> 1", refund.ID).
				Update("type", int32(1)).Error; err != nil {
				return err
			}
			refundCompleted.Type = 1
			// 整单退款回补优惠券：与取消订单同口径，全额退款后券应返还用户。
			// 这里只记录「需要回补」，ReleaseCoupon 的 RPC 同样移到事务外
			if o.CouponID > 0 {
				couponOrderID = o.ID
			}
		}

		// 条件更新防并发覆盖：订单行在本事务内是无锁 First 读取的，整行 Save 会把
		// 并发操作（同一订单另一笔退款完成、后台改状态）的写入整体盖掉。
		// 仅当订单仍停留退款中(7)时生效；未命中说明状态已被并发流转，中止事务——
		// 退款单保持退款中(4) 交由重试/调度兜底，也不会写下一条 from=7 的假状态日志
		res = tx.Model(&model.Order{}).Where("id = ? AND status = ?", o.ID, int32(7)).Updates(map[string]interface{}{
			"status":     nextStatus,
			"updated_at": now,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
		}
		o.Status = nextStatus
		o.UpdatedAt = now
		return createStatusLogTx(tx, o.ID, 7, nextStatus, operatorType, operatorID, remark)
	})
	if err != nil {
		return err
	}

	// 事务提交成功后再执行跨服务副作用与销量回退（失败均不回滚已提交的退款）
	_ = runRefundEffect(ctx, "回补库存", fmt.Sprintf("order_id=%d biz_no=%s", stockOrderID, stockBizNo),
		func(c context.Context) error { return releaseStockBatch(c, stockOrderID, stockItems, stockBizNo) })
	if couponOrderID > 0 {
		_ = runRefundEffect(ctx, "回补优惠券", fmt.Sprintf("order_id=%d", couponOrderID),
			func(c context.Context) error {
				_, err := grpcclient.ProductClient.ReleaseCoupon(c, &product.ReleaseCouponReq{OrderId: couponOrderID})
				return err
			})
	}
	// 按退款明细回退销量（仅金额退无商品明细自然跳过）；
	// AdjustSalesCount 无幂等键，放事务内重试会重复回退
	s.adjustRefundSales(ctx, refund.ID, items)
	// 退款成功事件（尽力而为）：覆盖后台代客/审批同意/重试/调度器同步全部完成路径
	s.publishRefundEvent(ctx, refund.OrderID, refund.RefundNo, refund.Amount, eventbus.RefundActionCompleted, operatorType, "")
	return nil
}

// 退款收尾跨服务调用的重试节奏
const (
	// refundEffectRetry 单笔副作用的最大尝试次数
	refundEffectRetry = 3
	// refundEffectRetryGap 重试间隔；product 侧抖动通常在百毫秒级恢复
	refundEffectRetryGap = 100 * time.Millisecond
)

// runRefundEffect 在退款主事务提交后执行一项跨服务副作用（库存回补 / 优惠券回补）
// 只做「有限次重试 + 失败告警」，绝不回滚已提交的退款：退款钱已经退给用户，
// 收尾动作不能让调用方误以为退款失败而重复退款。
// product 侧以 order_id+sku_id+op_type+biz_no 唯一键去重（退款场景 biz_no=退款单号，
// 优惠券按 order_id+status 幂等），因此重复调用只生效一次，重试是安全的；
// 三次仍失败时打 [CRITICAL] 日志并带上幂等槽位，运维按同一 biz_no 手工补做即可对齐
// 参数：
//   - ctx: 上下文
//   - name: 动作名，用于日志定位（如「回补库存」）
//   - detail: 定位信息（订单号 / 退款单号 / 幂等槽位），写进日志便于人工补做
//   - fn: 实际的跨服务调用
//
// 返回值：
//   - error: 全部尝试失败时返回最后一次错误；任一成功即返回 nil
//
// 使用示例：
//   - err := runRefundEffect(ctx, "回补库存", "order_id=12 biz_no=RF20261021001",
//   - func(c context.Context) error { return releaseStockBatch(c, 12, items, "RF20261021001") })
func runRefundEffect(ctx context.Context, name, detail string, fn func(context.Context) error) error {
	var last error
	for i := 0; i < refundEffectRetry; i++ {
		if last = fn(ctx); last == nil {
			return nil
		}
		log.Printf("[refund] 退款收尾%s失败（第 %d/%d 次）%s: %v", name, i+1, refundEffectRetry, detail, last)
		if i == refundEffectRetry-1 {
			break
		}
		time.Sleep(refundEffectRetryGap)
	}
	log.Printf("[CRITICAL] 退款收尾%s最终失败，退款单已置成功而该项未生效，请按幂等槽位人工补做：%s: %v", name, detail, last)
	return last
}

// adjustRefundSales 退款成功后按明细回退销量（净成交口径）
// 必须在退款主事务提交成功后调用，失败仅记日志不回滚退款
//
// 参数：
//   - ctx: 上下文
//   - refundID: 退款单 ID（日志定位用）
//   - items: 退款商品明细（仅金额退时为空）
func (s *RefundService) adjustRefundSales(ctx context.Context, refundID int64, items []*model.RefundItem) {
	reqItems := make([]*product.AdjustSalesCountItem, 0, len(items))
	for _, it := range items {
		if it.SkuID <= 0 || it.Quantity <= 0 {
			continue
		}
		reqItems = append(reqItems, &product.AdjustSalesCountItem{SkuId: it.SkuID, Delta: -it.Quantity})
	}
	if len(reqItems) == 0 {
		return
	}
	if _, err := grpcclient.ProductClient.AdjustSalesCount(ctx, &product.AdjustSalesCountReq{Items: reqItems}); err != nil {
		log.Printf("[refund] 回退销量失败 refund_id=%d err=%v", refundID, err)
	}
}

// createRefundLog 写入退款审批日志
// 参数：
//   - ctx: 上下文
//   - refundID: 退款单 ID
//   - action: 动作（approve/reject/retry）
//   - operatorType: 操作者类型
//   - operatorID: 操作者 ID
//   - remark: 备注
//
// 返回值：
//   - error: 错误信息
func (s *RefundService) createRefundLog(ctx context.Context, refundID int64, action, operatorType string, operatorID int64, remark string) error {
	return s.db.GetDb().WithContext(ctx).Create(&model.RefundLog{
		RefundID:     refundID,
		Action:       action,
		OperatorType: operatorType,
		OperatorID:   operatorID,
		Remark:       remark,
		CreatedAt:    time.Now().Format("2006-01-02 15:04:05"),
	}).Error
}

// RejectRefund 拒绝退款
// 拒绝后订单回到退款前状态（取最近一条进入退款中的来源状态）
// 参数：
//   - ctx: 上下文
//   - req: 拒绝请求
//
// 返回值：
//   - error: 错误信息
func (s *RefundService) RejectRefund(ctx context.Context, req *order.RejectRefundReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund model.Refund
		if err := tx.First(&refund, req.Id).Error; err != nil {
			return err
		}

		// 社区隔离强校验：拒绝操作会回滚订单状态，服务端校验退款单归属订单的社区
		if err := ensureOrderCommunity(tx, refund.OrderID, req.CommunityId); err != nil {
			return err
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		// 条件更新防并发覆盖：仅待审核(1)状态可拒绝，RowsAffected=0 说明状态已被其他操作变更
		res := tx.Model(&model.Refund{}).Where("id = ? AND status = 1", refund.ID).Updates(map[string]interface{}{
			"status":        int32(3),
			"reject_reason": req.RejectReason,
			"operator_id":   req.OperatorId,
			"updated_at":    now,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return invalidArg("退款状态不允许审核")
		}

		var o model.Order
		if err := tx.First(&o, refund.OrderID).Error; err != nil {
			return err
		}
		nextStatus := lastStatusBeforeRefundTx(tx, o.ID)
		// 条件更新防并发覆盖：订单行为无锁 First 读取，整行 Save 会盖掉并发写入
		// （同一订单另一笔退款完成已把订单推往 8/退款前状态）。仅停留退款中(7)时可回退，
		// 未命中即状态已被并发流转，中止事务（退款单的拒绝更新一并回滚，不留半完成状态）
		res = tx.Model(&model.Order{}).Where("id = ? AND status = ?", o.ID, int32(7)).Updates(map[string]interface{}{
			"status":     nextStatus,
			"updated_at": now,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
		}
		o.Status = nextStatus
		o.UpdatedAt = now
		// 审批日志：拒绝
		if err := tx.Create(&model.RefundLog{
			RefundID:     refund.ID,
			Action:       "reject",
			OperatorType: "admin",
			OperatorID:   req.OperatorId,
			Remark:       req.RejectReason,
			CreatedAt:    now,
		}).Error; err != nil {
			return err
		}
		return createStatusLogTx(tx, o.ID, 7, nextStatus, "admin", req.OperatorId, "拒绝退款: "+req.RejectReason)
	})
}

// orderMoneyFullyRefundedTx 判断订单钱款是否已全部退完（money 口径）：
// 累计成功退款金额（含运费）达到实付金额即视为全退，覆盖整单退、多笔部分退与仅金额退混退场景
// 参数：
//   - tx: 数据库事务
//   - orderID: 订单 ID
//
// 返回值：
//   - bool: 是否全部退完
//   - error: 查询失败时返回错误
func orderMoneyFullyRefundedTx(tx *gorm.DB, orderID int64) (bool, error) {
	var total, refunded float64
	if err := tx.Model(&model.Order{}).
		Where("id = ?", orderID).
		Select("COALESCE(pay_amount, 0)").
		Scan(&total).Error; err != nil {
		return false, err
	}
	if err := tx.Model(&model.Refund{}).
		Where("order_id = ? AND status = ?", orderID, 5).
		Select("COALESCE(SUM(amount), 0)").
		Scan(&refunded).Error; err != nil {
		return false, err
	}
	// 金额为 float64（decimal 化立项前），比较带 1e-6 容差防浮点误差误判
	return total > 0 && refunded >= total-1e-6, nil
}

// lastStatusBeforeRefundTx 查询订单最近一次进入退款中(7)之前的来源状态
// 合法来源状态为 2/3/4 与已完成(5)：
// - 到店单的 5 来自收款事务内自动完结（2026-10-21）；
// - 自提/配送单的 5 来自确认收货/核销/自动完成（2026-10-25 已完成单售后窗口放行后产生）；
// 部分退款完成后回到 5 而不是被兜底成 2（否则会凭空多出「已支付」停留态，
// 后台会把它当作待核销/待发货单处理），故 from=5 无需再查 delivery_type 二次确认。
// 来源状态非法（不在上述范围）时兜底返回 2 已支付
// 参数：
//   - tx: 数据库事务（或普通 DB）
//   - orderID: 订单 ID
//
// 返回值：
//   - int32: 退款前状态
func lastStatusBeforeRefundTx(tx *gorm.DB, orderID int64) int32 {
	var log model.OrderStatusLog
	if err := tx.Where("order_id = ? AND to_status = 7", orderID).
		Order("id desc").First(&log).Error; err != nil {
		return 2
	}
	if log.FromStatus >= 2 && log.FromStatus <= 5 {
		return log.FromStatus
	}
	return 2
}

// createStatusLogTx 在事务中创建订单状态日志
// 参数：
//   - tx: 数据库事务
//   - orderID: 订单 ID
//   - fromStatus: 原状态
//   - toStatus: 目标状态
//   - operatorType: 操作者类型
//   - operatorID: 操作者 ID
//   - remark: 备注
//
// 返回值：
//   - error: 错误信息
func createStatusLogTx(tx *gorm.DB, orderID int64, fromStatus, toStatus int32, operatorType string, operatorID int64, remark string) error {
	log := &model.OrderStatusLog{
		OrderID:      orderID,
		FromStatus:   fromStatus,
		ToStatus:     toStatus,
		OperatorType: operatorType,
		OperatorID:   operatorID,
		Remark:       remark,
		CreatedAt:    time.Now().Format("2006-01-02 15:04:05"),
	}
	return tx.Create(log).Error
}

// generateRefundNo 生成退款单号
// 返回值：
//   - string: 退款单号
func generateRefundNo() string {
	return "TK" + time.Now().Format("20060102150405") + fmt.Sprintf("%04d", time.Now().Nanosecond()/1e5)
}

// round2 金额保留两位小数
// 参数：
//   - v: 原始金额
//
// 返回值：
//   - float64: 保留两位小数后的金额
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
