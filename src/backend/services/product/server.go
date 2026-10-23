package main

import (
	stdctx "context"
	"errors"
	"log"
	"strings"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
	"github.com/jcbowen/market/src/backend/services/product/service"
)

// server 实现 product.ProductServiceServer 接口
type server struct {
	product.UnimplementedProductServiceServer
	categoryService   *service.CategoryService
	productService    *service.ProductService
	skuService        *service.SkuService
	pricingService    *service.PricingService
	stockService      *service.StockService
	stockCheckService *service.StockCheckService
	stockBillService  *service.StockBillService
	supplierService   *service.SupplierService
	purchaseService   *service.PurchaseService
	reportService     *service.ReportService
	marketingService  *service.MarketingService
	bannerService     *service.BannerService
	attachmentService *service.AttachmentService
}

// newServer 创建商品服务 gRPC 实现
// 参数：
//   - db: MySQL 实例
//   - cfg: 全局配置
//
// 返回值：
//   - *server: gRPC 服务实现
func newServer(db *mysql.Instance, cfg *config.Config) *server {
	attachmentService := service.NewAttachmentService(db)
	if err := attachmentService.EnsureDefaultAttachmentConfig(); err != nil {
		log.Printf("初始化默认附件配置失败: %v", err)
	}
	return &server{
		categoryService:   service.NewCategoryService(db),
		productService:    service.NewProductService(db),
		skuService:        service.NewSkuService(db),
		pricingService:    service.NewPricingService(db),
		stockService:      service.NewStockService(db, cfg.Business.ExpireWarningDays),
		stockCheckService: service.NewStockCheckService(db),
		stockBillService:  service.NewStockBillService(db),
		supplierService:   service.NewSupplierService(db),
		purchaseService:   service.NewPurchaseService(db),
		reportService:     service.NewReportService(db),
		marketingService:  service.NewMarketingService(db),
		bannerService:     service.NewBannerService(db),
		attachmentService: attachmentService,
	}
}

// readError 读接口错误归一：仅「记录不存在」透出 NotFound，其余一律 Internal
// 此前的写法是把任何 error（含连接超时、SQL 报错等瞬时故障）都包成 NotFound，
// 叠加下游（购物车 invalidReasonOf / AddItem）对 NotFound 的原样透出与「按失效处理」，
// 商品库抖机会被显示成「商品已失效」，既误导用户也掩盖故障。
// 服务层的 bizerr（已带状态码与文案）继续走 bizerr.ToStatus，语义不变。
//
// 参数：
//   - err: 服务层返回的错误
//   - name: 资源名称，用于原生 gorm.ErrRecordNotFound 的文案（如「商品」「SKU」）
//
// 返回值：
//   - error: gRPC 状态错误（NotFound / 业务码 / Internal）
//
// 使用示例：
//
//	p, err := s.productService.GetProduct(ctx, req.Id)
//	if err != nil {
//		return nil, readError(err, "商品")
//	}
func readError(err error, name string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.NotFound, name+"不存在")
	}
	return bizerr.ToStatus(err)
}

// ListCategories 获取分类列表（includeDisabled=true 时含禁用分类，管理端分类管理页使用）
func (s *server) ListCategories(ctx stdctx.Context, req *product.ListCategoriesReq) (*product.ListCategoriesResp, error) {
	list, err := s.categoryService.ListCategories(ctx, req.CommunityId, req.GetIncludeDisabled())
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &product.ListCategoriesResp{List: service.BuildCategoryTree(list)}, nil
}

