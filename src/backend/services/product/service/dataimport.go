package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
	userModel "github.com/jcbowen/market/src/backend/services/user/model"
	userService "github.com/jcbowen/market/src/backend/services/user/service"
)

// 本文件是「应用中心 · 数据包」导入模块的框架层：
// - 包定义（SeedPackage 及各 Seed 类型）与条目化注册表（PackageSection 接口）；
// - DataImportService 负责包解析、预检/执行的分发与结果汇总；
// - 各类数据的具体预检/导入逻辑见 section_catalog.go / section_banners.go / section_marketing.go
//   / section_bootstrap.go（全局种子）/ section_free_shipping.go / section_user_assets.go。
// 数据源：seeddata/full-demo.json（由 tools/gen-data-package.mjs 生成后固化提交）。
//
// 数据归属（SectionScope）：global 段（社区/账号/区划/用户/站点）是环境初始化种子，
// 仅随 market-admin init 的 SeedDemoBootstrap 导入，应用中心一律不展示不可导；
// community 段按社区导入，其中 InitOnly=true 的段（二期：订单/进销存等演示样本）
// 也仅随初始化灌入，应用中心同样不展示。
// 社区域条目逐条带 community_key 标注归属演示社区（见 communityScopeKey/seedInCommunityScope）：
// 初始化逐社区导入时各取自身归属；应用中心数据集为全部条目（SectionScopeAll，
// 两社区商品名已全部唯一，混灌不会产生按名解析歧义）。

//go:embed seeddata/full-demo.json
var seedPackageFullDemo []byte

// 数据归属常量（PackageSection.Scope）
const (
	// SectionScopeGlobal 全局种子：跨社区、仅初始化流程导入（应用中心不显示）
	SectionScopeGlobal = "global"
	// SectionScopeCommunity 社区域数据：按社区导入
	SectionScopeCommunity = "community"
	// SectionScopeAll 应用中心数据集口径：包内全部社区域条目都参与预检/导入
	//（两社区商品名已全部唯一，混灌一个社区不会产生按名解析歧义；见 communityScopeKey）
	SectionScopeAll = "all"
)

// SeedPackage 数据包定义（条目化注册表）
type SeedPackage struct {
	Key            string              `json:"key"`
	Name           string              `json:"name"`
	Description    string              `json:"description"`
	Communities    []SeedCommunity     `json:"communities"`
	Regions        []SeedRegion        `json:"regions"`
	Admins         []SeedAdmin         `json:"admins"`
	Users          []SeedUser          `json:"users"`
	SiteConfig     *SeedSiteConfig     `json:"site"`
	Categories     []SeedCategory      `json:"categories"`
	Products       []SeedProduct       `json:"products"`
	Skus           []SeedSku           `json:"skus"`
	Banners        []SeedBanner        `json:"banners"`
	FullReductions []SeedFullReduction `json:"full_reductions"`
	Coupons        []SeedCoupon        `json:"coupons"`
	FlashSales     []SeedFlashSale     `json:"flash_sales"`
	FreeShippings  []SeedFreeShipping  `json:"free_shippings"`
	UserCoupons    []SeedUserCoupon    `json:"user_coupons"`
	Carts          []SeedCart          `json:"carts"`
	// —— 以下为二期演示样本（init_only：仅随初始化灌入，应用中心不展示） ——
	Suppliers      []SeedSupplier      `json:"suppliers"`
	PurchaseOrders []SeedPurchaseOrder `json:"purchase_orders"`
	PosShifts      []SeedPosShift      `json:"pos_shifts"`
	PosHeldOrders  []SeedPosHeldOrder  `json:"pos_held_orders"`
	Orders         []SeedOrder         `json:"orders"`
	Refunds        []SeedRefund        `json:"refunds"`
	StockIns       []SeedStockInOrder  `json:"stock_in_orders"`
	StockOuts      []SeedStockOutOrder `json:"stock_out_orders"`
	StockChecks    []SeedStockCheck    `json:"stock_checks"`
	StockRecords   []SeedStockRecord   `json:"stock_records"`

	sections []PackageSection
}

// SeedTime 种子相对时间点：三种表达二选一。
//   - offset_minutes：相对导入时刻的分钟偏移（如 -120 = 两小时前）；
//   - day_offset + minute_of_day：相对今天零点的天数偏移 + 当天第 N 分钟
//     （如 day_offset=-1 + minute_of_day=540 = 昨天 09:00，用于班次/收银单的「昨天营业时段」语义）。
//
// 全部在导入时换算为绝对时间，避免种子数据随存放时间老化失真。
type SeedTime struct {
	OffsetMinutes *int32 `json:"offset_minutes,omitempty"`
	DayOffset     *int32 `json:"day_offset,omitempty"`
	MinuteOfDay   *int32 `json:"minute_of_day,omitempty"`
}

// Resolve 换算为绝对时间串（2006-01-02 15:04:05）；nil 返回空串
func (t *SeedTime) Resolve(now time.Time) string {
	if t == nil {
		return ""
	}
	if t.OffsetMinutes != nil {
		return now.Add(time.Duration(*t.OffsetMinutes) * time.Minute).Format("2006-01-02 15:04:05")
	}
	day, mod := 0, 0
	if t.DayOffset != nil {
		day = int(*t.DayOffset)
	}
	if t.MinuteOfDay != nil {
		mod = int(*t.MinuteOfDay)
	}
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, day).Add(time.Duration(mod) * time.Minute)
	return base.Format("2006-01-02 15:04:05")
}

