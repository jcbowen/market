package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"github.com/jcbowen/jcbaseGo/component/debugger"
	jcMiddleware "github.com/jcbowen/jcbaseGo/middleware"

	"github.com/jcbowen/market/src/backend/common/debuglog"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
)

// stubAlipayCallbackClient 支付宝回调 gRPC 桩：记录收到的通知参数并可控返回错误
// 嵌入接口后仅覆写 HandleAlipayCallback，避免实现全部方法
type stubAlipayCallbackClient struct {
	payment.PaymentServiceClient
	called bool
	params map[string]string
	err    error
}

// HandleAlipayCallback 记录入参并按预设返回
func (s *stubAlipayCallbackClient) HandleAlipayCallback(ctx context.Context, in *payment.AlipayCallbackReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.called = true
	s.params = in.GetParams()
	if s.err != nil {
		return nil, s.err
	}
	return &common.CommonResponse{}, nil
}

// doAlipayCallback 以指定 Content-Type 与请求体投递一次支付宝回调
// 参数：
//   - t: 测试对象
//   - stub: 回调 gRPC 桩
//   - contentType: 请求 Content-Type，传空串表示不带头
//   - body: 原始请求体
//
// 返回值：
//   - string: 网关返回给支付宝的响应体（success/failure）
func doAlipayCallback(t *testing.T, stub *stubAlipayCallbackClient, contentType, body string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/payment/callback/alipay", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req

	h := &PaymentHandler{paymentClient: stub}
	h.AlipayCallback(c)
	return recorder.Body.String()
}

// doAlipayCallbackViaEngine 经完整 gin 引擎（含 SetGPC 全局中间件）投递一次支付宝回调
// 复刻 main.go 的真实中间件链：SetGPC 对 application/x-www-form-urlencoded 的 POST
// 会调用 ParseForm 消费 body，用于验证回调 handler 在该链路下仍能取到通知参数
// 参数：
//   - t: 测试对象
//   - stub: 回调 gRPC 桩
//   - contentType: 请求 Content-Type，传空串表示不带头
//   - body: 原始请求体
//
// 返回值：
//   - string: 网关返回给支付宝的响应体（success/failure）
func doAlipayCallbackViaEngine(t *testing.T, stub *stubAlipayCallbackClient, contentType, body string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	baseMw := jcMiddleware.Base{}
	r.Use(baseMw.SetGPC(r))
	r.POST("/api/v1/payments/callback/alipay", (&PaymentHandler{paymentClient: stub}).AlipayCallback)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/callback/alipay", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, req)
	return recorder.Body.String()
}

// TestAlipayCallbackParsesRawBody 回调解析不依赖 Content-Type
// 覆盖：标准 urlencoded、Content-Type 缺失、非 urlencoded 投递、URL 解码、服务失败回 failure、
// 超限请求体与非法编码不投递服务
func TestAlipayCallbackParsesRawBody(t *testing.T) {
	t.Run("标准 urlencoded", func(t *testing.T) {
		stub := &stubAlipayCallbackClient{}
		body := "app_id=2021000000000000&out_trade_no=PAY001&trade_no=2026010122001&trade_status=TRADE_SUCCESS&total_amount=12.34&sign=abc&sign_type=RSA2"
		if got := doAlipayCallback(t, stub, "application/x-www-form-urlencoded", body); got != "success" {
			t.Fatalf("响应 = %q, 期望 success", got)
		}
		if !stub.called || stub.params["out_trade_no"] != "PAY001" || stub.params["sign"] != "abc" {
			t.Fatalf("通知参数传递错误: %+v", stub.params)
		}
	})

	t.Run("Content-Type 缺失仍解析", func(t *testing.T) {
		stub := &stubAlipayCallbackClient{}
		if got := doAlipayCallback(t, stub, "", "out_trade_no=PAY002&total_amount=1.00"); got != "success" {
			t.Fatalf("响应 = %q, 期望 success", got)
		}
		if stub.params["out_trade_no"] != "PAY002" {
			t.Fatalf("无 Content-Type 时参数丢失: %+v", stub.params)
		}
	})

	t.Run("非 urlencoded 投递仍解析", func(t *testing.T) {
		stub := &stubAlipayCallbackClient{}
		if got := doAlipayCallback(t, stub, "text/plain", "out_trade_no=PAY003&total_amount=2.00"); got != "success" {
			t.Fatalf("响应 = %q, 期望 success", got)
		}
		if stub.params["out_trade_no"] != "PAY003" {
			t.Fatalf("text/plain 投递时参数丢失: %+v", stub.params)
		}
	})

	t.Run("百分号编码还原", func(t *testing.T) {
		stub := &stubAlipayCallbackClient{}
		if got := doAlipayCallback(t, stub, "application/x-www-form-urlencoded", "subject=%E7%A4%BE%E5%8C%BA%E5%95%86%E8%B6%85%E8%AE%A2%E5%8D%95"); got != "success" {
			t.Fatalf("响应 = %q, 期望 success", got)
		}
		if stub.params["subject"] != "社区商超订单" {
			t.Fatalf("URL 解码失败: %q", stub.params["subject"])
		}
	})

	t.Run("服务处理失败回 failure", func(t *testing.T) {
		stub := &stubAlipayCallbackClient{err: errors.New("验签失败")}
		if got := doAlipayCallback(t, stub, "application/x-www-form-urlencoded", "out_trade_no=PAY004"); got != "failure" {
			t.Fatalf("响应 = %q, 期望 failure", got)
		}
	})

	t.Run("超限请求体不投递", func(t *testing.T) {
		stub := &stubAlipayCallbackClient{}
		body := "out_trade_no=" + strings.Repeat("x", alipayNotifyBodyLimit)
		if got := doAlipayCallback(t, stub, "application/x-www-form-urlencoded", body); got != "failure" {
			t.Fatalf("响应 = %q, 期望 failure", got)
		}
		if stub.called {
			t.Fatal("超限请求体不应投递给支付服务")
		}
	})

	t.Run("非法编码回 failure", func(t *testing.T) {
		stub := &stubAlipayCallbackClient{}
		if got := doAlipayCallback(t, stub, "application/x-www-form-urlencoded", "total_amount=%zz"); got != "failure" {
			t.Fatalf("响应 = %q, 期望 failure", got)
		}
		if stub.called {
			t.Fatal("非法编码请求不应投递给支付服务")
		}
	})
}

