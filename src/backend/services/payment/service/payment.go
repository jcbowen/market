package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/payment/model"
	wegopayment "github.com/jcbowen/wego/payment"
)

// PaymentService 支付业务服务
type PaymentService struct {
	db  *mysql.Instance
	cfg *config.Config

	// payMu 保护以下支付配置缓存字段的并发读写
	payMu sync.RWMutex
	// payCfg 数据库支付配置的内存副本（pay_config 单行），updated_at 兼作版本号
	payCfg *model.PayConfig

	wechatPayClient *wegopayment.Client
}

// NewPaymentService 创建支付业务服务
// 支付配置从数据库 pay_config 表加载（配置变更后各实例按版本号自动热更新，无需重启）
// 参数：
//   - db: MySQL 实例
//   - cfg: 全局配置
//
// 返回值：
//   - *PaymentService: 支付业务服务实例
func NewPaymentService(db *mysql.Instance, cfg *config.Config) *PaymentService {
	s := &PaymentService{db: db, cfg: cfg}
	if err := s.EnsureDefaultPayConfig(); err != nil {
		log.Printf("初始化支付配置失败: %v", err)
	}
	s.reloadPayConfig()
	return s
}

// reloadPayConfig 启动时从数据库加载支付配置并构建支付客户端
func (s *PaymentService) reloadPayConfig() {
	var row model.PayConfig
	if err := s.db.GetDb().First(&row, payConfigID).Error; err != nil {
		log.Printf("加载数据库支付配置失败（支付功能将不可用，可在后台配置后自动生效）: %v", err)
		return
	}
	s.payMu.Lock()
	defer s.payMu.Unlock()
	s.applyPayConfig(&row)
}

// CreatePayment 创建支付单并生成第三方支付调起参数
// 现金/货到付款无需第三方调起参数，创建成功后直接同步订单状态为已支付
// 幂等策略：订单已有支付成功记录时拒绝创建；已有同方式同金额的待支付单时直接复用该记录
// 参数：
//   - ctx: 上下文
//   - req: 创建支付请求
//
// 返回值：
//   - *model.Payment: 支付记录模型
//   - string: 微信/支付宝调起参数 JSON，现金支付返回空字符串
//   - error: 错误信息
func (s *PaymentService) CreatePayment(ctx context.Context, req *payment.CreatePaymentReq) (*model.Payment, string, error) {
	if req.OrderId <= 0 || req.Amount <= 0 {
		return nil, "", fmt.Errorf("参数错误")
	}
	if req.PayType != 1 && req.PayType != 2 && req.PayType != 3 {
		return nil, "", fmt.Errorf("不支持的支付方式")
	}
	// user_id 允许为 0 的场景：收银台散客单现金收款（无会员账户，第三方渠道仍要求实名用户）
	if req.UserId <= 0 && req.PayType != 3 {
		return nil, "", fmt.Errorf("参数错误")
	}

	// 幂等校验：订单已存在支付成功的记录时拒绝再次创建支付单；
	// 查询异常（非"记录不存在"）时同样拒绝，避免 DB 故障让幂等保护失效而产生重复支付
	if _, err := s.GetPayment(ctx, req.OrderId); err == nil {
		return nil, "", bizerr.New(codes.AlreadyExists, "订单已支付，请勿重复支付")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("查询订单支付状态失败 order_id=%d: %v", req.OrderId, err)
		return nil, "", bizerr.New(codes.Internal, "支付状态查询失败，请稍后重试")
	}

	// 复用待支付单，避免重复向第三方下单
	pending, err := s.findReusablePendingPayment(ctx, req)
	if err != nil {
		return nil, "", err
	}
	if pending != nil {
		return pending, pending.PayParams, nil
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	paymentNo := generatePaymentNo()

	p := &model.Payment{
		PaymentNo: paymentNo,
		OrderID:   req.OrderId,
		UserID:    req.UserId,
		PayType:   req.PayType,
		Amount:    req.Amount,
		Status:    1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	var payParams string
	switch req.PayType {
	case 1:
		payParams, err = s.buildWechatPayParams(ctx, p, req.Openid)
	case 2:
		payParams, err = s.buildAlipayParams(ctx, p)
	case 3:
		// 货到付款直接标记为支付成功，并同步订单状态
		p.Status = 2
		p.PayTime = &now
	}
	if err != nil {
		return nil, "", err
	}
	p.PayParams = payParams

	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(p).Error; err != nil {
			return err
		}
		if req.PayType == 3 {
			_, err = grpcclient.OrderClient.UpdateOrderStatus(ctx, &order.UpdateOrderStatusReq{
				Id:           req.OrderId,
				Status:       2,
				OperatorType: "system",
				Remark:       "货到付款，支付成功",
			})
			if err != nil {
				return fmt.Errorf("同步订单支付状态失败: %w", err)
			}
		}
		return nil
	}); err != nil {
		return nil, "", err
	}
	return p, payParams, nil
}

