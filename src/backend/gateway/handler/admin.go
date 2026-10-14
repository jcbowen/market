package handler

import (
	"context"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
)

// AdminHandler 管理员接口处理器
// 封装管理员登录、管理员 CRUD、角色查询、商品/订单/库存/营销/社区/退款/支付配置等管理端 HTTP 接口
type AdminHandler struct {
	jwtManager    *jwt.Manager
	userClient    user.UserServiceClient
	productClient product.ProductServiceClient
	orderClient   order.OrderServiceClient
	paymentClient payment.PaymentServiceClient
	cfg           *config.Config
}

// NewAdminHandler 创建管理员接口处理器
// 参数：
//   - jwtManager: JWT 管理器，用于签发 token
//   - userClient: user-service gRPC 客户端
//   - productClient: product-service gRPC 客户端
//   - orderClient: order-service gRPC 客户端
//   - paymentClient: payment-service gRPC 客户端，用于支付配置读写
//   - cfg: 全局配置指针
//
// 返回值：
//   - *AdminHandler: 管理员处理器实例
func NewAdminHandler(jwtManager *jwt.Manager, userClient user.UserServiceClient, productClient product.ProductServiceClient, orderClient order.OrderServiceClient, paymentClient payment.PaymentServiceClient, cfg *config.Config) *AdminHandler {
	return &AdminHandler{
		jwtManager:    jwtManager,
		userClient:    userClient,
		productClient: productClient,
		orderClient:   orderClient,
		paymentClient: paymentClient,
		cfg:           cfg,
	}
}

// AdminLoginReq 管理员登录请求
// 包含用户名与密码字段
type AdminLoginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// AdminLoginResp 管理员登录响应
// 包含双 token、过期时间及管理员基本信息
type AdminLoginResp struct {
	AccessToken      string      `json:"access_token"`
	RefreshToken     string      `json:"refresh_token"`
	ExpiresIn        int64       `json:"expires_in"`
	RefreshExpiresIn int64       `json:"refresh_expires_in"`
	ExpiresAt        int64       `json:"expires_at"`
	RefreshExpiresAt int64       `json:"refresh_expires_at"`
	Admin            interface{} `json:"admin"`
}

// Login 管理员登录
// 校验用户名密码，校验通过后签发 access_token 与 refresh_token
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) Login(c *gin.Context) {
	var req AdminLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	admin, err := h.userClient.GetAdminByUsername(c.Request.Context(), &user.GetAdminByUsernameReq{Username: req.Username})
	if err != nil {
		response.Error(c, errcode.IncorrectUsernameOrPassword)
		return
	}
	if admin.Status != 1 {
		response.Error(c, errcode.AccountDisabled)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(req.Password)); err != nil {
		response.Error(c, errcode.IncorrectUsernameOrPassword)
		return
	}

	// 权限按默认社区实时解析（多社区模型：不同社区权限不同，JWT 不再承载权限快照）
	communityID := h.getAdminDefaultCommunityID(admin)
	permissions := h.getAdminPermissionsForCommunity(c.Request.Context(), admin.Id, communityID)
	roleCode := "admin"
	if admin.IsSuper {
		roleCode = "super_admin"
	}
	accessToken, accessExpire, err := h.jwtManager.GenerateAdminAccessToken(
		admin.Id, admin.Phone, roleCode, communityID, admin.CommunityIds, nil, admin.Username, admin.RealName,
	)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	refreshToken, refreshExpire, err := h.jwtManager.GenerateRefreshToken(admin.Id, jwt.TokenTypeAdmin)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	now := time.Now()
	response.Success(c, AdminLoginResp{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		ExpiresIn:        int64(accessExpire.Sub(now).Seconds()),
		RefreshExpiresIn: int64(refreshExpire.Sub(now).Seconds()),
		ExpiresAt:        accessExpire.Unix(),
		RefreshExpiresAt: refreshExpire.Unix(),
		Admin: gin.H{
			"id":            admin.Id,
			"username":      admin.Username,
			"real_name":     admin.RealName,
			"phone":         admin.Phone,
			"is_super":      admin.IsSuper,
			"role_code":     roleCode,
			"community_id":  communityID,
			"community_ids": admin.CommunityIds,
			"permissions":   permissions,
		},
	})
}

// Info 获取当前登录管理员信息
// 供前端在刷新页面（store 状态重置）后恢复管理员资料与权限列表；
// 管理员基础资料通过 token 中的 username 反查 user 服务获取（含最新任职社区列表），
// 权限列表按当前社区语境（X-Community-Id）实时解析，与接口鉴权口径保持一致
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) Info(c *gin.Context) {
	uc := appcontext.GetUserContext(c)

	// 当前社区语境：优先请求头声明，缺省回退 JWT 默认社区
	communityID := uc.CommunityID
	if id, err := strconv.ParseInt(c.GetHeader("X-Community-Id"), 10, 64); err == nil && id > 0 {
		communityID = id
	}
	permissions := h.getAdminPermissionsForCommunity(c.Request.Context(), uc.UserID, communityID)

	roleCode := "admin"
	if uc.Role == "super_admin" {
		roleCode = "super_admin"
	}

	var admin *user.AdminInfo
	if uc.Phone != "" || uc.UserID > 0 {
		// token 携带 username 时按用户名反查完整资料（含最新 community_ids）
		if username, ok := c.Get("admin_username"); ok {
			if name, _ := username.(string); name != "" {
				if a, err := h.userClient.GetAdminByUsername(c.Request.Context(), &user.GetAdminByUsernameReq{Username: name}); err == nil {
					admin = a
					if a.IsSuper {
						roleCode = "super_admin"
					}
				}
			}
		}
	}

	resp := gin.H{
		"id":          uc.UserID,
		"phone":       uc.Phone,
		"is_super":    roleCode == "super_admin",
		"role_code":   roleCode,
		"permissions": permissions,
	}
	if admin != nil {
		resp["id"] = admin.Id
		resp["username"] = admin.Username
		resp["real_name"] = admin.RealName
		resp["phone"] = admin.Phone
		resp["is_super"] = admin.IsSuper
		resp["role_code"] = roleCode
		resp["community_id"] = h.getAdminDefaultCommunityID(admin)
		communityIDs := admin.CommunityIds
		if communityIDs == nil {
			communityIDs = []int64{}
		}
		resp["community_ids"] = communityIDs
		resp["status"] = admin.Status
	}

	response.Success(c, resp)
}

// getAdminDefaultCommunityID 获取管理员默认社区 ID
// 优先使用关联社区列表中的第一个，否则回退到原 community_id 字段
//
// 参数：
//   - admin: 管理员信息
//
// 返回值：
//   - int64: 默认社区 ID
func (h *AdminHandler) getAdminDefaultCommunityID(admin *user.AdminInfo) int64 {
	if len(admin.CommunityIds) > 0 {
		return admin.CommunityIds[0]
	}
	return admin.CommunityId
}

// getAdminPermissionsForCommunity 按社区语境实时解析管理员权限（任职角色权限并集）
// 参数：
//   - ctx: 上下文
//   - adminID: 管理员 ID
//   - communityID: 社区 ID
//
// 返回值：
//   - []string: 权限编码列表
func (h *AdminHandler) getAdminPermissionsForCommunity(ctx context.Context, adminID, communityID int64) []string {
	resp, err := h.userClient.GetAdminContext(ctx, &user.GetAdminContextReq{
		AdminId:     adminID,
		CommunityId: communityID,
	})
	if err != nil || resp == nil {
		return []string{}
	}
	// proto 空 repeated 反序列化后为 nil，统一输出空数组避免 JSON null
	if resp.Permissions == nil {
		return []string{}
	}
	return resp.Permissions
}

// getAdminCommunityID 获取管理员当前社区 ID
// 前端通过请求头 X-Community-Id 声明当前操作的社区（与业务筛选参数 community_id 解耦）：
//   - 超管：任意社区 ID 均合法（含未绑定社区）
//   - 普通管理员：仅允许切换到自己绑定的社区，越权值静默回退默认社区
//   - 未携带或值非法时回退 JWT 中的默认社区
//
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - int64: 社区 ID
func (h *AdminHandler) getAdminCommunityID(c *gin.Context) int64 {
	uc := appcontext.GetUserContext(c)
	if header := c.GetHeader("X-Community-Id"); header != "" {
		if id, err := strconv.ParseInt(header, 10, 64); err == nil && id > 0 {
			if uc.Role == "super_admin" || containsInt64(uc.CommunityIDs, id) {
				return id
			}
		}
	}
	return uc.CommunityID
}

// containsInt64 判断 int64 切片是否包含指定元素
// 参数：
//   - list: int64 切片
//   - item: 目标元素
//
// 返回值：
//   - bool: 是否包含
func containsInt64(list []int64, item int64) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

