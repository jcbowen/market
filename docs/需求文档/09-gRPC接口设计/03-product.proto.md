### product.proto

```protobuf
syntax = "proto3";

package product;

option go_package = "github.com/jcbowen/market/src/backend/common/proto/product";

import "common/common.proto";

service ProductService {
  // 分类
  rpc ListCategories(ListCategoriesReq) returns (ListCategoriesResp);
  rpc GetCategory(GetCategoryReq) returns (CategoryInfo);
  rpc CreateCategory(CreateCategoryReq) returns (CategoryInfo);
  rpc UpdateCategory(UpdateCategoryReq) returns (CategoryInfo);
  rpc DeleteCategory(DeleteCategoryReq) returns (common.CommonResponse);
  // BatchSortCategories 分类批量排序（管理端拖拽后整表提交，事务内按 items 顺序重写 sort）
  rpc BatchSortCategories(BatchSortCategoriesReq) returns (common.CommonResponse);

  // 商品
  rpc ListProducts(ListProductsReq) returns (ListProductsResp);
  rpc GetProduct(GetProductReq) returns (ProductDetail);
  rpc SearchProducts(SearchProductsReq) returns (ListProductsResp);
  rpc LookupBarcode(LookupBarcodeReq) returns (LookupBarcodeResp);
  rpc CreateProduct(CreateProductReq) returns (ProductDetail);
  rpc UpdateProduct(UpdateProductReq) returns (ProductDetail);
  rpc DeleteProduct(DeleteProductReq) returns (common.CommonResponse);
  rpc UpdateProductStatus(UpdateProductStatusReq) returns (common.CommonResponse);
  // 批量上下架（管理端列表多选操作，不触碰已删除商品）
  rpc BatchUpdateProductStatus(BatchUpdateProductStatusReq) returns (common.CommonResponse);
  // 商品状态统计（口径与列表筛选条件一致，供 Tab 计数展示）
  rpc GetProductStatusStats(GetProductStatusStatsReq) returns (GetProductStatusStatsResp);
  // 列表页行内改名轻量接口（仅更新 name，避免全量提交覆盖规格/SKU）
  rpc UpdateProductName(UpdateProductNameReq) returns (common.CommonResponse);

  // SKU
  rpc ListSkus(ListSkusReq) returns (ListSkusResp);
  // 社区内 SKU 级搜索（join products 携带商品名）；keyword 匹配商品名/SKU 编码/条形码（LIKE），
  // 为空时返回全量（分页）。供库存工作台列表与进销存表单的 SKU 选择器复用，替代网关侧 N+1 组合查询
  rpc SearchSkus(SearchSkusReq) returns (SearchSkusResp);
  rpc CreateSku(CreateSkuReq) returns (SkuInfo);
  rpc UpdateSku(UpdateSkuReq) returns (SkuInfo);
  rpc GetSkuById(GetSkuByIdReq) returns (SkuInfo);
  // SKU 批量查询：下单/收银按明细取价取库存，替代逐行 GetSkuById 的 N 次往返
  // 默认「交易口径」：任一 ID 缺失、越权或 SKU 停用即整体拒绝；
  // tolerate_missing=true 切「逐项判定口径」：缺失/越权项只从结果剔除、状态与库存原样返回，
  // 可售判定交由调用方（C 端再来一单据此逐项跳过失效规格，不阻断整单复购）
  rpc BatchGetSkus(BatchGetSkusReq) returns (BatchGetSkusResp);
  // 商品批量查询：下单按明细取商品名/封面/分类，替代逐行 GetProduct
  // require_sellable=true 为交易口径（任一非上架态整体拒绝）；tolerate_missing=true 为逐项判定口径
  // （缺失/越权剔除、上架态原样返回），优先于 require_sellable
  rpc BatchGetProducts(BatchGetProductsReq) returns (BatchGetProductsResp);

  // 库存
  rpc DeductStock(DeductStockReq) returns (DeductStockResp);
  rpc ReleaseStock(ReleaseStockReq) returns (common.CommonResponse);
  // 批量库存操作：整单明细一事务完成，下单 2N 次扣减往返压缩为 1 次
  rpc BatchDeductStock(BatchDeductStockReq) returns (BatchDeductStockResp);
  rpc BatchReleaseStock(BatchReleaseStockReq) returns (common.CommonResponse);
  rpc AdjustSalesCount(AdjustSalesCountReq) returns (common.CommonResponse);
  rpc ListStockRecords(ListStockRecordsReq) returns (ListStockRecordsResp);
  rpc GetStockWarnings(GetStockWarningsReq) returns (ListStockWarningsResp);
  rpc GeneratePurchaseOrder(GeneratePurchaseOrderReq) returns (GeneratePurchaseOrderResp);
  // 秒杀独立库存（flash_sale_items.stock）：下单扣减/取消退款回补，幂等键与主库存区分（op_type 3/4）
  rpc DeductFlashSaleStock(DeductFlashSaleStockReq) returns (common.CommonResponse);
  rpc ReleaseFlashSaleStock(ReleaseFlashSaleStockReq) returns (common.CommonResponse);
  rpc BatchDeductFlashSaleStock(BatchDeductFlashSaleStockReq) returns (common.CommonResponse);
  rpc BatchReleaseFlashSaleStock(BatchReleaseFlashSaleStockReq) returns (common.CommonResponse);

  // 供应商
  rpc ListSuppliers(ListSuppliersReq) returns (ListSuppliersResp);
  rpc CreateSupplier(CreateSupplierReq) returns (SupplierInfo);
  rpc UpdateSupplier(UpdateSupplierReq) returns (SupplierInfo);
  rpc DeleteSupplier(DeleteSupplierReq) returns (common.CommonResponse);

  // 采购单
  rpc CreatePurchaseOrder(CreatePurchaseOrderReq) returns (PurchaseOrderDetail);
  rpc UpdatePurchaseOrder(UpdatePurchaseOrderReq) returns (PurchaseOrderDetail);
  rpc SubmitPurchaseOrder(SubmitPurchaseOrderReq) returns (common.CommonResponse);
  rpc CancelPurchaseOrder(CancelPurchaseOrderReq) returns (common.CommonResponse);
  rpc ListPurchaseOrders(ListPurchaseOrdersReq) returns (ListPurchaseOrdersResp);
  rpc GetPurchaseOrder(GetPurchaseOrderReq) returns (PurchaseOrderDetail);

  // 入库单（创建即生效：生成批次 + 回写汇总库存 + 移动加权平均成本）
  rpc CreateStockInOrder(CreateStockInOrderReq) returns (StockInOrderDetail);
  rpc ListStockInOrders(ListStockInOrdersReq) returns (ListStockInOrdersResp);
  rpc GetStockInOrder(GetStockInOrderReq) returns (StockInOrderDetail);

  // 出库单（报损/领用/调整，后端枚举校验）
  rpc CreateStockOutOrder(CreateStockOutOrderReq) returns (StockOutOrderDetail);
  rpc ListStockOutOrders(ListStockOutOrdersReq) returns (ListStockOutOrdersResp);
  rpc GetStockOutOrder(GetStockOutOrderReq) returns (StockOutOrderDetail);

  // 批次库存
  rpc ListStockBatches(ListStockBatchesReq) returns (ListStockBatchesResp);

  // 报表
  rpc GetInventorySummaryReport(GetInventorySummaryReportReq) returns (GetInventorySummaryReportResp);
  rpc GetProfitReport(GetProfitReportReq) returns (GetProfitReportResp);

  // 盘点
  rpc CreateStockCheck(CreateStockCheckReq) returns (StockCheckInfo);
  rpc SubmitStockCheck(SubmitStockCheckReq) returns (common.CommonResponse);
  rpc ListStockChecks(ListStockChecksReq) returns (ListStockChecksResp);
  rpc GetStockCheck(GetStockCheckReq) returns (StockCheckDetail);

  // 定价
  rpc SetCommunityPricing(SetPricingReq) returns (common.CommonResponse);
  rpc ListCommunityPricing(ListPricingReq) returns (ListPricingResp);
  rpc GetReferencePricing(GetReferencePricingReq) returns (ListPricingResp);

  // 营销
  rpc ListCoupons(ListCouponsReq) returns (ListCouponsResp);
  rpc GetCoupon(GetCouponReq) returns (CouponInfo);
  rpc CreateCoupon(CreateCouponReq) returns (CouponInfo);
  rpc UpdateCoupon(UpdateCouponReq) returns (CouponInfo);
  rpc DeleteCoupon(DeleteCouponReq) returns (common.CommonResponse);
  rpc ReceiveCoupon(ReceiveCouponReq) returns (common.CommonResponse);
  rpc ListUserCoupons(ListUserCouponsReq) returns (ListUserCouponsResp);
  rpc UseCoupon(UseCouponReq) returns (common.CommonResponse);
  // 取消/退款时回补用户券（按订单 ID 找到已核销的 user_coupon，幂等）
  rpc ReleaseCoupon(ReleaseCouponReq) returns (common.CommonResponse);
  // 后台手动发放优惠券给指定会员（批量，逐人校验限领/总量/发放状态）
  rpc GrantCoupon(GrantCouponReq) returns (GrantCouponResp);
  // 后台领取/发放记录查询（user_coupons 按社区语境过滤）
  rpc ListCouponRecords(ListCouponRecordsReq) returns (ListCouponRecordsResp);

  rpc ListFlashSales(ListFlashSalesReq) returns (ListFlashSalesResp);
  rpc GetFlashSale(GetFlashSaleReq) returns (FlashSaleDetail);
  rpc CreateFlashSale(CreateFlashSaleReq) returns (FlashSaleInfo);
  rpc UpdateFlashSale(UpdateFlashSaleReq) returns (FlashSaleInfo);

  rpc ListFullReductions(ListFullReductionsReq) returns (ListFullReductionsResp);
  rpc CreateFullReduction(CreateFullReductionReq) returns (FullReductionInfo);
  rpc UpdateFullReduction(UpdateFullReductionReq) returns (FullReductionInfo);

  // 满额免配送费活动
  rpc ListFreeShippings(ListFreeShippingsReq) returns (ListFreeShippingsResp);
  rpc CreateFreeShipping(CreateFreeShippingReq) returns (FreeShippingInfo);
  rpc UpdateFreeShipping(UpdateFreeShippingReq) returns (FreeShippingInfo);
  rpc GetActiveFreeShipping(GetActiveFreeShippingReq) returns (GetActiveFreeShippingResp);

  // Banner
  rpc ListBanners(ListBannersReq) returns (ListBannersResp);
  rpc CreateBanner(CreateBannerReq) returns (BannerInfo);
  rpc UpdateBanner(UpdateBannerReq) returns (BannerInfo);
  rpc DeleteBanner(DeleteBannerReq) returns (common.CommonResponse);

  // 附件
  rpc GetAttachmentConfig(common.Empty) returns (AttachmentConfig);
  rpc SaveAttachmentConfig(AttachmentConfig) returns (common.CommonResponse);
  rpc UploadImageLocal(UploadImageLocalReq) returns (AttachmentInfo);
  rpc AttachmentPresign(AttachmentPresignReq) returns (AttachmentPresignResp);
  rpc AttachmentConfirm(AttachmentConfirmReq) returns (AttachmentInfo);
  rpc ListAttachments(ListAttachmentsReq) returns (ListAttachmentsResp);

  // 附件迁移（本地 → OSS）
  rpc ScanAttachmentMigrate(common.Empty) returns (AttachmentMigrateOverview);
  rpc ListAttachmentMigrate(AttachmentMigrateListReq) returns (AttachmentMigrateListResp);
  rpc MigrateAttachmentItem(AttachmentMigrateItemReq) returns (AttachmentMigrateItemInfo);
  rpc VerifyAttachmentItem(AttachmentMigrateItemReq) returns (AttachmentMigrateItemInfo);
  rpc DeleteAttachmentLocal(AttachmentMigrateItemReq) returns (AttachmentMigrateItemInfo);
  rpc DeleteAttachmentLocalVerified(common.Empty) returns (AttachmentMigrateDeleteBatchResp);
}

// --- 分类 ---
message ListCategoriesReq {
  int64 community_id = 1;
  // 是否包含禁用分类：false=仅启用（C 端公开接口默认）；true=全部（管理端分类管理页）
  bool include_disabled = 2;
}

// BatchSortCategoriesReq 分类批量排序请求（管理端拖拽排序后整表提交）
message BatchSortCategoriesReq {
  int64 community_id = 1; // 操作者社区语境（>0 时逐个校验分类归属，任一越权即整体拒绝）
  repeated CategorySortItem items = 2; // 全量有序列表，sort 按提交顺序写入
}

// CategorySortItem 分类排序项
message CategorySortItem {
  int64 id = 1;   // 分类 ID
  int32 sort = 2; // 目标排序值（建议按显示顺序 ×10 留空隙）
}

message GetCategoryReq {
  int64 id = 1;
  // 操作者社区语境：>0 时服务端强校验分类归属，防止跨社区读取（超管传 0 放行）
  int64 community_id = 2;

}

message CreateCategoryReq {
  int64 community_id = 1;
  int64 parent_id = 2;
  string name = 3;
  string icon = 4;
  int32 sort = 5;
}

message UpdateCategoryReq {
  int64 id = 1;
  string name = 2;
  string icon = 3;
  int32 sort = 4;
  int32 status = 5;
  // 操作者社区语境：>0 时服务端强校验分类归属，防止跨社区修改（超管传 0 放行）
  int64 community_id = 6;

}

message DeleteCategoryReq {
  int64 id = 1;
  // 操作者社区语境：>0 时服务端强校验分类归属，防止跨社区删除（超管传 0 放行）
  int64 community_id = 2;

}

message CategoryInfo {
  int64 id = 1;
  int64 community_id = 2;
  int64 parent_id = 3;
  string name = 4;
  string icon = 5;
  int32 sort = 6;
  int32 status = 7;
  repeated CategoryInfo children = 8;
}

message ListCategoriesResp {
  repeated CategoryInfo list = 1;
}

// --- 商品 ---
message ListProductsReq {
  int64 community_id = 1;
  int64 category_id = 2;
  string keyword = 3;
  string sort = 4;
  common.Pagination pagination = 5;
  int32 status = 6; // 商品状态过滤：0 不过滤（默认排除已删除），大于 0 时精确匹配（1 上架）
  // 展示状态过滤（管理端 Tab）：0 不过滤，1=出售中(status=1且stock>0)，2=已售罄(status=1且stock<=0)，
  // 3=仓库中(status=0，含定时上架中)，4=库存紧张(status=1 且 0<stock<=SKU安全库存合计 且 合计>0)
  int32 status_filter = 7;

}

message GetProductReq {
  int64 id = 1;
  // 数据作用域：>0 时强校验商品归属社区。C 端公开出口由网关保证恒 >0
  // （公开路由不解析令牌，缺社区只能来自 query；<=0 会被网关直接拒绝，
  //  不再是「跳过归属校验」的旁路）；管理端按当前社区语境填，超管为 0
  int64 community_id = 2;
  // 可售校验（P1-4）：true 时商品非上架态（status≠1，下架/仓库中/定时上架中）直接报错，
  // 供加购等交易入口收口「可售」判定；公开详情与管理端详情不传，保持原行为
  // （下架商品要能打开页面，只按 public_view 抹交易字段而非报错）
  bool require_sellable = 3;
  // 放行软删除行（status=2）：仅管理端详情/编辑回显传 true；
  // 缺省 false 时按 ID 直查也排除已删除商品（列表口径本就恒排除）
  bool include_deleted = 4;
  // 公开口径：true 时由服务层脱敏（service.RedactPublicProductDetail）——
  // cost_price/safety_stock 一律归零，商品非上架态再抹价格/划线价/库存与富文本正文；
  // sku_code/barcode 保留（收银台规格名兜底与扫码加购依赖）。C 端出口恒传 true，管理端不传
  bool public_view = 5;

}

message SearchProductsReq {
  int64 community_id = 1;
  string keyword = 2;
  common.Pagination pagination = 3;
}

message CreateProductReq {
  reserved 10; // 原 repeated CreateSkuReq skus 字段，已被 specs + sku_items 取代
  int64 community_id = 1;
  int64 category_id = 2;
  string name = 3;
  string description = 4;
  string cover_image = 5;
  repeated string images = 6;
  string unit = 7;
  bool is_multi_spec = 8;
  int32 sort = 9;
  repeated SpecReq specs = 12;         // 规格树（多规格时必填）
  repeated SkuItemReq sku_items = 13; // SKU 组合列表（单规格时仅一条，不带规格值）
  int32 min_buy = 11;
  bool show_sales = 14;   // C端是否显示销量
  int32 virtual_sales = 15; // 虚拟销量（展示口径=真实销量+虚拟销量）
  // 定时上架时间（"2006-01-02 15:04:05"）：非空且为未来时间时商品保存为下架+预约，到点由调度器自动上架
  string on_sale_at = 16;
  // 商品富文本详情（服务端白名单消毒后的 HTML，C 端 rich-text 渲染）
  string content = 17;

}

message UpdateProductReq {
  int64 id = 1;
  int64 category_id = 2;
  string name = 3;
  string description = 4;
  string cover_image = 5;
  repeated string images = 6;
  string unit = 7;
  int32 sort = 8;
  int32 status = 9;
  int32 min_buy = 10;
  bool is_multi_spec = 11;
  repeated SpecReq specs = 12;         // 规格树（多规格时必填，全量提交）
  repeated SkuItemReq sku_items = 13; // SKU 组合列表（全量提交）
  bool show_sales = 14;     // C端是否显示销量
  int32 virtual_sales = 15; // 虚拟销量（展示口径=真实销量+虚拟销量）
  // 操作者社区语境：>0 时服务端强校验商品归属，防止跨社区修改（超管传 0 放行）
  int64 community_id = 16;
  // 定时上架时间（"2006-01-02 15:04:05"）：非空且为未来时间时置为下架+预约；
  // 显式传空串表示取消定时（配合 status 决定立即上架或保持下架）
  string on_sale_at = 17;
  // 商品富文本详情（服务端白名单消毒后的 HTML；admin 全量提交，直接覆盖）
  string content = 18;

}

message DeleteProductReq {
  int64 id = 1;
  // 操作者社区语境：>0 时服务端强校验商品归属，防止跨社区删除（超管传 0 放行）
  int64 community_id = 2;

}

message UpdateProductStatusReq {
  int64 id = 1;
  int32 status = 2;
  // 操作者社区语境：>0 时服务端强校验商品归属，防止跨社区上下架（超管传 0 放行）
  int64 community_id = 3;

}

// 商品名称更新（后台列表页行内改名轻量接口，仅改 name 不影响规格/SKU）
message UpdateProductNameReq {
  int64 id = 1;
  string name = 2;
  // 操作者社区语境（>0 时校验商品归属社区，跨社区修改直接拒绝）
  int64 community_id = 3;
}

message ProductInfo {
  int64 id = 1;
  int64 community_id = 2;
  int64 category_id = 3;
  string name = 4;
  string description = 5;
  string cover_image = 6;
  repeated string images = 7;
  string unit = 8;
  bool is_multi_spec = 9;
  int32 status = 10;
  int32 sort = 11;
  int32 sales_count = 12;
  double price = 13;
  int32 stock = 14;
  string category_name = 15;
  int32 min_buy = 16;
  double min_price = 17; // 价格区间下限（汇总字段）
  double max_price = 18; // 价格区间上限（汇总字段）
  double original_price = 19; // 划线价（汇总字段：最低价 SKU 的划线价，0 表示不展示）
  bool show_sales = 20;       // C端是否显示销量
  int32 virtual_sales = 21;   // 虚拟销量（展示口径=真实销量+虚拟销量）
  string on_sale_at = 22;     // 定时上架时间（空=不定时；仅 status=0 且非空时为预约中）
  string content = 23;        // 商品富文本详情（服务端已消毒的 HTML，C 端 rich-text 渲染）

}

message ProductDetail {
  ProductInfo product = 1;
  repeated SkuInfo skus = 2;
  repeated SpecInfo specs = 3; // 规格树（单规格时为空）
}

message ListProductsResp {
  repeated ProductInfo list = 1;
  common.PaginationResponse pagination = 2;
}


// BatchUpdateProductStatusReq 批量上下架请求（管理端列表多选操作）
message BatchUpdateProductStatusReq {
  repeated int64 ids = 1; // 商品 ID 列表
  int32 status = 2;       // 目标状态：1 上架，0 下架
  int64 community_id = 3; // 操作者社区语境（>0 时仅更新归属该社区的商品）
}

// GetProductStatusStatsReq 商品状态统计请求（筛选口径与列表查询一致）
message GetProductStatusStatsReq {
  int64 community_id = 1;
  string keyword = 2;
  int64 category_id = 3;
}

// GetProductStatusStatsResp 商品状态统计响应
// total/on_sale/sold_out/warehouse 互斥合计=total；low_stock 为出售中的子集维度，不参与互斥合计
message GetProductStatusStatsResp {
  int64 total = 1;     // 全部（排除已删除）
  int64 on_sale = 2;   // 出售中：status=1 且 stock>0
  int64 sold_out = 3;  // 已售罄：status=1 且 stock<=0
  int64 warehouse = 4; // 仓库中：status=0（含定时上架中）
  int64 low_stock = 5; // 库存紧张：出售中且 0<stock<=SKU安全库存合计（SUM>0）
}

// --- 规格 ---
// SpecValueReq 规格值保存项（商品保存流程内嵌）
message SpecValueReq {
  int64 id = 1;       // 已存在的规格值 ID，新增时为 0
  string temp_id = 2; // 前端临时 ID（如 new_value_1），新增时必填，供 SKU 组合引用替换
  string name = 3;
  int32 sort = 4;
}

// SpecReq 规格名保存项（商品保存流程内嵌）
message SpecReq {
  int64 id = 1;       // 已存在的规格 ID，新增时为 0
  string temp_id = 2; // 前端临时 ID（如 new_spec_1），新增时必填
  string name = 3;
  int32 sort = 4;
  repeated SpecValueReq values = 5;
}

// SkuItemReq SKU 保存项（商品保存流程内嵌）
message SkuItemReq {
  int64 id = 1;                        // 已存在的 SKU ID，新增时为 0
  repeated string spec_value_ids = 2; // 规格值 ID 列表（真实数字 ID 或前端临时 ID）
  string sku_code = 3;                // 为空时服务端自动生成
  double price = 4;
  double cost_price = 5;
  int32 stock = 6;
  int32 safety_stock = 7;
  string image = 8;
  double original_price = 9; // 划线价（原价，0 表示不展示）
  string barcode = 10;      // 商品条形码（纯数字 6-20 位，社区内在售 SKU 间唯一，空表示未设置；商品软删后自动回收）
}

// LookupBarcodeReq 条形码精确查询（扫码直达：返回命中的商品与具体 SKU）
message LookupBarcodeReq {
  int64 community_id = 1;
  string barcode = 2;
}

// LookupBarcodeResp 条形码查询结果
// 未命中时 product / sku 均为空；多规格商品命中时 sku 为具体规格
message LookupBarcodeResp {
  ProductInfo product = 1;
  SkuInfo sku = 2;
}

// SpecValueInfo 规格值信息（详情返回）
message SpecValueInfo {
  int64 id = 1;
  string name = 2;
  int32 sort = 3;
}

// SpecInfo 规格名信息（详情返回，含层级规格值）
message SpecInfo {
  int64 id = 1;
  string name = 2;
  int32 sort = 3;
  repeated SpecValueInfo values = 4;
}

// --- SKU ---
message ListSkusReq {
  int64 product_id = 1;
  // 公开口径：true 时只抹经营字段（cost_price/safety_stock，见 service.RedactPublicSku）——
  // 能列出该商品就说明它在售，价格本就要展示。C 端出口恒传 true；
  // 管理端库存工作台/进销存需要成本价，不传
  bool public_view = 2;
}

message GetSkuByIdReq {
  int64 id = 1;
  // 操作者社区语境：>0 时服务端强校验 SKU 归属，防止跨社区读取（超管传 0 放行）
  int64 community_id = 2;
  // 公开口径：同 ListSkusReq，只抹经营字段；商品上下架状态的价格脱敏由详情出口收口
  // （本出口只拿到 SKU，不掌握商品状态）
  bool public_view = 3;

}

message CreateSkuReq {
  int64 product_id = 1;
  int64 community_id = 2; // 仅作调用方的社区语境：落库的 SKU 归属社区取商品的 community_id（2026-10-26 加固，先校验商品存在/未软删且在权限范围内）
  string sku_code = 3;
  string spec_name = 4;
  string spec_json = 5;
  double price = 6;
  double cost_price = 7;
  int32 stock = 8;
  int32 safety_stock = 9;
  string image = 10;
  string barcode = 11; // 商品条形码（口径同 UpdateSkuReq：先校验格式与社区内占用，再进事务写入）
}

message UpdateSkuReq {
  int64 id = 1;
  string spec_name = 2;
  string spec_json = 3;
  double price = 4;
  double cost_price = 5;
  int32 safety_stock = 6;
  string image = 7;
  // 目标状态：1 启用，0 禁用（SKU 口径 status 只有 0/1）。
  // 必须声明为 optional：proto3 裸 int32 的「未传」与「传 0」都是 0，
  // 服务端无法区分「不改状态」和「要禁用」，历史上因此永远改不出禁用态
  optional int32 status = 8;
  string barcode = 9; // 商品条形码（同社区内唯一，撞占用报 InvalidArgument 并点出持有该条码的商品；改码前置校验，校验失败不落库）
  // 操作者社区语境：>0 时服务端强校验 SKU 归属，防止跨社区改价/改状态（超管传 0 放行）
  int64 community_id = 10;
}

message SkuInfo {
  int64 id = 1;
  int64 product_id = 2;
  int64 community_id = 3;
  string sku_code = 4;
  string spec_name = 5;
  string spec_json = 6;
  double price = 7;
  double cost_price = 8;
  int32 stock = 9;
  int32 safety_stock = 10;
  string image = 11;
  int32 status = 12;
  string spec_value_ids = 13; // 逗号分隔的规格值 ID（多规格组合键，单规格为空）
  double original_price = 14; // 划线价（原价，0 表示不展示）
  string barcode = 15;       // 商品条形码（纯数字 6-20 位，社区内在售 SKU 间唯一，空表示未设置）
  string product_name = 16;  // 冗余商品名（仅 SearchSkus 填充，其余 RPC 为空）

}

message ListSkusResp {
  repeated SkuInfo list = 1;
}


// SearchSkusReq SKU 级搜索请求（keyword 为空 = 全量分页，即库存列表）
message SearchSkusReq {
  int64 community_id = 1;
  string keyword = 2; // 匹配商品名 / SKU 编码 / 条形码
  common.Pagination pagination = 3;
}

message SearchSkusResp {
  repeated SkuInfo list = 1;
  common.PaginationResponse pagination = 2;
}

// BatchGetSkusReq SKU 批量查询（下单/收银等按明细取价取库存，替代逐行 GetSkuById 的 N 次往返）
// community_id 为操作者社区语境：>0 时逐个校验 SKU 归属，任一越权即整体拒绝
message BatchGetSkusReq {
  repeated int64 ids = 1;
  int64 community_id = 2;
  // 容忍缺失：true 时任一 ID 不存在（含被删除）或归属越权只从结果中剔除，不再整体报错，
  // 供「逐项判定、部分可用」的调用方使用（如 C 端再来一单：一个失效规格不应阻断整单复购）。
  // 启用/停售状态与库存原样返回，可售判定交由调用方；默认 false 保持交易入口的整体拒绝口径
  bool tolerate_missing = 3;
}

message BatchGetSkusResp {
  repeated SkuInfo list = 1;
}

// BatchGetProductsReq 商品批量查询（下单按明细取商品名/封面/分类，替代逐行 GetProduct）
message BatchGetProductsReq {
  repeated int64 ids = 1;
  int64 community_id = 2;
  // 可售校验（P1-4）：true 时任一商品非上架态（status≠1）即整体报错，
  // 供下单入口收口「可售」判定，与 GetByBarcode 的 status=1 口径一致
  bool require_sellable = 3;
  // 容忍缺失：语义同 BatchGetSkusReq.tolerate_missing——缺失/越权项只剔除不整体报错，
  // 上架状态原样返回交由调用方判定；与 require_sellable 互斥，同时为 true 时本字段优先
  bool tolerate_missing = 4;
}

message BatchGetProductsResp {
  repeated ProductInfo list = 1;
}

// --- 库存 ---
message DeductStockReq {
  int64 sku_id = 1;
  int32 quantity = 2;
  int64 order_id = 3;
}

message DeductStockResp {
  bool success = 1;
  int32 remain_stock = 2;
}

message ReleaseStockReq {
  int64 sku_id = 1;
  int32 quantity = 2;
  int64 order_id = 3;
  // 幂等业务号：同一 biz_no 的重复释放只生效一次；不同业务（多笔部分退款/取消/超时关单）
  // 各自携带不同 biz_no，避免同一订单同一 SKU 的多次部分释放被幂等约束吞掉
  string biz_no = 4;
}


// StockItemQty 库存操作明细项
message StockItemQty {
  int64 sku_id = 1;
  int32 quantity = 2;
}

// BatchDeductStockReq 批量扣减库存：整单明细在同一事务内按 SKU 主键升序加锁扣减，
// 幂等键仍为 order_id + sku_id + op_type（+ biz_no），整单要么全扣要么全不扣
message BatchDeductStockReq {
  int64 order_id = 1;
  repeated StockItemQty items = 2;
}

message BatchDeductStockResp {
  bool success = 1;
}

// BatchReleaseStockReq 批量释放库存（取消/退款/超时关单整单回补）
message BatchReleaseStockReq {
  int64 order_id = 1;
  string biz_no = 2;
  repeated StockItemQty items = 3;
}

// BatchDeductFlashSaleStockReq 批量扣减秒杀独立库存：无生效秒杀的 SKU 自动 no-op
message BatchDeductFlashSaleStockReq {
  int64 order_id = 1;
  repeated StockItemQty items = 2;
}

// BatchReleaseFlashSaleStockReq 批量回补秒杀独立库存（biz_no 为空时由服务端按 cancel:{order_id} 生成）
message BatchReleaseFlashSaleStockReq {
  int64 order_id = 1;
  repeated StockItemQty items = 2;
  string biz_no = 3;
}

// --- 秒杀库存 ---
message DeductFlashSaleStockReq {
  int64 order_id = 1;
  int64 sku_id = 2;
  int32 quantity = 3;
}

message ReleaseFlashSaleStockReq {
  int64 order_id = 1;
  int64 sku_id = 2;
  int32 quantity = 3;
  string biz_no = 4; // 幂等业务号（退款单号/cancel:{orderID} 等，与主库存释放同口径）
}

// --- 销量（净成交口径：支付成功累加，取消/退款回退） ---
// AdjustSalesCountItem 销量调整项（SKU 维度）
message AdjustSalesCountItem {
  int64 sku_id = 1;
  // 销量变化量：正=累加（支付成功），负=回退（取消/退款）
  int32 delta = 2;
}

// AdjustSalesCountReq 批量调整销量（SKU 行与所属商品汇总行同事务同步累加，
// 结果向下钳制为 0 防负数；无幂等键，调用方必须在主事务提交成功后调用）
message AdjustSalesCountReq {
  repeated AdjustSalesCountItem items = 1;
}

message ListStockRecordsReq {
  int64 community_id = 1;
  int64 sku_id = 2;
  int32 type = 3;
  string start_date = 4;
  string end_date = 5;
  common.Pagination pagination = 6;
}

message StockRecordInfo {
  int64 id = 1;
  int64 sku_id = 2;
  int32 type = 3;
  int32 quantity = 4;
  int32 before_stock = 5;
  int32 after_stock = 6;
  string batch_no = 7;
  string production_date = 8;
  string expire_date = 9;
  int64 related_order_id = 10;
  int64 operator_id = 11;
  string remark = 12;
  string created_at = 13;
  int64 batch_id = 14; // 批次 ID（0=无批次语境）
  string ref_no = 15;  // 来源单据号（入库单号/出库单号等）

}

message ListStockRecordsResp {
  repeated StockRecordInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message GetStockWarningsReq {
  int64 community_id = 1;
}

message StockWarningInfo {
  int64 sku_id = 1;
  string sku_code = 2;
  string spec_name = 3;
  string product_name = 4;
  int32 stock = 5;
  int32 safety_stock = 6;
  int32 warning_type = 7;
  string expire_date = 8;
}

message ListStockWarningsResp {
  repeated StockWarningInfo list = 1;
}

message PurchaseOrderItem {
  int64 sku_id = 1;
  string sku_code = 2;
  string spec_name = 3;
  string product_name = 4;
  int32 current_stock = 5;
  int32 safety_stock = 6;
  int32 suggest_quantity = 7;
}

message GeneratePurchaseOrderReq {
  int64 community_id = 1;
  repeated int64 sku_ids = 2;          // 指定仅生成这些 SKU 的补货建议（库存工作台勾选补货）；为空时生成全部低库存建议
}

message GeneratePurchaseOrderResp {
  repeated PurchaseOrderItem items = 1;
}

// --- 供应商 ---
message SupplierInfo {
  int64 id = 1;
  int64 community_id = 2;
  string name = 3;
  string contact_name = 4;
  string phone = 5;
  string address = 6;
  string remark = 7;
  int32 status = 8;
  string created_at = 9;
}

message ListSuppliersReq {
  int64 community_id = 1;
  string keyword = 2;
  int32 status = 3; // 0=全部
  common.Pagination pagination = 4;
}

message ListSuppliersResp {
  repeated SupplierInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message CreateSupplierReq {
  int64 community_id = 1;
  string name = 2;
  string contact_name = 3;
  string phone = 4;
  string address = 5;
  string remark = 6;
}

message UpdateSupplierReq {
  int64 id = 1;
  int64 community_id = 2; // 归属校验语境
  string name = 3;
  string contact_name = 4;
  string phone = 5;
  string address = 6;
  string remark = 7;
  int32 status = 8;
}

message DeleteSupplierReq {
  int64 id = 1;
  int64 community_id = 2;
}

// --- 采购单 ---
message PurchaseOrderItemReq {
  int64 sku_id = 1;
  int32 quantity = 2;
  double purchase_price = 3;
}

message CreatePurchaseOrderReq {
  int64 community_id = 1;
  int64 supplier_id = 2;
  int64 operator_id = 3;
  string remark = 4;
  bool submit_now = 5; // true=创建后直接下单（跳过草稿态）
  repeated PurchaseOrderItemReq items = 6;
}

message UpdatePurchaseOrderReq {
  int64 id = 1;
  int64 community_id = 2;
  int64 supplier_id = 3;
  string remark = 4;
  repeated PurchaseOrderItemReq items = 5; // 全量替换，仅草稿态可改
}

message SubmitPurchaseOrderReq {
  int64 id = 1;
  int64 community_id = 2;
}

message CancelPurchaseOrderReq {
  int64 id = 1;
  int64 community_id = 2;
}

message PurchaseOrderItemInfo {
  int64 id = 1;
  int64 sku_id = 2;
  string sku_code = 3;
  string spec_name = 4;
  string product_name = 5;
  int32 quantity = 6;
  int32 received_quantity = 7;
  double purchase_price = 8;
}

message PurchaseOrderInfo {
  int64 id = 1;
  string purchase_no = 2;
  int64 community_id = 3;
  int64 supplier_id = 4;
  string supplier_name = 5;
  int32 status = 6;
  double total_amount = 7;
  string remark = 8;
  int64 operator_id = 9;
  string created_at = 10;
  string updated_at = 11;
}

message PurchaseOrderDetail {
  PurchaseOrderInfo info = 1;
  repeated PurchaseOrderItemInfo items = 2;
}

message ListPurchaseOrdersReq {
  int64 community_id = 1;
  int32 status = 2; // 0=全部
  int64 supplier_id = 3;
  string keyword = 4; // 按采购单号模糊
  common.Pagination pagination = 5;
}

message ListPurchaseOrdersResp {
  repeated PurchaseOrderInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message GetPurchaseOrderReq {
  int64 id = 1;
}

// --- 入库单 ---
message StockInOrderItemReq {
  int64 sku_id = 1;
  int32 quantity = 2;
  double unit_cost = 3; // 入库单价（成本），0 时回落 SKU 当前成本价
  string batch_no = 4;
  string production_date = 5;
  string expire_date = 6;
}

message CreateStockInOrderReq {
  int64 community_id = 1;
  int64 supplier_id = 2;
  int64 purchase_order_id = 3; // 0=直接入库
  int64 operator_id = 4;
  string remark = 5;
  repeated StockInOrderItemReq items = 6;
}

message StockInOrderInfo {
  int64 id = 1;
  string in_no = 2;
  int64 community_id = 3;
  int64 supplier_id = 4;
  string supplier_name = 5;
  int64 purchase_order_id = 6;
  string purchase_no = 7;
  int32 type = 8;
  double total_amount = 9;
  int64 operator_id = 10;
  string remark = 11;
  string created_at = 12;
}

message StockInOrderItemInfo {
  int64 id = 1;
  int64 sku_id = 2;
  string sku_code = 3;
  string product_name = 4;
  string spec_name = 5;
  int32 quantity = 6;
  double unit_cost = 7;
  string batch_no = 8;
  string production_date = 9;
  string expire_date = 10;
  int64 batch_id = 11;
}

message StockInOrderDetail {
  StockInOrderInfo info = 1;
  repeated StockInOrderItemInfo items = 2;
}

message ListStockInOrdersReq {
  int64 community_id = 1;
  int64 purchase_order_id = 2;
  string start_date = 3;
  string end_date = 4;
  common.Pagination pagination = 5;
}

message ListStockInOrdersResp {
  repeated StockInOrderInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message GetStockInOrderReq {
  int64 id = 1;
  // 操作者社区语境：>0 时服务端强校验入库单归属（超管传 0 放行）
  int64 community_id = 2;
}

// --- 出库单 ---
message StockOutOrderItemReq {
  int64 sku_id = 1;
  int32 quantity = 2;
}

message CreateStockOutOrderReq {
  int64 community_id = 1;
  int32 type = 2; // 1报损 2领用 3调整出库
  int64 operator_id = 3;
  string remark = 4;
  repeated StockOutOrderItemReq items = 5;
}

message StockOutOrderInfo {
  int64 id = 1;
  string out_no = 2;
  int64 community_id = 3;
  int32 type = 4;
  int64 operator_id = 5;
  string remark = 6;
  string created_at = 7;
}

message StockOutOrderItemInfo {
  int64 id = 1;
  int64 sku_id = 2;
  string sku_code = 3;
  string product_name = 4;
  string spec_name = 5;
  int32 quantity = 6;
}

message StockOutOrderDetail {
  StockOutOrderInfo info = 1;
  repeated StockOutOrderItemInfo items = 2;
}

message ListStockOutOrdersReq {
  int64 community_id = 1;
  int32 type = 2; // 0=全部
  string start_date = 3;
  string end_date = 4;
  common.Pagination pagination = 5;
}

message ListStockOutOrdersResp {
  repeated StockOutOrderInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message GetStockOutOrderReq {
  int64 id = 1;
  // 操作者社区语境：>0 时服务端强校验出库单归属（超管传 0 放行）
  int64 community_id = 2;
}

// --- 批次库存 ---
message StockBatchInfo {
  int64 id = 1;
  int64 sku_id = 2;
  string sku_code = 3;
  string product_name = 4;
  string spec_name = 5;
  string batch_no = 6;
  string production_date = 7;
  string expire_date = 8;
  int32 quantity = 9;
  double unit_cost = 10;
  int32 status = 11;
  int32 source_type = 12;
  string ref_no = 13;
  string created_at = 14;
}

message ListStockBatchesReq {
  int64 community_id = 1;
  int64 sku_id = 2;
  string keyword = 3; // 商品名/SKU 编码/批次号模糊
  bool in_stock_only = 4; // 仅在库（quantity>0）
  common.Pagination pagination = 5;
}

message ListStockBatchesResp {
  repeated StockBatchInfo list = 1;
  common.PaginationResponse pagination = 2;
}

// --- 进销存报表 ---
// InventorySummaryItem 进销存汇总行：期初 + 入 - 出 = 期末（口径取自 stock_records）
message InventorySummaryItem {
  int64 sku_id = 1;
  string sku_code = 2;
  string spec_name = 3;
  string product_name = 4;
  int32 opening_stock = 5;
  int32 in_quantity = 6;  // 期间入库合计（type=1）
  int32 out_quantity = 7; // 期间出库合计（type=2 绝对值）
  int32 closing_stock = 8;
  double cost_price = 9;  // 当前移动加权成本
  double price = 10;      // 当前售价
}

message GetInventorySummaryReportReq {
  int64 community_id = 1;
  string start_date = 2;
  string end_date = 3;
  string keyword = 4;
  common.Pagination pagination = 5;
}

message GetInventorySummaryReportResp {
  repeated InventorySummaryItem list = 1;
  common.PaginationResponse pagination = 2;
}

// ProfitReportItem 毛利分析行（近似口径：期间销售出库数量 × 当前售价/当前成本）
message ProfitReportItem {
  int64 sku_id = 1;
  string sku_code = 2;
  string spec_name = 3;
  string product_name = 4;
  int32 quantity = 5;       // 期间销售数量（type=2 且无出库单号的记录）
  double sales_amount = 6;  // 销售额（当前售价估算）
  double cost_amount = 7;   // 销售成本（当前成本估算）
  double profit = 8;
}

message GetProfitReportReq {
  int64 community_id = 1;
  string start_date = 2;
  string end_date = 3;
  string keyword = 4;
  common.Pagination pagination = 5;
}

message GetProfitReportResp {
  repeated ProfitReportItem list = 1;
  common.PaginationResponse pagination = 2;
}

// --- 盘点 ---
message CreateStockCheckReq {
  int64 community_id = 1;
  int64 operator_id = 2;
  string remark = 3;
}

message StockCheckItem {
  int64 sku_id = 1;
  int32 actual_stock = 2; // 实盘库存：0 是合法结果（该 SKU 已清空），负数拒绝；同一单内 sku_id 不能重复
}

message SubmitStockCheckReq {
  int64 id = 1;
  repeated StockCheckItem items = 2; // 空明细拒绝
  // 操作者所属社区：>0 时服务端强校验盘点单归属，防止跨社区提交（与订单/退款族同口径）
  int64 community_id = 3;

}

message StockCheckInfo {
  int64 id = 1;
  string check_no = 2;
  int32 status = 3;
  int32 total_items = 4;
  int32 diff_items = 5;
  string remark = 6;
  string created_at = 7;
  int64 community_id = 8;

}

message ListStockChecksReq {
  int64 community_id = 1;
  common.Pagination pagination = 2;
}

message ListStockChecksResp {
  repeated StockCheckInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message GetStockCheckReq {
  int64 id = 1;
  // 操作者所属社区：>0 时服务端强校验盘点单归属
  int64 community_id = 2;

}

message StockCheckDetailItem {
  int64 id = 1;
  int64 sku_id = 2;
  string sku_code = 3;
  string product_name = 4;
  string spec_name = 5;
  int32 system_stock = 6;
  int32 actual_stock = 7;
  int32 diff_quantity = 8;
  string created_at = 9;
}

message StockCheckDetail {
  StockCheckInfo info = 1;
  repeated StockCheckDetailItem items = 2;
}

// --- 定价 ---
message SetPricingReq {
  int64 community_id = 1;
  int64 sku_id = 2;
  double price = 3;
}

message PricingInfo {
  int64 id = 1;
  int64 community_id = 2;
  string community_name = 3;
  int64 sku_id = 4;
  double price = 5;
}

message ListPricingReq {
  int64 sku_id = 1;
  int64 community_id = 2;
}

message ListPricingResp {
  repeated PricingInfo list = 1;
}

message GetReferencePricingReq {
  int64 sku_id = 1;
  int64 exclude_community_id = 2;
}

// --- 营销 ---
message ListCouponsReq {
  int64 community_id = 1;
  int32 status = 2;
  // 仅领券中心可见（is_center_visible=1 且 publish_status=1），C 端领券中心使用
  bool center_visible_only = 3;
}

message GetCouponReq {
  int64 id = 1;
  // 操作者社区语境：>0 时服务端强校验券归属社区（超管传 0 放行）
  int64 community_id = 2;

}

// 优惠券商品范围项
// scope_type：1全场 2指定分类 3指定商品 4排除分类 5排除商品；
// 无范围行=全场通用；指定与排除可组合（先取指定集合再剔除排除项）
message CouponScopeReq {
  int32 scope_type = 1;
  int64 category_id = 2;
  int64 product_id = 3;
}

message CreateCouponReq {
  int64 community_id = 1;
  string name = 2;
  int32 type = 3;
  double threshold = 4;
  double discount_value = 5;
  int32 total_count = 6;
  int32 per_user_limit = 7;
  string start_time = 8;
  string end_time = 9;
  int32 valid_days = 10;
  int32 publish_status = 11;      // 0草稿 1已发布 2已下架，创建默认草稿
  optional bool is_center_visible = 12; // 是否进入领券中心；缺省=进领券中心
  int32 expire_type = 13;         // 1领取后N天 2指定可用范围
  string use_start_time = 14;     // expire_type=2 的可用范围起点
  string use_end_time = 15;       // expire_type=2 的可用范围终点
  string rule_text = 16;          // 规则文案
  repeated CouponScopeReq scopes = 17; // 商品范围，全量提交
}

message UpdateCouponReq {
  int64 id = 1;
  string name = 2;
  optional int32 status = 3; // 0禁用 1启用；缺省=不修改（int32 零值歧义规避）
  double threshold = 4;
  double discount_value = 5;
  int32 total_count = 6;
  int32 per_user_limit = 7;
  string start_time = 8;
  string end_time = 9;
  int32 valid_days = 10;
  int32 type = 11;
  int32 publish_status = 12;    // 发布/下架状态机
  optional int32 grant_status = 13; // 0停止发放 1发放中；缺省=不修改
  optional bool is_center_visible = 14; // 缺省=不修改该字段
  int32 expire_type = 15;
  string use_start_time = 16;
  string use_end_time = 17;
  string rule_text = 18;
  repeated CouponScopeReq scopes = 19; // 全量替换；nil=不修改
  // 操作者社区语境：>0 时服务端强校验券归属社区，防止跨社区改券（超管传 0 放行）
  int64 community_id = 20;

}

message DeleteCouponReq {
  int64 id = 1;
  // 操作者社区语境（>0 时校验券归属社区，跨社区删除直接拒绝）
  int64 community_id = 2;
}

// ReleaseCouponReq 取消订单时回补用户券
message ReleaseCouponReq {
  int64 order_id = 1; // 订单 ID（按 order_id 定位已核销的 user_coupon）
}

message CouponScopeInfo {
  int64 id = 1;
  int32 scope_type = 2;
  int64 category_id = 3;
  string category_name = 4;
  int64 product_id = 5;
  string product_name = 6;
}

message CouponInfo {
  int64 id = 1;
  int64 community_id = 2;
  string name = 3;
  int32 type = 4;
  double threshold = 5;
  double discount_value = 6;
  int32 total_count = 7;
  int32 received_count = 8;
  int32 used_count = 9;
  int32 per_user_limit = 10;
  string start_time = 11;
  string end_time = 12;
  int32 valid_days = 13;
  int32 status = 14;
  int32 publish_status = 15;    // 0草稿 1已发布 2已下架
  int32 grant_status = 16;      // 0停止发放 1发放中
  bool is_center_visible = 17;
  int32 expire_type = 18;       // 1领取后N天 2指定可用范围
  string use_start_time = 19;
  string use_end_time = 20;
  string rule_text = 21;
  repeated CouponScopeInfo scopes = 22;
}

message ListCouponsResp {
  repeated CouponInfo list = 1;
}

message ReceiveCouponReq {
  int64 coupon_id = 1;
  int64 user_id = 2;
}

message ListUserCouponsReq {
  int64 user_id = 1;
  int32 status = 2;
}

message UserCouponInfo {
  int64 id = 1;
  int64 coupon_id = 2;
  string name = 3;
  int32 type = 4;
  double threshold = 5;
  double discount_value = 6;
  int32 status = 7;
  string expire_at = 8;
}

message ListUserCouponsResp {
  repeated UserCouponInfo list = 1;
}

message UseCouponReq {
  int64 user_coupon_id = 1;
  int64 order_id = 2;
}

// GrantCouponReq 后台手动发放优惠券
message GrantCouponReq {
  int64 coupon_id = 1;
  // 发放目标会员（批量；逐人独立校验，单人不合格不影响其他人）
  repeated int64 user_ids = 2;
  // 发放操作者（管理员）ID 与用户名快照
  int64 operator_id = 3;
  string operator_name = 4;
  // 操作者社区语境（>0 时校验券归属社区，跨社区发放直接拒绝）
  int64 community_id = 5;
}

// CouponGrantFailure 单个会员发放失败原因
message CouponGrantFailure {
  int64 user_id = 1;
  string reason = 2;
}

message GrantCouponResp {
  // 实际成功发放人数
  int32 granted_count = 1;
  // 发放失败的会员及原因（超限/停发/下架等）
  repeated CouponGrantFailure failures = 2;
}

// ListCouponRecordsReq 后台领取/发放记录查询
message ListCouponRecordsReq {
  // 按券筛选（0=该社区全部券的记录）
  int64 coupon_id = 1;
  // 按会员筛选（0=全部）
  int64 user_id = 2;
  // 状态筛选：1未使用 2已使用 3已过期（0=全部）
  int32 status = 3;
  // 来源筛选：1用户自领 2后台发放（0=全部）
  int32 source = 4;
  // 社区语境（用于把记录范围限定在本社区发放的券，防跨社区泄露）
  int64 community_id = 5;
  common.Pagination pagination = 6;
}

message CouponRecordInfo {
  int64 id = 1;
  int64 coupon_id = 2;
  string coupon_name = 3;
  int32 type = 4;
  double threshold = 5;
  double discount_value = 6;
  int64 user_id = 7;
  // 会员昵称/手机号由 gateway 按 user_id 批量回填
  string user_nickname = 8;
  string user_phone = 9;
  int32 status = 10;        // 1未使用 2已使用 3已过期
  int64 order_id = 11;
  int32 source = 12;        // 1用户自领 2后台发放
  int64 operator_id = 13;
  string operator_name = 14;
  string expire_at = 15;
  string created_at = 16;   // 领取/发放时间
}

message ListCouponRecordsResp {
  repeated CouponRecordInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message ListFlashSalesReq {
  int64 community_id = 1;
}

message GetFlashSaleReq {
  int64 id = 1;
  // 操作者社区语境：>0 时服务端强校验活动归属社区（超管传 0 放行）
  int64 community_id = 2;

}

message CreateFlashSaleReq {
  int64 community_id = 1;
  string name = 2;
  string start_time = 3;
  string end_time = 4;
  repeated FlashSaleItemReq items = 5;
}

message UpdateFlashSaleReq {
  int64 id = 1;
  string name = 2;
  string start_time = 3;
  string end_time = 4;
  int32 status = 5;
  // 操作者社区语境：>0 时服务端强校验活动归属社区，防止跨社区改秒杀（超管传 0 放行）
  int64 community_id = 6;

}

message FlashSaleItemReq {
  int64 sku_id = 1;
  double flash_price = 2;
  int32 stock = 3;
  int32 limit_per_user = 4;
}

message FlashSaleInfo {
  int64 id = 1;
  int64 community_id = 2;
  string name = 3;
  string start_time = 4;
  string end_time = 5;
  int32 status = 6;
}

message FlashSaleDetail {
  FlashSaleInfo info = 1;
  repeated FlashSaleItemInfo items = 2;
}

message FlashSaleItemInfo {
  int64 id = 1;
  int64 sku_id = 2;
  string sku_code = 3;
  string product_name = 4;
  string spec_name = 5;
  double flash_price = 6;
  double original_price = 7;
  int32 stock = 8;
  int32 limit_per_user = 9;
  // 商品 ID：由服务端随 SKU 一并回填，避免调用方为拿 product_id 逐行再查一次 SKU
  int64 product_id = 10;
  // 商品封面图（已按附件访问域名拼接为完整地址）：C 端列表展示用
  string product_image = 11;
  // 已售数量（flash_sale_items.sold_count 真实值，扣减秒杀库存时累加）
  int32 sold = 12;
}

message ListFlashSalesResp {
  repeated FlashSaleInfo list = 1;
}

message ListFullReductionsReq {
  int64 community_id = 1;
}

message CreateFullReductionReq {
  int64 community_id = 1;
  string name = 2;
  double threshold = 3;
  double reduce_amount = 4;
  string start_time = 5;
  string end_time = 6;
}

message UpdateFullReductionReq {
  int64 id = 1;
  string name = 2;
  double threshold = 3;
  double reduce_amount = 4;
  string start_time = 5;
  string end_time = 6;
  int32 status = 7;
  // 操作者社区语境（>0 时校验活动归属社区，跨社区修改直接拒绝）
  int64 community_id = 8;
}

message FullReductionInfo {
  int64 id = 1;
  int64 community_id = 2;
  string name = 3;
  double threshold = 4;
  double reduce_amount = 5;
  string start_time = 6;
  string end_time = 7;
  int32 status = 8;
}

message ListFullReductionsResp {
  repeated FullReductionInfo list = 1;
}

// --- 满额免配送费（FreeShipping）---
// 活动语义：配送订单中「参与免配送费的商品金额合计 >= threshold」则免配送费；
// 排除范围（scope）按分类/商品指定，被排除商品的金额不计入门槛。
// scope_type 复用优惠券范围枚举：4=排除分类 5=排除商品（当前仅排除语义，保留扩展）。
message FreeShippingScopeReq {
  int32 scope_type = 1; // 4=排除分类 5=排除商品
  int64 category_id = 2; // scope_type=4 时有效
  int64 product_id = 3;  // scope_type=5 时有效
}

message CreateFreeShippingReq {
  int64 community_id = 1;
  string name = 2;
  double threshold = 3;   // 免配送费门槛（参与商品额）
  string start_time = 4;
  string end_time = 5;
  repeated FreeShippingScopeReq scopes = 6; // 排除范围，全量提交
}

message UpdateFreeShippingReq {
  int64 id = 1;
  string name = 2;
  double threshold = 3;
  string start_time = 4;
  string end_time = 5;
  int32 status = 6;
  repeated FreeShippingScopeReq scopes = 7; // 排除范围，全量替换
  // 操作者社区语境（>0 时校验活动归属社区，跨社区修改直接拒绝）
  int64 community_id = 8;
}

message FreeShippingScopeInfo {
  int64 id = 1;
  int32 scope_type = 2;
  int64 category_id = 3;
  string category_name = 4;
  int64 product_id = 5;
  string product_name = 6;
}

message FreeShippingInfo {
  int64 id = 1;
  int64 community_id = 2;
  string name = 3;
  double threshold = 4;
  string start_time = 5;
  string end_time = 6;
  int32 status = 7;
  repeated FreeShippingScopeInfo scopes = 8;
}

message ListFreeShippingsReq {
  int64 community_id = 1;
}

message ListFreeShippingsResp {
  repeated FreeShippingInfo list = 1;
}

// 查询当前社区「正在生效」的免配送费活动（启用 + 时效内，门槛最优优先）；
// 无生效活动时 activity 为空
message GetActiveFreeShippingReq {
  int64 community_id = 1;
}

message GetActiveFreeShippingResp {
  FreeShippingInfo activity = 1;
}

// --- Banner ---
message ListBannersReq {
  int64 community_id = 1;
  int32 position = 2;
  bool is_admin = 3;
}

message BannerInfo {
  int64 id = 1;
  int64 community_id = 2;
  string title = 3;
  string image_url = 4;
  string link_url = 5;
  int32 position = 6;
  int32 sort = 7;
  int32 status = 8;
  string start_time = 9;
  string end_time = 10;
}

message ListBannersResp {
  repeated BannerInfo list = 1;
}

message CreateBannerReq {
  int64 community_id = 1;
  string title = 2;
  string image_url = 3;
  string link_url = 4;
  int32 position = 5;
  int32 sort = 6;
  string start_time = 7;
  string end_time = 8;
}

message UpdateBannerReq {
  int64 id = 1;
  string title = 2;
  string image_url = 3;
  string link_url = 4;
  int32 position = 5;
  int32 sort = 6;
  int32 status = 7;
  string start_time = 8;
  string end_time = 9;
  // 适用社区（optional 显式传参才更新；0=全部社区通用）
  optional int64 community_id = 10;
  // 操作者社区语境：>0 时服务端强校验 banner 归属，社区管理员不得改全局 banner 与其他社区数据
  // 与上面的 community_id（业务字段"适用社区"）语义不同，故独立命名
  int64 scope_community_id = 11;

}

message DeleteBannerReq {
  int64 id = 1;
  // 操作者社区语境：>0 时服务端强校验 banner 归属（超管传 0 放行）
  int64 community_id = 2;

}

// --- 附件 ---
// AttachmentConfig 附件存储配置（数据库单行存储，id 固定为 1）
// 读写均通过 product 服务，gateway 不直接访问数据库
// 注意：oss_access_key_secret 写入时为空或掩码值表示保留原值，读取时服务端已脱敏
message AttachmentConfig {
  string storage_type = 1;             // 存储类型：local / oss（预留 cos）
  string visit_domain = 2;             // 自定义访问域名，选填；local 未配置时按 /uploads/ 站内路径兜底、随访问域名解析（仅站点域名/SITE_BASE_URL 等显式配置联动写入，请求现推域名不入库）、OSS 用 bucket 域名
  string oss_access_key_id = 3;        // 阿里云 AccessKey ID
  string oss_access_key_secret = 4;    // 阿里云 AccessKey Secret（读取时脱敏）
  string oss_endpoint = 5;             // OSS Endpoint，如 oss-cn-shanghai.aliyuncs.com
  string oss_bucket_name = 6;          // OSS Bucket 名称
  string oss_custom_visit_domain = 7;  // OSS 自定义访问域名（CDN），可空
  int64 max_size = 8;                  // 图片大小上限（字节），0 表示使用默认 5MB
  string updated_at = 9;               // 更新时间，兼作版本号
  string local_dir = 10;               // 本地存储目录名（落盘目录与 URL 路径段共用，空回默认 uploads）；只读字段，保存接口不受理

}

// UploadImageLocalReq 本地模式图片上传请求
// gateway 接收 multipart 后将文件内容转发至 product 服务落盘并写附件记录
message UploadImageLocalReq {
  int64 admin_id = 1;      // 上传管理员 ID，写附件记录归属
  string filename = 2;     // 原始文件名，需含扩展名
  string content_type = 3; // 文件 MIME 类型
  bytes content = 4;       // 文件内容
  bool is_display = 5;     // 是否在附件选择器中显示：后台上传传 true；客户端用户上传不传（默认 false 不显示）
}

// AttachmentPresignReq 预签名请求（OSS 直传模式）
message AttachmentPresignReq {
  int64 admin_id = 1;      // 上传管理员 ID，写附件记录归属
  string filename = 2;     // 原始文件名，需含扩展名
  string md5 = 3;          // 文件内容 MD5，32 位十六进制（大小写不敏感），用于秒传
  string content_type = 4; // 文件 MIME 类型，为空时由扩展名推导
  int64 size = 5;          // 文件大小（字节）
  int32 width = 6;         // 图片宽度（像素）
  int32 height = 7;        // 图片高度（像素）
  bool is_display = 8;     // 是否在附件选择器中显示：后台上传传 true；客户端用户上传不传（默认 false 不显示）
}

// AttachmentPresignResp 预签名响应
// hit=true 表示秒传命中，直接使用 final_url；否则用 url + headers 以 PUT 直传 OSS
message AttachmentPresignResp {
  string attach_id = 1;             // 附件记录 ID（数字字符串），confirm 时回传
  bool hit = 2;                     // 是否命中秒传
  string url = 3;                   // 预签名上传 URL（秒传命中时为空）
  string method = 4;                // 上传方法，固定 PUT（秒传命中时为空）
  map<string, string> headers = 5;  // 上传时必须携带的请求头（秒传命中时为空）
  string final_url = 6;             // 最终可访问 URL
  string remote_path = 7;           // OSS 对象相对路径
}

// AttachmentConfirmReq 确认上传请求
// md5 必传且校验格式；confirm 成功后才补写记录 md5，保证「md5 非空 ⇒ 对象已上传」不变量
// attach_id 为数字可枚举，admin_id 必须与记录归属一致才允许补写
message AttachmentConfirmReq {
  int64 admin_id = 1;   // 上传管理员 ID，须与附件记录归属一致
  string attach_id = 2; // presign 返回的附件记录 ID
  string md5 = 3;       // 文件内容 MD5，32 位十六进制
  int64 size = 4;       // 文件大小（字节）
  int32 width = 5;      // 图片宽度（像素）
  int32 height = 6;     // 图片高度（像素）
}

// AttachmentInfo 附件信息
// local 模式 url 为空，由 gateway 按请求域名动态拼接后返回前端
message AttachmentInfo {
  string attach_id = 1;   // 附件记录 ID（数字字符串）
  string url = 2;         // 完整访问 URL（local 模式为空，gateway 动态拼接）
  string remote_path = 3; // 对象相对路径（OSS 为 OSS Key，本地为附件目录下相对路径）
  int64 size = 4;         // 文件大小（字节）
  int32 width = 5;        // 图片宽度（像素）
  int32 height = 6;       // 图片高度（像素）
  string name = 7;        // 原始文件名（附件选择器展示用）
  string type = 8;        // 文件 MIME 类型
  string created_at = 9;  // 上传时间（YYYY-MM-DD HH:mm:ss）
}

// ListAttachmentsReq 附件选择器分页查询请求
// 可见性按上传管理员隔离：gateway 注入 admin_id，服务端仅返回本人上传且 is_display=1 的图片附件
message ListAttachmentsReq {
  int64 admin_id = 1;     // 上传管理员 ID（gateway 从登录态注入）
  string keyword = 2;     // 关键字，模糊匹配原始文件名 name 或路径 path
  string start_time = 3;  // 上传时间范围起（YYYY-MM-DD HH:mm:ss，含）
  string end_time = 4;    // 上传时间范围止（YYYY-MM-DD HH:mm:ss，含）
  int32 page = 5;         // 页码，从 1 开始
  int32 page_size = 6;    // 每页条数，默认 12
}

// ListAttachmentsResp 附件选择器分页响应
message ListAttachmentsResp {
  repeated AttachmentInfo list = 1;
  int64 total = 2;
  int32 page = 3;
  int32 page_size = 4;
}

// --- 附件迁移（本地 → OSS） ---
// 迁移工具三段式：扫描匹配生成清单 → 逐条迁移（成功后自动验证）→ 手动删除本地文件

// AttachmentMigrateItemInfo 迁移条目信息
message AttachmentMigrateItemInfo {
  int64 id = 1;                // 条目 ID
  string path = 2;             // 对象相对路径（与 attachment.path 同口径）
  string name = 3;             // 原始文件名
  string type = 4;             // 文件 MIME 类型
  int64 size = 5;              // 文件大小（字节）
  int32 width = 6;             // 图片宽度（像素）
  int32 height = 7;            // 图片高度（像素）
  string status = 8;           // 条目状态：pending/migrating/migrated/verified/failed/verify_failed/local_deleted/orphan
  string error_msg = 9;        // 最近一次失败原因
  string migrated_at = 10;     // 迁移完成时间
  string verified_at = 11;     // 验证通过时间
  string local_deleted_at = 12; // 本地文件删除时间
  string created_at = 13;      // 入库时间
  string url = 14;             // 完整访问 URL（服务端按附件配置拼接，前端不得自行拼装）

}

// AttachmentMigrateOverview 迁移任务概览统计
// orphan 为孤儿文件数（本地有、附件表无记录）；missing 仅为扫描响应填充（附件表有记录、本地文件缺失）
message AttachmentMigrateOverview {
  int32 total = 1;
  int32 pending = 2;
  int32 migrating = 3;
  int32 migrated = 4;
  int32 verified = 5;
  int32 failed = 6;
  int32 verify_failed = 7;
  int32 local_deleted = 8;
  int32 orphan = 9;
  int32 missing = 10;
}

// AttachmentMigrateListReq 迁移条目列表请求
message AttachmentMigrateListReq {
  string status = 1;               // 状态过滤，空为全部
  common.Pagination pagination = 2;
}

// AttachmentMigrateListResp 迁移条目列表响应
message AttachmentMigrateListResp {
  repeated AttachmentMigrateItemInfo list = 1;
  common.PaginationResponse pagination = 2;
  AttachmentMigrateOverview overview = 3;
}

// AttachmentMigrateItemReq 单条目操作请求（迁移/验证/删除本地文件）
message AttachmentMigrateItemReq {
  int64 id = 1;
}

// AttachmentMigrateDeleteBatchResp 批量删除本地文件响应
message AttachmentMigrateDeleteBatchResp {
  int32 deleted = 1; // 实际删除的文件数
}
```

