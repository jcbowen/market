package main

import (
	stdctx "context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/model"
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
	// 调起参数按支付方式归属字段，现金支付无需第三方调起参数
	switch p.PayType {
	case 1:
		resp.WechatPayParams = payParams
	case 2:
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

// BatchGetPayments 按订单批量查询成功支付记录（后台订单列表一次往返取回整页支付方式）
func (s *server) BatchGetPayments(ctx stdctx.Context, req *payment.BatchGetPaymentsReq) (*payment.BatchGetPaymentsResp, error) {
	list, err := s.paymentService.BatchGetPayments(ctx, req.OrderIds)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	infos := make([]*payment.PaymentInfo, 0, len(list))
	for _, p := range list {
		infos = append(infos, service.ToPaymentInfo(p))
	}
	return &payment.BatchGetPaymentsResp{List: infos}, nil
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

// GetPayConfig 查询支付配置
// 返回数据库中的支付渠道开关与微信/支付宝配置，并附渠道就绪状态
func (s *server) GetPayConfig(ctx stdctx.Context, _ *payment.GetPayConfigReq) (*payment.PayConfig, error) {
	row, err := s.paymentService.GetPayConfig(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return s.toPayConfig(row), nil
}

// SavePayConfig 保存支付配置
// 保存成功后本实例立即热更新，其余实例按版本号在下一次支付调用时自动刷新
func (s *server) SavePayConfig(ctx stdctx.Context, cfg *payment.PayConfig) (*common.CommonResponse, error) {
	if err := s.paymentService.SavePayConfig(ctx, cfg); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// UploadPayCert 上传支付证书文件
// 文件内容直接存入数据库，上传后立即热更新支付客户端
func (s *server) UploadPayCert(ctx stdctx.Context, req *payment.UploadPayCertReq) (*payment.UploadPayCertResp, error) {
	if err := s.paymentService.UploadPayCert(ctx, req); err != nil {
		return &payment.UploadPayCertResp{Success: false, Message: err.Error()}, nil
	}
	return &payment.UploadPayCertResp{Success: true, Message: "上传成功"}, nil
}

// toPayConfig 将支付配置模型转换为 proto 结构
// 参数：
//   - row: 支付配置模型
//
// 返回值：
//   - *payment.PayConfig: proto 支付配置
func (s *server) toPayConfig(row *model.PayConfig) *payment.PayConfig {
	var channels []string
	if row.EnabledChannels != "" {
		_ = json.Unmarshal([]byte(row.EnabledChannels), &channels)
	}
	var wechatClientChannels []string
	if row.WechatClientChannels != "" {
		_ = json.Unmarshal([]byte(row.WechatClientChannels), &wechatClientChannels)
	}
	var alipayClientChannels []string
	if row.AlipayClientChannels != "" {
		_ = json.Unmarshal([]byte(row.AlipayClientChannels), &alipayClientChannels)
	}

	wechat := config.WechatConfig{
		AppID:        row.WechatAppID,
		AppSecret:    row.WechatAppSecret,
		MchID:        row.WechatMchID,
		APIV3Key:     row.WechatAPIV3Key,
		CertSerialNo: row.WechatCertSerialNo,
		PrivateKey:   row.WechatPrivateKey,
		NotifyURL:    row.WechatNotifyURL,
	}
	// 支付宝就绪口径与 C 端展示、下单可用性一致：字段齐全且密钥离线自检通过
	alipayReady, alipayReadyMsg := service.AlipayConfigStatus(row)

	return &payment.PayConfig{
		EnabledChannels:    channels,
		WechatAppId:        row.WechatAppID,
		WechatAppSecret:    row.WechatAppSecret,
		WechatMchId:        row.WechatMchID,
		WechatApiV3Key:     row.WechatAPIV3Key,
		WechatCertSerialNo: row.WechatCertSerialNo,
		WechatPrivateKey:   row.WechatPrivateKey,
		WechatNotifyUrl:    row.WechatNotifyURL,
		AlipayAppId:        row.AlipayAppID,
		AlipayPrivateKey:   row.AlipayPrivateKey,
		AlipayPublicKey:    row.AlipayPublicKey,
		AlipayNotifyUrl:    row.AlipayNotifyURL,
		AlipayReturnUrl:    row.AlipayReturnURL,
		WechatCertUploaded: row.WechatPrivateKey != "",
		AlipayCertUploaded: row.AlipayPrivateKey != "",
		// 支付宝加签方式与三证书上传状态：证书内容不回传，仅回传标志供后台表单展示
		AlipayMode:               row.AlipayMode,
		AlipayAppCertUploaded:    row.AlipayAppCert != "",
		AlipayPublicCertUploaded: row.AlipayPublicCert != "",
		AlipayRootCertUploaded:   row.AlipayRootCert != "",
		UpdatedAt:                row.UpdatedAt,
		WechatPaymentReady:       wechat.IsPaymentConfigured(),
		AlipayReady:              alipayReady,
		AlipayReadyMessage:       alipayReadyMsg,
		AlipayGatewayUrl:         row.AlipayGatewayURL,
		WechatClientChannels:     wechatClientChannels,
		AlipayClientChannels:     alipayClientChannels,
	}
}
