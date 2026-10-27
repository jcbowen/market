package service

import (
	"context"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	orderModel "github.com/jcbowen/market/src/backend/services/order/model"
	payModel "github.com/jcbowen/market/src/backend/services/payment/model"
	"github.com/jcbowen/market/src/backend/services/product/model"
	userModel "github.com/jcbowen/market/src/backend/services/user/model"
)

// 本文件是数据包的「演示订单样本」段（scope=community, init_only=true）：
// 覆盖 状态 × 配送方式 × 来源 三维组合 + 待支付/已取消/退款中等生命周期单，
// 时间全部为相对偏移（SeedTime），导入时换算为绝对时间，避免种子老化失真：
//   - 待支付单 created 在 30 分钟超时窗内（防调度器自动取消）；
//   - 配送中单 deliver_time 在 7 天自动完成窗内；
//   - 已完成单在售后窗口内（可直接演示 C 端申请退款）。
// 每条样本带 community_key 标注归属社区：初始化按社区逐个导入时只落各自样本
// （订单号全局唯一，跨社区重复导入靠 community_key 过滤而不是判重跳过）。
// 收银台代客单（source=2）挂历史班次（pos 段先于本段导入）。

// SeedOrderItem 演示订单明细（快照字段随包携带；created_at 取订单创建时间）
type SeedOrderItem struct {
	SkuKey        string  `json:"sku_key"`
	ProductName   string  `json:"product_name"`
	SpecName      string  `json:"spec_name"`
	ProductImage  string  `json:"product_image"`
	Price         float64 `json:"price"`
	Quantity      int32   `json:"quantity"`
	TotalPrice    float64 `json:"total_price"`
	OriginalPrice float64 `json:"original_price"`
	PriceModified int32   `json:"price_modified"`
}

// SeedOrderPayment 演示支付单（status 恒为 2 支付成功；待支付/已取消单无支付行）
type SeedOrderPayment struct {
	PayType       int32     `json:"pay_type"` // 1微信 2支付宝 3货到付款 4收银台现金
	Amount        float64   `json:"amount"`
	TransactionID string    `json:"transaction_id"` // 现金单为空
	ConfirmSource int32     `json:"confirm_source"` // 1渠道回调 4线下/收银台
	ConfirmRef    string    `json:"confirm_ref"`
	PayTime       *SeedTime `json:"pay_time"` // created_at/updated_at 同支付时间
}

// SeedOrderStatusLog 演示订单状态日志（operator_type：user/system/admin）
type SeedOrderStatusLog struct {
	FromStatus  int32     `json:"from_status"`
	ToStatus    int32     `json:"to_status"`
	OperatorTyp string    `json:"operator_type"`
	OperatorKey string    `json:"operator_key"` // user → 包内用户 key；admin → 包内管理员 key；system → 空
	Remark      string    `json:"remark"`
	CreatedAt   *SeedTime `json:"created_at"`
}

// SeedOrderUsedCoupon 演示「已使用」用户券（随订单落库：user_coupons.status=2 且绑定订单，
// 与 user_assets 段的未使用券互补，还原「一人两券：一可用一已用」演示形态）
type SeedOrderUsedCoupon struct {
	CouponName   string `json:"coupon_name"`
	ExpireInDays int32  `json:"expire_in_days"`
}

