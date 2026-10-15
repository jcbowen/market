package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 收银台交班业务（班次体系）
 * 口径：
 * - 收银前必须开工：同操作员同社区存在进行中班次才可下单（网关 CreatePosOrder 前置校验）
 * - 收款统计：班内订单（orders.shift_id = 班次 ID）排除待支付(1)/已取消(6)，金额按支付单 pay_type 分组
 * - 退款统计：退款单关联订单归属班次（订单在哪个班次创建，退款计入该班次），
 *   状态取已同意(2)/退款中(4)/已退款(5)（凡实际执行过渠道退款的均计钱）
 * - 现金合计 = 现金收款 - 现金退款（钱箱应交金额）
 * - 进行中班次实时计算；交班时把统计冻结为 JSON 快照存 statistics_json
 */

// ShiftService 收银台交班业务服务
type ShiftService struct {
	db *mysql.Instance
}

// NewShiftService 创建收银台交班业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *ShiftService: 交班业务服务实例
func NewShiftService(db *mysql.Instance) *ShiftService {
	return &ShiftService{db: db}
}

// StartShift 开工：创建进行中班次
// 校验：同操作员同社区已有进行中班次时拒绝（防止重复开工导致统计分裂）
// 参数：
//   - ctx: 上下文
//   - req: 开工请求
//
// 返回值：
//   - *order.PosShiftInfo: 班次信息（含实时统计）
//   - error: 错误信息
func (s *ShiftService) StartShift(ctx context.Context, req *order.StartShiftReq) (*order.PosShiftInfo, error) {
	if req.CommunityId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少社区信息")
	}
	if req.OperatorId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少操作员信息")
	}

	var ongoing int64
	if err := s.db.GetDb().WithContext(ctx).Model(&model.PosShift{}).
		Where("community_id = ? AND operator_id = ? AND status = ?", req.CommunityId, req.OperatorId, 1).
		Count(&ongoing).Error; err != nil {
		return nil, err
	}
	if ongoing > 0 {
		return nil, bizerr.New(codes.FailedPrecondition, "您已有进行中的班次，无需重复开工")
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	m := &model.PosShift{
		CommunityID:  req.CommunityId,
		OperatorID:   req.OperatorId,
		OperatorName: req.OperatorName,
		StartTime:    now,
		Status:       1,
		Note:         req.Note,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(m).Error; err != nil {
		return nil, err
	}
	return s.toShiftInfo(ctx, m, true)
}

// GetCurrentShift 查询当前进行中班次（含实时统计）
// 无进行中班次时返回 id=0 的空信息（网关据此判定是否放行收银）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *order.PosShiftInfo: 班次信息（id=0 表示无进行中班次）
//   - error: 错误信息
func (s *ShiftService) GetCurrentShift(ctx context.Context, req *order.GetCurrentShiftReq) (*order.PosShiftInfo, error) {
	if req.CommunityId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少社区信息")
	}

	db := s.db.GetDb().WithContext(ctx).Where("community_id = ? AND status = ?", req.CommunityId, 1)
	if req.OperatorId > 0 {
		db = db.Where("operator_id = ?", req.OperatorId)
	}
	var m model.PosShift
	if err := db.Order("id desc").First(&m).Error; err != nil {
		// 无进行中班次：返回空信息（非错误），前端/网关按 id=0 判定
		return &order.PosShiftInfo{Id: 0}, nil
	}
	return s.toShiftInfo(ctx, &m, true)
}

