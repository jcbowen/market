package service

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

// OrderService 订单业务服务
type OrderService struct {
	db *mysql.Instance
}

// NewOrderService 创建订单业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *OrderService: 订单业务服务实例
func NewOrderService(db *mysql.Instance) *OrderService {
	return &OrderService{db: db}
}

// CreateOrder 创建订单
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.Order: 订单模型
//   - error: 错误信息
func (s *OrderService) CreateOrder(ctx context.Context, req *order.CreateOrderReq) (*model.Order, error) {
	if len(req.Items) == 0 {
		return nil, bizerr.New(codes.InvalidArgument, "订单商品不能为空")
	}
	if req.DeliveryType != 1 && req.DeliveryType != 2 {
		return nil, bizerr.New(codes.InvalidArgument, "配送类型不合法，仅支持 1（自提）或 2（配送）")
	}

	// 社区信息统一获取（一次 RPC，自提/配送共用）：
	// - 停业状态校验：无论自提还是配送，停业社区一律不允许下单。
	//   C 端本地缓存的社区可能已停业（前端缓存无法实时感知后台停业操作），此处做服务端兜底
	// - 配送订单还需起送金额与配送费
	// 放在商品循环之前：停业时直接短路，不必白跑逐个商品的 RPC
	communityInfo, err := grpcclient.UserClient.GetCommunity(ctx, &user.GetCommunityReq{Id: req.CommunityId})
	if err != nil {
		return nil, fmt.Errorf("获取社区信息失败: %w", err)
	}
	if communityInfo.Status != 1 {
		return nil, bizerr.New(codes.FailedPrecondition, "当前社区已停业，暂不支持下单，请切换社区")
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	var totalAmount float64
	var orderItems []*model.OrderItem
	// 免配送费范围判定上下文（参与商品额计算用，见 calcFreeShipping）
	var itemScopes []itemScope

	for _, item := range req.Items {
		skuInfo, err := grpcclient.ProductClient.GetSkuById(ctx, &product.GetSkuByIdReq{Id: item.SkuId})
		if err != nil {
			return nil, fmt.Errorf("获取 SKU 失败: %w", err)
		}
		if skuInfo.Stock < item.Quantity {
			return nil, bizerr.New(codes.ResourceExhausted, fmt.Sprintf("商品 %s 库存不足", skuInfo.SkuCode))
		}

		productInfo, err := grpcclient.ProductClient.GetProduct(ctx, &product.GetProductReq{Id: skuInfo.ProductId})
		if err != nil {
			return nil, fmt.Errorf("获取商品失败: %w", err)
		}

		// 优先使用生效的秒杀价
		price := skuInfo.Price
		if flashPrice, flashErr := s.getActiveFlashSalePrice(ctx, req.CommunityId, item.SkuId); flashErr == nil && flashPrice > 0 {
			price = flashPrice
		}

		totalPrice := price * float64(item.Quantity)
		totalAmount += totalPrice
		itemScopes = append(itemScopes, itemScope{
			productID:  skuInfo.ProductId,
			categoryID: productInfo.Product.CategoryId,
			totalPrice: totalPrice,
		})

		orderItems = append(orderItems, &model.OrderItem{
			ProductID:    skuInfo.ProductId,
			SkuID:        item.SkuId,
			ProductName:  productInfo.Product.Name,
			SpecName:     skuInfo.SpecName,
			ProductImage: productInfo.Product.CoverImage,
			Price:        price,
			Quantity:     item.Quantity,
			TotalPrice:   totalPrice,
			CreatedAt:    now,
		})
	}

	// 配送订单需计算配送费并校验起送金额（按商品总额校验，优惠不影响起送门槛）
	// 社区信息已在方法开头统一获取（communityInfo），此处不再重复调用
	var deliveryFee float64
	if req.DeliveryType == 2 {
		if communityInfo.MinOrderAmount > 0 && totalAmount < communityInfo.MinOrderAmount {
			return nil, bizerr.New(codes.FailedPrecondition, fmt.Sprintf("订单金额未满 %.2f 元起送金额", communityInfo.MinOrderAmount))
		}
		deliveryFee = communityInfo.DeliveryFee
		// 满额免配送费：参与商品额达门槛则免收配送费（口径见 calcFreeShipping）
		if s.calcFreeShipping(ctx, req.CommunityId, itemScopes).hit {
			deliveryFee = 0
		}
	}

	// 分别计算满减与优惠券优惠，两者均按商品总价校验门槛并可叠加
	fullReductionAmount := s.calculateFullReduction(ctx, req.CommunityId, totalAmount)

	// 优惠券是用户主动选择，校验失败（券不存在/已使用/门槛不满足）直接中断下单，
	// 避免页面展示优惠与订单实付不符的静默失败
	var discountAmount float64
	var userCouponID int64
	if req.CouponId > 0 {
		couponDiscount, ucID, err := s.applyCoupon(ctx, req.UserId, req.CouponId, itemScopes)
		if err != nil {
			return nil, bizerr.New(codes.FailedPrecondition, err.Error())
		}
		discountAmount = couponDiscount
		userCouponID = ucID
	}

	payAmount := totalAmount + deliveryFee - fullReductionAmount - discountAmount
	if payAmount < 0 {
		payAmount = 0
	}

	var contactName, contactPhone, contactAddress string
	if req.AddressId > 0 {
		addrResp, err := grpcclient.UserClient.GetAddress(ctx, &user.GetAddressReq{
			Id:     req.AddressId,
			UserId: req.UserId,
		})
		if err == nil && addrResp != nil {
			contactName = addrResp.ContactName
			contactPhone = addrResp.ContactPhone
			contactAddress = addrResp.Province + addrResp.City + addrResp.District + addrResp.Detail
		}
	} else if req.ContactName != "" || req.ContactPhone != "" || req.ContactAddress != "" {
		// 收银台代客下单：无地址簿，联系信息由收银员直填
		contactName = req.ContactName
		contactPhone = req.ContactPhone
		contactAddress = req.ContactAddress
	}

	// 订单来源：0/1=C端 2=收银台代客（决策点⑨）
	source := req.Source
	if source != 2 {
		source = 1
	}

	orderModel := &model.Order{
		OrderNo:        generateOrderNo(),
		CommunityID:    req.CommunityId,
		UserID:         req.UserId,
		Status:         1,
		TotalAmount:    totalAmount,
		DiscountAmount: fullReductionAmount + discountAmount,
		DeliveryFee:    deliveryFee,
		PayAmount:      payAmount,
		DeliveryType:   req.DeliveryType,
		DeliveryTime:   req.DeliveryTime,
		AddressID:      req.AddressId,
		ContactName:    contactName,
		ContactPhone:   contactPhone,
		ContactAddress: contactAddress,
		Remark:         req.Remark,
		CouponID:       req.CouponId,
		Source:         source,
		OperatorID:     req.OperatorId,
		OperatorName:   req.OperatorName,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	// 自提单生成取货核销码（C 端订单详情展示，履约人员/后台凭码核销）；
	// 预查重避免撞唯一索引（8 位数字码空间 1e8，常规重试一次即可命中）
	if req.DeliveryType == 1 {
		for i := 0; i < 5; i++ {
			code := genPickupCode()
			if code == nil {
				continue
			}
			var cnt int64
			if err := s.db.GetDb().WithContext(ctx).Model(&model.Order{}).Where("pickup_code = ?", *code).Count(&cnt).Error; err != nil {
				return nil, bizerr.New(codes.Internal, "生成取货码失败")
			}
			if cnt == 0 {
				orderModel.PickupCode = code
				break
			}
		}
		if orderModel.PickupCode == nil {
			return nil, bizerr.New(codes.Internal, "生成取货码失败，请重试")
		}
	}

	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(orderModel).Error; err != nil {
			return err
		}
		for _, item := range orderItems {
			item.OrderID = orderModel.ID
			if err := tx.Create(item).Error; err != nil {
				return err
			}
		}
		for _, item := range req.Items {
			_, err := grpcclient.ProductClient.DeductStock(ctx, &product.DeductStockReq{
				SkuId:    item.SkuId,
				Quantity: item.Quantity,
				OrderId:  orderModel.ID,
			})
			if err != nil {
				return fmt.Errorf("扣减库存失败: %w", err)
			}
		}
		if userCouponID > 0 {
			if _, err := grpcclient.ProductClient.UseCoupon(ctx, &product.UseCouponReq{
				UserCouponId: userCouponID,
				OrderId:      orderModel.ID,
			}); err != nil {
				return fmt.Errorf("使用优惠券失败: %w", err)
			}
		}
		// 创建日志操作者：收银台代客单记经手人（admin），C 端沿用 system
		logType, logOperator := "system", int64(0)
		if source == 2 {
			logType, logOperator = "admin", req.OperatorId
		}
		return s.createStatusLogTx(tx, orderModel.ID, 0, 1, logType, logOperator, "创建订单")
	}); err != nil {
		return nil, err
	}

	// 记录会员-社区关联：在该社区下过单即视为该社区会员（幂等，尽力而为不阻塞下单结果）；
	// 收银台散客单（user_id=0）无会员可标记，跳过
	if req.UserId > 0 {
		if _, mErr := grpcclient.UserClient.MarkUserCommunity(ctx, &user.MarkUserCommunityReq{
			UserId:      req.UserId,
			CommunityId: req.CommunityId,
			Source:      "order",
		}); mErr != nil {
			fmt.Printf("记录会员-社区关联失败（订单 %d）: %v\n", orderModel.ID, mErr)
		}
	}

	return orderModel, nil
}

