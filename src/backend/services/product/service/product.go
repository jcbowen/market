package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/likeutil"
	"github.com/jcbowen/market/src/backend/common/pageparam"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// ProductService 商品业务服务
type ProductService struct {
	db *mysql.Instance
}

// NewProductService 创建商品业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *ProductService: 商品业务服务实例
func NewProductService(db *mysql.Instance) *ProductService {
	return &ProductService{db: db}
}

// ListProducts 分页查询商品列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.Product: 商品列表
//   - int: 总条数
//   - error: 错误信息
func (s *ProductService) ListProducts(ctx context.Context, req *product.ListProductsReq) ([]*model.Product, int, error) {
	page, pageSize := pageparam.Clamp(int(req.Pagination.GetPage()), int(req.Pagination.GetPageSize()))
	// 服务层兜底上限：HTTP 网关已按 maxPageSize=100 收敛，但 gRPC 可被内部或直连调用
	// 传入超大值导致全表拉取，这里独立设限（口径见 pageparam）

	// 列表口径不取富文本正文（大字段，仅商品详情需要）：避免整页正文进内存
	db := s.db.GetDb().WithContext(ctx).Model(&model.Product{}).Omit("content")
	switch req.StatusFilter {
	case 1: // 出售中：上架且有库存
		db = db.Where("status = 1 AND stock > 0")
	case 2: // 已售罄：上架但无库存
		db = db.Where("status = 1 AND stock <= 0")
	case 3: // 仓库中：下架（无论库存，含定时上架中）
		db = db.Where("status = 0")
	case 4: // 库存紧张：出售中且库存不超过 SKU 安全库存合计（未设安全库存的商品不参与）
		db = db.Where("status = 1 AND stock > 0 AND " + lowStockCondition)
	default:
		if req.Status > 0 {
			db = db.Where("status = ?", req.Status)
		} else {
			db = db.Where("status != ?", 2)
		}
	}
	db = s.applyProductFilters(ctx, db, req.CommunityId, req.CategoryId, req.Keyword)

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var list []*model.Product
	// 排序口径（docs/需求文档/06-接口设计/02-C端接口.md）：
	// sales=销量降序（真实+虚拟）、price_asc/price_desc=价格升/降序（按 SKU 汇总下限价），
	// 缺省按后台 sort 值降序，同值按 id 降序兜底
	switch req.Sort {
	case "sales":
		db = db.Order("sales_count + virtual_sales desc, id desc")
	case "price_asc":
		db = db.Order("min_price asc, id desc")
	case "price_desc":
		db = db.Order("min_price desc, id desc")
	default:
		db = db.Order("sort desc, id desc")
	}
	err := db.Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// lowStockCondition 库存紧张判定条件（相关子查询，供列表 status_filter=4 与状态统计复用同一口径）：
// 商品汇总库存不超过启用 SKU 的安全库存合计；所有 SKU 均未设安全库存（合计被过滤为空组）时不判紧张
const lowStockCondition = "EXISTS (" +
	"SELECT s.product_id FROM product_skus s " +
	"WHERE s.product_id = products.id AND s.status = 1 AND s.safety_stock > 0 " +
	"GROUP BY s.product_id HAVING SUM(s.safety_stock) >= products.stock)"

// isDigits 判断字符串是否全部为数字字符（非空）
//
// 参数：
//   - s: 待判断字符串
//
// 返回值：
//   - bool: 非空且全为数字时为 true
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// applyProductFilters 按社区/分类/关键词构造商品过滤条件（列表与状态统计共用口径）
// 关键字双通道匹配：商品名模糊匹配 OR SKU 条形码精确命中；
// 条码为纯数字时才走子查询，避免无谓的 IN 查询
//
// 参数：
//   - ctx: 上下文
//   - db: 已挂载 Model 的查询构建器
//   - communityId: 社区 ID（0=不过滤）
//   - categoryId: 分类 ID（0=不过滤）
//   - keyword: 关键词（空=不过滤）
//
// 返回值：
//   - *gorm.DB: 追加过滤条件后的查询构建器
func (s *ProductService) applyProductFilters(ctx context.Context, db *gorm.DB, communityId, categoryId int64, keyword string) *gorm.DB {
	if communityId > 0 {
		db = db.Where("community_id = ?", communityId)
	}
	if categoryId > 0 {
		db = db.Where("category_id = ?", categoryId)
	}
	if keyword != "" {
		// 关键词按字面量匹配：不转义时输入一个 % 就把条件变成「匹配全部」，
		// 精确搜索失效且整表扫描（name 上的前导 % 本就用不到索引）
		like := likeutil.Contains(keyword)
		if isDigits(keyword) {
			sub := s.db.GetDb().WithContext(ctx).Model(&model.Sku{}).
				Select("product_id").
				Where("barcode = ? AND status = 1", keyword)
			if communityId > 0 {
				sub = sub.Where("community_id = ?", communityId)
			}
			db = db.Where("name like ? OR id IN (?)", like, sub)
		} else {
			db = db.Where("name like ?", like)
		}
	}
	return db
}

// GetProductStatusStats 商品状态统计（口径与列表筛选一致，供管理端 Tab 计数）
// 单条聚合查询完成四项统计：全部（排除已删除）/ 在售中 / 已售罄 / 仓库中
//
// 参数：
//   - ctx: 上下文
//   - req: 统计请求（community_id/keyword/category_id 与列表筛选条件一致）
//
// 返回值：
//   - *product.GetProductStatusStatsResp: 各状态计数（各状态互斥，合计=total）
//   - error: 错误信息
//
// 使用示例：
//
//	resp, err := svc.GetProductStatusStats(ctx, &product.GetProductStatusStatsReq{CommunityId: 1})
func (s *ProductService) GetProductStatusStats(ctx context.Context, req *product.GetProductStatusStatsReq) (*product.GetProductStatusStatsResp, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.Product{})
	db = s.applyProductFilters(ctx, db, req.CommunityId, req.CategoryId, req.Keyword)

	var row struct {
		Total     int64
		OnSale    int64
		SoldOut   int64
		Warehouse int64
		LowStock  int64
	}
	err := db.Session(&gorm.Session{}).
		Select(`COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN status = 1 AND stock > 0 THEN 1 ELSE 0 END), 0) AS on_sale,
			COALESCE(SUM(CASE WHEN status = 1 AND stock <= 0 THEN 1 ELSE 0 END), 0) AS sold_out,
			COALESCE(SUM(CASE WHEN status = 0 THEN 1 ELSE 0 END), 0) AS warehouse,
			COALESCE(SUM(CASE WHEN status = 1 AND stock > 0 AND `+lowStockCondition+` THEN 1 ELSE 0 END), 0) AS low_stock`).
		Where("status != ?", 2).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	return &product.GetProductStatusStatsResp{
		Total:     row.Total,
		OnSale:    row.OnSale,
		SoldOut:   row.SoldOut,
		Warehouse: row.Warehouse,
		LowStock:  row.LowStock,
	}, nil
}

