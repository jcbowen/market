package service

import (
	"context"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// freeShippingSection 满免邮活动条目：按名称判重，条目按 community_key 归属演示社区；
// 排除范围里的商品按「社区内商品名称」解析（不依赖跨段 key 映射，应用中心单独导入亦可工作）
type freeShippingSection struct {
	svc *DataImportService
}

func (s *freeShippingSection) Key() string    { return "free_shipping" }
func (s *freeShippingSection) Name() string   { return "满免邮活动" }
func (s *freeShippingSection) Scope() string  { return SectionScopeCommunity }
func (s *freeShippingSection) InitOnly() bool { return false }
func (s *freeShippingSection) Description() string {
	return "满额免配送费活动（长期有效，含排除商品演示范围）"
}

func (s *freeShippingSection) ItemCount(pkg *SeedPackage) int32 {
	scope := communityScopeKey(pkg, "")
	var n int32
	for i := range pkg.FreeShippings {
		if seedInCommunityScope(pkg.FreeShippings[i].CommunityKey, scope) {
			n++
		}
	}
	return n
}

// Preview 按目标社区名称判重；排除商品在社区内不存在、且不随本次勾选的目录段一起导入时
// 标注依赖缺失
func (s *freeShippingSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
	names, _ := s.loadNameSet(ctx, req.CommunityID)
	scope := communityScopeKey(pkg, "")
	products := seedProductKeys(pkg)
	willImport := func(name string) bool {
		return seedDepSelected(name, products, scope, func(key string) bool { return isSelected(req, key) })
	}
	for i := range pkg.FreeShippings {
		f := &pkg.FreeShippings[i]
		if !isSelected(req, f.Key) || !seedInCommunityScope(f.CommunityKey, scope) {
			continue
		}
		if names[f.Name] {
			appendPreviewItem(items, s.Key(), f.Key, f.Name, "skip", "同名活动已存在", true)
			continue
		}
		if miss := s.findMissingProduct(ctx, req.CommunityID, f, willImport); miss != "" {
			appendPreviewItem(items, s.Key(), f.Key, f.Name, "depend_missing",
				"排除商品「"+miss+"」不在目标社区，请先导入商品目录", false)
			continue
		}
		appendPreviewItem(items, s.Key(), f.Key, f.Name, "create", "将创建活动（免邮门槛按参与商品额计）", true)
	}
}

// Import 逐条独立创建：活动 + 排除范围（仅本次归属社区的条目）
func (s *freeShippingSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	names, _ := s.loadNameSet(ctx, req.CommunityID)
	scope := communityScopeKey(pkg, req.CommunityKey)
	for i := range pkg.FreeShippings {
		f := &pkg.FreeShippings[i]
		if !isImportable(req, f.Key) || !seedInCommunityScope(f.CommunityKey, scope) {
			continue
		}
		if names[f.Name] {
			appendImportResult(items, s.Key(), f.Key, f.Name, "skipped", "同名活动已存在")
			continue
		}
		scopeReqs := make([]*product.FreeShippingScopeReq, 0, len(f.Scopes))
		for _, sc := range f.Scopes {
			sr := &product.FreeShippingScopeReq{ScopeType: sc.ScopeType}
			switch sc.ScopeType {
			case model.FreeShippingScopeTypeExcludeCategory:
				if id, ok := req.Refs.Get(sc.CategoryKey); ok {
					sr.CategoryId = id
				} else if cat := seedCategoryByKey(pkg, sc.CategoryKey); cat != nil {
					sr.CategoryId, _ = s.svc.categoryIDByName(ctx, req.CommunityID, cat.Name)
				}
			case model.FreeShippingScopeTypeExcludeProduct:
				id, err := s.svc.productIDByName(ctx, req.CommunityID, sc.ProductName)
				if err != nil {
					appendImportResult(items, s.Key(), f.Key, f.Name, "failed",
						"排除商品「"+sc.ProductName+"」不存在，请先导入商品目录")
					sr = nil
				}
				if sr != nil {
					sr.ProductId = id
				}
			}
			if sr == nil {
				scopeReqs = nil
				break
			}
			scopeReqs = append(scopeReqs, sr)
		}
		if scopeReqs == nil {
			continue
		}
		row, err := s.svc.marketingSvc.CreateFreeShipping(ctx, &product.CreateFreeShippingReq{
			CommunityId: req.CommunityID,
			Name:        f.Name,
			Threshold:   f.Threshold,
			// start/end 留空 = 长期有效（与种子口径一致）
			Scopes: scopeReqs,
		})
		if err != nil {
			appendImportResult(items, s.Key(), f.Key, f.Name, "failed", "创建活动失败："+err.Error())
			continue
		}
		req.Refs.Set(f.Key, row.ID)
		appendImportResult(items, s.Key(), f.Key, f.Name, "created", "活动已创建")
		created++
	}
	return created
}

// loadNameSet 目标社区活动名称集合
func (s *freeShippingSection) loadNameSet(ctx context.Context, communityID int64) (map[string]bool, error) {
	var names []string
	if err := s.svc.db.GetDb().WithContext(ctx).Model(&model.FreeShippingActivity{}).
		Where("community_id = ?", communityID).Pluck("name", &names).Error; err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m, nil
}

// findMissingProduct 排除范围中首个「既不在目标社区、也不随本次勾选目录段导入」的商品名
// （空串=全部就绪）
// 参数: willImport 依赖兜底判定（见 seedDepSelected），全选导入空社区时避免误报依赖缺失
func (s *freeShippingSection) findMissingProduct(ctx context.Context, communityID int64, f *SeedFreeShipping, willImport func(string) bool) string {
	for _, sc := range f.Scopes {
		if sc.ScopeType != model.FreeShippingScopeTypeExcludeProduct || sc.ProductName == "" {
			continue
		}
		if _, err := s.svc.productIDByName(ctx, communityID, sc.ProductName); err != nil && !willImport(sc.ProductName) {
			return sc.ProductName
		}
	}
	return ""
}
