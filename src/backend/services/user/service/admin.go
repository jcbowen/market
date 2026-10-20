package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// AdminService 管理员（账号与社区任职）业务服务
type AdminService struct {
	db          *mysql.Instance
	roleService *RoleService
}

// NewAdminService 创建管理员业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回：
//   - *AdminService: 管理员业务服务实例
func NewAdminService(db *mysql.Instance) *AdminService {
	return &AdminService{db: db, roleService: NewRoleService(db)}
}

// GetAdminByUsername 根据用户名查询管理员
// 参数：
//   - ctx: 上下文
//   - username: 用户名
//
// 返回：
//   - *model.Admin: 管理员模型
//   - error: 账号不存在返回 codes.NotFound 业务错误；数据库等其他故障原样上抛
//     （由调用方归为 Internal）。区分口径是登录链路需要的：只有「账号不存在」才算凭证失败，
//     基础设施故障不能伪装成不存在，否则网关会误计失败次数把正常管理员锁在门外
func (s *AdminService) GetAdminByUsername(ctx context.Context, username string) (*model.Admin, error) {
	var a model.Admin
	err := s.db.GetDb().WithContext(ctx).Where("username = ?", username).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, bizerr.New(codes.NotFound, "管理员不存在")
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAdmins 分页查询员工列表（账号 + 指定社区任职）
// communityID > 0：仅列出在该社区任职的员工；communityID = 0：列出全部账号
// 返回值 assignments 按管理员 ID 分组返回其（语境社区内的）任职行
//
// 参数：
//   - ctx: 上下文
//   - communityID: 任职社区 ID
//   - keyword: 关键词（用户名/姓名/手机号模糊匹配）
//   - page: 页码
//   - pageSize: 每页条数
//
// 返回：
//   - []*model.Admin: 员工列表
//   - map[int64][]*model.AdminCommunity: 管理员 ID → 任职行列表
//   - int: 总条数
//   - error: 错误信息
func (s *AdminService) ListAdmins(ctx context.Context, communityID int64, keyword string, page, pageSize int) ([]*model.Admin, map[int64][]*model.AdminCommunity, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.Admin{})
	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("username LIKE ? OR real_name LIKE ? OR phone LIKE ?", like, like, like)
	}
	if communityID > 0 {
		db = db.Where("id IN (SELECT admin_id FROM admin_communities WHERE community_id = ?)", communityID)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, nil, 0, err
	}

	var list []*model.Admin
	offset := (page - 1) * pageSize
	if err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, nil, 0, err
	}

	// 批量取语境社区内的任职行
	adminIDs := make([]int64, 0, len(list))
	for _, a := range list {
		adminIDs = append(adminIDs, a.ID)
	}
	assignments := make(map[int64][]*model.AdminCommunity, len(list))
	if len(adminIDs) > 0 {
		adbm := s.db.GetDb().WithContext(ctx).Model(&model.AdminCommunity{}).Where("admin_id IN ?", adminIDs)
		if communityID > 0 {
			adbm = adbm.Where("community_id = ?", communityID)
		}
		var rows []*model.AdminCommunity
		if err := adbm.Order("id asc").Find(&rows).Error; err != nil {
			return nil, nil, 0, err
		}
		for _, row := range rows {
			assignments[row.AdminID] = append(assignments[row.AdminID], row)
		}
	}

	return list, assignments, int(total), nil
}

