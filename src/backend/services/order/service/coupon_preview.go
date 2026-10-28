package service

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
)

// PreviewCouponEligibility 券可用性试算（C 端确认页展示券是否可用与预计抵扣）
// 门槛与优惠基数均按「券范围内商品金额」计算，与下单 applyCoupon 同一口径
// （couponEligibleAmount + 用户券快照门槛 + calculateDiscount），
// 避免前端按全车金额误判为可用、提交时才被服务端打回。
// 商品清单的取价口径与 CreateOrder / PreviewFreeShipping 一致：
// 走 loadOrderItemCatalog + loadFlashSalePrices，优先使用生效秒杀价。
// 参数：
//   - ctx: 上下文
//   - req: 试算请求（社区 + 用户 + 商品清单 + 待试算券 ID）
//
// 返回值：
//   - *order.PreviewCouponEligibilityResp: 逐券试算结果（顺序与入参 coupon_ids 一致）
//   - error: 错误信息（参数缺失、商品/SKU 失效、用户券读取失败）
//
// 使用示例：
//
//	resp, err := svc.PreviewCouponEligibility(ctx, &order.PreviewCouponEligibilityReq{
//		CommunityId: 1,
//		UserId:      2,
//		Items:       []*order.PreviewFreeShippingItem{{SkuId: 3, Quantity: 1}},
//		CouponIds:   []int64{4},
//	})
func (s *OrderService) PreviewCouponEligibility(ctx context.Context, req *order.PreviewCouponEligibilityReq) (*order.PreviewCouponEligibilityResp, error) {
	if len(req.Items) == 0 {
		return nil, bizerr.New(codes.InvalidArgument, "商品清单不能为空")
	}
	if req.UserId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少用户信息")
	}
	// 没有待试算券时直接返回空表：确认页未选券/无券时不必再拉商品档案
	if len(req.CouponIds) == 0 {
		return &order.PreviewCouponEligibilityResp{List: []*order.CouponEligibility{}}, nil
	}

	items, err := s.buildItemScopes(ctx, req.Items, req.CommunityId)
	if err != nil {
		return nil, err
	}

	// 用户券快照：门槛校验用发放时的 threshold（与 applyCoupon 一致），未在列表中的券视为已用/失效
	ucResp, err := grpcclient.ProductClient.ListUserCoupons(ctx, &product.ListUserCouponsReq{
		UserId: req.UserId,
		Status: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("获取用户优惠券失败: %w", err)
	}
	ucByCouponID := make(map[int64]*product.UserCouponInfo, len(ucResp.List))
	for _, uc := range ucResp.List {
		ucByCouponID[uc.CouponId] = uc
	}

	list := make([]*order.CouponEligibility, 0, len(req.CouponIds))
	for _, couponID := range req.CouponIds {
		list = append(list, s.previewOneCoupon(ctx, couponID, items, ucByCouponID))
	}
	return &order.PreviewCouponEligibilityResp{List: list}, nil
}

// previewOneCoupon 单个券的可用性试算
// 判定顺序与 applyCoupon 对齐：用户券存在 → 范围内金额达快照门槛 → 按实时券面复算抵扣 > 0；
// 门槛差额不在此拼文案（前端按 threshold 与 eligible_amount 组合，便于统一格式化金额）
// 参数：
//   - ctx: 上下文
//   - couponID: 券 ID（coupons.id）
//   - items: 结算商品范围上下文
//   - ucByCouponID: 用户可用券快照索引
//
// 返回值：
//   - *order.CouponEligibility: 试算结果（不可用时 reason 说明原因、discount_amount 为 0）
func (s *OrderService) previewOneCoupon(ctx context.Context, couponID int64, items []itemScope, ucByCouponID map[int64]*product.UserCouponInfo) *order.CouponEligibility {
	result := &order.CouponEligibility{CouponId: couponID}
	uc := ucByCouponID[couponID]
	if uc == nil {
		result.Reason = "券已失效或已使用"
		return result
	}
	result.Threshold = uc.Threshold

	// 券面（含适用范围）单条查询：仅试算用户在用的券，数量与确认页券列表同量级
	coupon, err := grpcclient.ProductClient.GetCoupon(ctx, &product.GetCouponReq{Id: couponID})
	if err != nil || coupon == nil {
		result.Reason = "券信息读取失败，请稍后重试"
		return result
	}

	result.EligibleAmount = round2(couponEligibleAmount(coupon, items))
	if result.EligibleAmount < uc.Threshold {
		return result
	}
	// calculateDiscount 按实时券面门槛复算：与 applyCoupon 的「条件已变更」拦截保持同一判定
	discount := round2(calculateDiscount(result.EligibleAmount, coupon))
	if discount <= 0 {
		result.Reason = "该券使用条件已变更，暂不可用"
		return result
	}
	result.Available = true
	result.DiscountAmount = discount
	return result
}

// buildItemScopes 把商品清单换算为范围判定上下文（productID/categoryID/totalPrice）
// 取价口径与 CreateOrder、PreviewFreeShipping 一致，优先使用生效秒杀价
// 参数：
//   - ctx: 上下文
//   - reqItems: 商品清单（sku_id + quantity）
//   - communityID: 结算社区（秒杀价按社区生效）
//
// 返回值：
//   - []itemScope: 范围判定上下文
//   - error: 商品/SKU 失效或档案读取失败
func (s *OrderService) buildItemScopes(ctx context.Context, reqItems []*order.PreviewFreeShippingItem, communityID int64) ([]itemScope, error) {
	skuIDs := make([]int64, 0, len(reqItems))
	for _, item := range reqItems {
		skuIDs = append(skuIDs, item.SkuId)
	}
	skuByID, productByID, err := s.loadOrderItemCatalog(ctx, skuIDs, communityID)
	if err != nil {
		return nil, err
	}
	flashPriceByID := s.loadFlashSalePrices(ctx, communityID)

	items := make([]itemScope, 0, len(reqItems))
	for _, item := range reqItems {
		skuInfo := skuByID[item.SkuId]
		if skuInfo == nil {
			return nil, bizerr.New(codes.FailedPrecondition, "SKU 已失效，请刷新后重试")
		}
		productInfo := productByID[skuInfo.ProductId]
		if productInfo == nil {
			return nil, bizerr.New(codes.FailedPrecondition, "商品已失效，请刷新后重试")
		}
		price := skuInfo.Price
		if flashPrice := flashPriceByID[item.SkuId]; flashPrice > 0 {
			price = flashPrice
		}
		items = append(items, itemScope{
			productID:  skuInfo.ProductId,
			categoryID: productInfo.CategoryId,
			totalPrice: price * float64(item.Quantity),
		})
	}
	return items, nil
}
