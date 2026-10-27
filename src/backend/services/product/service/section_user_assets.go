package service

import (
	"context"
	"errors"
	"strconv"

	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	cartModel "github.com/jcbowen/market/src/backend/services/cart/model"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// userAssetsSection 用户侧演示数据条目：用户持券 + 购物车。
// 用户按手机号解析（users 全局段唯一键），商品/SKU 按社区内「商品名 + 规格名」解析，
// 不依赖跨段 key 映射，单独经应用中心导入到已初始化的社区同样可工作；
// 应用中心路径（RequireTargetUser=true）必须指定导入会员，条目落到该会员名下
// （覆盖种子条目的演示用户手机号）；初始化链路仍按种子 user_phone 落演示用户；
// 预检的依赖判定含「同一次勾选导入」兜底（见 seedDepSelected），导入空社区时不误报缺失；
// 条目按 community_key 归属演示社区（用户券/购物车都挂在具体社区的用户语境下）。
type userAssetsSection struct {
	svc *DataImportService
}

func (s *userAssetsSection) Key() string    { return "user_assets" }
func (s *userAssetsSection) Name() string   { return "用户券与购物车" }
func (s *userAssetsSection) Scope() string  { return SectionScopeCommunity }
func (s *userAssetsSection) InitOnly() bool { return false }
func (s *userAssetsSection) Description() string {
	return "演示用户持券（未使用态）与购物车（含一条未选中）"
}

func (s *userAssetsSection) ItemCount(pkg *SeedPackage) int32 {
	scope := communityScopeKey(pkg, "")
	var n int32
	for i := range pkg.UserCoupons {
		if seedInCommunityScope(pkg.UserCoupons[i].CommunityKey, scope) {
			n++
		}
	}
	for i := range pkg.Carts {
		if seedInCommunityScope(pkg.Carts[i].CommunityKey, scope) {
			n++
		}
	}
	return n
}

// Preview 依赖与幂等判定：目标会员（应用中心必须指定）、券（营销段/库内）、商品与 SKU（目录段/库内）；
// 已持有该券 / 购物车已含该 SKU 的条目报 skip，与 Import 的实际落库行为同口径
func (s *userAssetsSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
	scope := communityScopeKey(pkg, "")
	selected := func(key string) bool { return isSelected(req, key) }
	coupons := seedCouponKeys(pkg)
	products := seedProductKeys(pkg)
	willImportCoupon := func(name string) bool { return seedDepSelected(name, coupons, scope, selected) }
	willImportProduct := func(name string) bool { return seedDepSelected(name, products, scope, selected) }
	targetUserID, userMissing, userErr := s.resolveTargetUser(ctx, req.TargetUserPhone, req.RequireTargetUser)
	for i := range pkg.UserCoupons {
		uc := &pkg.UserCoupons[i]
		if !isSelected(req, uc.Key) || !seedInCommunityScope(uc.CommunityKey, scope) {
			continue
		}
		if userMissing {
			appendPreviewItem(items, s.Key(), uc.Key, uc.CouponName, "depend_missing",
				"未指定导入会员，请先选择会员后重新预检", false)
			continue
		}
		if userErr != nil {
			appendPreviewItem(items, s.Key(), uc.Key, uc.CouponName, "depend_missing",
				"指定会员（"+req.TargetUserPhone+"）不存在，请重新选择", false)
			continue
		}
		userID := targetUserID
		if userID == 0 {
			var err error
			userID, err = s.svc.userIDByPhone(ctx, uc.UserPhone)
			if err != nil {
				appendPreviewItem(items, s.Key(), uc.Key, uc.CouponName, "depend_missing",
					"演示用户（"+uc.UserPhone+"）未初始化，用户券随环境初始化导入", false)
				continue
			}
		}
		couponID, err := s.svc.couponIDByName(ctx, req.CommunityID, uc.CouponName)
		if err != nil {
			if !willImportCoupon(uc.CouponName) {
				appendPreviewItem(items, s.Key(), uc.Key, uc.CouponName, "depend_missing",
					"优惠券「"+uc.CouponName+"」不在目标社区，请一并勾选营销数据", false)
				continue
			}
		} else if s.holdsCoupon(ctx, userID, couponID) {
			// skip 行保持默认勾选（与目录/营销段同口径：执行时自动跳过，无副作用）
			appendPreviewItem(items, s.Key(), uc.Key, uc.CouponName, "skip", "该用户已持有此券", true)
			continue
		}
		appendPreviewItem(items, s.Key(), uc.Key, uc.CouponName, "create", "将发放 1 张未使用券", true)
	}
	for i := range pkg.Carts {
		c := &pkg.Carts[i]
		if !isSelected(req, c.Key) || !seedInCommunityScope(c.CommunityKey, scope) {
			continue
		}
		if userMissing {
			appendPreviewItem(items, s.Key(), c.Key, c.ProductName, "depend_missing",
				"未指定导入会员，请先选择会员后重新预检", false)
			continue
		}
		if userErr != nil {
			appendPreviewItem(items, s.Key(), c.Key, c.ProductName, "depend_missing",
				"指定会员（"+req.TargetUserPhone+"）不存在，请重新选择", false)
			continue
		}
		userID := targetUserID
		if userID == 0 {
			var err error
			userID, err = s.svc.userIDByPhone(ctx, c.UserPhone)
			if err != nil {
				appendPreviewItem(items, s.Key(), c.Key, c.ProductName, "depend_missing",
					"演示用户（"+c.UserPhone+"）未初始化，购物车随环境初始化导入", false)
				continue
			}
		}
		_, sku, err := s.resolveCartItem(ctx, req.CommunityID, c)
		if err != nil {
			if !willImportProduct(c.ProductName) {
				appendPreviewItem(items, s.Key(), c.Key, c.ProductName, "depend_missing",
					"商品「"+c.ProductName+"（"+c.SpecName+"）」不在目标社区，请一并勾选商品目录", false)
				continue
			}
		} else if s.cartContains(ctx, userID, sku.ID) {
			appendPreviewItem(items, s.Key(), c.Key, c.ProductName, "skip", "该用户购物车已含此 SKU", true)
			continue
		}
		detail := "将加购 " + strconv.Itoa(int(c.Quantity)) + " 件"
		if !c.Selected {
			detail += "（未选中态）"
		}
		appendPreviewItem(items, s.Key(), c.Key, c.ProductName, "create", detail, true)
	}
}

// Import 用户券与购物车逐条独立创建（仅本次归属社区的条目）
func (s *userAssetsSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	scope := communityScopeKey(pkg, req.CommunityKey)
	targetUserID, userMissing, userErr := s.resolveTargetUser(ctx, req.TargetUserPhone, req.RequireTargetUser)
	// 用户券
	for i := range pkg.UserCoupons {
		uc := &pkg.UserCoupons[i]
		if !isImportable(req, uc.Key) || !seedInCommunityScope(uc.CommunityKey, scope) {
			continue
		}
		if userMissing {
			appendImportResult(items, s.Key(), uc.Key, uc.CouponName, "failed", "未指定导入会员，禁止导入")
			continue
		}
		if userErr != nil {
			appendImportResult(items, s.Key(), uc.Key, uc.CouponName, "failed",
				"指定会员（"+req.TargetUserPhone+"）不存在")
			continue
		}
		userID := targetUserID
		if userID == 0 {
			var err error
			userID, err = s.svc.userIDByPhone(ctx, uc.UserPhone)
			if err != nil {
				appendImportResult(items, s.Key(), uc.Key, uc.CouponName, "failed",
					"演示用户（"+uc.UserPhone+"）不存在")
				continue
			}
		}
		couponID, err := s.svc.couponIDByName(ctx, req.CommunityID, uc.CouponName)
		if err != nil {
			appendImportResult(items, s.Key(), uc.Key, uc.CouponName, "failed",
				"优惠券「"+uc.CouponName+"」不在目标社区")
			continue
		}
		// 幂等：该用户已有此券（任意状态）即跳过，避免重复发放
		if s.holdsCoupon(ctx, userID, couponID) {
			appendImportResult(items, s.Key(), uc.Key, uc.CouponName, "skipped", "该用户已持有此券")
			continue
		}
		// 券模板快照
		var coupon model.Coupon
		if err := s.svc.db.GetDb().WithContext(ctx).
			Where("id = ?", couponID).First(&coupon).Error; err != nil {
			appendImportResult(items, s.Key(), uc.Key, uc.CouponName, "failed", "读取券模板失败："+err.Error())
			continue
		}
		expireAt := datetimeOffsetString(int(uc.ExpireInDays) * 24 * 60)
		now := datetimeOffsetString(0)
		row := &model.UserCoupon{
			UserID:        userID,
			CouponID:      coupon.ID,
			Name:          coupon.Name,
			Type:          coupon.Type,
			Threshold:     coupon.Threshold,
			DiscountValue: coupon.DiscountValue,
			Status:        1, // 未使用（已用券绑定订单，随二期订单段导入）
			Source:        1, // 用户自领
			ExpireAt:      &expireAt,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		// 用户券行 + 券模板领取计数同事务：计数用列表达式自增，
		// 读回值再 +1 会与并发领取相互覆盖（演示包也会与真实领用并存）
		err = s.svc.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
			return tx.Model(&model.Coupon{}).Where("id = ?", coupon.ID).
				UpdateColumn("received_count", gorm.Expr("received_count + 1")).Error
		})
		if err != nil {
			appendImportResult(items, s.Key(), uc.Key, uc.CouponName, "failed", "发放券失败："+err.Error())
			continue
		}
		appendImportResult(items, s.Key(), uc.Key, uc.CouponName, "created", "券已发放")
		created++
	}

	// 购物车
	for i := range pkg.Carts {
		c := &pkg.Carts[i]
		if !isImportable(req, c.Key) || !seedInCommunityScope(c.CommunityKey, scope) {
			continue
		}
		if userMissing {
			appendImportResult(items, s.Key(), c.Key, c.ProductName, "failed", "未指定导入会员，禁止导入")
			continue
		}
		if userErr != nil {
			appendImportResult(items, s.Key(), c.Key, c.ProductName, "failed",
				"指定会员（"+req.TargetUserPhone+"）不存在")
			continue
		}
		userID := targetUserID
		if userID == 0 {
			var err error
			userID, err = s.svc.userIDByPhone(ctx, c.UserPhone)
			if err != nil {
				appendImportResult(items, s.Key(), c.Key, c.ProductName, "failed",
					"演示用户（"+c.UserPhone+"）不存在")
				continue
			}
		}
		prod, sku, err := s.resolveCartItem(ctx, req.CommunityID, c)
		if err != nil {
			appendImportResult(items, s.Key(), c.Key, c.ProductName, "failed",
				"商品「"+c.ProductName+"（"+c.SpecName+"）」不在目标社区")
			continue
		}
		// 幂等：同用户同 SKU 已在购物车即跳过（uk_user_sku）
		if s.cartContains(ctx, userID, sku.ID) {
			appendImportResult(items, s.Key(), c.Key, c.ProductName, "skipped", "该用户购物车已含此 SKU")
			continue
		}
		now := datetimeOffsetString(0)
		row := &cartModel.Cart{
			UserID:       userID,
			CommunityID:  req.CommunityID,
			ProductID:    prod.ID,
			SkuID:        sku.ID,
			Quantity:     c.Quantity,
			Selected:     c.Selected,
			ProductName:  prod.Name,
			SpecName:     sku.SpecName,
			ProductImage: sku.Image,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Create(row).Error; err != nil {
			appendImportResult(items, s.Key(), c.Key, c.ProductName, "failed", "加购失败："+err.Error())
			continue
		}
		// GORM bool 带 default:1 标签：Create 零值 false 会跳列落默认值 1，
		// 未选中态必须显式回写（同 memory 中的零值跳列坑）
		if !c.Selected {
			s.svc.db.GetDb().WithContext(ctx).Model(&cartModel.Cart{}).
				Where("id = ?", row.ID).Update("selected", false)
		}
		detail := "已加购"
		if !c.Selected {
			detail += "（未选中态）"
		}
		appendImportResult(items, s.Key(), c.Key, c.ProductName, "created", detail)
		created++
	}
	return created
}

// resolveTargetUser 解析本次导入的目标会员（预检与导入共用同一口径）：
//   - require=true（应用中心路径）必须显式指定 phone，未指定返回 missing=true，
//     由调用方按「禁止导入」口径逐条标注；指定时按手机号查库，无法解析返回 err；
//   - require=false（初始化链路）且未指定时返回 (0, false, nil)，调用方回退
//     种子条目的 user_phone 落演示用户。
//
// 参数：ctx 请求上下文；phone 指定导入会员手机号；require 是否强制指定（应用中心路径）
// 返回值：userID 已解析的会员 ID（回退场景为 0）；missing 是否未指定会员；err 指定手机号无法解析
func (s *userAssetsSection) resolveTargetUser(ctx context.Context, phone string, require bool) (int64, bool, error) {
	if phone == "" {
		if require {
			return 0, true, nil
		}
		return 0, false, nil
	}
	id, err := s.svc.userIDByPhone(ctx, phone)
	if err != nil {
		return 0, false, err
	}
	return id, false, nil
}

// resolveCartItem 按社区内「商品名 + SKU 规格名」解析商品与 SKU。
// 注意：单规格商品经 CreateProduct 链路落库的 spec_name 为空串（无规格树），
// 精确匹配不到时若该商品仅一个 SKU 则直接取用（种子口径与手工建品一致）
func (s *userAssetsSection) resolveCartItem(ctx context.Context, communityID int64, c *SeedCart) (*model.Product, *model.Sku, error) {
	var prod model.Product
	if err := s.svc.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND name = ?", communityID, c.ProductName).
		First(&prod).Error; err != nil {
		return nil, nil, err
	}
	var skus []model.Sku
	if err := s.svc.db.GetDb().WithContext(ctx).
		Where("product_id = ?", prod.ID).Order("id asc").Find(&skus).Error; err != nil {
		return nil, nil, err
	}
	for i := range skus {
		if skus[i].SpecName == c.SpecName {
			return &prod, &skus[i], nil
		}
	}
	if len(skus) == 1 {
		return &prod, &skus[0], nil
	}
	return nil, nil, errSkuNotFound
}

// errSkuNotFound 规格解析失败（多规格商品规格名不匹配）
var errSkuNotFound = errors.New("未找到匹配规格的 SKU")

// holdsCoupon 该用户是否已持有指定券模板的券（任意状态）——预检与导入共用同一幂等口径
// - 参数: userID 用户 ID；couponID 券模板 ID
// - 返回: bool 已持有则为 true（已用券同样计入，避免重复发放）
func (s *userAssetsSection) holdsCoupon(ctx context.Context, userID, couponID int64) bool {
	var count int64
	s.svc.db.GetDb().WithContext(ctx).Model(&model.UserCoupon{}).
		Where("user_id = ? AND coupon_id = ?", userID, couponID).Count(&count)
	return count > 0
}

// cartContains 该用户购物车是否已含指定 SKU——预检与导入共用同一幂等口径
// - 参数: userID 用户 ID；skuID SKU ID
// - 返回: bool 已存在则为 true
func (s *userAssetsSection) cartContains(ctx context.Context, userID, skuID int64) bool {
	var count int64
	s.svc.db.GetDb().WithContext(ctx).Model(&cartModel.Cart{}).
		Where("user_id = ? AND sku_id = ?", userID, skuID).Count(&count)
	return count > 0
}
