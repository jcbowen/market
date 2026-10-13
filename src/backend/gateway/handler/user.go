package handler

import (
	"strconv"
	"strings"

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

// addressItem 地址视图对象
// 用于向前端统一返回地址数据，避免 proto 的 omitempty 导致 bool 字段被省略
type addressItem struct {
	ID            int64  `json:"id"`
	UserID        int64  `json:"user_id"`
	ContactName   string `json:"contact_name"`
	ContactPhone  string `json:"contact_phone"`
	Province      string `json:"province"`
	City          string `json:"city"`
	District      string `json:"district"`
	Detail        string `json:"detail"`
	Tag           string `json:"tag"`
	CommunityID   int64  `json:"community_id"`
	CommunityName string `json:"community_name"`
	IsDefault     bool   `json:"is_default"`
	CreatedAt     string `json:"created_at"`
}

// toAddressItem 将 proto 地址信息转换为前端视图对象
// 参数：
//   - addr: proto 地址信息
//
// 返回值：
//   - addressItem: 前端视图对象
func toAddressItem(addr *user.AddressInfo) addressItem {
	if addr == nil {
		return addressItem{}
	}
	return addressItem{
		ID:            addr.Id,
		UserID:        addr.UserId,
		ContactName:   addr.ContactName,
		ContactPhone:  addr.ContactPhone,
		Province:      addr.Province,
		City:          addr.City,
		District:      addr.District,
		Detail:        addr.Detail,
		Tag:           addr.Tag,
		CommunityID:   addr.CommunityId,
		CommunityName: addr.CommunityName,
		IsDefault:     addr.IsDefault,
		CreatedAt:     addr.CreatedAt,
	}
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
		response.GRPCError(c, err)
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
		response.GRPCError(c, err)
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
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListAddresses 获取当前用户地址列表
// 调用 user.ListAddresses 并按通用分页格式返回
// 参数：
//   - c: gin 上下文
func (h *UserHandler) ListAddresses(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	resp, err := h.userClient.ListAddresses(c.Request.Context(), &user.ListAddressesReq{UserId: uc.UserID})
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

	list := make([]addressItem, 0, end-start)
	for _, addr := range resp.List[start:end] {
		list = append(list, toAddressItem(addr))
	}
	response.SuccessWithPage(c, list, total, page, pageSize)
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
		response.GRPCError(c, err)
		return
	}
	response.Success(c, toAddressItem(resp))
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
		response.GRPCError(c, err)
		return
	}
	response.Success(c, toAddressItem(resp))
}

// SetDefaultAddress 设置默认地址
// 调用 user.SetDefaultAddress
// 参数：
//   - c: gin 上下文
func (h *UserHandler) SetDefaultAddress(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.userClient.SetDefaultAddress(c.Request.Context(), &user.SetDefaultAddressReq{
		Id:     id,
		UserId: uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
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
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// ListCommunities 获取社区列表（公开接口）
// 支持关键词搜索与基于经纬度的附近社区排序
// 参数：
//   - c: gin 上下文
func (h *UserHandler) ListCommunities(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	keyword := c.Query("keyword")
	longitude, _ := strconv.ParseFloat(c.Query("longitude"), 64)
	latitude, _ := strconv.ParseFloat(c.Query("latitude"), 64)

	var resp *user.ListCommunitiesResp
	var err error
	if longitude != 0 && latitude != 0 {
		resp, err = h.userClient.ListNearbyCommunities(c.Request.Context(), &user.ListNearbyCommunitiesReq{
			Longitude: longitude,
			Latitude:  latitude,
			Pagination: &common.Pagination{
				Page:     int32(page),
				PageSize: int32(pageSize),
			},
		})
	} else {
		resp, err = h.userClient.ListCommunities(c.Request.Context(), &user.ListCommunitiesReq{
			// C 端仅展示营业中社区，显式声明语义，避免依赖 proto3 零值默认
			IncludeDisabled: false,
			Pagination: &common.Pagination{
				Page:     int32(page),
				PageSize: int32(pageSize),
			},
		})
	}
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	list := resp.List
	if keyword != "" {
		filtered := make([]*user.CommunityInfo, 0, len(list))
		lowerKeyword := strings.ToLower(keyword)
		for _, item := range list {
			if strings.Contains(strings.ToLower(item.Name), lowerKeyword) || strings.Contains(strings.ToLower(item.Address), lowerKeyword) {
				filtered = append(filtered, item)
			}
		}
		list = filtered
	}

	response.SuccessWithPage(c, list, int(resp.Pagination.Total), page, pageSize)
}

// GetCommunity 获取社区详情（公开接口）
// C 端地址编辑页据此判断是否锁定地址前缀（allow_outside_delivery）并回填省市区
// 参数：
//   - c: gin 上下文
func (h *UserHandler) GetCommunity(c *gin.Context) {
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
