### cart.proto

```protobuf
syntax = "proto3";

package cart;

option go_package = "github.com/jcbowen/market/src/backend/common/proto/cart";

import "common/common.proto";

service CartService {
  rpc GetCart(GetCartReq) returns (CartResp);
  rpc AddItem(AddItemReq) returns (CartResp);
  rpc UpdateItem(UpdateItemReq) returns (CartResp);
  rpc RemoveItem(RemoveItemReq) returns (CartResp);
  rpc SelectAll(SelectAllReq) returns (CartResp);
  rpc ClearCart(ClearCartReq) returns (common.CommonResponse);
  rpc GetSelectedItems(GetSelectedItemsReq) returns (CartResp);
  rpc ClearInvalidItems(ClearInvalidItemsReq) returns (CartResp);
}

message GetCartReq {
  int64 user_id = 1;
  int64 community_id = 2;
}

message AddItemReq {
  int64 user_id = 1;
  int64 community_id = 2;
  int64 product_id = 3;
  int64 sku_id = 4;
  int32 quantity = 5;
}

message UpdateItemReq {
  int64 id = 1;
  int64 user_id = 2;
  int32 quantity = 3;
  bool selected = 4;
}

message RemoveItemReq {
  int64 id = 1;
  int64 user_id = 2;
}

// 全选/取消全选。
// community_id > 0 时仅更新该社区的购物车项（购物车页分组全选），
// 为 0 时更新全部购物车项（整页全选）
message SelectAllReq {
  int64 user_id = 1;
  bool selected = 2;
  int64 community_id = 3;
}

message ClearCartReq {
  int64 user_id = 1;
}

message GetSelectedItemsReq {
  int64 user_id = 1;
}

message ClearInvalidItemsReq {
  int64 user_id = 1;
}

message CartItem {
  int64 id = 1;
  int64 product_id = 2;
  int64 sku_id = 3;
  string product_name = 4;
  string spec_name = 5;
  string product_image = 6;
  double price = 7;
  int32 stock = 8;
  int32 quantity = 9;
  bool selected = 10;
  int64 community_id = 11;
  int32 min_buy = 12;
  int32 sku_total = 13; // 商品 SKU 总数（前端据此区分单/多规格，仅多规格展示切换入口）
  bool invalid = 14; // 是否失效（商品下架/删除、SKU 删除、库存为 0）
  string invalid_reason = 15; // 失效原因文案（如"商品已下架"），未失效为空
  string stock_hint = 16; // 库存紧张提示文案（如"仅剩 5 件"），库存充足为空
}

message CartResp {
  repeated CartItem items = 1;
}
```