// findReusablePendingPayment 查找可复用的待支付支付单
// 仅微信/支付宝这类需要第三方调起参数的渠道才复用，避免重复向第三方下单产生多笔可实际扣款的支付单；
// 现金支付创建时即置为已支付并同步订单状态，不复用异常遗留的待支付现金单，否则会跳过该副作用导致订单停留在待支付
// 参数：
//   - ctx: 上下文
//   - req: 创建支付请求
//
// 返回值：
//   - *model.Payment: 可复用的待支付单，渠道不支持复用或不存在时返回 nil
//   - error: 查询异常时返回内部错误（不含"记录不存在"），避免故障被当作无待支付单而重复下单
func (s *PaymentService) findReusablePendingPayment(ctx context.Context, req *payment.CreatePaymentReq) (*model.Payment, error) {
	if req.PayType != 1 && req.PayType != 2 {
		return nil, nil
	}

	var pending model.Payment
	err := s.db.GetDb().WithContext(ctx).
		Where("order_id = ? AND pay_type = ? AND amount = ? AND status = ?", req.OrderId, req.PayType, req.Amount, 1).
		Order("id desc").First(&pending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		log.Printf("查询可复用待支付单失败 order_id=%d: %v", req.OrderId, err)
		return nil, bizerr.New(codes.Internal, "支付单查询失败，请稍后重试")
	}
	return &pending, nil
}

// GetPayment 根据订单 ID 查询最新成功支付记录
// 多次支付场景下返回该订单最近一次支付成功的记录
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - *model.Payment: 支付记录模型
//   - error: 错误信息
func (s *PaymentService) GetPayment(ctx context.Context, orderID int64) (*model.Payment, error) {
	var p model.Payment
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ? AND status = ?", orderID, 2).Order("id desc").First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// HandleWechatCallback 处理微信支付回调
// 解析回调体，更新支付单状态，并同步订单状态为已支付
// 参数：
//   - ctx: 上下文
//   - req: 微信支付回调请求
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) HandleWechatCallback(ctx context.Context, req *payment.WechatCallbackReq) error {
	headers := make(http.Header)
	for k, v := range req.Headers {
		headers.Set(k, v)
	}
	params, err := parseWechatCallback(s, req.Body, headers)
	if err != nil {
		return err
	}
	return s.handlePaySuccess(ctx, params)
}

// HandleAlipayCallback 处理支付宝支付回调
// 解析回调参数，更新支付单状态，并同步订单状态为已支付
// 参数：
//   - ctx: 上下文
//   - req: 支付宝回调请求
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) HandleAlipayCallback(ctx context.Context, req *payment.AlipayCallbackReq) error {
	params, err := parseAlipayCallback(req.Params)
	if err != nil {
		return err
	}
	return s.handlePaySuccess(ctx, params)
}