// BatchUpdateProductStatus 批量上下架（管理端列表多选操作）
// 单条 UPDATE 完成，不触碰已删除商品（status=2）；communityId>0 时仅更新归属该社区的商品
//
// 参数：
//   - ctx: 上下文
//   - ids: 商品 ID 列表（非空）
//   - status: 目标状态（0 下架 / 1 上架）
//   - communityId: 操作者社区语境（0=跳过归属过滤）
//
// 返回值：
//   - error: 参数非法/数据库错误
//
// 使用示例：
//
//	err := svc.BatchUpdateProductStatus(ctx, []int64{1, 2, 3}, 0, 456)
func (s *ProductService) BatchUpdateProductStatus(ctx context.Context, ids []int64, status int32, communityId int64) error {
	if len(ids) == 0 {
		return bizerr.New(codes.InvalidArgument, "请选择要操作的商品")
	}
	if status != 0 && status != 1 {
		return bizerr.New(codes.InvalidArgument, "目标状态非法")
	}
	db := s.db.GetDb().WithContext(ctx).Model(&model.Product{}).
		Where("id IN ? AND status <> ?", ids, 2)
	if communityId > 0 {
		db = db.Where("community_id = ?", communityId)
	}
	// 批量上架/下架同步清除定时预约，与单条 UpdateProductStatus 口径一致
	return db.Updates(map[string]interface{}{
		"status":     status,
		"on_sale_at": nil,
	}).Error
}

// BuildCategoryNames 批量查询商品列表的分类名称
//
// 参数：
//   - ctx: 上下文
//   - products: 商品模型列表
//
// 返回值：
//   - map[int64]string: 分类 ID -> 分类名称
//   - error: 错误信息
func (s *ProductService) BuildCategoryNames(ctx context.Context, products []*model.Product) (map[int64]string, error) {
	categoryNames := make(map[int64]string)
	if len(products) == 0 {
		return categoryNames, nil
	}

	categoryIDs := make([]int64, 0, len(products))
	seenCategory := make(map[int64]struct{})
	for _, p := range products {
		if _, ok := seenCategory[p.CategoryID]; !ok && p.CategoryID > 0 {
			seenCategory[p.CategoryID] = struct{}{}
			categoryIDs = append(categoryIDs, p.CategoryID)
		}
	}
	if len(categoryIDs) == 0 {
		return categoryNames, nil
	}

	var categories []*model.Category
	if err := s.db.GetDb().WithContext(ctx).Where("id IN ?", categoryIDs).Find(&categories).Error; err != nil {
		return nil, err
	}
	for _, c := range categories {
		categoryNames[c.ID] = c.Name
	}
	return categoryNames, nil
}

// BuildProductImages 批量查询商品对应的图片 URL 列表
// 返回结果中的图片按 `sort` 升序、`id` 升序排列。
//
// 参数：
//   - ctx: 上下文
//   - productIDs: 商品 ID 列表
//
// 返回值：
//   - map[int64][]string: 商品 ID -> 图片 URL 列表
//   - error: 错误信息
func (s *ProductService) BuildProductImages(ctx context.Context, productIDs []int64) (map[int64][]string, error) {
	imageMap := make(map[int64][]string)
	if len(productIDs) == 0 {
		return imageMap, nil
	}

	var images []model.ProductImage
	if err := s.db.GetDb().WithContext(ctx).
		Where("product_id IN ?", productIDs).
		Order("sort asc, id asc").
		Find(&images).Error; err != nil {
		return nil, err
	}

	for _, img := range images {
		imageMap[img.ProductID] = append(imageMap[img.ProductID], img.URL)
	}
	return imageMap, nil
}

// ProductStatusOnSale 上架（可售）状态：人工上架与定时上架调度器到点翻转均落该值，
// 是各交易入口「可售」判定的唯一口径（P1-4 收口，与 GetByBarcode 的 status=1 一致）
const ProductStatusOnSale = 1

// ProductStatusDeleted 软删除状态：DeleteProduct 只置状态位、不删行，
// 因此所有按 ID 直查的出口都要显式排除它（见 GetProduct），删除语义才不会只活在列表筛选里
const ProductStatusDeleted = 2