// ResolvePtr 换算为可空时间指针（订单 pay_time/deliver_time 等可空列）
func (t *SeedTime) ResolvePtr(now time.Time) *string {
	s := t.Resolve(now)
	if s == "" {
		return nil
	}
	return &s
}

// backfillTimestamps 回填种子时间：基座 CommonBaseModel.BeforeCreate 会把
// CreatedAt/UpdatedAt 无条件覆盖为当前时刻，而演示样本（订单/售后/单据）依赖
// 历史时间语义（超时窗/售后窗/时间线展示），Create 后经 UpdateColumns（跳过钩子）
// 写回种子相对时间换算值。updatedAt 传空则只回填 created_at。
func backfillTimestamps(db *gorm.DB, table any, id int64, createdAt, updatedAt string) {
	updates := map[string]any{"created_at": createdAt}
	if updatedAt != "" {
		updates["updated_at"] = updatedAt
	}
	db.Model(table).Where("id = ?", id).UpdateColumns(updates)
}

// latestSeedTime 取多个相对时间点的最大值（订单 updated_at = 最后一个里程碑）
func latestSeedTime(now time.Time, times ...*SeedTime) string {
	latest := ""
	var latestT time.Time
	for _, t := range times {
		s := t.Resolve(now)
		if s == "" {
			continue
		}
		parsed, err := time.ParseInLocation("2006-01-02 15:04:05", s, now.Location())
		if err != nil {
			continue
		}
		if latest == "" || parsed.After(latestT) {
			latest, latestT = s, parsed
		}
	}
	return latest
}

// SeedCommunity 演示社区（全局段：仅初始化导入，key 全包唯一）
type SeedCommunity struct {
	Key                  string  `json:"key"`
	Name                 string  `json:"name"`
	Address              string  `json:"address"`
	Longitude            float64 `json:"longitude"`
	Latitude             float64 `json:"latitude"`
	ContactPhone         string  `json:"contact_phone"`
	ManagerName          string  `json:"manager_name"`
	ManagerPhone         string  `json:"manager_phone"`
	PickupAddress        string  `json:"pickup_address"`
	DeliveryFee          float64 `json:"delivery_fee"`
	MinOrderAmount       float64 `json:"min_order_amount"`
	DeliveryRangeKm      float64 `json:"delivery_range_km"`
	BusinessHours        string  `json:"business_hours"` // 原样存储：多段 JSON 或 "HH:mm-HH:mm" 单段文本
	Province             string  `json:"province"`
	City                 string  `json:"city"`
	District             string  `json:"district"`
	ProvinceCode         string  `json:"province_code"`
	CityCode             string  `json:"city_code"` // 直辖市无 4 位市级码，为空
	DistrictCode         string  `json:"district_code"`
	AllowOutsideDelivery bool    `json:"allow_outside_delivery"`
}

// SeedRegion 演示行政区划（全局段；仅覆盖演示省份数据，全国数据由后台「检查更新」同步）
type SeedRegion struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	ParentCode string `json:"parent_code"`
	Level      int32  `json:"level"`
}

// SeedAdminAssignment 管理员社区任职（角色按 code 在目标社区解析/补建）
type SeedAdminAssignment struct {
	CommunityKey string `json:"community_key"`
	RoleCode     string `json:"role_code"` // 当前仅 community_admin（内置角色）
}

// SeedAdmin 演示管理员（全局段）
// Password 为明文弱口令，仅存在于演示包（APP_ENV=production 时初始化流程拒绝导入），
// 导入时 bcrypt 加密落库
type SeedAdmin struct {
	Key         string                `json:"key"`
	Username    string                `json:"username"`
	Password    string                `json:"password"`
	RealName    string                `json:"real_name"`
	Phone       string                `json:"phone"`
	IsSuper     bool                  `json:"is_super"`
	Assignments []SeedAdminAssignment `json:"assignments"`
}

// SeedUserAddress 演示用户默认收货地址
type SeedUserAddress struct {
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	Province     string `json:"province"`
	City         string `json:"city"`
	District     string `json:"district"`
	ProvinceCode string `json:"province_code"`
	CityCode     string `json:"city_code"`
	DistrictCode string `json:"district_code"`
	Detail       string `json:"detail"`
	Tag          string `json:"tag"`
	IsDefault    bool   `json:"is_default"`
}

// SeedUserMembership 会员-社区关联（会员列表按社区过滤与拉黑校验依赖此表，
// 必须显式导入——启动回填 BackfillUserCommunities 早于种子导入，覆盖不到种子用户）
type SeedUserMembership struct {
	CommunityKey string `json:"community_key"`
	Source       string `json:"source"` // order / address / admin / backfill
}

// SeedUser 演示 C 端用户（全局段；openid/phone 均为唯一键，按 phone 幂等）
type SeedUser struct {
	Key          string               `json:"key"`
	Openid       string               `json:"openid"`
	Phone        string               `json:"phone"`
	Nickname     string               `json:"nickname"`
	Avatar       string               `json:"avatar"`
	Gender       int32                `json:"gender"`
	CommunityKey string               `json:"community_key"` // 默认社区
	Address      *SeedUserAddress     `json:"address"`
	Memberships  []SeedUserMembership `json:"memberships"`
}

