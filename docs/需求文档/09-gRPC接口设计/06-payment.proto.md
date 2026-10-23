### payment.proto

以 `src/backend/common/proto/payment/payment.proto` 为唯一依据，下表为其完整定义。

```protobuf
syntax = "proto3";

package payment;

option go_package = "github.com/jcbowen/market/src/backend/common/proto/payment";

import "common/common.proto";

service PaymentService {
  rpc CreatePayment(CreatePaymentReq) returns (CreatePaymentResp);
  // PayByAuthCode 付款码扣款（收银台 B扫C）：对已有待支付支付单提交顾客付款码向渠道收款
  // 两步化的第二步：CreatePayment 只建待支付单（pay_type 5/6），扫码后由本方法完成扣款
  rpc PayByAuthCode(PayByAuthCodeReq) returns (PayByAuthCodeResp);
  rpc GetPayment(GetPaymentReq) returns (PaymentInfo);
  // BatchGetPayments 按订单批量取支付记录：后台订单列表按行显示支付方式，替代逐行 GetPayment 的 N 次往返
  rpc BatchGetPayments(BatchGetPaymentsReq) returns (BatchGetPaymentsResp);
  rpc HandleWechatCallback(WechatCallbackReq) returns (common.CommonResponse);
  rpc HandleAlipayCallback(AlipayCallbackReq) returns (common.CommonResponse);
  rpc ProcessRefund(ProcessRefundReq) returns (common.CommonResponse);
  rpc GetPayConfig(GetPayConfigReq) returns (PayConfig);
  rpc SavePayConfig(PayConfig) returns (common.CommonResponse);
  rpc UploadPayCert(UploadPayCertReq) returns (UploadPayCertResp);
  // 关闭订单的全部待支付支付单（订单取消/超时取消后调用，防迟到回调误置已支付）
  rpc ClosePayments(ClosePaymentsReq) returns (common.CommonResponse);
  // SyncPaymentFromChannel 主动向渠道查询收款结果并在已收款时补同步
  // 两个消费方：后台订单详情「同步支付状态」按钮；order 服务取消前置确认（order_id 单入参即查该单待支付单）
  rpc SyncPaymentFromChannel(SyncPaymentReq) returns (SyncPaymentResp);
  // ListOrderPayments 按订单取全部支付尝试（含未成功），供后台详情展示支付次数与状态
  // 与 BatchGetPayments 的区别：后者只回口径与 GetPayment 一致的成功单，无法反映失败/待支付记录
  rpc ListOrderPayments(ListOrderPaymentsReq) returns (ListOrderPaymentsResp);
  // ListPaymentAnomalies 支付异常队列：回调验签失败、金额不符、对账补同步、挂账检出、放弃对账
  rpc ListPaymentAnomalies(ListPaymentAnomaliesReq) returns (ListPaymentAnomaliesResp);
  // MarkPaymentAnomalyHandled 异常销账（人工在渠道侧核对完成后标记已处理）
  rpc MarkPaymentAnomalyHandled(MarkPaymentAnomalyReq) returns (common.CommonResponse);
  // MarkPaymentPaid 人工把指定待支付单标记为已支付（渠道查询失效但钱确实到账时的最后出路）
  // 必填渠道交易号与实际收款金额，仅对待支付订单开放，与「同步支付状态」形成分工：后者问渠道，前者凭人工核实
  rpc MarkPaymentPaid(MarkPaymentPaidReq) returns (common.CommonResponse);
}

message CreatePaymentReq {
  int64 order_id = 1;
  int64 user_id = 2;
  int32 pay_type = 3;       // 1微信 2支付宝 3现金 4门店现金 5微信付款码 6支付宝付款码
  double amount = 4;
  string openid = 5;        // 微信 JSAPI 需要，由网关从用户资料解析后传入；为空走微信 H5 支付
  string app_id = 6;        // 微信下单使用的 AppID（JSAPI/H5），由网关按客户端渠道解析后传入；为空回落支付配置默认 AppID
  // 订单所属社区：由网关从订单详情取，支付服务落库后据此按社区隔离支付异常队列。
  // 下单入口均先查订单再创建支付单，社区随订单带入，不由前端提交
  int64 community_id = 7;
  // 订单号：同样随订单带入。异常流水需要订单号快照供后台阅读，
  // 而 payment 服务不查 orders 表，只能在创建支付单时把订单事实一起落库
  string order_no = 8;
  // 顾客付款码：付款码支付（pay_type 5/6）扣款用。两步化流程下 CreatePayment 不填
  // （只建待支付单不扣款），扫码后由 PayByAuthCode 传入
  string auth_code = 9;
}

message CreatePaymentResp {
  string payment_no = 1;
  int32 pay_type = 2;
  double amount = 3;
  string wechat_pay_params = 4;   // JSON string，仅 pay_type=1 时填充
  string alipay_pay_params = 5;   // JSON string，仅 pay_type=2 时填充；现金支付两者均为空
}

// PayByAuthCodeReq 付款码扣款入参（收银台 B扫C）
// 定位待支付单以 payment_no 为准（下单响应已回传，收银台持有）；同订单多笔待支付单时保证精确。
// order_id 为网关侧经社区隔离校验的订单 ID，服务端强校验与支付单的绑定关系，
// 防止传错/恶意传其它订单的支付单号造成跨订单收款
message PayByAuthCodeReq {
  string payment_no = 1;  // 待支付支付单号（CreatePayment 响应返回）
  string auth_code = 2;   // 顾客付款码（扫码枪读取：微信 10-15 开头 / 支付宝 25-30 开头）
  // 收银员操作人：写入支付单确认来源流水备查
  int64 operator_id = 3;
  // 订单 ID（URL 路径参数，必传）：与支付单 OrderID 不一致时拒绝扣款
  int64 order_id = 4;
}

// PayByAuthCodeResp 付款码扣款结果（归一化）
// state：PAID 扣款成功（订单已同步已支付）；PAYING 用户支付中（余额输密码等），收银台继续轮询
// payment-sync；FAILED 扣款失败（渠道拒绝/余额不足等），message 带原因，收银台提示重新扫码
message PayByAuthCodeResp {
  string state = 1;          // PAID / PAYING / FAILED
  string message = 2;        // 失败原因或渠道状态描述，仅 FAILED/PAYING 时有意义
  string payment_no = 3;
  int64 order_id = 4;
  string transaction_id = 5; // 渠道交易号，仅 PAID 时有值
  int32 pay_type = 6;
  double amount = 7;
}

message GetPaymentReq {
  int64 order_id = 1;
}

// GetPayment 口径（2026-10-23 修正）：返回该订单最近一笔「有效」支付单——
// status 为支付成功(2)或已退款(4)。已退款单的支付单状态被置 4，
// 原先只查 status=2 导致已退款订单详情支付方式显示「未支付」，现退款后仍返回该笔（Status=4），调用方按需甄别。
// BatchGetPayments 与本口径保持一致。
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
  // 确认来源：0未确认 1渠道回调 2对账补偿 3后台手动同步 4线下/收银台
  int32 confirm_source = 10;
  // 来源凭据：cancel-guard / manual-sync / manual-mark / reconcile:支付单号
  string confirm_ref = 11;
  // 渠道回查次数与最近回查时间（判断渠道是否仍在被反复打扰）
  int32 reconcile_count = 12;
  string last_reconciled_at = 13;
  string created_at = 14;
}

// BatchGetPaymentsReq 支付记录批量查询（后台订单列表按行取支付方式，替代逐行 GetPayment）
// 每个订单可能有多条支付记录，服务端按订单只回传口径与 GetPayment 一致的那条（成功 2 或已退款 4）
message BatchGetPaymentsReq {
  repeated int64 order_ids = 1;
}

message BatchGetPaymentsResp {
  repeated PaymentInfo list = 1;
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
  // 适用客户端渠道（勾选的客户端渠道 code 列表：mp/gzh/app/h5；空=全部客户端渠道可用，兼容存量数据）
  repeated string wechat_client_channels = 19;
  repeated string alipay_client_channels = 20;
  // 支付宝网关地址，空=生产环境 https://openapi.alipay.com（填沙箱网关即切沙箱）
  string alipay_gateway_url = 21;
  // 支付宝配置自检失败原因，空表示自检通过；就绪状态为「字段齐全且自检通过」
  string alipay_ready_message = 22;
  // 支付宝加签方式：public 公钥模式（默认，空串兼容存量）/ cert 证书模式
  string alipay_mode = 23;
  // 证书模式三证书上传状态（内容存库仅走上传接口，此处只回传标志，读写均不携带内容）
  bool alipay_app_cert_uploaded = 24;
  bool alipay_public_cert_uploaded = 25;
  bool alipay_root_cert_uploaded = 26;
}

message UploadPayCertReq {
  string channel = 1;    // 渠道：wechat / alipay
  string cert_type = 2;  // 类型：private_key（商户/应用私钥）、app_cert / alipay_public_cert / root_cert（支付宝证书模式）
  string filename = 3;   // 原始文件名，用于校验扩展名
  bytes content = 4;     // 证书文件内容
}

message UploadPayCertResp {
  bool success = 1;
  string message = 2;
}

// SyncPaymentReq 渠道查询入参：order_id 与 payment_no 二选一
// 仅给 order_id 时，服务端取该订单最近一笔待支付的渠道支付单（pay_type 1/2）去查
message SyncPaymentReq {
  int64 order_id = 1;
  string payment_no = 2;
  // 触发场景，写入 payments.confirm_ref 便于区分「后台按钮」与「取消前置确认」：
  // manual-sync / cancel-guard；为空按 manual-sync
  string reason = 3;
}

// SyncPaymentResp 渠道查询结果
// paid=渠道是否已收款；changed=本次是否发生了状态补同步
message SyncPaymentResp {
  bool paid = 1;
  bool changed = 2;
  // 归一化渠道状态：PAID/PAYING/CLOSED/NOT_EXIST/ERROR/NO_PAYMENT
  string channel_state = 3;
  string transaction_id = 4;
  double amount = 5;
  string payment_no = 6;
  string message = 7;
}

message ListOrderPaymentsReq {
  int64 order_id = 1;
}

message ListOrderPaymentsResp {
  repeated PaymentInfo list = 1;
}

// PaymentAnomalyInfo 支付通知/对账流水一行
// raw_body/headers 仅当请求 with_raw=true（网关校验 payment:raw:view 权限）时回填
message PaymentAnomalyInfo {
  int64 id = 1;
  // 1渠道回调 2对账补同步 3挂账检出 4渠道关单 5后台手动同步 6放弃对账
  int32 kind = 2;
  // 1确认成功 2幂等忽略 3验签或解密失败 4金额不符 5处理异常 6渠道未支付 7渠道无此交易 8关单结果 9资金挂账
  int32 result = 3;
  // 1微信 2支付宝
  int32 channel = 4;
  string payment_no = 5;
  int64 order_id = 6;
  string error_msg = 7;
  int32 handled = 8;
  string created_at = 9;
  string raw_body = 10;
  string headers = 11;
  int32 pay_type = 12;
  double amount = 13;
  // 归属社区：验签失败等解析不出支付单的流水为 0（仅超管可见全量队列）
  int64 community_id = 14;
  // 订单号快照：支付单解析不出时（order_id=0）后台仍能按订单号定位
  string order_no = 15;
}

// ListPaymentAnomaliesReq 异常队列查询
// handled：0全部 1仅待处理 2仅已处理；include_ok=true 时把确认成功的流水一并返回（订单详情时间线用）
// community_id>0 时只回该社区的流水（普通管理员），0 表示不限（超管）；
// 验签失败等解析不出支付单的流水 community_id 为 0，故只对超管可见
message ListPaymentAnomaliesReq {
  int32 handled = 1;
  int64 order_id = 2;
  int32 page = 3;
  int32 page_size = 4;
  bool with_raw = 5;
  bool include_ok = 6;
  int64 community_id = 7;
}

message ListPaymentAnomaliesResp {
  repeated PaymentAnomalyInfo list = 1;
  int32 total = 2;
  // 待处理条数（异常页红标）
  int32 pending = 3;
}

// MarkPaymentAnomalyReq 销账；remark 记录人工核实依据
// community_id>0 时服务端只允许销该社区的流水（普通管理员），0 表示不限（超管）
message MarkPaymentAnomalyReq {
  int64 id = 1;
  string remark = 2;
  int64 community_id = 3;
}

// MarkPaymentPaidReq 人工标记已支付入参
// transaction_id/amount 均由人工从商户平台抄录，服务端与应付金额做分位比对后才肯确认
message MarkPaymentPaidReq {
  string payment_no = 1;
  string transaction_id = 2;
  double amount = 3;
  string remark = 4;
}
```

