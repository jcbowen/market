package config

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// DefaultJWTSecret 默认 JWT 签名密钥（仅限本地开发）
// 该字符串曾随配置文件入库，必须视为公开值：
// 生产环境检测到未通过 JWT_SECRET 覆盖时将拒绝启动（见 EnsureSecureJWT）
const DefaultJWTSecret = "market-secret-key-change-in-production"

// IsProduction 判断当前是否生产环境
// 以 APP_ENV 环境变量为准（未设置视为非生产，保持本地开发零配置可用）
//
// 返回：
//   - bool: APP_ENV=production 时返回 true
func IsProduction() bool {
	return os.Getenv("APP_ENV") == "production"
}

// ApplyEnvOverrides 用环境变量覆盖配置文件中的部署相关项
// 敏感凭据不入 git 仓库：MySQL/Redis 密码与 JWT 密钥支持经环境变量注入，
// 未设置时沿用配置文件值（本地开发零配置可用）；
// 可信代理网段属部署拓扑（同机反代 vs 直连），同样由环境变量给，避免配置文件写死网段；
// debugger 的开关/页面白名单/来源标识同理：compose 多服务共用一份配置模板，
// 须按进程给值才能区分 debug_logs 的日志来源
// 参数：
//   - cfg: 已加载的全局配置（原地修改）
func ApplyEnvOverrides(cfg *Config) {
	if v := os.Getenv("MYSQL_PASSWORD"); v != "" {
		cfg.Db.Password = v
	}
	if v := os.Getenv("REDIS_PASSWORD"); v != "" {
		cfg.Redis.Password = v
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.JWT.Secret = v
	}
	// DEBUGGER_ENABLED 强制开关 debugger 请求日志（compose 部署注入，优先于配置文件）
	if v := os.Getenv("DEBUGGER_ENABLED"); v != "" {
		cfg.Debugger.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	// DEBUGGER_ALLOWED_IPS 逗号分隔的调试页面访问白名单（IP/CIDR）
	// 只给 ENABLED 不给白名单时页面路由不会注册（fail-closed），
	// 故临时排查须两项齐给：DEBUGGER_ENABLED=true + DEBUGGER_ALLOWED_IPS=127.0.0.1
	if v := os.Getenv("DEBUGGER_ALLOWED_IPS"); v != "" {
		cfg.Debugger.AllowedIPs = splitCSVList(v)
	}
	// DEBUGGER_SERVICE_NAME debug_logs 日志来源标识：compose 部署下各服务共用一份
	// 配置模板（service_name 均为 gateway），进程级记录（支付/订单调度器摘要）须按
	// 进程名覆写才能区分来源；留空 = 沿用配置文件值。
	// allinone 部署由 entrypoint 覆写配置副本，不需要此变量
	if v := os.Getenv("DEBUGGER_SERVICE_NAME"); v != "" {
		cfg.Debugger.ServiceName = strings.TrimSpace(v)
	}
	// TRUSTED_PROXIES 逗号分隔可信代理网段（compose/all-in-one 注入，避免配置文件里写死网段）
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		cfg.Security.TrustedProxies = splitCSVList(v)
	}
	// TRUSTED_PLATFORM 可信平台专用头名（如前置阿里云 CDN 时注入 Ali-CDN-Real-IP，
	// 该头由 CDN 强制覆盖写入、客户端伪造无效，ClientIP 优先采信）
	if v := os.Getenv("TRUSTED_PLATFORM"); v != "" {
		cfg.Security.TrustedPlatform = strings.TrimSpace(v)
	}
}

// splitCSVList 解析逗号分隔列表（去空白、丢空项），供可信代理网段与调试白名单共用
// 参数：
//   - v: 原始环境变量值，如 "127.0.0.1, 10.0.0.0/8"
//
// 返回值：
//   - []string: 拆分后的条目；输入全为空白时返回空切片（调用方按「未配置」处理）
//
// 异常：不触发 panic；CIDR 合法性不在此校验（由使用方 gin.SetTrustedProxies 报错）
// 使用示例：
//
//	proxies := splitCSVList(os.Getenv("TRUSTED_PROXIES"))
func splitCSVList(v string) []string {
	parts := strings.Split(v, ",")
	list := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			list = append(list, p)
		}
	}
	return list
}

// EnvAllowInsecureJWT 显式允许使用默认占位密钥的开关名（仅本地开发用）
const EnvAllowInsecureJWT = "JWT_ALLOW_INSECURE_DEV"

// minJWTSecretLen 密钥最小长度：低于该长度即使非默认值也难以暴力猜测的门槛，
// 视为误配（示例：随机 base62 密钥通常 32 位以上）
const minJWTSecretLen = 32

// EnsureSecureJWT JWT 密钥安全校验（fail-fast）
// 默认占位串随配置文件入库，必须视为公开值：持有者可伪造任意身份（含超管）的
// 合法 token。此前的「仅 APP_ENV=production 才检查」在 compose 部署下形同不存在
// （compose 只注入 JWT_SECRET，从不传 APP_ENV），故改为不区分环境一律拒绝；
// 空值与过短密钥同属误配，一并拒绝。
// 纯本地开发确需零配置时，显式设 JWT_ALLOW_INSECURE_DEV=true 放行。
//
// 参数：
//   - cfg: 已完成环境变量覆盖的配置
//
// 返回值：
//   - error: 密钥不可用且未显式放行时返回错误
func EnsureSecureJWT(cfg *Config) error {
	insecure := cfg.JWT.Secret == "" || cfg.JWT.Secret == DefaultJWTSecret || len(cfg.JWT.Secret) < minJWTSecretLen
	if !insecure {
		return nil
	}
	if os.Getenv(EnvAllowInsecureJWT) == "true" {
		log.Printf("警告: JWT 密钥为空/默认/过短，且已设 %s=true 放行，仅限本地开发；"+
			"默认占位密钥是公开值，任何人都能据此伪造超管 token", EnvAllowInsecureJWT)
		return nil
	}
	return fmt.Errorf("JWT 密钥为空、为默认占位值或长度不足 %d 位，拒绝启动；"+
		"请通过 JWT_SECRET 环境变量注入随机强密钥（本地开发可设 %s=true 临时放行）",
		minJWTSecretLen, EnvAllowInsecureJWT)
}
