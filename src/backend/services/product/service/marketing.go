package service

import (
	"context"
	"errors"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/pageparam"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/timeutil"
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

// normPtrTime 归一化可空时间字符串指针（nil 透传）：parseTime DSN 读回的 RFC3339
// 字面量整行回写 DATETIME 列，MySQL 8.0.19+ 可解析、5.7 直接 1292
// （与 sku/category/user 服务同库同源问题，2026-10-30 接口测试暴露）
func normPtrTime(p *string) *string {
	if p == nil {
		return nil
	}
	n := timeutil.NormalizeRFC3339(*p)
	return &n
}

// normCouponScannedTimes 优惠券整行 Save 前归一化全部时间字段（含可空窗口字段）
func normCouponScannedTimes(c *model.Coupon) {
	c.CreatedAt = timeutil.NormalizeRFC3339(c.CreatedAt)
	c.UpdatedAt = timeutil.NormalizeRFC3339(c.UpdatedAt)
	c.StartTime = normPtrTime(c.StartTime)
	c.EndTime = normPtrTime(c.EndTime)
	c.UseStartTime = normPtrTime(c.UseStartTime)
	c.UseEndTime = normPtrTime(c.UseEndTime)
}

// normFlashSaleScannedTimes 秒杀活动整行 Save 前归一化时间字段
func normFlashSaleScannedTimes(fs *model.FlashSale) {
	fs.CreatedAt = timeutil.NormalizeRFC3339(fs.CreatedAt)
	fs.UpdatedAt = timeutil.NormalizeRFC3339(fs.UpdatedAt)
	fs.StartTime = timeutil.NormalizeRFC3339(fs.StartTime)
	fs.EndTime = timeutil.NormalizeRFC3339(fs.EndTime)
}

// normFullReductionScannedTimes 满减活动整行 Save 前归一化时间字段
func normFullReductionScannedTimes(fr *model.FullReduction) {
	fr.CreatedAt = timeutil.NormalizeRFC3339(fr.CreatedAt)
	fr.UpdatedAt = timeutil.NormalizeRFC3339(fr.UpdatedAt)
	fr.StartTime = normPtrTime(fr.StartTime)
	fr.EndTime = normPtrTime(fr.EndTime)
}

// normFreeShippingScannedTimes 免邮活动整行 Save 前归一化时间字段
func normFreeShippingScannedTimes(a *model.FreeShippingActivity) {
	a.CreatedAt = timeutil.NormalizeRFC3339(a.CreatedAt)
	a.UpdatedAt = timeutil.NormalizeRFC3339(a.UpdatedAt)
	a.StartTime = normPtrTime(a.StartTime)
	a.EndTime = normPtrTime(a.EndTime)
}

// ==================== 优惠券 ====================

// couponRecordMaxPageSize 券领取/使用记录单页上限，比通用档位（pageparam.MaxSize）更紧：
// 每条记录都要回填会员昵称与手机号，页长放大即 N 次关联查询放大
const couponRecordMaxPageSize = 100

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
	// C 端领券中心：仅已发布 + 进入领券中心的券
	if req.CenterVisibleOnly {
		db = db.Where("publish_status = 1 AND is_center_visible = 1")
	}
	if err := db.Order("id desc").Find(&list).Error; err != nil {
		return nil, err
	}
	if err := s.loadCouponScopes(ctx, list); err != nil {
		return nil, err
	}
	return list, nil
}

