package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
)

/**
 * 收银台（POS）付款码链路测试（P1-7 + P1-8）
 *
 * 背景：
 *   P1-7 轮询「是否已收款」此前只认 status=2，付款成功后被发货/核销/完成推离 2 的订单
 *        （以及退款态）会被误报成未收款，诱导收银台重复扣款；
 *   P1-8 付款码渠道就绪校验此前发生在建单之后，渠道不可用时订单已落库，
 *        留下一笔无法收款的孤儿待支付订单（顾客可能已离店）。
 *
 * 运行方式（无外部依赖）：
 *   go test ./gateway/handler/ -run 'TestPos|TestIsPosPaidStatus|TestGetPosOrderPayStatus' -v
 *
 * 覆盖：
 *   - 渠道未就绪：先于建单拒绝，不产生订单/支付单；
 *   - 渠道就绪：以收银台来源建单并创建付款码待支付单（paid=false，两步化第一步）；
 *   - 0 元单误选付款码：结算金额只能下单后确定，订单已建 → best-effort 自动取消；
 *   - 已收款状态族 {2,3,4,5,7,8} 在轮询接口统一回 paid=true，已取消(6)回 false。
 */

// stubPosOrderClient 收银台链路订单服务桩：仅覆写用到的四个方法
// 嵌入接口后其余方法无需实现（误调用会 panic，直接暴露测试缺陷）
type stubPosOrderClient struct {
	order.OrderServiceClient
	shift        *order.PosShiftInfo
	orderInfo    *order.OrderInfo
	createCalled bool
	createReq    *order.CreateOrderReq
	updateCalled bool
	updateReq    *order.UpdateOrderStatusReq
}

func (s *stubPosOrderClient) GetCurrentShift(ctx context.Context, in *order.GetCurrentShiftReq, opts ...grpc.CallOption) (*order.PosShiftInfo, error) {
	return s.shift, nil
}

func (s *stubPosOrderClient) CreateOrder(ctx context.Context, in *order.CreateOrderReq, opts ...grpc.CallOption) (*order.OrderInfo, error) {
	s.createCalled = true
	s.createReq = in
	return s.orderInfo, nil
}

func (s *stubPosOrderClient) UpdateOrderStatus(ctx context.Context, in *order.UpdateOrderStatusReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.updateCalled = true
	s.updateReq = in
	return &common.CommonResponse{}, nil
}

func (s *stubPosOrderClient) GetOrder(ctx context.Context, in *order.GetOrderReq, opts ...grpc.CallOption) (*order.OrderDetail, error) {
	return &order.OrderDetail{Order: s.orderInfo}, nil
}

// stubPosPaymentClient 收银台链路支付服务桩
type stubPosPaymentClient struct {
	payment.PaymentServiceClient
	cfg          *payment.PayConfig
	createCalled bool
	createReq    *payment.CreatePaymentReq
}

func (s *stubPosPaymentClient) GetPayConfig(ctx context.Context, in *payment.GetPayConfigReq, opts ...grpc.CallOption) (*payment.PayConfig, error) {
	return s.cfg, nil
}

func (s *stubPosPaymentClient) CreatePayment(ctx context.Context, in *payment.CreatePaymentReq, opts ...grpc.CallOption) (*payment.CreatePaymentResp, error) {
	s.createCalled = true
	s.createReq = in
	return &payment.CreatePaymentResp{PaymentNo: "ZFPOSTEST"}, nil
}

func (s *stubPosPaymentClient) SyncPaymentFromChannel(ctx context.Context, in *payment.SyncPaymentReq, opts ...grpc.CallOption) (*payment.SyncPaymentResp, error) {
	return &payment.SyncPaymentResp{Paid: false, Changed: false, ChannelState: "NOTPAY"}, nil
}

// newPosTestContext 构造带超管语境的 gin 测试上下文（社区 1）
//
// 参数：
//   - method: 请求方法
//   - path: 请求路径
//   - body: 请求体（GET 传 nil）
//
// 返回值：
//   - *gin.Context: 处理器内可用的上下文
//   - *httptest.ResponseRecorder: 响应记录器
func newPosTestContext(method, path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set(string(appcontext.KeyUserID), int64(7))
	c.Set(string(appcontext.KeyRole), "super_admin")
	c.Set(string(appcontext.KeyCommunityID), int64(1))
	c.Set(string(appcontext.KeyCommunityIDs), []int64{1})
	c.Set("admin_username", "postest")
	return c, recorder
}

// newPosTestHandler 组装只带收银台链路依赖的处理器
// jwt/product/user 客户端为 nil：CreatePosOrder/GetPosOrderPayStatus 不触达
func newPosTestHandler(orderStub *stubPosOrderClient, payStub *stubPosPaymentClient) *AdminHandler {
	return &AdminHandler{orderClient: orderStub, paymentClient: payStub}
}

