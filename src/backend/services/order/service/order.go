package service

import (
	"context"
	crand "crypto/rand"
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
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

// OrderService 订单业务服务
type OrderService struct {
	db          *mysql.Instance
	redisClient *redis.Client
	// cfg 全局配置：取消前置渠道确认开关需要读 business.payment_reconcile
	cfg *config.Config
}

// NewOrderService 创建订单业务服务
// 参数：
//   - db: MySQL 实例
//   - cfg: 全局配置（可为 nil，此时按默认口径处理）
//
// 返回值：
//   - *OrderService: 订单业务服务实例
func NewOrderService(db *mysql.Instance, cfg *config.Config) *OrderService {
	return &OrderService{db: db, cfg: cfg}
}

// WithRedis 注入 Redis 客户端（可选，用于取货码防暴力破解等场景）
// 参数：
//   - client: Redis 客户端
//
// 返回值：
//   - *OrderService: 自身（链式调用）
func (s *OrderService) WithRedis(client *redis.Client) *OrderService {
	s.redisClient = client
	return s
}

// loadOrderItemCatalog 批量预取明细涉及的 SKU 与商品档案
// 下单/试算原本逐行调用 GetSkuById + GetProduct（N 行 2N 次往返），改走两次批量 RPC 后
// 整单固定 2 次往返；返回值以 ID 为键，重复 SKU 的多行共用同一份档案。
// 商品档案带可售收口（require_sellable）：任一明细商品非上架态即整体报错（P1-4），
// 未到点的定时上架商品与已下架商品都无法下单。
// 社区语境必须原样透传成交社区（trade 链路不适用「内部调用传 0」的旧约定）：
// 商品与 SKU 都是社区私有资产，跨社区取用价格/库存无从拦截（P1-3 收口点）。
//
// 参数：
//   - ctx: 上下文
//   - skuIDs: 明细 SKU ID 集合（可重复，product 侧会去重）
//   - communityID: 成交社区 ID（<=0 时 product 侧跳过归属校验，仅内部全量场景可用）
//
// 返回值：
//   - map[int64]*product.SkuInfo: SKU ID -> SKU 档案
//   - map[int64]*product.ProductInfo: 商品 ID -> 商品档案
//   - error: RPC 失败（SKU/商品缺失由 product 侧整体报错，不会返回部分结果；越权为 PermissionDenied）
//
// 使用示例：
//
//	skuByID, productByID, err := s.loadOrderItemCatalog(ctx, []int64{11, 12}, 456)
func (s *OrderService) loadOrderItemCatalog(ctx context.Context, skuIDs []int64, communityID int64) (map[int64]*product.SkuInfo, map[int64]*product.ProductInfo, error) {
	skuResp, err := grpcclient.ProductClient.BatchGetSkus(ctx, &product.BatchGetSkusReq{
		Ids:         skuIDs,
		CommunityId: communityID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("批量获取 SKU 失败: %w", err)
	}
	skuByID := make(map[int64]*product.SkuInfo, len(skuResp.List))
	productIDs := make([]int64, 0, len(skuResp.List))
	for _, sku := range skuResp.List {
		skuByID[sku.Id] = sku
		productIDs = append(productIDs, sku.ProductId)
	}

	productResp, err := grpcclient.ProductClient.BatchGetProducts(ctx, &product.BatchGetProductsReq{
		Ids: productIDs,
		// 与 BatchGetSkus 传同一个社区，归属漂移时两侧任一侧都能拦下
		CommunityId: communityID,
		// 可售收口（P1-4）：任一明细商品非上架态（下架/定时上架未到点）即拒单，
		// 判定统一在商品服务（require_sellable），此处只声明意图
		RequireSellable: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("批量获取商品失败: %w", err)
	}
	productByID := make(map[int64]*product.ProductInfo, len(productResp.List))
	for _, p := range productResp.List {
		productByID[p.Id] = p
	}
	return skuByID, productByID, nil
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
	// 配送类型：1自提 2配送 3到店（仅收银台，即买即走，不生成取货码、无需核销）
	if req.DeliveryType != 1 && req.DeliveryType != 2 && req.DeliveryType != 3 {
		return nil, bizerr.New(codes.InvalidArgument, "配送类型不合法，仅支持 1（自提）、2（配送）或 3（到店）")
	}
	// 到店模式目前仅收银台使用（即买即走，无取货码与配送履约），C 端不可选
	if req.DeliveryType == 3 && req.Source != 2 {
		return nil, bizerr.New(codes.InvalidArgument, "到店模式仅收银台可用")
	}
	// 社区前置校验：未选社区（community_id=0）时直接给出业务提示，
	// 避免 GetCommunity(0) 报"社区不存在"误导用户
	if req.CommunityId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "请先选择社区")
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
	// 取货渠道社区级开关兜底（前端渠道按钮隐藏后仍可能绕过直调）：
	// 自提/配送关闭时拒绝对应渠道下单；到店（delivery_type=3）不受渠道配置控制，恒可收银
	if req.DeliveryType == 1 && !communityInfo.EnableSelfPickup {
		return nil, bizerr.New(codes.FailedPrecondition, "当前社区未开启自提渠道，请切换为其他方式")
	}
	if req.DeliveryType == 2 && !communityInfo.EnableDelivery {
		return nil, bizerr.New(codes.FailedPrecondition, "当前社区未开启配送渠道，请切换为其他方式")
	}

	// 收银单（source=2）必须携带班次 ID：网关下单前已校验进行中班次，
	// 此处做服务端兜底（防止绕过网关直调 RPC 产生无班次归属的收银单，交班统计漏单）
	if req.Source == 2 && req.ShiftId <= 0 {
		return nil, bizerr.New(codes.FailedPrecondition, "收银单缺少班次信息，请先开工")
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	var totalAmount float64
	var orderItems []*model.OrderItem
	// 免配送费范围判定上下文（参与商品额计算用，见 calcFreeShipping）
	var itemScopes []itemScope

	// 明细商品预取：原先每行 2 次 RPC（SKU + 商品），N 行订单即 2N 次往返，
	// 收银台整单尤其明显；收口为两次批量查询后按 ID 取内存结果。
	// 批量查询任一 ID 缺失即整体报错，不会出现「部分明细算价」的金额/库存双口径偏差。
	// 社区语境透传成交社区：越权取用其他社区的 SKU/商品在此拦下
	skuIDs := make([]int64, 0, len(req.Items))
	for _, item := range req.Items {
		skuIDs = append(skuIDs, item.SkuId)
	}
	skuByID, productByID, err := s.loadOrderItemCatalog(ctx, skuIDs, req.CommunityId)
	if err != nil {
		return nil, err
	}
	// 秒杀价按请求维度一次取回，明细侧查表（原先逐明细 ListFlashSales + GetFlashSale，N 行放大为 N×(1+K) 次 RPC）
	flashPriceByID := s.loadFlashSalePrices(ctx, req.CommunityId)

	for _, item := range req.Items {
		if item.Quantity <= 0 || item.Quantity > 999 {
			return nil, bizerr.New(codes.InvalidArgument, "单品购买数量须在 1~999 之间")
		}
		skuInfo := skuByID[item.SkuId]
		if skuInfo.Stock < item.Quantity {
			return nil, bizerr.New(codes.ResourceExhausted, fmt.Sprintf("商品 %s 库存不足", skuInfo.SkuCode))
		}
		productInfo := productByID[skuInfo.ProductId]

		// 优先使用生效的秒杀价
		price := skuInfo.Price
		if flashPrice := flashPriceByID[item.SkuId]; flashPrice > 0 {
			price = flashPrice
		}

		// 收银台单品改价（仅对当前订单生效，不改商品档案价）：
		// 仅限收银台来源（Source==2）允许改价，C 端用户下单一律服务端取价，
		// 防止客户端注入 price 字段以任意低价成交
		// item.Price>0 时以改价成交，original_price 留服务端实时价快照供后台对账
		originalPrice := price
		priceModified := int32(0)
		if req.Source == 2 && item.Price > 0 {
			price = item.Price
			priceModified = 1
		}

		totalPrice := price * float64(item.Quantity)
		totalAmount += totalPrice
		itemScopes = append(itemScopes, itemScope{
			productID:  skuInfo.ProductId,
			categoryID: productInfo.CategoryId,
			totalPrice: totalPrice,
		})

		orderItems = append(orderItems, &model.OrderItem{
			ProductID:     skuInfo.ProductId,
			SkuID:         item.SkuId,
			ProductName:   productInfo.Name,
			SpecName:      skuInfo.SpecName,
			ProductImage:  productInfo.CoverImage,
			Price:         price,
			Quantity:      item.Quantity,
			TotalPrice:    totalPrice,
			OriginalPrice: originalPrice,
			PriceModified: priceModified,
			CreatedAt:     now,
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
	// 手动优惠（收银台抹零/整单改价折算）：与券/满减叠加，但不可为负、不可超原应付（实付≥0 兜底）
	if req.ManualDiscount > 0 {
		if req.ManualDiscount > payAmount {
			return nil, bizerr.New(codes.InvalidArgument, "手动优惠金额不能超过应付金额")
		}
		payAmount -= req.ManualDiscount
	}
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
		ManualDiscount: req.ManualDiscount,
		ShiftID:        req.ShiftId,
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

	// 高并发下时间戳订单号可能碰撞（order_no 唯一索引），冲突时换号重试，最多 3 次
	const maxOrderNoAttempts = 3
	var createErr error
	for attempt := 0; attempt < maxOrderNoAttempts; attempt++ {
		if attempt > 0 {
			// 换新订单号与主键重试：上一轮远端已提交的库存扣减/优惠券核销已由补偿路径回补
			orderModel.ID = 0
			orderModel.OrderNo = generateOrderNo()
		}
		createErr = s.createOrderTransaction(ctx, orderModel, orderItems, req, userCouponID, source)
		if createErr == nil || !isDuplicateOrderNoErr(createErr) {
			break
		}
	}
	if createErr != nil {
		return nil, createErr
	}

	// 记录会员-社区关联：在该社区下过单即视为该社区会员（幂等，尽力而为不阻塞下单结果）；
	// 收银台散客单（user_id=0）无会员可标记，跳过
	if req.UserId > 0 {
		if _, mErr := grpcclient.UserClient.MarkUserCommunity(ctx, &user.MarkUserCommunityReq{
			UserId:      req.UserId,
			CommunityId: req.CommunityId,
			Source:      "order",
		}); mErr != nil {
			log.Printf("记录会员-社区关联失败（订单 %d）: %v", orderModel.ID, mErr)
		}
	}

	return orderModel, nil
}

// createOrderTransaction 执行下单事务：落订单与明细、扣减库存、核销优惠券、写状态日志。
// 任一步失败时回滚本地事务，并补偿释放已提交到商品服务的库存扣减与优惠券核销
// （库存幂等日志保证重复补偿只生效一次），尽力而为，不掩盖原错误。
// 参数：
//   - ctx: 上下文
//   - orderModel: 订单模型（调用前需已填充 OrderNo 与业务字段）
//   - orderItems: 订单商品明细（OrderID 在事务内回填）
//   - req: 下单请求（取商品明细与经手人）
//   - userCouponID: 使用的用户优惠券 ID，0 表示未用券
//   - source: 订单来源（1=C 端 2=收银台代客）
//
// 返回值：
//   - error: 事务错误信息
func (s *OrderService) createOrderTransaction(ctx context.Context, orderModel *model.Order, orderItems []*model.OrderItem, req *order.CreateOrderReq, userCouponID int64, source int32) error {
	// 整单库存收口为两次批量 RPC（主库存 + 秒杀库存），替代原先逐行各一次的 2N 次往返。
	// 批量在 product 侧同一事务内完成：要么整单扣减成功，要么一条都没扣，
	// 因此补偿只需按「是否已提交成功」整单回补，不再逐项追踪下标
	stockItems := toStockItemQtys(req.Items)
	var stockDeducted, flashDeducted bool
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
		if _, err := grpcclient.ProductClient.BatchDeductStock(ctx, &product.BatchDeductStockReq{
			OrderId: orderModel.ID,
			Items:   stockItems,
		}); err != nil {
			return fmt.Errorf("扣减库存失败: %w", err)
		}
		stockDeducted = true

		// 秒杀独立库存：命中秒杀价的商品在 product 侧自动识别扣减（无生效秒杀时 no-op）
		if _, err := grpcclient.ProductClient.BatchDeductFlashSaleStock(ctx, &product.BatchDeductFlashSaleStockReq{
			OrderId: orderModel.ID,
			Items:   stockItems,
		}); err != nil {
			return fmt.Errorf("扣减秒杀库存失败: %w", err)
		}
		flashDeducted = true

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
		// 补偿路径：释放已扣减的库存，尽力而为不掩盖原错误；
		// biz_no 用 comp:{orderID} 与取消/退款链路区分，重复补偿重试被幂等约束拦截
		compBizNo := fmt.Sprintf("comp:%d", orderModel.ID)
		if stockDeducted {
			if _, rErr := grpcclient.ProductClient.BatchReleaseStock(ctx, &product.BatchReleaseStockReq{
				OrderId: orderModel.ID,
				BizNo:   compBizNo,
				Items:   stockItems,
			}); rErr != nil {
				log.Printf("下单失败补偿释放库存失败（订单 %d）: %v", orderModel.ID, rErr)
			}
		}
		// 秒杀库存同样在远端已提交：一并回补（非秒杀商品由 product 侧幂等日志判无后 no-op）
		if flashDeducted {
			if _, rErr := grpcclient.ProductClient.BatchReleaseFlashSaleStock(ctx, &product.BatchReleaseFlashSaleStockReq{
				OrderId: orderModel.ID,
				BizNo:   compBizNo,
				Items:   stockItems,
			}); rErr != nil {
				log.Printf("下单失败补偿释放秒杀库存失败（订单 %d）: %v", orderModel.ID, rErr)
			}
		}
		// 优惠券核销同样在远端已提交：一并回补，保证换号重试时同一张券可再次使用
		if userCouponID > 0 {
			if _, rErr := grpcclient.ProductClient.ReleaseCoupon(ctx, &product.ReleaseCouponReq{OrderId: orderModel.ID}); rErr != nil {
				log.Printf("下单失败补偿回补优惠券失败（订单 %d）: %v", orderModel.ID, rErr)
			}
		}
		return err
	}
	return nil
}

// toStockItemQtys 把下单明细转换为库存批量操作项（SKU + 数量）
//
// 参数：
//   - items: 下单商品明细
//
// 返回值：
//   - []*product.StockItemQty: 批量库存操作项（同一 SKU 的多行由 product 侧聚合）
func toStockItemQtys(items []*order.OrderItem) []*product.StockItemQty {
	list := make([]*product.StockItemQty, 0, len(items))
	for _, item := range items {
		list = append(list, &product.StockItemQty{SkuId: item.SkuId, Quantity: item.Quantity})
	}
	return list
}

// isDuplicateOrderNoErr 判断错误是否为订单号唯一索引冲突（MySQL 1062 Duplicate entry）
// 参数：
//   - err: 下单事务错误
//
// 返回值：
//   - bool: 订单号冲突返回 true
func isDuplicateOrderNoErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") && strings.Contains(msg, "order_no")
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
func (s *OrderService) GetOrder(ctx context.Context, id, userID int64) (*model.Order, []*model.OrderItem, []*model.OrderStatusLog, []*model.Refund, error) {
	var o model.Order
	if err := s.db.GetDb().WithContext(ctx).First(&o, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, nil, bizerr.New(codes.NotFound, "订单不存在")
		}
		return nil, nil, nil, nil, err
	}
	if userID > 0 && o.UserID != userID {
		return nil, nil, nil, nil, bizerr.New(codes.PermissionDenied, "无权限查看该订单")
	}

	var items []*model.OrderItem
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ?", id).Find(&items).Error; err != nil {
		return nil, nil, nil, nil, err
	}

	var logs []*model.OrderStatusLog
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ?", id).Order("id asc").Find(&logs).Error; err != nil {
		return nil, nil, nil, nil, err
	}

	// 售后记录：本单全部退款单（含商品明细），订单详情按次展示
	var refunds []*model.Refund
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ?", id).Order("id asc").Find(&refunds).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	if len(refunds) > 0 {
		refundIDs := make([]int64, 0, len(refunds))
		for _, r := range refunds {
			refundIDs = append(refundIDs, r.ID)
		}
		var refundItems []*model.RefundItem
		if err := s.db.GetDb().WithContext(ctx).Where("refund_id IN ?", refundIDs).Find(&refundItems).Error; err != nil {
			return nil, nil, nil, nil, err
		}
		itemMap := make(map[int64][]*model.RefundItem)
		for _, item := range refundItems {
			itemMap[item.RefundID] = append(itemMap[item.RefundID], item)
		}
		for _, r := range refunds {
			r.OrderNo = o.OrderNo
			r.Items = itemMap[r.ID]
		}
	}

	return &o, items, logs, refunds, nil
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

	db := s.applyOrderListFilters(ctx, req)

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var list []*model.Order
	err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// applyOrderListFilters 构建订单列表查询过滤条件（列表查询与后台导出共用单一事实源，
// 导出按查询快照还原 ListOrdersReq 后复用，保证「所见即所导」）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求（仅过滤字段生效，分页由调用方自行处理）
//
// 返回值：
//   - *gorm.DB: 已挂过滤条件的查询链
func (s *OrderService) applyOrderListFilters(ctx context.Context, req *order.ListOrdersReq) *gorm.DB {
	db := s.db.GetDb().WithContext(ctx).Model(&model.Order{})
	// 用户软删除过滤：靠 include_user_deleted 显式区分调用方，不能拿 user_id 是否非零推断——
	// 后台「按会员查订单」（UserOrdersModal）同样传 user_id，靠它推断会把已删单从客服视图
	// 一并抹掉，与「软删仅影响 C 端列表」的设计不变量冲突
	if !req.IncludeUserDeleted {
		db = db.Where("user_deleted = ?", int32(0))
	}
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
	// 后台管理端搜索条件（订单号右模糊走 order_no 唯一索引，前置通配符会使索引失效；
	// 收货人姓名/电话模糊）
	if req.OrderNo != "" {
		db = db.Where("order_no LIKE ?", req.OrderNo+"%")
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
	// 取货码精确查询（收银台核销 Tab 扫码/输码查单，本社区范围由 CommunityId 条件约束）
	if req.PickupCode != "" {
		db = db.Where("pickup_code = ?", req.PickupCode)
	}
	// 收银班次筛选（交班记录「查看班内订单」入口）
	if req.ShiftId > 0 {
		db = db.Where("shift_id = ?", req.ShiftId)
	}
	// 支付单号/渠道交易号反查命中集（gateway 先经 payment 服务反查订单号，再传集合精确匹配；
	// has_pay_no=true 且集合为空表示反查无命中，挂恒假集合返回空结果）
	if req.HasPayNo {
		nos := req.PayOrderNos
		if len(nos) == 0 {
			// 反查无命中：挂恒假集合返回空结果（不修改入参，req 可能被调用方复用）
			nos = []string{"__no_match__"}
		}
		db = db.Where("order_no IN ?", nos)
	}
	// 商品过滤（含全部规格）：order_items.product_id 索引定位订单，量级可控
	if req.ProductId > 0 {
		var orderIds []int64
		if err := s.db.GetDb().WithContext(ctx).Model(&model.OrderItem{}).
			Where("product_id = ?", req.ProductId).
			Distinct().Pluck("order_id", &orderIds).Error; err != nil {
			// 过滤查询失败时挂恒假条件返回空列表，与 ListOrders 错误返回语义解耦：
			// 导出链路同样走本方法，空结果优于整个任务失败
			db = db.Where("id IN ?", []int64{-1})
		} else if len(orderIds) == 0 {
			db = db.Where("id IN ?", []int64{-1})
		} else {
			db = db.Where("id IN ?", orderIds)
		}
	}
	switch {
	case req.Status == queryStatusRefundAggregate:
		db = db.Where("status IN ?", []int32{7, 8})
	case req.Status == queryStatusShippingAggregate:
		db = db.Where("status IN ?", []int32{2, 3})
	case req.Status > 0:
		db = db.Where("status = ?", req.Status)
	}
	return db
}

// GetUserOrderStatusCounts 用户订单状态角标统计（C 端「我的-我的订单」红标）
// 口径与 C 端订单列表 Tab 一致：待支付=1；待发货=2/3；待收货=4；
// 退款/售后角标（2026-10-23 调整）只统计存在待审核售后单(refunds.status=1)的订单——
// 商家同意/拒绝后即不再亮角标；不再按订单状态 7/8 统计：8 已退款是终态会永远亮角标，
// 7 中已同意、渠道打款在途的售后也不属于"待商家处理"（进度可到订单列表退款中标签查看）
// 参数：
//   - ctx: 上下文
//   - req: 请求（user_id 必传）
//
// 返回值：
//   - *order.GetUserOrderStatusCountsResp: 各状态订单数
//   - error: 查询错误
func (s *OrderService) GetUserOrderStatusCounts(ctx context.Context, req *order.GetUserOrderStatusCountsReq) (*order.GetUserOrderStatusCountsResp, error) {
	resp := &order.GetUserOrderStatusCountsResp{}
	if req.UserId <= 0 {
		return resp, nil
	}

	type statusCount struct {
		Status int32 `gorm:"column:status"`
		Total  int32 `gorm:"column:total"`
	}
	var rows []statusCount
	err := s.db.GetDb().WithContext(ctx).Model(&model.Order{}).
		Select("status, COUNT(*) AS total").
		Where("user_id = ?", req.UserId).
		// 排除用户已软删除的订单，角标与列表口径一致
		Where("user_deleted = ?", int32(0)).
		Where("status IN ?", []int32{1, 2, 3, 4}).
		Group("status").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		switch row.Status {
		case 1:
			resp.PendingPay += row.Total
		case 2, 3:
			resp.PendingShip += row.Total
		case 4:
			resp.PendingReceive += row.Total
		}
	}

	// after_sale 专项统计：订单维度去重（同一订单可有多笔待审核售后单只计 1）
	// 不再硬绑 orders.status=7：状态机正常时待审核售后单只存在于退款中的订单，
	// 但后台改状态/历史数据可能留下「非 7 且挂着待审核售后单」的形态，
	// 绑死 7 会漏计用户真正需要被提醒的一笔待处理售后
	var afterSale int64
	err = s.db.GetDb().WithContext(ctx).Model(&model.Refund{}).
		Joins("JOIN orders ON orders.id = refunds.order_id").
		Where("orders.user_id = ? AND refunds.status = ?", req.UserId, int32(1)).
		// 用户已软删除的订单不参与角标统计（该口径理论上少命中：有进行中售后时不允许删除）
		Where("orders.user_deleted = ?", int32(0)).
		Distinct("refunds.order_id").
		Count(&afterSale).Error
	if err != nil {
		return nil, err
	}
	resp.AfterSale = int32(afterSale)
	return resp, nil
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
	if err != nil {
		// 查询失败按在线支付口径处理（fail-safe：已收款单不会被误判为可无责取消），但需留痕排查
		log.Printf("查询订单 %d 支付单失败，货到付款判定按在线支付处理: %v", orderID, err)
		return false
	}
	if resp == nil {
		return false
	}
	return resp.PayType == 3
}

// isPendingPaymentOrder 订单是否仍处于「等钱到账」的状态
// 含 100（超时取消处理中）：调度器抢占后订单短暂停留该态，此时同样需要渠道确认
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - bool: 仍需确认收款；查询失败返回 false（不因此阻断取消，渠道确认本就尽力而为）
func (s *OrderService) isPendingPaymentOrder(ctx context.Context, orderID int64) bool {
	var count int64
	if err := s.db.GetDb().WithContext(ctx).Model(&model.Order{}).
		Where("id = ? AND status IN ?", orderID, []int32{1, processingStatusCancel}).
		Count(&count).Error; err != nil {
		log.Printf("[order] 查询订单 %d 状态失败: %v", orderID, err)
		return false
	}
	return count > 0
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
	// 事务外预读订单并判定货到付款口径（内部走支付服务 RPC）：
	// 避免在事务持锁期间发起 gRPC 拉长锁持有时间；状态竞态由事务内条件更新兜底
	var pre model.Order
	if err := s.db.GetDb().WithContext(ctx).First(&pre, req.Id).Error; err != nil {
		return err
	}
	if pre.UserID != req.UserId {
		return bizerr.New(codes.PermissionDenied, "无权限取消该订单")
	}
	// 门店订单（收银台代客 source=2）C 端只读：取消走后台处理，
	// 防止门店已收款的现金单被 C 端按"货到付款"口径直接取消（无退款流水造成资损）
	if pre.Source == 2 {
		return bizerr.New(codes.PermissionDenied, "门店订单请联系门店处理")
	}
	// 取消前置渠道确认（P0-1）：与后台取消、超时调度器同口径——
	// 本地无成功支付单不代表用户没付，回调可能丢失或验签失败；而订单一旦进 6，
	// 状态机禁 6→2，库存与优惠券已回补，资金挂账只能人工处理，故取消前必须问渠道。
	// 仅待支付单（status∈{1,100}）需要确认：已支付/备货中取消属退款流程，与到账无关
	if s.isPendingPaymentOrder(ctx, pre.ID) &&
		confirmPaidWithChannel(ctx, pre.ID, s.cfg) {
		return bizerr.New(codes.FailedPrecondition, "订单支付处理中，已同步为已支付，请稍后刷新查看")
	}
	allowed := cancelableStatuses(s.isCashOnDeliveryOrder(ctx, pre.ID))

	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o model.Order
		if err := tx.First(&o, req.Id).Error; err != nil {
			return err
		}
		if o.UserID != req.UserId {
			return bizerr.New(codes.PermissionDenied, "无权限取消该订单")
		}
		if !containsInt32(allowed, o.Status) {
			return bizerr.New(codes.FailedPrecondition, "订单状态不允许取消")
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
			return bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
		}

		var items []*model.OrderItem
		if err := tx.Where("order_id = ?", o.ID).Find(&items).Error; err != nil {
			return err
		}
		if err := releaseOrderStock(ctx, o.ID, items); err != nil {
			return err
		}

		// 回补优惠券：取消后把订单占用的用户券复位为未使用（否则券被永久核销）
		if o.CouponID > 0 {
			if _, err := grpcclient.ProductClient.ReleaseCoupon(ctx, &product.ReleaseCouponReq{OrderId: o.ID}); err != nil {
				return fmt.Errorf("回补优惠券失败: %w", err)
			}
		}

		return s.createStatusLogTx(tx, o.ID, o.Status, 6, "user", req.UserId, req.Reason)
	})
	if err == nil {
		// 取消成功后关闭待支付支付单，防迟到回调误置已支付
		closePendingPayments(ctx, req.Id)
	}
	return err
}

