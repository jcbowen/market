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

// DefaultCommunityAdminPermissions 内置社区管理员角色的默认权限集
// 即「新社区的权限上限」默认值：覆盖社区门店日常运营所需，
// 不含系统级权限（社区管理 community:*、行政区划 region:*、系统配置 settings:*），
// 这些仅能由超管在「社区权限」页按社区显式下发
var DefaultCommunityAdminPermissions = []string{
	"dashboard:view",
	"product:view", "product:create", "product:update", "product:delete",
	"inventory:view", "inventory:create", "inventory:update",
	"order:view", "order:update",
	"refund:view", "refund:update",
	"user:view", "user:create", "user:update",
	"marketing:view", "marketing:create", "marketing:update", "marketing:delete",
	"statistics:view",
	"admin:view", "admin:create", "admin:update", "admin:delete",
	"role:view", "role:create", "role:update", "role:delete",
	// 门店设置：社区管理员自助维护本社区资料与配送配置（不含名称/地址/经纬度/负责人电话）
	"community:config",
}

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

// ListRoles 查询角色列表
// 多社区口径：communityID > 0 返回该社区的角色（includeGlobal 时附带全局角色）；
// communityID = 0 仅返回全局角色
//
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//   - includeGlobal: 是否附带全局角色（community_id = 0）
//
// 返回：
//   - []*model.Role: 角色列表
//   - error: 错误信息
func (s *RoleService) ListRoles(ctx context.Context, communityID int64, includeGlobal bool) ([]*model.Role, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.Role{})
	if communityID > 0 {
		if includeGlobal {
			db = db.Where("community_id = ? OR community_id = 0", communityID)
		} else {
			db = db.Where("community_id = ?", communityID)
		}
	} else {
		db = db.Where("community_id = 0")
	}

	var list []*model.Role
	err := db.Order("id desc").Find(&list).Error
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

// GetRolesByIDs 批量查询角色
// 参数：
//   - ctx: 上下文
//   - ids: 角色 ID 列表
// 返回：
//   - map[int64]*model.Role: 角色 ID → 角色
//   - error: 错误信息
func (s *RoleService) GetRolesByIDs(ctx context.Context, ids []int64) (map[int64]*model.Role, error) {
	result := make(map[int64]*model.Role, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var list []*model.Role
	if err := s.db.GetDb().WithContext(ctx).Where("id IN ?", ids).Find(&list).Error; err != nil {
		return nil, err
	}
	for i := range list {
		result[list[i].ID] = list[i]
	}
	return result, nil
}

// CreateRole 创建角色
// 角色归属社区由 req.CommunityId 决定（0=全局角色，仅超管可创建，由网关层校验）
//
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
		CommunityID: req.CommunityId,
		Permissions: permsJSON,
		Description: req.Description,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(r).Error; err != nil {
		return nil, fmt.Errorf("角色编码在当前范围内已存在")
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
// 内置社区管理员角色不可删除；仍有员工任职的角色不可删除
//
// 参数：
//   - ctx: 上下文
//   - id: 角色 ID
// 返回：
//   - error: 错误信息
func (s *RoleService) DeleteRole(ctx context.Context, id int64) error {
	r, err := s.GetRoleByID(ctx, id)
	if err != nil {
		return err
	}
	if r.IsBuiltinCommunityAdmin() {
		return fmt.Errorf("内置社区管理员角色不可删除")
	}

	var count int64
	if err := s.db.GetDb().WithContext(ctx).
		Model(&model.AdminCommunity{}).
		Where("role_id = ?", id).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("该角色仍有员工任职，请先在员工管理中调整")
	}

	return s.db.GetDb().WithContext(ctx).Delete(&model.Role{}, id).Error
}

// GetBuiltinCommunityAdminRole 只读查询指定社区的内置社区管理员角色（不存在返回 nil，不自动创建）
// 该角色的权限即「社区权限上限」：普通管理员的有效权限运行时与其取交集
//
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//
// 返回：
//   - *model.Role: 内置角色，不存在时返回 nil
//   - error: 错误信息
func (s *RoleService) GetBuiltinCommunityAdminRole(ctx context.Context, communityID int64) (*model.Role, error) {
	if communityID <= 0 {
		return nil, nil
	}
	var r model.Role
	err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND code = ?", communityID, model.RoleCodeCommunityAdmin).
		First(&r).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// EnsureCommunityAdminRole 获取（或创建）指定社区的内置社区管理员角色
// 每个社区仅一条（code = community_admin），权限默认社区级运营集合，可由超管调整
//
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
// 返回：
//   - *model.Role: 内置角色
//   - error: 错误信息
func (s *RoleService) EnsureCommunityAdminRole(ctx context.Context, communityID int64) (*model.Role, error) {
	if communityID <= 0 {
		return nil, fmt.Errorf("社区 ID 非法")
	}
	var r model.Role
	err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND code = ?", communityID, model.RoleCodeCommunityAdmin).
		First(&r).Error
	if err == nil {
		return &r, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	r = model.Role{
		Name:        "社区管理员",
		Code:        model.RoleCodeCommunityAdmin,
		CommunityID: communityID,
		Permissions: PermissionsMustToJSON(DefaultCommunityAdminPermissions),
		Description: "内置角色：负责本社区的日常运营与员工管理；其权限即本社区权限上限",
	}
	if err := s.db.GetDb().WithContext(ctx).Create(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
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
		CommunityId: r.CommunityID,
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
//
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

// PermissionsMustToJSON 将权限切片序列化为 JSON 字符串，序列化失败时返回空数组
// 供内部默认值构造等不可能失败的场景使用
//
// 参数：
//   - permissions: 权限编码列表
//
// 返回：
//   - string: JSON 字符串
func PermissionsMustToJSON(permissions []string) string {
	b, err := json.Marshal(permissions)
	if err != nil {
		return "[]"
	}
	return string(b)
}
