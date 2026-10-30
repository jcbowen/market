package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/timeutil"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// AddressService 地址业务服务
type AddressService struct {
	db *mysql.Instance
}

// NewAddressService 创建地址业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回：
//   - *AddressService: 地址业务服务实例
func NewAddressService(db *mysql.Instance) *AddressService {
	return &AddressService{db: db}
}

// ListAddresses 查询用户地址列表
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//
// 返回：
//   - []*model.Address: 地址列表
//   - map[int64]string: 地址所属社区 ID -> 社区名称（供按社区分组展示）
//   - error: 错误信息
func (s *AddressService) ListAddresses(ctx context.Context, userID int64) ([]*model.Address, map[int64]string, error) {
	var list []*model.Address
	err := s.db.GetDb().WithContext(ctx).
		Where("user_id = ?", userID).
		Order("is_default desc, id desc").
		Find(&list).Error
	if err != nil {
		return nil, nil, err
	}

	ids := make([]int64, 0, len(list))
	for _, addr := range list {
		if addr.CommunityID > 0 {
			ids = append(ids, addr.CommunityID)
		}
	}
	return list, s.ListCommunityNames(ctx, ids), nil
}

// ListCommunityNames 批量查询社区名称
// 参数：
//   - ctx: 上下文
//   - ids: 社区 ID 列表
//
// 返回：
//   - map[int64]string: 社区 ID -> 名称
func (s *AddressService) ListCommunityNames(ctx context.Context, ids []int64) map[int64]string {
	names := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return names
	}
	var communities []*model.Community
	if err := s.db.GetDb().WithContext(ctx).
		Where("id IN ?", ids).
		Select("id, name").
		Find(&communities).Error; err != nil {
		return names
	}
	for _, c := range communities {
		names[c.ID] = c.Name
	}
	return names
}

// GetCommunityName 查询单个社区名称
// 参数：
//   - ctx: 上下文
//   - id: 社区 ID
//
// 返回：
//   - string: 社区名称（不存在时为空串）
func (s *AddressService) GetCommunityName(ctx context.Context, id int64) string {
	if id <= 0 {
		return ""
	}
	return s.ListCommunityNames(ctx, []int64{id})[id]
}

// getUserCommunity 查询用户当前所属社区
// 错误一律返回业务错误（InvalidArgument），保证网关透传文案而非吞成"服务器内部错误"
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//
// 返回：
//   - *model.Community: 用户所属社区（未选择社区或社区已删除时返回错误）
//   - error: 错误信息
func (s *AddressService) getUserCommunity(ctx context.Context, userID int64) (*model.Community, error) {
	var u model.User
	if err := s.db.GetDb().WithContext(ctx).Select("id, community_id").First(&u, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, bizerr.New(codes.InvalidArgument, "用户不存在")
		}
		return nil, err
	}
	if u.CommunityID <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "请先选择社区")
	}
	var c model.Community
	if err := s.db.GetDb().WithContext(ctx).First(&c, u.CommunityID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, bizerr.New(codes.InvalidArgument, "所属社区不存在，请重新选择社区")
		}
		return nil, err
	}
	return &c, nil
}

// GetAddress 查询地址详情
// 参数：
//   - ctx: 上下文
//   - id: 地址 ID
//   - userID: 用户 ID
//
// 返回：
//   - *model.Address: 地址模型
//   - error: 错误信息
func (s *AddressService) GetAddress(ctx context.Context, id, userID int64) (*model.Address, error) {
	var addr model.Address
	err := s.db.GetDb().WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&addr).Error
	if err == gorm.ErrRecordNotFound {
		return nil, bizerr.New(codes.NotFound, "地址不存在")
	}
	return &addr, err
}

// CreateAddress 创建地址
// 语义：地址归属与省市区前缀由服务端按用户当前社区裁决——
// 社区不允许外配（默认）时强制覆盖为本社区省市区，防止绕过前端约束。
// 参数：
//   - ctx: 上下文
//   - req: 创建地址请求
//
// 返回：
//   - *model.Address: 地址模型
//   - error: 错误信息
func (s *AddressService) CreateAddress(ctx context.Context, req *user.CreateAddressReq) (*model.Address, error) {
	community, province, city, district, provinceCode, cityCode, districtCode, err :=
		s.resolveAddressRegion(ctx, req.UserId, req.Province, req.City, req.District, req.ProvinceCode, req.CityCode, req.DistrictCode)
	if err != nil {
		return nil, err
	}
	addr := &model.Address{
		UserID:       req.UserId,
		ContactName:  req.ContactName,
		ContactPhone: req.ContactPhone,
		Province:     province,
		City:         city,
		District:     district,
		ProvinceCode: provinceCode,
		CityCode:     cityCode,
		DistrictCode: districtCode,
		Detail:       req.Detail,
		Tag:          req.Tag,
		CommunityID:  community.ID,
		IsDefault:    req.IsDefault,
	}
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if addr.IsDefault {
			if err := tx.Model(&model.Address{}).
				Where("user_id = ?", req.UserId).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Create(addr).Error
	}); err != nil {
		return nil, err
	}
	// 记录会员-社区关联：在该社区添加过收货地址即视为该社区会员（幂等）
	if err := NewUserService(s.db).MarkUserCommunity(ctx, addr.UserID, addr.CommunityID, "address"); err != nil {
		return nil, err
	}
	return addr, nil
}

