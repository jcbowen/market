package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// StockCheckService 盘点业务服务
type StockCheckService struct {
	db *mysql.Instance
}

// NewStockCheckService 创建盘点业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *StockCheckService: 盘点业务服务实例
func NewStockCheckService(db *mysql.Instance) *StockCheckService {
	return &StockCheckService{db: db}
}

// CreateStockCheck 创建盘点单
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.StockCheck: 盘点单模型
//   - error: 错误信息
func (s *StockCheckService) CreateStockCheck(ctx context.Context, req *product.CreateStockCheckReq) (*model.StockCheck, error) {
	check := &model.StockCheck{
		CheckNo:     generateCheckNo(),
		CommunityID: req.CommunityId,
		OperatorID:  req.OperatorId,
		Status:      1,
		Remark:      req.Remark,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(check).Error; err != nil {
		return nil, err
	}
	return check, nil
}

// SubmitStockCheck 提交盘点单
// 参数：
//   - ctx: 上下文
//   - req: 提交请求
//
// 返回值：
//   - error: 错误信息
func (s *StockCheckService) SubmitStockCheck(ctx context.Context, req *product.SubmitStockCheckReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var check model.StockCheck
		if err := tx.First(&check, req.Id).Error; err != nil {
			return err
		}
		// 社区隔离强校验：管理端提交必传 community_id（>0 时校验盘点单归属社区），
		// 与订单/退款族同口径；内部调用传 0 跳过
		if req.CommunityId > 0 && check.CommunityID != req.CommunityId {
			return bizerr.New(codes.PermissionDenied, "无权操作其他社区的盘点单")
		}
		if check.Status != 1 {
			return fmt.Errorf("盘点单状态不允许提交")
		}

		totalItems := len(req.Items)
		diffItems := 0
		now := time.Now().Format("2006-01-02 15:04:05")

		// 明细 SKU 按 sku_id 升序统一加锁：并发盘点/入库若按各自入参顺序对多行
		// FOR UPDATE，会互相持有对方等待的行锁形成死锁；一次 IN 查询同时把
		// N 次加锁往返压成 1 次。归属社区取盘点单自身，杜绝把其他社区的 SKU
		// 塞进本单以改写其库存
		skuIDs := make([]int64, 0, len(req.Items))
		for _, item := range req.Items {
			skuIDs = append(skuIDs, item.SkuId)
		}
		locked, err := lockSkus(tx, skuIDs, check.CommunityID)
		if err != nil {
			return err
		}

		for _, item := range req.Items {
			sku := locked[item.SkuId]
			before := sku.Stock
			diff := item.ActualStock - before
			var batchID int64
			if diff != 0 {
				diffItems++
				if diff < 0 {
					// 盘亏：按 FEFO 扣减批次
					takes, err := deductBatches(tx, sku, -diff, now)
					if err != nil {
						return err
					}
					if len(takes) > 0 {
						batchID = takes[0].Batch.ID
					}
				} else {
					// 盘盈：生成盘盈批次，单位成本取当前移动加权成本
					batch := &model.StockBatch{
						CommunityID: check.CommunityID,
						SkuID:       sku.ID,
						Quantity:    diff,
						UnitCost:    sku.CostPrice,
						Status:      1,
						SourceType:  3,
						RefNo:       check.CheckNo,
						CreatedAt:   now,
						UpdatedAt:   now,
					}
					if err := tx.Create(batch).Error; err != nil {
						return err
					}
					batchID = batch.ID
				}
				sku.Stock = item.ActualStock
				if err := tx.Model(&model.Sku{}).Where("id = ?", sku.ID).
					Update("stock", sku.Stock).Error; err != nil {
					return err
				}
				record := &model.StockRecord{
					SkuID:       sku.ID,
					CommunityID: check.CommunityID,
					Type:        3,
					Quantity:    diff,
					BeforeStock: before,
					AfterStock:  sku.Stock,
					BatchID:     batchID,
					OperatorID:  check.OperatorID,
					Remark:      "盘点调整",
					RefNo:       check.CheckNo,
					CreatedAt:   now,
				}
				if err := tx.Create(record).Error; err != nil {
					return err
				}
			}

			checkItem := &model.StockCheckItem{
				CheckID:      check.ID,
				SkuID:        sku.ID,
				SystemStock:  before,
				ActualStock:  item.ActualStock,
				DiffQuantity: diff,
				CreatedAt:    now,
			}
			if err := tx.Create(checkItem).Error; err != nil {
				return err
			}
		}

		check.Status = 2
		check.TotalItems = int32(totalItems)
		check.DiffItems = int32(diffItems)
		check.UpdatedAt = now
		return tx.Save(&check).Error
	})
}