// GetCoupon 获取优惠券详情（含商品范围）
// communityId>0 时校验券归属社区（见 scope.go），防止跨社区读取券配置
func (s *MarketingService) GetCoupon(ctx context.Context, id, communityId int64) (*model.Coupon, error) {
	var c model.Coupon
	err := s.db.GetDb().WithContext(ctx).First(&c, id).Error
	if err != nil {
		return nil, err
	}
	if err := EnsureScope(communityId, c.CommunityID, "优惠券"); err != nil {
		return nil, err
	}
	if err := s.loadCouponScopes(ctx, []*model.Coupon{&c}); err != nil {
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
	// 资金安全校验：折扣率区间、固定有效期截止时间（后端兜底，不信任前端）
	if err := validateCouponRule(req.Type, req.DiscountValue, req.ExpireType, req.UseEndTime); err != nil {
		return nil, err
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	// is_center_visible：缺省=进领券中心（1）
	centerVisible := int32(1)
	if req.IsCenterVisible != nil {
		if *req.IsCenterVisible {
			centerVisible = 1
		} else {
			centerVisible = 0
		}
	}
	c := &model.Coupon{
		CommunityID:     req.CommunityId,
		Name:            req.Name,
		Type:            req.Type,
		Threshold:       req.Threshold,
		DiscountValue:   req.DiscountValue,
		TotalCount:      req.TotalCount,
		PerUserLimit:    req.PerUserLimit,
		StartTime:       strPtr(req.StartTime),
		EndTime:         strPtr(req.EndTime),
		ValidDays:       req.ValidDays,
		Status:          1,
		PublishStatus:   req.PublishStatus, // 0=草稿（默认）1=直接发布
		GrantStatus:     1,
		IsCenterVisible: centerVisible,
		ExpireType:      req.ExpireType, // 0 视为 1（领取后N天）
		UseStartTime:    strPtr(req.UseStartTime),
		UseEndTime:      strPtr(req.UseEndTime),
		RuleText:        req.RuleText,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if c.ExpireType == 0 {
		c.ExpireType = 1
	}
	// Create 前保存期望值：GORM 对带 default 标签的 int32 字段，Create 遇零值会跳过该列
	// 落 DB 默认值 1 并回填结构体——后续回写必须用期望值而非结构体字段
	wantPublish := req.PublishStatus
	wantCenter := centerVisible
	wantExpire := c.ExpireType
	wantValidDays := c.ValidDays
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(c).Error; err != nil {
			return err
		}
		// 显式回写零值语义字段（草稿 0 / 不进领券中心 0）
		if err := tx.Model(c).Updates(map[string]interface{}{
			"publish_status":    wantPublish,
			"is_center_visible": wantCenter,
			"grant_status":      int32(1),
			"expire_type":       wantExpire,
			"valid_days":        wantValidDays,
		}).Error; err != nil {
			return err
		}
		return s.saveCouponScopesTx(tx, c.ID, req.Scopes, now)
	})
	if err != nil {
		return nil, err
	}
	s.loadCouponScopes(ctx, []*model.Coupon{c})
	return c, nil
}

// UpdateCoupon 更新优惠券（编辑放开：字段级应用，编辑边界由 gateway 按发布状态裁剪）
func (s *MarketingService) UpdateCoupon(ctx context.Context, req *product.UpdateCouponReq) (*model.Coupon, error) {
	var c model.Coupon
	if err := s.db.GetDb().WithContext(ctx).First(&c, req.Id).Error; err != nil {
		return nil, err
	}
	// 社区隔离：仅允许修改本社区语境下的券（超管传 0 放行）
	if err := EnsureScope(req.CommunityId, c.CommunityID, "优惠券"); err != nil {
		return nil, err
	}

	// 状态机白名单：发布状态仅允许 1/2，且草稿只能直接发布（禁止 0→2 跳过发布期）
	if req.PublishStatus > 0 {
		if req.PublishStatus != 1 && req.PublishStatus != 2 {
			return nil, bizerr.New(codes.InvalidArgument, "非法的发布状态")
		}
		if c.PublishStatus == 0 && req.PublishStatus != 1 {
			return nil, bizerr.New(codes.InvalidArgument, "草稿券只能发布，不能直接下架")
		}
	}
	// 发放状态仅允许 0/1
	if req.GrantStatus != nil && *req.GrantStatus != 0 && *req.GrantStatus != 1 {
		return nil, bizerr.New(codes.InvalidArgument, "非法的发放状态")
	}

	// 按「合并后的有效值」做资金安全校验（面额/有效期仅草稿态可改，但校验不依赖该前提）
	effType := c.Type
	if req.Type > 0 {
		effType = req.Type
	}
	effDiscount := c.DiscountValue
	if req.DiscountValue > 0 {
		effDiscount = req.DiscountValue
	}
	effExpire := c.ExpireType
	if req.ExpireType > 0 {
		effExpire = req.ExpireType
	}
	effUseEnd := ""
	if c.UseEndTime != nil {
		effUseEnd = *c.UseEndTime
	}
	if req.UseEndTime != "" {
		effUseEnd = req.UseEndTime
	}
	if err := validateCouponRule(effType, effDiscount, effExpire, effUseEnd); err != nil {
		return nil, err
	}

	if req.Name != "" {
		c.Name = req.Name
	}
	if req.Type > 0 {
		c.Type = req.Type
	}
	if req.Threshold > 0 {
		c.Threshold = req.Threshold
	}
	if req.DiscountValue > 0 {
		c.DiscountValue = req.DiscountValue
	}
	// 补充数量：仅允许增加（已领取量不可被调小截断）
	if req.TotalCount > c.TotalCount {
		c.TotalCount = req.TotalCount
	}
	if req.PerUserLimit > 0 {
		c.PerUserLimit = req.PerUserLimit
	}
	if req.StartTime != "" {
		c.StartTime = strPtr(req.StartTime)
	}
	if req.EndTime != "" {
		c.EndTime = strPtr(req.EndTime)
	}
	if req.ValidDays > 0 {
		c.ValidDays = req.ValidDays
	}
	// status：proto optional，nil=不修改（0=禁用 是合法值）
	if req.Status != nil {
		c.Status = *req.Status
	}
	if req.PublishStatus > 0 {
		c.PublishStatus = req.PublishStatus
	}
	// grant_status：proto optional，nil=不修改（0=停止发放 是合法值）
	if req.GrantStatus != nil {
		c.GrantStatus = *req.GrantStatus
	}
	// is_center_visible：proto optional，nil=不修改
	if req.IsCenterVisible != nil {
		if *req.IsCenterVisible {
			c.IsCenterVisible = 1
		} else {
			c.IsCenterVisible = 0
		}
	}
	if req.ExpireType > 0 {
		c.ExpireType = req.ExpireType
	}
	if req.UseStartTime != "" {
		c.UseStartTime = strPtr(req.UseStartTime)
	}
	if req.UseEndTime != "" {
		c.UseEndTime = strPtr(req.UseEndTime)
	}
	if req.RuleText != "" {
		c.RuleText = req.RuleText
	}
	c.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	normCouponScannedTimes(&c) // 整行 Save 前归一化读回时间（含未编辑的可空窗口字段）

	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&c).Error; err != nil {
			return err
		}
		if req.Scopes == nil {
			return nil
		}
		if err := tx.Where("coupon_id = ?", c.ID).Delete(&model.CouponProductScope{}).Error; err != nil {
			return err
		}
		return s.saveCouponScopesTx(tx, c.ID, req.Scopes, c.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	if err := s.loadCouponScopes(ctx, []*model.Coupon{&c}); err != nil {
		return nil, err
	}
	return &c, nil
}

// DeleteCoupon 删除优惠券（社区隔离 + 已领取禁删；删除时清理范围行）
func (s *MarketingService) DeleteCoupon(ctx context.Context, req *product.DeleteCouponReq) error {
	var c model.Coupon
	if err := s.db.GetDb().WithContext(ctx).First(&c, req.Id).Error; err != nil {
		return err
	}
	// 社区隔离：仅允许删除本社区语境下的券
	if req.CommunityId > 0 && c.CommunityID != req.CommunityId {
		return bizerr.New(codes.PermissionDenied, "无权操作其他社区的优惠券")
	}
	// 已有用户领取的券不可删除：删除会导致持券用户下单时找不到模板券而整体失败
	if c.ReceivedCount > 0 {
		return bizerr.New(codes.FailedPrecondition, "优惠券已被领取，不能删除；请「停止发放」并保留供已领用户使用")
	}
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("coupon_id = ?", req.Id).Delete(&model.CouponProductScope{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Coupon{}, req.Id).Error
	})
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
			return bizerr.New(codes.FailedPrecondition, "优惠券未启用")
		}
		// 发布状态闸口：草稿/已下架的券一律不可领取（下架语义=领券中心不可见且不可领）
		if c.PublishStatus != 1 {
			return bizerr.New(codes.FailedPrecondition, "优惠券未发布或已下架")
		}
		if c.GrantStatus != 1 {
			return bizerr.New(codes.FailedPrecondition, "优惠券已停止发放")
		}
		return s.grantToUserTx(tx, &c, req.UserId, 1, 0, "", time.Now())
	})
}

