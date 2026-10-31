package service

import (
	"context"
	"strconv"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// bannersSection Banner 条目：按标题判重；
// 平台级条目（platform=true）落 community_id=0 全平台通用位，
// 社区级条目按 community_key 归属演示社区，导入时落到目标社区
type bannersSection struct {
	svc *DataImportService
}

func (s *bannersSection) Key() string    { return "banners" }
func (s *bannersSection) Name() string   { return "Banner 轮播" }
func (s *bannersSection) Scope() string  { return SectionScopeCommunity }
func (s *bannersSection) InitOnly() bool { return false }
func (s *bannersSection) Description() string {
	return "首页轮播演示位（平台级通用位 + 社区定制位，图片为内置演示素材，可替换为本地上传素材）"
}

// inScope 该 Banner 是否参与本次导入：平台级恒参与（全局唯一，重复导入由 community_id=0 判重跳过）
func (s *bannersSection) inScope(b *SeedBanner, scopeKey string) bool {
	return b.Platform || seedInCommunityScope(b.CommunityKey, scopeKey)
}

// ItemCount 平台级 + 全部社区域归属的条目数（应用中心数据集口径）
func (s *bannersSection) ItemCount(pkg *SeedPackage) int32 {
	scope := communityScopeKey(pkg, "")
	var n int32
	for i := range pkg.Banners {
		if s.inScope(&pkg.Banners[i], scope) {
			n++
		}
	}
	return n
}

// Preview 按各自落库社区（平台级=0）的标题判重
func (s *bannersSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
	scope := communityScopeKey(pkg, "")
	titles := s.loadTitleSets(ctx, pkg, scope, req.CommunityID)
	for i := range pkg.Banners {
		b := &pkg.Banners[i]
		if !isSelected(req, b.Key) || !s.inScope(b, scope) {
			continue
		}
		target := bannerCommunityID(b, req.CommunityID)
		if titles[target][b.Title] {
			appendPreviewItem(items, s.Key(), b.Key, b.Title, "skip", "同社区已存在同名 Banner", true)
			continue
		}
		where := "position=" + strconv.Itoa(int(b.Position))
		if b.Platform {
			where = "平台级（全部社区可见）"
		}
		appendPreviewItem(items, s.Key(), b.Key, b.Title, "create", "将新建 Banner（"+where+"）", true)
	}
}

// Import 逐条独立创建；平台级条目落 community_id=0
func (s *bannersSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	scope := communityScopeKey(pkg, req.CommunityKey)
	titles := s.loadTitleSets(ctx, pkg, scope, req.CommunityID)
	for i := range pkg.Banners {
		b := &pkg.Banners[i]
		if !isImportable(req, b.Key) || !s.inScope(b, scope) {
			continue
		}
		target := bannerCommunityID(b, req.CommunityID)
		if titles[target][b.Title] {
			appendImportResult(items, s.Key(), b.Key, b.Title, "skipped", "同名 Banner 已存在")
			continue
		}
		row, err := s.svc.bannerSvc.CreateBanner(ctx, &product.CreateBannerReq{
			CommunityId: target,
			Title:       b.Title,
			ImageUrl:    b.ImageURL,
			LinkUrl:     b.LinkURL,
			Position:    b.Position,
			Sort:        b.Sort,
		})
		if err != nil {
			appendImportResult(items, s.Key(), b.Key, b.Title, "failed", "创建 Banner 失败："+err.Error())
			continue
		}
		req.Refs.Set(b.Key, row.ID)
		appendImportResult(items, s.Key(), b.Key, b.Title, "created", "Banner 已创建")
		created++
	}
	return created
}

// bannerCommunityID Banner 落库的 community_id：平台级为 0（全平台通用位），否则目标社区
func bannerCommunityID(b *SeedBanner, communityID int64) int64 {
	if b.Platform {
		return 0
	}
	return communityID
}

// loadTitleSets 按条目实际归属分组加载标题判重集（target community_id → 标题集合）：
// 平台级与社区级同名不冲突，必须按落库社区分别判重
func (s *bannersSection) loadTitleSets(ctx context.Context, pkg *SeedPackage, scopeKey string, communityID int64) map[int64]map[string]bool {
	targets := map[int64]bool{}
	for i := range pkg.Banners {
		b := &pkg.Banners[i]
		if !s.inScope(b, scopeKey) {
			continue
		}
		targets[bannerCommunityID(b, communityID)] = true
	}
	sets := make(map[int64]map[string]bool, len(targets))
	for id := range targets {
		t, _ := s.loadTitleSet(ctx, id)
		sets[id] = t
	}
	return sets
}

// loadTitleSet 指定社区（0=平台级）的 Banner 标题集合
func (s *bannersSection) loadTitleSet(ctx context.Context, communityID int64) (map[string]bool, error) {
	var titles []string
	if err := s.svc.db.GetDb().WithContext(ctx).Model(&model.Banner{}).
		Where("community_id = ?", communityID).Pluck("title", &titles).Error; err != nil {
		return nil, err
	}
	return toNameSet(titles), nil
}
