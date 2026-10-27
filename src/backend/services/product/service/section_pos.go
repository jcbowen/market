package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	orderModel "github.com/jcbowen/market/src/backend/services/order/model"
)

// 本文件是数据包的「收银台样本」段（scope=community, init_only=true）：
// 已交班历史班次（绝不预置进行中班次，避免占用开工门禁）+ 收银台挂单草稿。
// 订单样本（orders 段）的收银单按班次 key 映射挂到 posShift:{key} 上，
// 因此本段先于订单段导入。

// SeedPosShift 演示收银班次（已结束态；统计快照为交班冻结 JSON，snake_case 与
// EndShift 的 json.Marshal 输出一致，解析不出会静默丢字段）
type SeedPosShift struct {
	Key            string    `json:"key"`
	CommunityKey   string    `json:"community_key"`
	OperatorKey    string    `json:"operator_key"`
	OperatorName   string    `json:"operator_name"`
	StartTime      *SeedTime `json:"start_time"`
	EndTime        *SeedTime `json:"end_time"`
	Status         int32     `json:"status"` // 1进行中 2已结束（种子恒为 2）
	Note           string    `json:"note"`
	StatisticsJSON string    `json:"statistics_json"`
}

// SeedPosHeldItem 演示挂单明细（items_json 快照，恢复时由收银台 service 反序列化）
type SeedPosHeldItem struct {
	SkuKey       string  `json:"sku_key"`
	ProductName  string  `json:"product_name"`
	SpecName     string  `json:"spec_name"`
	ProductImage string  `json:"product_image"`
	Price        float64 `json:"price"`
	Quantity     int32   `json:"quantity"`
}

// SeedPosHeldOrder 演示收银台挂单（未成单草稿：不生成订单、不扣库存、不占班次）
type SeedPosHeldOrder struct {
	Key          string            `json:"key"`
	CommunityKey string            `json:"community_key"`
	OperatorKey  string            `json:"operator_key"`
	OperatorName string            `json:"operator_name"`
	UserKey      string            `json:"user_key"`
	UserName     string            `json:"user_name"`
	Items        []SeedPosHeldItem `json:"items"`
	TotalAmount  float64           `json:"total_amount"`
	Remark       string            `json:"remark"`
	CreatedAt    *SeedTime         `json:"created_at"`
}

// posSection 收银台样本
type posSection struct {
	svc *DataImportService
}

func (s *posSection) Key() string    { return "pos" }
func (s *posSection) Name() string   { return "收银台样本" }
func (s *posSection) Scope() string  { return SectionScopeCommunity }
func (s *posSection) InitOnly() bool { return true }
func (s *posSection) Description() string {
	return "已交班历史班次与收银台挂单草稿，仅随初始化导入"
}

func (s *posSection) ItemCount(pkg *SeedPackage) int32 {
	return int32(len(pkg.PosShifts) + len(pkg.PosHeldOrders))
}

func (s *posSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
}