// UpdateAddress 更新地址
// 语义同 CreateAddress：归属社区与省市区前缀由服务端按用户当前社区裁决。
// 参数：
//   - ctx: 上下文
//   - req: 更新地址请求
//
// 返回：
//   - *model.Address: 地址模型
//   - error: 错误信息
func (s *AddressService) UpdateAddress(ctx context.Context, req *user.UpdateAddressReq) (*model.Address, error) {
	community, province, city, district, provinceCode, cityCode, districtCode, err :=
		s.resolveAddressRegion(ctx, req.UserId, req.Province, req.City, req.District, req.ProvinceCode, req.CityCode, req.DistrictCode)
	if err != nil {
		return nil, err
	}
	addr, err := s.GetAddress(ctx, req.Id, req.UserId)
	if err != nil {
		return nil, err
	}

	addr.ContactName = req.ContactName
	addr.ContactPhone = req.ContactPhone
	addr.Province = province
	addr.City = city
	addr.District = district
	addr.ProvinceCode = provinceCode
	addr.CityCode = cityCode
	addr.DistrictCode = districtCode
	addr.Detail = req.Detail
	addr.Tag = req.Tag
	addr.CommunityID = community.ID
	addr.IsDefault = req.IsDefault

	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if addr.IsDefault {
			if err := tx.Model(&model.Address{}).
				Where("user_id = ? AND id != ?", req.UserId, req.Id).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		addr.CreatedAt = timeutil.NormalizeRFC3339(addr.CreatedAt)
		addr.UpdatedAt = timeutil.NormalizeRFC3339(addr.UpdatedAt)
		return tx.Save(addr).Error
	}); err != nil {
		return nil, err
	}
	// 记录会员-社区关联：地址改绑社区时同样建立新社区的会员关联（幂等）
	if err := NewUserService(s.db).MarkUserCommunity(ctx, addr.UserID, addr.CommunityID, "address"); err != nil {
		return nil, err
	}
	return addr, nil
}

// DeleteAddress 删除地址
// 语义：SQL 带 user_id 条件保证越权行不会被真删；删除 0 行
// （地址不存在或非本人地址）时返回 NotFound 业务错误，
// 避免越权删除静默成功造成"删除他人地址返回 code=0"的安全误导
// 参数：
//   - ctx: 上下文
//   - id: 地址 ID
//   - userID: 用户 ID
//
// 返回：
//   - error: 错误信息（地址不存在或无权操作时返回 NotFound）
func (s *AddressService) DeleteAddress(ctx context.Context, id, userID int64) error {
	res := s.db.GetDb().WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&model.Address{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return bizerr.New(codes.NotFound, "地址不存在或无权操作")
	}
	return nil
}

// SetDefaultAddress 设置默认地址
// 参数：
//   - ctx: 上下文
//   - id: 地址 ID
//   - userID: 用户 ID
//
// 返回：
//   - error: 错误信息
func (s *AddressService) SetDefaultAddress(ctx context.Context, id, userID int64) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Address{}).
			Where("user_id = ?", userID).
			Update("is_default", false).Error; err != nil {
			return err
		}
		return tx.Model(&model.Address{}).
			Where("id = ? AND user_id = ?", id, userID).
			Update("is_default", true).Error
	})
}

// resolveAddressRegion 按用户当前社区裁决地址省市区前缀（含区划代码）与归属社区
// 语义：用户必须已选择社区；社区不允许外配（默认）时，
// 省/市/区名称与区划代码强制取社区配置值，忽略请求中传入的前缀。
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - province, city, district: 请求中的省市区名称
//   - provinceCode, cityCode, districtCode: 请求中的省市区区划代码
//
// 返回：
//   - *model.Community: 用户所属社区
//   - string × 6: 裁决后的省、市、区名称与区划代码
//   - error: 错误信息
func (s *AddressService) resolveAddressRegion(ctx context.Context, userID int64, province, city, district, provinceCode, cityCode, districtCode string) (*model.Community, string, string, string, string, string, string, error) {
	community, err := s.getUserCommunity(ctx, userID)
	if err != nil {
		return nil, "", "", "", "", "", "", err
	}
	if !community.AllowOutsideDelivery {
		province = community.Province
		city = community.City
		district = community.District
		provinceCode = community.ProvinceCode
		cityCode = community.CityCode
		districtCode = community.DistrictCode
	}
	return community, province, city, district, provinceCode, cityCode, districtCode, nil
}

// ToAddressInfo 将地址模型转换为 proto 地址信息
// 参数：
//   - addr: 地址模型
//   - communityName: 地址所属社区名称（用于按社区分组展示，可为空）
//
// 返回：
//   - *user.AddressInfo: proto 地址信息
func ToAddressInfo(addr *model.Address, communityName string) *user.AddressInfo {
	if addr == nil {
		return nil
	}
	return &user.AddressInfo{
		Id:            addr.ID,
		UserId:        addr.UserID,
		ContactName:   addr.ContactName,
		ContactPhone:  addr.ContactPhone,
		Province:      addr.Province,
		City:          addr.City,
		District:      addr.District,
		ProvinceCode:  addr.ProvinceCode,
		CityCode:      addr.CityCode,
		DistrictCode:  addr.DistrictCode,
		Detail:        addr.Detail,
		Tag:           addr.Tag,
		CommunityId:   addr.CommunityID,
		CommunityName: communityName,
		IsDefault:     addr.IsDefault,
		CreatedAt:     addr.CreatedAt,
	}
}
