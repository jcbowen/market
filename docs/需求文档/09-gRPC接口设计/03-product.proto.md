
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

  // 商品
  rpc ListProducts(ListProductsReq) returns (ListProductsResp);
  rpc GetProduct(GetProductReq) returns (ProductDetail);
  rpc SearchProducts(SearchProductsReq) returns (ListProductsResp);
  rpc CreateProduct(CreateProductReq) returns (ProductDetail);
  rpc UpdateProduct(UpdateProductReq) returns (ProductDetail);
  rpc DeleteProduct(DeleteProductReq) returns (common.CommonResponse);
  rpc UpdateProductStatus(UpdateProductStatusReq) returns (common.CommonResponse);

  // SKU
  rpc ListSkus(ListSkusReq) returns (ListSkusResp);
  rpc CreateSku(CreateSkuReq) returns (SkuInfo);
  rpc UpdateSku(UpdateSkuReq) returns (SkuInfo);
  rpc GetSkuById(GetSkuByIdReq) returns (SkuInfo);

  // 库存
  rpc DeductStock(DeductStockReq) returns (DeductStockResp);
  rpc ReleaseStock(ReleaseStockReq) returns (common.CommonResponse);
  rpc StockEntry(StockEntryReq) returns (common.CommonResponse);
  rpc ListStockRecords(ListStockRecordsReq) returns (ListStockRecordsResp);
  rpc GetStockWarnings(GetStockWarningsReq) returns (ListStockWarningsResp);

  // 盘点
  rpc CreateStockCheck(CreateStockCheckReq) returns (StockCheckInfo);
  rpc SubmitStockCheck(SubmitStockCheckReq) returns (common.CommonResponse);
  rpc ListStockChecks(ListStockChecksReq) returns (ListStockChecksResp);

  // 定价
  rpc SetCommunityPricing(SetPricingReq) returns (common.CommonResponse);
  rpc ListCommunityPricing(ListPricingReq) returns (ListPricingResp);
  rpc GetReferencePricing(GetReferencePricingReq) returns (ListPricingResp);

  // 营销
  rpc ListCoupons(ListCouponsReq) returns (ListCouponsResp);
  rpc GetCoupon(GetCouponReq) returns (CouponInfo);
  rpc CreateCoupon(CreateCouponReq) returns (CouponInfo);
  rpc UpdateCoupon(UpdateCouponReq) returns (CouponInfo);
  rpc ReceiveCoupon(ReceiveCouponReq) returns (common.CommonResponse);
  rpc ListUserCoupons(ListUserCouponsReq) returns (ListUserCouponsResp);
  rpc UseCoupon(UseCouponReq) returns (common.CommonResponse);

  rpc ListFlashSales(ListFlashSalesReq) returns (ListFlashSalesResp);
  rpc GetFlashSale(GetFlashSaleReq) returns (FlashSaleDetail);
  rpc CreateFlashSale(CreateFlashSaleReq) returns (FlashSaleInfo);

  rpc ListFullReductions(ListFullReductionsReq) returns (ListFullReductionsResp);
  rpc CreateFullReduction(CreateFullReductionReq) returns (FullReductionInfo);
}

// --- 分类 ---
message ListCategoriesReq {
  int64 community_id = 1;
}

message GetCategoryReq {
  int64 id = 1;
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
}

message DeleteCategoryReq {
  int64 id = 1;
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
  string sort = 4;           // sales | price_asc | price_desc
  common.Pagination pagination = 5;
}

message GetProductReq {
  int64 id = 1;
  int64 community_id = 2;
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
}

message DeleteProductReq {
  int64 id = 1;
}

message UpdateProductStatusReq {
  int64 id = 1;
  int32 status = 2;
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
}

message GetSkuByIdReq {
  int64 id = 1;
}

message CreateSkuReq {
  int64 product_id = 1;
  int64 community_id = 2;
  string sku_code = 3;
  string spec_name = 4;
  string spec_json = 5;
  double price = 6;
  double cost_price = 7;
  int32 stock = 8;
  int32 safety_stock = 9;
  string image = 10;
}

message UpdateSkuReq {
  int64 id = 1;
  string spec_name = 2;
  string spec_json = 3;
  double price = 4;
  double cost_price = 5;
  int32 safety_stock = 6;
  string image = 7;
  int32 status = 8;
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
}

message ListSkusResp {
  repeated SkuInfo list = 1;
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
}

message StockEntryItem {
  int64 sku_id = 1;
  int32 quantity = 2;
  string batch_no = 3;
  string production_date = 4;
  string expire_date = 5;
}

message StockEntryReq {
  int64 community_id = 1;
  int32 type = 2;               // 1采购入库 2退货入库
  repeated StockEntryItem items = 3;
  int64 operator_id = 4;
  string remark = 5;
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
  int32 warning_type = 7;       // 1低库存 2临期
  string expire_date = 8;
}

message ListStockWarningsResp {
  repeated StockWarningInfo list = 1;
}

// --- 盘点 ---
message CreateStockCheckReq {
  int64 community_id = 1;
  int64 operator_id = 2;
  string remark = 3;
}

message StockCheckItem {
  int64 sku_id = 1;
  int32 actual_stock = 2;
}

