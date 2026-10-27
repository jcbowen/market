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

// ListDataPackages 包列表
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

// PreviewDataImport 预检报告
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

// ImportDataPackage 执行导入
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
