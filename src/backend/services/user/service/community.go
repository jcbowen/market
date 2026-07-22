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
	db *mysql.Instance
}

// NewCommunityService 创建社区业务服务
// 参数：
//   - db: MySQL 实例
// 返回：
//   - *CommunityService: 社区业务服务实例
func NewCommunityService(db *mysql.Instance) *CommunityService {
	return &CommunityService{db: db}
}

// ListCommunities 分页查询社区列表
// 参数：
//   - ctx: 上下文
//   - page: 页码
//   - pageSize: 每页条数
// 返回：
//   - []*model.Community: 社区列表
//   - int: 总条数
//   - error: 错误信息
func (s *CommunityService) ListCommunities(ctx context.Context, page, pageSize int) ([]*model.Community, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	var total int64
	var list []*model.Community
	db := s.db.GetDb().WithContext(ctx).Where("status = ?", 1)
	if err := db.Model(&model.Community{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	err := db.Order("sort desc, id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// GetCommunity 查询社区详情
// 参数：
//   - ctx: 上下文
//   - id: 社区 ID
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
	// 按经纬度平面距离近似排序，固定返回最近的 10 个社区
	err := db.Order(fmt.Sprintf(
		"POWER(longitude - %v, 2) + POWER(latitude - %v, 2) ASC, sort desc, id desc",
		longitude, latitude,
	)).Limit(limit).Find(&list).Error
	return list, int(total), err
}

// ToCommunityInfo 将社区模型转换为 proto 社区信息
// 参数：
//   - c: 社区模型
// 返回：
//   - *user.CommunityInfo: proto 社区信息
func ToCommunityInfo(c *model.Community) *user.CommunityInfo {
	if c == nil {
		return nil
	}
	return &user.CommunityInfo{
		Id:              c.ID,
		Name:            c.Name,
		Address:         c.Address,
		Longitude:       c.Longitude,
		Latitude:        c.Latitude,
		ContactPhone:    c.ContactPhone,
		PickupAddress:   c.PickupAddress,
		DeliveryFee:     c.DeliveryFee,
		MinOrderAmount:  c.MinOrderAmount,
		DeliveryRangeKm: c.DeliveryRangeKm,
		BusinessHours:   c.BusinessHours,
		Sort:            c.Sort,
		Status:          c.Status,
	}
}

// CreateCommunity 创建社区
// 参数：
//   - ctx: 上下文
//   - req: 创建社区请求
// 返回：
//   - *model.Community: 社区模型
//   - error: 错误信息
func (s *CommunityService) CreateCommunity(ctx context.Context, req *user.CreateCommunityReq) (*model.Community, error) {
	c := &model.Community{
		Name:            req.Name,
		Address:         req.Address,
		Longitude:       req.Longitude,
		Latitude:        req.Latitude,
		ContactPhone:    req.ContactPhone,
		PickupAddress:   req.PickupAddress,
		DeliveryFee:     req.DeliveryFee,
		MinOrderAmount:  req.MinOrderAmount,
		DeliveryRangeKm: req.DeliveryRangeKm,
		BusinessHours:   req.BusinessHours,
		Sort:            req.Sort,
		Status:          req.Status,
	}
	if c.Status == 0 {
		c.Status = 1
	}
	if err := s.db.GetDb().WithContext(ctx).Create(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateCommunity 更新社区
// 参数：
//   - ctx: 上下文
//   - req: 更新社区请求
// 返回：
//   - *model.Community: 社区模型
//   - error: 错误信息
func (s *CommunityService) UpdateCommunity(ctx context.Context, req *user.UpdateCommunityInfoReq) (*model.Community, error) {
	c, err := s.GetCommunity(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if req.Name != "" {
		c.Name = req.Name
	}
	if req.Address != "" {
		c.Address = req.Address
	}
	if req.ContactPhone != "" {
		c.ContactPhone = req.ContactPhone
	}
	if req.PickupAddress != "" {
		c.PickupAddress = req.PickupAddress
	}
	if req.BusinessHours != "" {
		c.BusinessHours = req.BusinessHours
	}
	c.Longitude = req.Longitude
	c.Latitude = req.Latitude
	c.DeliveryFee = req.DeliveryFee
	c.MinOrderAmount = req.MinOrderAmount
	c.DeliveryRangeKm = req.DeliveryRangeKm
	c.Sort = req.Sort
	if req.Status > 0 {
		c.Status = req.Status
	}
	if err := s.db.GetDb().WithContext(ctx).Save(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// DeleteCommunity 删除社区
// 参数：
//   - ctx: 上下文
//   - id: 社区 ID
// 返回：
//   - error: 错误信息
func (s *CommunityService) DeleteCommunity(ctx context.Context, id int64) error {
	return s.db.GetDb().WithContext(ctx).Delete(&model.Community{}, id).Error
}

// earthDistanceKm 计算地球表面两点间距离（单位：公里）
// 参数：
//   - lat1, lon1: 第一点经纬度
//   - lat2, lon2: 第二点经纬度
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