// ProcessRefund 处理退款
// 按 payment_no 找到对应支付单后创建退款记录，支持一笔支付单多次部分退款
// 通过 refund_no 唯一索引保证幂等，重复提交时直接返回成功
// 参数：
//   - ctx: 上下文
//   - req: 退款请求
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) ProcessRefund(ctx context.Context, req *payment.ProcessRefundReq) error {
	if req.OrderId <= 0 || req.RefundNo == "" || req.Amount <= 0 {
		return fmt.Errorf("退款参数错误")
	}

	// 幂等：已存在相同 refund_no 的退款记录则直接返回成功
	var exists model.RefundPayment
	if err := s.db.GetDb().WithContext(ctx).Where("refund_no = ?", req.RefundNo).First(&exists).Error; err == nil {
		return nil
	}

	p, err := s.findRefundPayment(ctx, req.OrderId, req.PaymentNo)
	if err != nil {
		return err
	}

	// 货到付款（pay_type=3）不经过第三方退款，直接完成
	if p.PayType == 3 {
		return s.recordCashRefund(ctx, req, p)
	}

	refunded, err := s.getRefundedAmount(ctx, p.PaymentNo)
	if err != nil {
		return err
	}
	if refunded+req.Amount > p.Amount {
		return fmt.Errorf("退款金额超过剩余可退金额")
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	refund := &model.RefundPayment{
		RefundNo:  req.RefundNo,
		PaymentNo: p.PaymentNo,
		OrderID:   req.OrderId,
		Amount:    req.Amount,
		Status:    1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(refund).Error; err != nil {
			return err
		}

		var refundErr error
		if p.PayType == 1 {
			refundErr = s.wechatRefund(ctx, p, refund)
		} else {
			refundErr = s.alipayRefund(ctx, p, refund)
		}
		if refundErr != nil {
			return refundErr
		}

		refund.Status = 2
		refundTime := time.Now().Format("2006-01-02 15:04:05")
		refund.RefundTime = &refundTime
		refund.UpdatedAt = refundTime
		if err := tx.Save(refund).Error; err != nil {
			return err
		}

		// 累计退款金额等于支付金额时才标记支付单为已退款
		if totalRefunded, err := s.getRefundedAmountTx(tx, p.PaymentNo); err != nil {
			return err
		} else if totalRefunded >= p.Amount {
			p.Status = 4
			p.UpdatedAt = refundTime
			if err := tx.Save(p).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// findRefundPayment 查找退款对应的原支付单
// 若指定 payment_no 则直接查询；否则返回该订单最近一次支付成功记录
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - paymentNo: 支付单号，可为空
//
// 返回值：
//   - *model.Payment: 支付记录
//   - error: 错误信息
func (s *PaymentService) findRefundPayment(ctx context.Context, orderID int64, paymentNo string) (*model.Payment, error) {
	if paymentNo != "" {
		var p model.Payment
		if err := s.db.GetDb().WithContext(ctx).Where("payment_no = ? AND status = ?", paymentNo, 2).First(&p).Error; err != nil {
			return nil, fmt.Errorf("未找到指定支付单: %w", err)
		}
		return &p, nil
	}
	p, err := s.GetPayment(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("未找到有效支付单: %w", err)
	}
	return p, nil
}

// recordCashRefund 记录货到付款退款
// 现金/货到付款不调用第三方支付，仅记录退款流水
// 参数：
//   - ctx: 上下文
//   - req: 退款请求
//   - p: 原支付记录
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) recordCashRefund(ctx context.Context, req *payment.ProcessRefundReq, p *model.Payment) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	refund := &model.RefundPayment{
		RefundNo:      req.RefundNo,
		PaymentNo:     p.PaymentNo,
		OrderID:       req.OrderId,
		Amount:        req.Amount,
		Status:        2,
		TransactionID: "CASH_REFUND",
		RefundTime:    &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	return s.db.GetDb().WithContext(ctx).Create(refund).Error
}

// getRefundedAmount 统计支付单已成功退款的累计金额
// 参数：
//   - ctx: 上下文
//   - paymentNo: 支付单号
//
// 返回值：
//   - float64: 累计退款金额
//   - error: 错误信息
func (s *PaymentService) getRefundedAmount(ctx context.Context, paymentNo string) (float64, error) {
	var total float64
	err := s.db.GetDb().WithContext(ctx).Model(&model.RefundPayment{}).
		Select("COALESCE(SUM(amount), 0)").
		Where("payment_no = ? AND status = ?", paymentNo, 2).
		Scan(&total).Error
	return total, err
}

// getRefundedAmountTx 在事务中统计支付单已成功退款的累计金额
// 参数：
//   - tx: 数据库事务
//   - paymentNo: 支付单号
//
// 返回值：
//   - float64: 累计退款金额
//   - error: 错误信息
func (s *PaymentService) getRefundedAmountTx(tx *gorm.DB, paymentNo string) (float64, error) {
	var total float64
	err := tx.Model(&model.RefundPayment{}).
		Select("COALESCE(SUM(amount), 0)").
		Where("payment_no = ? AND status = ?", paymentNo, 2).
		Scan(&total).Error
	return total, err
}

// handlePaySuccess 统一处理支付成功逻辑
// 参数：
//   - ctx: 上下文
//   - params: 解析后的回调参数
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) handlePaySuccess(ctx context.Context, params *payCallbackParams) error {
	if params.PaymentNo == "" || params.TransactionID == "" {
		return fmt.Errorf("回调参数缺失")
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p model.Payment
		if err := tx.Where("payment_no = ?", params.PaymentNo).First(&p).Error; err != nil {
			return err
		}
		if p.Status != 1 {
			return nil
		}

		p.Status = 2
		p.TransactionID = params.TransactionID
		p.PayTime = &now
		p.UpdatedAt = now
		if err := tx.Save(&p).Error; err != nil {
			return err
		}

		_, err := grpcclient.OrderClient.UpdateOrderStatus(ctx, &order.UpdateOrderStatusReq{
			Id:           p.OrderID,
			Status:       2,
			OperatorType: "system",
			Remark:       "支付成功",
		})
		if err != nil {
			return fmt.Errorf("同步订单支付状态失败: %w", err)
		}
		return nil
	})
}

// payCallbackParams 支付回调解析后的通用参数
type payCallbackParams struct {
	PaymentNo     string
	TransactionID string
	Amount        float64
}

// ToPaymentInfo 将支付模型转换为 proto 信息
// 参数：
//   - p: 支付记录模型
//
// 返回值：
//   - *payment.PaymentInfo: proto 支付信息
func ToPaymentInfo(p *model.Payment) *payment.PaymentInfo {
	if p == nil {
		return nil
	}
	return &payment.PaymentInfo{
		Id:            p.ID,
		PaymentNo:     p.PaymentNo,
		OrderId:       p.OrderID,
		UserId:        p.UserID,
		PayType:       p.PayType,
		Amount:        p.Amount,
		Status:        p.Status,
		TransactionId: p.TransactionID,
		PayTime:       ptrString(p.PayTime),
	}
}

// ptrString 将字符串指针安全转换为字符串
//
// 参数：
//   - s: 字符串指针
//
// 返回值：
//   - string: 空字符串或指针指向的值
func ptrString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// generatePaymentNo 生成支付单号
// 使用 UUID 替代时间戳+纳秒，避免高并发下重复
// 返回值：
//   - string: 支付单号
func generatePaymentNo() string {
	return "ZF" + uuid.New().String()
}

// _ 占位，避免 common 未使用告警
var _ = common.CommonResponse{}

// _ 占位，避免 json 未使用告警
var _ = json.Marshal
