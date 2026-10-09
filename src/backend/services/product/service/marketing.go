package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// MarketingService 营销业务服务
// 提供优惠券、秒杀活动、满减活动的 CURD 及用户领用/使用能力
type MarketingService struct {
	db *mysql.Instance
}

// NewMarketingService 创建营销业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *MarketingService: 营销业务服务实例
func NewMarketingService(db *mysql.Instance) *MarketingService {
	return &MarketingService{db: db}
}

// ==================== 优惠券 ====================

// ListCoupons 查询优惠券列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.Coupon: 优惠券列表
//   - error: 错误信息
func (s *MarketingService) ListCoupons(ctx context.Context, req *product.ListCouponsReq) ([]*model.Coupon, error) {
	var list []*model.Coupon
	db := s.db.GetDb().WithContext(ctx).Where("community_id = ?", req.CommunityId)
	if req.Status > 0 {
		db = db.Where("status = ?", req.Status)
	}
	err := db.Order("id desc").Find(&list).Error
	return list, err
}

// GetCoupon 获取优惠券详情
// 参数：
//   - ctx: 上下文
//   - id: 优惠券 ID
//
// 返回值：
//   - *model.Coupon: 优惠券模型
//   - error: 错误信息
func (s *MarketingService) GetCoupon(ctx context.Context, id int64) (*model.Coupon, error) {
	var c model.Coupon
	err := s.db.GetDb().WithContext(ctx).First(&c, id).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// CreateCoupon 创建优惠券
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.Coupon: 优惠券模型
//   - error: 错误信息
func (s *MarketingService) CreateCoupon(ctx context.Context, req *product.CreateCouponReq) (*model.Coupon, error) {
	now := time.Now().Format("2006-01-02 15:04:05")
	c := &model.Coupon{
		CommunityID:   req.CommunityId,
		Name:          req.Name,
		Type:          req.Type,
		Threshold:     req.Threshold,
		DiscountValue: req.DiscountValue,
		TotalCount:    req.TotalCount,
		PerUserLimit:  req.PerUserLimit,
		StartTime:     strPtr(req.StartTime),
		EndTime:       strPtr(req.EndTime),
		ValidDays:     req.ValidDays,
		Status:        1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateCoupon 更新优惠券
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *model.Coupon: 优惠券模型
//   - error: 错误信息
func (s *MarketingService) UpdateCoupon(ctx context.Context, req *product.UpdateCouponReq) (*model.Coupon, error) {
	var c model.Coupon
	if err := s.db.GetDb().WithContext(ctx).First(&c, req.Id).Error; err != nil {
		return nil, err
	}
	if req.Name != "" {
		c.Name = req.Name
	}
	if req.Status >= 0 {
		c.Status = req.Status
	}
	c.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	if err := s.db.GetDb().WithContext(ctx).Save(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// DeleteCoupon 删除优惠券
// 参数：
//   - ctx: 上下文
//   - id: 优惠券 ID
//
// 返回值：
//   - error: 错误信息
func (s *MarketingService) DeleteCoupon(ctx context.Context, id int64) error {
	return s.db.GetDb().WithContext(ctx).Delete(&model.Coupon{}, id).Error
}

// ReceiveCoupon 用户领取优惠券
// 参数：
//   - ctx: 上下文
//   - req: 领取请求
//
// 返回值：
//   - error: 错误信息
func (s *MarketingService) ReceiveCoupon(ctx context.Context, req *product.ReceiveCouponReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c model.Coupon
		if err := tx.Clauses(lockClause()).First(&c, req.CouponId).Error; err != nil {
			// 优惠券不存在时返回业务文案，避免 GORM 原始错误透出到客户端
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return bizerr.New(codes.NotFound, "优惠券不存在")
			}
			return err
		}
		if c.Status != 1 {
			return fmt.Errorf("优惠券未启用")
		}
		if c.ReceivedCount >= c.TotalCount {
			return fmt.Errorf("优惠券已领完")
		}
		now := time.Now()
		// 未配置时间窗口（nil）时不限制领取时间
		if (c.StartTime != nil && now.Before(parseTime(*c.StartTime))) ||
			(c.EndTime != nil && now.After(parseTime(*c.EndTime))) {
			return fmt.Errorf("不在领取时间范围内")
		}

		var count int64
		if err := tx.Model(&model.UserCoupon{}).Where("user_id = ? AND coupon_id = ?", req.UserId, req.CouponId).Count(&count).Error; err != nil {
			return err
		}
		if int32(count) >= c.PerUserLimit {
			return fmt.Errorf("已超过每人限领数量")
		}

		expireAt := c.EndTime
		if c.ValidDays > 0 {
			validUntil := now.AddDate(0, 0, int(c.ValidDays)).Format("2006-01-02 15:04:05")
			expireAt = &validUntil
		}
		uc := &model.UserCoupon{
			UserID:        req.UserId,
			CouponID:      c.ID,
			Name:          c.Name,
			Type:          c.Type,
			Threshold:     c.Threshold,
			DiscountValue: c.DiscountValue,
			Status:        1,
			ExpireAt:      expireAt,
			CreatedAt:     now.Format("2006-01-02 15:04:05"),
			UpdatedAt:     now.Format("2006-01-02 15:04:05"),
		}
		if err := tx.Create(uc).Error; err != nil {
			return err
		}

		c.ReceivedCount++
		c.UpdatedAt = now.Format("2006-01-02 15:04:05")
		return tx.Save(&c).Error
	})
}

// ListUserCoupons 查询用户优惠券列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.UserCoupon: 用户优惠券列表
//   - error: 错误信息
func (s *MarketingService) ListUserCoupons(ctx context.Context, req *product.ListUserCouponsReq) ([]*model.UserCoupon, error) {
	var list []*model.UserCoupon
	db := s.db.GetDb().WithContext(ctx).Where("user_id = ?", req.UserId)
	if req.Status > 0 {
		db = db.Where("status = ?", req.Status)
	}
	err := db.Order("id desc").Find(&list).Error
	return list, err
}

// UseCoupon 使用优惠券
// 参数：
//   - ctx: 上下文
//   - req: 使用请求
//
// 返回值：
//   - error: 错误信息
func (s *MarketingService) UseCoupon(ctx context.Context, req *product.UseCouponReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var uc model.UserCoupon
		if err := tx.First(&uc, req.UserCouponId).Error; err != nil {
			return err
		}
		if uc.Status != 1 {
			return fmt.Errorf("优惠券状态不可使用")
		}
		now := time.Now().Format("2006-01-02 15:04:05")
		if uc.ExpireAt != nil && *uc.ExpireAt < now {
			return fmt.Errorf("优惠券已过期")
		}

		uc.Status = 2
		uc.OrderID = req.OrderId
		uc.UpdatedAt = now
		if err := tx.Save(&uc).Error; err != nil {
			return err
		}

		var c model.Coupon
		if err := tx.First(&c, uc.CouponID).Error; err != nil {
			return err
		}
		c.UsedCount++
		c.UpdatedAt = now
		return tx.Save(&c).Error
	})
}