message SubmitStockCheckReq {
  int64 id = 1;
  repeated StockCheckItem items = 2;
}

message StockCheckInfo {
  int64 id = 1;
  string check_no = 2;
  int32 status = 3;
  int32 total_items = 4;
  int32 diff_items = 5;
  string remark = 6;
  string created_at = 7;
}

message ListStockChecksReq {
  int64 community_id = 1;
  common.Pagination pagination = 2;
}

message ListStockChecksResp {
  repeated StockCheckInfo list = 1;
  common.PaginationResponse pagination = 2;
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
}

message GetCouponReq {
  int64 id = 1;
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
}

message UpdateCouponReq {
  int64 id = 1;
  string name = 2;
  int32 status = 3;
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
  int32 per_user_limit = 9;
  string start_time = 10;
  string end_time = 11;
  int32 valid_days = 12;
  int32 status = 13;
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

message ListFlashSalesReq {
  int64 community_id = 1;
}

message GetFlashSaleReq {
  int64 id = 1;
}

message CreateFlashSaleReq {
  int64 community_id = 1;
  string name = 2;
  string start_time = 3;
  string end_time = 4;
  repeated FlashSaleItemReq items = 5;
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

// --- 附件 ---
// 附件服务（config 读写、本地上传、OSS 预签名直传与 MD5 秒传）
service 追加：
  rpc GetAttachmentConfig(common.Empty) returns (AttachmentConfig);
  rpc SaveAttachmentConfig(AttachmentConfig) returns (common.CommonResponse);
  rpc UploadImageLocal(UploadImageLocalReq) returns (AttachmentInfo);
  rpc AttachmentPresign(AttachmentPresignReq) returns (AttachmentPresignResp);
  rpc AttachmentConfirm(AttachmentConfirmReq) returns (AttachmentInfo);

// AttachmentConfig 附件存储配置（数据库单行存储，id 固定为 1）
// oss_access_key_secret 写入时空或掩码值表示保留原值，读取时服务端脱敏
message AttachmentConfig {
  string storage_type = 1;             // local / oss（预留 cos）
  string visit_domain = 2;
  string oss_access_key_id = 3;
  string oss_access_key_secret = 4;
  string oss_endpoint = 5;
  string oss_bucket_name = 6;
  string oss_custom_visit_domain = 7;
  int64 max_size = 8;                  // 图片上限（字节），0 默认 5MB
  string updated_at = 9;               // 兼作版本号
}

message UploadImageLocalReq {
  int64 admin_id = 1;
  string filename = 2;
  string content_type = 3;
  bytes content = 4;
}

message AttachmentPresignReq {
  int64 admin_id = 1;
  string filename = 2;
  string md5 = 3;          // 32 位十六进制，用于秒传
  string content_type = 4;
  int64 size = 5;
  int32 width = 6;
  int32 height = 7;
}

message AttachmentPresignResp {
  string attach_id = 1;
  bool hit = 2;                    // true 表示秒传命中，直接用 final_url
  string url = 3;                  // 预签名上传 URL
  string method = 4;               // PUT
  map<string, string> headers = 5;
  string final_url = 6;
  string remote_path = 7;
}

message AttachmentConfirmReq {
  int64 admin_id = 1;              // 须与记录归属一致
  string attach_id = 2;
  string md5 = 3;                  // confirm 补写，激活秒传
  int64 size = 4;
  int32 width = 5;
  int32 height = 6;
}

message AttachmentInfo {
  string attach_id = 1;
  string url = 2;                  // local 模式为空，gateway 按请求域名拼接
  string remote_path = 3;
  int64 size = 4;
  int32 width = 5;
  int32 height = 6;
}

// --- 附件迁移 ---
// 本地附件一键迁移 OSS（扫描生成任务清单 → 迁移（成功自动验证）→ 手动删除本地文件）
// 批量由前端循环调用单条接口驱动，状态落库 attachment_migrate_item，无后端常驻任务
service 追加：
  rpc ScanAttachmentMigrate(common.Empty) returns (AttachmentMigrateOverview);
  rpc ListAttachmentMigrate(AttachmentMigrateListReq) returns (AttachmentMigrateListResp);
  rpc MigrateAttachmentItem(AttachmentMigrateItemReq) returns (AttachmentMigrateItemInfo);
  rpc VerifyAttachmentItem(AttachmentMigrateItemReq) returns (AttachmentMigrateItemInfo);
  rpc DeleteAttachmentLocal(AttachmentMigrateItemReq) returns (AttachmentMigrateItemInfo);
  rpc DeleteAttachmentLocalVerified(common.Empty) returns (AttachmentMigrateDeleteBatchResp);

// AttachmentMigrateItemInfo 迁移条目
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

对应网关路由（admin，需 JWT）：

| 方法 | 路径 | 权限 |
|------|------|------|
| POST | /api/v1/admin/attachment/migrate/scan | settings:update |
| GET | /api/v1/admin/attachment/migrate/items | settings:view |
| POST | /api/v1/admin/attachment/migrate/delete-verified | settings:update |
| POST | /api/v1/admin/attachment/migrate/:id/migrate | settings:update |
| POST | /api/v1/admin/attachment/migrate/:id/verify | settings:update |
| POST | /api/v1/admin/attachment/migrate/:id/delete-local | settings:update |
