package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	jcMiddleware "github.com/jcbowen/jcbaseGo/middleware"

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
