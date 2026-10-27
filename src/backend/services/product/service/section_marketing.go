package service

import (
	"context"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// marketingSection 营销条目：满减 / 优惠券 / 秒杀
// 依赖 catalog 产出的分类与 SKU ID 映射（Refs）；映射缺失时兜底按名称/条码查目标社区，
// 仍缺失则该条目预检标注 depend_missing、执行时失败并说明依赖。
// 条目按 community_key 归属演示社区（营销全部挂社区1，社区2 只保留精简目录），
// 秒杀项的演示已售数（sold_count）在活动创建后按 SKU 回写。
type marketingSection struct {
	svc *DataImportService
}

func (s *marketingSection) Key() string    { return "marketing" }
func (s *marketingSection) Name() string   { return "营销活动" }
func (s *marketingSection) Scope() string  { return SectionScopeCommunity }
func (s *marketingSection) InitOnly() bool { return false }
func (s *marketingSection) Description() string {
	return "满减规则、优惠券（指定分类）与秒杀活动（引用目录 SKU，含相对时间窗换算与演示已售数）"
}

func (s *marketingSection) ItemCount(pkg *SeedPackage) int32 {
	scope := communityScopeKey(pkg, "")
	var n int32
	for i := range pkg.FullReductions {
		if seedInCommunityScope(pkg.FullReductions[i].CommunityKey, scope) {
			n++
		}
	}
	for i := range pkg.Coupons {
		if seedInCommunityScope(pkg.Coupons[i].CommunityKey, scope) {
			n++
		}
	}
	for i := range pkg.FlashSales {
		if seedInCommunityScope(pkg.FlashSales[i].CommunityKey, scope) {
			n++
		}
	}
	return n
}

// Preview 逐条预检：名称判重 + 依赖（分类/SKU）解析；仅本次归属社区的条目参与
func (s *marketingSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
	scope := communityScopeKey(pkg, "")
	frNames, _ := s.loadFRNames(ctx, req.CommunityID)
	for i := range pkg.FullReductions {
		r := &pkg.FullReductions[i]
		if !isSelected(req, r.Key) || !seedInCommunityScope(r.CommunityKey, scope) {
			continue
		}
		if frNames[r.Name] {
			appendPreviewItem(items, s.Key(), r.Key, r.Name, "skip", "同名满减规则已存在", true)
			continue
		}
		appendPreviewItem(items, s.Key(), r.Key, r.Name, "create", "将新建满减规则", true)
	}

	couponNames, _ := s.loadCouponNames(ctx, req.CommunityID)
	categories := seedCategoryNameKeys(pkg)
	for i := range pkg.Coupons {
		c := &pkg.Coupons[i]
		if !isSelected(req, c.Key) || !seedInCommunityScope(c.CommunityKey, scope) {
			continue
		}
		if couponNames[c.Name] {
			appendPreviewItem(items, s.Key(), c.Key, c.Name, "skip", "同名优惠券已存在", true)
			continue
		}
		if catName := s.resolveCategoryName(pkg, c); catName != "" {
			if _, err := s.svc.categoryIDByName(ctx, req.CommunityID, catName); err != nil &&
				!seedDepSelected(catName, categories, scope, func(key string) bool { return isSelected(req, key) }) {
				appendPreviewItem(items, s.Key(), c.Key, c.Name, "depend_missing",
					"适用分类「"+catName+"」不存在，请先导入商品目录或在分类管理中创建", false)
				continue
			}
		}
		appendPreviewItem(items, s.Key(), c.Key, c.Name, "create", "将新建优惠券", true)
	}

	fsNames, _ := s.loadFSNames(ctx, req.CommunityID)
	for i := range pkg.FlashSales {
		f := &pkg.FlashSales[i]
		if !isSelected(req, f.Key) || !seedInCommunityScope(f.CommunityKey, scope) {
			continue
		}
		if fsNames[f.Name] {
			appendPreviewItem(items, s.Key(), f.Key, f.Name, "skip", "同名秒杀活动已存在", true)
			continue
		}
		missing := s.findMissingSkuName(pkg, f)
		if missing != "" {
			appendPreviewItem(items, s.Key(), f.Key, f.Name, "depend_missing",
				"秒杀商品「"+missing+"」不存在，请先导入商品目录", false)
			continue
		}
		appendPreviewItem(items, s.Key(), f.Key, f.Name, "create",
			"将新建秒杀活动（时间窗按导入时刻相对换算，导入后立即处于进行中）", true)
	}
}

// Import 逐条独立创建（仅本次归属社区的条目）
func (s *marketingSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	scope := communityScopeKey(pkg, req.CommunityKey)
	// 满减
	frNames, _ := s.loadFRNames(ctx, req.CommunityID)
	for i := range pkg.FullReductions {
		r := &pkg.FullReductions[i]
		if !isImportable(req, r.Key) || !seedInCommunityScope(r.CommunityKey, scope) {
			continue
		}
		if frNames[r.Name] {
			appendImportResult(items, s.Key(), r.Key, r.Name, "skipped", "同名满减规则已存在")
			continue
		}
		row, err := s.svc.marketingSvc.CreateFullReduction(ctx, &product.CreateFullReductionReq{
			CommunityId:  req.CommunityID,
			Name:         r.Name,
			Threshold:    r.Threshold,
			ReduceAmount: r.ReduceAmount,
		})
		if err != nil {
			appendImportResult(items, s.Key(), r.Key, r.Name, "failed", "创建满减失败："+err.Error())
			continue
		}
		req.Refs.Set(r.Key, row.ID)
		appendImportResult(items, s.Key(), r.Key, r.Name, "created", "满减规则已创建")
		created++
	}

	// 优惠券
	couponNames, _ := s.loadCouponNames(ctx, req.CommunityID)
	for i := range pkg.Coupons {
		c := &pkg.Coupons[i]
		if !isImportable(req, c.Key) || !seedInCommunityScope(c.CommunityKey, scope) {
			continue
		}
		if couponNames[c.Name] {
			appendImportResult(items, s.Key(), c.Key, c.Name, "skipped", "同名优惠券已存在")
			continue
		}
		createReq := &product.CreateCouponReq{
			CommunityId:   req.CommunityID,
			Name:          c.Name,
			Type:          c.Type,
			Threshold:     c.Threshold,
			DiscountValue: c.DiscountValue,
			TotalCount:    c.TotalCount,
			PerUserLimit:  c.PerUserLimit,
			ValidDays:     c.ValidDays,
			PublishStatus: 1, // 已发布（演示数据直接可领）
			ExpireType:    1, // 领取后 N 天有效
		}
		scopeFailed := false
		for _, sc := range c.Scopes {
			scope := &product.CouponScopeReq{ScopeType: sc.ScopeType}
			if sc.CategoryKey != "" {
				catID, ok := req.Refs.Get(sc.CategoryKey)
				if !ok {
					if cat := seedCategoryByKey(pkg, sc.CategoryKey); cat != nil {
						catID, _ = s.svc.categoryIDByName(ctx, req.CommunityID, cat.Name)
					}
				}
				if catID == 0 {
					appendImportResult(items, s.Key(), c.Key, c.Name, "failed", "适用分类不存在，请先导入商品目录")
					scopeFailed = true
					break
				}
				scope.CategoryId = catID
			}
			createReq.Scopes = append(createReq.Scopes, scope)
		}
		if scopeFailed {
			continue
		}
		row, err := s.svc.marketingSvc.CreateCoupon(ctx, createReq)
		if err != nil {
			appendImportResult(items, s.Key(), c.Key, c.Name, "failed", "创建优惠券失败："+err.Error())
			continue
		}
		req.Refs.Set(c.Key, row.ID)
		appendImportResult(items, s.Key(), c.Key, c.Name, "created", "优惠券已创建")
		created++
	}

	// 秒杀
	fsNames, _ := s.loadFSNames(ctx, req.CommunityID)
	for i := range pkg.FlashSales {
		f := &pkg.FlashSales[i]
		if !isImportable(req, f.Key) || !seedInCommunityScope(f.CommunityKey, scope) {
			continue
		}
		if fsNames[f.Name] {
			appendImportResult(items, s.Key(), f.Key, f.Name, "skipped", "同名秒杀活动已存在")
			continue
		}
		fsReq := &product.CreateFlashSaleReq{
			CommunityId: req.CommunityID,
			Name:        f.Name,
			// 相对时间窗按导入时刻换算，保证导入后活动立即处于进行中（演示语义）
			StartTime: datetimeOffsetString(int(f.StartOffsetMinutes)),
			EndTime:   datetimeOffsetString(int(f.EndOffsetMinutes)),
		}
		skuFailed := false
		skuIDs := make([]int64, 0, len(f.Items))
		for _, it := range f.Items {
			skuID, ok := req.Refs.Get(it.SkuKey)
			if !ok {
				if sku := seedSkuByKey(pkg)[it.SkuKey]; sku != nil && sku.Barcode != "" {
					// 兜底：按条码在目标社区找已存在的 SKU
					var row model.Sku
					if err := s.svc.db.GetDb().WithContext(ctx).
						Where("community_id = ? AND barcode = ?", req.CommunityID, sku.Barcode).
						First(&row).Error; err == nil {
						skuID, ok = row.ID, true
					}
				}
			}
			if !ok {
				appendImportResult(items, s.Key(), f.Key, f.Name, "failed", "秒杀 SKU 不存在，请先导入商品目录")
				skuFailed = true
				break
			}
			skuIDs = append(skuIDs, skuID)
			fsReq.Items = append(fsReq.Items, &product.FlashSaleItemReq{
				SkuId:        skuID,
				FlashPrice:   it.FlashPrice,
				Stock:        it.Stock,
				LimitPerUser: it.LimitPerUser,
			})
		}
		if skuFailed {
			continue
		}
		row, err := s.svc.marketingSvc.CreateFlashSale(ctx, fsReq)
		if err != nil {
			appendImportResult(items, s.Key(), f.Key, f.Name, "failed", "创建秒杀活动失败："+err.Error())
			continue
		}
		// 演示已售数：CreateFlashSale 只建活动与项目，sold_count 由下单链路累加，
		// 演示包需要「已卖出 N 件」的展示口径，创建后按 SKU 回写
		if err := s.writeFlashSoldCounts(ctx, row.ID, f.Items, skuIDs); err != nil {
			appendImportResult(items, s.Key(), f.Key, f.Name, "failed", "回写秒杀已售数失败："+err.Error())
			continue
		}
		req.Refs.Set(f.Key, row.ID)
		appendImportResult(items, s.Key(), f.Key, f.Name, "created", "秒杀活动已创建")
		created++
	}
	return created
}

// writeFlashSoldCounts 回写秒杀项的演示已售数
// 参数：flashSaleID 新建活动 ID；seedItems 包内秒杀项（与 skuIDs 同序）；skuIDs 已解析的真实 SKU ID
// 返回值：error 任一项目回写失败即返回（活动已建，由调用方标注失败）
func (s *marketingSection) writeFlashSoldCounts(ctx context.Context, flashSaleID int64, seedItems []SeedFlashSaleItem, skuIDs []int64) error {
	for i := range seedItems {
		it := &seedItems[i]
		if it.SoldCount <= 0 || i >= len(skuIDs) {
			continue
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Model(&model.FlashSaleItem{}).
			Where("flash_sale_id = ? AND sku_id = ?", flashSaleID, skuIDs[i]).
			UpdateColumn("sold_count", it.SoldCount).Error; err != nil {
			return err
		}
	}
	return nil
}

// resolveCategoryName 取优惠券第一个分类范围的种子分类名（演示数据均为单分类范围）
func (s *marketingSection) resolveCategoryName(pkg *SeedPackage, c *SeedCoupon) string {
	for _, sc := range c.Scopes {
		if sc.CategoryKey == "" {
			continue
		}
		if cat := seedCategoryByKey(pkg, sc.CategoryKey); cat != nil {
			return cat.Name
		}
	}
	return ""
}

// findMissingSkuName 返回第一个解析不到的秒杀 SKU 展示名（名称+规格）
// 包内 SKU 视为可用（随目录条目导入，导入期有明确的失败提示）；包外引用直接判缺失
func (s *marketingSection) findMissingSkuName(pkg *SeedPackage, f *SeedFlashSale) string {
	prodByKey := seedProductByKey(pkg)
	skuByKey := seedSkuByKey(pkg)
	for _, it := range f.Items {
		if _, ok := skuByKey[it.SkuKey]; ok {
			continue
		}
		name := it.SkuKey
		if sku := skuByKey[it.SkuKey]; sku != nil {
			name = sku.SpecName
			if p := prodByKey[sku.ProductKey]; p != nil {
				name = p.Name + " " + sku.SpecName
			}
		}
		return name
	}
	return ""
}

// loadFRNames 目标社区满减规则名集合
func (s *marketingSection) loadFRNames(ctx context.Context, communityID int64) (map[string]bool, error) {
	var names []string
	if err := s.svc.db.GetDb().WithContext(ctx).Model(&model.FullReduction{}).
		Where("community_id = ?", communityID).Pluck("name", &names).Error; err != nil {
		return nil, err
	}
	return toNameSet(names), nil
}

// loadCouponNames 目标社区优惠券名集合
func (s *marketingSection) loadCouponNames(ctx context.Context, communityID int64) (map[string]bool, error) {
	var names []string
	if err := s.svc.db.GetDb().WithContext(ctx).Model(&model.Coupon{}).
		Where("community_id = ?", communityID).Pluck("name", &names).Error; err != nil {
		return nil, err
	}
	return toNameSet(names), nil
}

// loadFSNames 目标社区秒杀活动名集合
func (s *marketingSection) loadFSNames(ctx context.Context, communityID int64) (map[string]bool, error) {
	var names []string
	if err := s.svc.db.GetDb().WithContext(ctx).Model(&model.FlashSale{}).
		Where("community_id = ?", communityID).Pluck("name", &names).Error; err != nil {
		return nil, err
	}
	return toNameSet(names), nil
}

// toNameSet 字符串切片转集合
func toNameSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
