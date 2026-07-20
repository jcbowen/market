package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
)

// UserHandler C 端用户接口处理器
type UserHandler struct {
	userClient user.UserServiceClient
}

// NewUserHandler 创建 C 端用户处理器
// 参数：
//   - userClient: user-service gRPC 客户端
//
// 返回值：
//   - *UserHandler: 用户处理器实例
func NewUserHandler(userClient user.UserServiceClient) *UserHandler {
	return &UserHandler{userClient: userClient}
}

// Profile 获取当前登录用户信息
// 调用 user.GetUserById，从 appcontext 取 user_id
// 参数：
//   - c: gin 上下文
func (h *UserHandler) Profile(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	resp, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: uc.UserID})
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}
	response.Success(c, resp)
}

// UpdateCommunityReq 更新用户社区请求
type UpdateCommunityReq struct {
	CommunityID int64 `json:"community_id" binding:"required"`
}

// UpdateCommunity 更新当前用户所属社区
// 调用 user.UpdateUserCommunity
// 参数：
//   - c: gin 上下文
func (h *UserHandler) UpdateCommunity(c *gin.Context) {
	var req UpdateCommunityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err := h.userClient.UpdateUserCommunity(c.Request.Context(), &user.UpdateCommunityReq{
		UserId:      uc.UserID,
		CommunityId: req.CommunityID,
	})
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}
	response.Success(c, gin.H{"community_id": req.CommunityID})
}

// UpdateUserProfileReq 更新用户资料请求
type UpdateUserProfileReq struct {
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	Gender   int32  `json:"gender"`
}

// UpdateUserProfile 更新当前登录用户资料
// 调用 user.UpdateUserProfile
// 参数：
//   - c: gin 上下文
func (h *UserHandler) UpdateUserProfile(c *gin.Context) {
	var req UpdateUserProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.userClient.UpdateUserProfile(c.Request.Context(), &user.UpdateUserProfileReq{
		Id:       uc.UserID,
		Nickname: req.Nickname,
		Avatar:   req.Avatar,
		Gender:   req.Gender,
	})
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}
	response.Success(c, resp)
}

// ListAddresses 获取当前用户地址列表
// 调用 user.ListAddresses
// 参数：
//   - c: gin 上下文
func (h *UserHandler) ListAddresses(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	resp, err := h.userClient.ListAddresses(c.Request.Context(), &user.ListAddressesReq{UserId: uc.UserID})
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// CreateAddressReq 创建地址请求
type CreateAddressReq struct {
	ContactName  string `json:"contact_name" binding:"required"`
	ContactPhone string `json:"contact_phone" binding:"required"`
	Province     string `json:"province"`
	City         string `json:"city"`
	District     string `json:"district"`
	Detail       string `json:"detail" binding:"required"`
	Tag          string `json:"tag"`
	IsDefault    bool   `json:"is_default"`
}

// CreateAddress 创建收货地址
// 调用 user.CreateAddress
// 参数：
//   - c: gin 上下文
func (h *UserHandler) CreateAddress(c *gin.Context) {
	var req CreateAddressReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.userClient.CreateAddress(c.Request.Context(), &user.CreateAddressReq{
		UserId:       uc.UserID,
		ContactName:  req.ContactName,
		ContactPhone: req.ContactPhone,
		Province:     req.Province,
		City:         req.City,
		District:     req.District,
		Detail:       req.Detail,
		Tag:          req.Tag,
		IsDefault:    req.IsDefault,
	})
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}
	response.Success(c, resp)
}

// UpdateAddressReq 更新地址请求
type UpdateAddressReq struct {
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	Province     string `json:"province"`
	City         string `json:"city"`
	District     string `json:"district"`
	Detail       string `json:"detail"`
	Tag          string `json:"tag"`
	IsDefault    bool   `json:"is_default"`
}

// UpdateAddress 更新收货地址
// 调用 user.UpdateAddress
// 参数：
//   - c: gin 上下文
func (h *UserHandler) UpdateAddress(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateAddressReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.userClient.UpdateAddress(c.Request.Context(), &user.UpdateAddressReq{
		Id:           id,
		UserId:       uc.UserID,
		ContactName:  req.ContactName,
		ContactPhone: req.ContactPhone,
		Province:     req.Province,
		City:         req.City,
		District:     req.District,
		Detail:       req.Detail,
		Tag:          req.Tag,
		IsDefault:    req.IsDefault,
	})
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}
	response.Success(c, resp)
}

// DeleteAddress 删除收货地址
// 调用 user.DeleteAddress
// 参数：
//   - c: gin 上下文
func (h *UserHandler) DeleteAddress(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.userClient.DeleteAddress(c.Request.Context(), &user.DeleteAddressReq{
		Id:     id,
		UserId: uc.UserID,
	})
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// ListCommunities 获取社区列表（公开接口）
// 调用 user.ListCommunities
// 参数：
//   - c: gin 上下文
func (h *UserHandler) ListCommunities(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.userClient.ListCommunities(c.Request.Context(), &common.Pagination{
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}
	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}
