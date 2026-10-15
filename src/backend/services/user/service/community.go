package service

import (
	"context"
	"fmt"
	"math"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

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
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

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
		return nil, fmt.Errorf("社区不存在")
	}
	return &c, err
}

// ListNearbyCommunities 查询附近社区（按距离排序，固定返回前 10 个）
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
	err := db.Order(fmt.Sprintf(
		"POWER(longitude - %v, 2) + POWER(latitude - %v, 2) ASC, id asc",
		longitude, latitude,
	)).Limit(limit).Find(&list).Error
	return list, int(total), err
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
		return nil, fmt.Errorf("社区名称不能为空")
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
	if err := s.db.GetDb().WithContext(ctx).Save(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// DeleteCommunity 删除社区
// 参数：
//   - ctx: 上下文
//   - id: 社区 ID
//
// 返回：
//   - error: 错误信息
func (s *CommunityService) DeleteCommunity(ctx context.Context, id int64) error {
	return s.db.GetDb().WithContext(ctx).Delete(&model.Community{}, id).Error
}

// earthDistanceKm 计算地球表面两点间距离（单位：公里）
// 参数：
//   - lat1, lon1: 第一点经纬度
//   - lat2, lon2: 第二点经纬度
//
// 返回：
//   - float64: 距离公里数
func earthDistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	φ1 := lat1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	Δφ := (lat2 - lat1) * math.Pi / 180
	Δλ := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(Δφ/2)*math.Sin(Δφ/2) +
		math.Cos(φ1)*math.Cos(φ2)*
			math.Sin(Δλ/2)*math.Sin(Δλ/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return r * c
}

// _ 占位，避免 common 未使用告警
var _ = (*common.Pagination)(nil)
