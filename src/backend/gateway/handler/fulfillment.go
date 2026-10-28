package handler

import (
	"io"
	"log"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
)

// ============================================================================
// C 端社区履约工作台（B）
// 鉴权模型：authUser 中间件验登录 → 每个 handler 实时校验当前用户在其当前归属社区
// 是履约人员（员工编辑页绑定会员的员工，admin_users.user_id），非履约人员一律拒绝。
// 用户当前社区实时查（与 CreateOrder 同口径），不用 token 快照。
// ============================================================================

// currentUserCommunity 实时查询用户当前归属社区（token 快照不可信，切换社区不重签）
// 参数：
//   - c: gin 上下文
//   - userID: 用户 ID
//
// 返回值：
//   - int64: 社区 ID（<=0 表示未归属任何社区）
//   - bool: 是否查询成功
func (h *OrderHandler) currentUserCommunity(c *gin.Context, userID int64) (int64, bool) {
	resp, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: userID})
	if err != nil {
		log.Printf("查询用户归属社区失败: %v", err)
		response.GRPCError(c, err)
		return 0, false
	}
	if resp == nil {
		response.Error(c, errcode.NotFound, "用户不存在")
		return 0, false
	}
	return resp.CommunityId, true
}

// checkFulfiller 校验当前用户在指定社区是履约人员；校验失败时直接写响应
// 参数：
//   - c: gin 上下文
//   - userID: 用户 ID
//   - communityID: 社区 ID
//
// 返回值：
//   - bool: 是否通过（false 时响应已写）
func (h *OrderHandler) checkFulfiller(c *gin.Context, userID, communityID int64) bool {
	if communityID <= 0 {
		response.Error(c, errcode.NoPermission, "请先选择社区")
		return false
	}
	check, err := h.userClient.CheckFulfiller(c.Request.Context(), &user.CheckFulfillerReq{
		UserId:      userID,
		CommunityId: communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return false
	}
	if check == nil || !check.IsFulfiller {
		response.Error(c, errcode.NoPermission, "您不是本社区履约人员")
		return false
	}
	return true
}

// GetFulfillmentStatus 查询当前用户履约人员身份（我的页工作台入口判断）
//
// 接口: GET /api/v1/user/fulfillment-status
//
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) GetFulfillmentStatus(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	communityID, ok := h.currentUserCommunity(c, uc.UserID)
	if !ok {
		return
	}
	isFulfiller := false
	if communityID > 0 {
		check, err := h.userClient.CheckFulfiller(c.Request.Context(), &user.CheckFulfillerReq{
			UserId:      uc.UserID,
			CommunityId: communityID,
		})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		isFulfiller = check != nil && check.IsFulfiller
	}
	response.Success(c, gin.H{
		"is_fulfiller": isFulfiller,
		"community_id": communityID,
	})
}

// fulfillmentMaxPageSize 工作台单次查单上限，与 order 服务 fulfillment.go 的页长封顶保持一致
const fulfillmentMaxPageSize = 50

// ListFulfillmentOrdersReq 工作台查单请求
// 分页不在此结构体里声明：统一走 parsePageParams（与其余列表接口同一套默认值与封顶口径）
type ListFulfillmentOrdersReq struct {
	OrderType   int32 `form:"order_type"`   // 1=自提核销 2=送货上门
	StatusGroup int32 `form:"status_group"` // 0=进行中 5=已完成/已核销
}

// fulfillmentStatusText 工作台状态文案（履约视角，不复用 C 端买家文案）
// 买家口径把 status=4 的配送单说成「待收货」，履约人员看自己是「正在配送」，语义相反；
// 自提单同理是「待核销」而不是「待自提」。
// 参数：
//   - status: 订单状态
//   - orderType: 单据类型（1=自提核销 2=送货）
//
// 返回值：
//   - string: 展示文案
func fulfillmentStatusText(status, orderType int32) string {
	switch status {
	case 2, 3:
		if orderType == 1 {
			return "待核销"
		}
		return "待配送"
	case 4:
		if orderType == 1 {
			return "待核销"
		}
		return "配送中"
	case 5:
		if orderType == 1 {
			return "已核销"
		}
		return "已送达"
	}
	return ""
}

