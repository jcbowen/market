package handler

import (
	"context"
	"log"
	"strconv"
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
	cfg           *config.Config
}

// NewAdminHandler 创建管理员接口处理器
// 参数：
//   - jwtManager: JWT 管理器，用于签发 token
//   - userClient: user-service gRPC 客户端
//   - productClient: product-service gRPC 客户端
//   - orderClient: order-service gRPC 客户端
//   - cfg: 全局配置指针
//
// 返回值：
//   - *AdminHandler: 管理员处理器实例
func NewAdminHandler(jwtManager *jwt.Manager, userClient user.UserServiceClient, productClient product.ProductServiceClient, orderClient order.OrderServiceClient, cfg *config.Config) *AdminHandler {
	return &AdminHandler{
		jwtManager:    jwtManager,
		userClient:    userClient,
		productClient: productClient,
		orderClient:   orderClient,
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

	permissions := h.getAdminPermissions(c.Request.Context(), admin.RoleId)
	communityID := h.getAdminDefaultCommunityID(admin)
	accessToken, accessExpire, err := h.jwtManager.GenerateAccessToken(
		admin.Id, jwt.TokenTypeAdmin, admin.Phone, admin.RoleCode, communityID, permissions,
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
			"id":             admin.Id,
			"username":       admin.Username,
			"real_name":      admin.RealName,
			"phone":          admin.Phone,
			"role_id":        admin.RoleId,
			"role_code":      admin.RoleCode,
			"role_name":      admin.RoleName,
			"community_id":   communityID,
			"community_ids":  admin.CommunityIds,
		},
	})
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

// getAdminPermissions 根据角色 ID 获取权限列表
// 参数：
//   - ctx: 上下文
//   - roleID: 角色 ID
//
// 返回值：
//   - []string: 权限编码列表
func (h *AdminHandler) getAdminPermissions(ctx context.Context, roleID int64) []string {
	roles, err := h.userClient.ListRoles(ctx, &common.Empty{})
	if err != nil {
		return nil
	}
	for _, r := range roles.List {
		if r.Id == roleID {
			return r.Permissions
		}
	}
	return nil
}

// getAdminCommunityID 获取管理员当前社区 ID，优先查询参数，其次 JWT 上下文
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - int64: 社区 ID
func (h *AdminHandler) getAdminCommunityID(c *gin.Context) int64 {
	if id, err := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64); err == nil && id > 0 {
		return id
	}
	return appcontext.GetUserContext(c).CommunityID
}

