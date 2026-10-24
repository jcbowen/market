### order.proto

```protobuf
syntax = "proto3";

package order;

option go_package = "github.com/jcbowen/market/src/backend/common/proto/order";

import "common/common.proto";

service OrderService {
  rpc CreateOrder(CreateOrderReq) returns (OrderInfo);
  rpc GetOrder(GetOrderReq) returns (OrderDetail);
  rpc ListOrders(ListOrdersReq) returns (ListOrdersResp);
  rpc CancelOrder(CancelOrderReq) returns (common.CommonResponse);
  // C 端用户删除订单（软删除）：仅终态订单可删，列表与角标统计排除，详情链路保留可达
  rpc DeleteOrder(DeleteOrderReq) returns (common.CommonResponse);
  // 后台恢复被用户软删除的订单（客服场景「我删错了，帮我找回上次买的东西」）：
  // 按 community_id 限定作用域，仅在该社区范围内把 user_deleted 复位
  rpc RestoreOrder(RestoreOrderReq) returns (common.CommonResponse);
  rpc UpdateOrderStatus(UpdateOrderStatusReq) returns (common.CommonResponse);
  // 自提订单核销：校验取货码后置为已完成（后台兜底核销 / 履约工作台核销共用）
  rpc VerifyPickup(VerifyPickupReq) returns (VerifyPickupResp);

  // 退款
  rpc ApplyRefund(ApplyRefundReq) returns (RefundInfo);
  // 后台代客退款：发起即执行（免审核），items 为空表示整单退
  rpc AdminCreateRefund(AdminCreateRefundReq) returns (RefundInfo);
  rpc ListRefunds(ListRefundsReq) returns (ListRefundsResp);
  rpc ApproveRefund(ApproveRefundReq) returns (common.CommonResponse);
  rpc RejectRefund(RejectRefundReq) returns (common.CommonResponse);
  // 重试卡在"退款中"(status=4) 的退款单：以退款单自身的 payment_no 重新发起渠道退款
  rpc RetryRefund(RetryRefundReq) returns (common.CommonResponse);
  // 恢复卡在"退款中"的退款单的自动收敛（B2）：
  // 先把 payment 侧退款流水的重试预算归零（人工核实渠道结果后重新交系统收敛），
  // 再把本侧 refunds.retry_count/last_checked_at 复位，使调度器下一轮立即重新核查。
  // 与 RetryRefund 的区别：本方法不再次触渠道出款，只恢复「问渠道结果」的节奏
  rpc ResetRefundRetry(ResetRefundRetryReq) returns (common.CommonResponse);
  rpc GetRefundConfig(GetRefundConfigReq) returns (RefundConfig);
  rpc SaveRefundConfig(SaveRefundConfigReq) returns (common.CommonResponse);

  // 统计
  rpc GetOrderStatistics(GetOrderStatisticsReq) returns (OrderStatisticsResp);
  rpc GetSalesStatistics(GetSalesStatisticsReq) returns (SalesStatisticsResp);
  rpc GetProductRankings(GetProductRankingsReq) returns (ProductRankingsResp);
  rpc GetRevenueReport(GetRevenueReportReq) returns (RevenueReportResp);
  // 用户常买商品（购物车"常买商品"列表）：按用户有效订单统计
  rpc GetUserFrequentProducts(GetUserFrequentProductsReq) returns (GetUserFrequentProductsResp);
  // 用户订单状态角标统计（C 端「我的-我的订单」红标）：按用户维度聚合；
  // after_sale 特殊——统计存在待审核售后单(refunds.status=1)的订单数，非按订单状态
  rpc GetUserOrderStatusCounts(GetUserOrderStatusCountsReq) returns (GetUserOrderStatusCountsResp);
  // 免配送费试算：按商品清单计算当前社区免配送费活动命中情况（C 端确认页展示）
  rpc PreviewFreeShipping(PreviewFreeShippingReq) returns (PreviewFreeShippingResp);
  // 收银台挂单：暂存/列表/删除（草稿不生成正式订单、不占库存，决策点⑥）
  rpc CreatePosHeldOrder(CreatePosHeldOrderReq) returns (PosHeldOrderInfo);
  rpc ListPosHeldOrders(ListPosHeldOrdersReq) returns (ListPosHeldOrdersResp);
  rpc DeletePosHeldOrder(DeletePosHeldOrderReq) returns (common.CommonResponse);

  // --- 收银台交班体系 ---
  // 开工：创建进行中班次（同操作员同社区已有进行中班次时报错）
  rpc StartShift(StartShiftReq) returns (PosShiftInfo);
  // 当前进行中班次（无则返回 statistics 为空的 PosShiftInfo，id=0 表示无进行中班次），含实时统计
  rpc GetCurrentShift(GetCurrentShiftReq) returns (PosShiftInfo);
  // 交班：生成统计快照 → 置已结束 → 返回快照
  rpc EndShift(EndShiftReq) returns (PosShiftInfo);
  // 交班记录分页（operator_id=0 本店全部班次，>0 仅该操作员）
  rpc ListShifts(ListShiftsReq) returns (ListShiftsResp);

  // --- 应用偏好（应用页「常用」，社区级单行） ---
  rpc GetAppPreferences(GetAppPreferencesReq) returns (AppPreferencesInfo);
  rpc SaveAppPreferences(SaveAppPreferencesReq) returns (common.CommonResponse);

  // --- 履约工作台（B） ---
  // 后台指派配送员（delivery_user_id=0 表示取消指派）
  rpc AssignDelivery(AssignDeliveryReq) returns (common.CommonResponse);
  // 工作台查单：送货单按指派人过滤，自提单为本社区全部待核销/已核销
  rpc ListFulfillmentOrders(ListFulfillmentOrdersReq) returns (ListFulfillmentOrdersResp);
  // 开始配送：status 2/3 → 4（履约人员操作，不经后台 ShipOrder 的物流单号校验）
  rpc StartDelivery(StartDeliveryReq) returns (common.CommonResponse);
  // 送达完成：status 4 → 5，强制签收照片（决策点②拍照留证）
  rpc CompleteDelivery(CompleteDeliveryReq) returns (common.CommonResponse);
}

message OrderItem {
  int64 sku_id = 1;
  int32 quantity = 2;
  // 收银台单品改价：>0 时以该价成交（仅对当前订单生效，不改商品档案价）；0=按服务端实时价
  double price = 3;
}

message CreateOrderReq {
  int64 user_id = 1;
  int64 community_id = 2;
  // 配送类型：1自提 2配送 3到店（到店仅收银台 source=2 可用，不生成取货码）
  int32 delivery_type = 3;
  int64 address_id = 4;
  string delivery_time = 5;
  string remark = 6;
  int64 coupon_id = 7;
  repeated OrderItem items = 8;
  // 订单来源：0/1=C端用户 2=后台收银台代客（决策点⑨）
  int32 source = 9;
  // 经手人（收银员/管理员），source=2 时由网关填写
  int64 operator_id = 10;
  string operator_name = 11;
  // 直填联系人信息：收银台代客下单无 address_id 时使用（address_id>0 仍以地址簿为准）
  string contact_name = 12;
  string contact_phone = 13;
  string contact_address = 14;
  // 手动优惠总额（收银台抹零/整单改价折算），实付 = max(0, 应付 - manual_discount)；超应付拒绝
  double manual_discount = 15;
  // 收银班次 ID（source=2 收银单必填，下单时由网关校验进行中班次后传入；0=非收银单）
  int64 shift_id = 16;
}

// --- 免配送费试算 ---
message PreviewFreeShippingItem {
  int64 sku_id = 1;
  int32 quantity = 2;
}

message PreviewFreeShippingReq {
  int64 community_id = 1;
  repeated PreviewFreeShippingItem items = 2;
}

message PreviewFreeShippingResp {
  // 是否已满足免配送费（参与商品额 >= 门槛）
  bool free_shipping = 1;
  string activity_name = 2;
  double threshold = 3;
  // 参与免配送费的商品金额合计
  double eligible_amount = 4;
  // 距门槛差额（已满足时为 0）
  double remain_amount = 5;
  // 被排除商品/分类的展示名清单（点击展开提示用）
  repeated string excluded_names = 6;
  // 基础配送费（社区配置值；free_shipping=true 时订单实际配送费为 0）
  double delivery_fee = 7;
}

message GetOrderReq {
  int64 id = 1;
  int64 user_id = 2;
}

message ListOrdersReq {
  int64 user_id = 1;
  int64 community_id = 2;
  int32 status = 3;
  common.Pagination pagination = 4;
  // --- 后台管理端搜索条件（C 端列表不使用） ---
  // 订单号模糊匹配
  string order_no = 5;
  // 下单时间范围（YYYY-MM-DD，闭区间）
  string start_time = 6;
  string end_time = 7;
  // 收货人姓名模糊匹配
  string receiver_name = 8;
  // 收货人电话模糊匹配
  string receiver_phone = 9;
  // 订单来源筛选：1=C端 2=收银台（0=全部）
  int32 source = 10;
  // 取货码精确查询（收银台核销 Tab 扫码/输码查单）
  string pickup_code = 11;
  // 收银班次筛选（交班记录「查看班内订单」入口；0=全部）
  int64 shift_id = 12;
  // 是否包含被用户软删除的订单。
  // 判定「调用方是谁」不能靠 user_id 是否非零——后台按会员查订单同样传 user_id，
  // 若靠它推断会把已删单从客服视图一并抹掉。故：C 端列表不传（false=排除），
  // 后台管理端显式传 true（含已删单，配合 OrderInfo.user_deleted 标记展示）
  bool include_user_deleted = 13;
  // 支付单号/渠道交易号反查命中集（2026-10-24 新增，后台订单搜索）：
  // gateway 先经 payment 服务 FindOrdersByPayNo 反查订单号，再传集合到 order 服务精确匹配；
  // has_pay_no=true 区分「没传」与「传了但没查到」（后者挂恒假集合返回空结果）
  bool has_pay_no = 14;
  repeated string pay_order_nos = 15;
  // 商品过滤（2026-10-24 新增，含全部规格）：命中含该商品任意规格（order_items.product_id）的订单
  int64 product_id = 16;
}

// --- 后台导出任务（下载中心，2026-10-24 新增） ---

// ExportTaskInfo 导出任务信息（下载中心列表行）
// status: 1待处理 2生成中 3已完成 4失败；downloaders 下载留痕 JSON（完整名单仅超管可见）；
// error_msg 失败原因（完成态复用为超限截断提示）；expires_at 文件过期时间（生成后 3 天）
message ExportTaskInfo {
  int64 id = 1;
  string export_type = 2;
  int32 status = 3;
  string query_desc = 4;
  string file_name = 5;
  int64 file_size = 6;
  int64 row_count = 7;
  int64 admin_id = 8;
  string admin_name = 9;
  int64 community_id = 10;
  int32 download_count = 11;
  string downloaders = 12;
  string error_msg = 13;
  string created_at = 14;
  string expires_at = 15;
  // 查询条件 JSON 快照：失败任务「重新导出」时前端解析还原为创建参数，按原条件重发
  string query_cond = 16;
}

// CreateExportTaskReq 创建导出任务：query_json 查询条件快照（与 ListOrdersReq 过滤字段对齐）；
// pay_order_nos 支付单号反查命中集（order 服务无法反查 payment，由 gateway 反查后随快照下发）
message CreateExportTaskReq {
  string export_type = 1;
  string query_json = 2;
  string query_desc = 3;
  int64 admin_id = 4;
  string admin_name = 5;
  int64 community_id = 6;
  repeated string pay_order_nos = 7;
}

message CreateExportTaskResp {
  int64 id = 1;
}

// ListExportTasksReq 任务列表：admin_id>0 仅查该管理员创建的（普通管理员）；
// super_admin=true 时查全部；community_id>0 时超管语境限定该社区（跨社区任务 0 仍全部可见）
message ListExportTasksReq {
  int64 admin_id = 1;
  bool super_admin = 2;
  int64 community_id = 3;
  int32 page = 4;
  int32 page_size = 5;
}

message ListExportTasksResp {
  repeated ExportTaskInfo list = 1;
  int32 total = 2;
  // 已完成且未下载的任务数（右上角下载图标角标）
  int32 undownloaded = 3;
}

message GetExportTaskFileReq {
  int64 id = 1;
  int64 admin_id = 2;
  string admin_name = 3;
  bool super_admin = 4;
  // 下载人社区（普通管理员社区隔离校验用；0=跨社区/超管）
  int64 community_id = 5;
}

message GetExportTaskFileResp {
  // 文件绝对路径（服务端 attachment 目录下，gateway 凭此流式读取）
  string file_path = 1;
  string file_name = 2;
}

message DeleteExportTaskReq {
  int64 id = 1;
}

message CountUndownloadedReq {
  int64 admin_id = 1;
  bool super_admin = 2;
  int64 community_id = 3;
}

message CountUndownloadedResp {
  int32 count = 1;
}

// RestoreOrderReq 后台恢复用户软删除订单
message RestoreOrderReq {
  int64 id = 1;
  // 管理员可操作社区（0=不限，由网关按权限收敛后传入）
  int64 community_id = 2;
}

message CancelOrderReq {
  int64 id = 1;
  int64 user_id = 2;
  string reason = 3;
}

// DeleteOrderReq C 端用户删除订单请求（软删除，仅终态订单）
message DeleteOrderReq {
  int64 id = 1;
  int64 user_id = 2;
}

message UpdateOrderStatusReq {
  int64 id = 1;
  int32 status = 2;
  string operator_type = 3;
  int64 operator_id = 4;
  string remark = 5;
  // 发货（status=4）时写入的物流信息；其余状态流转忽略
  string express_company = 6;
  string express_no = 7;
  // 社区隔离强校验：管理端操作必传，服务端校验订单归属社区；
  // 内部系统调用（支付回调等）传 0 跳过校验
  int64 community_id = 8;
}

message OrderInfo {
  int64 id = 1;
  string order_no = 2;
  int64 community_id = 3;
  int64 user_id = 4;
  int32 status = 5;
  double total_amount = 6;
  double discount_amount = 7;
  double delivery_fee = 8;
  double pay_amount = 9;
  int32 delivery_type = 10;
  string delivery_time = 11;
  string contact_name = 12;
  string contact_phone = 13;
  string contact_address = 21;
  string remark = 14;
  string pay_time = 15;
  string deliver_time = 16;
  string complete_time = 17;
  string cancel_time = 18;
  string cancel_reason = 19;
  string created_at = 20;
  // 物流信息（发货时写入，独立字段，不再拼接进 remark）
  string express_company = 22;
  string express_no = 23;
  // 自提核销码（仅自提单生成，配送单为空）
  string pickup_code = 24;
  // 订单来源：1=C端用户自下单 2=后台收银台代客下单（决策点⑨）
  int32 source = 25;
  // 经手人（收银员/管理员）ID 与姓名快照；C 端订单为 0/空
  int64 operator_id = 26;
  string operator_name = 27;
  // --- 履约工作台（B） ---
  // 指派的履约人员（0=未指派）与姓名快照
  int64 delivery_user_id = 28;
  string delivery_user_name = 29;
  // 送达签收凭证照片 URL（决策点②拍照留证）
  repeated string delivery_proof_images = 30;
  // 手动优惠总额（收银台抹零/整单改价折算痕迹）
  double manual_discount = 31;
  // 收银班次 ID（0=非收银单）
  int64 shift_id = 32;
  // 用户软删除标记：1=该单已被用户在 C 端删除（仅从 C 端列表移除，后台与详情链路保留）
  int32 user_deleted = 33;
}

message OrderItemInfo {
  int64 id = 1;
  int64 product_id = 2;
  int64 sku_id = 3;
  string product_name = 4;
  string spec_name = 5;
  string product_image = 6;
  double price = 7;
  int32 quantity = 8;
  double total_price = 9;
  // 收银台改价痕迹：original_price=服务端实时单价快照；price_modified=true 表示行单价被手动修改
  double original_price = 10;
  bool price_modified = 11;
}

message OrderStatusLog {
  int32 from_status = 1;
  int32 to_status = 2;
  string operator_type = 3;
  string remark = 4;
  string created_at = 5;
}

message OrderDetail {
  OrderInfo order = 1;
  repeated OrderItemInfo items = 2;
  repeated OrderStatusLog status_logs = 3;
  // 本单全部售后（退款）记录：含每次退款单金额/类型/状态/明细，订单详情展示用
  repeated RefundInfo refunds = 4;
}

message ListOrdersResp {
  repeated OrderInfo list = 1;
  common.PaginationResponse pagination = 2;
}

// ApplyRefundItem 申请退款商品明细项
message ApplyRefundItem {
  int64 order_item_id = 1;
  int32 quantity = 2;
}

message ApplyRefundReq {
  int64 order_id = 1;
  int64 user_id = 2;
  double amount = 3;
  string reason = 4;
  // 退款商品明细；为空表示整单退
  repeated ApplyRefundItem items = 5;
  // 运费退还策略为 user_decide 时用户是否选择退运费
  bool refund_freight = 6;
}

// RefundItemInfo 退款商品明细信息
message RefundItemInfo {
  int64 id = 1;
  int64 refund_id = 2;
  int64 order_item_id = 3;
  int64 sku_id = 4;
  string product_name = 5;
  string spec_name = 6;
  int32 quantity = 7;
  double refund_amount = 8;
}

message RefundInfo {
  int64 id = 1;
  string refund_no = 2;
  int64 order_id = 3;
  int64 user_id = 4;
  double amount = 5;
  string reason = 6;
  int32 status = 7;
  string reject_reason = 8;
  string refund_time = 9;
  string created_at = 10;
  string payment_no = 11;
  // 关联订单号
  string order_no = 12;
  // 退款类型：1整单退 2部分退 3仅金额退（不退商品；退款完成后订单钱款全额退完时置为 1 整单退展示）
  int32 type = 13;
  // 退款商品明细
  repeated RefundItemInfo items = 14;
  // 本单退还的运费金额
  double freight_amount = 15;
  // 审批/重试操作日志（后台列表展示用）
  repeated RefundLogInfo logs = 16;
}

// RefundLogInfo 退款审批日志
message RefundLogInfo {
  int64 id = 1;
  // 动作：approve 同意 / reject 拒绝 / retry 重试
  string action = 2;
  string operator_type = 3;
  int64 operator_id = 4;
  string remark = 5;
  string created_at = 6;
}

// AdminCreateRefundReq 后台代客退款请求（发起即执行，免审核）
// 金额以实付口径服务端汇算（含手动优惠分摊），amount 仅允许调低；
// mode=1 整单/部分商品退（items 为空=整单退，金额可调低）；mode=2 仅金额退（不退商品、不回补库存，amount 必填且不超过剩余可退）
message AdminCreateRefundReq {
  int64 order_id = 1;
  double amount = 2;
  string reason = 3;
  // 退款商品明细；mode=1 时为空表示整单退，mode=2 时必须为空
  repeated ApplyRefundItem items = 4;
  // 运费退还策略为 user_decide 时是否退还运费
  bool refund_freight = 5;
  int64 operator_id = 6;
  // 社区隔离强校验：管理端操作必传，服务端校验订单归属社区；内部调用传 0 跳过
  int64 community_id = 7;
  // 退款模式：0/1=按商品退（空 items 整单退） 2=仅金额退
  int32 mode = 8;
}

message ListRefundsReq {
  int64 community_id = 1;
  int32 status = 2;
  common.Pagination pagination = 3;
  // 按用户过滤（2026-10-23 新增，C端退款进度：网关凭登录态传入，防越权查他人退款单）
  int64 user_id = 4;
  // 按订单过滤（2026-10-23 新增，C端订单详情退款进度查询）
  int64 order_id = 5;
}

// GetRefundConfigReq 查询退款配置请求
message GetRefundConfigReq {}

// RefundConfig 退款配置
message RefundConfig {
  // 运费退还策略：always始终退运费 never不退运费 user_decide用户自行选择
  string refund_freight_policy = 1;
}

// SaveRefundConfigReq 保存退款配置请求
message SaveRefundConfigReq {
  string refund_freight_policy = 1;
}

message ListRefundsResp {
  repeated RefundInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message ApproveRefundReq {
  int64 id = 1;
  int64 operator_id = 2;
  // 已废弃：支付单号一律以退款单服务端记录为准，请求传入值将被忽略（防越权）
  string payment_no = 3;
  // 社区隔离强校验：管理端操作必传，服务端校验退款单归属订单的社区；
  // 内部系统调用（调度器）传 0 跳过校验
  int64 community_id = 4;
}

message RejectRefundReq {
  int64 id = 1;
  int64 operator_id = 2;
  string reject_reason = 3;
  // 社区隔离强校验：同 ApproveRefundReq.community_id
  int64 community_id = 4;
}

// RetryRefundReq 重试退款请求（针对卡在退款中 status=4 的退款单）
message RetryRefundReq {
  int64 id = 1;
  int64 operator_id = 2;
  // 社区隔离强校验：同 ApproveRefundReq.community_id
  int64 community_id = 3;
}

// ResetRefundRetryReq 恢复自动收敛请求（B2）
// 编排顺序：先调 payment.ResetRefundFlow 归零流水预算，成功后才复位本侧
// refunds.retry_count/last_checked_at（UpdateColumns 绕开钩子，updated_at 不动），
// 最后落 refund_logs(action=reset)；支付侧拒绝时本侧保持原样并回错，不留半截状态
message ResetRefundRetryReq {
  int64 id = 1;
  int64 operator_id = 2;
  // 社区隔离强校验：同 RetryRefundReq.community_id
  int64 community_id = 3;
  // 核实依据（必填）：在渠道商户平台看到的退款单状态/到账结论，落 refund_logs 供审计
  string remark = 4;
  string operator_name = 5;
}

message GetOrderStatisticsReq {
  int64 community_id = 1;
  string start_date = 2;
  string end_date = 3;
}

message OrderStatisticsResp {
  int32 pending_pay = 1;
  int32 pending_ship = 2;
  int32 pending_receive = 3;
  int32 completed = 4;
  int32 cancelled = 5;
  int32 refunding = 6;
  int32 total_orders = 7;
}

message GetSalesStatisticsReq {
  int64 community_id = 1;
  string start_date = 2;
  string end_date = 3;
}

message SalesStatisticsResp {
  double total_amount = 1;
  double pay_amount = 2;
  int32 total_orders = 3;
  double avg_order_amount = 4;
}

message GetProductRankingsReq {
  int64 community_id = 1;
  string start_date = 2;
  string end_date = 3;
  int32 limit = 4;
}

message ProductRankingItem {
  int64 product_id = 1;
  string product_name = 2;
  int32 sales_count = 3;
  double sales_amount = 4;
}

message ProductRankingsResp {
  repeated ProductRankingItem list = 1;
}

message GetRevenueReportReq {
  int64 community_id = 1;
  string start_date = 2;
  string end_date = 3;
  string group_by = 4;
}

message RevenueReportItem {
  string date = 1;
  double revenue = 2;
  int32 order_count = 3;
}

message RevenueReportResp {
  repeated RevenueReportItem list = 1;
}

// --- 自提核销 ---
message VerifyPickupReq {
  int64 order_id = 1;
  // 顾客出示的取货码（数字码）
  string pickup_code = 2;
  // 操作者类型：admin（后台兜底）/ staff（履约人员，B 工作台）
  string operator_type = 3;
  int64 operator_id = 4;
  // 操作者社区语境（>0 时校验订单归属社区，跨社区核销直接拒绝；0=跳过校验）
  int64 community_id = 5;
}

message VerifyPickupResp {
  int64 order_id = 1;
  int32 status = 2;
}

// --- 用户常买商品 ---
message GetUserFrequentProductsReq {
  int64 user_id = 1;
  // 返回条数，默认 10（page=0 时生效，兼容 C 端购物车用法）
  int32 limit = 2;
  // 页码（>0 走分页模式，管理端会员订单弹窗用；分页时忽略 limit）
  int32 page = 3;
  // 每页条数（分页模式生效，默认 5）
  int32 page_size = 4;
  // 操作者社区语境：>0 时仅统计/返回该社区的订单数据，防止跨社区读取会员消费记录（超管/C 端传 0）
  int64 community_id = 5;
  // 是否包含被用户软删除的订单（与 ListOrdersReq.include_user_deleted 同语义）：
  // C 端购物车「常买商品」不传（用户删掉的历史不再推荐）；
  // 后台会员订单弹窗传 true，与同弹窗「订单」Tab 能看到已删单保持一致
  bool include_user_deleted = 6;
}

message FrequentProductItem {
  int64 product_id = 1;
  string product_name = 2;
  // 商品图片快照（order_items.product_image，取该用户购买记录中的一张）
  string product_image = 3;
  // 该用户买该商品最常买的 SKU（用于一键加购）
  int64 sku_id = 4;
  // 购买次数（有效订单数，口径=已支付及之后，排除待支付/取消/退款）
  int32 purchase_count = 5;
  // 累计购买件数
  int32 total_quantity = 6;
}

message GetUserFrequentProductsResp {
  repeated FrequentProductItem list = 1;
  // 商品总数（分页模式返回；limit 模式为 0）
  int64 total = 2;
}

// --- 用户订单状态角标统计（C 端「我的-我的订单」红标） ---

message GetUserOrderStatusCountsReq {
  int64 user_id = 1;
}

// 口径与订单列表 Tab 一致：待支付=1；待发货=2/3 聚合；待收货=4；
// 退款/售后角标（2026-10-23 调整）只统计存在待审核售后单的订单——商家同意/拒绝后不再亮
message GetUserOrderStatusCountsResp {
  // 待支付（status=1）
  int32 pending_pay = 1;
  // 待发货（status=2 已支付 / 3 备货中 聚合）
  int32 pending_ship = 2;
  // 待收货（status=4 配送中）
  int32 pending_receive = 3;
  // 退款/售后（存在待审核售后单 refunds.status=1 的订单数，订单维度去重；
  // 不再按 status=7/8 聚合：8 已退款为终态会永远亮角标，7 中已同意打款在途的不属"待商家处理"）
  int32 after_sale = 4;
}

// --- 收银台挂单（未成单草稿，后端暂存；结账成功或放弃后删除） ---
message PosHeldOrderItem {
  int64 sku_id = 1;
  int32 quantity = 2;
  // 商品/规格快照（恢复挂单时回填收银台展示；结账时以服务端实时价格为准）
  string product_name = 3;
  string spec_name = 4;
  string product_image = 5;
  double price = 6;
}

message CreatePosHeldOrderReq {
  int64 community_id = 1;
  int64 operator_id = 2;
  string operator_name = 3;
  // 挂单时选定的会员（0=散客）
  int64 user_id = 4;
  string user_name = 5;
  repeated PosHeldOrderItem items = 6;
  string remark = 7;
}

message PosHeldOrderInfo {
  int64 id = 1;
  int64 community_id = 2;
  int64 operator_id = 3;
  string operator_name = 4;
  int64 user_id = 5;
  string user_name = 6;
  repeated PosHeldOrderItem items = 7;
  int32 item_count = 8;
  double total_amount = 9;
  string remark = 10;
  string created_at = 11;
}

message ListPosHeldOrdersReq {
  int64 community_id = 1;
  // 0=本社区全部挂单；>0=仅该收银员的挂单
  int64 operator_id = 2;
}

message ListPosHeldOrdersResp {
  repeated PosHeldOrderInfo list = 1;
}

message DeletePosHeldOrderReq {
  int64 id = 1;
  // 社区语境（>0 时校验挂单归属，防跨社区删除）
  int64 community_id = 2;
}

// --- 履约工作台（B） ---

// AssignDeliveryReq 后台指派配送请求
message AssignDeliveryReq {
  int64 order_id = 1;
  // 指派的履约人员 user_id；0=取消指派
  int64 delivery_user_id = 2;
  // 履约人员姓名快照（列表/详情回显，gateway 填写）
  string delivery_user_name = 3;
  // 操作者社区语境（>0 时校验订单归属社区）
  int64 community_id = 4;
  string operator_type = 5;
  int64 operator_id = 6;
}

// ListFulfillmentOrdersReq 工作台查单请求
message ListFulfillmentOrdersReq {
  // 单类型：1=自提核销 2=送货上门
  int32 order_type = 1;
  int64 community_id = 2;
  // 送货单按指派履约人员过滤（自提单忽略此字段，本社区全部可见可核销）
  int64 delivery_user_id = 3;
  // 状态组：0=进行中（待处理 2/3 + 配送中 4） 5=已完成/已核销（status=5）
  int32 status_group = 4;
  common.Pagination pagination = 5;
}

message ListFulfillmentOrdersResp {
  repeated OrderInfo list = 1;
  common.PaginationResponse pagination = 2;
}

// StartDeliveryReq 开始配送请求（status 2/3 → 4）
message StartDeliveryReq {
  int64 order_id = 1;
  // 操作的履约人员（须与订单指派人一致）
  int64 delivery_user_id = 2;
  int64 community_id = 3;
  string operator_type = 4;
  int64 operator_id = 5;
}

// CompleteDeliveryReq 送达完成请求（status 4 → 5，强制拍照留证）
message CompleteDeliveryReq {
  int64 order_id = 1;
  int64 delivery_user_id = 2;
  int64 community_id = 3;
  string operator_type = 4;
  int64 operator_id = 5;
  // 签收凭证照片 URL（1~N 张，服务端强制非空，决策点②）
  repeated string proof_images = 6;
}

// --- 收银台交班体系 ---

// StartShiftReq 开工请求：创建进行中班次
message StartShiftReq {
  int64 community_id = 1;
  int64 operator_id = 2;
  string operator_name = 3;
  string note = 4;
}

// GetCurrentShiftReq 当前进行中班次查询（operator_id>0 限定该操作员）
message GetCurrentShiftReq {
  int64 community_id = 1;
  int64 operator_id = 2;
}

// EndShiftReq 交班请求：生成统计快照 → 置已结束
message EndShiftReq {
  int64 community_id = 1;
  int64 operator_id = 2;
  string note = 3;
}

// ListShiftsReq 交班记录分页
message ListShiftsReq {
  int64 community_id = 1;
  // 0=本店全部班次（有 order:view 权限时）；>0=仅该操作员
  int64 operator_id = 2;
  common.Pagination pagination = 3;
  // 开班时间下限（YYYY-MM-DD HH:MM:SS，空=不过滤），交班记录近7天/近30天筛选
  string start_time = 4;
}

// PosShiftStatItem 班次统计分组项（按支付方式）
message PosShiftStatItem {
  // 支付方式（1微信 2支付宝 4现金，后续付款码 5/6 自动纳入）
  int32 pay_type = 1;
  double amount = 2;
  int32 order_count = 3;
}

// PosShiftStatistics 班次统计（收款/退款按支付方式分组）
// 进行中班次为实时计算，已结束班次为交班时的快照
message PosShiftStatistics {
  // 收款金额按支付方式分组
  repeated PosShiftStatItem income = 1;
  // 退款金额按支付方式分组（按订单归属班次口径，关联订单支付方式）
  repeated PosShiftStatItem refund = 2;
  // 收款合计
  double total_income = 3;
  // 退款合计
  double total_refund = 4;
  // 现金合计（现金收款 - 现金退款，钱箱应交金额）
  double cash_total = 5;
  // 班内有效订单数（排除待支付/已取消）
  int32 order_count = 6;
}

// PosShiftInfo 班次信息
message PosShiftInfo {
  int64 id = 1;
  int64 community_id = 2;
  int64 operator_id = 3;
  string operator_name = 4;
  string start_time = 5;
  // 进行中为空
  string end_time = 6;
  // 1进行中 2已结束
  int32 status = 7;
  string note = 8;
  // 统计（无班次时整体缺省）
  PosShiftStatistics statistics = 9;
  string created_at = 10;
}

// ListShiftsResp 交班记录分页响应
message ListShiftsResp {
  repeated PosShiftInfo list = 1;
  common.PaginationResponse pagination = 2;
}

// --- 应用偏好（应用页「常用」，社区级单行） ---

message GetAppPreferencesReq {
  int64 community_id = 1;
}

message AppPreferencesInfo {
  // 常用应用 key 列表（有序，前端按此置顶）
  repeated string app_keys = 1;
}

message SaveAppPreferencesReq {
  int64 community_id = 1;
  // 全量保存（覆盖式）
  repeated string app_keys = 2;
}
```