### 配置管理说明

支付配置存储于数据库 pay_config 表（单行），由 payment 服务统一管理，gateway 通过 `GetPayConfig/SavePayConfig/UploadPayCert` 三个 RPC 读写，不直接访问数据库。

热更新机制：payment 服务进程内缓存配置副本，每次支付链路调用时对比数据库 `updated_at` 版本号——版本一致时直接复用已构建的微信支付客户端（零重建开销），版本变更时才刷新缓存并重建客户端。后台保存配置后即时生效，无需重启。

### 支付回调与关单语义

- **回调金额强校验**：`HandleWechatCallback` 落账前将回调报文中的金额与支付单 `amount` 强制比对（注意微信侧单位为分、支付单为元），不一致直接拒绝并返回失败，防止篡改金额或串单；`HandleAlipayCallback` 同口径。
- **支付宝回调验签（已实装）**：网关 `POST /api/v1/payments/callback/alipay` 为公开路由，验签在 payment 服务 `parseAlipayCallback` 内按加签方式分流——公钥模式用 `alipaysdk.RsaCheckV1` + `alipay_public_key`；证书模式用 `alipaysdk.RsaCheckV1WithCert`，按通知的 `alipay_cert_sn` 在 `alipay_public_cert`（命中则取证书公钥）与 `alipay_root_cert`（逐段比对序列号）中定位验签公钥，序列号不匹配即拒绝。随后依次校验通知身份 `app_id` 与本地配置一致、`trade_status` 仅限 `TRADE_SUCCESS`/`TRADE_FINISHED`、`total_amount` 解析成功且大于 0，任一项不过即拒绝处理。当前模式所需验签载体缺失时直接拒绝，不存在「未验签放行」的路径。
- **支付宝加签方式（2026-10-20 新增）**：`alipay_mode`（23 号字段）决定必填口径与录入面，公钥模式需 `alipay_public_key`，证书模式需三证书；证书内容刻意义不进 `PayConfig`（保存是单行全量覆写，携带内容字段会把未提交的证书清空），只经 `UploadPayCert` 写入并以 `alipay_app_cert_uploaded`/`alipay_public_cert_uploaded`/`alipay_root_cert_uploaded`（24/25/26 号字段）回传状态。`UploadPayCert` 对支付宝四类内容先校验可解析性再落库，校验失败以 `codes.FailedPrecondition` 原样透传原因（`codes.Internal` 网关不透传原始消息）。
- **支付宝渠道开关**：就绪判定为「当前加签方式的必填字段齐全且配置自检通过」（`alipay_ready` + `alipay_ready_message`，见 `PayConfig` 18/22 号字段），配置来源是数据库 `pay_config` 单行而非环境变量；就绪即可下单、回调与退款。当前仅实现手机网站支付（`alipay.trade.wap.pay`），小程序/公众号客户端渠道不支持，网关会明确拒绝。
- **回调幂等**：置支付成功采用条件更新 `WHERE payment_no=? AND status=1`，重复回调与并发回调只生效一次；支付单/订单已处于终态时走终态兜底，不再重复推进订单状态。
- **支付确认与订单同步解耦（2026-10-17）**：`handlePaySuccess` 拆分为两阶段——阶段一仅在数据库事务内落支付单成功状态（不依赖 order-service 可用性，事务内不再有跨服务 RPC）；阶段二在事务提交后调用 order 服务 `UpdateOrderStatus` 同步订单状态，失败重试 3 次仍不成功时记录 `[CRITICAL]` 日志（支付已确认但订单状态未同步，需人工介入排查），不再因跨服务 RPC 失败回滚已确认的支付，避免回调超时导致渠道重复通知与支付状态反复。
- **ClosePayments**：订单取消（用户取消/后台取消）或调度器超时关单后，由 order 服务调用本 RPC，将该订单下全部 `status=1`（待支付）支付单条件更新为 `status=5 已关闭`；此后到达的迟到回调因状态不匹配被幂等拦截，防止"已关单却回调成功"造成资损。`ClosePaymentsReq` 按订单维度（`order_id`）批量关闭，无需逐单枚举支付单号。2026-10-21 起在本地关单前先逐单 `inspectForClose` 向渠道核实：渠道已收款 → 记挂账流水（`kind=3`/`result=9`）进异常队列且不改本地状态；`NOT_EXIST` → 无渠道交易可关，跳过；其余未收款 → 调渠道关单（支付宝 `/v3/alipay/trade/close`、微信 `Client.CloseOrder`），最后才做本地条件更新。现金/货到付款在 `queryChannelOrder` 返回「无渠道查询能力」，按查询失败跳过（不猜资金状态也不调渠道）。渠道语义不可颠倒：两侧都只允许关闭未支付交易，对已付款交易返回业务失败且不会自动退款。
- **支付单状态**：1待支付 2支付成功 3支付失败 4已退款 5已关闭（`5` 为本次新增，不复用 `3 支付失败`——后者语义为渠道侧确认失败）。

