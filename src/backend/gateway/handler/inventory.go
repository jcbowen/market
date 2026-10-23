package handler

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/response"
	gwMiddleware "github.com/jcbowen/market/src/backend/gateway/middleware"
)

// ==================== 进销存重构：供应商 / 采购单 / 入库单 / 出库单 / 批次 / 报表 ====================
// 说明：与 admin.go 中的库存查询类 handler（流水/预警/盘点/补货建议）互补，
// 本文件承载单据化进销存的写操作与新查询。

// ensureBillInCommunity 管理端单据详情的社区归属校验（多社区隔离）
// 采购单/入库单/出库单按社区隔离，防跨社区 ID 枚举读取商业数据
// （含采购成本/供应商信息）；超管跨社区查询不受限。
// 参数：
//   - c: gin 上下文
//   - communityID: 单据归属社区 ID
//
// 返回值：
//   - error: 跨社区时返回错误
func (h *AdminHandler) ensureBillInCommunity(c *gin.Context, communityID int64) error {
	if gwMiddleware.IsSuperAdmin(c) {
		return nil
	}
	if communityID != h.getAdminCommunityID(c) {
		return errors.New("无权查看其他社区的单据")
	}
	return nil
}

// --- 供应商 ---

// CreateSupplier 创建供应商
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateSupplier(c *gin.Context) {
	var req product.CreateSupplierReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	req.CommunityId = h.getAdminCommunityID(c)

	sp, err := h.productClient.CreateSupplier(c.Request.Context(), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, sp)
}

// UpdateSupplier 更新供应商
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateSupplier(c *gin.Context) {
	var req product.UpdateSupplierReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	req.Id = id
	req.CommunityId = h.getAdminCommunityID(c)

	sp, err := h.productClient.UpdateSupplier(c.Request.Context(), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, sp)
}

