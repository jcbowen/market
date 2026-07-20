package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

// PaymentService 支付业务服务
type PaymentService struct {
	db  *mysql.Instance
	cfg *config.Config
}

// NewPaymentService 创建支付业务服务
// 参数：
//   - db: MySQL 实例
//   - cfg: 全局配置
//
// 返回值：
//   - *PaymentService: 支付业务服务实例
func NewPaymentService(db *mysql.Instance, cfg *config.Config) *PaymentService {
	return &PaymentService{db: db, cfg: cfg}
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

// GetPayment 根据订单 ID 查询支付记录
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - *model.Payment: 支付记录模型
//   - error: 错误信息
func (s *PaymentService) GetPayment(ctx context.Context, orderID int64) (*model.Payment, error) {
	var p model.Payment
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ?", orderID).Order("id desc").First(&p).Error; err != nil {
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
	params, err := parseWechatCallback(req.Body)
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
// 创建退款记录，调用第三方支付退款，成功后更新订单状态为已退款
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

	var p model.Payment
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ? AND status = ?", req.OrderId, 2).First(&p).Error; err != nil {
		return fmt.Errorf("未找到有效支付单: %w", err)
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

		var err error
		if p.PayType == 1 {
			err = s.wechatRefund(ctx, &p, refund)
		} else {
			err = s.alipayRefund(ctx, &p, refund)
		}
		if err != nil {
			return err
		}

		refund.Status = 2
		refund.RefundTime = time.Now().Format("2006-01-02 15:04:05")
		refund.UpdatedAt = refund.RefundTime
		if err := tx.Save(refund).Error; err != nil {
			return err
		}

		p.Status = 4
		p.UpdatedAt = refund.RefundTime
		if err := tx.Save(&p).Error; err != nil {
			return err
		}

		_, err = grpcclient.OrderClient.UpdateOrderStatus(ctx, &order.UpdateOrderStatusReq{
			Id:           req.OrderId,
			Status:       8,
			OperatorType: "system",
			Remark:       "退款完成",
		})
		if err != nil {
			return fmt.Errorf("同步订单退款状态失败: %w", err)
		}
		return nil
	})
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
// 返回值：
//   - string: 支付单号
func generatePaymentNo() string {
	return "ZF" + time.Now().Format("20060102150405") + fmt.Sprintf("%04d", time.Now().Nanosecond()/1e5)
}

// _ 占位，避免 common 未使用告警
var _ = common.CommonResponse{}

// _ 占位，避免 json 未使用告警
var _ = json.Marshal