// GetStockCheck 查询盘点单详情（含明细）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *product.StockCheckDetail: 盘点单详情
//   - error: 错误信息
func (s *StockCheckService) GetStockCheck(ctx context.Context, req *product.GetStockCheckReq) (*product.StockCheckDetail, error) {
	var check model.StockCheck
	if err := s.db.GetDb().WithContext(ctx).First(&check, req.Id).Error; err != nil {
		return nil, err
	}
	// 社区隔离强校验：与 SubmitStockCheck 同口径，防止跨社区读取盘点单明细
	if req.CommunityId > 0 && check.CommunityID != req.CommunityId {
		return nil, bizerr.New(codes.PermissionDenied, "无权查看其他社区的盘点单")
	}

	type itemRow struct {
		ID           int64
		SkuID        int64
		SkuCode      string
		ProductName  string
		SpecName     string
		SystemStock  int32
		ActualStock  int32
		DiffQuantity int32
		CreatedAt    string
	}
	var rows []itemRow
	err := s.db.GetDb().WithContext(ctx).Raw(`
		SELECT
			sci.id,
			sci.sku_id,
			s.sku_code,
			p.name AS product_name,
			s.spec_name,
			sci.system_stock,
			sci.actual_stock,
			sci.diff_quantity,
			sci.created_at
		FROM stock_check_items sci
		INNER JOIN product_skus s ON s.id = sci.sku_id
		INNER JOIN products p ON p.id = s.product_id
		WHERE sci.check_id = ?
		ORDER BY sci.id ASC
	`, req.Id).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	items := make([]*product.StockCheckDetailItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, &product.StockCheckDetailItem{
			Id:           r.ID,
			SkuId:        r.SkuID,
			SkuCode:      r.SkuCode,
			ProductName:  r.ProductName,
			SpecName:     r.SpecName,
			SystemStock:  r.SystemStock,
			ActualStock:  r.ActualStock,
			DiffQuantity: r.DiffQuantity,
			CreatedAt:    r.CreatedAt,
		})
	}

	return &product.StockCheckDetail{
		Info:  ToStockCheckInfo(&check),
		Items: items,
	}, nil
}

// ListStockChecks 查询盘点单列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.StockCheck: 盘点单列表
//   - int: 总条数
//   - error: 错误信息
func (s *StockCheckService) ListStockChecks(ctx context.Context, req *product.ListStockChecksReq) ([]*model.StockCheck, int, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.StockCheck{})
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var list []*model.StockCheck
	err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// ToStockCheckInfo 将盘点单模型转换为 proto 信息
// 参数：
//   - c: 盘点单模型
//
// 返回值：
//   - *product.StockCheckInfo: proto 盘点单信息
func ToStockCheckInfo(c *model.StockCheck) *product.StockCheckInfo {
	if c == nil {
		return nil
	}
	return &product.StockCheckInfo{
		Id:          c.ID,
		CheckNo:     c.CheckNo,
		Status:      c.Status,
		TotalItems:  c.TotalItems,
		DiffItems:   c.DiffItems,
		Remark:      c.Remark,
		CreatedAt:   c.CreatedAt,
		CommunityId: c.CommunityID,
	}
}

// generateCheckNo 生成盘点单号
// 返回值：
//   - string: 盘点单号
func generateCheckNo() string {
	return "PD" + time.Now().Format("20060102150405") + fmt.Sprintf("%04d", time.Now().Nanosecond()/1e5)
}
