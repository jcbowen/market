package config

import (
	"github.com/jcbowen/jcbaseGo"
)

// Config 全局配置结构体
// 用于 gateway 及各微服务加载 bin/config.yaml
// 注意：支付配置（微信支付/支付宝/渠道开关）已迁移至数据库 pay_config 表，由后台配置管理，
// 此处仅保留微信小程序登录配置（AppID/AppSecret）
type Config struct {
	WebServer jcbaseGo.WebServer   `json:"web_server" yaml:"web_server"`
	Db        jcbaseGo.DbStruct    `json:"db" yaml:"db"`
	Redis     jcbaseGo.RedisStruct `json:"redis" yaml:"redis"`
	Project   struct {
		Name string `json:"name" yaml:"name" default:"社区商超"`
	} `json:"project" yaml:"project"`
	Grpc struct {
		User    string `json:"user_service" yaml:"user_service" default:"localhost:50051"`
		Product string `json:"product_service" yaml:"product_service" default:"localhost:50052"`
		Order   string `json:"order_service" yaml:"order_service" default:"localhost:50053"`
		Cart    string `json:"cart_service" yaml:"cart_service" default:"localhost:50054"`
		Payment string `json:"payment_service" yaml:"payment_service" default:"localhost:50055"`
	} `json:"grpc" yaml:"grpc"`
	JWT struct {
		Secret          string `json:"secret" yaml:"secret" default:"market-secret-key-change-in-production"`
		AccessTokenTTL  int64  `json:"access_token_ttl" yaml:"access_token_ttl" default:"7200"`
		RefreshTokenTTL int64  `json:"refresh_token_ttl" yaml:"refresh_token_ttl" default:"604800"`
	} `json:"jwt" yaml:"jwt"`
	Log struct {
		Level      string `json:"level" yaml:"level" default:"info"`
		Format     string `json:"format" yaml:"format" default:"json"`
		FilePath   string `json:"file_path" yaml:"file_path" default:"logs/"`
		MaxSize    int    `json:"max_size" yaml:"max_size" default:"100"`
		MaxBackups int    `json:"max_backups" yaml:"max_backups" default:"30"`
		MaxAge     int    `json:"max_age" yaml:"max_age" default:"7"`
		Compress   bool   `json:"compress" yaml:"compress" default:"true"`
	} `json:"log" yaml:"log"`
	Upload struct {
		MaxSize      int      `json:"max_size" yaml:"max_size" default:"10485760"`
		AllowedTypes []string `json:"allowed_types" yaml:"allowed_types"`
		Storage      string   `json:"storage" yaml:"storage" default:"local"`
		LocalPath    string   `json:"local_path" yaml:"local_path" default:"static/uploads/"`
	} `json:"upload" yaml:"upload"`
	GeoIP struct {
		// GeoLite2-City 数据库路径，相对路径以可执行文件所在目录为基准
		// 文件由 cmd/geoip-download 下载工具定期下载更新（不入 git 仓库），
		// 缺失时 IP 定位接口返回未命中，不影响其他功能
		DBPath string `json:"db_path" yaml:"db_path" default:"data/geoip/GeoLite2-City.mmdb"`
	} `json:"geoip" yaml:"geoip"`
	Wechat   WechatConfig `json:"wechat" yaml:"wechat"`
	Security struct {
		// TrustedProxies 可信反向代理网段（CIDR 或单个 IP），决定 ClientIP 是否采信代理头
		// 缺省为空＝不信任任何代理头，只认直连地址：伪造 X-Forwarded-For 绕过限流的口子由此封死
		// nginx/caddy 与 gateway 同机或同网段反代时必须显式配置（如 ["127.0.0.1"]、["172.16.0.0/12"]），
		// 否则限流、访问日志与 IP 定位会把所有用户合并成代理 IP（表现为整站共用一个限流桶）
		TrustedProxies []string `json:"trusted_proxies" yaml:"trusted_proxies"`
		// TrustedPlatform 可信平台专用头名（gin Engine.TrustedPlatform）。
		// 前置 CDN/云厂商代理时，由代理**强制覆盖**写入的客户端真实 IP 头（客户端伪造无效），
		// 典型值：阿里云 CDN 的 Ali-CDN-Real-IP、Cloudflare 的 CF-Connecting-IP。
		// ClientIP() 优先返回该头值，头缺失（如本地直连调试）时回落 trusted_proxies/直连逻辑，零影响。
		// 与 trusted_proxies 的分工：后者解决「代理链要读哪段」；本项解决「厂商头值即真话」——
		// CDN 回源 IP 无固定段不可枚举时，这是白名单/限流拿到真实 IP 的唯一可靠路径
		TrustedPlatform string `json:"trusted_platform" yaml:"trusted_platform"`
		// PasswordRSAPrivateKey 登录密码传输加密的 RSA 私钥（PEM，PKCS#8/PKCS#1 均可）。
		// 前端以公钥加密密码（RSA-OAEP-SHA256，PKCS#1 v1.5 已弃用勿回退）后传输，网关持私钥解密，
		// 链路抓包不再可见密码明文；
		// 多实例/负载均衡部署必须让各实例持有同一私钥，否则拿公钥与提交密文落在不同实例
		// 会随机解密失败。生产推荐改用下方 PasswordRSAKeyFile 文件方式（免 \n 转义、可挂载共享），
		// 本内联项留作兼容。私钥不入 git 仓库；该密钥对不是 TLS 证书，无有效期概念，轮换只需替换并重启
		PasswordRSAPrivateKey string `json:"password_rsa_private_key" yaml:"password_rsa_private_key"`
		// PasswordRSAKeyFile 登录密码加密 RSA 私钥的文件路径（推荐方式，替代内联 PEM）。
		// 文件存在 → 直接加载（内容非法拒绝启动）；文件不存在 → 自动生成并写入该路径
		// （目录 0700 自动创建、文件 0600）：把路径指向挂载目录即可实现密钥持久化，
		// 容器重建不换钥，多实例挂载同一宿主文件即共享同一密钥对（多实例建议预生成后挂载，
		// 避免同时首启的生成竞争）；落盘失败降级为内存密钥并告警，不阻断启动。
		// 优先级：PASSWORD_RSA_PRIVATE_KEY_FILE（ENV 路径）> PASSWORD_RSA_PRIVATE_KEY
		// （ENV 内联，设值会清空本项）> 本项 > PasswordRSAPrivateKey（配置内联）
		// > 均空时纯自动生成（内存，重启更换）
		PasswordRSAKeyFile string `json:"password_rsa_key_file" yaml:"password_rsa_key_file"`
		// ApiRateQPS/ApiRateBurst 接口洪水兜底阈值（按客户端 IP 的令牌桶，速率/突发容量），
		// 作用于 /api/v1 与 /api/v1/admin 两个分组，超限回 HTTP 429 + 业务码 10005 + Retry-After: 1。
		// 缺省 100/150 面向真实人机流量（单个用户峰值不过几十 r/s，正常用户撞不到）。
		// 需要上调的场景：① 大量用户经同一 NAT/办公出口聚合（整层楼共用一个客户端 IP）；
		// ② 本机跑整套接口测试——用例从同一 IP 串行打出，实测峰值 116~133 r/s 贴着缺省阈值，
		// 会在随机某条用例上撞出 429（2026-10-29 取证：api/49 现金支付被「请求频率限制」打红），
		// 本地 docker 栈已在 compose 里上调，生产保持缺省。
		// 设 0 或留空回落缺省值（配置加载按 CheckAndSetDefault 语义，零值即默认，不存在「配成 0」）
		ApiRateQPS   float64 `json:"api_rate_qps" yaml:"api_rate_qps" default:"100"`
		ApiRateBurst float64 `json:"api_rate_burst" yaml:"api_rate_burst" default:"150"`
	} `json:"security" yaml:"security"`
	SMS struct {
		Provider     string `json:"provider" yaml:"provider" default:"aliyun"`
		AccessKey    string `json:"access_key" yaml:"access_key"`
		SecretKey    string `json:"secret_key" yaml:"secret_key"`
		SignName     string `json:"sign_name" yaml:"sign_name" default:"社区商超"`
		TemplateCode string `json:"template_code" yaml:"template_code"`
		CodeExpire   int    `json:"code_expire" yaml:"code_expire" default:"300"`
		SendInterval int    `json:"send_interval" yaml:"send_interval" default:"60"`
	} `json:"sms" yaml:"sms"`
	Business struct {
		ExpireWarningDays int `json:"expire_warning_days" yaml:"expire_warning_days" default:"3"`
		OrderTimeout      int `json:"order_timeout" yaml:"order_timeout" default:"1800"`
		AutoCompleteDays  int `json:"auto_complete_days" yaml:"auto_complete_days" default:"7"`
		// PaymentReconcile 支付对账与回调可靠性配置（渠道为事实来源的最终一致兜底）
		PaymentReconcile struct {
			// Enabled 渠道主动回查 + 关单时关闭渠道交易的总开关。
			// 使用 *bool 表达三态语义：nil（未配置）= 启用；显式 false = 关闭（退回纯回调路径 + 纯本地关单）。
			// 不用 bool + default:"true"：CheckAndSetDefault 的布尔默认值机制无法区分「未配置」与「显式 false」，
			// 会把显式关闭强行改回开启，导致回退开关失效
			Enabled *bool `json:"enabled" yaml:"enabled"`
			// CancelGuard 取消前置渠道确认（订单仍处待支付时问渠道，已收款则放弃取消，防资金挂账）
			// 同为 *bool 三态：nil=启用；受 Enabled 约束（Enabled 关闭时本开关无效）
			CancelGuard *bool `json:"cancel_guard" yaml:"cancel_guard"`
			// AutoRefundStuck 挂账单自动退款。默认关闭：渠道退款回执需真实商户号联调验证后才能开启
			AutoRefundStuck *bool `json:"auto_refund_stuck" yaml:"auto_refund_stuck"`
			// PendingMaxMinutes 待支付单回查的时间上界（分钟），按微信 prepay 时效取值；
			// 支付宝侧实际取 min(本值, business.order_timeout 换算分钟)，与下单 timeout_express 对齐
			PendingMaxMinutes int `json:"pending_max_minutes" yaml:"pending_max_minutes" default:"120"`
			// ClosedLookbackMinutes 已关单挂账回溯窗口（分钟），与在途单渠道时效（PendingMaxMinutes）分档取值：
			// 钱可能在关单之后才到账（H5 页面长时间停留、用户在超时边缘完成支付），
			// 若在途窗口内扫已关单，超过窗口才生效的付款会永久漏检
			ClosedLookbackMinutes int `json:"closed_lookback_minutes" yaml:"closed_lookback_minutes" default:"1440"`
			// ClosedThrottleMinutes 已关单挂账回溯的最小间隔（分钟）：关闭单事实变化慢，按低频回溯，
			// 与 ClosedLookbackMinutes、MaxReconcileCount 共同控制渠道调用成本
			ClosedThrottleMinutes int `json:"closed_throttle_minutes" yaml:"closed_throttle_minutes" default:"30"`
			// IntervalSeconds 对账调度轮询间隔（秒）
			IntervalSeconds int `json:"interval_seconds" yaml:"interval_seconds" default:"60"`
			// MinAgeSeconds 支付单创建后至少等待多少秒才回查渠道（给正常回调留时间，避免打扰渠道）
			MinAgeSeconds int `json:"min_age_seconds" yaml:"min_age_seconds" default:"60"`
			// ThrottleMinutes 同一支付单两次回查的最小间隔（分钟）
			ThrottleMinutes int `json:"throttle_minutes" yaml:"throttle_minutes" default:"5"`
			// MaxReconcileCount 同一支付单累计回查上限，超限写「放弃对账」流水并停止回查
			MaxReconcileCount int `json:"max_reconcile_count" yaml:"max_reconcile_count" default:"24"`
			// BatchSize 单轮每类扫描的支付单条数上限
			BatchSize int `json:"batch_size" yaml:"batch_size" default:"100"`
			// NotifyLogRetentionDays 通知与对账流水保留天数
			NotifyLogRetentionDays int `json:"notify_log_retention_days" yaml:"notify_log_retention_days" default:"30"`
		} `json:"payment_reconcile" yaml:"payment_reconcile"`
	} `json:"business" yaml:"business"`
	Debugger struct {
		// Enabled 是否启用 debugger 请求日志（jcbaseGo 组件，写共享 debug_logs 表，前端页面见 BasePath）
		Enabled bool `json:"enabled" yaml:"enabled" default:"false"`
		// ServiceName 服务标识：多服务共享 debug_logs 表时区分日志来源
		ServiceName string `json:"service_name" yaml:"service_name" default:"gateway"`
		// AllowedIPs debugger 前端页面访问白名单（IP/CIDR），鉴权一律走 gin 的 ClientIP
		// 空 = 不注册页面路由（fail-closed），需同时配 allow_empty_whitelist: true 才开放
		AllowedIPs []string `json:"allowed_ips" yaml:"allowed_ips"`
		// AllowEmptyWhitelist 白名单为空时是否仍开放调试页面，默认 false（只记日志不暴露界面）
		// preserve:"true" 为与组件侧对齐的预留声明；当前框架加载走 CheckAndSetDefault 并不读该 tag，
		// 因 default 即为 false，零值填充结果一致，无行为差异
		AllowEmptyWhitelist bool `json:"allow_empty_whitelist" yaml:"allow_empty_whitelist" default:"false" preserve:"true"`
		// MaxBodySize 单条记录捕获的请求/响应体上限（KB），超出只记上限内正文并附截断说明
		// 无「0」语义：写 0 会被框架回填成默认 16
		MaxBodySize int64 `json:"max_body_size" yaml:"max_body_size" default:"16"`
		// MaxRecords 表内最大记录条数（近似上限，按写入次数均摊裁剪）
		// *int 三态：不配置（nil）= 5000 条；显式 0 = 不限制条数
		// 之所以用指针：框架在读取配置后会把零值统一回填为 default 标签值，
		// 普通 int 写 0 无法表达「不限制」，会被静默变成 5000
		MaxRecords *int `json:"max_records" yaml:"max_records"`
		// RetentionDays 日志保留天数：不配置（nil）= 7 天；显式 0 = 不做过期清理
		// 任何取值都不会清空全表（清理条件为 timestamp < now-保留期，保留期非正即拒绝清理）
		// 清理由组件的周期协程执行，见 CleanupIntervalHours；三态原因同 MaxRecords
		RetentionDays *int `json:"retention_days" yaml:"retention_days"`
		// CleanupIntervalHours 过期清理执行间隔（小时），启动时先清一次
		// 无「0」语义：写 0 会被框架回填成默认 24
		CleanupIntervalHours int `json:"cleanup_interval_hours" yaml:"cleanup_interval_hours" default:"24"`
		// SampleRate 请求采样率 0-1，不配置（nil）= 1.0 全量记录；显式 0 = 一条都不记录
		// 开启态线上建议 0.1；三态原因同 MaxRecords（写 0 若按普通字段会被回填成全量）
		SampleRate *float64 `json:"sample_rate" yaml:"sample_rate"`
		// UseCDN 仅影响日志里「记录的 client_ip」怎么取，不参与白名单鉴权（鉴权固定走 ClientIP）
		// 生产前置 nginx/CDN 时保持 false，改由 security.trusted_proxies / TRUSTED_PROXIES 放行代理网段
		UseCDN bool `json:"use_cdn" yaml:"use_cdn" default:"false"`
		// BasePath debugger 前端页面路径
		BasePath string `json:"base_path" yaml:"base_path" default:"/jcbase/debug"`
		// SkipPaths 额外跳过的路径前缀（默认已含健康检查、静态资源、认证/支付回调与上传路由）
		SkipPaths []string `json:"skip_paths" yaml:"skip_paths"`
		// MaskBody 凭据掩码开关：*bool 三态，不配置（nil）= 组件默认开启，显式 false 才记录原文
		MaskBody *bool `json:"mask_body" yaml:"mask_body"`
		// HeaderMaskKeys 追加需掩码的请求/响应头名（组件已内置 authorization/cookie/set-cookie 等）
		HeaderMaskKeys []string `json:"header_mask_keys" yaml:"header_mask_keys"`
		// BodyMaskKeys 追加需掩码的请求/响应体字段名（组件已内置 password/token/captcha 等）
		BodyMaskKeys []string `json:"body_mask_keys" yaml:"body_mask_keys"`
		// MultipartSkipFiles 上传文件部件是否只记元数据：*bool 三态，不配置 = 默认只记元数据
		MultipartSkipFiles *bool `json:"multipart_skip_files" yaml:"multipart_skip_files"`
		// MultipartPreserveState 是否在记录时复制 multipart 请求体：*bool 三态，不配置 = 默认开启
		// 关闭会让后续 c.FormFile 报「multipart handled by MultipartReader」，破坏业务上传
		MultipartPreserveState *bool `json:"multipart_preserve_state" yaml:"multipart_preserve_state"`
	} `json:"debugger" yaml:"debugger"`

	// 各微服务独立配置
	UserService    ServiceConfig `json:"user_service" yaml:"user_service"`
	ProductService ServiceConfig `json:"product_service" yaml:"product_service"`
	OrderService   ServiceConfig `json:"order_service" yaml:"order_service"`
	CartService    ServiceConfig `json:"cart_service" yaml:"cart_service"`
	PaymentService ServiceConfig `json:"payment_service" yaml:"payment_service"`
}

