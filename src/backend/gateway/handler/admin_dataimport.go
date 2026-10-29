package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/response"
	"github.com/jcbowen/market/src/backend/gateway/middleware"
)

// 数据导入（应用中心 · 数据包）
// 初期仅超管可用：路由级挂权限码 + handler 内 IsSuperAdmin 双闸（与 /accounts 组同一模式）；
// 后续如需下放，定义独立权限码并登记进两端权限目录后去掉双闸即可

// DataImportPreviewReq 预检请求体
type DataImportPreviewReq struct {
	PackageKey      string   `json:"package_key" binding:"required"`
	SelectedKeys    []string `json:"selected_keys"`     // 空 = 全选
	TargetUserPhone string   `json:"target_user_phone"` // 指定导入会员（用户券/购物车落到该会员名下）
}

// DataImportExecuteReq 执行导入请求体
type DataImportExecuteReq struct {
	PackageKey      string   `json:"package_key" binding:"required"`
	SelectedKeys    []string `json:"selected_keys"`     // 空 = 全选
	ExcludedKeys    []string `json:"excluded_keys"`     // 预检后手动排除
	OnSale          bool     `json:"on_sale"`           // 默认 false，前端显式传 true
	TargetUserPhone string   `json:"target_user_phone"` // 指定导入会员（用户券/购物车落到该会员名下）
}

// ListDataPackages 返回可导入的数据包清单（应用中心 · 数据包页）
// 仅超管可用：路由级 product:create 权限码 + 本函数 IsSuperAdmin 双闸；
// 清单按当前管理员所属社区返回（各社区的导入进度独立）
// 参数：
//   - c: gin 上下文（取管理员社区语境）
//
// 返回值：
//   - 无（响应写回 gin）：成功为包列表；非超管回 11005；RPC 失败按 GRPCError 映射业务码
//
// 使用示例：
//
//	GET /api/v1/admin/data-import/packages
func (h *AdminHandler) ListDataPackages(c *gin.Context) {
	if !middleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	resp, err := h.productClient.ListDataPackages(c.Request.Context(), &product.ListDataPackagesReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// PreviewDataImport 导入前预检：解析数据包并与目标社区现状比对，输出新增/冲突/跳过清单
// 只读，不写库；前端凭预检结果让用户勾选要跳过的条目后再调 ImportDataPackage
// 异常：package_key 为空或非 JSON → 10001；非超管 → 11005；包文件缺失/解析失败按服务侧错误码透传
// 参数：
//   - c: gin 上下文，请求体为 DataImportPreviewReq（selected_keys 空=全选；target_user_phone 指定会员归属）
//
// 返回值：
//   - 无（响应写回 gin）：成功为预检报告（含各条目状态与统计）
//
// 使用示例：
//
//	POST /api/v1/admin/data-import/preview
//	{"package_key":"full-demo","selected_keys":[],"target_user_phone":"13800138000"}
func (h *AdminHandler) PreviewDataImport(c *gin.Context) {
	if !middleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	var req DataImportPreviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	resp, err := h.productClient.PreviewDataImport(c.Request.Context(), &product.DataImportPreviewReq{
		CommunityId:     h.getAdminCommunityID(c),
		PackageKey:      req.PackageKey,
		SelectedKeys:    req.SelectedKeys,
		TargetUserPhone: req.TargetUserPhone,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ImportDataPackage 执行导入：把预检选中的条目写入当前社区
// 与 PreviewDataImport 的区别是本函数有写库副作用且耗时较长（逐段导入商品/优惠券等），
// on_sale 为 false 时导入的商品保持下架态，避免演示数据直接对 C 端可见
// 异常：请求体非法 → 10001；非超管 → 11005；包 key 不存在/段校验失败按服务侧错误码透传
// 参数：
//   - c: gin 上下文，请求体为 DataImportExecuteReq（excluded_keys 为预检后手动排除项）
//
// 返回值：
//   - 无（响应写回 gin）：成功为逐段导入结果（成功/失败计数与失败原因清单）
//
// 使用示例：
//
//	POST /api/v1/admin/data-import/import
//	{"package_key":"full-demo","selected_keys":[],"excluded_keys":[],"on_sale":true,"target_user_phone":"13800138000"}
func (h *AdminHandler) ImportDataPackage(c *gin.Context) {
	if !middleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	var req DataImportExecuteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	resp, err := h.productClient.ImportDataPackage(c.Request.Context(), &product.DataImportReq{
		CommunityId:     h.getAdminCommunityID(c),
		PackageKey:      req.PackageKey,
		SelectedKeys:    req.SelectedKeys,
		ExcludedKeys:    req.ExcludedKeys,
		OnSale:          req.OnSale,
		TargetUserPhone: req.TargetUserPhone,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DataCleanupRunReqBody 执行清理请求体
type DataCleanupRunReqBody struct {
	// ConfirmText 强确认文案，必须与服务端约定文案完全一致才执行
	ConfirmText string `json:"confirm_text" binding:"required"`
}

// PreviewDataCleanup 数据清理预检：逐域统计将清理的记录行数与附件文件数（只读）
// 仅超管可用：路由级 product:create 权限码 + 本函数 IsSuperAdmin 双闸（与数据导入同模式）
// 异常：非超管 → 11005；预检失败按服务侧错误码透传
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - 无（响应写回 gin）：成功为各数据域行数/文件数统计
//
// 使用示例：
//
//	GET /api/v1/admin/data-cleanup/preview
func (h *AdminHandler) PreviewDataCleanup(c *gin.Context) {
	if !middleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	resp, err := h.productClient.PreviewDataCleanup(c.Request.Context(), &product.DataCleanupPreviewReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// RunDataCleanup 执行数据清理：一键清空全部业务数据（不可逆，需强确认文案）
// 仅超管可用：路由级 product:create 权限码 + 本函数 IsSuperAdmin 双闸
// 异常：请求体非法 → 10001；非超管 → 11005；确认文案不匹配按服务侧错误码透传
// 参数：
//   - c: gin 上下文，请求体为 DataCleanupRunReqBody
//
// 返回值：
//   - 无（响应写回 gin）：成功为清理统计（域数/行数/文件数）与逐域、逐文件明细
//
// 使用示例：
//
//	POST /api/v1/admin/data-cleanup/run
//	{"confirm_text":"确认清空全部业务数据"}
func (h *AdminHandler) RunDataCleanup(c *gin.Context) {
	if !middleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	var req DataCleanupRunReqBody
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	resp, err := h.productClient.RunDataCleanup(c.Request.Context(), &product.DataCleanupRunReq{
		ConfirmText: req.ConfirmText,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
