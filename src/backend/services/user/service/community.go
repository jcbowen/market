package service

import (
	"context"
	"fmt"
	"math"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/pageparam"
	"github.com/jcbowen/market/src/backend/common/timeutil"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// CommunityService 社区业务服务
type CommunityService struct {
	db          *mysql.Instance
	roleService *RoleService
}

// NewCommunityService 创建社区业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回：
//   - *CommunityService: 社区业务服务实例
func NewCommunityService(db *mysql.Instance) *CommunityService {
	return &CommunityService{db: db, roleService: NewRoleService(db)}
}

// ListCommunities 分页查询社区列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求（含分页、是否包含停业社区、状态筛选）
//
// 语义：req 为 nil 或 IncludeDisabled=false 时仅返回营业中社区（客户端语义），
// 按创建时间升序（id asc）排序兜底；
// IncludeDisabled=true 时（管理端语义）按 Status 筛选，-1 表示全部，保持创建时间倒序。
// 返回：
//   - []*model.Community: 社区列表
//   - int: 总条数
//   - error: 错误信息
func (s *CommunityService) ListCommunities(ctx context.Context, req *user.ListCommunitiesReq) ([]*model.Community, int, error) {
	page, pageSize := 1, 10
	if req != nil && req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	// 批量档位：社区选择器与超管总览要一次取全量（网关按 500 请求），社区表规模有限
	page, pageSize = pageparam.ClampBatch(page, pageSize)

	var total int64
	var list []*model.Community
	db := s.db.GetDb().WithContext(ctx)
	switch {
	case req == nil || !req.IncludeDisabled:
		db = db.Where("status = ?", 1)
	case req.Status >= 0:
		db = db.Where("status = ?", req.Status)
	}
	if err := db.Model(&model.Community{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	// 排序字段已废弃：C 端有定位时按距离排序（见 ListNearbyCommunities），
	// 默认列表按创建时间升序（id asc）兜底；管理端保持创建时间倒序
	order := "id asc"
	if req != nil && req.IncludeDisabled {
		order = "id desc"
	}
	err := db.Order(order).Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// GetCommunity 查询社区详情
// 参数：
//   - ctx: 上下文
//   - id: 社区 ID
//
// 返回：
//   - *model.Community: 社区模型
//   - error: 错误信息
func (s *CommunityService) GetCommunity(ctx context.Context, id int64) (*model.Community, error) {
	var c model.Community
	err := s.db.GetDb().WithContext(ctx).First(&c, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, bizerr.New(codes.NotFound, "社区不存在")
	}
	return &c, err
}

// ListNearbyCommunities 查询附近社区（按距离排序，固定返回前 10 个）
// 经纬度非有限值（NaN/±Inf）时不做距离排序，退化为按 id 升序取前 10：
// 这两个值可由 ?longitude=NaN 直达（strconv.ParseFloat 认 NaN/Inf 为合法浮点字面量，
// 且不报错），拼进 SQL 后 MySQL 把 NaN 当列名、把 +Inf 当语法错误，公开列表接口直接 500
// 参数：
//   - ctx: 上下文
//   - longitude: 经度
//   - latitude: 纬度
//   - page: 页码（已废弃，仅保留兼容签名）
//   - pageSize: 每页条数（已废弃，仅保留兼容签名）
//
// 返回：
//   - []*model.Community: 社区列表
//   - int: 总条数
//   - error: 错误信息
func (s *CommunityService) ListNearbyCommunities(ctx context.Context, longitude, latitude float64, page, pageSize int) ([]*model.Community, int, error) {
	const limit = 10

	var total int64
	db := s.db.GetDb().WithContext(ctx).Where("status = ?", 1)
	if err := db.Model(&model.Community{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []*model.Community
	// 按经纬度平面距离近似排序，固定返回最近的 10 个社区；
	// 排序字段已废弃，同距离时按创建时间升序（id asc）兜底
	err := db.Order(nearbyOrderSQL(longitude, latitude)).Limit(limit).Find(&list).Error
	return list, int(total), err
}

// nearbyOrderSQL 组装附近社区的排序片段
// 只接受有限浮点数：坐标值经 %v 直接进 SQL，NaN/±Inf 会让 MySQL 报错而非退化排序
// 参数：
//   - longitude: 经度
//   - latitude: 纬度
//
// 返回值：
//   - string: ORDER BY 片段（非法坐标时退化为 id asc）
//
// 使用示例：
//   - db.Order(nearbyOrderSQL(120.1, 30.2))
func nearbyOrderSQL(longitude, latitude float64) string {
	if isNonFinite(longitude) || isNonFinite(latitude) {
		return "id asc"
	}
	return fmt.Sprintf("POWER(longitude - %v, 2) + POWER(latitude - %v, 2) ASC, id asc", longitude, latitude)
}

// isNonFinite 判定浮点数是否为 NaN 或 ±Inf
// 参数：
//   - v: 待判定值
//
// 返回值：
//   - bool: 非有限值时为 true
func isNonFinite(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0)
}

// ToCommunityInfo 将社区模型转换为 proto 社区信息
// 参数：
//   - c: 社区模型
//
// 返回：
//   - *user.CommunityInfo: proto 社区信息
func ToCommunityInfo(c *model.Community) *user.CommunityInfo {
	if c == nil {
		return nil
	}
	return &user.CommunityInfo{
		Id:                   c.ID,
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
		Sort:                 c.Sort,
		Status:               c.Status,
		Province:             c.Province,
		City:                 c.City,
		District:             c.District,
		ProvinceCode:         c.ProvinceCode,
		CityCode:             c.CityCode,
		DistrictCode:         c.DistrictCode,
		AllowOutsideDelivery: c.AllowOutsideDelivery,
		EnableSelfPickup:     c.EnableSelfPickup,
		EnableDelivery:       c.EnableDelivery,
	}
}

// CreateCommunity 创建社区
// 参数：
//   - ctx: 上下文
//   - req: 创建社区请求
//
// 返回：
//   - *model.Community: 社区模型
//   - error: 错误信息
func (s *CommunityService) CreateCommunity(ctx context.Context, req *user.CreateCommunityReq) (*model.Community, error) {
	c := &model.Community{
		Name:                 req.Name,
		Address:              req.Address,
		Longitude:            req.Longitude,
		Latitude:             req.Latitude,
		ContactPhone:         req.ContactPhone,
		ManagerName:          req.ManagerName,
		ManagerPhone:         req.ManagerPhone,
		PickupAddress:        req.PickupAddress,
		DeliveryFee:          req.DeliveryFee,
		MinOrderAmount:       req.MinOrderAmount,
		DeliveryRangeKm:      req.DeliveryRangeKm,
		BusinessHours:        req.BusinessHours,
		Province:             req.Province,
		City:                 req.City,
		District:             req.District,
		ProvinceCode:         req.ProvinceCode,
		CityCode:             req.CityCode,
		DistrictCode:         req.DistrictCode,
		AllowOutsideDelivery: req.AllowOutsideDelivery,
		Sort:                 req.Sort,
		Status:               req.Status,
	}
	if c.Status == 0 {
		c.Status = 1
	}
	if err := s.db.GetDb().WithContext(ctx).Create(c).Error; err != nil {
		return nil, err
	}
	// 随社区初始化内置社区管理员角色（社区权限上限的载体，默认社区级运营权限集）
	if _, err := s.roleService.EnsureCommunityAdminRole(ctx, c.ID); err != nil {
		return nil, fmt.Errorf("初始化社区管理员角色失败: %w", err)
	}
	return c, nil
}

// UpdateCommunity 更新社区
// 参数：
//   - ctx: 上下文
//   - req: 更新社区请求
//
// 返回：
//   - *model.Community: 社区模型
//   - error: 错误信息
func (s *CommunityService) UpdateCommunity(ctx context.Context, req *user.UpdateCommunityInfoReq) (*model.Community, error) {
	if req.Name == "" {
		return nil, bizerr.New(codes.InvalidArgument, "社区名称不能为空")
	}
	c, err := s.GetCommunity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	// 管理端编辑表单为整表提交，所有字段全量覆盖：
	// 保证清空文本字段、状态改为停业（0）等场景可正常保存
	c.Name = req.Name
	c.Address = req.Address
	c.ContactPhone = req.ContactPhone
	c.ManagerName = req.ManagerName
	c.ManagerPhone = req.ManagerPhone
	c.PickupAddress = req.PickupAddress
	c.BusinessHours = req.BusinessHours
	c.Longitude = req.Longitude
	c.Latitude = req.Latitude
	c.DeliveryFee = req.DeliveryFee
	c.MinOrderAmount = req.MinOrderAmount
	c.DeliveryRangeKm = req.DeliveryRangeKm
	c.Province = req.Province
	c.City = req.City
	c.District = req.District
	c.ProvinceCode = req.ProvinceCode
	c.CityCode = req.CityCode
	c.DistrictCode = req.DistrictCode
	c.AllowOutsideDelivery = req.AllowOutsideDelivery
	// 取货渠道开关：门店设置（白名单接口）维护；超管社区编辑未传时由网关回填当前值，避免整表覆盖误关
	c.EnableSelfPickup = req.EnableSelfPickup
	c.EnableDelivery = req.EnableDelivery
	c.Sort = req.Sort
	c.Status = req.Status
	c.CreatedAt = timeutil.NormalizeRFC3339(c.CreatedAt)
	c.UpdatedAt = timeutil.NormalizeRFC3339(c.UpdatedAt)
	if err := s.db.GetDb().WithContext(ctx).Save(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// communityHardReferences 删除社区前必须为空的本地关联
// communities 无软删列（base.MysqlBaseModel 不含 deleted_at），DeleteCommunity 是硬删，
// 一旦有残留引用就是指向不存在社区的孤儿行；商品与订单分属 product/order 服务，
// 由网关在发起删除前各自计数（见 gateway/handler/admin.go DeleteCommunity）
var communityHardReferences = []struct {
	model any
	label string
}{
	{&model.UserCommunity{}, "会员社区归属"},
	{&model.Address{}, "收货地址"},
	{&model.AdminCommunity{}, "管理员任职"},
	{&model.Role{}, "社区角色"},
}

// DeleteCommunity 删除社区（先做引用守卫，再在同一事务内硬删）
// 阻塞条件：仍有会员归属、收货地址、管理员任职或社区角色挂在本社区（内置社区管理员角色除外，随社区一并硬删）；
// 非阻塞的浏览态引用（users.community_id 记录的是「当前所在社区」）在删除时归零，
// 由 C 端重新选择社区，避免留下悬空 ID
// 参数：
//   - ctx: 上下文
//   - id: 社区 ID
//
// 返回：
//   - error: 参数非法（InvalidArgument）、社区不存在（NotFound）、
//     仍有引用（FailedPrecondition，文案含具体引用类型与条数）或数据库错误
//
// 使用示例：
//   - err := s.communityService.DeleteCommunity(ctx, req.Id)
func (s *CommunityService) DeleteCommunity(ctx context.Context, id int64) error {
	if id <= 0 {
		return bizerr.New(codes.InvalidArgument, "缺少社区 ID")
	}
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 内置社区管理员角色是随社区创建自动补齐的（CreateCommunity → EnsureCommunityAdminRole），
		// 若计入引用守卫，等于每个社区都自带一条「删不掉的引用」，社区一旦建成就永远无法删除。
		// 这里与社区同事务硬删该内置角色；人工创建的自定义角色仍计入守卫，由操作者先清理
		if err := tx.Where("community_id = ? AND code = ?", id, model.RoleCodeCommunityAdmin).
			Delete(&model.Role{}).Error; err != nil {
			return err
		}
		for _, ref := range communityHardReferences {
			var n int64
			if err := tx.Model(ref.model).Where("community_id = ?", id).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return bizerr.New(codes.FailedPrecondition,
					fmt.Sprintf("该社区仍有 %d 条%s，请先清理后再删除", n, ref.label))
			}
		}
		if err := tx.Model(&model.User{}).Where("community_id = ?", id).
			Update("community_id", 0).Error; err != nil {
			return err
		}
		res := tx.Delete(&model.Community{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return bizerr.New(codes.NotFound, "社区不存在")
		}
		return nil
	})
}

// _ 占位，避免 common 未使用告警
var _ = (*common.Pagination)(nil)