// CreateAdmin 创建员工（含社区任职）
// username 已存在时复用该账号（不修改其密码与状态），仅追加任职——
// 支持同一人在多个社区任职；不存在时以给定信息新建账号；
// req.AccountOnly = true 时仅创建账号不在任何社区任职（全局账号管理场景，
// 此时忽略 role_ids，密码必填）
//
// 参数：
//   - ctx: 上下文
//   - req: 创建请求（community_id = 任职社区，role_ids = 该社区角色）
//
// 返回：
//   - *model.Admin: 员工账号
//   - error: 错误信息
func (s *AdminService) CreateAdmin(ctx context.Context, req *user.CreateAdminReq) (*model.Admin, error) {
	// 仅建账号：跳过任职校验与写入
	if req.AccountOnly {
		var account model.Admin
		err := s.db.GetDb().WithContext(ctx).Where("username = ?", req.Username).First(&account).Error
		if err == nil {
			return nil, bizerr.New(codes.FailedPrecondition, "用户名已存在")
		}
		if err != gorm.ErrRecordNotFound {
			return nil, err
		}
		if len(req.Password) < 6 {
			return nil, fmt.Errorf("新账号密码长度至少 6 位")
		}
		hashed, err := hashPassword(req.Password)
		if err != nil {
			return nil, err
		}
		a := &model.Admin{
			Username: req.Username,
			Password: hashed,
			RealName: req.RealName,
			Phone:    req.Phone,
			Status:   1,
		}
		if err := s.db.GetDb().WithContext(ctx).Create(a).Error; err != nil {
			return nil, err
		}
		return a, nil
	}

	if req.CommunityId <= 0 {
		return nil, fmt.Errorf("缺少任职社区")
	}
	// 指定为内置社区管理员：自动解析/创建内置角色（角色 ID 由服务端裁决，忽略 role_ids）
	if req.AssignCommunityAdmin {
		builtin, err := s.roleService.EnsureCommunityAdminRole(ctx, req.CommunityId)
		if err != nil {
			return nil, err
		}
		req.RoleIds = []int64{builtin.ID}
	}
	if len(req.RoleIds) == 0 {
		return nil, fmt.Errorf("请至少分配一个角色")
	}
	if err := s.validateRolesForCommunity(ctx, req.CommunityId, req.RoleIds, req.OperatorIsSuper); err != nil {
		return nil, err
	}

	var a *model.Admin
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.Admin
		switch err := tx.Where("username = ?", req.Username).First(&existing).Error; err {
		case nil:
			// 复用已有账号
			a = &existing
		case gorm.ErrRecordNotFound:
			if len(req.Password) < 6 {
				return fmt.Errorf("新账号密码长度至少 6 位")
			}
			hashed, err := hashPassword(req.Password)
			if err != nil {
				return err
			}
			a = &model.Admin{
				Username: req.Username,
				Password: hashed,
				RealName: req.RealName,
				Phone:    req.Phone,
				Status:   1,
			}
			if err := tx.Create(a).Error; err != nil {
				return err
			}
		default:
			return err
		}

		// 创建/复用账号后绑定会员（>0 时绑定，绑定后即履约人员）
		if req.UserId > 0 {
			if err := s.bindMemberTx(tx, a.ID, req.UserId); err != nil {
				return err
			}
		}
		return replaceAssignmentsTx(tx, a.ID, req.CommunityId, req.RoleIds, req.OperatorIsSuper)
	})
	if err != nil {
		return nil, err
	}
	return s.getAdminByID(ctx, a.ID)
}

// UpdateAdmin 更新员工（账号资料 + 指定社区的角色任职）
// req.CommunityId > 0 时按 req.RoleIds 全量覆盖该社区任职
//
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回：
//   - *model.Admin: 员工账号
//   - error: 错误信息
func (s *AdminService) UpdateAdmin(ctx context.Context, req *user.UpdateAdminReq) (*model.Admin, error) {
	a, err := s.getAdminByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	// 仅更新状态（超管账号管理：停用/启用），不触碰姓名/手机号/角色任职
	if req.UpdateStatusOnly {
		if req.Status != 0 && req.Status != 1 {
			return nil, fmt.Errorf("状态取值非法")
		}
		if err := s.db.GetDb().WithContext(ctx).
			Model(&model.Admin{}).
			Where("id = ?", req.Id).
			Update("status", req.Status).Error; err != nil {
			return nil, err
		}
		return s.getAdminByID(ctx, req.Id)
	}

	if req.CommunityId > 0 && req.RoleIds != nil {
		if len(req.RoleIds) == 0 {
			return nil, fmt.Errorf("请至少保留一个角色；如需移除员工请使用移除操作")
		}
		if err := s.validateRolesForCommunity(ctx, req.CommunityId, req.RoleIds, req.OperatorIsSuper); err != nil {
			return nil, err
		}
	}

	err = s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		a.RealName = req.RealName
		a.Phone = req.Phone
		if req.Status != 0 {
			a.Status = req.Status
		}
		if err := tx.Save(a).Error; err != nil {
			return err
		}
		// 绑定会员（三态：未传=不改动；0=解绑；>0=绑定/换绑）
		if req.UserId != nil {
			if err := s.bindMemberTx(tx, a.ID, *req.UserId); err != nil {
				return err
			}
		}
		if req.CommunityId > 0 && req.RoleIds != nil {
			return replaceAssignmentsTx(tx, a.ID, req.CommunityId, req.RoleIds, req.OperatorIsSuper)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.getAdminByID(ctx, req.Id)
}

// bindMemberTx 在给定事务内绑定/解绑员工的会员身份
// 绑定后该会员在员工全部任职社区获得履约人员身份（随任职增减自动生效）；
// userID = 0 表示解绑；>0 时校验会员存在且未被其他员工绑定（唯一索引兜底防并发）
//
// 参数：
//   - tx: gorm 事务
//   - adminID: 员工 ID
//   - userID: 会员 ID（0=解绑）
//
// 返回：
//   - error: 错误信息
func (s *AdminService) bindMemberTx(tx *gorm.DB, adminID, userID int64) error {
	if userID < 0 {
		return fmt.Errorf("参数错误")
	}
	if userID == 0 {
		// 解绑：仅清空本人的绑定，不影响其他员工
		return tx.Model(&model.Admin{}).
			Where("id = ? AND user_id IS NOT NULL", adminID).
			Update("user_id", nil).Error
	}
	var member model.User
	if err := tx.First(&member, userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("会员不存在（ID %d）", userID)
		}
		return err
	}
	var cnt int64
	if err := tx.Model(&model.Admin{}).
		Where("user_id = ? AND id <> ?", userID, adminID).
		Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return fmt.Errorf("该会员已绑定其他员工")
	}
	return tx.Model(&model.Admin{}).
		Where("id = ?", adminID).
		Update("user_id", userID).Error
}