// DeleteSupplier 删除供应商
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteSupplier(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	_, err = h.productClient.DeleteSupplier(c.Request.Context(), &product.DeleteSupplierReq{
		Id:          id,
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

// ListSuppliers 查询供应商列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListSuppliers(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))

	resp, err := h.productClient.ListSuppliers(c.Request.Context(), &product.ListSuppliersReq{
		CommunityId: h.getAdminCommunityID(c),
		Keyword:     c.Query("keyword"),
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

// --- 采购单 ---

// CreatePurchaseOrder 创建采购单
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreatePurchaseOrder(c *gin.Context) {
	var req product.CreatePurchaseOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	req.CommunityId = h.getAdminCommunityID(c)
	req.OperatorId = appcontext.GetUserContext(c).UserID

	detail, err := h.productClient.CreatePurchaseOrder(c.Request.Context(), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, detail)
}

// UpdatePurchaseOrder 更新采购单（仅草稿）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdatePurchaseOrder(c *gin.Context) {
	var req product.UpdatePurchaseOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	req.Id = id
	req.CommunityId = h.getAdminCommunityID(c)

	detail, err := h.productClient.UpdatePurchaseOrder(c.Request.Context(), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, detail)
}

// SubmitPurchaseOrder 提交采购单（草稿→已下单）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) SubmitPurchaseOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	_, err = h.productClient.SubmitPurchaseOrder(c.Request.Context(), &product.SubmitPurchaseOrderReq{
		Id:          id,
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

// CancelPurchaseOrder 取消采购单
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CancelPurchaseOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	_, err = h.productClient.CancelPurchaseOrder(c.Request.Context(), &product.CancelPurchaseOrderReq{
		Id:          id,
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

// ListPurchaseOrders 查询采购单列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListPurchaseOrders(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))
	supplierID, _ := strconv.ParseInt(c.DefaultQuery("supplier_id", "0"), 10, 64)

	resp, err := h.productClient.ListPurchaseOrders(c.Request.Context(), &product.ListPurchaseOrdersReq{
		CommunityId: h.getAdminCommunityID(c),
		Status:      int32(status),
		SupplierId:  supplierID,
		Keyword:     c.Query("keyword"),
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

// GetPurchaseOrder 查询采购单详情
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetPurchaseOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	detail, err := h.productClient.GetPurchaseOrder(c.Request.Context(), &product.GetPurchaseOrderReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if err := h.ensureBillInCommunity(c, detail.Info.CommunityId); err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}
	response.Success(c, detail)
}

// --- 入库单 ---

// CreateStockInOrder 创建入库单（创建即生效）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateStockInOrder(c *gin.Context) {
	var req product.CreateStockInOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	req.CommunityId = h.getAdminCommunityID(c)
	req.OperatorId = appcontext.GetUserContext(c).UserID

	detail, err := h.productClient.CreateStockInOrder(c.Request.Context(), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, detail)
}

// ListStockInOrders 查询入库单列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListStockInOrders(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	purchaseOrderID, _ := strconv.ParseInt(c.DefaultQuery("purchase_order_id", "0"), 10, 64)

	resp, err := h.productClient.ListStockInOrders(c.Request.Context(), &product.ListStockInOrdersReq{
		CommunityId:     h.getAdminCommunityID(c),
		PurchaseOrderId: purchaseOrderID,
		StartDate:       c.Query("start_date"),
		EndDate:         c.Query("end_date"),
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

// GetStockInOrder 查询入库单详情
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetStockInOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	detail, err := h.productClient.GetStockInOrder(c.Request.Context(), &product.GetStockInOrderReq{Id: id, CommunityId: h.scopeCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if err := h.ensureBillInCommunity(c, detail.Info.CommunityId); err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}
	response.Success(c, detail)
}

// --- 出库单 ---

// CreateStockOutOrder 创建出库单（报损/领用/调整）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateStockOutOrder(c *gin.Context) {
	var req product.CreateStockOutOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	req.CommunityId = h.getAdminCommunityID(c)
	req.OperatorId = appcontext.GetUserContext(c).UserID

	detail, err := h.productClient.CreateStockOutOrder(c.Request.Context(), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, detail)
}

// ListStockOutOrders 查询出库单列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListStockOutOrders(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	outType, _ := strconv.Atoi(c.DefaultQuery("type", "0"))

	resp, err := h.productClient.ListStockOutOrders(c.Request.Context(), &product.ListStockOutOrdersReq{
		CommunityId: h.getAdminCommunityID(c),
		Type:        int32(outType),
		StartDate:   c.Query("start_date"),
		EndDate:     c.Query("end_date"),
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

// GetStockOutOrder 查询出库单详情
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetStockOutOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	detail, err := h.productClient.GetStockOutOrder(c.Request.Context(), &product.GetStockOutOrderReq{Id: id, CommunityId: h.scopeCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if err := h.ensureBillInCommunity(c, detail.Info.CommunityId); err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}
	response.Success(c, detail)
}

// --- 批次库存 ---

// ListStockBatches 查询批次库存
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListStockBatches(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	skuID, _ := strconv.ParseInt(c.DefaultQuery("sku_id", "0"), 10, 64)
	inStockOnly := c.Query("in_stock_only") == "1" || c.Query("in_stock_only") == "true"

	resp, err := h.productClient.ListStockBatches(c.Request.Context(), &product.ListStockBatchesReq{
		CommunityId: h.getAdminCommunityID(c),
		SkuId:       skuID,
		Keyword:     c.Query("keyword"),
		InStockOnly: inStockOnly,
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

// --- 报表 ---

// GetInventorySummaryReport 进销存汇总报表（期初+入-出=期末）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetInventorySummaryReport(c *gin.Context) {
	page, pageSize := parsePageParams(c)

	resp, err := h.productClient.GetInventorySummaryReport(c.Request.Context(), &product.GetInventorySummaryReportReq{
		CommunityId: h.getAdminCommunityID(c),
		StartDate:   c.Query("start_date"),
		EndDate:     c.Query("end_date"),
		Keyword:     c.Query("keyword"),
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

// GetProfitReport 毛利分析报表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetProfitReport(c *gin.Context) {
	page, pageSize := parsePageParams(c)

	resp, err := h.productClient.GetProfitReport(c.Request.Context(), &product.GetProfitReportReq{
		CommunityId: h.getAdminCommunityID(c),
		StartDate:   c.Query("start_date"),
		EndDate:     c.Query("end_date"),
		Keyword:     c.Query("keyword"),
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