### 渠道为准的核实与人工处置语义（2026-10-21 新增）

四个主动入口共用唯一收敛点 `handlePaySuccess`，任何入口都不得自行写订单状态：

| RPC | 用途 | 来源标记 |
|---|---|---|
| `SyncPaymentFromChannel` | 向渠道核实并补同步（后台按钮、取消前置确认共用） | `confirm_source=3`，`confirm_ref` 由 `reason` 决定 |
| 对账调度器 | 定时回查待支付单（内部复用同一核实逻辑） | `confirm_source=2`，`confirm_ref=reconcile:支付单号` |
| `MarkPaymentPaid` | 渠道查不到但线下已核实到账的人工确认 | `confirm_source=3`，`confirm_ref=manual-mark` |
| 渠道回调 | 正常快速路径 | `confirm_source=1` |

- `SyncPaymentReq.reason` 的作用即区分「后台按钮捞回」与「取消流程拦下」，异常页与详情时间线据此展示；网关调用不传（按 `manual-sync`），order 服务固定传 `cancel-guard`。
- `SyncPaymentResp` 的 `changed` 只在本次真正发生状态补同步时为 true；`paid=true && changed=false` 表示支付单早已确认（重复点击），`channel_state=ERROR` 表示渠道查询失败，**调用方不得据此判定资金状态**。
- 订单没有待同步的渠道支付单（从未发起支付，或只有现金/货到付款单）时返回 `NO_PAYMENT`，`paid=false`、零状态改动，因此该入口可安全重复点击，也不会凭空判定收款。
- `MarkPaymentPaid` 服务端校验链：入参非空 → 支付单存在（`codes.NotFound`）→ 非现金/货到付款（`FailedPrecondition`）→ 状态为待支付 → 填写金额与应付金额分位一致（不一致提示改用退款）。`remark` 由网关追加操作人后再落库。
- `ListPaymentAnomaliesReq.handled` 是**筛选选择器**（0全部 1仅待处理 2仅已处理），与行数据 `PaymentAnomalyInfo.handled` 的落库值（0待处理 1已处理）口径不同，转换在服务层完成；`community_id=0` 表示不限（超管），验签失败等解析不出支付单的流水 `community_id` 为 0，故只对超管可见。
- `with_raw` 由网关按权限点 `payment:raw:view` 决定（该权限点刻意不进社区管理员默认权限上限），服务层不判权限；`raw_body`/`headers` 仅失败与挂账场景入库，成功回调不落原文，`headers` 中签名与证书序列号入库前掩码。另注意支付宝的 `raw_body` 是**含 `sign` 原文**的通知参数（2026-10-23 放开掩码）：验签类故障只有拿到签名值才能离线复算待验签串，而买家账号等字段本就原文入库，凭据出口统一由该权限点守住（详见 07-基础设施/04-第三方支付对接.md「验签失败取证」）。
- 网关错误映射约定：payment 服务需让运营看到具体原因的业务拒绝一律用 `bizerr.New(codes.InvalidArgument/NotFound/FailedPrecondition, msg)`，`TranslateGRPCError` 才对映射码透传原始文案；裸 `fmt.Errorf` 会落到 Internal/Unknown，前端只显示「服务器内部错误」。

