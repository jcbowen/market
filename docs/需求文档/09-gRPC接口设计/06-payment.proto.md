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
  // ProcessRefund 发起渠道退款。completed=false 表示渠道已受理但未到终态
  // （微信受理常态 PROCESSING），调用方不得按成功收尾，由退款查单任务收敛
  rpc ProcessRefund(ProcessRefundReq) returns (ProcessRefundResp);
  // QueryRefundResult 查询渠道退款流水状态（order 侧卡单退款收敛用，读库不触渠道）
  rpc QueryRefundResult(QueryRefundResultReq) returns (QueryRefundResultResp);
  // ResetRefundFlow 人工重置「待退款」流水的自动收敛预算（B2）
  // 退款查单任务对同一笔流水连续失败达上限后会永久退出自动批次，
  // 而微信退款允许 1-3 个工作日到账，渠道后续给出终态也没人再来收；
  // 本方法把 retry_count/escalated 归零，让流水重新被查单任务看见，
  // 只处理待退款(1)流水——已成功(2)/已失败(3)一律拒绝，避免对已出款流水重复触渠道
  rpc ResetRefundFlow(ResetRefundFlowReq) returns (ResetRefundFlowResp);
  // ListPayConfigs 支付配置列表（管理端，含凭证与就绪状态；网关负责脱敏）
  rpc ListPayConfigs(ListPayConfigsReq) returns (ListPayConfigsResp);
  // SavePayConfigRow 新增（id=0）或更新一条支付配置；updated_at 乐观锁
  rpc SavePayConfigRow(PayConfigRow) returns (PayConfigRow);
  // DeletePayConfigRow 删除一条支付配置（渠道引用校验由网关负责）
  rpc DeletePayConfigRow(DeletePayConfigRowReq) returns (common.CommonResponse);
  // ListActivePayMethods 启用且就绪的支付配置清单（C 端渲染过滤与下单解析用）
  rpc ListActivePayMethods(ListActivePayMethodsReq) returns (ListActivePayMethodsResp);
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
  // GetPaymentStatus 按支付单号查询支付状态（C 端支付宝回跳结果页免登录轮询用）
  // 只回状态级最小字段集：payment_no 本身是不可枚举的能力凭据（ZF+UUID），
  // 不携带用户/商品/地址等隐私信息，网关注册在公开路由组
  rpc GetPaymentStatus(GetPaymentStatusReq) returns (GetPaymentStatusResp);
  // GetAlipayPayUrl 凭支付单号重新生成支付宝手机网站支付收银台链接（H5 支付引导页免登录换取用）
  // 引导页 URL 只携带不可枚举的支付单号，在系统浏览器打开后凭单号实时换取新签名的收银台链接，
  // 规避支付链接（含 # 等保留字符）经 uni-app H5 路由 query 传递时被框架编解码破坏的问题
  // （实测 ? 会被 uni-app 路由解析改写为 =，无论编码几层）；
  // 仅对待支付的支付宝单返回链接，其它状态只回支付单状态，引导页据此跳结果页
  rpc GetAlipayPayUrl(GetAlipayPayUrlReq) returns (GetAlipayPayUrlResp);
  // FindOrdersByPayNo 按支付单号或渠道交易号反查关联订单号（后台订单列表「支付单号/渠道交易号」搜索用）
  // 单框双号，字段渠道无关：payment_no 即渠道账单的「商家订单号」（out_trade_no），
  // transaction_id 即渠道交易号（支付宝 trade_no / 微信 transaction_id）。
  // 精确匹配；同一订单可能有多笔支付尝试，命中后去重回传订单号
  rpc FindOrdersByPayNo(FindOrdersByPayNoReq) returns (FindOrdersByPayNoResp);
}

// FindOrdersByPayNoReq 按支付单号/渠道交易号反查订单
message FindOrdersByPayNoReq {
  string pay_no = 1;
}

// FindOrdersByPayNoResp 反查结果：关联订单号去重列表
message FindOrdersByPayNoResp {
  repeated string order_nos = 1;
}

// GetPaymentStatusReq 按支付单号查询支付状态
message GetPaymentStatusReq {
  string payment_no = 1;
}

