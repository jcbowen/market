package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/channel"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
	"github.com/jcbowen/market/src/backend/gateway/brand"
	gwMiddleware "github.com/jcbowen/market/src/backend/gateway/middleware"
)

// AdminHandler 管理员接口处理器
// 封装管理员登录、管理员 CRUD、角色查询、商品/订单/库存/营销/社区/退款/支付配置等管理端 HTTP 接口
type AdminHandler struct {
	jwtManager    *jwt.Manager
	userClient    user.UserServiceClient
	productClient product.ProductServiceClient
	orderClient   order.OrderServiceClient
	paymentClient payment.PaymentServiceClient
	cfg           *config.Config
	// guard 登录失败锁定（D1）：按账号计数，防分布式慢速猜密码
	guard *loginGuard
	// refreshGuard refresh 令牌黑名单（P1-5）：轮转吊销旧令牌，停用/登出即时生效
	refreshGuard *refreshGuard
	// pwdCrypto 密码传输加密器：RSA 私钥解密前端密文（见 password_crypto.go 协议约定）
	pwdCrypto *passwordCrypto
}

// NewAdminHandler 创建管理员接口处理器
// 参数：
//   - jwtManager: JWT 管理器，用于签发 token
//   - userClient: user-service gRPC 客户端
//   - productClient: product-service gRPC 客户端
//   - orderClient: order-service gRPC 客户端
//   - paymentClient: payment-service gRPC 客户端，用于支付配置读写
//   - cfg: 全局配置指针
//
// 返回值：
//   - *AdminHandler: 管理员处理器实例
func NewAdminHandler(jwtManager *jwt.Manager, userClient user.UserServiceClient, productClient product.ProductServiceClient, orderClient order.OrderServiceClient, paymentClient payment.PaymentServiceClient, cfg *config.Config) *AdminHandler {
	return &AdminHandler{
		jwtManager:    jwtManager,
		userClient:    userClient,
		productClient: productClient,
		orderClient:   orderClient,
		paymentClient: paymentClient,
		cfg:           cfg,
		guard:         newLoginGuard(cfg),
		refreshGuard:  newRefreshGuard(cfg),
		pwdCrypto:     newPasswordCrypto(cfg),
	}
}

// AdminLoginReq 管理员登录请求
// 包含用户名与密码字段
type AdminLoginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	// 密文传输声明（可省略 = 明文兼容）：声明任一即视为 Password 为 RSA-OAEP-SHA256 base64 密文，
	// 协议详见 password_crypto.go；下同（其余密码请求结构体的同名字段不再重复注释）
	PwdAlg string `json:"pwd_alg"`
	PwdKey string `json:"pwd_key"`
}

// AdminLoginResp 管理员登录响应
// 包含双 token、过期时间及管理员基本信息
// refresh_token 带 omitempty：前端声明由 httpOnly cookie 承载时不再出现在响应体
type AdminLoginResp struct {
	AccessToken      string      `json:"access_token"`
	RefreshToken     string      `json:"refresh_token,omitempty"`
	ExpiresIn        int64       `json:"expires_in"`
	RefreshExpiresIn int64       `json:"refresh_expires_in"`
	ExpiresAt        int64       `json:"expires_at"`
	RefreshExpiresAt int64       `json:"refresh_expires_at"`
	Admin            interface{} `json:"admin"`
}

// Login 管理员登录
// 校验用户名密码，校验通过后签发 access_token 与 refresh_token
// 防护（D1）：连续失败达上限按账号锁定一段时间（计数存 Redis，跨 IP 有效）；
// 「账号不存在」「密码错误」「token 无效」一律回同一句「用户名或密码错误」，消除账号枚举；
// 禁用状态只在密码正确后才提示，避免未登录者据此探测账号是否存在；
// 账号查询只有 NotFound 计入失败锁定，其他状态码按内部错误返回（避免服务抖动误锁正常账号）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) Login(c *gin.Context) {
	var req AdminLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	// 密文传输：按 pwd_alg/pwd_key 声明解密密码（未声明走明文兼容），失败已写响应
	if !h.decryptPwdField(c, &req.Password, req.PwdAlg, req.PwdKey) {
		return
	}

	ctx := c.Request.Context()
	if ttl, locked := h.guard.lockedRemaining(ctx, loginSceneAdmin, req.Username); locked {
		response.Error(c, errcode.LoginLocked, loginLockedMessage(ttl))
		return
	}

	admin, err := h.userClient.GetAdminByUsername(ctx, &user.GetAdminByUsernameReq{Username: req.Username})
	if err != nil {
		// 只有「账号不存在」算凭证失败、计入锁定；服务不可用等故障不得污染计数，
		// 否则 user-service 抖动或数据库故障会把正常管理员连带锁在门外
		if status.Code(err) != codes.NotFound {
			log.Printf("[WARN] 管理员登录查询账号失败 username=%s: %v", maskAccount(req.Username), err)
			response.Error(c, errcode.InternalServerError)
			return
		}
		h.guard.recordFailure(ctx, loginSceneAdmin, req.Username)
		response.Error(c, errcode.IncorrectUsernameOrPassword)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(req.Password)); err != nil {
		h.guard.recordFailure(ctx, loginSceneAdmin, req.Username)
		response.Error(c, errcode.IncorrectUsernameOrPassword)
		return
	}
	if admin.Status != 1 {
		response.Error(c, errcode.AccountDisabled)
		return
	}
	h.guard.clearFailures(ctx, loginSceneAdmin, req.Username)

	// 权限按默认社区实时解析（多社区模型：不同社区权限不同，JWT 不再承载权限快照）
	communityID := h.getAdminDefaultCommunityID(admin)
	permissions := h.getAdminPermissionsForCommunity(c.Request.Context(), admin.Id, communityID)
	roleCode := "admin"
	if admin.IsSuper {
		roleCode = "super_admin"
	}
	accessToken, accessExpire, err := h.jwtManager.GenerateAdminAccessToken(
		admin.Id, admin.Phone, roleCode, communityID, admin.CommunityIds, nil, admin.Username, admin.RealName,
	)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	refreshToken, refreshExpire, err := h.jwtManager.GenerateRefreshToken(admin.Id, jwt.TokenTypeAdmin)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	now := time.Now()
	resp := AdminLoginResp{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		ExpiresIn:        int64(accessExpire.Sub(now).Seconds()),
		RefreshExpiresIn: int64(refreshExpire.Sub(now).Seconds()),
		ExpiresAt:        accessExpire.Unix(),
		RefreshExpiresAt: refreshExpire.Unix(),
		Admin: gin.H{
			"id":            admin.Id,
			"username":      admin.Username,
			"real_name":     admin.RealName,
			"phone":         admin.Phone,
			"is_super":      admin.IsSuper,
			"role_code":     roleCode,
			"community_id":  communityID,
			"community_ids": admin.CommunityIds,
			"permissions":   permissions,
		},
	}
	// 新版后台/H5 前端带 X-Refresh-Cookie: 1，refresh_token 改由 httpOnly cookie 下发，
	// 响应体清空；未带该头（旧前端缓存、POS 未更新）时保持原行为，避免登录直接不可用。
	// 同一声明下顺带下发 access_token 的 httpOnly cookie（WS 握手承载用）：
	// 浏览器 WebSocket 无法自定义请求头，凭 cookie 鉴权可让握手 URL 不再明文携带令牌
	if wantsRefreshCookie(c) {
		setRefreshTokenCookie(c, refreshToken, int64(refreshExpire.Sub(now).Seconds()))
		setAccessTokenCookie(c, accessToken, int64(accessExpire.Sub(now).Seconds()))
		resp.RefreshToken = ""
	}
	response.Success(c, resp)
}

// AdminLogout 后台登出：清除 refresh_token 与 access_token 的 httpOnly cookie 并吊销 refresh 令牌
// 前端本地 access_token 由 JS 自行删除，JS 删不掉的 cookie 必须由后端下发过期值清除；
// 吊销黑名单（P1-5）让服务端也不再认这枚 refresh 令牌——否则登出只是前端表演，
// 令牌在剩余有效期内仍可刷新出新 access_token
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) AdminLogout(c *gin.Context) {
	clearRefreshTokenCookie(c)
	clearAccessTokenCookie(c)
	// 吊销当前 refresh 令牌：解析失败（过期/伪造）时无需处理，令牌本就不可用
	if token := firstNonEmptyRefreshToken(c, ""); token != "" {
		if claims, err := h.jwtManager.ParseToken(token); err == nil &&
			claims.Type == jwt.TokenTypeAdmin && claims.TokenUse == jwt.TokenUseRefresh {
			h.refreshGuard.revoke(c.Request.Context(), claims.ID, time.Until(claims.ExpiresAt.Time))
		}
	}
	response.Success(c, gin.H{})
}

// Info 获取当前登录管理员信息
// 供前端在刷新页面（store 状态重置）后恢复管理员资料与权限列表；
// 管理员基础资料通过 token 中的 username 反查 user 服务获取（含最新任职社区列表），
// 权限列表按当前社区语境（X-Community-Id）实时解析，与接口鉴权口径保持一致
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) Info(c *gin.Context) {
	uc := appcontext.GetUserContext(c)

	// 当前社区语境：优先请求头声明，缺省回退 JWT 默认社区
	communityID := uc.CommunityID
	if id, err := strconv.ParseInt(c.GetHeader("X-Community-Id"), 10, 64); err == nil && id > 0 {
		communityID = id
	}
	permissions := h.getAdminPermissionsForCommunity(c.Request.Context(), uc.UserID, communityID)

	// 角色编码取实时口径：前端据此决定系统管理菜单是否出现，
	// 照抄登录快照会让降权/停用前的超管在令牌剩余有效期内继续看到入口
	roleCode := "admin"
	if gwMiddleware.IsSuperAdmin(c) {
		roleCode = "super_admin"
	}

	var admin *user.AdminInfo
	if uc.Phone != "" || uc.UserID > 0 {
		// token 携带 username 时按用户名反查完整资料（含最新 community_ids）
		if username, ok := c.Get("admin_username"); ok {
			if name, _ := username.(string); name != "" {
				if a, err := h.userClient.GetAdminByUsername(c.Request.Context(), &user.GetAdminByUsernameReq{Username: name}); err == nil {
					admin = a
					if a.IsSuper {
						roleCode = "super_admin"
					}
				}
			}
		}
	}

	resp := gin.H{
		"id":          uc.UserID,
		"phone":       uc.Phone,
		"is_super":    roleCode == "super_admin",
		"role_code":   roleCode,
		"permissions": permissions,
	}
	if admin != nil {
		resp["id"] = admin.Id
		resp["username"] = admin.Username
		resp["real_name"] = admin.RealName
		resp["phone"] = admin.Phone
		resp["is_super"] = admin.IsSuper
		resp["role_code"] = roleCode
		resp["community_id"] = h.getAdminDefaultCommunityID(admin)
		communityIDs := admin.CommunityIds
		if communityIDs == nil {
			communityIDs = []int64{}
		}
		resp["community_ids"] = communityIDs
		resp["status"] = admin.Status
	}

	response.Success(c, resp)
}

// getAdminDefaultCommunityID 获取管理员默认社区 ID
// 优先使用关联社区列表中的第一个，否则回退到原 community_id 字段
//
// 参数：
//   - admin: 管理员信息
//
// 返回值：
//   - int64: 默认社区 ID
func (h *AdminHandler) getAdminDefaultCommunityID(admin *user.AdminInfo) int64 {
	if len(admin.CommunityIds) > 0 {
		return admin.CommunityIds[0]
	}
	return admin.CommunityId
}

// getAdminPermissionsForCommunity 按社区语境实时解析管理员权限（任职角色权限并集）
// 参数：
//   - ctx: 上下文
//   - adminID: 管理员 ID
//   - communityID: 社区 ID
//
// 返回值：
//   - []string: 权限编码列表
func (h *AdminHandler) getAdminPermissionsForCommunity(ctx context.Context, adminID, communityID int64) []string {
	resp, err := h.userClient.GetAdminContext(ctx, &user.GetAdminContextReq{
		AdminId:     adminID,
		CommunityId: communityID,
	})
	if err != nil || resp == nil {
		return []string{}
	}
	// proto 空 repeated 反序列化后为 nil，统一输出空数组避免 JSON null
	if resp.Permissions == nil {
		return []string{}
	}
	return resp.Permissions
}

// getAdminCommunityID 获取管理员当前社区 ID
// 前端通过请求头 X-Community-Id 声明当前操作的社区（与业务筛选参数 community_id 解耦）：
//   - 超管：任意社区 ID 均合法（含未绑定社区）
//   - 普通管理员：仅允许切换到自己绑定的社区，越权值静默回退默认社区
//   - 未携带或值非法时回退 JWT 中的默认社区
//
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - int64: 社区 ID
//
// getAdminCommunityID 解析当前请求的数据归属社区（管理端查询的统一作用域）
// 优先级：X-Community-Id 请求头（实时校验归属后才采用）→ JWT 默认社区快照
// 实时校验（P1-10）：JWT 快照的 CommunityIDs 绘于登录时，任职被撤销/追加后需重登才刷新，
// 不能再作为归属判定依据——与 RequireCommunityAccess 中间件同源（复用请求内记忆化的
// GetAdminContext 结果，不增加 RPC）；解析失败时回退默认社区，真正的越权拒绝
// 由 RequireCommunityAccess/RequirePermission 中间件负责
//
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - int64: 数据归属社区 ID
func (h *AdminHandler) getAdminCommunityID(c *gin.Context) int64 {
	uc := appcontext.GetUserContext(c)
	if header := c.GetHeader(gwMiddleware.HeaderCommunityID); header != "" {
		if id, err := strconv.ParseInt(header, 10, 64); err == nil && id > 0 {
			// 实时口径：超管或该社区仍有任职（Bound）才采用请求头社区；
			// 已失去任职的社区（快照里可能还在）不得作为数据作用域
			if gwMiddleware.AdminCommunityAccessible(c) {
				return id
			}
		}
	}
	return uc.CommunityID
}

// checkOrderCommunity 校验订单是否落在当前管理员可操作的社区内（超管不受限）
// 社区归属判定的唯一实现，供「只拦截」与「拦截后还要用订单」的两类入口共用，避免两处各写一份
// 参数：
//   - c: gin 上下文
//   - o: 已取到的订单信息，nil 视为订单不存在
//
// 返回值：
//   - error: 订单不存在或跨社区时返回错误
func (h *AdminHandler) checkOrderCommunity(c *gin.Context, o *order.OrderInfo) error {
	if o == nil || o.Id == 0 {
		return errors.New("订单不存在")
	}
	if gwMiddleware.IsSuperAdmin(c) {
		return nil
	}
	if o.CommunityId != h.getAdminCommunityID(c) {
		return errors.New("无权操作其他社区的订单")
	}
	return nil
}

// orderInCommunity 读取订单并做社区归属校验，返回订单信息
// 「先读订单状态再决定动作」的接口用它（同步支付状态、人工标记已支付都要看订单当前状态），
// 只做拦截的入口继续用 ensureOrderInCommunity——后者对超管免查订单，不必为校验多一次 RPC
//
// 参数：
//   - c: gin 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - *order.OrderInfo: 订单信息
//   - error: 订单不存在或跨社区时返回错误
func (h *AdminHandler) orderInCommunity(c *gin.Context, orderID int64) (*order.OrderInfo, error) {
	resp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: orderID})
	if err != nil || resp == nil {
		return nil, errors.New("订单不存在")
	}
	if err := h.checkOrderCommunity(c, resp.Order); err != nil {
		return nil, err
	}
	return resp.Order, nil
}

// ensureOrderInCommunity 管理端订单操作前的社区归属校验（多社区隔离）
// 超管跨社区操作不受限；普通管理员仅可操作自身社区订单。
// 网关层第一道防线，order 服务端对出款链路另有强校验（纵深防御）
// 参数：
//   - c: gin 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - error: 订单不存在或跨社区时返回错误
func (h *AdminHandler) ensureOrderInCommunity(c *gin.Context, orderID int64) error {
	// 超管跨社区均合法，无需为校验多查一次订单
	if gwMiddleware.IsSuperAdmin(c) {
		return nil
	}
	resp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: orderID})
	if err != nil || resp == nil || resp.Order == nil {
		return errors.New("订单不存在")
	}
	return h.checkOrderCommunity(c, resp.Order)
}

// scopeCommunityID 返回下发给下游服务做归属校验的社区 ID
// 服务端各按 ID 操作的 RPC 采用「CommunityId>0 才校验」的约定（同 order 退款族），
// 超管跨社区操作时返回 0 表示放行，普通管理员返回其当前操作社区
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - int64: 用于社区校验的社区 ID，0 表示跳过
func (h *AdminHandler) scopeCommunityID(c *gin.Context) int64 {
	if gwMiddleware.IsSuperAdmin(c) {
		return 0
	}
	return h.getAdminCommunityID(c)
}

// containsInt64 判断 int64 切片是否包含指定元素
// 参数：
//   - list: int64 切片
//   - item: 目标元素
//
// 返回值：
//   - bool: 是否包含
func containsInt64(list []int64, item int64) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