// BatchSortCategories 分类批量排序（管理端拖拽后整表提交）
func (s *server) BatchSortCategories(ctx stdctx.Context, req *product.BatchSortCategoriesReq) (*common.CommonResponse, error) {
	if err := s.categoryService.BatchSortCategories(ctx, req.GetItems(), req.GetCommunityId()); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// GetCategory 获取分类详情
func (s *server) GetCategory(ctx stdctx.Context, req *product.GetCategoryReq) (*product.CategoryInfo, error) {
	c, err := s.categoryService.GetCategory(ctx, req.Id, req.CommunityId)
	if err != nil {
		return nil, readError(err, "分类")
	}
	return service.ToCategoryInfo(c), nil
}

// CreateCategory 创建分类
func (s *server) CreateCategory(ctx stdctx.Context, req *product.CreateCategoryReq) (*product.CategoryInfo, error) {
	c, err := s.categoryService.CreateCategory(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToCategoryInfo(c), nil
}

// UpdateCategory 更新分类
func (s *server) UpdateCategory(ctx stdctx.Context, req *product.UpdateCategoryReq) (*product.CategoryInfo, error) {
	c, err := s.categoryService.UpdateCategory(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToCategoryInfo(c), nil
}

// DeleteCategory 删除分类
func (s *server) DeleteCategory(ctx stdctx.Context, req *product.DeleteCategoryReq) (*common.CommonResponse, error) {
	if err := s.categoryService.DeleteCategory(ctx, req.Id, req.CommunityId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListProducts 获取商品列表
func (s *server) ListProducts(ctx stdctx.Context, req *product.ListProductsReq) (*product.ListProductsResp, error) {
	list, total, err := s.productService.ListProducts(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	categoryNames, err := s.productService.BuildCategoryNames(ctx, list)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	productIDs := make([]int64, 0, len(list))
	for _, p := range list {
		productIDs = append(productIDs, p.ID)
	}
	imageMap, err := s.productService.BuildProductImages(ctx, productIDs)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	resp := &product.ListProductsResp{
		List: make([]*product.ProductInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, p := range list {
		info := service.ToProductInfo(p, categoryNames[p.CategoryID], imageMap[p.ID])
		// 出库统一拼接完整访问 URL（库存相对路径）
		info.CoverImage = s.attachmentService.ToMediaURL(ctx, p.CoverImage)
		info.Images = s.attachmentService.ToMediaURLs(ctx, imageMap[p.ID])
		resp.List = append(resp.List, info)
	}
	return resp, nil
}

// GetProduct 获取商品详情
// 返回商品基础信息、SKU 列表与规格树（单规格时规格树为空）。
// require_sellable=true 时非上架态商品直接报错（加购等交易入口的可售收口，P1-4）；
// 公开详情与管理端详情不传，仓库中商品仍可查看
func (s *server) GetProduct(ctx stdctx.Context, req *product.GetProductReq) (*product.ProductDetail, error) {
	p, err := s.productService.GetProduct(ctx, req.Id)
	if err != nil {
		return nil, readError(err, "商品")
	}
	// 可售校验收口（P1-4）：与 BatchGetProducts 的 require_sellable 同口径，
	// 归属 + 可售由 EnsureTradeAccess 一次判完，交易入口（加购）只声明意图
	if err := service.EnsureTradeAccess(req.GetCommunityId(), p, nil, req.GetRequireSellable()); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	skus, err := s.productService.ListSkusByProductID(ctx, req.Id)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	specs, err := s.productService.ListSpecs(ctx, req.Id)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	images, err := s.productService.ListProductImages(ctx, req.Id)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	imageURLs := make([]string, 0, len(images))
	for _, img := range images {
		imageURLs = append(imageURLs, img.URL)
	}

	info := service.ToProductInfo(p, "", imageURLs)
	// 出库统一拼接完整访问 URL（库存相对路径）
	info.CoverImage = s.attachmentService.ToMediaURL(ctx, p.CoverImage)
	info.Images = s.attachmentService.ToMediaURLs(ctx, imageURLs)

	detail := &product.ProductDetail{
		Product: info,
		Skus:    make([]*product.SkuInfo, 0, len(skus)),
		Specs:   specs,
	}
	for _, sku := range skus {
		skuInfo := service.ToSkuInfo(sku)
		// 出库统一拼接完整访问 URL（SKU 规格图片相对路径）
		skuInfo.Image = s.attachmentService.ToMediaURL(ctx, sku.Image)
		detail.Skus = append(detail.Skus, skuInfo)
	}
	return detail, nil
}

// SearchProducts 搜索商品
func (s *server) SearchProducts(ctx stdctx.Context, req *product.SearchProductsReq) (*product.ListProductsResp, error) {
	listReq := &product.ListProductsReq{
		CommunityId: req.CommunityId,
		Keyword:     req.Keyword,
		Pagination:  req.Pagination,
	}
	return s.ListProducts(ctx, listReq)
}

// LookupBarcode 按条形码精确查询商品与命中的 SKU（扫码直达）
// 未命中返回 NotFound 业务错误（文案"未找到该条码对应的商品"）
func (s *server) LookupBarcode(ctx stdctx.Context, req *product.LookupBarcodeReq) (*product.LookupBarcodeResp, error) {
	barcode := strings.TrimSpace(req.Barcode)
	if barcode == "" || req.CommunityId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "参数错误")
	}
	p, sku, err := s.productService.GetByBarcode(ctx, req.CommunityId, barcode)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	info := service.ToProductInfo(p, "", nil)
	// 出库统一拼接完整访问 URL（封面相对路径）
	info.CoverImage = s.attachmentService.ToMediaURL(ctx, p.CoverImage)
	skuInfo := service.ToSkuInfo(sku)
	skuInfo.Image = s.attachmentService.ToMediaURL(ctx, sku.Image)
	return &product.LookupBarcodeResp{Product: info, Sku: skuInfo}, nil
}

// CreateProduct 创建商品
// SKU 由 ProductService.CreateProduct 在同一事务内创建
func (s *server) CreateProduct(ctx stdctx.Context, req *product.CreateProductReq) (*product.ProductDetail, error) {
	// 入库统一归一化：本站附件剥为相对路径，外链 URL 原样保存
	req.CoverImage = s.attachmentService.ToSourcePath(ctx, req.CoverImage)
	req.Images = s.attachmentService.ToSourcePaths(ctx, req.Images)
	for _, item := range req.SkuItems {
		item.Image = s.attachmentService.ToSourcePath(ctx, item.Image)
	}
	p, err := s.productService.CreateProduct(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	return s.GetProduct(ctx, &product.GetProductReq{Id: p.ID})
}

// UpdateProduct 更新商品
func (s *server) UpdateProduct(ctx stdctx.Context, req *product.UpdateProductReq) (*product.ProductDetail, error) {
	// 入库统一归一化：本站附件剥为相对路径，外链 URL 原样保存
	req.CoverImage = s.attachmentService.ToSourcePath(ctx, req.CoverImage)
	req.Images = s.attachmentService.ToSourcePaths(ctx, req.Images)
	for _, item := range req.SkuItems {
		item.Image = s.attachmentService.ToSourcePath(ctx, item.Image)
	}
	if _, err := s.productService.UpdateProduct(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return s.GetProduct(ctx, &product.GetProductReq{Id: req.Id})
}

// DeleteProduct 删除商品
func (s *server) DeleteProduct(ctx stdctx.Context, req *product.DeleteProductReq) (*common.CommonResponse, error) {
	if err := s.productService.DeleteProduct(ctx, req.Id, req.CommunityId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// UpdateProductStatus 更新商品状态
func (s *server) UpdateProductStatus(ctx stdctx.Context, req *product.UpdateProductStatusReq) (*common.CommonResponse, error) {
	if err := s.productService.UpdateProductStatus(ctx, req.Id, req.Status, req.CommunityId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// BatchUpdateProductStatus 批量上下架（管理端列表多选操作）
func (s *server) BatchUpdateProductStatus(ctx stdctx.Context, req *product.BatchUpdateProductStatusReq) (*common.CommonResponse, error) {
	if err := s.productService.BatchUpdateProductStatus(ctx, req.GetIds(), req.GetStatus(), req.GetCommunityId()); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// GetProductStatusStats 商品状态统计（口径与列表筛选一致，供管理端 Tab 计数）
func (s *server) GetProductStatusStats(ctx stdctx.Context, req *product.GetProductStatusStatsReq) (*product.GetProductStatusStatsResp, error) {
	return s.productService.GetProductStatusStats(ctx, req)
}

// UpdateProductName 更新商品名称（列表页行内改名轻量接口）
func (s *server) UpdateProductName(ctx stdctx.Context, req *product.UpdateProductNameReq) (*common.CommonResponse, error) {
	if err := s.productService.UpdateProductName(ctx, req.GetId(), req.GetName(), req.GetCommunityId()); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// AdjustSalesCount 批量调整销量（净成交口径：支付成功累加、取消/退款回退）
func (s *server) AdjustSalesCount(ctx stdctx.Context, req *product.AdjustSalesCountReq) (*common.CommonResponse, error) {
	if err := s.productService.AdjustSalesCount(ctx, req.GetItems()); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListSkus 获取 SKU 列表
func (s *server) ListSkus(ctx stdctx.Context, req *product.ListSkusReq) (*product.ListSkusResp, error) {
	list, err := s.skuService.ListSkus(ctx, req.ProductId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &product.ListSkusResp{List: make([]*product.SkuInfo, 0, len(list))}
	for _, sku := range list {
		info := service.ToSkuInfo(sku)
		// 出库统一拼接完整访问 URL（SKU 规格图片相对路径）
		info.Image = s.attachmentService.ToMediaURL(ctx, sku.Image)
		resp.List = append(resp.List, info)
	}
	return resp, nil
}

// SearchSkus 社区内 SKU 级搜索（库存工作台列表 + 进销存表单 SKU 选择器共用）
// 填充冗余商品名 product_name；SKU 规格图片拼接完整访问 URL
func (s *server) SearchSkus(ctx stdctx.Context, req *product.SearchSkusReq) (*product.SearchSkusResp, error) {
	page, pageSize := 1, 20
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	list, total, err := s.skuService.SearchSkus(ctx, req.CommunityId, req.Keyword, page, pageSize)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &product.SearchSkusResp{
		List: make([]*product.SkuInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, row := range list {
		info := service.ToSkuInfo(&row.Sku)
		info.ProductName = row.ProductName
		info.Image = s.attachmentService.ToMediaURL(ctx, row.Image)
		resp.List = append(resp.List, info)
	}
	return resp, nil
}

// CreateSku 创建 SKU
func (s *server) CreateSku(ctx stdctx.Context, req *product.CreateSkuReq) (*product.SkuInfo, error) {
	sku, err := s.skuService.CreateSku(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToSkuInfo(sku), nil
}

// UpdateSku 更新 SKU
func (s *server) UpdateSku(ctx stdctx.Context, req *product.UpdateSkuReq) (*product.SkuInfo, error) {
	sku, err := s.skuService.UpdateSku(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToSkuInfo(sku), nil
}

// GetSkuById 根据 ID 获取 SKU
// 原始读：不按 status 过滤（管理端编辑、C 端详情展示都取得到禁用规格），
// 但回包含 status 字段供下游（购物车/加购）自判；
// 交易取用的状态与归属收口在 BatchGetSkus
func (s *server) GetSkuById(ctx stdctx.Context, req *product.GetSkuByIdReq) (*product.SkuInfo, error) {
	sku, err := s.skuService.GetSkuByID(ctx, req.Id)
	if err != nil {
		return nil, readError(err, "SKU")
	}
	if err := service.EnsureScope(req.GetCommunityId(), sku.CommunityID, "SKU"); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	info := service.ToSkuInfo(sku)
	// 出库统一拼接完整访问 URL（SKU 规格图片相对路径）
	info.Image = s.attachmentService.ToMediaURL(ctx, sku.Image)
	return info, nil
}

// BatchGetSkus SKU 批量查询（下单/收银明细取价取库存，替代逐行 GetSkuById）
// 交易口径入口：归属（community_id>0 逐条比对）与 SKU 启用态（status=1）都在商品服务收口，
// 调用方（订单服务 loadOrderItemCatalog）须与 BatchGetProducts 传同一个 community_id
func (s *server) BatchGetSkus(ctx stdctx.Context, req *product.BatchGetSkusReq) (*product.BatchGetSkusResp, error) {
	skus, err := s.skuService.BatchGetSkus(ctx, req.Ids, req.GetCommunityId())
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	list := make([]*product.SkuInfo, 0, len(skus))
	for _, sku := range skus {
		info := service.ToSkuInfo(sku)
		// 与 GetSkuById 同口径：出库统一拼接完整访问 URL
		info.Image = s.attachmentService.ToMediaURL(ctx, sku.Image)
		list = append(list, info)
	}
	return &product.BatchGetSkusResp{List: list}, nil
}

// BatchGetProducts 商品批量查询（下单明细取商品名/封面/分类，替代逐行 GetProduct）
// 只回主表字段：调用方不需要 SKU 明细、规格树与图集，避免按行放大附属查询。
// require_sellable=true 时任一商品非上架态即整体报错（下单入口的可售收口，P1-4）
func (s *server) BatchGetProducts(ctx stdctx.Context, req *product.BatchGetProductsReq) (*product.BatchGetProductsResp, error) {
	products, err := s.productService.BatchGetProducts(ctx, req.Ids, req.GetCommunityId(), req.GetRequireSellable())
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	list := make([]*product.ProductInfo, 0, len(products))
	for _, p := range products {
		info := service.ToProductInfo(p, "", nil)
		info.CoverImage = s.attachmentService.ToMediaURL(ctx, p.CoverImage)
		list = append(list, info)
	}
	return &product.BatchGetProductsResp{List: list}, nil
}

// SetCommunityPricing 设置社区定价
func (s *server) SetCommunityPricing(ctx stdctx.Context, req *product.SetPricingReq) (*common.CommonResponse, error) {
	if err := s.pricingService.SetCommunityPricing(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListCommunityPricing 查询社区定价
func (s *server) ListCommunityPricing(ctx stdctx.Context, req *product.ListPricingReq) (*product.ListPricingResp, error) {
	list, err := s.pricingService.ListCommunityPricing(ctx, req.SkuId, req.CommunityId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &product.ListPricingResp{List: make([]*product.PricingInfo, 0, len(list))}
	for _, p := range list {
		resp.List = append(resp.List, service.ToPricingInfo(p, ""))
	}
	return resp, nil
}

// GetReferencePricing 查询参考定价
func (s *server) GetReferencePricing(ctx stdctx.Context, req *product.GetReferencePricingReq) (*product.ListPricingResp, error) {
	list, err := s.pricingService.GetReferencePricing(ctx, req.SkuId, req.ExcludeCommunityId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &product.ListPricingResp{List: make([]*product.PricingInfo, 0, len(list))}
	for _, p := range list {
		resp.List = append(resp.List, service.ToPricingInfo(p, ""))
	}
	return resp, nil
}

// DeductStock 扣减库存
func (s *server) DeductStock(ctx stdctx.Context, req *product.DeductStockReq) (*product.DeductStockResp, error) {
	success, remain, err := s.stockService.DeductStock(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &product.DeductStockResp{Success: success, RemainStock: remain}, nil
}

// ReleaseStock 释放库存
func (s *server) ReleaseStock(ctx stdctx.Context, req *product.ReleaseStockReq) (*common.CommonResponse, error) {
	if err := s.stockService.ReleaseStock(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// DeductFlashSaleStock 扣减秒杀库存
func (s *server) DeductFlashSaleStock(ctx stdctx.Context, req *product.DeductFlashSaleStockReq) (*common.CommonResponse, error) {
	if err := s.stockService.DeductFlashSaleStock(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ReleaseFlashSaleStock 释放秒杀库存
func (s *server) ReleaseFlashSaleStock(ctx stdctx.Context, req *product.ReleaseFlashSaleStockReq) (*common.CommonResponse, error) {
	if err := s.stockService.ReleaseFlashSaleStock(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// BatchDeductStock 批量扣减库存（整单明细一事务一次往返）
func (s *server) BatchDeductStock(ctx stdctx.Context, req *product.BatchDeductStockReq) (*product.BatchDeductStockResp, error) {
	success, err := s.stockService.BatchDeductStock(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &product.BatchDeductStockResp{Success: success}, nil
}

// BatchReleaseStock 批量释放库存
func (s *server) BatchReleaseStock(ctx stdctx.Context, req *product.BatchReleaseStockReq) (*common.CommonResponse, error) {
	if err := s.stockService.BatchReleaseStock(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// BatchDeductFlashSaleStock 批量扣减秒杀库存
func (s *server) BatchDeductFlashSaleStock(ctx stdctx.Context, req *product.BatchDeductFlashSaleStockReq) (*common.CommonResponse, error) {
	if err := s.stockService.BatchDeductFlashSaleStock(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// BatchReleaseFlashSaleStock 批量回补秒杀库存
func (s *server) BatchReleaseFlashSaleStock(ctx stdctx.Context, req *product.BatchReleaseFlashSaleStockReq) (*common.CommonResponse, error) {
	if err := s.stockService.BatchReleaseFlashSaleStock(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ==================== 供应商 ====================

// ListSuppliers 查询供应商列表
func (s *server) ListSuppliers(ctx stdctx.Context, req *product.ListSuppliersReq) (*product.ListSuppliersResp, error) {
	return s.supplierService.ListSuppliers(ctx, req)
}

// CreateSupplier 创建供应商
func (s *server) CreateSupplier(ctx stdctx.Context, req *product.CreateSupplierReq) (*product.SupplierInfo, error) {
	sp, err := s.supplierService.CreateSupplier(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return sp, nil
}

// UpdateSupplier 更新供应商
func (s *server) UpdateSupplier(ctx stdctx.Context, req *product.UpdateSupplierReq) (*product.SupplierInfo, error) {
	sp, err := s.supplierService.UpdateSupplier(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return sp, nil
}

// DeleteSupplier 删除供应商
func (s *server) DeleteSupplier(ctx stdctx.Context, req *product.DeleteSupplierReq) (*common.CommonResponse, error) {
	if err := s.supplierService.DeleteSupplier(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ==================== 采购单 ====================

// CreatePurchaseOrder 创建采购单
func (s *server) CreatePurchaseOrder(ctx stdctx.Context, req *product.CreatePurchaseOrderReq) (*product.PurchaseOrderDetail, error) {
	detail, err := s.purchaseService.CreatePurchaseOrder(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return detail, nil
}

// UpdatePurchaseOrder 更新采购单（仅草稿）
func (s *server) UpdatePurchaseOrder(ctx stdctx.Context, req *product.UpdatePurchaseOrderReq) (*product.PurchaseOrderDetail, error) {
	detail, err := s.purchaseService.UpdatePurchaseOrder(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return detail, nil
}

// SubmitPurchaseOrder 提交采购单（草稿→已下单）
func (s *server) SubmitPurchaseOrder(ctx stdctx.Context, req *product.SubmitPurchaseOrderReq) (*common.CommonResponse, error) {
	if err := s.purchaseService.SubmitPurchaseOrder(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// CancelPurchaseOrder 取消采购单
func (s *server) CancelPurchaseOrder(ctx stdctx.Context, req *product.CancelPurchaseOrderReq) (*common.CommonResponse, error) {
	if err := s.purchaseService.CancelPurchaseOrder(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListPurchaseOrders 查询采购单列表
func (s *server) ListPurchaseOrders(ctx stdctx.Context, req *product.ListPurchaseOrdersReq) (*product.ListPurchaseOrdersResp, error) {
	return s.purchaseService.ListPurchaseOrders(ctx, req)
}

// GetPurchaseOrder 查询采购单详情
func (s *server) GetPurchaseOrder(ctx stdctx.Context, req *product.GetPurchaseOrderReq) (*product.PurchaseOrderDetail, error) {
	detail, err := s.purchaseService.GetPurchaseOrder(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return detail, nil
}

// ==================== 入库单 / 出库单 / 批次 ====================

// CreateStockInOrder 创建入库单
func (s *server) CreateStockInOrder(ctx stdctx.Context, req *product.CreateStockInOrderReq) (*product.StockInOrderDetail, error) {
	order, items, err := s.stockBillService.CreateStockInOrder(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return s.buildStockInOrderDetail(ctx, order, items)
}

// ListStockInOrders 查询入库单列表
func (s *server) ListStockInOrders(ctx stdctx.Context, req *product.ListStockInOrdersReq) (*product.ListStockInOrdersResp, error) {
	list, total, err := s.stockBillService.ListStockInOrders(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	resp := &product.ListStockInOrdersResp{
		List: make([]*product.StockInOrderInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	// 列表页不带明细，供应商名/采购单号由 gateway 或详情页回填；此处回填供应商名
	for _, o := range list {
		resp.List = append(resp.List, toStockInOrderInfo(o))
	}
	return resp, nil
}

// GetStockInOrder 查询入库单详情
func (s *server) GetStockInOrder(ctx stdctx.Context, req *product.GetStockInOrderReq) (*product.StockInOrderDetail, error) {
	detail, err := s.stockBillService.GetStockInOrder(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return detail, nil
}

// buildStockInOrderDetail 组装入库单创建响应（复用详情查询的回填逻辑）
func (s *server) buildStockInOrderDetail(ctx stdctx.Context, order *model.StockInOrder, items []*model.StockInOrderItem) (*product.StockInOrderDetail, error) {
	return s.stockBillService.GetStockInOrder(ctx, &product.GetStockInOrderReq{Id: order.ID})
}

// toStockInOrderInfo 入库单模型转 proto（列表页基础字段）
func toStockInOrderInfo(o *model.StockInOrder) *product.StockInOrderInfo {
	return &product.StockInOrderInfo{
		Id:              o.ID,
		InNo:            o.InNo,
		CommunityId:     o.CommunityID,
		SupplierId:      o.SupplierID,
		PurchaseOrderId: o.PurchaseOrderID,
		Type:            o.Type,
		TotalAmount:     o.TotalAmount,
		OperatorId:      o.OperatorID,
		Remark:          o.Remark,
		CreatedAt:       o.CreatedAt,
	}
}

// CreateStockOutOrder 创建出库单
func (s *server) CreateStockOutOrder(ctx stdctx.Context, req *product.CreateStockOutOrderReq) (*product.StockOutOrderDetail, error) {
	order, _, err := s.stockBillService.CreateStockOutOrder(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return s.stockBillService.GetStockOutOrder(ctx, &product.GetStockOutOrderReq{Id: order.ID})
}

// ListStockOutOrders 查询出库单列表
func (s *server) ListStockOutOrders(ctx stdctx.Context, req *product.ListStockOutOrdersReq) (*product.ListStockOutOrdersResp, error) {
	list, total, err := s.stockBillService.ListStockOutOrders(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	resp := &product.ListStockOutOrdersResp{
		List: make([]*product.StockOutOrderInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, o := range list {
		resp.List = append(resp.List, &product.StockOutOrderInfo{
			Id:          o.ID,
			OutNo:       o.OutNo,
			CommunityId: o.CommunityID,
			Type:        o.Type,
			OperatorId:  o.OperatorID,
			Remark:      o.Remark,
			CreatedAt:   o.CreatedAt,
		})
	}
	return resp, nil
}

// GetStockOutOrder 查询出库单详情
func (s *server) GetStockOutOrder(ctx stdctx.Context, req *product.GetStockOutOrderReq) (*product.StockOutOrderDetail, error) {
	detail, err := s.stockBillService.GetStockOutOrder(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return detail, nil
}

// ListStockBatches 查询批次库存
func (s *server) ListStockBatches(ctx stdctx.Context, req *product.ListStockBatchesReq) (*product.ListStockBatchesResp, error) {
	return s.stockBillService.ListStockBatches(ctx, req)
}

// ==================== 报表 ====================

// GetInventorySummaryReport 进销存汇总报表
func (s *server) GetInventorySummaryReport(ctx stdctx.Context, req *product.GetInventorySummaryReportReq) (*product.GetInventorySummaryReportResp, error) {
	return s.reportService.GetInventorySummaryReport(ctx, req)
}

// GetProfitReport 毛利分析报表
func (s *server) GetProfitReport(ctx stdctx.Context, req *product.GetProfitReportReq) (*product.GetProfitReportResp, error) {
	return s.reportService.GetProfitReport(ctx, req)
}

// ListStockRecords 查询库存流水
func (s *server) ListStockRecords(ctx stdctx.Context, req *product.ListStockRecordsReq) (*product.ListStockRecordsResp, error) {
	list, total, err := s.stockService.ListStockRecords(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	resp := &product.ListStockRecordsResp{
		List: make([]*product.StockRecordInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, r := range list {
		resp.List = append(resp.List, service.ToStockRecordInfo(r))
	}
	return resp, nil
}

// GetStockWarnings 获取库存预警
func (s *server) GetStockWarnings(ctx stdctx.Context, req *product.GetStockWarningsReq) (*product.ListStockWarningsResp, error) {
	list, err := s.stockService.GetStockWarnings(ctx, req.CommunityId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &product.ListStockWarningsResp{List: list}, nil
}

// GeneratePurchaseOrder 生成补货建议单
func (s *server) GeneratePurchaseOrder(ctx stdctx.Context, req *product.GeneratePurchaseOrderReq) (*product.GeneratePurchaseOrderResp, error) {
	items, err := s.stockService.GeneratePurchaseOrder(ctx, req.CommunityId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &product.GeneratePurchaseOrderResp{Items: items}, nil
}

// CreateStockCheck 创建盘点单
func (s *server) CreateStockCheck(ctx stdctx.Context, req *product.CreateStockCheckReq) (*product.StockCheckInfo, error) {
	check, err := s.stockCheckService.CreateStockCheck(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToStockCheckInfo(check), nil
}

// SubmitStockCheck 提交盘点单
func (s *server) SubmitStockCheck(ctx stdctx.Context, req *product.SubmitStockCheckReq) (*common.CommonResponse, error) {
	if err := s.stockCheckService.SubmitStockCheck(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListStockChecks 查询盘点单列表
func (s *server) ListStockChecks(ctx stdctx.Context, req *product.ListStockChecksReq) (*product.ListStockChecksResp, error) {
	list, total, err := s.stockCheckService.ListStockChecks(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	resp := &product.ListStockChecksResp{
		List: make([]*product.StockCheckInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, c := range list {
		resp.List = append(resp.List, service.ToStockCheckInfo(c))
	}
	return resp, nil
}

// GetStockCheck 查询盘点单详情
func (s *server) GetStockCheck(ctx stdctx.Context, req *product.GetStockCheckReq) (*product.StockCheckDetail, error) {
	detail, err := s.stockCheckService.GetStockCheck(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return detail, nil
}

// ==================== 营销 ====================

// ListCoupons 查询优惠券列表
func (s *server) ListCoupons(ctx stdctx.Context, req *product.ListCouponsReq) (*product.ListCouponsResp, error) {
	list, err := s.marketingService.ListCoupons(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &product.ListCouponsResp{List: make([]*product.CouponInfo, 0, len(list))}
	for _, c := range list {
		resp.List = append(resp.List, service.ToCouponInfo(c))
	}
	return resp, nil
}

// GetCoupon 获取优惠券详情
func (s *server) GetCoupon(ctx stdctx.Context, req *product.GetCouponReq) (*product.CouponInfo, error) {
	c, err := s.marketingService.GetCoupon(ctx, req.Id, req.CommunityId)
	if err != nil {
		return nil, readError(err, "优惠券")
	}
	return service.ToCouponInfo(c), nil
}

// CreateCoupon 创建优惠券
func (s *server) CreateCoupon(ctx stdctx.Context, req *product.CreateCouponReq) (*product.CouponInfo, error) {
	c, err := s.marketingService.CreateCoupon(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToCouponInfo(c), nil
}

// UpdateCoupon 更新优惠券
func (s *server) UpdateCoupon(ctx stdctx.Context, req *product.UpdateCouponReq) (*product.CouponInfo, error) {
	c, err := s.marketingService.UpdateCoupon(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToCouponInfo(c), nil
}

// DeleteCoupon 删除优惠券
func (s *server) DeleteCoupon(ctx stdctx.Context, req *product.DeleteCouponReq) (*common.CommonResponse, error) {
	if err := s.marketingService.DeleteCoupon(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ReleaseCoupon 取消订单时回补用户券
func (s *server) ReleaseCoupon(ctx stdctx.Context, req *product.ReleaseCouponReq) (*common.CommonResponse, error) {
	if err := s.marketingService.ReleaseCoupon(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// GrantCoupon 后台手动发放优惠券（批量，逐人独立校验）
func (s *server) GrantCoupon(ctx stdctx.Context, req *product.GrantCouponReq) (*product.GrantCouponResp, error) {
	resp, err := s.marketingService.GrantCoupon(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return resp, nil
}

// ListCouponRecords 后台领取/发放记录查询
func (s *server) ListCouponRecords(ctx stdctx.Context, req *product.ListCouponRecordsReq) (*product.ListCouponRecordsResp, error) {
	list, total, err := s.marketingService.ListCouponRecords(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	page, pageSize := int32(1), int32(10)
	if req.Pagination != nil {
		page = req.Pagination.Page
		pageSize = req.Pagination.PageSize
	}
	resp := &product.ListCouponRecordsResp{
		List: make([]*product.CouponRecordInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     page,
			PageSize: pageSize,
		},
	}
	for _, uc := range list {
		resp.List = append(resp.List, service.ToCouponRecordInfo(uc))
	}
	return resp, nil
}

// ReceiveCoupon 领取优惠券
func (s *server) ReceiveCoupon(ctx stdctx.Context, req *product.ReceiveCouponReq) (*common.CommonResponse, error) {
	if err := s.marketingService.ReceiveCoupon(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListUserCoupons 查询用户优惠券列表
func (s *server) ListUserCoupons(ctx stdctx.Context, req *product.ListUserCouponsReq) (*product.ListUserCouponsResp, error) {
	list, err := s.marketingService.ListUserCoupons(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &product.ListUserCouponsResp{List: make([]*product.UserCouponInfo, 0, len(list))}
	for _, uc := range list {
		resp.List = append(resp.List, service.ToUserCouponInfo(uc))
	}
	return resp, nil
}

// UseCoupon 使用优惠券
func (s *server) UseCoupon(ctx stdctx.Context, req *product.UseCouponReq) (*common.CommonResponse, error) {
	if err := s.marketingService.UseCoupon(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListFlashSales 查询秒杀活动列表
func (s *server) ListFlashSales(ctx stdctx.Context, req *product.ListFlashSalesReq) (*product.ListFlashSalesResp, error) {
	list, err := s.marketingService.ListFlashSales(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &product.ListFlashSalesResp{List: make([]*product.FlashSaleInfo, 0, len(list))}
	for _, fs := range list {
		resp.List = append(resp.List, service.ToFlashSaleInfo(fs))
	}
	return resp, nil
}

// GetFlashSale 获取秒杀活动详情
func (s *server) GetFlashSale(ctx stdctx.Context, req *product.GetFlashSaleReq) (*product.FlashSaleDetail, error) {
	fs, items, err := s.marketingService.GetFlashSale(ctx, req.Id, req.CommunityId)
	if err != nil {
		return nil, readError(err, "秒杀活动")
	}

	// 明细档案批量回填：原先逐行 GetSkuByID + GetProduct（N 行 2N 次查询），
	// 这里固定 2 次查询取回全部 SKU 与商品；取不到留空而非整体报错，
	// 与改造前「sku, _ :=」的容错口径一致
	skuIDs := make([]int64, 0, len(items))
	for _, item := range items {
		skuIDs = append(skuIDs, item.SkuID)
	}
	skuByID, err := s.skuService.FindSkusByIDs(ctx, skuIDs)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	productIDs := make([]int64, 0, len(skuByID))
	for _, sku := range skuByID {
		productIDs = append(productIDs, sku.ProductID)
	}
	productByID, err := s.productService.FindProductsByIDs(ctx, productIDs)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	detail := &product.FlashSaleDetail{
		Info:  service.ToFlashSaleInfo(fs),
		Items: make([]*product.FlashSaleItemInfo, 0, len(items)),
	}
	for _, item := range items {
		sku := skuByID[item.SkuID]
		var productName string
		if sku != nil {
			if p := productByID[sku.ProductID]; p != nil {
				productName = p.Name
			}
		}
		detail.Items = append(detail.Items, service.ToFlashSaleItemInfo(item, sku, productName))
	}
	return detail, nil
}

// CreateFlashSale 创建秒杀活动
func (s *server) CreateFlashSale(ctx stdctx.Context, req *product.CreateFlashSaleReq) (*product.FlashSaleInfo, error) {
	fs, err := s.marketingService.CreateFlashSale(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToFlashSaleInfo(fs), nil
}

// UpdateFlashSale 更新秒杀活动
func (s *server) UpdateFlashSale(ctx stdctx.Context, req *product.UpdateFlashSaleReq) (*product.FlashSaleInfo, error) {
	fs, err := s.marketingService.UpdateFlashSale(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToFlashSaleInfo(fs), nil
}

// ListFullReductions 查询满减活动列表
func (s *server) ListFullReductions(ctx stdctx.Context, req *product.ListFullReductionsReq) (*product.ListFullReductionsResp, error) {
	list, err := s.marketingService.ListFullReductions(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &product.ListFullReductionsResp{List: make([]*product.FullReductionInfo, 0, len(list))}
	for _, fr := range list {
		resp.List = append(resp.List, service.ToFullReductionInfo(fr))
	}
	return resp, nil
}

// CreateFullReduction 创建满减活动
func (s *server) CreateFullReduction(ctx stdctx.Context, req *product.CreateFullReductionReq) (*product.FullReductionInfo, error) {
	fr, err := s.marketingService.CreateFullReduction(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToFullReductionInfo(fr), nil
}

// UpdateFullReduction 更新满减活动
func (s *server) UpdateFullReduction(ctx stdctx.Context, req *product.UpdateFullReductionReq) (*product.FullReductionInfo, error) {
	fr, err := s.marketingService.UpdateFullReduction(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToFullReductionInfo(fr), nil
}

// ListFreeShippings 查询免配送费活动列表（含排除范围）
func (s *server) ListFreeShippings(ctx stdctx.Context, req *product.ListFreeShippingsReq) (*product.ListFreeShippingsResp, error) {
	list, err := s.marketingService.ListFreeShippings(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &product.ListFreeShippingsResp{List: make([]*product.FreeShippingInfo, 0, len(list))}
	for _, a := range list {
		resp.List = append(resp.List, service.ToFreeShippingInfo(a))
	}
	return resp, nil
}

// CreateFreeShipping 创建免配送费活动
func (s *server) CreateFreeShipping(ctx stdctx.Context, req *product.CreateFreeShippingReq) (*product.FreeShippingInfo, error) {
	activity, err := s.marketingService.CreateFreeShipping(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToFreeShippingInfo(activity), nil
}

// UpdateFreeShipping 更新免配送费活动
func (s *server) UpdateFreeShipping(ctx stdctx.Context, req *product.UpdateFreeShippingReq) (*product.FreeShippingInfo, error) {
	activity, err := s.marketingService.UpdateFreeShipping(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToFreeShippingInfo(activity), nil
}

// GetActiveFreeShipping 查询当前社区正在生效的免配送费活动（无生效活动时 activity 为空）
func (s *server) GetActiveFreeShipping(ctx stdctx.Context, req *product.GetActiveFreeShippingReq) (*product.GetActiveFreeShippingResp, error) {
	activity, err := s.marketingService.GetActiveFreeShipping(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &product.GetActiveFreeShippingResp{Activity: service.ToFreeShippingInfo(activity)}, nil
}

// ListBanners 查询 Banner 列表
func (s *server) ListBanners(ctx stdctx.Context, req *product.ListBannersReq) (*product.ListBannersResp, error) {
	list, err := s.bannerService.ListBanners(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &product.ListBannersResp{List: make([]*product.BannerInfo, 0, len(list))}
	for _, b := range list {
		info := service.ToBannerInfo(b)
		// 出库统一拼接完整访问 URL（库存相对路径）
		info.ImageUrl = s.attachmentService.ToMediaURL(ctx, b.ImageURL)
		resp.List = append(resp.List, info)
	}
	return resp, nil
}

// CreateBanner 创建 Banner
func (s *server) CreateBanner(ctx stdctx.Context, req *product.CreateBannerReq) (*product.BannerInfo, error) {
	// 入库统一归一化：本站附件剥为相对路径，外链 URL 原样保存
	req.ImageUrl = s.attachmentService.ToSourcePath(ctx, req.ImageUrl)
	b, err := s.bannerService.CreateBanner(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	info := service.ToBannerInfo(b)
	info.ImageUrl = s.attachmentService.ToMediaURL(ctx, b.ImageURL)
	return info, nil
}

// UpdateBanner 更新 Banner
func (s *server) UpdateBanner(ctx stdctx.Context, req *product.UpdateBannerReq) (*product.BannerInfo, error) {
	// 入库统一归一化：本站附件剥为相对路径，外链 URL 原样保存
	req.ImageUrl = s.attachmentService.ToSourcePath(ctx, req.ImageUrl)
	b, err := s.bannerService.UpdateBanner(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	info := service.ToBannerInfo(b)
	info.ImageUrl = s.attachmentService.ToMediaURL(ctx, b.ImageURL)
	return info, nil
}

// DeleteBanner 删除 Banner
func (s *server) DeleteBanner(ctx stdctx.Context, req *product.DeleteBannerReq) (*common.CommonResponse, error) {
	if err := s.bannerService.DeleteBanner(ctx, req.Id, req.CommunityId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{}, nil
}

// GetAttachmentConfig 获取附件配置（AccessKeySecret 脱敏）
func (s *server) GetAttachmentConfig(ctx stdctx.Context, req *common.Empty) (*product.AttachmentConfig, error) {
	cfg, err := s.attachmentService.GetConfigMasked(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return cfg, nil
}

// SaveAttachmentConfig 保存附件配置（Secret 为空或掩码时保留原值）
func (s *server) SaveAttachmentConfig(ctx stdctx.Context, req *product.AttachmentConfig) (*common.CommonResponse, error) {
	if err := s.attachmentService.SaveConfig(ctx, req); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &common.CommonResponse{}, nil
}

// UploadImageLocal 本地模式图片上传（gateway 转发文件内容）
func (s *server) UploadImageLocal(ctx stdctx.Context, req *product.UploadImageLocalReq) (*product.AttachmentInfo, error) {
	info, err := s.attachmentService.UploadImageLocal(ctx, req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return info, nil
}

// AttachmentPresign 获取预签名上传 URL（OSS 直传模式）
func (s *server) AttachmentPresign(ctx stdctx.Context, req *product.AttachmentPresignReq) (*product.AttachmentPresignResp, error) {
	resp, err := s.attachmentService.AttachmentPresign(ctx, req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return resp, nil
}

// AttachmentConfirm 确认上传完成（补写 md5 与尺寸，激活秒传）
func (s *server) AttachmentConfirm(ctx stdctx.Context, req *product.AttachmentConfirmReq) (*product.AttachmentInfo, error) {
	info, err := s.attachmentService.AttachmentConfirm(ctx, req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return info, nil
}

// ListAttachments 附件选择器分页列表（本人上传且 is_display=1 的图片附件）
func (s *server) ListAttachments(ctx stdctx.Context, req *product.ListAttachmentsReq) (*product.ListAttachmentsResp, error) {
	resp, err := s.attachmentService.ListAttachments(ctx, req)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return resp, nil
}

// ScanAttachmentMigrate 扫描本地附件目录与附件表匹配，生成/刷新迁移任务清单
func (s *server) ScanAttachmentMigrate(ctx stdctx.Context, _ *common.Empty) (*product.AttachmentMigrateOverview, error) {
	overview, err := s.attachmentService.ScanAttachmentMigrate(ctx)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return overview, nil
}

// ListAttachmentMigrate 迁移条目分页列表（含概览统计）
func (s *server) ListAttachmentMigrate(ctx stdctx.Context, req *product.AttachmentMigrateListReq) (*product.AttachmentMigrateListResp, error) {
	resp, err := s.attachmentService.ListAttachmentMigrateItems(ctx, req)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return resp, nil
}

// MigrateAttachmentItem 迁移单个条目（上传 OSS 后自动验证）
func (s *server) MigrateAttachmentItem(ctx stdctx.Context, req *product.AttachmentMigrateItemReq) (*product.AttachmentMigrateItemInfo, error) {
	info, err := s.attachmentService.MigrateAttachmentItem(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return info, nil
}

// VerifyAttachmentItem 验证单个条目（OSS 对象存在 + 大小一致）
func (s *server) VerifyAttachmentItem(ctx stdctx.Context, req *product.AttachmentMigrateItemReq) (*product.AttachmentMigrateItemInfo, error) {
	info, err := s.attachmentService.VerifyAttachmentItem(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return info, nil
}

// DeleteAttachmentLocal 删除单个条目的本地文件（仅验证成功条目）
func (s *server) DeleteAttachmentLocal(ctx stdctx.Context, req *product.AttachmentMigrateItemReq) (*product.AttachmentMigrateItemInfo, error) {
	info, err := s.attachmentService.DeleteAttachmentLocal(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return info, nil
}

// DeleteAttachmentLocalVerified 一键删除全部已验证条目的本地文件
func (s *server) DeleteAttachmentLocalVerified(ctx stdctx.Context, _ *common.Empty) (*product.AttachmentMigrateDeleteBatchResp, error) {
	deleted, err := s.attachmentService.DeleteAttachmentLocalVerified(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &product.AttachmentMigrateDeleteBatchResp{Deleted: deleted}, nil
}
