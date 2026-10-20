package handler

import (
	"context"
	"log"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/channel"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
	"github.com/jcbowen/wego/miniprogram"
)

// phoneOpenidPrefix 手机号登录用户的 openid 占位前缀
// 手机号登录没有微信 openid，用 phone_<手机号> 占位以满足用户表 openid 唯一索引，
// 该值不是微信真实 openid，不可用于微信 JSAPI 支付
const phoneOpenidPrefix = "phone_"

// AuthHandler C 端认证处理器
// 负责微信小程序登录、手机号验证码登录、token 刷新
// 当前手机号登录为测试模式，不调用真实短信服务
// 微信小程序登录凭证取自后台「渠道管理」（client_channels 表，热更），
// 登录成功后按渠道落用户身份（user_channel_identities），并签发 access_token 与 refresh_token
type AuthHandler struct {
	jwtManager *jwt.Manager
	userClient user.UserServiceClient
}

// NewAuthHandler 创建 C 端认证处理器
// 小程序客户端不再启动时构建：每次登录按 DB 渠道配置现取现用（配置变更即时生效，登录频率低无性能压力）
//
// 参数：
//   - jwtManager: JWT 管理器
//   - userClient: user-service gRPC 客户端
//
// 返回：
//   - *AuthHandler: 认证处理器实例
func NewAuthHandler(jwtManager *jwt.Manager, userClient user.UserServiceClient) *AuthHandler {
	return &AuthHandler{
		jwtManager: jwtManager,
		userClient: userClient,
	}
}

// WechatLoginReq 微信小程序登录请求
type WechatLoginReq struct {
	Code string `json:"code" binding:"required"`
}

// PhoneCodeReq 发送手机号验证码请求
type PhoneCodeReq struct {
	Phone string `json:"phone" binding:"required"`
}

// PhoneLoginReq 手机号登录请求
// 当前开发阶段仅输入手机号即可登录/注册，验证码字段收下后从未校验（P0，见 PhoneLogin 注释）
type PhoneLoginReq struct {
	Phone string `json:"phone" binding:"required"`
	Code  string `json:"code"`
}

// RefreshTokenReq 刷新 token 请求
// refresh_token 可来自请求体（小程序/App）或 httpOnly cookie（H5，见 token_cookie.go），
// 故不再 binding:"required"，两者皆空时在 handler 内返回未登录
type RefreshTokenReq struct {
	RefreshToken string `json:"refresh_token"`
}

// loginResult 统一登录响应结构
// 与前端 LoginResult 接口对应
// refresh_token 带 omitempty：前端声明由 httpOnly cookie 承载时不再出现在响应体
type loginResult struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token,omitempty"`
	ExpiresIn    int64      `json:"expires_in"`
	User         *loginUser `json:"user"`
}

// loginUser 登录用户信息
// 用于向前端返回精简的用户资料
type loginUser struct {
	ID            int64  `json:"id"`
	Nickname      string `json:"nickname"`
	Avatar        string `json:"avatar"`
	Phone         string `json:"phone"`
	CommunityID   int64  `json:"community_id"`
	CommunityName string `json:"community_name"`
}

