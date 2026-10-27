package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	orderModel "github.com/jcbowen/market/src/backend/services/order/model"
	payModel "github.com/jcbowen/market/src/backend/services/payment/model"
)

// 本文件是数据包的「演示售后样本」段（scope=community, init_only=true）：
// 整单退款完成（含运费）+ 部分退款审批中（渠道处理中）两种形态，
// 覆盖 refunds / refund_items / refund_logs / refund_payments 四表，
// 依赖订单段先导入（订单、支付单、订单明细行按 key 映射引用）。

// SeedRefundItem 演示退款明细（order_item 引用「订单 key#明细行下标」映射）
type SeedRefundItem struct {
	OrderKey     string  `json:"order_key"`
	ItemIndex    int32   `json:"item_index"`
	SkuKey       string  `json:"sku_key"`
	ProductName  string  `json:"product_name"`
	SpecName     string  `json:"spec_name"`
	Quantity     int32   `json:"quantity"`
	RefundAmount float64 `json:"refund_amount"`
}

// SeedRefundLog 演示退款审批日志（action：create/approve/reject/retry/give_up/reset）
type SeedRefundLog struct {
	Action      string    `json:"action"`
	OperatorTyp string    `json:"operator_type"`
	OperatorKey string    `json:"operator_key"`
	Remark      string    `json:"remark"`
	CreatedAt   *SeedTime `json:"created_at"`
}

// SeedRefundChannel 演示渠道退款流水（refund_payments 单行；status 1待退款 2退款成功 3退款失败）
type SeedRefundChannel struct {
	Status        int32     `json:"status"`
	TransactionID string    `json:"transaction_id"`
	CreatedAt     *SeedTime `json:"created_at"`
	RefundTime    *SeedTime `json:"refund_time"`
}

// SeedRefund 演示退款单（状态：1待审核 2已同意 3已拒绝 4退款中 5已退款；类型：1整单退 2部分退）
type SeedRefund struct {
	Key            string             `json:"key"`
	CommunityKey   string             `json:"community_key"`
	RefundNo       string             `json:"refund_no"`
	OrderKey       string             `json:"order_key"`
	Amount         float64            `json:"amount"`
	FreightAmount  float64            `json:"freight_amount"`
	Type           int32              `json:"type"`
	Reason         string             `json:"reason"`
	Status         int32              `json:"status"`
	OperatorKey    string             `json:"operator_key"`
	RefundTime     *SeedTime          `json:"refund_time"`
	LastCheckedAt  *SeedTime          `json:"last_checked_at"`
	CreatedAt      *SeedTime          `json:"created_at"`
	Items          []SeedRefundItem   `json:"items"`
	Logs           []SeedRefundLog    `json:"logs"`
	ChannelPayment *SeedRefundChannel `json:"channel_payment"`
}

// refundsSection 演示售后样本
type refundsSection struct {
	svc *DataImportService
}

func (s *refundsSection) Key() string    { return "refunds" }
func (s *refundsSection) Name() string   { return "售后样本" }
func (s *refundsSection) Scope() string  { return SectionScopeCommunity }
func (s *refundsSection) InitOnly() bool { return true }
func (s *refundsSection) Description() string {
	return "整单退款完成 + 部分退款审批中（含审批日志与渠道退款流水），仅随初始化导入"
}

func (s *refundsSection) ItemCount(pkg *SeedPackage) int32 { return int32(len(pkg.Refunds)) }

func (s *refundsSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
}

// Import 逐退款单导入（单退款单单事务）；按退款单号幂等
func (s *refundsSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	now := time.Now()
	for i := range pkg.Refunds {
		r := &pkg.Refunds[i]
		if !isImportable(req, r.Key) {
			continue
		}
		if r.CommunityKey == "" || r.CommunityKey != req.CommunityKey {
			continue
		}
		var count int64
		s.svc.db.GetDb().WithContext(ctx).Model(&orderModel.Refund{}).
			Where("refund_no = ?", r.RefundNo).Count(&count)
		if count > 0 {
			appendImportResult(items, s.Key(), r.Key, r.RefundNo, "skipped", "退款单已存在")
			continue
		}
		if err := s.importOneRefund(ctx, pkg, req, r, now); err != nil {
			appendImportResult(items, s.Key(), r.Key, r.RefundNo, "failed", "退款单导入失败："+err.Error())
			continue
		}
		appendImportResult(items, s.Key(), r.Key, r.RefundNo, "created", "售后样本已导入")
		created++
	}
	return created
}