接口语义说明：
- `ScanAttachmentMigrate`：前置校验 OSS 配置完整性（未配置时返回业务错误）；遍历本地附件目录与 `attachment` 表全量匹配生成/刷新任务清单，返回概览统计。
- `MigrateAttachmentItem`：仅 pending/failed/verify_failed 状态可迁移（orphan 拒绝）；读本地文件经 jcbaseGo OSS 客户端上传至相同 path，成功后自动验证（对象存在且大小一致 → verified，否则 verify_failed）。
- `VerifyAttachmentItem`：对 migrated/verify_failed 条目重新验证。
- `DeleteAttachmentLocal`：仅 verified 条目可删本地文件（OSS 保留副本），删除后置 local_deleted。
- `DeleteAttachmentLocalVerified`：批量删除所有 verified 条目的本地文件，单个失败跳过不中断，返回实际删除数。
- `ReleaseCoupon`：订单取消/退款时按 `order_id` 定位已核销的 user_coupon 回补（状态回未使用、UsedCount 原子递减），幂等可重入。
- `GrantCoupon`：后台手动发放，逐人独立校验限领/总量/发放状态，单人不合格不影响其他人；`ListCouponRecords` 按社区语境过滤领取/发放记录，防跨社区泄露。
- `DeductStock`/`ReleaseStock` 幂等：以 `stock_op_logs` 表 `uk_stock_op_order_sku_biz(order_id, sku_id, op_type, biz_no)` 复合唯一索引约束同一业务操作，同一业务号的重复扣减/释放请求（如 gRPC 超时重试）只会生效一次，防止重复扣减或重复回补。`ReleaseStockReq.biz_no` 为幂等业务号，由调用方按业务场景区分：退款释放传退款单号，取消/超时关单传 `cancel:{order_id}`，下单失败补偿传 `comp:{order_id}`（整单一个槽位，2026-10-26 起随批量回补收敛），使同一订单同一 SKU 的多笔部分释放各自独立生效（旧数据 `biz_no` 为空串，与 `DeductStock` 的缺省口径一致）。
- 回补入参校验（2026-10-26）：`releaseSkuLocked` 拒绝 `quantity <= 0`（`stock_op_logs.quantity` 是有符号 int，0/负数不会报错，会静默写进流水并让「释放」变成一次扣减，故在服务层直接拒绝）；`order_id != 0` 时按订单维度校验**累计回补 ≤ 累计扣减**（`assertReleaseWithinDeduct`，统计含本次）——唯一索引只防同一 `biz_no` 重放，跨槽位（取消 + 退款 + 补偿各占独立 `biz_no`）的重复回补必须由这道净上限拦住，否则货到付款单「先部分退款、再整单取消」会把同一 SKU 回补两次。`order_id = 0` 的表单调整类回补不适用该口径。
- 库存交易与商品汇总列：扣减/回补（单条与批量）、入库、出库、盘点在同一事务内调用 `syncProductAggregates` 回写 `products` 的 `stock/min_price/max_price/min_original_price`，后台商品列表的「已售罄/库存紧张」Tab 直接依赖该列。
- 免配送费四 RPC（List/Create/Update/GetActive）：活动按社区隔离，门槛与排除范围全量提交；`GetActiveFreeShipping` 供 C 端确认页与 order 服务 `PreviewFreeShipping` 试算共用。
- `SearchSkus`（SKU 级搜索）：强制社区隔离（`s.community_id`），只返回启用 SKU 与非软删商品；`keyword` 为空返回全量分页，非空匹配商品名 / SKU 编码 / 条形码（`LIKE %kw%`，先 TrimSpace 判空）；分页缺省 1/20、上限 200，排序 `p.id DESC, s.id ASC`，响应携带 `product_name`。供库存工作台与进销存表单的 SKU 选择器复用，替代网关侧 N+1 组合查询。
- `BatchGetSkus`/`BatchGetProducts`（批量取价取库存）：默认「交易口径」——任一 ID 缺失/越权/SKU 停用即整体拒绝；`tolerate_missing=true` 切「逐项判定口径」（缺失项从结果剔除、状态与库存原样返回），优先于 `require_sellable`。C 端「再来一单」据此逐项跳过失效规格而不阻断整单复购。
- `BatchDeductStock`/`BatchReleaseStock`（批量库存）：与单条口径一致，差异在事务边界——整单明细并入一个事务，全成或全败（逐行调用是 N 次往返 + N 个独立事务，失败要靠订单侧补偿且会留半态）。幂等日志**先于扣减**插入；整批全部冲突判为重放直接返回成功，**部分冲突按错误处理**（无法判定应扣集合，宁可失败也不静默少扣）。`normalizeStockItems` 必须先把同订单同 SKU 的多行合并数量，否则第二行被唯一键判为重放而静默跳过造成少扣超卖；入口拒绝 `quantity <= 0`（负数会绕过 `stock < quantity` 判定反向加库存）；加锁按主键升序防死锁。
- 秒杀独立库存（`flash_sale_items.stock`，与主库存完全独立，仅作秒杀限量池）：幂等键用 `op_type=3`（扣减）/ `4`（释放），与主库存 1/2 区分，同一 `order_id + sku_id` 下两套日志并存互不干扰。`DeductFlashSaleStock` 无生效秒杀活动时直接 no-op 且不落幂等日志（订单侧对全部明细统一调用，非秒杀商品不应报错），释放侧据此判断是否需要回补；扣减用 `WHERE id = ? AND stock >= ?` 条件更新防并发超卖，不足返回「秒杀库存不足」由订单侧补偿回滚。`ReleaseFlashSaleStock` 仅当存在 `op_type=3` 日志才回补，**优先按日志记录的活动项 ID 定向回补原活动池**（同 SKU 多活动并发时避免「A 扣 B 补」的池错位，活动结束后退款也能回到原池），日志缺失或活动项被删才回落按 SKU 定位现有活动。批量版逐条口径一致，`biz_no` 为空回落 `cancel:{order_id}`。
- 供应商（List/Create/Update/Delete）：`keyword` 模糊匹配名称/联系人/电话，分页缺省 1/10、`id desc`；创建时 `status` 强制置 1（启用），无重名校验；更新为按 map 全量覆盖，传空即清空，`community_id` 与记录不一致即拒绝。**删除为物理删除且受引用保护**：`purchase_orders` 或 `stock_in_orders` 任一存在 `supplier_id` 引用即拒绝（被入库单引用同样不能删），提示改为「可停用」。
- 采购单状态机：1 草稿 / 2 已下单 / 3 部分入库 / 4 已完结 / 5 已取消。`CreatePurchaseOrder` 默认落草稿，`submit_now=true` 直接置 2；校验明细非空、`sku_id > 0`、`quantity > 0`、`purchase_price >= 0`，`total_amount = Σ(数量 × 进价)` 四舍五入到分，头与明细同一事务。`UpdatePurchaseOrder` **仅草稿态可改**（`FOR UPDATE` 锁头，明细全量替换：先删后建）。`SubmitPurchaseOrder` 1 → 2。`CancelPurchaseOrder` 仅 1/2 可取消，3/4/5 返回「采购单已产生入库或已完结，不能取消」。`ListPurchaseOrders` 的 `keyword` **只匹配采购单号**，`GetPurchaseOrder` 回填 SKU 与供应商信息（该接口未做社区归属校验，出库/入库单走 `EnsureScope`）。
- 入库单（创建即生效，无草稿/审批态）：`purchase_order_id > 0` → `type=1` 采购入库，否则 `type=2` 直接入库；关联采购单时锁单头，要求同社区且状态为 2 或 3，`supplier_id=0` 时回落采购单供应商。事务内逐明细建批次（`source_type=1`、`ref_no` 为入库单号、`unit_cost <= 0` 时取 `sku.cost_price`）→ `product_skus.stock += qty` 且按**移动加权平均**重算 `cost_price` → 落流水 `type=1`（含 `before/after_stock`、`batch_id`）。随后回写采购单：按 SKU 聚合本次入库量，SKU 不在明细或 `received + qty > quantity` 均拒绝，重算状态（全满→4、部分→3、无→2）。`lockSkus` 按 `sku_id` 升序一次性 `IN` 查询加行锁，把 N 次 `FOR UPDATE` 压成 1 次往返并避免并发单据交叉加锁死锁。
- 出库单（报损/领用/调整）：`type` 后端强校验仅接受 1 报损 / 2 领用 / 3 调整；先 `lockSkus` 升序加锁，`sku.Stock < quantity` 返回「商品 {sku_code} 库存不足（当前 n）」；再按 **FEFO** 从批次扣（`expire_date IS NULL ASC, expire_date ASC, id ASC`，仅取 `quantity > 0` 的批次），每个批次一条流水（`type=2`、`quantity` 取负、`ref_no` 为出库单号），批次扣至 0 时 `status` 置 0。**出库不重算 `cost_price`**（只有入库做移动加权）。
- 批次库存（`ListStockBatches`）：筛选 `community_id`/`sku_id`/`in_stock_only`（`quantity > 0`），`keyword` 非空才 JOIN 商品与 SKU 匹配商品名/SKU 编码/批次号；排序固定 `id desc`（**非 FEFO**，FEFO 只在出库扣减时由 `deductBatches` 决定）。
- 报表（2026-10 进销存）：`GetInventorySummaryReport` 期初取期间首条流水的 `before_stock`、期末取末条流水的 `after_stock`，无流水回落当前 `sku.stock`，入库/出库按期间流水 `quantity` 正负聚合，只统计启用 SKU；时间范围为空回退近 30 天（起补 `00:00:00`、止补 `23:59:59`），分页上限 100。`GetProfitReport` 为**近似口径**：销量取期间 `type=2` 且 `ref_no=''`（无出库单号，即销售出库）的 `-quantity` 合计，销售额按当前售价、成本按当前移动加权成本估算，均四舍五入到分，只返回有销量的 SKU。

