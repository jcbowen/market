package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

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
		if check.Status != 1 {
			return fmt.Errorf("盘点单状态不允许提交")
		}

		totalItems := len(req.Items)
		diffItems := 0
		now := time.Now().Format("2006-01-02 15:04:05")

		for _, item := range req.Items {
			var sku model.Sku
			if err := tx.Clauses(lockClause()).First(&sku, item.SkuId).Error; err != nil {
				return err
			}
			before := sku.Stock
			diff := item.ActualStock - before
			if diff != 0 {
				diffItems++
				sku.Stock = item.ActualStock
				if err := tx.Save(&sku).Error; err != nil {
					return err
				}
				record := &model.StockRecord{
					SkuID:       sku.ID,
					CommunityID: check.CommunityID,
					Type:        3,
					Quantity:    diff,
					BeforeStock: before,
					AfterStock:  sku.Stock,
					OperatorID:  check.OperatorID,
					Remark:      "盘点调整",
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
		Id:         c.ID,
		CheckNo:    c.CheckNo,
		Status:     c.Status,
		TotalItems: c.TotalItems,
		DiffItems:  c.DiffItems,
		Remark:     c.Remark,
		CreatedAt:  c.CreatedAt,
	}
}

// generateCheckNo 生成盘点单号
// 返回值：
//   - string: 盘点单号
func generateCheckNo() string {
	return "PD" + time.Now().Format("20060102150405") + fmt.Sprintf("%04d", time.Now().Nanosecond()/1e5)
}
