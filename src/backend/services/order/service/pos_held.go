package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/attachmenturl"
	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 收银台挂单业务（G 收银台代客下单，决策点⑥）
 * 挂单是「未成单」草稿：顾客商品添加完毕但暂不结账时暂存工作区，
 * 不生成正式订单、不占库存；结账成功或放弃后删除。
 * 存储走后端 DB（pos_held_orders），跨设备/跨收银员场景不丢单。
 */

// PosHeldService 收银台挂单业务服务
type PosHeldService struct {
	db *mysql.Instance
	// attachmentLoader 附件配置缓存读取器：挂单商品图快照入库归一与出库拼完整地址共用
	attachmentLoader *attachmenturl.Loader
}

// NewPosHeldService 创建收银台挂单业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *PosHeldService: 挂单业务服务实例
func NewPosHeldService(db *mysql.Instance) *PosHeldService {
	return &PosHeldService{db: db, attachmentLoader: attachmenturl.NewLoader(db.GetDb())}
}

// Create 创建挂单：商品明细快照落库，合计金额按快照价计算（结账时以服务端实时计价为准）
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *order.PosHeldOrderInfo: 挂单信息
//   - error: 错误信息
func (s *PosHeldService) Create(ctx context.Context, req *order.CreatePosHeldOrderReq) (*order.PosHeldOrderInfo, error) {
	if req.CommunityId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少社区信息")
	}
	if len(req.Items) == 0 {
		return nil, bizerr.New(codes.InvalidArgument, "挂单商品不能为空")
	}

	// 商品图快照入库归一为附件相对路径（common/attachmenturl 口径），出库按当前配置重拼
	// proto 消息含内部状态锁，必须逐字段构造新对象，不能值拷贝
	items := make([]*order.PosHeldOrderItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, &order.PosHeldOrderItem{
			SkuId:        item.SkuId,
			Quantity:     item.Quantity,
			ProductName:  item.ProductName,
			SpecName:     item.SpecName,
			ProductImage: s.attachmentLoader.SourcePath(ctx, item.ProductImage),
			Price:        item.Price,
		})
	}

	raw, err := json.Marshal(items)
	if err != nil {
		return nil, bizerr.New(codes.Internal, "挂单明细序列化失败")
	}
	req.Items = items

	var total float64
	now := time.Now().Format("2006-01-02 15:04:05")
	m := &model.PosHeldOrder{
		CommunityID:  req.CommunityId,
		OperatorID:   req.OperatorId,
		OperatorName: req.OperatorName,
		UserID:       req.UserId,
		UserName:     req.UserName,
		ItemsJSON:    string(raw),
		ItemCount:    int32(len(req.Items)),
		Remark:       req.Remark,
		CreatedAt:    now,
	}
	for _, item := range req.Items {
		total += item.Price * float64(item.Quantity)
	}
	m.TotalAmount = total

	if err := s.db.GetDb().WithContext(ctx).Create(m).Error; err != nil {
		return nil, err
	}
	info := ToPosHeldOrderInfo(m)
	s.restoreItemImages(ctx, info.Items)
	return info, nil
}

// List 挂单列表：按社区语境（必选）+ 可选收银员过滤，最新在前
// 参数：
//   - ctx: 上下文
//   - req: 列表请求
//
// 返回值：
//   - []*order.PosHeldOrderInfo: 挂单列表
//   - error: 错误信息
func (s *PosHeldService) List(ctx context.Context, req *order.ListPosHeldOrdersReq) ([]*order.PosHeldOrderInfo, error) {
	if req.CommunityId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少社区信息")
	}
	db := s.db.GetDb().WithContext(ctx).Model(&model.PosHeldOrder{}).Where("community_id = ?", req.CommunityId)
	if req.OperatorId > 0 {
		db = db.Where("operator_id = ?", req.OperatorId)
	}

	var list []*model.PosHeldOrder
	if err := db.Order("id desc").Limit(100).Find(&list).Error; err != nil {
		return nil, err
	}

	resp := make([]*order.PosHeldOrderInfo, 0, len(list))
	for _, m := range list {
		info := ToPosHeldOrderInfo(m)
		s.restoreItemImages(ctx, info.Items)
		resp = append(resp, info)
	}
	return resp, nil
}

// restoreItemImages 就地还原挂单明细商品图快照的完整访问地址
// 快照入库为相对路径（存量完整 URL 自动归一），出库按当前附件配置重拼
//
// 参数：
//   - ctx: 上下文
//   - items: 挂单明细
func (s *PosHeldService) restoreItemImages(ctx context.Context, items []*order.PosHeldOrderItem) {
	for _, item := range items {
		item.ProductImage = s.attachmentLoader.RestoreURL(ctx, item.ProductImage)
	}
}

// Delete 删除挂单（恢复结账/顾客放弃后调用）；community_id>0 时校验归属防跨社区删除
// 参数：
//   - ctx: 上下文
//   - req: 删除请求
//
// 返回值：
//   - error: 错误信息
func (s *PosHeldService) Delete(ctx context.Context, req *order.DeletePosHeldOrderReq) error {
	if req.Id <= 0 {
		return bizerr.New(codes.InvalidArgument, "参数错误")
	}
	db := s.db.GetDb().WithContext(ctx).Where("id = ?", req.Id)
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	res := db.Delete(&model.PosHeldOrder{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return bizerr.New(codes.NotFound, "挂单不存在或已删除")
	}
	return nil
}

// ToPosHeldOrderInfo 将挂单模型转换为 proto 信息（含明细 JSON 反序列化，失败按空明细兜底）
// 参数：
//   - m: 挂单模型
//
// 返回值：
//   - *order.PosHeldOrderInfo: proto 挂单信息
func ToPosHeldOrderInfo(m *model.PosHeldOrder) *order.PosHeldOrderInfo {
	if m == nil {
		return nil
	}
	var items []*order.PosHeldOrderItem
	if m.ItemsJSON != "" {
		if err := json.Unmarshal([]byte(m.ItemsJSON), &items); err != nil {
			log.Printf("挂单 %d 明细反序列化失败: %v", m.ID, err)
			items = nil
		}
	}
	if items == nil {
		items = make([]*order.PosHeldOrderItem, 0)
	}
	return &order.PosHeldOrderInfo{
		Id:           m.ID,
		CommunityId:  m.CommunityID,
		OperatorId:   m.OperatorID,
		OperatorName: m.OperatorName,
		UserId:       m.UserID,
		UserName:     m.UserName,
		Items:        items,
		ItemCount:    m.ItemCount,
		TotalAmount:  m.TotalAmount,
		Remark:       m.Remark,
		CreatedAt:    m.CreatedAt,
	}
}
