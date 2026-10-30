package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/pwdpolicy"
	userModel "github.com/jcbowen/market/src/backend/services/user/model"
	userService "github.com/jcbowen/market/src/backend/services/user/service"
)

// 本文件是数据包的全局段（scope=global）：社区/行政区划/管理员账号/C 端用户/站点配置。
// 这些数据是环境初始化种子（跨社区、非运营内容），仅随 market-admin init 的
// SeedDemoBootstrap 导入，应用中心不展示、HTTP 导入链路不可触达。
// 全部按自然键（社区名/区划码/用户名/手机号）幂等判重，重复执行无副作用。

// bootstrapNow 统一的当前时间串（段内多条目共用，减少秒级漂移）
func bootstrapNow() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// resolveCommunityID 解析社区引用：优先跨段映射，兜底按社区名查库（映射仅本次运行有效）
func (s *DataImportService) resolveCommunityID(ctx context.Context, pkg *SeedPackage, refs *SectionRefs, key string) (int64, error) {
	if id, ok := refs.Get(key); ok && id > 0 {
		return id, nil
	}
	for i := range pkg.Communities {
		if pkg.Communities[i].Key == key {
			var row userModel.Community
			if err := s.db.GetDb().WithContext(ctx).
				Where("name = ?", pkg.Communities[i].Name).First(&row).Error; err != nil {
				return 0, err
			}
			return row.ID, nil
		}
	}
	return 0, bizerr.New(codes.NotFound, "数据包内社区不存在："+key)
}

// ============================================================
// communitiesSection 演示社区
// ============================================================

type communitiesSection struct {
	svc *DataImportService
}

func (s *communitiesSection) Key() string                      { return "communities" }
func (s *communitiesSection) Name() string                     { return "演示社区" }
func (s *communitiesSection) Scope() string                    { return SectionScopeGlobal }
func (s *communitiesSection) InitOnly() bool                   { return false }
func (s *communitiesSection) ItemCount(pkg *SeedPackage) int32 { return int32(len(pkg.Communities)) }
func (s *communitiesSection) Description() string {
	return "盘龙金茂悦 / 阳光花园两个演示社区（含配送配置与省市区锁定口径）"
}

// Preview 全局段不参与应用中心预检（HTTP 链路已过滤，此实现兜底空操作）
func (s *communitiesSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
}

