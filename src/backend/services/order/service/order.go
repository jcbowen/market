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

	"github.com/jcbowen/market/src/backend/common/attachmenturl"
	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/likeutil"
	"github.com/jcbowen/market/src/backend/common/pageparam"
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
	// attachmentLoader 附件配置缓存读取器（common/attachmenturl）：
	// 签收凭证/商品图快照入库归一与出库拼完整地址共用
	attachmentLoader *attachmenturl.Loader
}

// NewOrderService 创建订单业务服务
// 参数：
//   - db: MySQL 实例
//   - cfg: 全局配置（可为 nil，此时按默认口径处理）
//
// 返回值：
//   - *OrderService: 订单业务服务实例
func NewOrderService(db *mysql.Instance, cfg *config.Config) *OrderService {
	return &OrderService{
		db:               db,
		cfg:              cfg,
		attachmentLoader: attachmenturl.NewLoader(db.GetDb()),
	}
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

// RestoreAttachmentURL 出库还原附件完整地址（快照口径）
// 走 common/attachmenturl.RestoreURL：先归一为相对路径再按当前附件配置重拼，
// 存量完整 URL（任意本站域名）自动跟随新配置，外链原样保留
//
// 参数：
//   - ctx: 上下文
//   - src: 相对路径、存量完整 URL 或外链
//
// 返回值：
//   - string: 完整访问 URL（空值原样返回）
func (s *OrderService) RestoreAttachmentURL(ctx context.Context, src string) string {
	if src == "" {
		return ""
	}
	return s.attachmentLoader.RestoreURL(ctx, src)
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
	// 商品明细与直接收款金额二选一：常规单必带明细；直接收款单（manual_amount>0）无明细
	if len(req.Items) == 0 && req.ManualAmount <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "订单商品不能为空")
	}
	// 直接收款单收口：仅收银台（source=2）到店（delivery_type=3）可用，
	// 金额即成交金额，不允许叠加商品明细/券/满减/手动优惠（网关已拦截，此处服务端兜底）。
	// 明细必须拦截：算价走 manual 分支不建 order_items，但事务仍会按 req.Items 扣库存，
	// 取消单按明细回补即回补不到，造成永久库存泄漏
	isManualOrder := req.ManualAmount > 0
	if isManualOrder {
		if req.Source != 2 || req.DeliveryType != 3 {
			return nil, bizerr.New(codes.InvalidArgument, "直接收款单仅收银台到店模式可用")
		}
		if len(req.Items) > 0 {
			return nil, bizerr.New(codes.InvalidArgument, "直接收款单不支持商品明细")
		}
		if req.CouponId > 0 || req.ManualDiscount > 0 {
			return nil, bizerr.New(codes.InvalidArgument, "直接收款单不支持优惠券与手动优惠")
		}
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

	// 直接收款单：金额即成交金额（四舍五入到分），无商品明细、无库存扣减、无满减/券
	if isManualOrder {
		totalAmount = math.Round(req.ManualAmount*100) / 100
	} else {
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
			if skuInfo == nil {
				// product 侧 BatchGetSkus 缺失即整体报错，正常不落到这里；
				// nil 防御避免跨服务协议回退为部分结果时解引用 panic
				return nil, bizerr.New(codes.FailedPrecondition, "SKU 已失效，请刷新后重新下单")
			}
			if skuInfo.Stock < item.Quantity {
				return nil, bizerr.New(codes.ResourceExhausted, fmt.Sprintf("商品 %s 库存不足", skuInfo.SkuCode))
			}
			productInfo := productByID[skuInfo.ProductId]
			if productInfo == nil {
				return nil, bizerr.New(codes.FailedPrecondition, "商品已失效，请刷新后重新下单")
			}

			// 起订份数收口（仅 C 端单）：min_buy 是线上订单的下单门槛，前端数量框只是体验约束，
			// 加购、再来一单、直调下单接口都能带进低于起订量的数量，统一在此拦下。
			// 收银台（source=2）不校验：店员按件线下收款，用起订量拦截会挡住正常交易
			if req.Source != 2 && productInfo.MinBuy > 1 && item.Quantity < productInfo.MinBuy {
				return nil, bizerr.New(codes.InvalidArgument,
					fmt.Sprintf("商品 %s 最低购买 %d 份", productInfo.Name, productInfo.MinBuy))
			}

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
				ProductID:   skuInfo.ProductId,
				SkuID:       item.SkuId,
				ProductName: productInfo.Name,
				SpecName:    skuInfo.SpecName,
				// 图片快照入库归一为相对路径（product 响应为完整 URL），出库按当前附件配置重拼，
				// 域名/存储模式变更后存量订单图片不失效
				ProductImage:  s.attachmentLoader.SourcePath(ctx, productInfo.CoverImage),
				Price:         price,
				Quantity:      item.Quantity,
				TotalPrice:    totalPrice,
				OriginalPrice: originalPrice,
				PriceModified: priceModified,
				CreatedAt:     now,
			})
		}
	}

	// 明细累加是多次浮点加法的叠加，19.99×3 之类的组合会出现 59.97000000000001，
	// 统一在汇入口收敛到 2 位小数（与 refund.go 的金额口径一致），起送门槛与文案不再被误差影响
	totalAmount = round2(totalAmount)

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

	// 分别计算满减与优惠券优惠，两者均按商品总价校验门槛并可叠加；
	// 直接收款单金额即成交金额，不参与满减与券（服务端兜底，网关已拦）
	fullReductionAmount := 0.0
	var discountAmount float64
	var userCouponID int64
	if !isManualOrder {
		fullReductionAmount = s.calculateFullReduction(ctx, req.CommunityId, totalAmount)

		// 优惠券是用户主动选择，校验失败（券不存在/已使用/门槛不满足）直接中断下单，
		// 避免页面展示优惠与订单实付不符的静默失败
		if req.CouponId > 0 {
			couponDiscount, ucID, err := s.applyCoupon(ctx, req.UserId, req.CouponId, itemScopes)
			if err != nil {
				return nil, bizerr.New(codes.FailedPrecondition, err.Error())
			}
			discountAmount = couponDiscount
			userCouponID = ucID
		}
	}

	payAmount := round2(totalAmount + deliveryFee - fullReductionAmount - discountAmount)
	// 手动优惠（收银台抹零/整单改价折算）：与券/满减叠加，但不可为负、不可超原应付（实付≥0 兜底）
	if req.ManualDiscount > 0 {
		if req.ManualDiscount > payAmount {
			return nil, bizerr.New(codes.InvalidArgument, "手动优惠金额不能超过应付金额")
		}
		payAmount = round2(payAmount - req.ManualDiscount)
	}
	if payAmount < 0 {
		payAmount = 0
	}

	var contactName, contactPhone, contactAddress string
	if req.AddressId > 0 {
		// 取地址失败必须拒单：吞错继续会产出收货信息全空的配送单——
		// 用户付了配送费却无地址可配（原实现 err==nil && resp!=nil 静默跳过）
		addrResp, err := grpcclient.UserClient.GetAddress(ctx, &user.GetAddressReq{
			Id:     req.AddressId,
			UserId: req.UserId,
		})
		if err != nil || addrResp == nil {
			return nil, bizerr.New(codes.FailedPrecondition, "收货地址获取失败，请重新选择地址后下单")
		}
		contactName = addrResp.ContactName
		contactPhone = addrResp.ContactPhone
		contactAddress = addrResp.Province + addrResp.City + addrResp.District + addrResp.Detail
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
	// 因此补偿只需按「是否已提交成功」整单回补，不再逐项追踪下标。
	// 直接收款单无商品明细，跳过库存扣减（事务仍无条件执行：落订单与创建日志）
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
		if len(stockItems) > 0 {
			if _, err := grpcclient.ProductClient.BatchDeductStock(ctx, &product.BatchDeductStockReq{
				OrderId: orderModel.ID,
				Items:   stockItems,
			}); err != nil {
				return fmt.Errorf("扣减库存失败: %w", err)
			}
			stockDeducted = true

			// 秒杀独立库存只在秒杀价开关开启时扣减：product 侧按「SKU 有生效活动」自动识别，
			// 开关关闭时订单仍按原价成交，却会把活动池扣走并因池耗尽（ResourceExhausted）
			// 拒掉本应有主库存的普通单，故与价格口径共用同一开关。
			// 回补侧无需同步收紧：无秒杀扣减日志时 product 侧本身就是 no-op。
			if flashSalePriceEnabled {
				if _, err := grpcclient.ProductClient.BatchDeductFlashSaleStock(ctx, &product.BatchDeductFlashSaleStockReq{
					OrderId: orderModel.ID,
					Items:   stockItems,
				}); err != nil {
					return fmt.Errorf("扣减秒杀库存失败: %w", err)
				}
				flashDeducted = true
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
		// 补偿路径：回补已提交到商品服务的远端副作用，尽力而为不掩盖原错误；
		// biz_no 用 comp:{orderID} 与取消/退款链路区分，重复补偿重试被幂等约束拦截。
		// 走 runPostCommitEffect：请求 ctx 可能已取消/超时（客户端断开），
		// 直接用它会令回补必然失败，而本地事务已回滚、远端扣减已生效 → 库存被白占，
		// 因此脱离取消信号 + 有限次重试，三次仍失败打 [CRITICAL] 供人工按幂等槽位补做。
		compBizNo := fmt.Sprintf("comp:%d", orderModel.ID)
		compDetail := fmt.Sprintf("order_id=%d biz_no=%s", orderModel.ID, compBizNo)
		if stockDeducted {
			_ = runPostCommitEffect(ctx, "下单补偿回补库存", compDetail,
				func(c context.Context) error {
					_, rErr := grpcclient.ProductClient.BatchReleaseStock(c, &product.BatchReleaseStockReq{
						OrderId: orderModel.ID,
						BizNo:   compBizNo,
						Items:   stockItems,
					})
					return rErr
				})
		}
		// 秒杀库存同样在远端已提交：一并回补（未扣减时 product 侧判无扣减日志后 no-op）
		if flashDeducted {
			_ = runPostCommitEffect(ctx, "下单补偿回补秒杀库存", compDetail,
				func(c context.Context) error {
					_, rErr := grpcclient.ProductClient.BatchReleaseFlashSaleStock(c, &product.BatchReleaseFlashSaleStockReq{
						OrderId: orderModel.ID,
						BizNo:   compBizNo,
						Items:   stockItems,
					})
					return rErr
				})
		}
		// 优惠券核销同样在远端已提交：一并回补，保证换号重试时同一张券可再次使用
		if userCouponID > 0 {
			_ = runPostCommitEffect(ctx, "下单补偿回补优惠券", fmt.Sprintf("order_id=%d", orderModel.ID),
				func(c context.Context) error {
					_, rErr := grpcclient.ProductClient.ReleaseCoupon(c, &product.ReleaseCouponReq{OrderId: orderModel.ID})
					return rErr
				})
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

// frequentProductDefaultPageSize 会员常买商品排行未传页长时的默认条数（后台弹窗一次展示 5 条）
const frequentProductDefaultPageSize = 5

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
	page, pageSize := pageparam.Clamp(int(req.Pagination.GetPage()), int(req.Pagination.GetPageSize()))

	db := s.applyOrderListFilters(ctx, req)

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var list []*model.Order
	if err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	if err := s.fillOrderItemCounts(ctx, list); err != nil {
		return nil, 0, err
	}
	return list, int(total), nil
}

// fillOrderItemCounts 批量回填订单商品明细行数
// 无商品单（如收银台直接收款）保持 0，调用方据此判断是否提供
// 「引用/按商品退款」等依赖商品明细的入口
// 参数：
//   - ctx: 上下文
//   - list: 订单列表（原地写入 ItemCount）
//
// 返回值：
//   - error: 统计查询失败时返回错误（宁可整页失败也不回填空值，避免误隐藏有效入口）
func (s *OrderService) fillOrderItemCounts(ctx context.Context, list []*model.Order) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(list))
	for _, o := range list {
		ids = append(ids, o.ID)
	}
	counts, err := orderItemCountsByOrders(ctx, s.db.GetDb(), ids)
	if err != nil {
		return err
	}
	for _, o := range list {
		o.ItemCount = counts[o.ID]
	}
	return nil
}

// orderItemCountsByOrders 批量统计订单的商品明细行数（一次 GROUP BY 覆盖全部入参订单）
// 订单服务与退款服务共用：订单列表回填 ItemCount、退款列表回填 OrderItemCount，
// 两者都要靠「订单有没有商品」区分收银台直接收款单，口径必须一致
// 参数：
//   - ctx: 上下文
//   - db: 事务/查询句柄
//   - orderIds: 订单 ID 集合（空集合直接返回空映射，不发查询）
//
// 返回值：
//   - map[int64]int32: 订单 ID -> 明细行数，无商品单不出现在结果集中（调用方按零值处理）
//   - error: 统计查询失败时返回错误
func orderItemCountsByOrders(ctx context.Context, db *gorm.DB, orderIds []int64) (map[int64]int32, error) {
	counts := make(map[int64]int32, len(orderIds))
	if len(orderIds) == 0 {
		return counts, nil
	}
	var rows []struct {
		OrderID int64
		Cnt     int32
	}
	if err := db.WithContext(ctx).Model(&model.OrderItem{}).
		Select("order_id, COUNT(*) AS cnt").
		Where("order_id IN ?", orderIds).
		Group("order_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.OrderID] = row.Cnt
	}
	return counts, nil
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
		db = db.Where("order_no LIKE ?", likeutil.Prefix(req.OrderNo))
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
		db = db.Where("contact_name LIKE ?", likeutil.Contains(req.ReceiverName))
	}
	if req.ReceiverPhone != "" {
		db = db.Where("contact_phone LIKE ?", likeutil.Contains(req.ReceiverPhone))
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
// 货到付款订单未实际收款，未发货（status∈{2,3}）可直接取消：置已取消并提交后再回补库存，不产生退款流水；
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

	// 待回补清单在事务内收集、提交后再执行：远端回补撤不回，本地回滚会造成
	// 「订单仍待支付而库存已放走」的一物两卖，故订单置 6 必须先于回补落定
	var eff *cancelEffect
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
		eff = &cancelEffect{OrderID: o.ID, Items: items, CouponID: o.CouponID}

		return s.createStatusLogTx(tx, o.ID, o.Status, 6, "user", req.UserId, req.Reason)
	})
	if err == nil {
		// 取消成功后回补库存与优惠券（失败入重试队列，不回滚已提交的取消）
		releaseCancelledOrder(ctx, s.db, eff)
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
// 只能在本地取消事务提交后调用（见 releaseCancelledOrder）：早先在事务内调用时，
// 远端回补一旦提交就撤不回，本地却可能因后续失败回滚，订单仍是待支付而库存已放走，
// 同一份货能被两个买家各下一单（一物两卖）。
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

// cancelEffect 取消链路在本地事务提交后要回补的远端副作用清单
// 由 cancelOrderInTx / CancelOrder / 调度器超时关单在事务内收集（明细与用券信息），
// 事务提交成功后交给 releaseCancelledOrder 执行
// 参数：
//   - OrderID: 订单 ID（同时是幂等号 cancel:{orderID} 的来源）
//   - Items: 取消时读到的订单明细（整单全量回补）
//   - CouponID: 订单占用的用户券 ID，0 表示未用券
//
// 使用示例：
//
//	var eff *cancelEffect
//	err := s.db.GetDb().Transaction(func(tx *gorm.DB) error {
//	    eff, err = s.cancelOrderInTx(tx, &o, ...)
//	    return err
//	})
//	if err == nil {
//	    releaseCancelledOrder(ctx, s.db, eff)
//	}
type cancelEffect struct {
	OrderID  int64
	Items    []*model.OrderItem
	CouponID int64
}

// taskTypeCancelRelease 取消收尾回补的调度器重试任务类型
// 取消已提交而回补最终失败时落一条 scheduler_failures，由调度器按幂等号重放到收敛
const taskTypeCancelRelease = "cancel_release"

// releaseCancelledOrder 取消事务提交后回补库存与优惠券（三条取消入口共用）
// 口径与退款完成路径一致：本地状态（订单置 6）先提交，远端回补随后尽力而为，
// 绝不回滚已提交的取消——订单回到待支付而库存已放走才是资损（一物两卖），
// 而回补失败只是货被暂占（少卖）。商品服务整体不可用时不会拖垮取消本身。
// 有限次重试仍失败时落 scheduler_failures(cancel_release) 交给调度器重放，
// 幂等号 cancel:{orderID} 保证重放只生效一次。
//
// 参数：
//   - ctx: 上下文（调用方请求 ctx；runPostCommitEffect 内部已脱离取消信号）
//   - db: 订单库实例（用于写重试队列）
//   - eff: 事务内收集的待回补清单，nil 表示无副作用需执行
func releaseCancelledOrder(ctx context.Context, db *mysql.Instance, eff *cancelEffect) {
	if eff == nil {
		return
	}
	if err := runCancelEffects(ctx, eff); err != nil {
		recordSchedulerFailure(ctx, db, eff.OrderID, taskTypeCancelRelease, err.Error())
	}
}

// runCancelEffects 执行取消收尾的远端回补（库存 + 优惠券），返回首个最终失败项
// 单项各自做有限次重试；两项互不阻断（库存回补失败不影响继续回券），
// 任一失败即返回错误，由调用方决定是否入队重放；重放整单安全（两侧均按幂等号去重）
//
// 参数：
//   - ctx: 上下文
//   - eff: 待回补清单（非 nil）
//
// 返回值：
//   - error: 任一收尾项重试后仍失败时返回其错误，全部成功返回 nil
func runCancelEffects(ctx context.Context, eff *cancelEffect) error {
	var firstErr error
	if len(eff.Items) > 0 {
		if err := runPostCommitEffect(ctx, "取消回补库存",
			fmt.Sprintf("order_id=%d biz_no=cancel:%d", eff.OrderID, eff.OrderID),
			func(c context.Context) error { return releaseOrderStock(c, eff.OrderID, eff.Items) }); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// 回补优惠券：取消后把订单占用的用户券复位为未使用（否则券被永久核销）；
	// product 侧按 order_id+status 条件更新幂等，重复调用只生效一次
	if eff.CouponID > 0 {
		if err := runPostCommitEffect(ctx, "取消回补优惠券", fmt.Sprintf("order_id=%d", eff.OrderID),
			func(c context.Context) error {
				_, err := grpcclient.ProductClient.ReleaseCoupon(c, &product.ReleaseCouponReq{OrderId: eff.OrderID})
				return err
			}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// loadCancelEffect 按订单重建待回补清单（供调度器重放取消收尾回补）
// 整单取消的回补口径固定为全量明细，订单已置 6 时明细与用券不会再变，重建安全
//
// 参数：
//   - ctx: 上下文
//   - db: 订单库实例
//   - orderID: 订单 ID
//
// 返回值：
//   - *cancelEffect: 重建的清单
//   - error: 明细查询失败
func loadCancelEffect(ctx context.Context, db *mysql.Instance, orderID int64) (*cancelEffect, error) {
	var o model.Order
	if err := db.GetDb().WithContext(ctx).First(&o, orderID).Error; err != nil {
		return nil, err
	}
	var items []*model.OrderItem
	if err := db.GetDb().WithContext(ctx).Where("order_id = ?", orderID).Find(&items).Error; err != nil {
		return nil, err
	}
	return &cancelEffect{OrderID: o.ID, Items: items, CouponID: o.CouponID}, nil
}

// recordSchedulerFailure 写入调度器失败重试队列（scheduler_failures）
// 从 Scheduler 方法收口为包级函数，供无调度器实例的订单服务写入同一张重试表；
// 入队失败只记日志，前置的 [CRITICAL] 日志已保证可观测
//
// 参数：
//   - ctx: 上下文
//   - db: 数据库实例
//   - orderID: 订单 ID
//   - taskType: 任务类型
//   - errMsg: 错误信息
func recordSchedulerFailure(ctx context.Context, db *mysql.Instance, orderID int64, taskType string, errMsg string) {
	now := time.Now().Format("2006-01-02 15:04:05")
	failure := &model.SchedulerFailure{
		OrderID:   orderID,
		TaskType:  taskType,
		Status:    1,
		ErrorMsg:  errMsg,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.GetDb().WithContext(ctx).Create(failure).Error; err != nil {
		log.Printf("[order-scheduler] 记录失败订单 %d 出错: %v", orderID, err)
	}
}

// closePendingPayments 关闭订单的待支付支付单（订单取消后的收尾动作）
// 走 runPostCommitEffect 加固：调用方 ctx 已可能因客户端断连被取消，收尾必须脱离该信号，
// 并在有限次重试后打 [CRITICAL] 留出补做线索；失败不阻断取消流程（迟到回调由
// handlePaySuccess 的终态兜底拦住，支付单状态不一致可由对账调度器或人工处理）。
// ClosePayments 幂等（没有待支付单直接返回成功），重试安全。
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
func closePendingPayments(ctx context.Context, orderID int64) {
	if grpcclient.PaymentClient == nil {
		return
	}
	_ = runPostCommitEffect(ctx, "关闭待支付支付单", fmt.Sprintf("order_id=%d", orderID),
		func(c context.Context) error {
			_, err := grpcclient.PaymentClient.ClosePayments(c, &payment.ClosePaymentsReq{OrderId: orderID})
			return err
		})
}

// orderTransitions 订单状态合法流转表
// key 为目标状态，value 为允许的来源状态集合
// 1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款
// 注意：目标状态 6（取消）不走本表，由 UpdateOrderStatus 的取消分支单独处理（需回补库存）；
// 100 为调度器「超时取消处理中」抢占态（见 scheduler.go）：支付回调二阶段/调度器补同步
// 与抢占并发时订单处于 100，需允许 100→2 补同步，否则已收款订单会卡在处理中态反复重试
var orderTransitions = map[int32][]int32{
	2: {1, 100},     // 待支付/取消处理中 -> 已支付（支付成功，含调度器反查补同步）
	3: {2},          // 已支付 -> 备货中
	4: {2, 3},       // 已支付/备货中 -> 配送中（发货）
	5: {2, 4},       // 已支付(自提)/配送中 -> 已完成（确认收货）
	7: {2, 3, 4, 5}, // 已支付/备货中/配送中/已完成 -> 退款中（已完成的售后窗口见 ApplyRefund）
	8: {7},          // 退款中 -> 已退款
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

// transitionOrderStatusTx 事务内按状态机校验并落库订单状态
// 退款发起（C 端申请 / 后台代客）等不走 UpdateOrderStatus 的旁路统一走这里，
// 避免「校验一套、写库另一套」后两处口径漂移。
// 调用方必须已在同一事务内 FOR UPDATE 锁住订单行；只更新 status/updated_at 两列，
// 不用整行 Save，防止把读取时点的其它列写回。
//
// 参数：
//   - tx: 已锁住订单行的事务
//   - o: 订单模型（读取时的状态作为流转起点，成功后同步内存值）
//   - to: 目标状态
//   - now: 更新时间，格式 2006-01-02 15:04:05
//
// 返回值：
//   - error: 流转不合法返回 FailedPrecondition；写库失败原样返回
//
// 使用示例：
//
//	if err := transitionOrderStatusTx(tx, &o, 7, now); err != nil {
//	    return err
//	}
func transitionOrderStatusTx(tx *gorm.DB, o *model.Order, to int32, now string) error {
	if !isTransitionAllowed(o.Status, to) {
		return bizerr.New(codes.FailedPrecondition, "订单状态不允许该操作")
	}
	if err := tx.Model(&model.Order{}).Where("id = ?", o.ID).
		Updates(map[string]interface{}{"status": to, "updated_at": now}).Error; err != nil {
		return err
	}
	o.Status = to
	o.UpdatedAt = now
	return nil
}

// UpdateOrderStatus 更新订单状态
// 带状态机校验：仅允许合法流转，退款中/已退款/已取消等状态不可再发货、完成或重新支付。
// 目标状态为 6（取消）时走独立取消语义：写取消原因/时间，事务提交后再回补库存与优惠券（后台取消复用）。
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
	// 取消链路提交后待执行的远端回补（库存 + 优惠券）
	var cancelEff *cancelEffect

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
			eff, err := s.cancelOrderInTx(tx, &o, req.OperatorType, req.OperatorId, req.Remark, isCOD)
			if err != nil {
				return err
			}
			cancelEff = eff
			return nil
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
			// 支付成功事件：全部支付路径的收口，尽力而为发布供后台 WS 语音通知
			s.publishOrderPaidEvent(ctx, req.Id)
		case 6:
			// 取消成功后回补库存与优惠券（失败入重试队列，不回滚已提交的取消）
			releaseCancelledOrder(ctx, s.db, cancelEff)
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

// bindableStatuses 散客单补绑会员允许的订单状态：已收款后的流转中/终态
// （2已支付 3备货中 4配送中 5已完成）；待支付应走「继续收款」时选会员，
// 已取消/退款单绑定无业务意义
var bindableStatuses = []int32{2, 3, 4, 5}

// BindOrderUser 散客订单补绑会员：仅 user_id=0 的已收款散客单可绑定一次（绑后不可换绑/解绑）。
// 收银台收款完成后顾客要求把消费挂到会员名下（积分等会员权益前置）使用；
// 绑定仅更新 orders.user_id，会员名等展示字段由网关按 user_id 实时填充，无需快照落库。
// 条件更新（WHERE user_id=0）防并发下重复绑定/换绑；会员存在性经 user 服务二次校验（防绕过网关直调）
// 参数：
//   - ctx: 上下文
//   - req: 绑定请求（order_id/user_id/操作者社区语境）
//
// 返回值：
//   - error: 订单不存在、跨社区、非散客单、状态不允许、会员不存在或已禁用时返回业务错误
func (s *OrderService) BindOrderUser(ctx context.Context, req *order.BindOrderUserReq) error {
	if req.OrderId <= 0 || req.UserId <= 0 {
		return bizerr.New(codes.InvalidArgument, "参数错误")
	}
	// 会员存在性校验：带社区语境（操作者非超管时校验会员归属，防跨社区挂账）；
	// 禁用会员不接受挂账
	if grpcclient.UserClient != nil {
		u, err := grpcclient.UserClient.GetUserById(ctx, &user.GetUserByIdReq{Id: req.UserId, CommunityId: req.CommunityId})
		if err != nil || u == nil || u.Id <= 0 {
			return bizerr.New(codes.NotFound, "会员不存在")
		}
		if u.Status != 1 {
			return bizerr.New(codes.FailedPrecondition, "该会员已被禁用，无法绑定")
		}
	}

	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o model.Order
		if err := tx.First(&o, req.OrderId).Error; err != nil {
			return bizerr.New(codes.NotFound, "订单不存在")
		}
		// 社区隔离：操作者社区语境与订单归属社区不一致时直接拒绝
		if req.CommunityId > 0 && o.CommunityID != req.CommunityId {
			return bizerr.New(codes.PermissionDenied, "无权操作其他社区的订单")
		}
		// 仅散客单可绑：user_id>0 说明已有归属（绑后不改口径）
		if o.UserID != 0 {
			return bizerr.New(codes.FailedPrecondition, "该订单已关联会员，不可重复绑定")
		}
		if !containsInt32(bindableStatuses, o.Status) {
			return bizerr.New(codes.FailedPrecondition, "仅已收款的订单可绑定会员")
		}
		// 条件更新防并发：仅当订单仍是散客时生效（并发重复绑定只成功一次）
		res := tx.Model(&model.Order{}).Where("id = ? AND user_id = 0", o.ID).Update("user_id", req.UserId)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return bizerr.New(codes.FailedPrecondition, "该订单已关联会员，不可重复绑定")
		}
		return nil
	})
}

// cancelOrderInTx 在事务中执行取消语义：条件更新状态、写取消原因/时间，并返回待回补清单。
// 供后台取消（UpdateOrderStatus 特判 status=6）复用；C 端 CancelOrder 有额外的归属校验，不走此方法。
// 库存/优惠券回补不在本方法内发起（远端已提交无法随本地事务回滚撤回），
// 由调用方在事务提交成功后把返回值交给 releaseCancelledOrder 执行。
// 参数：
//   - tx: 数据库事务
//   - o: 已加载的订单
//   - operatorType: 操作者类型（admin/user/system）
//   - operatorID: 操作者 ID
//   - reason: 取消原因（写入 cancel_reason 与状态日志备注）
//   - isCOD: 是否货到付款（由调用方在事务外预读，避免事务内 RPC）
//
// 返回值：
//   - *cancelEffect: 提交后需回补的清单（明细 + 用券）；失败时为 nil
//   - error: 错误信息
func (s *OrderService) cancelOrderInTx(tx *gorm.DB, o *model.Order, operatorType string, operatorID int64, reason string, isCOD bool) (*cancelEffect, error) {
	// 货到付款单未实际收款，未发货（2/3）也可直接取消（与 C 端 CancelOrder 同口径）
	allowed := cancelableStatuses(isCOD)
	if !containsInt32(allowed, o.Status) {
		return nil, bizerr.New(codes.FailedPrecondition, fmt.Sprintf("订单状态不允许取消: %d", o.Status))
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	res := tx.Model(&model.Order{}).Where("id = ? AND status IN ?", o.ID, allowed).Updates(map[string]interface{}{
		"status":        int32(6),
		"updated_at":    now,
		"cancel_time":   &now,
		"cancel_reason": reason,
	})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
	}

	var items []*model.OrderItem
	if err := tx.Where("order_id = ?", o.ID).Find(&items).Error; err != nil {
		return nil, err
	}
	// 库存与优惠券回补不在事务内做（远端已提交无法随本地回滚撤回），
	// 交给调用方在事务提交后经 releaseCancelledOrder 执行
	eff := &cancelEffect{OrderID: o.ID, Items: items, CouponID: o.CouponID}

	if err := s.createStatusLogTx(tx, o.ID, o.Status, 6, operatorType, operatorID, reason); err != nil {
		return nil, err
	}
	return eff, nil
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
	discount := calculateDiscount(eligible, coupon)
	// 门槛单一口径收口：calculateDiscount 按实时券面 coupon.Threshold 复算，
	// 发放后商家调高门槛时快照校验放行但复算恒为 0——券照常核销即静默烧券，
	// 与上方「券校验失败直接中断下单，避免静默失败」的设计意图相悖，拒单优于白烧
	if discount <= 0 {
		return 0, 0, bizerr.New(codes.FailedPrecondition, "该券使用条件已变更，暂不可用，请重新选择优惠或下单")
	}
	return discount, target.Id, nil
}

// flashSalePriceEnabled 秒杀价开关：关闭时按 SKU 原价成交且不扣秒杀池。
// 阻塞项是每人限购（limit_per_user）校验尚未实现——单用户可超额抢购活动价；
// 秒杀库存扣减本身已实现（product 侧幂等日志 + 条件更新防活动池超卖），
// 待 LimitPerUser 落地后置为 true
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
		// 调度器抢占态（100-103）不占业务状态值域，统计时必须按「来源状态」归桶，
		// 否则这些行只进 TotalOrders、不进任何桶，状态之和会小于总单数：
		// 100 超时取消处理中（原 1 待支付）、101 自动完成处理中（原 4 配送中）、
		// 102/103 自提单自动完成处理中（原 2 已支付 / 3 备货中）
		case 100:
			resp.PendingPay += int32(c.Count)
		case 101:
			resp.PendingReceive += int32(c.Count)
		case 102, 103:
			resp.PendingShip += int32(c.Count)
		}
		resp.TotalOrders += int32(c.Count)
	}
	return resp, nil
}

// statisticsExcludedStatuses 营收/销售/热销三处统计共用的排除口径：
// 待支付(1)与已取消(6)从未成交，已退款(8)为交易未达成的终态，三者都不计入金额与销量；
// 退款中(7)退款尚未成功，按既定口径仍计入，避免退款一发起就把当日营收整单抹掉。
// 三处查询必须复用本切片，避免各写一份字面量导致口径漂移。
var statisticsExcludedStatuses = []int32{1, 6, 8}

// GetSalesStatistics 获取销售统计
// 参数：
//   - ctx: 上下文
//   - req: 统计请求
//
// 返回值：
//   - *order.SalesStatisticsResp: 销售统计结果
//   - error: 错误信息
func (s *OrderService) GetSalesStatistics(ctx context.Context, req *order.GetSalesStatisticsReq) (*order.SalesStatisticsResp, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.Order{}).Where("status NOT IN ?", statisticsExcludedStatuses)
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
		Where("orders.status NOT IN ?", statisticsExcludedStatuses)
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
		page, pageSize := pageparam.ClampWith(int(req.Page), int(req.PageSize), frequentProductDefaultPageSize, pageparam.MaxSize)
		query = query.Limit(pageSize).Offset((page - 1) * pageSize)
	} else {
		// C 端 Top N：limit 来自查询参数，同样要封顶（分组聚合的行数放大不受控会拖慢整条订单链路）
		_, limit := pageparam.ClampWith(1, int(req.Limit), 10, pageparam.MaxSize)
		query = query.Limit(limit)
	}

	var rows []*order.FrequentProductItem
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	// 商品图快照出库还原完整地址（快照已入库相对路径，存量完整 URL 自动归一重拼）
	for _, row := range rows {
		row.ProductImage = s.attachmentLoader.RestoreURL(ctx, row.ProductImage)
	}

	// 附带每个商品该用户最常买的 SKU（按累计数量取 Top1，供一键加购）
	// 窗口函数一次查出全部商品的 Top1 SKU（MySQL 8.0+），替代逐商品循环查询，避免 N+1
	// 过滤条件必须与上方 baseQuery 逐项一致（用户/状态/软删/社区），
	// 否则「常买商品」列表里没有的订单可能反过来决定 Top1 SKU，一键加购会加错规格
	type skuTopRow struct {
		ProductId int64
		SkuId     int64
	}
	skuSQL := `
SELECT product_id, sku_id FROM (
    SELECT order_items.product_id AS product_id, order_items.sku_id AS sku_id,
           ROW_NUMBER() OVER (
               PARTITION BY order_items.product_id
               ORDER BY SUM(order_items.quantity) DESC
           ) AS rn
    FROM order_items
    JOIN orders ON orders.id = order_items.order_id
    WHERE orders.user_id = ? AND orders.status IN ?`
	skuArgs := []interface{}{req.UserId, validStatuses}
	if !req.IncludeUserDeleted {
		skuSQL += " AND orders.user_deleted = ?"
		skuArgs = append(skuArgs, int32(0))
	}
	if req.CommunityId > 0 {
		skuSQL += " AND orders.community_id = ?"
		skuArgs = append(skuArgs, req.CommunityId)
	}
	skuSQL += `
    GROUP BY order_items.product_id, order_items.sku_id
) ranked
WHERE rn = 1`

	var skuRows []skuTopRow
	err := s.db.GetDb().WithContext(ctx).Raw(skuSQL, skuArgs...).Scan(&skuRows).Error
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
		Where("status NOT IN ?", statisticsExcludedStatuses)
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
