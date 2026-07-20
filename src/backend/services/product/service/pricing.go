package service

import (
	"context"
	"fmt"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// PricingService 社区定价业务服务
type PricingService struct {
	db *mysql.Instance
}

// NewPricingService 创建社区定价业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *PricingService: 定价业务服务实例
func NewPricingService(db *mysql.Instance) *PricingService {
	return &PricingService{db: db}
}

// SetCommunityPricing 设置 SKU 社区定价
// 参数：
//   - ctx: 上下文
//   - req: 定价请求
//
// 返回值：
//   - error: 错误信息
func (s *PricingService) SetCommunityPricing(ctx context.Context, req *product.SetPricingReq) error {
	var p model.CommunityPricing
	err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND sku_id = ?", req.CommunityId, req.SkuId).
		First(&p).Error

	if err == gorm.ErrRecordNotFound {
		p = model.CommunityPricing{
			CommunityID: req.CommunityId,
			SkuID:       req.SkuId,
			Price:       req.Price,
		}
		return s.db.GetDb().WithContext(ctx).Create(&p).Error
	}
	if err != nil {
		return err
	}

	p.Price = req.Price
	return s.db.GetDb().WithContext(ctx).Save(&p).Error
}

// ListCommunityPricing 查询 SKU 社区定价
// 参数：
//   - ctx: 上下文
//   - skuID: SKU ID
//   - communityID: 社区 ID，0 表示全部
//
// 返回值：
//   - []*model.CommunityPricing: 定价列表
//   - error: 错误信息
func (s *PricingService) ListCommunityPricing(ctx context.Context, skuID, communityID int64) ([]*model.CommunityPricing, error) {
	var list []*model.CommunityPricing
	db := s.db.GetDb().WithContext(ctx).Where("sku_id = ?", skuID)
	if communityID > 0 {
		db = db.Where("community_id = ?", communityID)
	}
	err := db.Order("id desc").Find(&list).Error
	return list, err
}

// GetReferencePricing 查询参考定价（排除指定社区）
// 参数：
//   - ctx: 上下文
//   - skuID: SKU ID
//   - excludeCommunityID: 排除的社区 ID
//
// 返回值：
//   - []*model.CommunityPricing: 定价列表
//   - error: 错误信息
func (s *PricingService) GetReferencePricing(ctx context.Context, skuID, excludeCommunityID int64) ([]*model.CommunityPricing, error) {
	var list []*model.CommunityPricing
	db := s.db.GetDb().WithContext(ctx).Where("sku_id = ?", skuID)
	if excludeCommunityID > 0 {
		db = db.Where("community_id != ?", excludeCommunityID)
	}
	err := db.Order("id desc").Limit(10).Find(&list).Error
	return list, err
}

// ToPricingInfo 将定价模型转换为 proto 定价信息
// 参数：
//   - p: 定价模型
//   - communityName: 社区名称
//
// 返回值：
//   - *product.PricingInfo: proto 定价信息
func ToPricingInfo(p *model.CommunityPricing, communityName string) *product.PricingInfo {
	if p == nil {
		return nil
	}
	return &product.PricingInfo{
		Id:            p.ID,
		CommunityId:   p.CommunityID,
		CommunityName: communityName,
		SkuId:         p.SkuID,
		Price:         p.Price,
	}
}

// _ 避免 fmt 未使用告警
var _ = fmt.Errorf