// GrantCoupon 后台手动发放优惠券（批量，逐人独立校验）
// 发放规则与 C 端自领一致：仅「已发布 + 发放中 + 启用」的券可发放；
// 券级校验（归属社区/状态）整体做一次，逐人校验（限领/总量/窗口）在各自事务内进行，
// 单人失败不影响其他人，失败原因逐条返回给前端展示。
// 参数：
//   - ctx: 上下文
//   - req: 发放请求（券 + 目标会员 + 经手人快照）
//
// 返回值：
//   - *product.GrantCouponResp: 成功人数 + 逐人失败原因
//   - error: 错误信息
func (s *MarketingService) GrantCoupon(ctx context.Context, req *product.GrantCouponReq) (*product.GrantCouponResp, error) {
	if req.CouponId <= 0 || len(req.UserIds) == 0 {
		return nil, bizerr.New(codes.InvalidArgument, "参数错误：请选择要发放的会员")
	}
	// 券级校验一次性完成（社区隔离 + 状态闸口），避免逐人加锁后整体失败
	var c model.Coupon
	if err := s.db.GetDb().WithContext(ctx).First(&c, req.CouponId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, bizerr.New(codes.NotFound, "优惠券不存在")
		}
		return nil, err
	}
	if req.CommunityId > 0 && c.CommunityID != req.CommunityId {
		return nil, bizerr.New(codes.PermissionDenied, "无权操作其他社区的优惠券")
	}
	if c.PublishStatus != 1 {
		return nil, bizerr.New(codes.FailedPrecondition, "优惠券未发布或已下架，不能发放")
	}
	if c.GrantStatus != 1 {
		return nil, bizerr.New(codes.FailedPrecondition, "优惠券已停止发放")
	}
	if c.Status != 1 {
		return nil, bizerr.New(codes.FailedPrecondition, "优惠券未启用")
	}

	resp := &product.GrantCouponResp{Failures: make([]*product.CouponGrantFailure, 0)}
	now := time.Now()
	for _, uid := range req.UserIds {
		if uid <= 0 {
			continue
		}
		// 逐人独立事务：事务内重新加锁读券，总量/限领校验基于最新计数
		err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var cur model.Coupon
			if err := tx.Clauses(lockClause()).First(&cur, req.CouponId).Error; err != nil {
				return err
			}
			return s.grantToUserTx(tx, &cur, uid, 2, req.OperatorId, req.OperatorName, now)
		})
		if err != nil {
			resp.Failures = append(resp.Failures, &product.CouponGrantFailure{UserId: uid, Reason: err.Error()})
			continue
		}
		resp.GrantedCount++
	}
	return resp, nil
}

// grantToUserTx 事务内将券发放给单个会员（C 端自领与后台手动发放共用）
// 调用方需保证券已加锁且状态校验通过；限领/总量/领取窗口/到期计算在此校验与落库
// 参数：
//   - tx: 事务对象
//   - c: 已加锁的优惠券模型（ReceivedCount 会被自增）
//   - userID: 目标会员
//   - source: 来源 1用户自领 2后台发放
//   - operatorID / operatorName: 后台发放时的经手人快照（自领传 0/空）
//   - now: 当前时间
//
// 返回值：
//   - error: 错误信息
func (s *MarketingService) grantToUserTx(tx *gorm.DB, c *model.Coupon, userID int64, source int32, operatorID int64, operatorName string, now time.Time) error {
	if c.ReceivedCount >= c.TotalCount {
		return bizerr.New(codes.FailedPrecondition, "优惠券已领完")
	}
	// 未配置时间窗口（nil）时不限制领取时间
	if (c.StartTime != nil && now.Before(parseTime(*c.StartTime))) ||
		(c.EndTime != nil && now.After(parseTime(*c.EndTime))) {
		return bizerr.New(codes.FailedPrecondition, "不在领取时间范围内")
	}

	var count int64
	if err := tx.Model(&model.UserCoupon{}).Where("user_id = ? AND coupon_id = ?", userID, c.ID).Count(&count).Error; err != nil {
		return err
	}
	if int32(count) >= c.PerUserLimit {
		return bizerr.New(codes.FailedPrecondition, "已超过每人限领数量")
	}

	// 到期时间按 expire_type：
	// 1（默认）=领取后 valid_days 天；valid_days=0 回退领取窗口 end_time（存量口径）
	// 2=固定可用范围，直接取 use_end_time
	var expireAt *string
	if c.ExpireType == 2 {
		expireAt = c.UseEndTime
	} else if c.ValidDays > 0 {
		validUntil := now.AddDate(0, 0, int(c.ValidDays)).Format("2006-01-02 15:04:05")
		expireAt = &validUntil
	} else {
		expireAt = c.EndTime
	}
	nowStr := now.Format("2006-01-02 15:04:05")
	uc := &model.UserCoupon{
		UserID:        userID,
		CouponID:      c.ID,
		Name:          c.Name,
		Type:          c.Type,
		Threshold:     c.Threshold,
		DiscountValue: c.DiscountValue,
		Status:        1,
		Source:        source,
		OperatorID:    operatorID,
		OperatorName:  operatorName,
		ExpireAt:      expireAt,
		CreatedAt:     nowStr,
		UpdatedAt:     nowStr,
	}
	if err := tx.Create(uc).Error; err != nil {
		return err
	}

	c.ReceivedCount++
	c.UpdatedAt = nowStr
	normCouponScannedTimes(c)
	return tx.Save(c).Error
}

