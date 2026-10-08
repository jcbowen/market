package main

import (
	stdctx "context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/service"
)

// server 实现 payment.PaymentServiceServer 接口
type server struct {
	payment.UnimplementedPaymentServiceServer
	paymentService *service.PaymentService
}

// newServer 创建支付服务 gRPC 实现
// 参数：
//   - paymentService: 支付业务服务
//
// 返回值：
//   - *server: gRPC 服务实现
func newServer(paymentService *service.PaymentService) *server {
	return &server{paymentService: paymentService}
}

// CreatePayment 创建支付单
func (s *server) CreatePayment(ctx stdctx.Context, req *payment.CreatePaymentReq) (*payment.CreatePaymentResp, error) {
	p, payParams, err := s.paymentService.CreatePayment(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	resp := &payment.CreatePaymentResp{
		PaymentNo: p.PaymentNo,
		PayType:   p.PayType,
		Amount:    p.Amount,
	}
	if p.PayType == 1 {
		resp.WechatPayParams = payParams
	} else {
		resp.AlipayPayParams = payParams
	}
	return resp, nil
}

// GetPayment 查询支付记录
func (s *server) GetPayment(ctx stdctx.Context, req *payment.GetPaymentReq) (*payment.PaymentInfo, error) {
	p, err := s.paymentService.GetPayment(ctx, req.OrderId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return service.ToPaymentInfo(p), nil
}

// HandleWechatCallback 处理微信支付回调
func (s *server) HandleWechatCallback(ctx stdctx.Context, req *payment.WechatCallbackReq) (*common.CommonResponse, error) {
	if err := s.paymentService.HandleWechatCallback(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// HandleAlipayCallback 处理支付宝支付回调
func (s *server) HandleAlipayCallback(ctx stdctx.Context, req *payment.AlipayCallbackReq) (*common.CommonResponse, error) {
	if err := s.paymentService.HandleAlipayCallback(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ProcessRefund 处理退款
func (s *server) ProcessRefund(ctx stdctx.Context, req *payment.ProcessRefundReq) (*common.CommonResponse, error) {
	if err := s.paymentService.ProcessRefund(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}
