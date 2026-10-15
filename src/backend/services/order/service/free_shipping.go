package service

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
)

// itemScope 结算商品的范围判定上下文（免配送费门槛计算用）
type itemScope struct {
	productID  int64
	categoryID int64
	totalPrice float64
}

// freeShippingResult 免配送费命中结果
type freeShippingResult struct {
	// hit 是否满足免配送费（参与商品额 >= 门槛）
	hit bool
	// activityName 命中的活动名
	activityName string
	// threshold 免配送费门槛
	threshold float64
	// eligibleAmount 参与免配送费的商品金额合计
	eligibleAmount float64
	// remainAmount 距门槛差额（已满足时为 0）
	remainAmount float64
	// excludedNames 被排除商品/分类的展示名（去重，用于 C 端提示）
	excludedNames []string
}

// calcFreeShipping 计算商品清单在当前社区免配送费活动下的命中情况
// 口径（决策点①）：参与免配送费的商品金额合计 >= 门槛则免配送费；
// 被排除分类/商品命中的商品金额不计入门槛。RPC 失败或无生效活动时返回未命中（不阻断下单）。
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//   - items: 结算商品范围上下文
//
// 返回值：
//   - *freeShippingResult: 命中结果
func (s *OrderService) calcFreeShipping(ctx context.Context, communityID int64, items []itemScope) *freeShippingResult {
	res := &freeShippingResult{}
	resp, err := grpcclient.ProductClient.GetActiveFreeShipping(ctx, &product.GetActiveFreeShippingReq{CommunityId: communityID})
	if err != nil || resp == nil || resp.Activity == nil {
		return res
	}
	act := resp.Activity
	res.activityName = act.Name
	res.threshold = act.Threshold

	excludeCategories := make(map[int64]struct{})
	excludeProducts := make(map[int64]struct{})
	categoryNames := make(map[int64]string)
	productNames := make(map[int64]string)
	for _, sc := range act.Scopes {
		switch sc.ScopeType {
		case 4: // 排除分类
			excludeCategories[sc.CategoryId] = struct{}{}
			categoryNames[sc.CategoryId] = sc.CategoryName
		case 5: // 排除商品
			excludeProducts[sc.ProductId] = struct{}{}
			productNames[sc.ProductId] = sc.ProductName
		}
	}

	seenCategory := make(map[int64]bool)
	seenProduct := make(map[int64]bool)
	var eligible float64
	for _, it := range items {
		_, inCat := excludeCategories[it.categoryID]
		_, inProd := excludeProducts[it.productID]
		if inCat || inProd {
			if inCat && !seenCategory[it.categoryID] {
				seenCategory[it.categoryID] = true
				name := categoryNames[it.categoryID]
				if name == "" {
					name = fmt.Sprintf("分类#%d", it.categoryID)
				}
				res.excludedNames = append(res.excludedNames, name)
			}
			if inProd && !seenProduct[it.productID] {
				seenProduct[it.productID] = true
				name := productNames[it.productID]
				if name == "" {
					name = fmt.Sprintf("商品#%d", it.productID)
				}
				res.excludedNames = append(res.excludedNames, name)
			}
			continue
		}
		eligible += it.totalPrice
	}
	res.eligibleAmount = eligible
	if eligible >= act.Threshold {
		res.hit = true
		return res
	}
	res.remainAmount = act.Threshold - eligible
	return res
}