**取消语义与并发约束（重要）：**
- 目标状态 `6`（取消）不走通用状态机：`UpdateOrderStatus` 收到 `status=6` 时进入独立取消分支——校验当前状态为 1（待支付）、条件更新状态、写 `cancel_reason/cancel_time` 并逐行调用 product-service `ReleaseStock` 回补库存。后台取消复用该分支（`operator_type=admin`），C 端 `CancelOrder` 有额外归属校验。
- `UpdateOrderStatus` 全部状态更新均按 `WHERE status = fromStatus` 条件执行，`RowsAffected==0` 时返回 `FailedPrecondition`（订单状态已变更），避免多端并发后写覆盖前写。
- 发货（`status=4`）时将 `express_company/express_no` 写入独立字段。
- 订单号冲突重试：`CreateOrder` 生成订单号遇唯一约束冲突时自动换号重试，避免极小概率的号段碰撞导致下单失败。
- 取消/超时关单后同步调用 payment 服务 `ClosePayments` 关闭待支付支付单（`status=5 已关闭`），防迟到回调误置已支付。

**C 端用户删除订单（软删除，2026-10-23 新增）：** `DeleteOrder` 仅终态订单（`5已完成 6已取消 8已退款`）可删，删除为用户侧软删除（`orders.user_deleted=1`）。可见性由 `include_user_deleted` 标志显式区分，**不能用 `user_id` 是否非零推断**——后台「按会员查订单」同样传 `user_id`，靠它推断会把已删单从客服视图一并抹掉：C 端 `ListOrders`、`GetUserOrderStatusCounts` 角标统计与 C 端购物车「常买商品」均走默认口径（`false`=排除已删单）；后台管理端 `ListAdminOrders` 与会员订单弹窗（订单 Tab + 常买 Tab）显式传 `true` 看全量，并透传 `OrderInfo.user_deleted` 供前端打标展示。`GetOrder` 详情对已删单保留可达（售后进度/支付详情链路依赖）。