// TestAlipayCallbackBehindSetGPCMiddleware 回调经真实中间件链（SetGPC）时的参数解析
// 回归背景：SetGPC 对 application/x-www-form-urlencoded 的 POST 调用 ParseForm 消费 body
// 且不恢复，handler 再读 raw body 得空，表现为线上「回调参数为空」→ 支付宝无限重发、
// 用户已付款订单始终未支付（2026-10-22 线上故障）。此处锁定两种投递形态下参数均完整：
//   - 标准 form-urlencoded：body 被中间件消费，须回退 PostForm 取参；
//   - Content-Type 缺失：中间件不解析，raw body 路径取参。
func TestAlipayCallbackBehindSetGPCMiddleware(t *testing.T) {
	const notifyBody = "app_id=2021000000000000&out_trade_no=PAY100&trade_no=2026010122001&trade_status=TRADE_SUCCESS&total_amount=12.34&sign=abc&sign_type=RSA2"

	t.Run("标准 form-urlencoded 经中间件链", func(t *testing.T) {
		stub := &stubAlipayCallbackClient{}
		if got := doAlipayCallbackViaEngine(t, stub, "application/x-www-form-urlencoded", notifyBody); got != "success" {
			t.Fatalf("响应 = %q, 期望 success（SetGPC 消费 body 后应回退 PostForm 取参）", got)
		}
		if !stub.called {
			t.Fatal("通知未投递给支付服务")
		}
		if stub.params["out_trade_no"] != "PAY100" || stub.params["trade_no"] != "2026010122001" ||
			stub.params["total_amount"] != "12.34" || stub.params["sign"] != "abc" {
			t.Fatalf("经中间件链后通知参数不完整: %+v", stub.params)
		}
	})

	t.Run("Content-Type 缺失经中间件链", func(t *testing.T) {
		stub := &stubAlipayCallbackClient{}
		if got := doAlipayCallbackViaEngine(t, stub, "", "out_trade_no=PAY101&total_amount=1.00"); got != "success" {
			t.Fatalf("响应 = %q, 期望 success", got)
		}
		if stub.params["out_trade_no"] != "PAY101" {
			t.Fatalf("无 Content-Type 经中间件链时参数丢失: %+v", stub.params)
		}
	})
}

// newCallbackTestDebugger 内存存储的调试器实例（不连 MySQL）
// Level 必须显式 Info：组件 Config.Level 默认 warn 会把 Info 级摘要全部丢弃
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *debugger.Debugger: 可直接登记给 debuglog 的实例
func newCallbackTestDebugger(t *testing.T) *debugger.Debugger {
	t.Helper()
	storage, err := debugger.NewMemoryStorage(200)
	if err != nil {
		t.Fatalf("创建内存存储失败: %v", err)
	}
	dbg, err := debugger.New(&debugger.Config{
		Enabled:     true,
		ServiceName: "gateway-test",
		MaxRecords:  200,
		Level:       debugger.LevelInfo,
		Storage:     storage,
	})
	if err != nil {
		t.Fatalf("创建调试器实例失败: %v", err)
	}
	return dbg
}