// couponEligibleAmount 计算订单中「券范围内商品」的金额合计
// 范围语义（与优惠券模板 coupon_product_scopes 一致）：
//   - 无指定行（scope_type 2/3）：全场通用，全部商品计入
//   - 有指定行：仅指定分类/商品计入（选分类=该分类全部商品）
//   - 排除行（scope_type 4/5）：从计入集合中剔除
//
// 门槛判断与优惠计算均按该金额（方案 M 决策：仅范围内商品额计入门槛/享受优惠）
// 参数：
//   - coupon: 优惠券 proto 信息（含 scopes；nil/空 scopes 视为全场）
//   - items: 结算商品范围上下文
//
// 返回值：
//   - float64: 范围内商品金额合计
func couponEligibleAmount(coupon *product.CouponInfo, items []itemScope) float64 {
	if coupon == nil || len(coupon.Scopes) == 0 {
		var total float64
		for _, it := range items {
			total += it.totalPrice
		}
		return total
	}
	includeCategories := make(map[int64]struct{})
	includeProducts := make(map[int64]struct{})
	excludeCategories := make(map[int64]struct{})
	excludeProducts := make(map[int64]struct{})
	hasInclude := false
	for _, sc := range coupon.Scopes {
		switch sc.ScopeType {
		case 2:
			includeCategories[sc.CategoryId] = struct{}{}
			hasInclude = true
		case 3:
			includeProducts[sc.ProductId] = struct{}{}
			hasInclude = true
		case 4:
			excludeCategories[sc.CategoryId] = struct{}{}
		case 5:
			excludeProducts[sc.ProductId] = struct{}{}
		}
	}
	var total float64
	for _, it := range items {
		_, inExclude := excludeProducts[it.productID]
		if _, ok := excludeCategories[it.categoryID]; ok || inExclude {
			continue
		}
		if hasInclude {
			_, inCat := includeCategories[it.categoryID]
			_, inProd := includeProducts[it.productID]
			if !inCat && !inProd {
				continue
			}
		}
		total += it.totalPrice
	}
	return total
}

// PreviewFreeShipping 免配送费试算（C 端确认页展示）
// 按商品清单逐个取 SKU/商品（价格口径与 CreateOrder 一致：优先生效秒杀价），
// 计算参与商品额与门槛差额，供确认页展示「已免配送费 / 还差 X 元 / 不参与商品」
// 参数：
//   - ctx: 上下文
//   - req: 试算请求（社区 + 商品清单）
//
// 返回值：
//   - *order.PreviewFreeShippingResp: 试算结果
//   - error: 错误信息
func (s *OrderService) PreviewFreeShipping(ctx context.Context, req *order.PreviewFreeShippingReq) (*order.PreviewFreeShippingResp, error) {
	if len(req.Items) == 0 {
		return nil, bizerr.New(codes.InvalidArgument, "商品清单不能为空")
	}
	items := make([]itemScope, 0, len(req.Items))
	for _, item := range req.Items {
		skuInfo, err := grpcclient.ProductClient.GetSkuById(ctx, &product.GetSkuByIdReq{Id: item.SkuId})
		if err != nil {
			return nil, fmt.Errorf("获取 SKU 失败: %w", err)
		}
		productInfo, err := grpcclient.ProductClient.GetProduct(ctx, &product.GetProductReq{Id: skuInfo.ProductId})
		if err != nil {
			return nil, fmt.Errorf("获取商品失败: %w", err)
		}
		// 价格口径与 CreateOrder 一致：优先使用生效的秒杀价
		price := skuInfo.Price
		if flashPrice, flashErr := s.getActiveFlashSalePrice(ctx, req.CommunityId, item.SkuId); flashErr == nil && flashPrice > 0 {
			price = flashPrice
		}
		items = append(items, itemScope{
			productID:  skuInfo.ProductId,
			categoryID: productInfo.Product.CategoryId,
			totalPrice: price * float64(item.Quantity),
		})
	}

	// 基础配送费取社区配置值
	communityInfo, err := grpcclient.UserClient.GetCommunity(ctx, &user.GetCommunityReq{Id: req.CommunityId})
	if err != nil {
		return nil, fmt.Errorf("获取社区信息失败: %w", err)
	}

	res := s.calcFreeShipping(ctx, req.CommunityId, items)
	names := res.excludedNames
	if names == nil {
		names = []string{}
	}
	return &order.PreviewFreeShippingResp{
		FreeShipping:   res.hit,
		ActivityName:   res.activityName,
		Threshold:      res.threshold,
		EligibleAmount: res.eligibleAmount,
		RemainAmount:   res.remainAmount,
		ExcludedNames:  names,
		DeliveryFee:    communityInfo.DeliveryFee,
	}, nil
}