// importOneRefund 单退款单事务导入
func (s *refundsSection) importOneRefund(ctx context.Context, pkg *SeedPackage, req *ImportRequest, r *SeedRefund, now time.Time) error {
	return s.svc.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 种子时间 Create 前先解析（钩子会覆盖结构体时间字段）
		createdStr := r.CreatedAt.Resolve(now)
		orderID, ok := req.Refs.Get("order:" + r.OrderKey)
		if !ok || orderID == 0 {
			// 兜底按订单号解析（订单段注册 orderNo: 前缀映射；仍取不到即依赖缺失）
			return errOrderRefMissing(r.OrderKey)
		}
		var order orderModel.Order
		if err := tx.Where("id = ?", orderID).First(&order).Error; err != nil {
			return err
		}
		// 关联支付单号：取该订单最近一笔支付成功单（演示单每订单至多一笔）
		var payment payModel.Payment
		if err := tx.Where("order_id = ? AND status = ?", orderID, payModel.PaymentStatusSuccess).
			Order("id desc").First(&payment).Error; err != nil {
			return err
		}
		userID := order.UserID

		row := &orderModel.Refund{
			RefundNo:      r.RefundNo,
			OrderID:       orderID,
			UserID:        userID,
			PaymentNo:     payment.PaymentNo,
			Amount:        r.Amount,
			FreightAmount: r.FreightAmount,
			Type:          r.Type,
			Reason:        r.Reason,
			Status:        r.Status,
			OperatorID:    0,
			RefundTime:    r.RefundTime.ResolvePtr(now),
			LastCheckedAt: r.LastCheckedAt.ResolvePtr(now),
		}
		if r.OperatorKey != "" {
			operatorID, err := s.svc.resolveSeedOperatorRef(ctx, pkg, req.Refs, "admin", r.OperatorKey)
			if err != nil {
				return err
			}
			row.OperatorID = operatorID
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		// BeforeCreate 钩子覆盖时间：回填（updated_at = 退款完成/最近核查，驱动卡单重试游标语义）
		backfillTimestamps(tx, &orderModel.Refund{}, row.ID, createdStr,
			latestSeedTime(now, r.RefundTime, r.LastCheckedAt, r.CreatedAt))
		req.Refs.Set("refund:"+r.Key, row.ID)

		// 退款明细（订单明细行 ID 从订单段映射解析；sku_id 从目录段映射解析）
		for i := range r.Items {
			ri := &r.Items[i]
			orderItemID, _ := req.Refs.Get("orderItem:" + ri.OrderKey + "#" + strconv.Itoa(int(ri.ItemIndex)))
			skuID, _ := req.Refs.Get(ri.SkuKey)
			item := &orderModel.RefundItem{
				RefundID:     row.ID,
				OrderItemID:  orderItemID,
				SkuID:        skuID,
				ProductName:  ri.ProductName,
				SpecName:     ri.SpecName,
				Quantity:     ri.Quantity,
				RefundAmount: ri.RefundAmount,
			}
			if err := tx.Create(item).Error; err != nil {
				return err
			}
			backfillTimestamps(tx, &orderModel.RefundItem{}, item.ID, createdStr, "")
		}

		// 审批日志
		for i := range r.Logs {
			l := &r.Logs[i]
			operatorID, err := s.svc.resolveSeedOperatorRef(ctx, pkg, req.Refs, l.OperatorTyp, l.OperatorKey)
			if err != nil {
				return err
			}
			log := &orderModel.RefundLog{
				RefundID:     row.ID,
				Action:       l.Action,
				OperatorType: l.OperatorTyp,
				OperatorID:   operatorID,
				Remark:       l.Remark,
			}
			if err := tx.Create(log).Error; err != nil {
				return err
			}
			backfillTimestamps(tx, &orderModel.RefundLog{}, log.ID, l.CreatedAt.Resolve(now), "")
		}

		// 渠道退款流水
		if c := r.ChannelPayment; c != nil {
			rp := &payModel.RefundPayment{
				RefundNo:      r.RefundNo,
				PaymentNo:     payment.PaymentNo,
				OrderID:       orderID,
				Amount:        r.Amount,
				Status:        c.Status,
				TransactionID: c.TransactionID,
				RefundTime:    c.RefundTime.ResolvePtr(now),
			}
			if err := tx.Create(rp).Error; err != nil {
				return err
			}
			backfillTimestamps(tx, &payModel.RefundPayment{}, rp.ID, c.CreatedAt.Resolve(now),
				latestSeedTime(now, c.RefundTime, c.CreatedAt))
		}
		return nil
	})
}

// errOrderRefMissing 订单引用缺失（订单段未导入或映射断裂）
func errOrderRefMissing(orderKey string) error {
	return errors.New("依赖缺失：订单样本未导入（" + orderKey + "）")
}
