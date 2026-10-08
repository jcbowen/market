package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// AdminService 管理员业务服务
type AdminService struct {
	db *mysql.Instance
}

// NewAdminService 创建管理员业务服务
// 参数：
//   - db: MySQL 实例
// 返回：
//   - *AdminService: 管理员业务服务实例
func NewAdminService(db *mysql.Instance) *AdminService {
	return &AdminService{db: db}
}

// GetAdminByUsername 根据用户名查询管理员
// 参数：
//   - ctx: 上下文
//   - username: 用户名
// 返回：
//   - *model.Admin: 管理员模型
//   - error: 错误信息
func (s *AdminService) GetAdminByUsername(ctx context.Context, username string) (*model.Admin, error) {
	var a model.Admin
	err := s.db.GetDb().WithContext(ctx).Where("username = ?", username).First(&a).Error
	if err == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("管理员不存在")
	}
	return &a, err
}

// ListAdmins 分页查询管理员列表
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID，0 表示全部
//   - page: 页码
//   - pageSize: 每页条数
// 返回：
//   - []*model.Admin: 管理员列表
//   - int: 总条数
//   - error: 错误信息
func (s *AdminService) ListAdmins(ctx context.Context, communityID int64, page, pageSize int) ([]*model.Admin, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.Admin{})
	if communityID > 0 {
		db = db.Where("community_id = ?", communityID)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []*model.Admin
	offset := (page - 1) * pageSize
	err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// CreateAdmin 创建管理员
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
// 返回：
//   - *model.Admin: 管理员模型
//   - error: 错误信息
func (s *AdminService) CreateAdmin(ctx context.Context, req *user.CreateAdminReq) (*model.Admin, error) {
	hashed, err := hashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	a := &model.Admin{
		Username:    req.Username,
		Password:    hashed,
		RealName:    req.RealName,
		Phone:       req.Phone,
		RoleID:      req.RoleId,
		CommunityID: req.CommunityId,
		Status:      1,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(a).Error; err != nil {
		return nil, err
	}
	return a, nil
}

// UpdateAdmin 更新管理员
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
// 返回：
//   - *model.Admin: 管理员模型
//   - error: 错误信息
func (s *AdminService) UpdateAdmin(ctx context.Context, req *user.UpdateAdminReq) (*model.Admin, error) {
	a, err := s.getAdminByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	a.RealName = req.RealName
	a.Phone = req.Phone
	a.RoleID = req.RoleId
	a.CommunityID = req.CommunityId
	if req.Status != 0 {
		a.Status = req.Status
	}

	if err := s.db.GetDb().WithContext(ctx).Save(a).Error; err != nil {
		return nil, err
	}
	return a, nil
}

// DeleteAdmin 删除管理员
// 参数：
//   - ctx: 上下文
//   - id: 管理员 ID
// 返回：
//   - error: 错误信息
func (s *AdminService) DeleteAdmin(ctx context.Context, id int64) error {
	return s.db.GetDb().WithContext(ctx).Delete(&model.Admin{}, id).Error
}

// ChangeAdminPassword 修改管理员密码
// 参数：
//   - ctx: 上下文
//   - id: 管理员 ID
//   - oldPassword: 旧密码
//   - newPassword: 新密码
//
// 返回：
//   - error: 错误信息
func (s *AdminService) ChangeAdminPassword(ctx context.Context, id int64, oldPassword, newPassword string) error {
	a, err := s.getAdminByID(ctx, id)
	if err != nil {
		return err
	}

	if err := CheckPassword(oldPassword, a.Password); err != nil {
		return fmt.Errorf("旧密码错误")
	}

	hashed, err := hashPassword(newPassword)
	if err != nil {
		return err
	}

	return s.db.GetDb().WithContext(ctx).
		Model(&model.Admin{}).
		Where("id = ?", id).
		Update("password", hashed).Error
}

// UpdateLoginInfo 更新管理员登录信息
// 参数：
//   - ctx: 上下文
//   - id: 管理员 ID
//   - ip: 登录 IP
// 返回：
//   - error: 错误信息
func (s *AdminService) UpdateLoginInfo(ctx context.Context, id int64, ip string) error {
	return s.db.GetDb().WithContext(ctx).
		Model(&model.Admin{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"last_login_at": time.Now().Format("2006-01-02 15:04:05"),
			"last_login_ip": ip,
		}).Error
}

// getAdminByID 根据 ID 查询管理员
// 参数：
//   - ctx: 上下文
//   - id: 管理员 ID
// 返回：
//   - *model.Admin: 管理员模型
//   - error: 错误信息
func (s *AdminService) getAdminByID(ctx context.Context, id int64) (*model.Admin, error) {
	var a model.Admin
	err := s.db.GetDb().WithContext(ctx).First(&a, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("管理员不存在")
	}
	return &a, err
}

// ListAdminCommunityIDs 查询管理员关联的社区 ID 列表
// 超管角色返回空列表，由调用方自行判断为全部社区
//
// 参数：
//   - ctx: 上下文
//   - adminID: 管理员 ID
//   - isSuperAdmin: 是否为超管
//
// 返回：
//   - []int64: 社区 ID 列表
//   - error: 错误信息
func (s *AdminService) ListAdminCommunityIDs(ctx context.Context, adminID int64, isSuperAdmin bool) ([]int64, error) {
	if isSuperAdmin {
		return nil, nil
	}
	var list []*model.AdminCommunity
	err := s.db.GetDb().WithContext(ctx).
		Where("admin_id = ?", adminID).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(list))
	for _, item := range list {
		ids = append(ids, item.CommunityID)
	}
	return ids, nil
}

// BindAdminCommunities 绑定管理员与社区关联
// 会清空原有绑定关系后重新写入
//
// 参数：
//   - ctx: 上下文
//   - adminID: 管理员 ID
//   - communityIDs: 社区 ID 列表
//
// 返回：
//   - error: 错误信息
func (s *AdminService) BindAdminCommunities(ctx context.Context, adminID int64, communityIDs []int64) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("admin_id = ?", adminID).Delete(&model.AdminCommunity{}).Error; err != nil {
			return err
		}
		for _, cid := range communityIDs {
			if cid <= 0 {
				continue
			}
			if err := tx.Create(&model.AdminCommunity{AdminID: adminID, CommunityID: cid}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// hashPassword 对密码进行 bcrypt 哈希
// 参数：
//   - password: 明文密码
// 返回：
//   - string: 哈希后的密码
//   - error: 错误信息
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword 校验密码
// 参数：
//   - password: 明文密码
//   - hashed: 哈希密码
// 返回：
//   - error: 校验错误信息
func CheckPassword(password, hashed string) error {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(password))
}

// ToAdminInfo 将管理员模型转换为 proto 管理员信息
// 参数：
//   - a: 管理员模型
//   - role: 角色模型（可选）
//   - communityIDs: 关联社区 ID 列表（可选）
// 返回：
//   - *user.AdminInfo: proto 管理员信息
func ToAdminInfo(a *model.Admin, role *model.Role, communityIDs []int64) *user.AdminInfo {
	if a == nil {
		return nil
	}
	info := &user.AdminInfo{
		Id:           a.ID,
		Username:     a.Username,
		Password:     a.Password,
		RealName:     a.RealName,
		Phone:        a.Phone,
		RoleId:       a.RoleID,
		CommunityId:  a.CommunityID,
		CommunityIds: communityIDs,
		Status:       a.Status,
		LastLoginAt:  ptrString(a.LastLoginAt),
		LastLoginIp:  a.LastLoginIP,
	}
	if role != nil {
		info.RoleCode = role.Code
		info.RoleName = role.Name
	}
	return info
}