// TestAlipayCallbackRecordsProcessLog 支付宝回调受理情况记入 debugger 进程级日志
// 覆盖（排查口径，详见需求文档 07-基础设施/03 与 07）：
//   - 成功回调落 completed 记录，摘要只含 out_trade_no 等白名单字段；
//   - 失败回调落 failed 记录并带全量通知原文（含 sign、alipay_cert_sn）与报文元信息，
//     这是验签类故障唯一可离线复算的取证入口；
//   - 受理失败带 error 字段（界面错误徽标可筛选）；
//   - 未登记实例（debugger 未启用/降级）时不 panic、无记录。
//
// 封顶行为见 common/debuglog 包测试（滑窗包级共享，不在此重复）
func TestAlipayCallbackRecordsProcessLog(t *testing.T) {

	t.Run("成功回调落 completed 且不含 sign", func(t *testing.T) {
		dbg := newCallbackTestDebugger(t)
		debuglog.SetInstance(dbg)
		t.Cleanup(func() { debuglog.SetInstance(nil) })

		stub := &stubAlipayCallbackClient{}
		body := "app_id=2021000000000000&out_trade_no=PAY900&trade_status=TRADE_SUCCESS&total_amount=9.90&notify_id=NID900&sign=SHOULD_NOT_RECORD&buyer_logon_id=138****1234"
		if got := doAlipayCallback(t, stub, "application/x-www-form-urlencoded", body); got != "success" {
			t.Fatalf("响应 = %q, 期望 success", got)
		}

		entries, total, err := dbg.GetStorage().FindAll(1, 50, map[string]interface{}{"record_type": "process"})
		if err != nil || total != 1 {
			t.Fatalf("应落 1 条进程记录，实际 total=%d err=%v", total, err)
		}
		entry := entries[0]
		if entry.ProcessName != "支付宝回调" || entry.ProcessType != "payment_callback_alipay" {
			t.Errorf("进程标识错误: %+v", entry)
		}
		if entry.Status != "completed" {
			t.Errorf("状态应为 completed，实际 %q", entry.Status)
		}
		if len(entry.LoggerLogs) != 1 || entry.LoggerLogs[0].Message != "支付宝回调受理完成" {
			t.Fatalf("日志内容错误: %+v", entry.LoggerLogs)
		}
		fields := entry.LoggerLogs[0].Fields
		if fields["out_trade_no"] != "PAY900" || fields["trade_status"] != "TRADE_SUCCESS" ||
			fields["total_amount"] != "9.90" || fields["result"] != "success" {
			t.Errorf("摘要字段缺失: %+v", fields)
		}
		// 凭据与买家账号不入库：sign/buyer_logon_id/app_id 均不在白名单
		for _, key := range []string{"sign", "buyer_logon_id", "app_id", "sign_type"} {
			if _, ok := fields[key]; ok {
				t.Errorf("非白名单字段 %q 不应入调试库: %+v", key, fields)
			}
		}
	})

	t.Run("受理失败落 failed 且带全量原文与 sign", func(t *testing.T) {
		dbg := newCallbackTestDebugger(t)
		debuglog.SetInstance(dbg)
		t.Cleanup(func() { debuglog.SetInstance(nil) })

		stub := &stubAlipayCallbackClient{err: errors.New("验签失败")}
		body := "out_trade_no=PAY901&total_amount=1.00&sign=x&alipay_cert_sn=abc123&buyer_logon_id=138****1234"
		if got := doAlipayCallback(t, stub, "application/x-www-form-urlencoded", body); got != "failure" {
			t.Fatalf("响应 = %q, 期望 failure", got)
		}

		entries, total, err := dbg.GetStorage().FindAll(1, 50, map[string]interface{}{"record_type": "process"})
		if err != nil || total != 1 {
			t.Fatalf("应落 1 条进程记录，实际 total=%d err=%v", total, err)
		}
		entry := entries[0]
		if entry.Status != "failed" || !entry.HasError {
			t.Errorf("失败记录应为 failed 且 HasError，实际 status=%q hasErr=%v", entry.Status, entry.HasError)
		}
		fields := entry.LoggerLogs[0].Fields
		if fields["error"] != "验签失败" || fields["out_trade_no"] != "PAY901" {
			t.Errorf("失败摘要字段错误: %+v", fields)
		}
		// 取证口径：失败必须能离线复算待验签串，故 sign 与证书序列号原样入库
		if fields["sign"] != "x" || fields["alipay_cert_sn"] != "abc123" {
			t.Errorf("失败记录应保留 sign 与 alipay_cert_sn 原文: %+v", fields)
		}
		if fields["content_type"] != "application/x-www-form-urlencoded" || fields["param_count"] != 5 {
			t.Errorf("报文元信息缺失: %+v", fields)
		}
		if bytes, _ := fields["body_bytes"].(int); bytes != len(body) {
			t.Errorf("body_bytes 应为 %d，实际 %+v", len(body), fields["body_bytes"])
		}
	})

	t.Run("请求体超限只落元信息并带 parse_error", func(t *testing.T) {
		dbg := newCallbackTestDebugger(t)
		debuglog.SetInstance(dbg)
		t.Cleanup(func() { debuglog.SetInstance(nil) })

		stub := &stubAlipayCallbackClient{}
		// 超限判定在读体阶段触发，此时没有任何已解析参数可记
		body := "out_trade_no=PAY903&sign=" + strings.Repeat("A", alipayNotifyBodyLimit)
		if got := doAlipayCallback(t, stub, "application/x-www-form-urlencoded", body); got != "failure" {
			t.Fatalf("响应 = %q, 期望 failure", got)
		}

		entries, total, err := dbg.GetStorage().FindAll(1, 50, map[string]interface{}{"record_type": "process"})
		if err != nil || total != 1 {
			t.Fatalf("应落 1 条进程记录，实际 total=%d err=%v", total, err)
		}
		fields := entries[0].LoggerLogs[0].Fields
		if fields["parse_error"] == nil || fields["param_count"] != 0 {
			t.Errorf("超限场景应带 parse_error 且无已解析参数: %+v", fields)
		}
		if fields["body_bytes"] != alipayNotifyBodyLimit+1 {
			t.Errorf("body_bytes 应为读到的 %d 字节，实际 %+v", alipayNotifyBodyLimit+1, fields["body_bytes"])
		}
	})

	t.Run("未登记实例时不 panic 无记录", func(t *testing.T) {
		debuglog.SetInstance(nil)
		t.Cleanup(func() { debuglog.SetInstance(nil) })

		stub := &stubAlipayCallbackClient{}
		if got := doAlipayCallback(t, stub, "application/x-www-form-urlencoded", "out_trade_no=PAY902"); got != "success" {
			t.Fatalf("响应 = %q, 期望 success（记录缺失不影响受理）", got)
		}
	})
	// 注：同 IP 每分钟封顶行为由 common/debuglog 包的 TestQuotaPerMinuteCap 覆盖
	// （封顶滑窗为包级共享，handler 层多用例会相互消耗配额，故不在此重复验证）
}