// GetPaymentStatusResp 支付状态查询结果（最小字段集）
message GetPaymentStatusResp {
  string payment_no = 1;
  // 支付单状态：1 待支付 2 已支付 4 已退款 5 已关闭
  int32 status = 2;
  double amount = 3;
  int64 order_id = 4;
  string order_no = 5;
}

// GetAlipayPayUrlReq 凭支付单号换取支付宝收银台链接
message GetAlipayPayUrlReq {
  string payment_no = 1;
}

// GetAlipayPayUrlResp 支付宝收银台链接换取结果
// 仅待支付（status=1）且 pay_type=2 时 alipay_pay_url 有值；其它状态只回 status 供引导页分流
message GetAlipayPayUrlResp {
  string payment_no = 1;
  // 支付单状态：1 待支付 2 已支付 3 支付失败 4 已退款 5 已关闭
  int32 status = 2;
  int64 order_id = 3;
  string alipay_pay_url = 4;
}

message CreatePaymentReq {
  int64 order_id = 1;
  int64 user_id = 2;
  int32 pay_type = 3;       // 1微信 2支付宝 3现金 4门店现金 5微信付款码 6支付宝付款码
  double amount = 4;
  string openid = 5;        // 微信 JSAPI 需要，由网关从用户资料解析后传入；为空走微信 H5 支付
  string app_id = 6;        // 微信下单使用的 AppID（JSAPI/H5），由网关按客户端渠道解析后传入；
                            // 微信 AppID 属渠道身份（pay_config 不含该字段），为空即拒绝下单，不回落任何默认值；
                            // 同时作为支付单快照落库（payments.app_id），供回调归属校验与撤销取用
  // 订单所属社区：由网关从订单详情取，支付服务落库后据此按社区隔离支付异常队列。
  // 下单入口均先查订单再创建支付单，社区随订单带入，不由前端提交
  int64 community_id = 7;
  // 订单号：同样随订单带入。异常流水需要订单号快照供后台阅读，
  // 而 payment 服务不查 orders 表，只能在创建支付单时把订单事实一起落库
  string order_no = 8;
  // 顾客付款码：付款码支付（pay_type 5/6）扣款用。两步化流程下 CreatePayment 不填
  // （只建待支付单不扣款），扫码后由 PayByAuthCode 传入
  string auth_code = 9;
  // 支付配置 ID：网关按渠道绑定（client_channels.pay_methods）解析后传入；
  // pay_type 1/2/5/6 必填，服务端校验配置存在、启用、平台匹配且凭证就绪；
  // 现金/货到付款（3/4）不填
  int64 config_id = 10;
  // 客户端渠道 code（X-Channel-Code）：支付宝按它分流下单方式
  // （alimp → alipay.trade.create 小程序内支付，其余 → 手机网站支付）；其余渠道暂未使用
  string client_channel = 11;
}