对应网关路由（admin，需 JWT）：

| 方法 | 路径 | 权限 |
|------|------|------|
| POST | /api/v1/admin/attachment/migrate/scan | settings:update |
| GET | /api/v1/admin/attachment/migrate/items | settings:view |
| POST | /api/v1/admin/attachment/migrate/delete-verified | settings:update |
| POST | /api/v1/admin/attachment/migrate/:id/migrate | settings:update |
| POST | /api/v1/admin/attachment/migrate/:id/delete-local | settings:update |

### 数据导入（应用中心 · 数据包，2026-10-27 新增）

面向空社区/新环境的演示数据一键导入能力。数据包由 `tools/gen-data-package.mjs` 生成后固化提交（`services/product/service/seeddata/full-demo.json`，`go:embed` 编译进二进制），框架层见 `service/dataimport.go`，各段逻辑见 `service/section_*.go`。

```protobuf
// 数据导入（应用中心 · 数据包；实现见 service/dataimport*.go，初期仅超管）
rpc ListDataPackages(ListDataPackagesReq) returns (ListDataPackagesResp);
rpc PreviewDataImport(DataImportPreviewReq) returns (DataImportPreviewResp);
rpc ImportDataPackage(DataImportReq) returns (DataImportResp);
```

```protobuf
// --- 数据导入 ---
message DataPackageSectionInfo {
  string key = 1;        // 条目标识：catalog / banners / marketing ...
  string name = 2;       // 展示名
  string description = 3;
  int32 item_count = 4;  // 可导入条目数
}

message DataPackageInfo {
  string key = 1;
  string name = 2;
  string description = 3;
  repeated DataPackageSectionInfo sections = 4;
}

message ListDataPackagesReq {
  int64 community_id = 1; // 目标社区（当前选中社区）
}

message ListDataPackagesResp {
  repeated DataPackageInfo packages = 1;
}

message DataImportPreviewReq {
  int64 community_id = 1;
  string package_key = 2;
  // 勾选明细（条目 key，如 cat-1 / prod-3 / banner-1）；空 = 全选
  repeated string selected_keys = 3;
  // 指定导入会员手机号（应用中心路径：用户券/购物车落到该会员名下；空 = 未指定）
  string target_user_phone = 4;
}

// DataImportItem 预检/执行结果条目
// action: create=将新建 skip=已存在跳过 depend_missing=依赖缺失
// status: created=已创建 skipped=已跳过 failed=失败
message DataImportItem {
  string section_key = 1;
  string item_key = 2;
  string name = 3;
  string action = 4;
  string detail = 5;
  bool selected = 6; // 预检默认勾选态（depend_missing 固定 false）
  string status = 7; // 执行结果：created / skipped / failed（预检时为空）
  string message = 8; // 补充信息（失败原因等）
}

message DataImportPreviewResp {
  repeated DataImportItem items = 1;
}

message DataImportReq {
  int64 community_id = 1;
  string package_key = 2;
  repeated string selected_keys = 3; // 空 = 全选
  repeated string excluded_keys = 4; // 预检后手动排除的条目
  bool on_sale = 5;                  // 导入后商品状态：true=上架（默认）false=下架
  string target_user_phone = 6;      // 指定导入会员手机号（用户券/购物车落到该会员名下）
}

message DataImportResp {
  int32 created = 1;
  int32 skipped = 2;
  int32 failed = 3;
  repeated DataImportItem details = 4;
}
```

