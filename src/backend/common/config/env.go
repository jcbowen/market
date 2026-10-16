package config

import (
	"errors"
	"os"
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

// ApplyEnvOverrides 用环境变量覆盖配置文件中的敏感项
// 敏感凭据不入 git 仓库：MySQL/Redis 密码与 JWT 密钥支持经环境变量注入，
// 未设置时沿用配置文件值（本地开发零配置可用）
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
}

// EnsureSecureJWT 生产环境 JWT 密钥安全校验（fail-fast）
// 服务启动时检测：生产环境（APP_ENV=production）下若密钥仍为默认占位串，
// 直接拒绝启动，避免攻击者用公开的默认密钥伪造任意身份的合法 token
//
// 返回：
//   - error: 生产环境使用默认密钥时返回错误，其余情况返回 nil
func EnsureSecureJWT(cfg *Config) error {
	if IsProduction() && cfg.JWT.Secret == DefaultJWTSecret {
		return errors.New("生产环境检测到 JWT 密钥仍为默认占位值，拒绝启动；请通过 JWT_SECRET 环境变量注入随机强密钥")
	}
	return nil
}