// stubWechatCallbackClient 微信回调 gRPC 桩：记录收到的密文正文并可控返回错误
// 嵌入接口后仅覆写 HandleWechatCallback，避免实现全部方法
type stubWechatCallbackClient struct {
	payment.PaymentServiceClient
	called bool
	body   []byte
	err    error
}

// HandleWechatCallback 记录入参并按预设返回
func (s *stubWechatCallbackClient) HandleWechatCallback(ctx context.Context, in *payment.WechatCallbackReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.called = true
	s.body = in.GetBody()
	if s.err != nil {
		return nil, s.err
	}
	return &common.CommonResponse{}, nil
}

// doWechatCallback 经完整 gin 引擎投递一次微信回调
// 不能直接调 handler：gin responseWriter 的 WriteHeader 延迟到首次 Write 才落状态码，
// 成功应答（204 空体）绕过 engine 收尾的 WriteHeaderNow 会退化为默认 200
// 参数：
//   - t: 测试对象
//   - stub: 回调 gRPC 桩
//   - body: 原始请求体（V3 为渠道密文）
//
// 返回值：
//   - int: HTTP 状态码（V3 规范：成功 204、失败 5xx）
//   - string: 响应体（成功为空体，失败为 {"code":"FAIL",...}）
func doWechatCallback(t *testing.T, stub *stubWechatCallbackClient, body string) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/payments/callback/wechat", (&PaymentHandler{paymentClient: stub}).WechatCallback)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/callback/wechat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, req)
	return recorder.Code, recorder.Body.String()
}

