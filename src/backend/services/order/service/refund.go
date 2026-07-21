package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

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

// ApplyRefund 申请退款
// 申请时自动关联订单最近一次成功支付的支付单号
// 参数：
//   - ctx: 上下文
//   - req: 申请请求
//
// 返回值：
//   - *model.Refund: 退款模型
//   - error: 错误信息
func (s *RefundService) ApplyRefund(ctx context.Context, req *order.ApplyRefundReq) (*model.Refund, error) {
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

	paymentNo := ""
	if s.paymentClient != nil {
		if p, err := s.paymentClient.GetPayment(ctx, &payment.GetPaymentReq{OrderId: req.OrderId}); err == nil && p != nil {
			paymentNo = p.PaymentNo
		}
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	refund := &model.Refund{
		RefundNo:  generateRefundNo(),
		OrderID:   req.OrderId,
		UserID:    req.UserId,
		PaymentNo: paymentNo,
		Amount:    req.Amount,
		Reason:    req.Reason,
		Status:    1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(refund).Error; err != nil {
			return err
		}
		o.Status = 7
		o.UpdatedAt = now
		if err := tx.Save(&o).Error; err != nil {
			return err
		}
		return createStatusLogTx(tx, o.ID, o.Status, 7, "user", req.UserId, "申请退款: "+req.Reason)
	}); err != nil {
		return nil, err
	}
	return refund, nil
}

// ListRefunds 查询退款列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.Refund: 退款列表
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
	err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// ApproveRefund 同意退款
// 先调用 payment-service 发起实际退款，退款成功后再更新订单状态并释放库存
// 支持通过 req.PaymentNo 透传支付单号；未透传时自动使用退款申请时记录的 payment_no
// 参数：
//   - ctx: 上下文
//   - req: 审核请求
//
// 返回值：
//   - error: 错误信息
func (s *RefundService) ApproveRefund(ctx context.Context, req *order.ApproveRefundReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refund model.Refund
		if err := tx.First(&refund, req.Id).Error; err != nil {
			return err
		}
		if refund.Status != 1 {
			return fmt.Errorf("退款状态不允许审核")
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		refund.Status = 2
		refund.OperatorID = req.OperatorId
		refund.UpdatedAt = now
		if err := tx.Save(&refund).Error; err != nil {
			return err
		}

		paymentNo := req.PaymentNo
		if paymentNo == "" {
			paymentNo = refund.PaymentNo
		}

		if s.paymentClient != nil {
			_, err := s.paymentClient.ProcessRefund(ctx, &payment.ProcessRefundReq{
				OrderId:   refund.OrderID,
				RefundNo:  refund.RefundNo,
				Amount:    refund.Amount,
				PaymentNo: paymentNo,
			})
			if err != nil {
				return fmt.Errorf("调用支付退款失败: %w", err)
			}
		}

		var o model.Order
		if err := tx.First(&o, refund.OrderID).Error; err != nil {
			return err
		}

		var items []*model.OrderItem
		if err := tx.Where("order_id = ?", o.ID).Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			if _, err := grpcclient.ProductClient.ReleaseStock(ctx, &product.ReleaseStockReq{
				SkuId:    item.SkuID,
				Quantity: item.Quantity,
				OrderId:  o.ID,
			}); err != nil {
				return fmt.Errorf("释放库存失败: %w", err)
			}
		}

		refund.Status = 4
		refund.RefundTime = &now
		refund.UpdatedAt = now
		if err := tx.Save(&refund).Error; err != nil {
			return err
		}

		o.Status = 8
		o.UpdatedAt = now
		if err := tx.Save(&o).Error; err != nil {
			return err
		}
		return createStatusLogTx(tx, o.ID, 7, 8, "admin", req.OperatorId, "退款完成")
	})
}

// RejectRefund 拒绝退款
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
		o.Status = 2
		o.UpdatedAt = now
		if err := tx.Save(&o).Error; err != nil {
			return err
		}
		return createStatusLogTx(tx, o.ID, 7, 2, "admin", req.OperatorId, "拒绝退款: "+req.RejectReason)
	})
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