// ListAdmins 获取管理员列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdmins(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	communityID, _ := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64)

	resp, err := h.userClient.ListAdmins(c.Request.Context(), &user.ListAdminsReq{
		CommunityId: communityID,
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

// CreateAdminReq 创建管理员请求
// 包含管理员基础信息字段
type CreateAdminReq struct {
	Username    string `json:"username" binding:"required"`
	Password    string `json:"password" binding:"required,min=6"`
	RealName    string `json:"real_name"`
	Phone       string `json:"phone"`
	RoleID      int64  `json:"role_id" binding:"required"`
	CommunityID int64  `json:"community_id"`
}

// CreateAdmin 创建管理员
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateAdmin(c *gin.Context) {
	var req CreateAdminReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	admin, err := h.userClient.CreateAdmin(c.Request.Context(), &user.CreateAdminReq{
		Username:    req.Username,
		Password:    req.Password,
		RealName:    req.RealName,
		Phone:       req.Phone,
		RoleId:      req.RoleID,
		CommunityId: req.CommunityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, h.toAdminVO(admin))
}

// UpdateAdminReq 更新管理员请求
// 包含管理员可更新字段
type UpdateAdminReq struct {
	RealName    string `json:"real_name"`
	Phone       string `json:"phone"`
	RoleID      int64  `json:"role_id"`
	CommunityID int64  `json:"community_id"`
	Status      int32  `json:"status"`
}

// UpdateAdmin 更新管理员
// 普通管理员只允许修改自己的基础信息；超级管理员可修改任意管理员
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateAdmin(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	ctx := appcontext.GetUserContext(c)
	if ctx.Role != "super_admin" && ctx.UserID != id {
		response.Error(c, errcode.NoPermission)
		return
	}

	var req UpdateAdminReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	admin, err := h.userClient.UpdateAdmin(c.Request.Context(), &user.UpdateAdminReq{
		Id:          id,
		RealName:    req.RealName,
		Phone:       req.Phone,
		RoleId:      req.RoleID,
		CommunityId: req.CommunityID,
		Status:      req.Status,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, h.toAdminVO(admin))
}

// DeleteAdmin 删除管理员
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteAdmin(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.userClient.DeleteAdmin(c.Request.Context(), &user.DeleteAdminReq{Id: id})
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
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListRoles(c *gin.Context) {
	resp, err := h.userClient.ListRoles(c.Request.Context(), &common.Empty{})
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
}

// CreateRole 创建角色
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateRole(c *gin.Context) {
	var req CreateRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.userClient.CreateRole(c.Request.Context(), &user.CreateRoleReq{
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

// UpdateRoleReq 更新角色请求
type UpdateRoleReq struct {
	Name        string   `json:"name"`
	Code        string   `json:"code"`
	Permissions []string `json:"permissions"`
	Description string   `json:"description"`
}

// UpdateRole 更新角色
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
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteRole(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.userClient.DeleteRole(c.Request.Context(), &user.DeleteRoleReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

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

	accessToken, accessExpire, err := h.jwtManager.GenerateAccessToken(
		claims.UserID, jwt.TokenTypeAdmin, claims.Phone, claims.Role, claims.CommunityID, claims.Permissions,
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

// CreateProductReq 创建商品请求
type CreateProductReq struct {
	CategoryID  int64                   `json:"category_id" binding:"required"`
	Name        string                  `json:"name" binding:"required"`
	Description string                  `json:"description"`
	CoverImage  string                  `json:"cover_image"`
	Images      []string                `json:"images"`
	Unit        string                  `json:"unit"`
	IsMultiSpec bool                    `json:"is_multi_spec"`
	MinBuy      int32                   `json:"min_buy"`
	Sort        int32                   `json:"sort"`
	Skus        []*product.CreateSkuReq `json:"skus"`
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
		Skus:        req.Skus,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateProductReq 更新商品请求
type UpdateProductReq struct {
	CategoryID  int64    `json:"category_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CoverImage  string   `json:"cover_image"`
	Images      []string `json:"images"`
	Unit        string   `json:"unit"`
	Sort        int32    `json:"sort"`
	Status      int32    `json:"status"`
	MinBuy      int32    `json:"min_buy"`
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
			"id":           o.Id,
			"order_no":     o.OrderNo,
			"user_id":      o.UserId,
			"user_name":    userName,
			"total_amount": o.TotalAmount,
			"pay_amount":   o.PayAmount,
			"status":       o.Status,
			"status_text":  orderStatusText(o.Status),
			"created_at":   o.CreatedAt,
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
			"id":           item.Id,
			"product_id":   item.ProductId,
			"sku_id":       item.SkuId,
			"product_name": item.ProductName,
			"sku_name":     item.SpecName,
			"product_image": item.ProductImage,
			"price":        item.Price,
			"quantity":     item.Quantity,
			"total_amount": item.TotalPrice,
		})
	}

	data := gin.H{
		"id":           o.Id,
		"order_no":     o.OrderNo,
		"user_id":      o.UserId,
		"user_name":    userName,
		"total_amount": o.TotalAmount,
		"pay_amount":   o.PayAmount,
		"status":       o.Status,
		"status_text":  orderStatusText(o.Status),
		"created_at":   o.CreatedAt,
		"items":        items,
		"address":      address,
	}
	response.Success(c, data)
}

// UpdateOrderStatusReq 更新订单状态请求
type UpdateOrderStatusReq struct {
	Status int32  `json:"status" binding:"required"`
	Remark string `json:"remark"`
}

// UpdateOrderStatus 更新订单状态
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

// ShipOrderReq 订单发货请求
type ShipOrderReq struct {
	ExpressCompany string `json:"express_company"`
	ExpressNo      string `json:"express_no"`
	Remark         string `json:"remark"`
}

// ShipOrder 订单发货
// 校验订单状态为待发货（status=2）后，将订单状态更新为已发货（status=4），并记录物流信息到备注
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
	if orderResp.Order.Status != 2 {
		response.Error(c, errcode.ParamError, "订单状态不允许发货")
		return
	}

	remark := req.Remark
	if req.ExpressCompany != "" || req.ExpressNo != "" {
		if remark != "" {
			remark += "; "
		}
		remark += "物流公司: " + req.ExpressCompany + ", 物流单号: " + req.ExpressNo
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:           id,
		Status:       4,
		OperatorType: "admin",
		OperatorId:   uc.UserID,
		Remark:       remark,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": 4})
}

// CompleteOrder 完成订单
// 校验订单状态为已发货（status=4）后，将订单状态更新为已完成（status=5）
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
	if orderResp.Order.Status != 4 {
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
	Type   int32                  `json:"type" binding:"required"`
	Items  []*product.StockOutItem `json:"items" binding:"required"`
	Remark string                 `json:"remark"`
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
			"id":            u.Id,
			"openid":        u.Openid,
			"phone":         u.Phone,
			"nickname":      u.Nickname,
			"avatar":        u.Avatar,
			"gender":        u.Gender,
			"community_id":  u.CommunityId,
			"community_name": "",
			"status":        u.Status,
			"last_login_at": u.LastLoginAt,
			"created_at":    u.CreatedAt,
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

	resp, err := h.userClient.ListCommunities(c.Request.Context(), &common.Pagination{
		Page:     int32(page),
		PageSize: int32(pageSize),
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
	PickupAddress   string  `json:"pickup_address"`
	DeliveryFee     float64 `json:"delivery_fee"`
	MinOrderAmount  float64 `json:"min_order_amount"`
	DeliveryRangeKm float64 `json:"delivery_range_km"`
	BusinessHours   string  `json:"business_hours"`
	Sort            int32   `json:"sort"`
	Status          int32   `json:"status"`
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
		PickupAddress:   req.PickupAddress,
		DeliveryFee:     req.DeliveryFee,
		MinOrderAmount:  req.MinOrderAmount,
		DeliveryRangeKm: req.DeliveryRangeKm,
		BusinessHours:   req.BusinessHours,
		Sort:            req.Sort,
		Status:          req.Status,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateAdminCommunityReq 更新社区请求
type UpdateAdminCommunityReq struct {
	Name            string  `json:"name"`
	Address         string  `json:"address"`
	Longitude       float64 `json:"longitude"`
	Latitude        float64 `json:"latitude"`
	ContactPhone    string  `json:"contact_phone"`
	PickupAddress   string  `json:"pickup_address"`
	DeliveryFee     float64 `json:"delivery_fee"`
	MinOrderAmount  float64 `json:"min_order_amount"`
	DeliveryRangeKm float64 `json:"delivery_range_km"`
	BusinessHours   string  `json:"business_hours"`
	Sort            int32   `json:"sort"`
	Status          int32   `json:"status"`
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
		PickupAddress:   req.PickupAddress,
		DeliveryFee:     req.DeliveryFee,
		MinOrderAmount:  req.MinOrderAmount,
		DeliveryRangeKm: req.DeliveryRangeKm,
		BusinessHours:   req.BusinessHours,
		Sort:            req.Sort,
		Status:          req.Status,
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

// --- 退款管理 ---

// ListRefunds 获取退款列表
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

// ApproveRefundReq 同意退款请求
type ApproveRefundReq struct {
	PaymentNo string `json:"payment_no"`
}

// ApproveRefund 同意退款
// 接收前端透传的 payment_no，并原样转发给 order-service/payment-service
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ApproveRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req ApproveRefundReq
	_ = c.ShouldBindJSON(&req)

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.ApproveRefund(c.Request.Context(), &order.ApproveRefundReq{
		Id:         id,
		OperatorId: uc.UserID,
		PaymentNo:  req.PaymentNo,
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
		"role_id":       admin.RoleId,
		"role_code":     admin.RoleCode,
		"role_name":     admin.RoleName,
		"community_id":  admin.CommunityId,
		"status":        admin.Status,
		"last_login_at": admin.LastLoginAt,
		"last_login_ip": admin.LastLoginIp,
	}
}

// orderStatusText 将订单状态码转换为中文文案
// 参数：
//   - status: 状态码
//
// 返回值：
//   - string: 状态中文描述
func orderStatusText(status int32) string {
	switch status {
	case 1:
		return "待支付"
	case 2:
		return "待发货"
	case 3:
		return "备货中"
	case 4:
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
// 同时用于 GET 响应与 PUT 请求
type PaymentSetting struct {
	EnabledChannels    []string             `json:"enabled_channels"`
	Wechat             config.WechatConfig  `json:"wechat"`
	Alipay             config.AlipayConfig  `json:"alipay"`
	WechatPaymentReady bool                 `json:"wechat_payment_ready"`
	AlipayReady        bool                 `json:"alipay_ready"`
}

// GetPaymentSetting 获取支付配置
// 返回当前支付渠道开关、顺序以及微信/支付宝配置
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetPaymentSetting(c *gin.Context) {
	config.CfgMu.RLock()
	defer config.CfgMu.RUnlock()

	response.Success(c, PaymentSetting{
		EnabledChannels:    h.cfg.Payment.EnabledChannels,
		Wechat:             h.cfg.Wechat,
		Alipay:             h.cfg.Alipay,
		WechatPaymentReady: h.cfg.Wechat.IsPaymentConfigured(),
		AlipayReady:        h.cfg.Alipay.IsConfigured(),
	})
}

// UpdatePaymentSetting 更新支付配置
// 保存后会回写到配置文件，支付渠道列表即时生效；微信支付密钥等修改后需重启服务生效
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

	config.CfgMu.Lock()
	defer config.CfgMu.Unlock()

	h.cfg.Payment.EnabledChannels = filtered
	h.cfg.Wechat = req.Wechat
	h.cfg.Alipay = req.Alipay

	if err := config.Save(h.cfg); err != nil {
		log.Printf("保存支付设置失败: %v", err)
		response.Error(c, errcode.InternalServerError, "保存支付设置失败")
		return
	}

	response.Success(c, gin.H{"success": true})
}