// DeleteOrder C 端用户删除订单（软删除）
// 仅终态订单（5已完成 6已取消 8已退款）可删；收银台代客单（source=2）在 C 端只读、
// 不提供任何操作（与 CancelOrder/ApplyRefund/ConfirmReceive 同口径），存在进行中售后
// （待审核 1 / 退款中 4）时拒绝；软删仅影响 C 端列表与角标统计，后台管理端与详情链路
// （售后进度 / 支付详情）保留可达，不清理任何数据、不产生状态流转日志
// 并发口径：整段在事务内对订单行加写锁，最终 UPDATE 的 WHERE 同时带状态与未删除条件，
// 与后台代客退款（把 5 改成 7 并插入待审核售后单）串行化，杜绝「读校验通过后状态已变」
//
// 参数：
//   - ctx: 上下文
//   - req: 删除请求（id + user_id）
//
// 返回值：
//   - error: 错误信息（未登录 / 订单不存在 / 非本人订单 / 门店单 / 非终态 / 有进行中售后 / 状态已变更）
//
// 使用示例：
//
//	err := svc.DeleteOrder(ctx, &order.DeleteOrderReq{Id: 123, UserId: 456})
func (s *OrderService) DeleteOrder(ctx context.Context, req *order.DeleteOrderReq) error {
	// 归属必须来自服务端解析的用户上下文；拿不到时 UserID=0，
	// 若放行会命中收银台散客单（user_id=0）形成跨用户删除
	if req.UserId <= 0 {
		return bizerr.New(codes.InvalidArgument, "未登录或用户标识缺失")
	}

	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&o, req.Id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return bizerr.New(codes.NotFound, "订单不存在")
			}
			return err
		}
		if o.UserID != req.UserId {
			return bizerr.New(codes.PermissionDenied, "无权限操作该订单")
		}
		// 幂等：重复删除视为目标已达成，不再回错误（前端统一提示删除成功）
		if o.UserDeleted == 1 {
			return nil
		}
		// 门店/收银台代客单 C 端只读：这类单允许商家发起代客退款，
		// 一旦被删，用户在等一笔钱却在 App 里看不到任何售后进度入口
		if o.Source == 2 {
			return bizerr.New(codes.FailedPrecondition, "门店订单请在门店办理")
		}
		// 仅终态可删：进行中的订单（含退款中 7）一律拒绝
		if !containsInt32([]int32{5, 6, 8}, o.Status) {
			return bizerr.New(codes.FailedPrecondition, "仅已完成、已取消或已退款的订单可删除")
		}

		// 进行中售后拦截：待审核(1)/退款中(4)的售后单存在时不允许删除
		var refundCount int64
		if err := tx.Model(&model.Refund{}).
			Where("order_id = ? AND status IN ?", o.ID, []int32{1, 4}).
			Count(&refundCount).Error; err != nil {
			return err
		}
		if refundCount > 0 {
			return bizerr.New(codes.FailedPrecondition, "订单存在进行中的售后，暂不能删除")
		}

		// 条件更新：状态与未删除一并进 WHERE，与并发的后台代客退款串行化；
		// 0 行说明订单在这把锁拿到之前已变更为不可删状态
		res := tx.Model(&model.Order{}).
			Where("id = ? AND user_id = ? AND user_deleted = ? AND status IN ?",
				o.ID, req.UserId, int32(0), []int32{5, 6, 8}).
			Updates(map[string]interface{}{
				"user_deleted": int32(1),
				"updated_at":   time.Now().Format("2006-01-02 15:04:05"),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
		}
		return nil
	})
}