// GetOrder 获取订单详情
// 参数：
//   - ctx: 上下文
//   - id: 订单 ID
//   - userID: 用户 ID，0 表示不校验
//
// 返回值：
//   - *model.Order: 订单模型
//   - []*model.OrderItem: 订单商品项
//   - []*model.OrderStatusLog: 状态日志
//   - error: 错误信息
func (s *OrderService) GetOrder(ctx context.Context, id, userID int64) (*model.Order, []*model.OrderItem, []*model.OrderStatusLog, error) {
	var o model.Order
	if err := s.db.GetDb().WithContext(ctx).First(&o, id).Error; err != nil {
		return nil, nil, nil, err
	}
	if userID > 0 && o.UserID != userID {
		return nil, nil, nil, fmt.Errorf("无权限查看该订单")
	}

	var items []*model.OrderItem
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ?", id).Find(&items).Error; err != nil {
		return nil, nil, nil, err
	}

	var logs []*model.OrderStatusLog
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ?", id).Order("id asc").Find(&logs).Error; err != nil {
		return nil, nil, nil, err
	}

	return &o, items, logs, nil
}

// 查询专用虚拟状态：仅在 ListOrders 入参中用于聚合筛选，不落库、不参与流转
//   - 9  → 退款/售后聚合（7 退款中、8 已退款）
//   - 90 → 待发货聚合（2 已支付、3 备货中）
const (
	queryStatusRefundAggregate   int32 = 9
	queryStatusShippingAggregate int32 = 90
)

