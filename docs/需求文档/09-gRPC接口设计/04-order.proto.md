
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
}

message RejectRefundReq {
  int64 id = 1;
  int64 operator_id = 2;
  string reject_reason = 3;
}
```
