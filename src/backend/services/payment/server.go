package main

import (
	stdctx "context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/bizerr"
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

// PayByAuthCode 付款码扣款（收银台 B扫C：扫顾客付款码对已有待支付单收款）
func (s *server) PayByAuthCode(ctx stdctx.Context, req *payment.PayByAuthCodeReq) (*payment.PayByAuthCodeResp, error) {
	resp, err := s.paymentService.PayByAuthCode(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return resp, nil
}

// ClosePayments 关闭订单遗留的全部待支付支付单（取消订单/收银台换支付方式前置）
// 释放 pending_flag 占位键后同订单才可创建新待支付单；已关单不再可扣款
func (s *server) ClosePayments(ctx stdctx.Context, req *payment.ClosePaymentsReq) (*common.CommonResponse, error) {
	if err := s.paymentService.ClosePayments(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// GetPayment 查询支付记录
func (s *server) GetPayment(ctx stdctx.Context, req *payment.GetPaymentReq) (*payment.PaymentInfo, error) {
	p, err := s.paymentService.GetPayment(ctx, req.OrderId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return service.ToPaymentInfo(p), nil
}

// GetPaymentStatus 按支付单号查询支付状态（C 端支付宝回跳结果页免登录轮询用）
func (s *server) GetPaymentStatus(ctx stdctx.Context, req *payment.GetPaymentStatusReq) (*payment.GetPaymentStatusResp, error) {
	p, err := s.paymentService.GetPaymentStatusByNo(ctx, req.GetPaymentNo())
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &payment.GetPaymentStatusResp{
		PaymentNo: p.PaymentNo,
		Status:    p.Status,
		Amount:    p.Amount,
		OrderId:   p.OrderID,
		OrderNo:   p.OrderNo,
	}, nil
}

// GetAlipayPayUrl 凭支付单号换取支付宝收银台链接（H5 支付引导页免登录换取用）
// 仅待支付的支付宝单返回链接，其它状态只回支付单状态供引导页跳结果页
func (s *server) GetAlipayPayUrl(ctx stdctx.Context, req *payment.GetAlipayPayUrlReq) (*payment.GetAlipayPayUrlResp, error) {
	p, payURL, err := s.paymentService.GetAlipayPayUrl(ctx, req.GetPaymentNo(), req.GetFrom())
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &payment.GetAlipayPayUrlResp{
		PaymentNo:    p.PaymentNo,
		Status:       p.Status,
		OrderId:      p.OrderID,
		AlipayPayUrl: payURL,
	}, nil
}

// FindOrdersByPayNo 按支付单号或渠道交易号反查关联订单号（后台订单列表搜索用）
// 未命中返回空列表而非错误，gateway 以入参是否携带 pay_no 区分「没传」与「没查到」
func (s *server) FindOrdersByPayNo(ctx stdctx.Context, req *payment.FindOrdersByPayNoReq) (*payment.FindOrdersByPayNoResp, error) {
	orderNos, err := s.paymentService.FindOrdersByPayNo(ctx, req.GetPayNo())
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &payment.FindOrdersByPayNoResp{OrderNos: orderNos}, nil
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
// completed=false 表示渠道已受理未到终态（微信 PROCESSING 常态），
// 调用方不得按成功收尾，终态由 payment 侧退款查单任务收敛
func (s *server) ProcessRefund(ctx stdctx.Context, req *payment.ProcessRefundReq) (*payment.ProcessRefundResp, error) {
	resp, err := s.paymentService.ProcessRefund(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return resp, nil
}

// QueryRefundResult 查询渠道退款流水状态（读库不触渠道）
func (s *server) QueryRefundResult(ctx stdctx.Context, req *payment.QueryRefundResultReq) (*payment.QueryRefundResultResp, error) {
	resp, err := s.paymentService.QueryRefundResult(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return resp, nil
}

// ResetRefundFlow 人工重置待退款流水的自动收敛预算（不再次触渠道）
func (s *server) ResetRefundFlow(ctx stdctx.Context, req *payment.ResetRefundFlowReq) (*payment.ResetRefundFlowResp, error) {
	resp, err := s.paymentService.ResetRefundFlow(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return resp, nil
}

// ListPayConfigs 支付配置列表（管理端，含凭证与就绪状态；网关负责脱敏）
func (s *server) ListPayConfigs(ctx stdctx.Context, _ *payment.ListPayConfigsReq) (*payment.ListPayConfigsResp, error) {
	rows, err := s.paymentService.GetPayConfigs(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	list := make([]*payment.PayConfigRow, 0, len(rows))
	for i := range rows {
		list = append(list, s.toPayConfigRow(rows[i]))
	}
	return &payment.ListPayConfigsResp{List: list}, nil
}

// SavePayConfigRow 新增（id=0）或更新一条支付配置
// 保存成功后本实例立即热重建，其余实例按版本号在下一次支付调用时自动刷新
func (s *server) SavePayConfigRow(ctx stdctx.Context, in *payment.PayConfigRow) (*payment.PayConfigRow, error) {
	row, err := s.paymentService.SavePayConfigRow(ctx, in)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return s.toPayConfigRow(row), nil
}

// DeletePayConfigRow 删除一条支付配置（渠道引用校验由网关负责）
func (s *server) DeletePayConfigRow(ctx stdctx.Context, req *payment.DeletePayConfigRowReq) (*common.CommonResponse, error) {
	if err := s.paymentService.DeletePayConfigRow(ctx, req.Id); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListActivePayMethods 启用且就绪的支付配置清单（C 端渲染过滤与下单解析用）
func (s *server) ListActivePayMethods(ctx stdctx.Context, _ *payment.ListActivePayMethodsReq) (*payment.ListActivePayMethodsResp, error) {
	return &payment.ListActivePayMethodsResp{Methods: s.paymentService.ListActivePayMethods(ctx)}, nil
}

// UploadPayCert 上传支付证书文件
// 文件内容直接存入对应配置行，上传后立即热更新支付客户端；
// 成功时回传最新配置行（含新 updated_at 与上传状态位）：
// 上传推进了乐观锁版本，调用方同步新版本后才能继续保存该行而不被乐观锁拒绝
func (s *server) UploadPayCert(ctx stdctx.Context, req *payment.UploadPayCertReq) (*payment.UploadPayCertResp, error) {
	row, err := s.paymentService.UploadPayCert(ctx, req)
	if err != nil {
		return &payment.UploadPayCertResp{Success: false, Message: err.Error()}, nil
	}
	return &payment.UploadPayCertResp{Success: true, Message: "上传成功", Row: s.toPayConfigRow(row)}, nil
}

// SetPayConfigEnabled 单独启停一条支付配置（窄口径：仅更新 enabled，不做整行覆写）
// 列表开关专用：开关场景只持有 enabled + 版本号，走全量保存会因名称必填被拒
func (s *server) SetPayConfigEnabled(ctx stdctx.Context, req *payment.SetPayConfigEnabledReq) (*payment.PayConfigRow, error) {
	row, err := s.paymentService.SetPayConfigEnabled(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return s.toPayConfigRow(row), nil
}

// SyncPaymentFromChannel 向渠道核实收款结果，已收款则补同步
// 幂等可反复调用：渠道未收款时不改动任何本地状态，仅回传渠道事实
// operator_* 只用于留痕（B3）：后台按钮由网关注入操作人，内部路径（订单取消前置）留空即系统口径
func (s *server) SyncPaymentFromChannel(ctx stdctx.Context, req *payment.SyncPaymentReq) (*payment.SyncPaymentResp, error) {
	reason := req.Reason
	if reason == "" {
		reason = model.ConfirmRefManualSync
	}
	outcome, err := s.paymentService.SyncPaymentFromChannel(ctx, req.OrderId, req.PaymentNo, reason,
		service.SyncActorFromRequest(req.OperatorId, req.OperatorName))
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToSyncPaymentResp(outcome), nil
}

// ListOrderPayments 按订单列出全部支付尝试（含未成功），供后台详情展示支付次数与状态
func (s *server) ListOrderPayments(ctx stdctx.Context, req *payment.ListOrderPaymentsReq) (*payment.ListOrderPaymentsResp, error) {
	list, err := s.paymentService.ListOrderPayments(ctx, req.OrderId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	infos := make([]*payment.PaymentInfo, 0, len(list))
	for _, p := range list {
		infos = append(infos, service.ToPaymentInfo(p))
	}
	return &payment.ListOrderPaymentsResp{List: infos}, nil
}

// ListPaymentAnomalies 支付异常队列（含对账轨迹）
// with_raw 由网关按 payment:raw:view 权限设置，community_id 由网关按管理员社区权限下发（超管为 0 表示不限）；
// 权限判定只在网关做一次，服务层只按入参取数与回传原文
func (s *server) ListPaymentAnomalies(ctx stdctx.Context, req *payment.ListPaymentAnomaliesReq) (*payment.ListPaymentAnomaliesResp, error) {
	list, total, pending, err := s.paymentService.ListPaymentAnomalies(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &payment.ListPaymentAnomaliesResp{List: list, Total: total, Pending: pending}, nil
}

// MarkPaymentAnomalyHandled 异常销账，remark 记录人工核实依据
func (s *server) MarkPaymentAnomalyHandled(ctx stdctx.Context, req *payment.MarkPaymentAnomalyReq) (*common.CommonResponse, error) {
	if err := s.paymentService.MarkPaymentAnomalyHandled(ctx, req.Id, req.Remark, req.CommunityId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// MarkPaymentPaid 人工把待支付单标记为已支付
// 订单侧的「必须仍是待支付」约束由网关先读订单状态把关，服务层再按支付单状态兜底
func (s *server) MarkPaymentPaid(ctx stdctx.Context, req *payment.MarkPaymentPaidReq) (*common.CommonResponse, error) {
	if err := s.paymentService.MarkPaymentPaidManually(ctx, req.OrderId, req.PaymentNo, req.TransactionId, req.Amount, req.Remark); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// toPayConfigRow 将支付配置模型转换为 proto 结构
// 就绪口径：启用 + 当前平台必填字段齐全（支付宝另要求密钥离线自检通过）
//
// 参数：
//   - row: 支付配置模型
//
// 返回值：
//   - *payment.PayConfigRow: proto 支付配置行
func (s *server) toPayConfigRow(row *model.PayConfig) *payment.PayConfigRow {
	ready, readyMsg := false, ""
	switch row.Platform {
	case model.PayPlatformWechat:
		ready, readyMsg = service.WechatConfigStatus(row)
	case model.PayPlatformAlipay:
		ready, readyMsg = service.AlipayConfigStatus(row)
	}
	return &payment.PayConfigRow{
		Id:       row.ID,
		Name:     row.Name,
		Platform: row.Platform,
		Enabled:  row.Enabled,

		WechatAppSecret:    row.WechatAppSecret,
		WechatMchId:        row.WechatMchID,
		WechatApiV3Key:     row.WechatAPIV3Key,
		WechatCertSerialNo: row.WechatCertSerialNo,
		WechatPrivateKey:   row.WechatPrivateKey,
		WechatNotifyUrl:    row.WechatNotifyURL,
		WechatCertUploaded: row.WechatPrivateKey != "",

		AlipayAppId:              row.AlipayAppID,
		AlipayPrivateKey:         row.AlipayPrivateKey,
		AlipayPublicKey:          row.AlipayPublicKey,
		AlipayNotifyUrl:          row.AlipayNotifyURL,
		AlipayReturnUrl:          row.AlipayReturnURL,
		AlipayMode:               row.AlipayMode,
		AlipayGatewayUrl:         row.AlipayGatewayURL,
		AlipayCertUploaded:       row.AlipayPrivateKey != "",
		AlipayAppCertUploaded:    row.AlipayAppCert != "",
		AlipayPublicCertUploaded: row.AlipayPublicCert != "",
		AlipayRootCertUploaded:   row.AlipayRootCert != "",

		UpdatedAt:    row.UpdatedAt,
		Ready:        ready,
		ReadyMessage: readyMsg,
	}
}
