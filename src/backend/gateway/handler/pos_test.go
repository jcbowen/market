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
	"github.com/jcbowen/market/src/backend/common/proto/user"
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

// stubPosUserClient 收银台链路用户服务桩：提供 POS 渠道绑定信息
type stubPosUserClient struct {
	user.UserServiceClient
	posChannel *user.ClientChannelInfo
}

func (s *stubPosUserClient) GetClientChannel(ctx context.Context, in *user.GetClientChannelReq, opts ...grpc.CallOption) (*user.ClientChannelInfo, error) {
	return s.posChannel, nil
}

// posChannelOf 构造 POS 渠道信息（绑定 pay_methods）
func posChannelOf(methods ...string) *user.ClientChannelInfo {
	return &user.ClientChannelInfo{Code: "pos", Name: "门店收银台", Enabled: true, Configured: true, PayMethods: methods}
}

// stubPosPaymentClient 收银台链路支付服务桩（多行制：就绪配置以 ListActivePayMethods 提供）
type stubPosPaymentClient struct {
	payment.PaymentServiceClient
	active       []*payment.ActivePayMethod
	createCalled bool
	createReq    *payment.CreatePaymentReq
	closeCalled  bool
	closeReq     *payment.ClosePaymentsReq
	closeErr     error
}

func (s *stubPosPaymentClient) ListActivePayMethods(ctx context.Context, in *payment.ListActivePayMethodsReq, opts ...grpc.CallOption) (*payment.ListActivePayMethodsResp, error) {
	return &payment.ListActivePayMethodsResp{Methods: s.active}, nil
}

func (s *stubPosPaymentClient) CreatePayment(ctx context.Context, in *payment.CreatePaymentReq, opts ...grpc.CallOption) (*payment.CreatePaymentResp, error) {
	s.createCalled = true
	s.createReq = in
	return &payment.CreatePaymentResp{PaymentNo: "ZFPOSTEST"}, nil
}

