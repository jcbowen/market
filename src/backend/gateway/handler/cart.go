package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/cart"
	"github.com/jcbowen/market/src/backend/common/response"
)

// CartHandler 购物车接口处理器
type CartHandler struct {
	cartClient cart.CartServiceClient
}

// NewCartHandler 创建购物车处理器
// 参数：
//   - cartClient: cart-service gRPC 客户端
//
// 返回值：
//   - *CartHandler: 购物车处理器实例
func NewCartHandler(cartClient cart.CartServiceClient) *CartHandler {
	return &CartHandler{cartClient: cartClient}
}

// getCartCommunityID 从上下文或查询参数获取社区 ID
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - int64: 社区 ID
func getCartCommunityID(c *gin.Context) int64 {
	if id, err := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64); err == nil && id > 0 {
		return id
	}
	return appcontext.GetUserContext(c).CommunityID
}

// GetCart 获取当前用户购物车
// 在返回购物车项的同时汇总总金额、总件数与已选件数
// 参数：
//   - c: gin 上下文
func (h *CartHandler) GetCart(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	resp, err := h.cartClient.GetCart(c.Request.Context(), &cart.GetCartReq{
		UserId:      uc.UserID,
		CommunityId: getCartCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	var totalAmount float64
	var totalCount, selectedCount int32
	for _, item := range resp.Items {
		totalCount += item.Quantity
		if item.Selected {
			selectedCount += item.Quantity
			totalAmount += item.Price * float64(item.Quantity)
		}
	}

	response.Success(c, gin.H{
		"items":          resp.Items,
		"total_amount":   totalAmount,
		"total_count":    totalCount,
		"selected_count": selectedCount,
	})
}

// AddItemReq 添加购物车商品请求
// CommunityID 从 body 读取：前端统一以 JSON body 传社区 ID，
// 未传时降级 query，再降级 JWT 上下文（登录后切换社区时 JWT 中社区 ID 会过期，不能作为首选）
type AddItemReq struct {
	ProductID   int64 `json:"product_id" binding:"required"`
	SkuID       int64 `json:"sku_id" binding:"required"`
	Quantity    int32 `json:"quantity" binding:"required,min=1"`
	CommunityID int64 `json:"community_id"`
}

// AddItem 添加商品到购物车
// 参数：
//   - c: gin 上下文
func (h *CartHandler) AddItem(c *gin.Context) {
	var req AddItemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 社区 ID 优先级：body > query > JWT 上下文
	communityID := req.CommunityID
	if communityID <= 0 {
		communityID = getCartCommunityID(c)
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.cartClient.AddItem(c.Request.Context(), &cart.AddItemReq{
		UserId:      uc.UserID,
		CommunityId: communityID,
		ProductId:   req.ProductID,
		SkuId:       req.SkuID,
		Quantity:    req.Quantity,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateItemReq 更新购物车商品请求
type UpdateItemReq struct {
	Quantity int32 `json:"quantity"`
	Selected bool  `json:"selected"`
}

// UpdateItem 更新购物车商品（path id 为 sku_id）
// 参数：
//   - c: gin 上下文
func (h *CartHandler) UpdateItem(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateItemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.cartClient.UpdateItem(c.Request.Context(), &cart.UpdateItemReq{
		Id:       id,
		UserId:   uc.UserID,
		Quantity: req.Quantity,
		Selected: req.Selected,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// RemoveItem 删除购物车商品（path id 为 sku_id）
// 参数：
//   - c: gin 上下文
func (h *CartHandler) RemoveItem(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.cartClient.RemoveItem(c.Request.Context(), &cart.RemoveItemReq{
		Id:     id,
		UserId: uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// SelectAllReq 购物车全选/取消全选请求
type SelectAllReq struct {
	Selected bool `json:"selected"`
}

// SelectAll 购物车全选或取消全选
// 参数：
//   - c: gin 上下文
func (h *CartHandler) SelectAll(c *gin.Context) {
	var req SelectAllReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.cartClient.SelectAll(c.Request.Context(), &cart.SelectAllReq{
		UserId:   uc.UserID,
		Selected: req.Selected,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ClearInvalidItems 清空当前用户购物车中的失效商品
// 参数：
//   - c: gin 上下文
func (h *CartHandler) ClearInvalidItems(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	resp, err := h.cartClient.ClearInvalidItems(c.Request.Context(), &cart.ClearInvalidItemsReq{
		UserId: uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