// EnsureProductSellable 校验商品是否可售：仅上架态（status=1）可售；
// 下架(0)/删除(2)/定时上架未到点(0)一律拒绝，文案带商品名便于前端直接展示。
// 供 gRPC 层（GetProduct 的 require_sellable）与服务内批量入口共用，是商品侧「可售」判定的唯一口径；
// 交易入口统一经 EnsureTradeAccess 调用（与 SKU 侧 EnsureSkuSellable 同构）
//
// 参数：
//   - p: 商品模型
//
// 返回值：
//   - error: 不可售时返回 FailedPrecondition 业务错误
func EnsureProductSellable(p *model.Product) error {
	if p.Status != ProductStatusOnSale {
		return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("商品「%s」已下架，暂不可购买", p.Name))
	}
	return nil
}

// GetProduct 根据 ID 查询商品
// 软删除（status=2）默认不可取：删除只是置状态位，按 ID 直查若不过滤就会让已删商品
// 继续对外返回价格、库存与富文本正文（C 端详情与改密式回溯都靠这条口子）
// 参数：
//   - ctx: 上下文
//   - id: 商品 ID
//   - includeDeleted: 是否放行已删除行（仅管理端详情/编辑回显传 true）
//
// 返回值：
//   - *model.Product: 商品模型
//   - error: 不存在或已删除（includeDeleted=false 时）返回 NotFound
//
// 使用示例：
//
//	p, err := svc.GetProduct(ctx, 12, false)
func (s *ProductService) GetProduct(ctx context.Context, id int64, includeDeleted bool) (*model.Product, error) {
	var p model.Product
	db := s.db.GetDb().WithContext(ctx)
	if !includeDeleted {
		db = db.Where("status <> ?", ProductStatusDeleted)
	}
	err := db.First(&p, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, bizerr.New(codes.NotFound, "商品不存在")
	}
	return &p, err
}

// BatchGetProducts 按 ID 批量查询商品（下单/收银按明细取商品名与封面，替代逐行 GetProduct 的 N 次往返）
// 只查主表：列表口径不需要 SKU 明细、规格树与图集，避免 GetProduct 的 4 组附属查询被放大 N 倍。
// 任一 ID 不存在即整体报错（订单明细不允许部分命中）。
// communityID>0 时逐条比对商品归属社区，任一越权即整体拒绝：
// 网关 C 端下单的 community_id 由订单服务原样透传，传了就一定拦得住（PermissionDenied）。
// requireSellable=true 时追加「可售」收口校验：任一商品非上架态（status≠1，含定时上架未到点）
// 即整体报错，下单入口借此与 GetByBarcode 保持同一口径（P1-4）。
// 归属与可售的判定统一走 EnsureTradeAccess，与 SKU 侧同构。
//
// 参数：
//   - ctx: 上下文
//   - ids: 商品 ID 列表（重复 ID 只查一次）
//   - communityID: 成交/操作者社区语境，<=0 跳过归属校验
//   - requireSellable: 是否要求全部商品可售（下单传 true）
//
// 返回值：
//   - []*model.Product: 商品模型列表（按首次出现顺序去重）
//   - error: 参数非法/不存在/越权（PermissionDenied）/不可售（FailedPrecondition）/数据库错误
//
// 使用示例：
//
//	products, err := svc.BatchGetProducts(ctx, []int64{1, 2}, 456, true)
func (s *ProductService) BatchGetProducts(ctx context.Context, ids []int64, communityID int64, requireSellable bool) ([]*model.Product, error) {
	ordered := dedupIDs(ids)
	if len(ordered) == 0 {
		return nil, bizerr.New(codes.InvalidArgument, "请选择要查询的商品")
	}
	var rows []*model.Product
	if err := s.db.GetDb().WithContext(ctx).Find(&rows, ordered).Error; err != nil {
		return nil, err
	}
	byID := make(map[int64]*model.Product, len(rows))
	for _, p := range rows {
		if err := EnsureTradeAccess(communityID, p, nil, requireSellable); err != nil {
			return nil, err
		}
		byID[p.ID] = p
	}
	list := make([]*model.Product, 0, len(ordered))
	for _, id := range ordered {
		p, ok := byID[id]
		if !ok {
			return nil, bizerr.New(codes.NotFound, fmt.Sprintf("商品 %d 不存在", id))
		}
		list = append(list, p)
	}
	return list, nil
}

// BatchGetProductsTolerant 逐项判定口径的商品批量查询：缺失与越权项只剔除，不整体报错
// 取舍与 BatchGetSkusTolerant 一致：交易收口用 BatchGetProducts（任一缺失/越权/不可售整体拒绝），
// 「部分可用仍要继续」的调用方（C 端再来一单）用本口径。
// 上架状态原样返回，不在此判可售——调用方据此区分「已下架」与「已删除」两种提示；
// 社区归属在本层筛（communityID>0 时越权项剔除），与 BatchGetSkusTolerant 成对传同一社区
//
// 参数：
//   - ctx: 上下文
//   - ids: 商品 ID 集合（重复、0 与负数自动忽略）
//   - communityID: 调用方社区语境，<=0 时不筛归属
//
// 返回值：
//   - []*model.Product: 命中且归属合法的商品（按入参首次出现顺序）
//   - error: 仅数据库错误
//
// 使用示例：
//
//	products, err := svc.BatchGetProductsTolerant(ctx, []int64{1, 2}, 456)
func (s *ProductService) BatchGetProductsTolerant(ctx context.Context, ids []int64, communityID int64) ([]*model.Product, error) {
	byID, err := s.FindProductsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	list := make([]*model.Product, 0, len(byID))
	for _, id := range dedupIDs(ids) {
		p, ok := byID[id]
		if !ok || (communityID > 0 && p.CommunityID != communityID) {
			continue
		}
		list = append(list, p)
	}
	return list, nil
}