func (s *stubPosPaymentClient) ClosePayments(ctx context.Context, in *payment.ClosePaymentsReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.closeCalled = true
	s.closeReq = in
	if s.closeErr != nil {
		return nil, s.closeErr
	}
	return &common.CommonResponse{}, nil
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

// newPosTestHandlerWithUser 组装带用户服务桩（POS 渠道绑定）的处理器
func newPosTestHandlerWithUser(orderStub *stubPosOrderClient, userStub *stubPosUserClient, payStub *stubPosPaymentClient) *AdminHandler {
	return &AdminHandler{orderClient: orderStub, userClient: userStub, paymentClient: payStub}
}

// TestPosCodePayChecksChannelBeforeCreateOrder 付款码渠道就绪校验必须先于建单（P1-8）
func TestPosCodePayChecksChannelBeforeCreateOrder(t *testing.T) {
	body := []byte(`{"user_id":0,"pay_type":5,"delivery_type":3,"items":[{"sku_id":1,"quantity":1}]}`)
	// 未就绪：POS 仅绑现金；就绪：绑定 wechat:1 且该配置启用就绪
	notReadyUser := &stubPosUserClient{posChannel: posChannelOf("cash")}
	readyUser := &stubPosUserClient{posChannel: posChannelOf("wechat:1", "cash")}
	readyPay := &stubPosPaymentClient{active: []*payment.ActivePayMethod{{ConfigId: 1, Platform: "wechat", AppId: "wx1"}}}

	t.Run("渠道未就绪时不建单", func(t *testing.T) {
		orderStub := &stubPosOrderClient{shift: &order.PosShiftInfo{Id: 1}}
		payStub := &stubPosPaymentClient{}
		h := newPosTestHandlerWithUser(orderStub, notReadyUser, payStub)

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
		payStub := readyPay
		h := newPosTestHandlerWithUser(orderStub, readyUser, payStub)

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
		// 占位单发生在扫码前、平台未知：config_id 留空，扫码后由 PayByAuthCode 按前缀回填
		if payStub.createReq.GetConfigId() != 0 {
			t.Errorf("付款码占位单不应携带 config_id，got %d", payStub.createReq.GetConfigId())
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
		// 就绪桩须新建：同一实例跨子测试复用会残留 createCalled 状态
		payStub := &stubPosPaymentClient{active: readyPay.active}
		h := newPosTestHandlerWithUser(orderStub, readyUser, payStub)

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

// TestPayPosOrderByCash 待支付订单改用现金收款（扣款失败换现金场景）
// 编排口径：先关遗留待支付支付单（防旧付款码事后仍可扣款），再创建现金支付单
func TestPayPosOrderByCash(t *testing.T) {
	pendingPosOrder := &order.OrderInfo{Id: 300, OrderNo: "POSTEST-C", CommunityId: 1, Status: 1, Source: 2, PayAmount: 12.5, UserId: 0}

	t.Run("非收银台订单拒绝", func(t *testing.T) {
		orderStub := &stubPosOrderClient{orderInfo: &order.OrderInfo{Id: 301, CommunityId: 1, Status: 1, Source: 1, PayAmount: 10}}
		payStub := &stubPosPaymentClient{}
		h := newPosTestHandler(orderStub, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders/301/pay-cash", nil)
		c.Params = gin.Params{{Key: "id", Value: "301"}}
		h.PayPosOrderByCash(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.OperationNotAllowed {
			t.Fatalf("C 端订单应收银台代收限制拒绝，code = %d(%s)", resp.Code, resp.Message)
		}
		if payStub.closeCalled || payStub.createCalled {
			t.Fatal("拒绝路径不得触达支付服务")
		}
	})

	t.Run("非待支付订单拒绝", func(t *testing.T) {
		orderStub := &stubPosOrderClient{orderInfo: &order.OrderInfo{Id: 302, CommunityId: 1, Status: 2, Source: 2, PayAmount: 10}}
		payStub := &stubPosPaymentClient{}
		h := newPosTestHandler(orderStub, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders/302/pay-cash", nil)
		c.Params = gin.Params{{Key: "id", Value: "302"}}
		h.PayPosOrderByCash(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.OperationNotAllowed {
			t.Fatalf("非待支付单应拒绝，code = %d(%s)", resp.Code, resp.Message)
		}
		if payStub.closeCalled {
			t.Fatal("已收款订单不得关闭其支付单")
		}
	})

	t.Run("成功路径先关旧单再建现金单", func(t *testing.T) {
		orderStub := &stubPosOrderClient{orderInfo: pendingPosOrder}
		payStub := &stubPosPaymentClient{}
		h := newPosTestHandler(orderStub, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders/300/pay-cash", nil)
		c.Params = gin.Params{{Key: "id", Value: "300"}}
		h.PayPosOrderByCash(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.Success {
			t.Fatalf("现金收款应成功，code = %d(%s)", resp.Code, resp.Message)
		}
		// 先关遗留待支付单：付款码失败后换现金，旧 payment_no 必须先失效，杜绝双通道重复收款
		if !payStub.closeCalled || payStub.closeReq.GetOrderId() != 300 {
			t.Fatalf("必须先关闭订单遗留待支付支付单: %+v", payStub.closeReq)
		}
		if !payStub.createCalled {
			t.Fatal("应创建现金支付单")
		}
		if payStub.createReq.GetPayType() != posPayTypeCash || payStub.createReq.GetAmount() != 12.5 {
			t.Fatalf("现金支付单参数错误: %+v", payStub.createReq)
		}
		if paid, _ := resp.Data["paid"].(bool); !paid {
			t.Error("现金收款应立即回 paid=true")
		}
	})

	t.Run("关旧单失败时不建现金单", func(t *testing.T) {
		orderStub := &stubPosOrderClient{orderInfo: pendingPosOrder}
		payStub := &stubPosPaymentClient{closeErr: context.DeadlineExceeded}
		h := newPosTestHandler(orderStub, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders/300/pay-cash", nil)
		c.Params = gin.Params{{Key: "id", Value: "300"}}
		h.PayPosOrderByCash(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code == errcode.Success {
			t.Fatal("关闭遗留支付单失败时应报错而非继续收款")
		}
		if payStub.createCalled {
			t.Fatal("关旧单失败不得创建现金支付单（避免旧码+现金并存）")
		}
	})
}

// TestEnsurePosCodePaySession 待支付订单重建付款码收款会话（订单查询「继续收款」入口）
func TestEnsurePosCodePaySession(t *testing.T) {
	t.Run("两渠道均未绑定时拒绝", func(t *testing.T) {
		orderStub := &stubPosOrderClient{orderInfo: &order.OrderInfo{Id: 400, OrderNo: "POSTEST-S", CommunityId: 1, Status: 1, Source: 2, PayAmount: 8}}
		payStub := &stubPosPaymentClient{}
		h := newPosTestHandlerWithUser(orderStub, &stubPosUserClient{posChannel: posChannelOf("cash")}, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders/400/pay-code-session", nil)
		c.Params = gin.Params{{Key: "id", Value: "400"}}
		h.EnsurePosCodePaySession(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.OperationNotAllowed {
			t.Fatalf("付款码未开启应拒绝，code = %d(%s)", resp.Code, resp.Message)
		}
		if payStub.createCalled {
			t.Fatal("渠道未开启时不得创建支付单")
		}
	})

	t.Run("两渠道齐备时以占位 5 重建会话", func(t *testing.T) {
		orderStub := &stubPosOrderClient{orderInfo: &order.OrderInfo{Id: 400, OrderNo: "POSTEST-S", CommunityId: 1, Status: 1, Source: 2, PayAmount: 8}}
		userStub := &stubPosUserClient{posChannel: posChannelOf("wechat:1", "alipay:2", "cash")}
		payStub := &stubPosPaymentClient{active: []*payment.ActivePayMethod{
			{ConfigId: 1, Platform: "wechat", AppId: "wx1"},
			{ConfigId: 2, Platform: "alipay", AppId: "2021"},
		}}
		h := newPosTestHandlerWithUser(orderStub, userStub, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders/400/pay-code-session", nil)
		c.Params = gin.Params{{Key: "id", Value: "400"}}
		h.EnsurePosCodePaySession(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.Success {
			t.Fatalf("重建会话应成功，code = %d(%s)", resp.Code, resp.Message)
		}
		if !payStub.createCalled || payStub.createReq.GetPayType() != posPayTypeWechatCode {
			t.Fatalf("应以占位 pay_type=5 创建/复用待支付单: %+v", payStub.createReq)
		}
		if payStub.createReq.GetAmount() != 8 {
			t.Errorf("金额应取订单应付: %+v", payStub.createReq)
		}
		if no, _ := resp.Data["payment_no"].(string); no != "ZFPOSTEST" {
			t.Errorf("应回传支付单号供扫码窗使用，got %v", resp.Data["payment_no"])
		}
	})
}

// TestCreatePosOrderManualAmount 直接收款单（manual_amount>0 无商品单）入参校验
// 直接收款是 2026-10-25 新增形态：金额即成交金额，仅限到店，不叠加券/手动优惠
func TestCreatePosOrderManualAmount(t *testing.T) {
	t.Run("非到店模式拒绝", func(t *testing.T) {
		body := []byte(`{"user_id":0,"pay_type":4,"delivery_type":1,"manual_amount":5}`)
		orderStub := &stubPosOrderClient{shift: &order.PosShiftInfo{Id: 1}}
		h := newPosTestHandlerWithUser(orderStub, &stubPosUserClient{posChannel: posChannelOf("cash")}, &stubPosPaymentClient{})

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders", body)
		h.CreatePosOrder(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.ParamError {
			t.Fatalf("直接收款应仅限到店模式，code = %d(%s)", resp.Code, resp.Message)
		}
		if orderStub.createCalled {
			t.Fatal("校验失败不得建单")
		}
	})

	t.Run("金额超出单笔上限拒绝", func(t *testing.T) {
		body := []byte(`{"user_id":0,"pay_type":4,"delivery_type":3,"manual_amount":1000000}`)
		orderStub := &stubPosOrderClient{shift: &order.PosShiftInfo{Id: 1}}
		h := newPosTestHandlerWithUser(orderStub, &stubPosUserClient{posChannel: posChannelOf("cash")}, &stubPosPaymentClient{})

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders", body)
		h.CreatePosOrder(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.ParamError {
			t.Fatalf("超上限应拒绝，code = %d(%s)", resp.Code, resp.Message)
		}
		if orderStub.createCalled {
			t.Fatal("校验失败不得建单")
		}
	})

	t.Run("金额低于下限拒绝", func(t *testing.T) {
		// 0.004 会在服务端四舍五入成 0 元完结单，网关须拒绝（0 元口径属全额优惠特例）
		body := []byte(`{"user_id":0,"pay_type":4,"delivery_type":3,"manual_amount":0.004}`)
		orderStub := &stubPosOrderClient{shift: &order.PosShiftInfo{Id: 1}}
		h := newPosTestHandlerWithUser(orderStub, &stubPosUserClient{posChannel: posChannelOf("cash")}, &stubPosPaymentClient{})

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders", body)
		h.CreatePosOrder(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.ParamError {
			t.Fatalf("低于 0.01 元应拒绝，code = %d(%s)", resp.Code, resp.Message)
		}
		if orderStub.createCalled {
			t.Fatal("校验失败不得建单")
		}
	})

	t.Run("直接收款带商品明细拒绝", func(t *testing.T) {
		// manual_amount 与 items 互斥：若放行，服务端按明细扣库存但不建 order_items，
		// 取消单回补不到明细造成库存永久泄漏
		body := []byte(`{"user_id":0,"pay_type":4,"delivery_type":3,"manual_amount":8.8,"items":[{"sku_id":3,"quantity":1}]}`)
		orderStub := &stubPosOrderClient{shift: &order.PosShiftInfo{Id: 1}}
		payStub := &stubPosPaymentClient{}
		h := newPosTestHandlerWithUser(orderStub, &stubPosUserClient{posChannel: posChannelOf("cash")}, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders", body)
		h.CreatePosOrder(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.ParamError {
			t.Fatalf("直接收款带明细应拒绝，code = %d(%s)", resp.Code, resp.Message)
		}
		if orderStub.createCalled || payStub.createCalled {
			t.Fatal("校验失败不得建单/建支付单")
		}
	})

	t.Run("常规单无商品无金额拒绝", func(t *testing.T) {
		body := []byte(`{"user_id":0,"pay_type":4,"delivery_type":3}`)
		orderStub := &stubPosOrderClient{shift: &order.PosShiftInfo{Id: 1}}
		h := newPosTestHandlerWithUser(orderStub, &stubPosUserClient{posChannel: posChannelOf("cash")}, &stubPosPaymentClient{})

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders", body)
		h.CreatePosOrder(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.ParamError {
			t.Fatalf("无商品且非直接收款应拒绝，code = %d(%s)", resp.Code, resp.Message)
		}
		if orderStub.createCalled {
			t.Fatal("校验失败不得建单")
		}
	})

	t.Run("现金直接收款正常建单且券/优惠被清零", func(t *testing.T) {
		// 券挂在会员名下，直接收款不允许叠加：即使带 user_id 与 coupon_id 也应被清零
		body := []byte(`{"user_id":55,"pay_type":4,"delivery_type":3,"manual_amount":8.8,"coupon_id":9,"manual_discount":1}`)
		orderStub := &stubPosOrderClient{
			shift:     &order.PosShiftInfo{Id: 1},
			orderInfo: &order.OrderInfo{Id: 95, OrderNo: "POSTEST-M", PayAmount: 8.8},
		}
		payStub := &stubPosPaymentClient{}
		h := newPosTestHandlerWithUser(orderStub, &stubPosUserClient{posChannel: posChannelOf("cash")}, payStub)

		c, recorder := newPosTestContext(http.MethodPost, "/api/v1/admin/pos/orders", body)
		h.CreatePosOrder(c)

		resp := decodeRefreshResp(t, recorder)
		if resp.Code != errcode.Success {
			t.Fatalf("直接收款应成功，code = %d(%s)", resp.Code, resp.Message)
		}
		if !orderStub.createCalled {
			t.Fatal("应创建订单")
		}
		if got := orderStub.createReq.GetManualAmount(); got != 8.8 {
			t.Errorf("manual_amount 应原样透传，got %v", got)
		}
		if n := len(orderStub.createReq.GetItems()); n != 0 {
			t.Errorf("直接收款单不应有商品明细，got %d 项", n)
		}
		if got := orderStub.createReq.GetCouponId(); got != 0 {
			t.Errorf("直接收款单不允许用券，coupon_id 应清零，got %v", got)
		}
		if got := orderStub.createReq.GetManualDiscount(); got != 0 {
			t.Errorf("直接收款单不允许手动优惠，manual_discount 应清零，got %v", got)
		}
		if !payStub.createCalled || payStub.createReq.GetPayType() != posPayTypeCash || payStub.createReq.GetAmount() != 8.8 {
			t.Fatalf("现金直接收款应创建即收款的现金支付单: %+v", payStub.createReq)
		}
		if paid, _ := resp.Data["paid"].(bool); !paid {
			t.Error("现金直接收款应立即回 paid=true")
		}
	})
}
