package errcode

const (
	// Success 操作成功
	Success = 0

	// ----- 通用错误 -----
	ParamError          = 10001
	InternalServerError = 10002
	NotFound            = 10003
	OperationNotAllowed = 10004
	RateLimit           = 10005
	DataAlreadyExists   = 10006

	// ----- 认证模块 -----
	NotLoginOrTokenExpired      = 11001
	InvalidToken                = 11002
	InvalidVerifyCode           = 11003
	VerifyCodeTooFrequent       = 11004
	NoPermission                = 11005
	AccountDisabled             = 11006
	IncorrectUsernameOrPassword = 11007
	// UserBlocked 会员已被拉黑（C 端访问拦截，文案按需求伪装为系统错误）
	UserBlocked = 11008
	// LoginLocked 连续登录失败已达上限，账号在锁定期内拒绝再次尝试（具体剩余时间由调用方拼进文案）
	LoginLocked = 11009
	// PasswordEncryptedError 密码密文无效：RSA 解密失败或 key_id 不匹配（服务端密钥已轮换）。
	// 前端收到该码应重拉公钥（/auth/pubkey）后用新公钥重试一次，仍失败才提示用户
	PasswordEncryptedError = 11010
	// HTTPSRequired 生产环境密码类接口拒绝非 HTTPS 请求（传输层兜底，防止 TLS 配置失误时密码明文过网）
	HTTPSRequired = 11011

	// ----- 商品模块 -----
	ProductNotFoundOrOffline = 12001
	ProductStockInsufficient = 12002
	CategoryNotFound         = 12003
	SkuNotFound              = 12004
	ProductNotInCommunity    = 12005

	// ----- 订单模块 -----
	OrderNotFound         = 13001
	OrderStatusNotAllowed = 13002
	OrderTimeoutCancelled = 13003
	RefundAmountExceed    = 13004

	// ----- 购物车模块 -----
	CartItemNotFound = 14001
	CartItemOffline  = 14002

	// ----- 支付模块 -----
	PaymentFailed           = 15001
	PaymentNotFound         = 15002
	DuplicatePayment        = 15003
	PaymentCallbackSignFail = 15004

	// ----- 营销模块 -----
	CouponExhausted            = 16001
	CouponExpired              = 16002
	CouponNotMeetThreshold     = 16003
	FlashSaleEnded             = 16004
	FlashSaleStockInsufficient = 16005

	// ----- 库存模块 -----
	StockEntryFailed    = 17001
	StockCheckNotFound  = 17002
	StockCheckSubmitted = 17003

	// ----- 系统模块 -----
	CommunityNotFound = 18001
	AdminNotFound     = 18002
	RoleNotFound      = 18003
)

// Message 错误码对应的中文提示
var Message = map[int]string{
	Success:                     "success",
	ParamError:                  "参数错误",
	InternalServerError:         "服务器内部错误",
	NotFound:                    "资源不存在",
	OperationNotAllowed:         "操作不被允许",
	RateLimit:                   "请求频率限制",
	DataAlreadyExists:           "数据已存在",
	NotLoginOrTokenExpired:      "未登录或 token 已过期",
	InvalidToken:                "token 无效",
	InvalidVerifyCode:           "验证码错误",
	VerifyCodeTooFrequent:       "验证码发送过于频繁",
	NoPermission:                "无权限访问",
	AccountDisabled:             "账号已被禁用",
	IncorrectUsernameOrPassword: "用户名或密码错误",
	UserBlocked:                 "系统错误，请联系工作人员",
	LoginLocked:                 "登录失败次数过多，请稍后再试",
	PasswordEncryptedError:      "密码加密数据无效，请刷新页面后重试",
	HTTPSRequired:               "出于安全考虑，请通过 HTTPS 访问",
	ProductNotFoundOrOffline:    "商品不存在或已下架",
	ProductStockInsufficient:    "商品库存不足",
	CategoryNotFound:            "分类不存在",
	SkuNotFound:                 "SKU 不存在",
	ProductNotInCommunity:       "该社区无此商品",
	OrderNotFound:               "订单不存在",
	OrderStatusNotAllowed:       "订单状态不允许此操作",
	OrderTimeoutCancelled:       "订单已超时取消",
	RefundAmountExceed:          "退款金额超出订单金额",
	CartItemNotFound:            "购物车商品不存在",
	CartItemOffline:             "购物车商品已下架",
	PaymentFailed:               "支付失败",
	PaymentNotFound:             "支付单不存在",
	DuplicatePayment:            "重复支付",
	PaymentCallbackSignFail:     "支付回调验签失败",
	CouponExhausted:             "优惠券已领完",
	CouponExpired:               "优惠券已过期",
	CouponNotMeetThreshold:      "不满足优惠券使用条件",
	FlashSaleEnded:              "秒杀活动已结束",
	FlashSaleStockInsufficient:  "秒杀库存不足",
	StockEntryFailed:            "入库失败",
	StockCheckNotFound:          "盘点单不存在",
	StockCheckSubmitted:         "盘点单已提交",
	CommunityNotFound:           "社区不存在",
	AdminNotFound:               "管理员不存在",
	RoleNotFound:                "角色不存在",
}

// GetMessage 根据错误码获取提示信息
// 参数：
//   - code: 错误码
//
// 返回：
//   - 存在返回对应中文，不存在返回 "未知错误"
func GetMessage(code int) string {
	if msg, ok := Message[code]; ok {
		return msg
	}
	return "未知错误"
}
