package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/channel"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
)

// 渠道管理（系统空间）：client_channels 主数据的后台读写入口。
// 渠道为平台级配置（与社区无关），权限沿用系统配置 settings:view / settings:update

// ClientChannelView 客户端渠道视图对象
// 同时用于 GET 响应与 PUT 请求；AppSecret 为空提交时服务端保留原值；
// PayMethods 为渠道绑定的支付方式列表（有序，顺序即收银台展示顺序），
// 每项为 "cash" 或 "<platform>:<config_id>"（如 "wechat:3"），全量覆盖保存
type ClientChannelView struct {
	ID         int64    `json:"id"`
	Code       string   `json:"code"`
	Name       string   `json:"name"`
	AppID      string   `json:"app_id"`
	AppSecret  string   `json:"app_secret"`
	Enabled    bool     `json:"enabled"`
	PayMethods []string `json:"pay_methods"`
	Configured bool     `json:"configured"`
	UpdatedAt  string   `json:"updated_at"`
}

// GetClientChannels 获取客户端渠道列表（全部，含停用）
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetClientChannels(c *gin.Context) {
	resp, err := h.userClient.ListClientChannels(c.Request.Context(), &user.ListClientChannelsReq{
		OnlyEnabled: false,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	list := make([]ClientChannelView, 0, len(resp.List))
	for _, ch := range resp.List {
		list = append(list, toClientChannelView(ch))
	}
	response.Success(c, gin.H{"channels": list})
}

// UpdateClientChannelRequest 更新客户端渠道请求体
type UpdateClientChannelRequest struct {
	Code       string   `json:"code" binding:"required"`
	Name       string   `json:"name" binding:"required"`
	AppID      string   `json:"app_id"`
	AppSecret  string   `json:"app_secret"`
	Enabled    bool     `json:"enabled"`
	PayMethods []string `json:"pay_methods"`
}

// UpdateClientChannel 更新客户端渠道配置
// AppSecret 提交为空时保留原值；渠道 code 服务端校验一致性，不可变更
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateClientChannel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	var req UpdateClientChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if !channel.IsValid(req.Code) {
		response.Error(c, errcode.ParamError, "未知客户端渠道")
		return
	}

	resp, err := h.userClient.SaveClientChannel(c.Request.Context(), &user.SaveClientChannelReq{
		Id:         id,
		Code:       req.Code,
		Name:       req.Name,
		AppId:      req.AppID,
		AppSecret:  req.AppSecret,
		Enabled:    req.Enabled,
		PayMethods: req.PayMethods,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, toClientChannelView(resp))
}

// toClientChannelView 将 proto 渠道信息转换为后台视图对象
// AppSecret 不回传明文：前端以 configured 标志判断存量凭证，保存时空值由服务端保留原值
//
// 参数：
//   - ch: proto 渠道信息，可为 nil
//
// 返回值：
//   - ClientChannelView: 视图对象
func toClientChannelView(ch *user.ClientChannelInfo) ClientChannelView {
	if ch == nil {
		return ClientChannelView{}
	}
	return ClientChannelView{
		ID:         ch.Id,
		Code:       ch.Code,
		Name:       ch.Name,
		AppID:      ch.AppId,
		AppSecret:  "",
		Enabled:    ch.Enabled,
		PayMethods: ch.PayMethods,
		Configured: ch.Configured,
		UpdatedAt:  ch.UpdatedAt,
	}
}