// EndShift 交班：生成统计快照 → 置已结束 → 返回快照
// 参数：
//   - ctx: 上下文
//   - req: 交班请求
//
// 返回值：
//   - *order.PosShiftInfo: 已结束班次信息（含统计快照）
//   - error: 错误信息
func (s *ShiftService) EndShift(ctx context.Context, req *order.EndShiftReq) (*order.PosShiftInfo, error) {
	if req.CommunityId <= 0 || req.OperatorId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少社区或操作员信息")
	}

	var m model.PosShift
	if err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND operator_id = ? AND status = ?", req.CommunityId, req.OperatorId, 1).
		Order("id desc").First(&m).Error; err != nil {
		return nil, bizerr.New(codes.FailedPrecondition, "当前没有进行中的班次")
	}

	// 交班前实时统计冻结为快照
	stat, err := s.calcStatistics(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(stat)
	if err != nil {
		return nil, bizerr.New(codes.Internal, "统计快照序列化失败")
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	// 条件更新防并发：仅进行中班次可交班（重复提交时第二次不生效）
	res := s.db.GetDb().WithContext(ctx).Model(&model.PosShift{}).
		Where("id = ? AND status = ?", m.ID, 1).
		Updates(map[string]interface{}{
			"status":          int32(2),
			"end_time":        &now,
			"note":            req.Note,
			"statistics_json": string(raw),
			"updated_at":      now,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, bizerr.New(codes.FailedPrecondition, "班次已交班，请勿重复操作")
	}

	// 更新内存模型后返回快照（避免二次查询）
	m.Status = 2
	m.EndTime = &now
	m.Note = req.Note
	m.StatisticsJSON = string(raw)
	m.UpdatedAt = now
	info := ToPosShiftInfo(&m)
	info.Statistics = stat
	return info, nil
}

// ListShifts 交班记录分页（最新在前；operator_id=0 本店全部，>0 仅该操作员）
// 参数：
//   - ctx: 上下文
//   - req: 列表请求
//
// 返回值：
//   - *order.ListShiftsResp: 分页响应（列表按统计口径填充：已结束读快照，进行中实时算）
//   - error: 错误信息
func (s *ShiftService) ListShifts(ctx context.Context, req *order.ListShiftsReq) (*order.ListShiftsResp, error) {
	if req.CommunityId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少社区信息")
	}
	page, pageSize := int32(1), int32(10)
	if req.Pagination != nil {
		page = req.Pagination.Page
		pageSize = req.Pagination.PageSize
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.PosShift{}).Where("community_id = ?", req.CommunityId)
	if req.OperatorId > 0 {
		db = db.Where("operator_id = ?", req.OperatorId)
	}
	if req.StartTime != "" {
		db = db.Where("start_time >= ?", req.StartTime)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	var list []*model.PosShift
	if err := db.Order("id desc").Offset(int((page - 1) * pageSize)).Limit(int(pageSize)).Find(&list).Error; err != nil {
		return nil, err
	}

	respList := make([]*order.PosShiftInfo, 0, len(list))
	for _, m := range list {
		// 列表页统计恒填充：进行中班次实时算（供「当前班次」概览），已结束班次读交班快照
		// （此前误传 withStat=m.Status==1，导致已结束班次不带统计、前端金额全为 0）
		info, err := s.toShiftInfo(ctx, m, true)
		if err != nil {
			return nil, err
		}
		respList = append(respList, info)
	}
	return &order.ListShiftsResp{
		List: respList,
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     page,
			PageSize: pageSize,
		},
	}, nil
}

// calcStatistics 计算班次统计（收款/退款按支付方式分组）
// 收款：班内订单排除待支付(1)/已取消(6)，金额按支付单 pay_type 分组；
// 退款：退款单按关联订单归属班次，状态取已同意(2)/退款中(4)/已退款(5)
// 参数：
//   - ctx: 上下文
//   - shiftID: 班次 ID
//
// 返回值：
//   - *order.PosShiftStatistics: 统计结果
//   - error: 错误信息
func (s *ShiftService) calcStatistics(ctx context.Context, shiftID int64) (*order.PosShiftStatistics, error) {
	stat := &order.PosShiftStatistics{
		Income: make([]*order.PosShiftStatItem, 0),
		Refund: make([]*order.PosShiftStatItem, 0),
	}

	// 收款分组：班内有效订单按支付单 pay_type 汇总
	var incomeRows []struct {
		PayType    int32
		Amount     float64
		OrderCount int64
	}
	if err := s.db.GetDb().WithContext(ctx).
		Table("orders").
		Select("payments.pay_type AS pay_type, COALESCE(SUM(payments.amount),0) AS amount, COUNT(DISTINCT orders.id) AS order_count").
		Joins("JOIN payments ON payments.order_id = orders.id").
		Where("orders.shift_id = ? AND orders.status NOT IN ?", shiftID, []int32{1, 6}).
		Group("payments.pay_type").
		Scan(&incomeRows).Error; err != nil {
		return nil, err
	}
	for _, r := range incomeRows {
		stat.Income = append(stat.Income, &order.PosShiftStatItem{
			PayType: r.PayType, Amount: r.Amount, OrderCount: int32(r.OrderCount),
		})
		stat.TotalIncome += r.Amount
	}

	// 班内有效订单数（与收款口径一致）
	var orderCount int64
	if err := s.db.GetDb().WithContext(ctx).Model(&model.Order{}).
		Where("shift_id = ? AND status NOT IN ?", shiftID, []int32{1, 6}).
		Count(&orderCount).Error; err != nil {
		return nil, err
	}
	stat.OrderCount = int32(orderCount)

	// 退款分组：按订单归属班次口径，关联订单支付方式
	var refundRows []struct {
		PayType    int32
		Amount     float64
		OrderCount int64
	}
	if err := s.db.GetDb().WithContext(ctx).
		Table("refunds").
		Select("payments.pay_type AS pay_type, COALESCE(SUM(refunds.amount),0) AS amount, COUNT(DISTINCT refunds.id) AS order_count").
		Joins("JOIN orders ON orders.id = refunds.order_id").
		Joins("JOIN payments ON payments.order_id = orders.id").
		Where("orders.shift_id = ? AND refunds.status IN ?", shiftID, []int32{2, 4, 5}).
		Group("payments.pay_type").
		Scan(&refundRows).Error; err != nil {
		return nil, err
	}
	for _, r := range refundRows {
		stat.Refund = append(stat.Refund, &order.PosShiftStatItem{
			PayType: r.PayType, Amount: r.Amount, OrderCount: int32(r.OrderCount),
		})
		stat.TotalRefund += r.Amount
	}

	// 现金合计 = 现金收款 - 现金退款（钱箱应交金额；pay_type=4 现金）
	stat.CashTotal = stat.TotalIncome - stat.TotalRefund
	return stat, nil
}

// toShiftInfo 班次模型转 proto 信息
// 参数：
//   - ctx: 上下文（实时统计用）
//   - m: 班次模型
//   - withStat: 是否计算统计（已结束班次读快照，进行中实时计算）
//
// 返回值：
//   - *order.PosShiftInfo: proto 班次信息
//   - error: 错误信息
func (s *ShiftService) toShiftInfo(ctx context.Context, m *model.PosShift, withStat bool) (*order.PosShiftInfo, error) {
	info := ToPosShiftInfo(m)
	if !withStat {
		return info, nil
	}
	if m.Status == 2 && m.StatisticsJSON != "" {
		// 已结束班次：读交班时的统计快照
		var stat order.PosShiftStatistics
		if err := json.Unmarshal([]byte(m.StatisticsJSON), &stat); err != nil {
			return nil, fmt.Errorf("统计快照解析失败: %w", err)
		}
		info.Statistics = &stat
		return info, nil
	}
	// 进行中班次：实时计算
	stat, err := s.calcStatistics(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	info.Statistics = stat
	return info, nil
}

// normalizeTime 时间字符串归一化为 "2006-01-02 15:04:05"
// 背景：datetime 列读库扫进 string 字段时，database/sql 会输出 RFC3339（如 2026-10-16T06:26:14+08:00），
// 统一在出口处转换为前端友好的 "YYYY-MM-DD HH:mm:ss"；解析失败原样返回
// 参数：
//   - s: 原始时间字符串（"YYYY-MM-DD HH:mm:ss" 或 RFC3339）
//
// 返回值：
//   - string: 归一化后的时间字符串
func normalizeTime(s string) string {
	if s == "" {
		return s
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	return s
}

// ToPosShiftInfo 班次模型转 proto 信息（不含统计，统计由调用方按需填充）
// 时间字段统一归一化为 "YYYY-MM-DD HH:mm:ss"（datetime 读库经 database/sql 会变 RFC3339）
// 参数：
//   - m: 班次模型
//
// 返回值：
//   - *order.PosShiftInfo: proto 班次信息
func ToPosShiftInfo(m *model.PosShift) *order.PosShiftInfo {
	if m == nil {
		return nil
	}
	info := &order.PosShiftInfo{
		Id:           m.ID,
		CommunityId:  m.CommunityID,
		OperatorId:   m.OperatorID,
		OperatorName: m.OperatorName,
		StartTime:    normalizeTime(m.StartTime),
		Status:       m.Status,
		Note:         m.Note,
		CreatedAt:    normalizeTime(m.CreatedAt),
	}
	if m.EndTime != nil {
		info.EndTime = normalizeTime(*m.EndTime)
	}
	return info
}