message CreatePaymentResp {
  string payment_no = 1;
  int32 pay_type = 2;
  double amount = 3;
  string wechat_pay_params = 4;   // JSON string，仅 pay_type=1 时填充
  string alipay_pay_params = 5;   // JSON string，仅 pay_type=2 时填充；
                                  // H5/手机网站支付为 {"alipay_pay_url":"..."}，
                                  // 支付宝小程序为 {"trade_no":"..."}（my.tradePay 用）；现金支付两者均为空
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
  // 支付配置 ID：网关按 pos 渠道绑定解析（付款码前缀识别平台后取对应配置），必填
  int64 config_id = 5;
  // 微信 AppID：网关按 pos 渠道解析后传入，微信付款码扣款必填（支付配置行不再持有 AppID）；
  // 支付宝付款码留空——支付宝的 AppID 是签名身份，随 config_id 定位到配置行
  string app_id = 6;
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
// 每个订单可能有多条支付记录，服务端按订单只回传口径与 GetPayment 一致的那条
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

// ProcessRefundResp 退款处理结果
// completed=false：渠道已受理（退款流水保持待退款），调用方不得走成功收尾，
// 终态由 payment 侧退款查单任务对 RefundQuery 结果收敛
message ProcessRefundResp {
  bool completed = 1;
}

// QueryRefundResultReq 按退款单号查退款流水状态（order_id 仅作归属校验）
message QueryRefundResultReq {
  int64 order_id = 1;
  string refund_no = 2;
}

// QueryRefundResultResp 渠道退款流水状态
// status: 0=流水不存在 1=处理中（已受理待渠道终态） 2=退款成功 3=退款失败（可重新发起）
message QueryRefundResultResp {
  int32 status = 1;
}

// ResetRefundFlowReq 人工重置退款流水的自动收敛预算
// remark 必填：本动作等于「人工核实渠道后重新交给系统收敛」，没有核实依据不许执行
message ResetRefundFlowReq {
  int64 order_id = 1;
  string refund_no = 2;
  string remark = 3;
  int64 operator_id = 4;
  string operator_name = 5;
}

// ResetRefundFlowResp 重置结果
// recovered=false 表示无对应流水（历史失败流水已被清理），此时 message 说明原因、不算错误
// 字段名不叫 reset：与 protobuf 消息自带的 Reset() 方法冲突，生成物会变成 Reset_
message ResetRefundFlowResp {
  bool recovered = 1;
  string message = 2;
}

message ClosePaymentsReq {
  int64 order_id = 1;
}

// PayConfigRow 单条支付配置行（pay_config 多行存储）
// platform 决定凭证字段组：wechat 取 wechat_* 字段，alipay 取 alipay_* 字段；
// 读写均通过 payment 服务，gateway 不直接访问数据库
message PayConfigRow {
  int64 id = 1;
  // 展示名称（如「支付宝-H5」「微信支付-主力」），管理端列表与渠道绑定下拉展示用
  string name = 2;
  // 平台类型：wechat / alipay
  string platform = 3;
  bool enabled = 4;
  // 微信支付 V3 凭证（platform=wechat 时取用）
  // 10 曾为 wechat_app_id：微信 AppID 属渠道身份（client_channels.app_id），
  // 一条配置可服务小程序/H5 多个渠道，落在配置行会强迫一个商户号建多份配置，故移除
  reserved 10;
  reserved "wechat_app_id";
  string wechat_app_secret = 11;
  string wechat_mch_id = 12;
  string wechat_api_v3_key = 13;
  string wechat_cert_serial_no = 14;
  string wechat_private_key = 15;
  string wechat_notify_url = 16;
  // 商户私钥是否已上传（内容存库，此处仅回传标志）
  bool wechat_cert_uploaded = 17;
  // 支付宝凭证（platform=alipay 时取用）
  string alipay_app_id = 20;
  string alipay_private_key = 21;
  string alipay_public_key = 22;
  string alipay_notify_url = 23;
  string alipay_return_url = 24;
  // 加签方式：public 公钥模式（默认）/ cert 证书模式
  string alipay_mode = 25;
  // 支付宝网关地址，空=生产环境 https://openapi.alipay.com（填沙箱网关即切沙箱）
  string alipay_gateway_url = 26;
  // 应用私钥与三证书上传状态（内容存库，此处仅回传标志，读写均不携带内容）
  bool alipay_cert_uploaded = 27;
  bool alipay_app_cert_uploaded = 28;
  bool alipay_public_cert_uploaded = 29;
  bool alipay_root_cert_uploaded = 30;
  // 版本与就绪状态（仅查询回传；就绪 = 启用 + 字段齐全 + 自检通过）
  // 注：异步通知地址属「字段齐全」口径，且只由显式部署域名派生；未配置域名时为空 → 该行未就绪，
  // ready_message 给出「缺少异步通知地址…请在站点设置填写域名或设置 SITE_BASE_URL」的可读原因
  string updated_at = 40;
  bool ready = 41;
  string ready_message = 42;
}

message ListPayConfigsReq {}

message ListPayConfigsResp {
  repeated PayConfigRow list = 1;
}

message DeletePayConfigRowReq {
  int64 id = 1;
}

message ListActivePayMethodsReq {}

// ActivePayMethod 启用且就绪的支付配置摘要（渲染过滤与下单解析用）
message ActivePayMethod {
  int64 config_id = 1;
  // 平台类型：wechat / alipay
  string platform = 2;
  // 3 曾为 app_id：微信 AppID 属渠道身份，由渠道管理提供、建单时随请求传入，
  // 不再由支付配置摘要回传
  reserved 3;
  reserved "app_id";
}

message ListActivePayMethodsResp {
  repeated ActivePayMethod methods = 1;
}

message UploadPayCertReq {
  int64 config_id = 1;   // 支付配置行 ID
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
  // 操作人（2026-10-24 B3）：后台按钮由网关注入，内部路径（订单取消前置确认）留空即系统口径。
  // 只用于流水留痕——挂账待办要能回答「谁核实的、当时本地是什么状态」，
  // 否则事后只剩一条事实记录，责任链断在「反查不到人，也无从判断是人工检出还是调度器检出」
  int64 operator_id = 4;
  string operator_name = 5;
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
  // 1渠道回调 2对账补同步 3挂账检出 4渠道关单 5后台手动同步 6放弃对账 7人工恢复退款收敛 8人工核实挂账检出 9付款码扣款
  int32 kind = 2;
  // 1确认成功 2幂等忽略 3验签或解密失败 4金额不符 5处理异常 6渠道未支付 7渠道无此交易 8关单结果 9资金挂账 10订单同步失败 11人工恢复退款收敛
  int32 result = 3;
  // 1微信 2支付宝 0线下或未知（服务端按 pay_type 归一：付款码 5→1、6→2）
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
  // 检出留痕（B3）：这条流水由谁、在什么本地状态下登记（后台操作人或系统口径）
  string detect_remark = 16;
  // 人工销账依据：销账 SQL 整列覆写本字段，故检出留痕不能写在这里
  string remark = 17;
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
  // 路径中的订单 ID（网关已过社区隔离）：服务层据此校验支付单归属，
  // 防止在 A 订单页面提交 B 订单的支付单号，跨订单确认资金状态
  int64 order_id = 5;
}
```

### 配置管理说明

支付配置存储于数据库 pay_config 表（**多行**，2026-10-30 重构：每行 `name + platform(wechat/alipay) + enabled + 凭证`，同平台可多条、独立启停），由 payment 服务统一管理，gateway 通过 `ListPayConfigs/SavePayConfigRow/DeletePayConfigRow/UploadPayCert(config_id)` 读写，不直接访问数据库。「哪个客户端渠道启用哪些配置」由 user 服务 `client_channels.pay_methods` 绑定表达（见 02-user.proto.md），本表无渠道勾选；`ListActivePayMethods` 返回启用且就绪的配置摘要（config_id/platform；AppID 已随微信 AppID 归属渠道一并从摘要中移除），供网关在 C 端渲染过滤与下单/扣款解析 config_id 时使用。

热更新机制：payment 服务进程内缓存全部配置行（`payClientRegistry`：version = 行数 + 各行 updated_at 拼接），每次支付链路调用时对比版本号——版本一致时直接复用已构建的微信/支付宝客户端（零重建开销），版本变更时才刷新缓存并重建客户端；数据库查询异常时降级使用缓存，不阻断支付。后台保存配置后即时生效，无需重启。

### 支付回调与关单语义

- **回调金额强校验**：`HandleWechatCallback` 落账前将回调报文中的金额与支付单 `amount` 强制比对（注意微信侧单位为分、支付单为元），不一致直接拒绝并返回失败，防止篡改金额或串单；`HandleAlipayCallback` 同口径。
- **支付宝回调验签（已实装）**：网关 `POST /api/v1/payments/callback/alipay` 为公开路由，验签在 payment 服务 `parseAlipayCallback` 内按加签方式分流——公钥模式用 `alipaysdk.RsaCheckV1` + `alipay_public_key`；证书模式用 `alipaysdk.RsaCheckV1WithCert`，通知带 `alipay_cert_sn` 时在 `alipay_public_cert`（命中则取证书公钥）与 `alipay_root_cert`（逐段比对序列号）中按序列号定位验签公钥，定位不到即拒绝；通知未带该字段时直接用 `alipay_public_cert` 里的公钥验签（官方从未承诺异步通知携带该字段，各语言 SDK 的通知验签入口也都不读它；2026-10-23 起 SDK v0.0.6 口径，信任锚仍是本地那份支付宝公钥证书，签名不出自该证书私钥照样拒绝）。随后依次校验通知身份 `app_id` 与本地配置一致、`trade_status` 仅限 `TRADE_SUCCESS`/`TRADE_FINISHED`、`total_amount` 解析成功且大于 0，任一项不过即拒绝处理。当前模式所需验签载体（公钥或证书）缺失时直接拒绝，不存在「未验签放行」的路径。
- **支付宝加签方式（2026-10-20 新增）**：`PayConfigRow.alipay_mode`（25 号字段）决定必填口径与录入面，公钥模式需 `alipay_public_key`，证书模式需三证书；证书内容刻意义不进 `PayConfigRow`（证书只经上传接口写入，保存请求不携带内容字段，避免未提交的证书被清空），只经 `UploadPayCert` 写入并以 `alipay_app_cert_uploaded`/`alipay_public_cert_uploaded`/`alipay_root_cert_uploaded`（28/29/30 号字段）回传状态。`UploadPayCert` 的 `config_id`（1 号字段）指定写入的配置行，对支付宝四类内容先校验可解析性再落库，校验失败以 `codes.FailedPrecondition` 原样透传原因（`codes.Internal` 网关不透传原始消息）。
- **支付宝渠道开关**：就绪判定为「当前加签方式的必填字段齐全且配置自检通过」（`PayConfigRow.ready` + `ready_message`，见 41/42 号字段），配置来源是数据库 `pay_config` 多行；启用（enabled）且就绪即可被渠道绑定引用并下单、回调与退款。支持手机网站支付（h5 渠道）与支付宝小程序 `alipay.trade.create`（alimp 渠道，2026-10-30 起），其余客户端渠道的能力矩阵在渠道绑定保存时即拒绝。
- **回调幂等**：置支付成功采用条件更新 `WHERE payment_no=? AND status=1`，重复回调与并发回调只生效一次；支付单/订单已处于终态时走终态兜底，不再重复推进订单状态。
- **支付确认与订单同步解耦（2026-10-17）**：`handlePaySuccess` 拆分为两阶段——阶段一仅在数据库事务内落支付单成功状态（不依赖 order-service 可用性，事务内不再有跨服务 RPC）；阶段二在事务提交后调用 order 服务 `UpdateOrderStatus` 同步订单状态，失败重试 3 次仍不成功时记录 `[CRITICAL]` 日志（支付已确认但订单状态未同步，需人工介入排查），不再因跨服务 RPC 失败回滚已确认的支付，避免回调超时导致渠道重复通知与支付状态反复。
- **ClosePayments**：订单取消（用户取消/后台取消）或调度器超时关单后，由 order 服务调用本 RPC，将该订单下全部 `status=1`（待支付）支付单条件更新为 `status=5 已关闭`；此后到达的迟到回调因状态不匹配被幂等拦截，防止"已关单却回调成功"造成资损。`ClosePaymentsReq` 按订单维度（`order_id`）批量关闭，无需逐单枚举支付单号。2026-10-21 起在本地关单前先逐单 `inspectForClose` 向渠道核实：渠道已收款 → 记挂账流水（`kind=3`/`result=9`）进异常队列且不改本地状态；`NOT_EXIST` → 无渠道交易可关，跳过；其余未收款 → 调渠道关单（支付宝 `/v3/alipay/trade/close`、微信 `Client.CloseOrder`），最后才做本地条件更新。现金/货到付款在 `queryChannelOrder` 返回「无渠道查询能力」，按查询失败跳过（不猜资金状态也不调渠道）。渠道语义不可颠倒：两侧都只允许关闭未支付交易，对已付款交易返回业务失败且不会自动退款。
- **支付单状态**：1待支付 2支付成功 3支付失败 4已退款 5已关闭（`5` 为本次新增，不复用 `3 支付失败`——后者语义为渠道侧确认失败）。

### 渠道为准的核实与人工处置语义（2026-10-21 新增）

四个主动入口共用唯一收敛点 `handlePaySuccess`，任何入口都不得自行写订单状态：

| RPC | 用途 | 来源标记 |
|---|---|---|
| `SyncPaymentFromChannel` | 向渠道核实并补同步（后台按钮、取消前置确认共用）；线下收款单（`pay_type` 3/4）不发起渠道调用，以本地已确认支付单补同步；操作人身份随请求带入，决定挂账待办的 `kind` 与 `detect_remark` 留痕（B3） | `confirm_source=3`，`confirm_ref` 由 `reason` 决定 |
| 对账调度器 | 定时回查待支付单（内部复用同一核实逻辑） | `confirm_source=2`，`confirm_ref=reconcile:支付单号` |
| `MarkPaymentPaid` | 渠道查不到但线下已核实到账的人工确认 | `confirm_source=3`，`confirm_ref=manual-mark` |
| 渠道回调 | 正常快速路径 | `confirm_source=1` |

- `SyncPaymentReq.reason` 的作用即区分「后台按钮捞回」与「取消流程拦下」，异常页与详情时间线据此展示；网关调用不传（按 `manual-sync`），order 服务固定传 `cancel-guard`。
- `SyncPaymentReq.operator_id`/`operator_name`（2026-10-24 B3）是**留痕身份**，与 `reason` 各管一件事：`reason` 说明场景，operator 说明是谁。身份判据刻意用「有没有操作人」而不是 `reason`——用场景反推身份会把取消前置这类系统调用记成人工。后台按钮由网关注入登录态（前端不传、也无法自称系统），order 侧取消前置与调度器留空即为系统口径。身份决定挂账待办的 `kind`（3 系统 / 8 人工核实）、`detect_remark` 内容，以及补同步时订单状态日志的 `operator_type`/`operator_id`（有 ID 落 `admin`，无 ID 保持 `system`，不猜身份）。
- `SyncPaymentResp` 的 `changed` 只在本次真正发生状态补同步时为 true；`paid=true && changed=false` 表示支付单早已确认（重复点击），`channel_state=ERROR` 表示渠道查询失败，**调用方不得据此判定资金状态**。
- 订单没有待同步的渠道支付单（从未发起支付，或只有现金/货到付款单）时返回 `NO_PAYMENT`，`paid=false`、零状态改动，因此该入口可安全重复点击，也不会凭空判定收款。
- `MarkPaymentPaid` 服务端校验链：入参非空 → 支付单存在（`codes.NotFound`）→ 非现金/货到付款（`FailedPrecondition`）→ 状态为待支付 → 填写金额与应付金额分位一致（不一致提示改用退款）。`remark` 由网关追加操作人后再落库。
- `ListPaymentAnomaliesReq.handled` 是**筛选选择器**（0全部 1仅待处理 2仅已处理），与行数据 `PaymentAnomalyInfo.handled` 的落库值（0待处理 1已处理）口径不同，转换在服务层完成；`community_id=0` 表示不限（超管），验签失败等解析不出支付单的流水 `community_id` 为 0，故只对超管可见。
- `with_raw` 由网关按权限点 `payment:raw:view` 决定（该权限点刻意不进社区管理员默认权限上限），服务层不判权限；`raw_body`/`headers` 仅失败与挂账场景入库，成功回调不落原文，`headers` 中签名与证书序列号入库前掩码。另注意支付宝的 `raw_body` 是**含 `sign` 原文**的通知参数（2026-10-23 放开掩码）：验签类故障只有拿到签名值才能离线复算待验签串，而买家账号等字段本就原文入库，凭据出口统一由该权限点守住（详见 07-基础设施/04-第三方支付对接.md「验签失败取证」）。
- 网关错误映射约定：payment 服务需让运营看到具体原因的业务拒绝一律用 `bizerr.New(codes.InvalidArgument/NotFound/FailedPrecondition, msg)`，`TranslateGRPCError` 才对映射码透传原始文案；裸 `fmt.Errorf` 会落到 Internal/Unknown，前端只显示「服务器内部错误」。

### 渠道绑定与下单解析（2026-10-30 重构，替代原「客户端渠道相关字段」）

- **绑定关系反转**：原 `PayConfig.wechat_client_channels`/`alipay_client_channels`（适用客户端渠道勾选，19/20 号字段）与 `enabled_channels`（1 号字段）已随单行配置一并删除。「哪个客户端渠道用哪些支付配置」改由 user 服务 `client_channels.pay_methods` 表达（有序数组：`cash` 或 `<platform>:<config_id>`，见 02-user.proto.md「客户端渠道管理」），渠道内置渠道集合为 h5/gzh/mp/alimp/android/ios/pos（删除 app）。
- **下单解析链路**：网关按 `X-Channel-Code`（未携带按 h5 兜底）取渠道绑定，解析出 `pay_type` 对应平台的 `config_id`，经 `ListActivePayMethods` 的启用集校验后随 `CreatePaymentReq` 传入：`config_id`（10 号字段，pay_type 1/2 必填、付款码占位单 5/6 建单时免填——平台未知，扫码后由 `PayByAuthCode` 回填）与 `client_channel`（11 号字段，支付宝按它分流 alimp→`alipay.trade.create`、其余→手机网站支付）。微信系下单还须带 `app_id`（6 号字段）：它取自渠道管理（不再来自支付配置），网关按渠道解析后传入，缺失即在建单前拒绝并提示「该渠道未配置微信 AppID」，不做任何默认值回落。
- **付款码扣款**：`PayByAuthCodeReq` 新增 `config_id`（5 号字段）与 `app_id`（6 号字段）：前者由网关按 pos 渠道绑定 + 付款码前缀识别平台后解析传入，服务端校验配置存在、启用、平台匹配；后者仅在微信付款码扣款时必填（`ReverseOrder` 撤销同样按它传参），支付宝留空、由 `config_id` 定位到配置行的签名身份。渠道按付款码前缀识别（微信 10-15→5，支付宝 25-30→6，占位单原地纠正），结果归一化 PAID/PAYING/FAILED，失败重试复用同一支付单（out_trade_no 不变），云闪付（62 开头）明确拒绝；退款/查单/关单/对账调度器/SyncPaymentFromChannel 全链路按 5→微信、6→支付宝映射（微信付款码关单走撤销 reverse）。
- **C 端渲染**：`GET /payments/channels` 按渠道绑定 ∩ `ListActivePayMethods` 过滤返回，顺序即绑定顺序；运行时就绪门槛（如微信仅 mp/h5 放行、支付宝仅 h5/alimp 放行）由网关把控。
- 历史（2026-10-30 定案）：`CreatePaymentReq.app_id`（6 号字段）仍为微信下单 AppID，但**归属从支付配置改为渠道**——微信的 AppID 是渠道身份（一个商户号可关联小程序/H5 等多个 AppID），留在配置行会强迫同一商户号按端建多份配置，故 `PayConfigRow` 的 10 号字段与 `ActivePayMethod` 的 3 号字段一并 `reserved` 删除，下单/撤销/回调归属校验用的 AppID 全部来自 `client_channels.app_id` 并随建单快照落 `payments.app_id`。调起参数由 wego `BuildJSAPIPayParams(appID, prepayID)` 生成（请求级 AppID 覆盖），market 不再自建签名实现。`community_id`（7 号字段）与 `order_no`（8 号字段）语义不变：payment 服务不查 orders 表，社区隔离与异常流水展示所依赖的订单事实只能在创建支付单时随订单带入并落库。

### 支付状态查询与收银台链接换取（2026-10-23 新增，免登录公开口径）

- `GetPaymentStatus`：C 端支付宝**回跳结果页**按支付单号轮询支付结果（网关公开路由 `GET /api/v1/payments/status/:payment_no`，无需登录）。免登录的安全依据是 `payment_no` 本身不可枚举（`ZF` + UUID），且响应只回状态级最小字段集（`payment_no/status/amount/order_id/order_no`），不含用户、商品、地址等隐私字段。**新增字段前须先确认不引入隐私信息**，否则该接口必须改为登录态。
- `GetAlipayPayUrl`：H5 支付引导页在系统浏览器打开后，凭 URL 上的支付单号实时换取**新签名**的支付宝手机网站支付链接（网关公开路由 `GET /api/v1/payments/alipay/url`）。存在的理由是 uni-app H5 路由 query 会破坏支付链接（实测 `?` 被改写为 `=`，编码也不生效），因此不在前端传递完整链接，只传不可枚举的支付单号。
- 两接口都只回「状态」而非「资金结论」：入账、对账、翻牌仍以带权限的订单/支付查询为权威。