// ListUserCoupons 查询用户优惠券列表
// status=1（未使用）时同步排除已过期但尚未被调度器流转的行，
// 保证下单 applyCoupon（按 status=1 拉取）不会选中已过期券
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
		if req.Status == 1 {
			db = db.Where("(expire_at IS NULL OR expire_at >= NOW())")
		}
	}
	err := db.Order("id desc").Find(&list).Error
	return list, err
}

// ListCouponRecords 后台领取/发放记录查询（user_coupons 分页）
// user_coupons 本身无社区字段，通过 coupons.community_id 关联把记录范围限定在本社区发放的券，防跨社区泄露
// 参数：
//   - ctx: 上下文
//   - req: 查询请求（券/会员/状态/来源筛选 + 社区语境 + 分页）
//
// 返回值：
//   - []*model.UserCoupon: 记录列表
//   - int64: 总条数
//   - error: 错误信息
func (s *MarketingService) ListCouponRecords(ctx context.Context, req *product.ListCouponRecordsReq) ([]*model.UserCoupon, int64, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.UserCoupon{}).
		Joins("JOIN coupons ON coupons.id = user_coupons.coupon_id").
		Where("coupons.community_id = ?", req.CommunityId)
	if req.CouponId > 0 {
		db = db.Where("user_coupons.coupon_id = ?", req.CouponId)
	}
	if req.UserId > 0 {
		db = db.Where("user_coupons.user_id = ?", req.UserId)
	}
	if req.Status > 0 {
		db = db.Where("user_coupons.status = ?", req.Status)
		// 与 ListUserCoupons 同口径：未使用筛选排除已过期未流转的行
		if req.Status == 1 {
			db = db.Where("(user_coupons.expire_at IS NULL OR user_coupons.expire_at >= NOW())")
		}
	}
	if req.Source > 0 {
		db = db.Where("user_coupons.source = ?", req.Source)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// 券记录列表沿用 100 的严格页长上限（比通用档位更紧，避免逐行回填用户信息时放大查询）
	page, pageSize := pageparam.ClampMax(int(req.Pagination.GetPage()), int(req.Pagination.GetPageSize()), couponRecordMaxPageSize)
	var list []*model.UserCoupon
	err := db.Select("user_coupons.*").
		Order("user_coupons.id desc").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error
	return list, total, err
}

// UseCoupon 使用优惠券
// 条件更新防并发重复核销：仅未使用(1)状态可核销，并发场景只有一个请求生效；
// UsedCount 采用 gorm.Expr 原子自增，避免读-改-写竞态丢计数
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
			return bizerr.New(codes.FailedPrecondition, "优惠券状态不可使用")
		}
		now := time.Now().Format("2006-01-02 15:04:05")

		// 条件更新：仅未使用且未过期生效，并发核销时后到者 RowsAffected=0。
		// 过期判定必须在库里比（expire_at > NOW()）：DSN parseTime=True 时 datetime 列读回
		// 的 string 字段是 RFC3339 形态（2026-11-02T23:59:59+08:00），与 Go 侧
		// "2006-01-02 15:04:05" 字典序比较时第 11 位 'T' 恒大于空格，判定永远不成立
		res := tx.Model(&model.UserCoupon{}).
			Where("id = ? AND status = 1 AND (expire_at IS NULL OR expire_at > NOW())", uc.ID).
			Updates(map[string]interface{}{
				"status":     int32(2),
				"order_id":   req.OrderId,
				"updated_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return couponUseReject(tx, uc.ID)
		}

		// UsedCount 原子自增，防并发丢失更新
		return tx.Model(&model.Coupon{}).Where("id = ?", uc.CouponID).
			Update("used_count", gorm.Expr("used_count + 1")).Error
	})
}

// couponUseReject 复核优惠券核销被拒的具体原因
// 核销的条件更新把「未使用」与「未过期」合并成一条 WHERE，失败时须回查才能给出准确文案；
// 过期回查同样走库里比较（expire_at <= NOW()），不受 datetime 列读回形态影响。
// 回查失败（事务已中止等）时按状态原因收口，不影响最终拒绝结论。
//
// 参数：
//   - tx: 当前事务
//   - id: 用户券 ID
//
// 返回值：
//   - error: 恒为 FailedPrecondition 业务错误，文案「优惠券已过期」或「优惠券状态不可使用」
//
// 使用示例：
//   - if res.RowsAffected == 0 { return couponUseReject(tx, uc.ID) }
func couponUseReject(tx *gorm.DB, id int64) error {
	var expired int64
	if err := tx.Model(&model.UserCoupon{}).
		Where("id = ? AND expire_at IS NOT NULL AND expire_at <= NOW()", id).
		Count(&expired).Error; err == nil && expired > 0 {
		return bizerr.New(codes.FailedPrecondition, "优惠券已过期")
	}
	// 与函数开头的前置判定同口径（FailedPrecondition + 同文案），
	// 用 fmt.Errorf 会被 ToStatus 归为 Internal，用户与网关都看到"系统错误"
	return bizerr.New(codes.FailedPrecondition, "优惠券状态不可使用")
}