// WechatLogin 微信小程序登录
// 凭证取自渠道管理的 mp 渠道配置，Code2Session 换取 openid，
// 创建或更新用户、落渠道身份后签发 token
//
// 参数：
//   - c: gin 上下文
func (h *AuthHandler) WechatLogin(c *gin.Context) {
	var req WechatLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	ctx := c.Request.Context()
	// 登录凭证取 DB 渠道配置（渠道停用或凭证不完整时拒绝登录）
	chResp, err := h.userClient.GetClientChannel(ctx, &user.GetClientChannelReq{Code: channel.MP})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if chResp == nil || !chResp.Enabled || !chResp.Configured {
		response.Error(c, errcode.InternalServerError, "微信小程序渠道未启用或未配置")
		return
	}

	miniClient := miniprogram.NewClient(&miniprogram.Config{
		AppID:     chResp.AppId,
		AppSecret: chResp.AppSecret,
	})
	session, err := miniClient.Code2Session(ctx, req.Code)
	if err != nil {
		response.Error(c, errcode.InternalServerError, err.Error())
		return
	}

	u, err := h.userClient.CreateOrUpdateUser(ctx, &user.CreateUserReq{
		Openid:   session.OpenID,
		UnionId:  session.UnionID,
		Nickname: "微信用户",
		Avatar:   "",
		Gender:   0,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if u.Status != 1 {
		response.Error(c, errcode.UserBlocked)
		return
	}

	// 落渠道身份（幂等 upsert）；失败不阻断登录，支付侧仍有 users.openid 存量口径兜底
	if _, ierr := h.userClient.UpsertUserChannelIdentity(ctx, &user.UpsertUserChannelIdentityReq{
		UserId:      u.Id,
		ChannelCode: channel.MP,
		Openid:      session.OpenID,
		Unionid:     session.UnionID,
	}); ierr != nil {
		log.Printf("写入用户渠道身份失败 user_id=%d: %v", u.Id, ierr)
	}

	h.successResponse(c, u)
}

// SendPhoneCode 发送手机号验证码
// 【P0 上线门禁】当前为测试模式：不调用真实短信服务，也不落验证码记录，
// 因此 PhoneLogin 无从校验，任何知道手机号的人可直接登录该手机号对应的账号（等于任意账号接管）。
// 本轮按决定不改协议（本机无短信通道，改造需与发码/登录联动），
// 门禁与验证方法见 `docs/需求文档/11-开发计划.md` 的「认证入口上线前置项」；
// 生产环境开启前必须二选一：接入真实短信并在 PhoneLogin 强制校验，或直接下线本入口
// 参数：
//   - c: gin 上下文
func (h *AuthHandler) SendPhoneCode(c *gin.Context) {
	var req PhoneCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if !isValidPhone(req.Phone) {
		response.Error(c, errcode.ParamError, "手机号格式错误")
		return
	}
	response.Success(c, gin.H{"phone": req.Phone, "expire": 300})
}

// PhoneLogin 手机号登录
// 【P0 上线门禁】当前开发阶段仅校验手机号格式即可登录/注册，`req.Code` 收下后从未使用，
// 等价于「凭手机号即可接管任意已注册账号」——可读取他人地址、下单、发起退款。
// 本轮按决定不动本函数逻辑（本机无短信通道，无法端到端验证发码与校验联动），
// 已叠加的缓解仅限：本入口按 IP 的独立限流（见 main.go 的 cAuthLimiter）；
// 门禁项与验证方法见 `docs/需求文档/11-开发计划.md` 的「认证入口上线前置项」，
// 上线前必须实现服务端发码 + 校验（含发码频次、有效期、一次性消费）
//
// 参数：
//   - c: gin 上下文
func (h *AuthHandler) PhoneLogin(c *gin.Context) {
	var req PhoneLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if !isValidPhone(req.Phone) {
		response.Error(c, errcode.ParamError, "手机号格式错误")
		return
	}

	openid := h.phoneOpenid(c.Request.Context(), req.Phone)

	u, err := h.userClient.CreateOrUpdateUser(c.Request.Context(), &user.CreateUserReq{
		Openid: openid,
		Phone:  req.Phone,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if u.Status != 1 {
		response.Error(c, errcode.UserBlocked)
		return
	}

	h.successResponse(c, u)
}

// RefreshToken 使用 refresh_token 换取新的 access_token
// 同时刷新 refresh_token，返回完整登录信息
// 令牌来源：httpOnly cookie（H5）优先，请求体兜底（小程序/App）
//
// 参数：
//   - c: gin 上下文
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req RefreshTokenReq
	// 仅 cookie 承载时请求体可以为空，解析失败不作为错误（下面按空令牌处理）
	_ = c.ShouldBindJSON(&req)

	token := firstNonEmptyRefreshToken(c, req.RefreshToken)
	if token == "" {
		response.Error(c, errcode.NotLoginOrTokenExpired)
		return
	}

	claims, err := h.jwtManager.ParseToken(token)
	if err != nil {
		response.Error(c, errcode.InvalidToken)
		return
	}
	// 强校验 token 用途：仅 refresh token 可刷新，access token 不得用于换取新令牌
	if claims.Type != jwt.TokenTypeUser || claims.TokenUse != jwt.TokenUseRefresh {
		response.Error(c, errcode.NoPermission)
		return
	}

	u, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: claims.UserID})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if u.Status != 1 {
		response.Error(c, errcode.UserBlocked)
		return
	}

	h.successResponse(c, u)
}

// phoneOpenid 根据手机号获取或生成稳定的 openid 标识
// 若用户已存在且已有 openid，则沿用；否则生成 phone_<手机号>
// 用于绕过用户表 openid 唯一索引对空字符串的限制
//
// 参数：
//   - ctx: 上下文
//   - phone: 手机号
//
// 返回：
//   - string: openid 标识
func (h *AuthHandler) phoneOpenid(ctx context.Context, phone string) string {
	existing, err := h.userClient.GetUserByPhone(ctx, &user.GetUserByPhoneReq{Phone: phone})
	if err == nil && existing != nil && existing.Openid != "" {
		return existing.Openid
	}
	return phoneOpenidPrefix + phone
}

// successResponse 登录成功后统一返回 token 与用户信息
// 前端带 X-Refresh-Cookie: 1 时，refresh_token 改由 httpOnly cookie 下发、响应体不再返回
//
// 参数：
//   - c: gin 上下文
//   - u: user-service 返回的用户信息
func (h *AuthHandler) successResponse(c *gin.Context, u *user.UserInfo) {
	accessToken, accessExpire, err := h.jwtManager.GenerateAccessToken(
		u.Id, jwt.TokenTypeUser, u.Phone, "", u.CommunityId, nil,
	)
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}

	refreshToken, refreshExpire, err := h.jwtManager.GenerateRefreshToken(u.Id, jwt.TokenTypeUser)
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}

	result := loginResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(time.Until(accessExpire).Seconds()),
		User: &loginUser{
			ID:            u.Id,
			Nickname:      u.Nickname,
			Avatar:        u.Avatar,
			Phone:         u.Phone,
			CommunityID:   u.CommunityId,
			CommunityName: "",
		},
	}
	if wantsRefreshCookie(c) {
		setRefreshTokenCookie(c, refreshToken, int64(time.Until(refreshExpire).Seconds()))
		// 令牌只留在 cookie，响应体清空避免 JS 侧再拿到一份
		result.RefreshToken = ""
	}

	response.Success(c, result)
}

// Logout C 端登出：清除 refresh_token 的 httpOnly cookie
// 本地令牌清理由前端完成，本端点只负责 JS 清不掉的那一份
//
// 参数：
//   - c: gin 上下文
func (h *AuthHandler) Logout(c *gin.Context) {
	clearRefreshTokenCookie(c)
	response.Success(c, gin.H{})
}

// isValidPhone 校验中国大陆手机号格式
//
// 参数：
//   - phone: 手机号字符串
//
// 返回：
//   - bool: 是否合法
func isValidPhone(phone string) bool {
	matched, _ := regexp.MatchString(`^1[3-9]\d{9}$`, phone)
	return matched
}