// ListAdmins 获取员工列表（当前社区语境下的任职员工）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdmins(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	keyword := c.Query("keyword")

	resp, err := h.userClient.ListAdmins(c.Request.Context(), &user.ListAdminsReq{
		CommunityId: h.getAdminCommunityID(c),
		Keyword:     keyword,
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}

// CreateAdminReq 创建员工请求
// username 已存在时复用该账号（不修改其密码），仅追加当前社区任职
type CreateAdminReq struct {
	Username string  `json:"username" binding:"required"`
	Password string  `json:"password"`
	RealName string  `json:"real_name"`
	Phone    string  `json:"phone"`
	RoleIDs  []int64 `json:"role_ids" binding:"required,min=1"`
}

// CreateAdmin 创建员工（在当前社区任职）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateAdmin(c *gin.Context) {
	var req CreateAdminReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	admin, err := h.userClient.CreateAdmin(c.Request.Context(), &user.CreateAdminReq{
		Username:        req.Username,
		Password:        req.Password,
		RealName:        req.RealName,
		Phone:           req.Phone,
		CommunityId:     h.getAdminCommunityID(c),
		RoleIds:         req.RoleIDs,
		OperatorIsSuper: uc.Role == "super_admin",
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, h.toAdminVO(admin))
}

// UpdateAdminReq 更新员工请求
type UpdateAdminReq struct {
	RealName string  `json:"real_name"`
	Phone    string  `json:"phone"`
	Status   int32   `json:"status"`
	RoleIDs  []int64 `json:"role_ids"`
}

// UpdateAdmin 更新员工
// 仅超管可修改任意员工（含角色任职）；普通管理员仅可修改自己的基础资料
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateAdmin(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	ctx := appcontext.GetUserContext(c)
	isSuper := ctx.Role == "super_admin"
	if !isSuper && ctx.UserID != id {
		response.Error(c, errcode.NoPermission)
		return
	}

	var req UpdateAdminReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 普通管理员改自己资料时不允许动角色任职
	var roleIDs []int64
	if isSuper {
		roleIDs = req.RoleIDs
	}

	admin, err := h.userClient.UpdateAdmin(c.Request.Context(), &user.UpdateAdminReq{
		Id:              id,
		RealName:        req.RealName,
		Phone:           req.Phone,
		Status:          req.Status,
		CommunityId:     h.getAdminCommunityID(c),
		RoleIds:         roleIDs,
		OperatorIsSuper: isSuper,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, h.toAdminVO(admin))
}

// DeleteAdmin 移除员工（仅移除当前社区任职，账号与其他社区任职保留）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteAdmin(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.userClient.DeleteAdmin(c.Request.Context(), &user.DeleteAdminReq{
		Id:          id,
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// ChangeAdminPasswordReq 修改管理员密码请求
type ChangeAdminPasswordReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

// ChangeAdminPassword 修改管理员密码
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ChangeAdminPassword(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req ChangeAdminPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.userClient.ChangeAdminPassword(c.Request.Context(), &user.ChangeAdminPasswordReq{
		Id:          id,
		OldPassword: req.OldPassword,
		NewPassword: req.NewPassword,
	})
	if err != nil {
		if grpcErr, ok := status.FromError(err); ok && grpcErr.Code() == codes.InvalidArgument {
			response.Error(c, errcode.ParamError, grpcErr.Message())
			return
		}
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// ListRoles 获取角色列表
// 默认返回当前社区的角色；超管可传 scope=global 查看全局角色，
// 或传 community_id 查看任意社区的角色（社区权限分配页使用）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListRoles(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	isSuper := uc.Role == "super_admin"

	req := &user.ListRolesReq{CommunityId: h.getAdminCommunityID(c)}
	if c.Query("scope") == "global" {
		if !isSuper {
			response.Error(c, errcode.NoPermission)
			return
		}
		req.CommunityId = 0
	}
	if id, err := strconv.ParseInt(c.Query("community_id"), 10, 64); err == nil && id > 0 {
		if !isSuper {
			response.Error(c, errcode.NoPermission)
			return
		}
		req.CommunityId = id
	}
	resp, err := h.userClient.ListRoles(c.Request.Context(), req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// CreateRoleReq 创建角色请求
type CreateRoleReq struct {
	Name        string   `json:"name" binding:"required"`
	Code        string   `json:"code" binding:"required"`
	Permissions []string `json:"permissions"`
	Description string   `json:"description"`
	// 角色归属社区：0=全局角色（仅超管可建）；缺省为当前社区
	CommunityID int64 `json:"community_id"`
}

// CreateRole 创建角色
// 非超管仅能在当前社区创建，且权限点不得超出自身权限范围
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateRole(c *gin.Context) {
	var req CreateRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	isSuper := uc.Role == "super_admin"
	communityID := req.CommunityID
	if communityID < 0 || (!isSuper && communityID != 0) {
		response.Error(c, errcode.ParamError)
		return
	}
	if !isSuper {
		communityID = h.getAdminCommunityID(c)
		if err := h.validatePermissionsSubset(c, communityID, req.Permissions); err != nil {
			response.Error(c, errcode.ParamError, err.Error())
			return
		}
	}

	resp, err := h.userClient.CreateRole(c.Request.Context(), &user.CreateRoleReq{
		Name:        req.Name,
		Code:        req.Code,
		Permissions: req.Permissions,
		Description: req.Description,
		CommunityId: communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateRoleReq 更新角色请求
type UpdateRoleReq struct {
	Name        string   `json:"name"`
	Code        string   `json:"code"`
	Permissions []string `json:"permissions"`
	Description string   `json:"description"`
}

// UpdateRole 更新角色
// 全局角色仅超管可改；内置社区管理员角色仅超管可改；
// 非超管的权限点不得超出自身权限范围
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateRole(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	isSuper := uc.Role == "super_admin"

	roleResp, err := h.userClient.GetRole(c.Request.Context(), &user.GetRoleReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	role := roleResp
	if !isSuper {
		if role.CommunityId == 0 || role.CommunityId != h.getAdminCommunityID(c) {
			response.Error(c, errcode.NoPermission)
			return
		}
		if role.Code == "community_admin" {
			response.Error(c, errcode.NoPermission, "内置社区管理员角色仅超级管理员可修改")
			return
		}
		if err := h.validatePermissionsSubset(c, role.CommunityId, req.Permissions); err != nil {
			response.Error(c, errcode.ParamError, err.Error())
			return
		}
	}

	resp, err := h.userClient.UpdateRole(c.Request.Context(), &user.UpdateRoleReq{
		Id:          id,
		Name:        req.Name,
		Code:        req.Code,
		Permissions: req.Permissions,
		Description: req.Description,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteRole 删除角色
// 全局角色仅超管可删；内置社区管理员角色不可删除（服务层校验）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteRole(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	isSuper := uc.Role == "super_admin"

	roleResp, err := h.userClient.GetRole(c.Request.Context(), &user.GetRoleReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if !isSuper {
		if roleResp.CommunityId == 0 || roleResp.CommunityId != h.getAdminCommunityID(c) {
			response.Error(c, errcode.NoPermission)
			return
		}
	}

	_, err = h.userClient.DeleteRole(c.Request.Context(), &user.DeleteRoleReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// GetCommunityPermissions 获取社区权限分配详情（超管专用）
// 返回该社区的权限上限（内置社区管理员角色的权限集）及全部社区角色概览；
// 内置角色不存在时（存量社区）role_id 返回 0、permissions 返回空数组
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetCommunityPermissions(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	if uc.Role != "super_admin" {
		response.Error(c, errcode.NoPermission)
		return
	}

	resp, err := h.userClient.ListRoles(c.Request.Context(), &user.ListRolesReq{CommunityId: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	var ceiling *user.RoleInfo
	for _, r := range resp.List {
		if r.Code == "community_admin" {
			ceiling = r
			break
		}
	}

	permissions := []string{}
	var ceilingID int64
	if ceiling != nil {
		ceilingID = ceiling.Id
		if len(ceiling.Permissions) > 0 {
			permissions = ceiling.Permissions
		}
	}
	response.Success(c, gin.H{
		"community_id": id,
		"role_id":      ceilingID,
		"permissions":  permissions,
		"roles":        resp.List,
	})
}

// UpdateCommunityPermissionsReq 设置社区权限请求
type UpdateCommunityPermissionsReq struct {
	Permissions []string `json:"permissions"`
}

// systemLevelPermissions 系统级权限点：仅超管可用，不进入社区权限上限。
// 对应页面均在系统管理空间（社区管理/行政区划/支付·售后·附件配置），业务菜单不展示；
// 保存社区权限时自动剔除（前端同样过滤，此处兜底），存量 ["*"] 上限社区保存一次即收敛
var systemLevelPermissions = map[string]struct{}{
	"community:view":   {}, "community:create": {}, "community:update": {}, "community:delete": {},
	"region:view": {}, "region:update": {},
	"settings:view": {}, "settings:update": {},
}

// UpdateCommunityPermissions 设置社区权限上限（超管专用）
// 即更新该社区内置社区管理员角色的权限集；内置角色不存在时自动创建
// 权限解析运行时与上限取交集，保存后立即对社区全员生效
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateCommunityPermissions(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateCommunityPermissionsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	if uc.Role != "super_admin" {
		response.Error(c, errcode.NoPermission)
		return
	}

	// 剔除系统级权限点：社区权限上限只承载业务权限
	filtered := make([]string, 0, len(req.Permissions))
	for _, p := range req.Permissions {
		if _, ok := systemLevelPermissions[p]; !ok {
			filtered = append(filtered, p)
		}
	}
	req.Permissions = filtered

	if len(req.Permissions) == 0 {
		response.Error(c, errcode.ParamError, "至少保留一个权限点，否则社区管理员将无法登录后台")
		return
	}

	resp, err := h.userClient.ListRoles(c.Request.Context(), &user.ListRolesReq{CommunityId: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	var ceilingID int64
	for _, r := range resp.List {
		if r.Code == "community_admin" {
			ceilingID = r.Id
			break
		}
	}

	if ceilingID <= 0 {
		// 存量社区未初始化内置角色：随本次分配自动创建
		created, err := h.userClient.CreateRole(c.Request.Context(), &user.CreateRoleReq{
			Name:        "社区管理员",
			Code:        "community_admin",
			Permissions: req.Permissions,
			Description: "内置角色：负责本社区的日常运营与员工管理；其权限即本社区权限上限",
			CommunityId: id,
		})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		ceilingID = created.Id
	} else if _, err := h.userClient.UpdateRole(c.Request.Context(), &user.UpdateRoleReq{
		Id:          ceilingID,
		Permissions: req.Permissions,
	}); err != nil {
		response.GRPCError(c, err)
		return
	}

	response.Success(c, gin.H{"community_id": id, "role_id": ceilingID})
}

// validatePermissionsSubset 校验权限点列表不超出操作者在指定社区的权限范围
// 操作者权限含 "*" 通配（或为超管场景不调用此函数）时放行
// 参数：
//   - c: gin 上下文
//   - communityID: 社区语境
//   - permissions: 待校验的权限点列表
//
// 返回值：
//   - error: 超出范围时返回错误
func (h *AdminHandler) validatePermissionsSubset(c *gin.Context, communityID int64, permissions []string) error {
	if len(permissions) == 0 {
		return nil
	}
	uc := appcontext.GetUserContext(c)
	ctxInfo, err := h.userClient.GetAdminContext(c.Request.Context(), &user.GetAdminContextReq{
		AdminId:     uc.UserID,
		CommunityId: communityID,
	})
	if err != nil || ctxInfo == nil {
		return err
	}
	for _, p := range ctxInfo.Permissions {
		if p == "*" {
			return nil
		}
	}
	owned := make(map[string]struct{}, len(ctxInfo.Permissions))
	for _, p := range ctxInfo.Permissions {
		owned[p] = struct{}{}
	}
	for _, p := range permissions {
		if _, ok := owned[p]; !ok {
			return errPermissionsExceeded
		}
	}
	return nil
}

// errPermissionsExceeded 权限超出自身范围的错误
var errPermissionsExceeded = errorString("分配的权限超出自身权限范围")

// errorString 简单字符串错误类型
type errorString string

func (e errorString) Error() string { return string(e) }

// RefreshToken 刷新管理员 access_token
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) RefreshToken(c *gin.Context) {
	token := c.PostForm("refresh_token")
	if token == "" {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = c.ShouldBindJSON(&body)
		token = body.RefreshToken
	}
	if token == "" {
		response.Error(c, errcode.NotLoginOrTokenExpired)
		return
	}

	claims, err := h.jwtManager.ParseToken(token)
	if err != nil || claims.Type != jwt.TokenTypeAdmin {
		response.Error(c, errcode.InvalidToken)
		return
	}

	accessToken, accessExpire, err := h.jwtManager.GenerateAdminAccessToken(
		claims.UserID, claims.Phone, claims.Role, claims.CommunityID, claims.CommunityIDs, claims.Permissions, claims.Username, claims.RealName,
	)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	now := time.Now()
	response.Success(c, gin.H{
		"access_token": accessToken,
		"expires_in":   int64(accessExpire.Sub(now).Seconds()),
		"expires_at":   accessExpire.Unix(),
	})
}

// --- 分类管理 ---

// ListCategories 获取分类列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListCategories(c *gin.Context) {
	resp, err := h.productClient.ListCategories(c.Request.Context(), &product.ListCategoriesReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// CreateCategoryReq 创建分类请求
type CreateCategoryReq struct {
	ParentID int64  `json:"parent_id"`
	Name     string `json:"name" binding:"required"`
	Icon     string `json:"icon"`
	Sort     int32  `json:"sort"`
}

// CreateCategory 创建分类
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateCategory(c *gin.Context) {
	var req CreateCategoryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateCategory(c.Request.Context(), &product.CreateCategoryReq{
		CommunityId: h.getAdminCommunityID(c),
		ParentId:    req.ParentID,
		Name:        req.Name,
		Icon:        req.Icon,
		Sort:        req.Sort,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateCategoryReq 更新分类请求
type UpdateCategoryReq struct {
	Name   string `json:"name"`
	Icon   string `json:"icon"`
	Sort   int32  `json:"sort"`
	Status int32  `json:"status"`
}

// UpdateCategory 更新分类
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateCategory(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateCategoryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.UpdateCategory(c.Request.Context(), &product.UpdateCategoryReq{
		Id:     id,
		Name:   req.Name,
		Icon:   req.Icon,
		Sort:   req.Sort,
		Status: req.Status,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteCategory 删除分类
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteCategory(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.DeleteCategory(c.Request.Context(), &product.DeleteCategoryReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// --- 商品管理 ---

// GetAdminProduct 获取管理端商品详情（含 SKU 列表与规格树）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetAdminProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.GetProduct(c.Request.Context(), &product.GetProductReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListAdminProducts 获取管理端商品列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminProducts(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	categoryID, _ := strconv.ParseInt(c.DefaultQuery("category_id", "0"), 10, 64)
	keyword := c.Query("keyword")

	resp, err := h.productClient.ListProducts(c.Request.Context(), &product.ListProductsReq{
		CommunityId: h.getAdminCommunityID(c),
		CategoryId:  categoryID,
		Keyword:     keyword,
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}

// SpecValueReqItem 规格值保存项（前端临时 ID 或真实 ID）
type SpecValueReqItem struct {
	ID     int64  `json:"id"`
	TempID string `json:"temp_id"`
	Name   string `json:"name"`
	Sort   int32  `json:"sort"`
}

// SpecReqItem 规格名保存项（含层级规格值）
type SpecReqItem struct {
	ID     int64               `json:"id"`
	TempID string              `json:"temp_id"`
	Name   string              `json:"name"`
	Sort   int32               `json:"sort"`
	Values []*SpecValueReqItem `json:"values"`
}

// SkuItemReqItem SKU 保存项（spec_value_ids 为规格值 ID 列表，真实数字 ID 或临时 ID）
type SkuItemReqItem struct {
	ID           int64    `json:"id"`
	SpecValueIDs []string `json:"spec_value_ids"`
	SkuCode      string   `json:"sku_code"`
	Price        float64  `json:"price"`
	CostPrice    float64  `json:"cost_price"`
	Stock        int32    `json:"stock"`
	SafetyStock  int32    `json:"safety_stock"`
	Image        string   `json:"image"`
}

// CreateProductReq 创建商品请求
type CreateProductReq struct {
	CategoryID  int64             `json:"category_id" binding:"required"`
	Name        string            `json:"name" binding:"required"`
	Description string            `json:"description"`
	CoverImage  string            `json:"cover_image"`
	Images      []string          `json:"images"`
	Unit        string            `json:"unit"`
	IsMultiSpec bool              `json:"is_multi_spec"`
	MinBuy      int32             `json:"min_buy"`
	Sort        int32             `json:"sort"`
	Specs       []*SpecReqItem    `json:"specs"`
	SkuItems    []*SkuItemReqItem `json:"sku_items"`
}

// toProtoSpecs 转换规格树为 proto 结构
// 参数：
//   - specs: 网关层规格树列表
//
// 返回值：
//   - []*product.SpecReq: proto 规格树
func toProtoSpecs(specs []*SpecReqItem) []*product.SpecReq {
	if len(specs) == 0 {
		return nil
	}
	list := make([]*product.SpecReq, 0, len(specs))
	for _, sp := range specs {
		values := make([]*product.SpecValueReq, 0, len(sp.Values))
		for _, v := range sp.Values {
			values = append(values, &product.SpecValueReq{
				Id:     v.ID,
				TempId: v.TempID,
				Name:   v.Name,
				Sort:   v.Sort,
			})
		}
		list = append(list, &product.SpecReq{
			Id:     sp.ID,
			TempId: sp.TempID,
			Name:   sp.Name,
			Sort:   sp.Sort,
			Values: values,
		})
	}
	return list
}

// toProtoSkuItems 转换 SKU 列表为 proto 结构
// 参数：
//   - items: 网关层 SKU 列表
//
// 返回值：
//   - []*product.SkuItemReq: proto SKU 列表
func toProtoSkuItems(items []*SkuItemReqItem) []*product.SkuItemReq {
	if len(items) == 0 {
		return nil
	}
	list := make([]*product.SkuItemReq, 0, len(items))
	for _, it := range items {
		list = append(list, &product.SkuItemReq{
			Id:           it.ID,
			SpecValueIds: it.SpecValueIDs,
			SkuCode:      it.SkuCode,
			Price:        it.Price,
			CostPrice:    it.CostPrice,
			Stock:        it.Stock,
			SafetyStock:  it.SafetyStock,
			Image:        it.Image,
		})
	}
	return list
}

// CreateProduct 创建商品
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateProduct(c *gin.Context) {
	var req CreateProductReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateProduct(c.Request.Context(), &product.CreateProductReq{
		CommunityId: h.getAdminCommunityID(c),
		CategoryId:  req.CategoryID,
		Name:        req.Name,
		Description: req.Description,
		CoverImage:  req.CoverImage,
		Images:      req.Images,
		Unit:        req.Unit,
		IsMultiSpec: req.IsMultiSpec,
		MinBuy:      req.MinBuy,
		Sort:        req.Sort,
		Specs:       toProtoSpecs(req.Specs),
		SkuItems:    toProtoSkuItems(req.SkuItems),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateProductReq 更新商品请求
type UpdateProductReq struct {
	CategoryID  int64             `json:"category_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	CoverImage  string            `json:"cover_image"`
	Images      []string          `json:"images"`
	Unit        string            `json:"unit"`
	Sort        int32             `json:"sort"`
	Status      int32             `json:"status"`
	MinBuy      int32             `json:"min_buy"`
	IsMultiSpec bool              `json:"is_multi_spec"`
	Specs       []*SpecReqItem    `json:"specs"`
	SkuItems    []*SkuItemReqItem `json:"sku_items"`
}

// UpdateProduct 更新商品
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateProductReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.UpdateProduct(c.Request.Context(), &product.UpdateProductReq{
		Id:          id,
		CategoryId:  req.CategoryID,
		Name:        req.Name,
		Description: req.Description,
		CoverImage:  req.CoverImage,
		Images:      req.Images,
		Unit:        req.Unit,
		Sort:        req.Sort,
		Status:      req.Status,
		MinBuy:      req.MinBuy,
		IsMultiSpec: req.IsMultiSpec,
		Specs:       toProtoSpecs(req.Specs),
		SkuItems:    toProtoSkuItems(req.SkuItems),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteProduct 删除商品
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.DeleteProduct(c.Request.Context(), &product.DeleteProductReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// UpdateProductStatusReq 更新商品状态请求
type UpdateProductStatusReq struct {
	Status int32 `json:"status" binding:"required"`
}

// UpdateProductStatus 更新商品状态
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateProductStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateProductStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.UpdateProductStatus(c.Request.Context(), &product.UpdateProductStatusReq{
		Id:     id,
		Status: req.Status,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": req.Status})
}

// --- 订单管理 ---

// ListAdminOrders 获取管理端订单列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminOrders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))

	resp, err := h.orderClient.ListOrders(c.Request.Context(), &order.ListOrdersReq{
		CommunityId:   h.getAdminCommunityID(c),
		Status:        int32(status),
		OrderNo:       c.Query("order_no"),
		StartTime:     c.Query("start_time"),
		EndTime:       c.Query("end_time"),
		ReceiverName:  c.Query("receiver_name"),
		ReceiverPhone: c.Query("receiver_phone"),
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	list := make([]gin.H, 0, len(resp.List))
	for _, o := range resp.List {
		userName := ""
		if o.UserId > 0 {
			userResp, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: o.UserId})
			if err == nil && userResp != nil {
				userName = userResp.Nickname
				if userName == "" {
					userName = userResp.Phone
				}
			}
		}
		list = append(list, gin.H{
			"id":              o.Id,
			"order_no":        o.OrderNo,
			"user_id":         o.UserId,
			"user_name":       userName,
			"total_amount":    o.TotalAmount,
			"discount_amount": o.DiscountAmount,
			"delivery_fee":    o.DeliveryFee,
			"pay_amount":      o.PayAmount,
			"delivery_type":   o.DeliveryType,
			"status":          o.Status,
			"status_text":     orderStatusText(o.Status, o.DeliveryType),
			"created_at":      o.CreatedAt,
		})
	}
	response.SuccessWithPage(c, list, int(resp.Pagination.Total), page, pageSize)
}

// GetAdminOrder 获取管理端订单详情
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetAdminOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	o := resp.Order
	userName := ""
	if o != nil && o.UserId > 0 {
		userResp, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: o.UserId})
		if err == nil && userResp != nil {
			userName = userResp.Nickname
			if userName == "" {
				userName = userResp.Phone
			}
		}
	}

	address := gin.H{"name": "", "phone": "", "address": ""}
	if o != nil && o.ContactName != "" {
		address["name"] = o.ContactName
		address["phone"] = o.ContactPhone
		address["address"] = o.ContactAddress
	}

	items := make([]gin.H, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, gin.H{
			"id":            item.Id,
			"product_id":    item.ProductId,
			"sku_id":        item.SkuId,
			"product_name":  item.ProductName,
			"sku_name":      item.SpecName,
			"product_image": item.ProductImage,
			"price":         item.Price,
			"quantity":      item.Quantity,
			"total_amount":  item.TotalPrice,
		})
	}

	statusLogs := make([]gin.H, 0, len(resp.StatusLogs))
	for _, log := range resp.StatusLogs {
		statusLogs = append(statusLogs, gin.H{
			"from_status":   log.FromStatus,
			"to_status":     log.ToStatus,
			"operator_type": log.OperatorType,
			"remark":        log.Remark,
			"created_at":    log.CreatedAt,
		})
	}

	data := gin.H{
		"id":              o.Id,
		"order_no":        o.OrderNo,
		"user_id":         o.UserId,
		"user_name":       userName,
		"total_amount":    o.TotalAmount,
		"discount_amount": o.DiscountAmount,
		"delivery_fee":    o.DeliveryFee,
		"pay_amount":      o.PayAmount,
		"delivery_type":   o.DeliveryType,
		"delivery_time":   o.DeliveryTime,
		"remark":          o.Remark,
		"status":          o.Status,
		"status_text":     orderStatusText(o.Status, o.DeliveryType),
		"pay_time":        o.PayTime,
		"deliver_time":    o.DeliverTime,
		"complete_time":   o.CompleteTime,
		"cancel_time":     o.CancelTime,
		"cancel_reason":   o.CancelReason,
		"express_company": o.ExpressCompany,
		"express_no":      o.ExpressNo,
		"created_at":      o.CreatedAt,
		"items":           items,
		"address":         address,
		"status_logs":     statusLogs,
	}
	response.Success(c, data)
}

// UpdateOrderStatusReq 更新订单状态请求
type UpdateOrderStatusReq struct {
	Status int32  `json:"status" binding:"required"`
	Remark string `json:"remark"`
}

// UpdateOrderStatus 更新订单状态
// 注意：目标状态 6（取消）被明确拒绝——取消必须走 POST /orders/:id/cancel，
// 以保证库存回补、取消原因/时间写入等取消语义完整执行
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateOrderStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateOrderStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if req.Status == 6 {
		response.Error(c, errcode.ParamError, "取消订单请使用专用取消接口")
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:           id,
		Status:       req.Status,
		OperatorType: "admin",
		OperatorId:   uc.UserID,
		Remark:       req.Remark,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": req.Status})
}

// AdminCancelOrderReq 后台取消订单请求
type AdminCancelOrderReq struct {
	Reason string `json:"reason" binding:"required"`
}

// AdminCancelOrder 后台取消订单
// 走 order-service 的取消语义：条件更新状态、写取消原因/时间并逐行回补库存
//
// 接口: POST /api/v1/admin/orders/:id/cancel
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) AdminCancelOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req AdminCancelOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError, "请填写取消原因")
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:           id,
		Status:       6,
		OperatorType: "admin",
		OperatorId:   uc.UserID,
		Remark:       req.Reason,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": 6})
}

// ShipOrderReq 订单发货请求
type ShipOrderReq struct {
	ExpressCompany string `json:"express_company"`
	ExpressNo      string `json:"express_no"`
	Remark         string `json:"remark"`
}

// ShipOrder 订单发货
// 校验订单为配送单且状态为待发货（status=2）后，将订单状态更新为已发货（status=4），
// 物流公司/单号写入独立字段（express_company/express_no），不再拼接进备注
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ShipOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req ShipOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	orderResp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: id})
	if err != nil || orderResp.Order == nil {
		response.Error(c, errcode.NotFound, "订单不存在")
		return
	}
	if orderResp.Order.DeliveryType == 1 {
		response.Error(c, errcode.ParamError, "自提订单无需发货，请直接核销完成")
		return
	}
	if orderResp.Order.Status != 2 {
		response.Error(c, errcode.ParamError, "订单状态不允许发货")
		return
	}

	expressCompany := strings.TrimSpace(req.ExpressCompany)
	expressNo := strings.TrimSpace(req.ExpressNo)
	if expressCompany == "" || expressNo == "" {
		response.Error(c, errcode.ParamError, "物流公司和物流单号不能为空")
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:             id,
		Status:         4,
		OperatorType:   "admin",
		OperatorId:     uc.UserID,
		Remark:         req.Remark,
		ExpressCompany: expressCompany,
		ExpressNo:      expressNo,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": 4})
}

// CompleteOrder 完成订单
// 配送单须为已发货（status=4）；自提单允许在已支付（status=2 且 delivery_type=1）时直接核销完成，
// 与 C 端确认收货（handler/order.go ConfirmReceive）口径一致
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CompleteOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	orderResp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: id})
	if err != nil || orderResp.Order == nil {
		response.Error(c, errcode.NotFound, "订单不存在")
		return
	}
	o := orderResp.Order
	if o.Status != 4 && !(o.Status == 2 && o.DeliveryType == 1) {
		response.Error(c, errcode.ParamError, "订单状态不允许完成")
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:           id,
		Status:       5,
		OperatorType: "admin",
		OperatorId:   uc.UserID,
		Remark:       "订单完成",
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": 5})
}

// --- 仪表盘与统计报表 ---

// GetDashboard 获取仪表盘聚合数据
// 聚合今日订单数、今日销售额、注册用户数、在售商品数、待发货订单数、待处理退款数
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetDashboard(c *gin.Context) {
	ctx := c.Request.Context()
	communityID := h.getAdminCommunityID(c)
	today := time.Now().Format("2006-01-02")

	orderStats, err := h.orderClient.GetOrderStatistics(ctx, &order.GetOrderStatisticsReq{
		CommunityId: communityID,
		StartDate:   today,
		EndDate:     today,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	salesStats, err := h.orderClient.GetSalesStatistics(ctx, &order.GetSalesStatisticsReq{
		CommunityId: communityID,
		StartDate:   today,
		EndDate:     today,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	userResp, err := h.userClient.ListUsers(ctx, &user.ListUsersReq{
		CommunityId: communityID,
		Pagination:  &common.Pagination{Page: 1, PageSize: 1},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	productResp, err := h.productClient.ListProducts(ctx, &product.ListProductsReq{
		CommunityId: communityID,
		Status:      1,
		Pagination:  &common.Pagination{Page: 1, PageSize: 1},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	response.Success(c, gin.H{
		"order_count":   orderStats.TotalOrders,
		"sale_amount":   salesStats.PayAmount,
		"user_count":    userResp.Pagination.Total,
		"product_count": productResp.Pagination.Total,
		"pending_ship":  orderStats.PendingShip,
		"refunding":     orderStats.Refunding,
	})
}

// GetOrderStatistics 获取订单状态统计
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetOrderStatistics(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	resp, err := h.orderClient.GetOrderStatistics(c.Request.Context(), &order.GetOrderStatisticsReq{
		CommunityId: h.getAdminCommunityID(c),
		StartDate:   startDate,
		EndDate:     endDate,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// GetSalesStatistics 获取销售统计
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetSalesStatistics(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	resp, err := h.orderClient.GetSalesStatistics(c.Request.Context(), &order.GetSalesStatisticsReq{
		CommunityId: h.getAdminCommunityID(c),
		StartDate:   startDate,
		EndDate:     endDate,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// GetProductRankings 获取商品销售排行
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetProductRankings(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	resp, err := h.orderClient.GetProductRankings(c.Request.Context(), &order.GetProductRankingsReq{
		CommunityId: h.getAdminCommunityID(c),
		StartDate:   startDate,
		EndDate:     endDate,
		Limit:       int32(limit),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// GetRevenueReport 获取营收报表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetRevenueReport(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	groupBy := c.DefaultQuery("group_by", "day")

	resp, err := h.orderClient.GetRevenueReport(c.Request.Context(), &order.GetRevenueReportReq{
		CommunityId: h.getAdminCommunityID(c),
		StartDate:   startDate,
		EndDate:     endDate,
		GroupBy:     groupBy,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// --- 库存管理 ---

// ListStockRecords 获取库存流水列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListStockRecords(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	skuID, _ := strconv.ParseInt(c.DefaultQuery("sku_id", "0"), 10, 64)
	recordType, _ := strconv.Atoi(c.DefaultQuery("type", "0"))
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	resp, err := h.productClient.ListStockRecords(c.Request.Context(), &product.ListStockRecordsReq{
		CommunityId: h.getAdminCommunityID(c),
		SkuId:       skuID,
		Type:        int32(recordType),
		StartDate:   startDate,
		EndDate:     endDate,
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}

// StockEntryReq 商品入库请求
type StockEntryReq struct {
	Type   int32                     `json:"type" binding:"required"`
	Items  []*product.StockEntryItem `json:"items" binding:"required"`
	Remark string                    `json:"remark"`
}

// StockEntry 商品入库
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) StockEntry(c *gin.Context) {
	var req StockEntryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err := h.productClient.StockEntry(c.Request.Context(), &product.StockEntryReq{
		CommunityId: h.getAdminCommunityID(c),
		Type:        req.Type,
		Items:       req.Items,
		OperatorId:  uc.UserID,
		Remark:      req.Remark,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

// GetStockWarnings 获取库存预警列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetStockWarnings(c *gin.Context) {
	resp, err := h.productClient.GetStockWarnings(c.Request.Context(), &product.GetStockWarningsReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	total := len(resp.List)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	response.SuccessWithPage(c, resp.List[start:end], total, page, pageSize)
}

// ListSkuStock 获取当前社区 SKU 库存列表
// 通过组合 ListProducts 与 ListSkus 实现，后续可在 product-service 中增加独立 gRPC 方法优化
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListSkuStock(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	communityID := h.getAdminCommunityID(c)
	ctx := c.Request.Context()

	productResp, err := h.productClient.ListProducts(ctx, &product.ListProductsReq{
		CommunityId: communityID,
		Pagination: &common.Pagination{
			Page:     1,
			PageSize: 1000,
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	var allSkus []*product.SkuInfo
	for _, p := range productResp.List {
		skuResp, err := h.productClient.ListSkus(ctx, &product.ListSkusReq{ProductId: p.Id})
		if err != nil {
			continue
		}
		allSkus = append(allSkus, skuResp.List...)
	}

	total := len(allSkus)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	var pageList []*product.SkuInfo
	if start < end {
		pageList = allSkus[start:end]
	}

	response.SuccessWithPage(c, pageList, total, page, pageSize)
}

// ListStockChecks 获取库存盘点列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListStockChecks(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.productClient.ListStockChecks(c.Request.Context(), &product.ListStockChecksReq{
		CommunityId: h.getAdminCommunityID(c),
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}

// GetStockCheckDetail 获取盘点单详情
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetStockCheckDetail(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.GetStockCheck(c.Request.Context(), &product.GetStockCheckReq{
		Id: id,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// CreateStockCheckReq 创建盘点单请求
type CreateStockCheckReq struct {
	Remark string `json:"remark"`
}

// CreateStockCheck 创建盘点单
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateStockCheck(c *gin.Context) {
	var req CreateStockCheckReq
	_ = c.ShouldBindJSON(&req)

	uc := appcontext.GetUserContext(c)
	check, err := h.productClient.CreateStockCheck(c.Request.Context(), &product.CreateStockCheckReq{
		CommunityId: h.getAdminCommunityID(c),
		OperatorId:  uc.UserID,
		Remark:      req.Remark,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, check)
}

// StockOutReq 库存出库请求
type StockOutReq struct {
	Type   int32                   `json:"type" binding:"required"`
	Items  []*product.StockOutItem `json:"items" binding:"required"`
	Remark string                  `json:"remark"`
}

// StockOut 库存出库（销售出库/报损出库）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) StockOut(c *gin.Context) {
	var req StockOutReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err := h.productClient.StockOut(c.Request.Context(), &product.StockOutReq{
		CommunityId: h.getAdminCommunityID(c),
		Type:        req.Type,
		Items:       req.Items,
		OperatorId:  uc.UserID,
		Remark:      req.Remark,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

// GeneratePurchaseOrder 生成补货建议单
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GeneratePurchaseOrder(c *gin.Context) {
	resp, err := h.productClient.GeneratePurchaseOrder(c.Request.Context(), &product.GeneratePurchaseOrderReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.Items)
}

// SubmitStockCheckReq 提交盘点单请求
type SubmitStockCheckReq struct {
	Items []struct {
		SkuID       int64 `json:"sku_id" binding:"required"`
		ActualStock int32 `json:"actual_stock" binding:"required"`
	} `json:"items" binding:"required"`
}

// SubmitStockCheck 提交盘点单
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) SubmitStockCheck(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req SubmitStockCheckReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	items := make([]*product.StockCheckItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, &product.StockCheckItem{
			SkuId:       item.SkuID,
			ActualStock: item.ActualStock,
		})
	}

	_, err = h.productClient.SubmitStockCheck(c.Request.Context(), &product.SubmitStockCheckReq{
		Id:    id,
		Items: items,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// --- 营销管理 ---

// ListAdminCoupons 获取管理端优惠券列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminCoupons(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))
	resp, err := h.productClient.ListCoupons(c.Request.Context(), &product.ListCouponsReq{
		CommunityId: h.getAdminCommunityID(c),
		Status:      int32(status),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, len(resp.List), page, pageSize)
}

// CreateCouponReq 创建优惠券请求
type CreateCouponReq struct {
	Name          string  `json:"name" binding:"required"`
	Type          int32   `json:"type" binding:"required"`
	Threshold     float64 `json:"threshold"`
	DiscountValue float64 `json:"discount_value" binding:"required"`
	TotalCount    int32   `json:"total_count" binding:"required"`
	PerUserLimit  int32   `json:"per_user_limit"`
	StartTime     string  `json:"start_time"`
	EndTime       string  `json:"end_time"`
	ValidDays     int32   `json:"valid_days"`
}

// CreateCoupon 创建优惠券
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateCoupon(c *gin.Context) {
	var req CreateCouponReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateCoupon(c.Request.Context(), &product.CreateCouponReq{
		CommunityId:   h.getAdminCommunityID(c),
		Name:          req.Name,
		Type:          req.Type,
		Threshold:     req.Threshold,
		DiscountValue: req.DiscountValue,
		TotalCount:    req.TotalCount,
		PerUserLimit:  req.PerUserLimit,
		StartTime:     req.StartTime,
		EndTime:       req.EndTime,
		ValidDays:     req.ValidDays,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateCouponReq 更新优惠券请求
type UpdateCouponReq struct {
	Name   string `json:"name"`
	Status int32  `json:"status"`
}

// UpdateCoupon 更新优惠券
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateCoupon(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateCouponReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.UpdateCoupon(c.Request.Context(), &product.UpdateCouponReq{
		Id:     id,
		Name:   req.Name,
		Status: req.Status,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteCoupon 删除优惠券
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteCoupon(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.DeleteCoupon(c.Request.Context(), &product.DeleteCouponReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// ListAdminFlashSales 获取管理端秒杀活动列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminFlashSales(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	resp, err := h.productClient.ListFlashSales(c.Request.Context(), &product.ListFlashSalesReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, len(resp.List), page, pageSize)
}

// CreateFlashSaleReq 创建秒杀活动请求
type CreateFlashSaleReq struct {
	Name      string                      `json:"name" binding:"required"`
	StartTime string                      `json:"start_time" binding:"required"`
	EndTime   string                      `json:"end_time" binding:"required"`
	Items     []*product.FlashSaleItemReq `json:"items" binding:"required"`
}

// CreateFlashSale 创建秒杀活动
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateFlashSale(c *gin.Context) {
	var req CreateFlashSaleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateFlashSale(c.Request.Context(), &product.CreateFlashSaleReq{
		CommunityId: h.getAdminCommunityID(c),
		Name:        req.Name,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Items:       req.Items,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateFlashSaleReq 更新秒杀活动请求
type UpdateFlashSaleReq struct {
	Name      string `json:"name"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Status    int32  `json:"status"`
}

// UpdateFlashSale 更新秒杀活动
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateFlashSale(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateFlashSaleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.UpdateFlashSale(c.Request.Context(), &product.UpdateFlashSaleReq{
		Id:        id,
		Name:      req.Name,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Status:    req.Status,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListFullReductions 获取满减活动列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListFullReductions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	resp, err := h.productClient.ListFullReductions(c.Request.Context(), &product.ListFullReductionsReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, len(resp.List), page, pageSize)
}

// CreateFullReductionReq 创建满减活动请求
type CreateFullReductionReq struct {
	Name         string  `json:"name" binding:"required"`
	Threshold    float64 `json:"threshold" binding:"required"`
	ReduceAmount float64 `json:"reduce_amount" binding:"required"`
	StartTime    string  `json:"start_time"`
	EndTime      string  `json:"end_time"`
}

// CreateFullReduction 创建满减活动
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateFullReduction(c *gin.Context) {
	var req CreateFullReductionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateFullReduction(c.Request.Context(), &product.CreateFullReductionReq{
		CommunityId:  h.getAdminCommunityID(c),
		Name:         req.Name,
		Threshold:    req.Threshold,
		ReduceAmount: req.ReduceAmount,
		StartTime:    req.StartTime,
		EndTime:      req.EndTime,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateFullReductionReq 更新满减活动请求
type UpdateFullReductionReq struct {
	Name         string  `json:"name"`
	Threshold    float64 `json:"threshold"`
	ReduceAmount float64 `json:"reduce_amount"`
	StartTime    string  `json:"start_time"`
	EndTime      string  `json:"end_time"`
	Status       int32   `json:"status"`
}

// UpdateFullReduction 更新满减活动
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateFullReduction(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateFullReductionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.UpdateFullReduction(c.Request.Context(), &product.UpdateFullReductionReq{
		Id:           id,
		Name:         req.Name,
		Threshold:    req.Threshold,
		ReduceAmount: req.ReduceAmount,
		StartTime:    req.StartTime,
		EndTime:      req.EndTime,
		Status:       req.Status,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// --- Banner 管理 ---

// ListAdminBanners 获取 Banner 列表
// 管理态返回全部记录，支持按位置筛选
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminBanners(c *gin.Context) {
	communityID := h.getAdminCommunityID(c)
	position, _ := strconv.Atoi(c.DefaultQuery("position", "0"))

	resp, err := h.productClient.ListBanners(c.Request.Context(), &product.ListBannersReq{
		CommunityId: communityID,
		Position:    int32(position),
		IsAdmin:     true,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// CreateBannerReq 创建 Banner 请求
type CreateBannerReq struct {
	Title     string `json:"title"`
	ImageURL  string `json:"image_url" binding:"required"`
	LinkURL   string `json:"link_url"`
	Position  int32  `json:"position" binding:"required"`
	Sort      int32  `json:"sort"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

// CreateBanner 创建 Banner
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateBanner(c *gin.Context) {
	var req CreateBannerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateBanner(c.Request.Context(), &product.CreateBannerReq{
		CommunityId: h.getAdminCommunityID(c),
		Title:       req.Title,
		ImageUrl:    req.ImageURL,
		LinkUrl:     req.LinkURL,
		Position:    req.Position,
		Sort:        req.Sort,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateBannerReq 更新 Banner 请求
type UpdateBannerReq struct {
	Title     string `json:"title"`
	ImageURL  string `json:"image_url"`
	LinkURL   string `json:"link_url"`
	Position  int32  `json:"position"`
	Sort      int32  `json:"sort"`
	Status    int32  `json:"status"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

// UpdateBanner 更新 Banner
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateBanner(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateBannerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.UpdateBanner(c.Request.Context(), &product.UpdateBannerReq{
		Id:        id,
		Title:     req.Title,
		ImageUrl:  req.ImageURL,
		LinkUrl:   req.LinkURL,
		Position:  req.Position,
		Sort:      req.Sort,
		Status:    req.Status,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteBanner 删除 Banner
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteBanner(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.DeleteBanner(c.Request.Context(), &product.DeleteBannerReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// --- 社区管理 ---

// ListUsers 获取会员列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	keyword := c.DefaultQuery("keyword", "")
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))
	communityID, _ := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64)

	resp, err := h.userClient.ListUsers(c.Request.Context(), &user.ListUsersReq{
		CommunityId: communityID,
		Keyword:     keyword,
		Status:      int32(status),
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	list := make([]gin.H, 0, len(resp.List))
	for _, u := range resp.List {
		list = append(list, gin.H{
			"id":             u.Id,
			"openid":         u.Openid,
			"phone":          u.Phone,
			"nickname":       u.Nickname,
			"avatar":         u.Avatar,
			"gender":         u.Gender,
			"community_id":   u.CommunityId,
			"community_name": "",
			"status":         u.Status,
			"last_login_at":  u.LastLoginAt,
			"created_at":     u.CreatedAt,
		})
	}
	response.SuccessWithPage(c, list, int(resp.Pagination.Total), page, pageSize)
}

// ListAdminCommunities 获取社区列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminCommunities(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	// 状态筛选：-1 全部（默认），0 停业，1 营业
	status, _ := strconv.Atoi(c.DefaultQuery("status", "-1"))

	resp, err := h.userClient.ListCommunities(c.Request.Context(), &user.ListCommunitiesReq{
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
		// 管理端需要看到停业社区，否则停业后将无法再编辑
		IncludeDisabled: true,
		Status:          int32(status),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}

// CreateCommunityReq 创建社区请求
type CreateCommunityReq struct {
	Name            string  `json:"name" binding:"required"`
	Address         string  `json:"address"`
	Longitude       float64 `json:"longitude"`
	Latitude        float64 `json:"latitude"`
	ContactPhone    string  `json:"contact_phone"`
	ManagerName     string  `json:"manager_name"`
	ManagerPhone    string  `json:"manager_phone"`
	PickupAddress   string  `json:"pickup_address"`
	DeliveryFee     float64 `json:"delivery_fee"`
	MinOrderAmount  float64 `json:"min_order_amount"`
	DeliveryRangeKm float64 `json:"delivery_range_km"`
	BusinessHours   string  `json:"business_hours"`
	Sort            int32   `json:"sort"`
	Status          int32   `json:"status"`
	// 省/市/区名称与区划代码（来自区划选择器；历史数据允许代码为空）
	Province     string `json:"province"`
	City         string `json:"city"`
	District     string `json:"district"`
	ProvinceCode string `json:"province_code"`
	CityCode     string `json:"city_code"`
	DistrictCode string `json:"district_code"`
}

// CreateCommunity 创建社区
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateCommunity(c *gin.Context) {
	var req CreateCommunityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.userClient.CreateCommunity(c.Request.Context(), &user.CreateCommunityReq{
		Name:            req.Name,
		Address:         req.Address,
		Longitude:       req.Longitude,
		Latitude:        req.Latitude,
		ContactPhone:    req.ContactPhone,
		ManagerName:     req.ManagerName,
		ManagerPhone:    req.ManagerPhone,
		PickupAddress:   req.PickupAddress,
		DeliveryFee:     req.DeliveryFee,
		MinOrderAmount:  req.MinOrderAmount,
		DeliveryRangeKm: req.DeliveryRangeKm,
		BusinessHours:   req.BusinessHours,
		Sort:            req.Sort,
		Status:          req.Status,
		Province:        req.Province,
		City:            req.City,
		District:        req.District,
		ProvinceCode:    req.ProvinceCode,
		CityCode:        req.CityCode,
		DistrictCode:    req.DistrictCode,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// GetCommunity 获取社区详情
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetCommunity(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.userClient.GetCommunity(c.Request.Context(), &user.GetCommunityReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateAdminCommunityReq 更新社区请求
type UpdateAdminCommunityReq struct {
	Name            string  `json:"name" binding:"required"`
	Address         string  `json:"address"`
	Longitude       float64 `json:"longitude"`
	Latitude        float64 `json:"latitude"`
	ContactPhone    string  `json:"contact_phone"`
	ManagerName     string  `json:"manager_name"`
	ManagerPhone    string  `json:"manager_phone"`
	PickupAddress   string  `json:"pickup_address"`
	DeliveryFee     float64 `json:"delivery_fee"`
	MinOrderAmount  float64 `json:"min_order_amount"`
	DeliveryRangeKm float64 `json:"delivery_range_km"`
	BusinessHours   string  `json:"business_hours"`
	Sort            int32   `json:"sort"`
	Status          int32   `json:"status"`
	// 省/市/区名称与区划代码（来自区划选择器；历史数据允许代码为空）
	Province     string `json:"province"`
	City         string `json:"city"`
	District     string `json:"district"`
	ProvinceCode string `json:"province_code"`
	CityCode     string `json:"city_code"`
	DistrictCode string `json:"district_code"`
}

// UpdateCommunity 更新社区
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateCommunity(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateAdminCommunityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.userClient.UpdateCommunity(c.Request.Context(), &user.UpdateCommunityInfoReq{
		Id:              id,
		Name:            req.Name,
		Address:         req.Address,
		Longitude:       req.Longitude,
		Latitude:        req.Latitude,
		ContactPhone:    req.ContactPhone,
		ManagerName:     req.ManagerName,
		ManagerPhone:    req.ManagerPhone,
		PickupAddress:   req.PickupAddress,
		DeliveryFee:     req.DeliveryFee,
		MinOrderAmount:  req.MinOrderAmount,
		DeliveryRangeKm: req.DeliveryRangeKm,
		BusinessHours:   req.BusinessHours,
		Sort:            req.Sort,
		Status:          req.Status,
		Province:        req.Province,
		City:            req.City,
		District:        req.District,
		ProvinceCode:    req.ProvinceCode,
		CityCode:        req.CityCode,
		DistrictCode:    req.DistrictCode,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteCommunity 删除社区
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteCommunity(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.userClient.DeleteCommunity(c.Request.Context(), &user.DeleteCommunityReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// --- 门店设置（社区级资料/配置，社区管理员可自助维护） ---

// CommunityProfileReq 门店设置更新请求
// 仅包含社区管理员允许自行修改的字段（白名单）；
// 社区名称、地址、经纬度、负责人电话、营业状态等由超管在「社区管理」统一维护，不在本接口范围
type CommunityProfileReq struct {
	ContactPhone         string  `json:"contact_phone"`
	ManagerName          string  `json:"manager_name"`
	PickupAddress        string  `json:"pickup_address"`
	DeliveryFee          float64 `json:"delivery_fee"`
	MinOrderAmount       float64 `json:"min_order_amount"`
	DeliveryRangeKm      float64 `json:"delivery_range_km"`
	BusinessHours        string  `json:"business_hours"`
	AllowOutsideDelivery bool    `json:"allow_outside_delivery"`
}

// GetCommunityProfile 获取当前社区门店设置
// 目标社区取当前社区语境（X-Community-Id），无社区语境时报参数错误
//
// 接口: GET /api/v1/admin/community/profile
//
// 参数:
//   - c: gin 上下文
func (h *AdminHandler) GetCommunityProfile(c *gin.Context) {
	id := h.getAdminCommunityID(c)
	if id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.userClient.GetCommunity(c.Request.Context(), &user.GetCommunityReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateCommunityProfile 更新当前社区门店设置（白名单字段）
// 复用 UpdateCommunity RPC 的整表覆盖语义：先读当前社区，再仅覆盖白名单字段，
// 社区名称/地址/经纬度/负责人电话/省市区/状态等一律回填当前值，保证社区管理员无法借本接口篡改
//
// 接口: PUT /api/v1/admin/community/profile
//
// 参数:
//   - c: gin 上下文
func (h *AdminHandler) UpdateCommunityProfile(c *gin.Context) {
	id := h.getAdminCommunityID(c)
	if id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req CommunityProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	cur, err := h.userClient.GetCommunity(c.Request.Context(), &user.GetCommunityReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	resp, err := h.userClient.UpdateCommunity(c.Request.Context(), &user.UpdateCommunityInfoReq{
		Id: id,
		// 以下字段保持当前值不可覆盖（超管在社区管理维护）
		Name:         cur.Name,
		Address:      cur.Address,
		Longitude:    cur.Longitude,
		Latitude:     cur.Latitude,
		ManagerPhone: cur.ManagerPhone,
		Status:       cur.Status,
		Sort:         cur.Sort,
		Province:     cur.Province,
		City:         cur.City,
		District:     cur.District,

		ProvinceCode: cur.ProvinceCode,
		CityCode:     cur.CityCode,
		DistrictCode: cur.DistrictCode,
		// 白名单字段：社区管理员可自行维护
		ContactPhone:         req.ContactPhone,
		ManagerName:          req.ManagerName,
		PickupAddress:        req.PickupAddress,
		DeliveryFee:          req.DeliveryFee,
		MinOrderAmount:       req.MinOrderAmount,
		DeliveryRangeKm:      req.DeliveryRangeKm,
		BusinessHours:        req.BusinessHours,
		AllowOutsideDelivery: req.AllowOutsideDelivery,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// --- 退款管理 ---

// GetRefundSetting 获取退款配置（运费退还策略）
//
// 接口: GET /api/v1/admin/settings/refund
//
// 参数:
//   - c: gin 上下文
func (h *AdminHandler) GetRefundSetting(c *gin.Context) {
	resp, err := h.orderClient.GetRefundConfig(c.Request.Context(), &order.GetRefundConfigReq{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"refund_freight_policy": resp.RefundFreightPolicy,
	})
}

// UpdateRefundSetting 更新退款配置（运费退还策略）
//
// 接口: PUT /api/v1/admin/settings/refund
//
// 参数:
//   - c: gin 上下文
func (h *AdminHandler) UpdateRefundSetting(c *gin.Context) {
	var req struct {
		RefundFreightPolicy string `json:"refund_freight_policy" binding:"required,oneof=always never user_decide"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError, "运费退还策略仅支持 always / never / user_decide")
		return
	}

	_, err := h.orderClient.SaveRefundConfig(c.Request.Context(), &order.SaveRefundConfigReq{
		RefundFreightPolicy: req.RefundFreightPolicy,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"refund_freight_policy": req.RefundFreightPolicy})
}

// AdminCreateRefundReq 后台代客退款请求
// items 为空表示整单退；金额由 order-service 服务端汇算，amount 仅允许调低
type AdminCreateRefundReq struct {
	Amount        float64              `json:"amount"`
	Reason        string               `json:"reason" binding:"required"`
	Items         []AdminRefundItemReq `json:"items"`
	RefundFreight bool                 `json:"refund_freight"`
}

// AdminRefundItemReq 后台代客退款商品明细项
type AdminRefundItemReq struct {
	OrderItemID int64 `json:"order_item_id"`
	Quantity    int32 `json:"quantity"`
}

// AdminCreateRefund 后台代客退款（发起即执行，免审核）
//
// 接口: POST /api/v1/admin/orders/:id/refund
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) AdminCreateRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req AdminCreateRefundReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	pbReq := &order.AdminCreateRefundReq{
		OrderId:       id,
		Amount:        req.Amount,
		Reason:        req.Reason,
		RefundFreight: req.RefundFreight,
		OperatorId:    uc.UserID,
	}
	for _, item := range req.Items {
		pbReq.Items = append(pbReq.Items, &order.ApplyRefundItem{
			OrderItemId: item.OrderItemID,
			Quantity:    item.Quantity,
		})
	}

	resp, err := h.orderClient.AdminCreateRefund(c.Request.Context(), pbReq)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListRefunds 查询退款列表（透传 order-service，含 order_no/明细等字段）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListRefunds(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))

	resp, err := h.orderClient.ListRefunds(c.Request.Context(), &order.ListRefundsReq{
		CommunityId: h.getAdminCommunityID(c),
		Status:      int32(status),
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}

// ApproveRefund 同意退款
// 支付单号由 order-service 以退款单服务端记录为准，请求不再透传 payment_no（防越权指定支付流水）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ApproveRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.ApproveRefund(c.Request.Context(), &order.ApproveRefundReq{
		Id:         id,
		OperatorId: uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// RetryRefund 重试退款
// 对卡在"退款中"(status=4) 的退款单重新发起渠道退款并完成收尾
//
// 接口: POST /api/v1/admin/refunds/:id/retry
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) RetryRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.RetryRefund(c.Request.Context(), &order.RetryRefundReq{
		Id:         id,
		OperatorId: uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// RejectRefundReq 拒绝退款请求
type RejectRefundReq struct {
	RejectReason string `json:"reject_reason" binding:"required"`
}

// RejectRefund 拒绝退款
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) RejectRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req RejectRefundReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.RejectRefund(c.Request.Context(), &order.RejectRefundReq{
		Id:           id,
		OperatorId:   uc.UserID,
		RejectReason: req.RejectReason,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// toAdminVO 将 proto 管理员信息转换为前端视图对象
// 参数：
//   - admin: proto 管理员信息
//
// 返回值：
//   - gin.H: 前端视图对象
func (h *AdminHandler) toAdminVO(admin *user.AdminInfo) gin.H {
	if admin == nil {
		return nil
	}
	return gin.H{
		"id":            admin.Id,
		"username":      admin.Username,
		"real_name":     admin.RealName,
		"phone":         admin.Phone,
		"is_super":      admin.IsSuper,
		"role_code":     admin.RoleCode,
		"role_name":     admin.RoleName,
		"community_id":  admin.CommunityId,
		"community_ids": admin.CommunityIds,
		"status":        admin.Status,
		"last_login_at": admin.LastLoginAt,
		"last_login_ip": admin.LastLoginIp,
	}
}

// orderStatusText 将订单状态码转换为后台展示文案
// 与 C 端 clientOrderStatusText 对齐：自提单的"已支付/已发货"展示为「待自提」，
// 避免后台对自提单误显示「待发货」并引导管理员填写物流
// 参数：
//   - status: 订单状态码
//   - deliveryType: 配送方式（1 自提 2 配送）
//
// 返回值：
//   - string: 状态中文描述
func orderStatusText(status, deliveryType int32) string {
	switch status {
	case 1:
		return "待支付"
	case 2:
		if deliveryType == 1 {
			return "待自提"
		}
		return "待发货"
	case 3:
		return "备货中"
	case 4:
		if deliveryType == 1 {
			return "待自提"
		}
		return "已发货"
	case 5:
		return "已完成"
	case 6:
		return "已取消"
	case 7:
		return "退款中"
	case 8:
		return "已退款"
	default:
		return "未知"
	}
}

// PaymentSetting 支付配置视图对象
// 同时用于 GET 响应与 PUT 请求；配置持久化在数据库 pay_config 表，由 payment 服务管理
type PaymentSetting struct {
	EnabledChannels    []string             `json:"enabled_channels"`
	Wechat             config.WechatConfig  `json:"wechat"`
	Alipay             config.AlipayConfig  `json:"alipay"`
	WechatPaymentReady bool                 `json:"wechat_payment_ready"`
	AlipayReady        bool                 `json:"alipay_ready"`
	WechatCertUploaded bool                 `json:"wechat_cert_uploaded"`
	AlipayCertUploaded bool                 `json:"alipay_cert_uploaded"`
	UpdatedAt          string               `json:"updated_at"`
	SuggestedURLs      PaymentSuggestedURLs `json:"suggested_urls"`
}

// PaymentSuggestedURLs 回调 URL 配置引导
// 根据后台当前访问域名自动生成，引导使用人将回调地址配置到对应支付平台
type PaymentSuggestedURLs struct {
	BaseURL         string `json:"base_url"`
	WechatNotifyURL string `json:"wechat_notify_url"`
	AlipayNotifyURL string `json:"alipay_notify_url"`
}

// GetPaymentSetting 获取支付配置
// 从数据库读取支付渠道开关与微信/支付宝配置，并返回根据当前请求域名生成的回调 URL 建议
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetPaymentSetting(c *gin.Context) {
	resp, err := h.paymentClient.GetPayConfig(c.Request.Context(), &payment.GetPayConfigReq{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 私钥不回传明文，仅通过 cert_uploaded 标志告知前端上传状态
	setting := PaymentSetting{
		EnabledChannels: resp.EnabledChannels,
		Wechat: config.WechatConfig{
			AppID:        resp.WechatAppId,
			AppSecret:    resp.WechatAppSecret,
			MchID:        resp.WechatMchId,
			APIV3Key:     resp.WechatApiV3Key,
			CertSerialNo: resp.WechatCertSerialNo,
			NotifyURL:    resp.WechatNotifyUrl,
		},
		Alipay: config.AlipayConfig{
			AppID:           resp.AlipayAppId,
			AlipayPublicKey: resp.AlipayPublicKey,
			NotifyURL:       resp.AlipayNotifyUrl,
			ReturnURL:       resp.AlipayReturnUrl,
		},
		WechatPaymentReady: resp.WechatPaymentReady,
		AlipayReady:        resp.AlipayReady,
		WechatCertUploaded: resp.WechatCertUploaded,
		AlipayCertUploaded: resp.AlipayCertUploaded,
		UpdatedAt:          resp.UpdatedAt,
		SuggestedURLs:      buildSuggestedURLs(c),
	}

	response.Success(c, setting)
}

// UpdatePaymentSetting 更新支付配置
// 配置写入数据库，payment 服务即时热更新，无需重启
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdatePaymentSetting(c *gin.Context) {
	var req PaymentSetting
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 支付宝保持关闭，即使前端传入也过滤掉
	var filtered []string
	for _, ch := range req.EnabledChannels {
		if ch != "alipay" {
			filtered = append(filtered, ch)
		}
	}

	_, err := h.paymentClient.SavePayConfig(c.Request.Context(), &payment.PayConfig{
		EnabledChannels:    filtered,
		WechatAppId:        req.Wechat.AppID,
		WechatAppSecret:    req.Wechat.AppSecret,
		WechatMchId:        req.Wechat.MchID,
		WechatApiV3Key:     req.Wechat.APIV3Key,
		WechatCertSerialNo: req.Wechat.CertSerialNo,
		// 私钥由证书上传接口单独维护，此处恒传空，payment 侧保留原值
		WechatPrivateKey: "",
		WechatNotifyUrl:  req.Wechat.NotifyURL,
		AlipayAppId:      req.Alipay.AppID,
		AlipayPrivateKey: "",
		AlipayPublicKey:  req.Alipay.AlipayPublicKey,
		AlipayNotifyUrl:  req.Alipay.NotifyURL,
		AlipayReturnUrl:  req.Alipay.ReturnURL,
	})
	if err != nil {
		log.Printf("保存支付设置失败: %v", err)
		response.GRPCError(c, err)
		return
	}

	response.Success(c, gin.H{"success": true})
}

// UploadPaymentCert 上传支付证书文件
// 接收 multipart 文件，校验通过后转发给 payment 服务存入数据库
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UploadPaymentCert(c *gin.Context) {
	channel := c.PostForm("channel")
	certType := c.PostForm("cert_type")
	if channel != "wechat" && channel != "alipay" {
		response.Error(c, errcode.ParamError, "不支持的支付渠道")
		return
	}
	if certType == "" {
		certType = "private_key"
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.Error(c, errcode.ParamError, "请选择要上传的证书文件")
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		response.Error(c, errcode.InternalServerError, "读取证书文件失败")
		return
	}
	defer func() { _ = file.Close() }()

	content, err := io.ReadAll(file)
	if err != nil {
		response.Error(c, errcode.InternalServerError, "读取证书文件内容失败")
		return
	}

	resp, err := h.paymentClient.UploadPayCert(c.Request.Context(), &payment.UploadPayCertReq{
		Channel:  channel,
		CertType: certType,
		Filename: fileHeader.Filename,
		Content:  content,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if !resp.Success {
		response.Error(c, errcode.ParamError, resp.Message)
		return
	}

	response.Success(c, gin.H{"success": true, "message": resp.Message})
}

// buildSuggestedURLs 根据当前请求生成支付平台回调地址建议
// 优先取反向代理透传的 X-Forwarded-Proto，兼容 HTTPS 部署场景
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - PaymentSuggestedURLs: 回调地址建议
func buildSuggestedURLs(c *gin.Context) PaymentSuggestedURLs {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}

	base := scheme + "://" + c.Request.Host
	return PaymentSuggestedURLs{
		BaseURL:         base,
		WechatNotifyURL: base + "/api/v1/payments/callback/wechat",
		AlipayNotifyURL: base + "/api/v1/payments/callback/alipay",
	}
}