// Import 班次与挂单逐条导入；分别按「操作员+开工时间」「备注+合计」幂等
func (s *posSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	now := time.Now()
	for i := range pkg.PosShifts {
		sh := &pkg.PosShifts[i]
		if !isImportable(req, sh.Key) {
			continue
		}
		if sh.CommunityKey == "" || sh.CommunityKey != req.CommunityKey {
			continue
		}
		operatorID, err := s.svc.resolveSeedOperatorRef(ctx, pkg, req.Refs, "admin", sh.OperatorKey)
		if err != nil {
			appendImportResult(items, s.Key(), sh.Key, sh.OperatorName, "failed", "操作员解析失败："+err.Error())
			continue
		}
		startTime := sh.StartTime.Resolve(now)
		endTime := sh.EndTime.Resolve(now)
		var count int64
		s.svc.db.GetDb().WithContext(ctx).Model(&orderModel.PosShift{}).
			Where("community_id = ? AND operator_id = ? AND start_time = ?",
				req.CommunityID, operatorID, startTime).Count(&count)
		if count > 0 {
			req.Refs.Set("posShift:"+sh.Key, 0) // 占位避免下游引用报缺失（ID 由兜底查询补）
			s.registerExistingShift(ctx, req, sh, operatorID, startTime)
			appendImportResult(items, s.Key(), sh.Key, sh.OperatorName, "skipped", "班次已存在")
			continue
		}
		row := &orderModel.PosShift{
			CommunityID:    req.CommunityID,
			OperatorID:     operatorID,
			OperatorName:   sh.OperatorName,
			StartTime:      startTime,
			EndTime:        sh.EndTime.ResolvePtr(now),
			Status:         sh.Status,
			Note:           sh.Note,
			StatisticsJSON: sh.StatisticsJSON,
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Create(row).Error; err != nil {
			appendImportResult(items, s.Key(), sh.Key, sh.OperatorName, "failed", "班次创建失败："+err.Error())
			continue
		}
		backfillTimestamps(s.svc.db.GetDb(), &orderModel.PosShift{}, row.ID, startTime, endTime)
		req.Refs.Set("posShift:"+sh.Key, row.ID)
		appendImportResult(items, s.Key(), sh.Key, sh.OperatorName, "created", "历史班次已导入")
		created++
	}

	for i := range pkg.PosHeldOrders {
		h := &pkg.PosHeldOrders[i]
		if !isImportable(req, h.Key) {
			continue
		}
		if h.CommunityKey == "" || h.CommunityKey != req.CommunityKey {
			continue
		}
		itemsJSON, err := s.heldItemsJSON(pkg, req, h)
		if err != nil {
			appendImportResult(items, s.Key(), h.Key, h.UserName, "failed", "挂单明细解析失败："+err.Error())
			continue
		}
		var count int64
		s.svc.db.GetDb().WithContext(ctx).Model(&orderModel.PosHeldOrder{}).
			Where("community_id = ? AND remark = ? AND total_amount = ?",
				req.CommunityID, h.Remark, h.TotalAmount).Count(&count)
		if count > 0 {
			appendImportResult(items, s.Key(), h.Key, h.UserName, "skipped", "挂单已存在")
			continue
		}
		operatorID, _ := req.Refs.Get(h.OperatorKey)
		userID, _ := req.Refs.Get(h.UserKey)
		heldCreated := h.CreatedAt.Resolve(now)
		row := &orderModel.PosHeldOrder{
			CommunityID:  req.CommunityID,
			OperatorID:   operatorID,
			OperatorName: h.OperatorName,
			UserID:       userID,
			UserName:     h.UserName,
			ItemsJSON:    itemsJSON,
			ItemCount:    int32(len(h.Items)),
			TotalAmount:  h.TotalAmount,
			Remark:       h.Remark,
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Create(row).Error; err != nil {
			appendImportResult(items, s.Key(), h.Key, h.UserName, "failed", "挂单创建失败："+err.Error())
			continue
		}
		backfillTimestamps(s.svc.db.GetDb(), &orderModel.PosHeldOrder{}, row.ID, heldCreated, "")
		appendImportResult(items, s.Key(), h.Key, h.UserName, "created", "挂单样本已导入")
		created++
	}
	return created
}

// registerExistingShift 兜底按唯一条件查出已存在班次的 ID 并注册映射
func (s *posSection) registerExistingShift(ctx context.Context, req *ImportRequest, sh *SeedPosShift, operatorID int64, startTime string) {
	var row orderModel.PosShift
	if err := s.svc.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND operator_id = ? AND start_time = ?",
			req.CommunityID, operatorID, startTime).First(&row).Error; err == nil {
		req.Refs.Set("posShift:"+sh.Key, row.ID)
	}
}

// heldItemsJSON 生成挂单明细快照 JSON（snake_case，与 proto PosHeldOrderItem 的
// Go json tag 一致；sku_id 经目录段映射解析为真实 ID）
func (s *posSection) heldItemsJSON(pkg *SeedPackage, req *ImportRequest, h *SeedPosHeldOrder) (string, error) {
	type heldItemJSON struct {
		SkuID        int64   `json:"sku_id"`
		Quantity     int32   `json:"quantity"`
		ProductName  string  `json:"product_name"`
		SpecName     string  `json:"spec_name"`
		ProductImage string  `json:"product_image"`
		Price        float64 `json:"price"`
	}
	rows := make([]heldItemJSON, 0, len(h.Items))
	for i := range h.Items {
		it := &h.Items[i]
		skuID, _ := req.Refs.Get(it.SkuKey)
		rows = append(rows, heldItemJSON{
			SkuID:        skuID,
			Quantity:     it.Quantity,
			ProductName:  it.ProductName,
			SpecName:     it.SpecName,
			ProductImage: it.ProductImage,
			Price:        it.Price,
		})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