// ListAdmins 获取员工列表（当前社区语境下的任职员工）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdmins(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	keyword := c.Query("keyword")

	resp, err := h.userClient.ListAdmins(c.Request.Context(), &user.ListAdminsReq{
		CommunityId: h.getAdminCommunityID(c),
		Keyword:     keyword,
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

// CreateAdminReq 创建员工请求
// username 已存在时复用该账号（不修改其密码），仅追加当前社区任职
type CreateAdminReq struct {
	Username string  `json:"username" binding:"required"`
	Password string  `json:"password"`
	RealName string  `json:"real_name"`
	Phone    string  `json:"phone"`
	RoleIDs  []int64 `json:"role_ids" binding:"required,min=1"`
	// 创建时绑定的会员 ID（0=不绑定；绑定后该会员在任职社区即履约人员）
	UserID int64 `json:"user_id"`
	// 密文传输声明（语义同 AdminLoginReq）；Password 可不传（复用账号），声明字段与密码独立校验
	PwdAlg string `json:"pwd_alg"`
	PwdKey string `json:"pwd_key"`
}

// CreateAdmin 创建员工（在当前社区任职）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateAdmin(c *gin.Context) {
	var req CreateAdminReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if !h.decryptPwdField(c, &req.Password, req.PwdAlg, req.PwdKey) {
		return
	}

	admin, err := h.userClient.CreateAdmin(c.Request.Context(), &user.CreateAdminReq{
		Username:        req.Username,
		Password:        req.Password,
		RealName:        req.RealName,
		Phone:           req.Phone,
		CommunityId:     h.getAdminCommunityID(c),
		RoleIds:         req.RoleIDs,
		UserId:          req.UserID,
		OperatorIsSuper: gwMiddleware.IsSuperAdmin(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, h.toAdminVO(admin))
}

// UpdateAdminReq 更新员工请求
type UpdateAdminReq struct {
	RealName string  `json:"real_name"`
	Phone    string  `json:"phone"`
	Status   int32   `json:"status"`
	RoleIDs  []int64 `json:"role_ids"`
	// 绑定的会员 ID（三态：未传=不改动；0=解绑；>0=绑定/换绑）
	UserID *int64 `json:"user_id"`
}

// UpdateAdmin 更新员工
// 仅超管可修改任意员工（含角色任职）；普通管理员仅可修改自己的基础资料
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateAdmin(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	ctx := appcontext.GetUserContext(c)
	// 超管判定取实时口径：本接口普通管理员也能改自己，若照抄登录快照，
	// 被降权的人在任何拒绝生效前都能带着 role_ids 调一次 PUT 把角色改回去（自恢复权限）
	isSuper := gwMiddleware.IsSuperAdmin(c)
	if !isSuper && ctx.UserID != id {
		response.Error(c, errcode.NoPermission)
		return
	}

	var req UpdateAdminReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 普通管理员改自己资料时不允许动角色任职与会员绑定
	var roleIDs []int64
	var userID *int64
	if isSuper {
		roleIDs = req.RoleIDs
		userID = req.UserID
	}

	admin, err := h.userClient.UpdateAdmin(c.Request.Context(), &user.UpdateAdminReq{
		Id:              id,
		RealName:        req.RealName,
		Phone:           req.Phone,
		Status:          req.Status,
		CommunityId:     h.getAdminCommunityID(c),
		RoleIds:         roleIDs,
		UserId:          userID,
		OperatorIsSuper: isSuper,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, h.toAdminVO(admin))
}

// DeleteAdmin 移除员工（仅移除当前社区任职，账号与其他社区任职保留）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteAdmin(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.userClient.DeleteAdmin(c.Request.Context(), &user.DeleteAdminReq{
		Id:          id,
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// ChangeAdminPasswordReq 修改管理员密码请求
type ChangeAdminPasswordReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
	// 密文传输声明：两个密码字段同批加密上送（语义同 AdminLoginReq）
	PwdAlg string `json:"pwd_alg"`
	PwdKey string `json:"pwd_key"`
}

// ChangeAdminPassword 修改管理员密码
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ChangeAdminPassword(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req ChangeAdminPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	// 密文传输：新旧密码同批解密
	if !h.decryptPwdField(c, &req.OldPassword, req.PwdAlg, req.PwdKey) ||
		!h.decryptPwdField(c, &req.NewPassword, req.PwdAlg, req.PwdKey) {
		return
	}

	_, err = h.userClient.ChangeAdminPassword(c.Request.Context(), &user.ChangeAdminPasswordReq{
		Id:          id,
		OldPassword: req.OldPassword,
		NewPassword: req.NewPassword,
	})
	if err != nil {
		if grpcErr, ok := status.FromError(err); ok && grpcErr.Code() == codes.InvalidArgument {
			response.Error(c, errcode.ParamError, grpcErr.Message())
			return
		}
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// adminHoldsRole 判断管理员是否在指定社区担任指定角色
// 参数：
//   - ctx: 上下文
//   - adminID: 管理员 ID
//   - communityID: 社区 ID
//   - roleID: 角色 ID
//
// 返回值：
//   - bool: 是否担任
func (h *AdminHandler) adminHoldsRole(ctx context.Context, adminID, communityID, roleID int64) bool {
	resp, err := h.userClient.GetAdminContext(ctx, &user.GetAdminContextReq{
		AdminId:     adminID,
		CommunityId: communityID,
	})
	if err != nil || resp == nil {
		return false
	}
	for _, rid := range resp.RoleIds {
		if rid == roleID {
			return true
		}
	}
	return false
}

// ListAllAccountsReq 全局账号列表查询参数
type ListAllAccountsReq struct{}

// ListAllAccounts 全局账号列表（超管专用，系统管理·账号管理页）
// community_id = 0 查询全部账号，不限定社区任职
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAllAccounts(c *gin.Context) {
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}

	page, pageSize := parsePageParams(c)
	keyword := c.Query("keyword")

	resp, err := h.userClient.ListAdmins(c.Request.Context(), &user.ListAdminsReq{
		CommunityId: 0,
		Keyword:     keyword,
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	list := make([]gin.H, 0, len(resp.List))
	for _, a := range resp.List {
		communityIDs := a.CommunityIds
		if communityIDs == nil {
			communityIDs = []int64{}
		}
		list = append(list, gin.H{
			"id":            a.Id,
			"username":      a.Username,
			"real_name":     a.RealName,
			"phone":         a.Phone,
			"is_super":      a.IsSuper,
			"status":        a.Status,
			"last_login_at": a.LastLoginAt,
			"community_ids": communityIDs,
		})
	}
	response.SuccessWithPage(c, list, int(resp.Pagination.Total), page, pageSize)
}

// CreateAccountReq 全局新增账号请求（仅建账号，不在任何社区任职）
type CreateAccountReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,min=6"`
	RealName string `json:"real_name"`
	Phone    string `json:"phone"`
	// 密文传输声明（语义同 AdminLoginReq）
	PwdAlg string `json:"pwd_alg"`
	PwdKey string `json:"pwd_key"`
}

// CreateAccount 新增账号（超管专用）
// 仅创建账号本身；任职由各社区员工管理页分配
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateAccount(c *gin.Context) {
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}

	var req CreateAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if !h.decryptPwdField(c, &req.Password, req.PwdAlg, req.PwdKey) {
		return
	}

	admin, err := h.userClient.CreateAdmin(c.Request.Context(), &user.CreateAdminReq{
		Username:        req.Username,
		Password:        req.Password,
		RealName:        req.RealName,
		Phone:           req.Phone,
		AccountOnly:     true,
		OperatorIsSuper: true,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, h.toAdminVO(admin))
}

// UpdateAccountStatusReq 账号停用/启用请求
type UpdateAccountStatusReq struct {
	// 0 停用 1 启用（指针绑定：0 为合法值，不能用 required 校验）
	Status *int32 `json:"status" binding:"required"`
}

// UpdateAccountStatus 停用/启用账号（超管专用）
// 仅更新状态，不触碰姓名/手机号/角色任职
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateAccountStatus(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateAccountStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if req.Status == nil || (*req.Status != 0 && *req.Status != 1) {
		response.Error(c, errcode.ParamError, "状态取值非法")
		return
	}
	if uc.UserID == id && *req.Status == 0 {
		response.Error(c, errcode.OperationNotAllowed, "不能停用自己的账号")
		return
	}

	admin, err := h.userClient.UpdateAdmin(c.Request.Context(), &user.UpdateAdminReq{
		Id:               id,
		Status:           *req.Status,
		UpdateStatusOnly: true,
		OperatorIsSuper:  true,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, h.toAdminVO(admin))
}

// ResetAccountPasswordReq 超管重置账号密码请求
type ResetAccountPasswordReq struct {
	NewPassword string `json:"new_password" binding:"required,min=6"`
	// 密文传输声明（语义同 AdminLoginReq）
	PwdAlg string `json:"pwd_alg"`
	PwdKey string `json:"pwd_key"`
}

// ResetAccountPassword 重置账号密码（超管专用，免旧密码）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ResetAccountPassword(c *gin.Context) {
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req ResetAccountPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if !h.decryptPwdField(c, &req.NewPassword, req.PwdAlg, req.PwdKey) {
		return
	}

	if _, err = h.userClient.ChangeAdminPassword(c.Request.Context(), &user.ChangeAdminPasswordReq{
		Id:          id,
		NewPassword: req.NewPassword,
		Force:       true,
	}); err != nil {
		if grpcErr, ok := status.FromError(err); ok && grpcErr.Code() == codes.InvalidArgument {
			response.Error(c, errcode.ParamError, grpcErr.Message())
			return
		}
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// GetAccountAssignments 查询账号任职的社区与角色（超管专用，账号管理页任职详情弹窗）
// 按社区 ID 升序返回各任职社区及其角色列表；超管账号返回空列表（不受社区限制）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetAccountAssignments(c *gin.Context) {
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.userClient.GetAdminAssignments(c.Request.Context(), &user.GetAdminAssignmentsReq{AdminId: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	list := make([]gin.H, 0, len(resp.List))
	for _, item := range resp.List {
		roles := make([]gin.H, 0, len(item.Roles))
		for _, r := range item.Roles {
			roles = append(roles, gin.H{
				"id":   r.Id,
				"name": r.Name,
				"code": r.Code,
			})
		}
		list = append(list, gin.H{
			"community_id":   item.CommunityId,
			"community_name": item.CommunityName,
			"roles":          roles,
		})
	}
	response.Success(c, list)
}

// ListCommunityAdmins 查询社区现任管理员（超管专用，社区编辑页使用）
// 仅返回担任该社区内置社区管理员角色的账号
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListCommunityAdmins(c *gin.Context) {
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	communityID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || communityID <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.userClient.ListAdmins(c.Request.Context(), &user.ListAdminsReq{
		CommunityId: communityID,
		Pagination:  &common.Pagination{Page: 1, PageSize: 100},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	list := make([]gin.H, 0, len(resp.List))
	for _, a := range resp.List {
		isAdmin := false
		for _, r := range a.Roles {
			if r.Code == "community_admin" {
				isAdmin = true
				break
			}
		}
		if !isAdmin {
			continue
		}
		list = append(list, gin.H{
			"id":            a.Id,
			"username":      a.Username,
			"real_name":     a.RealName,
			"phone":         a.Phone,
			"status":        a.Status,
			"last_login_at": a.LastLoginAt,
		})
	}
	response.SuccessWithList(c, list)
}

// AddCommunityAdminReq 指定社区管理员请求
type AddCommunityAdminReq struct {
	Username string `json:"username" binding:"required"`
	// 新建账号时的初始密码；username 已存在（复用账号）时可省略
	Password string `json:"password"`
	RealName string `json:"real_name"`
	Phone    string `json:"phone"`
	// 密文传输声明（语义同 AdminLoginReq）
	PwdAlg string `json:"pwd_alg"`
	PwdKey string `json:"pwd_key"`
}

// AddCommunityAdmin 指定社区管理员（超管专用，社区编辑页使用）
// username 已存在时复用账号；授予该社区内置社区管理员角色（每社区仅一人，自动替换原任职者）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) AddCommunityAdmin(c *gin.Context) {
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	communityID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || communityID <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req AddCommunityAdminReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if !h.decryptPwdField(c, &req.Password, req.PwdAlg, req.PwdKey) {
		return
	}

	admin, err := h.userClient.CreateAdmin(c.Request.Context(), &user.CreateAdminReq{
		Username:             req.Username,
		Password:             req.Password,
		RealName:             req.RealName,
		Phone:                req.Phone,
		CommunityId:          communityID,
		AssignCommunityAdmin: true,
		OperatorIsSuper:      true,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, h.toAdminVO(admin))
}

// RemoveCommunityAdmin 移除社区管理员任职（超管专用，社区编辑页使用）
// 仅摘除该社区的内置社区管理员角色；若摘除后该社区无其他任职则移除全部任职
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) RemoveCommunityAdmin(c *gin.Context) {
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	communityID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || communityID <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	adminID, err := strconv.ParseInt(c.Param("adminId"), 10, 64)
	if err != nil || adminID <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	// 定位该社区内置社区管理员角色（不存在则说明无任职可移除）
	rolesResp, err := h.userClient.ListRoles(c.Request.Context(), &user.ListRolesReq{CommunityId: communityID})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	var builtinID int64
	for _, r := range rolesResp.List {
		if r.Code == "community_admin" {
			builtinID = r.Id
			break
		}
	}
	if builtinID == 0 {
		response.Error(c, errcode.NotFound, "该社区未初始化社区管理员角色")
		return
	}

	// 取该账号在社区的现有任职，剔除内置角色后保留其余角色
	ctxResp, err := h.userClient.GetAdminContext(c.Request.Context(), &user.GetAdminContextReq{
		AdminId:     adminID,
		CommunityId: communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	remaining := make([]int64, 0, len(ctxResp.RoleIds))
	for _, rid := range ctxResp.RoleIds {
		if rid != builtinID {
			remaining = append(remaining, rid)
		}
	}

	if len(remaining) == 0 {
		// 该社区仅担任社区管理员：移除全部任职
		if _, err = h.userClient.DeleteAdmin(c.Request.Context(), &user.DeleteAdminReq{
			Id:          adminID,
			CommunityId: communityID,
		}); err != nil {
			response.GRPCError(c, err)
			return
		}
	} else {
		// 兼任其他角色：保留其余任职，仅摘除社区管理员
		if _, err = h.userClient.UpdateAdmin(c.Request.Context(), &user.UpdateAdminReq{
			Id:              adminID,
			CommunityId:     communityID,
			RoleIds:         remaining,
			OperatorIsSuper: true,
		}); err != nil {
			response.GRPCError(c, err)
			return
		}
	}
	response.Success(c, gin.H{"id": adminID, "community_id": communityID})
}

// ListRoles 获取角色列表
// 默认返回当前社区的角色；超管可传 scope=all 跨社区总览（含所属社区名与任职人数），
// 或传 community_id 查看任意社区的角色（社区权限分配页使用）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListRoles(c *gin.Context) {
	isSuper := gwMiddleware.IsSuperAdmin(c)

	req := &user.ListRolesReq{CommunityId: h.getAdminCommunityID(c)}
	switch c.Query("scope") {
	case "all":
		if !isSuper {
			response.Error(c, errcode.NoPermission)
			return
		}
		req.AllCommunities = true
	case "global":
		// 旧入口：角色一律社区私有，不再维护全局角色
		response.Error(c, errcode.ParamError, "scope=global 已废弃，请使用 scope=all")
		return
	}
	if id, err := strconv.ParseInt(c.Query("community_id"), 10, 64); err == nil && id > 0 {
		if !isSuper {
			response.Error(c, errcode.NoPermission)
			return
		}
		req.CommunityId = id
	}
	resp, err := h.userClient.ListRoles(c.Request.Context(), req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// CreateRoleReq 创建角色请求
type CreateRoleReq struct {
	Name        string   `json:"name" binding:"required"`
	Code        string   `json:"code" binding:"required"`
	Permissions []string `json:"permissions"`
	Description string   `json:"description"`
	// 角色归属社区：必填且 >0（超管显式指定；非超管忽略此字段，落到当前社区语境）
	CommunityID int64 `json:"community_id"`
}

// CreateRole 创建角色
// 角色一律社区私有：超管必须显式指定 community_id，非超管只能在当前社区创建且权限点不得超出自身范围
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateRole(c *gin.Context) {
	var req CreateRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	isSuper := gwMiddleware.IsSuperAdmin(c)
	if !isSuper && req.CommunityID != 0 {
		response.Error(c, errcode.ParamError, "只能为当前社区创建角色")
		return
	}

	communityID := req.CommunityID
	if !isSuper {
		communityID = h.getAdminCommunityID(c)
	}
	if communityID <= 0 {
		response.Error(c, errcode.ParamError, "角色必须归属于某个社区，请先选择社区")
		return
	}
	if !isSuper {
		if err := h.validatePermissionsSubset(c, communityID, req.Permissions); err != nil {
			response.Error(c, errcode.ParamError, err.Error())
			return
		}
	}

	resp, err := h.userClient.CreateRole(c.Request.Context(), &user.CreateRoleReq{
		Name:        req.Name,
		Code:        req.Code,
		Permissions: req.Permissions,
		Description: req.Description,
		CommunityId: communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateRoleReq 更新角色请求
type UpdateRoleReq struct {
	Name        string   `json:"name"`
	Code        string   `json:"code"`
	Permissions []string `json:"permissions"`
	Description string   `json:"description"`
}

// UpdateRole 更新角色
// 历史保留行（community_id=0）与内置社区管理员角色仅超管可改；
// 非超管的权限点不得超出自身权限范围
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateRole(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	isSuper := gwMiddleware.IsSuperAdmin(c)

	roleResp, err := h.userClient.GetRole(c.Request.Context(), &user.GetRoleReq{Id: id, CommunityId: h.scopeCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	role := roleResp
	if !isSuper {
		if role.CommunityId == 0 || role.CommunityId != h.getAdminCommunityID(c) {
			response.Error(c, errcode.NoPermission)
			return
		}
		if role.Code == "community_admin" {
			response.Error(c, errcode.NoPermission, "内置社区管理员角色仅超级管理员可修改")
			return
		}
		// 社区管理员不得编辑自己担任角色的权限（防止变相自我提权）
		if h.adminHoldsRole(c.Request.Context(), uc.UserID, role.CommunityId, id) {
			response.Error(c, errcode.NoPermission, "不能编辑自己担任的角色的权限")
			return
		}
		if err := h.validatePermissionsSubset(c, role.CommunityId, req.Permissions); err != nil {
			response.Error(c, errcode.ParamError, err.Error())
			return
		}
	}

	resp, err := h.userClient.UpdateRole(c.Request.Context(), &user.UpdateRoleReq{
		Id:          id,
		Name:        req.Name,
		Code:        req.Code,
		Permissions: req.Permissions,
		Description: req.Description,

		CommunityId: h.scopeCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteRole 删除角色
// 历史保留行（community_id=0）仅超管可删；内置社区管理员角色不可删除（服务层校验）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteRole(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	isSuper := gwMiddleware.IsSuperAdmin(c)

	roleResp, err := h.userClient.GetRole(c.Request.Context(), &user.GetRoleReq{Id: id, CommunityId: h.scopeCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if !isSuper {
		if roleResp.CommunityId == 0 || roleResp.CommunityId != h.getAdminCommunityID(c) {
			response.Error(c, errcode.NoPermission)
			return
		}
	}

	_, err = h.userClient.DeleteRole(c.Request.Context(), &user.DeleteRoleReq{Id: id, CommunityId: h.scopeCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// GetCommunityPermissions 获取社区权限分配详情（超管专用）
// 返回该社区的权限上限（内置社区管理员角色的权限集）及全部社区角色概览；
// 内置角色不存在时（存量社区）role_id 返回 0、permissions 返回空数组
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetCommunityPermissions(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}

	resp, err := h.userClient.ListRoles(c.Request.Context(), &user.ListRolesReq{CommunityId: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	var ceiling *user.RoleInfo
	for _, r := range resp.List {
		if r.Code == "community_admin" {
			ceiling = r
			break
		}
	}

	permissions := []string{}
	var ceilingID int64
	if ceiling != nil {
		ceilingID = ceiling.Id
		if len(ceiling.Permissions) > 0 {
			permissions = ceiling.Permissions
		}
	}
	response.Success(c, gin.H{
		"community_id": id,
		"role_id":      ceilingID,
		"permissions":  permissions,
		"roles":        resp.List,
	})
}

// UpdateCommunityPermissionsReq 设置社区权限请求
type UpdateCommunityPermissionsReq struct {
	Permissions []string `json:"permissions"`
}

// systemLevelPermissions 系统级权限点：仅超管可用，不进入社区权限上限。
// 对应页面均在系统管理空间（社区管理/行政区划/支付·售后·附件配置），业务菜单不展示；
// 保存社区权限时自动剔除（前端同样过滤，此处兜底），存量 ["*"] 上限社区保存一次即收敛
var systemLevelPermissions = map[string]struct{}{
	"community:view": {}, "community:create": {}, "community:update": {}, "community:delete": {},
	"region:view": {}, "region:update": {},
	"settings:view": {}, "settings:update": {},
}

// UpdateCommunityPermissions 设置社区权限上限（超管专用）
// 即更新该社区内置社区管理员角色的权限集；内置角色不存在时自动创建
// 权限解析运行时与上限取交集，保存后立即对社区全员生效
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateCommunityPermissions(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateCommunityPermissionsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}

	// 剔除系统级权限点：社区权限上限只承载业务权限
	filtered := make([]string, 0, len(req.Permissions))
	for _, p := range req.Permissions {
		if _, ok := systemLevelPermissions[p]; !ok {
			filtered = append(filtered, p)
		}
	}
	req.Permissions = filtered

	if len(req.Permissions) == 0 {
		response.Error(c, errcode.ParamError, "至少保留一个权限点，否则社区管理员将无法登录后台")
		return
	}

	resp, err := h.userClient.ListRoles(c.Request.Context(), &user.ListRolesReq{CommunityId: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	var ceilingID int64
	for _, r := range resp.List {
		if r.Code == "community_admin" {
			ceilingID = r.Id
			break
		}
	}

	if ceilingID <= 0 {
		// 存量社区未初始化内置角色：随本次分配自动创建
		created, err := h.userClient.CreateRole(c.Request.Context(), &user.CreateRoleReq{
			Name:        "社区管理员",
			Code:        "community_admin",
			Permissions: req.Permissions,
			Description: "内置角色：负责本社区的日常运营与员工管理；其权限即本社区权限上限",
			CommunityId: id,
		})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		ceilingID = created.Id
	} else if _, err := h.userClient.UpdateRole(c.Request.Context(), &user.UpdateRoleReq{
		Id:          ceilingID,
		Permissions: req.Permissions,
	}); err != nil {
		response.GRPCError(c, err)
		return
	}

	response.Success(c, gin.H{"community_id": id, "role_id": ceilingID})
}

// validatePermissionsSubset 校验权限点列表不超出操作者在指定社区的权限范围
// 超管不调用此函数；普通管理员的有效权限已在用户服务侧展开（不再下发 "*" 通配），
// 故此处保留的通配判断仅作兜底，实际会走逐点比对，拒绝把系统级权限授予社区角色
// 参数：
//   - c: gin 上下文
//   - communityID: 社区语境
//   - permissions: 待校验的权限点列表
//
// 返回值：
//   - error: 超出范围时返回错误
func (h *AdminHandler) validatePermissionsSubset(c *gin.Context, communityID int64, permissions []string) error {
	if len(permissions) == 0 {
		return nil
	}
	uc := appcontext.GetUserContext(c)
	ctxInfo, err := h.userClient.GetAdminContext(c.Request.Context(), &user.GetAdminContextReq{
		AdminId:     uc.UserID,
		CommunityId: communityID,
	})
	if err != nil || ctxInfo == nil {
		return err
	}
	for _, p := range ctxInfo.Permissions {
		if p == "*" {
			return nil
		}
	}
	owned := make(map[string]struct{}, len(ctxInfo.Permissions))
	for _, p := range ctxInfo.Permissions {
		owned[p] = struct{}{}
	}
	for _, p := range permissions {
		if _, ok := owned[p]; !ok {
			return errPermissionsExceeded
		}
	}
	return nil
}

// errPermissionsExceeded 权限超出自身范围的错误
var errPermissionsExceeded = errorString("分配的权限超出自身权限范围")

// errorString 简单字符串错误类型
type errorString string

func (e errorString) Error() string { return string(e) }

// RefreshToken 刷新管理员 access_token
// 令牌来源：httpOnly cookie（新版前端）优先，表单/请求体兜底（旧前端缓存、POS 端）
//
// P1-5 三道加固（此前只凭旧 claims 续发，停用管理员在令牌有效期内永远在线）：
//  1. 黑名单原子消费（SETNX）：旧 jti 在入口即登记为「已使用」，并发重放只有一次能通过
//     （Redis 故障时放行降级）。代价是刷新中途失败也需重新登录——安全优先于便利；
//  2. 账号实时校验：按 claims.UserID 查库，账号已删除或 status≠1（停用）即拒，
//     不必等 7 天 refresh 令牌自然过期；
//  3. 轮转：签发新 refresh 令牌并吊销旧的——被盗令牌在合法用户刷新一次后即失效。
//     cookie 模式（新版前端）新令牌随 httpOnly cookie 下发；旧前端/POS 从响应体取，
//     两种模式都会拿到新值，不存在「吊销了却还在用旧值」的断档。
//  4. 角色/社区按实时账号重推导（P1-2）：不再照抄旧令牌 claims 的 super_admin 快照，
//     降权或解除社区绑定后刷新即生效。
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) RefreshToken(c *gin.Context) {
	token := c.PostForm("refresh_token")
	if token == "" {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = c.ShouldBindJSON(&body)
		token = body.RefreshToken
	}
	token = firstNonEmptyRefreshToken(c, token)
	if token == "" {
		response.Error(c, errcode.NotLoginOrTokenExpired)
		return
	}

	ctx := c.Request.Context()
	claims, err := h.jwtManager.ParseToken(token)
	// 强校验 token 用途：仅 refresh token 可刷新，access token 不得用于换取新令牌
	if err != nil || claims.Type != jwt.TokenTypeAdmin || claims.TokenUse != jwt.TokenUseRefresh {
		response.Error(c, errcode.InvalidToken)
		return
	}
	// 黑名单原子消费（P1-5 + P1-7）：SETNX 一次完成「查旧 jti 是否用过 + 登记为已用」，
	// 同一枚令牌被并发提交两次时只有先抢到的那次能刷新
	if h.refreshGuard.consume(ctx, claims.ID, time.Until(claims.ExpiresAt.Time)) {
		response.Error(c, errcode.InvalidToken)
		return
	}
	// 账号实时校验（P1-5）：停用/删除的管理员立即失去续期能力
	admin, err := h.userClient.GetAdminById(ctx, &user.GetAdminByIdReq{Id: claims.UserID})
	if err != nil {
		if status.Code(err) != codes.NotFound {
			log.Printf("[WARN] refresh 查询管理员失败 admin_id=%d: %v", claims.UserID, err)
			response.Error(c, errcode.InternalServerError)
			return
		}
		// 账号已删除：按令牌无效处理，不透露账号是否存在
		response.Error(c, errcode.InvalidToken)
		return
	}
	if admin.Status != 1 {
		response.Error(c, errcode.AccountDisabled)
		return
	}

	// 角色与社区语境按实时账号重推导（P1-2）：claims 里的 super_admin / community_ids
	// 是签发当时的快照，降权或解除社区绑定后若照抄快照，refresh 等于把旧权限无限续期。
	// 默认社区沿用登录口径；旧令牌社区若仍在当前任职列表内则保留（不打断正在操作的社区），
	// 已被解除绑定时回退默认社区。权限不写进 JWT（登录侧同样传 nil），由中间件按社区实时解析。
	communityID := h.getAdminDefaultCommunityID(admin)
	if claims.CommunityID > 0 && (admin.IsSuper || containsInt64(admin.CommunityIds, claims.CommunityID)) {
		communityID = claims.CommunityID
	}
	roleCode := "admin"
	if admin.IsSuper {
		roleCode = "super_admin"
	}

	accessToken, accessExpire, err := h.jwtManager.GenerateAdminAccessToken(
		admin.Id, admin.Phone, roleCode, communityID, admin.CommunityIds, nil, admin.Username, admin.RealName,
	)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	// 轮转：新 refresh 令牌随本次刷新一起签发；旧令牌已在入口原子消费，无需再写黑名单
	newRefresh, refreshExpire, err := h.jwtManager.GenerateRefreshToken(claims.UserID, jwt.TokenTypeAdmin)
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	now := time.Now()
	resp := gin.H{
		"access_token": accessToken,
		"expires_in":   int64(accessExpire.Sub(now).Seconds()),
		"expires_at":   accessExpire.Unix(),
	}
	if wantsRefreshCookie(c) {
		setRefreshTokenCookie(c, newRefresh, int64(refreshExpire.Sub(now).Seconds()))
		// access cookie 随新签发的 access_token 同步轮换，否则旧令牌过期后
		// WS 握手仍拿 cookie 里的失效 token 去鉴权
		setAccessTokenCookie(c, accessToken, int64(accessExpire.Sub(now).Seconds()))
	} else {
		// 旧前端/POS 从响应体取 refresh_token：轮转后必须回传新值，
		// 否则客户端继续持有已吊销的旧令牌，下次刷新即失败
		resp["refresh_token"] = newRefresh
		resp["refresh_expires_in"] = int64(refreshExpire.Sub(now).Seconds())
		resp["refresh_expires_at"] = refreshExpire.Unix()
	}
	response.Success(c, resp)
}

// --- 分类管理 ---

// ListCategories 获取分类列表（管理端含禁用分类，供分类管理页展示与再启用）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListCategories(c *gin.Context) {
	resp, err := h.productClient.ListCategories(c.Request.Context(), &product.ListCategoriesReq{
		CommunityId:     h.getAdminCommunityID(c),
		IncludeDisabled: true,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// CreateCategoryReq 创建分类请求
type CreateCategoryReq struct {
	ParentID int64  `json:"parent_id"`
	Name     string `json:"name" binding:"required"`
	Icon     string `json:"icon"`
	Sort     int32  `json:"sort"`
}

// CreateCategory 创建分类
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateCategory(c *gin.Context) {
	var req CreateCategoryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateCategory(c.Request.Context(), &product.CreateCategoryReq{
		CommunityId: h.getAdminCommunityID(c),
		ParentId:    req.ParentID,
		Name:        req.Name,
		Icon:        req.Icon,
		Sort:        req.Sort,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateCategoryReq 更新分类请求
type UpdateCategoryReq struct {
	Name   string `json:"name"`
	Icon   string `json:"icon"`
	Sort   int32  `json:"sort"`
	Status int32  `json:"status"`
}

// UpdateCategory 更新分类
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateCategory(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateCategoryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.UpdateCategory(c.Request.Context(), &product.UpdateCategoryReq{
		Id:          id,
		Name:        req.Name,
		Icon:        req.Icon,
		Sort:        req.Sort,
		Status:      req.Status,
		CommunityId: h.scopeCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// BatchSortCategoriesReq 分类批量排序请求（拖拽后按显示顺序整表提交）
type BatchSortCategoriesReq struct {
	// Items 全量有序列表：id + 目标 sort（建议按显示顺序 ×10 留空隙）
	Items []BatchSortCategoryItem `json:"items" binding:"required,min=1"`
}

// BatchSortCategoryItem 单个分类排序项
type BatchSortCategoryItem struct {
	ID   int64 `json:"id" binding:"required,min=1"`
	Sort int32 `json:"sort"`
}

// BatchSortCategories 分类批量排序（管理端拖拽保存）
// 事务内逐项校验归属，任一越权即整体失败
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) BatchSortCategories(c *gin.Context) {
	var req BatchSortCategoriesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	items := make([]*product.CategorySortItem, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, &product.CategorySortItem{Id: it.ID, Sort: it.Sort})
	}
	_, err := h.productClient.BatchSortCategories(c.Request.Context(), &product.BatchSortCategoriesReq{
		CommunityId: h.scopeCommunityID(c),
		Items:       items,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

// DeleteCategory 删除分类
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteCategory(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.DeleteCategory(c.Request.Context(), &product.DeleteCategoryReq{Id: id, CommunityId: h.scopeCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// --- 商品管理 ---

// GetAdminProduct 获取管理端商品详情（含 SKU 列表与规格树）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetAdminProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.GetProduct(c.Request.Context(), &product.GetProductReq{Id: id, CommunityId: h.scopeCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListAdminProducts 获取管理端商品列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminProducts(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	categoryID, _ := strconv.ParseInt(c.DefaultQuery("category_id", "0"), 10, 64)
	keyword := c.Query("keyword")
	// 商品状态精确过滤（选品/收银台传 status=1 只取上架商品）
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))
	// 展示状态过滤（Tab）：0 不过滤，1=在售中 2=已售罄 3=仓库中
	statusFilter, _ := strconv.Atoi(c.DefaultQuery("status_filter", "0"))

	resp, err := h.productClient.ListProducts(c.Request.Context(), &product.ListProductsReq{
		CommunityId:  h.getAdminCommunityID(c),
		CategoryId:   categoryID,
		Keyword:      keyword,
		Status:       int32(status),
		StatusFilter: int32(statusFilter),
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

// SpecValueReqItem 规格值保存项（前端临时 ID 或真实 ID）
type SpecValueReqItem struct {
	ID     int64  `json:"id"`
	TempID string `json:"temp_id"`
	Name   string `json:"name"`
	Sort   int32  `json:"sort"`
}

// SpecReqItem 规格名保存项（含层级规格值）
type SpecReqItem struct {
	ID     int64               `json:"id"`
	TempID string              `json:"temp_id"`
	Name   string              `json:"name"`
	Sort   int32               `json:"sort"`
	Values []*SpecValueReqItem `json:"values"`
}

// SkuItemReqItem SKU 保存项（spec_value_ids 为规格值 ID 列表，真实数字 ID 或临时 ID）
type SkuItemReqItem struct {
	ID            int64    `json:"id"`
	SpecValueIDs  []string `json:"spec_value_ids"`
	SkuCode       string   `json:"sku_code"`
	Price         float64  `json:"price"`
	CostPrice     float64  `json:"cost_price"`
	Stock         int32    `json:"stock"`
	SafetyStock   int32    `json:"safety_stock"`
	Image         string   `json:"image"`
	OriginalPrice float64  `json:"original_price"`
	Barcode       string   `json:"barcode"`
}

// CreateProductReq 创建商品请求
type CreateProductReq struct {
	CategoryID   int64             `json:"category_id" binding:"required"`
	Name         string            `json:"name" binding:"required"`
	Description  string            `json:"description"`
	CoverImage   string            `json:"cover_image"`
	Images       []string          `json:"images"`
	Unit         string            `json:"unit"`
	IsMultiSpec  bool              `json:"is_multi_spec"`
	MinBuy       int32             `json:"min_buy"`
	Sort         int32             `json:"sort"`
	ShowSales    *bool             `json:"show_sales"`
	VirtualSales int32             `json:"virtual_sales"`
	OnSaleAt     string            `json:"on_sale_at"` // 定时上架时间（空=立即上架）
	Specs        []*SpecReqItem    `json:"specs"`
	SkuItems     []*SkuItemReqItem `json:"sku_items"`
}

// toProtoSpecs 转换规格树为 proto 结构
// 参数：
//   - specs: 网关层规格树列表
//
// 返回值：
//   - []*product.SpecReq: proto 规格树
func toProtoSpecs(specs []*SpecReqItem) []*product.SpecReq {
	if len(specs) == 0 {
		return nil
	}
	list := make([]*product.SpecReq, 0, len(specs))
	for _, sp := range specs {
		values := make([]*product.SpecValueReq, 0, len(sp.Values))
		for _, v := range sp.Values {
			values = append(values, &product.SpecValueReq{
				Id:     v.ID,
				TempId: v.TempID,
				Name:   v.Name,
				Sort:   v.Sort,
			})
		}
		list = append(list, &product.SpecReq{
			Id:     sp.ID,
			TempId: sp.TempID,
			Name:   sp.Name,
			Sort:   sp.Sort,
			Values: values,
		})
	}
	return list
}

// toProtoSkuItems 转换 SKU 列表为 proto 结构
// 参数：
//   - items: 网关层 SKU 列表
//
// 返回值：
//   - []*product.SkuItemReq: proto SKU 列表
func toProtoSkuItems(items []*SkuItemReqItem) []*product.SkuItemReq {
	if len(items) == 0 {
		return nil
	}
	list := make([]*product.SkuItemReq, 0, len(items))
	for _, it := range items {
		list = append(list, &product.SkuItemReq{
			Id:            it.ID,
			SpecValueIds:  it.SpecValueIDs,
			SkuCode:       it.SkuCode,
			Price:         it.Price,
			CostPrice:     it.CostPrice,
			Stock:         it.Stock,
			SafetyStock:   it.SafetyStock,
			Image:         it.Image,
			OriginalPrice: it.OriginalPrice,
			Barcode:       it.Barcode,
		})
	}
	return list
}

// CreateProduct 创建商品
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateProduct(c *gin.Context) {
	var req CreateProductReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateProduct(c.Request.Context(), &product.CreateProductReq{
		CommunityId:  h.getAdminCommunityID(c),
		CategoryId:   req.CategoryID,
		Name:         req.Name,
		Description:  req.Description,
		CoverImage:   req.CoverImage,
		Images:       req.Images,
		Unit:         req.Unit,
		IsMultiSpec:  req.IsMultiSpec,
		MinBuy:       req.MinBuy,
		Sort:         req.Sort,
		ShowSales:    req.ShowSales != nil && *req.ShowSales,
		VirtualSales: req.VirtualSales,
		OnSaleAt:     req.OnSaleAt,
		Specs:        toProtoSpecs(req.Specs),
		SkuItems:     toProtoSkuItems(req.SkuItems),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateProductReq 更新商品请求
type UpdateProductReq struct {
	CategoryID   int64             `json:"category_id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	CoverImage   string            `json:"cover_image"`
	Images       []string          `json:"images"`
	Unit         string            `json:"unit"`
	Sort         int32             `json:"sort"`
	Status       int32             `json:"status"`
	MinBuy       int32             `json:"min_buy"`
	IsMultiSpec  bool              `json:"is_multi_spec"`
	ShowSales    *bool             `json:"show_sales"`
	VirtualSales int32             `json:"virtual_sales"`
	OnSaleAt     string            `json:"on_sale_at"` // 定时上架时间（空=取消定时）
	Specs        []*SpecReqItem    `json:"specs"`
	SkuItems     []*SkuItemReqItem `json:"sku_items"`
}

// UpdateProduct 更新商品
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateProductReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.UpdateProduct(c.Request.Context(), &product.UpdateProductReq{
		Id:           id,
		CategoryId:   req.CategoryID,
		CommunityId:  h.scopeCommunityID(c),
		Name:         req.Name,
		Description:  req.Description,
		CoverImage:   req.CoverImage,
		Images:       req.Images,
		Unit:         req.Unit,
		Sort:         req.Sort,
		Status:       req.Status,
		MinBuy:       req.MinBuy,
		IsMultiSpec:  req.IsMultiSpec,
		ShowSales:    req.ShowSales != nil && *req.ShowSales,
		VirtualSales: req.VirtualSales,
		OnSaleAt:     req.OnSaleAt,
		Specs:        toProtoSpecs(req.Specs),
		SkuItems:     toProtoSkuItems(req.SkuItems),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteProduct 删除商品
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteProduct(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.DeleteProduct(c.Request.Context(), &product.DeleteProductReq{Id: id, CommunityId: h.scopeCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// UpdateProductStatusReq 更新商品状态请求
// Status 使用指针：gin binding:"required" 对非指针零值会误拒 status=0（下架），
// 指针 required 仅校验字段已提供，0/1 均有效
type UpdateProductStatusReq struct {
	Status *int32 `json:"status" binding:"required"`
}

// BatchUpdateProductStatusReq 批量上下架请求体
type BatchUpdateProductStatusReq struct {
	IDs    []int64 `json:"ids" binding:"required,min=1"`
	Status *int32  `json:"status" binding:"required"`
}

// BatchUpdateProductStatus 批量上下架商品
// 单条 UPDATE 完成，不触碰已删除商品
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) BatchUpdateProductStatus(c *gin.Context) {
	var req BatchUpdateProductStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err := h.productClient.BatchUpdateProductStatus(c.Request.Context(), &product.BatchUpdateProductStatusReq{
		Ids:         req.IDs,
		Status:      *req.Status,
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, nil)
}

// GetProductStatusStats 商品状态统计（口径与列表筛选条件一致，供 Tab 计数展示）
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetProductStatusStats(c *gin.Context) {
	categoryID, _ := strconv.ParseInt(c.DefaultQuery("category_id", "0"), 10, 64)

	resp, err := h.productClient.GetProductStatusStats(c.Request.Context(), &product.GetProductStatusStatsReq{
		CommunityId: h.getAdminCommunityID(c),
		CategoryId:  categoryID,
		Keyword:     c.Query("keyword"),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateProductStatus 更新商品状态
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateProductStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateProductStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.UpdateProductStatus(c.Request.Context(), &product.UpdateProductStatusReq{
		Id:          id,
		Status:      *req.Status,
		CommunityId: h.scopeCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": *req.Status})
}

// UpdateProductName 商品名称行内更新请求
type UpdateProductNameReq struct {
	Name string `json:"name" binding:"required"`
}

// UpdateProductName 更新商品名称（列表页行内改名轻量接口）
// 携带管理员社区语境，商品归属校验在 product-service 内完成
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateProductName(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateProductNameReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.UpdateProductName(c.Request.Context(), &product.UpdateProductNameReq{
		Id:          id,
		Name:        req.Name,
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "name": req.Name})
}

// --- 订单管理 ---

// AdminVerifyPickupReq 后台自提核销请求
type AdminVerifyPickupReq struct {
	PickupCode string `json:"pickup_code" binding:"required"`
}

// AdminVerifyPickup 后台自提订单核销（兜底入口）：校验取货码后完成订单
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) AdminVerifyPickup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	var req AdminVerifyPickupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.orderClient.VerifyPickup(c.Request.Context(), &order.VerifyPickupReq{
		OrderId:      id,
		PickupCode:   req.PickupCode,
		OperatorType: "admin",
		OperatorId:   uc.UserID,
		// 社区隔离：服务端校验订单归属社区，跨社区核销直接拒绝
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// --- 履约工作台（B）：订单指派 ---
// 履约人员 = 本社区任职且绑定会员的员工，配置入口在员工编辑页（绑定 admin_users.user_id）

// ListFulfillers 列出本社区履约人员（订单指派弹窗数据源）
//
// 接口: GET /api/v1/admin/fulfillers（权限 user:view）
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListFulfillers(c *gin.Context) {
	communityID := h.getAdminCommunityID(c)
	if communityID <= 0 {
		response.Error(c, errcode.ParamError, "缺少社区语境")
		return
	}
	resp, err := h.userClient.ListFulfillers(c.Request.Context(), &user.ListFulfillersReq{
		CommunityId: communityID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// AssignDeliveryReq 后台指派配送请求
type AssignDeliveryReq struct {
	// 履约人员 user_id；0=取消指派
	DeliveryUserID int64 `json:"delivery_user_id"`
}

// AssignDelivery 指派/取消指派配送员（订单列表操作）
// 指派时校验目标用户是本社区履约人员（服务端二次校验），并回填姓名快照
//
// 接口: PUT /api/v1/admin/orders/:id/assign-delivery（权限 order:update）
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) AssignDelivery(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	var req AssignDeliveryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	communityID := h.getAdminCommunityID(c)
	if communityID <= 0 {
		response.Error(c, errcode.ParamError, "缺少社区语境")
		return
	}

	// 指派时查会员昵称做姓名快照（列表/详情回显免再查）
	var deliveryUserName string
	if req.DeliveryUserID > 0 {
		u, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: req.DeliveryUserID})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		if u == nil {
			response.Error(c, errcode.NotFound, "会员不存在")
			return
		}
		deliveryUserName = u.Nickname
	}

	uc := appcontext.GetUserContext(c)
	if _, err := h.orderClient.AssignDelivery(c.Request.Context(), &order.AssignDeliveryReq{
		OrderId:          id,
		DeliveryUserId:   req.DeliveryUserID,
		DeliveryUserName: deliveryUserName,
		CommunityId:      communityID,
		OperatorType:     "admin",
		OperatorId:       uc.UserID,
	}); err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"success": true, "delivery_user_name": deliveryUserName})
}

// adminOrderPayInfo 查询订单支付方式（类型数值 + 文案，管理端订单列表/详情共用）
// 未支付或查询不到支付记录时返回 0 与空串
// 参数：
//   - c: gin 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - int32: 支付类型（1微信 2支付宝 3货到付款 4现金；0=未支付/未知）
//   - string: 支付方式文案
func (h *AdminHandler) adminOrderPayInfo(c *gin.Context, orderID int64) (int32, string) {
	resp, err := h.paymentClient.GetPayment(c.Request.Context(), &payment.GetPaymentReq{OrderId: orderID})
	if err != nil || resp == nil {
		return 0, ""
	}
	return resp.PayType, payTypeTextOf(resp.PayType)
}

// ListAdminOrders 获取管理端订单列表
// 支持按会员过滤（user_id，会员列表弹窗查订单）；
// 超管可显式传 community_id=0 跨社区查询全部订单（普通管理员始终限定当前社区语境）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminOrders(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))

	// 目标社区：默认当前社区语境；超管显式传 community_id 时以其为准（0=跨社区查全部）
	communityID := h.getAdminCommunityID(c)
	isSuper := gwMiddleware.IsSuperAdmin(c)
	if isSuper && c.Query("community_id") != "" {
		if id, err := strconv.ParseInt(c.Query("community_id"), 10, 64); err == nil && id >= 0 {
			communityID = id
		}
	}
	// 会员过滤（会员列表弹窗查订单）
	userID, _ := strconv.ParseInt(c.DefaultQuery("user_id", "0"), 10, 64)
	// 来源过滤：1=C端 2=收银台（0=全部）
	source, _ := strconv.Atoi(c.DefaultQuery("source", "0"))
	// 收银班次过滤（交班记录「查看班内订单」入口）
	shiftID, _ := strconv.ParseInt(c.DefaultQuery("shift_id", "0"), 10, 64)
	// 商品过滤（含全部规格）
	productID, _ := strconv.ParseInt(c.DefaultQuery("product_id", "0"), 10, 64)
	// 支付单号/渠道交易号（支付宝商家订单号/交易号，单框双号）
	payNo := c.Query("pay_no")
	// 反查命中集：携带 pay_no 才先经 payment 服务反查订单号，再以集合到 order 服务精确匹配
	//（has_pay_no 区分「没传」与「传了但没查到」，后者直接返回空列表）
	var payOrderNos []string
	if payNo != "" {
		found, err := h.paymentClient.FindOrdersByPayNo(c.Request.Context(), &payment.FindOrdersByPayNoReq{PayNo: payNo})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		payOrderNos = found.GetOrderNos()
	}

	resp, err := h.orderClient.ListOrders(c.Request.Context(), &order.ListOrdersReq{
		CommunityId: communityID,
		UserId:      userID,
		// 后台管理端一律看全量：软删除只影响 C 端列表，客服按会员查订单必须能查到已删单
		IncludeUserDeleted: true,
		Source:             int32(source),
		Status:             int32(status),
		ShiftId:            shiftID,
		ProductId:          productID,
		HasPayNo:           payNo != "",
		PayOrderNos:        payOrderNos,
		OrderNo:            c.Query("order_no"),
		StartTime:          c.Query("start_time"),
		EndTime:            c.Query("end_time"),
		ReceiverName:       c.Query("receiver_name"),
		ReceiverPhone:      c.Query("receiver_phone"),
		PickupCode:         c.Query("pickup_code"),
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 跨社区查询（community_id=0）时回显订单所属社区名称
	communityNames := map[int64]string{}
	if communityID == 0 {
		for _, o := range resp.List {
			if o.CommunityId > 0 && communityNames[o.CommunityId] == "" {
				if comm, err := h.userClient.GetCommunity(c.Request.Context(), &user.GetCommunityReq{Id: o.CommunityId}); err == nil && comm != nil {
					communityNames[o.CommunityId] = comm.Name
				}
			}
		}
	}

	// 逐行 RPC 收口：会员名与支付方式原先每行各 1 次调用（一页 50 行即 100 次往返），
	// 改为整页一次批量查询后按 ID 查表。批量调用失败不阻断列表，退化为「会员名/支付方式留空」
	memberNames, memberErr := h.batchUserNames(c, resp.List, communityID)
	payTypes, payTypeErr := h.batchOrderPayTypes(c, resp.List)
	if memberErr != nil {
		log.Printf("[admin] 批量取会员名失败: %v", memberErr)
	}
	if payTypeErr != nil {
		log.Printf("[admin] 批量取支付方式失败: %v", payTypeErr)
	}

	list := make([]gin.H, 0, len(resp.List))
	for _, o := range resp.List {
		// 支付方式（列表与详情共用口径；0=未支付/未知）
		payType := payTypes[o.Id]
		list = append(list, gin.H{
			"id":              o.Id,
			"order_no":        o.OrderNo,
			"user_id":         o.UserId,
			"user_name":       memberNames[o.UserId],
			"community_id":    o.CommunityId,
			"community_name":  communityNames[o.CommunityId],
			"total_amount":    o.TotalAmount,
			"discount_amount": o.DiscountAmount,
			"manual_discount": o.ManualDiscount,
			"delivery_fee":    o.DeliveryFee,
			"pay_amount":      o.PayAmount,
			"delivery_type":   o.DeliveryType,
			"status":          o.Status,
			"status_text":     orderStatusText(o.Status, o.DeliveryType),
			"source":          o.Source,
			"user_deleted":    o.UserDeleted,
			"operator_name":   o.OperatorName,
			"shift_id":        o.ShiftId,
			"pay_type":        payType,
			"pay_type_text":   payTypeTextOf(payType),
			"created_at":      o.CreatedAt,
		})
	}
	response.SuccessWithPage(c, list, int(resp.Pagination.Total), page, pageSize)
}

// CreateOrderExportTask 创建订单导出任务（下载中心）
// 把当前列表筛选条件打包成 JSON 快照落库，后台异步生成 CSV 后在下载中心取文件；
// 创建即返回任务 ID，不再阻塞页面逐页拉数据
// 参数：
//   - c: gin 上下文（body: export_type/query_desc + query 参数复用列表筛选字段）
func (h *AdminHandler) CreateOrderExportTask(c *gin.Context) {
	// 目标社区：与列表同一口径（超管可显式传 community_id）
	communityID := h.getAdminCommunityID(c)
	isSuper := gwMiddleware.IsSuperAdmin(c)
	if isSuper && c.Query("community_id") != "" {
		if id, err := strconv.ParseInt(c.Query("community_id"), 10, 64); err == nil && id >= 0 {
			communityID = id
		}
	}
	userID, _ := strconv.ParseInt(c.DefaultQuery("user_id", "0"), 10, 64)
	source, _ := strconv.Atoi(c.DefaultQuery("source", "0"))
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))
	shiftID, _ := strconv.ParseInt(c.DefaultQuery("shift_id", "0"), 10, 64)
	productID, _ := strconv.ParseInt(c.DefaultQuery("product_id", "0"), 10, 64)

	// 支付单号反查：与列表一致先经 payment 服务反查订单号集合，随快照下发
	payNo := c.Query("pay_no")
	var payOrderNos []string
	if payNo != "" {
		found, err := h.paymentClient.FindOrdersByPayNo(c.Request.Context(), &payment.FindOrdersByPayNoReq{PayNo: payNo})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		payOrderNos = found.GetOrderNos()
	}

	// 查询条件快照：与 order 服务 exportQuery 结构对齐（所见即所导）
	cond := map[string]interface{}{
		"community_id":   communityID,
		"user_id":        userID,
		"source":         source,
		"status":         status,
		"shift_id":       shiftID,
		"product_id":     productID,
		"order_no":       c.Query("order_no"),
		"start_time":     c.Query("start_time"),
		"end_time":       c.Query("end_time"),
		"receiver_name":  c.Query("receiver_name"),
		"receiver_phone": c.Query("receiver_phone"),
		"pickup_code":    c.Query("pickup_code"),
		// pay_no 原始入参留档：失败任务「重新导出」时前端按快照原样重发，不丢账单号筛选
		"pay_no":          payNo,
		"include_deleted": true,
	}
	condJSON, err := json.Marshal(cond)
	if err != nil {
		response.Error(c, errcode.InternalServerError, "打包查询条件失败")
		return
	}

	uc := appcontext.GetUserContext(c)
	resp, err := h.orderClient.CreateExportTask(c.Request.Context(), &order.CreateExportTaskReq{
		ExportType:  "orders",
		QueryJson:   string(condJSON),
		QueryDesc:   c.Query("query_desc"),
		AdminId:     uc.UserID,
		AdminName:   c.GetString("admin_username"),
		CommunityId: communityID,
		PayOrderNos: payOrderNos,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": resp.Id})
}

// ListOrderExportTasks 下载中心任务列表
// 普通管理员仅见自己创建的；超管见全部（可按 community_id 限定语境）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListOrderExportTasks(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	isSuper := gwMiddleware.IsSuperAdmin(c)
	resp, err := h.orderClient.ListExportTasks(c.Request.Context(), &order.ListExportTasksReq{
		AdminId:     appcontext.GetUserContext(c).UserID,
		SuperAdmin:  isSuper,
		CommunityId: h.getAdminCommunityID(c),
		Page:        int32(page),
		PageSize:    int32(pageSize),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	list := make([]gin.H, 0, len(resp.List))
	for _, t := range resp.List {
		item := gin.H{
			"id":             t.Id,
			"export_type":    t.ExportType,
			"status":         t.Status,
			"query_desc":     t.QueryDesc,
			"file_name":      t.FileName,
			"file_size":      t.FileSize,
			"row_count":      t.RowCount,
			"admin_id":       t.AdminId,
			"admin_name":     t.AdminName,
			"download_count": t.DownloadCount,
			"error_msg":      t.ErrorMsg,
			"created_at":     t.CreatedAt,
			"expires_at":     t.ExpiresAt,
		}
		// 下载留痕名单仅超管可见（普通管理员只见自己的记录，名单即自己，无需回传）
		if isSuper {
			item["downloaders"] = t.Downloaders
		}
		list = append(list, item)
	}
	response.Success(c, gin.H{
		"list":         list,
		"total":        resp.Total,
		"undownloaded": resp.Undownloaded,
	})
}

// DownloadOrderExportTask 下载导出文件
// 校验可见性（order 服务）后由 gateway 凭服务端路径流式下发，不经公开静态目录；
// 下载留痕（下载人/次数）在 order 服务 GetExportTaskFile 内记录
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DownloadOrderExportTask(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError, "任务 ID 无效")
		return
	}
	uc := appcontext.GetUserContext(c)
	resp, err := h.orderClient.GetExportTaskFile(c.Request.Context(), &order.GetExportTaskFileReq{
		Id:          id,
		AdminId:     uc.UserID,
		AdminName:   c.GetString("admin_username"),
		SuperAdmin:  gwMiddleware.IsSuperAdmin(c),
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", url.PathEscape(resp.FileName)))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.File(resp.FilePath)
}

// DeleteOrderExportTask 删除导出任务（仅超管，文件一并删除）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteOrderExportTask(c *gin.Context) {
	// 前端仅给超管渲染删除入口，此处服务端再兜底一层（越权直接拒绝）
	if !gwMiddleware.IsSuperAdmin(c) {
		response.Error(c, errcode.NoPermission)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError, "任务 ID 无效")
		return
	}
	if _, err := h.orderClient.DeleteExportTask(c.Request.Context(), &order.DeleteExportTaskReq{Id: id}); err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

// CountOrderExportUndownloaded 未下载任务数（右上角下载图标角标）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CountOrderExportUndownloaded(c *gin.Context) {
	resp, err := h.orderClient.CountUndownloaded(c.Request.Context(), &order.CountUndownloadedReq{
		AdminId:     appcontext.GetUserContext(c).UserID,
		SuperAdmin:  gwMiddleware.IsSuperAdmin(c),
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"count": resp.Count})
}

// batchUserNames 批量取订单列表涉及的会员展示名（昵称优先，回落手机号）
// 社区语境沿用列表过滤口径：communityID>0 时由 user 服务按会员-社区关联过滤，
// 越权/已解绑会员不会回传，对应订单展示空名而非泄露其他社区资料
// 参数：
//   - c: gin 上下文
//   - orders: 订单 proto 列表
//   - communityID: 当前查询的社区语境（0=超管跨社区）
//
// 返回值：
//   - map[int64]string: 会员 ID -> 展示名
//   - error: RPC 失败
func (h *AdminHandler) batchUserNames(c *gin.Context, orders []*order.OrderInfo, communityID int64) (map[int64]string, error) {
	names := make(map[int64]string)
	ids := make([]int64, 0, len(orders))
	for _, o := range orders {
		if o.UserId > 0 {
			ids = append(ids, o.UserId)
		}
	}
	if len(ids) == 0 {
		return names, nil
	}
	resp, err := h.userClient.BatchGetUsers(c.Request.Context(), &user.BatchGetUsersReq{
		Ids: ids, CommunityId: communityID,
	})
	if err != nil {
		return names, err
	}
	for _, u := range resp.List {
		name := u.Nickname
		if name == "" {
			name = u.Phone
		}
		names[u.Id] = name
	}
	return names, nil
}

// batchOrderPayTypes 批量取订单列表涉及的支付方式（0=未支付/未知）
// 参数：
//   - c: gin 上下文
//   - orders: 订单 proto 列表
//
// 返回值：
//   - map[int64]int32: 订单 ID -> 支付类型
//   - error: RPC 失败
func (h *AdminHandler) batchOrderPayTypes(c *gin.Context, orders []*order.OrderInfo) (map[int64]int32, error) {
	payTypes := make(map[int64]int32)
	ids := make([]int64, 0, len(orders))
	for _, o := range orders {
		ids = append(ids, o.Id)
	}
	if len(ids) == 0 {
		return payTypes, nil
	}
	resp, err := h.paymentClient.BatchGetPayments(c.Request.Context(), &payment.BatchGetPaymentsReq{OrderIds: ids})
	if err != nil {
		return payTypes, err
	}
	for _, p := range resp.List {
		payTypes[p.OrderId] = p.PayType
	}
	return payTypes, nil
}

// GetAdminOrder 获取管理端订单详情
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetAdminOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	// 社区隔离：仅可查看自身社区订单（超管不受限）
	if err := h.ensureOrderInCommunity(c, id); err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}

	resp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	o := resp.Order
	userName := ""
	if o != nil && o.UserId > 0 {
		userResp, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{Id: o.UserId})
		if err == nil && userResp != nil {
			userName = userResp.Nickname
			if userName == "" {
				userName = userResp.Phone
			}
		}
	}
	// 支付方式（与列表共用口径；0=未支付/未知）
	payType, payTypeText := int32(0), ""
	if o != nil {
		payType, payTypeText = h.adminOrderPayInfo(c, o.Id)
	}

	address := gin.H{"name": "", "phone": "", "address": ""}
	if o != nil && o.ContactName != "" {
		address["name"] = o.ContactName
		address["phone"] = o.ContactPhone
		address["address"] = o.ContactAddress
	}

	items := make([]gin.H, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, gin.H{
			"id":             item.Id,
			"product_id":     item.ProductId,
			"sku_id":         item.SkuId,
			"product_name":   item.ProductName,
			"sku_name":       item.SpecName,
			"product_image":  item.ProductImage,
			"price":          item.Price,
			"quantity":       item.Quantity,
			"total_amount":   item.TotalPrice,
			"original_price": item.OriginalPrice,
			"price_modified": item.PriceModified,
		})
	}

	statusLogs := make([]gin.H, 0, len(resp.StatusLogs))
	for _, log := range resp.StatusLogs {
		statusLogs = append(statusLogs, gin.H{
			"from_status":   log.FromStatus,
			"to_status":     log.ToStatus,
			"operator_type": log.OperatorType,
			"remark":        log.Remark,
			"created_at":    log.CreatedAt,
		})
	}

	// 售后记录：本单每次退款单（类型/金额/状态/明细），订单详情按次展示
	refunds := make([]gin.H, 0, len(resp.Refunds))
	for _, r := range resp.Refunds {
		refundItems := make([]gin.H, 0, len(r.Items))
		for _, item := range r.Items {
			refundItems = append(refundItems, gin.H{
				"id":            item.Id,
				"refund_id":     item.RefundId,
				"order_item_id": item.OrderItemId,
				"sku_id":        item.SkuId,
				"product_name":  item.ProductName,
				"spec_name":     item.SpecName,
				"quantity":      item.Quantity,
				"refund_amount": item.RefundAmount,
			})
		}
		refunds = append(refunds, gin.H{
			"id":             r.Id,
			"refund_no":      r.RefundNo,
			"order_id":       r.OrderId,
			"order_no":       r.OrderNo,
			"amount":         r.Amount,
			"freight_amount": r.FreightAmount,
			"type":           r.Type,
			"reason":         r.Reason,
			"status":         r.Status,
			"reject_reason":  r.RejectReason,
			"refund_time":    r.RefundTime,
			"created_at":     r.CreatedAt,
			"items":          refundItems,
		})
	}

	// 支付明细与通知时间线（本轮可靠性改造的可观测面）：payment 服务旁路数据，
	// 取回失败不让详情页打不开，只以 payments_error 告知前端「这里查不到」，
	// 静默返回空表会被读成「这笔订单没有支付记录」，那是最坏的误导
	payments, payErr := h.adminOrderPayments(c, id)
	paymentLogs, canViewRaw, logErr := h.adminOrderPaymentLogs(c, id)
	paymentsError, paymentLogsError := "", ""
	if payErr != nil {
		paymentsError = "支付记录读取失败，请稍后重试或查看支付异常页"
	}
	if logErr != nil {
		paymentLogsError = "支付通知记录读取失败，请稍后重试"
	}

	data := gin.H{
		"id":              o.Id,
		"order_no":        o.OrderNo,
		"user_id":         o.UserId,
		"user_name":       userName,
		"total_amount":    o.TotalAmount,
		"discount_amount": o.DiscountAmount,
		"manual_discount": o.ManualDiscount,
		"delivery_fee":    o.DeliveryFee,
		"pay_amount":      o.PayAmount,
		"delivery_type":   o.DeliveryType,
		"delivery_time":   o.DeliveryTime,
		"remark":          o.Remark,
		"status":          o.Status,
		"status_text":     orderStatusText(o.Status, o.DeliveryType),
		"pay_time":        o.PayTime,
		"deliver_time":    o.DeliverTime,
		"complete_time":   o.CompleteTime,
		"cancel_time":     o.CancelTime,
		"cancel_reason":   o.CancelReason,
		"express_company": o.ExpressCompany,
		"express_no":      o.ExpressNo,
		"source":          o.Source,
		// 用户软删标记：详情页据此显示「已被用户删除」并给出恢复入口
		"user_deleted":  o.UserDeleted,
		"operator_name": o.OperatorName,
		"shift_id":      o.ShiftId,
		"pay_type":      payType,
		"pay_type_text": payTypeText,
		"pickup_code":   o.PickupCode,
		// 配送员（履约人员姓名快照）与签收凭证图片（决策点②拍照留证）
		"delivery_user_name":    o.DeliveryUserName,
		"delivery_proof_images": o.DeliveryProofImages,
		"created_at":            o.CreatedAt,
		"items":                 items,
		"address":               address,
		"status_logs":           statusLogs,
		"refunds":               refunds,
		// 支付尝试明细（含失败/关闭）与顶部汇总：attempts/succeeded/amount 一眼看出重复支付
		"payments":        payments,
		"payment_summary": paymentSummaryRows(payments),
		"payments_error":  paymentsError,
		// 通知/对账时间线（含确认成功条目）；raw_body 只在有 payment:raw:view 时才带
		"payment_logs":         paymentLogRows(paymentLogs, canViewRaw),
		"payment_logs_error":   paymentLogsError,
		"can_view_payment_raw": canViewRaw,
	}
	response.Success(c, data)
}

// adminOrderPayments 取订单的全部支付尝试（含失败与已关闭），供详情「支付记录」表渲染
// 参数：
//   - c: gin 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - []gin.H: 支付尝试行，失败时为空切片（调用方以 payments_error 区分「没有」与「查不到」）
//   - error: 支付服务调用错误
func (h *AdminHandler) adminOrderPayments(c *gin.Context, orderID int64) ([]gin.H, error) {
	resp, err := h.paymentClient.ListOrderPayments(c.Request.Context(), &payment.ListOrderPaymentsReq{OrderId: orderID})
	if err != nil {
		return []gin.H{}, err
	}
	rows := make([]gin.H, 0, len(resp.List))
	for _, p := range resp.List {
		rows = append(rows, gin.H{
			"id":                 p.Id,
			"payment_no":         p.PaymentNo,
			"pay_type":           p.PayType,
			"amount":             p.Amount,
			"status":             p.Status,
			"transaction_id":     p.TransactionId,
			"pay_time":           p.PayTime,
			"confirm_source":     p.ConfirmSource,
			"confirm_ref":        p.ConfirmRef,
			"reconcile_count":    p.ReconcileCount,
			"last_reconciled_at": p.LastReconciledAt,
			"created_at":         p.CreatedAt,
		})
	}
	return rows, nil
}

// paymentSummaryRows 汇总支付尝试：共几次、成功几笔、成功金额合计
// succeeded>1 即重复支付（同一订单被扣两次），是详情页要立刻提醒去退款的信号
// 参数：
//   - rows: adminOrderPayments 的结果
//
// 返回值：
//   - gin.H: attempts/succeeded/succeeded_amount 三项计数
func paymentSummaryRows(rows []gin.H) gin.H {
	succeeded, count := 0, 0
	var total float64
	for _, row := range rows {
		if status, ok := row["status"].(int32); ok && status == 2 {
			succeeded++
			if amount, aok := row["amount"].(float64); aok {
				total += amount
			}
		}
		count++
	}
	return gin.H{"attempts": count, "succeeded": succeeded, "succeeded_amount": total}
}

// adminOrderPaymentLogs 取订单的支付通知与对账时间线（include_ok 带上确认成功条目，完整轨迹才有意义）
// 参数：
//   - c: gin 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - []*payment.PaymentAnomalyInfo: 时间线原始行
//   - bool: 当前管理员是否具备查看报文原文权限
//   - error: 支付服务调用错误
func (h *AdminHandler) adminOrderPaymentLogs(c *gin.Context, orderID int64) ([]*payment.PaymentAnomalyInfo, bool, error) {
	canViewRaw := gwMiddleware.HasPermission(c, "payment:raw:view")
	resp, err := h.paymentClient.ListPaymentAnomalies(c.Request.Context(), &payment.ListPaymentAnomaliesReq{
		Handled:     0,
		OrderId:     orderID,
		Page:        1,
		PageSize:    50,
		WithRaw:     canViewRaw,
		IncludeOk:   true,
		CommunityId: h.scopeCommunityID(c),
	})
	if err != nil {
		return nil, canViewRaw, err
	}
	return resp.List, canViewRaw, nil
}

// paymentLogRows 把通知流水行映射为前端字段；无查看原文权限时不下发 raw_body/headers
// 参数：
//   - logs: 流水行
//   - withRaw: 是否具备 payment:raw:view
//
// 返回值：
//   - []gin.H: 时间线行
func paymentLogRows(logs []*payment.PaymentAnomalyInfo, withRaw bool) []gin.H {
	rows := make([]gin.H, 0, len(logs))
	for _, row := range logs {
		item := paymentAnomalyRow(row, withRaw)
		rows = append(rows, item)
	}
	return rows
}

// paymentAnomalyRow 单条支付流水的前端字段映射（异常页与详情时间线共用，两侧字段口径必须一致）
// 参数：
//   - row: 流水行
//   - withRaw: 是否具备 payment:raw:view，决定报文字段是否下发
//
// 返回值：
//   - gin.H: 字段map
func paymentAnomalyRow(row *payment.PaymentAnomalyInfo, withRaw bool) gin.H {
	item := gin.H{
		"id":            row.Id,
		"kind":          row.Kind,
		"result":        row.Result,
		"channel":       row.Channel,
		"payment_no":    row.PaymentNo,
		"order_id":      row.OrderId,
		"order_no":      row.OrderNo,
		"community_id":  row.CommunityId,
		"pay_type":      row.PayType,
		"amount":        row.Amount,
		"error_msg":     row.ErrorMsg,
		"detect_remark": row.DetectRemark,
		"remark":        row.Remark,
		"handled":       row.Handled,
		"created_at":    row.CreatedAt,
		"can_view_raw":  withRaw,
		"has_raw":       row.RawBody != "" || row.Headers != "",
	}
	if withRaw {
		item["raw_body"] = row.RawBody
		item["headers"] = row.Headers
	}
	return item
}

// UpdateOrderStatusReq 更新订单状态请求
type UpdateOrderStatusReq struct {
	Status int32  `json:"status" binding:"required"`
	Remark string `json:"remark"`
}

// UpdateOrderStatus 更新订单状态
// 注意：目标状态 6（取消）被明确拒绝——取消必须走 POST /orders/:id/cancel，
// 以保证库存回补、取消原因/时间写入等取消语义完整执行
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateOrderStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateOrderStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if req.Status == 6 {
		response.Error(c, errcode.ParamError, "取消订单请使用专用取消接口")
		return
	}

	// 社区隔离：仅可操作自身社区订单（超管不受限），并透传社区 ID 供服务端强校验
	if err := h.ensureOrderInCommunity(c, id); err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}

	// 改单护栏：置「已支付」必须以渠道收款事实为前提。
	// 所有订单都有 payments 行（现金/货到付款单创建即成功），所以这里无需任何豁免分支：
	// 查不到成功支付单就说明钱没到账，直接拒绝，再引导到「同步支付状态」/「标记已支付」两个正规入口
	if req.Status == 2 {
		if err := h.ensureOrderHasPaidRecord(c, id); err != nil {
			response.Error(c, errcode.OperationNotAllowed, err.Error())
			return
		}
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:           id,
		Status:       req.Status,
		OperatorType: "admin",
		OperatorId:   uc.UserID,
		Remark:       req.Remark,
		CommunityId:  h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": req.Status})
}

// ensureOrderHasPaidRecord 校验订单已有成功支付单（后台手工置「已支付」的前置条件）
// GetPayment 口径为「该订单最近一笔 status=2 的支付单」，查不到即渠道没确认收款
// 参数：
//   - c: gin 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - error: 无成功支付单或支付服务不可达时返回错误，调用方据此拒绝改单
func (h *AdminHandler) ensureOrderHasPaidRecord(c *gin.Context, orderID int64) error {
	resp, err := h.paymentClient.GetPayment(c.Request.Context(), &payment.GetPaymentReq{OrderId: orderID})
	if err != nil || resp == nil || resp.Status != 2 {
		return errors.New("渠道尚无该订单的收款记录，不能手工置为已支付；请先用「同步支付状态」向渠道核实，" +
			"线下已确认到账再用「标记已支付」")
	}
	return nil
}

// AdminSyncPaymentReq 后台同步支付状态请求（可选指定支付单号）
// 同一订单可能有多次支付尝试，默认核实最近一笔待支付的渠道单；排查特定一笔时带上支付单号
type AdminSyncPaymentReq struct {
	PaymentNo string `json:"payment_no"`
}

// RestoreAdminOrder 后台恢复被用户软删除的订单（客服场景「我删错了，帮我找回上次买的东西」）
// 软删除只影响 C 端列表与角标，数据与状态从未变动，故恢复只是把标记复位。
// 社区隔离沿用 orderInCommunity 的实时口径：社区管理员只能恢复可操作社区的订单。
//
// 接口: POST /api/v1/admin/orders/:id/restore
//
// 参数：
//   - c: gin 上下文，路径 :id 为订单 ID
func (h *AdminHandler) RestoreAdminOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	o, err := h.orderInCommunity(c, id)
	if err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}
	if o.UserDeleted != 1 {
		response.Error(c, errcode.OperationNotAllowed, "该订单未被用户删除")
		return
	}

	if _, err := h.orderClient.RestoreOrder(c.Request.Context(), &order.RestoreOrderReq{
		Id:          id,
		CommunityId: o.CommunityId,
	}); err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// SyncAdminOrderPayment 后台「同步支付状态」：向渠道核实该订单是否已收款，已收款则补同步
// 以渠道为准的最终一致入口，幂等可反复点：渠道未收款时不改动任何本地状态，只回传渠道事实
// 响应字段用 gin.H 显式列出，paid=false/amount=0 这类零值不会被 omitempty 吞掉
//
// 接口: POST /api/v1/admin/orders/:id/payment-sync
//
// 参数：
//   - c: gin 上下文，路径 :id 为订单 ID
func (h *AdminHandler) SyncAdminOrderPayment(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	// 社区隔离 + 读出订单：待支付族走「核实并补同步」；已取消单走「渠道核实挂账」——
	// 只查渠道 + 产出人工退款待办，不改任何资金状态（订单状态机不允许 6→2）
	o, err := h.orderInCommunity(c, id)
	if err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}
	if o.Status != 1 && o.Status != 100 && o.Status != 6 {
		response.Error(c, errcode.OperationNotAllowed, "订单当前状态无需同步支付状态")
		return
	}

	var req AdminSyncPaymentReq
	// 允许空 body：payment_no 只是可选定位条件，绑定失败按空值继续
	_ = c.ShouldBindJSON(&req)

	// reason 留空即由支付服务按 manual-sync 记入 payments.confirm_ref（区别于调度器的 cancel-guard）
	// 操作人随请求下发（B3）：挂账待办与核实流水要能回答「谁点的按钮」
	uc := appcontext.GetUserContext(c)
	resp, err := h.paymentClient.SyncPaymentFromChannel(c.Request.Context(), &payment.SyncPaymentReq{
		OrderId:      id,
		PaymentNo:    req.PaymentNo,
		OperatorId:   uc.UserID,
		OperatorName: c.GetString("admin_username"),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"paid":           resp.Paid,
		"changed":        resp.Changed,
		"channel_state":  resp.ChannelState,
		"transaction_id": resp.TransactionId,
		"amount":         resp.Amount,
		"payment_no":     resp.PaymentNo,
		"message":        resp.Message,
	})
}

// AdminMarkPaidReq 后台人工「标记已支付」入参
// 渠道交易号与核实依据均必填：这条入口绕过了渠道核实，全靠留痕保证事后可追责
type AdminMarkPaidReq struct {
	PaymentNo     string  `json:"payment_no" binding:"required"`
	TransactionID string  `json:"transaction_id" binding:"required"`
	Amount        float64 `json:"amount" binding:"required"`
	Remark        string  `json:"remark" binding:"required"`
}

// MarkAdminOrderPaid 后台人工把指定待支付单标记为已支付
// 用于「渠道查不到或接口不可用、但线下已核实到账」的死角；服务端会把订单一并流转为已支付，
// 因此只允许待支付（1）订单使用——已取消订单（6→2）在订单状态机上必然失败，只会造出半截数据，
// 那种挂账只能退款，入口在支付异常页
//
// 接口: POST /api/v1/admin/orders/:id/payment-mark-paid
//
// 参数：
//   - c: gin 上下文，路径 :id 为订单 ID
func (h *AdminHandler) MarkAdminOrderPaid(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req AdminMarkPaidReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError, "支付单号、渠道交易号、收款金额与核实依据均必填")
		return
	}

	o, err := h.orderInCommunity(c, id)
	if err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}
	if o.Status != 1 {
		response.Error(c, errcode.OperationNotAllowed,
			"仅待支付订单可标记已支付；订单已取消而渠道已收款属资金挂账，请在支付异常页走退款")
		return
	}

	uc := appcontext.GetUserContext(c)
	remark := strings.TrimSpace(req.Remark) + "（操作人 admin:" + strconv.FormatInt(uc.UserID, 10) + "）"
	// OrderId 传路径订单：服务层据此校验支付单归属（P1-2），
	// 网关的社区隔离只覆盖路径订单，管不住 body 里抄错的支付单号
	if _, err := h.paymentClient.MarkPaymentPaid(c.Request.Context(), &payment.MarkPaymentPaidReq{
		OrderId:       id,
		PaymentNo:     req.PaymentNo,
		TransactionId: req.TransactionID,
		Amount:        req.Amount,
		Remark:        remark,
	}); err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "payment_no": req.PaymentNo})
}

// ListAdminPaymentAnomalies 支付异常/对账流水队列
// 两个入口共用：异常页（handled/order_id 筛选）与订单详情的通知时间线（include_ok=true 看全轨迹）
// 敏感边界：raw_body 与 headers 只在具备 payment:raw:view 时回传（该权限点刻意不进社区管理员默认上限，
// 故实际只有超管可见）；社区范围由 scopeCommunityID 下发，超管为 0 表示看全量
//
// 接口: GET /api/v1/admin/payments/anomalies
//
// 参数：
//   - c: gin 上下文，query 支持 handled/order_id/page/page_size/include_ok
func (h *AdminHandler) ListAdminPaymentAnomalies(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	handled, _ := strconv.Atoi(c.DefaultQuery("handled", "0"))
	includeOK := c.Query("include_ok") == "true" || c.Query("include_ok") == "1"
	var orderID int64
	if v, err := strconv.ParseInt(c.Query("order_id"), 10, 64); err == nil && v > 0 {
		orderID = v
	}
	req := &payment.ListPaymentAnomaliesReq{
		Handled:     int32(handled),
		OrderId:     orderID,
		Page:        int32(page),
		PageSize:    int32(pageSize),
		CommunityId: h.scopeCommunityID(c),
		WithRaw:     gwMiddleware.HasPermission(c, "payment:raw:view"),
		IncludeOk:   includeOK,
	}
	resp, err := h.paymentClient.ListPaymentAnomalies(c.Request.Context(), req)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"list":            paymentLogRows(resp.List, req.WithRaw),
		"total":           resp.Total,
		"pending":         resp.Pending,
		"can_view_raw":    req.WithRaw,
		"community_scope": req.CommunityId,
	})
}

// AdminHandlePaymentAnomalyReq 异常销账请求
type AdminHandlePaymentAnomalyReq struct {
	Remark string `json:"remark" binding:"required"`
}

// HandleAdminPaymentAnomaly 支付异常销账（人工在渠道侧核对完成后关闭待办）
// 参数：
//   - c: gin 上下文，路径 :id 为流水主键，body 需带核实依据
func (h *AdminHandler) HandleAdminPaymentAnomaly(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	var req AdminHandlePaymentAnomalyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError, "请填写核实依据")
		return
	}
	if _, err := h.paymentClient.MarkPaymentAnomalyHandled(c.Request.Context(), &payment.MarkPaymentAnomalyReq{
		Id:          id,
		Remark:      req.Remark,
		CommunityId: h.scopeCommunityID(c),
	}); err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// AdminCancelOrderReq 后台取消订单请求
type AdminCancelOrderReq struct {
	Reason string `json:"reason" binding:"required"`
}

// AdminCancelOrder 后台取消订单
// 走 order-service 的取消语义：条件更新状态、写取消原因/时间并逐行回补库存
//
// 接口: POST /api/v1/admin/orders/:id/cancel
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) AdminCancelOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req AdminCancelOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError, "请填写取消原因")
		return
	}

	// 社区隔离：仅可操作自身社区订单（超管不受限），并透传社区 ID 供服务端强校验
	if err := h.ensureOrderInCommunity(c, id); err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:           id,
		Status:       6,
		OperatorType: "admin",
		OperatorId:   uc.UserID,
		Remark:       req.Reason,
		CommunityId:  h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": 6})
}

// ShipOrderReq 订单发货请求
type ShipOrderReq struct {
	ExpressCompany string `json:"express_company"`
	ExpressNo      string `json:"express_no"`
	Remark         string `json:"remark"`
}

// ShipOrder 订单发货
// 校验订单为配送单且状态为待发货（status=2）后，将订单状态更新为已发货（status=4），
// 物流公司/单号写入独立字段（express_company/express_no），不再拼接进备注
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ShipOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req ShipOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 社区隔离：仅可操作自身社区订单（超管不受限），并透传社区 ID 供服务端强校验
	if err := h.ensureOrderInCommunity(c, id); err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}

	orderResp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: id})
	if err != nil || orderResp.Order == nil {
		response.Error(c, errcode.NotFound, "订单不存在")
		return
	}
	if orderResp.Order.DeliveryType == 1 {
		response.Error(c, errcode.ParamError, "自提订单无需发货，请直接核销完成")
		return
	}
	if orderResp.Order.Status != 2 {
		response.Error(c, errcode.ParamError, "订单状态不允许发货")
		return
	}

	expressCompany := strings.TrimSpace(req.ExpressCompany)
	expressNo := strings.TrimSpace(req.ExpressNo)
	if expressCompany == "" || expressNo == "" {
		response.Error(c, errcode.ParamError, "物流公司和物流单号不能为空")
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:             id,
		Status:         4,
		OperatorType:   "admin",
		OperatorId:     uc.UserID,
		Remark:         req.Remark,
		ExpressCompany: expressCompany,
		ExpressNo:      expressNo,
		CommunityId:    h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": 4})
}

// CompleteOrder 完成订单
// 配送单须为已发货（status=4）；自提单允许在已支付（status=2 且 delivery_type=1）时直接核销完成，
// 与 C 端确认收货（handler/order.go ConfirmReceive）口径一致
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CompleteOrder(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	// 社区隔离：仅可操作自身社区订单（超管不受限），并透传社区 ID 供服务端强校验
	if err := h.ensureOrderInCommunity(c, id); err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}

	orderResp, err := h.orderClient.GetOrder(c.Request.Context(), &order.GetOrderReq{Id: id})
	if err != nil || orderResp.Order == nil {
		response.Error(c, errcode.NotFound, "订单不存在")
		return
	}
	o := orderResp.Order
	if o.Status != 4 && (o.Status != 2 || o.DeliveryType != 1) {
		response.Error(c, errcode.ParamError, "订单状态不允许完成")
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.UpdateOrderStatus(c.Request.Context(), &order.UpdateOrderStatusReq{
		Id:           id,
		Status:       5,
		OperatorType: "admin",
		OperatorId:   uc.UserID,
		Remark:       "订单完成",
		CommunityId:  h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": 5})
}

// --- 仪表盘与统计报表 ---

// GetDashboard 获取仪表盘聚合数据
// 聚合今日订单数、今日销售额、注册用户数、在售商品数、待发货订单数、待处理退款数
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetDashboard(c *gin.Context) {
	ctx := c.Request.Context()
	communityID := h.getAdminCommunityID(c)
	today := time.Now().Format("2006-01-02")

	orderStats, err := h.orderClient.GetOrderStatistics(ctx, &order.GetOrderStatisticsReq{
		CommunityId: communityID,
		StartDate:   today,
		EndDate:     today,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	salesStats, err := h.orderClient.GetSalesStatistics(ctx, &order.GetSalesStatisticsReq{
		CommunityId: communityID,
		StartDate:   today,
		EndDate:     today,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	userResp, err := h.userClient.ListUsers(ctx, &user.ListUsersReq{
		CommunityId: communityID,
		Pagination:  &common.Pagination{Page: 1, PageSize: 1},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	productResp, err := h.productClient.ListProducts(ctx, &product.ListProductsReq{
		CommunityId: communityID,
		Status:      1,
		Pagination:  &common.Pagination{Page: 1, PageSize: 1},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	response.Success(c, gin.H{
		"order_count":   orderStats.TotalOrders,
		"sale_amount":   salesStats.PayAmount,
		"user_count":    userResp.Pagination.Total,
		"product_count": productResp.Pagination.Total,
		"pending_ship":  orderStats.PendingShip,
		"refunding":     orderStats.Refunding,
	})
}

// resolveStatsCommunityID 统计接口的社区口径解析
// 默认取管理员当前社区；超管可通过查询参数显式指定口径：
//   - scope=all        → 0（全平台聚合，order 服务对 community_id<=0 不过滤）
//   - community_id=N>0 → 指定社区（用于全平台统计页的社区排行）
//
// 普通管理员的传参一律忽略（回落当前社区），避免越权查看其他社区数据
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) resolveStatsCommunityID(c *gin.Context) int64 {
	if gwMiddleware.IsSuperAdmin(c) {
		if c.Query("scope") == "all" {
			return 0
		}
		if id, err := strconv.ParseInt(c.Query("community_id"), 10, 64); err == nil && id > 0 {
			return id
		}
	}
	return h.getAdminCommunityID(c)
}

// GetOrderStatistics 获取订单状态统计
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetOrderStatistics(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	resp, err := h.orderClient.GetOrderStatistics(c.Request.Context(), &order.GetOrderStatisticsReq{
		CommunityId: h.resolveStatsCommunityID(c),
		StartDate:   startDate,
		EndDate:     endDate,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// GetSalesStatistics 获取销售统计
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetSalesStatistics(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	resp, err := h.orderClient.GetSalesStatistics(c.Request.Context(), &order.GetSalesStatisticsReq{
		CommunityId: h.resolveStatsCommunityID(c),
		StartDate:   startDate,
		EndDate:     endDate,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// GetProductRankings 获取商品销售排行
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetProductRankings(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	resp, err := h.orderClient.GetProductRankings(c.Request.Context(), &order.GetProductRankingsReq{
		CommunityId: h.resolveStatsCommunityID(c),
		StartDate:   startDate,
		EndDate:     endDate,
		Limit:       int32(limit),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// GetRevenueReport 获取营收报表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetRevenueReport(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	groupBy := c.DefaultQuery("group_by", "day")

	resp, err := h.orderClient.GetRevenueReport(c.Request.Context(), &order.GetRevenueReportReq{
		CommunityId: h.resolveStatsCommunityID(c),
		StartDate:   startDate,
		EndDate:     endDate,
		GroupBy:     groupBy,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// --- 库存管理 ---

// ListStockRecords 获取库存流水列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListStockRecords(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	skuID, _ := strconv.ParseInt(c.DefaultQuery("sku_id", "0"), 10, 64)
	recordType, _ := strconv.Atoi(c.DefaultQuery("type", "0"))
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	resp, err := h.productClient.ListStockRecords(c.Request.Context(), &product.ListStockRecordsReq{
		CommunityId: h.getAdminCommunityID(c),
		SkuId:       skuID,
		Type:        int32(recordType),
		StartDate:   startDate,
		EndDate:     endDate,
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

// GetStockWarnings 获取库存预警列表
// 预警为运营告警清单，需全量返回（RPC 层即无分页语义）；
// 历史问题：曾在此临时分页（默认 page_size=10），导致前端仪表盘计数恒 0、
// 工作台表格拿到 {list,total} 包装对象而非数组、调用方不传分页参数时丢数据
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetStockWarnings(c *gin.Context) {
	resp, err := h.productClient.GetStockWarnings(c.Request.Context(), &product.GetStockWarningsReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	list := resp.List
	if list == nil {
		list = []*product.StockWarningInfo{}
	}
	response.Success(c, list)
}

// ListSkuStock 获取当前社区 SKU 库存列表
// 走 product 服务 SearchSkus（keyword 匹配商品名/编码/条形码，DB 级分页）。
// 历史实现为 gateway 侧组合查询：先拉 1000 个商品再逐个 ListSkus（N+1），且不读 keyword 参数，
// 导致工作台关键字搜索为空操作、商品量大时接口变慢，2026-10-18 改造根治
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListSkuStock(c *gin.Context) {
	page, pageSize := parsePageParams(c, 20)
	keyword := strings.TrimSpace(c.Query("keyword"))

	resp, err := h.productClient.SearchSkus(c.Request.Context(), &product.SearchSkusReq{
		CommunityId: h.getAdminCommunityID(c),
		Keyword:     keyword,
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

// ListStockChecks 获取库存盘点列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListStockChecks(c *gin.Context) {
	page, pageSize := parsePageParams(c)

	resp, err := h.productClient.ListStockChecks(c.Request.Context(), &product.ListStockChecksReq{
		CommunityId: h.getAdminCommunityID(c),
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

// GetStockCheckDetail 获取盘点单详情
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetStockCheckDetail(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.GetStockCheck(c.Request.Context(), &product.GetStockCheckReq{
		Id:          id,
		CommunityId: h.scopeCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// CreateStockCheckReq 创建盘点单请求
type CreateStockCheckReq struct {
	Remark string `json:"remark"`
}

// CreateStockCheck 创建盘点单
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateStockCheck(c *gin.Context) {
	var req CreateStockCheckReq
	_ = c.ShouldBindJSON(&req)

	uc := appcontext.GetUserContext(c)
	check, err := h.productClient.CreateStockCheck(c.Request.Context(), &product.CreateStockCheckReq{
		CommunityId: h.getAdminCommunityID(c),
		OperatorId:  uc.UserID,
		Remark:      req.Remark,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, check)
}

// GeneratePurchaseOrder 生成补货建议单
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GeneratePurchaseOrder(c *gin.Context) {
	resp, err := h.productClient.GeneratePurchaseOrder(c.Request.Context(), &product.GeneratePurchaseOrderReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.Items)
}

// SubmitStockCheckReq 提交盘点单请求
type SubmitStockCheckReq struct {
	Items []struct {
		SkuID       int64 `json:"sku_id" binding:"required"`
		ActualStock int32 `json:"actual_stock" binding:"required"`
	} `json:"items" binding:"required"`
}

// SubmitStockCheck 提交盘点单
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) SubmitStockCheck(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req SubmitStockCheckReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	items := make([]*product.StockCheckItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, &product.StockCheckItem{
			SkuId:       item.SkuID,
			ActualStock: item.ActualStock,
		})
	}

	_, err = h.productClient.SubmitStockCheck(c.Request.Context(), &product.SubmitStockCheckReq{
		Id:          id,
		Items:       items,
		CommunityId: h.scopeCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// --- 营销管理 ---

// ListAdminCoupons 获取管理端优惠券列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminCoupons(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))
	resp, err := h.productClient.ListCoupons(c.Request.Context(), &product.ListCouponsReq{
		CommunityId: h.getAdminCommunityID(c),
		Status:      int32(status),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, len(resp.List), page, pageSize)
}

// CouponScopeInput 优惠券商品范围入参
// scope_type：2指定分类 3指定商品 4排除分类 5排除商品（1全场=不传范围行）
type CouponScopeInput struct {
	ScopeType  int32 `json:"scope_type"`
	CategoryID int64 `json:"category_id"`
	ProductID  int64 `json:"product_id"`
}

// CreateCouponReq 创建优惠券请求
type CreateCouponReq struct {
	Name            string             `json:"name" binding:"required"`
	Type            int32              `json:"type" binding:"required"`
	Threshold       float64            `json:"threshold"`
	DiscountValue   float64            `json:"discount_value" binding:"required"`
	TotalCount      int32              `json:"total_count" binding:"required"`
	PerUserLimit    int32              `json:"per_user_limit"`
	StartTime       string             `json:"start_time"`
	EndTime         string             `json:"end_time"`
	ValidDays       int32              `json:"valid_days"`
	PublishStatus   int32              `json:"publish_status"` // 0草稿（默认）1直接发布
	IsCenterVisible *bool              `json:"is_center_visible"`
	ExpireType      int32              `json:"expire_type"` // 1领取后N天 2指定可用范围
	UseStartTime    string             `json:"use_start_time"`
	UseEndTime      string             `json:"use_end_time"`
	RuleText        string             `json:"rule_text"`
	Scopes          []CouponScopeInput `json:"scopes"`
}

// CreateCoupon 创建优惠券
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateCoupon(c *gin.Context) {
	var req CreateCouponReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateCoupon(c.Request.Context(), &product.CreateCouponReq{
		CommunityId:     h.getAdminCommunityID(c),
		Name:            req.Name,
		Type:            req.Type,
		Threshold:       req.Threshold,
		DiscountValue:   req.DiscountValue,
		TotalCount:      req.TotalCount,
		PerUserLimit:    req.PerUserLimit,
		StartTime:       req.StartTime,
		EndTime:         req.EndTime,
		ValidDays:       req.ValidDays,
		PublishStatus:   req.PublishStatus,
		IsCenterVisible: req.IsCenterVisible,
		ExpireType:      req.ExpireType,
		UseStartTime:    req.UseStartTime,
		UseEndTime:      req.UseEndTime,
		RuleText:        req.RuleText,
		Scopes:          toCouponScopes(req.Scopes),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateCouponReq 更新优惠券请求
type UpdateCouponReq struct {
	Name            string              `json:"name"`
	Status          int32               `json:"status"`
	Type            int32               `json:"type"`
	Threshold       float64             `json:"threshold"`
	DiscountValue   float64             `json:"discount_value"`
	TotalCount      *int32              `json:"total_count"` // 补充数量
	PerUserLimit    int32               `json:"per_user_limit"`
	StartTime       string              `json:"start_time"`
	EndTime         string              `json:"end_time"`
	ValidDays       int32               `json:"valid_days"`
	PublishStatus   int32               `json:"publish_status"` // 发布/下架状态机
	GrantStatus     *int32              `json:"grant_status"`   // 0停止发放 1恢复发放；nil=不修改
	IsCenterVisible *bool               `json:"is_center_visible"`
	ExpireType      int32               `json:"expire_type"`
	UseStartTime    string              `json:"use_start_time"`
	UseEndTime      string              `json:"use_end_time"`
	RuleText        *string             `json:"rule_text"` // 指针区分未传与清空
	Scopes          *[]CouponScopeInput `json:"scopes"`    // nil=不修改；空数组=清空（恢复全场）
}

// UpdateCoupon 更新优惠券（编辑放开，决策点⑧）：
// 草稿态全字段可编辑；已发布/已下架仅允许「补充数量、停止/恢复发放、规则文案」，
// 面额/门槛/类型/时间/范围锁定（已领取的 user_coupon 是快照，不受模板改动影响）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateCoupon(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateCouponReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 读当前券判断发布状态，按状态裁剪可编辑字段
	cur, err := h.productClient.GetCoupon(c.Request.Context(), &product.GetCouponReq{Id: id, CommunityId: h.scopeCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	// 社区隔离：仅允许操作本社区语境下的券（网关第一道，服务端另有强校验）
	scope := h.scopeCommunityID(c)
	if scope > 0 && cur.CommunityId != scope {
		response.Error(c, errcode.NoPermission, "无权操作其他社区的优惠券")
		return
	}
	isDraft := cur.PublishStatus == 0

	rpcReq := &product.UpdateCouponReq{
		Id:          id,
		CommunityId: scope,
		// Status 启停已由「发布状态机」取代，不再单独透传
	}
	// 全状态通用：发布/下架流转、发放启停、规则文案、启停
	rpcReq.PublishStatus = req.PublishStatus
	rpcReq.GrantStatus = req.GrantStatus
	if req.RuleText != nil {
		rpcReq.RuleText = *req.RuleText
	}

	if isDraft {
		// 草稿态：全字段可编辑
		rpcReq.Name = req.Name
		rpcReq.Type = req.Type
		rpcReq.Threshold = req.Threshold
		rpcReq.DiscountValue = req.DiscountValue
		rpcReq.PerUserLimit = req.PerUserLimit
		rpcReq.StartTime = req.StartTime
		rpcReq.EndTime = req.EndTime
		rpcReq.ValidDays = req.ValidDays
		rpcReq.IsCenterVisible = req.IsCenterVisible
		rpcReq.ExpireType = req.ExpireType
		rpcReq.UseStartTime = req.UseStartTime
		rpcReq.UseEndTime = req.UseEndTime
		if req.Scopes != nil {
			rpcReq.Scopes = toCouponScopes(*req.Scopes)
		}
	}
	// 补充数量：仅允许增加，全状态可用
	if req.TotalCount != nil {
		rpcReq.TotalCount = *req.TotalCount
	}

	resp, err := h.productClient.UpdateCoupon(c.Request.Context(), rpcReq)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// toCouponScopes 优惠券范围入参转 proto
func toCouponScopes(inputs []CouponScopeInput) []*product.CouponScopeReq {
	if len(inputs) == 0 {
		return nil
	}
	scopes := make([]*product.CouponScopeReq, 0, len(inputs))
	for _, in := range inputs {
		scopes = append(scopes, &product.CouponScopeReq{
			ScopeType:  in.ScopeType,
			CategoryId: in.CategoryID,
			ProductId:  in.ProductID,
		})
	}
	return scopes
}

// DeleteCoupon 删除优惠券
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteCoupon(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.DeleteCoupon(c.Request.Context(), &product.DeleteCouponReq{Id: id, CommunityId: h.getAdminCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// GrantCouponReq 手动发放优惠券请求
type GrantCouponReq struct {
	UserIDs []int64 `json:"user_ids" binding:"required,min=1"`
}

// GrantCoupon 手动发放优惠券（M②期：后台选会员批量发券，逐人独立校验）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GrantCoupon(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	var req GrantCouponReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	uc := appcontext.GetUserContext(c)
	resp, err := h.productClient.GrantCoupon(c.Request.Context(), &product.GrantCouponReq{
		CouponId:     id,
		UserIds:      req.UserIDs,
		OperatorId:   uc.UserID,
		OperatorName: c.GetString("admin_username"),
		CommunityId:  h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	// failures 为空时输出空数组，避免 JSON null
	failures := resp.Failures
	if failures == nil {
		failures = make([]*product.CouponGrantFailure, 0)
	}
	response.Success(c, gin.H{
		"granted_count": resp.GrantedCount,
		"failures":      failures,
	})
}

// ListCouponRecords 领取/发放记录（user_coupons 按当前社区语境过滤）
// 会员昵称/手机号整页一次 BatchGetUsers 批量回填（与订单列表同一模式）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListCouponRecords(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	couponID, _ := strconv.ParseInt(c.Query("coupon_id"), 10, 64)
	userID, _ := strconv.ParseInt(c.Query("user_id"), 10, 64)
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))
	source, _ := strconv.Atoi(c.DefaultQuery("source", "0"))

	resp, err := h.productClient.ListCouponRecords(c.Request.Context(), &product.ListCouponRecordsReq{
		CouponId:    couponID,
		UserId:      userID,
		Status:      int32(status),
		Source:      int32(source),
		CommunityId: h.getAdminCommunityID(c),
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	// 回填会员昵称/手机号：原先逐行 GetUserById（同人缓存只去重，一页仍有 N 次往返），
	// 改为整页一次 BatchGetUsers；取不到的会员留空，不阻断列表
	ids := make([]int64, 0, len(resp.List))
	for _, r := range resp.List {
		if r.UserId > 0 {
			ids = append(ids, r.UserId)
		}
	}
	if len(ids) > 0 {
		users, err := h.userClient.BatchGetUsers(c.Request.Context(), &user.BatchGetUsersReq{
			Ids: ids, CommunityId: h.getAdminCommunityID(c),
		})
		if err != nil {
			log.Printf("[admin] 批量取会员失败: %v", err)
		} else {
			byID := make(map[int64]*user.UserInfo, len(users.List))
			for _, u := range users.List {
				byID[u.Id] = u
			}
			for _, r := range resp.List {
				if u := byID[r.UserId]; u != nil {
					r.UserNickname = u.Nickname
					r.UserPhone = u.Phone
				}
			}
		}
	}

	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}

// ListAdminFlashSales 获取管理端秒杀活动列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminFlashSales(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	resp, err := h.productClient.ListFlashSales(c.Request.Context(), &product.ListFlashSalesReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, len(resp.List), page, pageSize)
}

// CreateFlashSaleReq 创建秒杀活动请求
type CreateFlashSaleReq struct {
	Name      string                      `json:"name" binding:"required"`
	StartTime string                      `json:"start_time" binding:"required"`
	EndTime   string                      `json:"end_time" binding:"required"`
	Items     []*product.FlashSaleItemReq `json:"items" binding:"required"`
}

// CreateFlashSale 创建秒杀活动
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateFlashSale(c *gin.Context) {
	var req CreateFlashSaleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateFlashSale(c.Request.Context(), &product.CreateFlashSaleReq{
		CommunityId: h.getAdminCommunityID(c),
		Name:        req.Name,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Items:       req.Items,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateFlashSaleReq 更新秒杀活动请求
type UpdateFlashSaleReq struct {
	Name      string `json:"name"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Status    int32  `json:"status"`
}

// UpdateFlashSale 更新秒杀活动
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateFlashSale(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateFlashSaleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.UpdateFlashSale(c.Request.Context(), &product.UpdateFlashSaleReq{
		Id:          id,
		Name:        req.Name,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Status:      req.Status,
		CommunityId: h.scopeCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListFullReductions 获取满减活动列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListFullReductions(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	resp, err := h.productClient.ListFullReductions(c.Request.Context(), &product.ListFullReductionsReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, len(resp.List), page, pageSize)
}

// CreateFullReductionReq 创建满减活动请求
type CreateFullReductionReq struct {
	Name         string  `json:"name" binding:"required"`
	Threshold    float64 `json:"threshold" binding:"required"`
	ReduceAmount float64 `json:"reduce_amount" binding:"required"`
	StartTime    string  `json:"start_time"`
	EndTime      string  `json:"end_time"`
}

// CreateFullReduction 创建满减活动
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateFullReduction(c *gin.Context) {
	var req CreateFullReductionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateFullReduction(c.Request.Context(), &product.CreateFullReductionReq{
		CommunityId:  h.getAdminCommunityID(c),
		Name:         req.Name,
		Threshold:    req.Threshold,
		ReduceAmount: req.ReduceAmount,
		StartTime:    req.StartTime,
		EndTime:      req.EndTime,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateFullReductionReq 更新满减活动请求
type UpdateFullReductionReq struct {
	Name         string  `json:"name"`
	Threshold    float64 `json:"threshold"`
	ReduceAmount float64 `json:"reduce_amount"`
	StartTime    string  `json:"start_time"`
	EndTime      string  `json:"end_time"`
	Status       int32   `json:"status"`
}

// UpdateFullReduction 更新满减活动
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateFullReduction(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateFullReductionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.UpdateFullReduction(c.Request.Context(), &product.UpdateFullReductionReq{
		Id:           id,
		Name:         req.Name,
		Threshold:    req.Threshold,
		ReduceAmount: req.ReduceAmount,
		StartTime:    req.StartTime,
		EndTime:      req.EndTime,
		Status:       req.Status,
		// 社区隔离：服务端校验活动归属社区，跨社区修改直接拒绝
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// --- 满额免配送费管理 ---

// FreeShippingScopeInput 免配送费排除范围入参
type FreeShippingScopeInput struct {
	ScopeType  int32 `json:"scope_type"` // 4=排除分类 5=排除商品
	CategoryID int64 `json:"category_id"`
	ProductID  int64 `json:"product_id"`
}

// ListFreeShippings 获取免配送费活动列表（含排除范围）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListFreeShippings(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	resp, err := h.productClient.ListFreeShippings(c.Request.Context(), &product.ListFreeShippingsReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, len(resp.List), page, pageSize)
}

// CreateFreeShippingReq 创建免配送费活动请求
type CreateFreeShippingReq struct {
	Name      string                   `json:"name" binding:"required"`
	Threshold float64                  `json:"threshold" binding:"required"`
	StartTime string                   `json:"start_time"`
	EndTime   string                   `json:"end_time"`
	Scopes    []FreeShippingScopeInput `json:"scopes"`
}

// CreateFreeShipping 创建免配送费活动
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateFreeShipping(c *gin.Context) {
	var req CreateFreeShippingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.productClient.CreateFreeShipping(c.Request.Context(), &product.CreateFreeShippingReq{
		CommunityId: h.getAdminCommunityID(c),
		Name:        req.Name,
		Threshold:   req.Threshold,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Scopes:      toFreeShippingScopes(req.Scopes),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateFreeShippingReq 更新免配送费活动请求
type UpdateFreeShippingReq struct {
	Name      string                    `json:"name"`
	Threshold float64                   `json:"threshold"`
	StartTime string                    `json:"start_time"`
	EndTime   string                    `json:"end_time"`
	Status    int32                     `json:"status"`
	Scopes    *[]FreeShippingScopeInput `json:"scopes"` // nil=不修改范围；空数组=清空范围
}

// UpdateFreeShipping 更新免配送费活动（支持启停/编辑/排除范围全量替换）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateFreeShipping(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateFreeShippingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	rpcReq := &product.UpdateFreeShippingReq{
		Id:        id,
		Name:      req.Name,
		Threshold: req.Threshold,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Status:    req.Status,
		// 社区隔离：服务端校验活动归属社区，跨社区修改直接拒绝
		CommunityId: h.getAdminCommunityID(c),
	}
	// 区分「不传 scopes」与「传空数组」：仅前者跳过范围替换
	if req.Scopes != nil {
		rpcReq.Scopes = toFreeShippingScopes(*req.Scopes)
	}
	resp, err := h.productClient.UpdateFreeShipping(c.Request.Context(), rpcReq)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// toFreeShippingScopes 入参范围列表转 proto
// 参数：
//   - inputs: 入参列表
//
// 返回值：
//   - []*product.FreeShippingScopeReq: proto 范围列表
func toFreeShippingScopes(inputs []FreeShippingScopeInput) []*product.FreeShippingScopeReq {
	if len(inputs) == 0 {
		return nil
	}
	scopes := make([]*product.FreeShippingScopeReq, 0, len(inputs))
	for _, in := range inputs {
		scopes = append(scopes, &product.FreeShippingScopeReq{
			ScopeType:  in.ScopeType,
			CategoryId: in.CategoryID,
			ProductId:  in.ProductID,
		})
	}
	return scopes
}

// --- Banner 管理 ---

// ListAdminBanners 获取 Banner 列表
// 管理态返回全部记录，支持按位置筛选；
// 仅超管可显式传 community_id（0=仅全局 banner，系统管理语境使用），非超管忽略该参数，
// 其余场景按当前社区语境查询（含全局兜底）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminBanners(c *gin.Context) {
	communityID := h.getAdminCommunityID(c)
	if v := c.Query("community_id"); v != "" && gwMiddleware.IsSuperAdmin(c) {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id >= 0 {
			communityID = id
		}
	}
	position, _ := strconv.Atoi(c.DefaultQuery("position", "0"))

	resp, err := h.productClient.ListBanners(c.Request.Context(), &product.ListBannersReq{
		CommunityId: communityID,
		Position:    int32(position),
		IsAdmin:     true,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithList(c, resp.List)
}

// CreateBannerReq 创建 Banner 请求
type CreateBannerReq struct {
	Title     string `json:"title"`
	ImageURL  string `json:"image_url" binding:"required"`
	LinkURL   string `json:"link_url"`
	Position  int32  `json:"position" binding:"required"`
	Sort      int32  `json:"sort"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	// 适用社区（仅超管可指定；0=全部社区通用；不传=当前社区语境）
	CommunityID *int64 `json:"community_id"`
}

// CreateBanner 创建 Banner
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateBanner(c *gin.Context) {
	var req CreateBannerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 适用社区：默认当前社区语境；超管显式传 community_id 时以其为准（0=全部社区）
	communityID := h.getAdminCommunityID(c)
	if req.CommunityID != nil && gwMiddleware.IsSuperAdmin(c) {
		communityID = *req.CommunityID
	}

	resp, err := h.productClient.CreateBanner(c.Request.Context(), &product.CreateBannerReq{
		CommunityId: communityID,
		Title:       req.Title,
		ImageUrl:    req.ImageURL,
		LinkUrl:     req.LinkURL,
		Position:    req.Position,
		Sort:        req.Sort,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateBannerReq 更新 Banner 请求
type UpdateBannerReq struct {
	Title     string `json:"title"`
	ImageURL  string `json:"image_url"`
	LinkURL   string `json:"link_url"`
	Position  int32  `json:"position"`
	Sort      int32  `json:"sort"`
	Status    int32  `json:"status"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	// 适用社区（仅超管可指定；0=全部社区通用；不传=不修改）
	CommunityID *int64 `json:"community_id"`
}

// UpdateBanner 更新 Banner
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateBanner(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateBannerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 适用社区仅超管显式传参时透传（optional 语义：nil=不修改，0=全部社区）
	var communityID *int64
	if req.CommunityID != nil && gwMiddleware.IsSuperAdmin(c) {
		communityID = req.CommunityID
	}

	resp, err := h.productClient.UpdateBanner(c.Request.Context(), &product.UpdateBannerReq{
		Id:          id,
		Title:       req.Title,
		ImageUrl:    req.ImageURL,
		LinkUrl:     req.LinkURL,
		Position:    req.Position,
		Sort:        req.Sort,
		Status:      req.Status,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		CommunityId: communityID,

		ScopeCommunityId: h.scopeCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteBanner 删除 Banner
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteBanner(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.productClient.DeleteBanner(c.Request.Context(), &product.DeleteBannerReq{Id: id, CommunityId: h.scopeCommunityID(c)})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// --- 社区管理 ---

// MyCommunities 当前账号可进入的社区列表（社区选择页用，仅要求登录）
// 普通管理员返回其任职社区；超管返回全部社区（含停业，前端展示状态标识）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) MyCommunities(c *gin.Context) {
	// 超管判定与任职均取实时口径：本接口只要求登录、不挂权限码，
	// 照抄登录快照会把降权前的超管身份与已撤销的任职继续展示在community选择页
	isSuper := gwMiddleware.IsSuperAdmin(c)
	var boundIDs []int64
	if !isSuper {
		// 任职关系可能较 token 签发时发生变化，按用户名实时反查
		if username, ok := c.Get("admin_username"); ok {
			if name, _ := username.(string); name != "" {
				if a, err := h.userClient.GetAdminByUsername(c.Request.Context(), &user.GetAdminByUsernameReq{Username: name}); err == nil {
					boundIDs = a.CommunityIds
				}
			}
		}
	}

	resp, err := h.userClient.ListCommunities(c.Request.Context(), &user.ListCommunitiesReq{
		Pagination:      &common.Pagination{Page: 1, PageSize: 500},
		IncludeDisabled: true,
		Status:          -1,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	list := make([]gin.H, 0, len(resp.List))
	for _, cm := range resp.List {
		if !isSuper && !containsInt64(boundIDs, cm.Id) {
			continue
		}
		list = append(list, gin.H{
			"id":     cm.Id,
			"name":   cm.Name,
			"status": cm.Status,
		})
	}
	response.SuccessWithList(c, list)
}

// ListUsers 获取会员列表
// 多社区模型：按当前社区语境（X-Community-Id）过滤会员-社区关联；
// status_filter：-1 全部（默认），0 拉黑，1 正常
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListUsers(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	keyword := c.DefaultQuery("keyword", "")
	statusFilter, _ := strconv.Atoi(c.DefaultQuery("status_filter", "-1"))

	resp, err := h.userClient.ListUsers(c.Request.Context(), &user.ListUsersReq{
		CommunityId:  h.getAdminCommunityID(c),
		Keyword:      keyword,
		StatusFilter: int32(statusFilter),
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	list := make([]gin.H, 0, len(resp.List))
	for _, u := range resp.List {
		list = append(list, gin.H{
			"id":             u.Id,
			"openid":         u.Openid,
			"phone":          u.Phone,
			"nickname":       u.Nickname,
			"avatar":         u.Avatar,
			"gender":         u.Gender,
			"birthday":       u.Birthday,
			"community_id":   u.CommunityId,
			"community_name": u.CommunityName,
			"status":         u.Status,
			"last_login_at":  u.LastLoginAt,
			"created_at":     u.CreatedAt,
		})
	}
	response.SuccessWithPage(c, list, int(resp.Pagination.Total), page, pageSize)
}

// ListUserFrequentProducts 会员常买商品排行（会员订单弹窗「常买商品排行」板块）
// 按该会员有效订单（已支付及之后，排除待支付/取消/退款）统计：
// 购买次数 = 去重订单数，累计件数 = SUM(quantity)，按购买次数降序分页返回；
// 口径：普通管理员仅统计本社区订单，超管（语境 0）看会员全社区消费
// 参数：
//   - c: gin 上下文（路径 user_id；query: page/page_size）
func (h *AdminHandler) ListUserFrequentProducts(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	page, pageSize := parsePageParams(c, 5)

	resp, err := h.orderClient.GetUserFrequentProducts(c.Request.Context(), &order.GetUserFrequentProductsReq{
		UserId:      userID,
		Page:        int32(page),
		PageSize:    int32(pageSize),
		CommunityId: h.scopeCommunityID(c),
		// 后台会员弹窗为全量口径：同弹窗「订单」Tab 能看到已删单，
		// 「常买商品」也必须按同一批订单统计，否则两个 Tab 互相矛盾
		IncludeUserDeleted: true,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	list := make([]gin.H, 0, len(resp.List))
	for _, p := range resp.List {
		list = append(list, gin.H{
			"product_id":     p.ProductId,
			"product_name":   p.ProductName,
			"product_image":  p.ProductImage,
			"sku_id":         p.SkuId,
			"purchase_count": p.PurchaseCount,
			"total_quantity": p.TotalQuantity,
		})
	}
	response.SuccessWithPage(c, list, int(resp.Total), page, pageSize)
}

// isValidBirthday 校验生日字符串是否为合法的 YYYY-MM-DD 日期
// 参数：
//   - value: 待校验的生日字符串（空串视为未填写，放行由调用方跳过校验）
//
// 返回：
//   - bool: 空串或合法日期返回 true，格式或日期非法（如 1995-13-40）返回 false
//
// 示例：isValidBirthday("1995-06-15") => true；isValidBirthday("1995/06/15") => false
func isValidBirthday(value string) bool {
	if value == "" {
		return true
	}
	_, err := time.ParseInLocation("2006-01-02", value, time.Local)
	return err == nil
}

// CreateUserReq 后台/收银台添加会员请求
type CreateUserReq struct {
	// 手机号（唯一）
	Phone    string `json:"phone" binding:"required"`
	Nickname string `json:"nickname"`
	Gender   int32  `json:"gender"`
	// 生日（YYYY-MM-DD，选填）
	Birthday string `json:"birthday"`
	// 注册场景：admin 后台会员管理 / pos 收银台（仅影响会员-社区关联来源标记，非白名单值按 admin 处理）
	Scene string `json:"scene"`
}

// CreateUser 注册会员（幂等，归属当前社区）
// 手机号已注册（含其他社区老会员）时不报错：复用既有会员并自动关联当前社区；
// 响应 is_new 区分「新注册」与「复用」，新用户活动等扩展只对 is_new=true 生效
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateUser(c *gin.Context) {
	var req CreateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if !isValidPhone(req.Phone) {
		response.Error(c, errcode.ParamError, "手机号格式错误")
		return
	}
	if !isValidBirthday(req.Birthday) {
		response.Error(c, errcode.ParamError, "生日格式错误，应为 YYYY-MM-DD")
		return
	}
	source := "admin"
	if req.Scene == "pos" {
		source = "pos"
	}

	resp, err := h.userClient.CreateMember(c.Request.Context(), &user.CreateMemberReq{
		Phone:       req.Phone,
		Nickname:    req.Nickname,
		Gender:      req.Gender,
		Birthday:    req.Birthday,
		CommunityId: h.getAdminCommunityID(c),
		Source:      source,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	u := resp.User
	response.Success(c, gin.H{
		"id":         u.Id,
		"phone":      u.Phone,
		"nickname":   u.Nickname,
		"gender":     u.Gender,
		"birthday":   u.Birthday,
		"status":     u.Status,
		"created_at": u.CreatedAt,
		"is_new":     resp.IsNew,
	})
}

// SetUserStatusReq 会员拉黑/恢复请求
type SetUserStatusReq struct {
	// 0 拉黑 1 恢复（指针绑定：0 为合法值，不能用 required 校验）
	Status *int32 `json:"status" binding:"required"`
}

// SetUserStatus 拉黑/恢复会员
// 拉黑后该会员 C 端登录与所有已登录请求均被拦截，提示「系统错误，请联系工作人员」
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) SetUserStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req SetUserStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if req.Status == nil || (*req.Status != 0 && *req.Status != 1) {
		response.Error(c, errcode.ParamError, "状态取值非法")
		return
	}

	if _, err = h.userClient.SetUserStatus(c.Request.Context(), &user.SetUserStatusReq{
		Id:          id,
		Status:      *req.Status,
		CommunityId: h.scopeCommunityID(c),
	}); err != nil {
		response.GRPCError(c, err)
		return
	}
	// 清除状态缓存，使拦截立即生效
	gwMiddleware.InvalidateUserStatusCache(id)
	response.Success(c, gin.H{"id": id, "status": *req.Status})
}

// GetUser 会员详情（会员订单弹窗信息卡数据源）
// 社区语境校验：普通管理员仅可查看与本社区关联的会员，超管（语境 0）不限
// 参数：
//   - c: gin 上下文（路径 id）
func (h *AdminHandler) GetUser(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	resp, err := h.userClient.GetUserById(c.Request.Context(), &user.GetUserByIdReq{
		Id:          id,
		CommunityId: h.scopeCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateUserReq 后台编辑会员基础资料请求
// 表单为全量提交：gender 与 birthday 按全量覆盖语义透传（birthday 空串=清空）
type UpdateUserReq struct {
	// 昵称（空串不修改）
	Nickname string `json:"nickname"`
	// 0未知 1男 2女
	Gender int32 `json:"gender"`
	// 生日（YYYY-MM-DD，空串=清空）
	Birthday string `json:"birthday"`
}

// UpdateMember 编辑会员基础资料（昵称/性别/生日）
// 社区语境校验：普通管理员仅可修改与本社区关联的会员，超管（语境 0）不限
// 参数：
//   - c: gin 上下文（路径 id）
func (h *AdminHandler) UpdateMember(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	var req UpdateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if !isValidBirthday(req.Birthday) {
		response.Error(c, errcode.ParamError, "生日格式错误，应为 YYYY-MM-DD")
		return
	}

	u, err := h.userClient.UpdateMember(c.Request.Context(), &user.UpdateMemberReq{
		Id:          id,
		Nickname:    req.Nickname,
		Gender:      req.Gender,
		Birthday:    req.Birthday,
		CommunityId: h.scopeCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"id":       u.Id,
		"phone":    u.Phone,
		"nickname": u.Nickname,
		"gender":   u.Gender,
		"birthday": u.Birthday,
		"status":   u.Status,
	})
}

// ListAdminCommunities 获取社区列表
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListAdminCommunities(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	// 状态筛选：-1 全部（默认），0 停业，1 营业
	status, _ := strconv.Atoi(c.DefaultQuery("status", "-1"))

	resp, err := h.userClient.ListCommunities(c.Request.Context(), &user.ListCommunitiesReq{
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
		// 管理端需要看到停业社区，否则停业后将无法再编辑
		IncludeDisabled: true,
		Status:          int32(status),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}

// CreateCommunityReq 创建社区请求
type CreateCommunityReq struct {
	Name            string  `json:"name" binding:"required"`
	Address         string  `json:"address"`
	Longitude       float64 `json:"longitude"`
	Latitude        float64 `json:"latitude"`
	ContactPhone    string  `json:"contact_phone"`
	ManagerName     string  `json:"manager_name"`
	ManagerPhone    string  `json:"manager_phone"`
	PickupAddress   string  `json:"pickup_address"`
	DeliveryFee     float64 `json:"delivery_fee"`
	MinOrderAmount  float64 `json:"min_order_amount"`
	DeliveryRangeKm float64 `json:"delivery_range_km"`
	BusinessHours   string  `json:"business_hours"`
	Sort            int32   `json:"sort"`
	Status          int32   `json:"status"`
	// 省/市/区名称与区划代码（来自区划选择器；历史数据允许代码为空）
	Province     string `json:"province"`
	City         string `json:"city"`
	District     string `json:"district"`
	ProvinceCode string `json:"province_code"`
	CityCode     string `json:"city_code"`
	DistrictCode string `json:"district_code"`
}

// CreateCommunity 创建社区
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) CreateCommunity(c *gin.Context) {
	var req CreateCommunityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.userClient.CreateCommunity(c.Request.Context(), &user.CreateCommunityReq{
		Name:            req.Name,
		Address:         req.Address,
		Longitude:       req.Longitude,
		Latitude:        req.Latitude,
		ContactPhone:    req.ContactPhone,
		ManagerName:     req.ManagerName,
		ManagerPhone:    req.ManagerPhone,
		PickupAddress:   req.PickupAddress,
		DeliveryFee:     req.DeliveryFee,
		MinOrderAmount:  req.MinOrderAmount,
		DeliveryRangeKm: req.DeliveryRangeKm,
		BusinessHours:   req.BusinessHours,
		Sort:            req.Sort,
		Status:          req.Status,
		Province:        req.Province,
		City:            req.City,
		District:        req.District,
		ProvinceCode:    req.ProvinceCode,
		CityCode:        req.CityCode,
		DistrictCode:    req.DistrictCode,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// GetCommunity 获取社区详情
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetCommunity(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.userClient.GetCommunity(c.Request.Context(), &user.GetCommunityReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateAdminCommunityReq 更新社区请求
type UpdateAdminCommunityReq struct {
	Name            string  `json:"name" binding:"required"`
	Address         string  `json:"address"`
	Longitude       float64 `json:"longitude"`
	Latitude        float64 `json:"latitude"`
	ContactPhone    string  `json:"contact_phone"`
	ManagerName     string  `json:"manager_name"`
	ManagerPhone    string  `json:"manager_phone"`
	PickupAddress   string  `json:"pickup_address"`
	DeliveryFee     float64 `json:"delivery_fee"`
	MinOrderAmount  float64 `json:"min_order_amount"`
	DeliveryRangeKm float64 `json:"delivery_range_km"`
	BusinessHours   string  `json:"business_hours"`
	Sort            int32   `json:"sort"`
	Status          int32   `json:"status"`
	// 省/市/区名称与区划代码（来自区划选择器；历史数据允许代码为空）
	Province     string `json:"province"`
	City         string `json:"city"`
	District     string `json:"district"`
	ProvinceCode string `json:"province_code"`
	CityCode     string `json:"city_code"`
	DistrictCode string `json:"district_code"`
}

// UpdateCommunity 更新社区
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdateCommunity(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req UpdateAdminCommunityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 渠道开关不在超管编辑表单内：整表覆盖前回填当前值，避免被 bool 零值误关
	cur, err := h.userClient.GetCommunity(c.Request.Context(), &user.GetCommunityReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	resp, err := h.userClient.UpdateCommunity(c.Request.Context(), &user.UpdateCommunityInfoReq{
		Id:              id,
		Name:            req.Name,
		Address:         req.Address,
		Longitude:       req.Longitude,
		Latitude:        req.Latitude,
		ContactPhone:    req.ContactPhone,
		ManagerName:     req.ManagerName,
		ManagerPhone:    req.ManagerPhone,
		PickupAddress:   req.PickupAddress,
		DeliveryFee:     req.DeliveryFee,
		MinOrderAmount:  req.MinOrderAmount,
		DeliveryRangeKm: req.DeliveryRangeKm,
		BusinessHours:   req.BusinessHours,
		Sort:            req.Sort,
		Status:          req.Status,
		Province:        req.Province,
		City:            req.City,
		District:        req.District,
		ProvinceCode:    req.ProvinceCode,
		CityCode:        req.CityCode,
		DistrictCode:    req.DistrictCode,
		// 渠道开关保持当前值（门店设置接口维护）
		EnableSelfPickup: cur.EnableSelfPickup,
		EnableDelivery:   cur.EnableDelivery,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// DeleteCommunity 删除社区
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) DeleteCommunity(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	_, err = h.userClient.DeleteCommunity(c.Request.Context(), &user.DeleteCommunityReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// --- 门店设置（社区级资料/配置，社区管理员可自助维护） ---

// CommunityProfileReq 门店设置更新请求
// 仅包含社区管理员允许自行修改的字段（白名单）；
// 社区名称、地址、经纬度、负责人电话、营业状态等由超管在「社区管理」统一维护，不在本接口范围
type CommunityProfileReq struct {
	ContactPhone         string  `json:"contact_phone"`
	ManagerName          string  `json:"manager_name"`
	PickupAddress        string  `json:"pickup_address"`
	DeliveryFee          float64 `json:"delivery_fee"`
	MinOrderAmount       float64 `json:"min_order_amount"`
	DeliveryRangeKm      float64 `json:"delivery_range_km"`
	BusinessHours        string  `json:"business_hours"`
	AllowOutsideDelivery bool    `json:"allow_outside_delivery"`
	// 取货渠道开关（社区级配置，C 端与收银台共用；到店不受控恒可用）
	EnableSelfPickup bool `json:"enable_self_pickup"`
	EnableDelivery   bool `json:"enable_delivery"`
}

// GetCommunityProfile 获取当前社区门店设置
// 目标社区取当前社区语境（X-Community-Id），无社区语境时报参数错误
//
// 接口: GET /api/v1/admin/community/profile
//
// 参数:
//   - c: gin 上下文
func (h *AdminHandler) GetCommunityProfile(c *gin.Context) {
	id := h.getAdminCommunityID(c)
	if id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	resp, err := h.userClient.GetCommunity(c.Request.Context(), &user.GetCommunityReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateCommunityProfile 更新当前社区门店设置（白名单字段）
// 复用 UpdateCommunity RPC 的整表覆盖语义：先读当前社区，再仅覆盖白名单字段，
// 社区名称/地址/经纬度/负责人电话/省市区/状态等一律回填当前值，保证社区管理员无法借本接口篡改
//
// 接口: PUT /api/v1/admin/community/profile
//
// 参数:
//   - c: gin 上下文
func (h *AdminHandler) UpdateCommunityProfile(c *gin.Context) {
	id := h.getAdminCommunityID(c)
	if id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req CommunityProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	cur, err := h.userClient.GetCommunity(c.Request.Context(), &user.GetCommunityReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	resp, err := h.userClient.UpdateCommunity(c.Request.Context(), &user.UpdateCommunityInfoReq{
		Id: id,
		// 以下字段保持当前值不可覆盖（超管在社区管理维护）
		Name:         cur.Name,
		Address:      cur.Address,
		Longitude:    cur.Longitude,
		Latitude:     cur.Latitude,
		ManagerPhone: cur.ManagerPhone,
		Status:       cur.Status,
		Sort:         cur.Sort,
		Province:     cur.Province,
		City:         cur.City,
		District:     cur.District,

		ProvinceCode: cur.ProvinceCode,
		CityCode:     cur.CityCode,
		DistrictCode: cur.DistrictCode,
		// 白名单字段：社区管理员可自行维护
		ContactPhone:         req.ContactPhone,
		ManagerName:          req.ManagerName,
		PickupAddress:        req.PickupAddress,
		DeliveryFee:          req.DeliveryFee,
		MinOrderAmount:       req.MinOrderAmount,
		DeliveryRangeKm:      req.DeliveryRangeKm,
		BusinessHours:        req.BusinessHours,
		AllowOutsideDelivery: req.AllowOutsideDelivery,
		EnableSelfPickup:     req.EnableSelfPickup,
		EnableDelivery:       req.EnableDelivery,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// --- 退款管理 ---

// GetRefundSetting 获取退款配置（运费退还策略 + 已完成单售后窗口）
//
// 接口: GET /api/v1/admin/settings/refund
//
// 参数:
//   - c: gin 上下文
func (h *AdminHandler) GetRefundSetting(c *gin.Context) {
	resp, err := h.orderClient.GetRefundConfig(c.Request.Context(), &order.GetRefundConfigReq{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"refund_freight_policy":  resp.RefundFreightPolicy,
		"completed_refund_hours": resp.CompletedRefundHours,
	})
}

// UpdateRefundSetting 更新退款配置（运费退还策略 + 已完成单售后窗口）
//
// 接口: PUT /api/v1/admin/settings/refund
//
// 参数:
//   - c: gin 上下文
func (h *AdminHandler) UpdateRefundSetting(c *gin.Context) {
	// 手动解析请求体而非 binding:"required,oneof"：窗口字段 0 合法（关闭 C 端售后入口），
	// 用指针区分「未传→保留现值」与「传 0→显式关闭」；且各字段校验失败返回各自的准确文案
	// （binding 失败统一报运费策略文案，会把「窗口为负数」误报成「运费策略不支持」）
	var req struct {
		RefundFreightPolicy  string `json:"refund_freight_policy"`
		CompletedRefundHours *int32 `json:"completed_refund_hours"`
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil || len(raw) == 0 || json.Unmarshal(raw, &req) != nil {
		response.Error(c, errcode.ParamError, "请求体 JSON 格式不正确")
		return
	}
	if req.RefundFreightPolicy != "always" && req.RefundFreightPolicy != "never" && req.RefundFreightPolicy != "user_decide" {
		response.Error(c, errcode.ParamError, "运费退还策略仅支持 always / never / user_decide")
		return
	}
	if req.CompletedRefundHours != nil && *req.CompletedRefundHours < 0 {
		response.Error(c, errcode.ParamError, "已完成单售后窗口不能为负数")
		return
	}
	hours := int32(0)
	if req.CompletedRefundHours == nil {
		// 未传窗口字段：读现值透传给保存链路，避免把配置意外清零
		cur, err := h.orderClient.GetRefundConfig(c.Request.Context(), &order.GetRefundConfigReq{})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
		hours = cur.CompletedRefundHours
	} else {
		hours = *req.CompletedRefundHours
	}

	_, err = h.orderClient.SaveRefundConfig(c.Request.Context(), &order.SaveRefundConfigReq{
		RefundFreightPolicy:  req.RefundFreightPolicy,
		CompletedRefundHours: hours,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"refund_freight_policy":  req.RefundFreightPolicy,
		"completed_refund_hours": hours,
	})
}

// AdminCreateRefundReq 后台代客退款请求
// mode=0/1 按商品退（items 为空=整单退）；mode=2 仅金额退（不退商品，amount 必填）；
// 金额以实付口径服务端汇算，amount 仅允许调低（仅金额退时为自定义金额，不超过剩余可退）
type AdminCreateRefundReq struct {
	Amount        float64              `json:"amount"`
	Reason        string               `json:"reason" binding:"required"`
	Items         []AdminRefundItemReq `json:"items"`
	RefundFreight bool                 `json:"refund_freight"`
	Mode          int32                `json:"mode"`
}

// AdminRefundItemReq 后台代客退款商品明细项
type AdminRefundItemReq struct {
	OrderItemID int64 `json:"order_item_id"`
	Quantity    int32 `json:"quantity"`
}

// AdminCreateRefund 后台代客退款（发起即执行，免审核）
//
// 接口: POST /api/v1/admin/orders/:id/refund
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) AdminCreateRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req AdminCreateRefundReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 社区隔离：代客退款发起即出款，仅可操作自身社区订单（超管不受限），透传社区 ID 供服务端强校验
	if err := h.ensureOrderInCommunity(c, id); err != nil {
		response.Error(c, errcode.NoPermission, err.Error())
		return
	}

	uc := appcontext.GetUserContext(c)
	pbReq := &order.AdminCreateRefundReq{
		OrderId:       id,
		Amount:        req.Amount,
		Reason:        req.Reason,
		RefundFreight: req.RefundFreight,
		Mode:          req.Mode,
		OperatorId:    uc.UserID,
		CommunityId:   h.getAdminCommunityID(c),
	}
	for _, item := range req.Items {
		pbReq.Items = append(pbReq.Items, &order.ApplyRefundItem{
			OrderItemId: item.OrderItemID,
			Quantity:    item.Quantity,
		})
	}

	resp, err := h.orderClient.AdminCreateRefund(c.Request.Context(), pbReq)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ListRefunds 查询退款列表（透传 order-service，含 order_no/明细等字段）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ListRefunds(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))

	resp, err := h.orderClient.ListRefunds(c.Request.Context(), &order.ListRefundsReq{
		CommunityId: h.getAdminCommunityID(c),
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

// ApproveRefund 同意退款
// 支付单号由 order-service 以退款单服务端记录为准，请求不再透传 payment_no（防越权指定支付流水）
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ApproveRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	// 社区隔离：审批即触发渠道出款，透传社区 ID 由 order 服务端强校验退款单归属
	// （退款单无单查 RPC，网关不做预查，服务端校验为权威防线）
	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.ApproveRefund(c.Request.Context(), &order.ApproveRefundReq{
		Id:          id,
		OperatorId:  uc.UserID,
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// RetryRefund 重试退款
// 对卡在"退款中"(status=4) 的退款单重新发起渠道退款并完成收尾
//
// 接口: POST /api/v1/admin/refunds/:id/retry
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) RetryRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	// 社区隔离：重试即再次触发渠道出款，透传社区 ID 由 order 服务端强校验退款单归属
	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.RetryRefund(c.Request.Context(), &order.RetryRefundReq{
		Id:          id,
		OperatorId:  uc.UserID,
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// ResetRefundRetryReq 恢复退款单自动收敛请求
// remark 为渠道核实依据，必填：本动作把 payment/order 两侧的重试预算归零，
// 没有依据无法审计「谁批准把一笔放弃收敛的退款重新交给系统处理」
type ResetRefundRetryReq struct {
	Remark string `json:"remark" binding:"required"`
}

// ResetRefundRetry 恢复退款单的自动收敛
// 对卡在"退款中"(status=4) 且自动重试已用满上限的退款单，把支付流水与本侧退款单的重试预算归零，
// 让调度器与退款查单任务重新核实渠道终态；与「重试退款」不同，本动作不再次向渠道发起出款
//
// 接口: POST /api/v1/admin/refunds/:id/reset-retry
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) ResetRefundRetry(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	var req ResetRefundRetryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	// binding:"required" 放得过空格串，核实依据必须是有内容的
	if strings.TrimSpace(req.Remark) == "" {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.ResetRefundRetry(c.Request.Context(), &order.ResetRefundRetryReq{
		Id:           id,
		OperatorId:   uc.UserID,
		CommunityId:  h.getAdminCommunityID(c),
		Remark:       req.Remark,
		OperatorName: c.GetString("admin_username"),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// RejectRefundReq 拒绝退款请求
type RejectRefundReq struct {
	RejectReason string `json:"reject_reason" binding:"required"`
}

// RejectRefund 拒绝退款
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) RejectRefund(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}

	var req RejectRefundReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	uc := appcontext.GetUserContext(c)
	_, err = h.orderClient.RejectRefund(c.Request.Context(), &order.RejectRefundReq{
		Id:           id,
		OperatorId:   uc.UserID,
		RejectReason: req.RejectReason,
		CommunityId:  h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

// toAdminVO 将 proto 管理员信息转换为前端视图对象
// 参数：
//   - admin: proto 管理员信息
//
// 返回值：
//   - gin.H: 前端视图对象
func (h *AdminHandler) toAdminVO(admin *user.AdminInfo) gin.H {
	if admin == nil {
		return nil
	}
	return gin.H{
		"id":            admin.Id,
		"username":      admin.Username,
		"real_name":     admin.RealName,
		"phone":         admin.Phone,
		"is_super":      admin.IsSuper,
		"role_code":     admin.RoleCode,
		"role_name":     admin.RoleName,
		"community_id":  admin.CommunityId,
		"community_ids": admin.CommunityIds,
		"status":        admin.Status,
		"last_login_at": admin.LastLoginAt,
		"last_login_ip": admin.LastLoginIp,
		// 绑定的会员 ID（0=未绑定；绑定后该会员在任职社区即履约人员）
		"user_id": admin.UserId,
		// 绑定会员的昵称/手机号（列表展示与编辑回显用；未绑定或会员不存在时为空）
		"user_nickname": admin.UserNickname,
		"user_phone":    admin.UserPhone,
	}
}

// orderStatusText 将订单状态码转换为后台展示文案
// 与 C 端 clientOrderStatusText 对齐：自提单的"已支付/已发货"展示为「待自提」，
// 避免后台对自提单误显示「待发货」并引导管理员填写物流；
// 到店单（delivery_type=3）无物流/自提语义，已支付即展示「已支付」
// 参数：
//   - status: 订单状态码
//   - deliveryType: 配送方式（1 自提 2 配送 3 到店）
//
// 返回值：
//   - string: 状态中文描述
func orderStatusText(status, deliveryType int32) string {
	switch status {
	case 1:
		return "待支付"
	case 2:
		switch deliveryType {
		case 1:
			return "待自提"
		case 3:
			return "已支付"
		}
		return "待发货"
	case 3:
		return "备货中"
	case 4:
		if deliveryType == 1 {
			return "待自提"
		}
		if deliveryType == 3 {
			return "已支付"
		}
		return "已发货"
	case 5:
		return "已完成"
	case 6:
		return "已取消"
	case 7:
		return "退款中"
	case 8:
		return "已退款"
	default:
		return "未知"
	}
}

// PaymentSetting 支付配置视图对象
// 同时用于 GET 响应与 PUT 请求；配置持久化在数据库 pay_config 表，由 payment 服务管理
type PaymentSetting struct {
	EnabledChannels    []string             `json:"enabled_channels"`
	Wechat             config.WechatConfig  `json:"wechat"`
	Alipay             config.AlipayConfig  `json:"alipay"`
	WechatPaymentReady bool                 `json:"wechat_payment_ready"`
	AlipayReady        bool                 `json:"alipay_ready"`
	WechatCertUploaded bool                 `json:"wechat_cert_uploaded"`
	AlipayCertUploaded bool                 `json:"alipay_cert_uploaded"`
	UpdatedAt          string               `json:"updated_at"`
	SuggestedURLs      PaymentSuggestedURLs `json:"suggested_urls"`
	// 适用客户端渠道勾选（mp/gzh/app/h5；空数组=全部客户端渠道可用，存量兼容口径）
	WechatClientChannels []string `json:"wechat_client_channels"`
	AlipayClientChannels []string `json:"alipay_client_channels"`
	// AlipayReadyMessage 支付宝配置自检失败原因，空表示自检通过（就绪=字段齐全且自检通过）；仅出参
	AlipayReadyMessage string `json:"alipay_ready_message"`
	// WechatApiV3Configured V3 密钥是否已配置：密钥明文不回传，前端据此展示「已配置」；仅出参
	WechatApiV3Configured bool `json:"wechat_api_v3_configured"`
	// 支付宝证书模式三证书上传状态：证书内容不回传，前端仅据此展示「已上传」并可重新上传；仅出参
	AlipayAppCertUploaded    bool `json:"alipay_app_cert_uploaded"`
	AlipayPublicCertUploaded bool `json:"alipay_public_cert_uploaded"`
	AlipayRootCertUploaded   bool `json:"alipay_root_cert_uploaded"`
}

// PaymentSuggestedURLs 回调 URL 配置引导
// 根据后台当前访问域名自动生成，引导使用人将回调地址配置到对应支付平台
type PaymentSuggestedURLs struct {
	BaseURL         string `json:"base_url"`
	WechatNotifyURL string `json:"wechat_notify_url"`
	AlipayNotifyURL string `json:"alipay_notify_url"`
}

// wechatPayAppIDFromChannel 获取微信支付默认 AppID（渠道管理 mp 渠道为唯一来源）
// AppID 属客户端身份凭证，统一在「渠道管理」维护；pay_config 仅保存派生值供
// wego 客户端构建与存量口径（未携带 X-Channel-Code 的旧客户端）兜底
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - string: mp 渠道 AppID，渠道未配置或查询失败时为空串
//   - bool: 渠道 AppID 是否可用
func (h *AdminHandler) wechatPayAppIDFromChannel(ctx context.Context) (string, bool) {
	chResp, err := h.userClient.GetClientChannel(ctx, &user.GetClientChannelReq{Code: channel.MP})
	if err != nil || chResp == nil || chResp.AppId == "" {
		return "", false
	}
	return chResp.AppId, true
}

// writeBackPayConfig 以「读取到的配置为底 + 局部覆写」回写支付配置并返回最新配置
// 回写携带读取时的版本号，并发下若配置已被他人改写则放弃本次回写（仅记日志，下一次读取重新同步）；
// 回写成功后必须重新读取配置，使返回给前端的 updated_at 与库中版本一致，
// 否则前端下一次保存会被乐观锁误判为冲突
// 私钥由证书上传接口单独维护，此处恒传空，payment 侧保留原值
//
// 参数：
//   - ctx: 上下文
//   - resp: 本次读取到的支付配置
//   - apply: 需要覆写的字段，nil 表示仅原样回写
//
// 返回值：
//   - *payment.PayConfig: 最新配置，回写或重读失败时原样返回入参
func (h *AdminHandler) writeBackPayConfig(ctx context.Context, resp *payment.PayConfig, apply func(*payment.PayConfig)) *payment.PayConfig {
	payload := &payment.PayConfig{
		EnabledChannels:    resp.EnabledChannels,
		WechatAppId:        resp.WechatAppId,
		WechatAppSecret:    resp.WechatAppSecret,
		WechatMchId:        resp.WechatMchId,
		WechatApiV3Key:     resp.WechatApiV3Key,
		WechatCertSerialNo: resp.WechatCertSerialNo,
		WechatNotifyUrl:    resp.WechatNotifyUrl,
		AlipayAppId:        resp.AlipayAppId,
		AlipayPublicKey:    resp.AlipayPublicKey,
		AlipayNotifyUrl:    resp.AlipayNotifyUrl,
		AlipayReturnUrl:    resp.AlipayReturnUrl,
		AlipayGatewayUrl:   resp.AlipayGatewayUrl,
		// 加签方式原样回写，避免每次回写都把证书模式重置为公钥模式
		AlipayMode:           resp.AlipayMode,
		WechatClientChannels: resp.WechatClientChannels,
		AlipayClientChannels: resp.AlipayClientChannels,
		UpdatedAt:            resp.UpdatedAt,
	}
	if apply != nil {
		apply(payload)
	}

	if _, err := h.paymentClient.SavePayConfig(ctx, payload); err != nil {
		log.Printf("回写支付配置失败: %v", err)
		return resp
	}
	fresh, err := h.paymentClient.GetPayConfig(ctx, &payment.GetPayConfigReq{})
	if err != nil {
		log.Printf("回写支付配置后重读失败: %v", err)
		return resp
	}
	return fresh
}

// syncWechatPayAppID 将渠道管理的微信 AppID 回写到支付配置并返回最新配置
//
// 参数：
//   - ctx: 上下文
//   - resp: 本次读取到的支付配置
//   - appID: 渠道管理侧的微信 AppID
//
// 返回值：
//   - *payment.PayConfig: 最新配置
func (h *AdminHandler) syncWechatPayAppID(ctx context.Context, resp *payment.PayConfig, appID string) *payment.PayConfig {
	return h.writeBackPayConfig(ctx, resp, func(p *payment.PayConfig) { p.WechatAppId = appID })
}

// payCallbackURLsFor 按部署域名派生支付回调地址
//
// 参数：
//   - base: 部署域名基址，形如 https://host
//
// 返回值：
//   - string: 微信异步通知地址
//   - string: 支付宝异步通知地址
//   - string: 支付宝支付完成回跳地址
func payCallbackURLsFor(base string) (string, string, string) {
	return base + wechatPayCallbackPath, base + alipayPayCallbackPath, base + h5PayResultHashURL
}

// GetPaymentSetting 获取支付配置
// 从数据库读取支付渠道开关与微信/支付宝配置，并返回根据当前请求域名生成的回调 URL 建议
// 微信 AppID 以渠道管理 mp 渠道实时值为准，就绪状态按「支付凭证齐全 + AppID 可用」重算
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetPaymentSetting(c *gin.Context) {
	resp, err := h.paymentClient.GetPayConfig(c.Request.Context(), &payment.GetPayConfigReq{})
	if err != nil {
		response.GRPCError(c, err)
		return
	}

	wechatAppID := resp.WechatAppId
	if appID, ok := h.wechatPayAppIDFromChannel(c.Request.Context()); ok {
		wechatAppID = appID
	}
	// 渠道 AppID 与 pay_config 派生值不一致（渠道侧更新过）时回写同步，
	// 保证 payment 服务按最新 AppID 构建微信支付客户端；同步失败不阻断读取
	if wechatAppID != resp.WechatAppId {
		resp = h.syncWechatPayAppID(c.Request.Context(), resp, wechatAppID)
	}

	// 回调地址一律由部署域名派生：站点设置已配置域名（或 SITE_BASE_URL 兜底）且库中值与派生值不一致时回写自愈；
	// 两者均未配置时只按当前请求展示、不写库，避免把临时访问到的 host 固化进配置
	base := platformBaseURL(c)
	wechatNotify, alipayNotify, alipayReturn := payCallbackURLsFor(base)
	if snap := brand.Get(c.Request.Context()); snap.BaseURL != "" &&
		(resp.WechatNotifyUrl != wechatNotify || resp.AlipayNotifyUrl != alipayNotify || resp.AlipayReturnUrl != alipayReturn) {
		resp = h.writeBackPayConfig(c.Request.Context(), resp, func(p *payment.PayConfig) {
			p.WechatNotifyUrl = wechatNotify
			p.AlipayNotifyUrl = alipayNotify
			p.AlipayReturnUrl = alipayReturn
		})
	}
	// AppID 来源改为渠道管理后，就绪判定在网关重算，不再依赖 payment 服务基于 pay_config.AppID 的口径
	wechatReady := wechatAppID != "" &&
		resp.WechatMchId != "" &&
		resp.WechatApiV3Key != "" &&
		resp.WechatCertSerialNo != "" &&
		resp.WechatPrivateKey != "" &&
		resp.WechatNotifyUrl != ""

	// 私钥/V3 密钥/AppSecret 不回传明文：私钥通过 cert_uploaded 标志、V3 密钥通过
	// wechat_api_v3_configured 标志、AppSecret 通过渠道管理侧的 configured 标志告知前端状态；
	// 保存时空值由 payment 侧保留原值，不会误清空
	setting := PaymentSetting{
		EnabledChannels: resp.EnabledChannels,
		Wechat: config.WechatConfig{
			AppID:        wechatAppID,
			AppSecret:    "",
			MchID:        resp.WechatMchId,
			APIV3Key:     "",
			CertSerialNo: resp.WechatCertSerialNo,
			// 回调地址由部署域名派生，前端只读展示
			NotifyURL: wechatNotify,
		},
		Alipay: config.AlipayConfig{
			AppID:           resp.AlipayAppId,
			Mode:            resp.AlipayMode,
			AlipayPublicKey: resp.AlipayPublicKey,
			NotifyURL:       alipayNotify,
			ReturnURL:       alipayReturn,
			GatewayURL:      resp.AlipayGatewayUrl,
		},
		WechatPaymentReady:       wechatReady,
		AlipayReady:              resp.AlipayReady,
		AlipayReadyMessage:       resp.AlipayReadyMessage,
		WechatApiV3Configured:    resp.WechatApiV3Key != "",
		WechatCertUploaded:       resp.WechatCertUploaded,
		AlipayCertUploaded:       resp.AlipayCertUploaded,
		AlipayAppCertUploaded:    resp.AlipayAppCertUploaded,
		AlipayPublicCertUploaded: resp.AlipayPublicCertUploaded,
		AlipayRootCertUploaded:   resp.AlipayRootCertUploaded,
		UpdatedAt:                resp.UpdatedAt,
		SuggestedURLs:            buildSuggestedURLs(c),

		WechatClientChannels: resp.WechatClientChannels,
		AlipayClientChannels: resp.AlipayClientChannels,
	}

	response.Success(c, setting)
}

// UpdatePaymentSetting 更新支付配置
// 配置写入数据库，payment 服务即时热更新，无需重启
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UpdatePaymentSetting(c *gin.Context) {
	var req PaymentSetting
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}

	// 微信 AppID 单一来源为渠道管理 mp 渠道：保存时强制以渠道实时值覆盖（未配置则清空），
	// 前端不再提供 AppID/AppSecret 输入入口；AppSecret 为支付链路无关的历史字段，空值透传保留原值
	wechatAppID := ""
	if appID, ok := h.wechatPayAppIDFromChannel(c.Request.Context()); ok {
		wechatAppID = appID
	}

	// 回调地址不接受前端入参：一律按部署域名派生后落库，与只读展示口径一致
	saveWechatNotify, saveAlipayNotify, saveAlipayReturn := payCallbackURLsFor(platformBaseURL(c))
	_, err := h.paymentClient.SavePayConfig(c.Request.Context(), &payment.PayConfig{
		EnabledChannels:    req.EnabledChannels,
		WechatAppId:        wechatAppID,
		WechatAppSecret:    req.Wechat.AppSecret,
		WechatMchId:        req.Wechat.MchID,
		WechatApiV3Key:     req.Wechat.APIV3Key,
		WechatCertSerialNo: req.Wechat.CertSerialNo,
		// 私钥由证书上传接口单独维护，此处恒传空，payment 侧保留原值
		WechatPrivateKey: "",
		WechatNotifyUrl:  saveWechatNotify,
		AlipayAppId:      req.Alipay.AppID,
		AlipayPrivateKey: "",
		AlipayPublicKey:  req.Alipay.AlipayPublicKey,
		// 加签方式随表单整行覆写（证书内容不在本接口维护，由上传接口单独写入）
		AlipayMode:       req.Alipay.Mode,
		AlipayNotifyUrl:  saveAlipayNotify,
		AlipayReturnUrl:  saveAlipayReturn,
		AlipayGatewayUrl: req.Alipay.GatewayURL,

		WechatClientChannels: req.WechatClientChannels,
		AlipayClientChannels: req.AlipayClientChannels,
		// 携带前端读取时的版本号，库中版本已变化时 payment 侧拒绝整行覆写
		UpdatedAt: req.UpdatedAt,
	})
	if err != nil {
		log.Printf("保存支付设置失败: %v", err)
		response.GRPCError(c, err)
		return
	}

	response.Success(c, gin.H{"success": true})
}

// UploadPaymentCert 上传支付证书文件
// 接收 multipart 文件，校验通过后转发给 payment 服务存入数据库
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) UploadPaymentCert(c *gin.Context) {
	channel := c.PostForm("channel")
	certType := c.PostForm("cert_type")
	if channel != "wechat" && channel != "alipay" {
		response.Error(c, errcode.ParamError, "不支持的支付渠道")
		return
	}
	if certType == "" {
		certType = "private_key"
	}

	limitUploadBody(c)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.Error(c, errcode.ParamError, "请选择要上传的证书文件（不超过 12MB）")
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		response.Error(c, errcode.InternalServerError, "读取证书文件失败")
		return
	}
	defer func() { _ = file.Close() }()

	content, err := io.ReadAll(file)
	if err != nil {
		response.Error(c, errcode.InternalServerError, "读取证书文件内容失败")
		return
	}

	resp, err := h.paymentClient.UploadPayCert(c.Request.Context(), &payment.UploadPayCertReq{
		Channel:  channel,
		CertType: certType,
		Filename: fileHeader.Filename,
		Content:  content,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	if !resp.Success {
		response.Error(c, errcode.ParamError, resp.Message)
		return
	}

	response.Success(c, gin.H{"success": true, "message": resp.Message})
}

// buildSuggestedURLs 生成支付平台回调地址
// 基址取站点设置的部署域名（未配置时回落按当前请求现推），回调路径固定在网关路由上
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - PaymentSuggestedURLs: 回调地址
func buildSuggestedURLs(c *gin.Context) PaymentSuggestedURLs {
	base := platformBaseURL(c)
	return PaymentSuggestedURLs{
		BaseURL:         base,
		WechatNotifyURL: base + wechatPayCallbackPath,
		AlipayNotifyURL: base + alipayPayCallbackPath,
	}
}
