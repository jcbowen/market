package service

import (
	"context"
	"fmt"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// AddressService 地址业务服务
type AddressService struct {
	db *mysql.Instance
}

// NewAddressService 创建地址业务服务
// 参数：
//   - db: MySQL 实例
// 返回：
//   - *AddressService: 地址业务服务实例
func NewAddressService(db *mysql.Instance) *AddressService {
	return &AddressService{db: db}
}

// ListAddresses 查询用户地址列表
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
// 返回：
//   - []*model.Address: 地址列表
//   - error: 错误信息
func (s *AddressService) ListAddresses(ctx context.Context, userID int64) ([]*model.Address, error) {
	var list []*model.Address
	err := s.db.GetDb().WithContext(ctx).
		Where("user_id = ?", userID).
		Order("is_default desc, id desc").
		Find(&list).Error
	return list, err
}

// GetAddress 查询地址详情
// 参数：
//   - ctx: 上下文
//   - id: 地址 ID
//   - userID: 用户 ID
// 返回：
//   - *model.Address: 地址模型
//   - error: 错误信息
func (s *AddressService) GetAddress(ctx context.Context, id, userID int64) (*model.Address, error) {
	var addr model.Address
	err := s.db.GetDb().WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&addr).Error
	if err == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("地址不存在")
	}
	return &addr, err
}

// CreateAddress 创建地址
// 参数：
//   - ctx: 上下文
//   - req: 创建地址请求
// 返回：
//   - *model.Address: 地址模型
//   - error: 错误信息
func (s *AddressService) CreateAddress(ctx context.Context, req *user.CreateAddressReq) (*model.Address, error) {
	addr := &model.Address{
		UserID:       req.UserId,
		ContactName:  req.ContactName,
		ContactPhone: req.ContactPhone,
		Province:     req.Province,
		City:         req.City,
		District:     req.District,
		Detail:       req.Detail,
		Tag:          req.Tag,
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
	return addr, nil
}

// UpdateAddress 更新地址
// 参数：
//   - ctx: 上下文
//   - req: 更新地址请求
// 返回：
//   - *model.Address: 地址模型
//   - error: 错误信息
func (s *AddressService) UpdateAddress(ctx context.Context, req *user.UpdateAddressReq) (*model.Address, error) {
	addr, err := s.GetAddress(ctx, req.Id, req.UserId)
	if err != nil {
		return nil, err
	}

	addr.ContactName = req.ContactName
	addr.ContactPhone = req.ContactPhone
	addr.Province = req.Province
	addr.City = req.City
	addr.District = req.District
	addr.Detail = req.Detail
	addr.Tag = req.Tag
	addr.IsDefault = req.IsDefault

	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if addr.IsDefault {
			if err := tx.Model(&model.Address{}).
				Where("user_id = ? AND id != ?", req.UserId, req.Id).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Save(addr).Error
	}); err != nil {
		return nil, err
	}
	return addr, nil
}

// DeleteAddress 删除地址
// 参数：
//   - ctx: 上下文
//   - id: 地址 ID
//   - userID: 用户 ID
// 返回：
//   - error: 错误信息
func (s *AddressService) DeleteAddress(ctx context.Context, id, userID int64) error {
	return s.db.GetDb().WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&model.Address{}).Error
}

// SetDefaultAddress 设置默认地址
// 参数：
//   - ctx: 上下文
//   - id: 地址 ID
//   - userID: 用户 ID
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

// ToAddressInfo 将地址模型转换为 proto 地址信息
// 参数：
//   - addr: 地址模型
// 返回：
//   - *user.AddressInfo: proto 地址信息
func ToAddressInfo(addr *model.Address) *user.AddressInfo {
	if addr == nil {
		return nil
	}
	return &user.AddressInfo{
		Id:           addr.ID,
		UserId:       addr.UserID,
		ContactName:  addr.ContactName,
		ContactPhone: addr.ContactPhone,
		Province:     addr.Province,
		City:         addr.City,
		District:     addr.District,
		Detail:       addr.Detail,
		Tag:          addr.Tag,
		IsDefault:    addr.IsDefault,
		CreatedAt:    addr.CreatedAt,
	}
}