// ListFulfillmentOrders 工作台查单（送货按指派人过滤；自提本社区全部）
//
// 接口: GET /api/v1/fulfillment/orders
//
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) ListFulfillmentOrders(c *gin.Context) {
	var req ListFulfillmentOrdersReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	uc := appcontext.GetUserContext(c)
	communityID, ok := h.currentUserCommunity(c, uc.UserID)
	if !ok {
		return
	}
	if !h.checkFulfiller(c, uc.UserID, communityID) {
		return
	}

	// 分页统一走 parsePageParams；服务端把 >50 的页长一律回退成 10，
	// 网关若透传封顶值 100 会出现「请求 100 条只回 10 条」的静默短页，故先收到 50
	page, pageSize := parsePageParams(c)
	if pageSize > fulfillmentMaxPageSize {
		pageSize = fulfillmentMaxPageSize
	}

	resp, err := h.orderClient.ListFulfillmentOrders(c.Request.Context(), &order.ListFulfillmentOrdersReq{
		OrderType:      req.OrderType,
		CommunityId:    communityID,
		DeliveryUserId: uc.UserID,
		StatusGroup:    req.StatusGroup,
		Pagination:     &common.Pagination{Page: int32(page), PageSize: int32(pageSize)},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 不能直接把 proto 回给前端：OrderInfo 没有 items（只有 item_count），也没有 status_text，
	// 而工作台卡片要列商品名与状态，模板取 order.items.slice() 会当场抛错、整页画不出任何卡片
	// （2026-10-29 浏览器实测发现）。与 C 端订单列表同口径，并行补明细。
	// GetOrder 传 UserId=0 跳过买家归属校验：本接口已按「当前社区 + 履约身份 + 送货指派人」三重过滤，
	// 出现在这里的单本就归该履约人员处理
	detailCtx := c.Request.Context()
	itemLists := make([][]gin.H, len(resp.List))
	var wg sync.WaitGroup
	for i, o := range resp.List {
		wg.Add(1)
		go func(idx int64, orderID int64) {
			defer wg.Done()
			detail, err := h.orderClient.GetOrder(detailCtx, &order.GetOrderReq{Id: orderID})
			if err != nil {
				// 明细缺失只让该单少列商品，不应整页失败（订单本体字段已在列表里）
				log.Printf("履约工作台拉取订单明细失败（order=%d）: %v", orderID, err)
				return
			}
			items := make([]gin.H, 0, len(detail.GetItems()))
			for _, it := range detail.GetItems() {
				items = append(items, gin.H{
					"sku_id":       it.SkuId,
					"product_name": it.ProductName,
					"spec_name":    it.SpecName,
					"quantity":     it.Quantity,
				})
			}
			itemLists[idx] = items
		}(int64(i), o.Id)
	}
	wg.Wait()

	list := make([]gin.H, 0, len(resp.List))
	for i, o := range resp.List {
		items := itemLists[i]
		if items == nil {
			items = make([]gin.H, 0)
		}
		// 不回 pickup_code：取货码由顾客在详情页出示，回给履约端等于给了可截图冒领的凭据
		list = append(list, gin.H{
			"id":                    o.Id,
			"order_no":              o.OrderNo,
			"status":                o.Status,
			"status_text":           fulfillmentStatusText(o.Status, req.OrderType),
			"delivery_type":         o.DeliveryType,
			"contact_name":          o.ContactName,
			"contact_phone":         o.ContactPhone,
			"contact_address":       o.ContactAddress,
			"pay_amount":            o.PayAmount,
			"delivery_user_id":      o.DeliveryUserId,
			"delivery_user_name":    o.DeliveryUserName,
			"delivery_proof_images": o.DeliveryProofImages,
			"created_at":            o.CreatedAt,
			"items":                 items,
		})
	}
	response.SuccessWithPage(c, list, int(resp.GetPagination().GetTotal()), page, pageSize)
}

// FulfillmentOrderActionReq 工作台订单操作请求（核销码）
type FulfillmentOrderActionReq struct {
	PickupCode  string   `json:"pickup_code"`
	ProofImages []string `json:"proof_images"`
}

// StartDelivery 开始配送（履约人员，status 2/3 → 4）
//
// 接口: POST /api/v1/fulfillment/orders/:id/start-delivery
//
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) StartDelivery(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	uc := appcontext.GetUserContext(c)
	communityID, ok := h.currentUserCommunity(c, uc.UserID)
	if !ok {
		return
	}
	if !h.checkFulfiller(c, uc.UserID, communityID) {
		return
	}
	if _, err := h.orderClient.StartDelivery(c.Request.Context(), &order.StartDeliveryReq{
		OrderId:        id,
		DeliveryUserId: uc.UserID,
		CommunityId:    communityID,
		OperatorType:   "staff",
		OperatorId:     uc.UserID,
	}); err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

// CompleteDelivery 送达完成（履约人员，status 4 → 5，强制签收照片，决策点②）
//
// 接口: POST /api/v1/fulfillment/orders/:id/complete-delivery
//
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) CompleteDelivery(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	var req FulfillmentOrderActionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if len(req.ProofImages) == 0 {
		response.Error(c, errcode.ParamError, "请上传签收照片后再完成送达")
		return
	}
	uc := appcontext.GetUserContext(c)
	communityID, ok := h.currentUserCommunity(c, uc.UserID)
	if !ok {
		return
	}
	if !h.checkFulfiller(c, uc.UserID, communityID) {
		return
	}
	if _, err := h.orderClient.CompleteDelivery(c.Request.Context(), &order.CompleteDeliveryReq{
		OrderId:        id,
		DeliveryUserId: uc.UserID,
		CommunityId:    communityID,
		OperatorType:   "staff",
		OperatorId:     uc.UserID,
		ProofImages:    req.ProofImages,
	}); err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

// VerifyPickupByStaff 履约人员核销自提单（复用 VerifyPickup，operator_type=staff）
//
// 接口: POST /api/v1/fulfillment/orders/:id/verify-pickup
//
// 参数：
//   - c: gin 上下文
func (h *OrderHandler) VerifyPickupByStaff(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	var req FulfillmentOrderActionReq
	if err := c.ShouldBindJSON(&req); err != nil || req.PickupCode == "" {
		response.Error(c, errcode.ParamError, "请输入取货码")
		return
	}
	uc := appcontext.GetUserContext(c)
	communityID, ok := h.currentUserCommunity(c, uc.UserID)
	if !ok {
		return
	}
	if !h.checkFulfiller(c, uc.UserID, communityID) {
		return
	}
	resp, err := h.orderClient.VerifyPickup(c.Request.Context(), &order.VerifyPickupReq{
		OrderId:      id,
		PickupCode:   req.PickupCode,
		OperatorType: "staff",
		OperatorId:   uc.UserID,
		CommunityId:  communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// FulfillmentUploadHandler C 端上传处理器（持有 product 客户端）
type FulfillmentUploadHandler struct {
	productClient product.ProductServiceClient
}

// NewFulfillmentUploadHandler 创建 C 端上传处理器
func NewFulfillmentUploadHandler(productClient product.ProductServiceClient) *FulfillmentUploadHandler {
	return &FulfillmentUploadHandler{productClient: productClient}
}

// UploadImage C 端图片上传（IsDisplay=false 不进附件选择器）
//
// 接口: POST /api/v1/upload/image（multipart，字段名 file）
//
// 参数：
//   - c: gin 上下文
func (h *FulfillmentUploadHandler) UploadImage(c *gin.Context) {
	uc := appcontext.GetUserContext(c)

	limitUploadBody(c)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.Error(c, errcode.ParamError, "缺少文件字段 file 或文件超过 12MB 上限")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		response.Error(c, errcode.ParamError, "读取文件失败")
		return
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		response.Error(c, errcode.ParamError, "读取文件内容失败")
		return
	}

	resp, err := h.productClient.UploadImageLocal(c.Request.Context(), &product.UploadImageLocalReq{
		AdminId:     uc.UserID,
		Filename:    fileHeader.Filename,
		ContentType: fileHeader.Header.Get("Content-Type"),
		Content:     content,
		IsDisplay:   false, // C 端用户上传：签收凭证等，不进入后台附件选择器
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	// url 已由 product 服务按附件配置拼为完整地址，直接透传
	response.Success(c, gin.H{
		"attach_id":   resp.AttachId,
		"url":         resp.Url,
		"remote_path": resp.RemotePath,
		"size":        resp.Size,
		"width":       resp.Width,
		"height":      resp.Height,
	})
}
