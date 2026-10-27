package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/go-redis/redis/v8"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/eventbus"
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
	// 收银台代客下单与挂单（G）：社区运营动作，默认纳入社区权限上限
	"order:create",
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
	// redisClient 可选：权限变更经 Redis 发布到事件总线（WS 提醒在线管理员刷新权限快照），
	// 未注入时静默跳过（后端鉴权实时，事件仅消除前端观感滞后）
	redisClient *redis.Client
}

// WithRedis 注入 Redis 客户端（权限变更事件发布用，链式返回）
//
// 参数：
//   - client: go-redis 客户端
//
// 返回：
//   - *RoleService: 服务自身
func (s *RoleService) WithRedis(client *redis.Client) *RoleService {
	s.redisClient = client
	return s
}

// publishPermissionChanged 权限变更事件发布（尽力而为）
// 角色增删改会即时改变该社区管理员的有效权限（含社区权限上限调整，
// 社区权限页保存即走 UpdateRole），事件提醒 gateway 把在线管理员的
// 前端权限快照刷新；任何失败仅记日志，不影响角色主流程；
// Redis 客户端未注入时静默跳过
//
// 参数：
//   - ctx: 上下文（主流程事务已提交后调用，勿传入事务句柄）
//   - communityID: 角色归属社区 ID（>0 按社区广播；全局角色传 0 时广播全部社区）
//   - reason: 变更原因（日志用）
func (s *RoleService) publishPermissionChanged(ctx context.Context, communityID int64, reason string) {
	s.publishPermissionChangedTo(ctx, nil, []int64{communityID}, reason)
}

// publishPermissionChangedTo 权限变更事件发布（定向版，见 publishPermissionChanged）
// AdminIDs 非空时按管理员定向（跨社区连接均可达），否则按 CommunityIDs 逐社区广播
func (s *RoleService) publishPermissionChangedTo(ctx context.Context, adminIDs []int64, communityIDs []int64, reason string) {
	if s.redisClient == nil {
		return
	}
	if len(adminIDs) == 0 && len(communityIDs) == 0 {
		return
	}
	payload, err := json.Marshal(eventbus.AdminPermissionChangedEvent{
		AdminIDs:     adminIDs,
		CommunityIDs: communityIDs,
		Reason:       reason,
	})
	if err != nil {
		log.Printf("[user] 权限变更事件序列化失败: reason=%s err=%v", reason, err)
		return
	}
	if err := s.redisClient.Publish(ctx, eventbus.ChannelAdminPermissionChanged, payload).Err(); err != nil {
		log.Printf("[user] 权限变更事件发布失败: reason=%s err=%v", reason, err)
	}
}

// NewRoleService 创建角色业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回：
//   - *RoleService: 角色业务服务实例
func NewRoleService(db *mysql.Instance) *RoleService {
	return &RoleService{db: db}
}

// ListRoles 查询角色列表
// 多社区口径：communityID > 0 返回该社区的角色（includeGlobal 时附带历史保留行）；
// communityID = 0 仅返回历史保留行（community_id = 0 的存量数据，已无业务消费方）
//
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//   - includeGlobal: 是否附带历史保留行（community_id = 0）
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

// RoleOverview 跨社区角色总览行
// 角色本体 + 所属社区名 + 该角色当前任职人数
type RoleOverview struct {
	Role            *model.Role
	CommunityName   string
	AssignmentCount int64
}