// DeleteAdmin 移除员工在指定社区的任职（账号保留，不影响其在其他社区的任职）
// communityID = 0 时移除全部任职
//
// 参数：
//   - ctx: 上下文
//   - id: 管理员 ID
//   - communityID: 任职社区 ID
//
// 返回：
//   - error: 错误信息
func (s *AdminService) DeleteAdmin(ctx context.Context, id int64, communityID int64) error {
	a, err := s.getAdminByID(ctx, id)
	if err != nil {
		return err
	}
	if a.IsSuper {
		return fmt.Errorf("超级管理员账号不可移除")
	}

	db := s.db.GetDb().WithContext(ctx).Where("admin_id = ?", id)
	if communityID > 0 {
		db = db.Where("community_id = ?", communityID)
	}
	res := db.Delete(&model.AdminCommunity{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("该员工在此社区无任职")
	}
	return nil
}

// ChangeAdminPassword 修改管理员密码
// force = true 时跳过旧密码校验（超管重置密码场景，调用方须自行校验操作者身份）
//
// 参数：
//   - ctx: 上下文
//   - id: 管理员 ID
//   - oldPassword: 旧密码
//   - newPassword: 新密码
//   - force: 是否跳过旧密码校验
//
// 返回：
//   - error: 错误信息
func (s *AdminService) ChangeAdminPassword(ctx context.Context, id int64, oldPassword, newPassword string, force bool) error {
	a, err := s.getAdminByID(ctx, id)
	if err != nil {
		return err
	}

	if !force {
		if err := CheckPassword(oldPassword, a.Password); err != nil {
			return fmt.Errorf("旧密码错误")
		}
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
//
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

// ListMembersByIDs 批量查询会员（员工列表展示绑定会员昵称/手机号用）
//
// 参数：
//   - ctx: 上下文
//   - ids: 会员 ID 列表
//
// 返回：
//   - map[int64]*model.User: 会员 ID → 会员模型
//   - error: 错误信息
func (s *AdminService) ListMembersByIDs(ctx context.Context, ids []int64) (map[int64]*model.User, error) {
	result := make(map[int64]*model.User, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var members []*model.User
	if err := s.db.GetDb().WithContext(ctx).
		Select("id, nickname, phone").
		Where("id IN ?", ids).
		Find(&members).Error; err != nil {
		return nil, err
	}
	for _, m := range members {
		result[m.ID] = m
	}
	return result, nil
}

// getAdminByID 根据 ID 查询管理员
// 参数：
//   - ctx: 上下文
//   - id: 管理员 ID
//
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

// ListAdminCommunityIDs 查询管理员任职的全部社区 ID 列表（去重）
// 超管返回空列表，由调用方自行判断为不限制社区
//
// 参数：
//   - ctx: 上下文
//   - adminID: 管理员 ID
//
// 返回：
//   - []int64: 社区 ID 列表
//   - error: 错误信息
func (s *AdminService) ListAdminCommunityIDs(ctx context.Context, adminID int64) ([]int64, error) {
	a, err := s.getAdminByID(ctx, adminID)
	if err != nil {
		return nil, err
	}
	if a.IsSuper {
		return nil, nil
	}
	var rows []*model.AdminCommunity
	err = s.db.GetDb().WithContext(ctx).
		Where("admin_id = ?", adminID).
		Order("community_id asc").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.CommunityID]; ok {
			continue
		}
		seen[row.CommunityID] = struct{}{}
		ids = append(ids, row.CommunityID)
	}
	return ids, nil
}

// ListAdminAssignments 查询管理员全部社区任职行（含社区名称）
// 供账号管理页「任职社区数」详情弹窗使用；超管返回空（不受社区限制，无任职行）
//
// 参数：
//   - ctx: 上下文
//   - adminID: 管理员 ID
//
// 返回：
//   - map[int64][]*model.AdminCommunity: 社区 ID → 该社区任职行列表（按行 ID 升序）
//   - map[int64]string: 社区 ID → 社区名称
//   - error: 错误信息
func (s *AdminService) ListAdminAssignments(ctx context.Context, adminID int64) (map[int64][]*model.AdminCommunity, map[int64]string, error) {
	a, err := s.getAdminByID(ctx, adminID)
	if err != nil {
		return nil, nil, err
	}
	if a.IsSuper {
		return nil, nil, nil
	}

	var rows []*model.AdminCommunity
	if err := s.db.GetDb().WithContext(ctx).
		Where("admin_id = ?", adminID).
		Order("community_id asc, id asc").
		Find(&rows).Error; err != nil {
		return nil, nil, err
	}

	assignments := make(map[int64][]*model.AdminCommunity)
	communityIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		if _, ok := assignments[row.CommunityID]; !ok {
			communityIDs = append(communityIDs, row.CommunityID)
		}
		assignments[row.CommunityID] = append(assignments[row.CommunityID], row)
	}

	names := make(map[int64]string, len(communityIDs))
	if len(communityIDs) > 0 {
		var communities []*model.Community
		if err := s.db.GetDb().WithContext(ctx).
			Select("id, name").
			Where("id IN ?", communityIDs).
			Find(&communities).Error; err != nil {
			return nil, nil, err
		}
		for _, c := range communities {
			names[c.ID] = c.Name
		}
	}
	return assignments, names, nil
}

// GetAdminContext 实时解析管理员在指定社区的权限上下文
// 超管不受社区约束；普通管理员权限 = 该社区全部任职角色的权限并集，
// 再与本社区权限上限（内置社区管理员角色的权限集）取交集——
// 超管在「社区权限」页收紧上限后立即对全员生效，无需清理存量角色
//
// 参数：
//   - ctx: 上下文
//   - adminID: 管理员 ID
//   - communityID: 社区 ID
//
// 返回：
//   - isSuper: 是否超管
//   - roleIDs: 任职角色 ID 列表
//   - permissions: 有效权限（可能含 "*" 通配；存在上限且上限不含 "*" 时为并集 ∩ 上限）
//   - bound: 是否在该社区有任职
//   - err: 错误信息
func (s *AdminService) GetAdminContext(ctx context.Context, adminID, communityID int64) (isSuper bool, roleIDs []int64, permissions []string, bound bool, err error) {
	a, err := s.getAdminByID(ctx, adminID)
	if err != nil {
		return false, nil, nil, false, err
	}
	if a.IsSuper {
		return true, nil, nil, true, nil
	}

	var rows []*model.AdminCommunity
	q := s.db.GetDb().WithContext(ctx).Where("admin_id = ?", adminID)
	if communityID > 0 {
		q = q.Where("community_id = ?", communityID)
	}
	if err := q.Find(&rows).Error; err != nil {
		return false, nil, nil, false, err
	}
	if len(rows) == 0 {
		return false, nil, []string{}, false, nil
	}

	roleIDs = make([]int64, 0, len(rows))
	for _, row := range rows {
		roleIDs = append(roleIDs, row.RoleID)
	}
	roles, err := s.roleService.GetRolesByIDs(ctx, roleIDs)
	if err != nil {
		return false, nil, nil, false, err
	}

	permSet := make(map[string]struct{})
	for _, id := range roleIDs {
		r := roles[id]
		if r == nil {
			continue
		}
		var perms []string
		_ = json.Unmarshal([]byte(r.Permissions), &perms)
		for _, p := range perms {
			permSet[p] = struct{}{}
		}
	}
	permissions = make([]string, 0, len(permSet))
	for p := range permSet {
		permissions = append(permissions, p)
	}

	// 社区权限上限（内置社区管理员角色）：communityID = 0（未指定社区语境）时不做交集
	ceiling, err := s.roleService.GetBuiltinCommunityAdminRole(ctx, communityID)
	if err != nil {
		return false, nil, nil, false, err
	}
	if ceiling != nil {
		var ceilingPerms []string
		_ = json.Unmarshal([]byte(ceiling.Permissions), &ceilingPerms)
		permissions = intersectPermissions(permissions, ceilingPerms)
	}

	// 通配符只属于超管内置角色：经上限交集后仍残留 "*" 的（存量 ["*"] 上限、
	// 内置角色尚未配置的社区），展开为社区默认权限集合，
	// 避免社区管理员自建含 "*" 或 settings:* 的角色后读写全局支付配置（商户号 / APIv3Key）
	permissions = expandCommunityPermissions(permissions)

	return false, roleIDs, permissions, true, nil
}

// expandCommunityPermissions 将残留的 "*" 通配展开为社区默认权限集合
// 上限交集已把 "*" 收敛为"已配置上限"社区的权限集，此处只处理交集未生效的社区：
// 用 DefaultCommunityAdminPermissions 兜底，其中不含 community/region/settings 等系统级权限点
//
// 参数：
//   - permissions: 上限交集后的有效权限
//
// 返回：
//   - []string: 不含通配符的权限列表（保序去重）
func expandCommunityPermissions(permissions []string) []string {
	hasWildcard := false
	for _, p := range permissions {
		if p == "*" {
			hasWildcard = true
			break
		}
	}
	if !hasWildcard {
		return permissions
	}

	result := make([]string, 0, len(permissions)+len(DefaultCommunityAdminPermissions))
	seen := make(map[string]struct{}, len(permissions))
	for _, p := range permissions {
		if p == "*" {
			continue
		}
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			result = append(result, p)
		}
	}
	for _, p := range DefaultCommunityAdminPermissions {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			result = append(result, p)
		}
	}
	return result
}

