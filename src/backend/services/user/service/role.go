package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// RoleService 角色业务服务
type RoleService struct {
	db *mysql.Instance
}

// NewRoleService 创建角色业务服务
// 参数：
//   - db: MySQL 实例
// 返回：
//   - *RoleService: 角色业务服务实例
func NewRoleService(db *mysql.Instance) *RoleService {
	return &RoleService{db: db}
}

// ListRoles 查询所有角色
// 参数：
//   - ctx: 上下文
// 返回：
//   - []*model.Role: 角色列表
//   - error: 错误信息
func (s *RoleService) ListRoles(ctx context.Context) ([]*model.Role, error) {
	var list []*model.Role
	err := s.db.GetDb().WithContext(ctx).Order("id desc").Find(&list).Error
	return list, err
}

// GetRoleByID 根据 ID 查询角色
// 参数：
//   - ctx: 上下文
//   - id: 角色 ID
// 返回：
//   - *model.Role: 角色模型
//   - error: 错误信息
func (s *RoleService) GetRoleByID(ctx context.Context, id int64) (*model.Role, error) {
	var r model.Role
	err := s.db.GetDb().WithContext(ctx).First(&r, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("角色不存在")
	}
	return &r, err
}

// CreateRole 创建角色
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
// 返回：
//   - *model.Role: 角色模型
//   - error: 错误信息
func (s *RoleService) CreateRole(ctx context.Context, req *user.CreateRoleReq) (*model.Role, error) {
	permsJSON, err := PermissionsToJSON(req.Permissions)
	if err != nil {
		return nil, err
	}

	r := &model.Role{
		Name:        req.Name,
		Code:        req.Code,
		Permissions: permsJSON,
		Description: req.Description,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(r).Error; err != nil {
		return nil, err
	}
	return r, nil
}

// UpdateRole 更新角色
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
// 返回：
//   - *model.Role: 角色模型
//   - error: 错误信息
func (s *RoleService) UpdateRole(ctx context.Context, req *user.UpdateRoleReq) (*model.Role, error) {
	r, err := s.GetRoleByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	if req.Name != "" {
		r.Name = req.Name
	}
	if req.Code != "" {
		r.Code = req.Code
	}
	if req.Description != "" {
		r.Description = req.Description
	}
	permsJSON, err := PermissionsToJSON(req.Permissions)
	if err != nil {
		return nil, err
	}
	r.Permissions = permsJSON

	if err := s.db.GetDb().WithContext(ctx).Save(r).Error; err != nil {
		return nil, err
	}
	return r, nil
}

// DeleteRole 删除角色
// 参数：
//   - ctx: 上下文
//   - id: 角色 ID
// 返回：
//   - error: 错误信息
func (s *RoleService) DeleteRole(ctx context.Context, id int64) error {
	return s.db.GetDb().WithContext(ctx).Delete(&model.Role{}, id).Error
}

// ToRoleInfo 将角色模型转换为 proto 角色信息
// 参数：
//   - r: 角色模型
// 返回：
//   - *user.RoleInfo: proto 角色信息
func ToRoleInfo(r *model.Role) *user.RoleInfo {
	if r == nil {
		return nil
	}
	info := &user.RoleInfo{
		Id:          r.ID,
		Name:        r.Name,
		Code:        r.Code,
		Description: r.Description,
	}
	var permissions []string
	_ = json.Unmarshal([]byte(r.Permissions), &permissions)
	info.Permissions = permissions
	return info
}

// PermissionsToJSON 将权限切片序列化为 JSON 字符串
// 参数：
//   - permissions: 权限编码列表
// 返回：
//   - string: JSON 字符串
//   - error: 错误信息
func PermissionsToJSON(permissions []string) (string, error) {
	if len(permissions) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(permissions)
	return string(b), err
}