// RestoreOrder 后台恢复被用户软删除的订单
// 客服场景「我删错了，帮我找回上次买的东西」：只复位 user_deleted 标记，不改状态、
// 不补状态日志。community_id>0 时限定在该社区范围内操作（社区管理员不得跨社区恢复），
// =0 表示不限（超管）。
//
// 参数：
//   - ctx: 上下文
//   - req: 恢复请求（id + community_id）
//
// 返回值：
//   - error: 错误信息（ID 非法 / 订单不存在或未被用户删除 / 不在可操作社区 / 更新失败）
//
// 使用示例：
//
//	err := svc.RestoreOrder(ctx, &order.RestoreOrderReq{Id: 123, CommunityId: 1})
func (s *OrderService) RestoreOrder(ctx context.Context, req *order.RestoreOrderReq) error {
	if req.Id <= 0 {
		return bizerr.New(codes.InvalidArgument, "订单 ID 无效")
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.Order{}).
		Where("id = ? AND user_deleted = ?", req.Id, int32(1))
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	res := db.Updates(map[string]interface{}{
		"user_deleted": int32(0),
		"updated_at":   time.Now().Format("2006-01-02 15:04:05"),
	})
	if res.Error != nil {
		return res.Error
	}
	// 0 行：订单不存在、不在该社区，或本来就没被删（三种情况对外统一为「未找到需恢复的订单」，
	// 不透露其他社区是否存在该单）
	if res.RowsAffected == 0 {
		return bizerr.New(codes.NotFound, "未找到需恢复的订单")
	}
	return nil
}