// ListOrders 查询订单列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.Order: 订单列表
//   - int: 总条数
//   - error: 错误信息
func (s *OrderService) ListOrders(ctx context.Context, req *order.ListOrdersReq) ([]*model.Order, int, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.Order{})
	if req.UserId > 0 {
		db = db.Where("user_id = ?", req.UserId)
	}
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	// 订单来源筛选：1=C端 2=收银台（0=全部）
	if req.Source > 0 {
		db = db.Where("source = ?", req.Source)
	}
	// 后台管理端搜索条件（订单号模糊、下单时间范围、收货人姓名/电话模糊）
	if req.OrderNo != "" {
		db = db.Where("order_no LIKE ?", "%"+req.OrderNo+"%")
	}
	if req.StartTime != "" {
		db = db.Where("created_at >= ?", req.StartTime)
	}
	if req.EndTime != "" {
		// 前端传 YYYY-MM-DD 日期时补齐到当日末尾，保证闭区间语义
		end := req.EndTime
		if len(end) == 10 {
			end += " 23:59:59"
		}
		db = db.Where("created_at <= ?", end)
	}
	if req.ReceiverName != "" {
		db = db.Where("contact_name LIKE ?", "%"+req.ReceiverName+"%")
	}
	if req.ReceiverPhone != "" {
		db = db.Where("contact_phone LIKE ?", "%"+req.ReceiverPhone+"%")
	}
	switch {
	case req.Status == queryStatusRefundAggregate:
		db = db.Where("status IN ?", []int32{7, 8})
	case req.Status == queryStatusShippingAggregate:
		db = db.Where("status IN ?", []int32{2, 3})
	case req.Status > 0:
		db = db.Where("status = ?", req.Status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var list []*model.Order
	err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// cancelableStatuses 订单允许取消的状态集合
// status=1（待支付）恒可取消；货到付款单未实际收款，未发货（2已支付/3备货中）也可直接取消，
// 无需走退款审核（发货后 status=4 仍走退款/拒收流程）
func cancelableStatuses(isCashOnDelivery bool) []int32 {
	if isCashOnDelivery {
		return []int32{1, 2, 3}
	}
	return []int32{1}
}

// containsInt32 判断 int32 切片是否包含指定元素
func containsInt32(list []int32, item int32) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

// isCashOnDeliveryOrder 判断订单是否为货到付款（pay_type=3，查询支付单判定）
// 查询失败或无支付记录时按非货到付款处理（保守口径，不影响原有取消逻辑）
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - bool: 是否货到付款
func (s *OrderService) isCashOnDeliveryOrder(ctx context.Context, orderID int64) bool {
	resp, err := grpcclient.PaymentClient.GetPayment(ctx, &payment.GetPaymentReq{OrderId: orderID})
	if err != nil || resp == nil {
		return false
	}
	return resp.PayType == 3
}

// CancelOrder 取消订单
// 货到付款订单未实际收款，未发货（status∈{2,3}）可直接取消：回滚库存、置已取消，不产生退款流水；
// 在线支付订单仍仅允许待支付（status=1）取消，已支付走退款审核
// 参数：
//   - ctx: 上下文
//   - req: 取消请求
//
// 返回值：
//   - error: 错误信息
func (s *OrderService) CancelOrder(ctx context.Context, req *order.CancelOrderReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o model.Order
		if err := tx.First(&o, req.Id).Error; err != nil {
			return err
		}
		if o.UserID != req.UserId {
			return fmt.Errorf("无权限取消该订单")
		}
		allowed := cancelableStatuses(s.isCashOnDeliveryOrder(ctx, o.ID))
		if !containsInt32(allowed, o.Status) {
			return fmt.Errorf("订单状态不允许取消")
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		// 条件更新防并发：仅当订单仍处于允许取消的状态时生效
		res := tx.Model(&model.Order{}).Where("id = ? AND status IN ?", o.ID, allowed).Updates(map[string]interface{}{
			"status":        int32(6),
			"updated_at":    now,
			"cancel_time":   &now,
			"cancel_reason": req.Reason,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("订单状态不允许取消")
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

		// 回补优惠券：取消后把订单占用的用户券复位为未使用（否则券被永久核销）
		if o.CouponID > 0 {
			if _, err := grpcclient.ProductClient.ReleaseCoupon(ctx, &product.ReleaseCouponReq{OrderId: o.ID}); err != nil {
				return fmt.Errorf("回补优惠券失败: %w", err)
			}
		}

		return s.createStatusLogTx(tx, o.ID, o.Status, 6, "user", req.UserId, req.Reason)
	})
}

// orderTransitions 订单状态合法流转表
// key 为目标状态，value 为允许的来源状态集合
// 1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款
// 注意：目标状态 6（取消）不走本表，由 UpdateOrderStatus 的取消分支单独处理（需回补库存）
var orderTransitions = map[int32][]int32{
	2: {1},       // 待支付 -> 已支付（支付成功）
	3: {2},       // 已支付 -> 备货中
	4: {2, 3},    // 已支付/备货中 -> 配送中（发货）
	5: {2, 4},    // 已支付(自提)/配送中 -> 已完成（确认收货）
	7: {2, 3, 4}, // 已支付/备货中/配送中 -> 退款中
	8: {7},       // 退款中 -> 已退款
}

// isTransitionAllowed 校验订单状态流转是否合法
// 参数：
//   - from: 当前状态
//   - to: 目标状态
//
// 返回值：
//   - bool: 是否允许流转
func isTransitionAllowed(from, to int32) bool {
	allowed, ok := orderTransitions[to]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == from {
			return true
		}
	}
	return false
}

// UpdateOrderStatus 更新订单状态
// 带状态机校验：仅允许合法流转，退款中/已退款/已取消等状态不可再发货、完成或重新支付。
// 目标状态为 6（取消）时走独立取消语义：写取消原因/时间并逐行回补库存（后台取消复用）。
// 全部更新均按 WHERE status=fromStatus 条件执行，并发冲突时返回失败，避免后写覆盖前写。
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - error: 错误信息
func (s *OrderService) UpdateOrderStatus(ctx context.Context, req *order.UpdateOrderStatusReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o model.Order
		if err := tx.First(&o, req.Id).Error; err != nil {
			return err
		}

		// 取消订单：不走通用状态机，需回补库存并记录取消原因/时间
		if req.Status == 6 {
			return s.cancelOrderInTx(ctx, tx, &o, req.OperatorType, req.OperatorId, req.Remark)
		}

		if !isTransitionAllowed(o.Status, req.Status) {
			return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("订单状态不允许流转: %d -> %d", o.Status, req.Status))
		}
		fromStatus := o.Status
		now := time.Now().Format("2006-01-02 15:04:05")
		updates := map[string]interface{}{
			"status":     req.Status,
			"updated_at": now,
		}
		switch req.Status {
		case 2:
			updates["pay_time"] = &now
		case 4:
			updates["deliver_time"] = &now
			// 发货时写入独立物流字段（替代原先拼接 remark 的做法）
			updates["express_company"] = req.ExpressCompany
			updates["express_no"] = req.ExpressNo
		case 5:
			updates["complete_time"] = &now
		}

		res := tx.Model(&model.Order{}).Where("id = ? AND status = ?", req.Id, fromStatus).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
		}
		return s.createStatusLogTx(tx, o.ID, fromStatus, req.Status, req.OperatorType, req.OperatorId, req.Remark)
	})
}