// FindProductsByIDs 按 ID 集合批量取商品，返回 ID->商品 映射；缺失的 ID 直接不出现在结果里
// 口径与 SkuService.FindSkusByIDs 一致：列表回填用，容忍个别商品被删、不做越权校验，
// ID 须来自服务端已过滤的数据。
//
// 参数：
//   - ctx: 上下文
//   - ids: 商品 ID 集合（重复、0 与负数自动忽略）
//
// 返回值：
//   - map[int64]*model.Product: 商品 ID -> 商品模型
//   - error: 数据库错误
//
// 使用示例：
//
//	productByID, err := svc.FindProductsByIDs(ctx, []int64{1, 2})
func (s *ProductService) FindProductsByIDs(ctx context.Context, ids []int64) (map[int64]*model.Product, error) {
	positive := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			positive = append(positive, id)
		}
	}
	if len(positive) == 0 {
		return map[int64]*model.Product{}, nil
	}
	var rows []*model.Product
	if err := s.db.GetDb().WithContext(ctx).Find(&rows, dedupIDs(positive)).Error; err != nil {
		return nil, err
	}
	byID := make(map[int64]*model.Product, len(rows))
	for _, p := range rows {
		byID[p.ID] = p
	}
	return byID, nil
}

// 商品档案写入口径常量（与 products 列定义、后台表单 maxlength 同源）
const (
	// productNameMaxRunes products.name 为 varchar(100)，按字符数判定（与 UpdateProductName 同口径）
	productNameMaxRunes = 100
	// productContentMaxBytes 富文本详情上限 1MB：content 列是 mediumtext，更大也存得下，
	// 但不设限会让超长正文（含富文本粘贴带进来的 data URI 图片）撞到 gRPC 默认 4MB 消息上限，
	// 用户看到的是「系统错误」而不是「内容过大」
	productContentMaxBytes = 1 << 20
)

// normalizeProductName 归一化并校验商品名称（创建与更新共用口径）
// required=true（创建）时空名直接拒绝；required=false（更新）时空串表示「本次不改动」，
// 但全空白名同样拒绝——否则更新接口会写入一个空名称
//
// 参数：
//   - name: 原始名称
//   - required: 是否必填（创建口径）
//
// 返回值：
//   - string: 去空白后的名称
//   - error: 空名或超过 productNameMaxRunes 字时返回 InvalidArgument
//
// 使用示例：
//
//	name, err := normalizeProductName(req.Name, true)
func normalizeProductName(name string, required bool) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		if required {
			return "", bizerr.New(codes.InvalidArgument, "商品名称不能为空")
		}
		return "", nil
	}
	if utf8.RuneCountInString(trimmed) > productNameMaxRunes {
		return "", bizerr.New(codes.InvalidArgument, fmt.Sprintf("商品名称不能超过 %d 字", productNameMaxRunes))
	}
	return trimmed, nil
}

// validateProductContent 校验富文本详情体积（在消毒前按原始提交量判定）
// 参数：
//   - content: 原始富文本正文
//
// 返回值：
//   - error: 超过 productContentMaxBytes 时返回 InvalidArgument
func validateProductContent(content string) error {
	if len(content) > productContentMaxBytes {
		return bizerr.New(codes.InvalidArgument, fmt.Sprintf("商品详情内容过大（上限 %d MB），请精简正文或改用图片外链", productContentMaxBytes>>20))
	}
	return nil
}

// CreateProduct 创建商品
// 创建商品主记录的同时，将请求中的图片 URL 按顺序写入 `product_images` 表，
// 并继续保留 `products.images` JSON 字段作为过渡兼容。
// 规格树与 SKU 组合由 saveSpecsAndSkus 在同一事务内保存：
// 单规格维护唯一默认 SKU，多规格按规格树全量落库并回写汇总字段。
//
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.Product: 商品模型
//   - error: 错误信息
func (s *ProductService) CreateProduct(ctx context.Context, req *product.CreateProductReq) (*model.Product, error) {
	if len(req.SkuItems) == 0 {
		return nil, bizerr.New(codes.InvalidArgument, "请填写商品价格与库存")
	}
	name, err := normalizeProductName(req.Name, true)
	if err != nil {
		return nil, err
	}
	if err := validateProductContent(req.Content); err != nil {
		return nil, err
	}
	if err := ensureCategoryInCommunity(ctx, s.db.GetDb(), req.CategoryId, req.CommunityId); err != nil {
		return nil, err
	}

	imagesJSON, _ := json.Marshal(req.Images)
	p := &model.Product{
		CommunityID:  req.CommunityId,
		CategoryID:   req.CategoryId,
		Name:         name,
		Description:  req.Description,
		Content:      SanitizeProductContentHTML(req.Content),
		CoverImage:   req.CoverImage,
		Images:       string(imagesJSON),
		Unit:         req.Unit,
		IsMultiSpec:  req.IsMultiSpec,
		MinBuy:       req.MinBuy,
		ShowSales:    req.ShowSales,
		VirtualSales: req.VirtualSales,
		Status:       1,
		Sort:         req.Sort,
	}
	// 定时上架：非空且为未来时间时保存为下架+预约，到点由调度器自动上架（默认立即上架）
	if req.OnSaleAt != "" {
		if err := s.validateOnSaleAt(ctx, req.OnSaleAt); err != nil {
			return nil, err
		}
			p.Status = 0
			norm := dbTimeString(req.OnSaleAt) // 同 UpdateProduct：RFC3339 入参归一化
			p.OnSaleAt = &norm
	}

	// GORM 零值坑：Status 模型 tag 带 default:1，Create 遇零值 0 会跳过该列（落 DB 默认 1）
	// 并把默认值回填进结构体（p.Status 变回 1）。须在 Create 前捕获期望值，
	// Create 后显式回写，否则定时上架商品被直接创建为上架（同 show_sales 当年之坑）
	wantScheduled := p.Status == 0
	err = s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(p).Error; err != nil {
			return err
		}
		if wantScheduled {
			if err := tx.Model(&model.Product{}).Where("id = ?", p.ID).Update("status", int32(0)).Error; err != nil {
				return err
			}
		}
		if len(req.Images) > 0 {
			if err := s.saveProductImages(tx, p.ID, req.Images); err != nil {
				return err
			}
		}
		return s.saveSpecsAndSkus(tx, p, req.Specs, req.SkuItems)
	})
	if err != nil {
		return nil, err
	}
	if wantScheduled {
		// 回写后的真实状态（Create 时被 GORM 回填为 1，此处校正，保证返回值与库内一致）
		p.Status = 0
	}
	return p, nil
}