### 客户端渠道相关字段（2026-10-18 新增）

- `PayConfig` 新增 `wechat_client_channels` / `alipay_client_channels`（repeated string）：各支付方式适用的客户端渠道 code 勾选（mp/gzh/app/h5），落库为 `pay_config.wechat_channels/alipay_channels` JSON；空=全部客户端渠道可用（存量兼容口径）。
- `CreatePaymentReq` 新增 `app_id`（field 6）：微信下单使用的 AppID，由网关按客户端渠道解析后传入（小程序=渠道管理的 mp AppID；H5=mp AppID 或默认）；为空回落 pay_config 默认 AppID。wego `JSAPIPay/H5Pay` 支持请求级 AppID 覆盖，无需多客户端实例。
- `CreatePaymentReq` 新增 `community_id`（field 7）与 `order_no`（field 8，2026-10-21）：payment 服务不查 orders 表，社区隔离与异常流水展示所依赖的订单事实只能在创建支付单时随订单一起带入并落库（`payments.community_id`/`order_no`，流水继承该快照）。
- 付款码支付（2026-10-22）：`CreatePaymentReq.pay_type` 放行 5/6（只建待支付单不扣款、拒收 0 元、渠道就绪校验为微信/支付宝任一开启）；新增 `auth_code`（field 9）与 `PayByAuthCode` RPC——渠道按付款码前缀识别（微信 10-15→5，支付宝 25-30→6，占位单原地纠正），结果归一化 PAID/PAYING/FAILED，失败重试复用同一支付单（out_trade_no 不变），云闪付（62 开头）明确拒绝；退款/查单/关单/对账调度器/SyncPaymentFromChannel 全链路按 5→微信、6→支付宝映射（微信付款码关单走撤销 reverse）。