// TestPosCodePayChecksChannelBeforeCreateOrder 付款码渠道就绪校验必须先于建单（P1-8）
func TestPosCodePayChecksChannelBeforeCreateOrder(t *testing.T) {
	notReady := &payment.PayConfig{EnabledChannels: []string{"cash"}, WechatPaymentReady: false}
	ready := &payment.PayConfig{EnabledChannels: []string{"wechat"}, WechatPaymentReady: true}
	body := []byte(`{"user_id":0,"pay_type":5,"delivery_type":3,"items":[{"sku_id":1,"quantity":1}]}`)

	t.Run("渠道未就绪时不建单", func(t *testing.T) {
		orderStub := &stubPosOrderClient{shift: &order.PosShiftInfo{Id: 1}}
		payStub := &stubPosPaymentClient{cfg: notReady}
		h := newPosTestHandler(orderStub, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders", body)
		h.CreatePosOrder(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.OperationNotAllowed {
			t.Fatalf("渠道未就绪应拒单，code = %d(%s)", resp.Code, resp.Message)
		}
		if orderStub.createCalled {
			t.Fatal("渠道校验必须先于建单：未就绪时不得创建订单（否则留下无法收款的孤儿待支付单）")
		}
		if payStub.createCalled {
			t.Fatal("渠道未就绪时不得创建支付单")
		}
	})

	t.Run("渠道就绪时建待支付单", func(t *testing.T) {
		orderStub := &stubPosOrderClient{
			shift:     &order.PosShiftInfo{Id: 1},
			orderInfo: &order.OrderInfo{Id: 100, OrderNo: "POSTEST-1", PayAmount: 10, PickupCode: "1234"},
		}
		payStub := &stubPosPaymentClient{cfg: ready}
		h := newPosTestHandler(orderStub, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders", body)
		h.CreatePosOrder(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.Success {
			t.Fatalf("渠道就绪应正常建单，code = %d(%s)", resp.Code, resp.Message)
		}
		if !orderStub.createCalled || orderStub.createReq.GetSource() != 2 {
			t.Fatalf("应以收银台来源(source=2)建单: %+v", orderStub.createReq)
		}
		if !payStub.createCalled || payStub.createReq.GetPayType() != posPayTypeWechatCode {
			t.Fatalf("应创建付款码待支付单: %+v", payStub.createReq)
		}
		// 两步化第一步：仅建待支付单，扫码扣款后才确认收款
		if paid, _ := resp.Data["paid"].(bool); paid {
			t.Error("付款码下单应为待支付（paid=false）")
		}
	})

	t.Run("0 元订单误选付款码时 best-effort 取消", func(t *testing.T) {
		orderStub := &stubPosOrderClient{
			shift:     &order.PosShiftInfo{Id: 1},
			orderInfo: &order.OrderInfo{Id: 101, OrderNo: "POSTEST-0", PayAmount: 0},
		}
		payStub := &stubPosPaymentClient{cfg: ready}
		h := newPosTestHandler(orderStub, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders", body)
		h.CreatePosOrder(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.OperationNotAllowed {
			t.Fatalf("0 元付款码应拒单，code = %d(%s)", resp.Code, resp.Message)
		}
		// 结算金额（券/改价）只能下单后确定：订单已建，必须 best-effort 取消，不留孤儿单
		if !orderStub.updateCalled || orderStub.updateReq.GetStatus() != 6 {
			t.Fatalf("0 元付款码单应自动取消(status=6): %+v", orderStub.updateReq)
		}
		if payStub.createCalled {
			t.Fatal("0 元单不得创建支付单")
		}
	})
}

// TestIsPosPaidStatus 已收款状态族判定（P1-7）
func TestIsPosPaidStatus(t *testing.T) {
	// 2已支付 3备货中 4已发货 5已完成 7退款中 8已退款：后四态均由已收款订单流转而来，
	// 退款态同样以收款为前提
	for _, s := range []int32{2, 3, 4, 5, 7, 8} {
		if !isPosPaidStatus(s) {
			t.Errorf("状态 %d 属于已收款族，应判定为已收款", s)
		}
	}
	// 0未定义 1待支付 6已取消 9非法 100/101 处理中（取消/补同步抢占态）
	for _, s := range []int32{0, 1, 6, 9, 100, 101} {
		if isPosPaidStatus(s) {
			t.Errorf("状态 %d 不属于已收款族，不应判定为已收款", s)
		}
	}
}

// TestGetPosOrderPayStatusPaidFamily 轮询接口对已收款状态族统一回 paid=true（P1-7）
// 修复前只认 status=2：已发货/已完成/退款中的订单会被误报未收款，诱导重复扣款
func TestGetPosOrderPayStatusPaidFamily(t *testing.T) {
	cases := []struct {
		name   string
		status int32
		want   bool
	}{
		{"已支付", 2, true},
		{"备货中", 3, true},
		{"已发货", 4, true},
		{"已完成", 5, true},
		{"退款中", 7, true},
		{"已退款", 8, true},
		{"已取消", 6, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orderStub := &stubPosOrderClient{orderInfo: &order.OrderInfo{Id: 200, CommunityId: 1, Status: tc.status}}
			h := newPosTestHandler(orderStub, &stubPosPaymentClient{})

			c, recorder := newPosTestContext(http.MethodGet, "/api/v1/admin/pos/orders/200/pay-code", nil)
			c.Params = gin.Params{{Key: "id", Value: "200"}}
			h.GetPosOrderPayStatus(c)

			resp := decodeRefreshResp(t, recorder)
			if resp.Code != errcode.Success {
				t.Fatalf("查询应成功，code = %d(%s)", resp.Code, resp.Message)
			}
			if got, _ := resp.Data["paid"].(bool); got != tc.want {
				t.Fatalf("状态 %d 的 paid = %v, 期望 %v", tc.status, got, tc.want)
			}
			// 订单已离开待支付族：只回报事实，不再改动本地状态
			if changed, _ := resp.Data["changed"].(bool); changed {
				t.Error("订单已离开待支付族时不应改动本地状态（changed=false）")
			}
			if got, _ := resp.Data["order_status"].(float64); int32(got) != tc.status {
				t.Errorf("order_status = %v, 期望 %d", resp.Data["order_status"], tc.status)
			}
		})
	}
}