// intersectPermissions 有效权限与社区权限上限取交集
// 任一侧含 "*" 通配时视为该侧无限制：上限含 "*" 直接返回有效权限；
// 有效权限含 "*"（如任职角色仍为旧的全量内置角色）则收敛为上限本身
//
// 参数：
//   - effective: 角色权限并集
//   - ceiling: 社区权限上限（内置角色权限集）
//
// 返回：
//   - []string: 交集结果（保序去重）
func intersectPermissions(effective, ceiling []string) []string {
	if len(ceiling) == 0 {
		// 无上限配置：不限制（兼容未初始化内置角色的存量社区）
		return effective
	}
	ceilingSet := make(map[string]struct{}, len(ceiling))
	for _, p := range ceiling {
		ceilingSet[p] = struct{}{}
	}
	if _, ok := ceilingSet["*"]; ok {
		return effective
	}

	result := make([]string, 0, len(effective))
	seen := make(map[string]struct{}, len(effective))
	for _, p := range effective {
		if p == "*" {
			// 有效权限为通配但上限有限：收敛为上限全部权限点
			for _, cp := range ceiling {
				if _, ok := seen[cp]; !ok {
					seen[cp] = struct{}{}
					result = append(result, cp)
				}
			}
			return result
		}
		if _, ok := ceilingSet[p]; ok {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				result = append(result, p)
			}
		}
	}
	return result
}