// SeedSiteConfig 演示站点配置（全局段；site_configs 单行，读取端无行时惰性创建空默认行，
// 种子行仅为开箱即见品牌位；base_url 恒留空按请求现推）
type SeedSiteConfig struct {
	SiteName       string `json:"site_name"`
	FooterHTML     string `json:"footer_html"`
	SeoDescription string `json:"seo_description"`
	SeoKeywords    string `json:"seo_keywords"`
}

// SeedCategory 演示分类（key 全包唯一，跨社区同名分类导入时按名称去重）
type SeedCategory struct {
	Key          string `json:"key"`
	CommunityKey string `json:"community_key"`
	Name         string `json:"name"`
	Sort         int32  `json:"sort"`
	Icon         string `json:"icon"`
}

// SeedSpecValue 演示规格值
type SeedSpecValue struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// SeedSpec 演示规格树（当前种子均为单规格名+多规格值形态）
type SeedSpec struct {
	Name   string          `json:"name"`
	Values []SeedSpecValue `json:"values"`
}

// SeedProduct 演示商品
type SeedProduct struct {
	Key          string    `json:"key"`
	CommunityKey string    `json:"community_key"`
	CategoryKey  string    `json:"category_key"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Unit         string    `json:"unit"`
	IsMultiSpec  bool      `json:"is_multi_spec"`
	Sort         int32     `json:"sort"`
	VirtualSales int32     `json:"virtual_sales"`
	Status       int32     `json:"status"`
	Images       []string  `json:"images"`
	Spec         *SeedSpec `json:"spec"`
	SkuKeys      []string  `json:"sku_keys"`
}

// SeedBatch 演示批次（日期为相对天数，导入时换算为绝对日期，避免种子数据过期）
type SeedBatch struct {
	No            string  `json:"no"`
	ProductionAgo int32   `json:"production_ago"` // 生产日期 = 今天 - N 天
	ExpireIn      *int32  `json:"expire_in"`      // 过期日期 = 今天 + N 天；nil=不限期
	Quantity      int32   `json:"quantity"`
	UnitCost      float64 `json:"unit_cost"`
	Ref           string  `json:"ref"`
}

// SeedSku 演示 SKU
type SeedSku struct {
	Key           string      `json:"key"`
	ProductKey    string      `json:"product_key"`
	Code          string      `json:"code"`
	SpecName      string      `json:"spec_name"`
	SpecValueKey  string      `json:"spec_value_key"`
	Price         float64     `json:"price"`
	OriginalPrice float64     `json:"original_price"`
	CostPrice     float64     `json:"cost_price"`
	Barcode       string      `json:"barcode"`
	Stock         int32       `json:"stock"`
	SafetyStock   int32       `json:"safety_stock"`
	Image         string      `json:"image"`
	Status        int32       `json:"status"`
	Batches       []SeedBatch `json:"batches"`
}

// SeedBanner 演示 Banner
// Platform=true 时为全平台通用位（community_id=0，所有社区 C 端可见，初始化逐社区导入只落一次）；
// 否则按 CommunityKey 归属演示社区，导入时落到目标社区
type SeedBanner struct {
	Key          string `json:"key"`
	CommunityKey string `json:"community_key"`
	Platform     bool   `json:"platform"`
	Title        string `json:"title"`
	ImageURL     string `json:"image_url"`
	LinkURL      string `json:"link_url"`
	Position     int32  `json:"position"`
	Sort         int32  `json:"sort"`
}

// SeedFullReduction 演示满减规则（长期有效）
type SeedFullReduction struct {
	Key          string  `json:"key"`
	CommunityKey string  `json:"community_key"`
	Name         string  `json:"name"`
	Threshold    float64 `json:"threshold"`
	ReduceAmount float64 `json:"reduce_amount"`
}

// SeedCouponScope 演示优惠券适用范围（引用包内分类 key）
type SeedCouponScope struct {
	ScopeType   int32  `json:"scope_type"`
	CategoryKey string `json:"category_key"`
}

// SeedCoupon 演示优惠券（领取后 N 天有效，长期可领）
type SeedCoupon struct {
	Key           string            `json:"key"`
	CommunityKey  string            `json:"community_key"`
	Name          string            `json:"name"`
	Type          int32             `json:"type"`
	Threshold     float64           `json:"threshold"`
	DiscountValue float64           `json:"discount_value"`
	TotalCount    int32             `json:"total_count"`
	PerUserLimit  int32             `json:"per_user_limit"`
	ValidDays     int32             `json:"valid_days"`
	Scopes        []SeedCouponScope `json:"scopes"`
}

// SeedFlashSaleItem 演示秒杀项（引用包内 SKU key；SoldCount 为演示已售口径，创建后回写）
type SeedFlashSaleItem struct {
	Key          string  `json:"key"`
	SkuKey       string  `json:"sku_key"`
	FlashPrice   float64 `json:"flash_price"`
	Stock        int32   `json:"stock"`
	SoldCount    int32   `json:"sold_count"`
	LimitPerUser int32   `json:"limit_per_user"`
}

// SeedFlashSale 演示秒杀活动（时间窗为相对分钟偏移，导入时换算）
type SeedFlashSale struct {
	Key                string              `json:"key"`
	CommunityKey       string              `json:"community_key"`
	Name               string              `json:"name"`
	StartOffsetMinutes int32               `json:"start_offset_minutes"`
	EndOffsetMinutes   int32               `json:"end_offset_minutes"`
	Items              []SeedFlashSaleItem `json:"items"`
}

// SeedFreeShippingScope 演示满免邮排除范围（商品按社区内名称解析，避免依赖 key 映射的跨段时序）
type SeedFreeShippingScope struct {
	ScopeType   int32  `json:"scope_type"` // 4=排除分类 5=排除商品（当前仅排除语义）
	CategoryKey string `json:"category_key"`
	ProductName string `json:"product_name"`
}

// SeedFreeShipping 演示满免邮活动（长期有效：start/end 为空）
type SeedFreeShipping struct {
	Key          string                  `json:"key"`
	CommunityKey string                  `json:"community_key"`
	Name         string                  `json:"name"`
	Threshold    float64                 `json:"threshold"`
	Scopes       []SeedFreeShippingScope `json:"scopes"`
}

// SeedUserCoupon 演示用户持券（仅未使用态；已用券绑定订单，随订单段导入）
type SeedUserCoupon struct {
	Key          string `json:"key"`
	CommunityKey string `json:"community_key"`
	UserPhone    string `json:"user_phone"` // 用户按 phone 解析（users 全局段唯一键）
	CouponKey    string `json:"coupon_key"`
	CouponName   string `json:"coupon_name"`
	ExpireInDays int32  `json:"expire_in_days"` // 过期时间 = 导入时刻 + N 天
}

// SeedCart 演示购物车（商品按社区内名称+规格名解析；快照字段导入时从库回填）
type SeedCart struct {
	Key          string `json:"key"`
	CommunityKey string `json:"community_key"`
	UserPhone    string `json:"user_phone"`
	ProductName  string `json:"product_name"`
	SpecName     string `json:"spec_name"`
	Quantity     int32  `json:"quantity"`
	Selected     bool   `json:"selected"`
}

// PreviewRequest 预检入参
type PreviewRequest struct {
	CommunityID int64
	// Selected 勾选明细（条目 key → 是否勾选）；nil 表示全选
	Selected map[string]bool
	// TargetUserPhone 指定导入会员手机号（用户券/购物车落到该会员名下；空 = 未指定）
	TargetUserPhone string
	// RequireTargetUser 应用中心路径置 true：未指定会员时用户侧数据段禁止导入；
	// 初始化链路保持 false，仍按种子条目的 user_phone 落演示用户
	RequireTargetUser bool
}

// ImportRequest 执行导入入参
type ImportRequest struct {
	CommunityID int64
	// CommunityKey 目标社区在包内的 key（初始化流程逐社区导入时显式带入）；
	// 应用中心路径为空，数据集为全部社区域条目（SectionScopeAll），见 communityScopeKey
	CommunityKey string
	Selected     map[string]bool // nil = 全选
	Excluded     map[string]bool // 预检后手动排除的条目
	OnSale       bool            // 导入后商品状态
	// TargetUserPhone 指定导入会员手机号（用户券/购物车落到该会员名下；空 = 未指定）
	TargetUserPhone string
	// RequireTargetUser 应用中心路径置 true：未指定会员时用户侧数据段禁止导入
	RequireTargetUser bool
	// Refs 跨条目 key → 真实 ID 映射：catalog 导入时写入，下游条目（营销/秒杀）读取
	Refs *SectionRefs
}

// SectionRefs 条目间引用映射
type SectionRefs struct {
	m map[string]int64
}

// Set 写入映射
func (r *SectionRefs) Set(key string, id int64) {
	if r.m == nil {
		r.m = make(map[string]int64)
	}
	r.m[key] = id
}

// Get 读取映射
func (r *SectionRefs) Get(key string) (int64, bool) {
	if r == nil || r.m == nil {
		return 0, false
	}
	id, ok := r.m[key]
	return id, ok
}

// Clone 深拷贝映射（初始化流程：全局段映射带入各社区的社区域导入）
func (r *SectionRefs) Clone() *SectionRefs {
	c := &SectionRefs{}
	if r != nil && r.m != nil {
		c.m = make(map[string]int64, len(r.m))
		for k, v := range r.m {
			c.m[k] = v
		}
	}
	return c
}

// PackageSection 单类数据条目：预检只读、导入按条目独立事务、单条失败不中断整包
type PackageSection interface {
	Key() string
	Name() string
	Description() string
	// Scope 数据归属：SectionScopeGlobal=全局种子（仅初始化导入，应用中心不显示）
	Scope() string
	// InitOnly 仅随初始化导入（如演示订单样本），应用中心不显示但初始化会执行
	InitOnly() bool
	// ItemCount 可导入条目数（应用中心列表展示用）
	ItemCount(pkg *SeedPackage) int32
	// Preview 预检：对目标社区做存在性与依赖校验，items 按包内顺序追加
	Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem)
	// Import 执行导入：items 追加每条结果（created/skipped/failed），返回成功创建数
	Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32)
}

// isCommunitySection 该段是否对应用中心可见/可导（社区域且非仅初始化）
func isCommunitySection(sec PackageSection) bool {
	return sec.Scope() == SectionScopeCommunity && !sec.InitOnly()
}

// DataImportService 数据导入业务服务
type DataImportService struct {
	db           *mysql.Instance
	productSvc   *ProductService
	bannerSvc    *BannerService
	marketingSvc *MarketingService
	packages     map[string]*SeedPackage
	// demoPkg 演示包（SeedDemoBootstrap 初始化流程使用）
	demoPkg *SeedPackage
	// globalSections 全局段（按依赖顺序：社区 → 区划 → 账号 → 用户 → 站点）
	globalSections []PackageSection
	// communitySections 社区域段（按依赖顺序：目录 → Banner → 营销 → 满免邮 → 用户侧数据
	// → 采购/收银 → 订单 → 售后 → 进销存；后四段 init_only 仅随初始化灌入）
	communitySections []PackageSection
}

// NewDataImportService 创建数据导入服务（解析并注册内置数据包）
func NewDataImportService(db *mysql.Instance, productSvc *ProductService, bannerSvc *BannerService, marketingSvc *MarketingService) *DataImportService {
	s := &DataImportService{
		db:           db,
		productSvc:   productSvc,
		bannerSvc:    bannerSvc,
		marketingSvc: marketingSvc,
		packages:     make(map[string]*SeedPackage),
	}
	if pkg := parseSeedPackage(seedPackageFullDemo); pkg != nil {
		s.demoPkg = pkg
		s.globalSections = []PackageSection{
			&communitiesSection{svc: s},
			&regionsSection{svc: s},
			&accountsSection{svc: s},
			&usersSection{svc: s},
			&siteSection{svc: s},
		}
		s.communitySections = []PackageSection{
			&catalogSection{svc: s},
			&bannersSection{svc: s},
			&marketingSection{svc: s},
			&freeShippingSection{svc: s},
			&userAssetsSection{svc: s},
			&procurementSection{svc: s},
			&posSection{svc: s},
			&ordersSection{svc: s},
			&refundsSection{svc: s},
			&inventorySection{svc: s},
		}
		pkg.sections = append(append([]PackageSection{}, s.globalSections...), s.communitySections...)
		s.packages[pkg.Key] = pkg
	}
	return s
}

// parseSeedPackage 解析嵌入的数据包 JSON；非法数据返回 nil（编译期固化的数据，解析失败即缺陷）
func parseSeedPackage(data []byte) *SeedPackage {
	var pkg SeedPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	return &pkg
}

// ListPackages 包列表（应用中心展示；仅社区域且非仅初始化的段）
func (s *DataImportService) ListPackages() (*product.ListDataPackagesResp, error) {
	resp := &product.ListDataPackagesResp{}
	for _, pkg := range s.packages {
		info := &product.DataPackageInfo{
			Key:         pkg.Key,
			Name:        pkg.Name,
			Description: pkg.Description,
		}
		for _, sec := range pkg.sections {
			// 全局段/仅初始化段是环境初始化种子，应用中心不展示
			if !isCommunitySection(sec) {
				continue
			}
			info.Sections = append(info.Sections, &product.DataPackageSectionInfo{
				Key:         sec.Key(),
				Name:        sec.Name(),
				Description: sec.Description(),
				ItemCount:   sec.ItemCount(pkg),
			})
		}
		resp.Packages = append(resp.Packages, info)
	}
	return resp, nil
}

// Preview 预检报告（仅社区域且非仅初始化的段参与）
func (s *DataImportService) Preview(ctx context.Context, packageKey string, req *PreviewRequest) (*product.DataImportPreviewResp, error) {
	pkg, err := s.getPackage(packageKey)
	if err != nil {
		return nil, err
	}
	if req == nil {
		req = &PreviewRequest{}
	}
	resp := &product.DataImportPreviewResp{}
	for _, sec := range pkg.sections {
		if !isCommunitySection(sec) {
			continue
		}
		sec.Preview(ctx, pkg, req, &resp.Items)
	}
	return resp, nil
}

// Import 执行导入：按条目粒度独立事务，单条失败记录错误并继续（仅社区域且非仅初始化的段参与）
func (s *DataImportService) Import(ctx context.Context, packageKey string, req *ImportRequest) (*product.DataImportResp, error) {
	pkg, err := s.getPackage(packageKey)
	if err != nil {
		return nil, err
	}
	if req == nil {
		req = &ImportRequest{}
	}
	req.Refs = &SectionRefs{}
	resp := &product.DataImportResp{}
	for _, sec := range pkg.sections {
		if !isCommunitySection(sec) {
			continue
		}
		resp.Created += sec.Import(ctx, pkg, req, &resp.Details)
	}
	resp.Skipped = countImportItems(resp.Details, "skipped")
	resp.Failed = countImportItems(resp.Details, "failed")
	return resp, nil
}

// getPackage 按包 key 取已注册数据包
func (s *DataImportService) getPackage(key string) (*SeedPackage, error) {
	pkg, ok := s.packages[key]
	if !ok {
		return nil, bizerr.New(codes.InvalidArgument, "数据包不存在或未启用")
	}
	return pkg, nil
}

// countImportItems 统计指定状态的条目数
func countImportItems(items []*product.DataImportItem, status string) int32 {
	var n int32
	for _, it := range items {
		if it.Status == status {
			n++
		}
	}
	return n
}

// isSelected 判断条目是否勾选（未提供勾选明细时视为全选）
func isSelected(req *PreviewRequest, key string) bool {
	if req == nil || len(req.Selected) == 0 {
		return true
	}
	return req.Selected[key]
}

// isImportable 判断条目是否应执行导入（勾选且未被排除）
func isImportable(req *ImportRequest, key string) bool {
	if len(req.Selected) > 0 && !req.Selected[key] {
		return false
	}
	return !req.Excluded[key]
}

// appendPreviewItem 追加一条预检结果
func appendPreviewItem(items *[]*product.DataImportItem, section, key, name, action, detail string, selected bool) {
	*items = append(*items, &product.DataImportItem{
		SectionKey: section,
		ItemKey:    key,
		Name:       name,
		Action:     action,
		Detail:     detail,
		Selected:   selected,
	})
}

// appendImportResult 追加一条导入结果（status: created/skipped/failed）
func appendImportResult(items *[]*product.DataImportItem, section, key, name, status, message string) {
	*items = append(*items, &product.DataImportItem{
		SectionKey: section,
		ItemKey:    key,
		Name:       name,
		Action:     status,
		Status:     status,
		Message:    message,
	})
}

// categoryIDByName 按名称查目标社区分类 ID（预检与导入共用；种子分类跨社区同名按名称对齐）
func (s *DataImportService) categoryIDByName(ctx context.Context, communityID int64, name string) (int64, error) {
	var cat model.Category
	err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND name = ?", communityID, name).
		First(&cat).Error
	if err != nil {
		return 0, err
	}
	return cat.ID, nil
}

// skuExistsByBarcode 目标社区是否已有同条码 SKU（条码社区内唯一）
func (s *DataImportService) skuExistsByBarcode(ctx context.Context, communityID int64, barcode string) (bool, error) {
	var n int64
	err := s.db.GetDb().WithContext(ctx).Model(&model.Sku{}).
		Where("community_id = ? AND barcode = ?", communityID, barcode).
		Count(&n).Error
	return n > 0, err
}

// userIDByPhone 按手机号解析 C 端用户 ID（users 全局段种子以 phone 为幂等键）
// 空手机号一律拒绝：users.phone 未绑定时存 NULL，而空串在查询里是个具体值，
// 不带手机号就查会把历史遗留的空串行当成命中用户，引用错人
func (s *DataImportService) userIDByPhone(ctx context.Context, phone string) (int64, error) {
	if phone == "" {
		return 0, bizerr.New(codes.InvalidArgument, "手机号为空，无法按 phone 解析会员")
	}
	var row userModel.User
	err := s.db.GetDb().WithContext(ctx).
		Where("phone = ?", phone).First(&row).Error
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}

// productIDByName 按社区内商品名解析商品 ID（跨段引用的社区稳定标识）
func (s *DataImportService) productIDByName(ctx context.Context, communityID int64, name string) (int64, error) {
	var row model.Product
	err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND name = ?", communityID, name).First(&row).Error
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}

// couponIDByName 按社区内券名解析优惠券 ID
func (s *DataImportService) couponIDByName(ctx context.Context, communityID int64, name string) (int64, error) {
	var row model.Coupon
	err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND name = ?", communityID, name).First(&row).Error
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}

// seedSkuByKey 按包内 key 索引种子 SKU
func seedSkuByKey(pkg *SeedPackage) map[string]*SeedSku {
	m := make(map[string]*SeedSku, len(pkg.Skus))
	for i := range pkg.Skus {
		m[pkg.Skus[i].Key] = &pkg.Skus[i]
	}
	return m
}

// seedProductByKey 按包内 key 索引种子商品
func seedProductByKey(pkg *SeedPackage) map[string]*SeedProduct {
	m := make(map[string]*SeedProduct, len(pkg.Products))
	for i := range pkg.Products {
		m[pkg.Products[i].Key] = &pkg.Products[i]
	}
	return m
}

// dateOffsetString 以今天为基准换算相对日期（格式 2006-01-02）
func dateOffsetString(days int) string {
	return time.Now().AddDate(0, 0, days).Format("2006-01-02")
}

// datetimeOffsetString 以当前时刻为基准换算相对时间（格式 2006-01-02 15:04:05）
func datetimeOffsetString(minutes int) string {
	return time.Now().Add(time.Duration(minutes) * time.Minute).Format("2006-01-02 15:04:05")
}

// communityScopeKey 本次导入的归属社区 key：
//   - 初始化流程（SeedDemoBootstrap）逐社区导入，带入 req.CommunityKey；
//   - 应用中心单社区导入不指定包内社区：2026-10-27 起两社区商品名已全部唯一，
//     数据集口径放宽为 SectionScopeAll（包内全部社区域条目都参与）；
//     原「主演示社区（communities 首项）」口径废止——同名不同价样本已全部改名消除冲突。
//
// 参数：pkg 数据包；reqCommunityKey 请求显式指定的归属（可为空）
// 返回值：本次生效的归属社区 key（SectionScopeAll=全部）
func communityScopeKey(pkg *SeedPackage, reqCommunityKey string) string {
	if reqCommunityKey != "" {
		return reqCommunityKey
	}
	return SectionScopeAll
}

// seedInCommunityScope 条目的社区归属判定：scopeKey=SectionScopeAll 时全部放行
// （应用中心数据集口径）；否则空 community_key 视为通用条目（任何社区可导，
// 如平台级 Banner 与跨社区共用文案），其余必须落在本次导入的归属社区
// 参数：itemCommunityKey 条目标注的归属；scopeKey 本次导入归属（见 communityScopeKey）
// 返回值：是否应参与本次预检/导入
func seedInCommunityScope(itemCommunityKey, scopeKey string) bool {
	if scopeKey == SectionScopeAll {
		return true
	}
	return itemCommunityKey == "" || itemCommunityKey == scopeKey
}

// seedDepSelected 跨段依赖的预检兜底口径：库内暂无该对象，但包内存在同名条目且随本次
// 勾选一起导入时，依赖会在同一次执行里先落库（段顺序为目录 → 营销 → 免邮 → 用户资产），
// 因此不应判为 depend_missing。导入到空社区时全选场景即靠此避免误报。
// 参数：names 包内同域条目的「名称 → key」列表；scopeKey 本次归属社区；
// selected 勾选判定（isSelected）；name 依赖对象名称
// 返回值：该依赖是否将由本次导入产出
func seedDepSelected(name string, keys []seedNameKey, scopeKey string, selected func(string) bool) bool {
	for _, nk := range keys {
		if nk.Name == name && seedInCommunityScope(nk.CommunityKey, scopeKey) && selected(nk.Key) {
			return true
		}
	}
	return false
}

// seedNameKey 包内条目的「名称 + key + 归属社区」三元组，供 seedDepSelected 做依赖兜底
type seedNameKey struct {
	Name         string
	Key          string
	CommunityKey string
}

// seedCategoryNameKeys / seedProductKeys / seedCouponKeys 提取各段条目的名称索引
func seedCategoryNameKeys(pkg *SeedPackage) []seedNameKey {
	out := make([]seedNameKey, 0, len(pkg.Categories))
	for i := range pkg.Categories {
		c := &pkg.Categories[i]
		out = append(out, seedNameKey{Name: c.Name, Key: c.Key, CommunityKey: c.CommunityKey})
	}
	return out
}

func seedProductKeys(pkg *SeedPackage) []seedNameKey {
	out := make([]seedNameKey, 0, len(pkg.Products))
	for i := range pkg.Products {
		p := &pkg.Products[i]
		out = append(out, seedNameKey{Name: p.Name, Key: p.Key, CommunityKey: p.CommunityKey})
	}
	return out
}

func seedCouponKeys(pkg *SeedPackage) []seedNameKey {
	out := make([]seedNameKey, 0, len(pkg.Coupons))
	for i := range pkg.Coupons {
		c := &pkg.Coupons[i]
		out = append(out, seedNameKey{Name: c.Name, Key: c.Key, CommunityKey: c.CommunityKey})
	}
	return out
}

// resolveSeedOperatorRef 解析演示样本的操作者引用（订单状态日志/单据操作人）：
// operator_type=system 恒为 0；user/admin 按 refs（key → ID）解析，兜底经包内
// 自然键（用户手机号/管理员用户名）查库——refs 仅本次运行有效，兜底保证独立可复现
func (s *DataImportService) resolveSeedOperatorRef(ctx context.Context, pkg *SeedPackage, refs *SectionRefs, operatorType, operatorKey string) (int64, error) {
	switch operatorType {
	case "system", "":
		return 0, nil
	case "user":
		if id, ok := refs.Get(operatorKey); ok && id > 0 {
			return id, nil
		}
		for i := range pkg.Users {
			if pkg.Users[i].Key == operatorKey {
				return s.userIDByPhone(ctx, pkg.Users[i].Phone)
			}
		}
		return 0, bizerr.New(codes.NotFound, "演示用户不存在："+operatorKey)
	case "admin":
		if id, ok := refs.Get(operatorKey); ok && id > 0 {
			return id, nil
		}
		for i := range pkg.Admins {
			if pkg.Admins[i].Key == operatorKey {
				var row userModel.Admin
				if err := s.db.GetDb().WithContext(ctx).
					Where("username = ?", pkg.Admins[i].Username).First(&row).Error; err != nil {
					return 0, err
				}
				return row.ID, nil
			}
		}
		return 0, bizerr.New(codes.NotFound, "演示管理员不存在："+operatorKey)
	default:
		return 0, bizerr.New(codes.InvalidArgument, "未知操作者类型："+operatorType)
	}
}

// ============================================================
// 演示种子一次性初始化（market-admin init 直连库调用，非 HTTP 链路）
// ============================================================

// DemoSeedResult 演示种子初始化结果
type DemoSeedResult struct {
	// Ran 是否执行（communities 表非空时跳过，与原 init.dev.sql「仅新库生效」口径一致）
	Ran bool
	// Created/Skipped/Failed 全部段落的条目计数汇总
	Created int32
	Skipped int32
	Failed  int32
	// DemoAdminCreated 本次是否新建了演示超管（供 init 写入凭证文件）
	DemoAdminCreated bool
	// DemoAdminUsername/DemoAdminPassword 新建演示超管的账号明文（仅 DemoAdminCreated 时有效）
	DemoAdminUsername string
	DemoAdminPassword string
}

// SeedDemoBootstrap 演示种子初始化：先导全局段（社区/区划/账号/用户/站点），
// 再对每个演示社区导一遍社区域段（目录/Banner/营销/满免邮/用户侧数据/采购/收银/
// 订单/售后/进销存），各段按条目的 community_key 只落自身归属社区。
// 口径与原 init.dev.sql 一致：仅在 communities 表为空时执行，
// 已有业务数据一律跳过；是否可导入（环境/开关）由调用方（market-admin init）判定。
func (s *DataImportService) SeedDemoBootstrap(ctx context.Context, logf func(string, ...any)) (*DemoSeedResult, error) {
	res := &DemoSeedResult{}
	if s.demoPkg == nil {
		return nil, bizerr.New(codes.Internal, "演示数据包未注册（seeddata 缺失或解析失败）")
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}

	var communityCount int64
	if err := s.db.GetDb().WithContext(ctx).Model(&userModel.Community{}).Count(&communityCount).Error; err != nil {
		return nil, err
	}
	if communityCount > 0 {
		logf("库中已有 %d 个社区，视为已初始化，跳过演示种子导入", communityCount)
		return res, nil
	}
	res.Ran = true
	logf("空库，开始导入演示种子包 %q（%s）...", s.demoPkg.Key, s.demoPkg.Name)

	// 演示超管是否本次新建：导入前记录账号存在性，导入后比对
	demoAdmin := s.findDemoSuperAdmin(s.demoPkg)

	created, skipped, failed := s.runAllDemoSections(ctx, logf)
	res.Created, res.Skipped, res.Failed = created, skipped, failed

	// 演示超管凭证上报（供调用方写入凭证文件）
	if after := s.findDemoSuperAdmin(s.demoPkg); demoAdmin == nil && after != nil {
		for i := range s.demoPkg.Admins {
			a := &s.demoPkg.Admins[i]
			if a.IsSuper && a.Username == after.Username {
				res.DemoAdminCreated = true
				res.DemoAdminUsername = a.Username
				res.DemoAdminPassword = a.Password
			}
		}
	}
	return res, nil
}

// ReRunDemoSections 幂等复跑全部演示段（全局段 + 每个演示社区的社区域段，含 init_only）。
// 与 SeedDemoBootstrap 的区别：不做「空库短路」，逐条按各自幂等键判重——
// 供初始化中断重试与端到端验证使用；已有数据全部 skip，无副作用。
func (s *DataImportService) ReRunDemoSections(ctx context.Context, logf func(string, ...any)) (created, skipped, failed int32) {
	if s.demoPkg == nil {
		return 0, 0, 0
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return s.runAllDemoSections(ctx, logf)
}

// runAllDemoSections 导入主体：全局段 → 演示社区内置角色补建 → 各社区社区域段。
// 各段各自幂等，可安全重复执行。
func (s *DataImportService) runAllDemoSections(ctx context.Context, logf func(string, ...any)) (created, skipped, failed int32) {
	// 1. 全局段：社区/区划/账号/用户/站点（跨段映射进 globalRefs）
	globalRefs := &SectionRefs{}
	for _, sec := range s.globalSections {
		c, sk, fl := s.runSection(ctx, sec, &ImportRequest{Refs: globalRefs, OnSale: true}, logf)
		created += c
		skipped += sk
		failed += fl
	}

	// 为每个演示社区补建内置社区管理员角色（任职绑定与员工管理依赖；
	// market-admin init 的 backfillCommunityAdminRoles 也会兜底，这里保证种子自洽）
	roleSvc := userService.NewRoleService(s.db)
	for i := range s.demoPkg.Communities {
		comm := &s.demoPkg.Communities[i]
		if id, ok := globalRefs.Get(comm.Key); ok {
			if _, err := roleSvc.EnsureCommunityAdminRole(ctx, id); err != nil {
				logf("社区 %q 补建内置角色失败: %v", comm.Name, err)
			}
		}
	}

	// 2. 社区域段：对每个演示社区导一遍（全局段映射 Clone 带入，
	//    供用户侧数据解析 user_key → ID；社区域 key 映射互不干扰、随 Clone 隔离）
	for i := range s.demoPkg.Communities {
		comm := &s.demoPkg.Communities[i]
		communityID, ok := globalRefs.Get(comm.Key)
		if !ok {
			logf("社区 %q 未获取到 ID（全局段导入失败），跳过其社区域数据", comm.Name)
			continue
		}
		logf("---- 社区 %q（ID: %d）社区域数据 ----", comm.Name, communityID)
		req := &ImportRequest{CommunityID: communityID, CommunityKey: comm.Key, Refs: globalRefs.Clone(), OnSale: true}
		for _, sec := range s.communitySections {
			c, sk, fl := s.runSection(ctx, sec, req, logf)
			created += c
			skipped += sk
			failed += fl
		}
	}
	return created, skipped, failed
}

// runSection 执行单个段落的导入，逐条输出结果日志并返回 created/skipped/failed 计数
func (s *DataImportService) runSection(ctx context.Context, sec PackageSection, req *ImportRequest, logf func(string, ...any)) (created, skipped, failed int32) {
	details := &[]*product.DataImportItem{}
	created = sec.Import(ctx, s.demoPkg, req, details)
	for _, it := range *details {
		switch it.Status {
		case "failed":
			failed++
			logf("    [%s] %s 失败: %s", sec.Key(), it.Name, it.Message)
		case "skipped":
			skipped++
			logf("    [%s] %s 跳过: %s", sec.Key(), it.Name, it.Message)
		default:
			logf("    [%s] %s 完成", sec.Key(), it.Name)
		}
	}
	return created, skipped, failed
}

// findDemoSuperAdmin 查询包内首个演示超管账号在库中的存在性
func (s *DataImportService) findDemoSuperAdmin(pkg *SeedPackage) *userModel.Admin {
	for i := range pkg.Admins {
		if !pkg.Admins[i].IsSuper {
			continue
		}
		var row userModel.Admin
		if err := s.db.GetDb().
			Where("username = ?", pkg.Admins[i].Username).First(&row).Error; err != nil {
			return nil
		}
		return &row
	}
	return nil
}
