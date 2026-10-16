package handler

import (
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/response"
)

// AttachmentHandler 附件接口处理器
// 封装附件配置读写、本地模式图片上传与 OSS 模式预签名直传（presign/confirm）
type AttachmentHandler struct {
	productClient product.ProductServiceClient
}

// NewAttachmentHandler 创建附件接口处理器
//
// 参数:
//   - productClient: product-service gRPC 客户端
//
// 返回值:
//   - *AttachmentHandler: 附件处理器实例
func NewAttachmentHandler(productClient product.ProductServiceClient) *AttachmentHandler {
	return &AttachmentHandler{productClient: productClient}
}

// GetAttachmentSetting 获取附件配置
// AccessKeySecret 已由服务端脱敏，仅返回掩码
//
// 接口: GET /api/v1/admin/settings/attachment
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) GetAttachmentSetting(c *gin.Context) {
	resp, err := h.productClient.GetAttachmentConfig(c.Request.Context(), &common.Empty{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateAttachmentSetting 更新附件配置
// Secret 为空或掩码值时服务端保留原值，配置保存后各实例按版本号自动热更新
//
// 接口: PUT /api/v1/admin/settings/attachment
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) UpdateAttachmentSetting(c *gin.Context) {
	var req product.AttachmentConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	if _, err := h.productClient.SaveAttachmentConfig(c.Request.Context(), &req); err != nil {
		log.Printf("保存附件设置失败: %v", err)
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

// maxUploadBytes 上传请求体大小上限（12MB）
const maxUploadBytes = 12 << 20

// limitUploadBody 限制上传请求体大小，防止超大 multipart 撑爆网关内存
// 超限时后续 FormFile/PostForm 解析返回错误，由调用方统一按参数错误响应
// 参数：
//   - c: gin 上下文
func limitUploadBody(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
}

// UploadImage 本地模式图片上传
// 接收 multipart 文件后转发给 product 服务落盘并写附件记录；
// 本地模式访问 URL 由本方法按请求域名动态拼接（<scheme>://<host>/uploads/<path>）
//
// 接口: POST /api/v1/admin/upload/image（multipart，字段名 file）
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) UploadImage(c *gin.Context) {
	adminID := appcontext.GetUserContext(c).UserID

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
		AdminId:     adminID,
		Filename:    fileHeader.Filename,
		ContentType: fileHeader.Header.Get("Content-Type"),
		Content:     content,
		IsDisplay:   true, // 后台管理员上传：进入附件选择器；客户端用户上传默认 false 不进入
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 本地模式 url 为空，按当前请求域名动态拼接，域名变更无需迁移记录
	url := resp.Url
	if url == "" && resp.RemotePath != "" {
		url = baseURLOf(c) + "/uploads/" + resp.RemotePath
	}

	response.Success(c, gin.H{
		"attach_id":   resp.AttachId,
		"url":         url,
		"remote_path": resp.RemotePath,
		"size":        resp.Size,
		"width":       resp.Width,
		"height":      resp.Height,
	})
}

// AttachmentPresign 获取预签名上传 URL（OSS 直传模式）
// hit=true 表示秒传命中，直接使用 final_url；否则用 url + headers 以 PUT 直传 OSS 后调 confirm
//
// 接口: POST /api/v1/admin/attachment/presign
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) AttachmentPresign(c *gin.Context) {
	var req product.AttachmentPresignReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	req.AdminId = appcontext.GetUserContext(c).UserID
	req.IsDisplay = true // 后台管理员上传：进入附件选择器；客户端用户上传默认 false 不进入

	resp, err := h.productClient.AttachmentPresign(c.Request.Context(), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// OSS 模式 final_url 已是完整地址，直接透传
	response.Success(c, resp)
}

// AttachmentConfirm 确认上传完成
// 校验归属与 md5 格式后补写附件记录，激活秒传
//
// 接口: POST /api/v1/admin/attachment/confirm
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) AttachmentConfirm(c *gin.Context) {
	var req product.AttachmentConfirmReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	req.AdminId = appcontext.GetUserContext(c).UserID

	resp, err := h.productClient.AttachmentConfirm(c.Request.Context(), &req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	response.Success(c, gin.H{
		"attach_id":   resp.AttachId,
		"url":         resp.Url,
		"remote_path": resp.RemotePath,
		"size":        resp.Size,
		"width":       resp.Width,
		"height":      resp.Height,
	})
}

// ListAttachments 附件选择器分页列表
// 数据按上传管理员隔离：admin_id 取登录态注入，服务端仅返回本人上传且 is_display=1 的图片附件
//
// 接口: GET /api/v1/admin/attachment/list?keyword=&start_time=&end_time=&page=&page_size=
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) ListAttachments(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "12"))

	resp, err := h.productClient.ListAttachments(c.Request.Context(), &product.ListAttachmentsReq{
		AdminId:   appcontext.GetUserContext(c).UserID,
		Keyword:   c.Query("keyword"),
		StartTime: c.Query("start_time"),
		EndTime:   c.Query("end_time"),
		Page:      int32(page),
		PageSize:  int32(pageSize),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// baseURLOf 根据当前请求拼接基础地址
// 优先取反向代理透传的 X-Forwarded-Proto，兼容 HTTPS 部署场景
//
// 参数:
//   - c: gin 上下文
//
// 返回值:
//   - string: 形如 http(s)://host 的基础地址
func baseURLOf(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + c.Request.Host
}

// --- 附件迁移（本地 → OSS） ---

// ScanAttachmentMigrate 扫描本地附件目录与附件表匹配，生成/刷新迁移任务清单
//
// 接口: POST /api/v1/admin/attachment/migrate/scan
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) ScanAttachmentMigrate(c *gin.Context) {
	resp, err := h.productClient.ScanAttachmentMigrate(c.Request.Context(), &common.Empty{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListAttachmentMigrate 迁移条目分页列表（含概览统计）
//
// 接口: GET /api/v1/admin/attachment/migrate/items?status=&page=&page_size=
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) ListAttachmentMigrate(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.productClient.ListAttachmentMigrate(c.Request.Context(), &product.AttachmentMigrateListReq{
		Status:     c.Query("status"),
		Pagination: &common.Pagination{Page: int32(page), PageSize: int32(pageSize)},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// MigrateAttachmentItem 迁移单个条目（上传 OSS 后自动验证）
//
// 接口: POST /api/v1/admin/attachment/migrate/:id/migrate
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) MigrateAttachmentItem(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError, "条目 ID 不合法")
		return
	}

	resp, err := h.productClient.MigrateAttachmentItem(c.Request.Context(), &product.AttachmentMigrateItemReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// VerifyAttachmentItem 验证单个条目（OSS 对象存在 + 大小一致）
//
// 接口: POST /api/v1/admin/attachment/migrate/:id/verify
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) VerifyAttachmentItem(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError, "条目 ID 不合法")
		return
	}

	resp, err := h.productClient.VerifyAttachmentItem(c.Request.Context(), &product.AttachmentMigrateItemReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteAttachmentLocal 删除单个条目的本地文件（仅验证成功条目）
//
// 接口: POST /api/v1/admin/attachment/migrate/:id/delete-local
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) DeleteAttachmentLocal(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError, "条目 ID 不合法")
		return
	}

	resp, err := h.productClient.DeleteAttachmentLocal(c.Request.Context(), &product.AttachmentMigrateItemReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteAttachmentLocalVerified 一键删除全部已验证条目的本地文件
//
// 接口: POST /api/v1/admin/attachment/migrate/delete-verified
//
// 参数:
//   - c: gin 上下文
func (h *AttachmentHandler) DeleteAttachmentLocalVerified(c *gin.Context) {
	resp, err := h.productClient.DeleteAttachmentLocalVerified(c.Request.Context(), &common.Empty{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}