// validateRolesForCommunity 校验角色列表可被分配到指定社区：
// 角色必须存在且属于该社区；内置社区管理员角色仅超管可分配
//
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//   - roleIDs: 角色 ID 列表
//   - operatorIsSuper: 操作者是否超管
//
// 返回：
//   - error: 错误信息
func (s *AdminService) validateRolesForCommunity(ctx context.Context, communityID int64, roleIDs []int64, operatorIsSuper bool) error {
	roles, err := s.roleService.GetRolesByIDs(ctx, roleIDs)
	if err != nil {
		return err
	}
	for _, id := range roleIDs {
		r := roles[id]
		if r == nil {
			return fmt.Errorf("角色不存在（ID %d）", id)
		}
		if r.CommunityID != communityID {
			return fmt.Errorf("角色「%s」不属于当前社区", r.Name)
		}
		if r.IsBuiltinCommunityAdmin() && !operatorIsSuper {
			return fmt.Errorf("仅超级管理员可设置社区管理员")
		}
	}
	return nil
}

// replaceAssignmentsTx 在给定事务内全量覆盖管理员在指定社区的角色任职
// 若角色列表包含该社区内置社区管理员角色，则自动替换原任职者（每社区仅一人）
//
// 参数：
//   - tx: gorm 事务
//   - adminID: 管理员 ID
//   - communityID: 社区 ID
//   - roleIDs: 角色 ID 列表
//   - operatorIsSuper: 操作者是否超管（防越权兜底）
//
// 返回：
//   - error: 错误信息
func replaceAssignmentsTx(tx *gorm.DB, adminID int64, communityID int64, roleIDs []int64, operatorIsSuper bool) error {
	// 内置社区管理员角色替换语义：该社区此角色仅保留当前操作目标一人
	for _, id := range roleIDs {
		var r model.Role
		if err := tx.First(&r, id).Error; err != nil {
			return fmt.Errorf("角色不存在（ID %d）", id)
		}
		if r.IsBuiltinCommunityAdmin() && r.CommunityID == communityID {
			if !operatorIsSuper {
				return fmt.Errorf("仅超级管理员可设置社区管理员")
			}
			if err := tx.
				Where("community_id = ? AND role_id = ? AND admin_id <> ?", communityID, id, adminID).
				Delete(&model.AdminCommunity{}).Error; err != nil {
				return err
			}
		}
	}

	if err := tx.
		Where("admin_id = ? AND community_id = ?", adminID, communityID).
		Delete(&model.AdminCommunity{}).Error; err != nil {
		return err
	}
	for _, rid := range roleIDs {
		if rid <= 0 {
			continue
		}
		if err := tx.Create(&model.AdminCommunity{
			AdminID:     adminID,
			CommunityID: communityID,
			RoleID:      rid,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// hashPassword 对密码进行 bcrypt 哈希
// 参数：
//   - password: 明文密码
//
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
//
// 返回：
//   - error: 校验错误信息
func CheckPassword(password, hashed string) error {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(password))
}

// ToAdminInfo 将管理员模型转换为 proto 管理员信息
// 参数：
//   - a: 管理员模型
//   - communityIDs: 任职社区 ID 列表（可选，超管传空）
//   - roles: 语境社区内的角色列表（可选）
//
// 返回：
//   - *user.AdminInfo: proto 管理员信息
func ToAdminInfo(a *model.Admin, communityIDs []int64, roles []*model.Role) *user.AdminInfo {
	if a == nil {
		return nil
	}
	info := &user.AdminInfo{
		Id:           a.ID,
		Username:     a.Username,
		Password:     a.Password,
		RealName:     a.RealName,
		Phone:        a.Phone,
		CommunityIds: communityIDs,
		Status:       a.Status,
		LastLoginAt:  ptrString(a.LastLoginAt),
		LastLoginIp:  a.LastLoginIP,
		IsSuper:      a.IsSuper,
	}
	// 绑定的会员 ID（0=未绑定；绑定后该会员在任职社区即履约人员）
	if a.UserID != nil {
		info.UserId = *a.UserID
	}
	// 旧单角色字段兼容填充：前端仅用 role_code 判定超管
	if a.IsSuper {
		info.RoleCode = model.RoleCodeSuperAdmin
		info.RoleName = "超级管理员"
	}
	for _, r := range roles {
		info.Roles = append(info.Roles, ToRoleInfo(r))
		if !a.IsSuper && info.RoleCode == "" {
			info.RoleCode = "admin"
			info.RoleName = r.Name
		}
	}
	if info.RoleCode == "" {
		info.RoleCode = "admin"
	}
	return info
}