接口语义说明：

- **段落归属（`PackageSection.Scope`）**：`global`（社区/区划/账号/用户/站点）与 `InitOnly=true`（订单/售后/进销存/采购/收银样本）是**环境初始化种子**，仅随 `market-admin init` 的 `SeedDemoBootstrap` 导入，应用中心一律不展示、不可导；三个 RPC 只处理「社区域且非仅初始化」的段——`catalog` 商品目录、`banners` Banner 轮播、`marketing` 营销活动、`free_shipping` 满免邮活动、`user_assets` 用户券与购物车。
- `ListDataPackages`：返回可导包及其可见段的展示名与条目数；`community_id` 仅作上下文，不参与过滤。
- `PreviewDataImport`：逐条目给出动作报告 `create`/`skip`/`depend_missing` 与默认勾选态（`depend_missing` 固定不勾）。依赖判定有兜底 `seedDepSelected`——库内暂无但包内存在同名条目且随本次勾选一起导入时，因段顺序为「目录 → 营销 → 免邮 → 用户资产」，依赖会在同一次执行里先落库，故不判为 `depend_missing`（空社区全选场景靠此避免误报）。
- `ImportDataPackage`：按条目粒度独立事务，单条失败记录 `message` 后继续，不中断整包；`excluded_keys` 优先于 `selected_keys`。段内跨条目引用（如秒杀引用目录 SKU）经 `SectionRefs`（key → 真实 ID）在同一次执行内传递。
- **归属口径**：应用中心单社区导入不指定包内社区，2026-10-27 起数据集放宽为 `SectionScopeAll`（包内全部社区域条目参与，两社区商品名已全部唯一，混灌无按名解析歧义）；初始化流程逐社区导入时显式带入 `CommunityKey`，各取自身归属条目。
- **幂等键**：社区按名称、账号按 username、用户按手机号、SKU 按社区内条码、分类按社区内名称；已存在即 `skip`，可安全重跑。
- **目标会员**：`target_user_phone` 非空时用户券/购物车落到该会员名下；应用中心路径未指定会员时用户侧数据段（`user_assets`）禁止导入（初始化链路仍按种子条目自带手机号落演示用户）。
- **相对时间**：包内时间均为相对表达（`offset_minutes` 或 `day_offset + minute_of_day`），导入时换算为绝对时间，避免种子数据随存放时间老化；演示样本的 `created_at/updated_at` 经 `UpdateColumns`（跳过钩子）回填。

对应网关路由（admin，需 JWT + 超管双闸，见 06-接口设计/03 的「数据导入模块」）：

| 方法 | 路径 | 权限 |
|------|------|------|
| GET | /api/v1/admin/data-import/packages | `product:create` + 仅超管 |
| POST | /api/v1/admin/data-import/preview | `product:create` + 仅超管 |
| POST | /api/v1/admin/data-import/import | `product:create` + 仅超管 |