// ServiceConfig 微服务通用配置
type ServiceConfig struct {
	GrpcPort int `json:"grpc_port" yaml:"grpc_port"`
}

// WechatConfig 微信小程序登录配置
// 注意：微信支付 V3 的 AppID/商户号/密钥/证书等支付配置已迁移至数据库 pay_config 表，
// 此结构体仅保留小程序登录所需字段；支付侧使用 WechatConfig 作为数据载体传递
type WechatConfig struct {
	AppID        string `json:"app_id" yaml:"app_id"`                 // 小程序/公众号 AppID
	AppSecret    string `json:"app_secret" yaml:"app_secret"`         // 小程序/公众号 AppSecret
	MchID        string `json:"mch_id" yaml:"mch_id"`                 // 微信支付商户号（仅支付载体使用）
	APIV3Key     string `json:"api_v3_key" yaml:"api_v3_key"`         // 微信支付 V3 API 密钥（仅支付载体使用）
	CertSerialNo string `json:"cert_serial_no" yaml:"cert_serial_no"` // 微信支付商户证书序列号（仅支付载体使用）
	PrivateKey   string `json:"private_key" yaml:"private_key"`       // 微信支付商户私钥 PEM（仅支付载体使用）
	NotifyURL    string `json:"notify_url" yaml:"notify_url"`         // 微信支付结果通知 URL（仅支付载体使用）
}

