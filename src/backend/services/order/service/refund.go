package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

// refundConfigID 退款配置表固定单行主键
const refundConfigID = 1

// refundFreightPolicies 运费退还策略合法值集合
var refundFreightPolicies = map[string]bool{"always": true, "never": true, "user_decide": true}

// RefundService 退款业务服务
type RefundService struct {
	db            *mysql.Instance
	paymentClient payment.PaymentServiceClient
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
// 同一订单存在未完结退款申请（待审核/退款中）时禁止重复申请
// 参数：
//   - ctx: 上下文
//   - req: 申请请求
//
// 返回值：
//   - *model.Refund: 退款模型（含明细）
//   - error: 错误信息
func (s *RefundService) ApplyRefund(ctx context.Context, req *order.ApplyRefundReq) (*model.Refund, error) {
	if req.Reason == "" {
		return nil, fmt.Errorf("请填写退款原因")
	}

	var o model.Order
	if err := s.db.GetDb().WithContext(ctx).First(&o, req.OrderId).Error; err != nil {
		return nil, err
	}
	if o.UserID != req.UserId {
		return nil, fmt.Errorf("无权限申请退款")
	}
	if o.Status != 2 && o.Status != 3 && o.Status != 4 {
		return nil, fmt.Errorf("订单状态不允许申请退款")
	}

	// 同一订单存在未完结退款申请时禁止重复申请
	var pending int64
	if err := s.db.GetDb().WithContext(ctx).Model(&model.Refund{}).
		Where("order_id = ? AND status IN ?", o.ID, []int32{1, 4}).
		Count(&pending).Error; err != nil {
		return nil, err
	}
	if pending > 0 {
		return nil, fmt.Errorf("该订单存在未完结的退款申请，请等待处理完成")
	}

	calc, err := s.calcRefundOrder(ctx, &o, req.Items, req.Amount, req.RefundFreight)
	if err != nil {
		return nil, err
	}
	lines, isFull, freight, amount := calc.lines, calc.isFull, calc.freight, calc.amount

	paymentNo := ""
	if s.paymentClient != nil {
		if p, err := s.paymentClient.GetPayment(ctx, &payment.GetPaymentReq{OrderId: req.OrderId}); err == nil && p != nil {
			paymentNo = p.PaymentNo
		}
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	refundType := int32(2)
	if isFull {
		refundType = 1
	}
	refund := &model.Refund{
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
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(refund).Error; err != nil {
			return err
		}
		for _, item := range refund.Items {
			item.RefundID = refund.ID
			if err := tx.Create(item).Error; err != nil {
				return err
			}
		}
		o.Status = 7
		o.UpdatedAt = now
		if err := tx.Save(&o).Error; err != nil {
			return err
		}
		return createStatusLogTx(tx, o.ID, fromStatus, 7, "user", req.UserId, "申请退款: "+req.Reason)
	}); err != nil {
		return nil, err
	}
	refund.OrderNo = o.OrderNo
	return refund, nil
}

// AdminCreateRefund 后台代客退款（发起即执行，免审核）
// 整单退/部分退金额口径与 C 端申请一致（calcRefundOrder 服务端汇算）；
// 创建即置为退款中(4)并直接调用渠道退款，成功后走统一收尾（置已退款、回补库存、更新订单状态）；
// 渠道调用失败时退款单保留在退款中，由后台「重试退款」或调度器兜底
// 参数：
//   - ctx: 上下文
//   - req: 后台代客退款请求
//
// 返回值：
//   - *model.Refund: 退款模型（含明细）
//   - error: 错误信息
func (s *RefundService) AdminCreateRefund(ctx context.Context, req *order.AdminCreateRefundReq) (*model.Refund, error) {
	if req.Reason == "" {
		return nil, fmt.Errorf("请填写退款原因")
	}

	var o model.Order
	if err := s.db.GetDb().WithContext(ctx).First(&o, req.OrderId).Error; err != nil {
		return nil, err
	}
	if o.Status != 2 && o.Status != 3 && o.Status != 4 {
		return nil, fmt.Errorf("订单状态不允许退款")
	}

	// 存在未完结退款申请（待审核/退款中）时禁止重复发起
	var pending int64
	if err := s.db.GetDb().WithContext(ctx).Model(&model.Refund{}).
		Where("order_id = ? AND status IN ?", o.ID, []int32{1, 4}).
		Count(&pending).Error; err != nil {
		return nil, err
	}
	if pending > 0 {
		return nil, fmt.Errorf("该订单存在未完结的退款申请，请等待处理完成")
	}

	calc, err := s.calcRefundOrder(ctx, &o, req.Items, req.Amount, req.RefundFreight)
	if err != nil {
		return nil, err
	}

	paymentNo := ""
	if s.paymentClient != nil {
		if p, err := s.paymentClient.GetPayment(ctx, &payment.GetPaymentReq{OrderId: req.OrderId}); err == nil && p != nil {
			paymentNo = p.PaymentNo
		}
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	refundType := int32(2)
	if calc.isFull {
		refundType = 1
	}
	refund := &model.Refund{
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
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(refund).Error; err != nil {
			return err
		}
		for _, item := range refund.Items {
			item.RefundID = refund.ID
			if err := tx.Create(item).Error; err != nil {
				return err
			}
		}
		o.Status = 7
		o.UpdatedAt = now
		if err := tx.Save(&o).Error; err != nil {
			return err
		}
		return createStatusLogTx(tx, o.ID, fromStatus, 7, "admin", req.OperatorId, "后台代客退款: "+req.Reason)
	}); err != nil {
		return nil, err
	}
	refund.OrderNo = o.OrderNo

	if err := s.createRefundLog(ctx, refund.ID, "create", "admin", req.OperatorId, "后台代客退款: "+req.Reason); err != nil {
		return refund, err
	}

	// 发起即执行：直接发起渠道退款
	if err := s.processChannelRefund(ctx, refund); err != nil {
		// 渠道失败：退款单保留在退款中，审批日志留痕，由「重试退款」兜底
		_ = s.createRefundLog(ctx, refund.ID, "create", "admin", req.OperatorId, "渠道退款调用失败: "+err.Error())
		return refund, err
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
}

// calcRefundOrder 汇算一笔退款的退款行、运费与最终金额
// 金额以服务端汇算为准（行小计按比例分摊订单优惠，整单退尾差减法兜底），
// reqAmount 仅允许调低，调低差额从最后一行明细扣减；
// 运费仅整单退时按 refund_freight_policy 策略退还（user_decide 时按 refundFreight 勾选）
// 参数：
//   - ctx: 上下文
//   - o: 订单
//   - reqItems: 退款商品明细（为空表示整单退）
//   - reqAmount: 客户端传入金额（仅允许调低）
//   - refundFreight: user_decide 策略下是否退运费
//
// 返回值：
//   - *refundCalc: 汇算结果
//   - error: 校验失败时返回错误
func (s *RefundService) calcRefundOrder(ctx context.Context, o *model.Order, reqItems []*order.ApplyRefundItem, reqAmount float64, refundFreight bool) (*refundCalc, error) {
	var orderItems []*model.OrderItem
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ?", o.ID).Find(&orderItems).Error; err != nil {
		return nil, err
	}
	itemMap := make(map[int64]*model.OrderItem, len(orderItems))
	for _, item := range orderItems {
		itemMap[item.ID] = item
	}

	refundedQty, refundedGoods, err := s.refundedStat(ctx, o.ID)
	if err != nil {
		return nil, err
	}

	lines, isFull, err := buildRefundLines(o, orderItems, itemMap, reqItems, refundedQty)
	if err != nil {
		return nil, err
	}

	// 商品部分应退合计：整单退以"商品实付总额 - 已退商品金额"为准（尾差在最后一行兜底）
	goodsRefund := sumRefundLines(lines)
	if isFull {
		goodsRefund = round2(o.TotalAmount - o.DiscountAmount - refundedGoods)
		fixLastLineTailDiff(lines, goodsRefund)
	}

	// 运费：仅整单退按策略退还
	freight := s.resolveFreight(ctx, o, isFull, refundFreight)
	refundable := round2(goodsRefund + freight)

	// 客户端金额仅允许调低
	amount := refundable
	if reqAmount > 0 && reqAmount < refundable {
		amount = round2(reqAmount)
	}
	cut := round2(refundable - amount)
	if cut > goodsRefund {
		return nil, fmt.Errorf("退款金额不能低于运费金额")
	}
	if cut > 0 {
		last := lines[len(lines)-1]
		if last.amount < cut {
			return nil, fmt.Errorf("调整金额过低，最后一行商品应退不足抵扣")
		}
		last.amount = round2(last.amount - cut)
	}
	return &refundCalc{lines: lines, isFull: isFull, freight: freight, amount: amount}, nil
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
			return nil, false, fmt.Errorf("订单商品已全部退款")
		}
		return lines, true, nil
	}

	seen := make(map[int64]bool, len(reqItems))
	for _, ri := range reqItems {
		item, ok := itemMap[ri.OrderItemId]
		if !ok {
			return nil, false, fmt.Errorf("退款商品不属于该订单")
		}
		if seen[ri.OrderItemId] {
			return nil, false, fmt.Errorf("商品 %s 重复提交", item.ProductName)
		}
		seen[ri.OrderItemId] = true
		rem := item.Quantity - refundedQty[item.ID]
		if ri.Quantity <= 0 || ri.Quantity > rem {
			return nil, false, fmt.Errorf("商品 %s 可退数量不足（剩余 %d）", item.ProductName, rem)
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
	share := round2(o.DiscountAmount * item.TotalPrice / o.TotalAmount)
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

// resolveFreight 按运费退还策略解析本单应退运费
// 仅整单退涉及运费；user_decide 策略下按用户勾选结果退还
// 参数：
//   - ctx: 上下文
//   - o: 订单
//   - isFull: 是否整单退
//   - userChoosesFreight: user_decide 策略下用户是否选择退运费
//
// 返回值：
//   - float64: 应退运费金额（2位小数）
func (s *RefundService) resolveFreight(ctx context.Context, o *model.Order, isFull bool, userChoosesFreight bool) float64 {
	if !isFull || o.DeliveryFee <= 0 {
		return 0
	}
	switch s.getFreightPolicy(ctx) {
	case "always":
		return round2(o.DeliveryFee)
	case "user_decide":
		if userChoosesFreight {
			return round2(o.DeliveryFee)
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

// GetRefundConfig 查询退款配置
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - *model.RefundConfig: 退款配置
//   - error: 查询失败时返回错误
func (s *RefundService) GetRefundConfig(ctx context.Context) (*model.RefundConfig, error) {
	var row model.RefundConfig
	if err := s.db.GetDb().WithContext(ctx).First(&row, refundConfigID).Error; err != nil {
		return nil, fmt.Errorf("读取退款配置失败: %w", err)
	}
	return &row, nil
}

// SaveRefundConfig 保存退款配置
// 参数：
//   - ctx: 上下文
//   - policy: 运费退还策略（always/never/user_decide）
//
// 返回值：
//   - error: 策略非法或保存失败时返回错误
func (s *RefundService) SaveRefundConfig(ctx context.Context, policy string) error {
	if !refundFreightPolicies[policy] {
		return fmt.Errorf("运费退还策略不合法: %s", policy)
	}
	row, err := s.GetRefundConfig(ctx)
	if err != nil {
		return err
	}
	row.RefundFreightPolicy = policy
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
		ID:                 refundConfigID,
		RefundFreightPolicy: "always",
		CreatedAt:          now,
		UpdatedAt:          now,
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
	if refund.Status != 1 {
		return fmt.Errorf("退款状态不允许审核")
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
		return fmt.Errorf("退款状态不允许审核")
	}
	refund.Status = 4

	// 审批日志：同意（进入退款中）
	if err := s.createRefundLog(ctx, refund.ID, "approve", "admin", req.OperatorId, ""); err != nil {
		return err
	}

	// 安全收紧：支付单号一律以退款单服务端记录为准，忽略请求传入值，防止把退款打到非本单支付流水
	if err := s.processChannelRefund(ctx, &refund); err != nil {
		return err
	}

	return s.completeRefundSuccess(ctx, &refund, refundItems, "admin", req.OperatorId)
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
	if refund.Status != 4 {
		return fmt.Errorf("仅退款中状态的退款单可重试")
	}

	var refundItems []*model.RefundItem
	if err := s.db.GetDb().WithContext(ctx).Where("refund_id = ?", refund.ID).Find(&refundItems).Error; err != nil {
		return err
	}

	operatorType := "admin"
	if req.OperatorId == 0 {
		operatorType = "system"
	}

	if err := s.processChannelRefund(ctx, &refund); err != nil {
		// 记录失败日志，保留审计线索
		_ = s.createRefundLog(ctx, refund.ID, "retry", operatorType, req.OperatorId, "重试失败: "+err.Error())
		return err
	}

	if err := s.completeRefundSuccess(ctx, &refund, refundItems, operatorType, req.OperatorId); err != nil {
		return err
	}
	return s.createRefundLog(ctx, refund.ID, "retry", operatorType, req.OperatorId, "重试退款成功")
}

// processChannelRefund 调用 payment-service 以退款单记录的支付单号发起渠道退款
// 参数：
//   - ctx: 上下文
//   - refund: 退款单
//
// 返回值：
//   - error: 错误信息
func (s *RefundService) processChannelRefund(ctx context.Context, refund *model.Refund) error {
	if s.paymentClient == nil {
		return nil
	}
	if _, err := s.paymentClient.ProcessRefund(ctx, &payment.ProcessRefundReq{
		OrderId:   refund.OrderID,
		RefundNo:  refund.RefundNo,
		Amount:    refund.Amount,
		PaymentNo: refund.PaymentNo,
	}); err != nil {
		return fmt.Errorf("调用支付退款失败: %w", err)
	}
	return nil
}

// completeRefundSuccess 退款成功的统一收尾：置已退款、按明细回补库存、更新订单去向并写状态日志。
// 整单退/全部退完 → 订单 8 已退款；部分退款完成 → 回退款前状态。
// 参数：
//   - ctx: 上下文
//   - refund: 退款单（须处于退款中状态）
//   - items: 退款商品明细
//   - operatorType: 操作者类型（admin/system）
//   - operatorID: 操作者 ID
//
// 返回值：
//   - error: 错误信息
func (s *RefundService) completeRefundSuccess(ctx context.Context, refund *model.Refund, items []*model.RefundItem, operatorType string, operatorID int64) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refundCompleted model.Refund
		if err := tx.First(&refundCompleted, refund.ID).Error; err != nil {
			return err
		}
		if refundCompleted.Status != 4 {
			return fmt.Errorf("退款单状态异常")
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
			return fmt.Errorf("退款单状态已变更")
		}

		var o model.Order
		if err := tx.First(&o, refundCompleted.OrderID).Error; err != nil {
			return err
		}

		// 按退款明细逐行释放库存
		for _, item := range items {
			if _, err := grpcclient.ProductClient.ReleaseStock(ctx, &product.ReleaseStockReq{
				SkuId:    item.SkuID,
				Quantity: item.Quantity,
				OrderId:  o.ID,
			}); err != nil {
				return fmt.Errorf("释放库存失败: %w", err)
			}
		}

		// 订单去向：整单退/全部退完 → 8 已退款；部分退款完成 → 回退款前状态
		nextStatus := int32(8)
		remark := "退款完成"
		if refundCompleted.Type == 2 {
			fully, err := orderFullyRefundedTx(tx, o.ID)
			if err != nil {
				return err
			}
			if !fully {
				nextStatus = lastStatusBeforeRefundTx(tx, o.ID)
				remark = "部分退款完成"
			}
		}

		o.Status = nextStatus
		o.UpdatedAt = now
		if err := tx.Save(&o).Error; err != nil {
			return err
		}
		return createStatusLogTx(tx, o.ID, 7, nextStatus, operatorType, operatorID, remark)
	})
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
		if refund.Status != 1 {
			return fmt.Errorf("退款状态不允许审核")
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		refund.Status = 3
		refund.RejectReason = req.RejectReason
		refund.OperatorID = req.OperatorId
		refund.UpdatedAt = now
		if err := tx.Save(&refund).Error; err != nil {
			return err
		}

		var o model.Order
		if err := tx.First(&o, refund.OrderID).Error; err != nil {
			return err
		}
		nextStatus := lastStatusBeforeRefundTx(tx, o.ID)
		o.Status = nextStatus
		o.UpdatedAt = now
		if err := tx.Save(&o).Error; err != nil {
			return err
		}
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

// orderFullyRefundedTx 判断订单商品是否已全部退完（总数量与已完成退款数量对比）
// 参数：
//   - tx: 数据库事务
//   - orderID: 订单 ID
//
// 返回值：
//   - bool: 是否全部退完
//   - error: 查询失败时返回错误
func orderFullyRefundedTx(tx *gorm.DB, orderID int64) (bool, error) {
	var total, refunded int64
	if err := tx.Model(&model.OrderItem{}).
		Where("order_id = ?", orderID).
		Select("COALESCE(SUM(quantity), 0)").
		Scan(&total).Error; err != nil {
		return false, err
	}
	if err := tx.Table("refund_items").
		Joins("JOIN refunds ON refunds.id = refund_items.refund_id").
		Where("refunds.order_id = ? AND refunds.status = 5", orderID).
		Select("COALESCE(SUM(refund_items.quantity), 0)").
		Scan(&refunded).Error; err != nil {
		return false, err
	}
	return total > 0 && refunded >= total, nil
}

// lastStatusBeforeRefundTx 查询订单最近一次进入退款中(7)之前的来源状态
// 来源状态非法（不在 2/3/4 范围）时兜底返回 2 已支付
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
	if log.FromStatus >= 2 && log.FromStatus <= 4 {
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
