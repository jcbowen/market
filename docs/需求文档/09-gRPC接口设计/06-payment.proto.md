### payment.proto

```protobuf
syntax = "proto3";

package payment;

option go_package = "github.com/jcbowen/market/src/backend/common/proto/payment";

import "common/common.proto";

service PaymentService {
  rpc CreatePayment(CreatePaymentReq) returns (CreatePaymentResp);
  rpc GetPayment(GetPaymentReq) returns (PaymentInfo);
  rpc HandleWechatCallback(WechatCallbackReq) returns (common.CommonResponse);
  rpc HandleAlipayCallback(AlipayCallbackReq) returns (common.CommonResponse);
  rpc ProcessRefund(ProcessRefundReq) returns (common.CommonResponse);
}

message CreatePaymentReq {
  int64 order_id = 1;
  int64 user_id = 2;
  int32 pay_type = 3;       // 1微信 2支付宝 3现金
  double amount = 4;
  string openid = 5;        // 微信支付需要
}

message CreatePaymentResp {
  string payment_no = 1;
  int32 pay_type = 2;
  double amount = 3;
  string wechat_pay_params = 4;   // JSON string
  string alipay_pay_params = 5;   // JSON string
}

message GetPaymentReq {
  int64 order_id = 1;
}

message PaymentInfo {
  int64 id = 1;
  string payment_no = 2;
  int64 order_id = 3;
  int64 user_id = 4;
  int32 pay_type = 5;
  double amount = 6;
  int32 status = 7;
  string transaction_id = 8;
  string pay_time = 9;
}

message WechatCallbackReq {
  bytes body = 1;           // 微信回调原始 XML
}

message AlipayCallbackReq {
  map<string, string> params = 1;  // 支付宝回调参数
}

message ProcessRefundReq {
  int64 order_id = 1;
  string refund_no = 2;
  double amount = 3;
}
```

