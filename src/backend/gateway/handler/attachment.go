package handler

import (
	"io"
	"log"

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

	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.Error(c, errcode.ParamError, "缺少文件字段 file")
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