// productEditableColumns UpdateProduct 归属列白名单（字段级更新，见其事务内注释）
// 刻意不含派生/并发列：min_price、max_price、min_original_price、stock（SKU 聚合回写）、
// sales_count（支付与退款调整）、community_id、id、created_at
// 含 updated_at：兼作乐观锁版本号，须显式赋规范化时间（见 UpdateProduct 内赋值处）——
// 若省略让 gorm 自动 touch，结构体里读回的 RFC3339 字面量（parseTime DSN）会被原样写库，
// MySQL 8.0.19+ 可解析、5.7 直接 1292（sku/category 等整行 Save 同类问题的字段级更新版）
var productEditableColumns = []string{
	"category_id", "name", "description", "content", "cover_image", "images",
	"unit", "is_multi_spec", "show_sales", "virtual_sales", "min_buy", "status", "sort", "on_sale_at",
	"updated_at",
}

// UpdateProduct 更新商品
// 请求 images 非空时，删除该商品在 `product_images` 表中的全部旧记录并按新顺序重新插入，
// 同时同步更新 `products.images` JSON 字段；images 为空数组时保留现有图片不变更。
// 规格树与 SKU 组合由 saveSpecsAndSkus 在同一事务内全量保存（含 diff 软删除）。
// 主表按 productEditableColumns 白名单做字段级更新（不整行 Save），派生列与销量列由各自的写入方负责。
//
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *model.Product: 商品模型
//   - error: 错误信息
func (s *ProductService) UpdateProduct(ctx context.Context, req *product.UpdateProductReq) (*model.Product, error) {
	p, err := s.GetProduct(ctx, req.Id, false)
	if err != nil {
		return nil, err
	}
	if err := EnsureScope(req.CommunityId, p.CommunityID, "商品"); err != nil {
		return nil, err
	}
	// 乐观锁：商品为整份覆盖式保存，两人同时编辑后提交者会静默覆盖先提交者的改动；
	// 前端携带读取时的 updated_at，与库中不一致即返回 10007 提示重拉（与支付/附件配置同口径）
	if req.UpdatedAt != "" && req.UpdatedAt != p.UpdatedAt {
		return nil, bizerr.New(codes.Aborted, "商品已被其他操作更新，请刷新页面获取最新数据后重新提交")
	}
	name, err := normalizeProductName(req.Name, false)
	if err != nil {
		return nil, err
	}
	if err := validateProductContent(req.Content); err != nil {
		return nil, err
	}

	if req.CategoryId > 0 {
		if err := ensureCategoryInCommunity(ctx, s.db.GetDb(), req.CategoryId, p.CommunityID); err != nil {
			return nil, err
		}
		p.CategoryID = req.CategoryId
	}
	// description/cover_image/unit/min_buy/status 沿用「空值=不改动」，与上面 content/sort 的三态口径刻意不同：
	//   - description：后台表单恒提交空串（简介不在商品编辑页维护），改成无条件覆盖等于每次保存都清空简介；
	//   - unit：必填且非空，没有「清空单位」的入口；
	//   - min_buy：表单最小值 1，0 只表示「未填」，归 0 无入口也无意义（起购数量下限即 1）；
	//   - status：上下架走专用接口 PUT /admin/products/:id/status；编辑接口只在「预约中切立即上架」
	//     这一种场景显式提交 status=1，其余场景不传，避免编辑下架商品时被顺手上架。
	// 需要真正支持「清空」的字段（如封面图）应先给前端加清空语义，再按 sort 的方式改指针三态。
	if name != "" {
		p.Name = name
	}
	if req.Description != "" {
		p.Description = req.Description
	}
	if req.CoverImage != "" {
		p.CoverImage = req.CoverImage
	}
	if req.Unit != "" {
		p.Unit = req.Unit
	}
	if req.Sort != nil {
		// 三态：不传=不改动，传 0 就是置 0（旧写法 sort>0 才赋值，导致排序永远回不到 0）
		p.Sort = *req.Sort
	}
	if req.Status != 0 {
		p.Status = req.Status
	}
	if req.MinBuy > 0 {
		p.MinBuy = req.MinBuy
	}
	p.IsMultiSpec = req.IsMultiSpec
	// 销量展示配置：admin 保存时全量提交，直接赋值
	p.ShowSales = req.ShowSales
	p.VirtualSales = req.VirtualSales
	// 富文本详情：admin 全量提交，消毒后直接覆盖（允许清空）
	p.Content = SanitizeProductContentHTML(req.Content)

	// 定时上架（编辑页「上架设置」显式提交时才变更，三态见 proto UpdateProductReq.on_sale_at）：
	// 不传 → 维持原预约不变（旧写法把「不传」当空串，任何一次编辑都会顺手清掉预约）；
	// 非空且为未来时间 → 下架+预约（支持设置/改期）；显式传空串 → 仅清除预约，
	// 上下架终态由 status 提交值决定（预约中商品切「立即上架」由前端显式传 status=1）
	if req.OnSaleAt != nil {
		if at := *req.OnSaleAt; at != "" {
			if err := s.validateOnSaleAt(ctx, at); err != nil {
				return nil, err
			}
			p.Status = 0
			norm := dbTimeString(at) // RFC3339 入参归一化为「Y-m-d H:i:s」（5.7 不收带时区偏移字面量）
			p.OnSaleAt = &norm
		} else {
			p.OnSaleAt = nil
		}
	}

	// images 为空数组时视为"不变更"，保留商品现有图片，避免编辑接口意外清空图片
	if len(req.Images) > 0 {
		imagesJSON, _ := json.Marshal(req.Images)
		p.Images = string(imagesJSON)
	}

	// updated_at 兼作乐观锁版本号：显式赋「Y-m-d H:i:s」规范化当前时间（探针实证，
	// gorm 自动 touch 会把读回的 RFC3339 字面量原样写库，5.7 报 1292）；
	// 事务外赋值，成功后随返回值下发，前端下次保存携带即最新版本
	p.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	// on_sale_at 同理：不编辑预约时保留库中原值，但读回形态是 RFC3339（parseTime DSN），
	// 在白名单 Select 里会被原样写库 1292（MySQL 5.7）；统一归一化后再落库
	if p.OnSaleAt != nil {
		norm := dbTimeString(*p.OnSaleAt)
		p.OnSaleAt = &norm
	}

	err = s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 字段级更新，不整行 Save：p 是事务外读到的快照，整行回写会把读取时点的
		// sales_count（支付/退款并发累加）、min_price/max_price/stock/min_original_price
		// （扣减、回补、入库、盘点并发回写）一并覆盖回旧值，形成丢更新。
		// 只写本请求归属的列，派生列交给 syncProductAggregates（本事务末尾）与库存/销量各自的写入方
		if err := tx.Model(&model.Product{}).Where("id = ?", p.ID).Select(productEditableColumns).Updates(p).Error; err != nil {
			return err
		}
		if len(req.Images) > 0 {
			if err := tx.Where("product_id = ?", req.Id).Delete(&model.ProductImage{}).Error; err != nil {
				return err
			}
			if err := s.saveProductImages(tx, req.Id, req.Images); err != nil {
				return err
			}
		}
		return s.saveSpecsAndSkus(tx, p, req.Specs, req.SkuItems)
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// DeleteProduct 删除商品（软删除：状态置为 2，并连带禁用其全部 SKU）
// SKU 必须与商品同事务禁用：条码「存活」的唯一口径是 SKU status=1
// （应用层查重 spec.go ensureSkuBarcodeAvailable 排除已删商品的 SKU，
// 库级函数索引 main.go ensureSkuBarcodeUniqueIndex 也只对 status=1 的行生效），
// 只软删商品不禁用 SKU 会让商家复用旧条码时插入即撞 1062、保存被卡死
// 参数：
//   - ctx: 上下文
//   - id: 商品 ID
//   - communityId: 操作者社区语境（>0 时仅删除归属该社区的商品，见 scope.go）
//
// 返回值：
//   - error: 错误信息
func (s *ProductService) DeleteProduct(ctx context.Context, id, communityId int64) error {
	db := s.db.GetDb().WithContext(ctx)
	if err := ensureProductInScope(db, id, communityId); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Product{}).Where("id = ?", id).Update("status", 2).Error; err != nil {
			return err
		}
		return tx.Model(&model.Sku{}).
			Where("product_id = ? AND status = 1", id).
			Update("status", 0).Error
	})
}

