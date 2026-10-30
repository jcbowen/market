package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/response"
	gwMiddleware "github.com/jcbowen/market/src/backend/gateway/middleware"
	"github.com/jcbowen/market/src/backend/gateway/verifyfs"
)

// 平台验证文件（微信域名归属校验）：落盘网关运行目录 verify/，伪静态下发，零查库。
// 容器重建后文件丢失属预期行为（验证文件可随时在后台重新生成），故不持久化到数据库。

// ListVerifyFiles 列出已生成的平台验证文件
// 接口: GET /api/v1/admin/settings/verify-files
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListVerifyFiles(c *gin.Context) {
	list, err := verifyfs.List(verifyfs.DefaultDir())
	if err != nil {
		response.Error(c, errcode.InternalServerError, err.Error())
		return
	}
	items := make([]gin.H, 0, len(list))
	for _, it := range list {
		items = append(items, gin.H{
			"name":      it.Name,
			"base_name": it.BaseName,
			"size":      it.Size,
			"mod_time":  it.ModTime,
			// 对外可访问路径：站点根目录下原样输出，前端按部署域名拼完整 URL
			"path": "/" + it.Name,
		})
	}
	response.Success(c, gin.H{"list": items})
}

// SaveVerifyFile 保存平台验证文件（文件名 + 文本内容），同主体名覆盖更新
// 仅超管；文件名不含后缀（后端强制补 .txt），内容为平台下发的校验文件原文
// 接口: POST /api/v1/admin/settings/verify-files
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) SaveVerifyFile(c *gin.Context) {
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}

	var req struct {
		FileName string `json:"file_name"`
		Content  string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	base, err := verifyfs.ValidateBaseName(req.FileName)
	if err != nil {
		response.Error(c, errcode.ParamError, err.Error())
		return
	}
	if err := verifyfs.Save(verifyfs.DefaultDir(), base, []byte(req.Content)); err != nil {
		response.Error(c, errcode.InternalServerError, err.Error())
		return
	}

	name := verifyfs.FileName(base)
	response.Success(c, gin.H{"name": name, "path": "/" + name})
}

// DeleteVerifyFile 删除平台验证文件
// 仅超管；文件名来自 URL 段，verifyfs 内部白名单校验拒绝一切穿越形态
// 接口: DELETE /api/v1/admin/settings/verify-files/:name
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteVerifyFile(c *gin.Context) {
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}

	err := verifyfs.Delete(verifyfs.DefaultDir(), c.Param("name"))
	if err != nil {
		if errors.Is(err, verifyfs.ErrNotFound) {
			response.Error(c, errcode.NotFound, "验证文件不存在或已删除")
			return
		}
		response.Error(c, errcode.InternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{"success": true})
}
