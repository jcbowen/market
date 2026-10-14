package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/order"
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
	}

	// 分别计算满减与优惠券优惠，两者均按商品总价校验门槛并可叠加
	fullReductionAmount := s.calculateFullReduction(ctx, req.CommunityId, totalAmount)

	// 优惠券是用户主动选择，校验失败（券不存在/已使用/门槛不满足）直接中断下单，
	// 避免页面展示优惠与订单实付不符的静默失败
	var discountAmount float64
	var userCouponID int64
	if req.CouponId > 0 {
		couponDiscount, ucID, err := s.applyCoupon(ctx, req.UserId, req.CouponId, totalAmount)
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
		CreatedAt:      now,
		UpdatedAt:      now,
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
		return s.createStatusLogTx(tx, orderModel.ID, 0, 1, "system", 0, "创建订单")
	}); err != nil {
		return nil, err
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

// CancelOrder 取消订单
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
		if o.Status != 1 {
			return fmt.Errorf("订单状态不允许取消")
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		o.Status = 6
		o.CancelReason = req.Reason
		o.CancelTime = &now
		o.UpdatedAt = now
		if err := tx.Save(&o).Error; err != nil {
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

		return s.createStatusLogTx(tx, o.ID, 1, 6, "user", req.UserId, req.Reason)
	})
}

// orderTransitions 订单状态合法流转表
// key 为目标状态，value 为允许的来源状态集合
// 1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款
var orderTransitions = map[int32][]int32{
	2: {1},       // 待支付 -> 已支付（支付成功）
	3: {2},       // 已支付 -> 备货中
	4: {2, 3},    // 已支付/备货中 -> 配送中（发货）
	5: {2, 4},    // 已支付(自提)/配送中 -> 已完成（确认收货）
	6: {1},       // 待支付 -> 已取消
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
// 带状态机校验：仅允许合法流转，退款中/已退款/已取消等状态不可再发货、完成或重新支付
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
		if !isTransitionAllowed(o.Status, req.Status) {
			return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("订单状态不允许流转: %d -> %d", o.Status, req.Status))
		}
		fromStatus := o.Status
		now := time.Now().Format("2006-01-02 15:04:05")
		o.Status = req.Status
		o.UpdatedAt = now

		switch req.Status {
		case 2:
			o.PayTime = &now
		case 4:
			o.DeliverTime = &now
		case 5:
			o.CompleteTime = &now
		}

		if err := tx.Save(&o).Error; err != nil {
			return err
		}
		return s.createStatusLogTx(tx, o.ID, fromStatus, req.Status, req.OperatorType, req.OperatorId, req.Remark)
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
		return discount
	}
	return 0
}

// applyCoupon 根据优惠券 ID 查找用户可用的优惠券并计算抵扣
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - couponID: 优惠券 ID
//   - amount: 订单金额（用于校验门槛）
//
// 返回值：
//   - float64: 抵扣金额
//   - int64: 用户优惠券 ID（供后续 UseCoupon 使用）
//   - error: 错误信息
func (s *OrderService) applyCoupon(ctx context.Context, userID, couponID int64, amount float64) (float64, int64, error) {
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
	if amount < target.Threshold {
		return 0, 0, fmt.Errorf("订单金额不满足优惠券使用门槛")
	}
	coupon, err := grpcclient.ProductClient.GetCoupon(ctx, &product.GetCouponReq{Id: couponID})
	if err != nil {
		return 0, 0, err
	}
	return calculateDiscount(amount, coupon), target.Id, nil
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
		Select(dateExpr + " AS date, COALESCE(SUM(pay_amount),0) AS revenue, COUNT(*) AS order_count").
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