// IsMiniprogramConfigured 判断微信小程序登录配置是否完整
//
// 返回：
//   - bool: 配置完整返回 true
func (w *WechatConfig) IsMiniprogramConfigured() bool {
	return w.AppID != "" && w.AppSecret != ""
}

// IsPaymentConfigured 判断微信支付 V3 配置是否完整
// 缺少任一必要字段则 C 端不展示微信支付选项
//
// 返回：
//   - bool: 配置完整返回 true
func (w *WechatConfig) IsPaymentConfigured() bool {
	return w.AppID != "" &&
		w.MchID != "" &&
		w.APIV3Key != "" &&
		w.CertSerialNo != "" &&
		w.PrivateKey != "" &&
		w.NotifyURL != ""
}

// 支付宝加签方式常量（pay_config.alipay_mode）
// 空串按公钥模式处理，兼容存量数据
const (
	AlipayModePublic = "public" // 公钥模式：AppID + 应用私钥 + 支付宝公钥
	AlipayModeCert   = "cert"   // 证书模式：AppID + 应用私钥 + 应用公钥证书 + 支付宝公钥证书 + 支付宝根证书
)

// AlipayConfig 支付宝支付配置数据载体
// 支付配置实际存储于数据库 pay_config 表，此结构体仅作为服务间传递的数据载体
type AlipayConfig struct {
	AppID      string `json:"app_id" yaml:"app_id"`           // 支付宝 AppID
	PrivateKey string `json:"private_key" yaml:"private_key"` // 支付宝应用私钥
	// Mode 加签方式：public（默认，空串兼容存量）/ cert，见 AlipayMode* 常量
	Mode            string `json:"mode" yaml:"mode"`
	AlipayPublicKey string `json:"alipay_public_key" yaml:"alipay_public_key"` // 支付宝公钥（公钥模式）
	NotifyURL       string `json:"notify_url" yaml:"notify_url"`               // 支付宝支付结果通知 URL
	ReturnURL       string `json:"return_url" yaml:"return_url"`               // 支付宝支付完成回跳 URL
	// GatewayURL V2/V3 网关地址，空=生产环境 https://openapi.alipay.com；
	// 填支付宝沙箱网关（https://openapi-sandbox.dl.alipaydev.com）即切到沙箱环境联调
	GatewayURL string `json:"gateway_url" yaml:"gateway_url"`
	// 证书模式三证书内容（PEM 文本，仅证书模式参与必填判定与执行器构建）
	AppCert          string `json:"app_cert" yaml:"app_cert"`                     // 应用公钥证书
	AlipayPublicCert string `json:"alipay_public_cert" yaml:"alipay_public_cert"` // 支付宝公钥证书
	AlipayRootCert   string `json:"alipay_root_cert" yaml:"alipay_root_cert"`     // 支付宝根证书
}