// releaseOrderStock 整单回补库存（C 端取消 / 后台取消 / 超时关单共用）
// 取消整单时明细数量固定为全量，收口为两次批量 RPC（主库存 + 秒杀库存），
// 替代原先逐行各一次共 2N 次往返；biz_no 统一为 cancel:{orderID}，
// 幂等由 product 侧 order_id+sku_id+op_type+biz_no 唯一键保证，重复取消只生效一次。
// 事务内调用：远端已提交的回补在本地事务回滚后无法撤回，故与扣减侧同样保持
// 「批量成功即视为已回补」，失败直接返回错误由上层回滚整单取消。
//
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - items: 订单明细
//
// 返回值：
//   - error: 释放库存/秒杀库存失败
func releaseOrderStock(ctx context.Context, orderID int64, items []*model.OrderItem) error {
	stockItems := make([]*product.StockItemQty, 0, len(items))
	for _, item := range items {
		stockItems = append(stockItems, &product.StockItemQty{SkuId: item.SkuID, Quantity: item.Quantity})
	}
	return releaseStockBatch(ctx, orderID, stockItems, fmt.Sprintf("cancel:%d", orderID))
}

// releaseStockBatch 整批回补库存（主库存 + 秒杀库存各一次批量 RPC）
// bizNo 为幂等业务号：取消用 cancel:{orderID}、退款用退款单号，
// 由 product 侧 order_id+sku_id+op_type+biz_no 唯一键保证重复调用只生效一次。
// 空明细直接返回（仅金额退无商品明细时不回补库存）。
//
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - stockItems: 待回补的 SKU 数量清单（同一 SKU 多行由 product 侧聚合）
//   - bizNo: 幂等业务号
//
// 返回值：
//   - error: 释放库存/秒杀库存失败
//
// 使用示例：
//
//	err := releaseStockBatch(ctx, orderID, stockItems, refund.RefundNo)
func releaseStockBatch(ctx context.Context, orderID int64, stockItems []*product.StockItemQty, bizNo string) error {
	if len(stockItems) == 0 {
		return nil
	}
	if _, err := grpcclient.ProductClient.BatchReleaseStock(ctx, &product.BatchReleaseStockReq{
		OrderId: orderID,
		BizNo:   bizNo,
		Items:   stockItems,
	}); err != nil {
		return fmt.Errorf("释放库存失败: %w", err)
	}
	// 秒杀独立库存回补（非秒杀商品 product 侧自动 no-op）
	if _, err := grpcclient.ProductClient.BatchReleaseFlashSaleStock(ctx, &product.BatchReleaseFlashSaleStockReq{
		OrderId: orderID,
		BizNo:   bizNo,
		Items:   stockItems,
	}); err != nil {
		return fmt.Errorf("释放秒杀库存失败: %w", err)
	}
	return nil
}

