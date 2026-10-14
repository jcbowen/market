package service

import (
	"context"
	"fmt"
	"strings"
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
	lastLoginAt := time.Now().Format("2006-01-02 15:04:05")
	u.LastLoginAt = &lastLoginAt

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

// ListUsers 查询会员列表（多社区模型：communityID > 0 时按会员-社区关联表过滤）
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID，>0 只列出与该社区有关联的会员
//   - keyword: 昵称或手机号关键字
//   - status: 旧状态参数，>0 时按状态过滤（兼容保留）
//   - statusFilter: 状态筛选：-1 全部，0 拉黑，1 正常（提供时优先于 status）
//   - page: 页码
//   - pageSize: 每页数量
// 返回：
//   - []*model.User: 用户模型列表
//   - int64: 总数
//   - string: 过滤语境社区名称（communityID <= 0 时为空串）
//   - error: 错误信息
func (s *UserService) ListUsers(ctx context.Context, communityID int64, keyword string, status, statusFilter int32, page, pageSize int) ([]*model.User, int64, string, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.User{})
	if communityID > 0 {
		db = db.Where("id IN (SELECT user_id FROM user_communities WHERE community_id = ?)", communityID)
	}
	if keyword != "" {
		db = db.Where("nickname LIKE ? OR phone LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if statusFilter >= 0 {
		db = db.Where("status = ?", statusFilter)
	} else if status > 0 {
		db = db.Where("status = ?", status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, "", err
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
		return nil, 0, "", err
	}

	communityName := ""
	if communityID > 0 {
		communityName = s.getCommunityName(ctx, communityID)
	}
	return list, total, communityName, nil
}

// getCommunityName 查询社区名称
// 参数：
//   - ctx: 上下文
//   - id: 社区 ID
//
// 返回：
//   - string: 社区名称（不存在时为空串）
func (s *UserService) getCommunityName(ctx context.Context, id int64) string {
	var c model.Community
	if err := s.db.GetDb().WithContext(ctx).Select("name").First(&c, id).Error; err != nil {
		return ""
	}
	return c.Name
}

// MarkUserCommunity 幂等记录会员与社区的关联
// 同一 (user_id, community_id) 重复记录时保留首次来源，不报错
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - communityID: 社区 ID
//   - source: 关联来源（address/order/admin/backfill）
// 返回：
//   - error: 错误信息
func (s *UserService) MarkUserCommunity(ctx context.Context, userID, communityID int64, source string) error {
	if userID <= 0 || communityID <= 0 {
		return fmt.Errorf("参数错误")
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	row := &model.UserCommunity{
		UserID:      userID,
		CommunityID: communityID,
		Source:      source,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err := s.db.GetDb().WithContext(ctx).
		Where("user_id = ? AND community_id = ?", userID, communityID).
		FirstOrCreate(row).Error
	if err != nil {
		// 并发下唯一索引冲突等同已存在，视为成功
		if isDuplicateKeyErr(err) {
			return nil
		}
		return err
	}
	return nil
}

// SetUserStatus 设置会员状态（拉黑/恢复）
// 参数：
//   - ctx: 上下文
//   - id: 用户 ID
//   - status: 0 拉黑 1 正常
// 返回：
//   - error: 错误信息
func (s *UserService) SetUserStatus(ctx context.Context, id int64, status int32) error {
	if id <= 0 || (status != 0 && status != 1) {
		return fmt.Errorf("参数错误")
	}
	res := s.db.GetDb().WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", id).
		Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("会员不存在")
	}
	return nil
}

// CreateMember 后台添加会员
// 手机号唯一，已注册时报错；openid 沿用手机号登录的 phone_<手机号> 占位约定；
// 会员注册归属指定社区，并写入会员-社区关联（来源 admin）
// 参数：
//   - ctx: 上下文
//   - req: 添加会员请求
// 返回：
//   - *model.User: 新建会员
//   - error: 错误信息
func (s *UserService) CreateMember(ctx context.Context, req *user.CreateMemberReq) (*model.User, error) {
	if req.Phone == "" || req.CommunityId <= 0 {
		return nil, fmt.Errorf("参数错误")
	}
	if _, err := s.GetUserByPhone(ctx, req.Phone); err == nil {
		return nil, fmt.Errorf("该手机号已注册")
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	u := &model.User{
		Openid:      phoneOpenidPrefix(req.Phone),
		Phone:       req.Phone,
		Nickname:    orValue(req.Nickname, "会员"+req.Phone[len(req.Phone)-4:]),
		Gender:      req.Gender,
		CommunityID: req.CommunityId,
		Status:      1,
	}
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(u).Error; err != nil {
			return err
		}
		row := &model.UserCommunity{
			UserID:      u.ID,
			CommunityID: req.CommunityId,
			Source:      "admin",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		return tx.Create(row).Error
	}); err != nil {
		return nil, err
	}
	return u, nil
}

// phoneOpenidPrefix 生成手机号会员的 openid 占位标识（与手机号登录约定一致）
// 参数：
//   - phone: 手机号
// 返回：
//   - string: phone_<手机号>
func phoneOpenidPrefix(phone string) string {
	return "phone_" + phone
}

// isDuplicateKeyErr 判断是否为唯一键冲突错误（MySQL 1062）
// 参数：
//   - err: 数据库错误
// 返回：
//   - bool: 是否唯一键冲突
func isDuplicateKeyErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "1062") || strings.Contains(err.Error(), "Duplicate entry")
}

// BackfillUserCommunities 启动时幂等回填存量会员-社区关联
// 将 users.community_id > 0 且尚无关联记录的会员补写一条来源 backfill 的关联；
// 依赖 (user_id, community_id) 唯一索引保证重复执行不产生重复数据
// 参数：
//   - ctx: 上下文
// 返回：
//   - error: 错误信息
func (s *UserService) BackfillUserCommunities(ctx context.Context) error {
	db := s.db.GetDb().WithContext(ctx)
	var users []*model.User
	if err := db.
		Where("community_id > 0 AND id NOT IN (SELECT user_id FROM user_communities)").
		Select("id, community_id").
		Find(&users).Error; err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	rows := make([]*model.UserCommunity, 0, len(users))
	for _, u := range users {
		rows = append(rows, &model.UserCommunity{
			UserID:      u.ID,
			CommunityID: u.CommunityID,
			Source:      "backfill",
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}
	return db.CreateInBatches(rows, 500).Error
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
		LastLoginAt:  ptrString(u.LastLoginAt),
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