// ReleaseCoupon 取消订单时回补用户券：
// 将该订单绑定的已使用 user_coupon 复位为未使用并清空订单关联，同时回退模板券 used_count。
// 幂等：订单未绑定券或券已被回补时直接成功。
func (s *MarketingService) ReleaseCoupon(ctx context.Context, req *product.ReleaseCouponReq) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var uc model.UserCoupon
		err := tx.Where("order_id = ? AND status = ?", req.OrderId, 2).First(&uc).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		now := time.Now().Format("2006-01-02 15:04:05")
		// 条件更新防并发：仅当仍处于已使用状态时生效
		res := tx.Model(&model.UserCoupon{}).Where("id = ? AND status = ?", uc.ID, 2).Updates(map[string]interface{}{
			"status":     int32(1),
			"order_id":   int64(0),
			"updated_at": now,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		var c model.Coupon
		if err := tx.First(&c, uc.CouponID).Error; err != nil {
			return err
		}
		if c.UsedCount > 0 {
			c.UsedCount--
		}
		c.UpdatedAt = now
		normCouponScannedTimes(&c)
		return tx.Save(&c).Error
	})
}

// validateCouponRule 优惠券面额规则校验（创建/草稿编辑共用，按合并后的有效值校验）
// 折扣券 DiscountValue 语义是 0~1 的折扣率（0.85 = 85 折），
// 误填 >=1 会算出负优惠放大实付金额，必须后端兜底；
// 固定有效期（expire_type=2）必须配置使用截止时间，否则会生成永不过期的券
func validateCouponRule(couponType int32, discountValue float64, expireType int32, useEndTime string) error {
	if couponType == 2 && (discountValue <= 0 || discountValue >= 1) {
		return bizerr.New(codes.InvalidArgument, "折扣率必须在 0 到 1 之间（如 0.85 表示 85 折）")
	}
	if expireType == 2 && useEndTime == "" {
		return bizerr.New(codes.InvalidArgument, "固定有效期必须配置使用截止时间")
	}
	return nil
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
func (s *MarketingService) GetFlashSale(ctx context.Context, id, communityId int64) (*model.FlashSale, []*model.FlashSaleItem, error) {
	var fs model.FlashSale
	if err := s.db.GetDb().WithContext(ctx).First(&fs, id).Error; err != nil {
		return nil, nil, err
	}
	if err := EnsureScope(communityId, fs.CommunityID, "秒杀活动"); err != nil {
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
		// 商品项一次批量插入：逐条 Create 在事务里发 N 次往返，秒杀活动常配数十个 SKU
		items := make([]*model.FlashSaleItem, 0, len(req.Items))
		for _, item := range req.Items {
			items = append(items, &model.FlashSaleItem{
				FlashSaleID:  fs.ID,
				SkuID:        item.SkuId,
				FlashPrice:   item.FlashPrice,
				Stock:        item.Stock,
				LimitPerUser: item.LimitPerUser,
				Status:       1,
				CreatedAt:    now,
				UpdatedAt:    now,
			})
		}
		if len(items) > 0 {
			if err := tx.Create(&items).Error; err != nil {
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
	// 社区隔离：秒杀可改起止时间与状态，跨社区改动直接影响他社区库存与价格，须强校验
	if err := EnsureScope(req.CommunityId, fs.CommunityID, "秒杀活动"); err != nil {
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
	normFlashSaleScannedTimes(&fs)
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
	// 社区隔离：仅允许修改本社区语境下的活动
	if req.CommunityId > 0 && fr.CommunityID != req.CommunityId {
		return nil, bizerr.New(codes.PermissionDenied, "无权操作其他社区的活动")
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
	normFullReductionScannedTimes(&fr)
	if err := s.db.GetDb().WithContext(ctx).Save(&fr).Error; err != nil {
		return nil, err
	}
	return &fr, nil
}

// ==================== 满额免配送费 ====================

// ListFreeShippings 查询免配送费活动列表（含排除范围）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.FreeShippingActivity: 活动列表
//   - error: 错误信息
func (s *MarketingService) ListFreeShippings(ctx context.Context, req *product.ListFreeShippingsReq) ([]*model.FreeShippingActivity, error) {
	var list []*model.FreeShippingActivity
	if err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ?", req.CommunityId).
		Order("id desc").
		Find(&list).Error; err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return list, nil
	}
	// 批量加载排除范围
	activityIDs := make([]int64, 0, len(list))
	for _, a := range list {
		activityIDs = append(activityIDs, a.ID)
	}
	var scopes []*model.FreeShippingScope
	if err := s.db.GetDb().WithContext(ctx).
		Where("activity_id IN ?", activityIDs).
		Find(&scopes).Error; err != nil {
		return nil, err
	}
	scopeMap := make(map[int64][]*model.FreeShippingScope, len(list))
	for _, sc := range scopes {
		scopeMap[sc.ActivityID] = append(scopeMap[sc.ActivityID], sc)
	}
	s.fillScopeNames(ctx, scopes)
	for _, a := range list {
		a.Scopes = scopeMap[a.ID]
	}
	return list, nil
}

// CreateFreeShipping 创建免配送费活动（含排除范围，事务保存）
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.FreeShippingActivity: 活动模型
//   - error: 错误信息
func (s *MarketingService) CreateFreeShipping(ctx context.Context, req *product.CreateFreeShippingReq) (*model.FreeShippingActivity, error) {
	// 门槛必须为正：负数/0 门槛会使所有配送订单免配送费（binding:required 拦不住负值，需后端兜底）
	if req.Threshold <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "免配送费门槛必须大于 0")
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	activity := &model.FreeShippingActivity{
		CommunityID: req.CommunityId,
		Name:        req.Name,
		Threshold:   req.Threshold,
		StartTime:   strPtr(req.StartTime),
		EndTime:     strPtr(req.EndTime),
		Status:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(activity).Error; err != nil {
			return err
		}
		return s.saveScopesTx(tx, activity.ID, req.Scopes, now)
	})
	if err != nil {
		return nil, err
	}
	// 重新加载排除范围（含展示名），保证返回体完整
	if err := s.loadScopes(ctx, activity); err != nil {
		return nil, err
	}
	return activity, nil
}

// UpdateFreeShipping 更新免配送费活动（scopes 非nil 时全量替换排除范围）
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *model.FreeShippingActivity: 活动模型
//   - error: 错误信息
func (s *MarketingService) UpdateFreeShipping(ctx context.Context, req *product.UpdateFreeShippingReq) (*model.FreeShippingActivity, error) {
	var activity model.FreeShippingActivity
	if err := s.db.GetDb().WithContext(ctx).First(&activity, req.Id).Error; err != nil {
		return nil, err
	}
	// 社区隔离：仅允许修改本社区语境下的活动
	if req.CommunityId > 0 && activity.CommunityID != req.CommunityId {
		return nil, bizerr.New(codes.PermissionDenied, "无权操作其他社区的活动")
	}
	if req.Name != "" {
		activity.Name = req.Name
	}
	if req.Threshold > 0 {
		activity.Threshold = req.Threshold
	}
	if req.StartTime != "" {
		activity.StartTime = strPtr(req.StartTime)
	}
	if req.EndTime != "" {
		activity.EndTime = strPtr(req.EndTime)
	}
	if req.Status >= 0 {
		activity.Status = req.Status
	}
	activity.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	normFreeShippingScannedTimes(&activity)

	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&activity).Error; err != nil {
			return err
		}
		// scopes 为 nil 表示本次不涉及范围修改；空切片表示清空
		if req.Scopes == nil {
			return nil
		}
		if err := tx.Where("activity_id = ?", activity.ID).Delete(&model.FreeShippingScope{}).Error; err != nil {
			return err
		}
		return s.saveScopesTx(tx, activity.ID, req.Scopes, activity.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	// 重新加载排除范围（含展示名），保证返回体完整
	if err := s.loadScopes(ctx, &activity); err != nil {
		return nil, err
	}
	return &activity, nil
}

// loadScopes 加载活动的排除范围并回填展示名
// 参数：
//   - ctx: 上下文
//   - activity: 活动模型（Scopes 字段会被覆写）
//
// 返回值：
//   - error: 错误信息
func (s *MarketingService) loadScopes(ctx context.Context, activity *model.FreeShippingActivity) error {
	var scopes []*model.FreeShippingScope
	if err := s.db.GetDb().WithContext(ctx).
		Where("activity_id = ?", activity.ID).
		Find(&scopes).Error; err != nil {
		return err
	}
	s.fillScopeNames(ctx, scopes)
	activity.Scopes = scopes
	return nil
}

// GetActiveFreeShipping 查询当前社区正在生效的免配送费活动
// 生效条件：启用 + 时效内；同社区多个活动命中时取门槛最低（对用户最优）的一项
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *model.FreeShippingActivity: 生效活动（无则 nil）
//   - error: 错误信息
func (s *MarketingService) GetActiveFreeShipping(ctx context.Context, req *product.GetActiveFreeShippingReq) (*model.FreeShippingActivity, error) {
	var list []*model.FreeShippingActivity
	if err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND status = 1", req.CommunityId).
		Order("threshold asc, id desc").
		Find(&list).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	for _, a := range list {
		// 时间窗校验：无法解析（零值）视为未设置，不参与窗口判断
		if a.StartTime != nil && *a.StartTime != "" {
			if start := parseTime(*a.StartTime); !start.IsZero() && now.Before(start) {
				continue
			}
		}
		if a.EndTime != nil && *a.EndTime != "" {
			if end := parseTime(*a.EndTime); !end.IsZero() && now.After(end) {
				continue
			}
		}
		// 命中第一个（门槛最优），加载排除范围后返回
		var scopes []*model.FreeShippingScope
		if err := s.db.GetDb().WithContext(ctx).
			Where("activity_id = ?", a.ID).
			Find(&scopes).Error; err != nil {
			return nil, err
		}
		s.fillScopeNames(ctx, scopes)
		a.Scopes = scopes
		return a, nil
	}
	return nil, nil
}

// saveScopesTx 事务内保存排除范围
// 参数：
//   - tx: 事务对象
//   - activityID: 活动 ID
//   - scopes: 范围请求列表
//   - now: 创建时间
//
// 返回值：
//   - error: 错误信息
func (s *MarketingService) saveScopesTx(tx *gorm.DB, activityID int64, scopes []*product.FreeShippingScopeReq, now string) error {
	rows := make([]*model.FreeShippingScope, 0, len(scopes))
	for _, sc := range scopes {
		rows = append(rows, &model.FreeShippingScope{
			ActivityID: activityID,
			ScopeType:  sc.ScopeType,
			CategoryID: sc.CategoryId,
			ProductID:  sc.ProductId,
			CreatedAt:  now,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	// 一次批量插入：逐条 Create 会把「保存活动范围」变成 N 次往返
	return tx.Create(&rows).Error
}

// fillScopeNames 批量回填范围项的分类名/商品名（展示用，失败静默跳过）
// 参数：
//   - ctx: 上下文
//   - scopes: 范围模型列表
func (s *MarketingService) fillScopeNames(ctx context.Context, scopes []*model.FreeShippingScope) {
	categoryIDs := make([]int64, 0)
	productIDs := make([]int64, 0)
	for _, sc := range scopes {
		switch sc.ScopeType {
		case model.FreeShippingScopeTypeExcludeCategory:
			if sc.CategoryID > 0 {
				categoryIDs = append(categoryIDs, sc.CategoryID)
			}
		case model.FreeShippingScopeTypeExcludeProduct:
			if sc.ProductID > 0 {
				productIDs = append(productIDs, sc.ProductID)
			}
		}
	}
	categoryNames := make(map[int64]string)
	if len(categoryIDs) > 0 {
		var categories []*model.Category
		if err := s.db.GetDb().WithContext(ctx).Where("id IN ?", categoryIDs).Find(&categories).Error; err == nil {
			for _, c := range categories {
				categoryNames[c.ID] = c.Name
			}
		}
	}
	productNames := make(map[int64]string)
	if len(productIDs) > 0 {
		var products []*model.Product
		if err := s.db.GetDb().WithContext(ctx).Where("id IN ?", productIDs).Find(&products).Error; err == nil {
			for _, p := range products {
				productNames[p.ID] = p.Name
			}
		}
	}
	for _, sc := range scopes {
		sc.CategoryName = categoryNames[sc.CategoryID]
		sc.ProductName = productNames[sc.ProductID]
	}
}

// ToFreeShippingScopeInfo 将免配送费范围模型转换为 proto 信息
// 参数：
//   - sc: 范围模型
//
// 返回值：
//   - *product.FreeShippingScopeInfo: proto 范围信息
func ToFreeShippingScopeInfo(sc *model.FreeShippingScope) *product.FreeShippingScopeInfo {
	if sc == nil {
		return nil
	}
	return &product.FreeShippingScopeInfo{
		Id:           sc.ID,
		ScopeType:    sc.ScopeType,
		CategoryId:   sc.CategoryID,
		CategoryName: sc.CategoryName,
		ProductId:    sc.ProductID,
		ProductName:  sc.ProductName,
	}
}

// ToFreeShippingInfo 将免配送费活动模型转换为 proto 信息
// 参数：
//   - a: 活动模型
//
// 返回值：
//   - *product.FreeShippingInfo: proto 活动信息
func ToFreeShippingInfo(a *model.FreeShippingActivity) *product.FreeShippingInfo {
	if a == nil {
		return nil
	}
	info := &product.FreeShippingInfo{
		Id:          a.ID,
		CommunityId: a.CommunityID,
		Name:        a.Name,
		Threshold:   a.Threshold,
		StartTime:   ptrString(a.StartTime),
		EndTime:     ptrString(a.EndTime),
		Status:      a.Status,
		Scopes:      make([]*product.FreeShippingScopeInfo, 0, len(a.Scopes)),
	}
	for _, sc := range a.Scopes {
		info.Scopes = append(info.Scopes, ToFreeShippingScopeInfo(sc))
	}
	return info
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
	info := &product.CouponInfo{
		Id:              c.ID,
		CommunityId:     c.CommunityID,
		Name:            c.Name,
		Type:            c.Type,
		Threshold:       c.Threshold,
		DiscountValue:   c.DiscountValue,
		TotalCount:      c.TotalCount,
		ReceivedCount:   c.ReceivedCount,
		UsedCount:       c.UsedCount,
		PerUserLimit:    c.PerUserLimit,
		StartTime:       ptrString(c.StartTime),
		EndTime:         ptrString(c.EndTime),
		ValidDays:       c.ValidDays,
		Status:          c.Status,
		PublishStatus:   c.PublishStatus,
		GrantStatus:     c.GrantStatus,
		IsCenterVisible: c.IsCenterVisible == 1,
		ExpireType:      c.ExpireType,
		UseStartTime:    ptrString(c.UseStartTime),
		UseEndTime:      ptrString(c.UseEndTime),
		RuleText:        c.RuleText,
		Scopes:          make([]*product.CouponScopeInfo, 0, len(c.Scopes)),
	}
	for _, sc := range c.Scopes {
		info.Scopes = append(info.Scopes, ToCouponScopeInfo(sc))
	}
	return info
}

// loadCouponScopes 批量加载优惠券商品范围并回填展示名
// 资金路径 fail-closed：范围加载失败必须返回错误，
// 否则空 Scopes 会被 couponEligibleAmount 按「全场通用」计算门槛与优惠
// 参数：
//   - ctx: 上下文
//   - coupons: 优惠券模型列表（Scopes 字段会被覆写）
func (s *MarketingService) loadCouponScopes(ctx context.Context, coupons []*model.Coupon) error {
	if len(coupons) == 0 {
		return nil
	}
	couponIDs := make([]int64, 0, len(coupons))
	for _, c := range coupons {
		couponIDs = append(couponIDs, c.ID)
	}
	var scopes []*model.CouponProductScope
	if err := s.db.GetDb().WithContext(ctx).
		Where("coupon_id IN ?", couponIDs).
		Find(&scopes).Error; err != nil {
		return err
	}
	s.fillCouponScopeNames(ctx, scopes)
	scopeMap := make(map[int64][]*model.CouponProductScope, len(coupons))
	for _, sc := range scopes {
		scopeMap[sc.CouponID] = append(scopeMap[sc.CouponID], sc)
	}
	for _, c := range coupons {
		c.Scopes = scopeMap[c.ID]
	}
	return nil
}

// fillCouponScopeNames 回填范围项的分类名/商品名（失败静默跳过）
func (s *MarketingService) fillCouponScopeNames(ctx context.Context, scopes []*model.CouponProductScope) {
	categoryIDs := make([]int64, 0)
	productIDs := make([]int64, 0)
	for _, sc := range scopes {
		switch sc.ScopeType {
		case model.CouponScopeTypeIncludeCategory, model.CouponScopeTypeExcludeCategory:
			if sc.CategoryID > 0 {
				categoryIDs = append(categoryIDs, sc.CategoryID)
			}
		case model.CouponScopeTypeIncludeProduct, model.CouponScopeTypeExcludeProduct:
			if sc.ProductID > 0 {
				productIDs = append(productIDs, sc.ProductID)
			}
		}
	}
	categoryNames := make(map[int64]string)
	if len(categoryIDs) > 0 {
		var categories []*model.Category
		if err := s.db.GetDb().WithContext(ctx).Where("id IN ?", categoryIDs).Find(&categories).Error; err == nil {
			for _, c := range categories {
				categoryNames[c.ID] = c.Name
			}
		}
	}
	productNames := make(map[int64]string)
	if len(productIDs) > 0 {
		var products []*model.Product
		if err := s.db.GetDb().WithContext(ctx).Where("id IN ?", productIDs).Find(&products).Error; err == nil {
			for _, p := range products {
				productNames[p.ID] = p.Name
			}
		}
	}
	for _, sc := range scopes {
		sc.CategoryName = categoryNames[sc.CategoryID]
		sc.ProductName = productNames[sc.ProductID]
	}
}

// saveCouponScopesTx 事务内保存优惠券商品范围（全量）
func (s *MarketingService) saveCouponScopesTx(tx *gorm.DB, couponID int64, scopes []*product.CouponScopeReq, now string) error {
	rows := make([]*model.CouponProductScope, 0, len(scopes))
	for _, sc := range scopes {
		// 全场语义不落行（无行=全场），避免冗余
		if sc.ScopeType == model.CouponScopeTypeAll {
			continue
		}
		rows = append(rows, &model.CouponProductScope{
			CouponID:   couponID,
			ScopeType:  sc.ScopeType,
			CategoryID: sc.CategoryId,
			ProductID:  sc.ProductId,
			CreatedAt:  now,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	// 一次批量插入：逐条 Create 在券模板范围多达数十项时把保存变成 N 次往返
	return tx.Create(&rows).Error
}

// ToCouponScopeInfo 将优惠券范围模型转换为 proto 信息
func ToCouponScopeInfo(sc *model.CouponProductScope) *product.CouponScopeInfo {
	if sc == nil {
		return nil
	}
	return &product.CouponScopeInfo{
		Id:           sc.ID,
		ScopeType:    sc.ScopeType,
		CategoryId:   sc.CategoryID,
		CategoryName: sc.CategoryName,
		ProductId:    sc.ProductID,
		ProductName:  sc.ProductName,
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

// ToCouponRecordInfo 将用户优惠券模型转换为后台领取/发放记录信息
func ToCouponRecordInfo(uc *model.UserCoupon) *product.CouponRecordInfo {
	if uc == nil {
		return nil
	}
	return &product.CouponRecordInfo{
		Id:            uc.ID,
		CouponId:      uc.CouponID,
		CouponName:    uc.Name,
		Type:          uc.Type,
		Threshold:     uc.Threshold,
		DiscountValue: uc.DiscountValue,
		UserId:        uc.UserID,
		Status:        uc.Status,
		OrderId:       uc.OrderID,
		Source:        uc.Source,
		OperatorId:    uc.OperatorID,
		OperatorName:  uc.OperatorName,
		ExpireAt:      ptrString(uc.ExpireAt),
		CreatedAt:     uc.CreatedAt,
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
//   - sku: SKU 模型，用于补充商品 ID/编码/规格/原价
//   - productName: 商品名快照（SKU 表不含名称，由调用方批量取回后传入）
//   - productImage: 商品封面图完整地址（附件域名拼接在调用方完成，此处只做装配）
//
// 返回值：
//   - *product.FlashSaleItemInfo: proto 秒杀商品项信息
func ToFlashSaleItemInfo(item *model.FlashSaleItem, sku *model.Sku, productName, productImage string) *product.FlashSaleItemInfo {
	if item == nil {
		return nil
	}
	info := &product.FlashSaleItemInfo{
		Id:           item.ID,
		SkuId:        item.SkuID,
		FlashPrice:   item.FlashPrice,
		Stock:        item.Stock,
		LimitPerUser: item.LimitPerUser,
		Sold:         item.SoldCount,
	}
	if sku != nil {
		info.ProductId = sku.ProductID
		info.SkuCode = sku.SkuCode
		info.SpecName = sku.SpecName
		info.OriginalPrice = sku.Price
	}
	info.ProductName = productName
	info.ProductImage = productImage
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

// parseTime 解析时间字符串（兼容两种格式：空格分隔为前端/写入格式，
// RFC3339 为 GORM 从 DATETIME 列读出 string 时的格式，解析失败返回零值）
// 参数：
//   - s: 时间字符串，格式 2006-01-02 15:04:05 或 RFC3339（如 2026-10-16T00:00:00+08:00）
//
// 返回值：
//   - time.Time: 时间对象（无法解析时为零值）
func parseTime(s string) time.Time {
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local); err == nil {
		return t
	}
	t, _ := time.ParseInLocation(time.RFC3339, s, time.Local)
	return t
}