// SeedOrder 演示订单（含明细/支付单/状态日志，单订单单事务）
type SeedOrder struct {
	Key            string               `json:"key"`
	CommunityKey   string               `json:"community_key"`
	OrderNo        string               `json:"order_no"`
	UserKey        string               `json:"user_key"`
	Status         int32                `json:"status"`
	TotalAmount    float64              `json:"total_amount"`
	DiscountAmount float64              `json:"discount_amount"`
	DeliveryFee    float64              `json:"delivery_fee"`
	PayAmount      float64              `json:"pay_amount"`
	DeliveryType   int32                `json:"delivery_type"`
	DeliveryTime   string               `json:"delivery_time"`
	AddressDetail  string               `json:"address_detail"` // 收货地址按「用户 + 地址详情」解析；空 = 无地址（自提/到店）
	ContactName    string               `json:"contact_name"`
	ContactPhone   string               `json:"contact_phone"`
	ContactAddress string               `json:"contact_address"`
	Remark         string               `json:"remark"`
	UsedCoupon     *SeedOrderUsedCoupon `json:"used_coupon"`
	PickupCode     string               `json:"pickup_code"` // 空 = 无核销码
	Source         int32                `json:"source"`
	OperatorKey    string               `json:"operator_key"` // 收银台代客单的经手管理员
	OperatorName   string               `json:"operator_name"`
	ManualDiscount float64              `json:"manual_discount"`
	ShiftKey       string               `json:"shift_key"` // 收银班次（pos 段注册映射）
	ExpressCompany string               `json:"express_company"`
	ExpressNo      string               `json:"express_no"`
	CreatedAt      *SeedTime            `json:"created_at"`
	PayTime        *SeedTime            `json:"pay_time"`
	DeliverTime    *SeedTime            `json:"deliver_time"`
	CompleteTime   *SeedTime            `json:"complete_time"`
	CancelTime     *SeedTime            `json:"cancel_time"`
	CancelReason   string               `json:"cancel_reason"`
	Payments       []SeedOrderPayment   `json:"payments"`
	StatusLogs     []SeedOrderStatusLog `json:"status_logs"`
	Items          []SeedOrderItem      `json:"items"`
}

// ordersSection 演示订单样本
type ordersSection struct {
	svc *DataImportService
}

func (s *ordersSection) Key() string    { return "orders" }
func (s *ordersSection) Name() string   { return "订单样本" }
func (s *ordersSection) Scope() string  { return SectionScopeCommunity }
func (s *ordersSection) InitOnly() bool { return true }
func (s *ordersSection) Description() string {
	return "状态×配送方式×来源组合订单（含支付单与状态时间线），仅随初始化导入"
}

func (s *ordersSection) ItemCount(pkg *SeedPackage) int32 { return int32(len(pkg.Orders)) }

// Preview init_only 段不参与应用中心预检（HTTP 链路已过滤，此实现兜底空操作）
func (s *ordersSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
}

// Import 逐订单导入（单订单单事务：订单+明细+支付单+状态日志+已用券原子落库）；
// 按订单号幂等，跳过的订单同样注册 key 映射（订单号/明细行）供售后段引用
func (s *ordersSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	now := time.Now()
	for i := range pkg.Orders {
		o := &pkg.Orders[i]
		if !isImportable(req, o.Key) {
			continue
		}
		// 社区归属过滤：样本只在归属社区导入（订单号全局唯一，不做跨社区复制）
		if o.CommunityKey == "" || o.CommunityKey != req.CommunityKey {
			continue
		}
		// 幂等：同订单号已存在即跳过（仍注册映射，保证重复执行后下游段引用不断）
		var exist orderModel.Order
		if err := s.svc.db.GetDb().WithContext(ctx).
			Where("order_no = ?", o.OrderNo).First(&exist).Error; err == nil {
			s.registerOrderRefs(req, o, exist.ID)
			// 明细行映射按落库顺序补注册（售后段退款明细按行下标引用）
			if itemIDs, ierr := s.lookupOrderItemIDs(ctx, exist.ID); ierr == nil {
				for idx, itemID := range itemIDs {
					req.Refs.Set("orderItem:"+o.Key+"#"+strconv.Itoa(idx), itemID)
				}
			}
			appendImportResult(items, s.Key(), o.Key, o.OrderNo, "skipped", "订单已存在")
			continue
		}
		if err := s.importOneOrder(ctx, pkg, req, o, now); err != nil {
			appendImportResult(items, s.Key(), o.Key, o.OrderNo, "failed", "订单导入失败："+err.Error())
			continue
		}
		appendImportResult(items, s.Key(), o.Key, o.OrderNo, "created", "订单样本已导入")
		created++
	}
	return created
}

// registerOrderRefs 注册订单/明细行 key → ID 映射（售后段退款明细按明细行索引引用）
func (s *ordersSection) registerOrderRefs(req *ImportRequest, o *SeedOrder, orderID int64) {
	req.Refs.Set("order:"+o.Key, orderID)
	req.Refs.Set("orderNo:"+o.OrderNo, orderID)
}

// lookupOrderItemIDs 读取订单已落库的明细行 ID（按 id 升序，与包内 items 顺序一致）
func (s *ordersSection) lookupOrderItemIDs(ctx context.Context, orderID int64) ([]int64, error) {
	var rows []orderModel.OrderItem
	if err := s.svc.db.GetDb().WithContext(ctx).
		Where("order_id = ?", orderID).Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	return ids, nil
}

