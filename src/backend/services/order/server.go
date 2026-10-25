package main

import (
	stdctx "context"
	"encoding/json"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/config"
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
	posHeldService *service.PosHeldService
	shiftService   *service.ShiftService
	prefService    *service.AppPreferenceService
	// exportTaskService 后台导出任务服务（下载中心）
	exportTaskService *service.ExportTaskService
	paymentClient     payment.PaymentServiceClient
}

// newServer 创建订单服务 gRPC 实现
// 参数：
//   - db: MySQL 实例
//   - paymentClient: 支付服务 gRPC 客户端
//   - cfg: 全局配置（取消前置渠道确认开关）
//
// 返回值：
//   - *server: gRPC 服务实现
func newServer(db *mysql.Instance, paymentClient payment.PaymentServiceClient, cfg *config.Config) *server {
	orderSvc := service.NewOrderService(db, cfg)
	return &server{
		orderService:      orderSvc,
		refundService:     service.NewRefundService(db, paymentClient),
		posHeldService:    service.NewPosHeldService(db),
		shiftService:      service.NewShiftService(db),
		prefService:       service.NewAppPreferenceService(db),
		exportTaskService: service.NewExportTaskService(db, orderSvc),
		paymentClient:     paymentClient,
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

// GetOrder 获取订单详情（含商品明细、状态流转与每次售后记录）
func (s *server) GetOrder(ctx stdctx.Context, req *order.GetOrderReq) (*order.OrderDetail, error) {
	o, items, logs, refunds, err := s.orderService.GetOrder(ctx, req.Id, req.UserId)
	if err != nil {
		// 不再一刀切包成 NotFound：无权限（PermissionDenied）等业务错误需按原始码透传，
		// 否则越权探测与「订单不存在」不可区分是设计意图，但 DB 故障也被吞成 NotFound 掩盖真相
		return nil, bizerr.ToStatus(err)
	}

	detail := &order.OrderDetail{
		Order:      ToOrderInfo(o),
		Items:      make([]*order.OrderItemInfo, 0, len(items)),
		StatusLogs: make([]*order.OrderStatusLog, 0, len(logs)),
		Refunds:    make([]*order.RefundInfo, 0, len(refunds)),
	}
	// 详情不做额外聚合统计，明细行数即 len(items)，与列表口径保持一致
	detail.Order.ItemCount = int32(len(items))
	for _, item := range items {
		detail.Items = append(detail.Items, ToOrderItemInfo(item))
	}
	for _, log := range logs {
		detail.StatusLogs = append(detail.StatusLogs, ToOrderStatusLog(log))
	}
	for _, r := range refunds {
		// 详情内的退款单：所属订单商品行数即上面的 len(items)，与列表回填口径一致，
		// 供前端区分「直接收款单的整单退」与「仅金额退全额退完升格为整单退」
		r.OrderItemCount = int32(len(items))
		detail.Refunds = append(detail.Refunds, ToRefundInfo(r))
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

// DeleteOrder C 端用户删除订单（软删除）
func (s *server) DeleteOrder(ctx stdctx.Context, req *order.DeleteOrderReq) (*common.CommonResponse, error) {
	if err := s.orderService.DeleteOrder(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// RestoreOrder 后台恢复被用户软删除的订单
func (s *server) RestoreOrder(ctx stdctx.Context, req *order.RestoreOrderReq) (*common.CommonResponse, error) {
	if err := s.orderService.RestoreOrder(ctx, req); err != nil {
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

// GetUserFrequentProducts 获取用户常买商品（C 端购物车 Top N / 管理端分页排行）
func (s *server) GetUserFrequentProducts(ctx stdctx.Context, req *order.GetUserFrequentProductsReq) (*order.GetUserFrequentProductsResp, error) {
	return s.orderService.GetUserFrequentProducts(ctx, req)
}

// GetUserOrderStatusCounts 用户订单状态角标统计（C 端「我的-我的订单」红标）
func (s *server) GetUserOrderStatusCounts(ctx stdctx.Context, req *order.GetUserOrderStatusCountsReq) (*order.GetUserOrderStatusCountsResp, error) {
	resp, err := s.orderService.GetUserOrderStatusCounts(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return resp, nil
}

// PreviewFreeShipping 免配送费试算（C 端确认页展示）
func (s *server) PreviewFreeShipping(ctx stdctx.Context, req *order.PreviewFreeShippingReq) (*order.PreviewFreeShippingResp, error) {
	return s.orderService.PreviewFreeShipping(ctx, req)
}

// VerifyPickup 自提订单核销
func (s *server) VerifyPickup(ctx stdctx.Context, req *order.VerifyPickupReq) (*order.VerifyPickupResp, error) {
	resp, err := s.orderService.VerifyPickup(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return resp, nil
}

// AssignDelivery 后台指派/取消指派配送员（B 履约工作台）
func (s *server) AssignDelivery(ctx stdctx.Context, req *order.AssignDeliveryReq) (*common.CommonResponse, error) {
	if err := s.orderService.AssignDelivery(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListFulfillmentOrders 履约工作台查单（送货按指派人过滤；自提本社区全部）
func (s *server) ListFulfillmentOrders(ctx stdctx.Context, req *order.ListFulfillmentOrdersReq) (*order.ListFulfillmentOrdersResp, error) {
	list, total, err := s.orderService.ListFulfillmentOrders(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	page, pageSize := int(req.Pagination.GetPage()), int(req.Pagination.GetPageSize())
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 10
	}
	resp := &order.ListFulfillmentOrdersResp{
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

// StartDelivery 履约人员开始配送（status 2/3 → 4）
func (s *server) StartDelivery(ctx stdctx.Context, req *order.StartDeliveryReq) (*common.CommonResponse, error) {
	if err := s.orderService.StartDelivery(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// CompleteDelivery 履约人员送达完成（status 4 → 5，强制签收照片）
func (s *server) CompleteDelivery(ctx stdctx.Context, req *order.CompleteDeliveryReq) (*common.CommonResponse, error) {
	if err := s.orderService.CompleteDelivery(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// CreatePosHeldOrder 创建收银台挂单
func (s *server) CreatePosHeldOrder(ctx stdctx.Context, req *order.CreatePosHeldOrderReq) (*order.PosHeldOrderInfo, error) {
	info, err := s.posHeldService.Create(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return info, nil
}

// ListPosHeldOrders 收银台挂单列表
func (s *server) ListPosHeldOrders(ctx stdctx.Context, req *order.ListPosHeldOrdersReq) (*order.ListPosHeldOrdersResp, error) {
	list, err := s.posHeldService.List(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &order.ListPosHeldOrdersResp{List: list}, nil
}

// DeletePosHeldOrder 删除收银台挂单
func (s *server) DeletePosHeldOrder(ctx stdctx.Context, req *order.DeletePosHeldOrderReq) (*common.CommonResponse, error) {
	if err := s.posHeldService.Delete(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// StartShift 开工：创建进行中班次
func (s *server) StartShift(ctx stdctx.Context, req *order.StartShiftReq) (*order.PosShiftInfo, error) {
	info, err := s.shiftService.StartShift(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return info, nil
}

// GetCurrentShift 当前进行中班次（id=0 表示无进行中班次）
func (s *server) GetCurrentShift(ctx stdctx.Context, req *order.GetCurrentShiftReq) (*order.PosShiftInfo, error) {
	info, err := s.shiftService.GetCurrentShift(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return info, nil
}

// EndShift 交班：生成统计快照 → 置已结束
func (s *server) EndShift(ctx stdctx.Context, req *order.EndShiftReq) (*order.PosShiftInfo, error) {
	info, err := s.shiftService.EndShift(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return info, nil
}

// ListShifts 交班记录分页
func (s *server) ListShifts(ctx stdctx.Context, req *order.ListShiftsReq) (*order.ListShiftsResp, error) {
	return s.shiftService.ListShifts(ctx, req)
}

// GetAppPreferences 获取社区常用应用
func (s *server) GetAppPreferences(ctx stdctx.Context, req *order.GetAppPreferencesReq) (*order.AppPreferencesInfo, error) {
	info, err := s.prefService.Get(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return info, nil
}

// SaveAppPreferences 保存社区常用应用（覆盖式）
func (s *server) SaveAppPreferences(ctx stdctx.Context, req *order.SaveAppPreferencesReq) (*common.CommonResponse, error) {
	if err := s.prefService.Save(ctx, req); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// CreateExportTask 创建后台导出任务（异步生成 CSV，创建即返回）
func (s *server) CreateExportTask(ctx stdctx.Context, req *order.CreateExportTaskReq) (*order.CreateExportTaskResp, error) {
	id, err := s.exportTaskService.CreateExportTask(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &order.CreateExportTaskResp{Id: id}, nil
}

// ListExportTasks 下载中心任务列表（普通管理员仅见自己的，超管见全部）
func (s *server) ListExportTasks(ctx stdctx.Context, req *order.ListExportTasksReq) (*order.ListExportTasksResp, error) {
	list, total, undownloaded, err := s.exportTaskService.ListExportTasks(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &order.ListExportTasksResp{
		List:         list,
		Total:        total,
		Undownloaded: undownloaded,
	}, nil
}

// GetExportTaskFile 取导出文件（校验可见性 + 记录下载留痕，gateway 凭路径流式下发）
func (s *server) GetExportTaskFile(ctx stdctx.Context, req *order.GetExportTaskFileReq) (*order.GetExportTaskFileResp, error) {
	path, name, err := s.exportTaskService.GetExportTaskFile(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &order.GetExportTaskFileResp{FilePath: path, FileName: name}, nil
}

// DeleteExportTask 删除导出任务（仅超管，文件一并删除）
func (s *server) DeleteExportTask(ctx stdctx.Context, req *order.DeleteExportTaskReq) (*common.CommonResponse, error) {
	if err := s.exportTaskService.DeleteExportTask(ctx, req.Id); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// CountUndownloaded 未下载任务数（下载中心角标）
func (s *server) CountUndownloaded(ctx stdctx.Context, req *order.CountUndownloadedReq) (*order.CountUndownloadedResp, error) {
	count, err := s.exportTaskService.CountUndownloaded(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &order.CountUndownloadedResp{Count: count}, nil
}

// ApplyRefund 申请退款
func (s *server) ApplyRefund(ctx stdctx.Context, req *order.ApplyRefundReq) (*order.RefundInfo, error) {
	refund, err := s.refundService.ApplyRefund(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return ToRefundInfo(refund), nil
}

// AdminCreateRefund 后台代客退款（发起即执行，免审核）
func (s *server) AdminCreateRefund(ctx stdctx.Context, req *order.AdminCreateRefundReq) (*order.RefundInfo, error) {
	refund, err := s.refundService.AdminCreateRefund(ctx, req)
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

// ResetRefundRetry 恢复卡在退款中的退款单自动收敛（归零两侧重试预算，不触渠道）
func (s *server) ResetRefundRetry(ctx stdctx.Context, req *order.ResetRefundRetryReq) (*common.CommonResponse, error) {
	if err := s.refundService.ResetRefundRetry(ctx, req); err != nil {
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
	info := &order.OrderInfo{
		Id:               o.ID,
		OrderNo:          o.OrderNo,
		CommunityId:      o.CommunityID,
		UserId:           o.UserID,
		Status:           o.Status,
		TotalAmount:      o.TotalAmount,
		DiscountAmount:   o.DiscountAmount,
		DeliveryFee:      o.DeliveryFee,
		PayAmount:        o.PayAmount,
		DeliveryType:     o.DeliveryType,
		DeliveryTime:     o.DeliveryTime,
		ContactName:      o.ContactName,
		ContactPhone:     o.ContactPhone,
		ContactAddress:   o.ContactAddress,
		Remark:           o.Remark,
		PayTime:          ptrString(o.PayTime),
		DeliverTime:      ptrString(o.DeliverTime),
		CompleteTime:     ptrString(o.CompleteTime),
		CancelTime:       ptrString(o.CancelTime),
		CancelReason:     o.CancelReason,
		CreatedAt:        o.CreatedAt,
		PickupCode:       ptrString(o.PickupCode),
		ExpressCompany:   o.ExpressCompany,
		ExpressNo:        o.ExpressNo,
		Source:           o.Source,
		OperatorId:       o.OperatorID,
		OperatorName:     o.OperatorName,
		ManualDiscount:   o.ManualDiscount,
		ShiftId:          o.ShiftID,
		DeliveryUserId:   o.DeliveryUserID,
		DeliveryUserName: o.DeliveryUserName,
		// 用户软删标记透出，供后台列表/详情显示「已被用户删除」并给出恢复入口
		// （网关侧为手工装配 gin.H，不存在 omitempty 吞零值的问题）
		UserDeleted: o.UserDeleted,
		ItemCount:   o.ItemCount,
	}
	// 送达签收凭证：DB 存 JSON 数组字符串，解析失败静默为空
	if o.DeliveryProofImages != "" {
		var imgs []string
		if err := json.Unmarshal([]byte(o.DeliveryProofImages), &imgs); err == nil {
			info.DeliveryProofImages = imgs
		}
	}
	return info
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
		Id:            item.ID,
		ProductId:     item.ProductID,
		SkuId:         item.SkuID,
		ProductName:   item.ProductName,
		SpecName:      item.SpecName,
		ProductImage:  item.ProductImage,
		Price:         item.Price,
		Quantity:      item.Quantity,
		TotalPrice:    item.TotalPrice,
		OriginalPrice: item.OriginalPrice,
		PriceModified: item.PriceModified == 1,
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
		// 所属订单商品行数（由 GetOrder/ListRefunds 查询侧回填；新建退款单响应不携带，前端按需刷新详情）
		OrderItemCount: r.OrderItemCount,
		Items:          make([]*order.RefundItemInfo, 0, len(r.Items)),
		Logs:           make([]*order.RefundLogInfo, 0, len(r.Logs)),
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
	return &order.RefundConfig{
		RefundFreightPolicy:  row.RefundFreightPolicy,
		CompletedRefundHours: row.CompletedRefundHours,
	}, nil
}

// SaveRefundConfig 保存退款配置
func (s *server) SaveRefundConfig(ctx stdctx.Context, req *order.SaveRefundConfigReq) (*common.CommonResponse, error) {
	if err := s.refundService.SaveRefundConfig(ctx, req.RefundFreightPolicy, req.CompletedRefundHours); err != nil {
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
