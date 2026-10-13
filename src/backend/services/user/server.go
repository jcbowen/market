package main

import (
	stdctx "context"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/user"
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
}

// newServer 创建用户服务 gRPC 实现
// 参数：
//   - db: MySQL 实例
// 返回：
//   - *server: gRPC 服务实现
func newServer(db *mysql.Instance) *server {
	return &server{
		userService:      service.NewUserService(db),
		addressService:   service.NewAddressService(db),
		communityService: service.NewCommunityService(db),
		adminService:     service.NewAdminService(db),
		roleService:      service.NewRoleService(db),
	}
}

// GetUserById 根据 ID 获取用户信息
func (s *server) GetUserById(ctx stdctx.Context, req *user.GetUserByIdReq) (*user.UserInfo, error) {
	u, err := s.userService.GetUserById(ctx, req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return service.ToUserInfo(u), nil
}

// GetUserByPhone 根据手机号获取用户信息
func (s *server) GetUserByPhone(ctx stdctx.Context, req *user.GetUserByPhoneReq) (*user.UserInfo, error) {
	u, err := s.userService.GetUserByPhone(ctx, req.Phone)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return service.ToUserInfo(u), nil
}

// CreateOrUpdateUser 创建或更新用户
func (s *server) CreateOrUpdateUser(ctx stdctx.Context, req *user.CreateUserReq) (*user.UserInfo, error) {
	u, err := s.userService.CreateOrUpdateUser(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToUserInfo(u), nil
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
	return service.ToUserInfo(u), nil
}

// ListUsers 获取用户列表
func (s *server) ListUsers(ctx stdctx.Context, req *user.ListUsersReq) (*user.ListUsersResp, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	list, total, err := s.userService.ListUsers(ctx, req.CommunityId, req.Keyword, req.Status, page, pageSize)
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
		resp.List = append(resp.List, service.ToUserInfo(u))
	}
	return resp, nil
}

// ListAddresses 获取地址列表
func (s *server) ListAddresses(ctx stdctx.Context, req *user.ListAddressesReq) (*user.ListAddressesResp, error) {
	list, err := s.addressService.ListAddresses(ctx, req.UserId)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &user.ListAddressesResp{List: make([]*user.AddressInfo, 0, len(list))}
	for _, addr := range list {
		resp.List = append(resp.List, service.ToAddressInfo(addr))
	}
	return resp, nil
}

// GetAddress 获取地址详情
func (s *server) GetAddress(ctx stdctx.Context, req *user.GetAddressReq) (*user.AddressInfo, error) {
	addr, err := s.addressService.GetAddress(ctx, req.Id, req.UserId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return service.ToAddressInfo(addr), nil
}

// CreateAddress 创建地址
func (s *server) CreateAddress(ctx stdctx.Context, req *user.CreateAddressReq) (*user.AddressInfo, error) {
	addr, err := s.addressService.CreateAddress(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToAddressInfo(addr), nil
}

// UpdateAddress 更新地址
func (s *server) UpdateAddress(ctx stdctx.Context, req *user.UpdateAddressReq) (*user.AddressInfo, error) {
	addr, err := s.addressService.UpdateAddress(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return service.ToAddressInfo(addr), nil
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
func (s *server) ListCommunities(ctx stdctx.Context, req *common.Pagination) (*user.ListCommunitiesResp, error) {
	page, pageSize := int(req.Page), int(req.PageSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	list, total, err := s.communityService.ListCommunities(ctx, page, pageSize)
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
func (s *server) GetAdminByUsername(ctx stdctx.Context, req *user.GetAdminByUsernameReq) (*user.AdminInfo, error) {
	admin, err := s.adminService.GetAdminByUsername(ctx, req.Username)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	role, _ := s.roleService.GetRoleByID(ctx, admin.RoleID)
	isSuperAdmin := role != nil && role.Code == "super_admin"
	communityIDs, _ := s.adminService.ListAdminCommunityIDs(ctx, admin.ID, isSuperAdmin)
	return service.ToAdminInfo(admin, role, communityIDs), nil
}

// ListAdmins 获取管理员列表
func (s *server) ListAdmins(ctx stdctx.Context, req *user.ListAdminsReq) (*user.ListAdminsResp, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	list, total, err := s.adminService.ListAdmins(ctx, req.CommunityId, page, pageSize)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}

	// 批量查询各管理员的社区绑定关系
	adminIDs := make([]int64, 0, len(list))
	for _, a := range list {
		adminIDs = append(adminIDs, a.ID)
	}
	communityIDsByAdmin, err := s.adminService.ListCommunitiesForAdmins(ctx, adminIDs)
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
		role, _ := s.roleService.GetRoleByID(ctx, a.RoleID)
		resp.List = append(resp.List, service.ToAdminInfo(a, role, communityIDsByAdmin[a.ID]))
	}
	return resp, nil
}

// CreateAdmin 创建管理员
func (s *server) CreateAdmin(ctx stdctx.Context, req *user.CreateAdminReq) (*user.AdminInfo, error) {
	admin, err := s.adminService.CreateAdmin(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	role, _ := s.roleService.GetRoleByID(ctx, admin.RoleID)
	communityIDs, _ := s.adminService.ListAdminCommunityIDs(ctx, admin.ID, false)
	return service.ToAdminInfo(admin, role, communityIDs), nil
}

// UpdateAdmin 更新管理员
func (s *server) UpdateAdmin(ctx stdctx.Context, req *user.UpdateAdminReq) (*user.AdminInfo, error) {
	admin, err := s.adminService.UpdateAdmin(ctx, req)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	role, _ := s.roleService.GetRoleByID(ctx, admin.RoleID)
	communityIDs, _ := s.adminService.ListAdminCommunityIDs(ctx, admin.ID, false)
	return service.ToAdminInfo(admin, role, communityIDs), nil
}

// DeleteAdmin 删除管理员
func (s *server) DeleteAdmin(ctx stdctx.Context, req *user.DeleteAdminReq) (*common.CommonResponse, error) {
	if err := s.adminService.DeleteAdmin(ctx, req.Id); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ChangeAdminPassword 修改管理员密码
func (s *server) ChangeAdminPassword(ctx stdctx.Context, req *user.ChangeAdminPasswordReq) (*common.CommonResponse, error) {
	if err := s.adminService.ChangeAdminPassword(ctx, req.Id, req.OldPassword, req.NewPassword); err != nil {
		if err.Error() == "旧密码错误" || err.Error() == "管理员不存在" {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}

// ListRoles 获取角色列表
func (s *server) ListRoles(ctx stdctx.Context, req *common.Empty) (*user.ListRolesResp, error) {
	list, err := s.roleService.ListRoles(ctx)
	if err != nil {
		return nil, bizerr.ToStatus(err)
	}
	resp := &user.ListRolesResp{List: make([]*user.RoleInfo, 0, len(list))}
	for _, r := range list {
		resp.List = append(resp.List, service.ToRoleInfo(r))
	}
	return resp, nil
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
	if err := s.roleService.DeleteRole(ctx, req.Id); err != nil {
		return nil, bizerr.ToStatus(err)
	}
	return &common.CommonResponse{Code: 0, Message: "success"}, nil
}
