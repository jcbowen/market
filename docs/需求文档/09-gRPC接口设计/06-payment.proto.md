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
  rpc GetPayConfig(GetPayConfigReq) returns (PayConfig);
  rpc SavePayConfig(PayConfig) returns (common.CommonResponse);
  rpc UploadPayCert(UploadPayCertReq) returns (UploadPayCertResp);
  // 关闭订单的全部待支付支付单（订单取消/超时取消后调用，防迟到回调误置已支付）
  rpc ClosePayments(ClosePaymentsReq) returns (common.CommonResponse);
}

message CreatePaymentReq {
  int64 order_id = 1;
  int64 user_id = 2;
  int32 pay_type = 3;       // 1微信 2支付宝 3现金
  double amount = 4;
  string openid = 5;        // 微信 JSAPI 需要，由网关从用户资料解析后传入；为空走微信 H5 支付
}

message CreatePaymentResp {
  string payment_no = 1;
  int32 pay_type = 2;
  double amount = 3;
  string wechat_pay_params = 4;   // JSON string，仅 pay_type=1 时填充
  string alipay_pay_params = 5;   // JSON string，仅 pay_type=2 时填充；现金支付两者均为空
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
  bytes body = 1;
  map<string, string> headers = 2;
}

message AlipayCallbackReq {
  map<string, string> params = 1;  // 支付宝回调参数
}

message ProcessRefundReq {
  int64 order_id = 1;
  string refund_no = 2;
  double amount = 3;
  string payment_no = 4;
}

message ClosePaymentsReq {
  int64 order_id = 1;
}

message GetPayConfigReq {}

// PayConfig 支付配置（数据库单行存储）
// 读写均通过 payment 服务，gateway 不直接访问数据库
message PayConfig {
  repeated string enabled_channels = 1;
  // 微信支付 V3
  string wechat_app_id = 2;
  string wechat_app_secret = 3;
  string wechat_mch_id = 4;
  string wechat_api_v3_key = 5;
  string wechat_cert_serial_no = 6;
  string wechat_private_key = 7;
  string wechat_notify_url = 8;
  // 支付宝
  string alipay_app_id = 9;
  string alipay_private_key = 10;
  string alipay_public_key = 11;
  string alipay_notify_url = 12;
  string alipay_return_url = 13;
  // 证书上传状态（上传后内容存库，此处仅回传标志）
  bool wechat_cert_uploaded = 14;
  bool alipay_cert_uploaded = 15;
  // 版本与就绪状态
  string updated_at = 16;
  bool wechat_payment_ready = 17;
  bool alipay_ready = 18;
}

message UploadPayCertReq {
  string channel = 1;    // 渠道：wechat / alipay
  string cert_type = 2;  // 证书类型：private_key（商户/应用私钥）等
  string filename = 3;   // 原始文件名，用于校验扩展名
  bytes content = 4;     // 证书文件内容
}

message UploadPayCertResp {
  bool success = 1;
  string message = 2;
}
```

### 配置管理说明

支付配置存储于数据库 pay_config 表（单行），由 payment 服务统一管理，gateway 通过 `GetPayConfig/SavePayConfig/UploadPayCert` 三个 RPC 读写，不直接访问数据库。

热更新机制：payment 服务进程内缓存配置副本，每次支付链路调用时对比数据库 `updated_at` 版本号——版本一致时直接复用已构建的微信支付客户端（零重建开销），版本变更时才刷新缓存并重建客户端。后台保存配置后即时生效，无需重启。

### 支付回调与关单语义

- **回调金额强校验**：`HandleWechatCallback` 落账前将回调报文中的金额与支付单 `amount` 强制比对（注意微信侧单位为分、支付单为元），不一致直接拒绝并返回失败，防止篡改金额或串单；`HandleAlipayCallback` 同口径。支付宝回调路由当前已在网关下线（渠道未接入、回调无 RSA2 验签，公开暴露可被伪造支付成功），接入官方 SDK 完成验签后再开放。
- **回调幂等**：置支付成功采用条件更新 `WHERE payment_no=? AND status=1`，重复回调与并发回调只生效一次；支付单/订单已处于终态时走终态兜底，不再重复推进订单状态。
- **支付确认与订单同步解耦（2026-10-17）**：`handlePaySuccess` 拆分为两阶段——阶段一仅在数据库事务内落支付单成功状态（不依赖 order-service 可用性，事务内不再有跨服务 RPC）；阶段二在事务提交后调用 order 服务 `UpdateOrderStatus` 同步订单状态，失败重试 3 次仍不成功时记录 `[CRITICAL]` 日志（支付已确认但订单状态未同步，需人工介入排查），不再因跨服务 RPC 失败回滚已确认的支付，避免回调超时导致渠道重复通知与支付状态反复。
- **ClosePayments**：订单取消（用户取消/后台取消）或调度器超时关单后，由 order 服务调用本 RPC，将该订单下全部 `status=1`（待支付）支付单条件更新为 `status=5 已关闭`；此后到达的迟到回调因状态不匹配被幂等拦截，防止"已关单却回调成功"造成资损。`ClosePaymentsReq` 按订单维度（`order_id`）批量关闭，无需逐单枚举支付单号。
- **支付单状态**：1待支付 2支付成功 3支付失败 4已退款 5已关闭（`5` 为本次新增，不复用 `3 支付失败`——后者语义为渠道侧确认失败）。
