package main

import (
	stdctx "context"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/order/model"
	"github.com/jcbowen/market/src/backend/services/order/service"
)

// server 实现 order.OrderServiceServer 接口
type server struct {
	order.UnimplementedOrderServiceServer
	orderService   *service.OrderService
	refundService  *service.RefundService
	paymentClient  payment.PaymentServiceClient
}

// newServer 创建订单服务 gRPC 实现
// 参数：
//   - db: MySQL 实例
//   - paymentClient: 支付服务 gRPC 客户端
//
// 返回值：
//   - *server: gRPC 服务实现
func newServer(db *mysql.Instance, paymentClient payment.PaymentServiceClient) *server {
	return &server{
		orderService:  service.NewOrderService(db),
		refundService: service.NewRefundService(db, paymentClient),
		paymentClient: paymentClient,
	}
}

// CreateOrder 创建订单
func (s *server) CreateOrder(ctx stdctx.Context, req *order.CreateOrderReq) (*order.OrderInfo, error) {
	o, err := s.orderService.CreateOrder(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return ToOrderInfo(o), nil
}

// GetOrder 获取订单详情
func (s *server) GetOrder(ctx stdctx.Context, req *order.GetOrderReq) (*order.OrderDetail, error) {
	o, items, logs, err := s.orderService.GetOrder(ctx, req.Id, req.UserId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	detail := &order.OrderDetail{
		Order:      ToOrderInfo(o),
		Items:      make([]*order.OrderItemInfo, 0, len(items)),
		StatusLogs: make([]*order.OrderStatusLog, 0, len(logs)),
	}
	for _, item := range items {
		detail.Items = append(detail.Items, ToOrderItemInfo(item))
	}
	for _, log := range logs {
		detail.StatusLogs = append(detail.StatusLogs, ToOrderStatusLog(log))
	}
	return detail, nil
}

// ListOrders 查询订单列表
func (s *server) ListOrders(ctx stdctx.Context, req *order.ListOrdersReq) (*order.ListOrdersResp, error) {
	list, total, err := s.orderService.ListOrders(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	resp := &order.ListOrdersResp{
		List: make([]*order.OrderInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, o := range list {
		resp.List = append(resp.List, ToOrderInfo(o))
	}
	return resp, nil
}

// CancelOrder 取消订单
func (s *server) CancelOrder(ctx stdctx.Context, req *order.CancelOrderReq) (*common.CommonResponse, error) {
	if err := s.orderService.CancelOrder(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// UpdateOrderStatus 更新订单状态
func (s *server) UpdateOrderStatus(ctx stdctx.Context, req *order.UpdateOrderStatusReq) (*common.CommonResponse, error) {
	if err := s.orderService.UpdateOrderStatus(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// GetOrderStatistics 获取订单状态统计
func (s *server) GetOrderStatistics(ctx stdctx.Context, req *order.GetOrderStatisticsReq) (*order.OrderStatisticsResp, error) {
	resp, err := s.orderService.GetOrderStatistics(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return resp, nil
}

// GetSalesStatistics 获取销售统计
func (s *server) GetSalesStatistics(ctx stdctx.Context, req *order.GetSalesStatisticsReq) (*order.SalesStatisticsResp, error) {
	resp, err := s.orderService.GetSalesStatistics(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return resp, nil
}

// GetProductRankings 获取商品销售排行
func (s *server) GetProductRankings(ctx stdctx.Context, req *order.GetProductRankingsReq) (*order.ProductRankingsResp, error) {
	list, err := s.orderService.GetProductRankings(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &order.ProductRankingsResp{List: list}, nil
}

// GetRevenueReport 获取营收报表
func (s *server) GetRevenueReport(ctx stdctx.Context, req *order.GetRevenueReportReq) (*order.RevenueReportResp, error) {
	list, err := s.orderService.GetRevenueReport(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &order.RevenueReportResp{List: list}, nil
}

// ApplyRefund 申请退款
func (s *server) ApplyRefund(ctx stdctx.Context, req *order.ApplyRefundReq) (*order.RefundInfo, error) {
	refund, err := s.refundService.ApplyRefund(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return ToRefundInfo(refund), nil
}

// ListRefunds 查询退款列表
func (s *server) ListRefunds(ctx stdctx.Context, req *order.ListRefundsReq) (*order.ListRefundsResp, error) {
	list, total, err := s.refundService.ListRefunds(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	resp := &order.ListRefundsResp{
		List: make([]*order.RefundInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, r := range list {
		resp.List = append(resp.List, ToRefundInfo(r))
	}
	return resp, nil
}

// ApproveRefund 同意退款
func (s *server) ApproveRefund(ctx stdctx.Context, req *order.ApproveRefundReq) (*common.CommonResponse, error) {
	if err := s.refundService.ApproveRefund(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// RejectRefund 拒绝退款
func (s *server) RejectRefund(ctx stdctx.Context, req *order.RejectRefundReq) (*common.CommonResponse, error) {
	if err := s.refundService.RejectRefund(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// RetryRefund 重试退款（卡在退款中 status=4 的退款单重新发起渠道退款）
func (s *server) RetryRefund(ctx stdctx.Context, req *order.RetryRefundReq) (*common.CommonResponse, error) {
	if err := s.refundService.RetryRefund(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ToOrderInfo 将订单模型转换为 proto 信息
// 参数：
//   - o: 订单模型
//
// 返回值：
//   - *order.OrderInfo: proto 订单信息
func ToOrderInfo(o *model.Order) *order.OrderInfo {
	if o == nil {
		return nil
	}
	return &order.OrderInfo{
		Id:             o.ID,
		OrderNo:        o.OrderNo,
		CommunityId:    o.CommunityID,
		UserId:         o.UserID,
		Status:         o.Status,
		TotalAmount:    o.TotalAmount,
		DiscountAmount: o.DiscountAmount,
		DeliveryFee:    o.DeliveryFee,
		PayAmount:      o.PayAmount,
		DeliveryType:   o.DeliveryType,
		DeliveryTime:   o.DeliveryTime,
		ContactName:    o.ContactName,
		ContactPhone:   o.ContactPhone,
		ContactAddress: o.ContactAddress,
		Remark:         o.Remark,
		PayTime:        ptrString(o.PayTime),
		DeliverTime:    ptrString(o.DeliverTime),
		CompleteTime:   ptrString(o.CompleteTime),
		CancelTime:     ptrString(o.CancelTime),
		CancelReason:   o.CancelReason,
		CreatedAt:      o.CreatedAt,
		ExpressCompany: o.ExpressCompany,
		ExpressNo:      o.ExpressNo,
	}
}

// ToOrderItemInfo 将订单商品项模型转换为 proto 信息
// 参数：
//   - item: 订单商品项模型
//
// 返回值：
//   - *order.OrderItemInfo: proto 订单商品项信息
func ToOrderItemInfo(item *model.OrderItem) *order.OrderItemInfo {
	if item == nil {
		return nil
	}
	return &order.OrderItemInfo{
		Id:           item.ID,
		ProductId:    item.ProductID,
		SkuId:        item.SkuID,
		ProductName:  item.ProductName,
		SpecName:     item.SpecName,
		ProductImage: item.ProductImage,
		Price:        item.Price,
		Quantity:     item.Quantity,
		TotalPrice:   item.TotalPrice,
	}
}

// ToOrderStatusLog 将订单状态日志模型转换为 proto 信息
// 参数：
//   - log: 订单状态日志模型
//
// 返回值：
//   - *order.OrderStatusLog: proto 订单状态日志
func ToOrderStatusLog(log *model.OrderStatusLog) *order.OrderStatusLog {
	if log == nil {
		return nil
	}
	return &order.OrderStatusLog{
		FromStatus:   log.FromStatus,
		ToStatus:     log.ToStatus,
		OperatorType: log.OperatorType,
		Remark:       log.Remark,
		CreatedAt:    log.CreatedAt,
	}
}

// ToRefundInfo 将退款模型转换为 proto 信息（含关联订单号与商品明细）
// 参数：
//   - r: 退款模型
//
// 返回值：
//   - *order.RefundInfo: proto 退款信息
func ToRefundInfo(r *model.Refund) *order.RefundInfo {
	if r == nil {
		return nil
	}
	info := &order.RefundInfo{
		Id:            r.ID,
		RefundNo:      r.RefundNo,
		OrderId:       r.OrderID,
		UserId:        r.UserID,
		PaymentNo:     r.PaymentNo,
		Amount:        r.Amount,
		FreightAmount: r.FreightAmount,
		Type:          r.Type,
		Reason:        r.Reason,
		Status:        r.Status,
		RejectReason:  r.RejectReason,
		RefundTime:    ptrString(r.RefundTime),
		CreatedAt:     r.CreatedAt,
		OrderNo:       r.OrderNo,
		Items:         make([]*order.RefundItemInfo, 0, len(r.Items)),
		Logs:          make([]*order.RefundLogInfo, 0, len(r.Logs)),
	}
	for _, item := range r.Items {
		info.Items = append(info.Items, ToRefundItemInfo(item))
	}
	for _, l := range r.Logs {
		info.Logs = append(info.Logs, &order.RefundLogInfo{
			Id:           l.ID,
			Action:       l.Action,
			OperatorType: l.OperatorType,
			OperatorId:   l.OperatorID,
			Remark:       l.Remark,
			CreatedAt:    l.CreatedAt,
		})
	}
	return info
}

// ToRefundItemInfo 将退款商品明细模型转换为 proto 信息
// 参数：
//   - item: 退款商品明细模型
//
// 返回值：
//   - *order.RefundItemInfo: proto 退款商品明细信息
func ToRefundItemInfo(item *model.RefundItem) *order.RefundItemInfo {
	if item == nil {
		return nil
	}
	return &order.RefundItemInfo{
		Id:           item.ID,
		RefundId:     item.RefundID,
		OrderItemId:  item.OrderItemID,
		SkuId:        item.SkuID,
		ProductName:  item.ProductName,
		SpecName:     item.SpecName,
		Quantity:     item.Quantity,
		RefundAmount: item.RefundAmount,
	}
}

// GetRefundConfig 查询退款配置
func (s *server) GetRefundConfig(ctx stdctx.Context, req *order.GetRefundConfigReq) (*order.RefundConfig, error) {
	row, err := s.refundService.GetRefundConfig(ctx)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &order.RefundConfig{RefundFreightPolicy: row.RefundFreightPolicy}, nil
}

// SaveRefundConfig 保存退款配置
func (s *server) SaveRefundConfig(ctx stdctx.Context, req *order.SaveRefundConfigReq) (*common.CommonResponse, error) {
	if err := s.refundService.SaveRefundConfig(ctx, req.RefundFreightPolicy); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ptrString 将字符串指针安全转换为字符串
//
// 参数：
//   - s: 字符串指针
//
// 返回值：
//   - string: 空字符串或指针指向的值
func ptrString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