// ==================== 秒杀 ====================

// ListFlashSales 查询秒杀活动列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.FlashSale: 秒杀活动列表
//   - error: 错误信息
func (s *MarketingService) ListFlashSales(ctx context.Context, req *product.ListFlashSalesReq) ([]*model.FlashSale, error) {
	var list []*model.FlashSale
	err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ?", req.CommunityId).
		Order("id desc").
		Find(&list).Error
	return list, err
}

// GetFlashSale 获取秒杀活动详情
// 参数：
//   - ctx: 上下文
//   - id: 秒杀活动 ID
//
// 返回值：
//   - *model.FlashSale: 秒杀活动模型
//   - []*model.FlashSaleItem: 秒杀商品项列表
//   - error: 错误信息
func (s *MarketingService) GetFlashSale(ctx context.Context, id int64) (*model.FlashSale, []*model.FlashSaleItem, error) {
	var fs model.FlashSale
	if err := s.db.GetDb().WithContext(ctx).First(&fs, id).Error; err != nil {
		return nil, nil, err
	}
	var items []*model.FlashSaleItem
	if err := s.db.GetDb().WithContext(ctx).Where("flash_sale_id = ? AND status = ?", id, 1).Find(&items).Error; err != nil {
		return nil, nil, err
	}
	return &fs, items, nil
}