// closePendingPayments 关闭订单的待支付支付单（订单取消后的收尾动作）
// 尽力而为：调用失败仅记日志，不阻断取消流程（迟到回调由 handlePaySuccess 终态兜底拦住，
// 支付单状态不一致可由下次取消或人工处理）
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
func closePendingPayments(ctx context.Context, orderID int64) {
	if grpcclient.PaymentClient == nil {
		return
	}
	if _, err := grpcclient.PaymentClient.ClosePayments(ctx, &payment.ClosePaymentsReq{OrderId: orderID}); err != nil {
		log.Printf("[order] 关闭订单 %d 待支付支付单失败: %v", orderID, err)
	}
}

// orderTransitions 订单状态合法流转表
// key 为目标状态，value 为允许的来源状态集合
// 1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款
// 注意：目标状态 6（取消）不走本表，由 UpdateOrderStatus 的取消分支单独处理（需回补库存）；
// 100 为调度器「超时取消处理中」抢占态（见 scheduler.go）：支付回调二阶段/调度器补同步
// 与抢占并发时订单处于 100，需允许 100→2 补同步，否则已收款订单会卡在处理中态反复重试
var orderTransitions = map[int32][]int32{
	2: {1, 100},  // 待支付/取消处理中 -> 已支付（支付成功，含调度器反查补同步）
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
	// 取消订单需判定货到付款口径（涉及 payment-service RPC），提前到事务外预读，
	// 避免事务持锁期间依赖外部服务（payment-service 不可用会拖垮 order-service 连接池）
	var isCOD bool
	if req.Status == 6 {
		isCOD = s.isCashOnDeliveryOrder(ctx, req.Id)
		// 取消前置渠道确认（P0-B′）：本地无成功支付单不代表用户没付——回调可能丢失。
		// 订单进入 6 后状态机禁止 6→2，届时只能人工退款，所以必须在这里拦下。
		// 仅待支付单需要确认：已支付/备货中取消属于退款流程，与「钱有没有到账」无关
		if s.isPendingPaymentOrder(ctx, req.Id) &&
			confirmPaidWithChannel(ctx, req.Id, s.cfg) {
			return bizerr.New(codes.FailedPrecondition, "渠道已收款，订单已同步为已支付，请改走退款流程")
		}
	}
	// 取消前的订单状态（req.Status=6 时用于判断是否回退销量：仅已支付/备货中取消需回退，待支付取消未计销量）
	var statusBeforeCancel int32

	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o model.Order
		if err := tx.First(&o, req.Id).Error; err != nil {
			return err
		}

		// 社区隔离强校验：管理端操作必传 community_id（>0 时校验订单归属社区），
		// 内部系统调用（支付回调等）传 0 跳过
		if req.CommunityId > 0 && o.CommunityID != req.CommunityId {
			return bizerr.New(codes.PermissionDenied, "无权操作其他社区的订单")
		}

		// 取消订单：不走通用状态机，需回补库存并记录取消原因/时间
		if req.Status == 6 {
			statusBeforeCancel = o.Status
			return s.cancelOrderInTx(ctx, tx, &o, req.OperatorType, req.OperatorId, req.Remark, isCOD)
		}

		if !isTransitionAllowed(o.Status, req.Status) {
			return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("订单状态不允许流转: %d -> %d", o.Status, req.Status))
		}
		fromStatus := o.Status
		toStatus := req.Status
		now := time.Now().Format("2006-01-02 15:04:05")
		updates := map[string]interface{}{
			"status":     req.Status,
			"updated_at": now,
		}
		switch req.Status {
		case 2:
			updates["pay_time"] = &now
			// 到店单（delivery_type=3，即买即走）：收款成功即同事务终态化为已完成，
			// 不产生"已支付"停留态，后台无需再核销/完成（到店单无取货码，核销不适用）
			if o.DeliveryType == 3 {
				toStatus = 5
				updates["status"] = toStatus
				updates["complete_time"] = &now
			}
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
		if err := s.createStatusLogTx(tx, o.ID, fromStatus, req.Status, req.OperatorType, req.OperatorId, req.Remark); err != nil {
			return err
		}
		// 到店单一步终态化时补记完成日志，状态轨迹完整可追溯
		if toStatus != req.Status {
			return s.createStatusLogTx(tx, o.ID, req.Status, toStatus, req.OperatorType, req.OperatorId, "到店订单收款完成，自动完结")
		}
		return nil
	})
	if err == nil {
		switch req.Status {
		case 2:
			// 支付成功累加销量（含到店单收款即完结路径；调度器补同步同走此处）
			s.adjustOrderSales(ctx, req.Id, 1)
		case 6:
			// 取消成功后关闭待支付支付单，防迟到回调误置已支付
			closePendingPayments(ctx, req.Id)
			// 已支付/备货中取消需回退销量（待支付取消未计销量，不回退）
			if statusBeforeCancel == 2 || statusBeforeCancel == 3 {
				s.adjustOrderSales(ctx, req.Id, -1)
			}
		}
	}
	return err
}

