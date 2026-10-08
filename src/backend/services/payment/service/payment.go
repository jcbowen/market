package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

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
	db              *mysql.Instance
	cfg             *config.Config
	wechatPayClient *wegopayment.Client
}

// NewPaymentService 创建支付业务服务
// 若微信支付 V3 配置完整，则自动初始化 wego 微信支付客户端
// 参数：
//   - db: MySQL 实例
//   - cfg: 全局配置
//
// 返回值：
//   - *PaymentService: 支付业务服务实例
func NewPaymentService(db *mysql.Instance, cfg *config.Config) *PaymentService {
	s := &PaymentService{db: db, cfg: cfg}
	if cfg.Wechat.IsPaymentConfigured() {
		// wego NewClient 在配置异常时会 panic，使用 recover 避免影响服务启动
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("初始化微信支付客户端失败: %v", r)
				}
			}()
			s.wechatPayClient = wegopayment.NewClient(&wegopayment.Config{
				AppID:        cfg.Wechat.AppID,
				MchID:        cfg.Wechat.MchID,
				APIV3Key:     cfg.Wechat.APIV3Key,
				CertSerialNo: cfg.Wechat.CertSerialNo,
				PrivateKey:   cfg.Wechat.PrivateKey,
				NotifyURL:    cfg.Wechat.NotifyURL,
			})
		}()
	}
	return s
}

// CreatePayment 创建支付单并生成第三方支付调起参数
// 现金/货到付款无需第三方调起参数，创建成功后直接同步订单状态为已支付
// 参数：
//   - ctx: 上下文
//   - req: 创建支付请求
//
// 返回值：
//   - *model.Payment: 支付记录模型
//   - string: 微信/支付宝调起参数 JSON，现金支付返回空字符串
//   - error: 错误信息
func (s *PaymentService) CreatePayment(ctx context.Context, req *payment.CreatePaymentReq) (*model.Payment, string, error) {
	if req.OrderId <= 0 || req.UserId <= 0 || req.Amount <= 0 {
		return nil, "", fmt.Errorf("参数错误")
	}
	if req.PayType != 1 && req.PayType != 2 && req.PayType != 3 {
		return nil, "", fmt.Errorf("不支持的支付方式")
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
	var err error
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