// Import 按社区名幂等导入；登记 key → ID 供账号任职/用户默认社区引用
func (s *communitiesSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	for i := range pkg.Communities {
		c := &pkg.Communities[i]
		if !isImportable(req, c.Key) {
			continue
		}
		var exist userModel.Community
		if err := s.svc.db.GetDb().WithContext(ctx).
			Where("name = ?", c.Name).First(&exist).Error; err == nil {
			req.Refs.Set(c.Key, exist.ID)
			appendImportResult(items, s.Key(), c.Key, c.Name, "skipped", "同名社区已存在")
			continue
		}
		row := &userModel.Community{
			Name:                 c.Name,
			Address:              c.Address,
			Longitude:            c.Longitude,
			Latitude:             c.Latitude,
			ContactPhone:         c.ContactPhone,
			ManagerName:          c.ManagerName,
			ManagerPhone:         c.ManagerPhone,
			PickupAddress:        c.PickupAddress,
			DeliveryFee:          c.DeliveryFee,
			MinOrderAmount:       c.MinOrderAmount,
			DeliveryRangeKm:      c.DeliveryRangeKm,
			BusinessHours:        c.BusinessHours,
			Province:             c.Province,
			City:                 c.City,
			District:             c.District,
			ProvinceCode:         c.ProvinceCode,
			CityCode:             c.CityCode,
			DistrictCode:         c.DistrictCode,
			AllowOutsideDelivery: c.AllowOutsideDelivery,
			// 自提/配送渠道默认全开（与建社区服务链路的默认值一致）
			EnableSelfPickup: true,
			EnableDelivery:   true,
			Status:           1,
			CreatedAt:        bootstrapNow(),
			UpdatedAt:        bootstrapNow(),
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Create(row).Error; err != nil {
			appendImportResult(items, s.Key(), c.Key, c.Name, "failed", "创建社区失败："+err.Error())
			continue
		}
		req.Refs.Set(c.Key, row.ID)
		appendImportResult(items, s.Key(), c.Key, c.Name, "created", "社区已创建")
		created++
	}
	return created
}

// ============================================================
// regionsSection 演示行政区划（仅重庆市演示子集，全国数据由后台「检查更新」同步）
// ============================================================

type regionsSection struct {
	svc *DataImportService
}

func (s *regionsSection) Key() string    { return "regions" }
func (s *regionsSection) Name() string   { return "行政区划" }
func (s *regionsSection) Scope() string  { return SectionScopeGlobal }
func (s *regionsSection) InitOnly() bool { return false }
func (s *regionsSection) Description() string {
	return "重庆市级 + 37 区县（官方同步口径演示子集），按区划码幂等"
}

func (s *regionsSection) ItemCount(pkg *SeedPackage) int32 { return int32(len(pkg.Regions)) }

func (s *regionsSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
}

func (s *regionsSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	existing := make(map[string]bool, len(pkg.Regions))
	var codes []string
	if err := s.svc.db.GetDb().WithContext(ctx).Model(&userModel.Region{}).
		Pluck("code", &codes).Error; err == nil {
		for _, c := range codes {
			existing[c] = true
		}
	}
	now := bootstrapNow()
	for i := range pkg.Regions {
		r := &pkg.Regions[i]
		if !isImportable(req, r.Code) {
			continue
		}
		if existing[r.Code] {
			appendImportResult(items, s.Key(), r.Code, r.Name, "skipped", "区划码已存在")
			continue
		}
		row := userModel.Region{
			Code:       r.Code,
			Name:       r.Name,
			ParentCode: r.ParentCode,
			Level:      r.Level,
			Status:     userModel.RegionStatusActive,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Create(&row).Error; err != nil {
			appendImportResult(items, s.Key(), r.Code, r.Name, "failed", "创建区划失败："+err.Error())
			continue
		}
		existing[r.Code] = true
		appendImportResult(items, s.Key(), r.Code, r.Name, "created", "区划已创建")
		created++
	}
	return created
}

// ============================================================
// accountsSection 管理员账号与社区任职
// ============================================================

type accountsSection struct {
	svc *DataImportService
}

func (s *accountsSection) Key() string    { return "accounts" }
func (s *accountsSection) Name() string   { return "演示账号" }
func (s *accountsSection) Scope() string  { return SectionScopeGlobal }
func (s *accountsSection) InitOnly() bool { return false }
func (s *accountsSection) Description() string {
	return "超管 admin 与社区管理员 shequ（弱口令仅限演示环境，生产拒绝导入）"
}

func (s *accountsSection) ItemCount(pkg *SeedPackage) int32 { return int32(len(pkg.Admins)) }

func (s *accountsSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
}

// Import 按用户名幂等；任职角色经 EnsureCommunityAdminRole 补建（内置 community_admin）
func (s *accountsSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	roleSvc := userService.NewRoleService(s.svc.db)
	now := bootstrapNow()
	for i := range pkg.Admins {
		a := &pkg.Admins[i]
		if !isImportable(req, a.Key) {
			continue
		}
		// 口令选取：部署方显式配置 MARKET_ADMIN_PASSWORD 时，演示超管按配置口令就地
		// 创建（不再让位跳过）——后续段（采购/收银/订单/退款）的操作员解析经 Refs 依赖
		// 本账号，而按配置口令的超管原本要等种子导入完成后的 ensureSuperAdmin 才创建，
		// 跳过会导致整段操作员解析 record not found 连锁失败；
		// 未配置口令时沿用包内演示弱口令。ensureSuperAdmin 保留幂等兜底
		password := a.Password
		if a.IsSuper {
			if envPwd := os.Getenv("MARKET_ADMIN_PASSWORD"); envPwd != "" {
				if len(envPwd) < pwdpolicy.MinLength {
					appendImportResult(items, s.Key(), a.Key, a.Username, "failed",
						fmt.Sprintf("MARKET_ADMIN_PASSWORD 长度不足（至少 %d 位）", pwdpolicy.MinLength))
					continue
				}
				password = envPwd
			}
		}
		var exist userModel.Admin
		if err := s.svc.db.GetDb().WithContext(ctx).
			Where("username = ?", a.Username).First(&exist).Error; err == nil {
			req.Refs.Set(a.Key, exist.ID)
			appendImportResult(items, s.Key(), a.Key, a.Username, "skipped", "同名账号已存在")
			continue
		}
		hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			appendImportResult(items, s.Key(), a.Key, a.Username, "failed", "密码哈希失败："+err.Error())
			continue
		}
		row := &userModel.Admin{
			Username:  a.Username,
			Password:  string(hashed),
			RealName:  a.RealName,
			Phone:     a.Phone,
			IsSuper:   a.IsSuper,
			Status:    1,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Create(row).Error; err != nil {
			appendImportResult(items, s.Key(), a.Key, a.Username, "failed", "创建账号失败："+err.Error())
			continue
		}
		req.Refs.Set(a.Key, row.ID)
		appendImportResult(items, s.Key(), a.Key, a.Username, "created", "账号已创建")
		created++

		// 社区任职：非超管按 assignments 绑定（角色不存在时补建内置社区管理员角色）
		for _, as := range a.Assignments {
			communityID, err := s.svc.resolveCommunityID(ctx, pkg, req.Refs, as.CommunityKey)
			if err != nil {
				appendImportResult(items, s.Key(), a.Key, a.Username+" @ "+as.CommunityKey, "failed",
					"任职社区解析失败："+err.Error())
				continue
			}
			role, err := roleSvc.EnsureCommunityAdminRole(ctx, communityID)
			if err != nil {
				appendImportResult(items, s.Key(), a.Key, a.Username+" @ "+as.CommunityKey, "failed",
					"内置角色补建失败："+err.Error())
				continue
			}
			if as.RoleCode != userModel.RoleCodeCommunityAdmin {
				appendImportResult(items, s.Key(), a.Key, a.Username+" @ "+as.CommunityKey, "failed",
					"演示包仅支持内置社区管理员角色，收到："+as.RoleCode)
				continue
			}
			var count int64
			if err := s.svc.db.GetDb().WithContext(ctx).Model(&userModel.AdminCommunity{}).
				Where("admin_id = ? AND community_id = ? AND role_id = ?", row.ID, communityID, role.ID).
				Count(&count).Error; err == nil && count > 0 {
				continue
			}
			ac := &userModel.AdminCommunity{
				AdminID:     row.ID,
				CommunityID: communityID,
				RoleID:      role.ID,
			}
			if err := s.svc.db.GetDb().WithContext(ctx).Create(ac).Error; err != nil {
				appendImportResult(items, s.Key(), a.Key, a.Username+" @ "+as.CommunityKey, "failed",
					"创建任职失败："+err.Error())
			}
		}
	}
	return created
}

// ============================================================
// usersSection C 端演示用户（含默认地址与会员-社区关联）
// ============================================================

type usersSection struct {
	svc *DataImportService
}

func (s *usersSection) Key() string    { return "users" }
func (s *usersSection) Name() string   { return "演示用户" }
func (s *usersSection) Scope() string  { return SectionScopeGlobal }
func (s *usersSection) InitOnly() bool { return false }
func (s *usersSection) Description() string {
	return "C 端测试用户（含收货地址与会员-社区关联），按手机号幂等"
}

func (s *usersSection) ItemCount(pkg *SeedPackage) int32 { return int32(len(pkg.Users)) }

func (s *usersSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
}

func (s *usersSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	now := bootstrapNow()
	for i := range pkg.Users {
		u := &pkg.Users[i]
		if !isImportable(req, u.Key) {
			continue
		}
		var exist userModel.User
		if u.Phone == "" {
			appendImportResult(items, s.Key(), u.Key, u.Nickname, "failed", "种子缺少手机号（phone 是该段的幂等键）")
			continue
		}
		if err := s.svc.db.GetDb().WithContext(ctx).
			Where("phone = ?", u.Phone).First(&exist).Error; err == nil {
			req.Refs.Set(u.Key, exist.ID)
			appendImportResult(items, s.Key(), u.Key, u.Nickname, "skipped", "同手机号用户已存在")
			continue
		}
		communityID, err := s.svc.resolveCommunityID(ctx, pkg, req.Refs, u.CommunityKey)
		if err != nil {
			appendImportResult(items, s.Key(), u.Key, u.Nickname, "failed", "默认社区解析失败："+err.Error())
			continue
		}
		row := &userModel.User{
			Openid:      u.Openid,
			Phone:       userModel.NewPhone(u.Phone),
			Nickname:    u.Nickname,
			Avatar:      u.Avatar,
			Gender:      u.Gender,
			CommunityID: communityID,
			Status:      1,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Create(row).Error; err != nil {
			appendImportResult(items, s.Key(), u.Key, u.Nickname, "failed", "创建用户失败："+err.Error())
			continue
		}
		req.Refs.Set(u.Key, row.ID)
		appendImportResult(items, s.Key(), u.Key, u.Nickname, "created", "用户已创建")
		created++

		// 默认收货地址（按 user_id+detail 幂等）
		if u.Address != nil {
			var addrCount int64
			s.svc.db.GetDb().WithContext(ctx).Model(&userModel.Address{}).
				Where("user_id = ? AND detail = ?", row.ID, u.Address.Detail).Count(&addrCount)
			if addrCount == 0 {
				addr := &userModel.Address{
					UserID:       row.ID,
					ContactName:  u.Address.ContactName,
					ContactPhone: u.Address.ContactPhone,
					Province:     u.Address.Province,
					City:         u.Address.City,
					District:     u.Address.District,
					ProvinceCode: u.Address.ProvinceCode,
					CityCode:     u.Address.CityCode,
					DistrictCode: u.Address.DistrictCode,
					Detail:       u.Address.Detail,
					Tag:          u.Address.Tag,
					CommunityID:  communityID,
					IsDefault:    u.Address.IsDefault,
					CreatedAt:    now,
					UpdatedAt:    now,
				}
				if err := s.svc.db.GetDb().WithContext(ctx).Create(addr).Error; err != nil {
					appendImportResult(items, s.Key(), u.Key, u.Nickname+"地址", "failed", "创建地址失败："+err.Error())
				}
			}
		}

		// 会员-社区关联（uk_user_community 幂等；source 演示真实来源语义）
		for _, m := range u.Memberships {
			mCommunityID, err := s.svc.resolveCommunityID(ctx, pkg, req.Refs, m.CommunityKey)
			if err != nil {
				continue
			}
			var mCount int64
			s.svc.db.GetDb().WithContext(ctx).Model(&userModel.UserCommunity{}).
				Where("user_id = ? AND community_id = ?", row.ID, mCommunityID).Count(&mCount)
			if mCount > 0 {
				continue
			}
			uc := &userModel.UserCommunity{
				UserID:      row.ID,
				CommunityID: mCommunityID,
				Source:      m.Source,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := s.svc.db.GetDb().WithContext(ctx).Create(uc).Error; err != nil {
				appendImportResult(items, s.Key(), u.Key, u.Nickname+"关联", "failed", "创建会员-社区关联失败："+err.Error())
			}
		}
	}
	return created
}

// ============================================================
// siteSection 演示站点配置（site_configs 单行品牌位）
// ============================================================

type siteSection struct {
	svc *DataImportService
}

func (s *siteSection) Key() string    { return "site" }
func (s *siteSection) Name() string   { return "站点配置" }
func (s *siteSection) Scope() string  { return SectionScopeGlobal }
func (s *siteSection) InitOnly() bool { return false }
func (s *siteSection) Description() string {
	return "站名/页脚/SEO 演示配置（base_url 留空按请求现推）"
}

func (s *siteSection) ItemCount(pkg *SeedPackage) int32 {
	if pkg.SiteConfig == nil {
		return 0
	}
	return 1
}

func (s *siteSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
}

func (s *siteSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	cfg := pkg.SiteConfig
	if cfg == nil || !isImportable(req, "site") {
		return 0
	}
	var count int64
	if err := s.svc.db.GetDb().WithContext(ctx).Model(&userModel.SiteConfig{}).Count(&count).Error; err != nil {
		appendImportResult(items, s.Key(), "site", "站点配置", "failed", "查询站点配置失败："+err.Error())
		return 0
	}
	if count > 0 {
		appendImportResult(items, s.Key(), "site", "站点配置", "skipped", "站点配置已存在（单行表）")
		return 0
	}
	now := bootstrapNow()
	row := &userModel.SiteConfig{
		ID: userModel.SiteConfigID,
		// base_url 恒留空：空值时网关按请求现推派生回调与外链，避免种子写死部署域名
		SiteName:       cfg.SiteName,
		FooterHTML:     cfg.FooterHTML,
		SeoDescription: cfg.SeoDescription,
		SeoKeywords:    cfg.SeoKeywords,
		Version:        0,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.svc.db.GetDb().WithContext(ctx).Create(row).Error; err != nil {
		appendImportResult(items, s.Key(), "site", "站点配置", "failed", "创建站点配置失败："+err.Error())
		return 0
	}
	appendImportResult(items, s.Key(), "site", "站点配置", "created", "站点配置已创建")
	return 1
}
