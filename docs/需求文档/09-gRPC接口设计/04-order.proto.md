
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
  rpc UpdateOrderStatus(UpdateOrderStatusReq) returns (common.CommonResponse);

  // 退款
  rpc ApplyRefund(ApplyRefundReq) returns (RefundInfo);
  rpc ListRefunds(ListRefundsReq) returns (ListRefundsResp);
  rpc ApproveRefund(ApproveRefundReq) returns (common.CommonResponse);
  rpc RejectRefund(RejectRefundReq) returns (common.CommonResponse);
  // 重试卡在"退款中"(status=4) 的退款单：以退款单自身的 payment_no 重新发起渠道退款
  rpc RetryRefund(RetryRefundReq) returns (common.CommonResponse);
}

message OrderItem {
  int64 sku_id = 1;
  int32 quantity = 2;
}

message CreateOrderReq {
  int64 user_id = 1;
  int64 community_id = 2;
  int32 delivery_type = 3;
  int64 address_id = 4;
  string delivery_time = 5;
  string remark = 6;
  int64 coupon_id = 7;
  repeated OrderItem items = 8;
}

message GetOrderReq {
  int64 id = 1;
}

message ListOrdersReq {
  int64 user_id = 1;
  int64 community_id = 2;
  int32 status = 3;
  common.Pagination pagination = 4;
  // --- 后台管理端搜索条件（C 端列表不使用） ---
  string order_no = 5;        // 订单号模糊匹配
  string start_time = 6;      // 下单时间起（YYYY-MM-DD）
  string end_time = 7;        // 下单时间止（YYYY-MM-DD）
  string receiver_name = 8;   // 收货人姓名模糊匹配
  string receiver_phone = 9;  // 收货人电话模糊匹配
}

message CancelOrderReq {
  int64 id = 1;
  int64 user_id = 2;
  string reason = 3;
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
  string remark = 14;
  string pay_time = 15;
  string deliver_time = 16;
  string complete_time = 17;
  string cancel_time = 18;
  string cancel_reason = 19;
  string created_at = 20;
  string express_company = 22;  // 物流公司（发货时写入，独立字段）
  string express_no = 23;       // 物流单号
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
}

message ListOrdersResp {
  repeated OrderInfo list = 1;
  common.PaginationResponse pagination = 2;
}

message ApplyRefundReq {
  int64 order_id = 1;
  int64 user_id = 2;
  double amount = 3;
  string reason = 4;
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
}

message ListRefundsReq {
  int64 community_id = 1;
  int32 status = 2;
  common.Pagination pagination = 3;
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
}

message RejectRefundReq {
  int64 id = 1;
  int64 operator_id = 2;
  string reject_reason = 3;
}

// RetryRefundReq 重试退款请求（针对卡在退款中 status=4 的退款单）
message RetryRefundReq {
  int64 id = 1;
  int64 operator_id = 2;  // 0 表示系统（调度器）自动重试
}
```

**取消语义与并发约束（重要）：**
- 目标状态 `6`（取消）不走通用状态机：`UpdateOrderStatus` 收到 `status=6` 时进入独立取消分支——校验当前状态为 1（待支付）、条件更新状态、写 `cancel_reason/cancel_time` 并逐行调用 product-service `ReleaseStock` 回补库存。后台取消复用该分支（`operator_type=admin`），C 端 `CancelOrder` 有额外归属校验。
- `UpdateOrderStatus` 全部状态更新均按 `WHERE status = fromStatus` 条件执行，`RowsAffected==0` 时返回 `FailedPrecondition`（订单状态已变更），避免多端并发后写覆盖前写。
- 发货（`status=4`）时将 `express_company/express_no` 写入独立字段。
