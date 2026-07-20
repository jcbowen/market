package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// UserService 用户业务服务
type UserService struct {
	db *mysql.Instance
}

// NewUserService 创建用户业务服务
// 参数：
//   - db: MySQL 实例
// 返回：
//   - *UserService: 用户业务服务实例
func NewUserService(db *mysql.Instance) *UserService {
	return &UserService{db: db}
}

// GetUserById 根据 ID 查询用户
// 参数：
//   - ctx: 上下文
//   - id: 用户 ID
// 返回：
//   - *model.User: 用户模型
//   - error: 错误信息
func (s *UserService) GetUserById(ctx context.Context, id int64) (*model.User, error) {
	var u model.User
	if err := s.db.GetDb().WithContext(ctx).First(&u, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("用户不存在")
		}
		return nil, err
	}
	return &u, nil
}

// GetUserByPhone 根据手机号查询用户
// 参数：
//   - ctx: 上下文
//   - phone: 手机号
// 返回：
//   - *model.User: 用户模型
//   - error: 错误信息
func (s *UserService) GetUserByPhone(ctx context.Context, phone string) (*model.User, error) {
	var u model.User
	if err := s.db.GetDb().WithContext(ctx).Where("phone = ?", phone).First(&u).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("用户不存在")
		}
		return nil, err
	}
	return &u, nil
}

// CreateOrUpdateUser 根据手机号创建或更新用户
// 参数：
//   - ctx: 上下文
//   - req: 创建用户请求
// 返回：
//   - *model.User: 用户模型
//   - error: 错误信息
func (s *UserService) CreateOrUpdateUser(ctx context.Context, req *user.CreateUserReq) (*model.User, error) {
	u, err := s.GetUserByPhone(ctx, req.Phone)
	if err != nil {
		u = &model.User{}
	}

	u.Openid = orValue(req.Openid, u.Openid)
	u.AlipayUserID = orPtrValue(req.AlipayUserId, u.AlipayUserID)
	u.Unionid = orPtrValue(req.UnionId, u.Unionid)
	u.Phone = req.Phone
	u.Nickname = orValue(req.Nickname, u.Nickname)
	u.Avatar = orValue(req.Avatar, u.Avatar)
	u.Gender = req.Gender
	if req.CommunityId > 0 {
		u.CommunityID = req.CommunityId
	}
	u.LastLoginAt = time.Now().Format("2006-01-02 15:04:05")

	if u.ID > 0 {
		if err := s.db.GetDb().WithContext(ctx).Save(u).Error; err != nil {
			return nil, err
		}
		return u, nil
	}

	if err := s.db.GetDb().WithContext(ctx).Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateUserCommunity 更新用户所属社区
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - communityID: 社区 ID
// 返回：
//   - error: 错误信息
func (s *UserService) UpdateUserCommunity(ctx context.Context, userID, communityID int64) error {
	if userID <= 0 || communityID <= 0 {
		return fmt.Errorf("参数错误")
	}
	return s.db.GetDb().WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", userID).
		Update("community_id", communityID).Error
}

// UpdateUserProfile 更新用户资料
// 参数：
//   - ctx: 上下文
//   - req: 更新用户资料请求
// 返回：
//   - *model.User: 用户模型
//   - error: 错误信息
func (s *UserService) UpdateUserProfile(ctx context.Context, req *user.UpdateUserProfileReq) (*model.User, error) {
	u, err := s.GetUserById(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if req.Nickname != "" {
		u.Nickname = req.Nickname
	}
	if req.Avatar != "" {
		u.Avatar = req.Avatar
	}
	if req.Gender > 0 {
		u.Gender = req.Gender
	}
	if err := s.db.GetDb().WithContext(ctx).Save(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// ListUsers 查询用户列表
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID，0 表示全部
//   - keyword: 昵称或手机号关键字
//   - status: 状态，0 表示全部
//   - page: 页码
//   - pageSize: 每页数量
// 返回：
//   - []*model.User: 用户模型列表
//   - int64: 总数
//   - error: 错误信息
func (s *UserService) ListUsers(ctx context.Context, communityID int64, keyword string, status int32, page, pageSize int) ([]*model.User, int64, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.User{})
	if communityID > 0 {
		db = db.Where("community_id = ?", communityID)
	}
	if keyword != "" {
		db = db.Where("nickname LIKE ? OR phone LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if status > 0 {
		db = db.Where("status = ?", status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	var list []*model.User
	offset := (page - 1) * pageSize
	if err := db.Order("id DESC").Limit(pageSize).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ToUserInfo 将用户模型转换为 proto 用户信息
// 参数：
//   - u: 用户模型
// 返回：
//   - *user.UserInfo: proto 用户信息
func ToUserInfo(u *model.User) *user.UserInfo {
	if u == nil {
		return nil
	}
	return &user.UserInfo{
		Id:           u.ID,
		Openid:       u.Openid,
		AlipayUserId: ptrString(u.AlipayUserID),
		UnionId:      ptrString(u.Unionid),
		Phone:        u.Phone,
		Nickname:     u.Nickname,
		Avatar:       u.Avatar,
		Gender:       u.Gender,
		CommunityId:  u.CommunityID,
		Status:       u.Status,
		LastLoginAt:  u.LastLoginAt,
		CreatedAt:    u.CreatedAt,
	}
}

// orValue 返回非空值，优先使用新值
// 参数：
//   - newVal: 新值
//   - oldVal: 旧值
//
// 返回：
//   - string: 有效值
func orValue(newVal, oldVal string) string {
	if newVal != "" {
		return newVal
	}
	return oldVal
}

// orPtrValue 返回非空字符串指针，优先使用新值
// 空字符串视为未提供，保留旧指针值
//
// 参数：
//   - newVal: 新值
//   - oldVal: 旧指针值
//
// 返回：
//   - *string: 有效指针
func orPtrValue(newVal string, oldVal *string) *string {
	if newVal != "" {
		return &newVal
	}
	return oldVal
}

// ptrString 将字符串指针安全转换为字符串
//
// 参数：
//   - s: 字符串指针
//
// 返回：
//   - string: 空字符串或指针指向的值
func ptrString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// _ 占位，避免 errcode 未使用告警
var _ = errcode.Success