// UpdateProductStatus 更新商品状态
// 参数：
//   - ctx: 上下文
//   - id: 商品 ID
//   - status: 状态
//   - communityId: 操作者社区语境（>0 时仅更新归属该社区的商品，见 scope.go）
//
// 返回值：
//   - error: 错误信息
func (s *ProductService) UpdateProductStatus(ctx context.Context, id int64, status int32, communityId int64) error {
	if status != 0 && status != 1 {
		return bizerr.New(codes.InvalidArgument, "目标状态非法")
	}
	db := s.db.GetDb().WithContext(ctx)
	if err := ensureProductInScope(db, id, communityId); err != nil {
		return err
	}
	// 手动上下架同时清除定时预约：手动操作覆盖定时（上架即售、下架即撤预约）
	return db.Model(&model.Product{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":     status,
		"on_sale_at": nil,
	}).Error
}

// ensureProductInScope 确认商品存在且落在操作者社区语境内（更新前先计数，
// 避免直接按条件 UPDATE 后无法区分「不存在/跨社区」与「值未变化导致 0 行受影响」）
//
// 参数：
//   - db: 已绑定上下文的 GORM 实例
//   - id: 商品 ID
//   - communityId: 操作者社区语境（0=跳过归属过滤）
//
// 返回值：
//   - error: 不存在或跨社区时返回 NotFound
func ensureProductInScope(db *gorm.DB, id, communityId int64) error {
	query := db.Model(&model.Product{}).Where("id = ? AND status <> ?", id, 2)
	if communityId > 0 {
		query = query.Where("community_id = ?", communityId)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return bizerr.New(codes.NotFound, "商品不存在或无权操作")
	}
	return nil
}

