package main

import (
	stdctx "context"
	"slices"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
	"github.com/jcbowen/market/src/backend/services/user/service"
)

// server 实现 user.UserServiceServer 接口
type server struct {
	user.UnimplementedUserServiceServer
	userService      *service.UserService
	addressService   *service.AddressService
	communityService *service.CommunityService
	adminService     *service.AdminService
	roleService      *service.RoleService
	regionService    *service.RegionService
	channelService   *service.ChannelService
	siteService      *service.SiteService
}

// newServer 创建用户服务 gRPC 实现
// 参数：
//   - db: MySQL 实例
//
// 返回：
//   - *server: gRPC 服务实现
func newServer(db *mysql.Instance) *server {
	return &server{
		userService:      service.NewUserService(db),
		addressService:   service.NewAddressService(db),
		communityService: service.NewCommunityService(db),
		adminService:     service.NewAdminService(db),
		roleService:      service.NewRoleService(db),
		regionService:    service.NewRegionService(db),
		channelService:   service.NewChannelService(db),
		siteService:      service.NewSiteService(db),
	}
}

// GetUserById 根据 ID 获取用户信息（携带社区语境时做关联校验，防跨社区越权读取）
func (s *server) GetUserById(ctx stdctx.Context, req *user.GetUserByIdReq) (*user.UserInfo, error) {
	u, err := s.userService.GetUserByIdWithScope(ctx, req.Id, req.CommunityId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return s.toUserInfoOut(ctx, u), nil
}

// GetUserByPhone 根据手机号获取用户信息
func (s *server) GetUserByPhone(ctx stdctx.Context, req *user.GetUserByPhoneReq) (*user.UserInfo, error) {
	u, err := s.userService.GetUserByPhone(ctx, req.Phone)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return s.toUserInfoOut(ctx, u), nil
}

// CreateOrUpdateUser 创建或更新用户
func (s *server) CreateOrUpdateUser(ctx stdctx.Context, req *user.CreateUserReq) (*user.UserInfo, error) {
	u, err := s.userService.CreateOrUpdateUser(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return s.toUserInfoOut(ctx, u), nil
}

// UpdateUserCommunity 更新用户所属社区
func (s *server) UpdateUserCommunity(ctx stdctx.Context, req *user.UpdateCommunityReq) (*common.CommonResponse, error) {
	if err := s.userService.UpdateUserCommunity(ctx, req.UserId, req.CommunityId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// UpdateUserProfile 更新用户资料
func (s *server) UpdateUserProfile(ctx stdctx.Context, req *user.UpdateUserProfileReq) (*user.UserInfo, error) {
	u, err := s.userService.UpdateUserProfile(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return s.toUserInfoOut(ctx, u), nil
}

// UpdateMember 后台编辑会员基础资料（昵称/性别/生日，社区语境校验）
func (s *server) UpdateMember(ctx stdctx.Context, req *user.UpdateMemberReq) (*user.UserInfo, error) {
	u, err := s.userService.UpdateMember(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return s.toUserInfoOut(ctx, u), nil
}

// ListUsers 获取会员列表（按社区关联过滤，返回过滤语境社区名称）
func (s *server) ListUsers(ctx stdctx.Context, req *user.ListUsersReq) (*user.ListUsersResp, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	list, total, communityName, err := s.userService.ListUsers(ctx, req.CommunityId, req.Keyword, req.Status, req.StatusFilter, page, pageSize)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	resp := &user.ListUsersResp{
		List: make([]*user.UserInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, u := range list {
		info := s.toUserInfoOut(ctx, u)
		info.CommunityName = communityName
		resp.List = append(resp.List, info)
	}
	return resp, nil
}

// BatchGetUsers 按 ID 集合批量查询会员（后台订单列表一次往返取回整页会员名）
// community_id 为操作者社区语境：>0 时只回传与该社区有关联的会员，越权 ID 静默剔除
func (s *server) BatchGetUsers(ctx stdctx.Context, req *user.BatchGetUsersReq) (*user.BatchGetUsersResp, error) {
	list, err := s.userService.BatchGetUsers(ctx, req.Ids, req.CommunityId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	infos := make([]*user.UserInfo, 0, len(list))
	for _, u := range list {
		infos = append(infos, s.toUserInfoOut(ctx, u))
	}
	return &user.BatchGetUsersResp{List: infos}, nil
}

// MarkUserCommunity 幂等记录会员-社区关联
func (s *server) MarkUserCommunity(ctx stdctx.Context, req *user.MarkUserCommunityReq) (*common.CommonResponse, error) {
	if err := s.userService.MarkUserCommunity(ctx, req.UserId, req.CommunityId, req.Source); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListFulfillers 列出某社区全部履约人员（本社区任职且绑定会员的员工）
func (s *server) ListFulfillers(ctx stdctx.Context, req *user.ListFulfillersReq) (*user.ListFulfillersResp, error) {
	list, err := s.userService.ListFulfillers(ctx, req.CommunityId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &user.ListFulfillersResp{List: list}, nil
}

// CheckFulfiller 校验用户在某社区是否履约人员
func (s *server) CheckFulfiller(ctx stdctx.Context, req *user.CheckFulfillerReq) (*user.CheckFulfillerResp, error) {
	ok, err := s.userService.CheckFulfiller(ctx, req.UserId, req.CommunityId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &user.CheckFulfillerResp{IsFulfiller: ok}, nil
}

// SetUserStatus 设置会员状态（拉黑/恢复）
func (s *server) SetUserStatus(ctx stdctx.Context, req *user.SetUserStatusReq) (*common.CommonResponse, error) {
	if err := s.userService.SetUserStatus(ctx, req.Id, req.Status, req.CommunityId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// CreateMember 后台添加会员（手机号注册到指定社区）
func (s *server) CreateMember(ctx stdctx.Context, req *user.CreateMemberReq) (*user.CreateMemberResp, error) {
	u, isNew, err := s.userService.CreateMember(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &user.CreateMemberResp{User: s.toUserInfoOut(ctx, u), IsNew: isNew}, nil
}

// ListAddresses 获取地址列表（附带社区名称，供 C 端按社区分组）
func (s *server) ListAddresses(ctx stdctx.Context, req *user.ListAddressesReq) (*user.ListAddressesResp, error) {
	list, communityNames, err := s.addressService.ListAddresses(ctx, req.UserId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &user.ListAddressesResp{List: make([]*user.AddressInfo, 0, len(list))}
	for _, addr := range list {
		resp.List = append(resp.List, service.ToAddressInfo(addr, communityNames[addr.CommunityID]))
	}
	return resp, nil
}

// GetAddress 获取地址详情
func (s *server) GetAddress(ctx stdctx.Context, req *user.GetAddressReq) (*user.AddressInfo, error) {
	addr, err := s.addressService.GetAddress(ctx, req.Id, req.UserId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return service.ToAddressInfo(addr, s.addressService.GetCommunityName(ctx, addr.CommunityID)), nil
}

// CreateAddress 创建地址
func (s *server) CreateAddress(ctx stdctx.Context, req *user.CreateAddressReq) (*user.AddressInfo, error) {
	addr, err := s.addressService.CreateAddress(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToAddressInfo(addr, s.addressService.GetCommunityName(ctx, addr.CommunityID)), nil
}

// UpdateAddress 更新地址
func (s *server) UpdateAddress(ctx stdctx.Context, req *user.UpdateAddressReq) (*user.AddressInfo, error) {
	addr, err := s.addressService.UpdateAddress(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToAddressInfo(addr, s.addressService.GetCommunityName(ctx, addr.CommunityID)), nil
}

// DeleteAddress 删除地址
func (s *server) DeleteAddress(ctx stdctx.Context, req *user.DeleteAddressReq) (*common.CommonResponse, error) {
	if err := s.addressService.DeleteAddress(ctx, req.Id, req.UserId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// SetDefaultAddress 设置默认地址
func (s *server) SetDefaultAddress(ctx stdctx.Context, req *user.SetDefaultAddressReq) (*common.CommonResponse, error) {
	if err := s.addressService.SetDefaultAddress(ctx, req.Id, req.UserId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListCommunities 获取社区列表
func (s *server) ListCommunities(ctx stdctx.Context, req *user.ListCommunitiesReq) (*user.ListCommunitiesResp, error) {
	page, pageSize := 1, 10
	if req != nil && req.Pagination != nil {
		page, pageSize = int(req.Pagination.Page), int(req.Pagination.PageSize)
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	// 与服务层 ListCommunities 同口径封顶，保证回显的 page_size 与实际返回一致
	if pageSize > 200 {
		pageSize = 200
	}
	list, total, err := s.communityService.ListCommunities(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &user.ListCommunitiesResp{
		List: make([]*user.CommunityInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, c := range list {
		resp.List = append(resp.List, service.ToCommunityInfo(c))
	}
	return resp, nil
}

// GetCommunity 获取社区详情
func (s *server) GetCommunity(ctx stdctx.Context, req *user.GetCommunityReq) (*user.CommunityInfo, error) {
	c, err := s.communityService.GetCommunity(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return service.ToCommunityInfo(c), nil
}

// ListNearbyCommunities 获取附近社区
func (s *server) ListNearbyCommunities(ctx stdctx.Context, req *user.ListNearbyCommunitiesReq) (*user.ListCommunitiesResp, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	list, total, err := s.communityService.ListNearbyCommunities(ctx, req.Longitude, req.Latitude, page, pageSize)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &user.ListCommunitiesResp{
		List: make([]*user.CommunityInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, c := range list {
		resp.List = append(resp.List, service.ToCommunityInfo(c))
	}
	return resp, nil
}

// CreateCommunity 创建社区
func (s *server) CreateCommunity(ctx stdctx.Context, req *user.CreateCommunityReq) (*user.CommunityInfo, error) {
	c, err := s.communityService.CreateCommunity(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToCommunityInfo(c), nil
}

// UpdateCommunity 更新社区
func (s *server) UpdateCommunity(ctx stdctx.Context, req *user.UpdateCommunityInfoReq) (*user.CommunityInfo, error) {
	c, err := s.communityService.UpdateCommunity(ctx, req)
	if err != nil {
		if err.Error() == "社区不存在" {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, bizerr.ToStatus(err)
	}
	return service.ToCommunityInfo(c), nil
}

// DeleteCommunity 删除社区
func (s *server) DeleteCommunity(ctx stdctx.Context, req *user.DeleteCommunityReq) (*common.CommonResponse, error) {
	if err := s.communityService.DeleteCommunity(ctx, req.Id); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// GetAdminByUsername 根据用户名获取管理员
// 错误口径按 bizerr 状态码透传：账号不存在为 NotFound，数据库故障等为 Internal，
// 网关登录链路依赖该区分决定是否计入失败锁定
func (s *server) GetAdminByUsername(ctx stdctx.Context, req *user.GetAdminByUsernameReq) (*user.AdminInfo, error) {
	admin, err := s.adminService.GetAdminByUsername(ctx, req.Username)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	communityIDs, _ := s.adminService.ListAdminCommunityIDs(ctx, admin.ID)
	return service.ToAdminInfo(admin, communityIDs, nil), nil
}

// GetAdminById 按 ID 查询管理员（refresh 链路校验账号状态：停用/删除即失效）
func (s *server) GetAdminById(ctx stdctx.Context, req *user.GetAdminByIdReq) (*user.AdminInfo, error) {
	admin, err := s.adminService.GetAdminById(ctx, req.Id)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	communityIDs, _ := s.adminService.ListAdminCommunityIDs(ctx, admin.ID)
	return service.ToAdminInfo(admin, communityIDs, nil), nil
}

// GetAdminContext 实时解析管理员在指定社区的权限上下文
func (s *server) GetAdminContext(ctx stdctx.Context, req *user.GetAdminContextReq) (*user.AdminContextInfo, error) {
	isSuper, roleIDs, permissions, bound, err := s.adminService.GetAdminContext(ctx, req.AdminId, req.CommunityId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	if permissions == nil {
		permissions = []string{}
	}
	if roleIDs == nil {
		roleIDs = []int64{}
	}
	return &user.AdminContextInfo{
		IsSuper:     isSuper,
		RoleIds:     roleIDs,
		Permissions: permissions,
		Bound:       bound,
	}, nil
}

// ListAdmins 获取员工列表（按任职社区语境）
func (s *server) ListAdmins(ctx stdctx.Context, req *user.ListAdminsReq) (*user.ListAdminsResp, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	list, assignments, total, err := s.adminService.ListAdmins(ctx, req.CommunityId, req.Keyword, page, pageSize)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	// 批量加载任职行涉及的角色
	roleIDSet := make(map[int64]struct{})
	for _, rows := range assignments {
		for _, row := range rows {
			roleIDSet[row.RoleID] = struct{}{}
		}
	}
	roleIDs := make([]int64, 0, len(roleIDSet))
	for id := range roleIDSet {
		roleIDs = append(roleIDs, id)
	}
	rolesByID, err := s.roleService.GetRolesByIDs(ctx, roleIDs)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	// 批量加载绑定会员（员工列表「关联会员」列展示 + 编辑回显用）
	memberIDs := make([]int64, 0, len(list))
	for _, a := range list {
		if a.UserID != nil {
			memberIDs = append(memberIDs, *a.UserID)
		}
	}
	memberByID, err := s.adminService.ListMembersByIDs(ctx, memberIDs)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	resp := &user.ListAdminsResp{
		List: make([]*user.AdminInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, a := range list {
		var roles []*model.Role
		for _, row := range assignments[a.ID] {
			if r := rolesByID[row.RoleID]; r != nil {
				roles = append(roles, r)
			}
		}
		communityIDs, _ := s.adminService.ListAdminCommunityIDs(ctx, a.ID)
		info := service.ToAdminInfo(a, communityIDs, roles)
		if m := memberByID[info.UserId]; m != nil {
			info.UserNickname = m.Nickname
			info.UserPhone = m.Phone
		}
		resp.List = append(resp.List, info)
	}
	return resp, nil
}

// CreateAdmin 创建员工（社区任职）
func (s *server) CreateAdmin(ctx stdctx.Context, req *user.CreateAdminReq) (*user.AdminInfo, error) {
	admin, err := s.adminService.CreateAdmin(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	communityIDs, _ := s.adminService.ListAdminCommunityIDs(ctx, admin.ID)
	return service.ToAdminInfo(admin, communityIDs, nil), nil
}

// UpdateAdmin 更新员工
func (s *server) UpdateAdmin(ctx stdctx.Context, req *user.UpdateAdminReq) (*user.AdminInfo, error) {
	admin, err := s.adminService.UpdateAdmin(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	communityIDs, _ := s.adminService.ListAdminCommunityIDs(ctx, admin.ID)
	return service.ToAdminInfo(admin, communityIDs, nil), nil
}

// DeleteAdmin 移除员工在指定社区的任职
func (s *server) DeleteAdmin(ctx stdctx.Context, req *user.DeleteAdminReq) (*common.CommonResponse, error) {
	if err := s.adminService.DeleteAdmin(ctx, req.Id, req.CommunityId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// GetAdminAssignments 查询账号任职的全部社区与角色（账号管理页任职详情弹窗）
// 超管返回空列表（不受社区限制）；普通账号按社区 ID 升序返回
func (s *server) GetAdminAssignments(ctx stdctx.Context, req *user.GetAdminAssignmentsReq) (*user.GetAdminAssignmentsResp, error) {
	assignments, communityNames, err := s.adminService.ListAdminAssignments(ctx, req.AdminId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	// 批量加载任职行涉及的角色
	roleIDSet := make(map[int64]struct{})
	for _, rows := range assignments {
		for _, row := range rows {
			roleIDSet[row.RoleID] = struct{}{}
		}
	}
	roleIDs := make([]int64, 0, len(roleIDSet))
	for id := range roleIDSet {
		roleIDs = append(roleIDs, id)
	}
	rolesByID, err := s.roleService.GetRolesByIDs(ctx, roleIDs)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	communityIDs := make([]int64, 0, len(assignments))
	for id := range assignments {
		communityIDs = append(communityIDs, id)
	}
	slices.Sort(communityIDs)

	resp := &user.GetAdminAssignmentsResp{List: make([]*user.AdminAssignment, 0, len(communityIDs))}
	for _, communityID := range communityIDs {
		item := &user.AdminAssignment{
			CommunityId:   communityID,
			CommunityName: communityNames[communityID],
		}
		for _, row := range assignments[communityID] {
			if r := rolesByID[row.RoleID]; r != nil {
				item.Roles = append(item.Roles, service.ToRoleInfo(r))
			}
		}
		resp.List = append(resp.List, item)
	}
	return resp, nil
}

// ChangeAdminPassword 修改管理员密码（force=true 时跳过旧密码校验，须由网关校验超管身份）
func (s *server) ChangeAdminPassword(ctx stdctx.Context, req *user.ChangeAdminPasswordReq) (*common.CommonResponse, error) {
	if err := s.adminService.ChangeAdminPassword(ctx, req.Id, req.OldPassword, req.NewPassword, req.Force); err != nil {
		if err.Error() == "旧密码错误" || err.Error() == "管理员不存在" {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListRoles 获取角色列表（按社区语境；all_communities=true 时返回跨社区总览并回填社区名与任职人数）
func (s *server) ListRoles(ctx stdctx.Context, req *user.ListRolesReq) (*user.ListRolesResp, error) {
	resp := &user.ListRolesResp{}
	if req.AllCommunities {
		overview, err := s.roleService.ListRolesOverview(ctx)
		if err != nil {
			return nil, bizerr.ToStatus(err)
		}
		resp.List = make([]*user.RoleInfo, 0, len(overview))
		for _, item := range overview {
			info := service.ToRoleInfo(item.Role)
			info.CommunityName = item.CommunityName
			info.AssignmentCount = item.AssignmentCount
			resp.List = append(resp.List, info)
		}
		return resp, nil
	}

	list, err := s.roleService.ListRoles(ctx, req.CommunityId, req.IncludeGlobal)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp.List = make([]*user.RoleInfo, 0, len(list))
	for _, r := range list {
		resp.List = append(resp.List, service.ToRoleInfo(r))
	}
	return resp, nil
}

// GetRole 获取单个角色
func (s *server) GetRole(ctx stdctx.Context, req *user.GetRoleReq) (*user.RoleInfo, error) {
	r, err := s.roleService.GetRoleByID(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	if err := service.EnsureScope(req.GetCommunityId(), r.CommunityID, "角色"); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToRoleInfo(r), nil
}

// CreateRole 创建角色
func (s *server) CreateRole(ctx stdctx.Context, req *user.CreateRoleReq) (*user.RoleInfo, error) {
	r, err := s.roleService.CreateRole(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToRoleInfo(r), nil
}

// UpdateRole 更新角色
func (s *server) UpdateRole(ctx stdctx.Context, req *user.UpdateRoleReq) (*user.RoleInfo, error) {
	r, err := s.roleService.UpdateRole(ctx, req)
	if err != nil {
		if err.Error() == "角色不存在" {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, bizerr.ToStatus(err)
	}
	return service.ToRoleInfo(r), nil
}

// DeleteRole 删除角色
func (s *server) DeleteRole(ctx stdctx.Context, req *user.DeleteRoleReq) (*common.CommonResponse, error) {
	if err := s.roleService.DeleteRole(ctx, req.Id, req.CommunityId); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListRegionChildren 按父级代码查询下级区划（级联选择器懒加载）
func (s *server) ListRegionChildren(ctx stdctx.Context, req *user.ListRegionChildrenReq) (*user.ListRegionChildrenResp, error) {
	list, err := s.regionService.ListChildren(ctx, req.ParentCode)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &user.ListRegionChildrenResp{List: make([]*user.RegionInfo, 0, len(list))}
	for _, r := range list {
		resp.List = append(resp.List, service.ToRegionInfo(r))
	}
	return resp, nil
}

// ListRegions 管理端分页查询区划列表
func (s *server) ListRegions(ctx stdctx.Context, req *user.ListRegionsReq) (*user.ListRegionsResp, error) {
	page, pageSize := 1, 20
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	list, total, err := s.regionService.ListRegions(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &user.ListRegionsResp{
		List: make([]*user.RegionInfo, 0, len(list)),
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}
	for _, r := range list {
		resp.List = append(resp.List, service.ToRegionInfo(r))
	}
	return resp, nil
}

// MatchRegionByName 按名称匹配区划（IP 定位反查区划代码）
func (s *server) MatchRegionByName(ctx stdctx.Context, req *user.MatchRegionByNameReq) (*user.RegionMatchInfo, error) {
	city, province, err := s.regionService.MatchRegionByName(ctx, req.Name)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &user.RegionMatchInfo{
		Province: service.ToRegionInfo(province),
		City:     service.ToRegionInfo(city),
	}, nil
}

// CreateRegionUpdateTask 创建区划更新任务（异步执行，返回任务供前端轮询）
func (s *server) CreateRegionUpdateTask(ctx stdctx.Context, req *user.CreateRegionUpdateTaskReq) (*user.RegionUpdateTaskInfo, error) {
	task, err := s.regionService.CreateRegionUpdateTask(ctx, req.SourceType)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToRegionUpdateTaskInfo(task), nil
}

// GetRegionUpdateTask 查询区划更新任务详情（含差异明细）
func (s *server) GetRegionUpdateTask(ctx stdctx.Context, req *user.GetRegionUpdateTaskReq) (*user.RegionUpdateTaskInfo, error) {
	task, err := s.regionService.GetRegionUpdateTask(ctx, req.Id)
	if err != nil {
		if err.Error() == "任务不存在" {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, bizerr.ToStatus(err)
	}
	return service.ToRegionUpdateTaskInfo(task), nil
}

// ApplyRegionUpdateTask 将已完成任务的差异覆盖入库
func (s *server) ApplyRegionUpdateTask(ctx stdctx.Context, req *user.ApplyRegionUpdateTaskReq) (*common.CommonResponse, error) {
	if err := s.regionService.ApplyRegionUpdateTask(ctx, req.Id); err != nil {
		if err.Error() == "任务不存在" || err.Error() == "任务尚未完成，无法应用差异" || err.Error() == "任务差异已应用过，请重新执行检查更新" {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListClientChannels 查询客户端渠道列表
func (s *server) ListClientChannels(ctx stdctx.Context, req *user.ListClientChannelsReq) (*user.ListClientChannelsResp, error) {
	list, err := s.channelService.ListClientChannels(ctx, req.OnlyEnabled)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &user.ListClientChannelsResp{List: make([]*user.ClientChannelInfo, 0, len(list))}
	for _, c := range list {
		resp.List = append(resp.List, service.ToClientChannelInfo(c))
	}
	return resp, nil
}

// GetClientChannel 按渠道 code 查询客户端渠道
func (s *server) GetClientChannel(ctx stdctx.Context, req *user.GetClientChannelReq) (*user.ClientChannelInfo, error) {
	row, err := s.channelService.GetClientChannel(ctx, req.Code)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	if row == nil {
		return nil, status.Error(codes.NotFound, "客户端渠道不存在")
	}
	return service.ToClientChannelInfo(row), nil
}

// SaveClientChannel 保存客户端渠道配置
func (s *server) SaveClientChannel(ctx stdctx.Context, req *user.SaveClientChannelReq) (*user.ClientChannelInfo, error) {
	row, err := s.channelService.SaveClientChannel(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToClientChannelInfo(row), nil
}

// GetUserChannelOpenid 查询用户在指定渠道的微信 openid（mp 渠道回落存量 users.openid）
func (s *server) GetUserChannelOpenid(ctx stdctx.Context, req *user.GetUserChannelOpenidReq) (*user.GetUserChannelOpenidResp, error) {
	openid, unionid, err := s.channelService.GetUserChannelOpenid(ctx, req.UserId, req.ChannelCode)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &user.GetUserChannelOpenidResp{Openid: openid, Unionid: unionid}, nil
}

// UpsertUserChannelIdentity 幂等写入/更新用户渠道身份
func (s *server) UpsertUserChannelIdentity(ctx stdctx.Context, req *user.UpsertUserChannelIdentityReq) (*common.CommonResponse, error) {
	if err := s.channelService.UpsertUserChannelIdentity(ctx, req.UserId, req.ChannelCode, req.Openid, req.Unionid); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// GetSiteConfig 读取站点设置（平台品牌与部署域名）
func (s *server) GetSiteConfig(ctx stdctx.Context, req *user.GetSiteConfigReq) (*user.SiteConfigInfo, error) {
	row, err := s.siteService.GetSiteConfig(ctx)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return s.toSiteConfigOut(ctx, row), nil
}

// SaveSiteConfig 保存站点设置，版本号递增以驱动网关缓存失效
func (s *server) SaveSiteConfig(ctx stdctx.Context, req *user.SaveSiteConfigReq) (*user.SiteConfigInfo, error) {
	row, err := s.siteService.SaveSiteConfig(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return s.toSiteConfigOut(ctx, row), nil
}

// toSiteConfigOut 出库统一补全品牌资源的完整访问 URL（logo/favicon，外链原样）
//
// 参数:
//   - ctx: 上下文
//   - row: 站点设置行
//
// 返回值:
//   - *user.SiteConfigInfo: proto 站点设置（附件地址已拼完整 URL）
func (s *server) toSiteConfigOut(ctx stdctx.Context, row *model.SiteConfig) *user.SiteConfigInfo {
	info := service.ToSiteConfigInfo(row)
	if info == nil {
		return nil
	}
	info.LogoUrl = s.siteService.MediaURL(ctx, info.LogoUrl)
	info.FaviconUrl = s.siteService.MediaURL(ctx, info.FaviconUrl)
	return info
}

// toUserInfoOut 出库统一补全头像的完整访问 URL（外链原样）
//
// 参数:
//   - ctx: 上下文
//   - u: 用户模型
//
// 返回值:
//   - *user.UserInfo: proto 用户信息（头像已拼完整 URL）
func (s *server) toUserInfoOut(ctx stdctx.Context, u *model.User) *user.UserInfo {
	info := service.ToUserInfo(u)
	if info == nil {
		return nil
	}
	info.Avatar = s.userService.MediaURL(ctx, info.Avatar)
	return info
}
