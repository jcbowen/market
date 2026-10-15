package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/response"
)

// ProductHandler 商品接口处理器
type ProductHandler struct {
	productClient product.ProductServiceClient
}

// NewProductHandler 创建商品处理器
// 参数：
//   - productClient: product-service gRPC 客户端
//
// 返回值：
//   - *ProductHandler: 商品处理器实例
func NewProductHandler(productClient product.ProductServiceClient) *ProductHandler {
	return &ProductHandler{productClient: productClient}
}

// ListProducts 商品列表
// 支持 community_id、category_id、keyword、sort 与分页
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) ListProducts(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	communityID, _ := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64)
	categoryID, _ := strconv.ParseInt(c.DefaultQuery("category_id", "0"), 10, 64)
	keyword := c.Query("keyword")
	sort := c.Query("sort")

	resp, err := h.productClient.ListProducts(c.Request.Context(), &product.ListProductsReq{
		CommunityId: communityID,
		CategoryId:  categoryID,
		Keyword:     keyword,
		Sort:        sort,
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

// GetProduct 商品详情
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) GetProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	communityID, _ := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64)
	resp, err := h.productClient.GetProduct(c.Request.Context(), &product.GetProductReq{
		Id:          id,
		CommunityId: communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListSkus 根据商品 ID 查询 SKU 列表
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) ListSkus(c *gin.Context) {
	productID, err := strconv.ParseInt(c.DefaultQuery("product_id", "0"), 10, 64)
	if err != nil || productID <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.ListSkus(c.Request.Context(), &product.ListSkusReq{ProductId: productID})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// GetSkuById 根据 SKU ID 查询 SKU 信息
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) GetSkuById(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.GetSkuById(c.Request.Context(), &product.GetSkuByIdReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListCategories 分类列表（公开接口）
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) ListCategories(c *gin.Context) {
	communityID, _ := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64)
	resp, err := h.productClient.ListCategories(c.Request.Context(), &product.ListCategoriesReq{
		CommunityId: communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// ListCoupons 优惠券列表
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) ListCoupons(c *gin.Context) {
	communityID, _ := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64)
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))

	resp, err := h.productClient.ListCoupons(c.Request.Context(), &product.ListCouponsReq{
		CommunityId:      communityID,
		Status:           int32(status),
		CenterVisibleOnly: true, // C 端领券中心仅展示已发布且进入领券中心的券
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// ReceiveCouponReq 领取优惠券请求
type ReceiveCouponReq struct {
	CouponID int64 `json:"coupon_id" binding:"required"`
}

// ReceiveCoupon 领取优惠券
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) ReceiveCoupon(c *gin.Context) {
	var req ReceiveCouponReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err := h.productClient.ReceiveCoupon(c.Request.Context(), &product.ReceiveCouponReq{
		CouponId: req.CouponID,
		UserId:   uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"coupon_id": req.CouponID})
}

// ListUserCoupons 获取当前用户优惠券列表
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) ListUserCoupons(c *gin.Context) {
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))
	uc := appcontext.GetUserContext(c)

	resp, err := h.productClient.ListUserCoupons(c.Request.Context(), &product.ListUserCouponsReq{
		UserId: uc.UserID,
		Status: int32(status),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// ListFlashSales 秒杀活动列表
// 返回包含活动基本信息及商品项的嵌套结构，方便 C 端扁平化展示
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) ListFlashSales(c *gin.Context) {
	communityID, _ := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64)
	resp, err := h.productClient.ListFlashSales(c.Request.Context(), &product.ListFlashSalesReq{
		CommunityId: communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	result := make([]gin.H, 0, len(resp.List))
	ctx := c.Request.Context()
	for _, fs := range resp.List {
		detail, err := h.productClient.GetFlashSale(ctx, &product.GetFlashSaleReq{Id: fs.Id})
		if err != nil {
			continue
		}

		items := make([]gin.H, 0, len(detail.Items))
		for _, it := range detail.Items {
			productID := int64(0)
			sku, skuErr := h.productClient.GetSkuById(ctx, &product.GetSkuByIdReq{Id: it.SkuId})
			if skuErr == nil && sku != nil {
				productID = sku.ProductId
			}
			items = append(items, gin.H{
				"id":             it.Id,
				"sku_id":         it.SkuId,
				"product_id":     productID,
				"product_name":   it.ProductName,
				"spec_name":      it.SpecName,
				"flash_price":    it.FlashPrice,
				"original_price": it.OriginalPrice,
				"stock":          it.Stock,
				"limit_per_user": it.LimitPerUser,
			})
		}

		result = append(result, gin.H{
			"id":         fs.Id,
			"name":       fs.Name,
			"start_time": fs.StartTime,
			"end_time":   fs.EndTime,
			"status":     fs.Status,
			"items":      items,
		})
	}
	response.SuccessWithList(c, result)
}

// ListFullReductions 获取满减活动列表
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) ListFullReductions(c *gin.Context) {
	communityID, _ := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64)

	resp, err := h.productClient.ListFullReductions(c.Request.Context(), &product.ListFullReductionsReq{
		CommunityId: communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// GetActiveFreeShipping 获取当前社区正在生效的免配送费活动（C 端，无生效活动返回 null）
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) GetActiveFreeShipping(c *gin.Context) {
	communityID, _ := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64)
	resp, err := h.productClient.GetActiveFreeShipping(c.Request.Context(), &product.GetActiveFreeShippingReq{
		CommunityId: communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp.GetActivity())
}

// ListBanners 获取 Banner 列表
// 参数：
//   - c: gin 上下文
func (h *ProductHandler) ListBanners(c *gin.Context) {
	communityID, _ := strconv.ParseInt(c.DefaultQuery("community_id", "0"), 10, 64)
	position, _ := strconv.Atoi(c.DefaultQuery("position", "0"))

	resp, err := h.productClient.ListBanners(c.Request.Context(), &product.ListBannersReq{
		CommunityId: communityID,
		Position:    int32(position),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}