// VerifyPickup 自提订单核销：校验取货码后置为已完成
// 校验：自提单、核销码匹配、状态为已支付(2)/备货中(3)；核销成功写完成时间与状态日志（核销人/时间可追溯）。
// 供后台兜底核销与履约工作台核销（B）共用；核销即完成，不涉及库存变更（下单时已扣减）
// 参数：
//   - ctx: 上下文
//   - req: 核销请求
//
// 返回值：
//   - *order.VerifyPickupResp: 核销结果
//   - error: 错误信息
func (s *OrderService) VerifyPickup(ctx context.Context, req *order.VerifyPickupReq) (*order.VerifyPickupResp, error) {
	if req.PickupCode == "" {
		return nil, bizerr.New(codes.InvalidArgument, "请输入取货码")
	}
	var resp *order.VerifyPickupResp
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o model.Order
		if err := tx.First(&o, req.OrderId).Error; err != nil {
			return err
		}
		// 社区隔离：核销人社区语境与订单归属社区不一致时直接拒绝（防止 A 社区管理员核销 B 社区订单）
		if req.CommunityId > 0 && o.CommunityID != req.CommunityId {
			return bizerr.New(codes.PermissionDenied, "无权核销其他社区的订单")
		}
		if o.DeliveryType != 1 {
			return bizerr.New(codes.FailedPrecondition, "非自提订单，无需核销")
		}
		if o.PickupCode == nil || *o.PickupCode != req.PickupCode {
			return bizerr.New(codes.FailedPrecondition, "取货码不正确，请核对后重试")
		}
		if o.Status != 2 && o.Status != 3 {
			return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("订单状态不允许核销: %d", o.Status))
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		// 条件更新防并发：仅当订单仍处于待核销状态时生效（防重复核销）
		res := tx.Model(&model.Order{}).Where("id = ? AND status IN ?", o.ID, []int32{2, 3}).Updates(map[string]interface{}{
			"status":        int32(5),
			"updated_at":    now,
			"complete_time": &now,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
		}

		if err := s.createStatusLogTx(tx, o.ID, o.Status, 5, req.OperatorType, req.OperatorId, "核销码核销（取货码 "+*o.PickupCode+"）"); err != nil {
			return err
		}
		resp = &order.VerifyPickupResp{OrderId: o.ID, Status: 5}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// cancelOrderInTx 在事务中执行取消语义：条件更新状态、写取消原因/时间并逐行回补库存。
// 供后台取消（UpdateOrderStatus 特判 status=6）复用；C 端 CancelOrder 有额外的归属校验，不走此方法。
// 参数：
//   - ctx: 上下文（用于调用 product-service 释放库存）
//   - tx: 数据库事务
//   - o: 已加载的订单
//   - operatorType: 操作者类型（admin/user/system）
//   - operatorID: 操作者 ID
//   - reason: 取消原因（写入 cancel_reason 与状态日志备注）
//
// 返回值：
//   - error: 错误信息
func (s *OrderService) cancelOrderInTx(ctx context.Context, tx *gorm.DB, o *model.Order, operatorType string, operatorID int64, reason string) error {
	// 货到付款单未实际收款，未发货（2/3）也可直接取消（与 C 端 CancelOrder 同口径）
	allowed := cancelableStatuses(s.isCashOnDeliveryOrder(ctx, o.ID))
	if !containsInt32(allowed, o.Status) {
		return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("订单状态不允许取消: %d", o.Status))
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	res := tx.Model(&model.Order{}).Where("id = ? AND status IN ?", o.ID, allowed).Updates(map[string]interface{}{
		"status":        int32(6),
		"updated_at":    now,
		"cancel_time":   &now,
		"cancel_reason": reason,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
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

	// 回补优惠券：取消后把订单占用的用户券复位为未使用（否则券被永久核销）
	if o.CouponID > 0 {
		if _, err := grpcclient.ProductClient.ReleaseCoupon(ctx, &product.ReleaseCouponReq{OrderId: o.ID}); err != nil {
			return fmt.Errorf("回补优惠券失败: %w", err)
		}
	}

	return s.createStatusLogTx(tx, o.ID, o.Status, 6, operatorType, operatorID, reason)
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
func (s *OrderService) createStatusLogTx(tx *gorm.DB, orderID int64, fromStatus, toStatus int32, operatorType string, operatorID int64, remark string) error {
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

// calculateDiscount 计算优惠券抵扣金额
// 参数：
//   - totalAmount: 订单总金额
//   - coupon: 优惠券信息
//
// 返回值：
//   - float64: 抵扣金额
func calculateDiscount(totalAmount float64, coupon *product.CouponInfo) float64 {
	if totalAmount < coupon.Threshold {
		return 0
	}
	if coupon.Type == 1 {
		if coupon.DiscountValue > totalAmount {
			return totalAmount
		}
		return coupon.DiscountValue
	}
	if coupon.Type == 2 {
		discount := totalAmount * (1 - coupon.DiscountValue)
		if discount > totalAmount {
			return totalAmount
		}
		// 下限钳制：折扣率脏数据（>=1）会算出负优惠放大实付金额，必须兜底
		if discount < 0 {
			return 0
		}
		return discount
	}
	return 0
}

// applyCoupon 根据优惠券 ID 查找用户可用的优惠券并计算抵扣
// 门槛校验与优惠计算均按「券范围内商品金额」（见 couponEligibleAmount）：
// 仅范围内商品计入门槛并享受优惠，范围外商品不影响
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - couponID: 优惠券 ID
//   - items: 结算商品范围上下文
//
// 返回值：
//   - float64: 抵扣金额
//   - int64: 用户优惠券 ID（供后续 UseCoupon 使用）
//   - error: 错误信息
func (s *OrderService) applyCoupon(ctx context.Context, userID, couponID int64, items []itemScope) (float64, int64, error) {
	ucResp, err := grpcclient.ProductClient.ListUserCoupons(ctx, &product.ListUserCouponsReq{
		UserId: userID,
		Status: 1,
	})
	if err != nil {
		return 0, 0, err
	}
	var target *product.UserCouponInfo
	for _, uc := range ucResp.List {
		if uc.CouponId == couponID {
			target = uc
			break
		}
	}
	if target == nil {
		return 0, 0, fmt.Errorf("未找到可用优惠券")
	}
	coupon, err := grpcclient.ProductClient.GetCoupon(ctx, &product.GetCouponReq{Id: couponID})
	if err != nil {
		return 0, 0, err
	}
	// 券范围内商品金额（门槛与优惠基数）
	eligible := couponEligibleAmount(coupon, items)
	if eligible < target.Threshold {
		return 0, 0, fmt.Errorf("订单中可使用该券的商品金额不满足使用门槛")
	}
	return calculateDiscount(eligible, coupon), target.Id, nil
}

// getActiveFlashSalePrice 获取 SKU 在当前社区生效的秒杀价
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//   - skuID: SKU ID
//
// 返回值：
//   - float64: 秒杀价，无生效活动返回 0
//   - error: 错误信息
func (s *OrderService) getActiveFlashSalePrice(ctx context.Context, communityID, skuID int64) (float64, error) {
	fsResp, err := grpcclient.ProductClient.ListFlashSales(ctx, &product.ListFlashSalesReq{CommunityId: communityID})
	if err != nil {
		return 0, err
	}
	now := time.Now()
	for _, fs := range fsResp.List {
		if fs.Status != 1 {
			continue
		}
		start := parseTime(fs.StartTime)
		end := parseTime(fs.EndTime)
		if now.Before(start) || now.After(end) {
			continue
		}
		detail, err := grpcclient.ProductClient.GetFlashSale(ctx, &product.GetFlashSaleReq{Id: fs.Id})
		if err != nil {
			continue
		}
		for _, item := range detail.Items {
			if item.SkuId == skuID {
				return item.FlashPrice, nil
			}
		}
	}
	return 0, nil
}

// calculateFullReduction 计算当前社区生效的满减优惠
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//   - totalAmount: 订单总金额
//
// 返回值：
//   - float64: 满减金额
func (s *OrderService) calculateFullReduction(ctx context.Context, communityID int64, totalAmount float64) float64 {
	frResp, err := grpcclient.ProductClient.ListFullReductions(ctx, &product.ListFullReductionsReq{CommunityId: communityID})
	if err != nil {
		return 0
	}
	now := time.Now()
	var best float64
	for _, fr := range frResp.List {
		if fr.Status != 1 {
			continue
		}
		if fr.StartTime != "" && now.Before(parseTime(fr.StartTime)) {
			continue
		}
		if fr.EndTime != "" && now.After(parseTime(fr.EndTime)) {
			continue
		}
		if totalAmount >= fr.Threshold && fr.ReduceAmount > best {
			best = fr.ReduceAmount
		}
	}
	if best > totalAmount {
		return totalAmount
	}
	return best
}

// parseTime 解析时间字符串
// 参数：
//   - s: 时间字符串，格式 2006-01-02 15:04:05
//
// 返回值：
//   - time.Time: 时间对象
func parseTime(s string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
	return t
}

// GetOrderStatistics 获取订单状态统计
// 参数：
//   - ctx: 上下文
//   - req: 统计请求
//
// 返回值：
//   - *order.OrderStatisticsResp: 订单统计结果
//   - error: 错误信息
func (s *OrderService) GetOrderStatistics(ctx context.Context, req *order.GetOrderStatisticsReq) (*order.OrderStatisticsResp, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.Order{})
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	db = s.withDateRange(db, req.StartDate, req.EndDate)

	var counts []struct {
		Status int32
		Count  int64
	}
	if err := db.Select("status, COUNT(*) AS count").Group("status").Scan(&counts).Error; err != nil {
		return nil, err
	}

	resp := &order.OrderStatisticsResp{}
	for _, c := range counts {
		switch c.Status {
		case 1:
			resp.PendingPay = int32(c.Count)
		case 2:
			resp.PendingShip = int32(c.Count)
		case 4:
			resp.PendingReceive = int32(c.Count)
		case 5:
			resp.Completed = int32(c.Count)
		case 6:
			resp.Cancelled = int32(c.Count)
		case 7:
			resp.Refunding = int32(c.Count)
		}
		resp.TotalOrders += int32(c.Count)
	}
	return resp, nil
}

// GetSalesStatistics 获取销售统计
// 参数：
//   - ctx: 上下文
//   - req: 统计请求
//
// 返回值：
//   - *order.SalesStatisticsResp: 销售统计结果
//   - error: 错误信息
func (s *OrderService) GetSalesStatistics(ctx context.Context, req *order.GetSalesStatisticsReq) (*order.SalesStatisticsResp, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.Order{}).Where("status NOT IN ?", []int32{1, 6})
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	db = s.withDateRange(db, req.StartDate, req.EndDate)

	var result struct {
		TotalAmount float64
		PayAmount   float64
		TotalOrders int64
	}
	if err := db.Select("COALESCE(SUM(total_amount),0) AS total_amount, COALESCE(SUM(pay_amount),0) AS pay_amount, COUNT(*) AS total_orders").Scan(&result).Error; err != nil {
		return nil, err
	}

	avg := float64(0)
	if result.TotalOrders > 0 {
		avg = result.PayAmount / float64(result.TotalOrders)
	}
	return &order.SalesStatisticsResp{
		TotalAmount:    result.TotalAmount,
		PayAmount:      result.PayAmount,
		TotalOrders:    int32(result.TotalOrders),
		AvgOrderAmount: avg,
	}, nil
}

// GetProductRankings 获取商品销售排行
// 参数：
//   - ctx: 上下文
//   - req: 排行请求
//
// 返回值：
//   - []*order.ProductRankingItem: 排行列表
//   - error: 错误信息
func (s *OrderService) GetProductRankings(ctx context.Context, req *order.GetProductRankingsReq) ([]*order.ProductRankingItem, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 10
	}

	db := s.db.GetDb().WithContext(ctx).
		Table("order_items").
		Select("product_id, product_name, SUM(quantity) AS sales_count, SUM(total_price) AS sales_amount").
		Joins("JOIN orders ON orders.id = order_items.order_id").
		Where("orders.status NOT IN ?", []int32{1, 6})
	if req.CommunityId > 0 {
		db = db.Where("orders.community_id = ?", req.CommunityId)
	}
	db = s.withDateRangeOnOrders(db, req.StartDate, req.EndDate)

	var list []*order.ProductRankingItem
	err := db.Group("product_id, product_name").
		Order("sales_count DESC").
		Limit(limit).
		Scan(&list).Error
	return list, err
}

// GetRevenueReport 获取营收报表
// 参数：
//   - ctx: 上下文
//   - req: 报表请求
//
// GetUserFrequentProducts 获取用户常买商品（购物车"常买商品"列表）
// 统计口径（决策点③）：仅计有效订单（已支付及之后，排除待支付1/已取消6/退款7/已退款8）；
// 购买次数 = 去重订单数，另附该用户买该商品最常买的 SKU（供一键加购）
// 参数：
//   - ctx: 上下文
//   - req: 请求（user_id + limit）
//
// 返回值：
//   - []*order.FrequentProductItem: 常买商品列表（按购买次数降序）
//   - error: 错误信息
func (s *OrderService) GetUserFrequentProducts(ctx context.Context, req *order.GetUserFrequentProductsReq) ([]*order.FrequentProductItem, error) {
	if req.UserId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少用户 ID")
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 10
	}

	// 有效订单口径：排除待支付(1)/已取消(6)/退款中(7)/已退款(8)
	validStatuses := []int32{2, 3, 4, 5}

	var rows []*order.FrequentProductItem
	err := s.db.GetDb().WithContext(ctx).
		Table("order_items").
		Select("order_items.product_id, order_items.product_name, MAX(order_items.product_image) AS product_image, "+
			"COUNT(DISTINCT orders.id) AS purchase_count, SUM(order_items.quantity) AS total_quantity").
		Joins("JOIN orders ON orders.id = order_items.order_id").
		Where("orders.user_id = ? AND orders.status IN ?", req.UserId, validStatuses).
		Group("order_items.product_id, order_items.product_name").
		Order("purchase_count DESC, total_quantity DESC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	// 附带每个商品该用户最常买的 SKU（按累计数量取 Top1，供一键加购）
	for _, r := range rows {
		var skuID int64
		err := s.db.GetDb().WithContext(ctx).
			Table("order_items").
			Select("order_items.sku_id").
			Joins("JOIN orders ON orders.id = order_items.order_id").
			Where("orders.user_id = ? AND orders.status IN ? AND order_items.product_id = ?", req.UserId, validStatuses, r.ProductId).
			Group("order_items.sku_id").
			Order("SUM(order_items.quantity) DESC").
			Limit(1).
			Scan(&skuID).Error
		if err == nil {
			r.SkuId = skuID
		}
	}
	return rows, nil
}

// GetRevenueReport 获取营收报表
// 参数：
//   - ctx: 上下文
//   - req: 报表请求
//
// 返回值：
//   - []*order.RevenueReportItem: 报表列表
//   - error: 错误信息
func (s *OrderService) GetRevenueReport(ctx context.Context, req *order.GetRevenueReportReq) ([]*order.RevenueReportItem, error) {
	groupBy := req.GroupBy
	if groupBy != "month" {
		groupBy = "day"
	}
	var dateFormat string
	if groupBy == "month" {
		dateFormat = "%Y-%m"
	} else {
		dateFormat = "%Y-%m-%d"
	}

	dateExpr := fmt.Sprintf("DATE_FORMAT(created_at, '%s')", dateFormat)
	db := s.db.GetDb().WithContext(ctx).
		Table("orders").
		Select(dateExpr+" AS date, COALESCE(SUM(pay_amount),0) AS revenue, COUNT(*) AS order_count").
		Where("status NOT IN ?", []int32{1, 6})
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	db = s.withDateRange(db, req.StartDate, req.EndDate)

	var list []*order.RevenueReportItem
	err := db.Group(dateExpr).
		Order("date ASC").
		Scan(&list).Error
	return list, err
}

// withDateRange 给订单查询添加日期范围
// 参数：
//   - db: GORM DB
//   - startDate: 开始日期（yyyy-MM-dd）
//   - endDate: 结束日期（yyyy-MM-dd）
//
// 返回值：
//   - *gorm.DB: 添加条件后的 DB
func (s *OrderService) withDateRange(db *gorm.DB, startDate, endDate string) *gorm.DB {
	if startDate != "" {
		db = db.Where("created_at >= ?", startDate)
	}
	if endDate != "" {
		db = db.Where("created_at <= ?", endDate+" 23:59:59")
	}
	return db
}

// withDateRangeOnOrders 给订单 JOIN 查询添加 orders.created_at 日期范围
// 参数：
//   - db: GORM DB
//   - startDate: 开始日期
//   - endDate: 结束日期
//
// 返回值：
//   - *gorm.DB: 添加条件后的 DB
func (s *OrderService) withDateRangeOnOrders(db *gorm.DB, startDate, endDate string) *gorm.DB {
	if startDate != "" {
		db = db.Where("orders.created_at >= ?", startDate)
	}
	if endDate != "" {
		db = db.Where("orders.created_at <= ?", endDate+" 23:59:59")
	}
	return db
}

// generateOrderNo 生成订单编号
// 返回值：
//   - string: 订单编号
func generateOrderNo() string {
	return "DD" + time.Now().Format("20060102150405") + fmt.Sprintf("%04d", time.Now().Nanosecond()/1e5)
}

// genPickupCode 生成 8 位数字自提核销码（crypto/rand，正例空间 1e8，
// 唯一索引兜底：极端碰撞时由 CreateOrder 的重试逻辑处理）
func genPickupCode() *string {
	const digits = "0123456789"
	b := make([]byte, 8)
	if _, err := crand.Read(b); err != nil {
		// 随机源失败时降级为时间派生（极端场景，可接受）
		s := fmt.Sprintf("%08d", time.Now().UnixNano()%1e8)
		return &s
	}
	for i := range b {
		b[i] = digits[int(b[i])%len(digits)]
	}
	s := string(b)
	return &s
}
