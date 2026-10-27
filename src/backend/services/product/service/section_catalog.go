package service

import (
	"context"
	"strconv"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// catalogSection 商品目录条目：分类 → 商品 → SKU → 初始批次
// 复用 ProductService.CreateProduct 完整链路（规格树/SKU/初始批次/汇总列），保证与手工建品同口径；
// 种子批次（FEFO 演示拆批与效期）在创建后同事务替换自动生成的 INIT 批次，数量之和恒等于 SKU 库存。
// 分类与商品按 community_key 归属演示社区（社区1 9类46品、社区2 6类20品），
// 初始化逐社区导入时只落各自条目；应用中心数据集为全部条目（两社区商品名已全部唯一）
type catalogSection struct {
	svc *DataImportService
}

func (s *catalogSection) Key() string    { return "catalog" }
func (s *catalogSection) Name() string   { return "商品目录" }
func (s *catalogSection) Scope() string  { return SectionScopeCommunity }
func (s *catalogSection) InitOnly() bool { return false }
func (s *catalogSection) Description() string {
	return "分类、商品、SKU 与初始库存批次（含 FEFO 演示批次）"
}

// ItemCount 应用中心展示用条目数：数据集为包内全部社区域条目（SectionScopeAll）
func (s *catalogSection) ItemCount(pkg *SeedPackage) int32 {
	scope := communityScopeKey(pkg, "")
	var n int32
	for i := range pkg.Categories {
		if seedInCommunityScope(pkg.Categories[i].CommunityKey, scope) {
			n++
		}
	}
	for i := range pkg.Products {
		if seedInCommunityScope(pkg.Products[i].CommunityKey, scope) {
			n++
		}
	}
	return n
}

// Preview 目录预检：SKU 按条码判重（设计口径：名称+条码去重，条码为社区内强唯一标识；
// 同名不同条码是不同商品，照常导入）；分类按名称判重
func (s *catalogSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
	catIDByName, _ := s.loadCategoryMap(ctx, req.CommunityID)
	barcodes, _ := s.loadBarcodeSet(ctx, req.CommunityID)
	skuByKey := seedSkuByKey(pkg)
	scope := communityScopeKey(pkg, "")

	for i := range pkg.Categories {
		cat := &pkg.Categories[i]
		if !isSelected(req, cat.Key) || !seedInCommunityScope(cat.CommunityKey, scope) {
			continue
		}
		if _, ok := catIDByName[cat.Name]; ok {
			appendPreviewItem(items, s.Key(), cat.Key, cat.Name, "skip", "同名分类已存在，商品将导入到既有分类", true)
			continue
		}
		appendPreviewItem(items, s.Key(), cat.Key, cat.Name, "create", "将新建分类", true)
	}

	for i := range pkg.Products {
		p := &pkg.Products[i]
		if !isSelected(req, p.Key) || !seedInCommunityScope(p.CommunityKey, scope) {
			continue
		}
		// 依赖校验：分类既不在目标社区、其分类条目也不在勾选中 → 标注依赖缺失
		if _, ok := catIDByName[p.CategoryKey]; !ok {
			if cat := seedCategoryByKey(pkg, p.CategoryKey); cat != nil && !isSelected(req, cat.Key) {
				appendPreviewItem(items, s.Key(), p.Key, p.Name, "depend_missing", "依赖分类「"+cat.Name+"」未勾选，请一并勾选或先在分类管理中创建", false)
				continue
			}
		}
		if clashCode := s.findBarcodeClash(p, skuByKey, barcodes); clashCode != "" {
			appendPreviewItem(items, s.Key(), p.Key, p.Name, "skip", "同条码 SKU 已存在（"+clashCode+"）", true)
			continue
		}
		skuCount, batchCount := 0, 0
		for _, skuKey := range p.SkuKeys {
			if sku := skuByKey[skuKey]; sku != nil {
				skuCount++
				batchCount += len(sku.Batches)
			}
		}
		detail := "将创建 1 商品 / " + strconv.Itoa(skuCount) + " SKU"
		if batchCount > 0 {
			detail += " / " + strconv.Itoa(batchCount) + " 库存批次"
		}
		appendPreviewItem(items, s.Key(), p.Key, p.Name, "create", detail, true)
	}
}

// Import 目录导入：分类先行（供商品与下游条目引用映射），商品逐个独立事务
func (s *catalogSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	skuByKey := seedSkuByKey(pkg)
	created += s.importCategories(ctx, pkg, req, items)
	created += s.importProducts(ctx, pkg, req, items, skuByKey)
	return created
}

// importCategories 导入分类并登记 key → 真实 ID 映射（仅本次归属社区的条目）
func (s *catalogSection) importCategories(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	catIDByName, err := s.loadCategoryMap(ctx, req.CommunityID)
	if err != nil {
		return 0
	}
	scope := communityScopeKey(pkg, req.CommunityKey)
	for i := range pkg.Categories {
		cat := &pkg.Categories[i]
		if !isImportable(req, cat.Key) || !seedInCommunityScope(cat.CommunityKey, scope) {
			continue
		}
		if id, ok := catIDByName[cat.Name]; ok {
			req.Refs.Set(cat.Key, id)
			appendImportResult(items, s.Key(), cat.Key, cat.Name, "skipped", "同名分类已存在")
			continue
		}
		row := &model.Category{
			CommunityID: req.CommunityID,
			ParentID:    0,
			Name:        cat.Name,
			Icon:        cat.Icon,
			Sort:        cat.Sort,
			Status:      1,
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Create(row).Error; err != nil {
			appendImportResult(items, s.Key(), cat.Key, cat.Name, "failed", "创建分类失败："+err.Error())
			continue
		}
		req.Refs.Set(cat.Key, row.ID)
		appendImportResult(items, s.Key(), cat.Key, cat.Name, "created", "分类已创建")
		created++
	}
	return created
}

// importProducts 逐商品导入：CreateProduct 全链路 + 种子批次替换 + 下架可选（仅本次归属社区的条目）
func (s *catalogSection) importProducts(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem, skuByKey map[string]*SeedSku) (created int32) {
	scope := communityScopeKey(pkg, req.CommunityKey)
	for i := range pkg.Products {
		p := &pkg.Products[i]
		if !isImportable(req, p.Key) || !seedInCommunityScope(p.CommunityKey, scope) {
			continue
		}
		// 分类解析：优先引用映射，兜底按名称查库
		catID, ok := req.Refs.Get(p.CategoryKey)
		if !ok {
			if cat := seedCategoryByKey(pkg, p.CategoryKey); cat != nil {
				catID, _ = s.svc.categoryIDByName(ctx, req.CommunityID, cat.Name)
			}
		}
		if catID == 0 {
			catName := p.CategoryKey
			if cat := seedCategoryByKey(pkg, p.CategoryKey); cat != nil {
				catName = cat.Name
			}
			appendImportResult(items, s.Key(), p.Key, p.Name, "failed", "分类「"+catName+"」不存在，请一并导入分类")
			continue
		}
		// 判重（预检后状态可能变化，执行前再核一次）：条码为强唯一标识
		if clashCode := s.findBarcodeClashLive(ctx, req.CommunityID, p, skuByKey); clashCode != "" {
			// 跳过路径同样注册商品/SKU 映射——下游段（营销/订单/进销存）按 key 引用，
			// 已初始化社区重复执行时映射断裂会让引用解析成 0
			s.registerExistingProductRefs(ctx, req, p, skuByKey)
			appendImportResult(items, s.Key(), p.Key, p.Name, "skipped", "同条码 SKU 已存在（"+clashCode+"）")
			continue
		}

		createdID, err := s.createOneProduct(ctx, pkg, req, p, catID, skuByKey)
		if err != nil {
			appendImportResult(items, s.Key(), p.Key, p.Name, "failed", "创建商品失败："+err.Error())
			continue
		}
		detail := "商品与 SKU 已创建"
		if !req.OnSale {
			detail += "（默认下架，请调整后上架）"
		}
		appendImportResult(items, s.Key(), p.Key, p.Name, "created", detail)
		req.Refs.Set(p.Key, createdID)
		created++
	}
	return created
}

// registerExistingProductRefs 为已存在的商品（按社区内商品名定位）注册
// 商品/SKU key → ID 映射；商品不存在时静默跳过（下游段按依赖缺失自行报错）
func (s *catalogSection) registerExistingProductRefs(ctx context.Context, req *ImportRequest, p *SeedProduct, skuByKey map[string]*SeedSku) {
	var prod model.Product
	if err := s.svc.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND name = ?", req.CommunityID, p.Name).
		First(&prod).Error; err != nil {
		return
	}
	req.Refs.Set(p.Key, prod.ID)
	var skus []model.Sku
	if err := s.svc.db.GetDb().WithContext(ctx).
		Where("product_id = ?", prod.ID).Find(&skus).Error; err != nil {
		return
	}
	codeToID := make(map[string]int64, len(skus))
	for _, row := range skus {
		codeToID[row.SkuCode] = row.ID
	}
	for _, skuKey := range p.SkuKeys {
		sku := skuByKey[skuKey]
		if sku == nil {
			continue
		}
		if id, ok := codeToID[sku.Code]; ok {
			req.Refs.Set(sku.Key, id)
		}
	}
}

// createOneProduct 单商品导入事务链：CreateProduct → SKU 映射 → 批次替换 → 可选下架
func (s *catalogSection) createOneProduct(
	ctx context.Context, pkg *SeedPackage, req *ImportRequest, p *SeedProduct, catID int64, skuByKey map[string]*SeedSku,
) (int64, error) {
	createReq := &product.CreateProductReq{
		CommunityId:  req.CommunityID,
		CategoryId:   catID,
		Name:         p.Name,
		Description:  p.Description,
		CoverImage:   p.Images[0],
		Images:       p.Images,
		Unit:         p.Unit,
		IsMultiSpec:  p.IsMultiSpec,
		Sort:         p.Sort,
		MinBuy:       1,
		ShowSales:    true,
		VirtualSales: p.VirtualSales,
	}
	if p.IsMultiSpec && p.Spec != nil {
		spec := &product.SpecReq{Name: p.Spec.Name, Sort: 0}
		for vi, v := range p.Spec.Values {
			spec.Values = append(spec.Values, &product.SpecValueReq{
				TempId: v.Key,
				Name:   v.Name,
				Sort:   int32(vi + 1),
			})
		}
		createReq.Specs = append(createReq.Specs, spec)
	}
	for _, skuKey := range p.SkuKeys {
		sku := skuByKey[skuKey]
		if sku == nil {
			return 0, bizerr.New(codes.Internal, "数据包内 SKU 缺失："+skuKey)
		}
		item := &product.SkuItemReq{
			SkuCode:       sku.Code,
			Price:         sku.Price,
			CostPrice:     sku.CostPrice,
			Stock:         sku.Stock,
			SafetyStock:   sku.SafetyStock,
			Image:         sku.Image,
			OriginalPrice: sku.OriginalPrice,
			Barcode:       sku.Barcode,
		}
		if p.IsMultiSpec && sku.SpecValueKey != "" {
			item.SpecValueIds = []string{sku.SpecValueKey}
		}
		createReq.SkuItems = append(createReq.SkuItems, item)
	}

	pRow, err := s.svc.productSvc.CreateProduct(ctx, createReq)
	if err != nil {
		return 0, err
	}

	// SKU key → 真实 ID 映射（下游秒杀条目引用）
	var skus []model.Sku
	if err := s.svc.db.GetDb().WithContext(ctx).Where("product_id = ?", pRow.ID).Find(&skus).Error; err != nil {
		return 0, err
	}
	codeToID := make(map[string]int64, len(skus))
	for _, row := range skus {
		codeToID[row.SkuCode] = row.ID
	}
	for _, skuKey := range p.SkuKeys {
		sku := skuByKey[skuKey]
		if sku == nil {
			continue
		}
		if id, ok := codeToID[sku.Code]; ok {
			req.Refs.Set(sku.Key, id)
		}
	}

	// 种子批次（FEFO 演示）：替换 CreateProduct 自动生成的 INIT 批次
	if err := s.syncSeedBatches(ctx, req.CommunityID, p, skuByKey, codeToID); err != nil {
		return 0, err
	}

	// 下架选项：CreateProduct 恒为上架，按需显式回写
	if !req.OnSale {
		if err := s.svc.db.GetDb().WithContext(ctx).Model(&model.Product{}).
			Where("id = ?", pRow.ID).Update("status", 0).Error; err != nil {
			return 0, err
		}
	}
	return pRow.ID, nil
}

// syncSeedBatches 用种子批次替换自动 INIT 批次（同事务；数量之和必须等于 SKU 库存）
func (s *catalogSection) syncSeedBatches(
	ctx context.Context, communityID int64, p *SeedProduct, skuByKey map[string]*SeedSku, codeToID map[string]int64,
) error {
	type pending struct {
		skuID int64
		seed  *SeedSku
	}
	var pendings []pending
	for _, skuKey := range p.SkuKeys {
		seed := skuByKey[skuKey]
		if seed == nil || len(seed.Batches) == 0 {
			continue
		}
		skuID, ok := codeToID[seed.Code]
		if !ok {
			continue
		}
		pendings = append(pendings, pending{skuID: skuID, seed: seed})
	}
	if len(pendings) == 0 {
		return nil
	}

	tx := s.svc.db.GetDb().WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()
	for _, pd := range pendings {
		if err := tx.Where("sku_id = ? AND status = 1 AND ref_no = ?", pd.skuID, "INIT").
			Delete(&model.StockBatch{}).Error; err != nil {
			tx.Rollback()
			return err
		}
		sum := int32(0)
		for bi := range pd.seed.Batches {
			b := &pd.seed.Batches[bi]
			prod := dateOffsetString(-int(b.ProductionAgo))
			row := model.StockBatch{
				CommunityID:    communityID,
				SkuID:          pd.skuID,
				BatchNo:        b.No,
				ProductionDate: &prod,
				Quantity:       b.Quantity,
				UnitCost:       b.UnitCost,
				Status:         1,
				SourceType:     1,
				RefNo:          b.Ref,
			}
			if b.ExpireIn != nil {
				exp := dateOffsetString(int(*b.ExpireIn))
				row.ExpireDate = &exp
			}
			if err := tx.Create(&row).Error; err != nil {
				tx.Rollback()
				return err
			}
			sum += b.Quantity
		}
		// 兜底对齐：种子拆批取整导致的尾差记到最后一批，保证 Σ批次 = SKU 库存（FEFO 前置约束）
		if diff := pd.seed.Stock - sum; diff != 0 && len(pd.seed.Batches) > 0 {
			last := &pd.seed.Batches[len(pd.seed.Batches)-1]
			if err := tx.Model(&model.StockBatch{}).
				Where("sku_id = ? AND batch_no = ? AND status = 1", pd.skuID, last.No).
				Update("quantity", gorm.Expr("quantity + ?", diff)).Error; err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit().Error
}

// loadCategoryMap 目标社区分类名称 → ID
func (s *catalogSection) loadCategoryMap(ctx context.Context, communityID int64) (map[string]int64, error) {
	var cats []model.Category
	if err := s.svc.db.GetDb().WithContext(ctx).
		Where("community_id = ?", communityID).Find(&cats).Error; err != nil {
		return nil, err
	}
	m := make(map[string]int64, len(cats))
	for _, c := range cats {
		m[c.Name] = c.ID
	}
	return m, nil
}

// findBarcodeClash 在预检加载的条码集合中找包内商品首个冲突 SKU 编码（空串=无冲突）
func (s *catalogSection) findBarcodeClash(p *SeedProduct, skuByKey map[string]*SeedSku, barcodes map[string]bool) string {
	for _, skuKey := range p.SkuKeys {
		if sku := skuByKey[skuKey]; sku != nil && sku.Barcode != "" && barcodes[sku.Barcode] {
			return sku.Code
		}
	}
	return ""
}

// findBarcodeClashLive 执行前实时查库核对条码冲突（预检与执行之间社区数据可能变化）
func (s *catalogSection) findBarcodeClashLive(ctx context.Context, communityID int64, p *SeedProduct, skuByKey map[string]*SeedSku) string {
	for _, skuKey := range p.SkuKeys {
		sku := skuByKey[skuKey]
		if sku == nil || sku.Barcode == "" {
			continue
		}
		if exists, _ := s.svc.skuExistsByBarcode(ctx, communityID, sku.Barcode); exists {
			return sku.Code
		}
	}
	return ""
}

// loadBarcodeSet 目标社区 SKU 条码集合
func (s *catalogSection) loadBarcodeSet(ctx context.Context, communityID int64) (map[string]bool, error) {
	var codes []string
	if err := s.svc.db.GetDb().WithContext(ctx).Model(&model.Sku{}).
		Where("community_id = ? AND barcode <> ''", communityID).Pluck("barcode", &codes).Error; err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(codes))
	for _, c := range codes {
		m[c] = true
	}
	return m, nil
}

// seedCategoryByKey 按包内 key 取种子分类
func seedCategoryByKey(pkg *SeedPackage, key string) *SeedCategory {
	for i := range pkg.Categories {
		if pkg.Categories[i].Key == key {
			return &pkg.Categories[i]
		}
	}
	return nil
}
