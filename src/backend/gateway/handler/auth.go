package handler

import (
	"context"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/config"
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
// 微信小程序登录调用 wego/miniprogram Code2Session 接口换取 openid
// 登录成功后统一签发 access_token 与 refresh_token
type AuthHandler struct {
	jwtManager       *jwt.Manager
	userClient       user.UserServiceClient
	wechatMiniClient *miniprogram.Client
}

// NewAuthHandler 创建 C 端认证处理器
// 若微信小程序配置完整，则自动初始化 wego 小程序客户端
//
// 参数：
//   - jwtManager: JWT 管理器
//   - userClient: user-service gRPC 客户端
//   - wechatCfg: 微信配置
//
// 返回：
//   - *AuthHandler: 认证处理器实例
func NewAuthHandler(jwtManager *jwt.Manager, userClient user.UserServiceClient, wechatCfg config.WechatConfig) *AuthHandler {
	h := &AuthHandler{
		jwtManager: jwtManager,
		userClient: userClient,
	}
	if wechatCfg.IsMiniprogramConfigured() {
		h.wechatMiniClient = miniprogram.NewClient(&miniprogram.Config{
			AppID:     wechatCfg.AppID,
			AppSecret: wechatCfg.AppSecret,
		})
	}
	return h
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
// 当前开发阶段仅输入手机号即可登录/注册，验证码字段仅作兼容保留
type PhoneLoginReq struct {
	Phone string `json:"phone" binding:"required"`
	Code  string `json:"code"`
}

// RefreshTokenReq 刷新 token 请求
type RefreshTokenReq struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// loginResult 统一登录响应结构
// 与前端 LoginResult 接口对应
type loginResult struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token"`
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
// 使用 wego/miniprogram Code2Session 换取 openid，创建或更新用户后签发 token
//
// 参数：
//   - c: gin 上下文
func (h *AuthHandler) WechatLogin(c *gin.Context) {
	var req WechatLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	if h.wechatMiniClient == nil {
		response.Error(c, errcode.InternalServerError, "微信小程序未配置")
		return
	}

	session, err := h.wechatMiniClient.Code2Session(c.Request.Context(), req.Code)
	if err != nil {
		response.Error(c, errcode.InternalServerError, err.Error())
		return
	}

	u, err := h.userClient.CreateOrUpdateUser(c.Request.Context(), &user.CreateUserReq{
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

	h.successResponse(c, u)
}

// SendPhoneCode 发送手机号验证码
// 当前为测试模式，直接返回成功，不调用真实短信服务
//
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
// 当前开发阶段仅校验手机号格式即可登录/注册，不校验验证码
// 根据手机号创建或更新用户后签发 token
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
//
// 参数：
//   - c: gin 上下文
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req RefreshTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	claims, err := h.jwtManager.ParseToken(req.RefreshToken)
	if err != nil {
		response.Error(c, errcode.InvalidToken)
		return
	}
	if claims.Type != jwt.TokenTypeUser {
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

	refreshToken, _, err := h.jwtManager.GenerateRefreshToken(u.Id, jwt.TokenTypeUser)
	if err != nil {
		response.Error(c, errcode.InternalServerError)
		return
	}

	response.Success(c, loginResult{
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
	})
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