// TestWechatCallbackRecordsProcessLog 微信回调受理情况记入 debugger 进程级日志
// 微信 V3 通知体是渠道密文，记录侧只允许元数据（体量/结果/耗时/IP），
// 密文正文即便被解析无价值也不得进调试库；本机无商户配置、真实渠道不可用，
// 故这条链路只能靠此处静态断言锁定（详见需求文档 07-基础设施/04 的排查路径）
func TestWechatCallbackRecordsProcessLog(t *testing.T) {
	const ciphertext = "BASE64_LIKE_RESOURCE_CIPHERTEXT_SHOULD_NOT_RECORD"

	t.Run("受理成功只落元数据、不含密文", func(t *testing.T) {
		dbg := newCallbackTestDebugger(t)
		debuglog.SetInstance(dbg)
		t.Cleanup(func() { debuglog.SetInstance(nil) })

		// V3 规范成功应答：HTTP 204 空体（wego NotificationSuccess），不回 V2 XML 的 SUCCESS
		if code, got := doWechatCallback(t, &stubWechatCallbackClient{}, ciphertext); code != http.StatusNoContent || got != "" {
			t.Fatalf("响应 = (%d, %q), 期望 204 空体", code, got)
		}

		entries, total, err := dbg.GetStorage().FindAll(1, 50, map[string]interface{}{"record_type": "process"})
		if err != nil || total != 1 {
			t.Fatalf("应落 1 条进程记录，实际 total=%d err=%v", total, err)
		}
		entry := entries[0]
		if entry.ProcessName != "微信回调" || entry.ProcessType != "payment_callback_wechat" {
			t.Errorf("进程标识错误: name=%q type=%q", entry.ProcessName, entry.ProcessType)
		}
		if entry.Status != "completed" || entry.HasError {
			t.Errorf("受理成功应为 completed 且无错误，实际 status=%q hasErr=%v", entry.Status, entry.HasError)
		}
		fields := entry.LoggerLogs[0].Fields
		if fields["result"] != "success" || fields["body_size"] != len(ciphertext) {
			t.Errorf("元数据字段错误: %+v", fields)
		}
		// 只允许白名单元数据：新增字段须显式评审（密文/凭据一律不得入库）。
		// 组件会给每条日志自动附带 level/message/timestamp 与进程三件套，属固定字段
		componentFields := map[string]bool{
			"level": true, "message": true, "timestamp": true,
			"process_id": true, "process_name": true, "process_type": true,
		}
		for key := range fields {
			switch key {
			case "body_size", "result", "duration_ms", "client_ip":
			default:
				if !componentFields[key] {
					t.Errorf("非白名单字段 %q 不应出现在微信回调记录里: %+v", key, fields)
				}
			}
		}
		for _, e := range entry.LoggerLogs {
			for _, blob := range []string{fmt.Sprint(e.Fields), e.Message} {
				if strings.Contains(blob, ciphertext) {
					t.Errorf("密文正文泄入调试记录: %s", blob)
				}
			}
		}
	})

	t.Run("受理失败落 failed 并带 error", func(t *testing.T) {
		dbg := newCallbackTestDebugger(t)
		debuglog.SetInstance(dbg)
		t.Cleanup(func() { debuglog.SetInstance(nil) })

		stub := &stubWechatCallbackClient{err: errors.New("微信回调解密失败")}
		// V3 规范失败应答：5xx + {"code":"FAIL","message":"..."}，微信按间隔重试
		if code, got := doWechatCallback(t, stub, ciphertext); code != http.StatusInternalServerError || !strings.Contains(got, "FAIL") {
			t.Fatalf("响应 = (%d, %q), 期望 5xx + FAIL", code, got)
		}

		entries, total, err := dbg.GetStorage().FindAll(1, 50, map[string]interface{}{"record_type": "process"})
		if err != nil || total != 1 {
			t.Fatalf("应落 1 条进程记录，实际 total=%d err=%v", total, err)
		}
		entry := entries[0]
		if entry.Status != "failed" || !entry.HasError {
			t.Errorf("失败记录应为 failed 且 HasError，实际 status=%q hasErr=%v", entry.Status, entry.HasError)
		}
		if entry.LoggerLogs[0].Fields["error"] != "微信回调解密失败" {
			t.Errorf("失败原因未记录: %+v", entry.LoggerLogs[0].Fields)
		}
	})

	t.Run("未登记实例时不 panic 无记录", func(t *testing.T) {
		debuglog.SetInstance(nil)
		t.Cleanup(func() { debuglog.SetInstance(nil) })

		// 记录缺失不影响受理，仍按 V3 规范回 204 空体
		if code, got := doWechatCallback(t, &stubWechatCallbackClient{}, ciphertext); code != http.StatusNoContent || got != "" {
			t.Fatalf("响应 = (%d, %q), 期望 204 空体（记录缺失不影响受理）", code, got)
		}
	})
}