// IsCertMode 判断当前配置是否为证书模式（空串按公钥模式，兼容存量数据）
//
// 返回：
//   - bool: 证书模式返回 true
func (a *AlipayConfig) IsCertMode() bool {
	return a.Mode == AlipayModeCert
}

// IsConfigured 判断支付宝支付配置是否完整
// 按加签方式分流必填口径：公钥模式需支付宝公钥，证书模式需三证书齐全；
// 另一模式的残留字段不参与判定，模式切换后未补齐即判未就绪（fail-closed）
//
// 返回：
//   - bool: 配置完整返回 true
func (a *AlipayConfig) IsConfigured() bool {
	if a.AppID == "" || a.PrivateKey == "" || a.NotifyURL == "" {
		return false
	}
	if a.IsCertMode() {
		return a.AppCert != "" && a.AlipayPublicCert != "" && a.AlipayRootCert != ""
	}
	return a.AlipayPublicKey != ""
}

// currentConfigPath 当前加载的配置文件路径
// 由 gateway 启动时通过 SetConfigPath 设置
var currentConfigPath string

// SetConfigPath 设置配置文件保存路径
// 参数：
//   - path: 配置文件绝对路径
func SetConfigPath(path string) {
	currentConfigPath = path
}

// GetConfigPath 获取配置文件保存路径
//
// 返回：
//   - string: 配置文件路径
func GetConfigPath() string {
	return currentConfigPath
}