// ListRolesOverview 查询全部社区的角色总览（超管「社区角色」只读页用）
// 不按社区过滤，含 community_id = 0 的历史保留行；社区名与任职人数各用一条聚合查询回填，避免逐行 N+1
//
// 参数：
//   - ctx: 上下文
//
// 返回：
//   - []*RoleOverview: 总览行，按社区 ID 升序、角色 ID 降序
//   - error: 错误信息
func (s *RoleService) ListRolesOverview(ctx context.Context) ([]*RoleOverview, error) {
	var roles []*model.Role
	if err := s.db.GetDb().WithContext(ctx).Model(&model.Role{}).
		Order("community_id ASC, id DESC").Find(&roles).Error; err != nil {
		return nil, err
	}
	communityNames, err := s.communityNameMap(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := s.roleAssignmentCounts(ctx)
	if err != nil {
		return nil, err
	}

	list := make([]*RoleOverview, 0, len(roles))
	for _, r := range roles {
		list = append(list, &RoleOverview{
			Role:            r,
			CommunityName:   communityNames[r.CommunityID],
			AssignmentCount: counts[r.ID],
		})
	}
	return list, nil
}

// communityNameMap 查询社区 ID → 社区名称
// 返回：
//   - map[int64]string: 社区名映射，0 键不存在（历史保留行由调用方显示为「遗留·全局」）
//   - error: 错误信息
func (s *RoleService) communityNameMap(ctx context.Context) (map[int64]string, error) {
	var communities []*model.Community
	if err := s.db.GetDb().WithContext(ctx).Model(&model.Community{}).
		Select("id", "name").Find(&communities).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]string, len(communities))
	for _, c := range communities {
		result[c.ID] = c.Name
	}
	return result, nil
}

// roleAssignmentCounts 统计每个角色的任职人数（admin_communities 按 role_id 分组）
// 返回：
//   - map[int64]int64: 角色 ID → 任职人数
//   - error: 错误信息
func (s *RoleService) roleAssignmentCounts(ctx context.Context) (map[int64]int64, error) {
	var rows []struct {
		RoleID int64 `gorm:"column:role_id"`
		Count  int64 `gorm:"column:cnt"`
	}
	if err := s.db.GetDb().WithContext(ctx).Model(&model.AdminCommunity{}).
		Select("role_id, COUNT(*) AS cnt").Group("role_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]int64, len(rows))
	for _, row := range rows {
		result[row.RoleID] = row.Count
	}
	return result, nil
}

// GetRoleByID 根据 ID 查询角色
// 参数：
//   - ctx: 上下文
//   - id: 角色 ID
//
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
//
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
// 角色一律社区私有：req.CommunityId 必须 > 0，传 0（历史「全局角色」）由服务端拒绝
//
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回：
//   - *model.Role: 角色模型
//   - error: 错误信息
func (s *RoleService) CreateRole(ctx context.Context, req *user.CreateRoleReq) (*model.Role, error) {
	if req.CommunityId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "角色必须归属于某个社区，请先选择社区")
	}
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
	s.publishPermissionChanged(ctx, r.CommunityID, "创建角色")
	return r, nil
}

// UpdateRole 更新角色
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回：
//   - *model.Role: 角色模型
//   - error: 错误信息
func (s *RoleService) UpdateRole(ctx context.Context, req *user.UpdateRoleReq) (*model.Role, error) {
	r, err := s.GetRoleByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	// 服务端第二道防线：网关已按社区语境筛过，这里再校验，防止其他调用方绕过网关改他社区角色权限
	if err := EnsureScope(req.CommunityId, r.CommunityID, "角色"); err != nil {
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
	s.publishPermissionChanged(ctx, r.CommunityID, "更新角色")
	return r, nil
}

// DeleteRole 删除角色
// 内置社区管理员角色不可删除；仍有员工任职的角色不可删除
//
// 参数：
//   - ctx: 上下文
//   - id: 角色 ID
//
// 返回：
//   - error: 错误信息
func (s *RoleService) DeleteRole(ctx context.Context, id, communityId int64) error {
	r, err := s.GetRoleByID(ctx, id)
	if err != nil {
		return err
	}
	if err := EnsureScope(communityId, r.CommunityID, "角色"); err != nil {
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

	if err := s.db.GetDb().WithContext(ctx).Delete(&model.Role{}, id).Error; err != nil {
		return err
	}
	s.publishPermissionChanged(ctx, r.CommunityID, "删除角色")
	return nil
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
//
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
//
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