// UpdateProductName 更新商品名称（列表页行内改名轻量接口）
// 仅更新 name 字段，避免全量提交覆盖规格/SKU；communityId>0 时校验商品归属社区
//
// 参数：
//   - ctx: 上下文
//   - id: 商品 ID
//   - name: 新名称（去空白后非空）
//   - communityId: 操作者社区语境（0=跳过归属校验）
//
// 返回值：
//   - error: 名称非法/商品不存在/跨社区时返回业务错误
//
// 使用示例：
//
//	err := svc.UpdateProductName(ctx, 123, "新名称", 456)
func (s *ProductService) UpdateProductName(ctx context.Context, id int64, name string, communityId int64) error {
	// 名称口径与创建/更新商品同一函数收口（非空 + ≤100 字），避免三处各写一份
	normalized, err := normalizeProductName(name, true)
	if err != nil {
		return err
	}

	db := s.db.GetDb().WithContext(ctx)
	if err := ensureProductInScope(db, id, communityId); err != nil {
		return err
	}
	return db.Model(&model.Product{}).Where("id = ?", id).Update("name", normalized).Error
}

// AdjustSalesCount 批量调整销量（净成交口径）
// SKU 行与所属商品汇总行同事务同步累加；delta 正=累加（支付成功），负=回退（取消/退款）。
// 防御：结果向下钳制为 0，避免异常回退产生负销量；SKU 不存在时忽略（正常下单明细必存在）
//
// 参数：
//   - ctx: 上下文
//   - items: SKU 维度调整项（同 SKU 多条自动合并）
//
// 返回值：
//   - error: 错误信息
//
// 使用示例：
//
//	err := svc.AdjustSalesCount(ctx, []*product.AdjustSalesCountItem{{SkuId: 1, Delta: 2}})
func (s *ProductService) AdjustSalesCount(ctx context.Context, items []*product.AdjustSalesCountItem) error {
	if len(items) == 0 {
		return nil
	}
	// 按 SKU 合并 delta（同订单同 SKU 可能多行）
	merged := make(map[int64]int32, len(items))
	skuIDs := make([]int64, 0, len(items))
	for _, it := range items {
		if it.GetSkuId() <= 0 || it.GetDelta() == 0 {
			continue
		}
		if _, ok := merged[it.GetSkuId()]; !ok {
			skuIDs = append(skuIDs, it.GetSkuId())
		}
		merged[it.GetSkuId()] += it.GetDelta()
	}
	if len(merged) == 0 {
		return nil
	}

	var skus []model.Sku
	if err := s.db.GetDb().WithContext(ctx).Where("id IN ?", skuIDs).Find(&skus).Error; err != nil {
		return err
	}
	if len(skus) == 0 {
		return nil
	}

	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// SKU 行累加（GREATEST 钳制非负）
		for _, sku := range skus {
			if err := tx.Model(&model.Sku{}).Where("id = ?", sku.ID).
				Update("sales_count", gorm.Expr("GREATEST(sales_count + ?, 0)", merged[sku.ID])).Error; err != nil {
				return err
			}
		}
		// 商品汇总行按 product_id 聚合累加
		productDeltas := make(map[int64]int32, len(skus))
		for _, sku := range skus {
			productDeltas[sku.ProductID] += merged[sku.ID]
		}
		for pid, delta := range productDeltas {
			if err := tx.Model(&model.Product{}).Where("id = ?", pid).
				Update("sales_count", gorm.Expr("GREATEST(sales_count + ?, 0)", delta)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListSkusByProductID 查询商品下的 SKU 列表
// 参数：
//   - ctx: 上下文
//   - productID: 商品 ID
//
// 返回值：
//   - []*model.Sku: SKU 列表
//   - error: 错误信息
func (s *ProductService) ListSkusByProductID(ctx context.Context, productID int64) ([]*model.Sku, error) {
	var list []*model.Sku
	err := s.db.GetDb().WithContext(ctx).
		Where("product_id = ?", productID).
		Where("status = ?", 1).
		Order("id asc").
		Find(&list).Error
	return list, err
}

// ListProductImages 查询指定商品的图片记录
// 返回结果按 `sort` 升序、`id` 升序排列。
//
// 参数：
//   - ctx: 上下文
//   - productID: 商品 ID
//
// 返回值：
//   - []model.ProductImage: 商品图片记录列表
//   - error: 错误信息
func (s *ProductService) ListProductImages(ctx context.Context, productID int64) ([]model.ProductImage, error) {
	var list []model.ProductImage
	err := s.db.GetDb().WithContext(ctx).
		Where("product_id = ?", productID).
		Order("sort asc, id asc").
		Find(&list).Error
	return list, err
}

// saveProductImages 批量保存商品图片记录
// 图片按传入顺序设置 `sort`，从 0 开始递增。
//
// 参数：
//   - tx: GORM 事务对象
//   - productID: 商品 ID
//   - images: 图片 URL 列表
//
// 返回值：
//   - error: 错误信息
func (s *ProductService) saveProductImages(tx *gorm.DB, productID int64, images []string) error {
	// 批量插入时基类 BeforeCreate 钩子（SetFieldIfExist）不作用于切片，需显式填充时间，
	// 否则 created_at 以空串写入 strict 模式的 datetime 列会报 Error 1292
	now := time.Now().Format("2006-01-02 15:04:05")
	records := make([]model.ProductImage, 0, len(images))
	for i, url := range images {
		records = append(records, model.ProductImage{
			ProductID: productID,
			URL:       url,
			Sort:      int32(i),
			CreatedAt: now,
		})
	}
	return tx.Create(&records).Error
}

// validateOnSaleAt 解析并校验定时上架时间
// 时钟源统一到 MySQL NOW()（P1-11）：到点判定在调度器里就是按 MySQL NOW() 执行的
// （scheduler.go 的 UPDATE ... on_sale_at <= NOW()），保存校验若用应用服务器时钟，
// 双机时钟漂移会出现「保存即报过期」或「到点不触发」——三套时钟只保留 DB 一套权威源
// （前端浏览器时钟仅用于选择器展示，不参与任何判定）
//
// 参数：
//   - ctx: 上下文
//   - raw: 前端提交的时间字符串（"2006-01-02 15:04:05"，空串由调用方短路处理）
//
// 返回值：
//   - error: 格式非法或非未来时间时返回参数错误；DB 时钟读取失败返回内部错误
//
// 使用示例：
//
//	if err := s.validateOnSaleAt(ctx, "2026-11-01 10:00:00"); err != nil { ... }
func (s *ProductService) validateOnSaleAt(ctx context.Context, raw string) error {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.Local)
	if err != nil {
		return bizerr.New(codes.InvalidArgument, "定时上架时间格式错误")
	}
	// 取 DB 时钟：DATE_FORMAT 固定输出格式，规避驱动 parseTime/loc 配置差异对解析的影响
	var dbNowStr string
	if err := s.db.GetDb().WithContext(ctx).Raw("SELECT DATE_FORMAT(NOW(), '%Y-%m-%d %H:%i:%s')").Scan(&dbNowStr).Error; err != nil {
		return bizerr.New(codes.Internal, "时间校验失败，请稍后重试")
	}
	dbNow, err := time.ParseInLocation("2006-01-02 15:04:05", dbNowStr, time.Local)
	if err != nil {
		return bizerr.New(codes.Internal, "时间校验失败，请稍后重试")
	}
	if !t.After(dbNow) {
		return bizerr.New(codes.InvalidArgument, "定时上架时间需晚于当前时间")
	}
	return nil
}

// WarnIfDBClockZoneMismatch 启动期体检：MySQL 服务器墙钟是否与应用进程不在同一时区口径
// 定时上架整条链路以 MySQL NOW() 为权威时钟（见 validateOnSaleAt），而 NOW() 返回的是
// 服务器本地墙钟字符串：操作员在后台填「2026-11-01 10:00」时想的是北京时间，
// 若外部托管库的 time_zone 是 UTC（托管服务默认值），这串值就被当成 UTC 墙钟比较，
// 到点自动上架会整整晚 8 小时——且保存校验与调度器口径一致，全程无人报错、静默失效。
// 本体检只做一次墙钟差比对并告警，不改任何行为：偏差超过 5 分钟即打 WARN，
// 提示运维把数据库时区设为 +08:00（或让应用容器同口径）。
//
// 参数：
//   - db: 商品服务的 MySQL 实例
//
// 返回值：
//   - error: 读取 DB 时钟失败（调用方只记日志，不阻断启动）
//
// 使用示例：
//
//	if err := service.WarnIfDBClockZoneMismatch(db.GetDb()); err != nil {
//		log.Printf("[WARN] 数据库时钟体检失败: %v", err)
//	}
func WarnIfDBClockZoneMismatch(db *gorm.DB) error {
	// DATE_FORMAT 固定输出格式，规避驱动 parseTime/loc 配置差异对解析的影响
	var dbNowStr string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.WithContext(ctx).Raw("SELECT DATE_FORMAT(NOW(), '%Y-%m-%d %H:%i:%s')").Scan(&dbNowStr).Error; err != nil {
		return err
	}
	dbNow, err := time.ParseInLocation("2006-01-02 15:04:05", dbNowStr, time.Local)
	if err != nil {
		return fmt.Errorf("DB 时间解析失败 %q: %w", dbNowStr, err)
	}
	if skew := time.Since(dbNow); skew > 5*time.Minute || skew < -5*time.Minute {
		log.Printf("[WARN] MySQL 墙钟与应用本地时钟相差 %v（DB NOW()=%s）。"+
			"定时上架以 MySQL NOW() 为准，外部库请把 time_zone 设为 '+08:00'（或让应用容器与库同口径），"+
			"否则到点自动上架会按该偏差整体偏移", skew, dbNowStr)
	}
	return nil
}

// ToProductInfo 将商品模型转换为 proto 商品信息
// 价格/库存取自商品表汇总字段（由规格保存流程回写维护）
// 不含 Content（富文本正文）：正文只该出现在商品详情，列表/搜索/条码/批量取数
// 一律不带——单条正文可达数 MB（清洗时每张图还会追加内联样式），随列表下发会
// 成倍放大 gRPC 报文并撞爆 10MB 上限；需要正文的出口（GetProduct）显式回填
//
// 参数：
//   - p: 商品模型
//   - categoryName: 分类名称
//   - images: 商品图片 URL 列表
//
// 返回值：
//   - *product.ProductInfo: proto 商品信息
func ToProductInfo(p *model.Product, categoryName string, images []string) *product.ProductInfo {
	if p == nil {
		return nil
	}
	var onSaleAt string
	if p.OnSaleAt != nil {
		onSaleAt = *p.OnSaleAt
	}
	return &product.ProductInfo{
		Id:            p.ID,
		CommunityId:   p.CommunityID,
		CategoryId:    p.CategoryID,
		Name:          p.Name,
		Description:   p.Description,
		CoverImage:    p.CoverImage,
		Images:        images,
		Unit:          p.Unit,
		IsMultiSpec:   p.IsMultiSpec,
		MinBuy:        p.MinBuy,
		Status:        p.Status,
		Sort:          p.Sort,
		SalesCount:    p.SalesCount,
		Price:         p.MinPrice,
		Stock:         p.Stock,
		MinPrice:      p.MinPrice,
		MaxPrice:      p.MaxPrice,
		OriginalPrice: p.MinOriginalPrice,
		ShowSales:     p.ShowSales,
		VirtualSales:  p.VirtualSales,
		OnSaleAt:      onSaleAt,
		CategoryName:  categoryName,
		// 更新时间兼作乐观锁版本号：管理端编辑保存时回传，服务端比对不一致即拒绝（10007）
		UpdatedAt: p.UpdatedAt,
	}
}