// CreateFlashSale 创建秒杀活动
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.FlashSale: 秒杀活动模型
//   - error: 错误信息
func (s *MarketingService) CreateFlashSale(ctx context.Context, req *product.CreateFlashSaleReq) (*model.FlashSale, error) {
	now := time.Now().Format("2006-01-02 15:04:05")
	fs := &model.FlashSale{
		CommunityID: req.CommunityId,
		Name:        req.Name,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Status:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(fs).Error; err != nil {
			return err
		}
		for _, item := range req.Items {
			fi := &model.FlashSaleItem{
				FlashSaleID:  fs.ID,
				SkuID:        item.SkuId,
				FlashPrice:   item.FlashPrice,
				Stock:        item.Stock,
				LimitPerUser: item.LimitPerUser,
				Status:       1,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			if err := tx.Create(fi).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return fs, nil
}

// UpdateFlashSale 更新秒杀活动
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *model.FlashSale: 秒杀活动模型
//   - error: 错误信息
func (s *MarketingService) UpdateFlashSale(ctx context.Context, req *product.UpdateFlashSaleReq) (*model.FlashSale, error) {
	var fs model.FlashSale
	if err := s.db.GetDb().WithContext(ctx).First(&fs, req.Id).Error; err != nil {
		return nil, err
	}
	if req.Name != "" {
		fs.Name = req.Name
	}
	if req.StartTime != "" {
		fs.StartTime = req.StartTime
	}
	if req.EndTime != "" {
		fs.EndTime = req.EndTime
	}
	if req.Status >= 0 {
		fs.Status = req.Status
	}
	fs.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	if err := s.db.GetDb().WithContext(ctx).Save(&fs).Error; err != nil {
		return nil, err
	}
	return &fs, nil
}

// ==================== 满减 ====================

// ListFullReductions 查询满减活动列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.FullReduction: 满减活动列表
//   - error: 错误信息
func (s *MarketingService) ListFullReductions(ctx context.Context, req *product.ListFullReductionsReq) ([]*model.FullReduction, error) {
	var list []*model.FullReduction
	err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ?", req.CommunityId).
		Order("id desc").
		Find(&list).Error
	return list, err
}

// CreateFullReduction 创建满减活动
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.FullReduction: 满减活动模型
//   - error: 错误信息
func (s *MarketingService) CreateFullReduction(ctx context.Context, req *product.CreateFullReductionReq) (*model.FullReduction, error) {
	now := time.Now().Format("2006-01-02 15:04:05")
	fr := &model.FullReduction{
		CommunityID:  req.CommunityId,
		Name:         req.Name,
		Threshold:    req.Threshold,
		ReduceAmount: req.ReduceAmount,
		StartTime:    strPtr(req.StartTime),
		EndTime:      strPtr(req.EndTime),
		Status:       1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(fr).Error; err != nil {
		return nil, err
	}
	return fr, nil
}

// UpdateFullReduction 更新满减活动
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *model.FullReduction: 满减活动模型
//   - error: 错误信息
func (s *MarketingService) UpdateFullReduction(ctx context.Context, req *product.UpdateFullReductionReq) (*model.FullReduction, error) {
	var fr model.FullReduction
	if err := s.db.GetDb().WithContext(ctx).First(&fr, req.Id).Error; err != nil {
		return nil, err
	}
	if req.Name != "" {
		fr.Name = req.Name
	}
	if req.Threshold > 0 {
		fr.Threshold = req.Threshold
	}
	if req.ReduceAmount > 0 {
		fr.ReduceAmount = req.ReduceAmount
	}
	if req.StartTime != "" {
		fr.StartTime = strPtr(req.StartTime)
	}
	if req.EndTime != "" {
		fr.EndTime = strPtr(req.EndTime)
	}
	if req.Status >= 0 {
		fr.Status = req.Status
	}
	fr.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	if err := s.db.GetDb().WithContext(ctx).Save(&fr).Error; err != nil {
		return nil, err
	}
	return &fr, nil
}

// ==================== 模型转换 ====================

// ToCouponInfo 将优惠券模型转换为 proto 信息
// 参数：
//   - c: 优惠券模型
//
// 返回值：
//   - *product.CouponInfo: proto 优惠券信息
func ToCouponInfo(c *model.Coupon) *product.CouponInfo {
	if c == nil {
		return nil
	}
	return &product.CouponInfo{
		Id:            c.ID,
		CommunityId:   c.CommunityID,
		Name:          c.Name,
		Type:          c.Type,
		Threshold:     c.Threshold,
		DiscountValue: c.DiscountValue,
		TotalCount:    c.TotalCount,
		ReceivedCount: c.ReceivedCount,
		UsedCount:     c.UsedCount,
		PerUserLimit:  c.PerUserLimit,
		StartTime:     ptrString(c.StartTime),
		EndTime:       ptrString(c.EndTime),
		ValidDays:     c.ValidDays,
		Status:        c.Status,
	}
}

// ToUserCouponInfo 将用户优惠券模型转换为 proto 信息
// 参数：
//   - uc: 用户优惠券模型
//
// 返回值：
//   - *product.UserCouponInfo: proto 用户优惠券信息
func ToUserCouponInfo(uc *model.UserCoupon) *product.UserCouponInfo {
	if uc == nil {
		return nil
	}
	return &product.UserCouponInfo{
		Id:            uc.ID,
		CouponId:      uc.CouponID,
		Name:          uc.Name,
		Type:          uc.Type,
		Threshold:     uc.Threshold,
		DiscountValue: uc.DiscountValue,
		Status:        uc.Status,
		ExpireAt:      ptrString(uc.ExpireAt),
	}
}

// ToFlashSaleInfo 将秒杀活动模型转换为 proto 信息
// 参数：
//   - fs: 秒杀活动模型
//
// 返回值：
//   - *product.FlashSaleInfo: proto 秒杀活动信息
func ToFlashSaleInfo(fs *model.FlashSale) *product.FlashSaleInfo {
	if fs == nil {
		return nil
	}
	return &product.FlashSaleInfo{
		Id:          fs.ID,
		CommunityId: fs.CommunityID,
		Name:        fs.Name,
		StartTime:   fs.StartTime,
		EndTime:     fs.EndTime,
		Status:      fs.Status,
	}
}

// ToFlashSaleItemInfo 将秒杀商品项模型转换为 proto 信息
// 参数：
//   - item: 秒杀商品项模型
//   - sku: SKU 模型，用于补充商品信息
//
// 返回值：
//   - *product.FlashSaleItemInfo: proto 秒杀商品项信息
func ToFlashSaleItemInfo(item *model.FlashSaleItem, sku *model.Sku, productName string) *product.FlashSaleItemInfo {
	if item == nil {
		return nil
	}
	info := &product.FlashSaleItemInfo{
		Id:           item.ID,
		SkuId:        item.SkuID,
		FlashPrice:   item.FlashPrice,
		Stock:        item.Stock,
		LimitPerUser: item.LimitPerUser,
	}
	if sku != nil {
		info.SkuCode = sku.SkuCode
		info.SpecName = sku.SpecName
		info.OriginalPrice = sku.Price
	}
	info.ProductName = productName
	return info
}

// ToFullReductionInfo 将满减活动模型转换为 proto 信息
// 参数：
//   - fr: 满减活动模型
//
// 返回值：
//   - *product.FullReductionInfo: proto 满减活动信息
func ToFullReductionInfo(fr *model.FullReduction) *product.FullReductionInfo {
	if fr == nil {
		return nil
	}
	return &product.FullReductionInfo{
		Id:           fr.ID,
		CommunityId:  fr.CommunityID,
		Name:         fr.Name,
		Threshold:    fr.Threshold,
		ReduceAmount: fr.ReduceAmount,
		StartTime:    ptrString(fr.StartTime),
		EndTime:      ptrString(fr.EndTime),
		Status:       fr.Status,
	}
}

// parseTime 解析时间字符串
// 参数：
//   - s: 时间字符串，格式 2006-01-02 15:04:05
//
// 返回值：
//   - time.Time: 时间对象
func parseTime(s string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
	return t
}