守卫与并发：`user_id<=0` 直接拒绝（否则命中收银台散客单 `user_id=0` 形成跨用户删除）；`FOR UPDATE` 锁行后校验归属；`source=2` 门店/收银台代客单拒绝（这类单允许商家代客退款，删掉后用户在等一笔钱却看不到售后进度入口）；存在进行中售后（`refunds.status IN (1待审核, 4退款中)`）时拒绝；条件更新 `WHERE user_deleted=0 AND status IN (5,6,8)` 防并发重复删除与状态竞态。重复删除幂等成功（不报错、不写状态流转日志、不清理任何数据）。

**后台恢复（`RestoreOrder`）：** 客服场景「我删错了，帮我找回上次买的东西」，仅把 `user_deleted` 复位为 0，不改状态、不补状态日志。`community_id>0` 限定在该社区范围内操作（社区管理员不得跨社区恢复），`=0` 为超管不限；`RowsAffected==0` 统一返回 `NotFound`「未找到需恢复的订单」，不透露其他社区是否存在该单。

**退款社区隔离强校验：** `ApproveRefund`/`RejectRefund`/`RetryRefund`（出款链路）/`ResetRefundRetry`（恢复收敛链路，2026-10-24 B2）与 `AdminCreateRefund` 的 `community_id` 由网关从当前社区语境注入，order 服务端校验退款单归属订单的社区，跨社区操作直接拒绝；调度器等内部系统调用传 0 跳过校验。退款申请在事务内先锁订单行（FOR UPDATE）再查 pending，串行化同订单重复申请。