// importOneOrder 单订单事务导入
func (s *ordersSection) importOneOrder(ctx context.Context, pkg *SeedPackage, req *ImportRequest, o *SeedOrder, now time.Time) error {
	return s.svc.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 种子时间统一先解析成绝对串（Create 的 BeforeCreate 钩子会覆盖结构体时间字段，
		// 回填必须用 Create 前解析的局部值）
		createdStr := o.CreatedAt.Resolve(now)
		userID, err := s.svc.resolveSeedOperatorRef(ctx, pkg, req.Refs, "user", o.UserKey)
		if err != nil {
			return err
		}
		// 收货地址：按「用户 + 地址详情」解析（users 段种子地址的唯一业务键）
		var addressID int64
		if o.AddressDetail != "" {
			var addr userModel.Address
			if err := tx.Where("user_id = ? AND detail = ?", userID, o.AddressDetail).
				First(&addr).Error; err == nil {
				addressID = addr.ID
			}
		}
		// 收银班次（pos 段注册的 key 映射；缺失不阻断——班次仅作归属展示）
		var shiftID int64
		if o.ShiftKey != "" {
			shiftID, _ = req.Refs.Get("posShift:" + o.ShiftKey)
		}
		// 经手人（收银台代客单）
		var operatorID int64
		if o.OperatorKey != "" {
			operatorID, err = s.svc.resolveSeedOperatorRef(ctx, pkg, req.Refs, "admin", o.OperatorKey)
			if err != nil {
				return err
			}
		}

		row := &orderModel.Order{
			OrderNo:        o.OrderNo,
			CommunityID:    req.CommunityID,
			UserID:         userID,
			Status:         o.Status,
			TotalAmount:    o.TotalAmount,
			DiscountAmount: o.DiscountAmount,
			DeliveryFee:    o.DeliveryFee,
			PayAmount:      o.PayAmount,
			DeliveryType:   o.DeliveryType,
			DeliveryTime:   o.DeliveryTime,
			AddressID:      addressID,
			ContactName:    o.ContactName,
			ContactPhone:   o.ContactPhone,
			ContactAddress: o.ContactAddress,
			ExpressCompany: o.ExpressCompany,
			ExpressNo:      o.ExpressNo,
			Remark:         o.Remark,
			Source:         o.Source,
			OperatorID:     operatorID,
			OperatorName:   o.OperatorName,
			ManualDiscount: o.ManualDiscount,
			ShiftID:        shiftID,
			PayTime:        o.PayTime.ResolvePtr(now),
			DeliverTime:    o.DeliverTime.ResolvePtr(now),
			CompleteTime:   o.CompleteTime.ResolvePtr(now),
			CancelTime:     o.CancelTime.ResolvePtr(now),
			CancelReason:   o.CancelReason,
			CreatedAt:      o.CreatedAt.Resolve(now),
			UpdatedAt:      o.CreatedAt.Resolve(now),
		}
		if o.PickupCode != "" {
			row.PickupCode = &o.PickupCode
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		// BeforeCreate 钩子无条件覆盖 CreatedAt/UpdatedAt：回填种子相对时间
		// （updated_at = 最后一个里程碑，驱动调度器超时窗/自动完成扫描的正确语义）
		backfillTimestamps(tx, &orderModel.Order{}, row.ID, createdStr,
			latestSeedTime(now, o.PayTime, o.DeliverTime, o.CompleteTime, o.CancelTime, o.CreatedAt))
		s.registerOrderRefs(req, o, row.ID)

		// 明细（快照字段随包携带；sku_id/product_id 从目录段映射解析）
		skuByKey := seedSkuByKey(pkg)
		for i := range o.Items {
			it := &o.Items[i]
			skuID, _ := req.Refs.Get(it.SkuKey)
			productID := int64(0)
			if sku := skuByKey[it.SkuKey]; sku != nil {
				productID, _ = req.Refs.Get(sku.ProductKey)
			}
			item := &orderModel.OrderItem{
				OrderID:       row.ID,
				ProductID:     productID,
				SkuID:         skuID,
				ProductName:   it.ProductName,
				SpecName:      it.SpecName,
				ProductImage:  it.ProductImage,
				Price:         it.Price,
				Quantity:      it.Quantity,
				TotalPrice:    it.TotalPrice,
				OriginalPrice: it.OriginalPrice,
				PriceModified: it.PriceModified,
			}
			if err := tx.Create(item).Error; err != nil {
				return err
			}
			backfillTimestamps(tx, &orderModel.OrderItem{}, item.ID, createdStr, "")
			req.Refs.Set("orderItem:"+o.Key+"#"+strconv.Itoa(i), item.ID)
		}

		// 支付单（pending_flag 离开待支付后为 NULL；community_id 落订单社区）
		for i := range o.Payments {
			p := &o.Payments[i]
			payTime := p.PayTime.Resolve(now)
			payment := &payModel.Payment{
				PaymentNo:     "SEEDPAY-" + o.OrderNo + "-" + strconv.Itoa(i),
				OrderID:       row.ID,
				OrderNo:       o.OrderNo,
				UserID:        userID,
				CommunityID:   req.CommunityID,
				PayType:       p.PayType,
				Amount:        p.Amount,
				Status:        2,
				TransactionID: p.TransactionID,
				PayTime:       &payTime,
				ConfirmSource: p.ConfirmSource,
				ConfirmRef:    p.ConfirmRef,
				CreatedAt:     payTime,
				UpdatedAt:     payTime,
			}
			if err := tx.Create(payment).Error; err != nil {
				return err
			}
			backfillTimestamps(tx, &payModel.Payment{}, payment.ID, payTime, payTime)
			req.Refs.Set("payment:"+o.Key+"#"+strconv.Itoa(i), payment.ID)
		}

		// 状态时间线
		for i := range o.StatusLogs {
			l := &o.StatusLogs[i]
			operatorID, err := s.svc.resolveSeedOperatorRef(ctx, pkg, req.Refs, l.OperatorTyp, l.OperatorKey)
			if err != nil {
				return err
			}
			log := &orderModel.OrderStatusLog{
				OrderID:      row.ID,
				FromStatus:   l.FromStatus,
				ToStatus:     l.ToStatus,
				OperatorType: l.OperatorTyp,
				OperatorID:   operatorID,
				Remark:       l.Remark,
			}
			if err := tx.Create(log).Error; err != nil {
				return err
			}
			backfillTimestamps(tx, &orderModel.OrderStatusLog{}, log.ID, l.CreatedAt.Resolve(now), "")
		}

		// 已使用券：user_coupons.status=2 绑定本单，orders.coupon_id 回填
		if o.UsedCoupon != nil {
			var coupon model.Coupon
			if err := tx.Where("community_id = ? AND name = ?", req.CommunityID, o.UsedCoupon.CouponName).
				First(&coupon).Error; err != nil {
				return err
			}
			expireAt := now.Add(time.Duration(o.UsedCoupon.ExpireInDays) * 24 * time.Hour).Format("2006-01-02 15:04:05")
			uc := &model.UserCoupon{
				UserID:        userID,
				CouponID:      coupon.ID,
				Name:          coupon.Name,
				Type:          coupon.Type,
				Threshold:     coupon.Threshold,
				DiscountValue: coupon.DiscountValue,
				Status:        2,
				OrderID:       row.ID,
				Source:        1,
				ExpireAt:      &expireAt,
			}
			if err := tx.Create(uc).Error; err != nil {
				return err
			}
			backfillTimestamps(tx, &model.UserCoupon{}, uc.ID, createdStr, createdStr)
			if err := tx.Model(&orderModel.Order{}).Where("id = ?", row.ID).
				Update("coupon_id", uc.ID).Error; err != nil {
				return err
			}
			// 券核销计数（与用户券行一致；列表达式自增，避免读回值并发覆盖）
			if err := tx.Model(&model.Coupon{}).Where("id = ?", coupon.ID).
				UpdateColumn("used_count", gorm.Expr("used_count + 1")).Error; err != nil {
				return err
			}
			// 已用券同样是一次「领取」：received_count 须含本行，
			// 否则券模板领取量少于用户券行数，核销率与剩余可领口径失真
			if err := tx.Model(&model.Coupon{}).Where("id = ?", coupon.ID).
				UpdateColumn("received_count", gorm.Expr("received_count + 1")).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
