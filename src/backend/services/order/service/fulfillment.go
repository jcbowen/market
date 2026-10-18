package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

// ============================================================================
// 履约工作台（B）：后台指派配送 + C 端工作台查单/送货状态流转
// 自提核销复用 VerifyPickup（operator_type=staff）；决策点②送达强制拍照留证
// ============================================================================

// AssignDelivery 后台指派/取消指派配送员
// 校验：配送单、状态在待处理区（2/3，配送中/完成后不可改指派）、社区归属；
// 指派时校验目标用户是该社区履约人员。写状态日志留痕。
// 参数：
//   - ctx: 上下文
//   - req: 指派请求（DeliveryUserId=0 表示取消指派）
//
// 返回值：
//   - error: 错误信息
func (s *OrderService) AssignDelivery(ctx context.Context, req *order.AssignDeliveryReq) error {
	var o model.Order
	if err := s.db.GetDb().WithContext(ctx).First(&o, req.OrderId).Error; err != nil {
		return err
	}
	if req.CommunityId > 0 && o.CommunityID != req.CommunityId {
		return bizerr.New(codes.PermissionDenied, "无权操作其他社区的订单")
	}
	if o.DeliveryType != 2 {
		return bizerr.New(codes.FailedPrecondition, "仅配送订单支持指派履约人员")
	}
	if o.Status != 2 && o.Status != 3 {
		return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("订单当前状态不允许指派: %d", o.Status))
	}

	// 指派时校验目标用户是本社区履约人员
	if req.DeliveryUserId > 0 {
		check, err := grpcclient.UserClient.CheckFulfiller(ctx, &user.CheckFulfillerReq{
			UserId:      req.DeliveryUserId,
			CommunityId: o.CommunityID,
		})
		if err != nil {
			return err
		}
		if check == nil || !check.IsFulfiller {
			return bizerr.New(codes.FailedPrecondition, "该用户不是本社区履约人员，请先在员工管理中为其绑定会员")
		}
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	res := s.db.GetDb().WithContext(ctx).
		Model(&model.Order{}).
		Where("id = ? AND status IN ?", o.ID, []int32{2, 3}).
		Updates(map[string]interface{}{
			"delivery_user_id":   req.DeliveryUserId,
			"delivery_user_name": req.DeliveryUserName,
			"updated_at":         now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
	}

	remark := "取消配送指派"
	if req.DeliveryUserId > 0 {
		remark = fmt.Sprintf("指派履约人员配送（%s）", req.DeliveryUserName)
	}
	return s.createStatusLogTx(s.db.GetDb().WithContext(ctx), o.ID, o.Status, o.Status, req.OperatorType, req.OperatorId, remark)
}

// ListFulfillmentOrders 工作台查单
// 送货单（order_type=2）：按指派履约人员过滤；自提单（order_type=1）：本社区全部（任何履约人员可见可核销）。
// 状态组：0=进行中（待处理 2/3 + 配送中 4） 5=已完成/已核销（status=5）
// 参数：
//   - ctx: 上下文
//   - req: 查单请求
//
// 返回值：
//   - []*model.Order: 订单列表
//   - int64: 总数
//   - error: 错误信息
func (s *OrderService) ListFulfillmentOrders(ctx context.Context, req *order.ListFulfillmentOrdersReq) ([]*model.Order, int64, error) {
	if req.CommunityId <= 0 {
		return nil, 0, bizerr.New(codes.InvalidArgument, "缺少社区语境")
	}
	if req.OrderType != 1 && req.OrderType != 2 {
		return nil, 0, bizerr.New(codes.InvalidArgument, "参数错误：order_type 须为 1(自提) 或 2(送货)")
	}

	query := s.db.GetDb().WithContext(ctx).
		Model(&model.Order{}).
		Where("community_id = ? AND delivery_type = ?", req.CommunityId, req.OrderType)

	if req.OrderType == 2 {
		// 送货单只看指派给自己的
		if req.DeliveryUserId <= 0 {
			return nil, 0, bizerr.New(codes.PermissionDenied, "缺少履约人员身份")
		}
		query = query.Where("delivery_user_id = ?", req.DeliveryUserId)
	}

	switch req.StatusGroup {
	case 5:
		query = query.Where("status = 5")
	default:
		query = query.Where("status IN ?", []int32{2, 3, 4})
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, pageSize := int(req.Pagination.GetPage()), int(req.Pagination.GetPageSize())
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 10
	}
	var list []*model.Order
	if err := query.
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// StartDelivery 履约人员开始配送：status 2/3 → 4
// 校验：本社区配送单、操作者为订单指派人（未指派/他人指派均拒绝）。
// 不写物流公司/单号（社区自配送无物流单号，绕开后台 ShipOrder 的强制校验）。
// 参数：
//   - ctx: 上下文
//   - req: 开始配送请求
//
// 返回值：
//   - error: 错误信息
func (s *OrderService) StartDelivery(ctx context.Context, req *order.StartDeliveryReq) error {
	if req.DeliveryUserId <= 0 {
		return bizerr.New(codes.PermissionDenied, "缺少履约人员身份")
	}
	var o model.Order
	if err := s.db.GetDb().WithContext(ctx).First(&o, req.OrderId).Error; err != nil {
		return err
	}
	if req.CommunityId > 0 && o.CommunityID != req.CommunityId {
		return bizerr.New(codes.PermissionDenied, "无权操作其他社区的订单")
	}
	if o.DeliveryType != 2 {
		return bizerr.New(codes.FailedPrecondition, "非配送订单，无需配送")
	}
	if o.DeliveryUserID != req.DeliveryUserId {
		return bizerr.New(codes.PermissionDenied, "该订单未指派给您配送")
	}
	if o.Status != 2 && o.Status != 3 {
		return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("订单状态不允许开始配送: %d", o.Status))
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	res := s.db.GetDb().WithContext(ctx).
		Model(&model.Order{}).
		Where("id = ? AND status IN ?", o.ID, []int32{2, 3}).
		Updates(map[string]interface{}{
			"status":       int32(4),
			"deliver_time": &now,
			"updated_at":   now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
	}
	return s.createStatusLogTx(s.db.GetDb().WithContext(ctx), o.ID, o.Status, 4, req.OperatorType, req.OperatorId, "履约人员开始配送")
}

// CompleteDelivery 履约人员送达完成：status 4 → 5，强制签收照片留证（决策点②）
// 校验：本社区配送单、操作者为订单指派人、状态为配送中、照片 1~N 张。
// 参数：
//   - ctx: 上下文
//   - req: 送达请求
//
// 返回值：
//   - error: 错误信息
func (s *OrderService) CompleteDelivery(ctx context.Context, req *order.CompleteDeliveryReq) error {
	if req.DeliveryUserId <= 0 {
		return bizerr.New(codes.PermissionDenied, "缺少履约人员身份")
	}
	if len(req.ProofImages) == 0 {
		return bizerr.New(codes.InvalidArgument, "请上传签收照片后再完成送达")
	}
	var o model.Order
	if err := s.db.GetDb().WithContext(ctx).First(&o, req.OrderId).Error; err != nil {
		return err
	}
	if req.CommunityId > 0 && o.CommunityID != req.CommunityId {
		return bizerr.New(codes.PermissionDenied, "无权操作其他社区的订单")
	}
	if o.DeliveryType != 2 {
		return bizerr.New(codes.FailedPrecondition, "非配送订单")
	}
	if o.DeliveryUserID != req.DeliveryUserId {
		return bizerr.New(codes.PermissionDenied, "该订单未指派给您配送")
	}
	if o.Status != 4 {
		return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("订单状态不允许送达完成: %d", o.Status))
	}

	proofJSON, err := json.Marshal(req.ProofImages)
	if err != nil {
		return fmt.Errorf("签收照片序列化失败: %w", err)
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	res := s.db.GetDb().WithContext(ctx).
		Model(&model.Order{}).
		Where("id = ? AND status = ?", o.ID, int32(4)).
		Updates(map[string]interface{}{
			"status":                 int32(5),
			"complete_time":          &now,
			"delivery_proof_images":  string(proofJSON),
			"updated_at":             now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return bizerr.New(codes.FailedPrecondition, "订单状态已变更，请刷新后重试")
	}
	return s.createStatusLogTx(s.db.GetDb().WithContext(ctx), o.ID, 4, 5, req.OperatorType, req.OperatorId, "送达完成（已上传签收照片）")
}