// adjustOrderSales 按订单明细调整商品/SKU 销量（净成交口径）
// 支付成功(+1)/取消回退(-1)共用；必须在主事务提交成功后调用，
// 失败仅记日志不回滚主流程（展示口径容忍瞬时缺失；AdjustSalesCount 无幂等键，放事务内重试会重复调整）
//
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - sign: +1 累加 / -1 回退
func (s *OrderService) adjustOrderSales(ctx context.Context, orderID int64, sign int32) {
	var items []model.OrderItem
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ?", orderID).Find(&items).Error; err != nil {
		log.Printf("[order] 查询订单明细失败（销量未调整）: order_id=%d err=%v", orderID, err)
		return
	}
	reqItems := make([]*product.AdjustSalesCountItem, 0, len(items))
	for _, it := range items {
		reqItems = append(reqItems, &product.AdjustSalesCountItem{SkuId: it.SkuID, Delta: sign * it.Quantity})
	}
	if len(reqItems) == 0 {
		return
	}
	if _, err := grpcclient.ProductClient.AdjustSalesCount(ctx, &product.AdjustSalesCountReq{Items: reqItems}); err != nil {
		log.Printf("[order] 调整销量失败: order_id=%d sign=%d err=%v", orderID, sign, err)
	}
}

// errPickupCodeMismatch 取货码不匹配（哨兵错误：防爆破计数依赖它判定，避免字符串匹配错误文案）
var errPickupCodeMismatch = bizerr.New(codes.FailedPrecondition, "取货码不正确，请核对后重试")

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

	// 防暴力破解：同一订单连续输错取货码 5 次后锁定 30 分钟
	lockKey := fmt.Sprintf("pickup:lock:%d", req.OrderId)
	failKey := fmt.Sprintf("pickup:fail:%d", req.OrderId)
	if s.redisClient != nil {
		if locked, _ := s.redisClient.Exists(ctx, lockKey).Result(); locked > 0 {
			ttl, _ := s.redisClient.TTL(ctx, lockKey).Result()
			return nil, bizerr.New(codes.FailedPrecondition, fmt.Sprintf("取货码错误次数过多，请 %d 分钟后再试", int(ttl.Minutes())+1))
		}
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
			return errPickupCodeMismatch
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
		// 取货码错误时累计失败次数，达阈值后锁定（哨兵错误判定，与文案解耦）
		if s.redisClient != nil && errors.Is(err, errPickupCodeMismatch) {
			failures, _ := s.redisClient.Incr(ctx, failKey).Result()
			s.redisClient.Expire(ctx, failKey, 30*time.Minute)
			if failures >= 5 {
				s.redisClient.Set(ctx, lockKey, 1, 30*time.Minute)
				s.redisClient.Del(ctx, failKey)
			}
		}
		return nil, err
	}
	// 核销成功，清除失败计数
	if s.redisClient != nil {
		s.redisClient.Del(ctx, failKey)
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
//   - isCOD: 是否货到付款（由调用方在事务外预读，避免事务内 RPC）
//
// 返回值：
//   - error: 错误信息
func (s *OrderService) cancelOrderInTx(ctx context.Context, tx *gorm.DB, o *model.Order, operatorType string, operatorID int64, reason string, isCOD bool) error {
	// 货到付款单未实际收款，未发货（2/3）也可直接取消（与 C 端 CancelOrder 同口径）
	allowed := cancelableStatuses(isCOD)
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
	if err := releaseOrderStock(ctx, o.ID, items); err != nil {
		return err
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
		return 0, 0, bizerr.New(codes.FailedPrecondition, "未找到可用优惠券")
	}
	coupon, err := grpcclient.ProductClient.GetCoupon(ctx, &product.GetCouponReq{Id: couponID})
	if err != nil {
		return 0, 0, err
	}
	// 券范围内商品金额（门槛与优惠基数）
	eligible := couponEligibleAmount(coupon, items)
	if eligible < target.Threshold {
		return 0, 0, bizerr.New(codes.FailedPrecondition, "订单中可使用该券的商品金额不满足使用门槛")
	}
	return calculateDiscount(eligible, coupon), target.Id, nil
}

// flashSalePriceEnabled 秒杀价开关：库存扣减与限购校验尚未实现，关闭防止超卖资损；
// 待 DeductFlashStock + LimitPerUser 完整实现后置为 true
const flashSalePriceEnabled = false

// loadFlashSalePrices 一次取回社区当前生效的秒杀价表
// 原先按明细逐条查价：每条明细都要 ListFlashSales + 逐个生效活动 GetFlashSale，
// N 行明细即 N×(1+K) 次 RPC；本函数按请求维度只取一次，调用方用 sku_id 查表。
// 取数失败返回空表（与原先「flashErr 忽略、按原价成交」同口径，不阻断下单）。
//
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//
// 返回值：
//   - map[int64]float64: SKU ID -> 生效秒杀价；无生效活动或开关关闭时为空表
//
// 使用示例：
//
//	prices := s.loadFlashSalePrices(ctx, communityID)
//	price := prices[skuID] // 未命中为 0，调用方按 >0 判定
func (s *OrderService) loadFlashSalePrices(ctx context.Context, communityID int64) map[int64]float64 {
	prices := make(map[int64]float64)
	if !flashSalePriceEnabled {
		return prices
	}
	fsResp, err := grpcclient.ProductClient.ListFlashSales(ctx, &product.ListFlashSalesReq{CommunityId: communityID})
	if err != nil {
		return prices
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
			// 同一 SKU 命中多个生效活动时以先遍历到的活动为准，与逐条查价口径一致
			if _, ok := prices[item.SkuId]; !ok {
				prices[item.SkuId] = item.FlashPrice
			}
		}
	}
	return prices
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

// parseTime 解析时间字符串（兼容两种格式：空格分隔为前端/写入格式，
// RFC3339 为 GORM 从 DATETIME 列读出 string 时的格式，解析失败返回零值）
// 参数：
//   - s: 时间字符串，格式 2006-01-02 15:04:05 或 RFC3339（如 2026-10-16T00:00:00+08:00）
//
// 返回值：
//   - time.Time: 时间对象（无法解析时为零值）
func parseTime(s string) time.Time {
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local); err == nil {
		return t
	}
	t, _ := time.ParseInLocation(time.RFC3339, s, time.Local)
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
		case 2, 3:
			// 待发货口径涵盖备货中（3），否则状态之和会小于总单数
			resp.PendingShip += int32(c.Count)
		case 4:
			resp.PendingReceive = int32(c.Count)
		case 5:
			resp.Completed = int32(c.Count)
		case 6, 8:
			// 已退款（8）为交易未达成的终态，统计口径并入已取消，保证状态之和等于总单数
			resp.Cancelled += int32(c.Count)
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
		// 客单价按"分"取整，避免 float64 除法产生 5.0649999999999995 之类的尾差
		avg = math.Round(result.PayAmount/float64(result.TotalOrders)*100) / 100
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
// GetUserFrequentProducts 获取用户常买商品（购物车"常买商品"列表 / 管理端会员订单弹窗排行）
// 统计口径（决策点③）：仅计有效订单（已支付及之后，排除待支付1/已取消6/退款7/已退款8）；
// 购买次数 = 去重订单数，另附该用户买该商品最常买的 SKU（供一键加购）
// 两种模式：page>0 走分页（管理端，LIMIT/OFFSET 并回填 total）；否则按 limit 取 Top N（C 端购物车，total 不填）
// 参数：
//   - ctx: 上下文
//   - req: 请求（user_id + limit 或 page/page_size）
//
// 返回值：
//   - *order.GetUserFrequentProductsResp: 常买商品列表（按购买次数降序）+ 分页模式下的商品总数
//   - error: 错误信息
func (s *OrderService) GetUserFrequentProducts(ctx context.Context, req *order.GetUserFrequentProductsReq) (*order.GetUserFrequentProductsResp, error) {
	if req.UserId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少用户 ID")
	}

	// 有效订单口径：排除待支付(1)/已取消(6)/退款中(7)/已退款(8)
	validStatuses := []int32{2, 3, 4, 5}

	paged := req.Page > 0

	// 基础查询（Join + 过滤条件）供总数与分页两处复用，避免重复拼装导致口径漂移
	baseQuery := func() *gorm.DB {
		q := s.db.GetDb().WithContext(ctx).
			Table("order_items").
			Joins("JOIN orders ON orders.id = order_items.order_id").
			Where("orders.user_id = ? AND orders.status IN ?", req.UserId, validStatuses)
		// 用户软删除过滤：C 端购物车「常买商品」不统计已被用户删除的历史订单，
		// 后台会员弹窗（include_user_deleted=true）与同弹窗订单 Tab 保持全量口径
		if !req.IncludeUserDeleted {
			q = q.Where("orders.user_deleted = ?", int32(0))
		}
		// 社区语境：管理端仅统计本社区订单（超管/C 端本人查询传 0 表示不过滤）
		if req.CommunityId > 0 {
			q = q.Where("orders.community_id = ?", req.CommunityId)
		}
		return q
	}

	// 分页模式下先查商品总数（去重商品 ID），供前端计算页数
	var total int64
	if paged {
		if err := baseQuery().
			Select("COUNT(DISTINCT order_items.product_id) AS total").
			Scan(&total).Error; err != nil {
			return nil, err
		}
	}

	query := baseQuery().
		Select("order_items.product_id, order_items.product_name, MAX(order_items.product_image) AS product_image, " +
			"COUNT(DISTINCT orders.id) AS purchase_count, SUM(order_items.quantity) AS total_quantity").
		Group("order_items.product_id, order_items.product_name").
		Order("purchase_count DESC, total_quantity DESC")

	if paged {
		pageSize := int(req.PageSize)
		if pageSize <= 0 {
			pageSize = 5
		}
		query = query.Limit(pageSize).Offset((int(req.Page) - 1) * pageSize)
	} else {
		limit := int(req.Limit)
		if limit <= 0 {
			limit = 10
		}
		query = query.Limit(limit)
	}

	var rows []*order.FrequentProductItem
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}

	// 附带每个商品该用户最常买的 SKU（按累计数量取 Top1，供一键加购）
	// 窗口函数一次查出全部商品的 Top1 SKU（MySQL 8.0+），替代逐商品循环查询，避免 N+1
	type skuTopRow struct {
		ProductId int64
		SkuId     int64
	}
	var skuRows []skuTopRow
	err := s.db.GetDb().WithContext(ctx).Raw(`
SELECT product_id, sku_id FROM (
    SELECT order_items.product_id AS product_id, order_items.sku_id AS sku_id,
           ROW_NUMBER() OVER (
               PARTITION BY order_items.product_id
               ORDER BY SUM(order_items.quantity) DESC
           ) AS rn
    FROM order_items
    JOIN orders ON orders.id = order_items.order_id
    WHERE orders.user_id = ? AND orders.status IN ?
    GROUP BY order_items.product_id, order_items.sku_id
) ranked
WHERE rn = 1`, req.UserId, validStatuses).Scan(&skuRows).Error
	if err != nil {
		// SKU 仅为附属信息（一键加购用），查询失败不阻断列表返回，仅记日志
		log.Printf("[order] 查询用户 %d 常买商品 SKU 失败（sku_id 置 0）: %v", req.UserId, err)
	} else {
		skuByProduct := make(map[int64]int64, len(skuRows))
		for _, sr := range skuRows {
			skuByProduct[sr.ProductId] = sr.SkuId
		}
		for _, r := range rows {
			if id, ok := skuByProduct[r.ProductId]; ok {
				r.SkuId = id
			}
		}
	}
	return &order.GetUserFrequentProductsResp{List: rows, Total: total}, nil
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
