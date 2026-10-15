package service

import (
	"context"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// BannerService Banner 业务服务
// 提供 Banner 轮播图的 CRUD 及列表查询能力
type BannerService struct {
	db *mysql.Instance
}

// NewBannerService 创建 Banner 业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *BannerService: Banner 业务服务实例
func NewBannerService(db *mysql.Instance) *BannerService {
	return &BannerService{db: db}
}

// ListBanners 查询 Banner 列表
// 管理态返回全部记录，C 端仅返回启用且在有效期内的记录，均按 sort 升序、id 降序排列
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - []*model.Banner: Banner 列表
//   - error: 错误信息
func (s *BannerService) ListBanners(ctx context.Context, req *product.ListBannersReq) ([]*model.Banner, error) {
	var list []*model.Banner
	// C 端带全局兜底：community_id=0 表示全平台通用，任何社区可见；
	// 管理态保持精确匹配（后台按社区语境管理各自 banner）
	if req.IsAdmin {
		db := s.db.GetDb().WithContext(ctx).Where("community_id = ?", req.CommunityId)
		if req.Position > 0 {
			db = db.Where("position = ?", req.Position)
		}
		// 管理态不过滤有效期（后台需管理下架/过期的 banner）
		err := db.Order("sort asc, id desc").Find(&list).Error
		return list, err
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	db := s.db.GetDb().WithContext(ctx).
		Where("(community_id = ? OR community_id = 0)", req.CommunityId)
	if req.Position > 0 {
		db = db.Where("position = ?", req.Position)
	}
	// C 端调用：只查启用且在有效期内的
	db = db.Where("status = ?", 1).
		Where("(start_time IS NULL OR start_time <= ?)", now).
		Where("(end_time IS NULL OR end_time >= ?)", now)
	err := db.Order("sort asc, id desc").Find(&list).Error
	return list, err
}

// CreateBanner 创建 Banner
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.Banner: Banner 模型
//   - error: 错误信息
func (s *BannerService) CreateBanner(ctx context.Context, req *product.CreateBannerReq) (*model.Banner, error) {
	banner := &model.Banner{
		CommunityID: req.CommunityId,
		Title:       req.Title,
		ImageURL:    req.ImageUrl,
		LinkURL:     req.LinkUrl,
		Position:    req.Position,
		Sort:        req.Sort,
		Status:      1,
		StartTime:   strPtr(req.StartTime),
		EndTime:     strPtr(req.EndTime),
	}
	err := s.db.GetDb().WithContext(ctx).Create(banner).Error
	return banner, err
}

// UpdateBanner 更新 Banner
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *model.Banner: Banner 模型
//   - error: 错误信息
func (s *BannerService) UpdateBanner(ctx context.Context, req *product.UpdateBannerReq) (*model.Banner, error) {
	var banner model.Banner
	if err := s.db.GetDb().WithContext(ctx).First(&banner, req.Id).Error; err != nil {
		return nil, err
	}
	updates := map[string]any{
		"title":     req.Title,
		"image_url": req.ImageUrl,
		"link_url":  req.LinkUrl,
		"position":  req.Position,
		"sort":      req.Sort,
		"status":    req.Status,
		"start_time": strPtr(req.StartTime),
		"end_time":   strPtr(req.EndTime),
	}
	// 适用社区仅显式传参时更新（optional 字段，nil=不修改；0=全部社区通用）
	if req.CommunityId != nil {
		updates["community_id"] = *req.CommunityId
	}
	if err := s.db.GetDb().WithContext(ctx).Model(&banner).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &banner, nil
}

// DeleteBanner 删除 Banner
// 参数：
//   - ctx: 上下文
//   - id: Banner ID
//
// 返回值：
//   - error: 错误信息
func (s *BannerService) DeleteBanner(ctx context.Context, id int64) error {
	return s.db.GetDb().WithContext(ctx).Delete(&model.Banner{}, id).Error
}

// ToBannerInfo 将 Banner 模型转换为 proto BannerInfo
// 参数：
//   - b: Banner 模型
//
// 返回值：
//   - *product.BannerInfo: proto Banner 信息
func ToBannerInfo(b *model.Banner) *product.BannerInfo {
	if b == nil {
		return nil
	}
	return &product.BannerInfo{
		Id:          b.ID,
		CommunityId: b.CommunityID,
		Title:       b.Title,
		ImageUrl:    b.ImageURL,
		LinkUrl:     b.LinkURL,
		Position:    b.Position,
		Sort:        b.Sort,
		Status:      b.Status,
		StartTime:   ptrString(b.StartTime),
		EndTime:     ptrString(b.EndTime),
	}
}