**后台代客退款模式与金额口径：** `AdminCreateRefund` 的 `mode` 区分两种退款：`0/1` 按商品退（`items` 为空即整单退，退款完成后调用 `ReleaseStock` 回补库存）；`2` 仅金额退（不退商品、不回补库存，`items` 必须为空，`amount` 必填且不超过订单剩余可退，落地为 `RefundInfo.type=3`，钱款全额退完时展示为 `1` 整单退）。金额一律以实付口径服务端汇算（含收银台手动优惠分摊），`amount` 仅允许在汇算值基础上调低；库存释放按 `refund_no` 作为幂等业务号，支持同一订单多笔部分退款各自独立生效。订单详情 `OrderDetail.refunds` 返回本单全部售后记录（金额/类型/状态/明细）供后台与 C 端展示。

**收银台（POS）语义：** `CreateOrder` 支持收银台代客下单（`source=2`，`delivery_type=3` 到店即买即走）：单品改价走 `items[].price`（服务端快照原价到 `order_items.original_price/price_modified`）；整单优惠走 `manual_discount`（实付 = max(0, 应付 − manual_discount)，超应付拒绝）；收银单必须携带进行中班次 `shift_id`。挂单三 RPC（创建/列表/删除）仅暂存草稿，不生成订单、不占库存，删除按社区隔离。交班四 RPC（开工/当前/交班/记录）围绕 pos_shifts 表：同操作员同社区同时仅一条进行中班次；交班时冻结统计快照（收款 JOIN payments 限 `status=2`、退款按订单归属班次、现金合计仅取 pay_type=4）。

**履约工作台语义：** `AssignDelivery` 仅配送单 status 2/3 可指派（0=取消指派，服务端校验目标用户履约身份与社区归属）；`StartDelivery`/`CompleteDelivery` 由履约人员操作，须与订单指派人一致；`CompleteDelivery` 强制携带签收照片（1~N 张）；`VerifyPickup` 后台兜底核销与履约核销共用同一校验（取货码匹配 + 社区归属 + 状态检查，重复核销拒绝）。
